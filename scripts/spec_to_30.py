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
NULL_ONEOF_MEMBER = re.compile(r"^\s*-\s*type:\s*'null'\s*$")


def convert(lines):
    out = []
    i = 0
    n = len(lines)
    while i < n:
        line = lines[i]
        if NULL_ONEOF_MEMBER.match(line):
            i += 1
            continue
        m = TYPE_HEADER.match(line)
        if m and i + 2 < n:
            first = LIST_ITEM.match(lines[i + 1])
            second = LIST_ITEM.match(lines[i + 2])
            if first and second and second.group(1) == "'null'":
                indent = m.group(1)
                base = first.group(1)
                out.append(f"{indent}type: {base}\n")
                out.append(f"{indent}nullable: true\n")
                i += 3
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

    converted = convert(lines)
    with open(dst, "w", encoding="utf-8") as f:
        f.writelines(converted)

    print(
        f"renamed {len(response_schemas)} *Response schemas; "
        f"converted nullable unions; wrote {dst}"
    )


if __name__ == "__main__":
    main()
