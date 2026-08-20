# Workarounds for `namedotcom/core-api-go`

Two defects in `core-api-go` v1.33.2 are worked around in `internal/sdkspike`.
Neither report is filed yet; both are drafted in this directory.

**The intent is that these are temporary.** Everything below is arranged so that
the day upstream fixes a defect, the test suite says so and the removal is
mechanical.

## The two defects

| # | Defect | Draft | Worked around in |
|---|---|---|---|
| 1 | `option.WithoutRetries()` is silently ignored at client scope | [`core-api-go-withoutretries-ignored.md`](core-api-go-withoutretries-ignored.md) | `safeCallOptions` + the `Client` wrapper |
| 2 | Retry backoff sleeps with a bare `time.Sleep`, ignoring the request context | [`core-api-go-backoff-ignores-context.md`](core-api-go-backoff-ignores-context.md) | `stripRetryAfter` + `sdkHTTPClient` |

## They are not one problem

This is the part worth reading before touching either workaround, because the
obvious simplification is wrong.

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

Removing workaround 2 because workaround 1 "already covers it" reintroduces
that, and it will look like a hang rather than a bug.

## What each one costs

Measured against a stub; see the tests named below.

| Scenario | Unguarded | Guarded |
|---|---|---|
| POST against a 5xx | 2 requests | 1 request |
| 429 with `Retry-After: 30` | 30s | ~1s |
| Error typing | `*core.APIError`, status 429 | unchanged |

### Residue

**Roughly one second of dead sleep remains on every terminal 429 or 5xx.** That
is the SDK's own `minRetryDelay`, reached after `Retry-After` is stripped, and
it cannot be removed from outside the package. It is latency on the error path
only — no extra request, no lost status — but it is a real cost that arrives
with the SDK and does not exist on the current generated client.

## Why workaround 2 is safe

Deleting a response header is the kind of thing that should raise an eyebrow, so
the reasoning is here rather than only in the code.

`stripRetryAfter` sits on the client handed to the SDK, *above* our own
transport. By the time a response reaches it, `retryTransport` has already read
`Retry-After`, made its retry decision, and either waited on a context-aware
timer or declined to wait at all (see the deadline guard added in #41). The
header has no remaining consumer in-process — except the SDK's dead sleep.

It is scoped deliberately:

- Applied to the client passed to `sdk.NewNamecom`.
- **Not** applied to the caller's own `*http.Client`. `sdkHTTPClient` copies the
  client before swapping its transport, so `namecom api` and anything else
  reading raw responses still sees the header the server sent.

`TestStripIsScopedToTheSDKClient` fails if that copy is ever removed.

## Why workaround 1 is a wrapper and not a convention

`option.WithoutRetries()` has to be repeated on every call. An option that must
be remembered at ~53 call sites is one that will eventually be forgotten, and
forgetting it silently restores POST-on-5xx retries against endpoints that do
not honour idempotency keys — `CreateDomain`, `RenewDomain`, `ProcessRefund`.

So `Client` keeps `*sdk.Namecom` unexported and exposes only wrapped methods.
There is no way to issue a request that skips `safeCallOptions` without editing
`workaround.go`. Adding an operation means adding a method, which is the moment
to notice.

## How you will find out upstream has fixed it

Two tests assert the *defects*, not the workarounds, so they fail when the
defects go away:

| Test | Fires when |
|---|---|
| `TestWithoutRetriesHoldsAtClientScope/client-scoped_WithoutRetries_is_silently_ignored` | defect 1 is fixed |
| `TestSDKBackoffIgnoresContext` | defect 2 is fixed |

Both were verified against a locally patched copy of v1.33.2 carrying the fixes
suggested in the drafts. They fail with messages that say what to do:

```
client-scoped WithoutRetries now holds (server saw 1 request) —
the SDK has been fixed; drop the per-call workaround and update issue #40
```

```
call took 102ms; expected the bare time.Sleep to overrun the deadline.
If the SDK has become context-aware, this finding is stale and the
recommendation should be revisited
```

The workaround tests keep passing under a fixed SDK — a fix makes them
redundant, not broken — so a dependency bump surfaces exactly these two and
nothing else.

## Removing them

### Defect 1 fixed upstream

1. Delete `safeCallOptions` and the wrapped methods on `Client`.
2. Either export the SDK client or collapse `Client` to a constructor; keep
   `option.WithoutRetries()` on `NewGuarded`, which now actually works.
3. Delete the `client-scoped ... is silently ignored` subtest.
4. Mark the draft filed-and-fixed in [`README.md`](README.md).

### Defect 2 fixed upstream

1. Delete `stripRetryAfter`, `sdkHTTPClient`, and
   `TestStripIsScopedToTheSDKClient`.
2. Pass the caller's `*http.Client` straight to `option.WithHTTPClient`.
3. Delete `TestSDKBackoffIgnoresContext` and `TestWorkaroundCapsDeadSleep`.
4. The ~1s residue on terminal errors goes away with it.

### Neither fixed, and the migration proceeds anyway

The wrapper generalises: every service package needs the same treatment, so
`Client` grows from 5 methods to roughly 53. That is mechanical but large, and
it is the cost that belongs in the #40 decision — not the workaround itself,
which is small, but the obligation to maintain it across the whole API surface.
