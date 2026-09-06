package repository

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-search/internal/db"
	"tg-search/internal/model"
)

func TestLinkRepositoryPersistsResourceFields(t *testing.T) {
	ctx := context.Background()
	conn := openRepositoryTestDB(t)
	accounts := NewAccountRepository(conn)
	channels := NewChannelRepository(conn)
	messages := NewMessageRepository(conn)
	links := NewLinkRepository(conn)

	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	channelID, err := channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	if err != nil {
		t.Fatalf("save channel: %v", err)
	}
	stored, err := messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
		Text: "Ubuntu ISO https://example.com/ubuntu.iso", RawJSON: "{}", Date: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("save message: %v", err)
	}

	_, err = links.SaveBatch(ctx, stored[0].ID, []model.Link{{
		Type:          "url",
		Category:      "http",
		URL:           "https://example.com/ubuntu.iso",
		SourceSnippet: "Ubuntu ISO https://example.com/ubuntu.iso",
		Note:          "Ubuntu ISO",
		MediaTitle:    "Ubuntu",
		MediaYear:     "2026",
		MediaQuality:  "ISO",
		MediaSize:     "5.8 GB",
		MediaTags:     "linux release",
	}})
	if err != nil {
		t.Fatalf("save links: %v", err)
	}

	results, err := links.Search(ctx, LinkSearchParams{Keyword: "Ubuntu", Limit: 10})
	if err != nil {
		t.Fatalf("search links: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if results[0].Category != "http" || results[0].SourceSnippet != "Ubuntu ISO https://example.com/ubuntu.iso" {
		t.Fatalf("resource fields = category %q snippet %q", results[0].Category, results[0].SourceSnippet)
	}
	if results[0].MediaTitle != "Ubuntu" || results[0].MediaYear != "2026" || results[0].MediaQuality != "ISO" || results[0].MediaSize != "5.8 GB" || results[0].MediaTags != "linux release" {
		t.Fatalf("media fields = %+v", results[0])
	}
}

// The dashboard aggregate queries must run on the read pool when one is
// attached: on the writer pool they queue behind sync/index writes and can
// stall the dashboard for many seconds.
func TestLinkRepositoryAggregatesUseReadPool(t *testing.T) {
	ctx := context.Background()
	conn := openRepositoryTestDB(t)
	defer conn.Close()

	// Reader pool on a file with no schema: any aggregate that reaches it must
	// fail with "no such table", proving it did not run on the writer.
	reader, err := db.OpenRead(filepath.Join(t.TempDir(), "reader.db"))
	if err != nil {
		t.Fatalf("open read pool: %v", err)
	}
	defer reader.Close()

	links := NewLinkRepository(conn).WithReadDB(reader)
	if _, err := links.CountByType(ctx); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("CountByType error = %v, want no-such-table from the empty read pool (query ran on the writer pool)", err)
	}
	if _, err := links.CountByResourceCategory(ctx, LinkSearchParams{}); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("CountByResourceCategory error = %v, want no-such-table from the empty read pool (query ran on the writer pool)", err)
	}
	if _, err := links.CountSearch(ctx, LinkSearchParams{}); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("CountSearch error = %v, want no-such-table from the empty read pool (query ran on the writer pool)", err)
	}
}

// Once the per-type snapshot is older than the TTL it must still be served
// immediately (the compute joins every link row); the recompute happens in
// the background and replaces the cache.
func TestLinkRepositoryCountByTypeServesStaleSnapshotAndRefreshes(t *testing.T) {
	ctx := context.Background()
	conn := openRepositoryTestDB(t)
	defer conn.Close()

	links := NewLinkRepository(conn)
	links.typeStatsCache = map[string]int{"magnet": 7}
	links.typeStatsAt = time.Now().Add(-2 * linkTypeStatsTTL)

	got, err := links.CountByType(ctx)
	if err != nil {
		t.Fatalf("CountByType: %v", err)
	}
	if got["magnet"] != 7 {
		t.Fatalf("CountByType = %+v, want the stale snapshot served immediately", got)
	}

	// The background refresh on an empty library replaces it with an empty map.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		links.typeStatsMu.Lock()
		n := len(links.typeStatsCache)
		links.typeStatsMu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background refresh did not replace the stale cache")
}
