package linkcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// check123 derives the API host from the link's own host, so a single httptest
// server stands in for any 123pan domain.
func runCheck123(t *testing.T, shareKey, respStatus, respBody string) Result {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/share/info", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("shareKey") != shareKey {
			t.Errorf("shareKey param = %q, want %q", r.URL.Query().Get("shareKey"), shareKey)
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(parseStatus(t, respStatus))
		_, _ = w.Write([]byte(respBody))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	checker := NewHTTPChecker(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return checker.Check(ctx, Item{DiskType: "123", URL: server.URL + "/123pan/" + shareKey})
}

func TestCheck123Valid(t *testing.T) {
	res := runCheck123(t, "ZUtrvd-phu4H", "200",
		`{"code":0,"message":"ok","data":{"ShareName":"demo","HasPwd":false,"Expired":false}}`)
	if res.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", res.State, res.Summary)
	}
}

func TestCheck123Expired(t *testing.T) {
	res := runCheck123(t, "DEADKEY", "200",
		`{"code":5104,"message":"分享链接已失效","data":null}`)
	if res.State != StateBad {
		t.Fatalf("state = %q (%s), want bad", res.State, res.Summary)
	}
}

// A 403 is treated as valid (anti-bot rate limiting, not a dead link).
func TestCheck123ForbiddenIsOK(t *testing.T) {
	res := runCheck123(t, "RATELIMITED", "403", `{"code":403,"message":"forbidden"}`)
	if res.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", res.State, res.Summary)
	}
}

// The API must be called against the link's own host, not the dead www.123pan.com.
func TestCheck123UsesLinkHost(t *testing.T) {
	var calledHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledHost = r.Host
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"HasPwd":false}}`))
	}))
	t.Cleanup(server.Close)

	checker := NewHTTPChecker(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := checker.Check(ctx, Item{DiskType: "123", URL: server.URL + "/s/HostCheck"})
	if res.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", res.State, res.Summary)
	}
	if calledHost == "" {
		t.Fatal("share/info API was never hit")
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
