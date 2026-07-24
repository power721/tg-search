package resource

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"tg-search/internal/db"
	"tg-search/internal/link"
	"tg-search/internal/model"
	"tg-search/internal/repository"
)

func TestRepairMediaTitlesLogsEachChange(t *testing.T) {
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
	if _, err := links.SaveBatch(ctx, stored[0].ID, []model.Link{
		{Type: "guangya", URL: "https://www.guangyapan.com/s/abc", MediaTitle: "光鸭", Note: "光鸭", SourceSnippet: "光鸭：https://www.guangyapan.com/s/abc"},
	}); err != nil {
		t.Fatalf("save link: %v", err)
	}

	core, recorded := observer.New(zap.InfoLevel)
	service := NewService(links, nil, index, repository.NewMessageRepository(conn), link.NewExtractor(), zap.New(core))

	summary, err := service.RepairMediaTitles(ctx, nil, false)
	if err != nil {
		t.Fatalf("RepairMediaTitles: %v", err)
	}
	if summary.Changed != 1 {
		t.Fatalf("summary.Changed = %d, want 1", summary.Changed)
	}

	entries := recorded.FilterMessage("repaired media title").All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1 per change", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["old"] != "光鸭" || fields["new"] != "速度与激情9 F9: The Fast Saga" {
		t.Fatalf("log fields = %+v, want old=光鸭 new=速度与激情9 F9: The Fast Saga", fields)
	}
	if fields["url"] != "https://www.guangyapan.com/s/abc" {
		t.Fatalf("log url = %v", fields["url"])
	}
}
