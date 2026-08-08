package linkcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func runCheck123(t *testing.T, shareKey, itemURL, respStatus, respBody string) Result {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/share/list", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("shareKey"); got != shareKey {
			t.Errorf("shareKey param = %q, want %q", got, shareKey)
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(parseStatus(t, respStatus))
		_, _ = w.Write([]byte(respBody))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	previous := pan123ShareListAPI
	pan123ShareListAPI = server.URL + "/api/share/list"
	t.Cleanup(func() { pan123ShareListAPI = previous })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return NewHTTPChecker(nil).Check(ctx, Item{DiskType: "123", URL: itemURL})
}

func TestCheck123Valid(t *testing.T) {
	result := runCheck123(t, "ZUtrvd-phu4H", "https://1856539457.share.123pan.cn/123pan/ZUtrvd-phu4H", "200",
		`{"code":0,"message":"ok","data":{"Next":"-1","Expired":false,"InfoList":[{"Size":1024}]}}`)
	if result.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", result.State, result.Summary)
	}
}

func TestCheck123ReturnsSummedRootItemSizes(t *testing.T) {
	result := runCheck123(t, "MPrAjv-HE4W3", "https://www.123pan.com/s/MPrAjv-HE4W3", "200",
		`{"code":0,"data":{"Next":"-1","Len":2,"Expired":false,"InfoList":[{"Type":1,"Size":85185734402},{"Type":1,"Size":1000}]}}`)
	if result.State != StateOK || result.SizeBytes != 85_185_735_402 {
		t.Fatalf("result = %+v, want ok with size 85185735402", result)
	}
}

func TestCheck123OmitsPartialSizeWhenRootListHasNextPage(t *testing.T) {
	result := runCheck123(t, "PAGED", "https://www.123pan.com/s/PAGED", "200",
		`{"code":0,"data":{"Next":"cursor","InfoList":[{"Size":1024}]}}`)
	if result.State != StateOK || result.SizeBytes != 0 {
		t.Fatalf("result = %+v, want ok without partial size", result)
	}
}

func TestCheck123Expired(t *testing.T) {
	result := runCheck123(t, "DEADKEY", "https://www.123912.com/s/DEADKEY.html", "200",
		`{"code":5104,"message":"分享链接已失效","data":null}`)
	if result.State != StateBad {
		t.Fatalf("state = %q (%s), want bad", result.State, result.Summary)
	}
}

func TestCheck123ForbiddenIsOK(t *testing.T) {
	result := runCheck123(t, "RATELIMITED", "https://www.123865.com/s/RATELIMITED", "403", `{"code":403}`)
	if result.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", result.State, result.Summary)
	}
}

func TestCheck123UsesSingleCanonicalRequest(t *testing.T) {
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/share/list", func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"code":0,"data":{"Next":"-1","InfoList":[]}}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	previous := pan123ShareListAPI
	pan123ShareListAPI = server.URL + "/api/share/list"
	t.Cleanup(func() { pan123ShareListAPI = previous })

	result := NewHTTPChecker(nil).Check(context.Background(), Item{DiskType: "123", URL: "https://9999999.share.123pan.cn/123pan/HostCheck"})
	if result.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", result.State, result.Summary)
	}
	if hits != 1 {
		t.Fatalf("canonical API hits = %d, want 1", hits)
	}
}

func parseStatus(t *testing.T, value string) int {
	t.Helper()
	switch value {
	case "200":
		return http.StatusOK
	case "403":
		return http.StatusForbidden
	default:
		t.Fatalf("unknown test status %q", value)
		return 0
	}
}
