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

	DefaultTimeout       = 5 * time.Second
	BatchSize            = 10
	MaxConcurrentBatches = 5
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
	DiskType string `json:"disk_type"`
	URL      string `json:"url"`
	Password string `json:"password,omitempty"`
	State    string `json:"state"`
	Summary  string `json:"summary,omitempty"`
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
	Checker Checker
}

type Service struct {
	checker Checker
}

func NewService(options Options) *Service {
	checker := options.Checker
	if checker == nil {
		checker = NewHTTPChecker(nil)
	}
	return &Service{checker: checker}
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

	results := make([]Result, len(items))
	completed := make([]bool, len(items))
	resultCh := make(chan indexedResult, len(items))

	batches := buildBatches(items)
	batchSlots := make(chan struct{}, MaxConcurrentBatches)
	var wg sync.WaitGroup
	for _, batch := range batches {
		wg.Add(1)
		go func(batch []indexedItem) {
			defer wg.Done()
			select {
			case batchSlots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-batchSlots }()

			for _, entry := range batch {
				if ctx.Err() != nil {
					return
				}
				result := s.checker.Check(ctx, entry.item)
				result.DiskType = entry.item.DiskType
				result.URL = entry.item.URL
				result.Password = entry.item.Password
				select {
				case resultCh <- indexedResult{index: entry.index, result: result}:
				case <-ctx.Done():
					return
				}
			}
		}(batch)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	remaining := len(items)
	for remaining > 0 {
		select {
		case item, ok := <-resultCh:
			if !ok {
				remaining = 0
				continue
			}
			if !completed[item.index] {
				results[item.index] = item.result
				completed[item.index] = true
				remaining--
			}
		case <-ctx.Done():
			remaining = 0
		}
	}

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

type indexedResult struct {
	index  int
	result Result
}

type indexedItem struct {
	index int
	item  Item
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

func buildBatches(items []Item) [][]indexedItem {
	typeOrder := make([]string, 0)
	groups := map[string][]indexedItem{}
	for index, item := range items {
		if _, ok := groups[item.DiskType]; !ok {
			typeOrder = append(typeOrder, item.DiskType)
		}
		groups[item.DiskType] = append(groups[item.DiskType], indexedItem{index: index, item: item})
	}

	var batches [][]indexedItem
	for _, diskType := range typeOrder {
		group := groups[diskType]
		for start := 0; start < len(group); start += BatchSize {
			end := start + BatchSize
			if end > len(group) {
				end = len(group)
			}
			batches = append(batches, group[start:end])
		}
	}
	return batches
}

func groupResults(results []Result) map[string][]Result {
	grouped := make(map[string][]Result)
	for _, result := range results {
		grouped[result.DiskType] = append(grouped[result.DiskType], result)
	}
	return grouped
}
