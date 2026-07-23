# Repair Media Titles — Design

## Problem

Before the link-extractor fix (`mediaMetadataFromSameLinePrefix`), a link line
shaped `<provider>：<url>` (e.g. `光鸭：https://...`) clobbered the correct
title: the provider label was promoted to `media_title` and `overlay()` overwrote
the title parsed from the message header. Affected rows store
`media_title = note = <label>` (e.g. `光鸭`, `移动`, `迅雷链接`, `夸克高码`).

The extractor fix stops new occurrences. This feature repairs **existing** data
for all users of the open-source project, via a button in the admin UI.

## Goals

- One-click repair of affected rows from the admin UI (works for every user,
  including Docker installs — no CLI/build needed).
- Recover the real title by re-parsing the retained message text with the fixed
  extractor (full title, e.g. `速度与激情9 F9: The Fast Saga`, not just a tag).
- Safe: only change rows where re-parse yields a different non-empty title;
  never touch AI-enriched rows; idempotent.
- Keep `resource_index` and FTS consistent after the repair.
- Single implementation shared by the UI task and the headless CLI.

## Non-Goals

- No separate dry-run/preview endpoint. Progress and counts are reported by the
  task itself.
- No change to ingestion, ranking, pagination, or the resource-index schema.
- No automatic startup migration — the repair is user-initiated.

## Bug-signature scope

A link row is a repair candidate iff all hold:

- message not deleted;
- `media_title = note` and `media_title <> ''`;
- `source_snippet` begins with `<media_title>` followed by `：` or `:` (the title
  is literally the URL-line label).

This selects provider-label titles and excludes correctly-titled rows whose
title merely coincides with the note (their snippet starts with a generic label
like `链接：`/`夸克：`). Rows enriched by the AI metadata task
(`media_title != note`) are excluded by construction.

## Architecture

Background task, reusing the existing task system (enqueue → worker → SSE
progress → Tasks page). Runtime is ~1 min on a 1.9 GB / 593k-link DB and scales
with affected rows; running inside the service process is concurrency-safe
(SQLite serializes writers) and needs no service stop.

## Components

### Core logic — `internal/resource.Service`

New method:

```
RepairMediaTitles(ctx, sink taskpkg.ProgressSink) (RepairSummary, error)
```

Phases, each reporting progress:

1. Scan bug-signature rows (`loadAffected`).
2. Batch-load `telegram_message_contents.text` once per distinct message.
3. Re-parse each message with `link.NewExtractor()`; map URL → corrected title.
4. For each candidate, `UPDATE telegram_links SET media_title = ?` in a
   transaction where the re-parsed title is non-empty and differs.
5. `RefreshMessages` for the changed messages so `resource_index` (title,
   `media_title`) and the FTS triggers stay consistent.

`RepairSummary` reports: affected, changed, unchanged, refreshed-messages.

Dependency: inject `link.Extractor` into `resource.Service` (wire in
`cmd/tg-search/main.go`, mirroring `history`/`update`). New repository methods:
`LinkRepository.ListMediaTitleLabelCandidates` (scan for bug-signature rows);
`MessageRepository.BatchTextByMessageIDs` (batch-load
`telegram_message_contents.text`). The standalone tool's four functions
(`loadAffected`, `loadMessageTexts`, `planChanges`, `applyChanges`) collapse
into this method.

### Task

- Constant `model.TaskTypeRepairMediaTitle = "repair_media_title"`.
- Handler `resource.Service.RunRepairMediaTitleTask(ctx, model.Task, taskpkg.ProgressSink) error`:
  decode optional payload (`dry_run`), call `RepairMediaTitles`, honour
  cancellation via `sink.Status()`.
- Register in `cmd/tg-search/main.go` worker `Handlers` map.

### API

`POST /api/maintenance/media-title/repair` in the `adminOnly` group (same shape
as `rebuildResourceIndex`). Body optionally `{"dry_run": true}`. Handler
enqueues the task via `h.deps.Tasks.Enqueue` and returns
`202 Accepted {task_id, status}`. Returns `503` if `Resources` is unavailable.

### Frontend

In `SettingsView.vue`, 系统 (System) tab: new `<section class="panel maintenance-panel">`
with a Naive UI button + confirm dialog. Inline
`apiPost('/api/maintenance/media-title/repair')` (matches existing settings
conventions — no new api module). On enqueue: toast "task started, see Tasks
page"; progress is consumed by the existing Tasks page + SSE events store; on
completion a result toast shows the changed count.

### CLI

`cmd/tg-backfill-media-title` becomes a thin wrapper that constructs the needed
repositories + extractor from a `*sql.DB` and calls
`resource.Service.RepairMediaTitles`, keeping a headless path (and `-dry-run`)
without duplicating logic.

## Error handling

- Task handler error → worker calls `service.Fail`; the task page shows the error.
- Cancellation → handler returns `context.Canceled`; worker marks canceled.
- Link updates run in one transaction. If `RefreshMessages` fails after the
  commit, the task fails but link rows are already corrected; re-running the
  task refreshes `resource_index` (idempotent).

## Testing

- `resource.Service.RepairMediaTitles` unit test: seed bug row, correctly-titled
  row (title == note, generic-label snippet), and AI-enriched row
  (`media_title != note`); assert only the bug row changes, AI row untouched,
  and a second run is a no-op.
- API handler test: enqueue returns `202` with `task_id`; `503` when
  `Resources` is nil.
- Title-extraction correctness is already covered by the extractor tests.

## Resolved decisions

- Scope: all bug-signature rows (not only `光鸭`).
- No separate preview; progress carries counts.
