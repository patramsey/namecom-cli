package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

// TestFetchEach keeps each result and error at its item's index, whatever
// order the reads finish in, fetches every item even after a failure, and
// never has more than fetchConcurrency in flight.
func TestFetchEach(t *testing.T) {
	items := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	var inFlight, most, calls atomic.Int32
	results, errs := FetchEach(context.Background(), items, func(_ context.Context, n int) (string, error) {
		calls.Add(1)
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := most.Load()
			if now <= m || most.CompareAndSwap(m, now) {
				break
			}
		}
		if n%4 == 1 {
			return "", fmt.Errorf("item %d failed", n)
		}
		return fmt.Sprint("item ", n), nil
	})
	if got := calls.Load(); got != int32(len(items)) {
		t.Errorf("fetched %d items, want all %d", got, len(items))
	}
	if got := most.Load(); got > fetchConcurrency {
		t.Errorf("%d reads in flight at once, want at most %d", got, fetchConcurrency)
	}
	for i := range items {
		if i%4 == 1 {
			if errs[i] == nil || errs[i].Error() != fmt.Sprintf("item %d failed", i) {
				t.Errorf("errs[%d] = %v, want item %d's error", i, errs[i], i)
			}
			continue
		}
		if errs[i] != nil || results[i] != fmt.Sprint("item ", i) {
			t.Errorf("item %d: got (%q, %v), want its own result", i, results[i], errs[i])
		}
	}
}

// TestFetchEach_One reads a lone item inline.
func TestFetchEach_One(t *testing.T) {
	want := errors.New("nope")
	_, errs := FetchEach(context.Background(), []string{"a"}, func(context.Context, string) (int, error) { return 0, want })
	if !errors.Is(errs[0], want) {
		t.Errorf("errs[0] = %v, want %v", errs[0], want)
	}
}
