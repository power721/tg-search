package linkcheck

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	StateOK          = "ok"
	StateBad         = "bad"
	StateLocked      = "locked"
	StateUnsupported = "unsupported"
	StateUncertain   = "uncertain"
	StateRateLimited = "rate_limited" // 网盘风控/频控拦截,链接状态未知,不可当失效

	DefaultTimeout     = 5 * time.Second
	DefaultConcurrency = 128 // flat worker-pool size; HTTP/2 multiplexing keeps connection count low
)

var (
	ErrItemsRequired = errors.New("items is required")
)

type Item struct {
	DiskType string `json:"disk_type"`
	URL      string `json:"url"`
	Password string `json:"password,omitempty"`
}

type Request struct {
	Items   []Item
	Timeout time.Duration
}

type Result struct {
	DiskType  string `json:"disk_type"`
	URL       string `json:"url"`
	Password  string `json:"password,omitempty"`
	State     string `json:"state"`
	Summary   string `json:"summary,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

type Response struct {
	Results   []Result            `json:"results"`
	Grouped   map[string][]Result `json:"grouped"`
	TimeoutMS int64               `json:"timeout_ms"`
}

type Checker interface {
	Check(context.Context, Item) Result
}

type Options struct {
	Checker     Checker
	Concurrency int
	CacheTTL    time.Duration
}

type Service struct {
	checker     Checker
	concurrency int
	cacheTTL    time.Duration
}

func NewService(options Options) *Service {
	checker := options.Checker
	if checker == nil {
		checker = NewHTTPChecker(nil)
	}
	concurrency := options.Concurrency
	if concurrency < 1 {
		concurrency = DefaultConcurrency
	}
	cacheTTL := options.CacheTTL
	if cacheTTL < 0 {
		cacheTTL = 0 // negative clamped to 0; a CacheTTL of 0 disables memoization
	}
	return &Service{checker: checker, concurrency: concurrency, cacheTTL: cacheTTL}
}

func (s *Service) Check(ctx context.Context, req Request) (Response, error) {
	items := normalizeItems(req.Items)
	if len(items) == 0 {
		return Response{}, ErrItemsRequired
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Dedup by (disk_type|url|password): identical links are checked once and
	// the result is broadcast to every original position.
	keyOrder := make([]string, 0, len(items))
	keyItem := make(map[string]Item, len(items))
	keyIndices := make(map[string][]int, len(items))
	for index, item := range items {
		key := cacheKey(item)
		if _, ok := keyItem[key]; !ok {
			keyOrder = append(keyOrder, key)
			keyItem[key] = item
		}
		keyIndices[key] = append(keyIndices[key], index)
	}

	results := make([]Result, len(items))
	completed := make([]bool, len(items))

	// Flat bounded worker pool. Each unique key runs in its own goroutine;
	// different keys write disjoint index sets, so no locking is needed.
	sem := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup
	for _, key := range keyOrder {
		item := keyItem[key]
		indices := keyIndices[key]
		wg.Add(1)
		go func(item Item, indices []int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			result := s.checkKey(ctx, item)
			for _, index := range indices {
				results[index] = result
				completed[index] = true
			}
		}(item, indices)
	}
	wg.Wait()

	for index, item := range items {
		if !completed[index] {
			results[index] = Result{
				DiskType: item.DiskType,
				URL:      item.URL,
				Password: item.Password,
				State:    StateUncertain,
				Summary:  "检测超时",
			}
		}
		if strings.TrimSpace(results[index].State) == "" {
			results[index].State = StateUncertain
		}
	}

	return Response{
		Results:   results,
		Grouped:   groupResults(results),
		TimeoutMS: timeout.Milliseconds(),
	}, nil
}

// checkKey resolves a single (deduplicated) link, consulting the process-level
// cache first so repeated checks return instantly without hitting the network.
func (s *Service) checkKey(ctx context.Context, item Item) Result {
	key := cacheKey(item)
	if cached, ok := globalCache.get(key); ok {
		return cached
	}

	result := s.checker.Check(ctx, item)
	result.DiskType = item.DiskType
	result.URL = item.URL
	result.Password = item.Password
	if strings.TrimSpace(result.State) == "" {
		result.State = StateUncertain
	}
	globalCache.set(key, result, s.cacheTTL)
	return result
}

func cacheKey(item Item) string {
	return strings.Join([]string{item.DiskType, item.URL, item.Password}, "|")
}

func normalizeItems(items []Item) []Item {
	out := make([]Item, 0, len(items))
	for _, item := range items {
		item.DiskType = strings.ToLower(strings.TrimSpace(item.DiskType))
		item.URL = strings.TrimSpace(item.URL)
		item.Password = strings.TrimSpace(item.Password)
		if item.DiskType == "" || item.URL == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func groupResults(results []Result) map[string][]Result {
	grouped := make(map[string][]Result)
	for _, result := range results {
		grouped[result.DiskType] = append(grouped[result.DiskType], result)
	}
	return grouped
}

// cacheable reports whether a result state is definitive enough to memoize.
// Uncertain/rate-limited/unsupported results depend on transient conditions and are never cached.
func cacheable(state string) bool {
	switch state {
	case StateOK, StateBad, StateLocked:
		return true
	default:
		return false
	}
}

type cacheEntry struct {
	result    Result
	expiresAt time.Time
}

// resultCache is a process-wide TTL cache of definitive link-check results.
// A background sweeper reclaims expired entries so the map cannot grow unbounded.
type resultCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

var globalCache = newResultCache()

func newResultCache() *resultCache {
	return &resultCache{entries: make(map[string]cacheEntry)}
}

func (c *resultCache) get(key string) (Result, bool) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return Result{}, false
	}
	if now.After(entry.expiresAt) {
		delete(c.entries, key)
		return Result{}, false
	}
	return entry.result, true
}

func (c *resultCache) set(key string, result Result, ttl time.Duration) {
	if ttl <= 0 || !cacheable(result.State) {
		return
	}
	now := time.Now()
	c.mu.Lock()
	c.entries[key] = cacheEntry{result: result, expiresAt: now.Add(ttl)}
	if len(c.entries) > 4096 {
		c.sweepLocked(now)
	}
	c.mu.Unlock()
	startCacheSweeperOnce()
}

func (c *resultCache) sweepLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}

var cacheSweeperOnce sync.Once

func startCacheSweeperOnce() {
	cacheSweeperOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for now := range ticker.C {
				globalCache.mu.Lock()
				globalCache.sweepLocked(now)
				globalCache.mu.Unlock()
			}
		}()
	})
}

// resetCacheForTest clears the process-wide cache; intended only for tests.
func resetCacheForTest() {
	globalCache.mu.Lock()
	globalCache.entries = make(map[string]cacheEntry)
	globalCache.mu.Unlock()
}
