package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	"tg-search/internal/model"
)

type MessageRepository struct {
	db *sql.DB
}

func NewMessageRepository(db *sql.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

func (r *MessageRepository) SaveBatch(ctx context.Context, messages []model.Message) ([]model.Message, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	out, err := r.SaveBatchTx(ctx, tx, messages)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *MessageRepository) SaveBatchTx(ctx context.Context, tx *sql.Tx, messages []model.Message) ([]model.Message, error) {
	out := make([]model.Message, 0, len(messages))
	if len(messages) == 0 {
		return out, nil
	}
	messageStmt, err := tx.PrepareContext(ctx, saveMessageSQL)
	if err != nil {
		return nil, fmt.Errorf("prepare save message: %w", err)
	}
	defer messageStmt.Close()
	contentStmt, err := tx.PrepareContext(ctx, saveMessageContentSQL)
	if err != nil {
		return nil, fmt.Errorf("prepare save message content: %w", err)
	}
	defer contentStmt.Close()
	for _, msg := range messages {
		stored, err := r.savePrepared(ctx, messageStmt, contentStmt, msg)
		if err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	return out, nil
}

func (r *MessageRepository) savePrepared(ctx context.Context, messageStmt *sql.Stmt, contentStmt *sql.Stmt, msg model.Message) (model.Message, error) {
	stored, err := saveMessageMetadata(msg, func(args ...any) *sql.Row {
		return messageStmt.QueryRowContext(ctx, args...)
	})
	if err != nil {
		return model.Message{}, err
	}
	if _, err := contentStmt.ExecContext(ctx, stored.ID, msg.Text, msg.RawJSON, stored.UpdatedAt, stored.UpdatedAt); err != nil {
		return model.Message{}, fmt.Errorf("save message content %d/%d: %w", msg.ChannelID, msg.TelegramMessageID, err)
	}
	stored.Text = msg.Text
	stored.RawJSON = msg.RawJSON
	return stored, nil
}

func saveMessageMetadata(msg model.Message, query func(args ...any) *sql.Row) (model.Message, error) {
	if msg.MessageType == "" {
		msg.MessageType = "text"
	}
	return saveMessage(msg, query)
}

func saveMessage(msg model.Message, query func(args ...any) *sql.Row) (model.Message, error) {
	now := time.Now().UTC()
	var editDate any
	if msg.EditDate != nil {
		editDate = *msg.EditDate
	}
	deleted := 0
	if msg.Deleted {
		deleted = 1
	}
	err := query(
		msg.AccountID, msg.ChannelID, msg.TelegramMessageID, msg.SenderID, msg.MessageType, msg.MediaSummary, msg.Date, editDate, deleted, now, now,
	).Scan(&msg.ID, &msg.CreatedAt, &msg.UpdatedAt)
	if err != nil {
		return model.Message{}, fmt.Errorf("save message %d/%d: %w", msg.ChannelID, msg.TelegramMessageID, err)
	}
	return msg, nil
}

const saveMessageSQL = `
INSERT INTO telegram_messages
  (account_id, channel_id, telegram_message_id, sender_id, message_type, media_summary, date, edit_date, deleted, created_at, updated_at)
VALUES
  (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(channel_id, telegram_message_id) DO UPDATE SET
  account_id = excluded.account_id,
  sender_id = excluded.sender_id,
  message_type = excluded.message_type,
  media_summary = excluded.media_summary,
  date = excluded.date,
  edit_date = excluded.edit_date,
  deleted = excluded.deleted,
  updated_at = excluded.updated_at
RETURNING id, created_at, updated_at`

const saveMessageContentSQL = `
INSERT INTO telegram_message_contents
  (message_id, text, raw_json, created_at, updated_at)
VALUES
  (?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET
  text = excluded.text,
  raw_json = excluded.raw_json,
  updated_at = excluded.updated_at`

// BatchTextByMessageIDs returns telegram_message_contents.text for the given
// message ids, batched to bound the parameter count. Missing ids are absent
// from the map.
func (r *MessageRepository) BatchTextByMessageIDs(ctx context.Context, messageIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(messageIDs))
	if len(messageIDs) == 0 {
		return out, nil
	}
	const batchSize = 500
	for start := 0; start < len(messageIDs); start += batchSize {
		end := start + batchSize
		if end > len(messageIDs) {
			end = len(messageIDs)
		}
		batch := messageIDs[start:end]
		params := make([]any, len(batch))
		for i, id := range batch {
			params[i] = id
		}
		query := `SELECT message_id, COALESCE(text, '') FROM telegram_message_contents WHERE message_id IN (` +
			strings.Repeat("?,", len(batch)-1) + "?)"
		rows, err := r.db.QueryContext(ctx, query, params...)
		if err != nil {
			return nil, fmt.Errorf("batch load message texts: %w", err)
		}
		for rows.Next() {
			var id int64
			var text string
			if err := rows.Scan(&id, &text); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[id] = text
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *MessageRepository) Search(ctx context.Context, params SearchParams) ([]model.SearchResult, error) {
	limit := clampLimit(params.Limit, 50)
	where, args := messageSearchWhere(params)
	args = append(args, limit, params.Offset)
	query := `
SELECT m.id, m.account_id, m.channel_id, m.telegram_message_id, m.sender_id, m.message_type, m.media_summary,
       mc.text, mc.raw_json, m.date, m.edit_date, m.deleted, m.created_at, m.updated_at,
       a.phone, a.username, a.first_name, c.title, c.username, c.telegram_channel_id
FROM telegram_messages_fts
JOIN telegram_messages m ON m.id = telegram_messages_fts.rowid
JOIN telegram_message_contents mc ON mc.message_id = m.id
JOIN telegram_accounts a ON a.id = m.account_id
JOIN telegram_channels c ON c.id = m.channel_id
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY ` + dateOrderBy(params.Sort, "m.date", "m.id") + `
LIMIT ? OFFSET ?`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	var out []model.SearchResult
	for rows.Next() {
		item, err := scanSearchResult(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return attachSearchResultExtras(ctx, r.db, out)
}

func (r *MessageRepository) CountSearch(ctx context.Context, params SearchParams) (int, error) {
	where, args := messageSearchWhere(params)
	query := `
SELECT count(*)
FROM telegram_messages_fts
JOIN telegram_messages m ON m.id = telegram_messages_fts.rowid
WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count search messages: %w", err)
	}
	return total, nil
}

func (r *MessageRepository) FindByID(ctx context.Context, id int64) (model.Message, error) {
	query := `
SELECT m.id, m.account_id, m.channel_id, m.telegram_message_id, m.sender_id, m.message_type, m.media_summary,
       mc.text, mc.raw_json, m.date, m.edit_date, m.deleted, m.created_at, m.updated_at
FROM telegram_messages m
JOIN telegram_message_contents mc ON mc.message_id = m.id
WHERE m.id = ?`
	item, err := scanMessage(r.db.QueryRowContext(ctx, query, id))
	if err != nil {
		return model.Message{}, fmt.Errorf("find message by id: %w", err)
	}
	return item, nil
}

func messageSearchWhere(params SearchParams) ([]string, []any) {
	where := []string{`telegram_messages_fts MATCH ?`, `m.deleted = 0`}
	args := []any{fts5Query(params.Query)}
	if params.AccountID > 0 {
		where = append(where, `m.account_id = ?`)
		args = append(args, params.AccountID)
	}
	if params.ChannelID > 0 {
		where = append(where, `m.channel_id = ?`)
		args = append(args, params.ChannelID)
	}
	if params.LinkType != "" {
		where = append(where, `EXISTS (SELECT 1 FROM telegram_links fl WHERE fl.message_id = m.id AND fl.type = ?)`)
		args = append(args, params.LinkType)
	}
	if params.DateFrom != nil {
		where = append(where, `m.date >= ?`)
		args = append(args, *params.DateFrom)
	}
	if params.DateTo != nil {
		where = append(where, `m.date < ?`)
		args = append(args, *params.DateTo)
	}
	if params.BeforeDate != nil && params.BeforeID > 0 {
		where = append(where, `(m.date < ? OR (m.date = ? AND m.id < ?))`)
		args = append(args, *params.BeforeDate, *params.BeforeDate, params.BeforeID)
	}
	return where, args
}

func fts5Query(query string) string {
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

func (r *MessageRepository) Latest(ctx context.Context, params LatestParams) ([]model.SearchResult, error) {
	limit := clampLimit(params.Limit, 50)
	where := []string{`m.deleted = 0`}
	args := []any{}
	if params.AccountID > 0 {
		where = append(where, `m.account_id = ?`)
		args = append(args, params.AccountID)
	}
	if params.ChannelID > 0 {
		where = append(where, `m.channel_id = ?`)
		args = append(args, params.ChannelID)
	}
	if params.BeforeDate != nil && params.BeforeID > 0 {
		where = append(where, `(m.date < ? OR (m.date = ? AND m.id < ?))`)
		args = append(args, *params.BeforeDate, *params.BeforeDate, params.BeforeID)
	}
	args = append(args, limit)
	query := `
SELECT m.id, m.account_id, m.channel_id, m.telegram_message_id, m.sender_id, m.message_type, m.media_summary,
       mc.text, mc.raw_json, m.date, m.edit_date, m.deleted, m.created_at, m.updated_at,
       a.phone, a.username, a.first_name, c.title, c.username, c.telegram_channel_id
FROM telegram_messages m
JOIN telegram_message_contents mc ON mc.message_id = m.id
JOIN telegram_accounts a ON a.id = m.account_id
JOIN telegram_channels c ON c.id = m.channel_id
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY m.date DESC, m.id DESC
LIMIT ?`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("latest messages: %w", err)
	}
	var out []model.SearchResult
	for rows.Next() {
		item, err := scanSearchResult(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return attachSearchResultExtras(ctx, r.db, out)
}

func attachSearchResultExtras(ctx context.Context, db *sql.DB, items []model.SearchResult) ([]model.SearchResult, error) {
	items, err := attachLinks(ctx, db, items)
	if err != nil {
		return nil, err
	}
	return attachFiles(ctx, db, items)
}

func (r *MessageRepository) MarkDeleted(ctx context.Context, channelID int64, telegramMessageID int64) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE telegram_messages
SET deleted = 1, updated_at = ?
WHERE channel_id = ? AND telegram_message_id = ?`, time.Now().UTC(), channelID, telegramMessageID)
	if err != nil {
		return fmt.Errorf("mark message deleted: %w", err)
	}
	return requireRows(res, "message not found")
}

func (r *MessageRepository) FindIDByTelegramMessageID(ctx context.Context, channelID int64, telegramMessageID int64) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `
SELECT id
FROM telegram_messages
WHERE channel_id = ? AND telegram_message_id = ?`, channelID, telegramMessageID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (r *MessageRepository) MarkDeletedTx(ctx context.Context, tx *sql.Tx, channelID int64, telegramMessageID int64) error {
	res, err := tx.ExecContext(ctx, `
UPDATE telegram_messages
SET deleted = 1, updated_at = ?
WHERE channel_id = ? AND telegram_message_id = ?`, time.Now().UTC(), channelID, telegramMessageID)
	if err != nil {
		return fmt.Errorf("mark message deleted: %w", err)
	}
	return requireRows(res, "message not found")
}

func scanMessage(row interface {
	Scan(...any) error
}) (model.Message, error) {
	var item model.Message
	var editDate sql.NullTime
	var deleted int
	if err := row.Scan(
		&item.ID,
		&item.AccountID,
		&item.ChannelID,
		&item.TelegramMessageID,
		&item.SenderID,
		&item.MessageType,
		&item.MediaSummary,
		&item.Text,
		&item.RawJSON,
		&item.Date,
		&editDate,
		&deleted,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return model.Message{}, err
	}
	if editDate.Valid {
		item.EditDate = &editDate.Time
	}
	item.Deleted = deleted != 0
	return item, nil
}

func scanSearchResult(row interface {
	Scan(...any) error
}) (model.SearchResult, error) {
	var item model.SearchResult
	var editDate sql.NullTime
	var deleted int
	err := row.Scan(&item.ID, &item.AccountID, &item.ChannelID, &item.TelegramMessageID, &item.SenderID, &item.MessageType, &item.MediaSummary, &item.Text, &item.RawJSON, &item.Date, &editDate,
		&deleted, &item.CreatedAt, &item.UpdatedAt, &item.AccountPhone, &item.AccountUsername, &item.AccountFirstName, &item.ChannelTitle, &item.ChannelUsername, &item.TelegramChannelID)
	if err != nil {
		return model.SearchResult{}, err
	}
	if editDate.Valid {
		item.EditDate = &editDate.Time
	}
	item.Deleted = deleted != 0
	return item, nil
}

func attachLinks(ctx context.Context, db *sql.DB, items []model.SearchResult) ([]model.SearchResult, error) {
	if len(items) == 0 {
		return items, nil
	}
	ids := make([]any, 0, len(items))
	placeholders := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		placeholders = append(placeholders, "?")
	}
	rows, err := db.QueryContext(ctx, `
SELECT id, message_id, type, url, COALESCE(password, ''), COALESCE(note, ''),
       COALESCE(source_snippet, ''), COALESCE(category, ''), created_at
FROM telegram_links
WHERE message_id IN (`+strings.Join(placeholders, ",")+`)
ORDER BY message_id, id`, ids...)
	if err != nil {
		return nil, fmt.Errorf("load links: %w", err)
	}
	defer rows.Close()

	byMessageID := map[int64][]model.Link{}
	for rows.Next() {
		var link model.Link
		if err := rows.Scan(&link.ID, &link.MessageID, &link.Type, &link.URL, &link.Password, &link.Note, &link.SourceSnippet, &link.Category, &link.CreatedAt); err != nil {
			return nil, err
		}
		byMessageID[link.MessageID] = append(byMessageID[link.MessageID], link)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Links = byMessageID[items[i].ID]
	}
	return items, nil
}

func attachFiles(ctx context.Context, db *sql.DB, items []model.SearchResult) ([]model.SearchResult, error) {
	if len(items) == 0 {
		return items, nil
	}
	ids := make([]any, 0, len(items))
	placeholders := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		placeholders = append(placeholders, "?")
	}
	rows, err := db.QueryContext(ctx, `
SELECT id, message_id, telegram_file_id, file_name, extension, mime_type, size_bytes, category, created_at, updated_at
FROM telegram_files
WHERE message_id IN (`+strings.Join(placeholders, ",")+`)
ORDER BY message_id, id`, ids...)
	if err != nil {
		return nil, fmt.Errorf("load files: %w", err)
	}
	defer rows.Close()

	byMessageID := map[int64][]model.File{}
	for rows.Next() {
		var file model.File
		if err := rows.Scan(&file.ID, &file.MessageID, &file.TelegramFileID, &file.FileName, &file.Extension, &file.MimeType, &file.SizeBytes, &file.Category, &file.CreatedAt, &file.UpdatedAt); err != nil {
			return nil, err
		}
		byMessageID[file.MessageID] = append(byMessageID[file.MessageID], file)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Files = byMessageID[items[i].ID]
	}
	return items, nil
}
