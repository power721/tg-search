package linkcheck

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCheckUCReturnsShareSize(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.Contains(req.URL.Path, "/sharepage/token"):
			body = `{"code":0,"message":"ok","data":{"stoken":"uc-token"}}`
		case strings.Contains(req.URL.Path, "/transfer_share/detail"):
			body = `{"status":200,"code":0,"message":"ok","data":{"list":[{"fid":"root"}],"share":{"size":9876543210,"partial_violation":false}}}`
		default:
			t.Fatalf("unexpected request URL: %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}

	result := NewHTTPChecker(client).Check(context.Background(), Item{DiskType: "uc", URL: "https://drive.uc.cn/s/20a518857008"})
	if result.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", result.State, result.Summary)
	}
	if result.SizeBytes != 9_876_543_210 {
		t.Fatalf("size_bytes = %d, want 9876543210", result.SizeBytes)
	}
}
