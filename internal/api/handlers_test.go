package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"tg-search/internal/adminauth"
	"tg-search/internal/apikey"
	"tg-search/internal/build"
	"tg-search/internal/channel"
	"tg-search/internal/config"
	"tg-search/internal/db"
	"tg-search/internal/history"
	"tg-search/internal/link"
	"tg-search/internal/messagefilter"
	"tg-search/internal/model"
	"tg-search/internal/notification"
	"tg-search/internal/repository"
	"tg-search/internal/resource"
	"tg-search/internal/retry"
	"tg-search/internal/scheduler"
	"tg-search/internal/search"
	"tg-search/internal/session"
	"tg-search/internal/storage"
	taskpkg "tg-search/internal/task"
	"tg-search/internal/telegram"
)

func TestCoreReadAPIs(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "庆余年 https://example.com/a", RawJSON: "{}", Date: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	_, _ = deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "url", URL: "https://example.com/a"}})
	router := NewRouter(deps)

	for _, tc := range []struct {
		path string
		key  string
	}{
		{"/api/status", "accounts"},
		{"/api/admin/search?q=庆余年", "items"},
		{"/api/messages/latest?limit=10", "items"},
		{"/api/links?type=url", "items"},
		{"/api/accounts", "items"},
		{"/api/channels", "items"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		withAdminSession(t, deps, req)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", tc.path, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s invalid JSON: %v", tc.path, err)
		}
		if _, ok := body[tc.key]; !ok {
			t.Fatalf("%s response missing key %q: %s", tc.path, tc.key, w.Body.String())
		}
	}
}

func TestLogsAPIListsAndDownloadsLogFiles(t *testing.T) {
	deps := testDeps(t)
	logDir := filepath.Join(deps.RuntimeConfig.Storage.Path, "logs")
	logData := strings.Join([]string{
		`{"level":"info","ts":"2026-06-09T10:00:00.000+0800","caller":"cmd/main.go:1","msg":"boot complete"}`,
		`{"level":"error","ts":"2026-06-09T10:01:00.000+0800","caller":"cmd/main.go:2","msg":"worker failed","error":"boom"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "app.log"), []byte(logData), 0o600); err != nil {
		t.Fatalf("write app log: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/logs?file=app.log&q=boot&order=asc&limit=20", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list logs status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct {
			File    string `json:"file"`
			Level   string `json:"level"`
			Message string `json:"message"`
			Raw     string `json:"raw"`
		} `json:"items"`
		Total int `json:"total"`
		Files []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Total != 1 || len(body.Items) != 1 || body.Items[0].Message != "boot complete" {
		t.Fatalf("logs body = %+v, want boot entry", body)
	}
	if len(body.Files) != 4 || body.Files[0].Name != "app.log" || body.Files[0].Size == 0 {
		t.Fatalf("files metadata = %+v, want app.log with size", body.Files)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/logs/app.log/download", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("download log status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="app.log"`) {
		t.Fatalf("Content-Disposition = %q, want app.log attachment", got)
	}
	if !strings.Contains(w.Body.String(), "worker failed") {
		t.Fatalf("download body = %q, want log data", w.Body.String())
	}
}

func TestGlobalSearchAPI(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	deps.APIKeyService = apikey.NewService(deps.APIKeys, deps.Settings)
	files := repository.NewFileRepository(deps.BackupDB)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "Ubuntu Channel", Username: "ubuntu_resources", Type: model.ChannelTypeChannel})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
		Text: "ubuntu release mirror", RawJSON: "{}", Date: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "url", Category: "http", URL: "https://example.com/ubuntu", Note: "ubuntu download"}}); err != nil {
		t.Fatalf("save links: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[0].ID, []model.File{{FileName: "ubuntu.iso", Extension: ".iso", MimeType: "application/x-iso9660-image", SizeBytes: 5000}}); err != nil {
		t.Fatalf("save files: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/search/global?q=ubuntu", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body model.GlobalSearchResult
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Messages.Total != 1 || body.Links.Total != 1 || body.Files.Total != 1 || body.Channels.Total != 1 {
		t.Fatalf("global search body = %+v, want one item per group", body)
	}
}

func TestGlobalSearchAPIEncodesEmptyGroupItemsAsArray(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "推理频道", Username: "mystery", Type: model.ChannelTypeChannel})
	if _, err := deps.Messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
		Text: "推理 小说", RawJSON: "{}", Date: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("save messages: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/search/global?q=%E6%8E%A8%E7%90%86&limit=50", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Files struct {
			Items json.RawMessage `json:"items"`
			Total int             `json:"total"`
		} `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if string(body.Files.Items) != "[]" || body.Files.Total != 0 {
		t.Fatalf("files group raw items = %s total=%d, want [] and total 0; body=%s", string(body.Files.Items), body.Files.Total, w.Body.String())
	}
}

func TestSearchAPIReturnsMediaProxyURLs(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	files := repository.NewFileRepository(deps.BackupDB)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "Media Channel", Username: "media_channel", Type: model.ChannelTypeChannel})
	now := time.Now().UTC()
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 101,
			MessageType: "photo", MediaSummary: "photo", Text: "media poster", RawJSON: "{}", Date: now,
		},
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 102,
			MessageType: "video", MediaSummary: "video/mp4", Text: "media trailer", RawJSON: "{}", Date: now.Add(-time.Minute),
		},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[0].ID, []model.File{{TelegramFileID: 201001, FileName: "poster.jpg", Extension: ".jpg", MimeType: "image/jpeg", SizeBytes: 500}}); err != nil {
		t.Fatalf("save photo file: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[1].ID, []model.File{{TelegramFileID: 202001, FileName: "trailer.mp4", Extension: ".mp4", MimeType: "video/mp4", SizeBytes: 5000}}); err != nil {
		t.Fatalf("save file: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[1].ID, []model.Link{{Type: "url", Category: "http", URL: "https://example.com/media-trailer", Note: "media trailer"}}); err != nil {
		t.Fatalf("save link: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/search/global?q=media", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body model.GlobalSearchResult
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	messages := map[int64]model.SearchResult{}
	for _, item := range body.Messages.Items {
		messages[item.TelegramMessageID] = item
	}
	if messages[101].Media == nil || messages[101].Media.ImageURL != "/i/201001" || messages[101].Media.VideoURL != "" {
		t.Fatalf("photo media = %+v", messages[101].Media)
	}
	if messages[102].Media == nil || messages[102].Media.ImageURL != "/i/202001" || messages[102].Media.VideoURL != "/v/202001" {
		t.Fatalf("video message media = %+v", messages[102].Media)
	}
	if len(body.Links.Items) != 1 || body.Links.Items[0].Media == nil || body.Links.Items[0].Media.ImageURL != "/i/202001" || body.Links.Items[0].Media.VideoURL != "/v/202001" {
		t.Fatalf("link media = %+v", body.Links.Items)
	}
	var trailerFile *model.FileResult
	for i := range body.Files.Items {
		if body.Files.Items[i].TelegramFileID == 202001 {
			trailerFile = &body.Files.Items[i]
			break
		}
	}
	if trailerFile == nil || trailerFile.Media == nil || trailerFile.Media.VideoURL != "/v/202001" {
		t.Fatalf("file media = %+v", body.Files.Items)
	}
}

func TestResourcesAPI(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	files := repository.NewFileRepository(deps.BackupDB)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
		MediaSummary: "webpage_photo", Text: "ubuntu resources", RawJSON: "{}", Date: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{
		Type:          "url",
		Category:      "http",
		URL:           "https://example.com/ubuntu",
		Note:          "ubuntu",
		MediaTitle:    "Ubuntu Movie",
		MediaYear:     "2026",
		MediaSeason:   "S01",
		MediaEpisode:  "E02",
		MediaQuality:  "4K",
		MediaSize:     "12GB",
		MediaTMDBID:   "12345",
		MediaCategory: "movie",
		MediaTags:     "linux,release",
	}}); err != nil {
		t.Fatalf("save links: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[0].ID, []model.File{{FileName: "ubuntu.iso", Extension: ".iso", SizeBytes: 5000, Category: "software"}}); err != nil {
		t.Fatalf("save files: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resources?q=ubuntu", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items   []resource.Item  `json:"items"`
		Total   int              `json:"total"`
		Grouped *json.RawMessage `json:"grouped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Total != 2 {
		t.Fatalf("resources body = %+v, want link and file", body)
	}
	if body.Grouped != nil {
		t.Fatalf("resources response returned grouped stats: %s", w.Body.String())
	}
	var linkItem *resource.Item
	var fileItem *resource.Item
	for i := range body.Items {
		if body.Items[i].Kind == "link" {
			linkItem = &body.Items[i]
		}
		if body.Items[i].Kind == "file" {
			fileItem = &body.Items[i]
		}
	}
	if linkItem == nil || linkItem.Media == nil || linkItem.Media.Title != "Ubuntu Movie" || linkItem.Media.Year != "2026" || linkItem.Media.Quality != "4K" || linkItem.Media.TMDBID != "12345" || linkItem.Media.Category != "movie" || linkItem.Media.Tags != "linux,release" || linkItem.Media.Summary != "webpage_photo" {
		t.Fatalf("resource media metadata = %+v, want nested media metadata", linkItem)
	}
	if fileItem == nil || fileItem.FileName != "ubuntu.iso" || fileItem.SizeBytes != 5000 || fileItem.Category != "files" || fileItem.Type != "software" {
		t.Fatalf("resource file item = %+v, want ubuntu.iso with files category, software type, and size_bytes", fileItem)
	}
	for _, forbidden := range []string{"media_title", "media_year", "media_quality", "media_tmdb_id", "media_category", "media_tags", "media_summary"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("resources response used prefixed media field %q: %s", forbidden, w.Body.String())
		}
	}
}

func TestBulkDeleteResourcesAPI(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
		Text: "ubuntu resources", RawJSON: "{}", Date: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "url", Category: "http", URL: "https://example.com/ubuntu", Note: "ubuntu"}}); err != nil {
		t.Fatalf("save links: %v", err)
	}
	if _, err := deps.Files.SaveBatch(ctx, stored[0].ID, []model.File{{FileName: "ubuntu.iso", Extension: ".iso", SizeBytes: 5000, Category: "software"}}); err != nil {
		t.Fatalf("save files: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resources?q=ubuntu", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var list resource.ListResult
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("invalid list JSON: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("resources = %+v, want two items", list.Items)
	}

	body := fmt.Sprintf(`{"ids":[%q,%q,"file:999999"]}`, list.Items[0].ID, list.Items[1].ID)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/resources/bulk-delete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bulk delete status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var deleted resource.DeleteManyResult
	if err := json.Unmarshal(w.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("invalid delete JSON: %v", err)
	}
	if deleted.Deleted != 2 || len(deleted.MissingIDs) != 1 || deleted.MissingIDs[0] != "file:999999" {
		t.Fatalf("delete result = %+v, want two deleted and missing file", deleted)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/resources?q=ubuntu", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list after delete status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var after resource.ListResult
	if err := json.Unmarshal(w.Body.Bytes(), &after); err != nil {
		t.Fatalf("invalid after JSON: %v", err)
	}
	if after.Total != 0 {
		t.Fatalf("total after delete = %d, want 0", after.Total)
	}
}

func TestResourcesAPIMediaURLsRequireAdminSession(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	deps.APIKeyService = apikey.NewService(deps.APIKeys, deps.Settings)
	files := repository.NewFileRepository(deps.BackupDB)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "Media Channel", Username: "media_channel", Type: model.ChannelTypeChannel})
	now := time.Now().UTC()
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 201,
			MessageType: "photo", MediaSummary: "webpage_photo", Text: "poster link", RawJSON: "{}", Date: now,
		},
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 202,
			MessageType: "video", MediaSummary: "video/mp4", Text: "trailer file", RawJSON: "{}", Date: now.Add(-time.Minute),
		},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/poster", Note: "poster link"}}); err != nil {
		t.Fatalf("save link: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[1].ID, []model.File{{TelegramFileID: 202001, FileName: "trailer.mp4", Extension: ".mp4", MimeType: "video/mp4", SizeBytes: 5000}}); err != nil {
		t.Fatalf("save file: %v", err)
	}
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resources?q=trailer", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var adminBody resource.ListResult
	if err := json.Unmarshal(w.Body.Bytes(), &adminBody); err != nil {
		t.Fatalf("invalid admin JSON: %v", err)
	}
	if len(adminBody.Items) != 1 || adminBody.Items[0].Media == nil || adminBody.Items[0].Media.ImageURL != "/i/202001" || adminBody.Items[0].Media.VideoURL != "/v/202001" {
		t.Fatalf("admin resource media = %+v", adminBody.Items)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/resources?q=trailer", nil)
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("api key status = %d body=%s, want 401", w.Code, w.Body.String())
	}
}

func TestResourcesAPIHotSortReturnsScoreFields(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "Hot Channel", Type: model.ChannelTypeChannel})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
			Text: "hot resource", RawJSON: "{}", Date: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{
		Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/hot", Note: "Hot Resource",
		MediaTitle: "Hot Resource", MediaYear: "2026", MediaQuality: "4K", MediaCategory: "movie",
	}}); err != nil {
		t.Fatalf("save links: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resources?sort=hot", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body resource.ListResult
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %+v, want one hot resource", body.Items)
	}
	if body.Items[0].Score <= 0 || body.Items[0].ScoreExplain.MessageCount <= 0 || body.Items[0].ScoreExplain.TypeScore <= 0 {
		t.Fatalf("score fields = score:%d explain:%+v, want populated score fields", body.Items[0].Score, body.Items[0].ScoreExplain)
	}
}

func TestTrendingResourcesAPIUsesRangeAndHotSort(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	primaryChannelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "Primary", Type: model.ChannelTypeChannel})
	mirrorChannelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 2, Title: "Mirror", Type: model.ChannelTypeChannel})
	now := time.Now().UTC()
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: primaryChannelID, TelegramMessageID: 1, Text: "week hot", RawJSON: "{}", Date: now.Add(-48 * time.Hour)},
		{AccountID: accountID, ChannelID: mirrorChannelID, TelegramMessageID: 2, Text: "week hot mirror", RawJSON: "{}", Date: now.Add(-47 * time.Hour)},
		{AccountID: accountID, ChannelID: primaryChannelID, TelegramMessageID: 3, Text: "week weak", RawJSON: "{}", Date: now.Add(-time.Hour)},
		{AccountID: accountID, ChannelID: primaryChannelID, TelegramMessageID: 4, Text: "old resource", RawJSON: "{}", Date: now.AddDate(0, 0, -9)},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	for _, msg := range stored[:2] {
		if _, err := deps.Links.SaveBatch(ctx, msg.ID, []model.Link{{
			Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/week-hot", Note: "Week Hot",
			MediaTitle: "Week Hot", MediaYear: "2026", MediaQuality: "4K", MediaCategory: "movie",
		}}); err != nil {
			t.Fatalf("save hot link: %v", err)
		}
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[2].ID, []model.Link{{
		Type: "url", Category: "http", URL: "https://example.com/week-weak", Note: "Week Weak",
	}}); err != nil {
		t.Fatalf("save weak link: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[3].ID, []model.Link{{
		Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/old", Note: "Old Resource",
		MediaTitle: "Old Resource", MediaQuality: "4K",
	}}); err != nil {
		t.Fatalf("save old link: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/trending?range=week&limit=10", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body resource.ListResult
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %+v, want week resources only", body.Items)
	}
	if body.Items[0].URL != "https://pan.quark.cn/s/week-hot" {
		t.Fatalf("first trending resource = %+v, want higher scored week resource", body.Items[0])
	}
	for _, item := range body.Items {
		if item.URL == "https://pan.quark.cn/s/old" {
			t.Fatalf("trending items = %+v, want old resource outside week excluded", body.Items)
		}
	}
}

func TestTrendingResourcesAPIRejectsInvalidRange(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/trending?range=year", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestResourcesGroupedReturnsGlobalCountsOutsideListWindow(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	now := time.Now().UTC()

	messages := make([]model.Message, 0, 202)
	for i := 0; i < 201; i++ {
		messages = append(messages, model.Message{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: int64(i + 1),
			Text: "http resource", RawJSON: "{}", Date: now.Add(time.Duration(i) * time.Minute),
		})
	}
	messages = append(messages, model.Message{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1000,
		Text: "cloud resource", RawJSON: "{}", Date: now.Add(-time.Hour),
	})
	stored, err := deps.Messages.SaveBatch(ctx, messages)
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	for i := 0; i < 201; i++ {
		if _, err := deps.Links.SaveBatch(ctx, stored[i].ID, []model.Link{{
			Type: "url", Category: "http", URL: "https://example.com/" + strconv.Itoa(i),
		}}); err != nil {
			t.Fatalf("save http link %d: %v", i, err)
		}
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[201].ID, []model.Link{{
		Type: "aliyun", Category: "cloud_drive", URL: "https://www.alipan.com/s/older",
	}}); err != nil {
		t.Fatalf("save cloud link: %v", err)
	}

	router := NewRouter(deps)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resources/grouped", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Grouped map[string]int `json:"grouped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Grouped["_total"] != 202 {
		t.Fatalf("grouped = %+v, want _total=202", body.Grouped)
	}
	if body.Grouped["http"] != 0 || body.Grouped["cloud_drive"] != 0 {
		t.Fatalf("grouped = %+v, want resources grouped endpoint to keep total-only category counts", body.Grouped)
	}
}

func TestLinksGroupedReturnsCountsByLinkType(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
			Text: "cloud resources", RawJSON: "{}", Date: time.Now().UTC(),
		},
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2,
			Text: "magnet resources", RawJSON: "{}", Date: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{
		{Type: "aliyun", Category: "cloud_drive", URL: "https://www.alipan.com/s/a"},
		{Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/b"},
	}); err != nil {
		t.Fatalf("save cloud links: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[1].ID, []model.Link{
		{Type: "magnet", Category: "magnet", URL: "magnet:?xt=urn:btih:abc"},
	}); err != nil {
		t.Fatalf("save magnet links: %v", err)
	}

	router := NewRouter(deps)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/links/grouped", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Grouped map[string]int `json:"grouped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Grouped["aliyun"] != 1 || body.Grouped["quark"] != 1 || body.Grouped["magnet"] != 1 {
		t.Fatalf("grouped = %+v, want counts by original link type", body.Grouped)
	}
}

func TestDashboardResourceStatsReturnsResourceTypeCounts(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
			Text: "cloud resource", RawJSON: "{}", Date: time.Now().UTC(),
		},
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2,
			Text: "magnet resource", RawJSON: "{}", Date: time.Now().UTC(),
		},
		{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: 3,
			Text: "file resource", RawJSON: "{}", Date: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/ubuntu"}}); err != nil {
		t.Fatalf("save cloud link: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[1].ID, []model.Link{{Type: "magnet", Category: "magnet", URL: "magnet:?xt=urn:btih:ubuntu"}}); err != nil {
		t.Fatalf("save magnet link: %v", err)
	}
	if _, err := deps.Files.SaveBatch(ctx, stored[2].ID, []model.File{{FileName: "ubuntu.iso", Extension: ".iso", SizeBytes: 5000, Category: "software"}}); err != nil {
		t.Fatalf("save file: %v", err)
	}

	router := NewRouter(deps)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/resource-stats", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Grouped map[string]int `json:"grouped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Grouped["cloud_drive"] != 1 || body.Grouped["magnet"] != 1 || body.Grouped["files"] != 1 || body.Grouped["_total"] != 3 {
		t.Fatalf("grouped = %+v, want cloud_drive=1 magnet=1 files=1 _total=3", body.Grouped)
	}
}

func TestResourcesAPIUsesCompleteTotalWithoutGroupedStats(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	now := time.Now().UTC()

	messages := make([]model.Message, 0, 202)
	for i := 0; i < 201; i++ {
		messages = append(messages, model.Message{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: int64(i + 1),
			Text: "http resource", RawJSON: "{}", Date: now.Add(time.Duration(i) * time.Minute),
		})
	}
	messages = append(messages, model.Message{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1000,
		Text: "cloud resource", RawJSON: "{}", Date: now.Add(-time.Hour),
	})
	stored, err := deps.Messages.SaveBatch(ctx, messages)
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	for i := 0; i < 201; i++ {
		if _, err := deps.Links.SaveBatch(ctx, stored[i].ID, []model.Link{{
			Type: "url", Category: "http", URL: "https://example.com/" + strconv.Itoa(i),
		}}); err != nil {
			t.Fatalf("save http link %d: %v", i, err)
		}
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[201].ID, []model.Link{{
		Type: "aliyun", Category: "cloud_drive", URL: "https://www.alipan.com/s/older",
	}}); err != nil {
		t.Fatalf("save cloud link: %v", err)
	}

	router := NewRouter(deps)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resources?limit=50&offset=100", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items   []resource.Item  `json:"items"`
		Total   int              `json:"total"`
		Grouped *json.RawMessage `json:"grouped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 50 {
		t.Fatalf("items len = %d, want 50", len(body.Items))
	}
	if body.Total != 202 {
		t.Fatalf("total = %d, want complete resource count", body.Total)
	}
	if body.Grouped != nil {
		t.Fatalf("resources response returned grouped stats: %s", w.Body.String())
	}
}

func TestResourcesAPIUsesCompleteTotalWithKeywordWithoutGroupedStats(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	now := time.Now().UTC()

	messages := make([]model.Message, 0, 202)
	for i := 0; i < 201; i++ {
		messages = append(messages, model.Message{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: int64(i + 1),
			Text: "ubuntu http resource", RawJSON: "{}", Date: now.Add(time.Duration(i) * time.Minute),
		})
	}
	messages = append(messages, model.Message{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1000,
		Text: "ubuntu cloud resource", RawJSON: "{}", Date: now.Add(-time.Hour),
	})
	stored, err := deps.Messages.SaveBatch(ctx, messages)
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	for i := 0; i < 201; i++ {
		if _, err := deps.Links.SaveBatch(ctx, stored[i].ID, []model.Link{{
			Type: "url", Category: "http", URL: "https://example.com/" + strconv.Itoa(i),
		}}); err != nil {
			t.Fatalf("save http link %d: %v", i, err)
		}
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[201].ID, []model.Link{{
		Type: "aliyun", Category: "cloud_drive", URL: "https://www.alipan.com/s/older",
	}}); err != nil {
		t.Fatalf("save cloud link: %v", err)
	}

	router := NewRouter(deps)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resources?q=ubuntu&limit=50", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items   []resource.Item  `json:"items"`
		Total   int              `json:"total"`
		Grouped *json.RawMessage `json:"grouped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 50 {
		t.Fatalf("items len = %d, want 50", len(body.Items))
	}
	if body.Total != 202 {
		t.Fatalf("total = %d, want complete resource count", body.Total)
	}
	if body.Grouped != nil {
		t.Fatalf("resources response returned grouped stats: %s", w.Body.String())
	}
}

func TestSavedSearchesAPI(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "电影频道", Type: model.ChannelTypeChannel})
	if err := deps.BotSubscriptions.UpsertChat(ctx, model.TelegramBotChat{ChatID: 42, Username: "harold", FirstName: "Harold", Type: "private", LastSeenAt: time.Now().UTC()}); err != nil {
		t.Fatalf("upsert bot chat: %v", err)
	}
	if err := deps.BotSubscriptions.UpsertChat(ctx, model.TelegramBotChat{ChatID: 43, Title: "资源群", Type: "group", LastSeenAt: time.Now().UTC()}); err != nil {
		t.Fatalf("upsert second bot chat: %v", err)
	}
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 10, Text: "哪吒3 4K 夸克网盘", RawJSON: "{}", Date: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "quark", URL: "https://pan.quark.cn/s/nezha3", Note: "哪吒3 4K"}}); err != nil {
		t.Fatalf("save links: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/telegram-bot/chats", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("telegram bot chats code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var chatsBody struct {
		Items []model.TelegramBotChat `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &chatsBody); err != nil {
		t.Fatalf("decode bot chats: %v", err)
	}
	if len(chatsBody.Items) != 2 || chatsBody.Items[0].ChatID == 0 {
		t.Fatalf("bot chats = %+v, want stored chats", chatsBody.Items)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/saved-searches", strings.NewReader(`{"keyword":"哪吒3","filters":{"category":"cloud_drive"},"notify_webhook":true,"notify_telegram":true,"telegram_chat_ids":[42]}`))
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create saved search code = %d body=%s, want 201", w.Code, w.Body.String())
	}
	var created model.SavedSearch
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode saved search: %v", err)
	}
	if created.ID == 0 || created.Name != "哪吒3" || !created.NotifyRSS || !created.NotifyWebhook || !created.NotifyTelegram || !created.Enabled {
		t.Fatalf("created saved search = %+v, want defaults and webhook enabled", created)
	}
	if got := created.TelegramChatIDs; len(got) != 1 || got[0] != 42 {
		t.Fatalf("created telegram chat ids = %+v, want [42]", got)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/saved-searches/"+strconv.FormatInt(created.ID, 10)+"/test", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("test saved search code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var testBody struct {
		Items []resource.Item `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &testBody); err != nil {
		t.Fatalf("decode test response: %v", err)
	}
	if testBody.Total != 1 || len(testBody.Items) != 1 || testBody.Items[0].Type != "quark" {
		t.Fatalf("test matches = %+v, want one quark resource", testBody)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/saved-searches/"+strconv.FormatInt(created.ID, 10), strings.NewReader(`{"name":"Nezha","keyword":"哪吒3","filters":{"category":"cloud_drive"},"notify_rss":true,"notify_webhook":true,"notify_telegram":true,"telegram_chat_ids":[43],"enabled":false}`))
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update saved search code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var updated model.SavedSearch
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated saved search: %v", err)
	}
	if updated.Enabled {
		t.Fatalf("updated saved search enabled = true, want false")
	}
	if got := updated.TelegramChatIDs; len(got) != 1 || got[0] != 43 {
		t.Fatalf("updated telegram chat ids = %+v, want [43]", got)
	}
}

func TestRSSFeedsRequireAPIKeyAndReturnResources(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "电影频道", Type: model.ChannelTypeChannel})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 10, Text: "哪吒3 4K 夸克网盘", RawJSON: "{}", Date: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "quark", URL: "https://pan.quark.cn/s/nezha3", Note: "哪吒3 4K"}}); err != nil {
		t.Fatalf("save links: %v", err)
	}
	savedID, err := deps.SavedSearches.Create(ctx, model.SavedSearch{
		Name:      "哪吒3",
		Keyword:   "哪吒3",
		NotifyRSS: true,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("create saved search: %v", err)
	}
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/search?q=%E5%93%AA%E5%90%923", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("feed without api key code = %d body=%s, want 401", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/feeds/search?q=%E5%93%AA%E5%90%923", nil)
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("feed search code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if contentType := w.Header().Get("Content-Type"); !strings.Contains(contentType, "application/rss+xml") {
		t.Fatalf("content type = %q, want rss", contentType)
	}
	if body := w.Body.String(); !strings.Contains(body, `<rss version="2.0">`) || !strings.Contains(body, "哪吒3 4K") || !strings.Contains(body, "https://pan.quark.cn/s/nezha3") {
		t.Fatalf("feed search body missing resource: %s", body)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/feeds/saved/"+strconv.FormatInt(savedID, 10), nil)
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("saved feed code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "tg-search saved search") || !strings.Contains(body, "哪吒3 4K") {
		t.Fatalf("saved feed body missing saved search resource: %s", body)
	}
}

func TestWebhooksAndNotificationDeliveriesAPI(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks", strings.NewReader(`{"name":"n8n","url":"https://example.com/hook","events":["resource.created"],"secret":"topsecret"}`))
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create webhook code = %d body=%s, want 201", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "topsecret") {
		t.Fatalf("webhook response leaked secret: %s", w.Body.String())
	}
	var hook model.Webhook
	if err := json.Unmarshal(w.Body.Bytes(), &hook); err != nil {
		t.Fatalf("decode webhook: %v", err)
	}
	if hook.ID == 0 || hook.URL != "https://example.com/hook" || len(hook.Events) != 1 {
		t.Fatalf("webhook = %+v, want created hook", hook)
	}
	stored, err := deps.Webhooks.FindByID(ctx, hook.ID)
	if err != nil {
		t.Fatalf("find webhook: %v", err)
	}
	if stored.Secret != "topsecret" {
		t.Fatalf("stored secret = %q, want topsecret", stored.Secret)
	}

	if _, err := deps.Deliveries.Create(ctx, model.NotificationDelivery{
		EventType:   model.NotificationEventResourceCreated,
		TargetType:  model.NotificationTargetWebhook,
		TargetID:    hook.ID,
		PayloadJSON: `{"title":"哪吒3"}`,
	}); err != nil {
		t.Fatalf("create delivery: %v", err)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/notification-deliveries?status=pending", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("deliveries code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items []model.NotificationDelivery `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode deliveries: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].TargetID != hook.ID {
		t.Fatalf("deliveries = %+v, want one webhook delivery", body.Items)
	}
}

func TestSetupAndAuthAPIs(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup status code = %d body=%s", w.Code, w.Body.String())
	}
	var status model.SetupStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode setup status: %v", err)
	}
	if status.AdminConfigured {
		t.Fatalf("admin_configured = true, want false")
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/setup/admin", bytes.NewBufferString(`{"username":"admin","password":"secret123"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("setup admin code = %d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret123"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login code = %d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != adminSessionCookie || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookies = %+v, want HttpOnly SameSite=Lax %s cookie", cookies, adminSessionCookie)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cookies[0])
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("me code = %d body=%s", w.Code, w.Body.String())
	}
	var me model.User
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Username != "admin" || me.PasswordHash != "" {
		t.Fatalf("me = %+v, want admin without password hash", me)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(cookies[0])
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("logout code = %d body=%s", w.Code, w.Body.String())
	}
	cleared := w.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge >= 0 || cleared[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("logout cookies = %+v, want cleared SameSite=Lax session", cleared)
	}
}

func TestSetupAndSettingsRequireAdminAfterBootstrap(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/admin", bytes.NewBufferString(`{"username":"second","password":"password123"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("second setup admin code = %d body=%s, want 403", w.Code, w.Body.String())
	}

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/setup/api-key", ""},
		{http.MethodPost, "/api/setup/telegram-api", `{"app_id":123456,"app_hash":"hash-secret"}`},
		{http.MethodPost, "/api/setup/listen-rules", `{"message_types":["text"],"link_types":["cloud_drive"]}`},
		{http.MethodPost, "/api/setup/complete", ""},
		{http.MethodGet, "/api/settings/telegram-api", ""},
		{http.MethodPut, "/api/settings/telegram-api", `{"app_id":123456,"app_hash":"hash-secret"}`},
		{http.MethodGet, "/api/settings/telegram-bot", ""},
		{http.MethodPut, "/api/settings/telegram-bot", `{"enabled":false,"poll_interval":"5s"}`},
		{http.MethodGet, "/api/settings/runtime", ""},
		{http.MethodPut, "/api/settings/runtime", `{"sync":{"workers":1,"history_batch_size":100,"telegram_request_interval":"1s"},"storage":{"max_db_size":1000000000,"max_media_cache":1000000000},"telegram":{"reconnect_timeout":"1m","dial_timeout":"10s","rate_limit":{"enabled":true,"rate_per_second":1,"burst":1},"stream":{"concurrency":1,"buffers":1,"chunk_timeout":"10s"},"media":{"concurrency":1}}}`},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated %s %s code = %d body=%s, want 401", tc.method, tc.path, w.Code, w.Body.String())
			}
		})
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/settings/runtime", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated runtime settings code = %d body=%s, want 200", w.Code, w.Body.String())
	}
}

func TestSetupAPIKeyAutoGeneratesAndSkipIsDisabled(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/api-key", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("api key code = %d body=%s", w.Code, w.Body.String())
	}
	var body model.APIKeyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode api key: %v", err)
	}
	if body.Name != "default" || len(body.Prefix) != 8 || len(body.Key) != 32 || body.Prefix != body.Key[:8] || strings.Contains(body.Key, "-") {
		t.Fatalf("api key response = %+v", body)
	}
	count, err := deps.APIKeys.CountEnabled(context.Background())
	if err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if count != 1 {
		t.Fatalf("enabled key count = %d, want 1", count)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup status code = %d body=%s", w.Code, w.Body.String())
	}
	var status model.SetupStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode setup status: %v", err)
	}
	if !status.APIKeyStepComplete {
		t.Fatalf("api key step complete = false, want true")
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/setup/api-key/skip", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("skip code = %d body=%s, want 404 or 405", w.Code, w.Body.String())
	}
}

func TestResourcesRequireAdminSession(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resources", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("resources without credentials code = %d body=%s, want 401", w.Code, w.Body.String())
	}

	for _, path := range []string{"/api/resources", "/api/resources/grouped"} {
		t.Run("api key denied "+path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-API-Key", key)
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s with api key code = %d body=%s, want 401", path, w.Code, w.Body.String())
			}
		})

		t.Run("admin "+path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(cookie)
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("%s with admin session code = %d body=%s, want 200", path, w.Code, w.Body.String())
			}
		})

		t.Run("invalid api key "+path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-API-Key", "invalid")
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s with invalid api key code = %d body=%s, want 401", path, w.Code, w.Body.String())
			}
		})
	}
}

func TestExternalSearchRequiresAPIKeyAndReturnsPublicResourcesOnly(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	deps.APIKeyService = apikey.NewService(deps.APIKeys, deps.Settings)
	files := repository.NewFileRepository(deps.BackupDB)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID: accountID, TelegramChannelID: 1, Title: "Private Channel", Username: "private_channel", Type: model.ChannelTypeChannel,
	})
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 101, Text: "ubuntu cloud raw message secret", RawJSON: "{}", Date: now},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 102, Text: "ubuntu magnet raw message secret", RawJSON: "{}", Date: now.Add(-time.Minute)},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 103, Text: "ubuntu ed2k raw message secret", RawJSON: "{}", Date: now.Add(-2 * time.Minute)},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 104, Text: "ubuntu video raw message secret", RawJSON: "{}", Date: now.Add(-3 * time.Minute)},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 105, Text: "ubuntu http raw message secret", RawJSON: "{}", Date: now.Add(-4 * time.Minute)},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 106, Text: "ubuntu archive raw message secret", RawJSON: "{}", Date: now.Add(-5 * time.Minute)},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{
		Type:          "quark",
		URL:           "https://pan.quark.cn/s/ubuntu",
		Password:      "pass",
		Note:          "Ubuntu Quark",
		MediaTitle:    "Ubuntu Movie",
		MediaYear:     "2026",
		MediaQuality:  "4K",
		MediaTMDBID:   "12345",
		MediaCategory: "movie",
		MediaTags:     "linux,release",
	}}); err != nil {
		t.Fatalf("save quark link: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[1].ID, []model.Link{{Type: "magnet", URL: "magnet:?xt=urn:btih:abcdef", Note: "Ubuntu Magnet"}}); err != nil {
		t.Fatalf("save magnet link: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[2].ID, []model.Link{{Type: "ed2k", URL: "ed2k://|file|ubuntu.mkv|123|ABCDEF|/", Note: "Ubuntu ED2K"}}); err != nil {
		t.Fatalf("save ed2k link: %v", err)
	}
	if _, err := deps.BackupDB.ExecContext(ctx, `UPDATE telegram_links SET category = '' WHERE type IN ('quark', 'magnet', 'ed2k')`); err != nil {
		t.Fatalf("clear legacy link categories: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[0].ID, []model.File{{TelegramFileID: 101001, FileName: "ubuntu-cover.jpg", Extension: ".jpg", MimeType: "image/jpeg", SizeBytes: 1000}}); err != nil {
		t.Fatalf("save quark cover file: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[3].ID, []model.File{{TelegramFileID: 104001, FileName: "ubuntu-trailer.mp4", Extension: ".mp4", MimeType: "video/mp4", SizeBytes: 5000}}); err != nil {
		t.Fatalf("save video file: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[4].ID, []model.Link{{Type: "url", URL: "https://example.com/ubuntu", Note: "Ubuntu HTTP"}}); err != nil {
		t.Fatalf("save http link: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[5].ID, []model.File{{FileName: "ubuntu-archive.zip", Extension: ".zip", MimeType: "application/zip", SizeBytes: 5000}}); err != nil {
		t.Fatalf("save archive file: %v", err)
	}
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)
	cookie := createAdminSession(t, router)

	for _, tc := range []struct {
		name       string
		configure  func(*http.Request)
		wantStatus int
	}{
		{name: "missing api key", wantStatus: http.StatusUnauthorized},
		{name: "admin cookie without api key", configure: func(req *http.Request) { req.AddCookie(cookie) }, wantStatus: http.StatusUnauthorized},
		{name: "invalid api key", configure: func(req *http.Request) { req.Header.Set("X-API-Key", "invalid") }, wantStatus: http.StatusUnauthorized},
		{name: "query api key rejected", configure: func(req *http.Request) { req.URL.RawQuery = "kw=ubuntu&api_key=" + url.QueryEscape(key) }, wantStatus: http.StatusUnauthorized},
		{name: "bearer authorization rejected", configure: func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+key) }, wantStatus: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/search?kw=ubuntu", nil)
			if tc.configure != nil {
				tc.configure(req)
			}
			router.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d body=%s, want %d", w.Code, w.Body.String(), tc.wantStatus)
			}
		})
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?kw=ubuntu", nil)
	req.Header.Set("Authorization", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("external search status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Total        int `json:"total"`
			MergedByType map[string][]struct {
				URL      string   `json:"url"`
				Password string   `json:"password"`
				Note     string   `json:"note"`
				Images   []string `json:"images"`
			} `json:"merged_by_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Code != 0 || body.Data.Total != 4 {
		t.Fatalf("external response = %+v body=%s, want code 0 total 4", body, w.Body.String())
	}
	for _, key := range []string{"quark", "magnet", "ed2k", "video"} {
		if len(body.Data.MergedByType[key]) != 1 {
			t.Fatalf("merged_by_type[%s] = %+v, want one item; body=%s", key, body.Data.MergedByType[key], w.Body.String())
		}
	}
	if body.Data.MergedByType["quark"][0].Password != "pass" {
		t.Fatalf("quark password = %q, want pass", body.Data.MergedByType["quark"][0].Password)
	}
	videoURL := body.Data.MergedByType["video"][0].URL
	if !strings.HasPrefix(videoURL, "/v/") {
		t.Fatalf("video URL = %q, want signed media proxy URL", videoURL)
	}
	assertSignedMediaURL(t, deps.APIKeyService, videoURL)
	responseText := w.Body.String()
	if strings.Contains(responseText, `\u0026`) {
		t.Fatalf("external response escaped media URL query separator: %s", responseText)
	}
	if strings.Contains(responseText, `"images"`) {
		t.Fatalf("external response returned images without include_image=true: %s", responseText)
	}
	for _, forbidden := range []string{
		"channel_id",
		"telegram_message_id",
		"channel_title",
		"private_channel",
		"raw message secret",
		"media_title",
		"Ubuntu Movie",
		"https://example.com/ubuntu",
		"ubuntu-archive.zip",
	} {
		if strings.Contains(responseText, forbidden) {
			t.Fatalf("external response leaked %q: %s", forbidden, responseText)
		}
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/search?kw=ubuntu&include_media_metadata=true", nil)
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("external search with media metadata status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var metadataBody struct {
		Code int `json:"code"`
		Data struct {
			Results []struct {
				Media *externalMedia `json:"media"`
				Links []struct {
					Media *externalMedia `json:"media"`
				} `json:"links"`
			} `json:"results"`
			MergedByType map[string][]struct {
				Media *externalMedia `json:"media"`
			} `json:"merged_by_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &metadataBody); err != nil {
		t.Fatalf("invalid metadata JSON: %v", err)
	}
	quarkMetadata := metadataBody.Data.MergedByType["quark"][0].Media
	if quarkMetadata == nil || quarkMetadata.Title != "Ubuntu Movie" || quarkMetadata.Year != "2026" || quarkMetadata.Quality != "4K" || quarkMetadata.TMDBID != "12345" || quarkMetadata.Category != "movie" || quarkMetadata.Tags != "linux,release" {
		t.Fatalf("merged quark metadata = %+v, want populated media metadata; body=%s", quarkMetadata, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "media_title") {
		t.Fatalf("metadata response used prefixed media field: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"images"`) {
		t.Fatalf("metadata response returned images without include_image=true: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/search?kw=ubuntu&include_image=true", nil)
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("external search with images status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var imageBody struct {
		Code int `json:"code"`
		Data struct {
			MergedByType map[string][]struct {
				Images []string `json:"images"`
			} `json:"merged_by_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &imageBody); err != nil {
		t.Fatalf("invalid image JSON: %v", err)
	}
	quarkImages := imageBody.Data.MergedByType["quark"][0].Images
	if len(quarkImages) != 1 || !strings.HasPrefix(quarkImages[0], "/i/") {
		t.Fatalf("quark images = %+v body=%s, want signed image proxy URL", quarkImages, w.Body.String())
	}
	assertSignedMediaURL(t, deps.APIKeyService, quarkImages[0])

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/search?kw=ubuntu&include_media_metadata=maybe", nil)
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid include_media_metadata code = %d body=%s, want 400", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/search?kw=ubuntu&include_image=maybe", nil)
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid include_image code = %d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestExternalSearchUsesIndexedQueryForDefaultCloudTypes(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	index := repository.NewResourceIndexRepository(deps.BackupDB)
	deps.Resources = resource.NewService(deps.Links, deps.Files, repository.NewResourceStatsRepository(deps.BackupDB), index)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "Public", Type: model.ChannelTypeChannel})
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "ubuntu quark", RawJSON: "{}", Date: now},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "ubuntu magnet", RawJSON: "{}", Date: now.Add(-time.Minute)},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/ubuntu", Note: "Ubuntu Quark"}}); err != nil {
		t.Fatalf("save quark: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[1].ID, []model.Link{{Type: "magnet", Category: "magnet", URL: "magnet:?xt=urn:btih:ubuntu", Note: "Ubuntu Magnet"}}); err != nil {
		t.Fatalf("save magnet: %v", err)
	}
	if err := index.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?kw=ubuntu&limit=10", nil)
	req.Header.Set("Authorization", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Total        int                             `json:"total"`
			MergedByType map[string][]externalMergedLink `json:"merged_by_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Code != 0 || body.Data.Total != 2 || len(body.Data.MergedByType["quark"]) != 1 || len(body.Data.MergedByType["magnet"]) != 1 {
		t.Fatalf("body = %+v raw=%s, want quark and magnet results", body, w.Body.String())
	}
}

func TestExternalSearchWritesAccessLog(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	deps := testDeps(t)
	deps.Logger = zap.New(core)
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?kw=ubuntu&res=results&cloud_types=quark&limit=5&offset=1&include_media_metadata=true", nil)
	req.Header.Set("X-API-Key", key)
	req.Header.Set("User-Agent", "tg-search-test")
	req.RemoteAddr = "203.0.113.10:12345"
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("external search status = %d body=%s, want 200", w.Code, w.Body.String())
	}

	entries := observed.FilterMessage("public search access").All()
	if len(entries) != 1 {
		t.Fatalf("access logs = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["method"] != http.MethodGet || fields["path"] != "/api/search" || fields["status"] != int64(http.StatusOK) {
		t.Fatalf("basic log fields = %+v", fields)
	}
	if fields["keyword"] != "ubuntu" || fields["result_type"] != "results" || fields["include_media_metadata"] != true {
		t.Fatalf("search log fields = %+v", fields)
	}
	if fields["limit"] != int64(5) || fields["offset"] != int64(1) || fields["total"] != int64(0) || fields["returned"] != int64(0) {
		t.Fatalf("result log fields = %+v", fields)
	}
	if fields["api_key_id"] == nil {
		t.Fatalf("log fields missing api_key_id: %+v", fields)
	}
	if _, ok := fields["api_key"]; ok {
		t.Fatalf("log fields leaked api_key: %+v", fields)
	}
	if _, ok := fields["authorization"]; ok {
		t.Fatalf("log fields leaked authorization: %+v", fields)
	}
}

func TestExternalSearchRanksResultsByQuality(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	oldDate := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	newDate := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	stored, _ := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "ubuntu 24.04 完整合集", RawJSON: "{}", Date: oldDate},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "ubuntu random mirror", RawJSON: "{}", Date: newDate},
	})
	_, _ = deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{
		Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/high", Note: "Ubuntu 24.04 最新合集", MediaTitle: "Ubuntu 24.04",
		MediaYear: "2026", MediaQuality: "4K", MediaCategory: "software",
	}})
	_, _ = deps.Links.SaveBatch(ctx, stored[1].ID, []model.Link{{
		Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/weak", Note: "random mirror",
	}})
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?kw=ubuntu&res=results&cloud_types=quark", nil)
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Results []struct {
				Links []struct {
					URL string `json:"url"`
				} `json:"links"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Code != 0 || len(body.Data.Results) != 2 || len(body.Data.Results[0].Links) != 1 {
		t.Fatalf("external search body = %+v raw=%s, want two result items", body, w.Body.String())
	}
	if body.Data.Results[0].Links[0].URL != "https://pan.quark.cn/s/high" {
		t.Fatalf("first external result = %+v raw=%s, want high quality result first", body.Data.Results[0], w.Body.String())
	}
}

func TestExternalResourceFiltersSupportRequestedTypeAliases(t *testing.T) {
	got := externalResourceFilters([]string{"百度", "阿里", "夸克", "光鸭", "天翼", "115", "迅雷", "UC", "移动", "PikPak", "123", "磁力", "电驴"})
	want := []externalResourceFilter{
		{category: "cloud_drive", typ: "baidu"},
		{category: "cloud_drive", typ: "aliyun"},
		{category: "cloud_drive", typ: "quark"},
		{category: "cloud_drive", typ: "guangya"},
		{category: "cloud_drive", typ: "tianyi"},
		{category: "cloud_drive", typ: "115"},
		{category: "cloud_drive", typ: "xunlei"},
		{category: "cloud_drive", typ: "uc"},
		{category: "cloud_drive", typ: "mobile"},
		{category: "cloud_drive", typ: "pikpak"},
		{category: "cloud_drive", typ: "123"},
		{category: "magnet"},
		{category: "ed2k"},
	}
	if len(got) != len(want) {
		t.Fatalf("filters len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filter[%d] = %+v, want %+v; all=%+v", i, got[i], want[i], got)
		}
	}
}

func TestExternalSearchAllowsEmptyKeywordAndLargeLimit(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	deps.APIKeyService = apikey.NewService(deps.APIKeys, deps.Settings)
	files := repository.NewFileRepository(deps.BackupDB)
	deps.Resources = resource.NewService(deps.Links, files)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID: accountID, TelegramChannelID: 1, Title: "Public Resources", Username: "public_resources", Type: model.ChannelTypeChannel,
	})
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	input := make([]model.Message, 0, 250)
	for i := 0; i < 250; i++ {
		input = append(input, model.Message{
			AccountID: accountID, ChannelID: channelID, TelegramMessageID: int64(i + 1),
			Text: "resource item", RawJSON: "{}", Date: now.Add(time.Duration(i) * time.Minute),
		})
	}
	stored, err := deps.Messages.SaveBatch(ctx, input)
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	for i, msg := range stored {
		if _, err := deps.Links.SaveBatch(ctx, msg.ID, []model.Link{{
			Type: "quark", URL: "https://pan.quark.cn/s/item-" + strconv.Itoa(i), Note: "item " + strconv.Itoa(i),
		}}); err != nil {
			t.Fatalf("save link %d: %v", i, err)
		}
	}
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?limit=250&res=results&cloud_types=quark", nil)
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("external search status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Total   int `json:"total"`
			Results []struct {
				Title    string    `json:"title"`
				Datetime time.Time `json:"datetime"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Code != 0 || body.Data.Total != 250 {
		t.Fatalf("external response = %+v body=%s, want code 0 total 250", body, w.Body.String())
	}
	if len(body.Data.Results) != 250 {
		t.Fatalf("results len = %d, want 250; body=%s", len(body.Data.Results), w.Body.String())
	}
	if body.Data.Results[0].Title != "item 249" || !body.Data.Results[0].Datetime.Equal(now.Add(249*time.Minute)) {
		t.Fatalf("first result = %+v, want latest item", body.Data.Results[0])
	}
}

func TestNormalizeExternalLimitAllowsUpToThreeThousand(t *testing.T) {
	for _, tc := range []struct {
		limit int
		want  int
	}{
		{limit: 0, want: 50},
		{limit: 250, want: 250},
		{limit: 3000, want: 3000},
		{limit: 3001, want: 3000},
	} {
		if got := normalizeExternalLimit(tc.limit); got != tc.want {
			t.Fatalf("normalizeExternalLimit(%d) = %d, want %d", tc.limit, got, tc.want)
		}
	}
}

func TestSearchPathServesFrontend(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/search?kw=ubuntu", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("search page status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if contentType := w.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("search page content type = %q, want text/html", contentType)
	}
	if cacheControl := w.Header().Get("Cache-Control"); cacheControl != "no-cache" {
		t.Fatalf("search page cache control = %q, want no-cache", cacheControl)
	}
	if strings.Contains(w.Body.String(), `"code":401`) || strings.Contains(w.Body.String(), "X-API-Key") {
		t.Fatalf("/search returned API response instead of frontend: %s", w.Body.String())
	}
}

func TestResourceDetailRequiresAdminSession(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	files := repository.NewFileRepository(deps.BackupDB)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
		Text: "ubuntu resources", RawJSON: "{}", Date: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := files.SaveBatch(ctx, stored[0].ID, []model.File{{FileName: "ubuntu.iso", Extension: ".iso", SizeBytes: 5000, Category: "software"}}); err != nil {
		t.Fatalf("save files: %v", err)
	}
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resources?type=files&q=ubuntu", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin resources code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var list resource.ListResult
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("invalid list JSON: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("resources list has no items: %+v", list)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/resources/"+url.PathEscape(list.Items[0].ID), nil)
	req.Header.Set("Authorization", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("resource detail with api key code = %d body=%s, want 401", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/resources/"+url.PathEscape(list.Items[0].ID), nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("resource detail with admin session code = %d body=%s, want 200", w.Code, w.Body.String())
	}
}

func TestAPIKeyCannotAccessAdminOnlyEndpoints(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)
	cookie := createAdminSession(t, router)

	for _, path := range []string{"/api/status", "/api/tasks", "/api/admin/search/global?q=ubuntu", "/api/admin/search?q=ubuntu"} {
		t.Run("api key denied "+path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-API-Key", key)
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s with api key code = %d body=%s, want 401", path, w.Code, w.Body.String())
			}
		})

		t.Run("admin allowed "+path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(cookie)
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("%s with admin session code = %d body=%s, want 200", path, w.Code, w.Body.String())
			}
		})
	}
}

func TestAPIKeySettingsViewAndRegenerate(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	first := createTestAPIKey(t, router)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/api-key", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("settings api key code = %d body=%s", w.Code, w.Body.String())
	}
	var current model.APIKeyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode current key: %v", err)
	}
	if current.Key != first {
		t.Fatalf("current key = %q, want first key", current.Key)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/settings/api-key/regenerate", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("regenerate code = %d body=%s", w.Code, w.Body.String())
	}
	var regenerated model.APIKeyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &regenerated); err != nil {
		t.Fatalf("decode regenerated key: %v", err)
	}
	if regenerated.Key == first || regenerated.Prefix != regenerated.Key[:8] {
		t.Fatalf("regenerated key = %+v, first key should be invalidated", regenerated)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/search", nil)
	req.Header.Set("X-API-Key", first)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("old key code = %d body=%s, want 401", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/search", nil)
	req.Header.Set("X-API-Key", regenerated.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("new key code = %d body=%s", w.Code, w.Body.String())
	}
}

func TestAdminSettingsUpdatesCredentialsAndSessionUser(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/settings/admin", bytes.NewBufferString(`{"username":"root","current_password":"wrong","new_password":"newpassword123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password code = %d body=%s, want 401", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings/admin", bytes.NewBufferString(`{"username":"root","current_password":"password123","new_password":"newpassword123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update admin settings code = %d body=%s", w.Code, w.Body.String())
	}
	var updated model.User
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated user: %v", err)
	}
	if updated.Username != "root" || updated.PasswordHash != "" {
		t.Fatalf("updated user = %+v, want root without password hash", updated)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("me after update code = %d body=%s", w.Code, w.Body.String())
	}
	var me model.User
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me after update: %v", err)
	}
	if me.Username != "root" {
		t.Fatalf("me username = %q, want root", me.Username)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"password123"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("old credentials login code = %d body=%s, want 401", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"root","password":"newpassword123"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("new credentials login code = %d body=%s", w.Code, w.Body.String())
	}
}

func createTestAPIKey(t *testing.T, router *gin.Engine) string {
	t.Helper()
	cookie := createAdminSession(t, router)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/api-key", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create api key code = %d body=%s", w.Code, w.Body.String())
	}
	var body model.APIKeyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode api key: %v", err)
	}
	return body.Key
}

func assertSignedMediaURL(t *testing.T, service *apikey.Service, rawURL string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse media URL %q: %v", rawURL, err)
	}
	values := parsed.Query()
	exp := values.Get("exp")
	sig := values.Get("sig")
	if exp == "" || sig == "" {
		t.Fatalf("media URL %q missing exp or sig", rawURL)
	}
	ok, err := service.VerifyMediaSignature(context.Background(), http.MethodGet, parsed.EscapedPath(), exp, sig, time.Now().UTC())
	if err != nil {
		t.Fatalf("verify media URL %q: %v", rawURL, err)
	}
	if !ok {
		t.Fatalf("media URL %q signature did not verify", rawURL)
	}
}

func createAdminSession(t *testing.T, router *gin.Engine) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/admin", bytes.NewBufferString(`{"username":"admin","password":"password123"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusForbidden {
		t.Fatalf("create admin code = %d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"password123"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login code = %d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("login did not set session cookie")
	}
	return cookies[0]
}

func withAdminSession(t *testing.T, deps Dependencies, req *http.Request) *http.Request {
	t.Helper()
	ctx := context.Background()
	user, err := deps.Users.FindByID(ctx, 1)
	if errors.Is(err, sql.ErrNoRows) {
		userID, createErr := deps.Users.Create(ctx, model.User{
			Username:     "admin",
			PasswordHash: "hash",
			Role:         model.UserRoleAdmin,
		})
		if createErr != nil {
			t.Fatalf("create admin user: %v", createErr)
		}
		user, err = deps.Users.FindByID(ctx, userID)
	}
	if err != nil {
		t.Fatalf("find admin user: %v", err)
	}
	token, err := deps.AdminAuth.CreateSession(ctx, user)
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: adminSessionCookie, Value: token})
	return req
}

func TestSetupListenRulesMarksStepConfigured(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/listen-rules", bytes.NewBufferString(`{
		"includes":["电影","网盘"],
		"excludes":["预告"],
		"message_types":["link","video","audio","file","text"],
		"link_types":["cloud_drive","magnet","ed2k","other"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup listen rules code = %d body=%s", w.Code, w.Body.String())
	}
	var status model.SetupStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode setup status: %v", err)
	}
	if !status.ListenRulesConfigured {
		t.Fatalf("listen rules configured = false, want true")
	}
}

func TestTelegramAPISettings(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/telegram-api", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get default telegram api code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	assertTelegramAPISettingsResponse(t, w.Body.Bytes(), false, 0)

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/setup/telegram-api", bytes.NewBufferString(`{"app_id":0,"app_hash":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup telegram api skip code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	assertTelegramAPISettingsResponse(t, w.Body.Bytes(), false, 0)
	if raw, ok, err := deps.Settings.Get(context.Background(), "telegram_api"); err != nil || ok {
		t.Fatalf("telegram_api setting = %q ok=%v err=%v, want not stored", raw, ok, err)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/setup/telegram-api", bytes.NewBufferString(`{"app_id":123456,"app_hash":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("partial setup telegram api code = %d body=%s, want 400", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/setup/telegram-api", bytes.NewBufferString(`{"app_id":123456,"app_hash":"hash-secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup telegram api code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	assertTelegramAPISettingsResponse(t, w.Body.Bytes(), true, 123456)
	if bytes.Contains(w.Body.Bytes(), []byte("hash-secret")) {
		t.Fatalf("setup telegram api response leaked app hash: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/settings/telegram-api", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get telegram api code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	assertTelegramAPISettingsResponse(t, w.Body.Bytes(), true, 123456)
	if bytes.Contains(w.Body.Bytes(), []byte("hash-secret")) {
		t.Fatalf("get telegram api response leaked app hash: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings/telegram-api", bytes.NewBufferString(`{"app_id":654321,"app_hash":"new-hash-secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("put telegram api code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	assertTelegramAPISettingsResponse(t, w.Body.Bytes(), true, 654321)
	if bytes.Contains(w.Body.Bytes(), []byte("new-hash-secret")) {
		t.Fatalf("put telegram api response leaked app hash: %s", w.Body.String())
	}
}

func TestTelegramBotSettings(t *testing.T) {
	deps := testDeps(t)
	deps.RuntimeConfig.Bot.PollInterval = config.Duration(3 * time.Second)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/telegram-bot", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get default telegram bot code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	assertTelegramBotSettingsResponse(t, w.Body.Bytes(), false, false, "3s")

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings/telegram-bot", bytes.NewBufferString(`{"enabled":true,"poll_interval":"5s"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("put enabled telegram bot without token code = %d body=%s, want 400", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings/telegram-bot", bytes.NewBufferString(`{"enabled":true,"token":"bot-secret","poll_interval":"5s"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("put telegram bot code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	assertTelegramBotSettingsResponse(t, w.Body.Bytes(), true, true, "5s")
	if bytes.Contains(w.Body.Bytes(), []byte("bot-secret")) {
		t.Fatalf("put telegram bot response leaked token: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings/telegram-bot", bytes.NewBufferString(`{"enabled":true,"token":"","poll_interval":"10s"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("put telegram bot preserving token code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	assertTelegramBotSettingsResponse(t, w.Body.Bytes(), true, true, "10s")

	loaded, err := deps.Settings.LoadTelegramBot(context.Background(), deps.RuntimeConfig.Bot)
	if err != nil {
		t.Fatalf("load telegram bot: %v", err)
	}
	if loaded.Token != "bot-secret" {
		t.Fatalf("loaded token = %q, want preserved token", loaded.Token)
	}
}

func TestRuntimeSettings(t *testing.T) {
	deps := testDeps(t)
	deps.RuntimeConfig.Sync.Workers = 5
	deps.RuntimeConfig.Sync.HistoryBatchSize = 100
	deps.RuntimeConfig.Sync.TelegramRequestInterval = config.Duration(2 * time.Second)
	deps.RuntimeConfig.Storage.MaxDBSize = config.Size(10 * 1024 * 1024 * 1024)
	deps.RuntimeConfig.Storage.MaxMediaCache = config.Size(20 * 1024 * 1024 * 1024)
	deps.RuntimeConfig.Telegram.Proxy = ""
	deps.RuntimeConfig.Telegram.ReconnectTimeout = config.Duration(5 * time.Minute)
	deps.RuntimeConfig.Telegram.DialTimeout = config.Duration(10 * time.Second)
	deps.RuntimeConfig.Telegram.RateLimit = config.TelegramRateLimitConfig{Enabled: true, RatePerSecond: 10, Burst: 5}
	deps.RuntimeConfig.Telegram.Stream = config.TelegramStreamConfig{Concurrency: 2, Buffers: 4, ChunkTimeout: config.Duration(20 * time.Second)}
	deps.RuntimeConfig.Telegram.Media = config.TelegramMediaConfig{Concurrency: 2}
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/runtime", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get runtime settings code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var got config.RuntimeSettings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode runtime settings: %v", err)
	}
	if got.Sync.Workers != 5 || got.Sync.HistoryBatchSize != 100 || got.Sync.TelegramRequestInterval != config.Duration(2*time.Second) {
		t.Fatalf("default runtime sync settings = %+v", got.Sync)
	}
	if got.Storage.MaxDBSize != config.Size(10*1024*1024*1024) || got.Storage.MaxMediaCache != config.Size(20*1024*1024*1024) {
		t.Fatalf("default runtime storage settings = %+v", got.Storage)
	}

	body := `{
		"sync":{"workers":8,"history_batch_size":250,"telegram_request_interval":"1500ms"},
		"storage":{"max_db_size":30000000000,"max_media_cache":40000000000},
		"telegram":{
			"proxy":"socks5://127.0.0.1:1080",
			"reconnect_timeout":"6m",
			"dial_timeout":"15s",
			"rate_limit":{"enabled":false,"rate_per_second":12,"burst":6},
			"stream":{"concurrency":4,"buffers":8,"chunk_timeout":"30s"},
			"media":{"concurrency":3}
		}
	}`
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings/runtime", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("put runtime settings code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode updated runtime settings: %v", err)
	}
	if got.Sync.Workers != 8 || got.Sync.HistoryBatchSize != 250 || got.Sync.TelegramRequestInterval != config.Duration(1500*time.Millisecond) {
		t.Fatalf("updated runtime sync settings = %+v", got.Sync)
	}
	if got.Telegram.RateLimit.Enabled || got.Telegram.RateLimit.RatePerSecond != 12 || got.Telegram.RateLimit.Burst != 6 {
		t.Fatalf("updated runtime rate limit = %+v", got.Telegram.RateLimit)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/settings/runtime", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get saved runtime settings code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode saved runtime settings: %v", err)
	}
	if got.Telegram.Proxy != "socks5://127.0.0.1:1080" || got.Telegram.Media.Concurrency != 3 {
		t.Fatalf("saved runtime settings = %+v", got)
	}
}

func TestRuntimeSettingsRedactsAIMediaMetadataAPIKeyAndPreservesExistingKey(t *testing.T) {
	deps := testDeps(t)
	deps.RuntimeConfig.Sync.Workers = 5
	deps.RuntimeConfig.Sync.HistoryBatchSize = 100
	deps.RuntimeConfig.Sync.TelegramRequestInterval = config.Duration(2 * time.Second)
	deps.RuntimeConfig.Storage.MaxDBSize = config.Size(10 * 1024 * 1024 * 1024)
	deps.RuntimeConfig.Storage.MaxMediaCache = config.Size(20 * 1024 * 1024 * 1024)
	deps.RuntimeConfig.Telegram.ReconnectTimeout = config.Duration(5 * time.Minute)
	deps.RuntimeConfig.Telegram.DialTimeout = config.Duration(10 * time.Second)
	deps.RuntimeConfig.Telegram.RateLimit = config.TelegramRateLimitConfig{Enabled: true, RatePerSecond: 10, Burst: 5}
	deps.RuntimeConfig.Telegram.Stream = config.TelegramStreamConfig{Concurrency: 2, Buffers: 4, ChunkTimeout: config.Duration(20 * time.Second)}
	deps.RuntimeConfig.Telegram.Media = config.TelegramMediaConfig{Concurrency: 2}
	existing := config.RuntimeSettingsFromConfig(deps.RuntimeConfig)
	existing.AI.MediaMetadata = config.AIMediaMetadataSettings{
		Enabled: true,
		BaseURL: "https://api.example.com/v1",
		APIKey:  "secret-key",
		Model:   "media-model",
	}
	if err := deps.Settings.SaveRuntimeSettings(context.Background(), existing); err != nil {
		t.Fatalf("save runtime settings: %v", err)
	}
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/runtime", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get runtime settings code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("secret-key")) {
		t.Fatalf("runtime settings response leaked api key: %s", w.Body.String())
	}
	var redacted struct {
		AI struct {
			MediaMetadata struct {
				Enabled   bool   `json:"enabled"`
				BaseURL   string `json:"base_url"`
				Model     string `json:"model"`
				APIKeySet bool   `json:"api_key_set"`
			} `json:"media_metadata"`
		} `json:"ai"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &redacted); err != nil {
		t.Fatalf("decode runtime settings response: %v", err)
	}
	if !redacted.AI.MediaMetadata.Enabled || redacted.AI.MediaMetadata.BaseURL != "https://api.example.com/v1" || redacted.AI.MediaMetadata.Model != "media-model" || !redacted.AI.MediaMetadata.APIKeySet {
		t.Fatalf("redacted ai media metadata = %+v", redacted.AI.MediaMetadata)
	}

	body := `{
		"sync":{"workers":5,"history_batch_size":100,"telegram_request_interval":"2s"},
		"storage":{"max_db_size":10737418240,"max_media_cache":21474836480},
		"telegram":{
			"proxy":"",
			"reconnect_timeout":"5m",
			"dial_timeout":"10s",
			"rate_limit":{"enabled":true,"rate_per_second":10,"burst":5},
			"stream":{"concurrency":2,"buffers":4,"chunk_timeout":"20s"},
			"media":{"concurrency":2}
		},
		"ai":{"media_metadata":{"enabled":true,"base_url":"https://api.example.com/v1","api_key":"","model":"better-model"}}
	}`
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings/runtime", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("put runtime settings code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("secret-key")) {
		t.Fatalf("put runtime settings response leaked api key: %s", w.Body.String())
	}
	stored, err := deps.Settings.LoadRuntimeSettings(context.Background(), deps.RuntimeConfig)
	if err != nil {
		t.Fatalf("load saved runtime settings: %v", err)
	}
	if stored.AI.MediaMetadata.APIKey != "secret-key" || stored.AI.MediaMetadata.Model != "better-model" {
		t.Fatalf("stored ai media metadata = %+v", stored.AI.MediaMetadata)
	}
}

func TestAIModelsEndpointListsProviderModelsFromRequest(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s, want /v1/models", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4.1-mini"},{"id":"qwen-plus"}]}`))
	}))
	defer server.Close()

	deps := testDeps(t)
	settings := config.RuntimeSettingsFromConfig(deps.RuntimeConfig)
	settings.AI.MediaMetadata.APIKey = "stored-key"
	if err := deps.Settings.SaveRuntimeSettings(context.Background(), settings); err != nil {
		t.Fatalf("save runtime settings: %v", err)
	}
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/ai/models", bytes.NewBufferString(`{"base_url":"`+server.URL+`/v1","api_key":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ai models code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer stored-key" {
		t.Fatalf("provider authorization = %q, want saved key", gotAuth)
	}
	var body struct {
		Items []string `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ai models response: %v", err)
	}
	if !reflect.DeepEqual(body.Items, []string{"gpt-4.1-mini", "qwen-plus"}) {
		t.Fatalf("items = %#v", body.Items)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("stored-key")) {
		t.Fatalf("ai models response leaked api key: %s", w.Body.String())
	}
}

func TestAIProvidersEndpointReturnsPresets(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/ai/providers", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ai providers code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			BaseURL        string `json:"base_url"`
			DefaultModel   string `json:"default_model"`
			Website        string `json:"website"`
			RequiresAPIKey bool   `json:"requires_api_key"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode providers response: %v", err)
	}
	var foundGroq bool
	for _, item := range body.Items {
		if item.ID == "groq" {
			foundGroq = true
			if item.BaseURL != "https://api.groq.com/openai/v1" || item.DefaultModel != "llama-3.3-70b-versatile" || item.Website == "" || !item.RequiresAPIKey {
				t.Fatalf("groq provider = %+v", item)
			}
		}
	}
	if !foundGroq {
		t.Fatalf("providers = %+v, want groq", body.Items)
	}
}

func TestAIModelsEndpointAllowsOllamaWithoutAPIKey(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s, want /v1/models", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen2.5:7b"}]}`))
	}))
	defer server.Close()

	deps := testDeps(t)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/ai/models", bytes.NewBufferString(`{"provider":"ollama","base_url":"`+server.URL+`/v1","api_key":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ai models code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if gotAuth != "" {
		t.Fatalf("provider authorization = %q, want empty", gotAuth)
	}
}

func TestAITestEndpointReturnsOKForNonEmptyReply(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
	}))
	defer server.Close()

	deps := testDeps(t)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/ai/test", bytes.NewBufferString(`{"provider":"groq","base_url":"`+server.URL+`/v1","api_key":"secret","model":"media-model"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ai test code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("provider authorization = %q, want saved key", gotAuth)
	}
	var body struct {
		OK        bool   `json:"ok"`
		Model     string `json:"model"`
		LatencyMS int64  `json:"latency_ms"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ai test response: %v", err)
	}
	if !body.OK || body.Model != "media-model" || body.LatencyMS < 0 {
		t.Fatalf("ai test response = %+v", body)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("secret")) {
		t.Fatalf("ai test response leaked api key: %s", w.Body.String())
	}
}

func TestAITestEndpointUsesSavedProviderKeyWhenRequestKeyEmpty(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
	}))
	defer server.Close()

	deps := testDeps(t)
	settings := config.RuntimeSettingsFromConfig(deps.RuntimeConfig)
	settings.AI.MediaMetadata = config.AIMediaMetadataSettings{
		Enabled:         true,
		FallbackEnabled: true,
		Providers: []config.AIMediaMetadataProviderSettings{
			{ID: "zhipu-main", Provider: "zhipu", BaseURL: server.URL + "/v1", APIKey: "stored-key", Model: "glm-4.7-flash", Enabled: true},
		},
	}
	if err := deps.Settings.SaveRuntimeSettings(context.Background(), settings); err != nil {
		t.Fatalf("save runtime settings: %v", err)
	}
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/ai/test", bytes.NewBufferString(`{"id":"zhipu-main","provider":"zhipu","base_url":"`+server.URL+`/v1","api_key":"","model":"glm-4.7-flash"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ai test code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer stored-key" {
		t.Fatalf("provider authorization = %q, want saved key", gotAuth)
	}
}

func TestAITestEndpointAcceptsProviderRowPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
	}))
	defer server.Close()

	deps := testDeps(t)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/ai/test", bytes.NewBufferString(`{
		"id":"groq-main",
		"name":"Groq",
		"provider":"groq",
		"baseURL":"`+server.URL+`/v1",
		"apiKey":"secret",
		"apiKeySet":true,
		"model":"media-model",
		"enabled":true
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ai test code = %d body=%s, want 200", w.Code, w.Body.String())
	}
}

func TestRuntimeSettingsRejectInvalidValues(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	cookie := createAdminSession(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/settings/runtime", bytes.NewBufferString(`{
		"sync":{"workers":0,"history_batch_size":100,"telegram_request_interval":"2s"},
		"storage":{"max_db_size":10000000000,"max_media_cache":20000000000},
		"telegram":{
			"reconnect_timeout":"5m",
			"dial_timeout":"10s",
			"rate_limit":{"enabled":true,"rate_per_second":10,"burst":5},
			"stream":{"concurrency":2,"buffers":4,"chunk_timeout":"20s"},
			"media":{"concurrency":2}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid runtime settings code = %d body=%s, want 400", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode invalid runtime settings error: %v", err)
	}
	if body.Error.Code != "bad_request" || body.Error.Message == "" {
		t.Fatalf("invalid runtime settings error = %s", w.Body.String())
	}
}

func TestVersionSettingsReportsGitHubRelease(t *testing.T) {
	originalVersion := build.Version
	originalURL := versionFileURL
	originalClient := versionHTTPClient
	defer func() {
		build.Version = originalVersion
		versionFileURL = originalURL
		versionHTTPClient = originalClient
	}()

	build.Version = "v1.2.3"
	versionFileURL = "https://d.har01d.test/tgs.version.txt"
	versionHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != versionFileURL {
			t.Fatalf("unexpected version URL: %s", req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("v1.2.4")),
		}, nil
	})}

	router := NewRouter(testDeps(t))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/version?check_update=true", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body model.VersionInfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.CurrentVersion != "v1.2.3" || body.LatestVersion != "v1.2.4" || !body.UpdateAvailable {
		t.Fatalf("version response = %+v", body)
	}
	if body.LatestURL != "https://github.com/power721/tg-search/releases/latest" {
		t.Fatalf("latest url = %q", body.LatestURL)
	}
}

func TestVersionSettingsReportsCurrentVersionWithoutGitHubCheck(t *testing.T) {
	originalVersion := build.Version
	originalClient := versionHTTPClient
	defer func() {
		build.Version = originalVersion
		versionHTTPClient = originalClient
	}()

	build.Version = "v1.2.3"
	versionHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected version request: %s", req.URL.String())
		return nil, nil
	})}

	router := NewRouter(testDeps(t))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/version", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body model.VersionInfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.CurrentVersion != "v1.2.3" || body.LatestVersion != "" || body.UpdateAvailable {
		t.Fatalf("version response = %+v", body)
	}
}

func TestVersionSettingsDoesNotClaimUpdateForDevVersion(t *testing.T) {
	originalVersion := build.Version
	originalURL := versionFileURL
	originalClient := versionHTTPClient
	defer func() {
		build.Version = originalVersion
		versionFileURL = originalURL
		versionHTTPClient = originalClient
	}()

	build.Version = "dev"
	versionFileURL = "https://d.har01d.test/tgs.version.txt"
	versionHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("v9.9.9")),
		}, nil
	})}

	router := NewRouter(testDeps(t))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/version?check_update=true", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body model.VersionInfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.CurrentVersion != "dev" || body.LatestVersion != "v9.9.9" || body.UpdateAvailable {
		t.Fatalf("version response = %+v", body)
	}
}

func TestSystemInfoSettingsReportsRuntimeSystemDetails(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/system-info", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body model.SystemInfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Name == "" {
		t.Fatalf("system name is empty: %+v", body)
	}
	if body.Architecture != runtime.GOARCH {
		t.Fatalf("architecture = %q, want %q", body.Architecture, runtime.GOARCH)
	}
	if body.GoVersion != runtime.Version() {
		t.Fatalf("go version = %q, want %q", body.GoVersion, runtime.Version())
	}
	if body.CPUCount <= 0 {
		t.Fatalf("cpu count = %d, want positive", body.CPUCount)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSetupStatusUsesDefaultTelegramAPIWhenNotStored(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup status code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var status model.SetupStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode setup status: %v", err)
	}
	if !status.TelegramConfigured {
		t.Fatalf("telegram configured = false, want true with default Telegram API settings")
	}
}

func TestStorageUsageAPI(t *testing.T) {
	deps := testDeps(t)
	root := t.TempDir()
	writeSizedFile(t, filepath.Join(root, "tg-search.db"), 10)
	writeSizedFile(t, filepath.Join(root, "index", "fts.data"), 20)
	writeSizedFile(t, filepath.Join(root, "thumbnails", "thumb.bin"), 30)
	deps.StorageUsage = storage.NewUsageService(config.Config{
		Storage: config.StorageConfig{
			Path:          root,
			MaxDBSize:     config.Size(100),
			MaxMediaCache: config.Size(100),
		},
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/storage/usage", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("storage usage code = %d body=%s", w.Code, w.Body.String())
	}
	var usage model.StorageUsage
	if err := json.Unmarshal(w.Body.Bytes(), &usage); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if usage.DBBytes != 10 || usage.IndexBytes != 20 || usage.MediaCacheBytes != 30 || usage.TotalBytes != 60 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestHealthAndReadyEndpoints(t *testing.T) {
	deps := testDeps(t)
	runtimeRoot := t.TempDir()
	deps.RuntimeConfig = config.Config{Storage: config.StorageConfig{Path: runtimeRoot}}
	if err := config.EnsureRuntimeDirs(deps.RuntimeConfig); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	router := NewRouter(deps)

	for _, tc := range []struct {
		path string
		key  string
	}{
		{"/api/health", "service"},
		{"/api/ready", "ready"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s, want 200", tc.path, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s invalid JSON: %v", tc.path, err)
		}
		if _, ok := body[tc.key]; !ok {
			t.Fatalf("%s response missing key %q: %s", tc.path, tc.key, w.Body.String())
		}
	}
}

func TestHealthValidatesProvidedAPIKeyAndReturnsVersion(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Authorization", key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("health with api key status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("health with api key invalid JSON: %v", err)
	}
	if body["service"] != "ok" || body["version"] == "" {
		t.Fatalf("health with api key response = %+v, want service ok and version", body)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("X-API-Key", "invalid")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("health with invalid api key status = %d body=%s, want 401", w.Code, w.Body.String())
	}
}

func TestReadyEndpointFailsWhenRuntimeDirsAreInvalid(t *testing.T) {
	deps := testDeps(t)
	deps.RuntimeConfig = config.Config{Storage: config.StorageConfig{Path: t.TempDir()}}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d body=%s, want 503", w.Code, w.Body.String())
	}
	var body struct {
		Ready bool `json:"ready"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Ready {
		t.Fatal("ready = true, want false")
	}
}

func TestTaskAPIReturnsEmptyItemsArray(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"items":[]`)) {
		t.Fatalf("list body = %s, want empty items array", w.Body.String())
	}
}

func TestTaskAPILocalizesCompletedMessage(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	router := NewRouter(deps)

	completed, err := deps.Tasks.Enqueue(ctx, model.TaskTypeHistorySync, map[string]any{"channel_id": 1})
	if err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	if err := deps.Tasks.Start(ctx, completed.ID); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if err := deps.Tasks.Succeed(ctx, completed.ID, "completed"); err != nil {
		t.Fatalf("succeed task: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var listBody struct {
		Items []model.Task `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listBody.Items) != 1 || listBody.Items[0].Message != "已完成" {
		t.Fatalf("list items = %+v, want localized completed message", listBody.Items)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/tasks/"+strconv.FormatInt(completed.ID, 10), nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var detail model.Task
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Message != "已完成" {
		t.Fatalf("detail message = %q, want 已完成", detail.Message)
	}
}

func TestTaskAPI(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	router := NewRouter(deps)

	failed, err := deps.Tasks.Enqueue(ctx, model.TaskTypeHistorySync, map[string]any{"channel_id": 1})
	if err != nil {
		t.Fatalf("enqueue failed task: %v", err)
	}
	if err := deps.Tasks.Start(ctx, failed.ID); err != nil {
		t.Fatalf("start failed task: %v", err)
	}
	if err := deps.Tasks.Fail(ctx, failed.ID, "temporary", "temporary failure"); err != nil {
		t.Fatalf("fail task: %v", err)
	}

	running, err := deps.Tasks.Enqueue(ctx, model.TaskTypeHistorySync, map[string]any{"channel_id": 2})
	if err != nil {
		t.Fatalf("enqueue running task: %v", err)
	}
	if err := deps.Tasks.Start(ctx, running.ID); err != nil {
		t.Fatalf("start running task: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var listBody struct {
		Items []model.Task `json:"items"`
		Total int          `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listBody.Items) != 2 {
		t.Fatalf("list items = %+v, want 2 tasks", listBody.Items)
	}
	if listBody.Total != 2 {
		t.Fatalf("list total = %d, want 2", listBody.Total)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/tasks/"+strconv.FormatInt(failed.ID, 10), nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var detail model.Task
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.ID != failed.ID || detail.Status != model.TaskStatusFailed {
		t.Fatalf("detail = %+v, want failed task", detail)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/tasks/"+strconv.FormatInt(failed.ID, 10)+"/retry", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("retry status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var retried model.Task
	if err := json.Unmarshal(w.Body.Bytes(), &retried); err != nil {
		t.Fatalf("decode retry: %v", err)
	}
	if retried.Status != model.TaskStatusQueued || retried.RetryCount != 1 {
		t.Fatalf("retried = %+v, want queued retry_count=1", retried)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/tasks/"+strconv.FormatInt(running.ID, 10)+"/cancel", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var canceling model.Task
	if err := json.Unmarshal(w.Body.Bytes(), &canceling); err != nil {
		t.Fatalf("decode cancel: %v", err)
	}
	if canceling.Status != model.TaskStatusCanceling {
		t.Fatalf("canceling = %+v, want canceling", canceling)
	}

	deletable, err := deps.Tasks.Enqueue(ctx, model.TaskTypeHistorySync, map[string]any{"channel_id": 3})
	if err != nil {
		t.Fatalf("enqueue deletable task: %v", err)
	}
	if err := deps.Tasks.Start(ctx, deletable.ID); err != nil {
		t.Fatalf("start deletable task: %v", err)
	}
	if err := deps.Tasks.Fail(ctx, deletable.ID, "temporary", "temporary failure"); err != nil {
		t.Fatalf("fail deletable task: %v", err)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/tasks/"+strconv.FormatInt(deletable.ID, 10), nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if _, err := deps.TaskRepository.FindByID(ctx, deletable.ID); err == nil {
		t.Fatal("FindByID succeeded after task delete, want missing task")
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/tasks/"+strconv.FormatInt(running.ID, 10), nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("delete running status = %d body=%s, want 409", w.Code, w.Body.String())
	}

	bulkFailed, err := deps.Tasks.Enqueue(ctx, model.TaskTypeHistorySync, map[string]any{"channel_id": 4})
	if err != nil {
		t.Fatalf("enqueue bulk failed task: %v", err)
	}
	if err := deps.Tasks.Start(ctx, bulkFailed.ID); err != nil {
		t.Fatalf("start bulk failed task: %v", err)
	}
	if err := deps.Tasks.Fail(ctx, bulkFailed.ID, "temporary", "temporary failure"); err != nil {
		t.Fatalf("fail bulk task: %v", err)
	}
	bulkSucceeded, err := deps.Tasks.Enqueue(ctx, model.TaskTypeHistorySync, map[string]any{"channel_id": 5})
	if err != nil {
		t.Fatalf("enqueue bulk succeeded task: %v", err)
	}
	if err := deps.Tasks.Start(ctx, bulkSucceeded.ID); err != nil {
		t.Fatalf("start bulk succeeded task: %v", err)
	}
	if err := deps.Tasks.Succeed(ctx, bulkSucceeded.ID, "done"); err != nil {
		t.Fatalf("succeed bulk task: %v", err)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/tasks/bulk-delete", bytes.NewBufferString(fmt.Sprintf(`{"ids":[%d,%d,%d,9999]}`, bulkFailed.ID, bulkSucceeded.ID, running.ID)))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bulk delete status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var bulkResult struct {
		Deleted     int     `json:"deleted"`
		RejectedIDs []int64 `json:"rejected_ids"`
		MissingIDs  []int64 `json:"missing_ids"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &bulkResult); err != nil {
		t.Fatalf("decode bulk delete: %v", err)
	}
	if bulkResult.Deleted != 2 {
		t.Fatalf("bulk deleted = %d, want 2", bulkResult.Deleted)
	}
	if len(bulkResult.RejectedIDs) != 1 || bulkResult.RejectedIDs[0] != running.ID {
		t.Fatalf("bulk rejected ids = %+v, want running task id", bulkResult.RejectedIDs)
	}
	if len(bulkResult.MissingIDs) != 1 || bulkResult.MissingIDs[0] != 9999 {
		t.Fatalf("bulk missing ids = %+v, want 9999", bulkResult.MissingIDs)
	}
}

func TestEventsAPI(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("events status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}
}

func TestLocalizeEventLocalizesTaskUpdatedPayload(t *testing.T) {
	event := localizeEvent(taskpkg.Event{
		Type:    taskpkg.EventTaskUpdated,
		Payload: model.Task{Status: model.TaskStatusSucceeded, Message: "completed"},
	})
	task, ok := event.Payload.(model.Task)
	if !ok {
		t.Fatalf("payload type = %T, want model.Task", event.Payload)
	}
	if task.Message != "已完成" {
		t.Fatalf("task message = %q, want 已完成", task.Message)
	}
}

func assertTelegramAPISettingsResponse(t *testing.T, data []byte, configured bool, appID int) {
	t.Helper()
	var body model.TelegramAPISettingsResponse
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode telegram api settings: %v", err)
	}
	if body.Configured != configured || body.AppID != appID || body.AppHashSet != configured {
		t.Fatalf("telegram api settings = %+v, want configured=%v app_id=%d", body, configured, appID)
	}
}

func assertTelegramBotSettingsResponse(t *testing.T, data []byte, enabled bool, tokenSet bool, pollInterval string) {
	t.Helper()
	var body model.TelegramBotSettingsResponse
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode telegram bot settings: %v", err)
	}
	if body.Enabled != enabled || body.TokenSet != tokenSet || body.Configured != (enabled && tokenSet) || body.PollInterval != pollInterval {
		t.Fatalf("telegram bot settings = %+v, want enabled=%v token_set=%v poll_interval=%s", body, enabled, tokenSet, pollInterval)
	}
}

func TestGlobalListenRulesAPI(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/listen-rules", bytes.NewBufferString(`{"includes":[" 庆余年 "],"excludes":["预告"],"message_types":["link","text"],"link_types":["cloud_drive","magnet"],"ignored_link_patterns":[" t.me ","*.t.me"]}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var updated model.ListenRules
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update listen rules: %v", err)
	}
	if !sameStringSlices(updated.Includes, []string{"庆余年"}) ||
		!sameStringSlices(updated.Excludes, []string{"预告"}) ||
		!sameStringSlices(updated.MessageTypes, []string{"link", "text"}) ||
		!sameStringSlices(updated.LinkTypes, []string{"cloud_drive", "magnet"}) ||
		!sameStringSlices(updated.IgnoredLinkPatterns, []string{"t.me", "*.t.me"}) {
		t.Fatalf("updated = %+v", updated)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/listen-rules", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var got model.ListenRules
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get listen rules: %v", err)
	}
	if !sameStringSlices(got.Includes, []string{"庆余年"}) ||
		!sameStringSlices(got.MessageTypes, []string{"link", "text"}) ||
		!sameStringSlices(got.IgnoredLinkPatterns, []string{"t.me", "*.t.me"}) {
		t.Fatalf("got = %+v", got)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/listen-rules", bytes.NewBufferString(`{"message_types":[],"link_types":["cloud_drive"]}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid update status = %d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestWatchRuleAPICRUD(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/watch-rules", bytes.NewBufferString(`{"channel_id":`+strconv.FormatInt(channelID, 10)+`,"includes":[" 庆余年 "],"excludes":["预告"],"message_types":["text","file"],"link_types":["cloud_drive","magnet"]}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", w.Code, w.Body.String())
	}
	var created model.WatchRule
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("invalid create JSON: %v", err)
	}
	if created.ID == 0 || created.ChannelID != channelID || !created.Enabled ||
		!sameStringSlices(created.Includes, []string{"庆余年"}) ||
		!sameStringSlices(created.MessageTypes, []string{"text", "file"}) ||
		!sameStringSlices(created.LinkTypes, []string{"cloud_drive", "magnet"}) {
		t.Fatalf("created = %+v", created)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/watch-rules/"+strconv.FormatInt(created.ID, 10), bytes.NewBufferString(`{"channel_id":`+strconv.FormatInt(channelID, 10)+`,"enabled":false,"includes":["三体"],"excludes":["花絮"],"message_types":["text"],"link_types":["http"]}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s, want 200", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/watch-rules/"+strconv.FormatInt(created.ID, 10), nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var got model.WatchRule
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Enabled || !sameStringSlices(got.Includes, []string{"三体"}) ||
		!sameStringSlices(got.Excludes, []string{"花絮"}) ||
		!sameStringSlices(got.MessageTypes, []string{"text"}) ||
		!sameStringSlices(got.LinkTypes, []string{"http"}) {
		t.Fatalf("got = %+v", got)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/watch-rules", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"items"`)) {
		t.Fatalf("list status=%d body=%s, want items", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/watch-rules/"+strconv.FormatInt(created.ID, 10), nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s, want 200", w.Code, w.Body.String())
	}
}

func TestWatchRuleAPIRejectsInvalidRequests(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	router := NewRouter(deps)

	for _, body := range []string{
		`{"channel_id":0}`,
		`{"channel_id":999999}`,
		`{"channel_id":` + strconv.FormatInt(channelID, 10) + `,"includes":["ok",5]}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/watch-rules", bytes.NewBufferString(body))
		withAdminSession(t, deps, req)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d response=%s, want 400", body, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/watch-rules", bytes.NewBufferString(`{"channel_id":`+strconv.FormatInt(channelID, 10)+`}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first create status = %d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/watch-rules", bytes.NewBufferString(`{"channel_id":`+strconv.FormatInt(channelID, 10)+`}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d body=%s, want 409", w.Code, w.Body.String())
	}
}

func TestSearchRequiresQuery(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/search", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Error.Code != "bad_request" || body.Error.Message == "" {
		t.Fatalf("error response = %s, want standard bad_request envelope", w.Body.String())
	}
}

func TestAPIErrorMessagesAreLocalized(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	blockedID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:           accountID,
		TelegramChannelID:   44,
		Title:               "Blocked",
		Type:                model.ChannelTypeChannel,
		RemoteSearchAllowed: false,
	})
	router := NewRouter(deps)

	for _, tc := range []struct {
		name       string
		method     string
		path       string
		body       string
		auth       bool
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "missing admin session",
			method:     http.MethodGet,
			path:       "/api/accounts",
			wantStatus: http.StatusUnauthorized,
			wantMsg:    "请先登录",
		},
		{
			name:       "invalid json",
			method:     http.MethodPost,
			path:       "/api/setup/admin",
			body:       `{"username":`,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "请求体 JSON 格式错误",
		},
		{
			name:       "admin username required",
			method:     http.MethodPost,
			path:       "/api/setup/admin",
			body:       `{"username":" ","password":"secret123"}`,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "请输入用户名",
		},
		{
			name:       "phone required",
			method:     http.MethodPost,
			path:       "/api/telegram/login/send-code",
			body:       `{"phone":""}`,
			auth:       true,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "请输入手机号码",
		},
		{
			name:       "invalid account id",
			method:     http.MethodGet,
			path:       "/api/channels/not-a-number",
			body:       "",
			auth:       true,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "ID 无效",
		},
		{
			name:       "invalid date",
			method:     http.MethodGet,
			path:       "/api/admin/search/messages?q=ubuntu&date_from=bad-date",
			auth:       true,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "date_from 必须是 YYYY-MM-DD 或 RFC3339 格式",
		},
		{
			name:       "remote search forbidden",
			method:     http.MethodPost,
			path:       "/api/admin/search/remote",
			body:       `{"channel_id":` + strconv.FormatInt(blockedID, 10) + `,"query":"ubuntu"}`,
			auth:       true,
			wantStatus: http.StatusConflict,
			wantMsg:    "该频道不允许远程搜索",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.auth {
				withAdminSession(t, deps, req)
			}
			router.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d body=%s, want %d", w.Code, w.Body.String(), tc.wantStatus)
			}
			got := errorMessage(t, w.Body.Bytes())
			if got != tc.wantMsg {
				t.Fatalf("message = %q, want %q; body=%s", got, tc.wantMsg, w.Body.String())
			}
		})
	}
}

func TestSendCodeCreatesLoginRequiredAccount(t *testing.T) {
	deps := testDeps(t)
	fake := &fakeTelegram{}
	deps.Telegram = fake
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/login/send-code", bytes.NewBufferString(`{"phone":"+86 138-0013-8000"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	account, err := deps.Accounts.FindByPhone(context.Background(), "+8613800138000")
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	if account.Status != model.AccountStatusLoginRequired {
		t.Fatalf("status = %q, want LOGIN_REQUIRED", account.Status)
	}
	if !sameStringSlices(fake.sendCodePhones, []string{"+8613800138000"}) {
		t.Fatalf("SendCode phones = %v, want normalized phone", fake.sendCodePhones)
	}
	hash, ok := deps.CodeStore.Take("+8613800138000")
	if !ok || hash != "hash" {
		t.Fatalf("code store hash = %q ok=%v, want normalized key with hash", hash, ok)
	}
}

func TestTelegramLoginRoutes(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	deps.Telegram = &fakeTelegram{}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/login/send-code", bytes.NewBufferString(`{"phone":"+8613800138000"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("send-code status = %d body=%s, want 200", w.Code, w.Body.String())
	}

	deps.CodeStore.Save("+8613800138000", "hash")
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/telegram/login/sign-in", bytes.NewBufferString(`{"phone":"+86 13800138000","code":"12345"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in status = %d body=%s, want 200", w.Code, w.Body.String())
	}

	if _, err := deps.Accounts.Save(ctx, model.Account{Phone: "+8613800138001", Status: model.AccountStatusLoginRequired}); err != nil {
		t.Fatalf("save password account: %v", err)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/telegram/login/password", bytes.NewBufferString(`{"phone":"+8613800138001","password":"secret"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("password status = %d body=%s, want 200", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/login/send-code", bytes.NewBufferString(`{"phone":"+10000000002"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("old send-code status = %d body=%s, want 404", w.Code, w.Body.String())
	}
}

func TestTelegramQRLoginStartAndPendingPoll(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	fake := &fakeTelegram{
		qrTokenURL: "tg://login?token=test-token",
		qrExpires:  time.Now().UTC().Add(time.Minute),
	}
	deps.Telegram = fake
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/login/qr/start", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var started struct {
		LoginID   string    `json:"login_id"`
		QRURL     string    `json:"qr_url"`
		ExpiresAt time.Time `json:"expires_at"`
		Status    string    `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatalf("invalid start JSON: %v body=%s", err, w.Body.String())
	}
	if started.LoginID == "" || started.QRURL != "tg://login?token=test-token" || started.Status != "pending" {
		t.Fatalf("start body = %+v, want login id, token url, pending", started)
	}
	accounts, err := deps.Accounts.FindAll(ctx)
	if err != nil {
		t.Fatalf("find accounts: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("accounts len = %d, want 0 before QR confirmation", len(accounts))
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/telegram/login/qr/"+started.LoginID, nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("poll status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var polled struct {
		LoginID   string    `json:"login_id"`
		QRURL     string    `json:"qr_url"`
		ExpiresAt time.Time `json:"expires_at"`
		Status    string    `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &polled); err != nil {
		t.Fatalf("invalid poll JSON: %v body=%s", err, w.Body.String())
	}
	if polled.LoginID != started.LoginID || polled.Status != "pending" || polled.QRURL != started.QRURL {
		t.Fatalf("poll body = %+v, want same pending QR session", polled)
	}
}

func TestTelegramQRLoginCancelStopsSession(t *testing.T) {
	deps := testDeps(t)
	fake := &fakeTelegram{
		qrTokenURL: "tg://login?token=cancel",
		qrExpires:  time.Now().UTC().Add(time.Minute),
	}
	deps.Telegram = fake
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/login/qr/start", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var started struct {
		LoginID string `json:"login_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatalf("invalid start JSON: %v body=%s", err, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/telegram/login/qr/"+started.LoginID, nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if fake.qrSession == nil || !fake.qrSession.cancelled {
		t.Fatal("QR session was not canceled")
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/telegram/login/qr/"+started.LoginID, nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("poll canceled status = %d body=%s, want 404", w.Code, w.Body.String())
	}
}

func TestTelegramQRLoginPollFinalizesConfirmedAccount(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	fake := &fakeTelegram{
		qrTokenURL: "tg://login?token=ready",
		qrExpires:  time.Now().UTC().Add(time.Minute),
	}
	deps.Telegram = fake
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/login/qr/start", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	var started struct {
		LoginID string `json:"login_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatalf("invalid start JSON: %v", err)
	}
	if err := os.WriteFile(fake.qrSessionPath, []byte(`{"session":"qr"}`), 0o600); err != nil {
		t.Fatalf("write qr session: %v", err)
	}
	fake.qrSession.result = telegram.QRLoginPollResult{
		Status:  telegram.QRLoginStatusOnline,
		Profile: telegram.Profile{TelegramUserID: 99, Phone: "+19990000000", FirstName: "QR", LastName: "User", Username: "qr_user"},
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/telegram/login/qr/"+started.LoginID, nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("poll status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Status  string        `json:"status"`
		Account model.Account `json:"account"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid poll JSON: %v body=%s", err, w.Body.String())
	}
	if body.Status != model.AccountStatusOnline || body.Account.Phone != "+19990000000" || body.Account.TelegramUserID != 99 {
		t.Fatalf("body = %+v, want online QR account", body)
	}
	if _, err := os.Stat(body.Account.SessionPath); err != nil {
		t.Fatalf("final session stat: %v", err)
	}
	if _, ok := deps.QRLogins.Find(started.LoginID); ok {
		t.Fatal("completed QR login session was not removed")
	}
	account, err := deps.Accounts.FindByPhone(ctx, "+19990000000")
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	if account.Status != model.AccountStatusOnline {
		t.Fatalf("stored status = %q, want ONLINE", account.Status)
	}
}

func TestTelegramSignInStartsMetadataSyncOnly(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	fake := &fakeTelegram{
		channels: []telegram.Channel{
			{
				TelegramChannelID: 100,
				AccessHash:        200,
				Title:             "Resource Channel",
				Username:          "resources",
				Type:              model.ChannelTypeChannel,
				MemberCount:       1234,
				Description:       "resource index",
				AvatarState:       "unknown",
			},
			{
				TelegramChannelID: 101,
				AccessHash:        201,
				Title:             "Private Group",
				Type:              model.ChannelTypeSupergroup,
				MemberCount:       50,
				Description:       "invite only",
			},
		},
	}
	deps.Telegram = fake
	deps.ChannelSync = channel.NewService(deps.Channels, fake, deps.Sessions)
	deps.ChannelWebAccess = channel.NewWebAccessService(deps.Channels, &apiWebAccessChecker{results: map[string]bool{"async_resources": true}})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/login/send-code", bytes.NewBufferString(`{"phone":"+8613800138000"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("send-code status = %d body=%s, want 200", w.Code, w.Body.String())
	}

	deps.CodeStore.Save("+8613800138000", "hash")
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/telegram/login/sign-in", bytes.NewBufferString(`{"phone":"+8613800138000","code":"12345"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Status       string        `json:"status"`
		Account      model.Account `json:"account"`
		MetadataSync struct {
			Status       string `json:"status"`
			ChannelCount int    `json:"channel_count"`
		} `json:"metadata_sync"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v body=%s", err, w.Body.String())
	}
	if body.Status != model.AccountStatusOnline || body.Account.Status != model.AccountStatusOnline {
		t.Fatalf("status body = %+v, want ONLINE", body)
	}
	if body.Account.LastOnlineAt == nil || body.Account.SessionPath == "" || body.Account.LastError != "" {
		t.Fatalf("account metadata = %+v, want last_online_at, session_path, empty last_error", body.Account)
	}
	if body.MetadataSync.Status != "succeeded" || body.MetadataSync.ChannelCount != 3 {
		t.Fatalf("metadata_sync = %+v, want succeeded with 3 channels", body.MetadataSync)
	}

	items, err := deps.Channels.FindByAccountID(ctx, body.Account.ID)
	if err != nil {
		t.Fatalf("find channels: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("channels len = %d, want 3", len(items))
	}
	var public model.Channel
	for _, item := range items {
		if item.Username == "resources" {
			public = item
		}
	}
	if public.MemberCount != 1234 || public.Description != "resource index" || public.SyncState != "metadata_only" {
		t.Fatalf("public channel metadata = %+v", public)
	}
	counts, err := deps.Status.Counts(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Messages != 0 {
		t.Fatalf("message count = %d, want 0", counts.Messages)
	}
	if fake.fetchHistoryCalls != 0 {
		t.Fatalf("FetchHistory calls = %d, want 0", fake.fetchHistoryCalls)
	}
}

func TestTelegramSignInQueuesMetadataSyncWhenRetryQueueAvailable(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	deps.SyncQueue = scheduler.NewRetryQueue(scheduler.RetryQueueOptions{
		Policy: retry.Policy{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxTries: 1, Sleep: func(context.Context, time.Duration) error { return nil }},
	})
	fake := &fakeTelegram{
		channels: []telegram.Channel{
			{
				TelegramChannelID: 200,
				AccessHash:        300,
				Title:             "Async Resources",
				Username:          "async_resources",
				Type:              model.ChannelTypeChannel,
				MemberCount:       42,
				Description:       "loaded in the background",
			},
		},
	}
	deps.Telegram = fake
	deps.ChannelSync = channel.NewService(deps.Channels, fake, deps.Sessions)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/login/send-code", bytes.NewBufferString(`{"phone":"+8613800138000"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("send-code status = %d body=%s, want 200", w.Code, w.Body.String())
	}

	deps.CodeStore.Save("+8613800138000", "hash")
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/telegram/login/sign-in", bytes.NewBufferString(`{"phone":"+8613800138000","code":"12345"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Account      model.Account `json:"account"`
		MetadataSync struct {
			Status string `json:"status"`
			JobID  string `json:"job_id"`
		} `json:"metadata_sync"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v body=%s", err, w.Body.String())
	}
	if body.MetadataSync.Status != "queued" || body.MetadataSync.JobID == "" {
		t.Fatalf("metadata_sync = %+v, want queued job id", body.MetadataSync)
	}
	done, err := deps.SyncQueue.Wait(ctx, body.MetadataSync.JobID)
	if err != nil {
		t.Fatalf("wait metadata sync job: %v", err)
	}
	if done.Status != scheduler.RetryJobSucceeded {
		t.Fatalf("job status = %q error=%s, want succeeded", done.Status, done.Error)
	}
	items, err := deps.Channels.FindByAccountID(ctx, body.Account.ID)
	if err != nil {
		t.Fatalf("find channels: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("channels len = %d, want saved messages plus async channel", len(items))
	}
}

func TestTelegramSignInQueuedMetadataSyncChecksWebAccess(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	deps.SyncQueue = scheduler.NewRetryQueue(scheduler.RetryQueueOptions{
		Policy: retry.Policy{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxTries: 1, Sleep: func(context.Context, time.Duration) error { return nil }},
	})
	fake := &fakeTelegram{
		channels: []telegram.Channel{
			{
				TelegramChannelID: 201,
				AccessHash:        301,
				Title:             "Public After Login",
				Username:          "public_after_login",
				Type:              model.ChannelTypeChannel,
			},
		},
	}
	checker := &apiWebAccessChecker{results: map[string]bool{"public_after_login": true}}
	deps.Telegram = fake
	deps.ChannelSync = channel.NewService(deps.Channels, fake, deps.Sessions)
	deps.ChannelWebAccess = channel.NewWebAccessService(deps.Channels, checker)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/login/send-code", bytes.NewBufferString(`{"phone":"+8613800138000"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("send-code status = %d body=%s, want 200", w.Code, w.Body.String())
	}

	deps.CodeStore.Save("+8613800138000", "hash")
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/telegram/login/sign-in", bytes.NewBufferString(`{"phone":"+8613800138000","code":"12345"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Account      model.Account `json:"account"`
		MetadataSync struct {
			JobID string `json:"job_id"`
		} `json:"metadata_sync"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v body=%s", err, w.Body.String())
	}
	done, err := deps.SyncQueue.Wait(ctx, body.MetadataSync.JobID)
	if err != nil {
		t.Fatalf("wait metadata sync job: %v", err)
	}
	if done.Status != scheduler.RetryJobSucceeded {
		t.Fatalf("job status = %q error=%s, want succeeded", done.Status, done.Error)
	}
	if !sameStringSlices(checker.calls, []string{"public_after_login"}) {
		t.Fatalf("checker calls = %v, want public_after_login", checker.calls)
	}
	items, err := deps.Channels.FindByAccountID(ctx, body.Account.ID)
	if err != nil {
		t.Fatalf("find channels: %v", err)
	}
	var public model.Channel
	for _, item := range items {
		if item.Username == "public_after_login" {
			public = item
		}
	}
	if public.ID == 0 || public.WebAccess == nil || !*public.WebAccess || public.WebAccessCheckedAt == nil {
		t.Fatalf("public channel = %+v, want web access checked true", public)
	}
}

func TestTelegramSignInKeepsAccountOnlineWhenMetadataSyncFails(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	fake := &fakeTelegram{listErr: errors.New("flood wait")}
	deps.Telegram = fake
	deps.ChannelSync = channel.NewService(deps.Channels, fake, deps.Sessions)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/login/send-code", bytes.NewBufferString(`{"phone":"+8613800138000"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("send-code status = %d body=%s, want 200", w.Code, w.Body.String())
	}

	deps.CodeStore.Save("+8613800138000", "hash")
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/telegram/login/sign-in", bytes.NewBufferString(`{"phone":"+8613800138000","code":"12345"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Status       string        `json:"status"`
		Account      model.Account `json:"account"`
		MetadataSync struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"metadata_sync"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v body=%s", err, w.Body.String())
	}
	if body.Status != model.AccountStatusOnline || body.Account.Status != model.AccountStatusOnline {
		t.Fatalf("status body = %+v, want ONLINE", body)
	}
	if body.MetadataSync.Status != "failed" || body.MetadataSync.Error != "Telegram 请求触发限流，请稍后重试" {
		t.Fatalf("metadata_sync = %+v, want localized flood wait", body.MetadataSync)
	}
	account, err := deps.Accounts.FindByID(ctx, body.Account.ID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	if account.Status != model.AccountStatusOnline || account.LastError != "Telegram 请求触发限流，请稍后重试" {
		t.Fatalf("stored account = %+v, want ONLINE with localized last_error", account)
	}
}

func TestDeleteAccountStopsRuntimeAndRemovesSession(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, err := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	sessionPath := deps.Sessions.PathForAccount(accountID)
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("session"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	runtime := &recordingAccountRuntime{}
	deps.AccountRuntime = runtime
	fake := &fakeTelegram{}
	deps.Telegram = fake
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/accounts/"+strconv.FormatInt(accountID, 10), nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if got := runtime.stoppedIDs(); !sameInt64s(got, []int64{accountID}) {
		t.Fatalf("stopped ids = %v, want [%d]", got, accountID)
	}
	if got := fake.logoutSessionPaths(); !sameStrings(got, []string{sessionPath}) {
		t.Fatalf("logout session paths = %v, want [%q]", got, sessionPath)
	}
	if !fake.logoutSessionExisted {
		t.Fatal("telegram logout ran after session removal, want logout before local session cleanup")
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("session stat error = %v, want not exist", err)
	}
	if _, err := deps.Accounts.FindByID(ctx, accountID); err == nil {
		t.Fatal("FindByID succeeded after delete, want missing account")
	}
}

func TestDeleteAccountKeepsLocalStateWhenTelegramLogoutFails(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, err := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	sessionPath := deps.Sessions.PathForAccount(accountID)
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("session"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	deps.Telegram = &fakeTelegram{logoutErr: errors.New("remote logout failed")}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/accounts/"+strconv.FormatInt(accountID, 10), nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s, want 500", w.Code, w.Body.String())
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session stat error = %v, want session preserved", err)
	}
	if _, err := deps.Accounts.FindByID(ctx, accountID); err != nil {
		t.Fatalf("find account after failed delete: %v", err)
	}
}

func TestLogoutAccountStopsRuntimeRemovesSessionAndKeepsAccount(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, err := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	sessionPath := deps.Sessions.PathForAccount(accountID)
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("session"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	runtime := &recordingAccountRuntime{}
	deps.AccountRuntime = runtime
	fake := &fakeTelegram{}
	deps.Telegram = fake
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+strconv.FormatInt(accountID, 10)+"/logout", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if got := runtime.stoppedIDs(); !sameInt64s(got, []int64{accountID}) {
		t.Fatalf("stopped ids = %v, want [%d]", got, accountID)
	}
	if got := fake.logoutSessionPaths(); !sameStrings(got, []string{sessionPath}) {
		t.Fatalf("logout session paths = %v, want [%q]", got, sessionPath)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("session stat error = %v, want not exist", err)
	}
	var body model.Account
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.ID != accountID || body.Status != model.AccountStatusLoginRequired {
		t.Fatalf("response account = %+v, want id %d LOGIN_REQUIRED", body, accountID)
	}
	account, err := deps.Accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account after logout: %v", err)
	}
	if account.Status != model.AccountStatusLoginRequired {
		t.Fatalf("stored status = %q, want LOGIN_REQUIRED", account.Status)
	}
}

func TestStatusIncludesAccountStateSummary(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	_, _ = deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	_, _ = deps.Accounts.Save(ctx, model.Account{Phone: "+10000000001", Status: model.AccountStatusReconnecting})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		AccountStates map[string]int64 `json:"account_states"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.AccountStates[model.AccountStatusOnline] != 1 || body.AccountStates[model.AccountStatusReconnecting] != 1 {
		t.Fatalf("account_states = %+v, want ONLINE=1 RECONNECTING=1", body.AccountStates)
	}
}

func TestReadAPIsFilterByAccount(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	account1, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "one", Status: model.AccountStatusOnline})
	account2, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000001", Username: "two", Status: model.AccountStatusOnline})
	channel1, _ := deps.Channels.Save(ctx, model.Channel{AccountID: account1, TelegramChannelID: 1, Title: "one-channel", Type: model.ChannelTypeChannel, MemberCount: 10})
	channel2, _ := deps.Channels.Save(ctx, model.Channel{AccountID: account2, TelegramChannelID: 2, Title: "two-channel", Type: model.ChannelTypeChannel, MemberCount: 10})
	now := time.Now().UTC()
	stored1, _ := deps.Messages.SaveBatch(ctx, []model.Message{{AccountID: account1, ChannelID: channel1, TelegramMessageID: 1, Text: "shared title one", RawJSON: "{}", Date: now}})
	stored2, _ := deps.Messages.SaveBatch(ctx, []model.Message{{AccountID: account2, ChannelID: channel2, TelegramMessageID: 2, Text: "shared title two", RawJSON: "{}", Date: now}})
	_, _ = deps.Links.SaveBatch(ctx, stored1[0].ID, []model.Link{{Type: "url", URL: "https://example.com/one"}})
	_, _ = deps.Links.SaveBatch(ctx, stored2[0].ID, []model.Link{{Type: "url", URL: "https://example.com/two"}})
	router := NewRouter(deps)

	for _, path := range []string{
		"/api/admin/search?q=shared&account_id=" + strconv.FormatInt(account1, 10),
		"/api/messages/latest?account_id=" + strconv.FormatInt(account1, 10),
		"/api/links?account_id=" + strconv.FormatInt(account1, 10),
		"/api/channels?account_id=" + strconv.FormatInt(account1, 10),
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		withAdminSession(t, deps, req)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, w.Code, w.Body.String())
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("one")) {
			t.Fatalf("%s response missing account one data: %s", path, w.Body.String())
		}
		if bytes.Contains(w.Body.Bytes(), []byte("two")) || bytes.Contains(w.Body.Bytes(), []byte("https://example.com/two")) {
			t.Fatalf("%s response leaked account two data: %s", path, w.Body.String())
		}
	}
}

func TestLatestAPIOmitsAccountFields(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{
		Phone:     "+19999999999",
		FirstName: "PrivateFirst",
		Username:  "privateuser",
		Status:    model.AccountStatusOnline,
	})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 1,
		Title:             "Public Channel",
		Username:          "publicchannel",
		Type:              model.ChannelTypeChannel,
	})
	_, _ = deps.Messages.SaveBatch(ctx, []model.Message{{
		AccountID:         accountID,
		ChannelID:         channelID,
		TelegramMessageID: 1,
		Text:              "latest public message",
		RawJSON:           "{}",
		Date:              time.Now().UTC(),
	}})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/messages/latest?limit=10", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items len = %d, want 1: %s", len(body.Items), w.Body.String())
	}
	item := body.Items[0]
	for _, field := range []string{"account_id", "account_phone", "account_username", "account_first_name"} {
		if _, ok := item[field]; ok {
			t.Fatalf("latest item includes %q: %s", field, w.Body.String())
		}
	}
	if item["channel_title"] != "Public Channel" || item["channel_username"] != "publicchannel" {
		t.Fatalf("latest item missing channel context: %+v", item)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("+19999999999")) ||
		bytes.Contains(w.Body.Bytes(), []byte("PrivateFirst")) ||
		bytes.Contains(w.Body.Bytes(), []byte("privateuser")) {
		t.Fatalf("latest response leaked account data: %s", w.Body.String())
	}
}

func TestSearchAPIFiltersByLinkType(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	now := time.Now().UTC()
	stored, _ := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "shared aliyun", RawJSON: "{}", Date: now},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "shared quark", RawJSON: "{}", Date: now.Add(-time.Minute)},
	})
	_, _ = deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "aliyun", URL: "https://www.alipan.com/s/abc123"}})
	_, _ = deps.Links.SaveBatch(ctx, stored[1].ID, []model.Link{{Type: "quark", URL: "https://pan.quark.cn/s/quark123"}})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/search?q=shared&link_type=aliyun", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("https://www.alipan.com/s/abc123")) {
		t.Fatalf("response missing aliyun link: %s", w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("https://pan.quark.cn/s/quark123")) {
		t.Fatalf("response included quark link: %s", w.Body.String())
	}
}

func TestLinksAPIFiltersByDateRangeAndRejectsInvalidDate(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	january := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	february := time.Date(2026, 2, 5, 12, 0, 0, 0, time.UTC)
	stored, _ := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "jan aliyun", RawJSON: "{}", Date: january},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "feb aliyun", RawJSON: "{}", Date: february},
	})
	_, _ = deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "aliyun", URL: "https://www.alipan.com/s/jan"}})
	_, _ = deps.Links.SaveBatch(ctx, stored[1].ID, []model.Link{{Type: "aliyun", URL: "https://www.alipan.com/s/feb"}})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/links?type=aliyun&date_from=2026-01-01&date_to=2026-01-31", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("jan")) || bytes.Contains(w.Body.Bytes(), []byte("feb")) {
		t.Fatalf("date range response = %s, want jan only", w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/links?date_from=not-a-date", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid date status = %d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestMergedLinksAPI(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	oldDate := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	newDate := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	stored, _ := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "庆余年 old", RawJSON: "{}", Date: oldDate},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "庆余年 new", RawJSON: "{}", Date: newDate},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 3, Text: "庆余年 aliyun", RawJSON: "{}", Date: newDate.Add(-time.Minute)},
	})
	_, _ = deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "quark", URL: "https://pan.quark.cn/s/same", Note: "庆余年 旧"}})
	_, _ = deps.Links.SaveBatch(ctx, stored[1].ID, []model.Link{{Type: "quark", URL: "https://pan.quark.cn/s/same", Note: "庆余年 最新合集"}})
	_, _ = deps.Links.SaveBatch(ctx, stored[2].ID, []model.Link{{Type: "aliyun", URL: "https://www.alipan.com/s/abc123", Note: "庆余年 S02"}})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/links/merged?q=庆余年&limit=10", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body model.MergedLinksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Total != 2 {
		t.Fatalf("total = %d, want 2: %s", body.Total, w.Body.String())
	}
	if len(body.MergedByType["quark"]) != 1 || body.MergedByType["quark"][0].Note != "庆余年 最新合集" {
		t.Fatalf("quark merged links = %+v, want newest deduped note", body.MergedByType["quark"])
	}
	if len(body.MergedByType["aliyun"]) != 1 || body.MergedByType["aliyun"][0].Note != "庆余年 S02" {
		t.Fatalf("aliyun merged links = %+v, want aliyun note", body.MergedByType["aliyun"])
	}
}

func TestSearchAPIFiltersByDateRange(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	january := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	february := time.Date(2026, 2, 5, 12, 0, 0, 0, time.UTC)
	_, _ = deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "shared january", RawJSON: "{}", Date: january},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "shared february", RawJSON: "{}", Date: february},
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/search?q=shared&date_from=2026-01-01&date_to=2026-01-31", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("january")) || bytes.Contains(w.Body.Bytes(), []byte("february")) {
		t.Fatalf("date range response = %s, want january only", w.Body.String())
	}
}

func TestReadAPIsRejectInvalidQueryParameters(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	for _, path := range []string{
		"/api/admin/search?q=x&limit=abc",
		"/api/admin/search?q=x&limit=-1",
		"/api/admin/search?q=x&offset=-1",
		"/api/admin/search?q=x&account_id=abc",
		"/api/admin/search?q=x&account_id=0",
		"/api/admin/search?q=x&channel_id=abc",
		"/api/messages/latest?limit=-1",
		"/api/messages/latest?account_id=abc",
		"/api/links?offset=-1",
		"/api/links?channel_id=abc",
		"/api/channels?account_id=abc",
		"/api/admin/search?q=x&before_date=2026-02-05T12:00:00Z",
		"/api/admin/search?q=x&before_id=10",
		"/api/messages/latest?before_date=2026-02-05T12:00:00Z",
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		withAdminSession(t, deps, req)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%s, want 400", path, w.Code, w.Body.String())
		}
	}
}

func TestSearchAPICursorReturnsOlderRows(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "VIP", Type: model.ChannelTypeChannel})
	newer := time.Date(2026, 2, 5, 12, 0, 0, 0, time.UTC)
	older := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	stored, _ := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "shared newer", RawJSON: "{}", Date: newer},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "shared older", RawJSON: "{}", Date: older},
	})
	router := NewRouter(deps)

	path := "/api/admin/search?q=shared&before_date=" + url.QueryEscape(newer.Format(time.RFC3339)) + "&before_id=" + strconv.FormatInt(stored[0].ID, 10)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("shared older")) || bytes.Contains(w.Body.Bytes(), []byte("shared newer")) {
		t.Fatalf("cursor response = %s, want older only", w.Body.String())
	}
}

func TestMaintenanceSQLiteAPI(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/maintenance/sqlite", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("ANALYZE")) || !bytes.Contains(w.Body.Bytes(), []byte("telegram_messages_fts optimize")) {
		t.Fatalf("maintenance response = %s", w.Body.String())
	}
}

func TestMaintenanceBackupAPI(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/maintenance/backup", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Path == "" {
		t.Fatalf("path is empty in response %s", w.Body.String())
	}
	if _, err := os.Stat(body.Path); err != nil {
		t.Fatalf("backup path stat: %v", err)
	}
}

func TestResourceIndexMaintenanceRebuild(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	index := repository.NewResourceIndexRepository(deps.BackupDB)
	deps.Resources = resource.NewService(deps.Links, deps.Files, repository.NewResourceStatsRepository(deps.BackupDB), index)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Username: "main", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 1, Title: "Public", Type: model.ChannelTypeChannel})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{{
		AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1,
		Text: "ubuntu", RawJSON: "{}", Date: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("save message: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "quark", Category: "cloud_drive", URL: "https://pan.quark.cn/s/ubuntu", Note: "Ubuntu"}}); err != nil {
		t.Fatalf("save link: %v", err)
	}

	router := NewRouter(deps)
	req := httptest.NewRequest(http.MethodPost, "/api/maintenance/resource-index/rebuild", nil)
	withAdminSession(t, deps, req)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	result, err := deps.Resources.List(ctx, resource.Query{Keyword: "ubuntu", Limit: 10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("total = %d, want rebuilt resource", result.Total)
	}
}

func TestBatchSyncAPIValidatesChannelIDs(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	for _, body := range []string{
		`{}`,
		`{"channel_ids":[]}`,
		`{"channel_ids":[0]}`,
		`{"channel_ids":[-1]}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/channels/sync", bytes.NewBufferString(body))
		withAdminSession(t, deps, req)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d body=%s, want 400", body, w.Code, w.Body.String())
		}
	}
}

func TestBatchSyncAPIValidatesMaxMessages(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	for _, body := range []string{
		`{"channel_ids":[1],"max_messages":0}`,
		`{"channel_ids":[1],"max_messages":-1}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/channels/sync", bytes.NewBufferString(body))
		withAdminSession(t, deps, req)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d body=%s, want 400", body, w.Code, w.Body.String())
		}
	}
}

func TestChannelsAPIIncludesIndexedMessageCount(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 30,
		Title:             "Public Channel",
		Username:          "public_channel",
		Type:              model.ChannelTypeChannel,
		MemberCount:       10,
	})
	now := time.Now().UTC()
	_, _ = deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 1, Text: "indexed 1", RawJSON: "{}", Date: now},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 2, Text: "indexed 2", RawJSON: "{}", Date: now},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 3, Text: "deleted", RawJSON: "{}", Date: now, Deleted: true},
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct {
			ID                  int64 `json:"id"`
			IndexedMessageCount int64 `json:"indexed_message_count"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(body.Items))
	}
	if body.Items[0].IndexedMessageCount != 2 {
		t.Fatalf("indexed_message_count = %d, want 2", body.Items[0].IndexedMessageCount)
	}
}

func TestChannelsAPIDeduplicatesGlobalDuplicateOwners(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	account1, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	account2, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000001", Status: model.AccountStatusOnline})
	firstID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         account1,
		TelegramChannelID: 30,
		Title:             "Shared Channel",
		Username:          "shared_channel",
		Type:              model.ChannelTypeChannel,
		MemberCount:       10,
	})
	secondID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         account2,
		TelegramChannelID: 30,
		Title:             "Shared Channel",
		Username:          "shared_channel",
		Type:              model.ChannelTypeChannel,
		MemberCount:       10,
	})
	if err := deps.Channels.UpdateControl(ctx, secondID, model.ChannelControl{
		HistorySyncEnabled:  true,
		SyncProfile:         "Normal",
		ListenEnabled:       true,
		RemoteSearchAllowed: true,
	}); err != nil {
		t.Fatalf("enable second channel: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct {
			ID        int64 `json:"id"`
			AccountID int64 `json:"account_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("global items len = %d, want one representative channel: %s", len(body.Items), w.Body.String())
	}
	if body.Items[0].ID != secondID || body.Items[0].AccountID != account2 {
		t.Fatalf("global item = %+v, want enabled duplicate owner id %d account %d", body.Items[0], secondID, account2)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/channels?account_id="+strconv.FormatInt(account1, 10), nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("account status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	body.Items = nil
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid account JSON: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ID != firstID {
		t.Fatalf("account items = %+v, want first account channel id %d", body.Items, firstID)
	}
}

func TestChannelsAPIKeepsSavedMessagesPerAccount(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	account1, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	account2, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000001", Status: model.AccountStatusOnline})
	firstID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         account1,
		TelegramChannelID: 0,
		Title:             "Saved Messages",
		Type:              model.ChannelTypeSavedMessages,
	})
	secondID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         account2,
		TelegramChannelID: 0,
		Title:             "Saved Messages",
		Type:              model.ChannelTypeSavedMessages,
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items len = %d, want saved messages for both accounts: %s", len(body.Items), w.Body.String())
	}
	ids := map[int64]bool{}
	for _, item := range body.Items {
		ids[item.ID] = true
		if item.Type != model.ChannelTypeSavedMessages {
			t.Fatalf("item = %+v, want saved_messages", item)
		}
	}
	if !ids[firstID] || !ids[secondID] {
		t.Fatalf("ids = %+v, want %d and %d", ids, firstID, secondID)
	}
}

func TestChannelsAPIHidesZeroMemberChannelsAndGroups(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	visibleID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 30,
		Title:             "Visible Channel",
		Type:              model.ChannelTypeChannel,
		MemberCount:       10,
	})
	hiddenChannelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 31,
		Title:             "Banned Channel",
		Type:              model.ChannelTypeChannel,
		MemberCount:       0,
	})
	hiddenGroupID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 32,
		Title:             "Banned Group",
		Type:              model.ChannelTypeSupergroup,
		MemberCount:       0,
	})
	savedID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 0,
		Title:             "Saved Messages",
		Type:              model.ChannelTypeSavedMessages,
		MemberCount:       0,
	})
	router := NewRouter(deps)

	for _, path := range []string{"/api/channels", "/api/channels?account_id=" + strconv.FormatInt(accountID, 10)} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		withAdminSession(t, deps, req)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s, want 200", path, w.Code, w.Body.String())
		}
		var body struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s invalid JSON: %v", path, err)
		}
		ids := map[int64]bool{}
		for _, item := range body.Items {
			ids[item.ID] = true
		}
		if !ids[visibleID] || !ids[savedID] {
			t.Fatalf("%s ids = %+v, want visible channel %d and saved messages %d", path, ids, visibleID, savedID)
		}
		if ids[hiddenChannelID] || ids[hiddenGroupID] {
			t.Fatalf("%s ids = %+v, want hidden zero-member channel %d and group %d omitted", path, ids, hiddenChannelID, hiddenGroupID)
		}
	}
}

func TestChannelWebAccessCheckAPIUpdatesChannelList(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 30,
		Title:             "Public Channel",
		Username:          "public_channel",
		Type:              model.ChannelTypeChannel,
		MemberCount:       10,
	})
	checker := &apiWebAccessChecker{results: map[string]bool{"public_channel": true}}
	deps.ChannelWebAccess = channel.NewWebAccessService(deps.Channels, checker)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/channels/web-access/check", bytes.NewBufferString(`{"channel_ids":[`+strconv.FormatInt(channelID, 10)+`]}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items []channel.WebAccessResult `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ChannelID != channelID || !body.Items[0].WebAccess || body.Items[0].CheckedAt.IsZero() {
		t.Fatalf("response = %+v, want checked channel", body)
	}
	if !sameStringSlices(checker.calls, []string{"public_channel"}) {
		t.Fatalf("checker calls = %v, want public_channel", checker.calls)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var list struct {
		Items []model.Channel `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("invalid channel list JSON: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].WebAccess == nil || *list.Items[0].WebAccess != true || list.Items[0].WebAccessCheckedAt == nil {
		t.Fatalf("channel list = %+v, want web_access true", list)
	}
}

func TestChannelWebAccessCheckAPIStoresErrors(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 32,
		Title:             "Public",
		Username:          "public_channel",
		Type:              model.ChannelTypeChannel,
	})
	checker := &apiWebAccessChecker{errors: map[string]error{"public_channel": errors.New("telegram web 500")}}
	deps.ChannelWebAccess = channel.NewWebAccessService(deps.Channels, checker)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/channels/web-access/check", bytes.NewBufferString(`{"channel_ids":[`+strconv.FormatInt(channelID, 10)+`]}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items []channel.WebAccessResult `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].WebAccessError != "Telegram 网页访问检测失败" {
		t.Fatalf("response = %+v, want localized web access error", body)
	}

	stored, err := deps.Channels.FindByID(ctx, channelID)
	if err != nil {
		t.Fatalf("find channel: %v", err)
	}
	if stored.WebAccess == nil || *stored.WebAccess != false || !strings.Contains(stored.WebAccessError, "telegram web 500") {
		t.Fatalf("stored web access = %+v, want false with error", stored)
	}
}

func TestChannelWebAccessCheckAPIValidatesChannelIDs(t *testing.T) {
	deps := testDeps(t)
	deps.ChannelWebAccess = channel.NewWebAccessService(deps.Channels, &apiWebAccessChecker{})
	router := NewRouter(deps)
	for _, body := range []string{
		`{}`,
		`{"channel_ids":[]}`,
		`{"channel_ids":[0]}`,
		`{"channel_ids":[-1]}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/channels/web-access/check", bytes.NewBufferString(body))
		withAdminSession(t, deps, req)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d body=%s, want 400", body, w.Code, w.Body.String())
		}
	}
}

func TestChannelWebAccessCheckAPIRejectsMissingWithoutPartialUpdates(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 31,
		Title:             "Existing",
		Username:          "existing_channel",
		Type:              model.ChannelTypeChannel,
	})
	checker := &apiWebAccessChecker{results: map[string]bool{"existing_channel": true}}
	deps.ChannelWebAccess = channel.NewWebAccessService(deps.Channels, checker)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/channels/web-access/check", bytes.NewBufferString(`{"channel_ids":[`+strconv.FormatInt(channelID, 10)+`,999999]}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s, want 404", w.Code, w.Body.String())
	}
	if len(checker.calls) != 0 {
		t.Fatalf("checker calls = %v, want none", checker.calls)
	}
	stored, err := deps.Channels.FindByID(ctx, channelID)
	if err != nil {
		t.Fatalf("find channel: %v", err)
	}
	if stored.WebAccess != nil || stored.WebAccessCheckedAt != nil {
		t.Fatalf("stored web access = %v checked_at=%v, want unchanged nil values", stored.WebAccess, stored.WebAccessCheckedAt)
	}
}

func TestChannelControlAPIUpdatesProfileAndToggles(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 51,
		Title:             "Control",
		Type:              model.ChannelTypeChannel,
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/control", bytes.NewBufferString(`{
		"history_sync_enabled": true,
		"sync_profile": "Quick",
		"listen_enabled": true,
		"remote_search_allowed": false
	}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body model.Channel
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !body.HistorySyncEnabled || body.SyncProfile != "Quick" || !body.ListenEnabled || body.RemoteSearchAllowed {
		t.Fatalf("response control = %+v", body)
	}
	if body.SyncState != "pending" || body.ListenState != "enabled" {
		t.Fatalf("response states = sync:%q listen:%q, want pending/enabled", body.SyncState, body.ListenState)
	}

	stored, err := deps.Channels.FindByID(ctx, channelID)
	if err != nil {
		t.Fatalf("find channel: %v", err)
	}
	if !stored.HistorySyncEnabled || stored.SyncProfile != "Quick" || !stored.ListenEnabled || stored.RemoteSearchAllowed {
		t.Fatalf("stored control = %+v", stored)
	}
	if stored.SyncState != "pending" || stored.ListenState != "enabled" {
		t.Fatalf("stored states = sync:%q listen:%q, want pending/enabled", stored.SyncState, stored.ListenState)
	}
	tasks, err := deps.TaskRepository.List(ctx, taskpkg.ListFilter{Type: model.TaskTypeHistorySync})
	if err != nil {
		t.Fatalf("list history sync tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("history sync tasks len = %d, want 1", len(tasks))
	}
	var payload taskpkg.HistorySyncPayload
	if err := json.Unmarshal([]byte(tasks[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode history sync payload: %v", err)
	}
	if payload.ChannelID != channelID || payload.MaxMessages != 100 || len(payload.ChannelIDs) != 0 {
		t.Fatalf("history sync payload = %+v, want channel_id %d max_messages 100", payload, channelID)
	}
}

func TestBatchChannelControlAPIUpdatesSelectedChannels(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channel1, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 61,
		Title:             "Control 1",
		Type:              model.ChannelTypeChannel,
	})
	channel2, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 62,
		Title:             "Control 2",
		Type:              model.ChannelTypeChannel,
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/channels/control", bytes.NewBufferString(`{
		"channel_ids": [`+strconv.FormatInt(channel1, 10)+`,`+strconv.FormatInt(channel2, 10)+`],
		"control": {
			"history_sync_enabled": true,
			"sync_profile": "Normal",
			"listen_enabled": true,
			"remote_search_allowed": true
		}
	}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items []model.Channel `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(body.Items))
	}
	for _, id := range []int64{channel1, channel2} {
		stored, err := deps.Channels.FindByID(ctx, id)
		if err != nil {
			t.Fatalf("find channel %d: %v", id, err)
		}
		if !stored.HistorySyncEnabled || stored.SyncProfile != "Normal" || !stored.ListenEnabled || !stored.RemoteSearchAllowed {
			t.Fatalf("stored channel %d control = %+v", id, stored)
		}
		if stored.SyncState != "pending" || stored.ListenState != "enabled" {
			t.Fatalf("stored channel %d states = sync:%q listen:%q, want pending/enabled", id, stored.SyncState, stored.ListenState)
		}
	}
	tasks, err := deps.TaskRepository.List(ctx, taskpkg.ListFilter{Type: model.TaskTypeHistorySync})
	if err != nil {
		t.Fatalf("list history sync tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("history sync tasks len = %d, want 1", len(tasks))
	}
	var payload taskpkg.HistorySyncPayload
	if err := json.Unmarshal([]byte(tasks[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode history sync payload: %v", err)
	}
	if !reflect.DeepEqual(payload.ChannelIDs, []int64{channel1, channel2}) || payload.MaxMessages != 100 || payload.ChannelID != 0 {
		t.Fatalf("history sync payload = %+v, want channel_ids [%d %d] max_messages 100", payload, channel1, channel2)
	}
}

func TestChannelControlAPIDoesNotQueueHistorySyncWhenListenAlreadyEnabled(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 63,
		Title:             "Already Listening",
		Type:              model.ChannelTypeChannel,
		ListenEnabled:     true,
		ListenState:       "enabled",
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/control", bytes.NewBufferString(`{
		"history_sync_enabled": false,
		"sync_profile": "Normal",
		"listen_enabled": true,
		"remote_search_allowed": false
	}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	tasks, err := deps.TaskRepository.List(ctx, taskpkg.ListFilter{Type: model.TaskTypeHistorySync})
	if err != nil {
		t.Fatalf("list history sync tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("history sync tasks len = %d, want 0", len(tasks))
	}
}

func TestChannelControlAPIRejectsInvalidProfile(t *testing.T) {
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(context.Background(), model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(context.Background(), model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 52,
		Title:             "Control",
		Type:              model.ChannelTypeChannel,
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/control", bytes.NewBufferString(`{
		"history_sync_enabled": true,
		"sync_profile": "raw-1000",
		"listen_enabled": false,
		"remote_search_allowed": true
	}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestChannelControlAPIDeepProfileChecksStorageQuota(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	root := t.TempDir()
	writeSizedFile(t, filepath.Join(root, "tg-search.db"), 10)
	deps.StorageUsage = storage.NewUsageService(config.Config{
		Storage: config.StorageConfig{
			Path:      root,
			MaxDBSize: config.Size(10),
		},
	})
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 53,
		Title:             "Control",
		Type:              model.ChannelTypeChannel,
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/control", bytes.NewBufferString(`{
		"history_sync_enabled": true,
		"sync_profile": "Deep",
		"listen_enabled": false,
		"remote_search_allowed": true
	}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Error.Code != "storage_quota_exceeded" {
		t.Fatalf("error code = %q, want storage_quota_exceeded body=%s", body.Error.Code, w.Body.String())
	}
}

func TestClearChannelAPIStopsListeningAndDeletesIndexedData(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	lastSync := time.Now().UTC()
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:           accountID,
		TelegramChannelID:   64,
		Title:               "Clear Me",
		Type:                model.ChannelTypeChannel,
		HistorySyncEnabled:  true,
		SyncProfile:         "Normal",
		SyncState:           "synced",
		ListenEnabled:       true,
		ListenState:         "enabled",
		RemoteSearchAllowed: true,
		LastMessageID:       10,
		LastSyncTime:        &lastSync,
	})
	stored, err := deps.Messages.SaveBatch(ctx, []model.Message{
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 10, Text: "movie https://example.com/a", RawJSON: "{}", Date: time.Now().UTC()},
		{AccountID: accountID, ChannelID: channelID, TelegramMessageID: 11, Text: "clip", RawJSON: "{}", Date: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("save messages: %v", err)
	}
	if _, err := deps.Links.SaveBatch(ctx, stored[0].ID, []model.Link{{Type: "url", URL: "https://example.com/a"}}); err != nil {
		t.Fatalf("save links: %v", err)
	}
	if _, err := deps.Files.SaveBatch(ctx, stored[1].ID, []model.File{{FileName: "clip.mp4", SizeBytes: 1024, Category: "video"}}); err != nil {
		t.Fatalf("save files: %v", err)
	}
	if err := repository.NewSyncCursorRepository(deps.BackupDB).Save(ctx, model.SyncCursor{
		AccountID: accountID, ChannelID: channelID, CursorType: "history", LastMessageID: 11, Date: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save cursor: %v", err)
	}
	if err := repository.NewResourceStatsRepository(deps.BackupDB).SaveGrouped(ctx, map[string]int{"http": 1, "files": 1}); err != nil {
		t.Fatalf("save resource stats: %v", err)
	}
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/clear", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Channel model.Channel                 `json:"channel"`
		Deleted repository.ChannelClearResult `json:"deleted"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Deleted.Messages != 2 || body.Deleted.Links != 1 || body.Deleted.Files != 1 {
		t.Fatalf("deleted = %+v, want 2 messages, 1 link, 1 file", body.Deleted)
	}
	if body.Channel.ID != channelID || body.Channel.ListenEnabled || body.Channel.ListenState != "disabled" {
		t.Fatalf("response channel listen state = %+v", body.Channel)
	}
	if body.Channel.IndexedMessageCount != 0 || body.Channel.LastMessageID != 0 || body.Channel.LastSyncTime != nil {
		t.Fatalf("response channel indexed/cursor fields = %+v", body.Channel)
	}
	if body.Channel.SyncState != "pending" {
		t.Fatalf("response sync_state = %q, want pending for history-enabled channel", body.Channel.SyncState)
	}

	storedChannel, err := deps.Channels.FindByID(ctx, channelID)
	if err != nil {
		t.Fatalf("find channel: %v", err)
	}
	if storedChannel.ListenEnabled || storedChannel.ListenState != "disabled" || storedChannel.IndexedMessageCount != 0 {
		t.Fatalf("stored channel after clear = %+v", storedChannel)
	}
	for _, tc := range []struct {
		name  string
		query string
	}{
		{"messages", `SELECT count(*) FROM telegram_messages WHERE channel_id = ?`},
		{"message contents", `SELECT count(*) FROM telegram_message_contents WHERE message_id IN (?, ?)`},
		{"links", `SELECT count(*) FROM telegram_links WHERE message_id IN (?, ?)`},
		{"files", `SELECT count(*) FROM telegram_files WHERE message_id IN (?, ?)`},
		{"cursors", `SELECT count(*) FROM telegram_sync_cursors WHERE channel_id = ?`},
		{"resource stats", `SELECT count(*) FROM resource_group_counts`},
	} {
		var count int
		var scanErr error
		if strings.Contains(tc.query, "IN (?, ?)") {
			scanErr = deps.BackupDB.QueryRowContext(ctx, tc.query, stored[0].ID, stored[1].ID).Scan(&count)
		} else if strings.Contains(tc.query, "channel_id = ?") {
			scanErr = deps.BackupDB.QueryRowContext(ctx, tc.query, channelID).Scan(&count)
		} else {
			scanErr = deps.BackupDB.QueryRowContext(ctx, tc.query).Scan(&count)
		}
		if scanErr != nil {
			t.Fatalf("count %s: %v", tc.name, scanErr)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", tc.name, count)
		}
	}
	results, err := deps.Messages.Search(ctx, repository.SearchParams{Query: "movie", ChannelID: channelID, Limit: 10})
	if err != nil {
		t.Fatalf("search cleared messages: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("search results len = %d, want 0", len(results))
	}
}

func TestChannelAnalyze(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:           accountID,
		TelegramChannelID:   61,
		Title:               "Analysis Channel",
		Username:            "analysis",
		Type:                model.ChannelTypeChannel,
		MemberCount:         123,
		Description:         "metadata only",
		HistorySyncEnabled:  true,
		SyncProfile:         "Normal",
		ListenEnabled:       true,
		RemoteSearchAllowed: false,
	})
	_, _ = deps.WatchRules.Create(ctx, model.WatchRule{
		ChannelID:    channelID,
		Enabled:      true,
		Includes:     []string{"电影", "课程"},
		Excludes:     []string{"广告"},
		MessageTypes: []string{"text", "file"},
		LinkTypes:    []string{"cloud_drive", "magnet"},
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/analyze", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body model.ChannelAnalysis
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Channel.ID != channelID || body.Channel.Title != "Analysis Channel" || body.Channel.MemberCount != 123 {
		t.Fatalf("channel analysis metadata = %+v", body.Channel)
	}
	if !body.Control.HistorySyncEnabled || body.Control.SyncProfile != "Normal" || !body.Control.ListenEnabled || body.Control.RemoteSearchAllowed {
		t.Fatalf("control = %+v", body.Control)
	}
	if body.WatchRule == nil || !sameStringSlices(body.WatchRule.MessageTypes, []string{"text", "file"}) ||
		!sameStringSlices(body.WatchRule.LinkTypes, []string{"cloud_drive", "magnet"}) {
		t.Fatalf("watch rule = %+v", body.WatchRule)
	}
	if body.IndexedCounts.Messages != 0 || body.IndexedCounts.Links != 0 || body.IndexedCounts.Files != 0 {
		t.Fatalf("indexed counts = %+v, want zero counts", body.IndexedCounts)
	}
}

func TestRemoteSearchEntry(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	allowedID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:           accountID,
		TelegramChannelID:   71,
		Title:               "Allowed",
		Type:                model.ChannelTypeChannel,
		RemoteSearchAllowed: true,
	})
	blockedID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:           accountID,
		TelegramChannelID:   72,
		Title:               "Blocked",
		Type:                model.ChannelTypeChannel,
		RemoteSearchAllowed: false,
	})
	syncedAt := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	syncedID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:           accountID,
		TelegramChannelID:   73,
		Title:               "Synced",
		Type:                model.ChannelTypeChannel,
		LastMessageID:       100,
		LastSyncTime:        &syncedAt,
		RemoteSearchAllowed: true,
	})
	router := NewRouter(deps)

	for _, tc := range []struct {
		name string
		body string
		code int
		err  string
	}{
		{"empty query", `{"channel_id":` + strconv.FormatInt(allowedID, 10) + `,"query":" "}`, http.StatusBadRequest, "bad_request"},
		{"blocked", `{"channel_id":` + strconv.FormatInt(blockedID, 10) + `,"query":"ubuntu iso"}`, http.StatusConflict, "remote_search_not_allowed"},
		{"synced", `{"channel_id":` + strconv.FormatInt(syncedID, 10) + `,"query":"ubuntu iso"}`, http.StatusConflict, "remote_search_requires_unsynced_channel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/admin/search/remote", bytes.NewBufferString(tc.body))
			withAdminSession(t, deps, req)
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			if w.Code != tc.code {
				t.Fatalf("status = %d body=%s, want %d", w.Code, w.Body.String(), tc.code)
			}
			if tc.err != "" {
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("invalid error JSON: %v", err)
				}
				if body.Error.Code != tc.err {
					t.Fatalf("error code = %q, want %q body=%s", body.Error.Code, tc.err, w.Body.String())
				}
			}
		})
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/search/remote", bytes.NewBufferString(`{"channel_id":`+strconv.FormatInt(allowedID, 10)+`,"query":"ubuntu iso"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", w.Code, w.Body.String())
	}
	var body model.RemoteSearchTask
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.ID == 0 || body.Status != model.RemoteSearchStatusQueued || body.Source != "remote" || body.Query != "ubuntu iso" || body.ExpiresAt.IsZero() {
		t.Fatalf("remote search task = %+v", body)
	}
}

func TestRemoteSearchResultAPI(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{
		AccountID:           accountID,
		TelegramChannelID:   71,
		Title:               "Allowed",
		Type:                model.ChannelTypeChannel,
		RemoteSearchAllowed: true,
	})
	deps.RemoteSearchExec = search.NewRemoteService(search.RemoteOptions{
		Accounts: deps.Accounts,
		Channels: deps.Channels,
		Tasks:    deps.RemoteSearch,
		Cursors:  repository.NewSyncCursorRepository(deps.BackupDB),
		Telegram: &apiRemoteSearchClient{items: []telegram.Message{{
			TelegramMessageID: 99,
			Text:              "ubuntu remote result",
			RawJSON:           "{}",
			Date:              time.Now().UTC(),
		}}},
		Sessions: deps.Sessions,
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/search/remote", bytes.NewBufferString(`{"channel_id":`+strconv.FormatInt(channelID, 10)+`,"query":"ubuntu"}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s, want 202", w.Code, w.Body.String())
	}
	var task model.RemoteSearchTask
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatalf("invalid task JSON: %v", err)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/search/remote/"+strconv.FormatInt(task.ID, 10), nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("result status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var result model.RemoteSearchResults
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid result JSON: %v", err)
	}
	if result.Task.ID != task.ID || len(result.Items) != 1 || result.Items[0].Source != "remote" || result.Items[0].Text != "ubuntu remote result" {
		t.Fatalf("remote result = %+v", result)
	}
}

func TestBatchSyncAPIEnqueuesHistorySyncTask(t *testing.T) {
	ctx := context.Background()
	deps, conn := testDepsWithDB(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelID, _ := deps.Channels.Save(ctx, model.Channel{AccountID: accountID, TelegramChannelID: 10, AccessHash: 20, Title: "VIP", Type: model.ChannelTypeChannel})
	deps.History = history.NewService(history.Options{
		DB: conn, Accounts: deps.Accounts, Channels: deps.Channels, Messages: deps.Messages, Links: deps.Links,
		Telegram:  &apiHistoryClient{date: time.Now().UTC()},
		Sessions:  session.NewManager(filepath.Join(t.TempDir(), "sessions")),
		Extractor: link.NewExtractor(), HistoryBatchSize: 10, Workers: 2,
		RetryPolicy: retry.Policy{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxTries: 1, Sleep: func(context.Context, time.Duration) error { return nil }},
	})
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/channels/sync", bytes.NewBufferString(`{"channel_ids":[`+strconv.FormatInt(channelID, 10)+`]}`))
	withAdminSession(t, deps, req)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", w.Code, w.Body.String())
	}
	var body struct {
		TaskID int64  `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.TaskID == 0 || body.Status != model.TaskStatusQueued {
		t.Fatalf("response = %+v, want queued task id", body)
	}
	task, err := deps.TaskRepository.FindByID(ctx, body.TaskID)
	if err != nil {
		t.Fatalf("find history sync task: %v", err)
	}
	if task.Type != model.TaskTypeHistorySync || task.Status != model.TaskStatusQueued {
		t.Fatalf("task = %+v, want queued history sync", task)
	}
	var payload taskpkg.HistorySyncPayload
	if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !reflect.DeepEqual(payload.ChannelIDs, []int64{channelID}) {
		t.Fatalf("payload channel_ids = %+v, want [%d]", payload.ChannelIDs, channelID)
	}
}

func TestAccountChannelSyncAPIReturnsAsyncJob(t *testing.T) {
	ctx := context.Background()
	deps := testDeps(t)
	accountID, _ := deps.Accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	channelClient := &apiChannelClient{
		items: []telegram.Channel{{TelegramChannelID: 11, AccessHash: 22, Title: "Account Channel", Type: model.ChannelTypeChannel}},
	}
	deps.ChannelSync = channel.NewService(deps.Channels, channelClient, deps.Sessions)
	router := NewRouter(deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+strconv.FormatInt(accountID, 10)+"/channels/sync-metadata", nil)
	withAdminSession(t, deps, req)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", w.Code, w.Body.String())
	}
	var body struct {
		TaskID int64  `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.TaskID == 0 || body.Status != model.TaskStatusQueued {
		t.Fatalf("response = %+v, want queued task", body)
	}

	// Wait for task completion (poll since task execution is async via goroutine)
	timeout := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-timeout:
			t.Fatal("task did not complete within timeout")
		case <-tick.C:
			task, err := deps.TaskRepository.FindByID(ctx, body.TaskID)
			if err != nil {
				t.Fatalf("find task: %v", err)
			}
			if task.Status == model.TaskStatusSucceeded {
				goto taskDone
			}
			if task.Status == model.TaskStatusFailed {
				t.Fatalf("task failed: %s", task.ErrorMessage)
			}
		}
	}
taskDone:

	items, err := deps.Channels.FindByAccountID(ctx, accountID)
	if err != nil {
		t.Fatalf("find channels: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Account Channel" {
		t.Fatalf("channels = %+v, want synced account channel", items)
	}
}

type apiHistoryClient struct {
	telegram.NopClient
	date time.Time
}

type apiChannelClient struct {
	telegram.NopClient
	items []telegram.Channel
}

func (c *apiChannelClient) ListChannels(ctx context.Context, account telegram.AccountSession) ([]telegram.Channel, error) {
	return c.items, nil
}

func (f *apiHistoryClient) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	if offsetID > 0 {
		return nil, nil
	}
	return []telegram.Message{{TelegramMessageID: 1, SenderID: 1, Text: "api sync", RawJSON: "{}", Date: f.date}}, nil
}

type apiRemoteSearchClient struct {
	telegram.NopClient
	items []telegram.Message
}

func (f *apiRemoteSearchClient) SearchMessages(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, query string, limit int) ([]telegram.Message, error) {
	return f.items, nil
}

func testDeps(t *testing.T) Dependencies {
	t.Helper()
	deps, _ := testDepsWithDB(t)
	return deps
}

func errorMessage(t *testing.T, data []byte) string {
	t.Helper()
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, string(data))
	}
	return body.Error.Message
}

func testDepsWithDB(t *testing.T) (Dependencies, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	runtimeConfig := config.Config{Storage: config.StorageConfig{Path: root, MaxDBSize: config.Size(10), MaxMediaCache: config.Size(20)}}
	if err := config.EnsureRuntimeDirs(runtimeConfig); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	conn, err := db.Open(filepath.Join(root, "tg-search.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(context.Background(), conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	channels := repository.NewChannelRepository(conn)
	messages := repository.NewMessageRepository(conn)
	links := repository.NewLinkRepository(conn)
	files := repository.NewFileRepository(conn)
	resourceStats := repository.NewResourceStatsRepository(conn)
	watchRules := repository.NewWatchRuleRepository(conn)
	remoteSearch := repository.NewRemoteSearchTaskRepository(conn)
	savedSearches := repository.NewSavedSearchRepository(conn)
	webhooks := repository.NewWebhookRepository(conn)
	deliveries := repository.NewNotificationDeliveryRepository(conn)
	botSubscriptions := repository.NewTelegramBotSubscriptionRepository(conn)
	maintenance := repository.NewMaintenanceRepository(conn)
	status := repository.NewStatusRepository(conn)
	taskRepository := taskpkg.NewRepository(conn)
	taskService := taskpkg.NewService(taskRepository)
	users := repository.NewUserRepository(conn)
	adminSessions := repository.NewAdminSessionRepository(conn)
	apiKeys := repository.NewAPIKeyRepository(conn)
	settings := repository.NewSettingsRepository(conn)
	sessions := session.NewManager(filepath.Join(t.TempDir(), "sessions"))
	client := telegram.NopClient{}
	watchFilter := messagefilter.New(messagefilter.NewSettingsRuleStore(watchRules, settings))
	searchService := search.NewService(messages, links, files, channels)
	resourceService := resource.NewService(links, files, resourceStats)
	notificationService := notification.NewService(notification.Options{
		SavedSearches: savedSearches,
		Webhooks:      webhooks,
		Deliveries:    deliveries,
		BotSubs:       botSubscriptions,
	})
	historyService := history.NewService(history.Options{
		DB: conn, Accounts: accounts, Channels: channels, Messages: messages, Links: links,
		Resources: resourceService, Notifications: notificationService,
		Telegram: client, Sessions: sessions, Extractor: link.NewExtractor(), Filter: watchFilter, HistoryBatchSize: 100,
	})
	channelService := channel.NewService(channels, client, sessions)
	channelWebAccessService := channel.NewWebAccessService(channels, nil)
	return Dependencies{
		Users: users, APIKeys: apiKeys, Settings: settings, AdminAuth: adminauth.NewService(users, adminSessions),
		Accounts: accounts, Channels: channels, Messages: messages, Links: links, Files: files, WatchRules: watchRules, RemoteSearch: remoteSearch, SavedSearches: savedSearches, BotSubscriptions: botSubscriptions, Webhooks: webhooks, Deliveries: deliveries, Maintenance: maintenance, Status: status,
		BackupDB: conn, BackupDir: filepath.Join(t.TempDir(), "backup"),
		RuntimeConfig: runtimeConfig,
		StorageUsage:  storage.NewUsageService(runtimeConfig),
		Search:        searchService, History: historyService, Resources: resourceService, Notifications: notificationService, ChannelSync: channelService, ChannelWebAccess: channelWebAccessService,
		Tasks: taskService, TaskRepository: taskRepository, Events: taskpkg.NewEventBroker(),
		Telegram: client, Sessions: sessions, CodeStore: telegram.NewCodeStore(), QRLogins: NewQRLoginStore(2 * time.Minute),
	}, conn
}

type fakeTelegram struct {
	telegram.NopClient
	mu                   sync.Mutex
	channels             []telegram.Channel
	listErr              error
	logoutErr            error
	logoutSessions       []telegram.AccountSession
	logoutSessionExisted bool
	fetchHistoryCalls    int
	sendCodePhones       []string
	signInPhones         []string
	qrTokenURL           string
	qrExpires            time.Time
	qrSessionPath        string
	qrSession            *fakeQRLoginSession
}

func (f *fakeTelegram) SendCode(ctx context.Context, phone string, sessionPath string) (telegram.SentCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCodePhones = append(f.sendCodePhones, phone)
	return telegram.SentCode{PhoneCodeHash: "hash"}, nil
}

func (f *fakeTelegram) SignIn(ctx context.Context, phone string, code string, phoneCodeHash string, sessionPath string) (telegram.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signInPhones = append(f.signInPhones, phone)
	return telegram.Profile{TelegramUserID: 42, FirstName: "Ada", LastName: "Lovelace", Username: "ada"}, nil
}

func (fakeTelegram) Password(ctx context.Context, password string, sessionPath string) (telegram.Profile, error) {
	return telegram.Profile{TelegramUserID: 43, FirstName: "Grace", LastName: "Hopper", Username: "grace"}, nil
}

func (f *fakeTelegram) StartQRLogin(ctx context.Context, sessionPath string) (telegram.QRLoginSession, error) {
	session := &fakeQRLoginSession{
		token: telegram.QRLoginToken{URL: f.qrTokenURL, ExpiresAt: f.qrExpires},
	}
	f.qrSessionPath = sessionPath
	f.qrSession = session
	return session, nil
}

func (f *fakeTelegram) ListChannels(ctx context.Context, accountSession telegram.AccountSession) ([]telegram.Channel, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := []telegram.Channel{
		{
			TelegramChannelID: accountSession.AccountID,
			Title:             "Saved Messages",
			Type:              model.ChannelTypeSavedMessages,
		},
	}
	out = append(out, f.channels...)
	return out, nil
}

func (f *fakeTelegram) Logout(ctx context.Context, accountSession telegram.AccountSession) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logoutSessions = append(f.logoutSessions, accountSession)
	if _, err := os.Stat(accountSession.SessionPath); err == nil {
		f.logoutSessionExisted = true
	}
	return f.logoutErr
}

func (f *fakeTelegram) logoutSessionPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.logoutSessions))
	for _, session := range f.logoutSessions {
		out = append(out, session.SessionPath)
	}
	return out
}

func (f *fakeTelegram) FetchHistory(ctx context.Context, account telegram.AccountSession, channel telegram.ChannelRef, offsetID int64, limit int) ([]telegram.Message, error) {
	f.fetchHistoryCalls++
	return nil, nil
}

type fakeQRLoginSession struct {
	token     telegram.QRLoginToken
	result    telegram.QRLoginPollResult
	cancelled bool
}

func (s *fakeQRLoginSession) Token() telegram.QRLoginToken {
	return s.token
}

func (s *fakeQRLoginSession) Poll(ctx context.Context) (telegram.QRLoginPollResult, error) {
	if s.result.Status == "" {
		return telegram.QRLoginPollResult{Status: telegram.QRLoginStatusPending, Token: s.token}, nil
	}
	return s.result, nil
}

func (s *fakeQRLoginSession) Cancel(ctx context.Context) error {
	s.cancelled = true
	return nil
}

type apiWebAccessChecker struct {
	results map[string]bool
	errors  map[string]error
	calls   []string
}

func (c *apiWebAccessChecker) Check(ctx context.Context, username string) (bool, error) {
	c.calls = append(c.calls, username)
	if c.errors != nil && c.errors[username] != nil {
		return false, c.errors[username]
	}
	return c.results[username], nil
}

type recordingAccountRuntime struct {
	mu      sync.Mutex
	stopped []int64
}

func (r *recordingAccountRuntime) StopAccount(ctx context.Context, accountID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = append(r.stopped, accountID)
	return nil
}

func (r *recordingAccountRuntime) stoppedIDs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int64, len(r.stopped))
	copy(out, r.stopped)
	return out
}

func sameInt64s(got []int64, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[int64]int{}
	for _, id := range got {
		seen[id]++
	}
	for _, id := range want {
		seen[id]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

func sameStrings(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, item := range got {
		seen[item]++
	}
	for _, item := range want {
		seen[item]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

func sameStringSlices(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
