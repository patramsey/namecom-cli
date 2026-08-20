# Upstream issue drafts

Bug reports written against dependencies but **not yet filed**. They live here so
the analysis is not lost between the day it is done and the day someone opens
the issue, and so a workaround in this repo can point at the reasoning behind it.

When one is filed, replace its body with a link to the filed issue and keep the
file — the workaround it justifies will outlive the report.

| Draft | Against | Status |
|---|---|---|
| [`core-api-go-withoutretries-ignored.md`](core-api-go-withoutretries-ignored.md) | `namedotcom/core-api-go` v1.33.2 | not filed |
| [`core-api-go-backoff-ignores-context.md`](core-api-go-backoff-ignores-context.md) | `namedotcom/core-api-go` v1.33.2 | not filed |

Both are worked around in `internal/sdkspike`; see `workaround.go` for what each
costs and what residue is left over.
