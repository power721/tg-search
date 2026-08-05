package resource

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tg-search/internal/db"
	"tg-search/internal/link"
	"tg-search/internal/model"
	"tg-search/internal/repository"
)

func TestRepairMediaTitlesFixesProviderLabelRows(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	channels := repository.NewChannelRepository(conn)
	messages := repository.NewMessageRepository(conn)
	links := repository.NewLinkRepository(conn)
	index := repository.NewResourceIndexRepository(conn)

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "leoziyuan", Type: model.ChannelTypeChannel})
	text := "🗄 速度与激情9 F9: The Fast Saga (2021)【4K SDR 无损超清】\n\n链接\n光鸭：https://www.guangyapan.com/s/abc\n\n🏷 标签：#速度与激情9 #动作"
	stored, err := messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
		Text: text, RawJSON: "{}", Date: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("save message: %v", err)
	}
	msgID := stored[0].ID
	if _, err := links.SaveBatch(ctx, msgID, []model.Link{
		{Type: "guangya", URL: "https://www.guangyapan.com/s/abc", MediaTitle: "光鸭", Note: "光鸭", SourceSnippet: "光鸭：https://www.guangyapan.com/s/abc"},
		{Type: "baidu", URL: "https://pan.baidu.com/s/x", MediaTitle: "莫离", Note: "莫离", SourceSnippet: "链接：https://pan.baidu.com/s/x"},
		{Type: "quark", URL: "https://pan.quark.cn/s/y", MediaTitle: "速度与激情9", Note: "光鸭", SourceSnippet: "夸克：https://pan.quark.cn/s/y"},
	}); err != nil {
		t.Fatalf("save links: %v", err)
	}

	service := NewService(links, nil, index, repository.NewMessageRepository(conn), link.NewExtractor())

	summary, err := service.RepairMediaTitles(ctx, nil, false)
	if err != nil {
		t.Fatalf("RepairMediaTitles: %v", err)
	}
	if summary.Affected != 1 || summary.Changed != 1 {
		t.Fatalf("summary = %+v, want affected=1 changed=1", summary)
	}

	loaded, err := links.ListByMessage(ctx, msgID)
	if err != nil {
		t.Fatalf("ListByMessage: %v", err)
	}
	byURL := map[string]model.Link{}
	for _, l := range loaded {
		byURL[l.URL] = l
	}
	if byURL["https://www.guangyapan.com/s/abc"].MediaTitle != "速度与激情9 F9: The Fast Saga" {
		t.Fatalf("guangya title = %q, want 速度与激情9 F9: The Fast Saga", byURL["https://www.guangyapan.com/s/abc"].MediaTitle)
	}
	if byURL["https://pan.baidu.com/s/x"].MediaTitle != "莫离" {
		t.Fatalf("baidu title changed: %q", byURL["https://pan.baidu.com/s/x"].MediaTitle)
	}
	if byURL["https://pan.quark.cn/s/y"].MediaTitle != "速度与激情9" {
		t.Fatalf("quark (AI-enriched) title changed: %q", byURL["https://pan.quark.cn/s/y"].MediaTitle)
	}

	// idempotent: a second run changes nothing
	again, err := service.RepairMediaTitles(ctx, nil, false)
	if err != nil {
		t.Fatalf("second RepairMediaTitles: %v", err)
	}
	if again.Changed != 0 {
		t.Fatalf("second run changed = %d, want 0 (idempotent)", again.Changed)
	}
}

func TestRepairMediaTitlesUpdatesNoteAlongsideTitle(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	channels := repository.NewChannelRepository(conn)
	messages := repository.NewMessageRepository(conn)
	links := repository.NewLinkRepository(conn)
	index := repository.NewResourceIndexRepository(conn)

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000002", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 2, Title: "panlink", Type: model.ChannelTypeChannel})
	text := "🎬 ·✅【地球脉动（1-3季）】【4K】【国语】\n类型：纪录片\n💾 网盘：光鸭网盘\n网盘：https://pan.quark.cn/s/abc"
	stored, err := messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2,
		Text: text, RawJSON: "{}", Date: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("save message: %v", err)
	}
	msgID := stored[0].ID
	// Stored row exhibits the provider-label bug: title and note both clobbered
	// to "网盘", with the snippet beginning "<title>：".
	if _, err := links.SaveBatch(ctx, msgID, []model.Link{
		{Type: "quark", URL: "https://pan.quark.cn/s/abc", MediaTitle: "网盘", Note: "网盘", SourceSnippet: "网盘：https://pan.quark.cn/s/abc"},
	}); err != nil {
		t.Fatalf("save links: %v", err)
	}

	service := NewService(links, nil, index, repository.NewMessageRepository(conn), link.NewExtractor())
	summary, err := service.RepairMediaTitles(ctx, nil, false)
	if err != nil {
		t.Fatalf("RepairMediaTitles: %v", err)
	}
	if summary.Changed != 1 {
		t.Fatalf("summary.Changed = %d, want 1", summary.Changed)
	}

	loaded, err := links.ListByMessage(ctx, msgID)
	if err != nil {
		t.Fatalf("ListByMessage: %v", err)
	}
	if loaded[0].MediaTitle != "地球脉动" {
		t.Fatalf("title = %q, want 地球脉动", loaded[0].MediaTitle)
	}
	// Note must be repaired too, not left as the "网盘" label.
	if loaded[0].Note != "地球脉动" {
		t.Fatalf("note = %q, want 地球脉动", loaded[0].Note)
	}
}

func TestRepairMediaTitlesFixesDecorativeJunkTitle(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	channels := repository.NewChannelRepository(conn)
	messages := repository.NewMessageRepository(conn)
	links := repository.NewLinkRepository(conn)
	index := repository.NewResourceIndexRepository(conn)

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000003", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 3, Title: "panlink", Type: model.ChannelTypeChannel})
	text := "🎬 ·✅【史前星球】【4K】【国语】\n类型：纪录片\n💾 网盘：百度网盘\n\n·✅✅✅\nhttps://pan.baidu.com/s/junk"
	stored, err := messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 3,
		Text: text, RawJSON: "{}", Date: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("save message: %v", err)
	}
	msgID := stored[0].ID
	// Old parser stored the decorative marker line as the title (and note).
	if _, err := links.SaveBatch(ctx, msgID, []model.Link{
		{Type: "baidu", URL: "https://pan.baidu.com/s/junk", MediaTitle: "·✅✅✅", Note: "·✅✅✅", SourceSnippet: "https://pan.baidu.com/s/junk"},
	}); err != nil {
		t.Fatalf("save links: %v", err)
	}

	service := NewService(links, nil, index, repository.NewMessageRepository(conn), link.NewExtractor())
	summary, err := service.RepairMediaTitles(ctx, nil, false)
	if err != nil {
		t.Fatalf("RepairMediaTitles: %v", err)
	}
	if summary.Changed != 1 {
		t.Fatalf("summary.Changed = %d, want 1 (junk title should be a candidate)", summary.Changed)
	}

	loaded, err := links.ListByMessage(ctx, msgID)
	if err != nil {
		t.Fatalf("ListByMessage: %v", err)
	}
	if loaded[0].MediaTitle != "史前星球" {
		t.Fatalf("title = %q, want 史前星球", loaded[0].MediaTitle)
	}
}

func TestRepairMediaTitlesDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	channels := repository.NewChannelRepository(conn)
	messages := repository.NewMessageRepository(conn)
	links := repository.NewLinkRepository(conn)
	index := repository.NewResourceIndexRepository(conn)

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000001", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 2, Title: "leoziyuan", Type: model.ChannelTypeChannel})
	stored, _ := messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
		Text: "名称：巅峰猎杀 Apex (2026)\n光鸭：https://www.guangyapan.com/s/zzz", RawJSON: "{}", Date: time.Now().UTC(),
	}})
	saved, _ := links.SaveBatch(ctx, stored[0].ID, []model.Link{
		{Type: "guangya", URL: "https://www.guangyapan.com/s/zzz", MediaTitle: "光鸭", Note: "光鸭", SourceSnippet: "光鸭：https://www.guangyapan.com/s/zzz"},
	})

	service := NewService(links, nil, index, repository.NewMessageRepository(conn), link.NewExtractor())
	summary, err := service.RepairMediaTitles(ctx, nil, true)
	if err != nil {
		t.Fatalf("RepairMediaTitles dry-run: %v", err)
	}
	if summary.Changed != 1 {
		t.Fatalf("dry-run summary.Changed = %d, want 1", summary.Changed)
	}
	loaded, _ := links.ListByMessage(ctx, stored[0].ID)
	if loaded[0].MediaTitle != "光鸭" {
		t.Fatalf("dry-run wrote media_title = %q, want unchanged 光鸭", loaded[0].MediaTitle)
	}
	_ = saved
}
