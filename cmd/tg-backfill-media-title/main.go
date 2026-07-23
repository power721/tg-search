// Command tg-backfill-media-title is a one-time maintenance tool that repairs
// telegram_links rows whose media_title was clobbered by a provider/channel
// label (e.g. "光鸭") before the link extractor was fixed.
//
// For every link whose media_title equals its note (the bug signature — the
// label overwrote both), the tool re-parses the original message text (stored
// in telegram_message_contents) with the now-fixed extractor and restores the
// real title parsed from the message header. Rows whose re-parsed title is
// empty or unchanged are left alone, and rows enriched by the AI metadata task
// (media_title != note) are never touched. After updating telegram_links it
// refreshes resource_index for the affected messages so the derived title and
// the FTS index stay consistent.
//
// Usage:
//
//	go run ./cmd/tg-backfill-media-title -config config.yaml            # dry-run (default)
//	go run ./cmd/tg-backfill-media-title -config config.yaml -dry-run=false
//
// Run while the tg-search service is stopped to avoid concurrent writes.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"tg-search/internal/config"
	"tg-search/internal/db"
	"tg-search/internal/link"
	"tg-search/internal/repository"
)

type affectedLink struct {
	id         int64
	messageID  int64
	url        string
	mediaTitle string
	note       string
}

type plannedChange struct {
	linkID    int64
	messageID int64
	url       string
	oldTitle  string
	newTitle  string
}

func main() {
	configPath := flag.String("config", "", "config file path (same as tg-search --config)")
	dryRun := flag.Bool("dry-run", true, "when true (the default) print planned changes without writing")
	probe := flag.Int64("probe", 0, "debug: print the extractor output for a single message id, then exit")
	flag.Parse()

	if *probe > 0 {
		if err := probeMessage(*configPath, *probe); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	if err := run(*configPath, *dryRun); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func probeMessage(configPath string, messageID int64) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	conn, err := db.Open(config.DatabasePath(cfg))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()
	var text string
	if err := conn.QueryRowContext(context.Background(),
		`SELECT COALESCE(text, '') FROM telegram_message_contents WHERE message_id = ?`, messageID).Scan(&text); err != nil {
		return fmt.Errorf("load text for message %d: %w", messageID, err)
	}
	fmt.Printf("=== message %d (%d bytes) ===\n%s\n\n=== extracted links ===\n", messageID, len(text), text)
	for _, l := range link.NewExtractor().Extract(text) {
		fmt.Printf("  type=%-10s title=%q note=%q\n    url=%s\n", l.Type, l.MediaTitle, l.Note, l.URL)
	}
	return nil
}

func run(configPath string, dryRun bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	conn, err := db.Open(config.DatabasePath(cfg))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()

	// No migrations: this tool only reads/writes long-existing tables and is run
	// against an already-migrated database (the service migrates on startup).
	ctx := context.Background()

	affected, err := loadAffected(ctx, conn)
	if err != nil {
		return fmt.Errorf("load affected links: %w", err)
	}
	if len(affected) == 0 {
		fmt.Println("no affected links (provider-label title) found; nothing to do")
		return nil
	}
	fmt.Printf("found %d affected links; loading message texts...\n", len(affected))

	texts, err := loadMessageTexts(ctx, conn, affected)
	if err != nil {
		return fmt.Errorf("load message texts: %w", err)
	}
	fmt.Printf("re-parsing %d messages...\n", len(texts))

	changes, skipped, err := planChanges(affected, texts)
	if err != nil {
		return err
	}

	reportAffected(affected)
	fmt.Printf("planned title updates: %d (left unchanged after re-parse: %d)\n\n", len(changes), skipped)
	reportChanges(changes)

	if dryRun {
		fmt.Println("\n[dry-run] no changes written. Re-run with -dry-run=false to apply.")
		return nil
	}

	indexRepo := repository.NewResourceIndexRepository(conn)
	if err := applyChanges(ctx, conn, indexRepo, changes); err != nil {
		return err
	}
	fmt.Printf("\napplied %d link title updates and refreshed resource_index for %d messages\n",
		len(changes), len(distinctMessageIDs(changes)))
	return nil
}

// loadAffected selects link rows exhibiting the bug signature: media_title is
// non-empty, equals the link note, and the source_snippet (the URL's own line)
// begins with "<media_title><:／：>" — i.e. the stored title is literally the
// provider/channel label prefixing the URL on the same line. This excludes the
// far larger set of correctly-titled rows whose title merely coincides with the
// note (their snippet starts with a generic label like 链接：/夸克：). Text is
// fetched separately in loadMessageTexts.
func loadAffected(ctx context.Context, db *sql.DB) ([]affectedLink, error) {
	rows, err := db.QueryContext(ctx, `
SELECT l.id, l.message_id, l.url, COALESCE(l.media_title, ''), COALESCE(l.note, '')
FROM telegram_links l
JOIN telegram_messages m ON m.id = l.message_id
WHERE m.deleted = 0
  AND l.media_title = l.note
  AND l.media_title <> ''
  AND length(l.source_snippet) > length(l.media_title)
  AND substr(l.source_snippet, 1, length(l.media_title)) = l.media_title
  AND substr(l.source_snippet, length(l.media_title) + 1, 1) IN ('：', ':')
ORDER BY l.message_id, l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []affectedLink
	for rows.Next() {
		var a affectedLink
		if err := rows.Scan(&a.id, &a.messageID, &a.url, &a.mediaTitle, &a.note); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// loadMessageTexts fetches the original message body once per distinct message
// referenced by the affected links, batched to keep the parameter count bounded.
func loadMessageTexts(ctx context.Context, db *sql.DB, affected []affectedLink) (map[int64]string, error) {
	ids := map[int64]struct{}{}
	for _, a := range affected {
		ids[a.messageID] = struct{}{}
	}
	texts := make(map[int64]string, len(ids))
	pending := make([]int64, 0, len(ids))
	for id := range ids {
		pending = append(pending, id)
	}
	const batchSize = 500
	for start := 0; start < len(pending); start += batchSize {
		end := start + batchSize
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]
		params := make([]any, len(batch))
		for i, id := range batch {
			params[i] = id
		}
		query := `SELECT message_id, COALESCE(text, '') FROM telegram_message_contents WHERE message_id IN (` +
			strings.Repeat("?,", len(batch)-1) + "?)"
		rows, err := db.QueryContext(ctx, query, params...)
		if err != nil {
			return nil, fmt.Errorf("query message texts: %w", err)
		}
		for rows.Next() {
			var id int64
			var text string
			if err := rows.Scan(&id, &text); err != nil {
				_ = rows.Close()
				return nil, err
			}
			texts[id] = text
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return texts, nil
}

// planChanges re-parses each affected message once and maps the corrected
// titles back onto the stored link rows by URL. A change is planned only when
// the re-parsed title is non-empty and differs from the current value.
func planChanges(affected []affectedLink, texts map[int64]string) (changes []plannedChange, skipped int, err error) {
	extractor := link.NewExtractor()
	byMessage := map[int64][]affectedLink{}
	for _, a := range affected {
		byMessage[a.messageID] = append(byMessage[a.messageID], a)
	}
	for messageID, links := range byMessage {
		urlToTitle := map[string]string{}
		if text := strings.TrimSpace(texts[messageID]); text != "" {
			for _, parsed := range extractor.Extract(text) {
				if parsed.URL != "" {
					urlToTitle[parsed.URL] = parsed.MediaTitle
				}
			}
		}
		for _, a := range links {
			newTitle := urlToTitle[a.url]
			if newTitle == "" || newTitle == a.mediaTitle {
				skipped++
				continue
			}
			changes = append(changes, plannedChange{
				linkID:    a.id,
				messageID: messageID,
				url:       a.url,
				oldTitle:  a.mediaTitle,
				newTitle:  newTitle,
			})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].messageID != changes[j].messageID {
			return changes[i].messageID < changes[j].messageID
		}
		return changes[i].linkID < changes[j].linkID
	})
	return changes, skipped, nil
}

func reportAffected(affected []affectedLink) {
	counts := map[string]int{}
	for _, a := range affected {
		counts[a.mediaTitle]++
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	fmt.Printf("affected links (media_title = note): %d across %d distinct titles\n", len(affected), len(names))
	fmt.Println("distinct affected media_title values:")
	for _, name := range names {
		fmt.Printf("  %6d  %s\n", counts[name], name)
	}
}

// reportChanges summarises the planned updates grouped by the current (buggy)
// title, showing how many links each group fixes and a couple of recoveries.
func reportChanges(changes []plannedChange) {
	if len(changes) == 0 {
		fmt.Println("(no titles would change)")
		return
	}
	byOld := map[string][]plannedChange{}
	for _, c := range changes {
		byOld[c.oldTitle] = append(byOld[c.oldTitle], c)
	}
	olds := make([]string, 0, len(byOld))
	for old := range byOld {
		olds = append(olds, old)
	}
	sort.Slice(olds, func(i, j int) bool {
		if len(byOld[olds[i]]) != len(byOld[olds[j]]) {
			return len(byOld[olds[i]]) > len(byOld[olds[j]])
		}
		return olds[i] < olds[j]
	})
	fmt.Println("planned changes grouped by current (buggy) title:")
	limit := 30
	for i, old := range olds {
		if i >= limit {
			fmt.Printf("  ... and %d more distinct titles\n", len(olds)-limit)
			break
		}
		items := byOld[old]
		samples := make([]string, 0, 2)
		for _, c := range items {
			if len(samples) >= 2 {
				break
			}
			samples = append(samples, c.newTitle)
		}
		fmt.Printf("  %5d  %q  ->  e.g. %s\n", len(items), old, quoteJoin(samples))
	}
}

func quoteJoin(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(parts, ", ")
}

func applyChanges(ctx context.Context, db *sql.DB, indexRepo *repository.ResourceIndexRepository, changes []plannedChange) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, `UPDATE telegram_links SET media_title = ? WHERE id = ?`, c.newTitle, c.linkID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("update link %d: %w", c.linkID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit link updates: %w", err)
	}
	if err := indexRepo.RefreshMessages(ctx, distinctMessageIDs(changes)); err != nil {
		return fmt.Errorf("refresh resource_index: %w", err)
	}
	return nil
}

func distinctMessageIDs(changes []plannedChange) []int64 {
	seen := map[int64]struct{}{}
	ids := make([]int64, 0, len(changes))
	for _, c := range changes {
		if _, ok := seen[c.messageID]; ok {
			continue
		}
		seen[c.messageID] = struct{}{}
		ids = append(ids, c.messageID)
	}
	return ids
}
