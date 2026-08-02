package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/pprof"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"tg-search/internal/adminauth"
	"tg-search/internal/apikey"
	"tg-search/internal/avatar"
	"tg-search/internal/channel"
	"tg-search/internal/config"
	"tg-search/internal/history"
	"tg-search/internal/medialimit"
	"tg-search/internal/notification"
	"tg-search/internal/repository"
	"tg-search/internal/resource"
	"tg-search/internal/scheduler"
	"tg-search/internal/search"
	"tg-search/internal/session"
	"tg-search/internal/storage"
	taskpkg "tg-search/internal/task"
	"tg-search/internal/telegram"
)

type AccountRuntime interface {
	StopAccount(context.Context, int64) error
}

type Dependencies struct {
	Logger           *zap.Logger
	Users            *repository.UserRepository
	APIKeys          *repository.APIKeyRepository
	APIKeyService    *apikey.Service
	Settings         *repository.SettingsRepository
	AdminAuth        *adminauth.Service
	RuntimeConfig    config.Config
	StorageUsage     *storage.UsageService
	ImageCache       *storage.MediaCache
	AvatarCache      *storage.MediaCache
	Accounts         *repository.AccountRepository
	Channels         *repository.ChannelRepository
	Messages         *repository.MessageRepository
	Links            *repository.LinkRepository
	WatchRules       *repository.WatchRuleRepository
	RemoteSearch     *repository.RemoteSearchTaskRepository
	SavedSearches    *repository.SavedSearchRepository
	BotSubscriptions *repository.TelegramBotSubscriptionRepository
	Webhooks         *repository.WebhookRepository
	Deliveries       *repository.NotificationDeliveryRepository
	Files            *repository.FileRepository
	RemoteSearchExec *search.RemoteService
	Maintenance      *repository.MaintenanceRepository
	Status           *repository.StatusRepository
	BackupDB         *sql.DB
	BackupDir        string
	SyncQueue        *scheduler.RetryQueue
	Search           *search.Service
	History          *history.Service
	Resources        *resource.Service
	Notifications    *notification.Service
	ChannelSync      *channel.Service
	ChannelWebAccess *channel.WebAccessService
	Tasks            *taskpkg.Service
	TaskRepository   *taskpkg.Repository
	Events           *taskpkg.EventBroker
	AccountRuntime   AccountRuntime
	Telegram         telegram.Client
	MediaLimiter     *medialimit.Limiter
	AvatarLimiter    *medialimit.Limiter
	AvatarService    *avatar.Service
	Sessions         *session.Manager
	CodeStore        *telegram.CodeStore
	QRLogins         *QRLoginStore
}

func NewRouter(deps Dependencies) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(apiLoggerKey, deps.Logger)
		c.Next()
	})
	router.Use(gin.CustomRecoveryWithWriter(gin.DefaultErrorWriter, func(c *gin.Context, recovered any) {
		deps.Logger.Error("api panic recovered",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Any("panic", recovered),
		)
		errorWithCode(c, http.StatusInternalServerError, "internal_error", fmt.Sprint(recovered))
	}))

	h := handlers{deps: deps}
	if h.deps.APIKeyService == nil && h.deps.APIKeys != nil && h.deps.Settings != nil {
		h.deps.APIKeyService = apikey.NewService(h.deps.APIKeys, h.deps.Settings)
	}
	if h.deps.QRLogins == nil {
		h.deps.QRLogins = NewQRLoginStore(2 * time.Minute)
	}
	if h.deps.ImageCache == nil && h.deps.RuntimeConfig.Storage.Path != "" {
		h.deps.ImageCache = storage.NewMediaCache(h.deps.RuntimeConfig)
	}
	if h.deps.AvatarCache == nil && h.deps.RuntimeConfig.Storage.Path != "" {
		h.deps.AvatarCache = storage.NewMediaCacheWithOptions(storage.MediaCacheOptions{
			Root:     filepath.Join(h.deps.RuntimeConfig.Storage.Path, "avatars"),
			MaxBytes: int64(h.deps.RuntimeConfig.Storage.MaxMediaCache),
			TTL:      30 * 24 * time.Hour,
		})
	}
	api := router.Group("/api")
	api.GET("/health", h.health)
	api.GET("/ready", h.ready)
	api.GET("/setup/status", h.setupStatus)
	api.POST("/setup/admin", h.setupAdmin)
	api.POST("/setup/api-key", h.setupAPIKey)
	api.POST("/setup/telegram-api", h.saveSetupTelegramAPI)
	api.POST("/setup/listen-rules", h.setupListenRules)
	api.POST("/setup/complete", h.setupComplete)
	api.POST("/auth/login", h.authLogin)
	api.POST("/auth/logout", h.authLogout)
	api.GET("/auth/me", h.authMe)
	api.GET("/settings/version", h.getVersionSettings)

	external := router.Group("")
	external.Use(h.externalSearchAccessLog(), h.requireAPIKey())
	external.GET("/api/search", h.externalSearch)
	external.POST("/api/search", h.externalSearch)
	external.POST("/api/check/links", h.externalCheckLinks)
	external.GET("/feeds/latest", h.feedLatest)
	external.GET("/feeds/search", h.feedSearch)
	external.GET("/feeds/saved/:id", h.feedSavedSearch)

	adminOnly := api.Group("")
	adminOnly.Use(h.requireAdminSession())
	adminOnly.GET("/settings/telegram-api", h.getTelegramAPISettings)
	adminOnly.PUT("/settings/telegram-api", h.updateTelegramAPISettings)
	adminOnly.GET("/settings/telegram-bot", h.getTelegramBotSettings)
	adminOnly.PUT("/settings/telegram-bot", h.updateTelegramBotSettings)
	adminOnly.GET("/settings/runtime", h.getRuntimeSettings)
	adminOnly.PUT("/settings/runtime", h.updateRuntimeSettings)
	adminOnly.GET("/settings/ai/providers", h.aiProviders)
	adminOnly.POST("/settings/ai/models", h.aiModels)
	adminOnly.POST("/settings/ai/test", h.aiTest)
	adminOnly.PUT("/settings/admin", h.updateAdminSettings)
	adminOnly.GET("/settings/api-key", h.getAPIKeySettings)
	adminOnly.POST("/settings/api-key/regenerate", h.regenerateAPIKey)
	adminOnly.GET("/listen-rules", h.getListenRules)
	adminOnly.PUT("/listen-rules", h.updateListenRules)
	adminOnly.GET("/settings/system-info", h.getSystemInfoSettings)
	adminOnly.GET("/storage/usage", h.storageUsage)
	adminOnly.GET("/status", h.status)
	adminOnly.GET("/dashboard/resource-stats", h.dashboardResourceStats)
	adminOnly.GET("/tasks", h.tasks)
	adminOnly.POST("/tasks/bulk-delete", h.bulkDeleteTasks)
	adminOnly.GET("/tasks/:id", h.task)
	adminOnly.GET("/jobs/:id", h.retryJob)
	adminOnly.DELETE("/tasks/:id", h.deleteTask)
	adminOnly.POST("/tasks/:id/retry", h.retryTask)
	adminOnly.POST("/tasks/:id/cancel", h.cancelTask)
	adminOnly.POST("/tasks/:id/pause", h.pauseTask)
	adminOnly.POST("/tasks/:id/resume", h.resumeTask)
	adminOnly.GET("/events", h.events)
	adminOnly.GET("/logs", h.logs)
	adminOnly.GET("/logs/:file/download", h.downloadLog)
	telegramAPI := adminOnly.Group("/telegram")
	telegramAPI.POST("/login/send-code", h.sendCode)
	telegramAPI.POST("/login/sign-in", h.signIn)
	telegramAPI.POST("/login/password", h.password)
	telegramAPI.POST("/login/qr/start", h.startQRLogin)
	telegramAPI.POST("/login/telethon-session", h.telethonSessionLogin)
	telegramAPI.GET("/login/qr/:login_id", h.pollQRLogin)
	telegramAPI.DELETE("/login/qr/:login_id", h.cancelQRLogin)
	adminOnly.GET("/accounts", h.accounts)
	adminOnly.GET("/accounts/:id/avatar", h.serveAccountAvatar)
	adminOnly.HEAD("/accounts/:id/avatar", h.serveAccountAvatar)
	adminOnly.POST("/accounts/:id/sync-avatar", h.syncAccountAvatar)
	adminOnly.POST("/accounts/:id/logout", h.logoutAccount)
	adminOnly.DELETE("/accounts/:id", h.deleteAccount)
	adminOnly.POST("/accounts/:id/channels/sync-metadata", h.syncAccountChannels)
	adminOnly.GET("/channels", h.channels)
	adminOnly.POST("/channels/sync", h.syncChannels)
	adminOnly.POST("/channels/web-access/check", h.checkChannelWebAccess)
	adminOnly.PATCH("/channels/control", h.updateChannelsControl)
	adminOnly.GET("/channels/:id", h.channel)
	adminOnly.PATCH("/channels/:id/control", h.updateChannelControl)
	adminOnly.POST("/channels/:id/clear", h.clearChannel)
	adminOnly.POST("/channels/:id/analyze", h.analyzeChannel)
	adminOnly.POST("/channels/:id/sync", h.syncChannel)
	adminOnly.GET("/channels/:id/avatar", h.serveChannelAvatar)
	adminOnly.HEAD("/channels/:id/avatar", h.serveChannelAvatar)
	adminOnly.GET("/watch-rules", h.watchRules)
	adminOnly.POST("/watch-rules", h.createWatchRule)
	adminOnly.GET("/watch-rules/:id", h.watchRule)
	adminOnly.PUT("/watch-rules/:id", h.updateWatchRule)
	adminOnly.DELETE("/watch-rules/:id", h.deleteWatchRule)
	adminOnly.GET("/telegram-bot/chats", h.telegramBotChats)
	adminOnly.GET("/saved-searches", h.savedSearches)
	adminOnly.POST("/saved-searches", h.createSavedSearch)
	adminOnly.GET("/saved-searches/:id", h.savedSearch)
	adminOnly.PUT("/saved-searches/:id", h.updateSavedSearch)
	adminOnly.DELETE("/saved-searches/:id", h.deleteSavedSearch)
	adminOnly.POST("/saved-searches/:id/test", h.testSavedSearch)
	adminOnly.GET("/webhooks", h.webhooks)
	adminOnly.POST("/webhooks", h.createWebhook)
	adminOnly.GET("/webhooks/:id", h.webhook)
	adminOnly.PUT("/webhooks/:id", h.updateWebhook)
	adminOnly.DELETE("/webhooks/:id", h.deleteWebhook)
	adminOnly.GET("/notification-deliveries", h.notificationDeliveries)
	adminSearch := adminOnly.Group("/admin/search")
	adminSearch.GET("/global", h.searchGlobal)
	adminSearch.GET("/messages", h.searchMessages)
	adminSearch.GET("/links", h.searchLinks)
	adminSearch.GET("/files", h.searchFiles)
	adminSearch.GET("/channels", h.searchChannels)
	adminSearch.GET("", h.search)
	adminSearch.POST("/remote", h.createRemoteSearchTask)
	adminSearch.GET("/remote/:task_id", h.getRemoteSearchTask)
	adminOnly.GET("/messages/latest", h.latest)
	adminOnly.GET("/links/merged", h.mergedLinks)
	adminOnly.GET("/links/grouped", h.linksGrouped)
	adminOnly.GET("/links", h.links)
	adminOnly.POST("/maintenance/sqlite", h.maintenanceSQLite)
	adminOnly.POST("/maintenance/backup", h.maintenanceBackup)
	adminOnly.POST("/maintenance/resource-index/rebuild", h.rebuildResourceIndex)
	adminOnly.POST("/maintenance/media-title/repair", h.repairMediaTitle)

	resourceAccess := api.Group("")
	resourceAccess.Use(h.requireAdminSession())
	resourceAccess.GET("/trending", h.trendingResources)
	resourceAccess.GET("/resources/grouped", h.resourcesGrouped)
	resourceAccess.POST("/resources/bulk-delete", h.bulkDeleteResources)
	resourceAccess.GET("/resources/:id", h.resource)
	resourceAccess.DELETE("/resources/:id", h.deleteResource)
	resourceAccess.GET("/resources", h.resources)

	mediaAccess := router.Group("")
	mediaAccess.Use(h.requireMediaAccess())
	mediaAccess.GET("/v/:fileid", h.serveTelegramVideo)
	mediaAccess.HEAD("/v/:fileid", h.serveTelegramVideo)
	mediaAccess.GET("/i/:fileid", h.serveTelegramImage)
	mediaAccess.HEAD("/i/:fileid", h.serveTelegramImage)

	// pprof debug endpoints, gated behind an admin session cookie. Capture a
	// profile with the admin cookie, e.g.:
	//   go tool pprof 'http://127.0.0.1:<addr>/debug/pprof/profile?seconds=30'
	pprofGroup := router.Group("/debug/pprof", h.requireAdminSession())
	pprofGroup.GET("", gin.WrapF(pprof.Index))
	pprofGroup.GET("/", gin.WrapF(pprof.Index))
	pprofGroup.GET("/cmdline", gin.WrapF(pprof.Cmdline))
	pprofGroup.GET("/profile", gin.WrapF(pprof.Profile))
	pprofGroup.GET("/symbol", gin.WrapF(pprof.Symbol))
	pprofGroup.GET("/trace", gin.WrapF(pprof.Trace))
	pprofGroup.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
	pprofGroup.GET("/block", gin.WrapH(pprof.Handler("block")))
	pprofGroup.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
	pprofGroup.GET("/heap", gin.WrapH(pprof.Handler("heap")))
	pprofGroup.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
	pprofGroup.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))

	router.NoRoute(h.frontend)
	return router
}
