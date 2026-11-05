# name.com API: TXT values are stored with their quotes escaped

> Not filed. Observed against the sandbox API (`api.dev.name.com`) in
> October and November 2026; recorded here because the CLI's record matching
> and quoting depend on it. Tracked in this repository as #284 and #360.

## What the API does

A TXT record created with the answer

```
v=spf1 "quoted" ~all
```

is listed back, by `ListRecords` and `GetRecord`, as

```
v=spf1 \"quoted\" ~all
```

A backslash is added before each `"`. Creating the same record a second
time is answered with a **500**, not the `400 Record already exists` a
duplicate of any other record gets, so a caller that compares the value it
sends with the value it reads back sees no match, retries the create, and
fails every time.

A value that **starts or ends with `"`, or ends with `\`**, is refused:

| Sent              | Answer                                          |
|-------------------|-------------------------------------------------|
| `say "x"`         | `Parameter Value Error - Malformed Txt Record`  |
| `"x" said`        | `Parameter Value Error - Txt Missing Quote At End` |
| `trailing\`       | `Parameter Value Error - Malformed Txt Record`  |

The same values written as a quoted, escaped string are accepted, and
**stored as sent**, quotes and escapes included: `"say \"x\""` is listed
back as `"say \"x\""`. Quoted strings are also how a value is sent in
several character-strings: `"a" "b"` is stored as `"a""b"`.

## What the CLI does

- **It sends the value as given, unless the API would refuse it.** A value
  that starts or ends with `"`, or ends with `\`, is sent as a quoted
  string with `\` and `"` escaped (split at 255 bytes), which the API
  accepts (#360). Every other value is sent exactly as typed, including one
  with a `"` inside it, which the API escapes on storage. `--answer` text
  that is already quoted strings (`"a" "b"`) is zone syntax and is sent as
  it is. A zone file's quoted string is unescaped by the zone parser as BIND
  unescapes it (`"say \"x\""` is the value `say "x"`), then quoted again
  for the API by the same rule.
- **It compares every spelling as one record.** `normAnswer`
  (`cmd/dns/plan.go`), which `dns create --if-not-exists`, `dns sync` and
  `dns import --skip-existing` all match through, reads a value written as
  quoted strings as the strings' content, and otherwise reads `\"` as `"`
  and `\\` as `\`. So the value as typed, a hand-written zone line, the
  quoted form the CLI sends, and the escaped form the API stores all match.
- **`dns export` JSON writes what the API holds**, so a value is never
  changed by being exported and imported again.
- **`dns export --zone` writes the value the record means.** The storage
  escaping is undone, and a value stored quoted loses that one layer of
  quotes, before the value is escaped for the zone file: the stored
  `v=spf1 \"quoted\" ~all` is written `"v=spf1 \"quoted\" ~all"`, which BIND
  reads as `v=spf1 "quoted" ~all`. It wrote the stored form, so BIND read a
  value with literal backslashes. The export loads in `named-checkzone`,
  and synced straight back it plans no change.
- **A TXT value must be ASCII.** The API refuses anything else ("only
  printable ASCII"); the CLI now refuses it before sending, so a
  `dns sync --dry-run` no longer plans a create certain to fail.

## Not verified

Whether the API also doubles a backslash in a value sent unquoted, and what
it stores when it is sent `\"` unquoted, was not measured. A literal `\"` in
a TXT value — a backslash the record really contains — compares equal to
`"`, which is the cost of matching both spellings; the zone export reads it
the same way.
