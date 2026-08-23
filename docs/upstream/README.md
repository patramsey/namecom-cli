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

Neither is worked around in this repository. What a mitigation would look like,
and what each was measured to cost, is recorded in
[`core-api-go-mitigations.md`](core-api-go-mitigations.md).

## Status

**The Core SDK migration is on hold until #3 and #4 are resolved upstream.**

That is the standing decision for issue #40. #3 is the gate: with
`option.WithoutRetries()` ignored at client scope, the SDK retries POSTs on 5xx
against endpoints that do not honour idempotency keys, and the only fallback is
to repeat the option across every call site — a hazard that has to be carried
for as long as the defect stands rather than paid off once.

Nothing here blocks the CLI. The vendored spec and generated client keep working
and stay in place; this is a decision to wait, not a dependency to unpick.
