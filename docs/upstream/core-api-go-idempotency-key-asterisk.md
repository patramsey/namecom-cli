# `option.WithXIdempotencyKey` sends the key with a literal `*` prefix

**Filed 2026-08-26 as [namedotcom/core-api-go#5](https://github.com/namedotcom/core-api-go/issues/5). Fixed in v1.33.5:** the header is now built with `fmt.Sprintf("%v", …)` instead of `fmt.Sprintf("*%v", …)`. This repository still sets the header in `internal/api/headers.go` rather than using the option, which remains the simpler arrangement. Found while porting `namecom` to the SDK (#40). Kept here so the reproduction and the measurements stay with the repository that produced them.

**Version:** v1.33.3 · **Go:** 1.26.6

## Summary

`core/idempotent_request_option.go:56`:

```go
func (i *IdempotentRequestOptions) ToHeader() http.Header {
	header := i.RequestOptions.ToHeader()
	if i.XIdempotencyKey != nil {
		header.Set("X-Idempotency-Key", fmt.Sprintf("*%v", *i.XIdempotencyKey))
	}
	return header
}
```

The `*` is the Go dereference operator that belongs in the *expression*, and it
has ended up inside the *format string* as well. So

```go
option.WithXIdempotencyKey(&key)   // key == "USER-SUPPLIED-KEY"
```

sends

```
X-Idempotency-Key: *USER-SUPPLIED-KEY
```

Observed directly: a test asserting the caller's key reaches the wire failed
with `sent "*USER-SUPPLIED-KEY"`.

## Why it matters

Idempotency keys are opaque strings compared by equality, so this does not
error — it silently changes the key. Two consequences:

- **A retry does not deduplicate against the original.** If one request is sent
  through this option and another by any other means with the same key, the
  server sees two different keys. On the name.com Core API the five operations
  that honour the header include `CreateDomain` and `ProcessRefund`, so the
  failure mode is a double purchase or a double refund — exactly what the header
  exists to prevent.
- **A key the caller chose for correlation no longer matches their records.**

Nothing warns. The request succeeds.

## Suggested fix

```go
-		header.Set("X-Idempotency-Key", fmt.Sprintf("*%v", *i.XIdempotencyKey))
+		header.Set("X-Idempotency-Key", *i.XIdempotencyKey)
```

Worth checking whether the same `"*%v"` pattern appears for other pointer-typed
header or query options in the generator's templates; this one was found by
asserting the byte that reached the wire rather than by reading the code.

## Note on where the fix belongs

Fern-generated, so the durable fix is in the generator template rather than in a
patch to the Go source.

## What this repository does about it

Nothing needs working around. `namecom` sets `X-Idempotency-Key` in its own
transport (`internal/api/headers.go`) for every write, and that path never uses
this option — the SDK client is constructed without credentials or key handling
precisely so one implementation owns those headers.

The only place the option appeared was a test, which now sets the header through
`option.WithHTTPHeader` instead. If this repository ever does reach for
`WithXIdempotencyKey`, the key silently changes and the guard against double
purchases stops working, so: do not.
