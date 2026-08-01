package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	channelpkg "tg-search/internal/channel"
	"tg-search/internal/config"
	dbpkg "tg-search/internal/db"
	"tg-search/internal/link"
	"tg-search/internal/messagefilter"
	"tg-search/internal/model"
	"tg-search/internal/notification"
	"tg-search/internal/repository"
	"tg-search/internal/resource"
	"tg-search/internal/retry"
	"tg-search/internal/session"
	taskpkg "tg-search/internal/task"
	"tg-search/internal/telegram"
	"tg-search/internal/telegramguard"
)

type Options struct {
	DB                   *sql.DB
	Accounts             *repository.AccountRepository
	Channels             *repository.ChannelRepository
	Messages             *repository.MessageRepository
	Links                *repository.LinkRepository
	Files                *repository.FileRepository
	Resources            *resource.Service
	Notifications        *notification.Service
	Cursors              *repository.SyncCursorRepository
	Telegram             telegram.Client
	Sessions             *session.Manager
	Extractor            *link.Extractor
	Filter               *messagefilter.Filter
	HistoryBatchSize     int
	Workers              int
	RetryPolicy          retry.Policy
	RequestGovernor      *telegramguard.Governor
	Logger               *zap.Logger
	Settings             *repository.SettingsRepository
	RuntimeConfig        config.Config
	AIMediaMetadataTasks taskEnqueuer
	GapRecoveryCooldown  *taskpkg.GapRecoveryCooldown
}

type taskEnqueuer interface {
	Enqueue(context.Context, string, any) (model.Task, error)
}

type Service struct {
	db                   *sql.DB
	accounts             *repository.AccountRepository
	channels             *repository.ChannelRepository
	messages             *repository.MessageRepository
	links                *repository.LinkRepository
	files                *repository.FileRepository
	resources            *resource.Service
	notifications        *notification.Service
	cursors              *repository.SyncCursorRepository
	telegram             telegram.Client
	sessions             *session.Manager
	extractor            *link.Extractor
	filter               *messagefilter.Filter
	historyBatchSize     int
	workers              int
	retryPolicy          retry.Policy
	requestGovernor      *telegramguard.Governor
	logger               *zap.Logger
	settings             *repository.SettingsRepository
	runtimeConfig        config.Config
	aiMediaMetadataTasks taskEnqueuer
	gapRecoveryCooldown  *taskpkg.GapRecoveryCooldown
	mu                   sync.Mutex
	runningChannels      map[int64]struct{}
	backlogCancel        context.CancelFunc
	backlogWG            sync.WaitGroup
}

type SyncResult struct {
	Messages int `json:"messages"`
	Links    int `json:"links"`
}

type SyncManyResult struct {
	Queued   int                  `json:"queued"`
	Skipped  int                  `json:"skipped"`
	Results  map[int64]SyncResult `json:"results"`
	Failures map[int64]string     `json:"failures"`
}

var ErrChannelSyncInProgress = errors.New("channel sync already in progress")
var ErrTaskPaused = errors.New("task is paused")
var ErrFloodWaitDeferred = errors.New("telegram flood wait deferred")
var ErrAccountNotReady = errors.New("telegram account is not ready")

const accountFloodWaitDeferral = time.Hour

// gapRecoveryTaskTimeout caps a single gap-recovery run. Generous enough for a
// healthy multi-thousand-message gap yet short enough that a run hung in the
// gotd RPC retry loop is aborted before it monopolizes the worker.
const gapRecoveryTaskTimeout = 5 * time.Minute

type floodWaitDeferredError struct {
	err error
}

func (e floodWaitDeferredError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%v: %v", ErrFloodWaitDeferred, e.err)
	}
	return ErrFloodWaitDeferred.Error()
}

func (e floodWaitDeferredError) Unwrap() error {
	return e.err
}

func (e floodWaitDeferredError) Is(target error) bool {
	return target == ErrFloodWaitDeferred
}

func NewService(opts Options) *Service {
	if opts.Telegram == nil {
		opts.Telegram = telegram.NopClient{}
	}
	if opts.Extractor == nil {
		opts.Extractor = link.NewExtractor()
	}
	if opts.HistoryBatchSize <= 0 {
		opts.HistoryBatchSize = 100
	}
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	if opts.RetryPolicy.MaxTries == 0 && opts.RetryPolicy.BaseDelay == 0 && opts.RetryPolicy.MaxDelay == 0 && opts.RetryPolicy.Sleep == nil {
		opts.RetryPolicy = retry.DefaultPolicy()
	}
	if opts.Cursors == nil && opts.DB != nil {
		opts.Cursors = repository.NewSyncCursorRepository(opts.DB)
	}
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	return &Service{
		db:                   opts.DB,
		accounts:             opts.Accounts,
		channels:             opts.Channels,
		messages:             opts.Messages,
		links:                opts.Links,
		files:                opts.Files,
		resources:            opts.Resources,
		notifications:        opts.Notifications,
		cursors:              opts.Cursors,
		telegram:             opts.Telegram,
		sessions:             opts.Sessions,
		extractor:            opts.Extractor,
		filter:               opts.Filter,
		historyBatchSize:     opts.HistoryBatchSize,
		workers:              opts.Workers,
		retryPolicy:          opts.RetryPolicy,
		requestGovernor:      opts.RequestGovernor,
		logger:               opts.Logger,
		settings:             opts.Settings,
		runtimeConfig:        opts.RuntimeConfig,
		aiMediaMetadataTasks: opts.AIMediaMetadataTasks,
		gapRecoveryCooldown:  opts.GapRecoveryCooldown,
		runningChannels:      map[int64]struct{}{},
	}
}

func (s *Service) SyncChannel(ctx context.Context, channelID int64) (SyncResult, error) {
	return s.SyncChannelWithProfile(ctx, channelID, "")
}

func (s *Service) SyncChannelWithProfile(ctx context.Context, channelID int64, profile string) (SyncResult, error) {
	return s.syncChannel(ctx, channelID, profile, 0, nil)
}

func (s *Service) SyncChannelWithMaxMessages(ctx context.Context, channelID int64, maxMessages int) (SyncResult, error) {
	return s.syncChannel(ctx, channelID, "", maxMessages, nil)
}

func (s *Service) SyncChannelWithProgress(ctx context.Context, channelID int64, profile string, progress taskpkg.ProgressSink) (SyncResult, error) {
	return s.syncChannel(ctx, channelID, profile, 0, progress)
}

func (s *Service) RunGapRecoveryTask(ctx context.Context, item model.Task, progress taskpkg.ProgressSink) error {
	var payload taskpkg.GapRecoveryPayload
	if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("decode gap recovery payload: %w", err)
	}
	// Bound each run: when Telegram is flaky the gotd RPC retry loop
	// (MaxRetries 20, RetryInterval 2s) can hang inside FetchHistory for many
	// minutes with no progress, monopolizing the single task worker and burning
	// CPU on retries. A deadline aborts the stuck RPC (gotd honors ctx), the run
	// fails, and the per-channel cooldown suppresses immediate re-enqueue. Any
	// batches already stored are committed, so progress is not lost.
	taskCtx, cancel := context.WithTimeout(ctx, gapRecoveryTaskTimeout)
	defer cancel()
	_, err := s.RecoverGapWithProgress(taskCtx, payload, progress)
	if s.gapRecoveryCooldown != nil && payload.ChannelID > 0 {
		if err != nil {
			s.gapRecoveryCooldown.RecordFailure(payload.ChannelID, time.Now().UTC())
		} else {
			s.gapRecoveryCooldown.RecordSuccess(payload.ChannelID)
		}
	}
	return err
}

func (s *Service) RunHistorySyncTask(ctx context.Context, item model.Task, progress taskpkg.ProgressSink) error {
	var payload taskpkg.HistorySyncPayload
	if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("decode history sync payload: %w", err)
	}
	channelIDs := normalizeHistorySyncChannelIDs(payload)
	if len(channelIDs) == 0 {
		return fmt.Errorf("history sync channel_ids is required")
	}
	if payload.MaxMessages < 0 {
		return fmt.Errorf("history sync max_messages must be non-negative")
	}
	total := len(channelIDs)
	for i, channelID := range channelIDs {
		if err := checkTaskStatus(ctx, progress); err != nil {
			return err
		}
		if err := reportTaskProgress(ctx, progress, i, total, fmt.Sprintf("syncing channel %d", channelID)); err != nil {
			return err
		}
		if _, err := s.syncChannel(ctx, channelID, "", payload.MaxMessages, historyTaskProgressSink{ProgressSink: progress}); err != nil {
			return err
		}
		if err := reportTaskProgress(ctx, progress, i+1, total, fmt.Sprintf("synced channel %d", channelID)); err != nil {
			return err
		}
	}
	return nil
}

func normalizeHistorySyncChannelIDs(payload taskpkg.HistorySyncPayload) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(payload.ChannelIDs)+1)
	if payload.ChannelID > 0 {
		seen[payload.ChannelID] = struct{}{}
		out = append(out, payload.ChannelID)
	}
	for _, id := range payload.ChannelIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

type historyTaskProgressSink struct {
	taskpkg.ProgressSink
}

func (s historyTaskProgressSink) Progress(context.Context, int64, int64, string) error {
	return nil
}

func (s historyTaskProgressSink) FloodWait(ctx context.Context, nextRunAt time.Time, message string) error {
	sink, ok := s.ProgressSink.(taskpkg.FloodWaitSink)
	if !ok {
		return nil
	}
	return sink.FloodWait(ctx, nextRunAt, message)
}

func (s *Service) RecoverGapWithProgress(ctx context.Context, payload taskpkg.GapRecoveryPayload, progress taskpkg.ProgressSink) (SyncResult, error) {
	if payload.ChannelID <= 0 || payload.FromMessageID <= 0 || payload.ToMessageID < payload.FromMessageID {
		return SyncResult{}, fmt.Errorf("invalid gap recovery range %d..%d for channel %d", payload.FromMessageID, payload.ToMessageID, payload.ChannelID)
	}
	channel, err := s.channels.FindByID(ctx, payload.ChannelID)
	if err != nil {
		return SyncResult{}, fmt.Errorf("load gap recovery channel: %w", err)
	}
	accountID := channel.AccountID
	if payload.AccountID > 0 {
		accountID = payload.AccountID
	}
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		return SyncResult{}, fmt.Errorf("load gap recovery account: %w", err)
	}
	if err := s.ensureAccountReady(ctx, account, progress); err != nil {
		return SyncResult{}, err
	}
	sessionPath := ""
	if s.sessions != nil {
		sessionPath = s.sessions.PathForAccount(account.ID)
	}
	accountSession := telegram.AccountSession{
		AccountID:   account.ID,
		Phone:       account.Phone,
		SessionPath: sessionPath,
	}
	ref := telegram.ChannelRef{
		TelegramChannelID: channel.TelegramChannelID,
		AccessHash:        channel.AccessHash,
		Type:              channel.Type,
	}
	triggerID := payload.TriggerMessageID
	if triggerID <= payload.ToMessageID {
		triggerID = payload.ToMessageID + 1
	}
	total := int(payload.ToMessageID - payload.FromMessageID + 1)
	offsetID := triggerID
	var result SyncResult
	var completed int64

	for {
		if err := checkTaskStatus(ctx, progress); err != nil {
			return result, err
		}
		batch, err := s.fetchHistory(ctx, account.ID, accountSession, ref, offsetID, s.historyBatchSize)
		if err != nil {
			err = fmt.Errorf("fetch gap recovery history: %w", err)
			if classification := retry.Classify(err); classification.Kind == retry.KindFloodWait {
				s.markAccountFloodWait(ctx, account.ID)
				s.notifyFloodWait(ctx, progress, s.retryPolicy.Delay(1, classification), classification.Err)
				return result, floodWaitDeferredError{err: classification.Err}
			}
			s.markAccountAuthFailure(ctx, account.ID, err)
			return result, err
		}
		if len(batch) == 0 {
			break
		}
		minID := int64(0)
		reachedLowerBound := false
		modelMessages := make([]model.Message, 0, len(batch))
		for _, item := range batch {
			if item.TelegramMessageID <= 0 {
				continue
			}
			if minID == 0 || item.TelegramMessageID < minID {
				minID = item.TelegramMessageID
			}
			if item.TelegramMessageID < payload.FromMessageID {
				reachedLowerBound = true
				continue
			}
			if item.TelegramMessageID > payload.ToMessageID {
				continue
			}
			modelMessages = append(modelMessages, model.Message{
				AccountID:         account.ID,
				ChannelID:         channel.ID,
				TelegramMessageID: item.TelegramMessageID,
				SenderID:          item.SenderID,
				MessageType:       item.MessageType,
				MediaSummary:      item.MediaSummary,
				Text:              item.Text,
				RawJSON:           item.RawJSON,
				Date:              item.Date,
				EditDate:          item.EditDate,
				Files:             item.Files,
			})
		}
		if len(modelMessages) > 0 {
			links, err := s.storeBatch(ctx, account.ID, channel.ID, 0, time.Now().UTC(), modelMessages)
			if err != nil {
				return result, err
			}
			result.Messages += len(modelMessages)
			result.Links += links
		}
		if minID > 0 {
			switch {
			case minID <= payload.FromMessageID:
				completed = int64(total)
			case minID <= payload.ToMessageID:
				completed = payload.ToMessageID - minID + 1
			}
		}
		if int64(result.Messages) > completed {
			completed = int64(result.Messages)
		}
		if completed > int64(total) {
			completed = int64(total)
		}
		if err := reportTaskProgress(ctx, progress, int(completed), total, "gap recovery batch stored"); err != nil {
			return result, err
		}
		if reachedLowerBound || minID == 0 || minID == offsetID || minID <= payload.FromMessageID {
			break
		}
		offsetID = minID
	}
	if _, err := s.storeBatch(ctx, account.ID, channel.ID, triggerID, time.Now().UTC(), nil); err != nil {
		return result, err
	}
	if err := reportTaskProgress(ctx, progress, total, total, "gap recovery completed"); err != nil {
		return result, err
	}
	if result.Messages > 0 {
		if err := s.refreshResourceStats(ctx); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Service) syncChannel(ctx context.Context, channelID int64, profile string, maxMessages int, progress taskpkg.ProgressSink) (SyncResult, error) {
	if !s.tryAcquireChannel(channelID) {
		return SyncResult{}, ErrChannelSyncInProgress
	}
	defer s.releaseChannel(channelID)
	return s.syncChannelWithRetry(ctx, channelID, profile, maxMessages, progress)
}

func (s *Service) SyncMany(ctx context.Context, channelIDs []int64) SyncManyResult {
	return s.SyncManyWithMaxMessages(ctx, channelIDs, 0)
}

func (s *Service) SyncManyWithMaxMessages(ctx context.Context, channelIDs []int64, maxMessages int) SyncManyResult {
	started := time.Now()
	result := SyncManyResult{
		Results:  map[int64]SyncResult{},
		Failures: map[int64]string{},
	}
	unique := make([]int64, 0, len(channelIDs))
	seen := map[int64]struct{}{}
	for _, channelID := range channelIDs {
		if channelID <= 0 {
			result.Skipped++
			continue
		}
		if _, ok := seen[channelID]; ok {
			result.Skipped++
			continue
		}
		seen[channelID] = struct{}{}
		unique = append(unique, channelID)
	}
	if len(unique) == 0 {
		s.logger.Info("history sync skipped", zap.Int("requested_channels", len(channelIDs)), zap.Int("skipped", result.Skipped))
		return result
	}

	workers := s.workers
	if workers <= 0 {
		workers = 1
	}
	if workers > len(unique) {
		workers = len(unique)
	}

	jobs := make(chan int64)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for channelID := range jobs {
				if !s.tryAcquireChannel(channelID) {
					s.logger.Info("history sync channel skipped because already running", zap.Int64("channel_id", channelID))
					mu.Lock()
					result.Skipped++
					mu.Unlock()
					continue
				}
				syncResult, err := s.syncChannelWithRetry(ctx, channelID, "", maxMessages, nil)
				s.releaseChannel(channelID)
				mu.Lock()
				if err != nil {
					s.logger.Warn("history sync channel failed", zap.Int64("channel_id", channelID), zap.Error(err))
					result.Failures[channelID] = err.Error()
				} else {
					s.logger.Info("history sync channel completed", zap.Int64("channel_id", channelID), zap.Int("messages", syncResult.Messages), zap.Int("links", syncResult.Links))
					result.Queued++
					result.Results[channelID] = syncResult
				}
				mu.Unlock()
			}
		}()
	}
	for _, channelID := range unique {
		select {
		case <-ctx.Done():
			mu.Lock()
			result.Failures[channelID] = ctx.Err().Error()
			mu.Unlock()
		case jobs <- channelID:
		}
	}
	close(jobs)
	wg.Wait()
	s.logger.Info("history sync many completed",
		zap.Int("requested_channels", len(channelIDs)),
		zap.Int("unique_channels", len(unique)),
		zap.Int("queued", result.Queued),
		zap.Int("skipped", result.Skipped),
		zap.Int("failures", len(result.Failures)),
		zap.Duration("duration", time.Since(started)),
	)
	return result
}

func (s *Service) StartListenBacklog(ctx context.Context) {
	s.mu.Lock()
	if s.backlogCancel != nil {
		s.mu.Unlock()
		s.logger.Info("listen backlog sync already running")
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.backlogCancel = cancel
	s.backlogWG.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.backlogWG.Done()
		started := time.Now()
		result := s.SyncListenBacklog(runCtx)
		s.logger.Info("listen backlog sync completed",
			zap.Int("queued", result.Queued),
			zap.Int("skipped", result.Skipped),
			zap.Int("failures", len(result.Failures)),
			zap.Duration("duration", time.Since(started)),
		)
		s.mu.Lock()
		s.backlogCancel = nil
		s.mu.Unlock()
	}()
	s.logger.Info("listen backlog sync started")
}

func (s *Service) StopListenBacklog(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.backlogCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return waitForHistoryWorkers(ctx, &s.backlogWG)
}

func (s *Service) SyncListenBacklog(ctx context.Context) SyncManyResult {
	result := SyncManyResult{
		Results:  map[int64]SyncResult{},
		Failures: map[int64]string{},
	}
	if s.channels == nil {
		return result
	}
	channels, err := s.channels.FindAll(ctx)
	if err != nil {
		result.Failures[0] = err.Error()
		return result
	}
	channelIDs := make([]int64, 0, len(channels))
	listenChannels := make(map[int64]model.Channel, len(channels))
	for _, channel := range channels {
		if !channel.ListenEnabled {
			result.Skipped++
			continue
		}
		channelIDs = append(channelIDs, channel.ID)
		listenChannels[channel.ID] = channel
	}
	if len(channelIDs) == 0 {
		return result
	}

	workers := s.workers
	if workers <= 0 {
		workers = 1
	}
	if workers > len(channelIDs) {
		workers = len(channelIDs)
	}

	jobs := make(chan int64)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for channelID := range jobs {
				channel := listenChannels[channelID]
				if !s.tryAcquireChannel(channelID) {
					mu.Lock()
					result.Skipped++
					mu.Unlock()
					continue
				}
				syncResult, err := s.syncListenBacklogChannel(ctx, channel)
				s.releaseChannel(channelID)
				mu.Lock()
				if err != nil {
					result.Failures[channelID] = err.Error()
				} else if syncResult.Messages == 0 {
					result.Skipped++
				} else {
					result.Queued++
					result.Results[channelID] = syncResult
				}
				mu.Unlock()
			}
		}()
	}
	for _, channelID := range channelIDs {
		select {
		case <-ctx.Done():
			mu.Lock()
			result.Failures[channelID] = ctx.Err().Error()
			mu.Unlock()
		case jobs <- channelID:
		}
	}
	close(jobs)
	wg.Wait()
	return result
}

func (s *Service) syncListenBacklogChannel(ctx context.Context, channel model.Channel) (SyncResult, error) {
	var result SyncResult
	floodWaitHandled := false
	err := s.runTelegramRetry(ctx, func() error {
		next, err := s.syncListenBacklogChannelOnce(ctx, channel)
		result = next
		return err
	}, func(ctx context.Context, attempt retry.Attempt) {
		s.logger.Warn("listen backlog sync retry scheduled",
			zap.Int64("channel_id", channel.ID),
			zap.Int("attempt", attempt.Number),
			zap.Duration("delay", attempt.Delay),
			zap.String("classification", string(attempt.Classification.Kind)),
			zap.Error(attempt.Classification.Err),
		)
		if attempt.Classification.Kind == retry.KindFloodWait {
			s.markChannelAccountStatus(ctx, channel.ID, model.AccountStatusFloodWait)
			floodWaitHandled = true
		}
	})
	if err != nil {
		if classification := retry.Classify(err); classification.Kind == retry.KindFloodWait && !floodWaitHandled {
			s.markChannelAccountStatus(ctx, channel.ID, model.AccountStatusFloodWait)
			return result, floodWaitDeferredError{err: classification.Err}
		}
		s.markChannelAuthFailure(ctx, channel.ID, err)
		return result, err
	}
	if result.Messages > 0 {
		if err := s.refreshResourceStats(ctx); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Service) syncListenBacklogChannelOnce(ctx context.Context, channel model.Channel) (SyncResult, error) {
	var result SyncResult
	lowerBound, err := s.listenBacklogLowerBound(ctx, channel)
	if err != nil {
		return result, err
	}
	if lowerBound <= 0 {
		s.logger.Info("listen backlog sync skipped because channel has no history cursor", zap.Int64("channel_id", channel.ID))
		return result, nil
	}
	account, err := s.accounts.FindByID(ctx, channel.AccountID)
	if err != nil {
		return result, fmt.Errorf("load account: %w", err)
	}
	if err := s.ensureAccountReady(ctx, account, nil); err != nil {
		return result, err
	}
	sessionPath := ""
	if s.sessions != nil {
		sessionPath = s.sessions.PathForAccount(account.ID)
	}
	accountSession := telegram.AccountSession{
		AccountID:   account.ID,
		Phone:       account.Phone,
		SessionPath: sessionPath,
	}
	ref := telegram.ChannelRef{
		TelegramChannelID: channel.TelegramChannelID,
		AccessHash:        channel.AccessHash,
		Type:              channel.Type,
	}

	firstBatch, err := s.fetchHistory(ctx, account.ID, accountSession, ref, 0, 1)
	if err != nil {
		return result, fmt.Errorf("fetch latest history: %w", err)
	}
	latestID := maxTelegramMessageID(firstBatch)
	if latestID <= lowerBound {
		return result, nil
	}

	maxSeen := latestID
	offsetID := int64(0)
	batch := firstBatch
	for {
		if len(batch) == 0 {
			break
		}
		minID := int64(0)
		reachedLowerBound := false
		modelMessages := make([]model.Message, 0, len(batch))
		for _, item := range batch {
			if item.TelegramMessageID <= 0 {
				continue
			}
			if minID == 0 || item.TelegramMessageID < minID {
				minID = item.TelegramMessageID
			}
			if item.TelegramMessageID > maxSeen {
				maxSeen = item.TelegramMessageID
			}
			if item.TelegramMessageID <= lowerBound {
				reachedLowerBound = true
				continue
			}
			modelMessages = append(modelMessages, model.Message{
				AccountID:         account.ID,
				ChannelID:         channel.ID,
				TelegramMessageID: item.TelegramMessageID,
				SenderID:          item.SenderID,
				MessageType:       item.MessageType,
				MediaSummary:      item.MediaSummary,
				Text:              item.Text,
				RawJSON:           item.RawJSON,
				Date:              item.Date,
				EditDate:          item.EditDate,
				Files:             item.Files,
			})
		}
		if len(modelMessages) > 0 {
			links, err := s.storeBatch(ctx, account.ID, channel.ID, maxSeen, time.Now().UTC(), modelMessages)
			if err != nil {
				return result, err
			}
			result.Messages += len(modelMessages)
			result.Links += links
		}
		if reachedLowerBound || minID == 0 || minID == offsetID {
			break
		}
		offsetID = minID
		batch, err = s.fetchHistory(ctx, account.ID, accountSession, ref, offsetID, s.historyBatchSize)
		if err != nil {
			return result, fmt.Errorf("fetch backlog history: %w", err)
		}
	}
	return result, nil
}

func (s *Service) listenBacklogLowerBound(ctx context.Context, channel model.Channel) (int64, error) {
	if s.cursors != nil {
		cursor, err := s.cursors.Find(ctx, channel.AccountID, channel.ID, "history")
		if err == nil {
			return cursor.LastMessageID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("load history cursor: %w", err)
		}
	}
	return channel.LastMessageID, nil
}

func maxTelegramMessageID(messages []telegram.Message) int64 {
	var maxID int64
	for _, msg := range messages {
		if msg.TelegramMessageID > maxID {
			maxID = msg.TelegramMessageID
		}
	}
	return maxID
}

func waitForHistoryWorkers(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) syncChannelWithRetry(ctx context.Context, channelID int64, profile string, maxMessages int, progress taskpkg.ProgressSink) (SyncResult, error) {
	started := time.Now()
	s.logger.Info("history sync channel started", zap.Int64("channel_id", channelID), zap.String("profile", profile))
	var result SyncResult
	floodWaitHandled := false
	err := s.runTelegramRetry(ctx, func() error {
		next, err := s.syncChannelOnce(ctx, channelID, profile, maxMessages, progress)
		result = next
		return err
	}, func(ctx context.Context, attempt retry.Attempt) {
		s.logger.Warn("history sync retry scheduled",
			zap.Int64("channel_id", channelID),
			zap.Int("attempt", attempt.Number),
			zap.Duration("delay", attempt.Delay),
			zap.String("classification", string(attempt.Classification.Kind)),
			zap.Error(attempt.Classification.Err),
		)
		if attempt.Classification.Kind == retry.KindFloodWait {
			s.handleChannelFloodWait(ctx, channelID, progress, attempt.Delay, attempt.Classification.Err)
			floodWaitHandled = true
		}
	})
	if err != nil {
		if classification := retry.Classify(err); classification.Kind == retry.KindFloodWait && !floodWaitHandled {
			s.handleChannelFloodWait(ctx, channelID, progress, s.retryPolicy.Delay(1, classification), classification.Err)
			err = floodWaitDeferredError{err: classification.Err}
		}
		s.logger.Error("history sync channel failed",
			zap.Int64("channel_id", channelID),
			zap.Int("messages", result.Messages),
			zap.Int("links", result.Links),
			zap.Duration("duration", time.Since(started)),
			zap.Error(err),
		)
		s.markChannelAuthFailure(ctx, channelID, err)
		return result, err
	}
	if result.Messages > 0 {
		if err := s.refreshResourceStats(ctx); err != nil {
			s.logger.Error("history sync refresh resource stats failed", zap.Int64("channel_id", channelID), zap.Error(err))
			return result, err
		}
	}
	if err := s.channels.MarkSynced(ctx, channelID, time.Now().UTC()); err != nil {
		s.logger.Error("history sync mark channel synced failed", zap.Int64("channel_id", channelID), zap.Error(err))
		return result, err
	}
	s.logger.Info("history sync channel completed",
		zap.Int64("channel_id", channelID),
		zap.Int("messages", result.Messages),
		zap.Int("links", result.Links),
		zap.Duration("duration", time.Since(started)),
	)
	return result, nil
}

func (s *Service) syncChannelOnce(ctx context.Context, channelID int64, requestedProfile string, maxMessages int, progress taskpkg.ProgressSink) (SyncResult, error) {
	channel, err := s.channels.FindByID(ctx, channelID)
	if err != nil {
		return SyncResult{}, fmt.Errorf("load channel: %w", err)
	}
	account, err := s.accounts.FindByID(ctx, channel.AccountID)
	if err != nil {
		return SyncResult{}, fmt.Errorf("load account: %w", err)
	}
	if err := s.ensureAccountReady(ctx, account, progress); err != nil {
		return SyncResult{}, err
	}

	sessionPath := ""
	if s.sessions != nil {
		sessionPath = s.sessions.PathForAccount(account.ID)
	}
	accountSession := telegram.AccountSession{
		AccountID:   account.ID,
		Phone:       account.Phone,
		SessionPath: sessionPath,
	}
	ref := telegram.ChannelRef{
		TelegramChannelID: channel.TelegramChannelID,
		AccessHash:        channel.AccessHash,
		Type:              channel.Type,
	}
	profile := requestedProfile
	if profile == "" {
		profile = channel.SyncProfile
	}
	if profile == "" {
		profile = channelpkg.SyncProfileNormal
	}
	profileLimit, err := channelpkg.ProfileLimit(profile)
	if err != nil {
		return SyncResult{}, err
	}
	if maxMessages > 0 {
		profileLimit = maxMessages
	}

	var result SyncResult
	var maxSeen int64
	offsetID := int64(0)
	for {
		if err := checkTaskStatus(ctx, progress); err != nil {
			return result, err
		}
		fetchLimit := s.historyBatchSize
		if profileLimit > 0 {
			remaining := profileLimit - result.Messages
			if remaining <= 0 {
				break
			}
			if fetchLimit > remaining {
				fetchLimit = remaining
			}
		}
		batch, err := s.fetchHistory(ctx, account.ID, accountSession, ref, offsetID, fetchLimit)
		if err != nil {
			return result, fmt.Errorf("fetch history: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		result.Messages += len(batch)
		minID := int64(0)
		modelMessages := make([]model.Message, 0, len(batch))
		for _, item := range batch {
			if item.TelegramMessageID <= 0 {
				continue
			}
			if minID == 0 || item.TelegramMessageID < minID {
				minID = item.TelegramMessageID
			}
			if item.TelegramMessageID > maxSeen {
				maxSeen = item.TelegramMessageID
			}
			modelMessages = append(modelMessages, model.Message{
				AccountID:         account.ID,
				ChannelID:         channel.ID,
				TelegramMessageID: item.TelegramMessageID,
				SenderID:          item.SenderID,
				MessageType:       item.MessageType,
				MediaSummary:      item.MediaSummary,
				Text:              item.Text,
				RawJSON:           item.RawJSON,
				Date:              item.Date,
				EditDate:          item.EditDate,
				Files:             item.Files,
			})
		}
		if len(modelMessages) > 0 {
			links, err := s.storeBatch(ctx, account.ID, channel.ID, maxSeen, time.Now().UTC(), modelMessages)
			if err != nil {
				return result, err
			}
			result.Links += links
		}
		if err := reportTaskProgress(ctx, progress, result.Messages, profileLimit, "history sync batch stored"); err != nil {
			return result, err
		}
		if minID == 0 || minID == offsetID {
			break
		}
		if profileLimit > 0 {
			if result.Messages >= profileLimit || len(batch) < fetchLimit {
				break
			}
		}
		offsetID = minID
	}
	return result, nil
}

func reportTaskProgress(ctx context.Context, progress taskpkg.ProgressSink, current int, total int, message string) error {
	if progress == nil {
		return nil
	}
	return progress.Progress(ctx, int64(current), int64(total), message)
}

func checkTaskStatus(ctx context.Context, progress taskpkg.ProgressSink) error {
	if progress == nil {
		return nil
	}
	status, err := progress.Status(ctx)
	if err != nil {
		return err
	}
	if taskpkg.IsCancelingStatus(status) {
		return context.Canceled
	}
	if status == model.TaskStatusPaused {
		return ErrTaskPaused
	}
	return nil
}

func (s *Service) refreshResourceStats(ctx context.Context) error {
	if s.resources == nil {
		return nil
	}
	return s.resources.RefreshGlobalGrouped(ctx)
}

func (s *Service) tryAcquireChannel(channelID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runningChannels[channelID]; ok {
		return false
	}
	s.runningChannels[channelID] = struct{}{}
	return true
}

func (s *Service) releaseChannel(channelID int64) {
	s.mu.Lock()
	delete(s.runningChannels, channelID)
	s.mu.Unlock()
}

func (s *Service) markChannelAccountStatus(ctx context.Context, channelID int64, status string) {
	if s.accounts == nil || s.channels == nil {
		return
	}
	channel, err := s.channels.FindByID(ctx, channelID)
	if err != nil {
		return
	}
	_ = s.accounts.UpdateStatus(ctx, channel.AccountID, status)
}

func (s *Service) fetchHistory(ctx context.Context, accountID int64, account telegram.AccountSession, ref telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	var out []telegram.Message
	err := s.requestGovernor.Run(ctx, accountID, telegramguard.OperationFetchHistory, func() error {
		var err error
		out, err = s.telegram.FetchHistory(ctx, account, ref, offsetID, limit)
		return err
	})
	return out, err
}

func (s *Service) markAccountFloodWait(ctx context.Context, accountID int64) {
	if s.accounts == nil || accountID <= 0 {
		return
	}
	_ = s.accounts.UpdateStatus(ctx, accountID, model.AccountStatusFloodWait)
}

func (s *Service) handleChannelFloodWait(ctx context.Context, channelID int64, progress taskpkg.ProgressSink, delay time.Duration, err error) {
	s.markChannelAccountStatus(ctx, channelID, model.AccountStatusFloodWait)
	s.notifyFloodWait(ctx, progress, delay, err)
}

func (s *Service) notifyFloodWait(ctx context.Context, progress taskpkg.ProgressSink, delay time.Duration, err error) {
	if progress == nil {
		return
	}
	sink, ok := progress.(taskpkg.FloodWaitSink)
	if !ok {
		return
	}
	if delay < 0 {
		delay = 0
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	_ = sink.FloodWait(ctx, time.Now().UTC().Add(delay), message)
}

func (s *Service) runTelegramRetry(ctx context.Context, fn func() error, onRetry func(context.Context, retry.Attempt)) error {
	policy := s.retryPolicy
	sleep := policy.Sleep
	var deferred error
	policy.Sleep = func(ctx context.Context, d time.Duration) error {
		if deferred != nil {
			return deferred
		}
		if sleep != nil {
			return sleep(ctx, d)
		}
		return sleepContext(ctx, d)
	}
	return policy.Run(ctx, fn, func(ctx context.Context, attempt retry.Attempt) {
		if onRetry != nil {
			onRetry(ctx, attempt)
		}
		if attempt.Classification.Kind == retry.KindFloodWait {
			deferred = floodWaitDeferredError{err: attempt.Classification.Err}
		}
	})
}

func (s *Service) ensureAccountReady(ctx context.Context, account model.Account, progress taskpkg.ProgressSink) error {
	if account.Status == model.AccountStatusOnline {
		return nil
	}
	err := fmt.Errorf("%w: account %d status %s", ErrAccountNotReady, account.ID, account.Status)
	if account.Status == model.AccountStatusFloodWait {
		s.notifyFloodWait(ctx, progress, accountFloodWaitDeferral, err)
		return floodWaitDeferredError{err: err}
	}
	return retry.Permanent(err)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) markChannelAuthFailure(ctx context.Context, channelID int64, err error) {
	if s.accounts == nil || s.channels == nil || retry.Classify(err).Kind != retry.KindAuth {
		return
	}
	channel, findErr := s.channels.FindByID(ctx, channelID)
	if findErr != nil {
		return
	}
	s.markAccountAuthFailure(ctx, channel.AccountID, err)
}

func (s *Service) markAccountAuthFailure(ctx context.Context, accountID int64, err error) {
	if s.accounts == nil || accountID <= 0 || retry.Classify(err).Kind != retry.KindAuth {
		return
	}
	_ = s.accounts.UpdateStatus(ctx, accountID, model.AccountStatusLoginRequired)
}

func (s *Service) storeBatch(ctx context.Context, accountID int64, channelID int64, cursor int64, cursorDate time.Time, messages []model.Message) (int, error) {
	filtered := make([]model.Message, 0, len(messages))
	linksByTelegramID := map[int64][]model.Link{}
	for _, msg := range messages {
		extracted := s.extractor.Extract(msg.Text)
		if s.filter != nil {
			result, err := s.filter.Apply(ctx, messagefilter.Request{
				ChannelID:      msg.ChannelID,
				Text:           msg.Text,
				MessageType:    msg.MessageType,
				Files:          msg.Files,
				RequireRule:    false,
				RequireEnabled: false,
			})
			if err != nil {
				return 0, fmt.Errorf("filter history message: %w", err)
			}
			if !result.Keep {
				continue
			}
			extracted = result.Links
		}
		filtered = append(filtered, msg)
		linksByTelegramID[msg.TelegramMessageID] = extracted
	}

	var linkCount int
	channel := model.Channel{ID: channelID, AccountID: accountID}
	if s.channels != nil {
		if found, err := s.channels.FindByID(ctx, channelID); err == nil {
			channel = found
		}
	}
	createdResources := []resource.Item{}
	aiMessageIDs := []int64{}
	storedMessageIDs := []int64{}
	err := dbpkg.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		if len(filtered) > 0 {
			stored, err := s.messages.SaveBatchTx(ctx, tx, filtered)
			if err != nil {
				return err
			}
			for _, msg := range stored {
				storedMessageIDs = append(storedMessageIDs, msg.ID)
				extracted := linksByTelegramID[msg.TelegramMessageID]
				savedLinks, err := s.links.ReplaceForMessageTx(ctx, tx, msg.ID, extracted)
				if err != nil {
					return err
				}
				if hasCloudDriveLinks(savedLinks) {
					aiMessageIDs = append(aiMessageIDs, msg.ID)
				}
				if s.files != nil {
					if _, err := s.files.ReplaceForMessageTx(ctx, tx, msg.ID, msg.Files); err != nil {
						return err
					}
				}
				linkCount += len(extracted)
				createdResources = append(createdResources, resourceItemsFromMessage(channel, msg, extracted, msg.Files)...)
			}
		}
		if cursor > 0 && s.cursors != nil {
			if err := s.cursors.SaveTx(ctx, tx, model.SyncCursor{
				AccountID:     accountID,
				ChannelID:     channelID,
				CursorType:    "history",
				LastMessageID: cursor,
				Date:          cursorDate,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("store history batch: %w", err)
	}
	if s.resources != nil && len(storedMessageIDs) > 0 {
		if err := s.resources.RefreshMessages(ctx, storedMessageIDs); err != nil {
			return 0, fmt.Errorf("refresh resource index: %w", err)
		}
	}
	s.enqueueCreatedResources(ctx, createdResources)
	s.enqueueAIMediaMetadataTasks(ctx, aiMessageIDs)
	return linkCount, nil
}

func (s *Service) enqueueAIMediaMetadataTasks(ctx context.Context, messageIDs []int64) {
	if s.aiMediaMetadataTasks == nil || len(messageIDs) == 0 || !s.aiMediaMetadataEnabled(ctx) {
		return
	}
	for _, messageID := range messageIDs {
		if _, err := s.aiMediaMetadataTasks.Enqueue(ctx, model.TaskTypeAIMediaMetadata, taskpkg.AIMediaMetadataPayload{MessageID: messageID}); err != nil {
			s.logger.Warn("enqueue ai media metadata task failed", zap.Int64("message_id", messageID), zap.Error(err))
		}
	}
}

func (s *Service) aiMediaMetadataEnabled(ctx context.Context) bool {
	if s.settings == nil {
		return false
	}
	settings, err := s.settings.LoadRuntimeSettings(ctx, s.runtimeConfig)
	if err != nil {
		s.logger.Warn("load runtime settings for ai media metadata failed", zap.Error(err))
		return false
	}
	return settings.AI.MediaMetadata.Enabled
}

func hasCloudDriveLinks(links []model.Link) bool {
	for _, link := range links {
		if link.Category == "cloud_drive" || (link.Category == "" && isCloudDriveLinkType(link.Type)) {
			return true
		}
	}
	return false
}

func isCloudDriveLinkType(typ string) bool {
	switch typ {
	case "quark", "aliyun", "baidu", "115", "uc", "xunlei", "tianyi", "mobile", "123", "pikpak", "guangya":
		return true
	default:
		return false
	}
}

func (s *Service) enqueueCreatedResources(ctx context.Context, items []resource.Item) {
	if s.notifications == nil {
		return
	}
	for _, item := range items {
		if _, err := s.notifications.EnqueueResourceCreated(ctx, item); err != nil {
			s.logger.Warn("enqueue resource notification failed", zap.String("resource_id", item.ID), zap.Error(err))
		}
	}
}

func resourceItemsFromMessage(channel model.Channel, msg model.Message, links []model.Link, files []model.File) []resource.Item {
	items := make([]resource.Item, 0, len(links)+len(files))
	for _, link := range links {
		category := link.Category
		if category == "" {
			category = resourceCategoryFromLink(link)
		}
		items = append(items, resource.Item{
			ID:                "link:" + link.URL,
			Kind:              "link",
			Type:              link.Type,
			Category:          category,
			URL:               link.URL,
			Password:          link.Password,
			Note:              link.Note,
			Title:             firstResourceTitle(link.Note, link.URL),
			SourceSnippet:     link.SourceSnippet,
			Datetime:          msg.Date,
			AccountID:         msg.AccountID,
			ChannelID:         channel.ID,
			TelegramChannelID: channel.TelegramChannelID,
			ChannelTitle:      channel.Title,
			ChannelUsername:   channel.Username,
			TelegramMessageID: msg.TelegramMessageID,
			MessageType:       msg.MessageType,
			MediaSummary:      msg.MediaSummary,
		})
	}
	for _, file := range files {
		items = append(items, resource.Item{
			ID:                "file:" + file.FileName,
			Kind:              "file",
			Type:              file.Category,
			Category:          "files",
			FileName:          file.FileName,
			Extension:         file.Extension,
			MimeType:          file.MimeType,
			SizeBytes:         file.SizeBytes,
			Title:             file.FileName,
			Datetime:          msg.Date,
			AccountID:         msg.AccountID,
			ChannelID:         channel.ID,
			TelegramChannelID: channel.TelegramChannelID,
			ChannelTitle:      channel.Title,
			ChannelUsername:   channel.Username,
			TelegramMessageID: msg.TelegramMessageID,
			MessageType:       msg.MessageType,
			MediaSummary:      msg.MediaSummary,
		})
	}
	return items
}

func resourceCategoryFromLink(link model.Link) string {
	switch link.Type {
	case "magnet":
		return "magnet"
	case "ed2k":
		return "ed2k"
	case "url":
		return "http"
	default:
		return "cloud_drive"
	}
}

func firstResourceTitle(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
