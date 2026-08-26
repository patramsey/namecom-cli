# `option.WithoutRetries()` is silently ignored when set on the client

**Filed 2026-08-20 as [namedotcom/core-api-go#3](https://github.com/namedotcom/core-api-go/issues/3), fixed upstream in v1.33.3, closed 2026-08-26.** The suggested fix below was adopted almost verbatim, plus a refinement: a request-scoped `attempts > 0` now resets `disabled` to false. Kept here so the reproduction and the measurements stay with the repository that produced them.


**Version:** v1.33.2 · **Go:** 1.26.6

## Summary

`option.WithoutRetries()` has no effect when passed to `client.NewNamecom(...)`.
Requests are still retried with the default 2 attempts. The same option passed
per call works correctly.

This is the option that suppresses retrying non-idempotent requests, so an
option that appears to disable that behaviour and silently does not is worse
than one that does not exist.

## Root cause

`internal/retrier.go:58` — `NewRetrier` builds the options struct, reads
`attempts` out of it, and drops `disabled`:

```go
func NewRetrier(opts ...RetryOption) *Retrier {
	options := new(retryOptions)
	for _, opt := range opts {
		opt(options)
	}
	attempts := uint(defaultRetryAttempts)
	if options.attempts > 0 {
		attempts = options.attempts
	}
	return &Retrier{
		attempts: attempts,   // options.disabled is discarded
	}
}
```

`retryOptions` carries both fields (`internal/retrier.go:265`), but `Retrier`
has only `attempts` (`internal/retrier.go:53`), so there is nowhere to keep it.

`Run` then honours `disabled` only from the per-call options
(`internal/retrier.go:90`):

```go
maxRetryAttempts := r.attempts
if options.attempts > 0 {
	maxRetryAttempts = options.attempts
}
if options.disabled {
	maxRetryAttempts = 1
}
```

Each generated method rebuilds its options from scratch — e.g.
`dns/raw_client.go`: `options := core.NewRequestOptions(opts...)` — and passes
`DisableRetries: options.DisableRetries` into `CallParams`. With no per-call
options that value is `false`, so `buildRetryOptions(0, false)` returns an empty
slice and the client-scoped intent never reaches `Run`.

Note the asymmetry: `caller.Call` *does* fall back to the client-scoped HTTP
client (`client := c.client; if params.Client != nil { client = params.Client }`),
so `WithHTTPClient` behaves as expected at client scope. Only the retry settings
fail to.

## Reproduction

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	coreapigo "github.com/namedotcom/core-api-go"
	sdk "github.com/namedotcom/core-api-go/client"
	"github.com/namedotcom/core-api-go/option"
)

func main() {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer srv.Close()

	count := func(label string, opts ...option.RequestOption) {
		atomic.StoreInt32(&calls, 0)
		c := sdk.NewNamecom(append([]option.RequestOption{
			option.WithBaseURL(srv.URL),
			option.WithBasicAuth("u", "t"),
		}, opts...)...)
		_, _ = c.DNS.ListRecords(context.Background(),
			&coreapigo.ListRecordsRequest{DomainName: "example.com"})
		fmt.Printf("%-32s requests=%d\n", label, atomic.LoadInt32(&calls))
	}

	count("no option")
	count("client-scoped WithoutRetries", option.WithoutRetries())

	// Per-call, for contrast.
	atomic.StoreInt32(&calls, 0)
	c := sdk.NewNamecom(option.WithBaseURL(srv.URL), option.WithBasicAuth("u", "t"))
	_, _ = c.DNS.ListRecords(context.Background(),
		&coreapigo.ListRecordsRequest{DomainName: "example.com"},
		option.WithoutRetries())
	fmt.Printf("%-32s requests=%d\n", "per-call WithoutRetries", atomic.LoadInt32(&calls))
}
```

### Actual

```
no option                        requests=2
client-scoped WithoutRetries     requests=2
per-call WithoutRetries          requests=1
```

### Expected

```
no option                        requests=2
client-scoped WithoutRetries     requests=1
per-call WithoutRetries          requests=1
```

## Why this matters for this API specifically

`shouldRetry` (`internal/retrier.go:168`) dispatches on status code alone, with
no method check, so a **POST is retried on a 5xx**:

```go
func (r *Retrier) shouldRetry(response *http.Response) bool {
	return response.StatusCode == http.StatusTooManyRequests ||
		response.StatusCode == http.StatusRequestTimeout ||
		response.StatusCode >= http.StatusInternalServerError
}
```

Confirmed against a stub: a `CreateRecord` POST was sent twice on a 500.

On the name.com Core API only five operations declare `X-Idempotency-Key` —
`CreateDomain`, `PurchasePrivacy`, `ProcessRefund`, `VerifyContact`,
`ResendContactVerificationEmail`. Record creation is not among them, and I
verified in sandbox that `POST /core/v1/domains/{domain}/records` does not
deduplicate on the header: two posts under one key produced two records with
distinct IDs, and an identical body under the same key returned
`400 Parameter Value Error - Record already exists` rather than replaying the
original 200.

So for a caller that wants to opt out of retrying unsafe methods, the
client-scoped option is the natural place to do it, and it is exactly the place
where it does not work.

## Suggested fix

Carry `disabled` on the `Retrier` and honour either source in `Run`:

```go
 type Retrier struct {
 	attempts uint
+	disabled bool
 }

 func NewRetrier(opts ...RetryOption) *Retrier {
 	...
 	return &Retrier{
 		attempts: attempts,
+		disabled: options.disabled,
 	}
 }

 func (r *Retrier) Run(...) (*http.Response, error) {
 	...
-	if options.disabled {
+	if options.disabled || r.disabled {
 		maxRetryAttempts = 1
 	}
```

`WithMaxAttempts` at client scope already works, since `attempts` is retained —
this brings `disabled` in line with it.

**Verified.** With that patch applied to a local copy of v1.33.2, the
reproduction above prints the expected output:

```
no option                        requests=2
client-scoped WithoutRetries     requests=1
per-call WithoutRetries          requests=1
```

`go test ./internal/...` still passes against the patched copy.

## Note on where the fix belongs

`.fernignore` exempts only `.fern/replay.lock`, `.fern/replay.yml`, and
`.gitattributes`, so `internal/retrier.go` is regenerated. A patch landed here
would be overwritten on the next Fern run unless the file is added to
`.fernignore`. The durable fix is likely in the Fern Go generator template — 
happy to open a PR here if you would rather carry it via `.fernignore`, but
flagging it so this does not bounce.

## Workaround

Pass `option.WithoutRetries()` on every call. It works, but it has to be
repeated at every call site and anything added later that omits it silently
regains the behaviour.
