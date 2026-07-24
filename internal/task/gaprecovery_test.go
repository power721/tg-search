package task

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tg-search/internal/model"
)

func TestCoalesceGapRecovery(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testDB(t))

	countQueued := func(channelID int64) int {
		var n int
		err := repo.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sync_tasks WHERE type = ? AND status = ? AND json_extract(payload_json, '$.channel_id') = ?`,
			model.TaskTypeGapRecovery, model.TaskStatusQueued, channelID).Scan(&n)
		require.NoError(t, err)
		return n
	}
	toMessageID := func(id int64) int64 {
		var to int64
		require.NoError(t, repo.db.QueryRowContext(ctx,
			`SELECT json_extract(payload_json, '$.to_message_id') FROM sync_tasks WHERE id = ?`, id).Scan(&to))
		return to
	}

	// (a) no existing queued task -> inserts a new one.
	first, err := repo.CoalesceGapRecovery(ctx, GapRecoveryPayload{
		AccountID: 1, ChannelID: 10, FromMessageID: 100, ToMessageID: 110, TriggerMessageID: 111, TelegramChannelID: 999,
	})
	require.NoError(t, err)
	require.NotZero(t, first.ID)
	assert.Equal(t, model.TaskStatusQueued, first.Status)
	assert.Equal(t, 1, countQueued(10))

	// (b) existing task with smaller to -> extends to the new (larger) to, reuses same id.
	second, err := repo.CoalesceGapRecovery(ctx, GapRecoveryPayload{
		AccountID: 1, ChannelID: 10, FromMessageID: 100, ToMessageID: 150, TriggerMessageID: 151, TelegramChannelID: 999,
	})
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "should coalesce into the existing task")
	assert.Equal(t, int64(150), toMessageID(first.ID))
	assert.Equal(t, 1, countQueued(10), "still one queued task")

	// (c) existing task with larger to -> keeps the larger to (does not shrink).
	third, err := repo.CoalesceGapRecovery(ctx, GapRecoveryPayload{
		ChannelID: 10, FromMessageID: 100, ToMessageID: 120, TriggerMessageID: 121,
	})
	require.NoError(t, err)
	assert.Equal(t, first.ID, third.ID)
	assert.Equal(t, int64(150), toMessageID(first.ID), "should keep the larger to_message_id")

	// (d) two duplicate queued tasks -> collapses to one, keeping the widest range.
	for _, p := range []string{
		`{"channel_id":20,"from_message_id":5,"to_message_id":10,"trigger_message":11}`,
		`{"channel_id":20,"from_message_id":5,"to_message_id":30,"trigger_message":31}`,
	} {
		_, err := repo.Create(ctx, model.Task{Type: model.TaskTypeGapRecovery, Status: model.TaskStatusQueued, PayloadJSON: p})
		require.NoError(t, err)
	}
	assert.Equal(t, 2, countQueued(20))
	keeper, err := repo.CoalesceGapRecovery(ctx, GapRecoveryPayload{
		ChannelID: 20, FromMessageID: 5, ToMessageID: 40, TriggerMessageID: 41,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, countQueued(20), "should collapse duplicates to one")
	assert.Equal(t, int64(40), toMessageID(keeper.ID))

	// (e) leaves running + other-channel tasks untouched.
	running, err := repo.Create(ctx, model.Task{
		Type: model.TaskTypeGapRecovery, Status: model.TaskStatusRunning,
		PayloadJSON: `{"channel_id":30,"from_message_id":1,"to_message_id":5}`,
	})
	require.NoError(t, err)
	queued30, err := repo.Create(ctx, model.Task{
		Type: model.TaskTypeGapRecovery, Status: model.TaskStatusQueued,
		PayloadJSON: `{"channel_id":30,"from_message_id":1,"to_message_id":5}`,
	})
	require.NoError(t, err)
	_, err = repo.CoalesceGapRecovery(ctx, GapRecoveryPayload{
		ChannelID: 30, FromMessageID: 1, ToMessageID: 9, TriggerMessageID: 10,
	})
	require.NoError(t, err)
	runningAfter, err := repo.FindByID(ctx, running.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusRunning, runningAfter.Status, "running task must be untouched")
	// The pre-existing queued task for channel 30 is coalesced (extended), not duplicated.
	assert.Equal(t, 1, countQueued(30))
	assert.Equal(t, int64(9), toMessageID(queued30.ID))
}

func TestServiceEnqueueGapRecovery(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewRepository(testDB(t)))

	first, err := svc.EnqueueGapRecovery(ctx, GapRecoveryPayload{
		ChannelID: 7, FromMessageID: 10, ToMessageID: 20, TriggerMessageID: 21,
	})
	require.NoError(t, err)

	// Same channel, wider range -> coalesces into the same task.
	second, err := svc.EnqueueGapRecovery(ctx, GapRecoveryPayload{
		ChannelID: 7, FromMessageID: 10, ToMessageID: 30, TriggerMessageID: 31,
	})
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)

	// Different channel -> separate task.
	other, err := svc.EnqueueGapRecovery(ctx, GapRecoveryPayload{
		ChannelID: 8, FromMessageID: 1, ToMessageID: 2, TriggerMessageID: 3,
	})
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, other.ID)
}
