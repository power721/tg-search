package avatar

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"tg-search/internal/medialimit"
	"tg-search/internal/model"
	"tg-search/internal/retry"
	"tg-search/internal/scheduler"
	"tg-search/internal/telegram"
)

func TestNewService(t *testing.T) {
	queue := scheduler.NewRetryQueue(scheduler.RetryQueueOptions{
		Policy: retry.DefaultPolicy(),
		Logger: zap.NewNop(),
	})
	limiter := medialimit.New(5)

	svc := NewService(ServiceOptions{
		StorageRoot: "/tmp/test",
		Queue:       queue,
		Limiter:     limiter,
		Telegram:    nil,
		Logger:      zap.NewNop(),
	})

	if svc == nil {
		t.Fatal("NewService() returned nil")
	}
}

type mockTelegram struct {
	telegram.NopClient
	downloadUserAvatarCalled bool
}

func (m *mockTelegram) DownloadUserAvatar(ctx context.Context, session telegram.AccountSession, userID int64, photoID int64) (telegram.ImageFile, error) {
	m.downloadUserAvatarCalled = true
	return telegram.ImageFile{Data: []byte("avatar"), MIMEType: "image/jpeg"}, nil
}

// drainQueue blocks until every background worker the RetryQueue spawned has
// finished. Without this, enqueue tests return while workers are still writing
// avatar files into t.TempDir, racing with TempDir cleanup
// ("TempDir RemoveAll cleanup: ... directory not empty").
func drainQueue(t *testing.T, queue *scheduler.RetryQueue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := queue.Stop(ctx); err != nil {
		t.Fatalf("queue.Stop: %v", err)
	}
}

func TestEnqueueAccountAvatar(t *testing.T) {
	tmpDir := t.TempDir()
	queue := scheduler.NewRetryQueue(scheduler.RetryQueueOptions{
		Policy: retry.DefaultPolicy(),
		Logger: zap.NewNop(),
	})
	limiter := medialimit.New(5)
	mock := &mockTelegram{}

	svc := NewService(ServiceOptions{
		StorageRoot: tmpDir,
		Queue:       queue,
		Limiter:     limiter,
		Telegram:    mock,
		Logger:      zap.NewNop(),
	})

	account := model.Account{
		ID:             123,
		Phone:          "+1234567890",
		TelegramUserID: 999,
		PhotoID:        456789,
	}

	job := svc.EnqueueAccountAvatar(context.Background(), account)
	if job.ID == "" {
		t.Fatal("EnqueueAccountAvatar() returned empty job ID")
	}
	drainQueue(t, queue)
}

type mockTelegramChannel struct {
	telegram.NopClient
	downloadChannelAvatarCalled atomic.Bool
}

func (m *mockTelegramChannel) DownloadChannelAvatar(ctx context.Context, session telegram.AccountSession, channelID int64, accessHash int64, photoID int64) (telegram.ImageFile, error) {
	m.downloadChannelAvatarCalled.Store(true)
	return telegram.ImageFile{Data: []byte("channel-avatar"), MIMEType: "image/jpeg"}, nil
}

func TestEnqueueChannelAvatar(t *testing.T) {
	tmpDir := t.TempDir()
	queue := scheduler.NewRetryQueue(scheduler.RetryQueueOptions{
		Policy: retry.DefaultPolicy(),
		Logger: zap.NewNop(),
	})
	limiter := medialimit.New(5)
	mock := &mockTelegramChannel{}

	svc := NewService(ServiceOptions{
		StorageRoot: tmpDir,
		Queue:       queue,
		Limiter:     limiter,
		Telegram:    mock,
		Logger:      zap.NewNop(),
	})

	account := model.Account{ID: 1, Phone: "+1234567890"}
	channel := model.Channel{
		ID:                2,
		AccountID:         1,
		TelegramChannelID: 888,
		AccessHash:        777,
		PhotoID:           555,
	}

	job := svc.EnqueueChannelAvatar(context.Background(), account, channel)
	if job.ID == "" {
		t.Fatal("EnqueueChannelAvatar() returned empty job ID")
	}
	drainQueue(t, queue)
}

func TestEnqueueChannelAvatars(t *testing.T) {
	tmpDir := t.TempDir()
	queue := scheduler.NewRetryQueue(scheduler.RetryQueueOptions{
		Policy: retry.DefaultPolicy(),
		Logger: zap.NewNop(),
	})
	limiter := medialimit.New(5)
	mock := &mockTelegramChannel{}

	svc := NewService(ServiceOptions{
		StorageRoot: tmpDir,
		Queue:       queue,
		Limiter:     limiter,
		Telegram:    mock,
		Logger:      zap.NewNop(),
	})

	account := model.Account{ID: 1, Phone: "+1234567890"}
	channels := []model.Channel{
		{ID: 2, AccountID: 1, TelegramChannelID: 888, AccessHash: 777, PhotoID: 555},
		{ID: 3, AccountID: 1, TelegramChannelID: 999, AccessHash: 666, PhotoID: 444},
		{ID: 4, AccountID: 1, TelegramChannelID: 111, AccessHash: 222, PhotoID: 0}, // no photo
	}

	jobs := svc.EnqueueChannelAvatars(context.Background(), account, channels)
	if len(jobs) != 2 {
		t.Errorf("EnqueueChannelAvatars() returned %d jobs, want 2", len(jobs))
	}
	drainQueue(t, queue)
}
