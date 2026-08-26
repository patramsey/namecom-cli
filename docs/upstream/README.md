# Upstream defect reports

Bug reports written against dependencies, with their reproductions. They live
here so the analysis is not lost once the issue is filed — the work of
reproducing a defect and verifying a fix is worth more than the tracker entry
that summarises it, and trackers outlive neither the reasoning nor the numbers.

Each carries a banner naming the filed issue. The body stays put rather than
being replaced by a link.

| Report | Against | Filed |
|---|---|---|
| [`core-api-go-withoutretries-ignored.md`](core-api-go-withoutretries-ignored.md) | `namedotcom/core-api-go` v1.33.2 | [#3](https://github.com/namedotcom/core-api-go/issues/3) |
| [`core-api-go-backoff-ignores-context.md`](core-api-go-backoff-ignores-context.md) | `namedotcom/core-api-go` v1.33.2 | [#4](https://github.com/namedotcom/core-api-go/issues/4) |
| [`core-api-go-urlforwarding-host-required.md`](core-api-go-urlforwarding-host-required.md) | `namedotcom/core-api-go` v1.33.3 | not filed |
| [`core-api-go-forced-request-bodies.md`](core-api-go-forced-request-bodies.md) | `namedotcom/core-api-go` v1.33.3 | not filed |
| [`core-api-go-updatedomain-union.md`](core-api-go-updatedomain-union.md) | `namedotcom/core-api-go` v1.33.3 | not filed |

Neither is worked around in this repository. What a mitigation would look like,
and what each was measured to cost, is recorded in
[`core-api-go-mitigations.md`](core-api-go-mitigations.md).

## Status

**Both defects were fixed upstream in v1.33.3 (2026-08-25). The hold is lifted.**

Verified by measurement, not by reading the release: the two
defect-demonstration tests written during the #40 spike are constructed to fail
once the defects are gone, and both now fail against v1.33.3.

| | v1.33.2 | v1.33.3 |
|---|---|---|
| client-scoped `WithoutRetries()`, one 500 | 2 requests | **1 request** |
| 100ms deadline against `Retry-After: 2` | 2.001s | **100.8ms** |

The upstream patches match what these reports proposed — `Retrier` now carries
`disabled` and `Run` honours either source; the bare `time.Sleep` is now a
`sleepWithContext` selecting on `ctx.Done()`. One refinement was added that this
report did not anticipate: a request-scoped `attempts > 0` resets `disabled` to
false, so a per-call attempt count deliberately overrides a client-scoped
opt-out.

**Issues #3 and #4 are still open upstream with no comment.** The fixes shipped
without acknowledgement, so nothing in the tracker signals this. Left to
upstream to comment and close.

### What this means for #40

The gate is gone. #3 was the blocker: with client-scoped `WithoutRetries()`
ignored, the SDK retried POSTs on 5xx against endpoints that do not honour
idempotency keys, and the only fallback was repeating the option across ~53 call
sites where forgetting it fails silently. That is now a single client-scoped
option that holds.

What remains is the ordinary migration tradeoff, not a safety hazard. The other
spike findings are unaffected — they are spec-derived (Fern renames, the type
gotchas, `*int` vs `*int32` paging) rather than retry-related.

Nothing here blocks the CLI. The vendored spec and generated client keep working
and stay in place. Any migration should re-run the spike against v1.33.3 first:
it was evaluated at v1.33.2, and the retry layer is precisely the part that
changed.
