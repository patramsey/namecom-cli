# Upstream defect reports

Bug reports written against dependencies, with their reproductions. They live
here so the analysis is not lost once the issue is filed — the work of
reproducing a defect and verifying a fix is worth more than the tracker entry
that summarises it, and trackers outlive neither the reasoning nor the numbers.

Each carries a banner naming the filed issue. The body stays put rather than
being replaced by a link.

| Report | Against | Filed |
|---|---|---|
| [`core-api-go-withoutretries-ignored.md`](core-api-go-withoutretries-ignored.md) | `namedotcom/core-api-go` v1.33.2 | [#3](https://github.com/namedotcom/core-api-go/issues/3) — **fixed in v1.33.3** |
| [`core-api-go-backoff-ignores-context.md`](core-api-go-backoff-ignores-context.md) | `namedotcom/core-api-go` v1.33.2 | [#4](https://github.com/namedotcom/core-api-go/issues/4) — **fixed in v1.33.3** |
| [`core-api-go-urlforwarding-host-required.md`](core-api-go-urlforwarding-host-required.md) | `namedotcom/core-api-go` v1.33.3 | not filed |
| [`core-api-go-forced-request-bodies.md`](core-api-go-forced-request-bodies.md) | `namedotcom/core-api-go` v1.33.3 | not filed |
| [`core-api-go-updatedomain-union.md`](core-api-go-updatedomain-union.md) | `namedotcom/core-api-go` v1.33.3 | [#6](https://github.com/namedotcom/core-api-go/issues/6) — open |
| [`core-api-go-idempotency-key-asterisk.md`](core-api-go-idempotency-key-asterisk.md) | `namedotcom/core-api-go` v1.33.3 | [#5](https://github.com/namedotcom/core-api-go/issues/5) — open |

Neither is worked around in this repository. What a mitigation would look like,
and what each was measured to cost, is recorded in
[`core-api-go-mitigations.md`](core-api-go-mitigations.md).

## Status

**The migration is done.** `namecom` v0.4.0 runs entirely on the Core SDK; the
vendored spec, the Python preprocessor, and the generated client were removed in
#62. See #40 for how it went.

Two of the six reports here are fixed upstream and closed. Four stand, and three
of those are worked around in this repository:

| report | what this repo does |
|---|---|
| `updatedomain-union` ([#6]) | `domain update` builds a `map[string]any` and passes `option.WithBodyProperties`. The typed body would silently drop the transfer lock. |
| `idempotency-key-asterisk` ([#5]) | `option.WithXIdempotencyKey` is never used; `internal/api/headers.go` sets the header for every write. |
| `urlforwarding-host-required` | `url update` sends the host it just fetched, because the type cannot omit it and an empty host means the apex. |
| `forced-request-bodies` | bodyless endpoints are given `&EmptyObject{}` so they send `{}` rather than `null`. |

**Each workaround is used in exactly one place and pinned by a test that fails
if it is removed.** That is deliberate: the natural instinct on reading any of
them is to replace it with the obvious typed call, and in three of the four
cases the obvious call is silently wrong rather than broken.

If a report is fixed upstream, the sequence is: bump the SDK, delete the
workaround, watch the pinning test fail, and update it — in that order, so the
test proves the fix rather than being adjusted to match it.

### On the two that were fixed

#3 and #4 were filed 2026-08-20 and fixed in v1.33.3 five days later, without a
comment on either issue. Both were verified by measurement here before being
closed:

| | v1.33.2 | v1.33.3 |
|---|---|---|
| client-scoped `WithoutRetries()`, one 500 | 2 requests | **1 request** |
| 100ms deadline against `Retry-After: 2` | 2.001s | **100.8ms** |

Worth recording for anyone deciding whether reporting to this tracker is worth
the effort: the silence is not an answer, but the fixes shipped.

[#5]: https://github.com/namedotcom/core-api-go/issues/5
[#6]: https://github.com/namedotcom/core-api-go/issues/6
