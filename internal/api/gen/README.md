# Generated API client (`internal/api/gen`)

`zz_generated.go` is produced by [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)
from the vendored OpenAPI spec. **Do not hand-edit it** — run `make generate`.

- **Vendored spec:** `namecom.api.yaml` (repo root), OpenAPI **3.1.0**, upstream version 1.27.1
- **Source:** https://namedotcom-cdn.name.tools/api-info/namecom.api.yaml
- **SHA256:** recorded as `SPEC_SHA` in the `Makefile`; `make verify-spec` enforces it
- **Codegen version:** oapi-codegen v2.4.1, pinned via the `tool` directive in `go.mod`
- **Config:** `codegen.yaml` (generates both models and a `net/http` client in package `gen`)

## Why there is a preprocessing step

`make generate` does **not** feed the vendored spec to oapi-codegen directly. It
first runs `scripts/spec_to_30.py`, which rewrites the spec into a
3.0-compatible form in a temp file. This exists to work around three concrete
incompatibilities between the spec and oapi-codegen v2.4.1. If you re-vendor a
newer spec and codegen breaks, this is the first place to look.

### 1. oapi-codegen does not support OpenAPI 3.1

The spec declares `openapi: 3.1.0`. oapi-codegen v2.4.x only targets 3.0.x and
emits a warning (and, for the constructs below, hard errors). Tracking issue:
https://github.com/oapi-codegen/oapi-codegen/issues/373

The script downgrades the `openapi:` version declaration to `3.0.3` and fixes
the two 3.1-only constructs the spec actually uses (next two sections).

### 2. Nullable union types: `type: [<t>, 'null']`

OpenAPI 3.1 expresses nullability as a JSON-Schema type union:

```yaml
type:
  - string
  - 'null'
```

oapi-codegen 3.0 expects the `nullable` keyword instead. Generating against the
union form fails with:

```
error resolving primitive type: unhandled Schema type: &[string null]
```

The spec contains ~223 of these (types `string`, `integer`, `number`,
`boolean`, `object`). The script rewrites each to:

```yaml
type: string
nullable: true
```

It deliberately leaves alone the nine schema **properties literally named
`type`** (e.g. the DNS record `type` field) — those are `type:` keys followed by
a normal scalar definition, not type unions.

### 3. Bare `null` member inside a `oneOf`

One property (`RequirementField.options`) lists `- type: 'null'` as a `oneOf`
member, which 3.0 cannot express and which fails with:

```
unhandled Schema type: &[null]
```

The script drops that bare-null member. The field's nullability is conveyed by
its description and Go's nil zero-value, so no information is lost.

## Why `*Response` schemas are renamed

The spec defines 33 component schemas whose names end in `Response` (e.g.
`SearchResponse`, `ListDomainsResponse`). oapi-codegen **also** names each
operation's typed-response wrapper `<OperationID>Response`. In a single package
these collide:

```
SearchResponse redeclared in this block
ListDomainsResponse redeclared in this block
...
```

We considered splitting models and client into separate packages (the usual
remedy), but oapi-codegen cannot cleanly split a **single, self-contained** spec
that way: `import-mapping` only rewires references that point at *external*
documents, so the generated client ended up referencing operation types
(`ListDomainsParams`, `CreateDomainJSONRequestBody`, …) unqualified and failed
to compile.

Instead, `scripts/spec_to_30.py` renames the colliding component schemas to
`<Name>Schema` — updating both the `components/schemas` definitions and every
`$ref` to them. This keeps everything in one package and keeps the generated
client. **Consequence to remember:** these model types carry a `Schema` suffix
(e.g. `models`... `gen.SearchResponseSchema`, not `gen.SearchResponse`). The
operation wrappers keep the plain `<OperationID>Response` names.

## Re-vendoring the spec

1. Download the new spec to `namecom.api.yaml` (root).
2. `shasum -a 256 namecom.api.yaml` and update `SPEC_SHA` (and the version
   comment) in the `Makefile`.
3. `make generate`.
4. If generation fails on a new 3.1 construct, extend `scripts/spec_to_30.py`
   (it is line-based and intentionally narrow) and document the new case here.
5. `go build ./...` and run the tests.
