package update

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-search/internal/config"
	"tg-search/internal/db"
	"tg-search/internal/link"
	"tg-search/internal/messagefilter"
	"tg-search/internal/model"
	"tg-search/internal/repository"
	"tg-search/internal/resource"
	taskpkg "tg-search/internal/task"
)

func TestProcessorHandlesNewEditAndDeleteEvents(t *testing.T) {
	ctx := context.Background()
	fixture := newProcessorFixture(t)
	processor := NewProcessor(ProcessorOptions{
		DB:        fixture.conn,
		Channels:  fixture.channels,
		Messages:  fixture.messages,
		Links:     fixture.links,
		Extractor: link.NewExtractor(),
	})

	now := time.Now().UTC().Truncate(time.Second)
	newEvent := Event{
		Type:              EventNewMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         10,
		SenderID:          88,
		Text:              "庆余年 https://pan.quark.cn/s/old123 提取码: abcd",
		RawJSON:           "{}",
		Date:              now,
	}
	if err := processor.Process(ctx, newEvent); err != nil {
		t.Fatalf("process new event: %v", err)
	}

	results, err := fixture.messages.Search(ctx, repository.SearchParams{Query: "庆余年", Limit: 10})
	if err != nil {
		t.Fatalf("search after new event: %v", err)
	}
	if len(results) != 1 || len(results[0].Links) != 1 {
		t.Fatalf("new event search results = %+v", results)
	}
	if results[0].Links[0].Type != "quark" || results[0].Links[0].URL != "https://pan.quark.cn/s/old123" || results[0].Links[0].Password != "abcd" {
		t.Fatalf("new event link = %+v", results[0].Links[0])
	}

	editTime := now.Add(time.Minute)
	editEvent := Event{
		Type:              EventEditMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         10,
		SenderID:          88,
		Text:              "三体 https://pan.baidu.com/s/new123?pwd=wxyz",
		RawJSON:           `{"edited":true}`,
		Date:              now,
		EditDate:          &editTime,
	}
	if err := processor.Process(ctx, editEvent); err != nil {
		t.Fatalf("process edit event: %v", err)
	}

	results, err = fixture.messages.Search(ctx, repository.SearchParams{Query: "三体", Limit: 10})
	if err != nil {
		t.Fatalf("search after edit event: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("edited search len = %d, want 1", len(results))
	}
	if results[0].Text != "三体 https://pan.baidu.com/s/new123?pwd=wxyz" {
		t.Fatalf("edited text = %q", results[0].Text)
	}
	if results[0].EditDate == nil || !results[0].EditDate.Equal(editTime) {
		t.Fatalf("edit date = %v, want %v", results[0].EditDate, editTime)
	}
	if len(results[0].Links) != 1 || results[0].Links[0].Type != "baidu" || results[0].Links[0].URL != "https://pan.baidu.com/s/new123?pwd=wxyz" || results[0].Links[0].Password != "wxyz" {
		t.Fatalf("edited links = %+v", results[0].Links)
	}

	oldLinks, err := fixture.links.Search(ctx, repository.LinkSearchParams{Keyword: "庆余年", Limit: 10})
	if err != nil {
		t.Fatalf("search old links: %v", err)
	}
	if len(oldLinks) != 0 {
		t.Fatalf("old links len = %d, want 0", len(oldLinks))
	}

	deleteEvent := Event{
		Type:              EventDeleteMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         10,
	}
	if err := processor.Process(ctx, deleteEvent); err != nil {
		t.Fatalf("process delete event: %v", err)
	}

	results, err = fixture.messages.Search(ctx, repository.SearchParams{Query: "三体", Limit: 10})
	if err != nil {
		t.Fatalf("search after delete event: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("deleted search len = %d, want 0", len(results))
	}
}

func TestProcessorFiltersRealtimeMessagesByEnabledWatchRule(t *testing.T) {
	ctx := context.Background()
	fixture := newProcessorFixture(t)
	rules := repository.NewWatchRuleRepository(fixture.conn)
	_, err := rules.Create(ctx, model.WatchRule{
		ChannelID: fixture.channelID,
		Enabled:   true,
		Includes:  []string{"庆余年"},
		Excludes:  []string{"预告"},
	})
	if err != nil {
		t.Fatalf("create watch rule: %v", err)
	}
	processor := NewProcessor(ProcessorOptions{
		DB:        fixture.conn,
		Channels:  fixture.channels,
		Messages:  fixture.messages,
		Links:     fixture.links,
		Extractor: link.NewExtractor(),
		Filter:    messagefilter.New(rules),
	})
	now := time.Now().UTC()
	for _, event := range []Event{
		{Type: EventNewMessage, AccountID: fixture.accountID, TelegramChannelID: fixture.telegramChannelID, MessageID: 11, Text: "庆余年 https://pan.quark.cn/s/abc", RawJSON: "{}", Date: now},
		{Type: EventNewMessage, AccountID: fixture.accountID, TelegramChannelID: fixture.telegramChannelID, MessageID: 12, Text: "庆余年 无链接", RawJSON: "{}", Date: now},
		{Type: EventNewMessage, AccountID: fixture.accountID, TelegramChannelID: fixture.telegramChannelID, MessageID: 13, Text: "三体 https://pan.quark.cn/s/def", RawJSON: "{}", Date: now},
		{Type: EventNewMessage, AccountID: fixture.accountID, TelegramChannelID: fixture.telegramChannelID, MessageID: 14, Text: "庆余年 预告 https://pan.quark.cn/s/ghi", RawJSON: "{}", Date: now},
	} {
		if err := processor.Process(ctx, event); err != nil {
			t.Fatalf("process event %d: %v", event.MessageID, err)
		}
	}
	results, err := fixture.messages.Search(ctx, repository.SearchParams{Query: "庆余年", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].TelegramMessageID != 11 || len(results[0].Links) != 1 {
		t.Fatalf("results = %+v, want only message 11 with link", results)
	}
}

func TestProcessorEnqueuesAIMediaMetadataTaskForCloudLinks(t *testing.T) {
	ctx := context.Background()
	fixture := newProcessorFixture(t)
	settings := repository.NewSettingsRepository(fixture.conn)
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
	taskRepo := taskpkg.NewRepository(fixture.conn)
	taskService := taskpkg.NewService(taskRepo)
	processor := NewProcessor(ProcessorOptions{
		DB:                   fixture.conn,
		Channels:             fixture.channels,
		Messages:             fixture.messages,
		Links:                fixture.links,
		Extractor:            link.NewExtractor(),
		Settings:             settings,
		AIMediaMetadataTasks: taskService,
	})

	event := Event{
		Type:              EventNewMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         22,
		Text:              "迷墙 https://pan.quark.cn/s/a\n另一部 https://www.alipan.com/s/b",
		RawJSON:           "{}",
		Date:              time.Now().UTC(),
	}
	if err := processor.Process(ctx, event); err != nil {
		t.Fatalf("process event: %v", err)
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
	stored, err := fixture.messages.FindByID(ctx, payload.MessageID)
	if err != nil {
		t.Fatalf("find payload message: %v", err)
	}
	if stored.TelegramMessageID != 22 {
		t.Fatalf("payload message telegram id = %d, want 22", stored.TelegramMessageID)
	}
}

func TestProcessorKeepsRealtimeImageAndAudioMessagesWhenRuleAllowsMedia(t *testing.T) {
	ctx := context.Background()
	fixture := newProcessorFixture(t)
	rules := repository.NewWatchRuleRepository(fixture.conn)
	_, err := rules.Create(ctx, model.WatchRule{
		ChannelID:    fixture.channelID,
		Enabled:      true,
		MessageTypes: []string{"image", "audio"},
		LinkTypes:    []string{"cloud_drive"},
	})
	if err != nil {
		t.Fatalf("create watch rule: %v", err)
	}
	processor := NewProcessor(ProcessorOptions{
		DB:        fixture.conn,
		Channels:  fixture.channels,
		Messages:  fixture.messages,
		Links:     fixture.links,
		Files:     fixture.files,
		Extractor: link.NewExtractor(),
		Filter:    messagefilter.New(rules),
	})

	if err := processor.Process(ctx, Event{
		Type:              EventNewMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         15,
		MessageType:       "photo",
		MediaSummary:      "photo",
		Text:              "cover",
		RawJSON:           "{}",
		Date:              time.Now().UTC(),
		Files: []model.File{{
			FileName:  "telegram-photo-42.jpg",
			Extension: ".jpg",
			MimeType:  "image/jpeg",
			Category:  "image",
		}},
	}); err != nil {
		t.Fatalf("process image event: %v", err)
	}
	if err := processor.Process(ctx, Event{
		Type:              EventNewMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         16,
		MessageType:       "audio",
		MediaSummary:      "audio/mpeg",
		Text:              "track",
		RawJSON:           "{}",
		Date:              time.Now().UTC(),
		Files: []model.File{{
			FileName:  "telegram-audio-42.mp3",
			Extension: ".mp3",
			MimeType:  "audio/mpeg",
			Category:  "audio",
		}},
	}); err != nil {
		t.Fatalf("process audio event: %v", err)
	}

	results, err := fixture.messages.Search(ctx, repository.SearchParams{Query: "cover", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].TelegramMessageID != 15 || results[0].MessageType != "photo" {
		t.Fatalf("results = %+v, want stored photo message 15", results)
	}
	files, err := fixture.files.FindByMessageID(ctx, results[0].ID)
	if err != nil {
		t.Fatalf("find files: %v", err)
	}
	if len(files) != 1 || files[0].Category != "image" {
		t.Fatalf("files = %+v, want image metadata", files)
	}
	results, err = fixture.messages.Search(ctx, repository.SearchParams{Query: "track", Limit: 10})
	if err != nil {
		t.Fatalf("search audio: %v", err)
	}
	if len(results) != 1 || results[0].TelegramMessageID != 16 || results[0].MessageType != "audio" {
		t.Fatalf("audio results = %+v, want stored audio message 16", results)
	}
	files, err = fixture.files.FindByMessageID(ctx, results[0].ID)
	if err != nil {
		t.Fatalf("find audio files: %v", err)
	}
	if len(files) != 1 || files[0].Category != "audio" {
		t.Fatalf("audio files = %+v, want audio metadata", files)
	}
}

func TestProcessorSkipsRealtimeMessagesWithoutEnabledWatchRule(t *testing.T) {
	ctx := context.Background()
	fixture := newProcessorFixture(t)
	rules := repository.NewWatchRuleRepository(fixture.conn)
	processor := NewProcessor(ProcessorOptions{
		DB:        fixture.conn,
		Channels:  fixture.channels,
		Messages:  fixture.messages,
		Links:     fixture.links,
		Extractor: link.NewExtractor(),
		Filter:    messagefilter.New(rules),
	})
	err := processor.Process(ctx, Event{
		Type:              EventNewMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         20,
		Text:              "庆余年 https://pan.quark.cn/s/abc",
		RawJSON:           "{}",
		Date:              time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	latest, err := fixture.messages.Latest(ctx, repository.LatestParams{Limit: 10})
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(latest) != 0 {
		t.Fatalf("latest = %+v, want no stored realtime messages without enabled rule", latest)
	}
}

func TestProcessorSkipsRealtimeMessagesWhenChannelListenDisabled(t *testing.T) {
	ctx := context.Background()
	fixture := newProcessorFixture(t)
	if err := fixture.channels.UpdateControl(ctx, fixture.channelID, model.ChannelControl{
		HistorySyncEnabled:  false,
		SyncProfile:         "Normal",
		ListenEnabled:       false,
		RemoteSearchAllowed: true,
	}); err != nil {
		t.Fatalf("disable channel listener: %v", err)
	}
	processor := NewProcessor(ProcessorOptions{
		DB:        fixture.conn,
		Channels:  fixture.channels,
		Messages:  fixture.messages,
		Links:     fixture.links,
		Extractor: link.NewExtractor(),
	})

	err := processor.Process(ctx, Event{
		Type:              EventNewMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         21,
		Text:              "庆余年 https://pan.quark.cn/s/abc",
		RawJSON:           "{}",
		Date:              time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	latest, err := fixture.messages.Latest(ctx, repository.LatestParams{Limit: 10})
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(latest) != 0 {
		t.Fatalf("latest = %+v, want no stored realtime messages when listener is disabled", latest)
	}
}

func TestProcessorDeletesStoredMessageWhenRealtimeEditStopsMatching(t *testing.T) {
	ctx := context.Background()
	fixture := newProcessorFixture(t)
	rules := repository.NewWatchRuleRepository(fixture.conn)
	_, err := rules.Create(ctx, model.WatchRule{ChannelID: fixture.channelID, Enabled: true, Includes: []string{"庆余年"}})
	if err != nil {
		t.Fatalf("create watch rule: %v", err)
	}
	processor := NewProcessor(ProcessorOptions{
		DB:        fixture.conn,
		Channels:  fixture.channels,
		Messages:  fixture.messages,
		Links:     fixture.links,
		Extractor: link.NewExtractor(),
		Filter:    messagefilter.New(rules),
	})
	now := time.Now().UTC()
	if err := processor.Process(ctx, Event{Type: EventNewMessage, AccountID: fixture.accountID, TelegramChannelID: fixture.telegramChannelID, MessageID: 30, Text: "庆余年 https://pan.quark.cn/s/abc", RawJSON: "{}", Date: now}); err != nil {
		t.Fatalf("process new: %v", err)
	}
	if err := processor.Process(ctx, Event{Type: EventEditMessage, AccountID: fixture.accountID, TelegramChannelID: fixture.telegramChannelID, MessageID: 30, Text: "三体 https://pan.quark.cn/s/abc", RawJSON: "{}", Date: now}); err != nil {
		t.Fatalf("process edit: %v", err)
	}
	results, err := fixture.messages.Search(ctx, repository.SearchParams{Query: "庆余年", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("search after non-matching edit = %+v, want empty", results)
	}
}

func TestProcessorEnqueuesGapRecoveryTask(t *testing.T) {
	ctx := context.Background()
	fixture := newProcessorFixture(t)
	cursors := repository.NewSyncCursorRepository(fixture.conn)
	taskRepo := taskpkg.NewRepository(fixture.conn)
	tasks := taskpkg.NewService(taskRepo)
	if err := cursors.Save(ctx, model.SyncCursor{
		AccountID: fixture.accountID, ChannelID: fixture.channelID, CursorType: "history", LastMessageID: 10, Date: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save cursor: %v", err)
	}
	processor := NewProcessor(ProcessorOptions{
		DB:        fixture.conn,
		Channels:  fixture.channels,
		Messages:  fixture.messages,
		Links:     fixture.links,
		Extractor: link.NewExtractor(),
		Cursors:   cursors,
		Tasks:     tasks,
	})

	err := processor.Process(ctx, Event{
		Type:              EventNewMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         15,
		Text:              "gap https://pan.quark.cn/s/abc",
		RawJSON:           "{}",
		Date:              time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	items, err := taskRepo.List(ctx, taskpkg.ListFilter{Type: model.TaskTypeGapRecovery, Limit: 10})
	if err != nil {
		t.Fatalf("list gap recovery tasks: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("gap recovery tasks = %+v, want one", items)
	}
	if !strings.Contains(items[0].PayloadJSON, `"from_message_id":11`) || !strings.Contains(items[0].PayloadJSON, `"to_message_id":14`) {
		t.Fatalf("payload = %s, want missing range 11..14", items[0].PayloadJSON)
	}
	cursor, err := cursors.Find(ctx, fixture.accountID, fixture.channelID, "history")
	if err != nil {
		t.Fatalf("find cursor: %v", err)
	}
	if cursor.LastMessageID != 10 {
		t.Fatalf("cursor last message id = %d, want to stay 10 until gap is recovered", cursor.LastMessageID)
	}
}

func TestProcessorAdvancesHistoryCursorAfterNewMessage(t *testing.T) {
	ctx := context.Background()
	fixture := newProcessorFixture(t)
	cursors := repository.NewSyncCursorRepository(fixture.conn)
	processor := NewProcessor(ProcessorOptions{
		DB:        fixture.conn,
		Channels:  fixture.channels,
		Messages:  fixture.messages,
		Links:     fixture.links,
		Extractor: link.NewExtractor(),
		Cursors:   cursors,
	})

	if err := processor.Process(ctx, Event{
		Type:              EventNewMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         15,
		Text:              "cursor https://pan.quark.cn/s/abc",
		RawJSON:           "{}",
		Date:              time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	cursor, err := cursors.Find(ctx, fixture.accountID, fixture.channelID, "history")
	if err != nil {
		t.Fatalf("find cursor: %v", err)
	}
	if cursor.LastMessageID != 15 {
		t.Fatalf("cursor last message id = %d, want 15", cursor.LastMessageID)
	}

	if err := processor.Process(ctx, Event{
		Type:              EventNewMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         12,
		Text:              "older https://pan.quark.cn/s/older",
		RawJSON:           "{}",
		Date:              time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Process older returned error: %v", err)
	}
	cursor, err = cursors.Find(ctx, fixture.accountID, fixture.channelID, "history")
	if err != nil {
		t.Fatalf("find cursor after older event: %v", err)
	}
	if cursor.LastMessageID != 15 {
		t.Fatalf("cursor last message id = %d, want to stay 15", cursor.LastMessageID)
	}
}

func TestProcessorRefreshesResourceStatsAfterNewMessage(t *testing.T) {
	ctx := context.Background()
	fixture := newProcessorFixture(t)
	stats := repository.NewResourceStatsRepository(fixture.conn)
	resourceIndex := repository.NewResourceIndexRepository(fixture.conn)
	resources := resource.NewService(fixture.links, fixture.files, stats, resourceIndex)
	processor := NewProcessor(ProcessorOptions{
		DB:        fixture.conn,
		Channels:  fixture.channels,
		Messages:  fixture.messages,
		Links:     fixture.links,
		Files:     fixture.files,
		Resources: resources,
		Extractor: link.NewExtractor(),
	})

	if err := processor.Process(ctx, Event{
		Type:              EventNewMessage,
		AccountID:         fixture.accountID,
		TelegramChannelID: fixture.telegramChannelID,
		MessageID:         30,
		Text:              "资源 https://pan.quark.cn/s/abc",
		RawJSON:           "{}",
		Date:              time.Now().UTC(),
		Files: []model.File{{
			FileName:  "ubuntu.iso",
			Extension: ".iso",
			MimeType:  "application/x-iso9660-image",
			SizeBytes: 5000,
		}},
	}); err != nil {
		t.Fatalf("process new event: %v", err)
	}

	grouped, err := resources.GlobalGrouped(ctx)
	if err != nil {
		t.Fatalf("get grouped stats: %v", err)
	}
	if grouped["_total"] != 2 {
		t.Fatalf("grouped stats = %+v, want _total=2", grouped)
	}
	indexed, err := resources.List(ctx, resource.Query{Keyword: "资源", Limit: 10})
	if err != nil {
		t.Fatalf("indexed resources List returned error: %v", err)
	}
	if indexed.Total != 2 {
		t.Fatalf("indexed total = %d items=%+v, want link and file", indexed.Total, indexed.Items)
	}
	stored, err := fixture.files.FindByMessageID(ctx, 1)
	if err != nil {
		t.Fatalf("find files: %v", err)
	}
	if len(stored) != 1 || stored[0].FileName != "ubuntu.iso" {
		t.Fatalf("stored files = %+v, want ubuntu.iso", stored)
	}
}

type processorFixture struct {
	conn              *sql.DB
	accounts          *repository.AccountRepository
	channels          *repository.ChannelRepository
	messages          *repository.MessageRepository
	links             *repository.LinkRepository
	files             *repository.FileRepository
	accountID         int64
	channelID         int64
	telegramChannelID int64
}

func newProcessorFixture(t *testing.T) processorFixture {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	channels := repository.NewChannelRepository(conn)
	messages := repository.NewMessageRepository(conn)
	links := repository.NewLinkRepository(conn)
	files := repository.NewFileRepository(conn)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	telegramChannelID := int64(200)
	channelID, err := channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: telegramChannelID,
		AccessHash:        300,
		Title:             "VIP",
		Type:              model.ChannelTypeChannel,
		ListenEnabled:     true,
		ListenState:       "enabled",
	})
	if err != nil {
		t.Fatalf("save channel: %v", err)
	}
	return processorFixture{
		conn:              conn,
		accounts:          accounts,
		channels:          channels,
		messages:          messages,
		links:             links,
		files:             files,
		accountID:         accountID,
		channelID:         channelID,
		telegramChannelID: telegramChannelID,
	}
}
