package repository

import (
	"context"
	"path/filepath"
	"testing"

	"tg-search/internal/db"
	"tg-search/internal/model"
)

func TestStatusCountsReadsFromWriterByDefault(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "tg-search.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := NewAccountRepository(conn).Save(ctx, model.Account{Phone: "+10000000000"}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	counts, err := NewStatusRepository(conn).Counts(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Accounts != 1 || counts.AccountStates == nil {
		t.Fatalf("counts = %+v, want 1 account with non-nil account states", counts)
	}
}

// Counts must run on the read pool when one is attached: the writer pool is a
// single connection shared with sync/index writes, and dashboard counts queue
// behind them.
func TestStatusCountsUsesReadPool(t *testing.T) {
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
	if _, err := NewAccountRepository(conn).Save(ctx, model.Account{Phone: "+10000000000"}); err != nil {
		t.Fatalf("seed account: %v", err)
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

	counts, err := NewStatusRepository(conn).WithReadDB(reader).Counts(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Accounts != 0 {
		t.Fatalf("counts.Accounts = %d, want 0 from the empty read pool (query ran on the writer pool)", counts.Accounts)
	}
}
