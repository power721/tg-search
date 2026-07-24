package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	dbpkg "tg-search/internal/db"
	"tg-search/internal/model"
)

// EnqueueGapRecovery records a gap-recovery request for a channel while keeping at
// most one queued gap_recovery task per channel. If a queued task already exists for
// the channel it is extended to cover the requested range instead of creating a
// duplicate, which prevents the queue from growing on every incoming live message.
func (s *Service) EnqueueGapRecovery(ctx context.Context, payload GapRecoveryPayload) (model.Task, error) {
	return s.repo.CoalesceGapRecovery(ctx, payload)
}

// CoalesceGapRecovery ensures at most one queued gap_recovery task exists for the
// payload's channel:
//   - if none exists, payload is inserted as a new queued task;
//   - otherwise the widest existing queued task is extended to cover payload
//     (max to_message_id / trigger_message) and any duplicate queued rows for the
//     channel are removed.
//
// The coalesced (or newly created) task is returned.
func (r *Repository) CoalesceGapRecovery(ctx context.Context, payload GapRecoveryPayload) (model.Task, error) {
	if payload.ChannelID <= 0 {
		return model.Task{}, fmt.Errorf("coalesce gap recovery: channel_id is required")
	}

	var keeperID int64
	err := dbpkg.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
SELECT id, payload_json
FROM sync_tasks
WHERE type = ? AND status = ?
  AND json_extract(payload_json, '$.channel_id') = ?
ORDER BY json_extract(payload_json, '$.to_message_id') DESC, id DESC`,
			model.TaskTypeGapRecovery, model.TaskStatusQueued, payload.ChannelID)
		if err != nil {
			return fmt.Errorf("query queued gap recovery tasks: %w", err)
		}

		var (
			widestID   int64
			widestTo   int64
			widestTrig int64
			haveQueued bool
		)
		for rows.Next() {
			var id int64
			var payloadJSON string
			if err := rows.Scan(&id, &payloadJSON); err != nil {
				rows.Close()
				return fmt.Errorf("scan queued gap recovery task: %w", err)
			}
			if !haveQueued {
				var existing GapRecoveryPayload
				_ = json.Unmarshal([]byte(payloadJSON), &existing)
				widestID, widestTo, widestTrig, haveQueued = id, existing.ToMessageID, existing.TriggerMessageID, true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate queued gap recovery tasks: %w", err)
		}
		rows.Close()

		now := time.Now().UTC()

		if !haveQueued {
			encoded, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("encode gap recovery payload: %w", err)
			}
			if err := tx.QueryRowContext(ctx, `
INSERT INTO sync_tasks
  (type, status, progress, total, message, error_code, error_message, retry_count, next_run_at, payload_json, started_at, finished_at, created_at, updated_at)
VALUES
  (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id`,
				model.TaskTypeGapRecovery, model.TaskStatusQueued, 0, 0, "", "", "", 0, nil, string(encoded), nil, nil, now, now,
			).Scan(&keeperID); err != nil {
				return fmt.Errorf("insert gap recovery task: %w", err)
			}
			return nil
		}

		// Extend the widest existing queued task so a single run covers the full range.
		if widestTo > payload.ToMessageID {
			payload.ToMessageID = widestTo
		}
		if widestTrig > payload.TriggerMessageID {
			payload.TriggerMessageID = widestTrig
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode coalesced gap recovery payload: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE sync_tasks SET payload_json = ?, updated_at = ? WHERE id = ?`,
			string(encoded), now, widestID); err != nil {
			return fmt.Errorf("extend gap recovery task: %w", err)
		}
		// Collapse any other queued gap_recovery rows for the same channel.
		if _, err := tx.ExecContext(ctx, `
DELETE FROM sync_tasks
WHERE type = ? AND status = ?
  AND json_extract(payload_json, '$.channel_id') = ?
  AND id <> ?`,
			model.TaskTypeGapRecovery, model.TaskStatusQueued, payload.ChannelID, widestID); err != nil {
			return fmt.Errorf("collapse duplicate gap recovery tasks: %w", err)
		}
		keeperID = widestID
		return nil
	})
	if err != nil {
		return model.Task{}, err
	}
	return r.FindByID(ctx, keeperID)
}
