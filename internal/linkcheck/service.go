package linkcheck

import (
	"context"
	"errors"
	"fmt"
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

	DefaultTimeout = 5 * time.Second
	MaxPerDiskType = 10
)

var (
	ErrItemsRequired      = errors.New("items is required")
	ErrGroupLimitExceeded = errors.New("each disk_type group can contain at most 10 links")
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
	if err := validateGroupLimits(items); err != nil {
		return Response{}, err
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

	var wg sync.WaitGroup
	for index, item := range items {
		wg.Add(1)
		go func(index int, item Item) {
			defer wg.Done()
			result := s.checker.Check(ctx, item)
			result.DiskType = item.DiskType
			result.URL = item.URL
			result.Password = item.Password
			select {
			case resultCh <- indexedResult{index: index, result: result}:
			case <-ctx.Done():
			}
		}(index, item)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	remaining := len(items)
	for remaining > 0 {
		select {
		case item := <-resultCh:
			if !completed[item.index] {
				results[item.index] = item.result
				completed[item.index] = true
				remaining--
			}
		case <-done:
			remaining = 0
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

func validateGroupLimits(items []Item) error {
	counts := map[string]int{}
	for _, item := range items {
		counts[item.DiskType]++
		if counts[item.DiskType] > MaxPerDiskType {
			return fmt.Errorf("%w: %s", ErrGroupLimitExceeded, item.DiskType)
		}
	}
	return nil
}

func groupResults(results []Result) map[string][]Result {
	grouped := make(map[string][]Result)
	for _, result := range results {
		grouped[result.DiskType] = append(grouped[result.DiskType], result)
	}
	return grouped
}
