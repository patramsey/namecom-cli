# Bodyless endpoints are modelled with a body that cannot be omitted

**Not filed upstream.** Found while porting `namecom contact` to the SDK (#40,
slice 4). The least severe of the four: `{}` is accepted where `null` is not,
so the workaround is a one-line construction with no data risk. Worth filing if
[#5] and [#6] get attention.

**Version:** v1.33.3 · **Go:** 1.26.6

## Summary

`VerifyContact` and `ResendContactVerificationEmail` take no request body. The
SDK models both with a `Body *EmptyObject` field and marshals it unconditionally,
so:

| `Body` | bytes sent |
|---|---|
| `nil` (the zero value) | `null` |
| `&EmptyObject{}` | `{}` |
| — | there is no way to send nothing |

The generated client (oapi-codegen, same spec) sends no body at all.

## Why it matters

The zero value is the dangerous one. A caller who writes the obvious thing —

```go
client.ContactVerification.VerifyContact(ctx, &coreapigo.VerifyContactRequest{
    VerificationID: id,
})
```

— sends `null` as the request body with `Content-Type: application/json`. A
strict server rejects that for an object-typed body, and the failure is a 400
that looks like a malformed request rather than a client-library artifact.

`&EmptyObject{}` at least produces a valid document, but it is not obvious that
it is required, and nothing in the type says so.

## Suggested fix

Omit the body entirely when the operation declares none, as the generated client
does. Failing that, marshalling a nil `Body` as `{}` rather than `null` would
remove the trap without changing the surface.

## Note on where the fix belongs

These files are Fern-generated, so the durable fix is likely in the API
description or the generator rather than in a patch to the Go source. `EmptyObject`
appearing in the surface at all suggests the operations declare an empty schema
where they should declare none.

## What this repository does about it

`contact verify` and `contact resend` pass `&coreapigo.EmptyObject{}` explicitly,
so both send `{}`. The change from "no body" to `{}` is asserted in
`cmd/contact/drift_test.go` so it stays visible.

[#5]: https://github.com/namedotcom/core-api-go/issues/5
[#6]: https://github.com/namedotcom/core-api-go/issues/6
