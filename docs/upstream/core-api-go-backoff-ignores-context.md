# Retry backoff ignores the request context

**Filed 2026-08-20 as [namedotcom/core-api-go#4](https://github.com/namedotcom/core-api-go/issues/4).** Kept here so the reproduction stays with the repository that produced it.


**Version:** v1.33.2 · **Go:** 1.26.6

## Summary

`Retrier.run` waits between attempts with a bare `time.Sleep`. It does not
select on `ctx.Done()`, so a cancelled or expired context does not interrupt the
wait. A call therefore outlives its own deadline, and `context.WithTimeout` —
along with anything built on it, such as a CLI `--timeout` flag — stops bounding
the call once a retry begins.

## Root cause

`internal/retrier.go:146`:

```go
if r.shouldRetry(response) {
	defer func() { _ = response.Body.Close() }()

	delay, err := r.retryDelay(response, retryAttempt)
	if err != nil {
		return nil, err
	}

	time.Sleep(delay)   // not context-aware
	...
}
```

The context is checked before each attempt (`internal/retrier.go:120`), so
cancellation is noticed *eventually* — but only after the full sleep has already
elapsed. With `maxRetryDelay = 60s` and `Retry-After` honoured up to that cap, a
single 429 can hold the call for a minute past its deadline.

## Reproduction

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	coreapigo "github.com/namedotcom/core-api-go"
	sdk "github.com/namedotcom/core-api-go/client"
	"github.com/namedotcom/core-api-go/option"
)

func main() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"slow down"}`))
	}))
	defer srv.Close()

	c := sdk.NewNamecom(
		option.WithBaseURL(srv.URL),
		option.WithBasicAuth("u", "t"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.DNS.ListRecords(ctx, &coreapigo.ListRecordsRequest{DomainName: "example.com"})
	fmt.Printf("elapsed=%s err=%v\n", time.Since(start).Round(time.Millisecond), err)
}
```

### Actual

```
elapsed=2.001s err=context deadline exceeded
```

### Expected

Roughly `elapsed=100ms`, the deadline the caller set.

## Consequences

- **A deadline stops meaning anything during backoff.** Anything mapping a
  user-facing timeout onto `context.WithTimeout` silently loses control of the
  call.
- **Cancellation is not honoured promptly.** `ctrl-C` wired to `cancel()` leaves
  the process sitting in `time.Sleep` for up to `maxRetryDelay`.
- **The error loses its cause.** The call above reports
  `context deadline exceeded` when what actually happened is a 429 the server
  already answered clearly. A caller that wants to surface "rate limited, retry
  after 2s" cannot, because the useful response was discarded in favour of a
  timeout produced by the SDK's own sleep.

## Suggested fix

Make the wait cancellable:

```go
-		time.Sleep(delay)
+		timer := time.NewTimer(delay)
+		select {
+		case <-request.Context().Done():
+			timer.Stop()
+			return nil, request.Context().Err()
+		case <-timer.C:
+		}
```

**Verified.** With that patch applied to a local copy of v1.33.2, the
reproduction above prints `elapsed=101ms err=context deadline exceeded`
instead of `elapsed=2.001s`. `go test ./internal/...` still passes against the
patched copy.

Optionally, and separately: when the remaining time on the deadline is shorter
than `delay`, returning the response rather than sleeping at all preserves the
server's answer instead of converting it into a timeout. That turns the example
above into a 429 the caller can act on.

## Note on where the fix belongs

`.fernignore` exempts only `.fern/replay.lock`, `.fern/replay.yml`, and
`.gitattributes`, so `internal/retrier.go` is regenerated and a patch here would
not survive the next Fern run. Flagging it in case this needs to go to the Fern
Go generator template instead.

## Related

Filed separately from the `option.WithoutRetries()` issue — different root
cause, different fix — though both are in `internal/retrier.go` and both affect
callers trying to bound or opt out of retry behaviour.
