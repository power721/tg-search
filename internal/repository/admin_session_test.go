package repository

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"tg-search/internal/db"
	"tg-search/internal/model"
)

func TestAdminSessionRepositoryPersistsSessionsWithUsers(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "tg-search.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	users := NewUserRepository(conn)
	userID, err := users.Create(ctx, model.User{
		Username:     "admin",
		PasswordHash: "hash",
		Role:         model.UserRoleAdmin,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	user, err := users.FindByID(ctx, userID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}

	sessions := NewAdminSessionRepository(conn)
	expiresAt := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	if err := sessions.Create(ctx, model.AdminSession{
		Token:     "session-token",
		UserID:    user.ID,
		ExpiresAt: expiresAt,
		CreatedAt: expiresAt.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	got, err := sessions.FindUser(ctx, "session-token", expiresAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("find session user: %v", err)
	}
	if got.ID != user.ID || got.Username != "admin" || got.PasswordHash != "hash" || got.Role != model.UserRoleAdmin {
		t.Fatalf("session user = %+v, want persisted admin", got)
	}
}

func TestAdminSessionRepositoryDeletesAndPrunesSessions(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "tg-search.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	users := NewUserRepository(conn)
	userID, err := users.Create(ctx, model.User{
		Username:     "admin",
		PasswordHash: "hash",
		Role:         model.UserRoleAdmin,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	sessions := NewAdminSessionRepository(conn)
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	for _, session := range []model.AdminSession{
		{Token: "expired", UserID: userID, ExpiresAt: now.Add(-time.Second), CreatedAt: now.Add(-time.Hour)},
		{Token: "active", UserID: userID, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour)},
		{Token: "delete-me", UserID: userID, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour)},
	} {
		if err := sessions.Create(ctx, session); err != nil {
			t.Fatalf("create session %s: %v", session.Token, err)
		}
	}

	if _, err := sessions.PruneExpired(ctx, now); err != nil {
		t.Fatalf("prune expired: %v", err)
	}
	if _, err := sessions.FindUser(ctx, "expired", now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired session error = %v, want sql.ErrNoRows", err)
	}
	if err := sessions.Delete(ctx, "delete-me"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := sessions.FindUser(ctx, "delete-me", now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted session error = %v, want sql.ErrNoRows", err)
	}
	if got, err := sessions.FindUser(ctx, "active", now); err != nil || got.ID != userID {
		t.Fatalf("active session lookup = %+v err=%v, want user %d", got, err, userID)
	}
}

// FindUser runs on every authenticated admin request, so it must go through
// the read pool when one is attached instead of queueing behind writes on the
// single writer connection.
func TestAdminSessionRepositoryFindUserUsesReadPool(t *testing.T) {
	ctx := context.Background()
	writerPath := filepath.Join(t.TempDir(), "writer.db")
	readPath := filepath.Join(t.TempDir(), "reader.db")

	conn, err := db.Open(writerPath)
	if err != nil {
		t.Fatalf("open writer db: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("migrate writer db: %v", err)
	}

	users := NewUserRepository(conn)
	userID, err := users.Create(ctx, model.User{
		Username:     "admin",
		PasswordHash: "hash",
		Role:         model.UserRoleAdmin,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	sessions := NewAdminSessionRepository(conn)
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	if err := sessions.Create(ctx, model.AdminSession{
		Token:     "session-token",
		UserID:    userID,
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	readConn, err := db.Open(readPath)
	if err != nil {
		t.Fatalf("open read db: %v", err)
	}
	if err := db.Migrate(ctx, readConn); err != nil {
		t.Fatalf("migrate read db: %v", err)
	}
	if err := readConn.Close(); err != nil {
		t.Fatalf("close read db: %v", err)
	}
	reader, err := db.OpenRead(readPath)
	if err != nil {
		t.Fatalf("open read pool: %v", err)
	}
	defer reader.Close()

	routed := sessions.WithReadDB(reader)
	if _, err := routed.FindUser(ctx, "session-token", now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("routed FindUser error = %v, want sql.ErrNoRows from the empty read pool (query ran on the writer pool)", err)
	}
	// Mutations still go through the writer pool.
	if err := routed.Delete(ctx, "session-token"); err != nil {
		t.Fatalf("delete via writer: %v", err)
	}
	if _, err := sessions.FindUser(ctx, "session-token", now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("FindUser after delete error = %v, want sql.ErrNoRows", err)
	}
}
