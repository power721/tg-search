package linkcheck

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeChecker struct {
	results map[string]Result
	delays  map[string]time.Duration
}

func (f fakeChecker) Check(ctx context.Context, item Item) Result {
	if delay := f.delays[item.URL]; delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return Result{DiskType: item.DiskType, URL: item.URL, State: StateUncertain, Summary: "检测超时"}
		}
	}
	if result, ok := f.results[item.URL]; ok {
		return result
	}
	return Result{DiskType: item.DiskType, URL: item.URL, State: StateOK, Summary: "链接有效"}
}

func TestServiceChecksItemsConcurrentlyAndMarksTimeoutsUncertain(t *testing.T) {
	service := NewService(Options{
		Checker: fakeChecker{
			results: map[string]Result{
				"https://pan.quark.cn/s/ok": {DiskType: "quark", URL: "https://pan.quark.cn/s/ok", State: StateOK, Summary: "链接有效"},
			},
			delays: map[string]time.Duration{
				"https://pan.quark.cn/s/slow": 50 * time.Millisecond,
			},
		},
	})

	response, err := service.Check(context.Background(), Request{
		Timeout: 10 * time.Millisecond,
		Items: []Item{
			{DiskType: "quark", URL: "https://pan.quark.cn/s/ok"},
			{DiskType: "quark", URL: "https://pan.quark.cn/s/slow"},
		},
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if response.TimeoutMS != 10 {
		t.Fatalf("timeout_ms = %d, want 10", response.TimeoutMS)
	}
	if len(response.Results) != 2 {
		t.Fatalf("results length = %d, want 2: %+v", len(response.Results), response.Results)
	}
	if response.Results[0].State != StateOK {
		t.Fatalf("first state = %q, want ok", response.Results[0].State)
	}
	if response.Results[1].State != StateUncertain || response.Results[1].Summary == "" {
		t.Fatalf("second result = %+v, want uncertain timeout result", response.Results[1])
	}
}

func TestServiceLimitsEachDiskTypeGroupToTenItems(t *testing.T) {
	service := NewService(Options{Checker: fakeChecker{}})
	items := make([]Item, 0, 11)
	for i := 0; i < 11; i++ {
		items = append(items, Item{DiskType: "quark", URL: "https://pan.quark.cn/s/item" + string(rune('a'+i))})
	}

	_, err := service.Check(context.Background(), Request{Items: items})
	if !errors.Is(err, ErrGroupLimitExceeded) {
		t.Fatalf("Check error = %v, want ErrGroupLimitExceeded", err)
	}
}

func TestServiceUsesFiveSecondDefaultTimeout(t *testing.T) {
	service := NewService(Options{Checker: fakeChecker{}})

	response, err := service.Check(context.Background(), Request{
		Items: []Item{{DiskType: "aliyun", URL: "https://www.alipan.com/s/abc"}},
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if response.TimeoutMS != 5000 {
		t.Fatalf("timeout_ms = %d, want 5000", response.TimeoutMS)
	}
}
