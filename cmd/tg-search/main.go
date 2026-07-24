package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.uber.org/zap"

	"tg-search/internal/account"
	"tg-search/internal/adminauth"
	aipkg "tg-search/internal/ai"
	"tg-search/internal/api"
	avatarpkg "tg-search/internal/avatar"
	"tg-search/internal/channel"
	"tg-search/internal/config"
	"tg-search/internal/db"
	"tg-search/internal/history"
	"tg-search/internal/link"
	"tg-search/internal/logger"
	"tg-search/internal/medialimit"
	"tg-search/internal/messagefilter"
	"tg-search/internal/model"
	"tg-search/internal/notification"
	"tg-search/internal/repository"
	"tg-search/internal/resource"
	"tg-search/internal/retry"
	"tg-search/internal/scheduler"
	"tg-search/internal/search"
	"tg-search/internal/session"
	"tg-search/internal/storage"
	taskpkg "tg-search/internal/task"
	"tg-search/internal/telegram"
	"tg-search/internal/telegramguard"
	"tg-search/internal/tgbot"
	updatepkg "tg-search/internal/update"
)

func main() {
	configPath := flag.String("config", "", "config file path")
	flag.Parse()

	if err := run(*configPath); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := config.EnsureRuntimeDirs(cfg); err != nil {
		return err
	}

	logs, err := logger.New(filepath.Join(cfg.Storage.Path, "logs"))
	if err != nil {
		return err
	}
	defer logs.Sync()
	logs.App.Info("tg-search starting",
		zap.String("address", config.Address(cfg)),
		zap.String("storage_path", cfg.Storage.Path),
		zap.Int("sync_workers", cfg.Sync.Workers),
		zap.Int("history_batch_size", cfg.Sync.HistoryBatchSize),
	)

	conn, err := db.Open(config.DatabasePath(cfg))
	if err != nil {
		logs.App.Error("open database failed", zap.String("path", config.DatabasePath(cfg)), zap.Error(err))
		return err
	}
	defer conn.Close()
	logs.App.Info("database opened", zap.String("path", config.DatabasePath(cfg)))

	ctx := context.Background()
	if err := db.Migrate(ctx, conn); err != nil {
		logs.App.Error("database migration failed", zap.Error(err))
		return err
	}
	logs.App.Info("database migration completed")

	accounts := repository.NewAccountRepository(conn)
	channels := repository.NewChannelRepository(conn)
	messages := repository.NewMessageRepository(conn)
	links := repository.NewLinkRepository(conn)
	files := repository.NewFileRepository(conn)
	resourceStats := repository.NewResourceStatsRepository(conn)
	resourceIndex := repository.NewResourceIndexRepository(conn)
	cursors := repository.NewSyncCursorRepository(conn)
	watchRules := repository.NewWatchRuleRepository(conn)
	remoteSearch := repository.NewRemoteSearchTaskRepository(conn)
	savedSearches := repository.NewSavedSearchRepository(conn)
	webhooks := repository.NewWebhookRepository(conn)
	deliveries := repository.NewNotificationDeliveryRepository(conn)
	botSubscriptions := repository.NewTelegramBotSubscriptionRepository(conn)
	maintenance := repository.NewMaintenanceRepository(conn)
	status := repository.NewStatusRepository(conn)
	users := repository.NewUserRepository(conn)
	adminSessions := repository.NewAdminSessionRepository(conn)
	apiKeys := repository.NewAPIKeyRepository(conn)
	settings := repository.NewSettingsRepository(conn)
	runtimeSettings, err := settings.LoadRuntimeSettings(ctx, cfg)
	if err != nil {
		logs.App.Error("load runtime settings failed", zap.Error(err))
		return err
	}
	cfg, err = config.ApplyRuntimeSettings(cfg, runtimeSettings)
	if err != nil {
		logs.App.Error("apply runtime settings failed", zap.Error(err))
		return err
	}
	botSettings, err := settings.LoadTelegramBot(ctx, cfg.Bot)
	if err != nil {
		logs.App.Error("load telegram bot settings failed", zap.Error(err))
		return err
	}
	cfg.Bot = botSettings
	watchFilter := messagefilter.New(messagefilter.NewSettingsRuleStore(watchRules, settings))
	taskRepository := taskpkg.NewRepository(conn)
	taskService := taskpkg.NewService(taskRepository)
	eventBroker := taskpkg.NewEventBroker()
	adminAuth := adminauth.NewService(users, adminSessions)
	storageUsage := storage.NewUsageService(cfg)
	imageCache := storage.NewMediaCache(cfg)
	sessions := session.NewManager(filepath.Join(cfg.Storage.Path, "sessions"))
	telegramCredentials := repository.NewTelegramCredentialsProvider(settings)
	telegramRuntime := telegram.RuntimeConfigFromConfig(cfg.Telegram)
	tgClient := telegram.NewGotdClient(telegramCredentials, logs.Telegram, telegramRuntime)
	retryPolicy := retry.DefaultPolicy()
	telegramGovernor := telegramguard.New(telegramguard.Options{Interval: cfg.Sync.TelegramRequestInterval.Std()})
	mediaLimiter := medialimit.New(cfg.Telegram.Media.Concurrency)
	avatarLimiter := medialimit.New(20) // Higher concurrency for small images
	syncQueue := scheduler.NewRetryQueue(scheduler.RetryQueueOptions{Policy: retryPolicy, Logger: logs.SyncLog})
	avatarQueue := scheduler.NewRetryQueue(scheduler.RetryQueueOptions{Policy: retryPolicy, Logger: logs.App})
	avatarService := avatarpkg.NewService(avatarpkg.ServiceOptions{
		StorageRoot: cfg.Storage.Path,
		Queue:       avatarQueue,
		Limiter:     avatarLimiter,
		Telegram:    tgClient,
		Logger:      logs.App,
	})
	resourceService := resource.NewService(links, files, resourceStats, resourceIndex, messages, link.NewExtractor())
	if stats, err := resourceService.IndexStats(ctx); err == nil && stats.IndexedRows == 0 {
		if err := resourceService.RebuildIndex(ctx); err != nil {
			logs.App.Warn("resource index rebuild failed", zap.Error(err))
		}
	} else if err != nil {
		logs.App.Warn("resource index stats failed", zap.Error(err))
	}
	aiService := aipkg.NewService(aipkg.ServiceOptions{
		Settings:  settings,
		Defaults:  cfg,
		Messages:  messages,
		Links:     links,
		Resources: resourceService,
		Logger:    logs.App,
	})
	notificationService := notification.NewService(notification.Options{
		SavedSearches: savedSearches,
		Webhooks:      webhooks,
		Deliveries:    deliveries,
		BotSubs:       botSubscriptions,
	})
	updateProcessor := updatepkg.NewProcessor(updatepkg.ProcessorOptions{
		DB:                   conn,
		Channels:             channels,
		Messages:             messages,
		Links:                links,
		Files:                files,
		Resources:            resourceService,
		Notifications:        notificationService,
		Cursors:              cursors,
		Tasks:                taskService,
		Settings:             settings,
		RuntimeConfig:        cfg,
		AIMediaMetadataTasks: taskService,
		Extractor:            link.NewExtractor(),
		Filter:               watchFilter,
	})
	updateService := updatepkg.NewService(updatepkg.ServiceOptions{
		Accounts:    accounts,
		Channels:    channels,
		Processor:   updateProcessor,
		Listener:    updatepkg.NewGotdListener(telegramCredentials, sessions, logs.Telegram, telegramRuntime),
		RetryPolicy: retryPolicy,
		Logger:      logs.Telegram,
	})
	searchService := search.NewService(messages, links, files, channels)
	remoteSearchService := search.NewRemoteService(search.RemoteOptions{
		Accounts:        accounts,
		Channels:        channels,
		Tasks:           remoteSearch,
		Cursors:         cursors,
		Telegram:        tgClient,
		Sessions:        sessions,
		RequestGovernor: telegramGovernor,
		Logger:          logs.App,
	})
	historyService := history.NewService(history.Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links, Files: files, Cursors: cursors,
		Resources:     resourceService,
		Notifications: notificationService,
		Telegram:      tgClient, Sessions: sessions, Extractor: link.NewExtractor(),
		Filter:               watchFilter,
		HistoryBatchSize:     cfg.Sync.HistoryBatchSize,
		Workers:              cfg.Sync.Workers,
		RetryPolicy:          retryPolicy,
		RequestGovernor:      telegramGovernor,
		Logger:               logs.SyncLog,
		Settings:             settings,
		RuntimeConfig:        cfg,
		AIMediaMetadataTasks: taskService,
	})
	channelService := channel.NewService(channels, tgClient, sessions)
	channelWebAccessService := channel.NewWebAccessService(channels, nil)
	taskWorker := taskpkg.NewWorker(taskpkg.WorkerOptions{
		Service:    taskService,
		Repository: taskRepository,
		Events:     eventBroker,
		Handlers: map[string]taskpkg.Handler{
			model.TaskTypeGapRecovery:      historyService.RunGapRecoveryTask,
			model.TaskTypeHistorySync:      historyService.RunHistorySyncTask,
			model.TaskTypeAIMediaMetadata:  aiService.RunMediaMetadataTask,
			model.TaskTypeRepairMediaTitle: resourceService.RunRepairMediaTitleTask,
		},
		PollInterval: 2 * time.Second,
	})
	if err := taskService.RestoreUnfinished(ctx, time.Now().UTC()); err != nil {
		logs.App.Error("restore unfinished tasks failed", zap.Error(err))
		return err
	}
	logs.App.Info("unfinished tasks restored")
	taskWorker.Start(ctx)
	logs.App.Info("task worker started")
	accountManager := account.NewManager(account.ManagerOptions{
		Accounts: accounts,
		Updates:  updateService,
		Logger:   logs.Telegram,
	})
	if err := accountManager.Start(ctx); err != nil {
		logs.App.Error("account manager start failed", zap.Error(err))
		return err
	}
	historyService.StartListenBacklog(ctx)
	cleanupScheduler := scheduler.New(scheduler.Options{
		Interval: time.Hour,
		Jobs: []scheduler.Job{
			scheduler.CleanupJob{Logger: logs.App, MediaCache: imageCache},
			adminauth.SessionCleanupJob{Service: adminAuth},
			taskpkg.NewCleanupJob(taskService, cfg.TaskRetention, logs.App),
		},
		Logger: logs.App,
	})
	cleanupScheduler.Start(ctx)
	logs.App.Info("cleanup scheduler started", zap.Duration("interval", time.Hour))
	notificationJobs := []scheduler.Job{
		notification.NewDispatcher(notification.DispatcherOptions{
			Deliveries: deliveries,
			Webhooks:   webhooks,
			Logger:     logs.App,
		}),
	}
	notificationInterval := 15 * time.Second
	notificationScheduler := scheduler.New(scheduler.Options{
		Interval: notificationInterval,
		Jobs:     notificationJobs,
		Logger:   logs.App,
	})
	notificationScheduler.Start(ctx)
	logs.App.Info("notification dispatcher scheduler started", zap.Duration("interval", notificationInterval))
	botScheduler := scheduler.New(scheduler.Options{
		Interval: time.Second,
		Jobs: []scheduler.Job{
			tgbot.NewRuntime(tgbot.RuntimeOptions{
				Settings:      settings,
				Defaults:      cfg.Bot,
				Accounts:      accounts,
				Resources:     resourceService,
				SavedSearches: savedSearches,
				Subscriptions: botSubscriptions,
				Deliveries:    deliveries,
				Logger:        logs.App,
			}),
		},
		Logger: logs.App,
	})
	botScheduler.Start(ctx)
	logs.App.Info("telegram bot runtime scheduler started", zap.Duration("interval", time.Second))

	router := api.NewRouter(api.Dependencies{
		Logger: logs.App,
		Users:  users, APIKeys: apiKeys, Settings: settings, AdminAuth: adminAuth, RuntimeConfig: cfg, StorageUsage: storageUsage, ImageCache: imageCache,
		AvatarCache: imageCache, AvatarService: avatarService,
		Accounts: accounts, Channels: channels, Messages: messages, Links: links, Files: files, WatchRules: watchRules, RemoteSearch: remoteSearch, SavedSearches: savedSearches, BotSubscriptions: botSubscriptions, Webhooks: webhooks, Deliveries: deliveries, RemoteSearchExec: remoteSearchService, Maintenance: maintenance, Status: status,
		BackupDB: conn, BackupDir: filepath.Join(cfg.Storage.Path, "backup"),
		SyncQueue: syncQueue, Search: searchService, History: historyService, Resources: resourceService, Notifications: notificationService, ChannelSync: channelService, ChannelWebAccess: channelWebAccessService, AccountRuntime: accountManager,
		Tasks: taskService, TaskRepository: taskRepository, Events: eventBroker,
		Telegram: tgClient, MediaLimiter: mediaLimiter, AvatarLimiter: avatarLimiter, Sessions: sessions, CodeStore: telegram.NewCodeStore(),
	})
	server := &http.Server{
		Addr:              config.Address(cfg),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logs.App.Info("api server listening", zap.String("address", server.Addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		logs.App.Info("shutdown requested", zap.String("signal", sig.String()))
	case err := <-errCh:
		if err != nil {
			logs.App.Error("api server stopped with error", zap.Error(err))
		}
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logs.App.Error("api server shutdown failed", zap.Error(err))
		return err
	}
	logs.App.Info("api server shutdown completed")
	if err := cleanupScheduler.Stop(shutdownCtx); err != nil {
		logs.App.Error("cleanup scheduler stop failed", zap.Error(err))
		return err
	}
	if err := notificationScheduler.Stop(shutdownCtx); err != nil {
		logs.App.Error("notification scheduler stop failed", zap.Error(err))
		return err
	}
	if err := botScheduler.Stop(shutdownCtx); err != nil {
		logs.App.Error("telegram bot scheduler stop failed", zap.Error(err))
		return err
	}
	if err := taskWorker.Stop(shutdownCtx); err != nil {
		logs.App.Error("task worker stop failed", zap.Error(err))
		return err
	}
	if err := historyService.StopListenBacklog(shutdownCtx); err != nil {
		logs.App.Error("listen backlog sync stop failed", zap.Error(err))
		return err
	}
	if err := syncQueue.Stop(shutdownCtx); err != nil {
		logs.App.Error("sync queue stop failed", zap.Error(err))
		return err
	}
	if err := accountManager.Stop(shutdownCtx); err != nil {
		logs.App.Error("account manager stop failed", zap.Error(err))
		return err
	}
	logs.App.Info("tg-search stopped")
	return nil
}
