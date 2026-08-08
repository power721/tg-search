package linkcheck

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCheckBaiduSumsRootItemSizes(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/share/list") {
			t.Fatalf("unexpected request URL: %s", req.URL)
		}
		if got := req.URL.Query().Get("num"); got != "100" {
			t.Fatalf("num = %q, want 100", got)
		}
		body := `{"errno":0,"list":[{"isdir":1,"size":3000},{"isdir":0,"size":2000}]}`
		return testHTTPResponse(req, body), nil
	})}

	result := NewHTTPChecker(client).Check(context.Background(), Item{DiskType: "baidu", URL: "https://pan.baidu.com/s/1test"})
	if result.State != StateOK || result.SizeBytes != 5_000 {
		t.Fatalf("result = %+v, want ok with size 5000", result)
	}
}

func TestCheckTianyiReturnsRootItemSize(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `<shareVO><needAccessCode>0</needAccessCode><shareId>123</shareId><fileName>root</fileName><fileSize>6000</fileSize></shareVO>`
		return testHTTPResponse(req, body), nil
	})}

	result := NewHTTPChecker(client).Check(context.Background(), Item{DiskType: "tianyi", URL: "https://cloud.189.cn/t/test"})
	if result.State != StateOK || result.SizeBytes != 6_000 {
		t.Fatalf("result = %+v, want ok with size 6000", result)
	}
}

func testHTTPResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
