# Upstream issue drafts

Bug reports written against dependencies but **not yet filed**. They live here so
the analysis is not lost between the day it is done and the day someone opens
the issue — the work of reproducing a defect and verifying a fix is worth more
than the few minutes it takes to paste it into a tracker.

When one is filed, replace its body with a link to the filed issue and keep the
file, so the reproduction stays with the repository that produced it.

| Draft | Against | Status |
|---|---|---|
| [`core-api-go-withoutretries-ignored.md`](core-api-go-withoutretries-ignored.md) | `namedotcom/core-api-go` v1.33.2 | not filed |
| [`core-api-go-backoff-ignores-context.md`](core-api-go-backoff-ignores-context.md) | `namedotcom/core-api-go` v1.33.2 | not filed |

Neither is worked around in this repository. What a mitigation would look like,
and what each was measured to cost, is recorded in
[`core-api-go-mitigations.md`](core-api-go-mitigations.md) for the #40 decision.
