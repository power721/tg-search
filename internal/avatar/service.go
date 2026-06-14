package avatar

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"tg-search/internal/medialimit"
	"tg-search/internal/model"
	"tg-search/internal/scheduler"
	"tg-search/internal/telegram"
)

type ServiceOptions struct {
	StorageRoot string
	Queue       *scheduler.RetryQueue
	Limiter     *medialimit.Limiter
	Telegram    telegram.Client
	Logger      *zap.Logger
}

type Service struct {
	storageRoot string
	queue       *scheduler.RetryQueue
	limiter     *medialimit.Limiter
	telegram    telegram.Client
	logger      *zap.Logger
}

func NewService(opts ServiceOptions) *Service {
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	if opts.Telegram == nil {
		opts.Telegram = telegram.NopClient{}
	}
	return &Service{
		storageRoot: opts.StorageRoot,
		queue:       opts.Queue,
		limiter:     opts.Limiter,
		telegram:    opts.Telegram,
		logger:      opts.Logger,
	}
}

// EnqueueAccountAvatar enqueues an account avatar download task.
// Skips if PhotoID is 0 or file already exists.
func (s *Service) EnqueueAccountAvatar(ctx context.Context, account model.Account) scheduler.RetryJob {
	if account.PhotoID <= 0 {
		return scheduler.RetryJob{}
	}
	path := AvatarAbsolutePath(s.storageRoot, "account", account.ID, account.PhotoID)
	if FileExists(path) {
		return scheduler.RetryJob{}
	}
	if s.queue == nil {
		return scheduler.RetryJob{}
	}

	name := fmt.Sprintf("avatar-account-%d-%d", account.ID, account.PhotoID)
	return s.queue.Enqueue(ctx, name, func(ctx context.Context) error {
		return s.downloadAccountAvatar(ctx, account, path)
	})
}

func (s *Service) downloadAccountAvatar(ctx context.Context, account model.Account, destPath string) error {
	// Check again in case another worker downloaded it
	if FileExists(destPath) {
		return nil
	}

	session := telegram.AccountSession{
		AccountID:   account.ID,
		Phone:       account.Phone,
		SessionPath: account.SessionPath,
	}

	downloadFn := func() error {
		img, err := s.telegram.DownloadUserAvatar(ctx, session, account.TelegramUserID, account.PhotoID)
		if err != nil {
			return fmt.Errorf("download user avatar: %w", err)
		}
		if err := WriteAvatarFile(destPath, img.Data); err != nil {
			return fmt.Errorf("write avatar file: %w", err)
		}
		return nil
	}

	if s.limiter != nil {
		return s.limiter.Run(ctx, downloadFn)
	}
	return downloadFn()
}

// EnqueueChannelAvatar enqueues a channel avatar download task.
// Skips if PhotoID is 0 or file already exists.
func (s *Service) EnqueueChannelAvatar(ctx context.Context, account model.Account, channel model.Channel) scheduler.RetryJob {
	if channel.PhotoID <= 0 {
		s.logger.Debug("skipping channel avatar: no photo_id", zap.Int64("channel_id", channel.ID))
		return scheduler.RetryJob{}
	}
	path := AvatarAbsolutePath(s.storageRoot, "channel", channel.ID, channel.PhotoID)
	if FileExists(path) {
		s.logger.Debug("skipping channel avatar: file exists", zap.Int64("channel_id", channel.ID), zap.String("path", path))
		return scheduler.RetryJob{}
	}
	if s.queue == nil {
		s.logger.Warn("skipping channel avatar: queue is nil", zap.Int64("channel_id", channel.ID))
		return scheduler.RetryJob{}
	}

	name := fmt.Sprintf("avatar-channel-%d-%d", channel.ID, channel.PhotoID)
	s.logger.Info("enqueuing channel avatar download", zap.Int64("channel_id", channel.ID), zap.Int64("photo_id", channel.PhotoID), zap.String("path", path))
	return s.queue.Enqueue(ctx, name, func(ctx context.Context) error {
		return s.downloadChannelAvatar(ctx, account, channel, path)
	})
}

func (s *Service) downloadChannelAvatar(ctx context.Context, account model.Account, channel model.Channel, destPath string) error {
	if FileExists(destPath) {
		return nil
	}

	session := telegram.AccountSession{
		AccountID:   account.ID,
		Phone:       account.Phone,
		SessionPath: account.SessionPath,
	}

	downloadFn := func() error {
		img, err := s.telegram.DownloadChannelAvatar(ctx, session, channel.TelegramChannelID, channel.AccessHash, channel.PhotoID)
		if err != nil {
			return fmt.Errorf("download channel avatar: %w", err)
		}
		if err := WriteAvatarFile(destPath, img.Data); err != nil {
			return fmt.Errorf("write avatar file: %w", err)
		}
		return nil
	}

	if s.limiter != nil {
		return s.limiter.Run(ctx, downloadFn)
	}
	return downloadFn()
}

// EnqueueChannelAvatars enqueues avatar download tasks for multiple channels.
func (s *Service) EnqueueChannelAvatars(ctx context.Context, account model.Account, channels []model.Channel) []scheduler.RetryJob {
	var jobs []scheduler.RetryJob
	for _, channel := range channels {
		job := s.EnqueueChannelAvatar(ctx, account, channel)
		if job.ID != "" {
			jobs = append(jobs, job)
		}
	}
	return jobs
}
