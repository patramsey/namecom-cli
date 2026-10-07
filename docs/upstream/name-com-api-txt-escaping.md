# name.com API: TXT values are stored with their quotes escaped

> Not filed. Observed against the sandbox API (`api.dev.name.com`) in
> October 2026; recorded here because the CLI's record matching depends on it.
> Tracked in this repository as #284.

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

## What the CLI does

- **It sends the value as given.** The API escapes on storage, so the
  unescaped form is the one that means the record a user typed. A zone
  file's quoted string is unescaped by the zone parser as BIND unescapes it
  (`"v=spf1 \"quoted\" ~all"` is the value `v=spf1 "quoted" ~all`).
- **It compares both spellings as one.** `normAnswer` (`cmd/dns/plan.go`),
  which `dns create --if-not-exists`, `dns sync` and
  `dns import --skip-existing` all match through, reads `\"` as `"` and `\\`
  as `\` on both sides. So the value as typed, a hand-written zone line, and
  the escaped form a `dns export` carries all match the stored record.
- **`dns export` writes what the API holds.** The JSON and the zone file keep
  the escaped form, so a value is never changed by being exported; the
  comparison above is what lets an export be synced back without a change.

## Not verified

Whether the API also doubles a backslash it is sent, and what it stores when
it is sent the already-escaped `\"`, was not measured. A literal `\"` in a
TXT value — a backslash the record really contains — compares equal to `"`,
which is the cost of matching both spellings.
