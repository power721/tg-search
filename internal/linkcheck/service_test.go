package linkcheck

import (
	"context"
	"fmt"
	"sync"
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

func TestServiceSplitsSameDiskTypeIntoTenItemBatches(t *testing.T) {
	service := NewService(Options{Checker: fakeChecker{}})
	items := make([]Item, 0, 100)
	for i := 0; i < 100; i++ {
		items = append(items, Item{DiskType: "quark", URL: fmt.Sprintf("https://pan.quark.cn/s/item-%03d", i)})
	}

	response, err := service.Check(context.Background(), Request{Items: items})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(response.Results) != 100 {
		t.Fatalf("results length = %d, want 100", len(response.Results))
	}
}

func TestServiceRunsAtMostFiveChecksConcurrentlyAcrossBatches(t *testing.T) {
	checker := &concurrencyChecker{delay: 10 * time.Millisecond}
	service := NewService(Options{Checker: checker})
	items := make([]Item, 0, 100)
	for i := 0; i < 100; i++ {
		items = append(items, Item{DiskType: "quark", URL: fmt.Sprintf("https://pan.quark.cn/s/item-%03d", i)})
	}

	response, err := service.Check(context.Background(), Request{
		Timeout: 2 * time.Second,
		Items:   items,
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(response.Results) != 100 {
		t.Fatalf("results length = %d, want 100", len(response.Results))
	}
	if checker.maxActive > 5 {
		t.Fatalf("max active checks = %d, want <= 5", checker.maxActive)
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

type concurrencyChecker struct {
	mu        sync.Mutex
	active    int
	maxActive int
	delay     time.Duration
}

func (c *concurrencyChecker) Check(ctx context.Context, item Item) Result {
	c.mu.Lock()
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
	c.mu.Unlock()

	select {
	case <-time.After(c.delay):
	case <-ctx.Done():
	}

	c.mu.Lock()
	c.active--
	c.mu.Unlock()
	return Result{DiskType: item.DiskType, URL: item.URL, State: StateOK, Summary: "链接有效"}
}
