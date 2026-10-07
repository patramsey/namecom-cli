package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

// TestFetchEach keeps each result at its item's index, whatever order the
// reads finish in, and never has more than fetchConcurrency in flight.
func TestFetchEach(t *testing.T) {
	items := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	var inFlight, most, calls atomic.Int32
	results, err := FetchEach(context.Background(), items, func(_ context.Context, n int) (string, error) {
		calls.Add(1)
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := most.Load()
			if now <= m || most.CompareAndSwap(m, now) {
				break
			}
		}
		return fmt.Sprint("item ", n), nil
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := calls.Load(); got != int32(len(items)) {
		t.Errorf("fetched %d items, want all %d", got, len(items))
	}
	if got := most.Load(); got > fetchConcurrency {
		t.Errorf("%d reads in flight at once, want at most %d", got, fetchConcurrency)
	}
	for i := range items {
		if results[i] != fmt.Sprint("item ", i) {
			t.Errorf("item %d: got %q, want its own result", i, results[i])
		}
	}
}

// TestFetchEach_StopsAtFirstFailure: every caller gives up on the first
// failure, yet every item was still read (#312) — `domain list -q |
// domain get -` with the first name missing read all 6,500 to print one
// error. The failure cancels the reads in flight and starts no more: here
// item 0 fails once items 0–4 are all in flight, so exactly those five are
// fetched, the other four see their context cancelled, and item 0's error is
// the one returned.
func TestFetchEach_StopsAtFirstFailure(t *testing.T) {
	items := make([]int, 100)
	for i := range items {
		items[i] = i
	}
	want := errors.New("item 0 failed")
	var calls, cancelled atomic.Int32
	started := make(chan struct{}, len(items))
	_, err := FetchEach(context.Background(), items, func(ctx context.Context, n int) (string, error) {
		calls.Add(1)
		started <- struct{}{}
		if n == 0 {
			for range fetchConcurrency {
				<-started
			}
			return "", want
		}
		<-ctx.Done()
		cancelled.Add(1)
		return "", ctx.Err()
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if got := calls.Load(); got != fetchConcurrency {
		t.Errorf("fetched %d items, want the %d in flight when item 0 failed", got, fetchConcurrency)
	}
	if got := cancelled.Load(); got != fetchConcurrency-1 {
		t.Errorf("%d reads cancelled, want %d", got, fetchConcurrency-1)
	}
}

// TestFetchEach_One reads a lone item inline.
func TestFetchEach_One(t *testing.T) {
	want := errors.New("nope")
	_, err := FetchEach(context.Background(), []string{"a"}, func(context.Context, string) (int, error) { return 0, want })
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

// TestFetchEach_CallerCancelled: a context already ended starts nothing and
// says so, rather than returning empty results as if they were read.
func TestFetchEach_CallerCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	_, err := FetchEach(ctx, []int{1, 2, 3}, func(context.Context, int) (int, error) {
		calls.Add(1)
		return 0, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("fetched %d items, want none", got)
	}
}
