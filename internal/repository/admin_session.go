package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tg-search/internal/model"
)

type AdminSessionRepository struct {
	db     *sql.DB
	readDB *sql.DB
}

func NewAdminSessionRepository(db *sql.DB) *AdminSessionRepository {
	return &AdminSessionRepository{db: db, readDB: db}
}

// WithReadDB routes session validation lookups to a separate connection pool
// so every authenticated admin request does not queue behind writes on the
// single writer connection. Mutations stay on the writer pool. Falls back to
// the writer pool when nil.
func (r *AdminSessionRepository) WithReadDB(readDB *sql.DB) *AdminSessionRepository {
	if readDB != nil {
		r.readDB = readDB
	}
	return r
}

func (r *AdminSessionRepository) readConn() *sql.DB {
	if r.readDB != nil {
		return r.readDB
	}
	return r.db
}

func (r *AdminSessionRepository) Create(ctx context.Context, session model.AdminSession) error {
	createdAt := session.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if _, err := r.db.ExecContext(ctx, `
INSERT INTO admin_sessions
  (token, user_id, expires_at, created_at)
VALUES
  (?, ?, ?, ?)`,
		session.Token, session.UserID, session.ExpiresAt.UTC(), createdAt.UTC()); err != nil {
		return fmt.Errorf("create admin session: %w", err)
	}
	return nil
}

func (r *AdminSessionRepository) FindUser(ctx context.Context, token string, now time.Time) (model.User, error) {
	user, err := scanUser(r.readConn().QueryRowContext(ctx, `
SELECT u.id, u.username, u.password_hash, u.role, u.last_login_at, u.created_at, u.updated_at
FROM admin_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token = ? AND s.expires_at > ?`, token, now.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, sql.ErrNoRows
	}
	if err != nil {
		return model.User{}, fmt.Errorf("find admin session user: %w", err)
	}
	return user, nil
}

func (r *AdminSessionRepository) UpdateUser(ctx context.Context, token string, userID int64, now time.Time) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE admin_sessions
SET user_id = ?
WHERE token = ? AND expires_at > ?`, userID, token, now.UTC())
	if err != nil {
		return fmt.Errorf("update admin session user: %w", err)
	}
	if err := requireRows(res, "admin session not found"); err != nil {
		return err
	}
	return nil
}

func (r *AdminSessionRepository) Delete(ctx context.Context, token string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token = ?`, token); err != nil {
		return fmt.Errorf("delete admin session: %w", err)
	}
	return nil
}

func (r *AdminSessionRepository) PruneExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE expires_at <= ?`, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("prune expired admin sessions: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("admin sessions pruned count: %w", err)
	}
	return count, nil
}
