package repository

import (
	"context"
	"testing"
	"time"

	"tg-search/internal/model"
)

func TestListMediaTitleLabelCandidates(t *testing.T) {
	ctx := context.Background()
	conn := openRepositoryTestDB(t)
	accounts := NewAccountRepository(conn)
	channels := NewChannelRepository(conn)
	messages := NewMessageRepository(conn)
	links := NewLinkRepository(conn)

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, err := messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
		Text: "名称：巅峰猎杀 Apex (2026)\n光鸭：https://www.guangyapan.com/s/abc", RawJSON: "{}", Date: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("save message: %v", err)
	}
	msgID := stored[0].ID

	seed := []model.Link{
		// bug row: provider label clobbered the title (media_title == note, snippet "光鸭：").
		{Type: "guangya", URL: "https://www.guangyapan.com/s/abc", MediaTitle: "光鸭", Note: "光鸭", SourceSnippet: "光鸭：https://www.guangyapan.com/s/abc"},
		// clean row: title coincides with note but snippet uses a generic label.
		{Type: "baidu", URL: "https://pan.baidu.com/s/x", MediaTitle: "莫离", Note: "莫离", SourceSnippet: "链接：https://pan.baidu.com/s/x"},
		// ai-enriched row: media_title differs from note.
		{Type: "quark", URL: "https://pan.quark.cn/s/y", MediaTitle: "速度与激情9", Note: "光鸭", SourceSnippet: "夸克：https://pan.quark.cn/s/y"},
	}
	if _, err := links.SaveBatch(ctx, msgID, seed); err != nil {
		t.Fatalf("save links: %v", err)
	}

	got, err := links.ListMediaTitleLabelCandidates(ctx)
	if err != nil {
		t.Fatalf("ListMediaTitleLabelCandidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	if got[0].URL != "https://www.guangyapan.com/s/abc" || got[0].MediaTitle != "光鸭" {
		t.Fatalf("candidate = %+v, want the 光鸭 guangya row", got[0])
	}
}

func TestUpdateMediaTitle(t *testing.T) {
	ctx := context.Background()
	conn := openRepositoryTestDB(t)
	accounts := NewAccountRepository(conn)
	channels := NewChannelRepository(conn)
	messages := NewMessageRepository(conn)
	links := NewLinkRepository(conn)

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000001", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 2, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, _ := messages.SaveBatch(ctx, []model.Message{{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "x", RawJSON: "{}", Date: time.Now().UTC()}})
	saved, err := links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "url", URL: "https://example.com/a", MediaTitle: "光鸭", Note: "光鸭"}})
	if err != nil {
		t.Fatalf("save link: %v", err)
	}

	if err := links.UpdateMediaTitle(ctx, saved[0].ID, "巅峰猎杀 Apex"); err != nil {
		t.Fatalf("UpdateMediaTitle: %v", err)
	}
	loaded, err := links.ListByMessage(ctx, stored[0].ID)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("ListByMessage: %v len=%d", err, len(loaded))
	}
	if loaded[0].MediaTitle != "巅峰猎杀 Apex" {
		t.Fatalf("media_title = %q, want 巅峰猎杀 Apex", loaded[0].MediaTitle)
	}
}

func TestBatchTextByMessageIDs(t *testing.T) {
	ctx := context.Background()
	conn := openRepositoryTestDB(t)
	accounts := NewAccountRepository(conn)
	channels := NewChannelRepository(conn)
	messages := NewMessageRepository(conn)

	accountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000002", Status: model.AccountStatusOnline})
	channelID, _ := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 3, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, _ := messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "hello", RawJSON: "{}", Date: time.Now().UTC()},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "world", RawJSON: "{}", Date: time.Now().UTC()},
	})

	texts, err := messages.BatchTextByMessageIDs(ctx, []int64{stored[0].ID, stored[1].ID, 999999})
	if err != nil {
		t.Fatalf("BatchTextByMessageIDs: %v", err)
	}
	if texts[stored[0].ID] != "hello" || texts[stored[1].ID] != "world" {
		t.Fatalf("texts = %+v, want hello/world", texts)
	}
	if _, ok := texts[999999]; ok {
		t.Fatalf("missing id should be absent from map")
	}
}
