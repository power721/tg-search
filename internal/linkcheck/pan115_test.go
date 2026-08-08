package linkcheck

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCheck115ReturnsShareSize(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/webapi/share/snap") {
			t.Fatalf("unexpected request URL: %s", req.URL)
		}
		body := `{
			"state": true,
			"error": "",
			"errno": 0,
			"data": {
				"shareinfo": {
					"snap_id": "314741725",
					"file_size": 174735702578,
					"share_title": "test share",
					"forbid_reason": ""
				},
				"count": 1,
				"list": [{"cid": "3476685738592961899", "s": 174735702578}]
			}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}

	result := NewHTTPChecker(client).Check(context.Background(), Item{
		DiskType: "115",
		URL:      "https://115cdn.com/s/test-share?password=b0b6",
		Password: "b0b6",
	})

	if result.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", result.State, result.Summary)
	}
	if result.SizeBytes != 174_735_702_578 {
		t.Fatalf("size_bytes = %d, want 174735702578", result.SizeBytes)
	}
}
