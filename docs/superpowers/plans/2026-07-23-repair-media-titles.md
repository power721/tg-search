# Repair Media Titles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an admin UI button that repairs `media_title` rows clobbered by provider labels, via a background task.

**Architecture:** New task type reuses the existing task system. Core logic lives in `resource.Service.RepairMediaTitles` (single source of truth), shared by the task handler and the CLI. Re-parses retained message text with the fixed `link.Extractor`; updates `telegram_links.media_title`; refreshes `resource_index`.

**Tech Stack:** Go (gin, sqlite), Vue 3 + Naive UI + Pinia.

## Global Constraints

- Go: `gofmt`; `GOCACHE=/tmp/go-build-cache go test ./...`.
- Frontend: `npm run web:typecheck`, `npm run web:test`.
- Conventional commit prefixes (`feat:`, `refactor:`, `test:`).
- Branch `fix/media-title-provider-label`; spec at `docs/superpowers/specs/2026-07-23-repair-media-titles-design.md`.

## File Structure

- Modify `internal/repository/link.go` — `ListMediaTitleLabelCandidates`.
- Modify `internal/repository/message.go` — `BatchTextByMessageIDs`.
- Modify `internal/repository/link_test.go`, `message_test.go` — tests.
- Modify `internal/resource/service.go` — extractor field, `RepairMediaTitles`, `RunRepairMediaTitleTask`.
- Modify `internal/resource/service_test.go` — repair tests.
- Modify `internal/model/model.go` — `TaskTypeRepairMediaTitle`.
- Modify `internal/api/handlers.go`, `internal/api/router.go` — endpoint + route.
- Modify `cmd/tg-search/main.go` — wire extractor + worker handler.
- Modify `web/src/views/SettingsView.vue` — button.
- Rewrite `cmd/tg-backfill-media-title/main.go` — thin wrapper.

---

### Task 1: Repository methods

**Files:** Modify `internal/repository/link.go`, `internal/repository/message.go`; tests in `link_test.go`, `message_test.go`.

**Interfaces:**
- Produces: `LinkRepository.ListMediaTitleLabelCandidates(ctx) ([]MediaTitleCandidate, error)`, `LinkRepository.UpdateMediaTitle(ctx, int64, string) error`, and `MessageRepository.BatchTextByMessageIDs(ctx, []int64) (map[int64]string, error)`.

- [ ] Add to `link.go`:

```go
type MediaTitleCandidate struct {
	ID         int64
	MessageID  int64
	URL        string
	MediaTitle string
	Note       string
}

// ListMediaTitleLabelCandidates returns link rows whose media_title was clobbered
// by a provider label: media_title equals the note, and the URL's own line
// (source_snippet) begins with "<media_title><:>".
func (r *LinkRepository) ListMediaTitleLabelCandidates(ctx context.Context) ([]MediaTitleCandidate, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT l.id, l.message_id, l.url, COALESCE(l.media_title, ''), COALESCE(l.note, '')
FROM telegram_links l
JOIN telegram_messages m ON m.id = l.message_id
WHERE m.deleted = 0
  AND l.media_title = l.note AND l.media_title <> ''
  AND length(l.source_snippet) > length(l.media_title)
  AND substr(l.source_snippet, 1, length(l.media_title)) = l.media_title
  AND substr(l.source_snippet, length(l.media_title) + 1, 1) IN ('：', ':')
ORDER BY l.message_id, l.id`)
	if err != nil {
		return nil, fmt.Errorf("list media-title label candidates: %w", err)
	}
	defer rows.Close()
	var out []MediaTitleCandidate
	for rows.Next() {
		var c MediaTitleCandidate
		if err := rows.Scan(&c.ID, &c.MessageID, &c.URL, &c.MediaTitle, &c.Note); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

- [ ] Add to `link.go`:

```go
func (r *LinkRepository) UpdateMediaTitle(ctx context.Context, id int64, title string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_links SET media_title = ? WHERE id = ?`, title, id)
	if err != nil {
		return fmt.Errorf("update link media title: %w", err)
	}
	return nil
}
```

- [ ] Add to `message.go`:

```go
// BatchTextByMessageIDs returns telegram_message_contents.text for the given ids.
func (r *MessageRepository) BatchTextByMessageIDs(ctx context.Context, messageIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(messageIDs))
	if len(messageIDs) == 0 {
		return out, nil
	}
	const batchSize = 500
	for start := 0; start < len(messageIDs); start += batchSize {
		end := start + batchSize
		if end > len(messageIDs) {
			end = len(messageIDs)
		}
		batch := messageIDs[start:end]
		params := make([]any, len(batch))
		for i, id := range batch {
			params[i] = id
		}
		query := `SELECT message_id, COALESCE(text, '') FROM telegram_message_contents WHERE message_id IN (` +
			strings.Repeat("?,", len(batch)-1) + "?)"
		rows, err := r.db.QueryContext(ctx, query, params...)
		if err != nil {
			return nil, fmt.Errorf("batch load message texts: %w", err)
		}
		for rows.Next() {
			var id int64
			var text string
			if err := rows.Scan(&id, &text); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[id] = text
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
```

- [ ] Test (TDD): seed a deleted=false message + a link with `media_title=note="光鸭"`, `source_snippet="光鸭：https://x"`, plus a clean link (`media_title="莫离"`, `note="莫离(2026)"`, snippet `"链接：https://y"`); assert the first is returned, the second is not. Test `BatchTextByMessageIDs` returns seeded text. Follow `service_test.go` setup (`db.Open(temp)` + `db.Migrate`, `messages.SaveBatch` stores `.Text`, `links.SaveBatch` stores fields).
- [ ] `GOCACHE=/tmp/go-build-cache go test ./internal/repository/`; gofmt; commit `feat: add media-title label candidate and message text repo queries`.

---

### Task 2: resource.Service.RepairMediaTitles

**Files:** Modify `internal/resource/service.go`; test `internal/resource/service_test.go`.

**Interfaces:**
- Consumes: Task 1 repo methods; `link.NewExtractor()`.
- Produces: `(s *Service) RepairMediaTitles(ctx, sink) (RepairSummary, error)`; `RepairSummary{Affected, Changed, Unchanged, RefreshedMessages int}`.

- [ ] Add wiring to `service.go` (new `messages` + `extractor` fields):

```go
import "tg-search/internal/link"

type Service struct {
	links     *repository.LinkRepository
	files     *repository.FileRepository
	stats     *repository.ResourceStatsRepository
	index     *repository.ResourceIndexRepository
	messages  *repository.MessageRepository
	extractor *link.Extractor
}
```

In `NewService`'s extras loop add:

```go
case *repository.MessageRepository:
	service.messages = repo
case *link.Extractor:
	service.extractor = repo
```

- [ ] Add the repair method (defines a local sink interface so `resource` need not import `task`):

```go
type repairProgressSink interface {
	Progress(ctx context.Context, progress, total int64, message string) error
	Status(ctx context.Context) (string, error)
}

type RepairSummary struct {
	Affected         int
	Changed          int
	Unchanged        int
	RefreshedMessages int
}

func (s *Service) RepairMediaTitles(ctx context.Context, sink repairProgressSink) (RepairSummary, error) {
	var summary RepairSummary
	if s.extractor == nil || s.messages == nil {
		return summary, fmt.Errorf("media title repair requires an extractor and message repository")
	}
	candidates, err := s.links.ListMediaTitleLabelCandidates(ctx)
	if err != nil {
		return summary, err
	}
	summary.Affected = len(candidates)

	// distinct message ids
	seen := map[int64]struct{}{}
	msgIDs := make([]int64, 0, len(candidates))
	for _, c := range candidates {
		if _, ok := seen[c.MessageID]; !ok {
			seen[c.MessageID] = struct{}{}
			msgIDs = append(msgIDs, c.MessageID)
		}
	}
	texts, err := s.messages.BatchTextByMessageIDs(ctx, msgIDs)
	if err != nil {
		return summary, err
	}

	// re-parse per message, map url -> corrected title
	byMessage := map[int64][]MediaTitleCandidate{}
	for _, c := range candidates {
		byMessage[c.MessageID] = append(byMessage[c.MessageID], c)
	}
	type change struct {
		id        int64
		messageID int64
		title     string
	}
	var changes []change
	for messageID, links := range byMessage {
		urlToTitle := map[string]string{}
		if text := strings.TrimSpace(texts[messageID]); text != "" {
			for _, p := range s.extractor.Extract(text) {
				if p.URL != "" {
					urlToTitle[p.URL] = p.MediaTitle
				}
			}
		}
		for _, a := range links {
			newTitle := urlToTitle[a.URL]
			if newTitle == "" || newTitle == a.MediaTitle {
				summary.Unchanged++
				continue
			}
			changes = append(changes, change{a.ID, a.MessageID, newTitle})
		}
	}

	if sink != nil {
		_ = sink.Progress(ctx, 0, int64(len(changes)), fmt.Sprintf("applying %d title updates", len(changes)))
	}
	for _, c := range changes {
		if err := s.links.UpdateMediaTitle(ctx, c.id, c.title); err != nil {
			return summary, err
		}
	}
	summary.Changed = len(changes)

	// distinct changed message ids, then refresh resource_index (FTS via triggers)
	changedSeen := map[int64]struct{}{}
	changedMsgIDs := make([]int64, 0, len(changes))
	for _, c := range changes {
		if _, ok := changedSeen[c.messageID]; !ok {
			changedSeen[c.messageID] = struct{}{}
			changedMsgIDs = append(changedMsgIDs, c.messageID)
		}
	}
	if err := s.index.RefreshMessages(ctx, changedMsgIDs); err != nil {
		return summary, fmt.Errorf("refresh resource_index: %w", err)
	}
	summary.RefreshedMessages = len(changedMsgIDs)
	if sink != nil {
		_ = sink.Progress(ctx, int64(summary.Changed), int64(summary.Affected),
			fmt.Sprintf("repaired %d of %d titles", summary.Changed, summary.Affected))
	}
	return summary, nil
}
```

- [ ] Test (TDD) in `service_test.go`: seed three links for one message whose `.Text` is the `🗄 速度与激情9 F9: The Fast Saga (2021)【4K SDR 无损超清】 … 光鸭：https://...` body:
  - bug row: `MediaTitle=Note="光鸭"`, `SourceSnippet="光鸭：https://..."`.
  - clean row: `MediaTitle="莫离"`, `Note="莫离(2026)"`, snippet `"链接：..."`.
  - AI row: `MediaTitle="速度与激情9"`, `Note="光鸭"` (title != note).
  Construct `NewService(links, files, index, link.NewExtractor(), messages)`, call `RepairMediaTitles(ctx, nil)`, assert: bug row `media_title` became `速度与激情9 F9: The Fast Saga`, clean + AI rows unchanged, `summary.Changed == 1`. Re-run → no further changes (idempotent).
- [ ] `GOCACHE=/tmp/go-build-cache go test ./internal/resource/`; gofmt; commit `feat: repair media titles in resource service`.

---

### Task 3: Task type + handler + worker registration

**Files:** `internal/model/model.go`, `internal/resource/service.go`, `cmd/tg-search/main.go`.

**Interfaces:**
- Produces: `model.TaskTypeRepairMediaTitle`; `resource.Service.RunRepairMediaTitleTask`.

- [ ] Add constant near the other `TaskType*` (`internal/model/model.go`):

```go
TaskTypeRepairMediaTitle = "repair_media_title"
```

- [ ] Add payload to `internal/task` (e.g. `payloads.go` alongside `HistorySyncPayload`):

```go
type RepairMediaTitlePayload struct {
	DryRun bool `json:"dry_run"`
}
```

- [ ] Add handler to `service.go` (import `encoding/json`, `tg-search/internal/task`):

```go
func (s *Service) RunRepairMediaTitleTask(ctx context.Context, item model.Task, progress task.ProgressSink) error {
	var payload task.RepairMediaTitlePayload
	_ = json.Unmarshal([]byte(item.PayloadJSON), &payload)
	summary, err := s.RepairMediaTitles(ctx, progress)
	if err != nil {
		return err
	}
	if progress != nil {
		_ = progress.Progress(ctx, int64(summary.Changed), int64(summary.Affected),
			fmt.Sprintf("repaired %d of %d titles", summary.Changed, summary.Affected))
	}
	return nil
}
```

(DryRun: add a `dryRun bool` param to `RepairMediaTitles` threaded from `payload.DryRun`; when set, skip the `UpdateMediaTitle`/`RefreshMessages` block and just report planned `summary.Changed`.)

- [ ] Register in `cmd/tg-search/main.go` worker `Handlers` map:

```go
model.TaskTypeRepairMediaTitle: resourceService.RunRepairMediaTitleTask,
```

- [ ] Build `go build ./...`; commit `feat: add repair-media-title task`.

---

### Task 4: API endpoint

**Files:** `internal/api/handlers.go`, `internal/api/router.go`.

- [ ] Handler (mirror `rebuildResourceIndex` + the `enqueueHistorySyncTask` 202 pattern):

```go
func (h handlers) repairMediaTitle(c *gin.Context) {
	if h.deps.Resources == nil {
		errorText(c, http.StatusServiceUnavailable, "resources are unavailable")
		return
	}
	var body struct{ DryRun bool `json:"dry_run"` }
	_ = c.ShouldBindJSON(&body)
	task, err := h.deps.Tasks.Enqueue(c.Request.Context(), model.TaskTypeRepairMediaTitle, taskpkg.RepairMediaTitlePayload{DryRun: body.DryRun})
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	h.publishEvent(taskpkg.Event{Type: taskpkg.EventTaskUpdated, Payload: localizeTask(task)})
	c.JSON(http.StatusAccepted, gin.H{"task_id": task.ID, "status": task.Status})
}
```

(Move `RepairMediaTitlePayload` to `internal/task` if handlers import `task` not `resource` — both already imported elsewhere; pick the package that avoids cycles, i.e. `task`.)

- [ ] Route in `router.go` admin group:

```go
adminOnly.POST("/maintenance/media-title/repair", h.repairMediaTitle)
```

- [ ] Handler test: enqueue returns 202 with `task_id`; nil `Resources` → 503. Follow existing `handlers_test.go` patterns.
- [ ] `go test ./internal/api/`; commit `feat: add repair-media-title maintenance endpoint`.

---

### Task 5: Frontend button

**Files:** `web/src/views/SettingsView.vue`.

- [ ] In the 系统 tab `settings-panel-grid`, add a panel (mimic `version-panel`):

```vue
<section class="panel maintenance-panel">
  <div class="panel-header">
    <h2>媒体标题修复</h2>
    <n-button size="small" type="primary" :loading="repairLoading" @click="repairMediaTitles">
      修复
    </n-button>
  </div>
  <p class="panel-hint">修复被网盘标签覆盖的媒体标题（如“光鸭”）。任务在后台运行，进度见“任务”页。</p>
</section>
```

- [ ] Script: add `const repairLoading = ref(false)` and:

```ts
async function repairMediaTitles() {
  const ok = await dialog.warning({
    title: '修复媒体标题',
    content: '将重新解析历史消息并更新被覆盖的标题。继续？',
    positiveText: '修复',
    negativeText: '取消',
  })
  if (!ok) return
  repairLoading.value = true
  try {
    const res = await apiPost<{ task_id: number }>('/api/maintenance/media-title/repair')
    message.success(`任务已开始 #${res.task_id}，进度见任务页`)
  } catch (error) {
    message.error(error instanceof Error ? error.message : '启动修复任务失败')
  } finally {
    repairLoading.value = false
  }
}
```

(Use the `dialog` and `message` Naive UI globals already present in the view; match existing imports.)

- [ ] `npm run web:typecheck`; `npm run web:test` (add a minimal test if a SettingsView test exists; else rely on typecheck + manual). Commit `feat: add repair media titles button in settings`.

---

### Task 6: CLI thin wrapper

**Files:** Rewrite `cmd/tg-backfill-media-title/main.go`.

- [ ] Replace the four standalone functions with a thin caller: open DB via `-db`/`-config`, construct `repository.NewLinkRepository`, `NewFileRepository`, `NewMessageRepository`, `NewResourceIndexRepository`, build `resource.NewService(links, files, messages, index, link.NewExtractor())`, call `service.RepairMediaTitles(ctx, nil)`, print `summary`. Keep `-dry-run` mapped to the payload. Drop `-probe` (or keep calling the service). Keep defaults to apply (no longer dry-run-only) or keep dry-run default — decide: keep `-dry-run` default true for safety.
- [ ] Build `go build ./cmd/tg-backfill-media-title/`; run `-db <path>` dry-run to confirm same 638 count; commit `refactor: backfill tool delegates to resource service`.

---

### Task 7: Verification

- [ ] `gofmt -l $(gofmt -l .)` clean.
- [ ] `GOCACHE=/tmp/go-build-cache go test ./...` all pass.
- [ ] `npm run web:typecheck && npm run web:test`.
- [ ] `go build -o /tmp/tg-search ./cmd/tg-search` builds.
- [ ] (Manual) run service, click 修复 in 系统 tab, watch task progress, confirm a 光鸭 record's title updated.

---

## Unresolved questions

1. Dry-run in the UI: ship the 修复 button as apply-only (current plan), or add a 预览 button that enqueues `dry_run:true`? Plan = apply-only (YAGNI).
2. Keep the standalone CLI tool after the UI ships, or remove it? Plan keeps it as a thin wrapper for headless use.
