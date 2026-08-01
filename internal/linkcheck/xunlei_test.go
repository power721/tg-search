package linkcheck

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// resetXunleiToken clears the process-wide captcha token cache between tests.
func resetXunleiToken(t *testing.T) {
	t.Helper()
	xunleiTokenMu.Lock()
	xunleiToken = ""
	xunleiTokenExp = time.Time{}
	xunleiTokenMu.Unlock()
}

func TestClassifyXunlei(t *testing.T) {
	item := Item{DiskType: "xunlei", URL: "https://pan.xunlei.com/s/abc?pwd=1234"}
	cases := []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{"valid", `{"share_status":"OK","file_num":"1","files":[{"name":"x"}]}`, 200, StateOK},
		{"locked empty passcode", `{"share_status":"PASS_CODE_EMPTY"}`, 200, StateLocked},
		{"locked wrong passcode", `{"share_status":"PASS_CODE_ERROR"}`, 200, StateLocked},
		{"cancelled share", `{"share_status":"CANCEL","share_status_text":"分享已取消"}`, 200, StateBad},
		{"expired share", `{"share_status":"EXPIRED"}`, 200, StateBad},
		{"unknown id 4xx", `{"error":"invalid_argument","error_description":"请求参数错误"}`, 400, StateBad},
		{"server error", ``, 502, StateUncertain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyXunlei(item, []byte(tc.body), tc.status)
			if got.State != tc.want {
				t.Fatalf("state = %q (%s), want %q", got.State, got.Summary, tc.want)
			}
		})
	}
}

func TestIsXunleiCaptchaInvalid(t *testing.T) {
	if !isXunleiCaptchaInvalid([]byte(`{"error":"captcha_invalid","error_code":9}`), 400) {
		t.Error("captcha_invalid body should be detected")
	}
	if !isXunleiCaptchaInvalid([]byte(`{"error_code":9}`), 400) {
		t.Error("error_code 9 should be detected")
	}
	if isXunleiCaptchaInvalid([]byte(`{"share_status":"OK"}`), 200) {
		t.Error("OK response should not be flagged")
	}
	if isXunleiCaptchaInvalid([]byte(`{"error":"invalid_argument"}`), 400) {
		t.Error("invalid_argument should not be flagged as captcha failure")
	}
}

func TestXunleiCaptchaSign(t *testing.T) {
	a := xunleiCaptchaSign("1700000000000")
	b := xunleiCaptchaSign("1700000000000")
	if a != b {
		t.Error("sign should be deterministic for a fixed timestamp")
	}
	if !strings.HasPrefix(a, "1.") {
		t.Fatalf("sign should be prefixed with '1.', got %q", a)
	}
	// Final digest is a 32-char hex MD5.
	if got := len(strings.TrimPrefix(a, "1.")); got != 32 {
		t.Fatalf("sign digest length = %d, want 32", got)
	}
	if xunleiCaptchaSign("1700000000001") == a {
		t.Error("sign should change with the timestamp")
	}
}

// xunleiTestServer builds an httptest stand-in for the xunlei captcha + share APIs.
// The captcha handler returns the given token and optional verification url; the
// share handler delegates to shareResp (status, body) and counts calls.
func xunleiTestServer(t *testing.T, captchaURL string, shareResp func() (int, string)) (
	server *httptest.Server, initCount, shareCount *int64,
) {
	t.Helper()
	var initN, shareN int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/shield/captcha/init", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&initN, 1)
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Action string `json:"action"`
			Meta   struct {
				CaptchaSign string `json:"captcha_sign"`
				Timestamp   string `json:"timestamp"`
			} `json:"meta"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Action != "get:/drive/v1/share" {
			t.Errorf("unexpected action %q", req.Action)
		}
		if !strings.HasPrefix(req.Meta.CaptchaSign, "1.") || req.Meta.Timestamp == "" {
			t.Error("captcha init must carry a computed sign + timestamp")
		}
		w.Header().Set("content-type", "application/json")
		if captchaURL != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"captcha_token": "tok", "url": captchaURL})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"captcha_token": "tok", "expires_in": 300})
	})
	mux.HandleFunc("/drive/v1/share", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&shareN, 1)
		status, body := shareResp()
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &initN, &shareN
}

func runXunleiCheck(t *testing.T, shareResp func() (int, string), captchaURL string) (Result, int64, int64) {
	t.Helper()
	resetXunleiToken(t)
	server, initN, shareN := xunleiTestServer(t, captchaURL, shareResp)
	prevHost, prevCaptcha := xunleiShareHost, xunleiCaptchaInitURL
	xunleiShareHost = server.URL
	xunleiCaptchaInitURL = server.URL + "/v1/shield/captcha/init"
	t.Cleanup(func() {
		xunleiShareHost = prevHost
		xunleiCaptchaInitURL = prevCaptcha
	})
	checker := NewHTTPChecker(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := checker.Check(ctx, Item{DiskType: "xunlei", URL: "https://pan.xunlei.com/s/VOtest?pwd=1234"})
	return res, atomic.LoadInt64(initN), atomic.LoadInt64(shareN)
}

func TestCheckXunleiValid(t *testing.T) {
	res, initN, shareN := runXunleiCheck(t,
		func() (int, string) { return 200, `{"share_status":"OK","file_num":"1"}` }, "")
	if res.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", res.State, res.Summary)
	}
	if initN != 1 {
		t.Errorf("expected exactly one captcha init for a single link, got %d", initN)
	}
	if shareN != 1 {
		t.Errorf("expected one share call, got %d", shareN)
	}
}

func TestCheckXunleiLocked(t *testing.T) {
	res, _, _ := runXunleiCheck(t,
		func() (int, string) { return 200, `{"share_status":"PASS_CODE_ERROR"}` }, "")
	if res.State != StateLocked {
		t.Fatalf("state = %q (%s), want locked", res.State, res.Summary)
	}
}

func TestCheckXunleiBad(t *testing.T) {
	res, _, _ := runXunleiCheck(t,
		func() (int, string) { return 200, `{"share_status":"CANCEL","share_status_text":"分享已取消"}` }, "")
	if res.State != StateBad {
		t.Fatalf("state = %q (%s), want bad", res.State, res.Summary)
	}
}

func TestCheckXunleiRetriesAfterCaptchaInvalid(t *testing.T) {
	var attempt int64
	res, initN, shareN := runXunleiCheck(t, func() (int, string) {
		// First share call is rejected as an expired token; the refresh+retry succeeds.
		if atomic.AddInt64(&attempt, 1) == 1 {
			return 400, `{"error":"captcha_invalid","error_code":9}`
		}
		return 200, `{"share_status":"OK","file_num":"1"}`
	}, "")
	if res.State != StateOK {
		t.Fatalf("state = %q (%s), want ok after retry", res.State, res.Summary)
	}
	if shareN != 2 {
		t.Errorf("expected share call to be retried once, got %d calls", shareN)
	}
	if initN != 2 {
		t.Errorf("expected a token refresh (2 inits), got %d", initN)
	}
}

func TestCheckXunleiPersistentCaptchaInvalidIsUncertain(t *testing.T) {
	res, _, shareN := runXunleiCheck(t,
		func() (int, string) { return 400, `{"error":"captcha_invalid","error_code":9}` }, "")
	// Must NOT be marked bad (a persistent captcha failure is not a dead link).
	if res.State != StateUncertain {
		t.Fatalf("state = %q (%s), want uncertain", res.State, res.Summary)
	}
	if shareN != 2 {
		t.Errorf("expected initial + one retry, got %d", shareN)
	}
}

func TestCheckXunleiInteractiveCaptcha(t *testing.T) {
	res, initN, shareN := runXunleiCheck(t,
		func() (int, string) { return 200, `{"share_status":"OK"}` }, "https://xunlei.com/slider")
	if res.State != StateUncertain {
		t.Fatalf("state = %q (%s), want uncertain", res.State, res.Summary)
	}
	if initN != 1 {
		t.Errorf("expected one init, got %d", initN)
	}
	if shareN != 0 {
		t.Errorf("share call must be skipped when a captcha challenge is required, got %d", shareN)
	}
}

// A single token must be reused across a batch, so the captcha init runs once.
func TestCheckXunleiTokenReusedAcrossBatch(t *testing.T) {
	resetXunleiToken(t)
	server, initN, shareN := xunleiTestServer(t, "",
		func() (int, string) { return 200, `{"share_status":"OK","file_num":"1"}` })
	prevHost, prevCaptcha := xunleiShareHost, xunleiCaptchaInitURL
	xunleiShareHost = server.URL
	xunleiCaptchaInitURL = server.URL + "/v1/shield/captcha/init"
	t.Cleanup(func() {
		xunleiShareHost = prevHost
		xunleiCaptchaInitURL = prevCaptcha
	})
	checker := NewHTTPChecker(nil)
	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		res := checker.Check(ctx, Item{DiskType: "xunlei", URL: "https://pan.xunlei.com/s/VOtest?pwd=1234"})
		cancel()
		if res.State != StateOK {
			t.Fatalf("link %d state = %q (%s), want ok", i, res.State, res.Summary)
		}
	}
	if got := atomic.LoadInt64(initN); got != 1 {
		t.Errorf("batch of 4 should trigger a single captcha init, got %d", got)
	}
	if got := atomic.LoadInt64(shareN); got != 4 {
		t.Errorf("expected 4 share calls, got %d", got)
	}
}
