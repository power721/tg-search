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
	resetCacheForTest()
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

func TestServiceChecksAllItemsOfSameDiskType(t *testing.T) {
	resetCacheForTest()
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

func TestServiceBoundedByConfiguredConcurrency(t *testing.T) {
	resetCacheForTest()
	checker := &concurrencyChecker{delay: 10 * time.Millisecond}
	service := NewService(Options{Checker: checker, Concurrency: 8})
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
	if checker.maxActive > 8 {
		t.Fatalf("max active checks = %d, want <= 8", checker.maxActive)
	}
}

func TestServiceUsesFiveSecondDefaultTimeout(t *testing.T) {
	resetCacheForTest()
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

func TestServiceDeduplicatesIdenticalLinks(t *testing.T) {
	resetCacheForTest()
	checker := &countingChecker{}
	service := NewService(Options{Checker: checker})

	items := []Item{
		{DiskType: "quark", URL: "https://pan.quark.cn/s/dup"},
		{DiskType: "baidu", URL: "https://pan.baidu.com/s/other"},
		{DiskType: "quark", URL: "https://pan.quark.cn/s/dup"}, // duplicate
		{DiskType: "quark", URL: "https://pan.quark.cn/s/dup"}, // duplicate
	}

	response, err := service.Check(context.Background(), Request{Items: items})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if got := checker.calls(); got != 2 {
		t.Fatalf("checker calls = %d, want 2 (one per unique link)", got)
	}
	if len(response.Results) != 4 {
		t.Fatalf("results length = %d, want 4", len(response.Results))
	}
	// Every position, including duplicates, must be filled with the same result.
	for i, r := range response.Results {
		if r.State != StateOK {
			t.Fatalf("result[%d] state = %q, want ok", i, r.State)
		}
	}
	if response.Results[0].URL != response.Results[2].URL {
		t.Fatalf("duplicate positions should share the broadcast result")
	}
}

func TestServiceCachesDefinitiveResultsAcrossChecks(t *testing.T) {
	resetCacheForTest()
	checker := &countingChecker{}
	service := NewService(Options{Checker: checker, CacheTTL: time.Minute})
	item := Item{DiskType: "quark", URL: "https://pan.quark.cn/s/cached"}

	if _, err := service.Check(context.Background(), Request{Items: []Item{item}}); err != nil {
		t.Fatalf("first Check returned error: %v", err)
	}
	if got := checker.calls(); got != 1 {
		t.Fatalf("checker calls after first check = %d, want 1", got)
	}

	// Second check for the same link must be served from cache.
	if _, err := service.Check(context.Background(), Request{Items: []Item{item}}); err != nil {
		t.Fatalf("second Check returned error: %v", err)
	}
	if got := checker.calls(); got != 1 {
		t.Fatalf("checker calls after cached check = %d, want 1 (served from cache)", got)
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

type countingChecker struct {
	mu    sync.Mutex
	count int
}

func (c *countingChecker) Check(ctx context.Context, item Item) Result {
	c.mu.Lock()
	c.count++
	c.mu.Unlock()
	return Result{DiskType: item.DiskType, URL: item.URL, State: StateOK, Summary: "链接有效"}
}

func (c *countingChecker) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// latencyChecker simulates a fixed per-link network round-trip without real I/O.
type latencyChecker struct {
	delay time.Duration
}

func (l *latencyChecker) Check(ctx context.Context, item Item) Result {
	select {
	case <-time.After(l.delay):
	case <-ctx.Done():
		return Result{DiskType: item.DiskType, URL: item.URL, State: StateUncertain, Summary: "检测超时"}
	}
	return Result{DiskType: item.DiskType, URL: item.URL, State: StateOK, Summary: "链接有效"}
}

// BenchmarkServiceCheck300Links shows the flat worker pool completes 300 links in
// roughly ceil(300/concurrency) waves — well under the 1s target at a realistic
// ~60ms per-link latency. Run: go test ./internal/linkcheck/ -bench=300Links -benchtime=5x
func BenchmarkServiceCheck300Links(b *testing.B) {
	items := make([]Item, 300)
	for i := range items {
		items[i] = Item{DiskType: "quark", URL: fmt.Sprintf("https://pan.quark.cn/s/bench-%03d", i)}
	}
	service := NewService(Options{Checker: &latencyChecker{delay: 60 * time.Millisecond}, Concurrency: 128})
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetCacheForTest()
		if _, err := service.Check(ctx, Request{Items: items, Timeout: 5 * time.Second}); err != nil {
			b.Fatalf("Check: %v", err)
		}
	}
}
