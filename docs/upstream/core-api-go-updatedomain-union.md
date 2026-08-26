# `UpdateDomainRequestBody` is an exclusive union, silently dropping fields

**Filed 2026-08-26 as [namedotcom/core-api-go#6](https://github.com/namedotcom/core-api-go/issues/6).** Found while porting `namecom domain` to the SDK (#40, slice 6). This is the most serious of the modelling problems found during that migration, and the reproduction is kept here rather than replaced by a link.

**Version:** v1.33.3 · **Go:** 1.26.6

## Summary

`PATCH /core/v1/domains/{domainName}` accepts any combination of
`autorenewEnabled`, `privacyEnabled`, and `locked`. The spec combines them with
`anyOf`, which means *at least one* — a body carrying all three is valid, and is
what a read-modify-write client sends.

Fern modelled that `anyOf` as an **exclusive union**. `UpdateDomainRequestBody`
holds three pointers and one private `typ`, and its `MarshalJSON` returns on the
first non-nil variant:

```go
func (u UpdateDomainRequestBody) MarshalJSON() ([]byte, error) {
	if u.typ == "UpdateDomainRequestBodyAutorenewEnabled" || u.UpdateDomainRequestBodyAutorenewEnabled != nil {
		return json.Marshal(u.UpdateDomainRequestBodyAutorenewEnabled)
	}
	if u.typ == "UpdateDomainRequestBodyPrivacyEnabled" || u.UpdateDomainRequestBodyPrivacyEnabled != nil {
		return json.Marshal(u.UpdateDomainRequestBodyPrivacyEnabled)
	}
	...
```

Set all three and **two are silently discarded**. No error, no warning; the
request succeeds and reports success.

## Why it matters

Measured, by building the body the obvious way and capturing the request:

```
sent: {"autorenewEnabled":true}
want: {"autorenewEnabled":true,"locked":false,"privacyEnabled":false}
```

`locked` is the **transfer lock**. A caller asking to unlock a domain — or, worse,
to *lock* one — gets a success response and no change, and finds out when a
domain transfers away. `privacyEnabled` is billable. These are not fields where a
silent drop is recoverable by retrying.

The failure mode is the dangerous kind: the code reads correctly, compiles, and
passes any test that does not inspect the bytes on the wire.

## Suggested fix

Model the body as a single object with three optional fields, matching the spec.
`anyOf` over sibling properties expresses "at least one of these", not "exactly
one of these" — the latter is `oneOf`. If a constraint is genuinely required,
validating "at least one present" at the call site preserves the ability to send
several.

## Note on where the fix belongs

These files are Fern-generated, so the fix belongs in the API description or the
generator's `anyOf` handling rather than in a patch to the Go source. It is worth
checking whether other `anyOf` bodies in this SDK are affected the same way; this
one was found only because the command happened to send more than one field.

## What this repository does about it

`domain update` builds a plain `map[string]any` and passes it via
`option.WithBodyProperties` with a nil `Body`. The SDK marshals exactly that map,
which is byte-identical to what the generated client sent.

That is an escape hatch, not a fix, and it is used deliberately in one place
rather than adopted as a pattern. The single-field toggles — `domain lock`,
`domain autorenew`, `domain privacy` — each set exactly one field, so they use
the union as intended and are unaffected.

`TestRequestShape_Domain/update_sends_all_three_fields` pins the wire body.
Replacing the map with the union makes it fail with the output quoted above.
