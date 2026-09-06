package task

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"tg-search/internal/model"
)

type Repository struct {
	db     *sql.DB
	readDB *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db, readDB: db}
}

// WithReadDB routes read-only task listing/counting to a separate connection
// pool so dashboard reads do not queue behind task writes on the single writer
// connection. Mutations stay on the writer pool. Falls back to the writer pool
// when nil.
func (r *Repository) WithReadDB(readDB *sql.DB) *Repository {
	if readDB != nil {
		r.readDB = readDB
	}
	return r
}

func (r *Repository) readConn() *sql.DB {
	if r.readDB != nil {
		return r.readDB
	}
	return r.db
}

type StatusUpdate struct {
	Message      string
	ErrorCode    string
	ErrorMessage string
	RetryCount   *int64
	NextRunAt    *time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

type ListFilter struct {
	Status string
	Type   string
	Query  string
	Sort   string
	Order  string
	Limit  int
	Offset int
}

func (r *Repository) Create(ctx context.Context, item model.Task) (model.Task, error) {
	if item.Status == "" {
		item.Status = model.TaskStatusQueued
	}
	if item.PayloadJSON == "" {
		item.PayloadJSON = "{}"
	}
	now := time.Now().UTC()
	row := r.db.QueryRowContext(ctx, `
INSERT INTO sync_tasks
  (type, status, progress, total, message, error_code, error_message, retry_count, next_run_at, payload_json, started_at, finished_at, created_at, updated_at)
VALUES
  (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, type, status, progress, total, message, error_code, error_message, retry_count, next_run_at, payload_json, started_at, finished_at, created_at, updated_at`,
		item.Type, item.Status, item.Progress, item.Total, item.Message, item.ErrorCode, item.ErrorMessage, item.RetryCount,
		item.NextRunAt, item.PayloadJSON, item.StartedAt, item.FinishedAt, now, now,
	)
	item, err := scanTask(row)
	if err != nil {
		return model.Task{}, fmt.Errorf("create task: %w", err)
	}
	return item, nil
}

func (r *Repository) FindByID(ctx context.Context, id int64) (model.Task, error) {
	return scanTask(r.db.QueryRowContext(ctx, `
SELECT id, type, status, progress, total, message, error_code, error_message, retry_count, next_run_at, payload_json, started_at, finished_at, created_at, updated_at
FROM sync_tasks
WHERE id = ?`, id))
}

func (r *Repository) UpdateStatus(ctx context.Context, id int64, status string, update StatusUpdate) error {
	now := time.Now().UTC()
	retryCount := any(nil)
	if update.RetryCount != nil {
		retryCount = *update.RetryCount
	}
	res, err := r.db.ExecContext(ctx, `
UPDATE sync_tasks
SET status = ?,
    message = CASE WHEN ? <> '' THEN ? ELSE message END,
    error_code = CASE WHEN ? <> '' THEN ? ELSE error_code END,
    error_message = CASE WHEN ? <> '' THEN ? ELSE error_message END,
    retry_count = COALESCE(?, retry_count),
    next_run_at = ?,
    started_at = COALESCE(?, started_at),
    finished_at = COALESCE(?, finished_at),
    updated_at = ?
WHERE id = ?`,
		status, update.Message, update.Message, update.ErrorCode, update.ErrorCode, update.ErrorMessage, update.ErrorMessage,
		retryCount, update.NextRunAt, update.StartedAt, update.FinishedAt, now, id,
	)
	if err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	if rows, err := res.RowsAffected(); err != nil || rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) AppendProgress(ctx context.Context, id int64, progress int64, total int64, message string) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE sync_tasks
SET progress = ?, total = ?, message = ?, updated_at = ?
WHERE id = ?`, progress, total, message, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("append task progress: %w", err)
	}
	if rows, err := res.RowsAffected(); err != nil || rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) List(ctx context.Context, filter ListFilter) ([]model.Task, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	where, args := taskListWhere(filter)
	args = append(args, limit, filter.Offset)
	rows, err := r.readConn().QueryContext(ctx, `
SELECT id, type, status, progress, total, message, error_code, error_message, retry_count, next_run_at, payload_json, started_at, finished_at, created_at, updated_at
FROM sync_tasks
WHERE `+strings.Join(where, " AND ")+`
`+taskListOrder(filter)+`
LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (r *Repository) Count(ctx context.Context, filter ListFilter) (int, error) {
	where, args := taskListWhere(filter)
	var total int
	if err := r.readConn().QueryRowContext(ctx, `
SELECT COUNT(*)
FROM sync_tasks
WHERE `+strings.Join(where, " AND "), args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count tasks: %w", err)
	}
	return total, nil
}

func (r *Repository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM sync_tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if rows, err := res.RowsAffected(); err != nil || rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteOldTasks deletes tasks of the specified status older than the given days.
// Returns the number of deleted rows.
// Running and canceling tasks are never deleted regardless of age.
func (r *Repository) DeleteOldTasks(ctx context.Context, status string, olderThanDays int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -olderThanDays)

	result, err := r.db.ExecContext(ctx, `
		DELETE FROM sync_tasks
		WHERE status = ?
		  AND updated_at < ?
		  AND status NOT IN (?, ?)`,
		status, cutoff, model.TaskStatusRunning, model.TaskStatusCanceling)

	if err != nil {
		return 0, fmt.Errorf("delete old tasks: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("get rows affected: %w", err)
	}

	return deleted, nil
}

func taskListWhere(filter ListFilter) ([]string, []any) {
	where := []string{"1=1"}
	args := []any{}
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.Type != "" {
		where = append(where, "type = ?")
		args = append(args, filter.Type)
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		like := "%" + query + "%"
		where = append(where, `(CAST(id AS TEXT) LIKE ? OR type LIKE ? OR status LIKE ? OR message LIKE ? OR error_code LIKE ? OR error_message LIKE ? OR payload_json LIKE ?)`)
		args = append(args, like, like, like, like, like, like, like)
	}
	return where, args
}

func taskListOrder(filter ListFilter) string {
	columns := map[string]string{
		"id":          "id",
		"type":        "type",
		"status":      "status",
		"progress":    "progress",
		"total":       "total",
		"retry_count": "retry_count",
		"next_run_at": "next_run_at",
		"created_at":  "created_at",
		"updated_at":  "updated_at",
		"message":     "message",
	}
	column, ok := columns[filter.Sort]
	if !ok {
		return "ORDER BY updated_at DESC, id DESC"
	}
	direction := "DESC"
	if strings.EqualFold(filter.Order, "asc") {
		direction = "ASC"
	}
	if column == "id" {
		return "ORDER BY id " + direction
	}
	return "ORDER BY " + column + " " + direction + ", id " + direction
}

func (r *Repository) ListRestartable(ctx context.Context, now time.Time) ([]model.Task, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, type, status, progress, total, message, error_code, error_message, retry_count, next_run_at, payload_json, started_at, finished_at, created_at, updated_at
FROM sync_tasks
WHERE status IN (?, ?, ?, ?, ?)
  AND (next_run_at IS NULL OR next_run_at <= ?)
ORDER BY updated_at ASC, id ASC`,
		model.TaskStatusRunning,
		model.TaskStatusCanceling,
		model.TaskStatusPaused,
		model.TaskStatusFloodWait,
		model.TaskStatusReconnecting,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("list restartable tasks: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}

func scanTasks(rows *sql.Rows) ([]model.Task, error) {
	out := []model.Task{}
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanTask(row interface {
	Scan(...any) error
}) (model.Task, error) {
	var item model.Task
	var nextRunAt sql.NullTime
	var startedAt sql.NullTime
	var finishedAt sql.NullTime
	if err := row.Scan(
		&item.ID,
		&item.Type,
		&item.Status,
		&item.Progress,
		&item.Total,
		&item.Message,
		&item.ErrorCode,
		&item.ErrorMessage,
		&item.RetryCount,
		&nextRunAt,
		&item.PayloadJSON,
		&startedAt,
		&finishedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return model.Task{}, err
	}
	if nextRunAt.Valid {
		item.NextRunAt = &nextRunAt.Time
	}
	if startedAt.Valid {
		item.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		item.FinishedAt = &finishedAt.Time
	}
	return item, nil
}
