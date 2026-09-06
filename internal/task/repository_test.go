package task

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tg-search/internal/db"
	"tg-search/internal/model"
)

func TestTaskRepository(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	repo := NewRepository(conn)
	created, err := repo.Create(ctx, model.Task{
		Type:        model.TaskTypeHistorySync,
		Total:       100,
		Message:     "queued history sync",
		PayloadJSON: `{"channel_id":1}`,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.ID == 0 || created.Status != model.TaskStatusQueued || created.Type != model.TaskTypeHistorySync {
		t.Fatalf("created task = %+v", created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created timestamps = %+v", created)
	}

	if err := repo.UpdateStatus(ctx, created.ID, model.TaskStatusRunning, StatusUpdate{
		Message:   "running",
		StartedAt: ptrTime(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)),
	}); err != nil {
		t.Fatalf("UpdateStatus running returned error: %v", err)
	}
	if err := repo.AppendProgress(ctx, created.ID, 25, 100, "saved first batch"); err != nil {
		t.Fatalf("AppendProgress returned error: %v", err)
	}

	got, err := repo.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("FindByID returned error: %v", err)
	}
	if got.Status != model.TaskStatusRunning || got.Progress != 25 || got.Total != 100 || got.Message != "saved first batch" {
		t.Fatalf("updated task = %+v", got)
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("started_at = %v", got.StartedAt)
	}

	running, err := repo.List(ctx, ListFilter{Status: model.TaskStatusRunning, Limit: 10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(running) != 1 || running[0].ID != created.ID {
		t.Fatalf("running tasks = %+v", running)
	}
}

func TestTaskRepositoryRestartQuery(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	repo := NewRepository(conn)
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	running := createTaskForRestart(t, ctx, repo, model.TaskStatusRunning, nil)
	canceling := createTaskForRestart(t, ctx, repo, model.TaskStatusCanceling, nil)
	paused := createTaskForRestart(t, ctx, repo, model.TaskStatusPaused, nil)
	reconnecting := createTaskForRestart(t, ctx, repo, model.TaskStatusReconnecting, nil)
	floodPast := createTaskForRestart(t, ctx, repo, model.TaskStatusFloodWait, ptrTime(now.Add(-time.Minute)))
	_ = createTaskForRestart(t, ctx, repo, model.TaskStatusFloodWait, ptrTime(now.Add(time.Hour)))
	_ = createTaskForRestart(t, ctx, repo, model.TaskStatusSucceeded, nil)
	_ = createTaskForRestart(t, ctx, repo, model.TaskStatusCanceled, nil)

	restartable, err := repo.ListRestartable(ctx, now)
	if err != nil {
		t.Fatalf("ListRestartable returned error: %v", err)
	}
	gotIDs := map[int64]bool{}
	for _, item := range restartable {
		gotIDs[item.ID] = true
	}
	for _, id := range []int64{running.ID, canceling.ID, paused.ID, reconnecting.ID, floodPast.ID} {
		if !gotIDs[id] {
			t.Fatalf("restartable ids = %+v, missing %d", gotIDs, id)
		}
	}
	if len(restartable) != 5 {
		t.Fatalf("restartable len = %d, want 5: %+v", len(restartable), restartable)
	}
}

func TestTaskRepositoryListSearchAndSort(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	repo := NewRepository(conn)
	first, err := repo.Create(ctx, model.Task{
		Type:         model.TaskTypeHistorySync,
		Status:       model.TaskStatusFailed,
		Progress:     30,
		Total:        100,
		ErrorMessage: "temporary needle failure",
		PayloadJSON:  `{"channel_id":1}`,
	})
	if err != nil {
		t.Fatalf("Create first returned error: %v", err)
	}
	second, err := repo.Create(ctx, model.Task{
		Type:        model.TaskTypeHistorySync,
		Status:      model.TaskStatusQueued,
		Progress:    80,
		Total:       100,
		Message:     "needle queued",
		PayloadJSON: `{"channel_id":2}`,
	})
	if err != nil {
		t.Fatalf("Create second returned error: %v", err)
	}
	if _, err := repo.Create(ctx, model.Task{
		Type:        model.TaskTypeBackup,
		Status:      model.TaskStatusQueued,
		Message:     "unrelated",
		PayloadJSON: `{}`,
	}); err != nil {
		t.Fatalf("Create third returned error: %v", err)
	}

	filter := ListFilter{Type: model.TaskTypeHistorySync, Query: "needle", Sort: "progress", Order: "desc", Limit: 10}
	items, err := repo.List(ctx, filter)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 2 || items[0].ID != second.ID || items[1].ID != first.ID {
		t.Fatalf("sorted search items = %+v, want [%d %d]", items, second.ID, first.ID)
	}
	total, err := repo.Count(ctx, filter)
	if err != nil {
		t.Fatalf("Count returned error: %v", err)
	}
	if total != 2 {
		t.Fatalf("Count = %d, want 2", total)
	}
}

func TestTaskRepositoryDelete(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	repo := NewRepository(conn)
	created, err := repo.Create(ctx, model.Task{Type: model.TaskTypeHistorySync, PayloadJSON: `{}`})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := repo.FindByID(ctx, created.ID); err == nil {
		t.Fatal("FindByID succeeded after delete, want error")
	}
	if err := repo.Delete(ctx, created.ID); err == nil {
		t.Fatal("Delete missing task succeeded, want error")
	}
}

func createTaskForRestart(t *testing.T, ctx context.Context, repo *Repository, status string, nextRunAt *time.Time) model.Task {
	t.Helper()
	created, err := repo.Create(ctx, model.Task{Type: model.TaskTypeHistorySync, PayloadJSON: `{}`})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := repo.UpdateStatus(ctx, created.ID, status, StatusUpdate{NextRunAt: nextRunAt}); err != nil {
		t.Fatalf("update task %d to %s: %v", created.ID, status, err)
	}
	got, err := repo.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	return got
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func TestRepository_DeleteOldTasks(t *testing.T) {
	db := testDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.AddDate(0, 0, -10)
	recent := now.AddDate(0, 0, -3)

	// Create test tasks with different statuses and ages
	oldSucceeded := createTaskWithTimestamp(t, repo, model.TaskStatusSucceeded, old)
	recentSucceeded := createTaskWithTimestamp(t, repo, model.TaskStatusSucceeded, recent)
	oldRunning := createTaskWithTimestamp(t, repo, model.TaskStatusRunning, old)
	oldCanceling := createTaskWithTimestamp(t, repo, model.TaskStatusCanceling, old)
	oldFailed := createTaskWithTimestamp(t, repo, model.TaskStatusFailed, old)

	// Test: Delete succeeded tasks older than 7 days
	deleted, err := repo.DeleteOldTasks(ctx, model.TaskStatusSucceeded, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "should delete 1 old succeeded task")

	// Verify old succeeded is gone
	_, err = repo.FindByID(ctx, oldSucceeded.ID)
	assert.Equal(t, sql.ErrNoRows, err, "old succeeded should be deleted")

	// Verify recent succeeded still exists
	_, err = repo.FindByID(ctx, recentSucceeded.ID)
	assert.NoError(t, err, "recent succeeded should remain")

	// Test: Running and canceling tasks are never deleted
	deleted, err = repo.DeleteOldTasks(ctx, model.TaskStatusRunning, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted, "should not delete running tasks")

	_, err = repo.FindByID(ctx, oldRunning.ID)
	assert.NoError(t, err, "old running task should remain")

	deleted, err = repo.DeleteOldTasks(ctx, model.TaskStatusCanceling, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted, "should not delete canceling tasks")

	_, err = repo.FindByID(ctx, oldCanceling.ID)
	assert.NoError(t, err, "old canceling task should remain")

	// Test: Delete failed tasks older than 15 days
	deleted, err = repo.DeleteOldTasks(ctx, model.TaskStatusFailed, 15)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted, "10 day old failed task is within 15 day retention")

	deleted, err = repo.DeleteOldTasks(ctx, model.TaskStatusFailed, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "should delete failed task older than 5 days")

	_, err = repo.FindByID(ctx, oldFailed.ID)
	assert.Equal(t, sql.ErrNoRows, err, "old failed should be deleted")
}

func createTaskWithTimestamp(t *testing.T, repo *Repository, status string, updatedAt time.Time) model.Task {
	task := model.Task{
		Type:        model.TaskTypeHistorySync,
		Status:      status,
		PayloadJSON: "{}",
	}
	created, err := repo.Create(context.Background(), task)
	require.NoError(t, err)

	// Backdate the updated_at timestamp
	_, err = repo.db.ExecContext(context.Background(),
		`UPDATE sync_tasks SET updated_at = ? WHERE id = ?`,
		updatedAt, created.ID)
	require.NoError(t, err)

	// Re-fetch to get updated timestamp
	result, err := repo.FindByID(context.Background(), created.ID)
	require.NoError(t, err)
	return result
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(context.Background(), conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return conn
}

// Task listing/counting runs on every dashboard load, so it must go through
// the read pool when one is attached instead of queueing behind task writes
// on the single writer connection.
func TestTaskRepositoryListAndCountUseReadPool(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "writer.db"))
	require.NoError(t, err)
	defer conn.Close()

	// Reader pool on a file with no schema: any query that reaches it must
	// fail with "no such table", proving it did not run on the writer.
	reader, err := db.OpenRead(filepath.Join(t.TempDir(), "reader.db"))
	require.NoError(t, err)
	defer reader.Close()

	repo := NewRepository(conn).WithReadDB(reader)
	_, err = repo.List(ctx, ListFilter{})
	require.ErrorContains(t, err, "no such table")
	_, err = repo.Count(ctx, ListFilter{})
	require.ErrorContains(t, err, "no such table")
}
