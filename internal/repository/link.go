package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"tg-search/internal/model"
	"tg-search/internal/searchrank"
)

type LinkRepository struct {
	db *sql.DB
}

type LinkResourceStats struct {
	URL                string
	SourceChannelCount int
	MessageCount       int
	ProviderCount      int
}

type mergedLinkCandidate struct {
	id  int64
	typ string
	model.MergedLink
}

const linkResourceCategoryExpr = `CASE
           WHEN COALESCE(l.category, '') <> '' THEN l.category
           WHEN l.type = 'magnet' THEN 'magnet'
           WHEN l.type = 'ed2k' THEN 'ed2k'
           WHEN l.type = 'url' THEN 'http'
           ELSE 'cloud_drive'
         END`

func NewLinkRepository(db *sql.DB) *LinkRepository {
	return &LinkRepository{db: db}
}

func (r *LinkRepository) SaveBatch(ctx context.Context, messageID int64, links []model.Link) ([]model.Link, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	out, err := r.SaveBatchTx(ctx, tx, messageID, links)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *LinkRepository) SaveBatchTx(ctx context.Context, tx *sql.Tx, messageID int64, links []model.Link) ([]model.Link, error) {
	out := make([]model.Link, 0, len(links))
	now := time.Now().UTC()
	for _, link := range links {
		if link.Type == "" {
			link.Type = "url"
		}
		if link.Category == "" {
			link.Category = linkCategory(link)
		}
		err := tx.QueryRowContext(ctx, `
INSERT INTO telegram_links (
  message_id, type, url, password, note, source_snippet, category,
  media_title, media_year, media_season, media_episode, media_quality, media_size, media_tmdb_id, media_category, media_tags,
  created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id, type, url) DO UPDATE SET
  password = excluded.password,
  note = excluded.note,
  source_snippet = excluded.source_snippet,
  category = excluded.category,
  media_title = excluded.media_title,
  media_year = excluded.media_year,
  media_season = excluded.media_season,
  media_episode = excluded.media_episode,
  media_quality = excluded.media_quality,
  media_size = excluded.media_size,
  media_tmdb_id = excluded.media_tmdb_id,
  media_category = excluded.media_category,
  media_tags = excluded.media_tags
RETURNING id, created_at`,
			messageID, link.Type, link.URL, link.Password, link.Note, link.SourceSnippet, link.Category,
			link.MediaTitle, link.MediaYear, link.MediaSeason, link.MediaEpisode, link.MediaQuality, link.MediaSize, link.MediaTMDBID, link.MediaCategory, link.MediaTags,
			now,
		).Scan(&link.ID, &link.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("save link %s: %w", link.URL, err)
		}
		link.MessageID = messageID
		out = append(out, link)
	}
	return out, nil
}

func (r *LinkRepository) ReplaceForMessageTx(ctx context.Context, tx *sql.Tx, messageID int64, links []model.Link) ([]model.Link, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_links WHERE message_id = ?`, messageID); err != nil {
		return nil, fmt.Errorf("delete old links: %w", err)
	}
	return r.SaveBatchTx(ctx, tx, messageID, links)
}

func (r *LinkRepository) ListByMessage(ctx context.Context, messageID int64) ([]model.Link, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, message_id, type, url, COALESCE(password, ''), COALESCE(note, ''),
       COALESCE(source_snippet, ''), COALESCE(category, ''),
       COALESCE(media_title, ''), COALESCE(media_year, ''), COALESCE(media_season, ''), COALESCE(media_episode, ''),
       COALESCE(media_quality, ''), COALESCE(media_size, ''), COALESCE(media_tmdb_id, ''), COALESCE(media_category, ''), COALESCE(media_tags, ''),
       created_at
FROM telegram_links
WHERE message_id = ?
ORDER BY id ASC`, messageID)
	if err != nil {
		return nil, fmt.Errorf("list links by message: %w", err)
	}
	defer rows.Close()
	out := []model.Link{}
	for rows.Next() {
		var link model.Link
		if err := rows.Scan(&link.ID, &link.MessageID, &link.Type, &link.URL, &link.Password, &link.Note, &link.SourceSnippet, &link.Category, &link.MediaTitle, &link.MediaYear, &link.MediaSeason, &link.MediaEpisode, &link.MediaQuality, &link.MediaSize, &link.MediaTMDBID, &link.MediaCategory, &link.MediaTags, &link.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

func (r *LinkRepository) UpdateMediaMetadata(ctx context.Context, link model.Link) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE telegram_links
SET media_title = ?,
    media_year = ?,
    media_season = ?,
    media_episode = ?,
    media_quality = ?,
    media_size = ?,
    media_tmdb_id = ?,
    media_category = ?,
    media_tags = ?
WHERE id = ?`,
		link.MediaTitle,
		link.MediaYear,
		link.MediaSeason,
		link.MediaEpisode,
		link.MediaQuality,
		link.MediaSize,
		link.MediaTMDBID,
		link.MediaCategory,
		link.MediaTags,
		link.ID,
	)
	if err != nil {
		return fmt.Errorf("update link media metadata: %w", err)
	}
	return requireRows(res, "link not found")
}

// UpdateMediaTitle updates only media_title for a link by id.
func (r *LinkRepository) UpdateMediaTitle(ctx context.Context, id int64, title string) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE telegram_links SET media_title = ? WHERE id = ?`, title, id); err != nil {
		return fmt.Errorf("update link media title: %w", err)
	}
	return nil
}

// UpdateMediaTitleAndNote updates media_title and note for a link by id.
func (r *LinkRepository) UpdateMediaTitleAndNote(ctx context.Context, id int64, title, note string) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE telegram_links SET media_title = ?, note = ? WHERE id = ?`, title, note, id); err != nil {
		return fmt.Errorf("update link media title and note: %w", err)
	}
	return nil
}

// MediaTitleCandidate is a link row whose media_title may have been clobbered by
// a provider label, along with the data needed to re-derive the real title.
type MediaTitleCandidate struct {
	ID         int64
	MessageID  int64
	URL        string
	MediaTitle string
	Note       string
}

// ListMediaTitleLabelCandidates returns link rows exhibiting the provider-label
// bug signature: media_title is non-empty, equals the link note, and the URL's
// own line (source_snippet) begins with "<media_title><:>". This excludes
// correctly-titled rows whose title merely coincides with the note (their
// snippet starts with a generic label like 链接：/夸克：).
func (r *LinkRepository) ListMediaTitleLabelCandidates(ctx context.Context) ([]MediaTitleCandidate, error) {
	rows, err := r.db.QueryContext(ctx, `
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
		return nil, fmt.Errorf("list media-title label candidates: %w", err)
	}
	defer rows.Close()
	var out []MediaTitleCandidate
	for rows.Next() {
		var c MediaTitleCandidate
		if err := rows.Scan(&c.ID, &c.MessageID, &c.URL, &c.MediaTitle, &c.Note); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *LinkRepository) Search(ctx context.Context, params LinkSearchParams) ([]model.LinkResult, error) {
	limit := clampLimit(params.Limit, 50)
	where, args := linkSearchWhere(params)
	args = append(args, limit, params.Offset)
	query := `
SELECT l.id, l.message_id, l.type, l.url, COALESCE(l.password, ''), COALESCE(l.note, ''),
       COALESCE(l.source_snippet, ''), COALESCE(l.category, ''),
       COALESCE(l.media_title, ''), COALESCE(l.media_year, ''), COALESCE(l.media_season, ''), COALESCE(l.media_episode, ''),
       COALESCE(l.media_quality, ''), COALESCE(l.media_size, ''), COALESCE(l.media_tmdb_id, ''), COALESCE(l.media_category, ''), COALESCE(l.media_tags, ''),
       l.created_at,
       mc.text, m.date, m.message_type, m.media_summary, m.account_id, m.channel_id, c.telegram_channel_id, c.title, c.username, m.telegram_message_id
FROM telegram_links l
JOIN telegram_messages m ON m.id = l.message_id
JOIN telegram_message_contents mc ON mc.message_id = m.id
JOIN telegram_channels c ON c.id = m.channel_id
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY ` + dateOrderBy(params.Sort, "m.date", "l.id") + `
LIMIT ? OFFSET ?`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search links: %w", err)
	}
	defer rows.Close()
	var out []model.LinkResult
	for rows.Next() {
		var item model.LinkResult
		if err := rows.Scan(&item.ID, &item.MessageID, &item.Type, &item.URL, &item.Password, &item.Note, &item.SourceSnippet, &item.Category, &item.MediaTitle, &item.MediaYear, &item.MediaSeason, &item.MediaEpisode, &item.MediaQuality, &item.MediaSize, &item.MediaTMDBID, &item.MediaCategory, &item.MediaTags, &item.CreatedAt, &item.MessageText, &item.MessageDate, &item.MessageType, &item.MediaSummary, &item.AccountID, &item.ChannelID, &item.TelegramChannelID, &item.ChannelTitle, &item.ChannelUsername, &item.TelegramMessageID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *LinkRepository) SearchResources(ctx context.Context, params LinkSearchParams) ([]model.LinkResult, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	where, args := linkSearchWhere(params)
	args = append(args, limit, params.Offset)
	query := `
SELECT id, message_id, type, url, password, note, source_snippet, category,
       media_title, media_year, media_season, media_episode, media_quality, media_size, media_tmdb_id, media_category, media_tags,
       created_at,
       message_text, message_date, message_type, media_summary, account_id, channel_id, telegram_channel_id, channel_title, channel_username, telegram_message_id
FROM (
  SELECT l.id, l.message_id, l.type, l.url, COALESCE(l.password, '') AS password,
         COALESCE(l.note, '') AS note, COALESCE(l.source_snippet, '') AS source_snippet,
         ` + linkResourceCategoryExpr + ` AS category,
         COALESCE(l.media_title, '') AS media_title, COALESCE(l.media_year, '') AS media_year,
         COALESCE(l.media_season, '') AS media_season, COALESCE(l.media_episode, '') AS media_episode,
         COALESCE(l.media_quality, '') AS media_quality, COALESCE(l.media_size, '') AS media_size,
         COALESCE(l.media_tmdb_id, '') AS media_tmdb_id, COALESCE(l.media_category, '') AS media_category,
         COALESCE(l.media_tags, '') AS media_tags,
         l.created_at, mc.text AS message_text, m.date AS message_date, m.message_type, m.media_summary, m.account_id,
         m.channel_id, c.telegram_channel_id, c.title AS channel_title, c.username AS channel_username, m.telegram_message_id,
         row_number() OVER (PARTITION BY l.url ORDER BY m.date DESC, l.id DESC) AS rn
  FROM telegram_links l
  JOIN telegram_messages m ON m.id = l.message_id
  JOIN telegram_message_contents mc ON mc.message_id = m.id
  JOIN telegram_channels c ON c.id = m.channel_id
  WHERE ` + strings.Join(where, " AND ") + `
)
WHERE rn = 1
ORDER BY ` + dateOrderBy(params.Sort, "message_date", "id") + `
LIMIT ? OFFSET ?`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search resource links: %w", err)
	}
	defer rows.Close()
	var out []model.LinkResult
	for rows.Next() {
		var item model.LinkResult
		if err := rows.Scan(&item.ID, &item.MessageID, &item.Type, &item.URL, &item.Password, &item.Note, &item.SourceSnippet, &item.Category, &item.MediaTitle, &item.MediaYear, &item.MediaSeason, &item.MediaEpisode, &item.MediaQuality, &item.MediaSize, &item.MediaTMDBID, &item.MediaCategory, &item.MediaTags, &item.CreatedAt, &item.MessageText, &item.MessageDate, &item.MessageType, &item.MediaSummary, &item.AccountID, &item.ChannelID, &item.TelegramChannelID, &item.ChannelTitle, &item.ChannelUsername, &item.TelegramMessageID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *LinkRepository) CountSearch(ctx context.Context, params LinkSearchParams) (int, error) {
	where, args := linkSearchWhere(params)
	// Use COUNT(DISTINCT url) for deduplication - much faster than window functions
	query := `
SELECT count(DISTINCT l.url)
FROM telegram_links l
JOIN telegram_messages m ON m.id = l.message_id
JOIN telegram_message_contents mc ON mc.message_id = m.id
WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count search links: %w", err)
	}
	return total, nil
}

func (r *LinkRepository) CountByResourceCategory(ctx context.Context, params LinkSearchParams) (map[string]int, error) {
	where, args := linkSearchWhere(params)
	query := `
SELECT category, count(*)
FROM (
  SELECT
    l.url,
    ` + linkResourceCategoryExpr + ` AS category,
    row_number() OVER (PARTITION BY l.url ORDER BY m.date DESC, l.id DESC) AS rn
  FROM telegram_links l
  JOIN telegram_messages m ON m.id = l.message_id
  JOIN telegram_message_contents mc ON mc.message_id = m.id
  WHERE ` + strings.Join(where, " AND ") + `
)
WHERE rn = 1
GROUP BY category`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("count resource links by category: %w", err)
	}
	defer rows.Close()

	grouped := map[string]int{}
	for rows.Next() {
		var category string
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			return nil, err
		}
		grouped[category] = count
	}
	return grouped, rows.Err()
}

func (r *LinkRepository) CountByType(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT COALESCE(NULLIF(type, ''), 'url') AS type, count(*)
FROM telegram_links
JOIN telegram_messages m ON m.id = telegram_links.message_id
WHERE m.deleted = 0
GROUP BY COALESCE(NULLIF(type, ''), 'url')`)
	if err != nil {
		return nil, fmt.Errorf("count links by type: %w", err)
	}
	defer rows.Close()

	grouped := map[string]int{}
	for rows.Next() {
		var typ string
		var count int
		if err := rows.Scan(&typ, &count); err != nil {
			return nil, err
		}
		grouped[typ] = count
	}
	return grouped, rows.Err()
}

func (r *LinkRepository) ResourceStatsByURL(ctx context.Context, urls []string) (map[string]LinkResourceStats, error) {
	unique := map[string]struct{}{}
	ordered := []string{}
	for _, url := range urls {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}
		if _, ok := unique[url]; ok {
			continue
		}
		unique[url] = struct{}{}
		ordered = append(ordered, url)
	}
	if len(ordered) == 0 {
		return map[string]LinkResourceStats{}, nil
	}

	markers := make([]string, 0, len(ordered))
	args := make([]any, 0, len(ordered))
	for _, url := range ordered {
		markers = append(markers, "?")
		args = append(args, url)
	}
	query := `
SELECT l.url,
       count(DISTINCT m.channel_id) AS source_channel_count,
       count(DISTINCT m.id) AS message_count,
       count(DISTINCT COALESCE(NULLIF(l.type, ''), 'url')) AS provider_count
FROM telegram_links l
JOIN telegram_messages m ON m.id = l.message_id
WHERE m.deleted = 0 AND l.url IN (` + strings.Join(markers, ",") + `)
GROUP BY l.url`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("resource stats by url: %w", err)
	}
	defer rows.Close()

	out := map[string]LinkResourceStats{}
	for rows.Next() {
		var item LinkResourceStats
		if err := rows.Scan(&item.URL, &item.SourceChannelCount, &item.MessageCount, &item.ProviderCount); err != nil {
			return nil, err
		}
		out[item.URL] = item
	}
	return out, rows.Err()
}

func (r *LinkRepository) DeleteResourceByURL(ctx context.Context, url string) (int64, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM telegram_links WHERE url = ?`, url)
	if err != nil {
		return 0, fmt.Errorf("delete resource link: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete resource link rows affected: %w", err)
	}
	return affected, nil
}

func linkSearchWhere(params LinkSearchParams) ([]string, []any) {
	where := []string{`m.deleted = 0`}
	args := []any{}
	if params.Type != "" {
		where = append(where, `l.type = ?`)
		args = append(args, params.Type)
	}
	if params.Category != "" {
		where = append(where, `(`+linkResourceCategoryExpr+`) = ?`)
		args = append(args, params.Category)
	}
	if params.AccountID > 0 {
		where = append(where, `m.account_id = ?`)
		args = append(args, params.AccountID)
	}
	if params.ChannelID > 0 {
		where = append(where, `m.channel_id = ?`)
		args = append(args, params.ChannelID)
	}
	if params.Keyword != "" {
		where = append(where, `(mc.text LIKE ? OR COALESCE(l.note, '') LIKE ? OR COALESCE(l.media_title, '') LIKE ? OR COALESCE(l.media_tags, '') LIKE ?)`)
		like := "%" + params.Keyword + "%"
		args = append(args, like, like, like, like)
	}
	if params.DateFrom != nil {
		where = append(where, `m.date >= ?`)
		args = append(args, *params.DateFrom)
	}
	if params.DateTo != nil {
		where = append(where, `m.date < ?`)
		args = append(args, *params.DateTo)
	}
	return where, args
}

func (r *LinkRepository) SearchMerged(ctx context.Context, params MergedLinkSearchParams) (model.MergedLinksResponse, error) {
	where := []string{`m.deleted = 0`}
	args := []any{}
	if params.Type != "" {
		where = append(where, `l.type = ?`)
		args = append(args, params.Type)
	}
	if params.AccountID > 0 {
		where = append(where, `m.account_id = ?`)
		args = append(args, params.AccountID)
	}
	if params.ChannelID > 0 {
		where = append(where, `m.channel_id = ?`)
		args = append(args, params.ChannelID)
	}
	if params.Keyword != "" {
		where = append(where, `(mc.text LIKE ? OR COALESCE(l.note, '') LIKE ? OR COALESCE(l.media_title, '') LIKE ? OR COALESCE(l.media_tags, '') LIKE ?)`)
		like := "%" + params.Keyword + "%"
		args = append(args, like, like, like, like)
	}
	if params.DateFrom != nil {
		where = append(where, `m.date >= ?`)
		args = append(args, *params.DateFrom)
	}
	if params.DateTo != nil {
		where = append(where, `m.date < ?`)
		args = append(args, *params.DateTo)
	}
	query := `
SELECT l.id, l.type, l.url, COALESCE(l.password, ''), COALESCE(l.note, ''),
       COALESCE(l.media_title, ''), COALESCE(l.media_year, ''), COALESCE(l.media_season, ''), COALESCE(l.media_episode, ''),
       COALESCE(l.media_quality, ''), COALESCE(l.media_size, ''), COALESCE(l.media_tmdb_id, ''), COALESCE(l.media_category, ''), COALESCE(l.media_tags, ''),
       m.date, m.channel_id, c.title, c.username, m.telegram_message_id
FROM telegram_links l
JOIN telegram_messages m ON m.id = l.message_id
JOIN telegram_message_contents mc ON mc.message_id = m.id
JOIN telegram_channels c ON c.id = m.channel_id
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY m.date DESC, l.id DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return model.MergedLinksResponse{}, fmt.Errorf("search merged links: %w", err)
	}
	defer rows.Close()

	byURL := map[string]mergedLinkCandidate{}
	for rows.Next() {
		var item mergedLinkCandidate
		var channelTitle string
		var channelUsername string
		if err := rows.Scan(&item.id, &item.typ, &item.URL, &item.Password, &item.Note, &item.MediaTitle, &item.MediaYear, &item.MediaSeason, &item.MediaEpisode, &item.MediaQuality, &item.MediaSize, &item.MediaTMDBID, &item.MediaCategory, &item.MediaTags, &item.Datetime, &item.ChannelID, &channelTitle, &channelUsername, &item.TelegramMessageID); err != nil {
			return model.MergedLinksResponse{}, err
		}
		item.Source = sourceFromChannel(channelTitle, channelUsername)
		existing, ok := byURL[item.URL]
		if !ok || item.Datetime.After(existing.Datetime) || (item.Datetime.Equal(existing.Datetime) && item.id > existing.id) {
			byURL[item.URL] = item
		}
	}
	if err := rows.Err(); err != nil {
		return model.MergedLinksResponse{}, err
	}

	items := make([]mergedLinkCandidate, 0, len(byURL))
	for _, item := range byURL {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		leftScore := mergedQualityScore(items[i], params.Keyword)
		rightScore := mergedQualityScore(items[j], params.Keyword)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if !items[i].Datetime.Equal(items[j].Datetime) {
			return items[i].Datetime.After(items[j].Datetime)
		}
		return items[i].id > items[j].id
	})

	offset := params.Offset
	if offset > len(items) {
		offset = len(items)
	}
	limit := clampLimit(params.Limit, 50)
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[offset:end]
	response := model.MergedLinksResponse{
		Total:        len(page),
		MergedByType: model.MergedLinks{},
	}
	for _, item := range page {
		response.MergedByType[item.typ] = append(response.MergedByType[item.typ], item.MergedLink)
	}
	return response, nil
}

func sourceFromChannel(title string, username string) string {
	if title != "" {
		return "tg:" + title
	}
	if username != "" {
		return "tg:" + username
	}
	return "tg"
}

func linkCategory(link model.Link) string {
	switch link.Type {
	case "magnet":
		return "magnet"
	case "ed2k":
		return "ed2k"
	case "url":
		return "http"
	default:
		return "cloud_drive"
	}
}

func mergedQualityScore(item mergedLinkCandidate, query string) int {
	return searchrank.TextMatchScore(query, item.MediaTitle, item.Note, item.MediaTags, item.URL) +
		searchrank.TitleMarkerScore(item.MediaTitle, item.Note) +
		searchrank.MetadataScore(item.MediaTitle, item.MediaYear, item.MediaSeason, item.MediaEpisode, item.MediaQuality, item.MediaSize, item.MediaTMDBID, item.MediaCategory, item.MediaTags) +
		mergedTypeScore(item.typ) +
		linkPasswordScore(item.Password)
}

func mergedTypeScore(typ string) int {
	switch typ {
	case "quark", "aliyun", "baidu", "115", "uc", "xunlei", "tianyi", "mobile", "123", "pikpak", "guangya":
		return 80
	case "magnet":
		return 40
	case "ed2k":
		return 35
	case "url":
		return 10
	default:
		return 20
	}
}

func linkPasswordScore(password string) int {
	if strings.TrimSpace(password) == "" {
		return 0
	}
	return 10
}

func loadLinks(ctx context.Context, db *sql.DB, messageID int64) ([]model.Link, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, message_id, type, url, COALESCE(password, ''), COALESCE(note, ''),
       COALESCE(source_snippet, ''), COALESCE(category, ''), created_at
FROM telegram_links
WHERE message_id = ?
ORDER BY id`, messageID)
	if err != nil {
		return nil, fmt.Errorf("load links: %w", err)
	}
	defer rows.Close()
	var out []model.Link
	for rows.Next() {
		var link model.Link
		if err := rows.Scan(&link.ID, &link.MessageID, &link.Type, &link.URL, &link.Password, &link.Note, &link.SourceSnippet, &link.Category, &link.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}
