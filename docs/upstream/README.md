# Upstream issue drafts

Bug reports written against dependencies. They live here so
the analysis is not lost between the day it is done and the day someone opens
the issue — the work of reproducing a defect and verifying a fix is worth more
than the few minutes it takes to paste it into a tracker.

Each carries a banner naming the filed issue once it is filed. The body stays
put rather than being replaced by a link — the reproduction and the verified fix
are the expensive part, and they should survive the tracker.

| Draft | Against | Status |
|---|---|---|
| [`core-api-go-withoutretries-ignored.md`](core-api-go-withoutretries-ignored.md) | `namedotcom/core-api-go` v1.33.2 | [filed as #3](https://github.com/namedotcom/core-api-go/issues/3) |
| [`core-api-go-backoff-ignores-context.md`](core-api-go-backoff-ignores-context.md) | `namedotcom/core-api-go` v1.33.2 | [filed as #4](https://github.com/namedotcom/core-api-go/issues/4) |

Neither is worked around in this repository. What a mitigation would look like,
and what each was measured to cost, is recorded in
[`core-api-go-mitigations.md`](core-api-go-mitigations.md) for the #40 decision.
