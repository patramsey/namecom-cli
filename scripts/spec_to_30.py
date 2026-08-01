#!/usr/bin/env python3
"""Convert the vendored OpenAPI 3.1 spec to a 3.0-compatible form for codegen.

oapi-codegen (v2.4.x) does not yet support OpenAPI 3.1's nullable union types,
which the name.com spec expresses as:

    type:
      - string
      - 'null'

This script rewrites those blocks into the 3.0 equivalent:

    type: string
    nullable: true

and downgrades the declared OpenAPI version to 3.0.3. The vendored spec
(namecom.api.yaml) is left untouched; this produces a derived intermediate that
`make generate` feeds to oapi-codegen. The transform is line-based and only
touches the uniform two-element `[<type>, 'null']` unions present in the spec
(verified: every union is exactly this shape).

Usage: spec_to_30.py <input.yaml> <output.yaml>
"""

import re
import sys

# A union block is three consecutive lines:
#   <indent>type:
#   <indent>  - <basetype>
#   <indent>  - 'null'
TYPE_HEADER = re.compile(r"^(\s*)type:\s*$")
LIST_ITEM = re.compile(r"^\s*-\s*(.+?)\s*$")
# A bare null-type member of a oneOf/anyOf list, e.g. `- type: 'null'`.
# 3.0 has no standalone null type; the field's nullability is conveyed by its
# description and Go's nil zero-value, so we drop the member entirely.
# Accepts the three ways YAML can spell null so an upstream restyle doesn't
# silently slip past us.
NULL_ONEOF_MEMBER = re.compile(r"^(\s*)-\s*type:\s*(?:'null'|\"null\"|null)\s*$")

# Any null spelling as a union member.
NULL_MEMBER = re.compile(r"^(?:'null'|\"null\"|null)$")


class UnsupportedSpec(Exception):
    """The spec uses a construct this line-based transform cannot handle.

    Raised rather than passing the construct through untouched. A 3.1 union
    that reaches oapi-codegen unconverted does not fail the build — it silently
    generates the wrong Go type, which is far more expensive to discover than a
    failed `make generate`.
    """


# `format: float` on a `type: number` makes oapi-codegen emit float32 — roughly
# 7 significant decimal digits. That is fine for ordinary prices but silently
# wrong for the seven-figure sums premium domain sales reach: 1234567.89 becomes
# 1234567.88 when formatted and 1234567.9 when re-marshalled. Widening to
# `double` (float64) is loss-free — every float32 value is exactly representable
# — and makes the client echo the API's own figures faithfully.
FLOAT_FORMAT = re.compile(r"^(\s*)format:\s*float\s*$")


def widen_floats(lines):
    out = []
    for line in lines:
        m = FLOAT_FORMAT.match(line)
        if m:
            out.append(f"{m.group(1)}format: double\n")
            continue
        out.append(line)
    return out


def convert(lines):
    out = []
    i = 0
    n = len(lines)
    while i < n:
        line = lines[i]

        m_null = NULL_ONEOF_MEMBER.match(line)
        if m_null:
            # `- type: null` carrying sibling keys (a description, a title)
            # cannot be dropped line-wise: removing it orphans the siblings and
            # produces structurally invalid YAML. Refuse rather than corrupt.
            dash_indent = len(m_null.group(1))
            if i + 1 < n:
                nxt = lines[i + 1]
                if nxt.strip() and not nxt.lstrip().startswith("-"):
                    if len(nxt) - len(nxt.lstrip()) > dash_indent:
                        raise UnsupportedSpec(
                            f"line {i + 1}: `- type: null` has sibling keys "
                            f"({nxt.strip()!r}); dropping the member line alone "
                            f"would emit invalid YAML"
                        )
            i += 1
            continue

        m = TYPE_HEADER.match(line)
        if m:
            # Collect the full list of union members.
            members, j = [], i + 1
            while j < n:
                item = LIST_ITEM.match(lines[j])
                if not item:
                    break
                members.append(item.group(1))
                j += 1

            if members:
                nulls = [x for x in members if NULL_MEMBER.match(x)]
                non_null = [x for x in members if not NULL_MEMBER.match(x)]
                if len(members) != 2 or len(nulls) != 1:
                    raise UnsupportedSpec(
                        f"line {i + 1}: union type {members} is not the "
                        f"supported two-element [<type>, null] shape"
                    )
                out.append(f"{m.group(1)}type: {non_null[0]}\n")
                out.append(f"{m.group(1)}nullable: true\n")
                i = j
                continue

        out.append(line)
        i += 1
    return out


SCHEMA_DEF = re.compile(r"^(    )([A-Za-z][A-Za-z0-9]*):\s*$")
REF = re.compile(r"#/components/schemas/([A-Za-z][A-Za-z0-9]*)")


def collect_response_schemas(lines):
    """Return the set of component schema names ending in 'Response'.

    oapi-codegen names each operation's typed-response wrapper
    `<OperationID>Response`, which collides with schema components that share
    that name. We rename those components (definitions + $refs) to
    `<Name>Schema` so the generated client compiles in a single package.
    """
    names = set()
    in_components = in_schemas = False
    for line in lines:
        if line.startswith("components:"):
            in_components = True
        elif in_components and re.match(r"^  schemas:\s*$", line):
            in_schemas = True
        elif in_schemas and re.match(r"^  \S", line):
            # Left the schemas block (any 2-space top-level key).
            break
        elif in_schemas:
            m = SCHEMA_DEF.match(line)
            if m and m.group(2).endswith("Response"):
                names.add(m.group(2))
    return names


def rename_response_schemas(lines, names):
    if not names:
        return lines
    out = []
    for line in lines:
        m = SCHEMA_DEF.match(line)
        if m and m.group(2) in names:
            out.append(f"{m.group(1)}{m.group(2)}Schema:\n")
            continue
        out.append(
            REF.sub(
                lambda r: r.group(0) + "Schema" if r.group(1) in names else r.group(0),
                line,
            )
        )
    return out


def main():
    if len(sys.argv) != 3:
        sys.exit(f"usage: {sys.argv[0]} <input.yaml> <output.yaml>")
    src, dst = sys.argv[1], sys.argv[2]
    with open(src, "r", encoding="utf-8") as f:
        lines = f.readlines()

    # Downgrade the version declaration so oapi-codegen treats it as 3.0.
    for idx, line in enumerate(lines):
        if line.startswith("openapi:"):
            lines[idx] = "openapi: 3.0.3\n"
            break

    response_schemas = collect_response_schemas(lines)
    lines = rename_response_schemas(lines, response_schemas)

    lines = widen_floats(lines)

    try:
        converted = convert(lines)
    except UnsupportedSpec as e:
        sys.exit(
            f"{sys.argv[0]}: {src}: {e}\n"
            "The vendored spec uses an OpenAPI 3.1 construct this line-based "
            "transform does not handle. Extend convert() rather than letting it "
            "through: an unconverted 3.1 union does not fail codegen, it silently "
            "generates the wrong Go type."
        )
    with open(dst, "w", encoding="utf-8") as f:
        f.writelines(converted)

    print(
        f"renamed {len(response_schemas)} *Response schemas; "
        f"converted nullable unions; widened float->double; wrote {dst}"
    )


if __name__ == "__main__":
    main()
