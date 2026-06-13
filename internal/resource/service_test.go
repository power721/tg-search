package resource

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"tg-search/internal/db"
	"tg-search/internal/model"
	"tg-search/internal/repository"
)

func TestResourceLibraryDeduplicatesLinks(t *testing.T) {
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

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	oldDate := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	newDate := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	stored, err := messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "ubuntu old", RawJSON: "{}", Date: oldDate},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "ubuntu new", RawJSON: "{}", Date: newDate},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	for i, msg := range stored {
		note := "ubuntu old"
		if i == 1 {
			note = "ubuntu latest"
		}
		if _, err := links.SaveBatch(ctx, msg.ID, []model.Link{{Type: "url", Category: "http", URL: "https://example.com/ubuntu", Note: note}}); err != nil {
			t.Fatalf("save link: %v", err)
		}
	}
	if _, err := files.SaveBatch(ctx, stored[1].ID, []model.File{{FileName: "ubuntu.iso", Extension: ".iso", SizeBytes: 5000, Category: "software"}}); err != nil {
		t.Fatalf("save file: %v", err)
	}

	service := NewService(links, files)
	result, err := service.List(ctx, Query{Keyword: "ubuntu", Limit: 10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if result.Total != 2 {
		t.Fatalf("total = %d, want deduped link plus file", result.Total)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(result.Items))
	}
	if result.Items[0].Kind != "link" || result.Items[0].URL != "https://example.com/ubuntu" || result.Items[0].Note != "ubuntu latest" || !result.Items[0].Datetime.Equal(newDate) {
		t.Fatalf("first item = %+v, want newest deduped link", result.Items[0])
	}
	var fileItem *Item
	for i := range result.Items {
		if result.Items[i].Kind == "file" {
			fileItem = &result.Items[i]
			break
		}
	}
	if fileItem == nil || fileItem.Category != "files" || fileItem.Type != "software" {
		t.Fatalf("file item = %+v, want category files and concrete type software", fileItem)
	}
	if result.Grouped["_total"] != 2 {
		t.Fatalf("grouped = %+v, want _total=2", result.Grouped)
	}
}

func TestResourceLibraryDeleteManyRemovesLinksAndFiles(t *testing.T) {
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
	stats := repository.NewResourceStatsRepository(conn)

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, err := messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "ubuntu old", RawJSON: "{}", Date: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "ubuntu new", RawJSON: "{}", Date: time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	for _, msg := range stored {
		if _, err := links.SaveBatch(ctx, msg.ID, []model.Link{{Type: "url", Category: "http", URL: "https://example.com/ubuntu"}}); err != nil {
			t.Fatalf("save duplicate link: %v", err)
		}
	}
	if _, err := files.SaveBatch(ctx, stored[1].ID, []model.File{{FileName: "ubuntu.iso", Extension: ".iso", SizeBytes: 5000, Category: "software"}}); err != nil {
		t.Fatalf("save file: %v", err)
	}

	service := NewService(links, files, stats)
	before, err := service.List(ctx, Query{Keyword: "ubuntu", Limit: 10})
	if err != nil {
		t.Fatalf("List before delete returned error: %v", err)
	}
	if len(before.Items) != 2 {
		t.Fatalf("items before delete = %+v, want link and file", before.Items)
	}
	ids := []string{before.Items[0].ID, before.Items[1].ID, "link:https://example.com/missing"}
	result, err := service.DeleteMany(ctx, ids)
	if err != nil {
		t.Fatalf("DeleteMany returned error: %v", err)
	}
	if result.Deleted != 2 || len(result.MissingIDs) != 1 || result.MissingIDs[0] != "link:https://example.com/missing" {
		t.Fatalf("delete result = %+v, want two deleted and one missing", result)
	}
	after, err := service.List(ctx, Query{Keyword: "ubuntu", Limit: 10})
	if err != nil {
		t.Fatalf("List after delete returned error: %v", err)
	}
	if after.Total != 0 || len(after.Items) != 0 {
		t.Fatalf("items after delete = %+v total=%d, want empty", after.Items, after.Total)
	}
	grouped, err := service.GlobalGrouped(ctx)
	if err != nil {
		t.Fatalf("GlobalGrouped returned error: %v", err)
	}
	if grouped["http"] != 0 || grouped["files"] != 0 {
		t.Fatalf("grouped after delete = %+v, want empty resource counts", grouped)
	}
}

func TestResourceLibraryResourceTypeStatsCountsDashboardCategories(t *testing.T) {
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

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, err := messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "ubuntu cloud", RawJSON: "{}", Date: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "ubuntu magnet", RawJSON: "{}", Date: time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 3, Text: "ubuntu file", RawJSON: "{}", Date: time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/ubuntu"}}); err != nil {
		t.Fatalf("save cloud link: %v", err)
	}
	if _, err := links.SaveBatch(ctx, stored[1].ID, []model.Link{{Type: "magnet", Category: "magnet", URL: "magnet:?xt=urn:btih:ubuntu"}}); err != nil {
		t.Fatalf("save magnet link: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[2].ID, []model.File{{FileName: "ubuntu.iso", Extension: ".iso", SizeBytes: 5000, Category: "software"}}); err != nil {
		t.Fatalf("save file: %v", err)
	}

	service := NewService(links, files)
	grouped, err := service.ResourceTypeStats(ctx)
	if err != nil {
		t.Fatalf("ResourceTypeStats returned error: %v", err)
	}
	if grouped["cloud_drive"] != 1 || grouped["magnet"] != 1 || grouped["files"] != 1 || grouped["_total"] != 3 {
		t.Fatalf("grouped = %+v, want cloud_drive=1 magnet=1 files=1 _total=3", grouped)
	}
}

func TestResourceLibraryRanksQualityBeforeFreshness(t *testing.T) {
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

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	oldDate := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	newDate := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	stored, err := messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "ubuntu 24.04 完整合集", RawJSON: "{}", Date: oldDate},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "ubuntu random mirror", RawJSON: "{}", Date: newDate},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := links.SaveBatch(ctx, stored[0].ID, []model.Link{{
		Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/high", Note: "Ubuntu 24.04 最新合集", MediaTitle: "Ubuntu 24.04",
		MediaYear: "2026", MediaQuality: "4K", MediaCategory: "software",
	}}); err != nil {
		t.Fatalf("save high quality link: %v", err)
	}
	if _, err := links.SaveBatch(ctx, stored[1].ID, []model.Link{{
		Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/weak", Note: "random mirror",
	}}); err != nil {
		t.Fatalf("save weak link: %v", err)
	}

	service := NewService(links, nil)
	result, err := service.List(ctx, Query{Keyword: "ubuntu", Limit: 10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %+v, want two ranked resources", result.Items)
	}
	if result.Items[0].URL != "https://pan.quark.cn/s/high" {
		t.Fatalf("first resource = %+v, want high quality exact match before newer weak match", result.Items[0])
	}
}

func TestResourceLibraryHotSortUsesScoreExplanations(t *testing.T) {
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

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	primaryChannelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "Primary", Type: model.ChannelTypeChannel})
	mirrorChannelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 2, Title: "Mirror", Type: model.ChannelTypeChannel})
	oldDate := time.Now().UTC().Add(-48 * time.Hour)
	newDate := time.Now().UTC().Add(-time.Hour)
	stored, err := messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: primaryChannelID, TelegramMessageID: 1, Text: "hot pack 4k", RawJSON: "{}", Date: oldDate},
		{AccountID: accountID, ChannelID: mirrorChannelID, TelegramMessageID: 2, Text: "hot pack mirror", RawJSON: "{}", Date: oldDate.Add(time.Minute)},
		{AccountID: accountID, ChannelID: primaryChannelID, TelegramMessageID: 3, Text: "weak new resource", RawJSON: "{}", Date: newDate},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	for _, msg := range stored[:2] {
		if _, err := links.SaveBatch(ctx, msg.ID, []model.Link{{
			Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/hot", Note: "Hot Pack", MediaTitle: "Hot Pack",
			MediaYear: "2026", MediaQuality: "4K", MediaCategory: "movie", MediaTags: "hot,4k",
		}}); err != nil {
			t.Fatalf("save hot link: %v", err)
		}
	}
	if _, err := links.SaveBatch(ctx, stored[2].ID, []model.Link{{
		Type: "url", Category: "http", URL: "https://example.com/weak", Note: "weak new resource",
	}}); err != nil {
		t.Fatalf("save weak link: %v", err)
	}

	service := NewService(links, nil)
	result, err := service.List(ctx, Query{Sort: "hot", Limit: 10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %+v, want two deduped resources", result.Items)
	}
	if result.Items[0].URL != "https://pan.quark.cn/s/hot" {
		t.Fatalf("first hot resource = %+v, want repeated quark resource", result.Items[0])
	}
	if result.Items[0].Score <= result.Items[1].Score {
		t.Fatalf("scores = %d <= %d, want hot resource first", result.Items[0].Score, result.Items[1].Score)
	}
	if result.Items[0].ScoreExplain.SourceChannelCount != 2 || result.Items[0].ScoreExplain.MessageCount != 2 || result.Items[0].ScoreExplain.ProviderCount != 1 {
		t.Fatalf("score explain = %+v, want source/message/provider counts", result.Items[0].ScoreExplain)
	}
}

func TestResourceLibraryHotSortConsidersResourcesOutsideNewestPage(t *testing.T) {
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

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	primaryChannelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "Primary", Type: model.ChannelTypeChannel})
	mirrorChannelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 2, Title: "Mirror", Type: model.ChannelTypeChannel})
	now := time.Now().UTC()
	oldDate := now.Add(-48 * time.Hour)

	input := []model.Message{
		{AccountID: accountID, ChannelID: primaryChannelID, TelegramMessageID: 1, Text: "older hot pack", RawJSON: "{}", Date: oldDate},
		{AccountID: accountID, ChannelID: mirrorChannelID, TelegramMessageID: 2, Text: "older hot mirror", RawJSON: "{}", Date: oldDate.Add(time.Minute)},
	}
	for i := 0; i < 60; i++ {
		input = append(input, model.Message{
			AccountID: accountID, ChannelID: primaryChannelID, TelegramMessageID: int64(i + 10),
			Text: "new weak resource", RawJSON: "{}", Date: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	stored, err := messages.SaveBatch(ctx, input)
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	for _, msg := range stored[:2] {
		if _, err := links.SaveBatch(ctx, msg.ID, []model.Link{{
			Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/older-hot", Note: "Older Hot Pack",
			MediaTitle: "Older Hot Pack", MediaYear: "2026", MediaQuality: "4K", MediaCategory: "movie", MediaTags: "hot,4k",
		}}); err != nil {
			t.Fatalf("save older hot link: %v", err)
		}
	}
	for i, msg := range stored[2:] {
		if _, err := links.SaveBatch(ctx, msg.ID, []model.Link{{
			Type: "url", Category: "http", URL: "https://example.com/weak-" + strconv.Itoa(i), Note: "new weak resource",
		}}); err != nil {
			t.Fatalf("save weak link %d: %v", i, err)
		}
	}

	service := NewService(links, nil)
	result, err := service.List(ctx, Query{Sort: "hot", Limit: 10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(result.Items) != 10 {
		t.Fatalf("items len = %d, want first page of 10", len(result.Items))
	}
	if result.Items[0].URL != "https://pan.quark.cn/s/older-hot" {
		t.Fatalf("first hot resource = %+v, want older resource outside newest page", result.Items[0])
	}

	from := now.Add(-24 * time.Hour)
	recent, err := service.List(ctx, Query{Sort: "hot", DateFrom: &from, Limit: 10})
	if err != nil {
		t.Fatalf("recent List returned error: %v", err)
	}
	for _, item := range recent.Items {
		if item.URL == "https://pan.quark.cn/s/older-hot" {
			t.Fatalf("recent items = %+v, want older hot resource excluded by DateFrom", recent.Items)
		}
	}
}

func TestResourceLibraryExcludesImageFilesByDefault(t *testing.T) {
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

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	mirrorChannelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 2, Title: "Mirror", Type: model.ChannelTypeChannel})
	publishedAt := time.Date(2026, 6, 9, 16, 26, 0, 0, time.UTC)
	stored, err := messages.SaveBatch(ctx, []model.Message{
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 227,
			MessageType: "photo", MediaSummary: "photo", Text: "吞噬星空 https://pan.quark.cn/s/abc", RawJSON: "{}", Date: publishedAt,
		},
		{
			AccountID: accountID, ChannelID: mirrorChannelID, TelegramMessageID: 228,
			MessageType: "photo", MediaSummary: "photo", Text: "telegram-photo-6143006241194709282.jpg", RawJSON: "{}", Date: publishedAt,
		},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := links.SaveBatch(ctx, stored[0].ID, []model.Link{{
		Type: "url", Category: "cloud_drive", URL: "https://pan.quark.cn/s/abc", MediaTitle: "吞噬星空",
	}}); err != nil {
		t.Fatalf("save link: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[0].ID, []model.File{{
		FileName: "telegram-photo-6143006241194709282.jpg", Extension: ".jpg", MimeType: "image/jpeg", Category: "image",
	}}); err != nil {
		t.Fatalf("save image file: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[1].ID, []model.File{{
		FileName: "telegram-photo-6143006241194709282.jpg", Extension: ".jpg", MimeType: "image/jpeg", Category: "image",
	}}); err != nil {
		t.Fatalf("save mirrored image file: %v", err)
	}

	service := NewService(links, files)
	result, err := service.List(ctx, Query{Limit: 10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("total = %d, want only the link resource", result.Total)
	}
	if len(result.Items) != 1 || result.Items[0].Kind != "link" {
		t.Fatalf("items = %+v, want only the link resource", result.Items)
	}
	if result.Grouped["_total"] != 1 {
		t.Fatalf("grouped = %+v, want _total=1", result.Grouped)
	}
}

func TestResourceLibraryCountsAndPagesBeyondInitialLinkBatch(t *testing.T) {
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

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	input := make([]model.Message, 0, 250)
	for i := 0; i < 250; i++ {
		input = append(input, model.Message{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: int64(i + 1),
			Text: "ubuntu resource", RawJSON: "{}", Date: now.Add(time.Duration(i) * time.Minute),
		})
	}
	stored, err := messages.SaveBatch(ctx, input)
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	for i, msg := range stored {
		if _, err := links.SaveBatch(ctx, msg.ID, []model.Link{{
			Type: "url", Category: "http", URL: "https://example.com/" + strconv.Itoa(i),
		}}); err != nil {
			t.Fatalf("save link %d: %v", i, err)
		}
	}

	service := NewService(links, nil)
	result, err := service.List(ctx, Query{Keyword: "ubuntu", Limit: 50, Offset: 200})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if result.Total != 250 {
		t.Fatalf("total = %d, want all matching deduped resources", result.Total)
	}
	if len(result.Items) != 50 {
		t.Fatalf("items len = %d, want final page of 50 resources", len(result.Items))
	}
	if result.Grouped["_total"] != 250 {
		t.Fatalf("grouped = %+v, want _total=250", result.Grouped)
	}
}

func TestResourceServiceUsesIndexWhenAvailable(t *testing.T) {
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
	index := repository.NewResourceIndexRepository(conn)
	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, err := messages.SaveBatch(ctx, []model.Message{{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "ubuntu", RawJSON: "{}", Date: time.Now().UTC()}})
	if err != nil {
		t.Fatalf("save message: %v", err)
	}
	if _, err := links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/indexed", Note: "Ubuntu"}}); err != nil {
		t.Fatalf("save link: %v", err)
	}
	if err := index.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	service := NewService(links, files, nil, index)
	result, err := service.List(ctx, Query{Keyword: "ubuntu", Limit: 10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].URL != "https://pan.quark.cn/s/indexed" {
		t.Fatalf("result = %+v, want indexed resource", result)
	}
}
