package repository

import (
	"context"
	"database/sql"
	"fmt"

	"tg-search/internal/model"
)

type StatusRepository struct {
	db     *sql.DB
	readDB *sql.DB
}

func NewStatusRepository(db *sql.DB) *StatusRepository {
	return &StatusRepository{db: db, readDB: db}
}

// WithReadDB routes the dashboard COUNT queries to a separate connection pool
// so /api/status does not queue behind sync/index writes on the single writer
// connection. Falls back to the writer pool when nil.
func (r *StatusRepository) WithReadDB(readDB *sql.DB) *StatusRepository {
	if readDB != nil {
		r.readDB = readDB
	}
	return r
}

func (r *StatusRepository) readConn() *sql.DB {
	if r.readDB != nil {
		return r.readDB
	}
	return r.db
}

func (r *StatusRepository) Counts(ctx context.Context) (model.StatusCounts, error) {
	var counts model.StatusCounts
	queries := []struct {
		sql  string
		dest *int64
	}{
		{`SELECT count(*) FROM telegram_accounts`, &counts.Accounts},
		{`SELECT count(*) FROM telegram_channels`, &counts.Channels},
		{`SELECT count(*) FROM telegram_messages`, &counts.Messages},
		{`SELECT count(*) FROM telegram_links`, &counts.Links},
	}
	for _, query := range queries {
		if err := r.readConn().QueryRowContext(ctx, query.sql).Scan(query.dest); err != nil {
			return model.StatusCounts{}, fmt.Errorf("read status count: %w", err)
		}
	}
	counts.AccountStates = map[string]int64{}
	rows, err := r.readConn().QueryContext(ctx, `SELECT status, count(*) FROM telegram_accounts GROUP BY status`)
	if err != nil {
		return model.StatusCounts{}, fmt.Errorf("read account state counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return model.StatusCounts{}, err
		}
		counts.AccountStates[status] = count
	}
	if err := rows.Err(); err != nil {
		return model.StatusCounts{}, err
	}
	return counts, nil
}
