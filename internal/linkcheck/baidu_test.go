package linkcheck

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func baiduResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestCheckBaiduRateLimitedIsNotBad(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/share/verify") {
			return baiduResponse(`{"errno":-62}`), nil
		}
		if strings.Contains(req.URL.Path, "/share/list") {
			return baiduResponse(`{"errno":-65,"errmsg":"操作过于频繁"}`), nil
		}
		t.Fatalf("unexpected request URL: %s", req.URL)
		return nil, nil
	})}

	for _, item := range []Item{
		{DiskType: "baidu", URL: "https://pan.baidu.com/s/1abcDEF123?pwd=8888"},
		{DiskType: "baidu", URL: "https://pan.baidu.com/s/1abcDEF456"},
	} {
		result := NewHTTPChecker(client).Check(context.Background(), item)
		if result.State != StateRateLimited {
			t.Fatalf("state = %q (%s), want rate_limited", result.State, result.Summary)
		}
	}
}

func TestCheckBaiduDeletedShareIsBad(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return baiduResponse(`{"errno":-21}`), nil
	})}

	result := NewHTTPChecker(client).Check(context.Background(), Item{
		DiskType: "baidu",
		URL:      "https://pan.baidu.com/s/1abcDEF789",
	})

	if result.State != StateBad {
		t.Fatalf("state = %q (%s), want bad", result.State, result.Summary)
	}
}
