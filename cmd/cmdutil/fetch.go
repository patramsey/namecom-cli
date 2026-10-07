package cmdutil

import (
	"context"
	"sync"
)

// fetchConcurrency is how many reads FetchEach has in flight at once: the
// rate limiter's burst, so none of them waits on it to start.
const fetchConcurrency = 5

// FetchEach reads one thing per item — a domain, a record — several at a
// time, and returns each result at its item's index. Commands that take
// several targets read them all before writing any; one at a time, ten
// domains cost ten round trips of waiting (#294).
//
// The first fetch to fail ends the walk, and its error is returned: no item
// after it is started, and the reads in flight are cancelled. Every caller
// gives up on the first failure, so reading the rest was wasted — for
// `domain list -q | domain get -` with the first name missing, thousands of
// requests whose results were thrown away. At most fetchConcurrency reads are
// spent after it. When several fail at once, the one reported is the first to
// come back, not the first in the order given. A fetch that wants to carry on
// past an item, as `dns delete --if-exists` does past a missing record,
// returns no error for it.
//
// Only reads belong here: nothing is retried or ordered, and a write's order
// matters.
func FetchEach[T, R any](ctx context.Context, items []T, fetch func(ctx context.Context, item T) (R, error)) ([]R, error) {
	results := make([]R, len(items))
	if len(items) == 1 {
		var err error
		results[0], err = fetch(ctx, items[0])
		return results, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, fetchConcurrency)
	var wg sync.WaitGroup
	var once sync.Once
	var first, stopped error
	for i, item := range items {
		sem <- struct{}{}
		// A failure cancels before it frees its slot, so the item that
		// takes the slot sees it and is never started.
		if stopped = ctx.Err(); stopped != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			r, err := fetch(ctx, item)
			if err != nil {
				once.Do(func() { first = err; cancel() })
				return
			}
			results[i] = r
		}()
	}
	wg.Wait()
	if first == nil {
		// Nothing failed, but the caller's context ended the walk early.
		first = stopped
	}
	return results, first
}
