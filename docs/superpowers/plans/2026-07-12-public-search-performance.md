# Public Search Performance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Remove per-result database reads from public-search media enrichment while preserving the existing API response and signed media URLs.

**Architecture:** Keep the resource-index query unchanged. Build a request-scoped media signer, batch-fetch files for eligible public-search results, derive all media IDs before loading the signing key, then reuse one signer for the response page.

**Tech Stack:** Go, Gin, database/sql, modernc SQLite, Go testing and benchmarks.

---

## File Structure

- Modify: internal/api/search_media.go
  - Extract media-file ID selection.
  - Add a reusable request-scoped media URL signer.
  - Keep existing media routes behavior-compatible.
- Modify: internal/api/external_search.go
  - Replace per-item media lookup with one batch query.
  - Reuse one signer for all public-search results.
- Create: internal/api/search_media_test.go
  - Unit-test media ID selection and signer reuse.
- Modify: internal/api/handlers_test.go
  - Add public-search regression coverage for multiple signed images and image-disabled video behavior.
- Create: internal/api/external_search_benchmark_test.go
  - Benchmark a representative 30-item enrichment page.

### Task 1: Add request-scoped media signing

**Files:**
- Create: internal/api/search_media_test.go
- Modify: internal/api/search_media.go

- [ ] **Step 1: Write failing tests for media ID selection and signer reuse**

Create internal/api/search_media_test.go:

```go
package api

import (
    "net/http"
    "net/url"
    "strconv"
    "testing"
    "time"

    "tg-search/internal/apikey"
    "tg-search/internal/model"
)

func TestMediaFileIDsFallsBackToVideoForImage(t *testing.T) {
    imageID, videoID := mediaFileIDs("", []model.File{{
        TelegramFileID: 42,
        FileName:       "movie.mp4",
        MimeType:       "video/mp4",
    }})
    if imageID != 42 || videoID != 42 {
        t.Fatalf("media IDs = (%d, %d), want (42, 42)", imageID, videoID)
    }
}

func TestMediaURLSignerSignsMultiplePathsWithOneKey(t *testing.T) {
    expiresAt := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
    signer := newMediaURLSigner("0123456789abcdef0123456789abcdef", expiresAt)

    first, err := signer.mediaURL("i", 101)
    if err != nil {
        t.Fatalf("sign first URL: %v", err)
    }
    second, err := signer.mediaURL("i", 202)
    if err != nil {
        t.Fatalf("sign second URL: %v", err)
    }

    wantExp := strconv.FormatInt(expiresAt.Unix(), 10)
    for _, rawURL := range []string{first, second} {
        parsed, err := url.Parse(rawURL)
        if err != nil {
            t.Fatalf("parse %q: %v", rawURL, err)
        }
        if parsed.Query().Get("exp") != wantExp {
            t.Fatalf("URL %q exp = %q, want %q", rawURL, parsed.Query().Get("exp"), wantExp)
        }
        wantSig, err := apikey.MediaSignature(
            "0123456789abcdef0123456789abcdef",
            http.MethodGet,
            parsed.EscapedPath(),
            wantExp,
        )
        if err != nil {
            t.Fatalf("expected signature: %v", err)
        }
        if parsed.Query().Get("sig") != wantSig {
            t.Fatalf("URL %q signature = %q, want %q", rawURL, parsed.Query().Get("sig"), wantSig)
        }
    }
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```bash
GOCACHE=/tmp/go-build-cache go test ./internal/api -run 'TestMedia(FileIDs|URLSigner)' -count=1
```

Expected: compilation fails because mediaFileIDs, newMediaURLSigner, and mediaURLSigner.mediaURL do not exist.

- [ ] **Step 3: Implement media ID selection and the signer**

In internal/api/search_media.go, add:

```go
type mediaURLSigner struct {
    key string
    exp string
}

func newMediaURLSigner(key string, expiresAt time.Time) mediaURLSigner {
    exp := ""
    if key != "" {
        exp = strconv.FormatInt(expiresAt.UTC().Unix(), 10)
    }
    return mediaURLSigner{key: key, exp: exp}
}

func (h handlers) requestMediaURLSigner(ctx context.Context, signed bool) (mediaURLSigner, error) {
    if !signed || h.deps.APIKeyService == nil {
        return mediaURLSigner{}, nil
    }
    active, err := h.deps.APIKeyService.EnsureActive(ctx)
    if err != nil {
        return mediaURLSigner{}, err
    }
    return newMediaURLSigner(active.Key, time.Now().UTC().Add(searchMediaURLTTL)), nil
}

func (s mediaURLSigner) mediaURL(kind string, telegramFileID int64) (string, error) {
    path := "/" + kind + "/" + url.PathEscape(strconv.FormatInt(telegramFileID, 10))
    if s.key == "" {
        return path, nil
    }
    sig, err := apikey.MediaSignature(s.key, http.MethodGet, path, s.exp)
    if err != nil {
        return "", err
    }
    values := url.Values{}
    values.Set("exp", s.exp)
    values.Set("sig", sig)
    return path + "?" + values.Encode(), nil
}

func (s mediaURLSigner) mediaURLs(imageFileID int64, videoFileID int64) (*model.MediaURLs, error) {
    if imageFileID <= 0 && videoFileID <= 0 {
        return nil, nil
    }
    var media model.MediaURLs
    if imageFileID > 0 {
        imageURL, err := s.mediaURL("i", imageFileID)
        if err != nil {
            return nil, err
        }
        media.ImageURL = imageURL
    }
    if videoFileID > 0 {
        videoURL, err := s.mediaURL("v", videoFileID)
        if err != nil {
            return nil, err
        }
        media.VideoURL = videoURL
    }
    return &media, nil
}

func mediaFileIDs(messageType string, files []model.File) (int64, int64) {
    var imageFileID int64
    var videoFileID int64
    for _, file := range files {
        if file.TelegramFileID <= 0 {
            continue
        }
        if videoFileID == 0 && isVideoMedia(file.MimeType, file.Extension, file.FileName) {
            videoFileID = file.TelegramFileID
        }
        if imageFileID == 0 && isImageMedia(file.MimeType, file.Extension, file.FileName) {
            imageFileID = file.TelegramFileID
        }
    }
    if videoFileID > 0 && imageFileID == 0 {
        imageFileID = videoFileID
    }
    if messageType == "photo" && imageFileID == 0 {
        imageFileID = firstTelegramFileID(files)
    }
    return imageFileID, videoFileID
}
```

Replace the ID-selection body of searchResultMedia and replace handlers.mediaURLs and handlers.mediaURL with:

```go
func (h handlers) searchResultMedia(ctx context.Context, messageID int64, messageType string, files []model.File, signed bool) (*model.MediaURLs, error) {
    if len(files) == 0 && messageID > 0 && h.deps.Files != nil {
        found, err := h.deps.Files.FindByMessageID(ctx, messageID)
        if err != nil {
            return nil, err
        }
        files = found
    }
    imageFileID, videoFileID := mediaFileIDs(messageType, files)
    return h.mediaURLs(ctx, imageFileID, videoFileID, signed)
}

func (h handlers) mediaURLs(ctx context.Context, imageFileID int64, videoFileID int64, signed bool) (*model.MediaURLs, error) {
    signer, err := h.requestMediaURLSigner(ctx, signed)
    if err != nil {
        return nil, err
    }
    return signer.mediaURLs(imageFileID, videoFileID)
}

func (h handlers) mediaURL(ctx context.Context, kind string, telegramFileID int64, signed bool) (string, error) {
    signer, err := h.requestMediaURLSigner(ctx, signed)
    if err != nil {
        return "", err
    }
    return signer.mediaURL(kind, telegramFileID)
}
```

Leave callers outside public search unchanged; their behavior remains the same because handlers.mediaURLs and handlers.mediaURL retain their signatures.

- [ ] **Step 4: Format and verify GREEN**

Run:

```bash
gofmt -w internal/api/search_media.go internal/api/search_media_test.go
GOCACHE=/tmp/go-build-cache go test ./internal/api -run 'TestMedia(FileIDs|URLSigner)' -count=1
```

Expected: both focused tests pass.

- [ ] **Step 5: Run existing media API tests**

Run:

```bash
GOCACHE=/tmp/go-build-cache go test ./internal/api -run 'Test(ResourcesAPIMediaURLsRequireAdminSession|ExternalSearchRequiresAPIKeyAndReturnsPublicResourcesOnly|Media)' -count=1
```

Expected: PASS with existing signed and unsigned URL behavior unchanged.

- [ ] **Step 6: Commit the signer refactor**

```bash
git add internal/api/search_media.go internal/api/search_media_test.go
git commit -m "refactor: add request scoped media signer"
```

### Task 2: Batch public-search media enrichment

**Files:**
- Modify: internal/api/external_search.go
- Modify: internal/api/handlers_test.go

- [ ] **Step 1: Write the failing structural batching test**

Add beside the external-search tests in internal/api/handlers_test.go:

```go
func TestExternalResourceMediaRefsDeduplicateEligibleLinks(t *testing.T) {
    items := []resource.Item{
        {ID: "link:1", Kind: "link", ChannelID: 7, TelegramMessageID: 11},
        {ID: "link:2", Kind: "link", ChannelID: 7, TelegramMessageID: 11},
        {ID: "link:3", Kind: "link", ChannelID: 7, TelegramMessageID: 12},
        {ID: "file:4", Kind: "file", ChannelID: 7, TelegramMessageID: 13, TelegramFileID: 99},
        {ID: "invalid", Kind: "link"},
    }

    refs := externalResourceMediaRefs(items, true)
    if len(refs) != 2 {
        t.Fatalf("refs = %+v, want two deduplicated link references", refs)
    }
    if refs[0].ChannelID != 7 || refs[0].MessageID != 11 ||
        refs[1].ChannelID != 7 || refs[1].MessageID != 12 {
        t.Fatalf("refs = %+v, want message references 7:11 and 7:12", refs)
    }
    if got := externalResourceMediaRefs(items, false); len(got) != 0 {
        t.Fatalf("image-disabled refs = %+v, want none", got)
    }
}
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
GOCACHE=/tmp/go-build-cache go test ./internal/api -run TestExternalResourceMediaRefsDeduplicateEligibleLinks -count=1
```

Expected: compilation fails because externalResourceMediaRefs does not exist.

- [ ] **Step 3: Implement reference collection and batched attachment**

In internal/api/external_search.go, add:

```go
type externalResourceMediaRef struct {
    ChannelID int64
    MessageID int64
}

func externalResourceMediaRefs(items []resource.Item, includeImage bool) []externalResourceMediaRef {
    if !includeImage {
        return nil
    }
    refs := make([]externalResourceMediaRef, 0, len(items))
    seen := make(map[string]struct{}, len(items))
    for _, item := range items {
        if item.Kind == "file" || item.ChannelID <= 0 || item.TelegramMessageID <= 0 {
            continue
        }
        key := fmt.Sprintf("%d:%d", item.ChannelID, item.TelegramMessageID)
        if _, ok := seen[key]; ok {
            continue
        }
        seen[key] = struct{}{}
        refs = append(refs, externalResourceMediaRef{
            ChannelID: item.ChannelID,
            MessageID: item.TelegramMessageID,
        })
    }
    return refs
}
```

Convert the refs once at the repository boundary:

```go
repoRefs := make([]struct{ ChannelID, MessageID int64 }, len(refs))
for i, ref := range refs {
    repoRefs[i] = struct{ ChannelID, MessageID int64 }{
        ChannelID: ref.ChannelID,
        MessageID: ref.MessageID,
    }
}
filesByRef, err := h.deps.Files.FindByMessageRefs(ctx, repoRefs)
```

Replace attachMediaToExternalResourceItems with page-oriented logic:

```go
func (h handlers) attachMediaToExternalResourceItems(ctx context.Context, items []resource.Item, signed bool, includeImage bool) ([]resource.Item, error) {
    refs := externalResourceMediaRefs(items, includeImage)
    filesByRef := map[string][]model.File{}
    if len(refs) > 0 && h.deps.Files != nil {
        repoRefs := make([]struct{ ChannelID, MessageID int64 }, len(refs))
        for i, ref := range refs {
            repoRefs[i] = struct{ ChannelID, MessageID int64 }{
                ChannelID: ref.ChannelID,
                MessageID: ref.MessageID,
            }
        }
        found, err := h.deps.Files.FindByMessageRefs(ctx, repoRefs)
        if err != nil {
            return nil, err
        }
        filesByRef = found
    }

    type mediaIDs struct {
        image int64
        video int64
    }
    planned := make([]mediaIDs, len(items))
    hasMedia := false
    for i, item := range items {
        var files []model.File
        if item.Kind == "file" {
            files = []model.File{{
                TelegramFileID: item.TelegramFileID,
                FileName:       item.FileName,
                Extension:      item.Extension,
                MimeType:       item.MimeType,
                SizeBytes:      item.SizeBytes,
                Category:       item.Category,
            }}
        } else if includeImage {
            files = filesByRef[fmt.Sprintf("%d:%d", item.ChannelID, item.TelegramMessageID)]
        }

        imageFileID, videoFileID := mediaFileIDs(item.MessageType, files)
        if !includeImage {
            imageFileID = 0
        }
        if item.Kind != "file" {
            videoFileID = 0
        }
        planned[i] = mediaIDs{image: imageFileID, video: videoFileID}
        hasMedia = hasMedia || imageFileID > 0 || videoFileID > 0
    }
    if !hasMedia {
        return items, nil
    }

    signer, err := h.requestMediaURLSigner(ctx, signed)
    if err != nil {
        return nil, err
    }
    for i, ids := range planned {
        media, err := signer.mediaURLs(ids.image, ids.video)
        if err != nil {
            return nil, err
        }
        if media != nil {
            items[i].SetMediaURLs(media.ImageURL, media.VideoURL)
        }
    }
    return items, nil
}
```

Add fmt to external_search.go imports. Delete resourceItemMedia from search_media.go after confirming it has no remaining callers with:

```bash
rg -n "resourceItemMedia" internal
```

- [ ] **Step 4: Format and verify the batching test is GREEN**

Run:

```bash
gofmt -w internal/api/external_search.go internal/api/search_media.go internal/api/handlers_test.go
GOCACHE=/tmp/go-build-cache go test ./internal/api -run TestExternalResourceMediaRefsDeduplicateEligibleLinks -count=1
```

Expected: PASS.

- [ ] **Step 5: Add a multiple-image public-search regression test**

Add to internal/api/handlers_test.go:

```go
func TestExternalSearchReturnsMultipleSignedImages(t *testing.T) {
    ctx := context.Background()
    deps := testDeps(t)
    index := repository.NewResourceIndexRepository(deps.BackupDB)
    deps.Resources = resource.NewService(
        deps.Links,
        deps.Files,
        repository.NewResourceStatsRepository(deps.BackupDB),
        index,
    )

    accountID, err := deps.Accounts.Save(ctx, model.Account{
        Phone:    "+10000000000",
        Username: "main",
        Status:   model.AccountStatusOnline,
    })
    if err != nil {
        t.Fatalf("save account: %v", err)
    }
    channelID, err := deps.Channels.Save(ctx, model.Channel{
        AccountID:         accountID,
        TelegramChannelID: 1,
        Title:             "Mobile Resources",
        Type:              model.ChannelTypeChannel,
    })
    if err != nil {
        t.Fatalf("save channel: %v", err)
    }

    now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
    messages := make([]model.Message, 3)
    for i := range messages {
        messages[i] = model.Message{
            AccountID:        accountID,
            ChannelID:        channelID,
            TelegramMessageID: int64(i + 1),
            Text:             "mobile resource " + strconv.Itoa(i),
            RawJSON:          "{}",
            Date:             now.Add(-time.Duration(i) * time.Minute),
        }
    }
    stored, err := deps.Messages.SaveBatch(ctx, messages)
    if err != nil {
        t.Fatalf("save messages: %v", err)
    }
    for i, message := range stored {
        if _, err := deps.Links.SaveBatch(ctx, message.ID, []model.Link{{
            Type:     "mobile",
            Category: "cloud_drive",
            URL:      "https://yun.139.com/share/item-" + strconv.Itoa(i),
            Note:     "mobile item " + strconv.Itoa(i),
        }}); err != nil {
            t.Fatalf("save link %d: %v", i, err)
        }
        if _, err := deps.Files.SaveBatch(ctx, message.ID, []model.File{{
            TelegramFileID: int64(9000 + i),
            FileName:       "poster-" + strconv.Itoa(i) + ".jpg",
            Extension:      ".jpg",
            MimeType:       "image/jpeg",
            Category:       "image",
        }}); err != nil {
            t.Fatalf("save image %d: %v", i, err)
        }
    }
    if err := index.Rebuild(ctx); err != nil {
        t.Fatalf("rebuild resource index: %v", err)
    }

    router := NewRouter(deps)
    key := createTestAPIKey(t, router)
    request := httptest.NewRequest(
        http.MethodGet,
        "/api/search?cloud_types=mobile&limit=3&include_image=true",
        nil,
    )
    request.Header.Set("X-API-Key", key)
    response := httptest.NewRecorder()
    router.ServeHTTP(response, request)
    if response.Code != http.StatusOK {
        t.Fatalf("status = %d body=%s, want 200", response.Code, response.Body.String())
    }

    var body struct {
        Data struct {
            MergedByType map[string][]struct {
                Images []string `json:"images"`
            } `json:"merged_by_type"`
        } `json:"data"`
    }
    if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
        t.Fatalf("decode response: %v", err)
    }
    mobile := body.Data.MergedByType["mobile"]
    if len(mobile) != 3 {
        t.Fatalf("mobile results = %+v, want three", mobile)
    }
    for i, item := range mobile {
        if len(item.Images) != 1 || !strings.HasPrefix(item.Images[0], "/i/") {
            t.Fatalf("mobile result %d images = %+v, want one signed image", i, item.Images)
        }
        assertSignedMediaURL(t, deps.APIKeyService, item.Images[0])
    }
}
```

- [ ] **Step 6: Run external-search tests**

Run:

```bash
GOCACHE=/tmp/go-build-cache go test ./internal/api -run 'TestExternal(Search|Resource)' -count=1
```

Expected: PASS, including the new multiple-image test and existing metadata, filtering, pagination, logging, and signature checks.

- [ ] **Step 7: Commit the batching fix**

```bash
git add internal/api/external_search.go internal/api/search_media.go internal/api/handlers_test.go
git commit -m "perf: batch public search media enrichment"
```

### Task 3: Add the representative benchmark

**Files:**
- Create: internal/api/external_search_benchmark_test.go
- Modify: internal/api/handlers_test.go

- [ ] **Step 1: Add a 30-item benchmark**

Generalize the existing test helper parameter types in internal/api/handlers_test.go so benchmarks can reuse them:

```diff
-func testDeps(t *testing.T) Dependencies {
+func testDeps(t testing.TB) Dependencies {
     t.Helper()
     deps, _ := testDepsWithDB(t)
     return deps
 }

-func testDepsWithDB(t *testing.T) (Dependencies, *sql.DB) {
+func testDepsWithDB(t testing.TB) (Dependencies, *sql.DB) {
     t.Helper()
     root := t.TempDir()
```

Create internal/api/external_search_benchmark_test.go:

```go
package api

import (
    "context"
    "strconv"
    "testing"
    "time"

    "tg-search/internal/model"
    "tg-search/internal/resource"
)

func BenchmarkAttachMediaToExternalResourceItems30(b *testing.B) {
    ctx := context.Background()
    deps := testDeps(b)
    h := handlers{deps: deps}

    accountID, err := deps.Accounts.Save(ctx, model.Account{
        Phone:    "+10000000000",
        Username: "benchmark",
        Status:   model.AccountStatusOnline,
    })
    if err != nil {
        b.Fatal(err)
    }
    channelID, err := deps.Channels.Save(ctx, model.Channel{
        AccountID:         accountID,
        TelegramChannelID: 1,
        Title:             "Benchmark",
        Type:              model.ChannelTypeChannel,
    })
    if err != nil {
        b.Fatal(err)
    }

    now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
    messages := make([]model.Message, 30)
    for i := range messages {
        messages[i] = model.Message{
            AccountID:        accountID,
            ChannelID:        channelID,
            TelegramMessageID: int64(i + 1),
            Text:             "benchmark item " + strconv.Itoa(i),
            RawJSON:          "{}",
            Date:             now.Add(-time.Duration(i) * time.Minute),
        }
    }
    stored, err := deps.Messages.SaveBatch(ctx, messages)
    if err != nil {
        b.Fatal(err)
    }

    items := make([]resource.Item, 0, len(stored))
    for i, message := range stored {
        if _, err := deps.Files.SaveBatch(ctx, message.ID, []model.File{{
            TelegramFileID: int64(10000 + i),
            FileName:       "poster-" + strconv.Itoa(i) + ".jpg",
            Extension:      ".jpg",
            MimeType:       "image/jpeg",
            Category:       "image",
        }}); err != nil {
            b.Fatal(err)
        }
        items = append(items, resource.Item{
            ID:                "link:" + strconv.Itoa(i),
            Kind:              "link",
            ChannelID:         channelID,
            TelegramMessageID: message.TelegramMessageID,
            MessageType:       "photo",
        })
    }
    if _, err := deps.APIKeyService.EnsureActive(ctx); err != nil {
        b.Fatal(err)
    }

    b.ReportAllocs()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        itemsCopy := append([]resource.Item(nil), items...)
        if _, err := h.attachMediaToExternalResourceItems(ctx, itemsCopy, true, true); err != nil {
            b.Fatal(err)
        }
    }
}
```

Do not add a wall-clock assertion.

- [ ] **Step 2: Run the benchmark once**

Run:

```bash
GOCACHE=/tmp/go-build-cache go test ./internal/api -run '^$' -bench BenchmarkAttachMediaToExternalResourceItems30 -benchtime=1x
```

Expected: benchmark completes successfully and reports one iteration without errors.

- [ ] **Step 3: Commit the benchmark**

```bash
git add internal/api/external_search_benchmark_test.go internal/api/handlers_test.go
git commit -m "test: benchmark public search media enrichment"
```

### Task 4: Verify the complete change

**Files:**
- Verify all modified Go files and commits.

- [ ] **Step 1: Run formatting and static diff checks**

```bash
gofmt -w internal/api/search_media.go internal/api/search_media_test.go internal/api/external_search.go internal/api/handlers_test.go internal/api/external_search_benchmark_test.go
git diff --check
```

Expected: no output from git diff --check.

- [ ] **Step 2: Run focused package tests**

```bash
GOCACHE=/tmp/go-build-cache go test ./internal/api -count=1
```

Expected: PASS.

- [ ] **Step 3: Run the full Go suite**

```bash
GOCACHE=/tmp/go-build-cache go test ./...
```

Expected: all packages pass.

- [ ] **Step 4: Build the backend binary**

```bash
go build -o /tmp/tg-search ./cmd/tg-search
```

Expected: exit status 0 and /tmp/tg-search exists.

- [ ] **Step 5: Inspect the final branch**

```bash
git status --short --branch
git log --oneline --decorate -5
```

Expected: clean fix/public-search-performance branch containing the design, signer, batching, and benchmark commits.
