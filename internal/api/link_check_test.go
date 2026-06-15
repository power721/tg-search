package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExternalLinkCheckAPIRequiresAPIKey(t *testing.T) {
	router := NewRouter(testDeps(t))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/check/links", bytes.NewBufferString(`{"items":[{"disk_type":"quark","url":"https://pan.quark.cn/s/abc"}]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s, want 401", w.Code, w.Body.String())
	}
}

func TestExternalLinkCheckAPIAcceptsMoreThanTenLinksForSameDiskType(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)

	var items []map[string]string
	for i := 0; i < 100; i++ {
		items = append(items, map[string]string{
			"disk_type": "unsupported-drive",
			"url":       fmt.Sprintf("https://example.com/share-%03d", i),
		})
	}
	payload, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/check/links", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Results []struct {
				State string `json:"state"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v; body=%s", err, w.Body.String())
	}
	if len(body.Data.Results) != 100 {
		t.Fatalf("results length = %d, want 100", len(body.Data.Results))
	}
}

func TestExternalLinkCheckAPIReturnsGroupedResultsWithConfiguredTimeout(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	key := createTestAPIKey(t, router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/check/links", bytes.NewBufferString(`{
		"timeout": 2,
		"items": [
			{"disk_type": "unsupported-drive", "url": "https://example.com/share"}
		]
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			TimeoutMS int64 `json:"timeout_ms"`
			Results   []struct {
				DiskType string `json:"disk_type"`
				URL      string `json:"url"`
				State    string `json:"state"`
			} `json:"results"`
			Grouped map[string][]struct {
				State string `json:"state"`
			} `json:"grouped"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v; body=%s", err, w.Body.String())
	}
	if body.Code != 0 || body.Data.TimeoutMS != 2000 {
		t.Fatalf("response = %+v body=%s, want code 0 timeout 2000", body, w.Body.String())
	}
	if len(body.Data.Results) != 1 || body.Data.Results[0].State != "unsupported" {
		t.Fatalf("results = %+v, want one unsupported result", body.Data.Results)
	}
	if got := body.Data.Grouped["unsupported-drive"]; len(got) != 1 || got[0].State != "unsupported" {
		t.Fatalf("grouped = %+v, want unsupported-drive group", body.Data.Grouped)
	}
}
