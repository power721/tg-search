package db

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	dsn := path
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		dsn = fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=5000", path)
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := applyPragmas(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// OpenRead opens a multi-connection read-only pool for WAL-concurrent reads.
//
// Open returns a single-connection writer (SetMaxOpenConns(1)) so that all
// writes serialize and never hit "database is locked". The cost is that every
// read also funnels through that one connection, so a heavy writer (sync /
// index refresh) starves readers — a public search can stall for many seconds.
//
// SQLite in WAL mode allows many concurrent readers alongside one writer, but
// only across separate connections. OpenRead provides that reader pool: every
// connection runs with PRAGMA query_only(1), so an accidental write is rejected
// rather than silently contending. Repositories route pure-SELECT hot paths
// (resource index search, file lookups) here so they no longer queue behind
// writes. ConnMaxIdleTime lets idle readers release their snapshots so WAL
// checkpoints can recycle frames and the -wal file does not grow unbounded.
func OpenRead(path string) (*sql.DB, error) {
	dsn := path
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		dsn = fmt.Sprintf("file:%s?_foreign_keys=on&_pragma=query_only(1)&_pragma=busy_timeout(5000)&_pragma=cache_size(-200000)&_pragma=temp_store(MEMORY)", path)
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite read pool: %w", err)
	}
	size := ReadPoolSize()
	conn.SetMaxOpenConns(size)
	conn.SetMaxIdleConns(size)
	conn.SetConnMaxIdleTime(5 * time.Minute)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping sqlite read pool: %w", err)
	}
	return conn, nil
}

// ReadPoolSize returns the reader pool size used by OpenRead.
func ReadPoolSize() int {
	n := runtime.NumCPU()
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	return n
}

func WithTx(ctx context.Context, conn *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func applyPragmas(conn *sql.DB) error {
	pragmas := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA temp_store=MEMORY`,
		`PRAGMA cache_size=-200000`,
		`PRAGMA foreign_keys=ON`,
	}
	for _, pragma := range pragmas {
		if _, err := conn.Exec(pragma); err != nil {
			return fmt.Errorf("apply %s: %w", pragma, err)
		}
	}
	return nil
}
