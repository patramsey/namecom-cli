# Mitigations for the `core-api-go` retry defects — evaluated, not adopted

**Nothing in this repository works around these defects.** This file records
what a mitigation would look like and what it was measured to cost, so the
information is available to the #40 decision and to the upstream reports
without anyone having to rediscover it.

The defects themselves are filed upstream and drafted in this directory:

| # | Defect | Upstream |
|---|---|---|
| 1 | `option.WithoutRetries()` is silently ignored at client scope | [#3](https://github.com/namedotcom/core-api-go/issues/3) · [draft](core-api-go-withoutretries-ignored.md) |
| 2 | Retry backoff sleeps with a bare `time.Sleep`, ignoring the request context | [#4](https://github.com/namedotcom/core-api-go/issues/4) · [draft](core-api-go-backoff-ignores-context.md) |

## They are not one problem

Worth recording, because the obvious simplification is wrong and the reasoning
is not visible from the API surface.

Disabling retries does **not** skip the sleep. `Retrier.run` sleeps *before* it
checks the attempt counter:

```go
if r.shouldRetry(response) {
    delay, _ := r.retryDelay(response, retryAttempt)
    time.Sleep(delay)                       // happens first
    return r.run(..., retryAttempt+1, ...)  // counter checked on entry here
}
```

So a call with retries disabled still issues one request, waits out the server's
full `Retry-After`, and only then declines to retry. Measured against a 429
carrying `Retry-After: 30`: one request, thirty seconds.

Any mitigation therefore needs two parts. Treating defect 2 as a consequence of
defect 1 leaves the wait in place, where it presents as a hang rather than
as a bug.

## Mitigation for defect 1 — pass the option per call

`option.WithoutRetries()` works per call; only the client-scoped form is
ignored. Measured against a 500:

| wiring | requests |
|---|---|
| no option | 2 |
| `WithoutRetries()` on the client | 2 |
| `WithoutRetries()` per call | 1 |

**Cost.** The option has to be repeated at every call site, and forgetting it
silently restores POST-on-5xx retries against endpoints that do not honour
idempotency keys — `CreateDomain`, `RenewDomain`, `ProcessRefund`. Convention
will not hold across ~53 call sites, so it would have to be enforced by
construction: a wrapper type keeping the SDK client unexported and exposing only
methods that append the option. That is roughly 53 hand-written pass-through
methods to maintain, and one more for every operation added upstream.

This is the real cost to weigh in #40 — not the option itself, which is one line,
but the obligation to carry it across the whole API surface for as long as the
defect stands.

## Mitigation for defect 2 — strip `Retry-After` before the SDK sees it

Deleting the header from responses on their way back to the SDK makes
`retryDelay` fall through to its own `minRetryDelay`. Measured against a 429
carrying `Retry-After: 30`, with retries already disabled per call:

| response header | elapsed | requests | error |
|---|---|---|---|
| `Retry-After: 30` intact | 30s | 1 | `*core.APIError`, status 429 |
| `Retry-After` stripped | ~1s | 1 | `*core.APIError`, status 429 |

**Why it would be safe.** By the time a response passes back through our
transport, `retryTransport` has already read `Retry-After`, made its retry
decision, and either waited on a context-aware timer or declined to wait at all
(the deadline guard from #41). The header has no remaining consumer in-process.
It would have to be scoped to the client handed to the SDK and not to the shared
transport, so `namecom api` and anything else reading raw responses still sees
what the server sent.

**Cost.** Roughly one second of dead sleep would remain on every terminal 429 or
5xx — the SDK's own `minRetryDelay`, unreachable from outside the package. No
extra request and no lost status, but latency on the error path that does not
exist on the current generated client.

## What this means for #40

Both mitigations work and were measured. Neither is adopted, and the preference
is that upstream fixes the defects instead — the suggested patches in both
drafts were verified against a local copy of v1.33.2 and are small. Both are now
filed as [#3](https://github.com/namedotcom/core-api-go/issues/3) and
[#4](https://github.com/namedotcom/core-api-go/issues/4).

If the migration proceeds before a fix lands, mitigation 1 is not optional: it
guards the money endpoints. Mitigation 2 is a latency question and could be
skipped, at the cost of a call occasionally appearing to hang for up to the
SDK's 60s `maxRetryDelay`.

## Reproducing the measurements

The two issue drafts each carry a self-contained `main.go` that reproduces the
defect and prints the numbers above. Both were run against v1.33.2; the
suggested fixes were then applied to a local copy and the programs re-run to
confirm the fixes work and that `go test ./internal/...` still passes upstream.

The defect-demonstration tests in `internal/sdkspike` cover the same ground as
part of the #40 spike, and are written to fail if a defect is ever fixed:

| Test | Fires when |
|---|---|
| `TestWithoutRetriesHoldsAtClientScope/client-scoped_WithoutRetries_is_silently_ignored` | defect 1 is fixed |
| `TestSDKBackoffIgnoresContext` | defect 2 is fixed |
