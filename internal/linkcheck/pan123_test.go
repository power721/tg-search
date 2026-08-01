package linkcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// runCheck123 stands up a fake share/info API and runs check123, overriding the
// canonical API URL so the lookup hits the test server regardless of the link host.
func runCheck123(t *testing.T, shareKey, itemURL, respStatus, respBody string) Result {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/share/info", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("shareKey"); got != shareKey {
			t.Errorf("shareKey param = %q, want %q", got, shareKey)
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(parseStatus(t, respStatus))
		_, _ = w.Write([]byte(respBody))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	prev := pan123ShareInfoAPI
	pan123ShareInfoAPI = server.URL + "/api/share/info"
	t.Cleanup(func() { pan123ShareInfoAPI = prev })

	checker := NewHTTPChecker(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return checker.Check(ctx, Item{DiskType: "123", URL: itemURL})
}

func TestCheck123Valid(t *testing.T) {
	res := runCheck123(t, "ZUtrvd-phu4H", "https://1856539457.share.123pan.cn/123pan/ZUtrvd-phu4H", "200",
		`{"code":0,"message":"ok","data":{"ShareName":"demo","HasPwd":false,"Expired":false}}`)
	if res.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", res.State, res.Summary)
	}
}

func TestCheck123Expired(t *testing.T) {
	res := runCheck123(t, "DEADKEY", "https://www.123912.com/s/DEADKEY.html", "200",
		`{"code":5104,"message":"分享链接已失效","data":null}`)
	if res.State != StateBad {
		t.Fatalf("state = %q (%s), want bad", res.State, res.Summary)
	}
}

// A 403 is treated as valid (anti-bot rate limiting, not a dead link).
func TestCheck123ForbiddenIsOK(t *testing.T) {
	res := runCheck123(t, "RATELIMITED", "https://www.123865.com/s/RATELIMITED", "403", `{"code":403}`)
	if res.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", res.State, res.Summary)
	}
}

// The lookup must hit the single canonical API host, not the link's own per-user
// subdomain, so a batch of distinct-subdomain links reuses one connection pool.
func TestCheck123UsesCanonicalHost(t *testing.T) {
	var hit int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/share/info", func(w http.ResponseWriter, r *http.Request) {
		hit++
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"HasPwd":false}}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	prev := pan123ShareInfoAPI
	pan123ShareInfoAPI = server.URL + "/api/share/info"
	t.Cleanup(func() { pan123ShareInfoAPI = prev })

	checker := NewHTTPChecker(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Link host is an unrelated (fake) subdomain; the lookup must still reach the
	// canonical API host rather than the link's own host.
	res := checker.Check(ctx, Item{DiskType: "123", URL: "https://9999999.share.123pan.cn/123pan/HostCheck"})
	if res.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", res.State, res.Summary)
	}
	if hit != 1 {
		t.Fatalf("canonical API hit %d times, want 1", hit)
	}
}

func parseStatus(t *testing.T, s string) int {
	t.Helper()
	switch s {
	case "200":
		return 200
	case "403":
		return 403
	default:
		t.Fatalf("unknown test status %q", s)
		return 0
	}
}
