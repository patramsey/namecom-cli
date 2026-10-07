package cmdutil

import (
	"context"
	"sync"
)

// fetchConcurrency is how many reads FetchEach has in flight at once: the
// rate limiter's burst, so none of them waits on it to start.
const fetchConcurrency = 5

// FetchEach reads one thing per item — a domain, a record — several at a
// time, and returns each result and error at its item's index. Commands that
// take several targets read them all before writing any; one at a time, ten
// domains cost ten round trips of waiting (#294).
//
// Every item is fetched, even after one fails, so the caller can still walk
// the results in order and act on the first failure exactly as it did when
// the reads were sequential. Only reads belong here: nothing is retried or
// ordered, and a write's order matters.
func FetchEach[T, R any](ctx context.Context, items []T, fetch func(ctx context.Context, item T) (R, error)) ([]R, []error) {
	results := make([]R, len(items))
	errs := make([]error, len(items))
	if len(items) == 1 {
		results[0], errs[0] = fetch(ctx, items[0])
		return results, errs
	}
	sem := make(chan struct{}, fetchConcurrency)
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			results[i], errs[i] = fetch(ctx, item)
		}()
	}
	wg.Wait()
	return results, errs
}
