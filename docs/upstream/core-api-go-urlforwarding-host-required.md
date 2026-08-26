# `URLForwardingInput` forces a `host` key onto update requests

**Filed 2026-08-26 as [namedotcom/core-api-go#7](https://github.com/namedotcom/core-api-go/issues/7).**
Found while porting `namecom url` to the SDK (#40, slice 3). Kept here so the
reproduction and the measurements stay with the repository that produced them.

**Version:** v1.33.3 · **Go:** 1.26.6

## Summary

The SDK models create and update for URL forwardings with one input type,
`URLForwardingInput`. Its `Host` field is a plain `string` tagged
`json:"host"` with no `omitempty`, so **every update request serialises a
`host` key**, whether or not the caller wants to touch it.

The name.com API does not require it. `PATCH /core/v1/urlforwarding/{domain}/{id}`
accepts a body without `host`, and that is what a client should send when the
caller did not ask to move the forwarding.

## Why it matters

An empty host on a URL forwarding is not "unchanged" — it is the **apex**. So a
caller who builds the input as a struct literal and updates only `forwardsTo`
sends:

```json
{"forwardsTo":"https://example.org","host":"","type":"redirect"}
```

which asks the API to move the forwarding from `blog.example.com` to
`example.com`. The caller wrote three lines of obviously-correct code and
changed something they never mentioned.

## The explicit-field machinery does not cover this

`URLForwardingInput` has setters (`SetHost`, `SetForwardsTo`, …) that record a
bitmask, and `MarshalJSON` passes it to `internal.HandleExplicitFields`. That
looks like the intended way to say "only send what I set", but it is not what
the function does:

```go
if explicitFields == nil || explicitFields.Sign() == 0 {
    return marshaler
}
```

…and for the non-empty case it *removes* `omitempty` from fields that were set,
so their zero values are transmitted. It never omits a field that was not set.
A field without `omitempty` is therefore always serialised, and no combination
of setters suppresses `host`.

Confirmed by measurement — updating only `forwardsTo`:

| construction | body sent |
|---|---|
| struct literal | `{"forwardsTo":…,"host":"","type":…}` |
| setters, `SetHost` not called | `{"forwardsTo":…,"host":"","meta":null,"title":null,"type":…}` |
| generated client (oapi-codegen, same spec) | `{"forwardsTo":…,"type":…}` |

Using setters makes it worse: `title` and `meta` gain explicit nulls too.

## Suggested fix

Give update its own input type whose optional fields are pointers with
`omitempty`, as the update body already does elsewhere in this SDK —
`EmailForwardingsUpdateEmailForwardingBody.EmailTo` is `*string` with
`omitempty`, and `UpdateVanityNameserverBody.Ips` is `[]string` with
`omitempty`. URL forwarding is the odd one out in sharing its create type.

Failing that, `Host *string` with `omitempty` on the shared type would be
enough, since create can still send it explicitly.

## Note on where the fix belongs

Same caveat as the other reports here: these files are Fern-generated, so the
durable fix is likely in the API description or the generator configuration
rather than in a patch to the Go source.

## What this repository does about it

`url update` seeds `Host` from the record it fetched immediately before, in the
read-modify-write it already performs. The key is therefore present but is a
restatement of the stored value rather than a change.

This is the one request in the SDK migration whose wire shape differs from what
the generated client sent. It is asserted explicitly in
`cmd/url/shape_test.go` so the difference is visible and pinned rather than
discovered later.

[#5]: https://github.com/namedotcom/core-api-go/issues/5
[#6]: https://github.com/namedotcom/core-api-go/issues/6
