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
| [`core-api-go-urlforwarding-host-required.md`](core-api-go-urlforwarding-host-required.md) | `namedotcom/core-api-go` v1.33.3 | [#7](https://github.com/namedotcom/core-api-go/issues/7) — **fixed in v1.33.4** |
| [`core-api-go-forced-request-bodies.md`](core-api-go-forced-request-bodies.md) | `namedotcom/core-api-go` v1.33.3 | not filed |
| [`core-api-go-updatedomain-union.md`](core-api-go-updatedomain-union.md) | `namedotcom/core-api-go` v1.33.3 | [#6](https://github.com/namedotcom/core-api-go/issues/6) — **fixed in v1.33.5** |
| [`core-api-go-idempotency-key-asterisk.md`](core-api-go-idempotency-key-asterisk.md) | `namedotcom/core-api-go` v1.33.3 | [#5](https://github.com/namedotcom/core-api-go/issues/5) — **fixed in v1.33.5** |

Neither is worked around in this repository. What a mitigation would look like,
and what each was measured to cost, is recorded in
[`core-api-go-mitigations.md`](core-api-go-mitigations.md).

## Status

**The migration is done.** `namecom` v0.4.0 runs entirely on the Core SDK; the
vendored spec, the Python preprocessor, and the generated client were removed in
#62. See #40 for how it went.

**Five of the six reports here are fixed upstream.** One stands, and it is the
only one still worked around in this repository:

| report | status | what this repo does |
|---|---|---|
| `updatedomain-union` ([#6]) | fixed v1.33.5 | nothing — the union is gone and `domain update` uses the typed request. The `map[string]any` and `option.WithBodyProperties` were removed. |
| `urlforwarding-host-required` ([#7]) | fixed v1.33.4 | nothing — `url update` now omits `host` entirely, which is what "leave it alone" means. |
| `idempotency-key-asterisk` ([#5]) | fixed v1.33.5 | `internal/api/headers.go` still owns the header. `option.WithXIdempotencyKey` is now safe to use, but there is no reason to: the transport sets it centrally for every write. |
| `forced-request-bodies` | not filed, unchanged | bodyless endpoints are given `&EmptyObject{}` so they send `{}` rather than `null`. |

The sequence that removed the first two was the one written here before there
was anything to remove: bump the SDK, delete the workaround, watch the pinning
test fail, and update it — in that order, so the test proves the fix rather than
being adjusted to match it.

It paid off twice. `TestRequestShape_Domain` passed **unchanged** through the
removal of the map, which is what proves the typed request now sends the same
three fields the map did. And `TestRequestShape_URL/create` failed loudly on a
bug the bump introduced: v1.33.5 dropped the `CreateURLForwardingRequest`
wrapper that carried `DomainName`, so the path silently became
`/core/v1/domains//url/forwarding` until the field was set on the input itself.

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
[#7]: https://github.com/namedotcom/core-api-go/issues/7
[#6]: https://github.com/namedotcom/core-api-go/issues/6
