package linkcheck

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestCheckQuarkReturnsShareSize(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.Contains(req.URL.Path, "/sharepage/token"):
			body = `{"code":0,"message":"ok","data":{"stoken":"share-token"}}`
		case strings.Contains(req.URL.Path, "/sharepage/detail"):
			body = `{
				"code": 0,
				"message": "ok",
				"data": {
					"list": [{"fid": "0ba1aa9545d6424db4205113426181f2"}],
					"share": {
						"status": 1,
						"partial_violation": false,
						"size": 42704901049
					}
				}
			}`
		default:
			t.Fatalf("unexpected request URL: %s", req.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}

	result := NewHTTPChecker(client).Check(context.Background(), Item{
		DiskType: "quark",
		URL:      "https://pan.quark.cn/s/08fffee798dc",
	})

	if result.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", result.State, result.Summary)
	}
	if result.SizeBytes != 42_704_901_049 {
		t.Fatalf("size_bytes = %d, want 42704901049", result.SizeBytes)
	}
}
