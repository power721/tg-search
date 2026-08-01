package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"tg-search/internal/config"
	"tg-search/internal/db"
	"tg-search/internal/link"
	"tg-search/internal/messagefilter"
	"tg-search/internal/model"
	"tg-search/internal/repository"
	"tg-search/internal/resource"
	"tg-search/internal/retry"
	"tg-search/internal/session"
	taskpkg "tg-search/internal/task"
	"tg-search/internal/telegram"
	"tg-search/internal/telegramguard"
)

func TestSyncChannelUsesSyncProfile(t *testing.T) {
	profiles := []struct {
		name      string
		available int
		want      int
	}{
		{name: "Quick", available: 101, want: 100},
		{name: "Normal", available: 1001, want: 1000},
		{name: "Deep", available: 10001, want: 10000},
	}
	for _, tt := range profiles {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			conn, accounts, channels, messages, links := setupHistoryTestStore(t)
			accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
			if err != nil {
				t.Fatalf("save account: %v", err)
			}
			channelID, err := channels.Save(ctx, model.Channel{
				AccountID:          accountID,
				TelegramChannelID:  200,
				AccessHash:         300,
				Title:              "VIP",
				Type:               model.ChannelTypeChannel,
				HistorySyncEnabled: true,
				SyncProfile:        tt.name,
			})
			if err != nil {
				t.Fatalf("save channel: %v", err)
			}

			fake := &profileLimitHistoryClient{available: tt.available, startID: 50000}
			service := NewService(Options{
				DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
				Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
				Extractor: link.NewExtractor(), HistoryBatchSize: 600,
			})

			result, err := service.SyncChannel(ctx, channelID)
			if err != nil {
				t.Fatalf("SyncChannel returned error: %v", err)
			}
			if result.Messages != tt.want {
				t.Fatalf("messages = %d, want %d", result.Messages, tt.want)
			}
			if fake.fetched != tt.want {
				t.Fatalf("fetched = %d, want %d", fake.fetched, tt.want)
			}

			cursor, err := repository.NewSyncCursorRepository(conn).Find(ctx, accountID, channelID, "history")
			if err != nil {
				t.Fatalf("find sync cursor: %v", err)
			}
			if cursor.LastMessageID != 50000 {
				t.Fatalf("cursor last message id = %d, want 50000", cursor.LastMessageID)
			}
			if cursor.Date.IsZero() {
				t.Fatal("cursor date is zero")
			}

			channel, err := channels.FindByID(ctx, channelID)
			if err != nil {
				t.Fatalf("find channel: %v", err)
			}
			if channel.LastMessageID != 0 {
				t.Fatalf("channel last message id = %d, want 0 because sync cursor table owns history state", channel.LastMessageID)
			}
			if channel.SyncState != "synced" {
				t.Fatalf("channel sync state = %q, want synced", channel.SyncState)
			}
		})
	}
}

func TestSyncChannelWithMaxMessagesOverridesProfile(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	channelID, err := channels.Save(ctx, model.Channel{
		AccountID:          accountID,
		TelegramChannelID:  200,
		AccessHash:         300,
		Title:              "VIP",
		Type:               model.ChannelTypeChannel,
		HistorySyncEnabled: true,
		SyncProfile:        "Full",
	})
	if err != nil {
		t.Fatalf("save channel: %v", err)
	}

	fake := &profileLimitHistoryClient{available: 251, startID: 50000}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 600,
	})

	result, err := service.SyncChannelWithMaxMessages(ctx, channelID, 250)
	if err != nil {
		t.Fatalf("SyncChannelWithMaxMessages returned error: %v", err)
	}
	if result.Messages != 250 {
		t.Fatalf("messages = %d, want 250", result.Messages)
	}
	if fake.fetched != 250 {
		t.Fatalf("fetched = %d, want 250", fake.fetched)
	}
}

func TestSyncChannelFullProfileFetchesUntilEmptyBatch(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	channelID, err := channels.Save(ctx, model.Channel{
		AccountID:          accountID,
		TelegramChannelID:  200,
		AccessHash:         300,
		Title:              "VIP",
		Type:               model.ChannelTypeChannel,
		HistorySyncEnabled: true,
		SyncProfile:        "Full",
	})
	if err != nil {
		t.Fatalf("save channel: %v", err)
	}

	fake := &scriptedHistoryClient{batches: [][]telegram.Message{
		{
			{TelegramMessageID: 3, Text: "short batch 3", RawJSON: "{}", Date: time.Now().UTC()},
			{TelegramMessageID: 2, Text: "short batch 2", RawJSON: "{}", Date: time.Now().UTC()},
		},
		{},
	}}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 5,
	})

	result, err := service.SyncChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("SyncChannel returned error: %v", err)
	}
	if result.Messages != 2 {
		t.Fatalf("messages = %d, want 2", result.Messages)
	}
	if fake.calls != 2 {
		t.Fatalf("fetch calls = %d, want 2 so Full stops only after empty batch", fake.calls)
	}
	if !reflect.DeepEqual(fake.offsets, []int64{0, 2}) {
		t.Fatalf("offsets = %v, want [0 2]", fake.offsets)
	}
}

func TestSyncChannelStoresBatchesLinksAndCursor(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	accounts := repository.NewAccountRepository(conn)
	channels := repository.NewChannelRepository(conn)
	messages := repository.NewMessageRepository(conn)
	links := repository.NewLinkRepository(conn)
	files := repository.NewFileRepository(conn)
	resourceStats := repository.NewResourceStatsRepository(conn)
	resources := resource.NewService(links, files, resourceStats)
	status := repository.NewStatusRepository(conn)

	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	channelID, err := channels.Save(ctx, model.Channel{
		AccountID:          accountID,
		TelegramChannelID:  200,
		AccessHash:         300,
		Title:              "VIP",
		Type:               model.ChannelTypeChannel,
		HistorySyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("save channel: %v", err)
	}

	now := time.Now().UTC()
	fake := &fakeTelegramClient{batches: map[int64][]telegram.Message{
		0: {
			{TelegramMessageID: 11, SenderID: 1, Text: "最新消息", RawJSON: "{}", Date: now, Files: []model.File{{
				FileName:  "ubuntu.iso",
				Extension: ".iso",
				MimeType:  "application/x-iso9660-image",
				SizeBytes: 5000,
			}}},
			{TelegramMessageID: 10, SenderID: 1, Text: "庆余年 https://www.alipan.com/s/abc123 提取码: abcd", RawJSON: "{}", Date: now.Add(-time.Minute)},
		},
		10: {
			{TelegramMessageID: 10, SenderID: 1, Text: "庆余年 https://www.alipan.com/s/abc123 提取码: abcd", RawJSON: "{}", Date: now.Add(-time.Minute)},
			{TelegramMessageID: 9, SenderID: 1, Text: "更早消息 magnet:?xt=urn:btih:abc", RawJSON: "{}", Date: now.Add(-2 * time.Minute)},
		},
		9: {},
	}}
	service := NewService(Options{
		DB:               conn,
		Accounts:         accounts,
		Channels:         channels,
		Messages:         messages,
		Links:            links,
		Files:            files,
		Resources:        resources,
		Telegram:         fake,
		Sessions:         session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor:        link.NewExtractor(),
		HistoryBatchSize: 2,
	})

	result, err := service.SyncChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("SyncChannel returned error: %v", err)
	}
	if result.Messages != 4 {
		t.Fatalf("result messages = %d, want 4 fetched messages including duplicate", result.Messages)
	}

	counts, err := status.Counts(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Messages != 3 {
		t.Fatalf("stored message count = %d, want 3 unique messages", counts.Messages)
	}
	if counts.Links != 2 {
		t.Fatalf("stored link count = %d, want 2", counts.Links)
	}
	storedFiles, err := files.Search(ctx, repository.FileSearchParams{Query: "ubuntu", Limit: 10})
	if err != nil {
		t.Fatalf("search files: %v", err)
	}
	if len(storedFiles) != 1 || storedFiles[0].FileName != "ubuntu.iso" {
		t.Fatalf("stored files = %+v, want ubuntu.iso", storedFiles)
	}
	grouped, found, err := resourceStats.GetGrouped(ctx)
	if err != nil {
		t.Fatalf("get resource stats: %v", err)
	}
	if !found || grouped["_total"] != 3 {
		t.Fatalf("resource stats = %+v found=%v, want _total=3", grouped, found)
	}

	linkResults, err := links.Search(ctx, repository.LinkSearchParams{Type: "aliyun", Limit: 10})
	if err != nil {
		t.Fatalf("search aliyun links: %v", err)
	}
	if len(linkResults) != 1 || linkResults[0].Type != "aliyun" {
		t.Fatalf("aliyun links = %+v, want 1 aliyun link", linkResults)
	}

	cursor, err := repository.NewSyncCursorRepository(conn).Find(ctx, accountID, channelID, "history")
	if err != nil {
		t.Fatalf("find sync cursor: %v", err)
	}
	if cursor.LastMessageID != 11 {
		t.Fatalf("cursor last message id = %d, want 11", cursor.LastMessageID)
	}
	channel, err := channels.FindByID(ctx, channelID)
	if err != nil {
		t.Fatalf("find channel: %v", err)
	}
	if channel.LastMessageID != 0 {
		t.Fatalf("channel last message id = %d, want 0 because sync cursor table owns history state", channel.LastMessageID)
	}
	if channel.SyncState != "synced" {
		t.Fatalf("channel sync state = %q, want synced", channel.SyncState)
	}
}

func TestSyncChannelIgnoresHistorySyncDisabledFlag(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	if err := channels.UpdateControl(ctx, channelID, model.ChannelControl{
		HistorySyncEnabled:  false,
		SyncProfile:         "Normal",
		ListenEnabled:       false,
		RemoteSearchAllowed: true,
	}); err != nil {
		t.Fatalf("disable history sync: %v", err)
	}
	fake := &profileLimitHistoryClient{available: 1, startID: 50000}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10,
	})

	result, err := service.SyncChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("SyncChannel returned error: %v", err)
	}
	if result.Messages != 1 || fake.fetched != 1 {
		t.Fatalf("result = %+v fetched=%d, want manual sync to fetch history", result, fake.fetched)
	}
}

func TestSyncListenBacklogSyncsListenChannelToHistoryCursor(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	channelID, err := channels.Save(ctx, model.Channel{
		AccountID:          accountID,
		TelegramChannelID:  200,
		AccessHash:         300,
		Title:              "Listen",
		Type:               model.ChannelTypeChannel,
		HistorySyncEnabled: false,
		ListenEnabled:      true,
	})
	if err != nil {
		t.Fatalf("save listen channel: %v", err)
	}
	disabledID, err := channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 201,
		AccessHash:        301,
		Title:             "Disabled",
		Type:              model.ChannelTypeChannel,
		ListenEnabled:     false,
	})
	if err != nil {
		t.Fatalf("save disabled channel: %v", err)
	}
	cursors := repository.NewSyncCursorRepository(conn)
	if err := cursors.Save(ctx, model.SyncCursor{
		AccountID: accountID, ChannelID: channelID, CursorType: "history", LastMessageID: 10, Date: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save cursor: %v", err)
	}
	if err := cursors.Save(ctx, model.SyncCursor{
		AccountID: accountID, ChannelID: disabledID, CursorType: "history", LastMessageID: 10, Date: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save disabled cursor: %v", err)
	}

	now := time.Now().UTC()
	fake := &fakeTelegramClient{batches: map[int64][]telegram.Message{
		0:  {{TelegramMessageID: 15, Text: "latest https://pan.quark.cn/s/15", RawJSON: "{}", Date: now}},
		15: {{TelegramMessageID: 14, Text: "m14", RawJSON: "{}", Date: now}, {TelegramMessageID: 13, Text: "m13", RawJSON: "{}", Date: now}},
		13: {{TelegramMessageID: 12, Text: "m12", RawJSON: "{}", Date: now}, {TelegramMessageID: 11, Text: "m11", RawJSON: "{}", Date: now}, {TelegramMessageID: 10, Text: "old", RawJSON: "{}", Date: now}},
	}}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links, Cursors: cursors,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 3, Workers: 1,
	})

	result := service.SyncListenBacklog(ctx)
	if result.Queued != 1 || result.Skipped != 1 || len(result.Failures) != 0 {
		t.Fatalf("result = %+v, want queued=1 skipped=1 failures=0", result)
	}
	if result.Results[channelID].Messages != 5 {
		t.Fatalf("messages = %d, want 5", result.Results[channelID].Messages)
	}
	latest, err := messages.Latest(ctx, repository.LatestParams{Limit: 10})
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(latest) != 5 {
		t.Fatalf("stored messages = %d, want 5", len(latest))
	}
	for _, msg := range latest {
		if msg.TelegramMessageID <= 10 {
			t.Fatalf("stored message id = %d, want only ids > 10", msg.TelegramMessageID)
		}
	}
	cursor, err := cursors.Find(ctx, accountID, channelID, "history")
	if err != nil {
		t.Fatalf("find cursor: %v", err)
	}
	if cursor.LastMessageID != 15 {
		t.Fatalf("cursor last message id = %d, want 15", cursor.LastMessageID)
	}
}

type fakeTelegramClient struct {
	telegram.NopClient
	batches map[int64][]telegram.Message
}

func (f *fakeTelegramClient) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	return f.batches[offsetID], nil
}

type profileLimitHistoryClient struct {
	telegram.NopClient
	available int
	startID   int64
	fetched   int
}

func (f *profileLimitHistoryClient) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	if f.fetched >= f.available {
		return nil, nil
	}
	remaining := f.available - f.fetched
	if limit > remaining {
		limit = remaining
	}
	out := make([]telegram.Message, 0, limit)
	now := time.Now().UTC()
	for i := 0; i < limit; i++ {
		out = append(out, telegram.Message{
			TelegramMessageID: f.startID - int64(f.fetched+i),
			Text:              "profile message",
			RawJSON:           "{}",
			Date:              now,
		})
	}
	f.fetched += limit
	return out, nil
}

type scriptedHistoryClient struct {
	telegram.NopClient
	batches [][]telegram.Message
	calls   int
	offsets []int64
}

func (f *scriptedHistoryClient) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	f.offsets = append(f.offsets, offsetID)
	if f.calls >= len(f.batches) {
		f.calls++
		return nil, nil
	}
	batch := f.batches[f.calls]
	f.calls++
	return batch, nil
}

func TestSyncChannelRetriesTemporaryFetchError(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)

	now := time.Now().UTC()
	fake := &retryingHistoryClient{
		failuresBeforeSuccess: 1,
		successBatch: []telegram.Message{
			{TelegramMessageID: 7, SenderID: 1, Text: "retry success", RawJSON: "{}", Date: now},
		},
	}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  3,
			Sleep:     func(context.Context, time.Duration) error { return nil },
		},
	})

	result, err := service.SyncChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("SyncChannel returned error: %v", err)
	}
	if result.Messages != 1 {
		t.Fatalf("messages = %d, want 1", result.Messages)
	}
	if fake.calls != 2 {
		t.Fatalf("fetch calls = %d, want 2", fake.calls)
	}
}

func TestSyncChannelStopsAfterFloodWait(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)

	fake := &floodThenEmptyHistoryClient{}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  2,
			Sleep:     func(context.Context, time.Duration) error { return nil },
		},
	})

	_, err := service.SyncChannel(ctx, channelID)
	if !errors.Is(err, ErrFloodWaitDeferred) {
		t.Fatalf("SyncChannel error = %v, want ErrFloodWaitDeferred", err)
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	if account.Status != model.AccountStatusFloodWait {
		t.Fatalf("status = %q, want FLOOD_WAIT", account.Status)
	}
	if fake.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fake.calls)
	}
}

func TestSyncChannelSkipsTelegramWhenAccountNotOnline(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	if err := accounts.UpdateStatus(ctx, accountID, model.AccountStatusFloodWait); err != nil {
		t.Fatalf("update account status: %v", err)
	}
	fake := &retryingHistoryClient{}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  3,
			Sleep:     func(context.Context, time.Duration) error { return nil },
		},
	})

	_, err := service.SyncChannel(ctx, channelID)
	if !errors.Is(err, ErrAccountNotReady) {
		t.Fatalf("SyncChannel error = %v, want ErrAccountNotReady", err)
	}
	if fake.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", fake.calls)
	}
}

func TestSyncChannelMarksAccountLoginRequiredOnAuthFailure(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)

	fake := &authFailureHistoryClient{}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  3,
			Sleep:     func(context.Context, time.Duration) error { return nil },
		},
	})

	_, err := service.SyncChannel(ctx, channelID)
	if err == nil {
		t.Fatal("SyncChannel returned nil error")
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	if account.Status != model.AccountStatusLoginRequired {
		t.Fatalf("status = %q, want LOGIN_REQUIRED", account.Status)
	}
	if fake.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fake.calls)
	}
}

func TestRecoverGapMarksAccountLoginRequiredOnAuthFailure(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)

	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: &authFailureHistoryClient{}, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10,
	})

	_, err := service.RecoverGapWithProgress(ctx, taskpkg.GapRecoveryPayload{
		AccountID:         accountID,
		ChannelID:         channelID,
		FromMessageID:     10,
		ToMessageID:       12,
		TriggerMessageID:  13,
		TelegramChannelID: 200,
	}, nil)
	if err == nil {
		t.Fatal("RecoverGapWithProgress returned nil error")
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	if account.Status != model.AccountStatusLoginRequired {
		t.Fatalf("status = %q, want LOGIN_REQUIRED", account.Status)
	}
}

func TestSyncChannelAppliesWatchRuleAndIgnoresEnabled(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	rules := repository.NewWatchRuleRepository(conn)
	_, err := rules.Create(ctx, model.WatchRule{ChannelID: channelID, Enabled: false, Includes: []string{"庆余年"}, Excludes: []string{"预告"}})
	if err != nil {
		t.Fatalf("create watch rule: %v", err)
	}
	now := time.Now().UTC()
	fake := &fakeTelegramClient{batches: map[int64][]telegram.Message{
		0: {
			{TelegramMessageID: 3, Text: "庆余年 https://pan.quark.cn/s/keep", RawJSON: "{}", Date: now},
			{TelegramMessageID: 2, Text: "庆余年 无链接", RawJSON: "{}", Date: now},
			{TelegramMessageID: 1, Text: "庆余年 预告 https://pan.quark.cn/s/drop", RawJSON: "{}", Date: now},
		},
	}}
	service := NewService(Options{
		DB:               conn,
		Accounts:         accounts,
		Channels:         channels,
		Messages:         messages,
		Links:            links,
		Telegram:         fake,
		Sessions:         session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor:        link.NewExtractor(),
		HistoryBatchSize: 10,
		Filter:           messagefilter.New(rules),
	})
	result, err := service.SyncChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("SyncChannel returned error: %v", err)
	}
	if result.Messages != 3 || result.Links != 1 {
		t.Fatalf("result = %+v, want 3 fetched messages and 1 stored link", result)
	}
	results, err := messages.Search(ctx, repository.SearchParams{Query: "庆余年", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].TelegramMessageID != 3 {
		t.Fatalf("results = %+v, want only message 3", results)
	}
}

func TestHistoryStoreBatchEnqueuesAIMediaMetadataTaskForCloudLinks(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	settings := repository.NewSettingsRepository(conn)
	runtime := config.RuntimeSettingsFromConfig(config.Config{})
	runtime.AI.MediaMetadata = config.AIMediaMetadataSettings{
		Enabled: true,
		BaseURL: "https://api.example.com/v1",
		APIKey:  "secret",
		Model:   "media-model",
	}
	if err := settings.SaveRuntimeSettings(ctx, runtime); err != nil {
		t.Fatalf("save runtime settings: %v", err)
	}
	taskRepo := taskpkg.NewRepository(conn)
	taskService := taskpkg.NewService(taskRepo)
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Extractor:            link.NewExtractor(),
		Settings:             settings,
		AIMediaMetadataTasks: taskService,
	})

	count, err := service.storeBatch(ctx, accountID, channelID, 0, time.Now().UTC(), []model.Message{
		{
			AccountID:         accountID,
			ChannelID:         channelID,
			TelegramMessageID: 100,
			Text:              "迷墙 https://pan.quark.cn/s/a\n另一部 https://www.alipan.com/s/b",
			RawJSON:           "{}",
			Date:              time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("storeBatch: %v", err)
	}
	if count != 2 {
		t.Fatalf("stored link count = %d, want 2", count)
	}
	items, err := taskRepo.List(ctx, taskpkg.ListFilter{Type: model.TaskTypeAIMediaMetadata, Limit: 10})
	if err != nil {
		t.Fatalf("list ai tasks: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ai task count = %d, want 1: %+v", len(items), items)
	}
	var payload taskpkg.AIMediaMetadataPayload
	if err := json.Unmarshal([]byte(items[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode ai task payload: %v", err)
	}
	if payload.MessageID <= 0 {
		t.Fatalf("payload message id = %d, want stored message id", payload.MessageID)
	}
	stored, err := messages.FindByID(ctx, payload.MessageID)
	if err != nil {
		t.Fatalf("find payload message: %v", err)
	}
	if stored.TelegramMessageID != 100 {
		t.Fatalf("payload message telegram id = %d, want 100", stored.TelegramMessageID)
	}
}

func TestSyncChannelKeepsVideoAndAudioMessagesWhenRuleAllowsMedia(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	files := repository.NewFileRepository(conn)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	rules := repository.NewWatchRuleRepository(conn)
	_, err := rules.Create(ctx, model.WatchRule{
		ChannelID:    channelID,
		Enabled:      false,
		MessageTypes: []string{"video", "audio"},
		LinkTypes:    []string{"cloud_drive"},
	})
	if err != nil {
		t.Fatalf("create watch rule: %v", err)
	}
	now := time.Now().UTC()
	fake := &fakeTelegramClient{batches: map[int64][]telegram.Message{
		0: {
			{
				TelegramMessageID: 4,
				MessageType:       "video",
				MediaSummary:      "video/mp4",
				Text:              "clip",
				RawJSON:           "{}",
				Date:              now,
				Files: []model.File{{
					FileName:  "telegram-video-42.mp4",
					Extension: ".mp4",
					MimeType:  "video/mp4",
					SizeBytes: 12345,
					Category:  "video",
				}},
			},
			{
				TelegramMessageID: 5,
				MessageType:       "audio",
				MediaSummary:      "audio/mpeg",
				Text:              "track",
				RawJSON:           "{}",
				Date:              now.Add(-30 * time.Second),
				Files: []model.File{{
					FileName:  "telegram-audio-42.mp3",
					Extension: ".mp3",
					MimeType:  "audio/mpeg",
					SizeBytes: 23456,
					Category:  "audio",
				}},
			},
			{TelegramMessageID: 3, Text: "plain text", RawJSON: "{}", Date: now.Add(-time.Minute)},
		},
	}}
	service := NewService(Options{
		DB:               conn,
		Accounts:         accounts,
		Channels:         channels,
		Messages:         messages,
		Links:            links,
		Files:            files,
		Telegram:         fake,
		Sessions:         session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor:        link.NewExtractor(),
		HistoryBatchSize: 10,
		Filter:           messagefilter.New(rules),
	})

	if _, err := service.SyncChannel(ctx, channelID); err != nil {
		t.Fatalf("SyncChannel returned error: %v", err)
	}
	results, err := messages.Search(ctx, repository.SearchParams{Query: "clip", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].TelegramMessageID != 4 || results[0].MessageType != "video" {
		t.Fatalf("results = %+v, want stored video message 4", results)
	}
	storedFiles, err := files.FindByMessageID(ctx, results[0].ID)
	if err != nil {
		t.Fatalf("find files: %v", err)
	}
	if len(storedFiles) != 1 || storedFiles[0].Category != "video" {
		t.Fatalf("files = %+v, want video metadata", storedFiles)
	}
	results, err = messages.Search(ctx, repository.SearchParams{Query: "track", Limit: 10})
	if err != nil {
		t.Fatalf("search audio: %v", err)
	}
	if len(results) != 1 || results[0].TelegramMessageID != 5 || results[0].MessageType != "audio" {
		t.Fatalf("audio results = %+v, want stored audio message 5", results)
	}
	storedFiles, err = files.FindByMessageID(ctx, results[0].ID)
	if err != nil {
		t.Fatalf("find audio files: %v", err)
	}
	if len(storedFiles) != 1 || storedFiles[0].Category != "audio" {
		t.Fatalf("audio files = %+v, want audio metadata", storedFiles)
	}
}

func TestSyncChannelWithoutWatchRuleKeepsExistingBehavior(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	rules := repository.NewWatchRuleRepository(conn)
	now := time.Now().UTC()
	fake := &fakeTelegramClient{batches: map[int64][]telegram.Message{
		0: {
			{TelegramMessageID: 2, Text: "plain message without link", RawJSON: "{}", Date: now},
			{TelegramMessageID: 1, Text: "linked https://pan.quark.cn/s/abc", RawJSON: "{}", Date: now},
		},
	}}
	service := NewService(Options{
		DB:               conn,
		Accounts:         accounts,
		Channels:         channels,
		Messages:         messages,
		Links:            links,
		Telegram:         fake,
		Sessions:         session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor:        link.NewExtractor(),
		HistoryBatchSize: 10,
		Filter:           messagefilter.New(rules),
	})
	_, err := service.SyncChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("SyncChannel returned error: %v", err)
	}
	latest, err := messages.Latest(ctx, repository.LatestParams{Limit: 10})
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest len = %d, want 2 messages when no watch rule exists", len(latest))
	}
}

func TestHistorySyncTaskProgressUsesProfileLimit(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	channelID, err := channels.Save(ctx, model.Channel{
		AccountID: accountID, TelegramChannelID: 200, AccessHash: 300, Title: "VIP", Type: model.ChannelTypeChannel, HistorySyncEnabled: true,
		SyncProfile: "Quick",
	})
	if err != nil {
		t.Fatalf("save channel: %v", err)
	}
	now := time.Now().UTC()
	fake := &fakeTelegramClient{batches: map[int64][]telegram.Message{
		0: {{TelegramMessageID: 3, Text: "first", RawJSON: "{}", Date: now}},
		3: {{TelegramMessageID: 2, Text: "second", RawJSON: "{}", Date: now}},
		2: {},
	}}
	sink := &recordingProgressSink{}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 1,
	})

	result, err := service.SyncChannelWithProgress(ctx, channelID, "", sink)
	if err != nil {
		t.Fatalf("SyncChannelWithProgress returned error: %v", err)
	}
	if result.Messages != 2 {
		t.Fatalf("messages = %d, want 2", result.Messages)
	}
	if len(sink.updates) != 2 {
		t.Fatalf("progress updates = %+v, want 2 updates", sink.updates)
	}
	if sink.updates[0].progress != 1 || sink.updates[0].total != 100 {
		t.Fatalf("first progress = %+v, want progress=1 total=100", sink.updates[0])
	}
	if sink.updates[1].progress != 2 || sink.updates[1].total != 100 {
		t.Fatalf("second progress = %+v, want progress=2 total=100", sink.updates[1])
	}
}

func TestHistorySyncTaskProgressFullProfileUsesUnknownTotal(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	if err := channels.UpdateControl(ctx, channelID, model.ChannelControl{HistorySyncEnabled: true, SyncProfile: "Full", RemoteSearchAllowed: true}); err != nil {
		t.Fatalf("update controls: %v", err)
	}
	now := time.Now().UTC()
	fake := &fakeTelegramClient{batches: map[int64][]telegram.Message{
		0: {{TelegramMessageID: 3, Text: "first", RawJSON: "{}", Date: now}},
		3: {},
	}}
	sink := &recordingProgressSink{}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 1,
	})

	if _, err := service.SyncChannelWithProgress(ctx, channelID, "", sink); err != nil {
		t.Fatalf("SyncChannelWithProgress returned error: %v", err)
	}
	if len(sink.updates) != 1 || sink.updates[0].total != 0 {
		t.Fatalf("progress updates = %+v, want one unknown-total update", sink.updates)
	}
}

func TestHistorySyncTaskCancelStopsFutureBatches(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	now := time.Now().UTC()
	fake := &fakeTelegramClient{batches: map[int64][]telegram.Message{
		0: {{TelegramMessageID: 3, Text: "first", RawJSON: "{}", Date: now}},
		3: {{TelegramMessageID: 2, Text: "second", RawJSON: "{}", Date: now}},
	}}
	sink := &recordingProgressSink{statusAfterUpdates: model.TaskStatusCanceling}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 1,
	})

	result, err := service.SyncChannelWithProgress(ctx, channelID, "", sink)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if result.Messages != 1 {
		t.Fatalf("messages = %d, want first batch only", result.Messages)
	}
	latest, err := messages.Latest(ctx, repository.LatestParams{Limit: 10})
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(latest) != 1 || latest[0].TelegramMessageID != 3 {
		t.Fatalf("stored messages = %+v, want only first batch", latest)
	}
}

func TestHistorySyncTaskFloodWaitNotifiesSink(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	fake := &floodThenEmptyHistoryClient{}
	sink := &recordingProgressSink{}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  2,
			Sleep:     func(context.Context, time.Duration) error { return nil },
		},
	})

	before := time.Now().UTC()
	_, err := service.SyncChannelWithProgress(ctx, channelID, "", sink)
	if !errors.Is(err, ErrFloodWaitDeferred) {
		t.Fatalf("SyncChannelWithProgress error = %v, want ErrFloodWaitDeferred", err)
	}
	if len(sink.floodWaits) != 1 {
		t.Fatalf("flood waits = %+v, want one", sink.floodWaits)
	}
	if sink.floodWaits[0].nextRunAt.Before(before) || sink.floodWaits[0].message == "" {
		t.Fatalf("flood wait = %+v, want future next run and message", sink.floodWaits[0])
	}
	if fake.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fake.calls)
	}
}

func TestHistorySyncTaskWorkerProcessesQueuedHistorySync(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	now := time.Now().UTC()
	fake := &fakeTelegramClient{batches: map[int64][]telegram.Message{
		0: {
			{TelegramMessageID: 3, Text: "first https://pan.quark.cn/s/task", RawJSON: "{}", Date: now},
			{TelegramMessageID: 2, Text: "second", RawJSON: "{}", Date: now},
		},
		2: {},
	}}
	historyService := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 2,
	})
	taskRepo := taskpkg.NewRepository(conn)
	taskService := taskpkg.NewService(taskRepo)
	queued, err := taskService.Enqueue(ctx, model.TaskTypeHistorySync, taskpkg.HistorySyncPayload{ChannelIDs: []int64{channelID}})
	if err != nil {
		t.Fatalf("enqueue history sync task: %v", err)
	}
	worker := taskpkg.NewWorker(taskpkg.WorkerOptions{
		Service:    taskService,
		Repository: taskRepo,
		Handlers: map[string]taskpkg.Handler{
			model.TaskTypeHistorySync: historyService.RunHistorySyncTask,
		},
	})

	processed, err := worker.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce returned error: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	finished, err := taskRepo.FindByID(ctx, queued.ID)
	if err != nil {
		t.Fatalf("find finished task: %v", err)
	}
	if finished.Status != model.TaskStatusSucceeded || finished.Progress != 1 || finished.Total != 1 {
		t.Fatalf("finished task = %+v, want succeeded progress 1/1", finished)
	}
	latest, err := messages.Latest(ctx, repository.LatestParams{Limit: 10})
	if err != nil {
		t.Fatalf("latest messages: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("stored messages = %+v, want 2 history messages", latest)
	}
}

func TestHistorySyncTaskWorkerLeavesFloodWaitTaskDeferred(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	fake := &floodThenEmptyHistoryClient{}
	historyService := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  2,
			Sleep:     func(context.Context, time.Duration) error { return nil },
		},
	})
	taskRepo := taskpkg.NewRepository(conn)
	taskService := taskpkg.NewService(taskRepo)
	queued, err := taskService.Enqueue(ctx, model.TaskTypeHistorySync, taskpkg.HistorySyncPayload{ChannelIDs: []int64{channelID}})
	if err != nil {
		t.Fatalf("enqueue history sync task: %v", err)
	}
	worker := taskpkg.NewWorker(taskpkg.WorkerOptions{
		Service:    taskService,
		Repository: taskRepo,
		Handlers: map[string]taskpkg.Handler{
			model.TaskTypeHistorySync: historyService.RunHistorySyncTask,
		},
	})

	processed, err := worker.ProcessOnce(ctx)
	if !errors.Is(err, ErrFloodWaitDeferred) {
		t.Fatalf("ProcessOnce error = %v, want ErrFloodWaitDeferred", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	flooded, err := taskRepo.FindByID(ctx, queued.ID)
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	if flooded.Status != model.TaskStatusFloodWait || flooded.NextRunAt == nil {
		t.Fatalf("task = %+v, want flood_wait with next_run_at", flooded)
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	if account.Status != model.AccountStatusFloodWait {
		t.Fatalf("account status = %q, want FLOOD_WAIT", account.Status)
	}
	if fake.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fake.calls)
	}
}

func TestHistorySyncTaskWorkerDefersWhenAccountAlreadyFloodWait(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	if err := accounts.UpdateStatus(ctx, accountID, model.AccountStatusFloodWait); err != nil {
		t.Fatalf("update account status: %v", err)
	}
	fake := &retryingHistoryClient{}
	historyService := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  2,
			Sleep:     func(context.Context, time.Duration) error { return nil },
		},
	})
	taskRepo := taskpkg.NewRepository(conn)
	taskService := taskpkg.NewService(taskRepo)
	queued, err := taskService.Enqueue(ctx, model.TaskTypeHistorySync, taskpkg.HistorySyncPayload{ChannelIDs: []int64{channelID}})
	if err != nil {
		t.Fatalf("enqueue history sync task: %v", err)
	}
	worker := taskpkg.NewWorker(taskpkg.WorkerOptions{
		Service:    taskService,
		Repository: taskRepo,
		Handlers: map[string]taskpkg.Handler{
			model.TaskTypeHistorySync: historyService.RunHistorySyncTask,
		},
	})

	processed, err := worker.ProcessOnce(ctx)
	if !errors.Is(err, ErrFloodWaitDeferred) {
		t.Fatalf("ProcessOnce error = %v, want ErrFloodWaitDeferred", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	flooded, err := taskRepo.FindByID(ctx, queued.ID)
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	if flooded.Status != model.TaskStatusFloodWait || flooded.NextRunAt == nil {
		t.Fatalf("task = %+v, want flood_wait with next_run_at", flooded)
	}
	if fake.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", fake.calls)
	}
}

func TestGapRecoveryTaskWorkerProcessesQueuedGap(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	cursors := repository.NewSyncCursorRepository(conn)
	if err := cursors.Save(ctx, model.SyncCursor{
		AccountID: accountID, ChannelID: channelID, CursorType: "history", LastMessageID: 10, Date: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save cursor: %v", err)
	}
	now := time.Now().UTC()
	fake := &fakeTelegramClient{batches: map[int64][]telegram.Message{
		15: {
			{TelegramMessageID: 14, Text: "gap 14 https://pan.quark.cn/s/gap14", RawJSON: "{}", Date: now},
			{TelegramMessageID: 13, Text: "gap 13", RawJSON: "{}", Date: now},
		},
		13: {
			{TelegramMessageID: 12, Text: "gap 12", RawJSON: "{}", Date: now},
			{TelegramMessageID: 11, Text: "gap 11", RawJSON: "{}", Date: now},
		},
		11: {
			{TelegramMessageID: 10, Text: "old", RawJSON: "{}", Date: now},
		},
	}}
	historyService := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links, Cursors: cursors,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 2,
	})
	taskRepo := taskpkg.NewRepository(conn)
	taskService := taskpkg.NewService(taskRepo)
	queued, err := taskService.Enqueue(ctx, model.TaskTypeGapRecovery, taskpkg.GapRecoveryPayload{
		AccountID:         accountID,
		ChannelID:         channelID,
		FromMessageID:     11,
		ToMessageID:       14,
		TriggerMessageID:  15,
		TelegramChannelID: 200,
	})
	if err != nil {
		t.Fatalf("enqueue gap recovery task: %v", err)
	}
	worker := taskpkg.NewWorker(taskpkg.WorkerOptions{
		Service:    taskService,
		Repository: taskRepo,
		Handlers: map[string]taskpkg.Handler{
			model.TaskTypeGapRecovery: historyService.RunGapRecoveryTask,
		},
	})

	processed, err := worker.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce returned error: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	finished, err := taskRepo.FindByID(ctx, queued.ID)
	if err != nil {
		t.Fatalf("find finished task: %v", err)
	}
	if finished.Status != model.TaskStatusSucceeded || finished.Progress != 4 || finished.Total != 4 {
		t.Fatalf("finished task = %+v, want succeeded progress 4/4", finished)
	}
	latest, err := messages.Latest(ctx, repository.LatestParams{Limit: 10})
	if err != nil {
		t.Fatalf("latest messages: %v", err)
	}
	if len(latest) != 4 {
		t.Fatalf("stored messages = %+v, want 4 recovered messages", latest)
	}
	cursor, err := cursors.Find(ctx, accountID, channelID, "history")
	if err != nil {
		t.Fatalf("find cursor: %v", err)
	}
	if cursor.LastMessageID != 15 {
		t.Fatalf("cursor last message id = %d, want trigger message 15", cursor.LastMessageID)
	}
}

// blockingHistoryClient blocks every FetchHistory call until ctx is canceled,
// simulating a stuck gotd RPC retry loop that would otherwise hang the worker.
type stuckHistoryClient struct {
	telegram.NopClient
	called chan struct{}
}

func (f *stuckHistoryClient) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	select {
	case f.called <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestRunGapRecoveryTaskBoundsStuckRun verifies that a hung gap-recovery run is
// aborted by its per-run deadline rather than monopolizing the worker. The run
// inherits the caller's (shorter) context deadline, so the blocking fetch
// returns promptly with a context error.
func TestRunGapRecoveryTaskBoundsStuckRun(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	cursors := repository.NewSyncCursorRepository(conn)
	if err := cursors.Save(ctx, model.SyncCursor{
		AccountID: accountID, ChannelID: channelID, CursorType: "history", LastMessageID: 10, Date: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save cursor: %v", err)
	}
	stuck := &stuckHistoryClient{called: make(chan struct{}, 1)}
	historyService := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links, Cursors: cursors,
		Telegram:  stuck,
		Sessions:  session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(),
	})
	payload, err := json.Marshal(taskpkg.GapRecoveryPayload{
		AccountID:         accountID,
		ChannelID:         channelID,
		FromMessageID:     11,
		ToMessageID:       14,
		TriggerMessageID:  15,
		TelegramChannelID: 200,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	// Short caller deadline; the 5m run timeout inherits this earlier deadline.
	runCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = historyService.RunGapRecoveryTask(runCtx, model.Task{Type: model.TaskTypeGapRecovery, PayloadJSON: string(payload)}, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected stuck run to return a deadline error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("run was not bounded by the deadline, took %v", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

type progressUpdate struct {
	progress int64
	total    int64
	message  string
}

type floodWaitUpdate struct {
	nextRunAt time.Time
	message   string
}

type recordingProgressSink struct {
	updates            []progressUpdate
	statusAfterUpdates string
	floodWaits         []floodWaitUpdate
}

var _ taskpkg.ProgressSink = (*recordingProgressSink)(nil)

func (s *recordingProgressSink) Progress(ctx context.Context, progress int64, total int64, message string) error {
	s.updates = append(s.updates, progressUpdate{progress: progress, total: total, message: message})
	return nil
}

func (s *recordingProgressSink) Status(ctx context.Context) (string, error) {
	if s.statusAfterUpdates != "" && len(s.updates) > 0 {
		return s.statusAfterUpdates, nil
	}
	return model.TaskStatusRunning, nil
}

func (s *recordingProgressSink) FloodWait(ctx context.Context, nextRunAt time.Time, message string) error {
	s.floodWaits = append(s.floodWaits, floodWaitUpdate{nextRunAt: nextRunAt, message: message})
	return nil
}

func setupHistoryTestStore(t *testing.T) (*sql.DB, *repository.AccountRepository, *repository.ChannelRepository, *repository.MessageRepository, *repository.LinkRepository) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(context.Background(), conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return conn, repository.NewAccountRepository(conn), repository.NewChannelRepository(conn), repository.NewMessageRepository(conn), repository.NewLinkRepository(conn)
}

func seedHistoryAccountAndChannel(t *testing.T, ctx context.Context, accounts *repository.AccountRepository, channels *repository.ChannelRepository) (int64, int64) {
	t.Helper()
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	channelID, err := channels.Save(ctx, model.Channel{
		AccountID: accountID, TelegramChannelID: 200, AccessHash: 300, Title: "VIP", Type: model.ChannelTypeChannel, HistorySyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("save channel: %v", err)
	}
	return accountID, channelID
}

type retryingHistoryClient struct {
	telegram.NopClient
	calls                 int
	failuresBeforeSuccess int
	successBatch          []telegram.Message
}

func (f *retryingHistoryClient) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	f.calls++
	if f.calls <= f.failuresBeforeSuccess {
		return nil, retry.Temporary(errors.New("temporary history failure"))
	}
	if offsetID > 0 {
		return nil, nil
	}
	return f.successBatch, nil
}

type floodThenEmptyHistoryClient struct {
	telegram.NopClient
	calls int
}

func (f *floodThenEmptyHistoryClient) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	f.calls++
	if f.calls == 1 {
		return nil, retry.FloodWait(60, errors.New("FLOOD_WAIT_60"))
	}
	return nil, nil
}

type authFailureHistoryClient struct {
	telegram.NopClient
	calls int
}

func (f *authFailureHistoryClient) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	f.calls++
	return nil, errors.New("callback: rpcDoRequest: rpc error code 401: AUTH_KEY_UNREGISTERED")
}

func TestSyncManyDeduplicatesChannelIDsAndRespectsWorkerLimit(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channel1 := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	channel2, err := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 201, AccessHash: 301, Title: "VIP 2", Type: model.ChannelTypeChannel, HistorySyncEnabled: true})
	if err != nil {
		t.Fatalf("save channel2: %v", err)
	}
	channel3, err := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 202, AccessHash: 302, Title: "VIP 3", Type: model.ChannelTypeChannel, HistorySyncEnabled: true})
	if err != nil {
		t.Fatalf("save channel3: %v", err)
	}
	fake := &concurrentHistoryClient{delay: 5 * time.Millisecond}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10, Workers: 2,
		RetryPolicy: retry.Policy{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxTries: 1, Sleep: func(context.Context, time.Duration) error { return nil }},
	})

	result := service.SyncMany(ctx, []int64{channel1, channel1, channel2, channel3})
	if result.Queued != 3 {
		t.Fatalf("queued = %d, want 3 unique channels", result.Queued)
	}
	if result.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1 duplicate", result.Skipped)
	}
	if len(result.Failures) != 0 {
		t.Fatalf("failures = %+v, want none", result.Failures)
	}
	if fake.maxActive > 2 {
		t.Fatalf("max active = %d, want <= 2", fake.maxActive)
	}
}

func TestSyncManySerializesFetchHistoryPerAccountWithGovernor(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	accountID, channel1 := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	channel2, err := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 201, AccessHash: 301, Title: "VIP 2", Type: model.ChannelTypeChannel, HistorySyncEnabled: true})
	if err != nil {
		t.Fatalf("save channel2: %v", err)
	}
	channel3, err := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 202, AccessHash: 302, Title: "VIP 3", Type: model.ChannelTypeChannel, HistorySyncEnabled: true})
	if err != nil {
		t.Fatalf("save channel3: %v", err)
	}
	fake := &concurrentHistoryClient{delay: 5 * time.Millisecond}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10, Workers: 3,
		RetryPolicy:     retry.Policy{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxTries: 1, Sleep: func(context.Context, time.Duration) error { return nil }},
		RequestGovernor: telegramguard.New(telegramguard.Options{}),
	})

	result := service.SyncMany(ctx, []int64{channel1, channel2, channel3})
	if result.Queued != 3 || len(result.Failures) != 0 {
		t.Fatalf("result = %+v, want 3 queued and no failures", result)
	}
	if fake.maxActive != 1 {
		t.Fatalf("max active = %d, want 1", fake.maxActive)
	}
}

func TestSyncManyIgnoresHistorySyncDisabledFlag(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	if err := channels.UpdateControl(ctx, channelID, model.ChannelControl{
		HistorySyncEnabled:  false,
		SyncProfile:         "Normal",
		ListenEnabled:       false,
		RemoteSearchAllowed: true,
	}); err != nil {
		t.Fatalf("disable history sync: %v", err)
	}
	fake := &profileLimitHistoryClient{available: 1, startID: 50000}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10, Workers: 1,
	})

	result := service.SyncMany(ctx, []int64{channelID})
	if result.Queued != 1 || result.Skipped != 0 || fake.fetched != 1 {
		t.Fatalf("result = %+v fetched=%d, want manual sync to queue and fetch history", result, fake.fetched)
	}
	if len(result.Failures) != 0 || len(result.Results) != 1 {
		t.Fatalf("result = %+v, want one stored result and no failures", result)
	}
}

func TestSyncManySkipsChannelAlreadySyncing(t *testing.T) {
	ctx := context.Background()
	conn, accounts, channels, messages, links := setupHistoryTestStore(t)
	_, channelID := seedHistoryAccountAndChannel(t, ctx, accounts, channels)
	fake := &blockingHistoryClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	service := NewService(Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Telegram: fake, Sessions: session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10, Workers: 1,
		RetryPolicy: retry.Policy{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxTries: 1, Sleep: func(context.Context, time.Duration) error { return nil }},
	})

	done := make(chan error, 1)
	go func() {
		_, err := service.SyncChannel(ctx, channelID)
		done <- err
	}()
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first sync to start")
	}

	result := service.SyncMany(ctx, []int64{channelID})
	if result.Queued != 0 || result.Skipped != 1 {
		t.Fatalf("result = %+v, want queued=0 skipped=1", result)
	}

	close(fake.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first SyncChannel returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first sync")
	}
}

type concurrentHistoryClient struct {
	telegram.NopClient
	mu        sync.Mutex
	active    int
	maxActive int
	delay     time.Duration
}

func (f *concurrentHistoryClient) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.mu.Unlock()
	time.Sleep(f.delay)
	f.mu.Lock()
	f.active--
	f.mu.Unlock()
	return nil, nil
}

type blockingHistoryClient struct {
	telegram.NopClient
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (f *blockingHistoryClient) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	f.once.Do(func() { close(f.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.release:
		return nil, nil
	}
}
