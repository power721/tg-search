// Command tg-backfill-media-title is a headless wrapper around the same
// resource.Service.RepairMediaTitles logic that the admin UI's "修复媒体标题"
// button triggers (as a background task). It repairs telegram_links rows whose
// media_title was clobbered by a provider label by re-parsing each affected
// message's stored text with the fixed extractor.
//
// Usage:
//
//	go run ./cmd/tg-backfill-media-title -db /path/to/tg-search.db            # dry-run (default)
//	go run ./cmd/tg-backfill-media-title -db /path/to/tg-search.db -dry-run=false
//	go run ./cmd/tg-backfill-media-title -db /path/to/tg-search.db -probe <message_id>
//
// For the UI path, use the 修复 button in 设置 → 系统; this CLI exists for
// headless / automated runs. Run while the tg-search service is stopped.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"tg-search/internal/config"
	"tg-search/internal/db"
	"tg-search/internal/link"
	"tg-search/internal/repository"
	"tg-search/internal/resource"
)

func main() {
	configPath := flag.String("config", "", "config file path (same as tg-search --config); ignored when -db is set")
	dbPath := flag.String("db", "", "direct path to tg-search.db (overrides -config)")
	dryRun := flag.Bool("dry-run", true, "when true (the default) plan only, without writing")
	probe := flag.Int64("probe", 0, "debug: print the extractor output for a single message id, then exit")
	flag.Parse()

	path, err := resolveDBPath(*configPath, *dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if *probe > 0 {
		if err := probeMessage(path, *probe); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	conn, err := db.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", fmt.Errorf("open database: %w", err))
		os.Exit(1)
	}
	defer conn.Close()

	service := resource.NewService(
		repository.NewLinkRepository(conn),
		repository.NewFileRepository(conn),
		repository.NewResourceStatsRepository(conn),
		repository.NewResourceIndexRepository(conn),
		repository.NewMessageRepository(conn),
		link.NewExtractor(),
	)

	summary, err := service.RepairMediaTitles(context.Background(), nil, *dryRun)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	mode := "applied"
	if *dryRun {
		mode = "dry-run (no changes written)"
	}
	fmt.Printf("%s: affected=%d changed=%d unchanged=%d refreshed_messages=%d\n",
		mode, summary.Affected, summary.Changed, summary.Unchanged, summary.RefreshedMessages)
}

// resolveDBPath returns the database path from -db directly, otherwise loads it
// from the config (so the tool can run without a config pointing at the
// container's /data path).
func resolveDBPath(configPath, dbPath string) (string, error) {
	if strings.TrimSpace(dbPath) != "" {
		return dbPath, nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	return config.DatabasePath(cfg), nil
}

func probeMessage(dbPath string, messageID int64) error {
	conn, err := db.Open(dbPath)
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
