package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tg-search/internal/model"
)

type AccountRepository struct {
	db *sql.DB
}

func NewAccountRepository(db *sql.DB) *AccountRepository {
	return &AccountRepository{db: db}
}

func (r *AccountRepository) Save(ctx context.Context, account model.Account) (int64, error) {
	now := time.Now().UTC()
	if account.Status == "" {
		account.Status = model.AccountStatusNew
	}
	var id int64
	err := r.db.QueryRowContext(ctx, `
INSERT INTO telegram_accounts
  (phone, telegram_user_id, first_name, last_name, username, photo_id, status, session_path, last_online_at, last_error, created_at, updated_at)
VALUES
  (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(phone) DO UPDATE SET
  telegram_user_id = excluded.telegram_user_id,
  first_name = excluded.first_name,
  last_name = excluded.last_name,
  username = excluded.username,
  photo_id = excluded.photo_id,
  status = excluded.status,
  session_path = excluded.session_path,
  last_online_at = excluded.last_online_at,
  last_error = excluded.last_error,
  updated_at = excluded.updated_at
RETURNING id`,
		account.Phone, account.TelegramUserID, account.FirstName, account.LastName, account.Username, account.PhotoID, account.Status, account.SessionPath, account.LastOnlineAt, account.LastError, now, now,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("save account: %w", err)
	}
	return id, nil
}

func (r *AccountRepository) Update(ctx context.Context, account model.Account) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE telegram_accounts
SET phone = ?, telegram_user_id = ?, first_name = ?, last_name = ?, username = ?, photo_id = ?, status = ?, session_path = ?, last_online_at = ?, last_error = ?, updated_at = ?
WHERE id = ?`,
		account.Phone, account.TelegramUserID, account.FirstName, account.LastName, account.Username, account.PhotoID, account.Status, account.SessionPath, account.LastOnlineAt, account.LastError, time.Now().UTC(), account.ID)
	if err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	return requireRows(res, "account not found")
}

func (r *AccountRepository) UpdateStatus(ctx context.Context, id int64, status string) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE telegram_accounts
SET status = ?, updated_at = ?
WHERE id = ?`, status, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update account status: %w", err)
	}
	return requireRows(res, "account not found")
}

func (r *AccountRepository) UpdatePhotoID(ctx context.Context, id int64, photoID int64) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE telegram_accounts
SET photo_id = ?, updated_at = ?
WHERE id = ?`, photoID, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update account photo_id: %w", err)
	}
	return requireRows(res, "account not found")
}


func (r *AccountRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM telegram_accounts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	return requireRows(res, "account not found")
}

func (r *AccountRepository) FindByID(ctx context.Context, id int64) (model.Account, error) {
	return scanAccount(r.db.QueryRowContext(ctx, `
SELECT id, phone, telegram_user_id, first_name, last_name, username, photo_id, status, session_path, last_online_at, last_error, created_at, updated_at
FROM telegram_accounts WHERE id = ?`, id))
}

func (r *AccountRepository) FindByPhone(ctx context.Context, phone string) (model.Account, error) {
	return scanAccount(r.db.QueryRowContext(ctx, `
SELECT id, phone, telegram_user_id, first_name, last_name, username, photo_id, status, session_path, last_online_at, last_error, created_at, updated_at
FROM telegram_accounts WHERE phone = ?`, phone))
}

func (r *AccountRepository) FindByTelegramUserID(ctx context.Context, telegramUserID int64) (model.Account, error) {
	return scanAccount(r.db.QueryRowContext(ctx, `
SELECT id, phone, telegram_user_id, first_name, last_name, username, photo_id, status, session_path, last_online_at, last_error, created_at, updated_at
FROM telegram_accounts WHERE telegram_user_id = ?`, telegramUserID))
}

func (r *AccountRepository) FindAll(ctx context.Context) ([]model.Account, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, phone, telegram_user_id, first_name, last_name, username, photo_id, status, session_path, last_online_at, last_error, created_at, updated_at
FROM telegram_accounts ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("find accounts: %w", err)
	}
	defer rows.Close()

	var out []model.Account
	for rows.Next() {
		account, err := scanAccountRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, account)
	}
	return out, rows.Err()
}

func scanAccount(row interface {
	Scan(...any) error
}) (model.Account, error) {
	account, err := scanAccountRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Account{}, sql.ErrNoRows
	}
	return account, err
}

func scanAccountRows(row interface {
	Scan(...any) error
}) (model.Account, error) {
	var account model.Account
	var lastOnline sql.NullTime
	err := row.Scan(&account.ID, &account.Phone, &account.TelegramUserID, &account.FirstName, &account.LastName, &account.Username, &account.PhotoID, &account.Status, &account.SessionPath, &lastOnline, &account.LastError, &account.CreatedAt, &account.UpdatedAt)
	if err != nil {
		return model.Account{}, err
	}
	if lastOnline.Valid {
		account.LastOnlineAt = &lastOnline.Time
	}
	return account, nil
}
