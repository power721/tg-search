package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	"tg-search/internal/model"
	"tg-search/internal/searchrank"
)

type ResourceIndexRepository struct {
	db     *sql.DB
	readDB *sql.DB
}

func NewResourceIndexRepository(db *sql.DB) *ResourceIndexRepository {
	return &ResourceIndexRepository{db: db, readDB: db}
}

// WithReadDB routes read-only queries (List, Stats) to a separate connection
// pool so public searches do not queue behind index refresh writes on the
// single writer connection. Falls back to the writer pool when nil.
func (r *ResourceIndexRepository) WithReadDB(readDB *sql.DB) *ResourceIndexRepository {
	if readDB != nil {
		r.readDB = readDB
	}
	return r
}

func (r *ResourceIndexRepository) readConn() *sql.DB {
	if r.readDB != nil {
		return r.readDB
	}
	return r.db
}

func (r *ResourceIndexRepository) Rebuild(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM resource_index`); err != nil {
		return fmt.Errorf("clear resource index: %w", err)
	}
	if err := r.rebuildLinks(ctx); err != nil {
		return err
	}
	if err := r.rebuildFiles(ctx); err != nil {
		return err
	}
	return nil
}

func (r *ResourceIndexRepository) RefreshMessage(ctx context.Context, messageID int64) error {
	if messageID <= 0 {
		return nil
	}
	return r.refreshMessages(ctx, []int64{messageID})
}

func (r *ResourceIndexRepository) RefreshMessages(ctx context.Context, messageIDs []int64) error {
	seen := map[int64]struct{}{}
	ids := make([]int64, 0, len(messageIDs))
	for _, id := range messageIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	return r.refreshMessages(ctx, ids)
}

func (r *ResourceIndexRepository) DeleteMessage(ctx context.Context, messageID int64) error {
	if messageID <= 0 {
		return nil
	}
	return r.refreshMessages(ctx, []int64{messageID})
}

func (r *ResourceIndexRepository) Stats(ctx context.Context) (model.ResourceIndexStats, error) {
	var stats model.ResourceIndexStats
	var updatedAt string
	err := r.readConn().QueryRowContext(ctx, `
SELECT count(*), COALESCE(max(updated_at), '0001-01-01T00:00:00Z')
FROM resource_index`).Scan(&stats.IndexedRows, &updatedAt)
	if err != nil {
		return model.ResourceIndexStats{}, fmt.Errorf("resource index stats: %w", err)
	}
	stats.UpdatedAt, err = parseResourceIndexTime(updatedAt)
	if err != nil {
		return model.ResourceIndexStats{}, fmt.Errorf("resource index stats updated_at: %w", err)
	}
	return stats, nil
}

func (r *ResourceIndexRepository) List(ctx context.Context, query model.ResourceIndexQuery) (model.ResourceIndexListResult, error) {
	limit := clampLimit(query.Limit, 50)
	if query.MaxLimit > 0 && limit > query.MaxLimit {
		limit = query.MaxLimit
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	where, args, joinFTS := resourceIndexWhere(query)
	total, err := r.count(ctx, where, args, joinFTS)
	if err != nil {
		return model.ResourceIndexListResult{}, err
	}
	grouped, err := r.grouped(ctx, where, args, joinFTS)
	if err != nil {
		return model.ResourceIndexListResult{}, err
	}
	items, err := r.list(ctx, where, args, joinFTS, resourceIndexOrder(query.Sort), limit, offset)
	if err != nil {
		return model.ResourceIndexListResult{}, err
	}
	return model.ResourceIndexListResult{Items: items, Total: total, Grouped: grouped}, nil
}

func (r *ResourceIndexRepository) rebuildLinks(ctx context.Context) error {
	rows, err := r.db.QueryContext(ctx, `
WITH ranked AS (
  SELECT
    l.id AS link_id, l.message_id, l.type, l.url, COALESCE(l.password, '') AS password,
    COALESCE(l.note, '') AS note, COALESCE(l.source_snippet, '') AS source_snippet,
    CASE
      WHEN COALESCE(l.category, '') <> '' THEN l.category
      WHEN l.type = 'magnet' THEN 'magnet'
      WHEN l.type = 'ed2k' THEN 'ed2k'
      WHEN l.type = 'url' THEN 'http'
      ELSE 'cloud_drive'
    END AS category,
    COALESCE(l.media_title, '') AS media_title, COALESCE(l.media_year, '') AS media_year,
    COALESCE(l.media_season, '') AS media_season, COALESCE(l.media_episode, '') AS media_episode,
    COALESCE(l.media_quality, '') AS media_quality, COALESCE(l.media_size, '') AS media_size,
    COALESCE(l.media_tmdb_id, '') AS media_tmdb_id, COALESCE(l.media_category, '') AS media_category,
    COALESCE(l.media_tags, '') AS media_tags,
    m.date, m.message_type, m.media_summary, m.account_id, m.channel_id, c.telegram_channel_id,
    c.title AS channel_title, c.username AS channel_username, m.telegram_message_id,
    row_number() OVER (PARTITION BY l.url ORDER BY m.date DESC, l.id DESC) AS rn
  FROM telegram_links l
  JOIN telegram_messages m ON m.id = l.message_id
  JOIN telegram_channels c ON c.id = m.channel_id
  WHERE m.deleted = 0
),
stats AS (
  SELECT
    l.url,
    count(DISTINCT m.channel_id) AS source_channel_count,
    count(DISTINCT m.id) AS message_count,
    count(DISTINCT COALESCE(NULLIF(l.type, ''), 'url')) AS provider_count
  FROM telegram_links l
  JOIN telegram_messages m ON m.id = l.message_id
  WHERE m.deleted = 0
  GROUP BY l.url
)
SELECT ranked.link_id, ranked.message_id, ranked.type, ranked.url, ranked.password, ranked.note, ranked.source_snippet, ranked.category,
       ranked.media_title, ranked.media_year, ranked.media_season, ranked.media_episode, ranked.media_quality, ranked.media_size, ranked.media_tmdb_id, ranked.media_category, ranked.media_tags,
       ranked.date, ranked.message_type, ranked.media_summary, ranked.account_id, ranked.channel_id, ranked.telegram_channel_id, ranked.channel_title, ranked.channel_username, ranked.telegram_message_id,
       stats.source_channel_count, stats.message_count, stats.provider_count
FROM ranked
JOIN stats ON stats.url = ranked.url
WHERE rn = 1`)
	if err != nil {
		return fmt.Errorf("query resource index links: %w", err)
	}
	now := time.Now().UTC()
	items := []model.ResourceIndexItem{}
	for rows.Next() {
		var item model.ResourceIndexItem
		var linkID int64
		if err := rows.Scan(
			&linkID, &item.SourceMessageID, &item.Type, &item.URL, &item.Password, &item.Note, &item.SourceSnippet, &item.Category,
			&item.MediaTitle, &item.MediaYear, &item.MediaSeason, &item.MediaEpisode, &item.MediaQuality, &item.MediaSize, &item.MediaTMDBID, &item.MediaCategory, &item.MediaTags,
			&item.Datetime, &item.MessageType, &item.MediaSummary, &item.AccountID, &item.ChannelID, &item.TelegramChannelID, &item.ChannelTitle, &item.ChannelUsername, &item.TelegramMessageID,
			&item.SourceChannelCount, &item.MessageCount, &item.ProviderCount,
		); err != nil {
			return err
		}
		item.ResourceID = "link:" + item.URL
		item.Kind = "link"
		item.SourceKey = item.ResourceID
		item.Title = firstNonEmptyString(item.MediaTitle, item.Note, item.URL)
		item.Score = resourceIndexScore(item, now)
		item.UpdatedAt = now
		items = append(items, item)
		_ = linkID
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		if err := r.upsert(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *ResourceIndexRepository) rebuildFiles(ctx context.Context) error {
	rows, err := r.db.QueryContext(ctx, `
SELECT f.id, f.message_id, f.telegram_file_id, f.file_name, f.extension, f.mime_type, f.size_bytes, f.category,
       m.date, m.message_type, m.media_summary, m.account_id, m.channel_id, c.telegram_channel_id,
       c.title, c.username, m.telegram_message_id, mc.text
FROM telegram_files f
JOIN telegram_messages m ON m.id = f.message_id
JOIN telegram_message_contents mc ON mc.message_id = m.id
JOIN telegram_channels c ON c.id = m.channel_id
WHERE m.deleted = 0 AND f.category <> 'image'`)
	if err != nil {
		return fmt.Errorf("query resource index files: %w", err)
	}
	now := time.Now().UTC()
	items := []model.ResourceIndexItem{}
	for rows.Next() {
		var item model.ResourceIndexItem
		var fileID int64
		if err := rows.Scan(
			&fileID, &item.SourceMessageID, &item.TelegramFileID, &item.FileName, &item.Extension, &item.MimeType, &item.SizeBytes, &item.Type,
			&item.Datetime, &item.MessageType, &item.MediaSummary, &item.AccountID, &item.ChannelID, &item.TelegramChannelID,
			&item.ChannelTitle, &item.ChannelUsername, &item.TelegramMessageID, &item.SourceSnippet,
		); err != nil {
			return err
		}
		item.ResourceID = fmt.Sprintf("file:%d", fileID)
		item.Kind = "file"
		item.SourceKey = item.ResourceID
		item.Category = "files"
		item.Title = item.FileName
		item.SourceChannelCount = 1
		item.MessageCount = 1
		item.ProviderCount = 1
		item.Score = resourceIndexScore(item, now)
		item.UpdatedAt = now
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		if err := r.upsert(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *ResourceIndexRepository) refreshMessages(ctx context.Context, messageIDs []int64) error {
	urls := map[string]struct{}{}
	for _, messageID := range messageIDs {
		if err := r.collectIndexedURLs(ctx, urls, messageID); err != nil {
			return err
		}
		if err := r.collectSourceURLs(ctx, urls, messageID); err != nil {
			return err
		}
		if _, err := r.db.ExecContext(ctx, `DELETE FROM resource_index WHERE kind = 'file' AND source_message_id = ?`, messageID); err != nil {
			return fmt.Errorf("delete indexed files for message %d: %w", messageID, err)
		}
	}
	for url := range urls {
		if err := r.refreshURL(ctx, url); err != nil {
			return err
		}
	}
	for _, messageID := range messageIDs {
		if err := r.refreshFilesForMessage(ctx, messageID); err != nil {
			return err
		}
	}
	return nil
}

func (r *ResourceIndexRepository) collectIndexedURLs(ctx context.Context, urls map[string]struct{}, messageID int64) error {
	rows, err := r.db.QueryContext(ctx, `SELECT url FROM resource_index WHERE kind = 'link' AND source_message_id = ?`, messageID)
	if err != nil {
		return fmt.Errorf("collect indexed urls: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return err
		}
		if strings.TrimSpace(url) != "" {
			urls[url] = struct{}{}
		}
	}
	return rows.Err()
}

func (r *ResourceIndexRepository) collectSourceURLs(ctx context.Context, urls map[string]struct{}, messageID int64) error {
	rows, err := r.db.QueryContext(ctx, `SELECT url FROM telegram_links WHERE message_id = ?`, messageID)
	if err != nil {
		return fmt.Errorf("collect source urls: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return err
		}
		if strings.TrimSpace(url) != "" {
			urls[url] = struct{}{}
		}
	}
	return rows.Err()
}

func (r *ResourceIndexRepository) refreshURL(ctx context.Context, url string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM resource_index WHERE kind = 'link' AND url = ?`, url); err != nil {
		return fmt.Errorf("delete indexed url %s: %w", url, err)
	}
	item, found, err := r.linkItemByURL(ctx, url)
	if err != nil || !found {
		return err
	}
	return r.upsert(ctx, item)
}

func (r *ResourceIndexRepository) linkItemByURL(ctx context.Context, url string) (model.ResourceIndexItem, bool, error) {
	var item model.ResourceIndexItem
	var linkID int64
	err := r.db.QueryRowContext(ctx, `
WITH stats AS (
  SELECT
    l.url,
    count(DISTINCT m.channel_id) AS source_channel_count,
    count(DISTINCT m.id) AS message_count,
    count(DISTINCT COALESCE(NULLIF(l.type, ''), 'url')) AS provider_count
  FROM telegram_links l
  JOIN telegram_messages m ON m.id = l.message_id
  WHERE m.deleted = 0 AND l.url = ?
  GROUP BY l.url
)
SELECT l.id, l.message_id, l.type, l.url, COALESCE(l.password, ''), COALESCE(l.note, ''), COALESCE(l.source_snippet, ''),
       CASE
         WHEN COALESCE(l.category, '') <> '' THEN l.category
         WHEN l.type = 'magnet' THEN 'magnet'
         WHEN l.type = 'ed2k' THEN 'ed2k'
         WHEN l.type = 'url' THEN 'http'
         ELSE 'cloud_drive'
       END AS category,
       COALESCE(l.media_title, ''), COALESCE(l.media_year, ''), COALESCE(l.media_season, ''), COALESCE(l.media_episode, ''),
       COALESCE(l.media_quality, ''), COALESCE(l.media_size, ''), COALESCE(l.media_tmdb_id, ''), COALESCE(l.media_category, ''), COALESCE(l.media_tags, ''),
       m.date, m.message_type, m.media_summary, m.account_id, m.channel_id, c.telegram_channel_id, c.title, c.username, m.telegram_message_id,
       stats.source_channel_count, stats.message_count, stats.provider_count
FROM telegram_links l
JOIN telegram_messages m ON m.id = l.message_id
JOIN telegram_channels c ON c.id = m.channel_id
JOIN stats ON stats.url = l.url
WHERE m.deleted = 0 AND l.url = ?
ORDER BY m.date DESC, l.id DESC
LIMIT 1`, url, url).Scan(
		&linkID, &item.SourceMessageID, &item.Type, &item.URL, &item.Password, &item.Note, &item.SourceSnippet, &item.Category,
		&item.MediaTitle, &item.MediaYear, &item.MediaSeason, &item.MediaEpisode, &item.MediaQuality, &item.MediaSize, &item.MediaTMDBID, &item.MediaCategory, &item.MediaTags,
		&item.Datetime, &item.MessageType, &item.MediaSummary, &item.AccountID, &item.ChannelID, &item.TelegramChannelID, &item.ChannelTitle, &item.ChannelUsername, &item.TelegramMessageID,
		&item.SourceChannelCount, &item.MessageCount, &item.ProviderCount,
	)
	if err == sql.ErrNoRows {
		return model.ResourceIndexItem{}, false, nil
	}
	if err != nil {
		return model.ResourceIndexItem{}, false, fmt.Errorf("find link item by url: %w", err)
	}
	now := time.Now().UTC()
	item.ResourceID = "link:" + item.URL
	item.Kind = "link"
	item.SourceKey = item.ResourceID
	item.Title = firstNonEmptyString(item.MediaTitle, item.Note, item.URL)
	item.Score = resourceIndexScore(item, now)
	item.UpdatedAt = now
	_ = linkID
	return item, true, nil
}

func (r *ResourceIndexRepository) refreshFilesForMessage(ctx context.Context, messageID int64) error {
	rows, err := r.db.QueryContext(ctx, `
SELECT f.id, f.message_id, f.telegram_file_id, f.file_name, f.extension, f.mime_type, f.size_bytes, f.category,
       m.date, m.message_type, m.media_summary, m.account_id, m.channel_id, c.telegram_channel_id,
       c.title, c.username, m.telegram_message_id, mc.text
FROM telegram_files f
JOIN telegram_messages m ON m.id = f.message_id
JOIN telegram_message_contents mc ON mc.message_id = m.id
JOIN telegram_channels c ON c.id = m.channel_id
WHERE m.deleted = 0 AND f.category <> 'image' AND f.message_id = ?`, messageID)
	if err != nil {
		return fmt.Errorf("query message files for index: %w", err)
	}
	now := time.Now().UTC()
	items := []model.ResourceIndexItem{}
	for rows.Next() {
		var item model.ResourceIndexItem
		var fileID int64
		if err := rows.Scan(
			&fileID, &item.SourceMessageID, &item.TelegramFileID, &item.FileName, &item.Extension, &item.MimeType, &item.SizeBytes, &item.Type,
			&item.Datetime, &item.MessageType, &item.MediaSummary, &item.AccountID, &item.ChannelID, &item.TelegramChannelID,
			&item.ChannelTitle, &item.ChannelUsername, &item.TelegramMessageID, &item.SourceSnippet,
		); err != nil {
			return err
		}
		item.ResourceID = fmt.Sprintf("file:%d", fileID)
		item.Kind = "file"
		item.SourceKey = item.ResourceID
		item.Category = "files"
		item.Title = item.FileName
		item.SourceChannelCount = 1
		item.MessageCount = 1
		item.ProviderCount = 1
		item.Score = resourceIndexScore(item, now)
		item.UpdatedAt = now
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		if err := r.upsert(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *ResourceIndexRepository) upsert(ctx context.Context, item model.ResourceIndexItem) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO resource_index (
  resource_id, kind, source_key, source_message_id, url, type, category, password, note, title, source_snippet,
  telegram_file_id, file_name, extension, mime_type, size_bytes,
  media_title, media_year, media_season, media_episode, media_quality, media_size, media_tmdb_id, media_category, media_tags, media_summary,
  datetime, account_id, channel_id, telegram_channel_id, channel_title, channel_username, telegram_message_id, message_type,
  source_channel_count, message_count, provider_count, score, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(resource_id) DO UPDATE SET
  kind = excluded.kind,
  source_key = excluded.source_key,
  source_message_id = excluded.source_message_id,
  url = excluded.url,
  type = excluded.type,
  category = excluded.category,
  password = excluded.password,
  note = excluded.note,
  title = excluded.title,
  source_snippet = excluded.source_snippet,
  telegram_file_id = excluded.telegram_file_id,
  file_name = excluded.file_name,
  extension = excluded.extension,
  mime_type = excluded.mime_type,
  size_bytes = excluded.size_bytes,
  media_title = excluded.media_title,
  media_year = excluded.media_year,
  media_season = excluded.media_season,
  media_episode = excluded.media_episode,
  media_quality = excluded.media_quality,
  media_size = excluded.media_size,
  media_tmdb_id = excluded.media_tmdb_id,
  media_category = excluded.media_category,
  media_tags = excluded.media_tags,
  media_summary = excluded.media_summary,
  datetime = excluded.datetime,
  account_id = excluded.account_id,
  channel_id = excluded.channel_id,
  telegram_channel_id = excluded.telegram_channel_id,
  channel_title = excluded.channel_title,
  channel_username = excluded.channel_username,
  telegram_message_id = excluded.telegram_message_id,
  message_type = excluded.message_type,
  source_channel_count = excluded.source_channel_count,
  message_count = excluded.message_count,
  provider_count = excluded.provider_count,
  score = excluded.score,
  updated_at = excluded.updated_at`,
		item.ResourceID, item.Kind, item.SourceKey, item.SourceMessageID, item.URL, item.Type, item.Category, item.Password, item.Note, item.Title, item.SourceSnippet,
		item.TelegramFileID, item.FileName, item.Extension, item.MimeType, item.SizeBytes,
		item.MediaTitle, item.MediaYear, item.MediaSeason, item.MediaEpisode, item.MediaQuality, item.MediaSize, item.MediaTMDBID, item.MediaCategory, item.MediaTags, item.MediaSummary,
		item.Datetime, item.AccountID, item.ChannelID, item.TelegramChannelID, item.ChannelTitle, item.ChannelUsername, item.TelegramMessageID, item.MessageType,
		positiveInt(item.SourceChannelCount), positiveInt(item.MessageCount), positiveInt(item.ProviderCount), item.Score, item.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert resource index %s: %w", item.ResourceID, err)
	}
	return nil
}

func resourceIndexWhere(query model.ResourceIndexQuery) ([]string, []any, bool) {
	where := []string{"1 = 1"}
	args := []any{}
	joinFTS := strings.TrimSpace(query.Keyword) != ""
	if joinFTS {
		where = append(where, "resource_index_fts MATCH ?")
		args = append(args, fts5ResourceQuery(query.Keyword))
	}
	if query.Type != "" {
		where = append(where, "ri.type = ?")
		args = append(args, query.Type)
	}
	if len(query.Types) > 0 {
		markers := strings.TrimRight(strings.Repeat("?,", len(query.Types)), ",")
		where = append(where, "ri.type IN ("+markers+")")
		for _, value := range query.Types {
			args = append(args, value)
		}
	}
	if query.Category != "" {
		where = append(where, "ri.category = ?")
		args = append(args, query.Category)
	}
	if len(query.Categories) > 0 {
		markers := strings.TrimRight(strings.Repeat("?,", len(query.Categories)), ",")
		where = append(where, "ri.category IN ("+markers+")")
		for _, value := range query.Categories {
			args = append(args, value)
		}
	}
	if len(query.Filters) > 0 {
		filterWhere := make([]string, 0, len(query.Filters))
		for _, filter := range query.Filters {
			filterParts := []string{}
			if filter.Category != "" {
				filterParts = append(filterParts, "ri.category = ?")
				args = append(args, filter.Category)
			}
			if filter.Type != "" {
				filterParts = append(filterParts, "ri.type = ?")
				args = append(args, filter.Type)
			}
			if len(filterParts) == 0 {
				continue
			}
			filterWhere = append(filterWhere, "("+strings.Join(filterParts, " AND ")+")")
		}
		if len(filterWhere) > 0 {
			where = append(where, "("+strings.Join(filterWhere, " OR ")+")")
		}
	}
	if query.AccountID > 0 {
		where = append(where, "ri.account_id = ?")
		args = append(args, query.AccountID)
	}
	if query.ChannelID > 0 {
		where = append(where, "ri.channel_id = ?")
		args = append(args, query.ChannelID)
	}
	if query.Extension != "" {
		where = append(where, "ri.extension = ?")
		args = append(args, normalizeResourceIndexExtension(query.Extension))
	}
	if query.DateFrom != nil {
		where = append(where, "ri.datetime >= ?")
		args = append(args, *query.DateFrom)
	}
	if query.DateTo != nil {
		where = append(where, "ri.datetime < ?")
		args = append(args, *query.DateTo)
	}
	return where, args, joinFTS
}

func (r *ResourceIndexRepository) count(ctx context.Context, where []string, args []any, joinFTS bool) (int, error) {
	query := `SELECT count(*) FROM resource_index ri`
	if joinFTS {
		query += ` JOIN resource_index_fts ON resource_index_fts.rowid = ri.id`
	}
	query += ` WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := r.readConn().QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count resource index: %w", err)
	}
	return total, nil
}

func (r *ResourceIndexRepository) grouped(ctx context.Context, where []string, args []any, joinFTS bool) (map[string]int, error) {
	query := `SELECT ri.category, count(*) FROM resource_index ri`
	if joinFTS {
		query += ` JOIN resource_index_fts ON resource_index_fts.rowid = ri.id`
	}
	query += ` WHERE ` + strings.Join(where, " AND ") + ` GROUP BY ri.category`
	rows, err := r.readConn().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("group resource index: %w", err)
	}
	defer rows.Close()
	grouped := map[string]int{"cloud_drive": 0, "magnet": 0, "ed2k": 0, "http": 0, "files": 0}
	total := 0
	for rows.Next() {
		var category string
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			return nil, err
		}
		grouped[category] = count
		total += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	grouped["_total"] = total
	return grouped, nil
}

func (r *ResourceIndexRepository) list(ctx context.Context, where []string, args []any, joinFTS bool, orderBy string, limit int, offset int) ([]model.ResourceIndexItem, error) {
	query := resourceIndexSelectSQL() + ` FROM resource_index ri`
	if joinFTS {
		query += ` JOIN resource_index_fts ON resource_index_fts.rowid = ri.id`
	}
	query += ` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + orderBy + ` LIMIT ? OFFSET ?`
	listArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.readConn().QueryContext(ctx, query, listArgs...)
	if err != nil {
		return nil, fmt.Errorf("list resource index: %w", err)
	}
	defer rows.Close()
	var out []model.ResourceIndexItem
	for rows.Next() {
		item, err := scanResourceIndexItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func resourceIndexSelectSQL() string {
	return `SELECT ri.id, ri.resource_id, ri.kind, ri.source_key, ri.source_message_id, ri.url, ri.type, ri.category, ri.password, ri.note, ri.title, ri.source_snippet,
       ri.telegram_file_id, ri.file_name, ri.extension, ri.mime_type, ri.size_bytes,
       ri.media_title, ri.media_year, ri.media_season, ri.media_episode, ri.media_quality, ri.media_size, ri.media_tmdb_id, ri.media_category, ri.media_tags, ri.media_summary,
       ri.datetime, ri.account_id, ri.channel_id, ri.telegram_channel_id, ri.channel_title, ri.channel_username, ri.telegram_message_id, ri.message_type,
       ri.source_channel_count, ri.message_count, ri.provider_count, ri.score, ri.updated_at`
}

func scanResourceIndexItem(row interface {
	Scan(...any) error
}) (model.ResourceIndexItem, error) {
	var item model.ResourceIndexItem
	err := row.Scan(
		&item.ID, &item.ResourceID, &item.Kind, &item.SourceKey, &item.SourceMessageID, &item.URL, &item.Type, &item.Category, &item.Password, &item.Note, &item.Title, &item.SourceSnippet,
		&item.TelegramFileID, &item.FileName, &item.Extension, &item.MimeType, &item.SizeBytes,
		&item.MediaTitle, &item.MediaYear, &item.MediaSeason, &item.MediaEpisode, &item.MediaQuality, &item.MediaSize, &item.MediaTMDBID, &item.MediaCategory, &item.MediaTags, &item.MediaSummary,
		&item.Datetime, &item.AccountID, &item.ChannelID, &item.TelegramChannelID, &item.ChannelTitle, &item.ChannelUsername, &item.TelegramMessageID, &item.MessageType,
		&item.SourceChannelCount, &item.MessageCount, &item.ProviderCount, &item.Score, &item.UpdatedAt,
	)
	if err != nil {
		return model.ResourceIndexItem{}, err
	}
	return item, nil
}

func resourceIndexOrder(sort string) string {
	if sort == "hot" {
		return "ri.score DESC, ri.datetime DESC, ri.resource_id DESC"
	}
	if sort == "date_asc" {
		return "ri.datetime ASC, ri.resource_id ASC"
	}
	return "ri.datetime DESC, ri.resource_id DESC"
}

func parseResourceIndexTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time %q", value)
}

func fts5ResourceQuery(query string) string {
	tokens := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	quoted := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(token, `"`, `""`)+`"`)
	}
	if len(quoted) == 0 {
		return `"__tg_search_no_match__"`
	}
	return strings.Join(quoted, " ")
}

func resourceIndexScore(item model.ResourceIndexItem, now time.Time) int {
	return positiveInt(item.SourceChannelCount)*10 +
		positiveInt(item.MessageCount)*3 +
		positiveInt(item.ProviderCount)*6 +
		resourceIndexRecencyScore(item.Datetime, now) +
		resourceIndexCategoryScore(item) +
		searchrank.MetadataScore(item.MediaTitle, item.MediaYear, item.MediaSeason, item.MediaEpisode, item.MediaQuality, item.MediaSize, item.MediaTMDBID, item.MediaCategory, item.MediaTags)
}

func resourceIndexRecencyScore(publishedAt time.Time, now time.Time) int {
	if publishedAt.IsZero() {
		return 0
	}
	publishedAt = publishedAt.UTC()
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch {
	case !publishedAt.Before(today):
		return 30
	case !publishedAt.Before(now.AddDate(0, 0, -7)):
		return 20
	case !publishedAt.Before(now.AddDate(0, 0, -30)):
		return 10
	default:
		return 0
	}
}

func resourceIndexCategoryScore(item model.ResourceIndexItem) int {
	switch item.Category {
	case "cloud_drive":
		return 90 + resourceIndexProviderScore(item.Type)
	case "video":
		return 75
	case "files":
		return 55
	case "magnet":
		return 45
	case "ed2k":
		return 40
	case "http":
		return 15
	default:
		if item.Kind == "file" {
			return 50
		}
		return resourceIndexProviderScore(item.Type)
	}
}

func resourceIndexProviderScore(typ string) int {
	switch typ {
	case "quark", "aliyun", "baidu", "115", "uc", "xunlei", "tianyi", "mobile", "123", "pikpak", "guangya":
		return 35
	default:
		return 0
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func positiveInt(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func normalizeResourceIndexExtension(extension string) string {
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension == "" || strings.HasPrefix(extension, ".") {
		return extension
	}
	return "." + extension
}
