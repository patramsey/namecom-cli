<div align="center">

# namecom

**The command-line interface for [name.com](https://www.name.com)**

[![Beta](https://img.shields.io/badge/status-beta-orange)](https://github.com/patramsey/namecom-cli/releases)
[![CI](https://github.com/patramsey/namecom-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/patramsey/namecom-cli/actions/workflows/ci.yml)
[![Coverage](https://codecov.io/gh/patramsey/namecom-cli/branch/main/graph/badge.svg)](https://codecov.io/gh/patramsey/namecom-cli)
[![Latest Release](https://img.shields.io/github/v/release/patramsey/namecom-cli)](https://github.com/patramsey/namecom-cli/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/patramsey/namecom-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Register domains, manage DNS, and run your entire domain portfolio — without leaving the terminal.

</div>

---

![Registering a domain with namecom](docs/demo.gif)

*Recorded against the sandbox API with [vhs](https://github.com/charmbracelet/vhs) — see [`docs/demo.tape`](docs/demo.tape) for the script.*

And `namecom status`, for the portfolio at a glance — illustrated below with a
representative account rather than a real one:

```
$ namecom status
Profile  default  https://api.name.com
47 domains  3 expiring within 30 days  1 transfer pending  2 unlocked
Balance  $125.40

Expiring soon
  acme.io                        2026-10-18  (16 days)
  staging.dev                    2026-10-25  (23 days)
  oldsite.net                    2026-10-30  (28 days)

Transfers in progress
  newco.com

→ Run 'namecom domain renew <domain>' to renew expiring domains
→ Run 'namecom domain list' to see all domains
```

`namecom status -q` prints just the expired and soon-expiring domains, one per
line, ready for `xargs`.

## Contents

- [Why](#why)
- [Installation](#installation)
- [Quick start](#quick-start)
- [Commands](#commands)
- [Workflows](#workflows)
- [Output formats](#output-formats)
- [JSON contract](#json-contract)
- [Configuration](#configuration)
- [Shell completion](#shell-completion)
- [Global flags](#global-flags)
- [Exit codes](#exit-codes)
- [Idempotency keys](#idempotency-keys)
- [Development](#development)
- [Contributing](#contributing)
- [Changelog](CHANGELOG.md)
- [Security policy](SECURITY.md)
- [License](#license)

## Why

The name.com web UI is great for humans. The CLI is for agents and terminal wizards

- **Automate** domain renewals, DNS changes, and email forwards in CI/CD pipelines
- **Script** bulk operations across dozens of domains at once
- **Integrate** with secret managers via `token_cmd` — no credentials in shell history
- **Pipe** JSON output directly into `jq`, `grep`, and other tools
- **Stay fast** — tab completion, `--dry-run`, and `--yes` flags for confident automation

## Installation

**Homebrew (macOS / Linux):**
```bash
brew tap patramsey/tap
brew trust patramsey/tap
brew install namecom
```

> **Upgrading from v0.2.4 - v0.3.1?** Those versions shipped as a Homebrew
> *cask*; this is a *formula* again. `brew update` migrates you automatically
> but cannot finish the job, because Homebrew will not link a formula while a
> cask of the same name is installed. Two steps remain:
>
> ```bash
> brew uninstall --cask --force namecom
> brew link namecom
> ```
>
> Without the second, `namecom` will not be on your PATH. Fresh installs need
> neither.

**Download a release binary:**
```bash
# macOS (Apple Silicon)
curl -L https://github.com/patramsey/namecom-cli/releases/latest/download/namecom_darwin_arm64.tar.gz | tar xz
sudo mv namecom /usr/local/bin/

# macOS (Intel)
curl -L https://github.com/patramsey/namecom-cli/releases/latest/download/namecom_darwin_amd64.tar.gz | tar xz
sudo mv namecom /usr/local/bin/

# Linux (amd64)
curl -L https://github.com/patramsey/namecom-cli/releases/latest/download/namecom_linux_amd64.tar.gz | tar xz
sudo mv namecom /usr/local/bin/
```

All platforms and checksums on the [releases page](https://github.com/patramsey/namecom-cli/releases).

> **macOS:** these binaries are not signed with an Apple Developer ID. The
> commands above are unaffected — `curl` does not set the quarantine attribute —
> but a tarball downloaded through a **browser** is quarantined, and macOS will
> refuse to run it with *"Apple could not verify 'namecom' is free of malware."*
> Clear it with `xattr -d com.apple.quarantine namecom`, or approve the binary
> under System Settings → Privacy & Security. Homebrew installs are unaffected.

**Go install:**
```bash
go install github.com/patramsey/namecom-cli@latest
mv "$(go env GOPATH)/bin/namecom-cli" "$(go env GOPATH)/bin/namecom"
```
`go install` names the binary after the module path (`namecom-cli`); the `mv` renames it to `namecom` to match the rest of this README.

## Quick start

```bash
# 1. Authenticate (create an API token at https://www.name.com/account/settings/api)
namecom auth login

# 2. See your portfolio at a glance
namecom status

# 3. Check if a domain is available
namecom domain check mycoolstartup.com

# 4. Register it
namecom domain register mycoolstartup.com

# 5. Point it somewhere
namecom dns create mycoolstartup.com --type A --answer 1.2.3.4

# Tip: jump to the name.com dashboard for any domain
namecom open mycoolstartup.com
```

## Commands

| Group | Commands |
|---|---|
| `domain` | `list` `get` `search` `check` `register` `renew` `lock` `autorenew` `privacy` `set-ns` `contacts` `auth-code` `pricing` `update` `claims` `requirements` |
| `dns` | `list` `create` `update` `delete` `export` `import` `sync` |
| `dnssec` | `list` `get` `create` `delete` |
| `transfer` | `list` `get` `create` `cancel` `eligibility` `internal-in` `cancel-outbound` |
| `email` | `list` `get` `create` `update` `delete` |
| `url` | `list` `get` `create` `update` `delete` |
| `vanity-ns` | `list` `get` `create` `update` `delete` |
| `contact` | `unverified` `resend` `verify` — ICANN contact verification |
| `auth` | `login` `logout` `status` |
| `status` | account overview: domain counts, expiring domains, pending transfers, balance |
| `order` | `list` `get` `refund` |
| `config` | `list-profiles` (alias `profiles`) `use` `show` |
| `api` | raw HTTP passthrough with auth applied |
| `open` | open name.com in a browser (honors `$BROWSER`; prints the URL when no browser can be opened) |
| `version` | version and build information |
| `completion` | shell completion scripts: `bash` `zsh` `fish` `powershell` |

Every `list` also answers to `ls` and every `delete` to `rm`. In `dns`,
`dnssec`, `email`, `url` and `vanity-ns`, `create` also answers to `add`.

```
namecom --help
namecom domain --help
namecom dns --help
```

## Workflows

**Register a domain and set it up:**
```bash
namecom domain check acme.io                              # check availability
namecom domain register acme.io                           # register it
namecom dns create acme.io --type A --answer 1.2.3.4
namecom dns create acme.io --type MX --answer mail.google.com --priority 10
namecom email create acme.io hello --to you@gmail.com     # hello@acme.io → you@gmail.com
namecom domain autorenew on acme.io                       # never let it expire
```

**Manage DNS records:**
```bash
namecom dns list acme.io
namecom dns create acme.io --type CNAME --host www --answer acme.io.
namecom dns update acme.io 12345 --answer 5.6.7.8
namecom dns export acme.io --zone > acme.io.zone           # export as BIND zone file
namecom dns list acme.io --host www                       # only the records at www
```

**Keep DNS in a file and sync it** (re-runnable: a second run changes nothing):
```bash
namecom dns export acme.io --zone > acme.io.zone           # 1. snapshot the live zone
$EDITOR acme.io.zone                                       # 2. edit it, or keep it in git
namecom dns sync acme.io --file acme.io.zone --dry-run     # 3. see the plan; nothing is sent
namecom dns sync acme.io --file acme.io.zone               # 4. apply it, after a confirmation
namecom dns sync acme.io --file acme.io.zone --prune       #    ...also deleting what the file dropped
```

`dns sync` matches records on host, type and answer: a TTL or priority change
is an update, and a changed answer is a new record (the old one is deleted
only with `--prune`; a CNAME's target is updated in place, since a name holds
one CNAME). Without `--prune` nothing is deleted. `--prune` never touches NS
records at the apex — the domain's delegation — or CAA records, which the API
cannot recreate; `--prune-all` does. A file with no records is refused with
either, so a wrong path cannot empty the zone. Changes are applied creates first, then
updates, then deletes. If one fails, sync stops, reports what was applied, and
exits non-zero; fix the cause and run it again. The file can also be the JSON
`dns export` writes. In CI, `--dry-run -o json` gives the plan as one
document, and `--yes` skips the confirmation.

For scripts that add or remove single records, `dns create --if-not-exists`,
`dns delete --if-exists` and `dns import --skip-existing` succeed when the
work is already done, so a retry does not fail.

**Transfer a domain in:**
```bash
namecom transfer eligibility acme.io                      # confirm it's eligible
namecom transfer create acme.io --auth-code XXXXXX
namecom transfer get acme.io                              # check status
# set WHOIS contacts on arrival (same JSON as domain register --contacts-file);
# changing contacts may start a registrar transfer lock
namecom transfer create acme.io --auth-code XXXXXX --contacts-file contacts.json
```

**Set up email forwarding and URL forwarding:**
```bash
namecom email create acme.io hello --to you@gmail.com     # hello@acme.io → you@gmail.com
namecom email list acme.io
namecom url create acme.io --to https://new-site.com      # redirect apex to another URL
```

**Publish DNSSEC DS records** (values from your DNS host, which signs the zone):
```bash
namecom dnssec list acme.io
namecom dnssec create acme.io --algorithm 13 --digest-type 2 --key-tag 12345 --digest abc123
```

**Set up vanity nameservers:**
```bash
namecom vanity-ns create acme.io --hostname ns1.acme.io --ips 1.2.3.4
namecom vanity-ns create acme.io --hostname ns2.acme.io --ips 5.6.7.8
namecom domain set-ns acme.io --ns ns1.acme.io,ns2.acme.io
```

**Call an endpoint namecom does not wrap** (`namecom api`, modelled on `gh api`):
```bash
namecom api /core/v1/domains/acme.io                      # the method defaults to GET
namecom api /core/v1/domains --paginate --jq '.domains[].domainName'   # every page as one list
namecom api POST /core/v1/domains/acme.io/records -f host=www -f type=A -f answer=1.2.3.4 -F ttl=300 --dry-run
namecom api PUT /core/v1/domains/acme.io/records/123 --input record.json --yes   # in a script, a write needs --yes
namecom api -X DELETE /core/v1/domains/acme.io/records/123 --dry-run
namecom api /core/v1/hello --include                      # status line and headers, then the body
namecom api /core/v1/domains -i --jq '.totalCount'       # headers as they came, then the filtered body
```

The method is GET, or POST when the request has a body (`--data`, `--input`,
`-f` or `-F`); name it first, or with `-X`/`--method`, to send anything
else. `-f key=value` adds a string, and `-F key=value` keeps `true`, `false`,
`null` and numbers as JSON and reads `@file` (or `@-`, stdin). Keys nest as
`contact[firstName]=Ada`, and `ns[]=x` appends to a list. On a GET the fields are query parameters instead.
`--paginate` follows `nextPage` and prints one document whose lists hold every
page's items, without `nextPage` and `lastPage`; it asks for 1000 items a
page unless the path or `-f` sets `perPage`. `--include` prints the
status line and headers ahead of the body, and `--jq` and `--fields` filter
the body alone. Any method but GET and HEAD is a write: it asks first in a
terminal, needs `--yes` in a script or a pipe (exit 2,
`confirmation_required`, without it), and is previewed, not sent, under
`--dry-run`. A POST inferred from `-f` or `-F` says so in the question and
in a warning; pass `-X GET` to send the fields as a query instead.

**Scripting and automation:**
```bash
# List every domain expiring within 60 days (BSD/macOS date, then GNU date)
namecom domain list --all --expiring-before "$(date -v+60d +%F 2>/dev/null || date -d '+60 days' +%F)" -q

# Bulk-create an A record across all domains
namecom domain list --all -q | xargs -I{} namecom dns create {} --type A --answer 1.2.3.4

# '-' reads names from stdin, one per line (blank lines and # comments skipped)
namecom domain check - < names.txt
namecom domain list --all -q | namecom domain autorenew on - --yes   # one request per domain, one confirmation

# Dry-run first, then apply
namecom dns create acme.io --type TXT --answer "v=spf1 include:sendgrid.net ~all" --dry-run
namecom dns create acme.io --type TXT --answer "v=spf1 include:sendgrid.net ~all" --yes

# Capture the new record's ID
ID=$(namecom dns create acme.io --type A --host api --answer 1.2.3.4 -q)
namecom dns delete acme.io "$ID" --yes
```

**Rate limit.** namecom paces itself to 10 requests a second (bursts of 5)
and retries a 429 with backoff, which leaves headroom under the API's limit
of 20 a second for the account. The limiter is per process: `xargs -P 8`
runs eight processes with eight limiters, which together send up to 80
requests a second, so the excess comes back as 429s and, once retries run
out, exit code 5. Prefer one process with many arguments, which is paced as
a whole:

```bash
namecom domain list --all -q | namecom domain check -                 # not xargs -P
namecom dns delete acme.io $(namecom dns list acme.io --type TXT -q) --yes
```

For commands that take one domain, run `xargs` without `-P` (one process
at a time); `-P 2` already reaches the account's limit, and anything else
using the same account shares it.

**Confirmations.** Writes that are hard to undo ask first when run in a
terminal:

- deletes: `dns delete`, `email delete`, `url delete`, `vanity-ns delete`,
  `dnssec delete`
- anything that charges, refunds or moves a domain: `domain register`,
  `domain renew`, `transfer create`, `transfer internal-in`,
  `transfer cancel`, `transfer cancel-outbound`, `order refund`
- `domain set-ns`, `domain contacts set`, `domain lock off`,
  `domain privacy off`, `domain autorenew on` and `off`, and `domain update`
  making any of those three changes
- `dns sync` with changes to apply, `auth login` replacing a saved profile,
  and `namecom api` with any method but GET and HEAD

In a script or a pipe there is no one to ask, so these stop with
*"confirmation required for … — pass --yes to confirm when not running in a
terminal"* and exit 2 until you pass `--yes`. Every other write runs without
asking, in a terminal or not: `dns create`, `dns update`, `dns import`,
`email create` and `update`, `url create` and `update`, `vanity-ns create`
and `update`, `dnssec create`, `domain lock on`, `domain privacy on`,
`contact resend` and `verify`, `auth logout` and `config use`. Any write can
be previewed first with `--dry-run`.

**Paging.** A `list` prints one page: 1 to 1000 items with `--limit` (the
API's page size without it) from `--page`, filtered or not, and its footer —
or `nextPage` in JSON — says when there are more. `--all` fetches every page,
1000 items a request, and so does `-q` without `--page` or `--limit`.

## Output formats

Every command supports `--output table`, `--output json`, `--output yaml`
and `--output tsv`, except `dns export`, which writes a file: JSON, YAML or
a zone file. The default is `table` in a terminal and `json` when output is
piped or redirected:

```bash
namecom domain list                     # rich table with colors and expiry urgency
namecom domain list --output json       # machine-readable JSON
namecom domain list --output tsv        # the table's columns, tab-separated
namecom domain list --quiet             # one domain per line, for scripting
```

`namecom help formatting` covers everything in this section, with examples.

### Picking fields, jq, and TSV

`--fields a,b,c` keeps only those keys — the JSON keys `-o json` shows — of
each list item, or of the object a command prints, in that order. A list
keeps its `{"data": [...]}` envelope, so `nextPage` and `total` are still
there. It works with every `-o`, in the TSV shapes below: for a list the
fields are the columns, and for one object the `field<TAB>value` rows. A
table keeps the list's footer on stderr, so a list cut short by `--limit`
says so. The values are the JSON's, not the table's: `true` rather than
`yes`, a timestamp rather than a date, an empty host rather than `@`.

```bash
namecom domain list --all --fields domainName,expireDate -o tsv --no-header |
  while IFS=$'\t' read -r name expires; do echo "$name $expires"; done
namecom dns list example.com --fields id,type,host,answer -o table
```

An item without a field gets `null` for it (an empty TSV cell): the API
leaves out empty values, so items do not all have the same keys. A field
the output cannot have is a usage error (exit 2) that lists the fields there
are — on an empty list too.
`--fields` names top-level keys only; reach into nested ones with `--jq`.

`--jq <expr>` runs a jq expression over the document `-o json` would print,
with an embedded jq ([gojq](https://github.com/itchyny/gojq)), so `jq` need
not be installed. Each result prints on its own line: a string as itself,
without quotes (as `jq -r` and `gh --jq` print it), anything else as compact
JSON. gojq prints an object's keys sorted.

```bash
namecom domain list --all --jq '.data[] | select(.locked | not) | .domainName'
id=$(namecom dns create example.com --type A --answer 192.0.2.1 --yes --jq .id)
namecom dns create example.com --type A --answer 192.0.2.1 --dry-run --jq .body
```

`--jq` means JSON: without `-o` it prints JSON in a terminal too, and with
`-o table`, `yaml` or `tsv` it is a usage error. With `--fields`, the fields
are picked first. A malformed expression is a usage error with gojq's
message, reported before the command sends anything. An expression that
fails on the output, and an unknown field, are usage errors too, with
nothing printed — except after a write, where the change has been made:
the output is printed unfiltered, with a warning, and the exit code is 0.
Both flags act on stdout only; a failing command prints its error envelope
on stderr with its usual exit code.

`-o tsv` prints a table's columns as tab-separated values, with a header row
unless `--no-header`. There is no colour, a date has no "(in 3 months)", and
a missing value is an empty cell, not "—". A backslash, tab, line feed or
carriage return in a value is written `\\`, `\t`, `\n` or `\r`, so every row
is one line. The shape never depends on the data:

- A list prints a header row and a row per item, with the same columns
  whatever the items hold, and the header alone when it is empty. Several
  objects (`domain get a.com b.com`, the roles of `domain contacts get`)
  are a list.
- One object prints `field<TAB>value` rows, the same rows whatever it holds:
  a value it lacks is an empty cell. `domain get` uses its table's field
  names; a command without a detail table (`status`, `version`,
  `auth status`, `config show`, `domain claims`, `open`) uses the `-o json`
  keys with bare values, a value's source as a key of its own
  (`profileSource`).
- A write prints its result's keys the same way (`changed<TAB>true`); a read
  never does. A dry run prints `method`, `path` and `body` (as compact JSON)
  columns.

`-q` still wins over `-o`, `tsv` included. With `--fields` or `--jq`, which
choose what to print as well, it is a usage error.

`-q`/`--quiet` follows one rule whatever `--output` says: lists print one ID
or name per line, create commands print the new resource's ID, other writes
print nothing, and other reads print the one value a script most likely wants
(`version` the version, `auth status` the username, `domain pricing` the
price). Errors still go to stderr.

Tables drop their rightmost columns to fit a narrow terminal and say which
they hid; `--wide` keeps them all.

`--dry-run` prints the request a write would send, and sends nothing. In JSON
mode — including the default when piped — that is a JSON document with
`dryRun`, `method`, `path` and `body` keys; `-o table` prints
`METHOD /path` and the body instead.

## JSON contract

With `-o json` — the default when output is piped — and with `-o yaml`,
which carries the same keys, output follows the rules below. A change to any
of them is a breaking change and is called out in the
[CHANGELOG](CHANGELOG.md).

- **One document per stream.** The result goes to stdout. stderr carries at
  most one document: the error envelope when the command fails, or
  `{"warnings": [...]}` when it succeeded with something to say.
- **Lists are `{"data": [...]}`**, with `nextPage` and `total` added when the
  list is paged. `data` is `[]`, never `null`, when there is nothing in it.
  This covers every `list`, and `domain check`, `domain search`,
  `config list-profiles` and `dns export` too (`dns import` and `dns sync`
  read both that and the bare array older versions exported), and
  `domain get` given several domains or `-`.
- **One resource is the object itself**, as the API returns it: `domain get`
  with one domain, `dns create`, `email update`. With `--if-not-exists`,
  `dns create` adds `"changed"` to the record: `false` when it was already
  there, `true` when it was created.
- **Keys are camelCase** everywhere: `domainsTotal`, `dryRun`,
  `idempotencyKey`. Values that name a kind of thing, such as error types
  (`not_found`) or dry-run actions (`save_profile`), are snake_case.
- **A write with no resource to return** prints
  `{"success": true, "changed": true, "message": "…"}`. `changed` is `false`
  when the target was already in the requested state and nothing was sent —
  `domain lock on` for a locked domain, `dns delete --if-exists` for a record
  that is gone, `dns import --skip-existing` with nothing new. `message` is
  for people; branch on `changed`, not on its wording.
- **A write over several targets** — a toggle given several domains,
  `dns delete` with several IDs — is still one document: the same three
  keys, with `changed` true when any target changed, and one
  `{"domain", "id", "changed", "message"}` item per target under `data`
  (`domain` or `id` as applies). One target prints the plain document
  above, so `.changed` reads either.
- **A dry run** prints `{"dryRun": true, "method": …, "path": …, "body": …}`,
  with a `quote` object for a write that costs money. A dry run that plans
  several requests — `dns import`, a toggle or `dns delete` over several
  targets — prints `{"dryRun": true, "data": [ … ]}`; `dns sync --dry-run`
  adds its plan (`creates`, `updates`, `deletes`, `kept`, `unchanged`)
  beside that `data`. A dry run that would only change the config file —
  `auth login`, `auth logout`, `config use` — prints
  `{"dryRun": true, "config", "action", "profile", "default"}`, where
  `auth logout`'s `default` is the profile that would be the default
  afterwards and `defaultSource` says whether the file's `default:` key
  names it (`config`) or the profiles left imply it (`implied`).
  `open --dry-run` prints `{"url", "opened": false, "dryRun": true}`.
- **`domain check`** gives each name `purchasable`: `true`, `false` for a
  taken name, or `null` when the registry did not answer for it (the table
  says `unknown`).
- **`dns sync`** prints what it did: `{"domain", "changed", "applied": [ … ],
  "unchanged"}`. When a change fails, that document still goes to stdout,
  with `failed` (and `outcomeUnknown: true` when it may have gone through)
  and `notAttempted`, and the error envelope goes to stderr.
- **Warnings** — a `--base-url` or `NAMECOM_BASE_URL` that is not
  name.com, duplicate IDs dropped from `order refund`, records created before
  a `dns import` failed, an existing record's different TTL under
  `dns create --if-not-exists` — are
  not printed as text. They come out at the end, in the error envelope's
  `warnings`, or as `{"warnings": [...]}` on stderr when the command
  succeeded.
- **Nothing is HTML-escaped.** `<`, `>` and `&` print as themselves.
- **Errors** are one document on stderr:

  ```json
  {
    "error": {
      "type": "not_found",
      "status": 404,
      "message": "Not Found",
      "hint": "check the name or ID for typos"
    }
  }
  ```

  `type` is always there, and is one of:

  | `type` | Meaning | Exit code |
  |---|---|---|
  | `usage` | The command line is wrong: an unknown command or flag, a bad argument or value | 2 |
  | `confirmation_required` | A write needs `--yes`, because there is no terminal to ask | 2 |
  | `auth` | Credentials missing, failing or rejected, or access denied (HTTP 401/403) | 3 |
  | `not_found` | HTTP 404 | 4 |
  | `rate_limited` | HTTP 429, after the CLI's own retries | 5 |
  | `conflict` | The thing already exists (the API answers a duplicate DNS record with a 400 that says so), or HTTP 409, which the API uses for a reused idempotency key | 1 |
  | `aborted` | A confirmation was declined or a prompt cancelled | 1 |
  | `network` | No HTTP response: a timeout, or a connection that failed | 1 |
  | `unavailable` | `domain check --exit-status` found a name that is not available; nothing failed | 1 |
  | `api` | Any other failure: another API error, or a local one such as an unreadable file | 1 |

  `status` is the HTTP status, present only when the API answered. `message`
  and `hint` are for people. The other keys appear only when they apply:
  `details` holds structured detail (the raw response body for `namecom api`,
  the profile, username, endpoint and config file for a rejected
  `auth status`, with where each came from as `usernameSource`,
  `tokenSource` and so on), and `suggestions` the full command lines an unknown command
  was probably meant to be (`["namecom dns delete"]`). `idempotencyKey` is
  set when a write's outcome is unknown (exit 6): the `X-Idempotency-Key`
  the request carried. The envelope also has
  a top-level `hint`, a copy of `error.hint` where older versions put it.
  **It is deprecated**, kept for this release only so scripts can move to
  `error.hint`.

`namecom api` is the one exception: it prints the API's response body exactly
as received (`{"domains": [...]}`, not `{"data": [...]}`). It exists to reach
endpoints namecom does not wrap and to show what the API itself says, and
reshaping the body would hide the very thing it was asked for. Its errors use
the envelope above, with the response body as `details`. `-o yaml`, `-o tsv`
and `-q` do not apply to it and are usage errors (exit 2); use `--jq` or
`--fields` to pick from the body.

## Configuration

Credentials are written by `namecom auth login` to your platform's user config directory:

| Platform | Location |
|---|---|
| macOS | `~/Library/Application Support/namecom/config.yaml` |
| Linux | `$XDG_CONFIG_HOME/namecom/config.yaml`, or `~/.config/namecom/config.yaml` |
| Windows | `%AppData%\namecom\config.yaml` |

`namecom auth status` prints the path in use. An older `~/.namecom/config.yaml` is still read if the current location has no config, and `NAMECOM_CONFIG` overrides both. Multiple profiles are supported for managing separate accounts:

```bash
namecom auth login --profile work
namecom auth login --profile personal
namecom domain list --profile work
namecom config use work                 # make it the default
```

**Sandbox vs. production** — test changes safely against name.com's sandbox API before running them for real:
```bash
namecom auth login --profile sandbox --sandbox
namecom domain register test.com --profile sandbox
```
`--sandbox` at login saves the profile as a sandbox one, so every command run
with it targets `api.dev.name.com`. The sandbox has its own API token,
separate from your production one. Omit `--profile` to use your default
(production) profile.

**Environment variables** (useful in CI; `namecom help environment` lists them all):
```bash
export NAMECOM_USERNAME=yourname
export NAMECOM_TOKEN=yourtoken
export NAMECOM_SANDBOX=true        # target sandbox API (true/false, yes/no, on/off, 1/0)
export NAMECOM_PROFILE=staging     # select a profile
export NAMECOM_CONFIG=~/namecom-ci.yaml    # use this file instead of the default
export NAMECOM_BASE_URL=http://127.0.0.1:8080  # a local stub; --base-url overrides it
export NAMECOM_NO_UPDATE_NOTIFIER=1        # never print the "new release" notice
namecom domain list
```

`NAMECOM_USERNAME` and `NAMECOM_TOKEN` are enough on their own: no config file
or profile is needed. `namecom config show` and `namecom auth status` resolve
credentials exactly as other commands do and say where each value came from
(`env NAMECOM_TOKEN`, `flag --username`, `profile work`, `token_cmd`); in JSON
that is a sibling key such as `"usernameSource": "env NAMECOM_USERNAME"`.

**CI** — a GitHub Actions job needs only the two variables, from repository
secrets. `auth status` checks them first, so a bad token fails the job before
any real work:

```yaml
jobs:
  dns:
    runs-on: ubuntu-latest
    env:
      NAMECOM_USERNAME: ${{ secrets.NAMECOM_USERNAME }}
      NAMECOM_TOKEN: ${{ secrets.NAMECOM_TOKEN }}
      NAMECOM_NO_UPDATE_NOTIFIER: "1"
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: |
          go install github.com/patramsey/namecom-cli@latest
          mv "$(go env GOPATH)/bin/namecom-cli" "$(go env GOPATH)/bin/namecom"
      - run: namecom auth status
      - run: namecom dns create example.com --type TXT --host _verify --answer "${{ vars.VERIFY_TOKEN }}" --yes
```

To write a profile without a terminal instead — for a later step, or a
machine image — pipe the token to `--with-token`, or save a credential
helper with `--token-cmd`. Both check the credentials with the API first
(`--no-verify` skips that), and replacing an existing profile needs `--yes`:

```bash
echo "$NAMECOM_TOKEN" | namecom auth login --username alice --with-token
namecom auth login --username alice --token-cmd 'op read op://vault/namecom/token' --profile ci
```

**Secret manager integration** — add `token_cmd` to your config and credentials are fetched at runtime, never stored on disk:
```yaml
profiles:
  default:
    username: yourname
    token_cmd: "op read op://vault/namecom/token"  # 1Password example
```

The command must print the token on a single line. It runs through `sh -c` on
macOS and Linux, and through `cmd.exe` on Windows. To use `sh` syntax on
Windows, call `sh` yourself — for example
`token_cmd: sh -c "op read op://vault/namecom/token | tr -d '\r'"` — with an
`sh` on your `PATH` (Git for Windows ships one).

## Shell completion

The Homebrew formula installs completions for bash, zsh and fish. For other
installs, write the script to a directory your shell reads; these need no
root:

```bash
# bash (needs the bash-completion package, v2)
mkdir -p ~/.local/share/bash-completion/completions
namecom completion bash > ~/.local/share/bash-completion/completions/namecom

# zsh: then add `fpath=(~/.zfunc $fpath)` to ~/.zshrc, before `compinit` runs
mkdir -p ~/.zfunc
namecom completion zsh > ~/.zfunc/_namecom

# fish
mkdir -p ~/.config/fish/completions
namecom completion fish > ~/.config/fish/completions/namecom.fish
```

PowerShell: add `namecom completion powershell | Out-String | Invoke-Expression`
to your `$PROFILE`.

Open a new shell afterwards. `namecom completion <shell> --help` has more.

## Global flags

| Flag | Default | Description |
|---|---|---|
| `-o, --output` | `table` in TTY, `json` otherwise | Output format: `table`, `json`, `yaml`, `tsv` |
| `--fields` | | Keep only these keys of each list item, or of the object, in this order — see [Picking fields, jq, and TSV](#picking-fields-jq-and-tsv) |
| `--jq` | | Filter the JSON output with a jq expression; strings print unquoted |
| `-q, --quiet` | | Script output: lists print one ID or name per line, creates the new ID, other writes nothing — see [Output formats](#output-formats) |
| `-y, --yes` | | Skip all confirmation prompts; required, when not in a terminal, for the writes that confirm — see [Confirmations](#workflows) |
| `--dry-run` | | Print the request a write would send, without sending it — a JSON document in JSON mode. Reads are unaffected |
| `--profile` | | Use a named credential profile |
| `--sandbox` | | Target the sandbox API (`api.dev.name.com`) |
| `--base-url` | | Send requests to another API base URL, such as a local stub or a proxy (overrides `NAMECOM_BASE_URL`). Your credentials go wherever it points; a warning says so when it is not name.com |
| `--color` | `auto` | Colorize output: `auto`, `always`, `never` |
| `--wide` | | Keep every table column, even when the table is wider than the terminal |
| `--timeout` | `30s` | Total time budget for one API call, retries included |
| `--debug` | | Log HTTP requests/responses to stderr (token and auth codes redacted) |
| `--debug-file` | | Log HTTP requests/responses to a file (appends; useful as an audit log) |
| `--no-header` | | Omit the header row from table and TSV output |
| `--idempotency-key` | a fresh key per write | Pin every write in this invocation to one key, so re-running the same command after a failure can be recognized as a retry by endpoints that honor idempotency keys — see [Idempotency keys](#idempotency-keys) |
| `--username` | | API username (overrides config and `NAMECOM_USERNAME`) |
| `--token` | | API token (overrides config and `NAMECOM_TOKEN`) |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | API or other runtime error, a confirmation declined or a prompt cancelled (Ctrl-C), or — with `domain check --exit-status` — a name that is not available |
| `2` | Usage error: an unknown command or flag, a wrong number of arguments, or an invalid value |
| `3` | Authentication: credentials missing (an unknown `--profile` included), failing or rejected, or access denied (HTTP 401/403) |
| `4` | Not found (HTTP 404) |
| `5` | Rate limited (HTTP 429), after the CLI's own retries |
| `6` | Write outcome unknown: a request that changes something got a 5xx, or timed out or lost its connection after it was sent, so it may or may not have been carried out — see [Idempotency keys](#idempotency-keys) |

With `--output json` or `yaml` — including the JSON default when piped — an
error is written to stderr as one document, an `error` object whose `type`
says which of these it is. See [JSON contract](#json-contract).

## Idempotency keys

Every `POST`, `PUT` and `DELETE` namecom sends carries an `X-Idempotency-Key`
header: a fresh key per request, or, with `--idempotency-key`, the key you
name for every write in that invocation. `PATCH` (`domain update`) carries
none.

When a write fails in a way that leaves its outcome unknown — the API
answered 5xx, or the request timed out or lost its connection after it was
sent — namecom exits `6` and names the key it used: in the hint
(`outcome unknown; re-run with --idempotency-key <key>`) and, in JSON mode,
as `error.idempotencyKey`. A `POST` is never retried on a 5xx, because the
server may already have done the work. Check whether the change was made;
if it was not, re-run the same command with `--idempotency-key <key>`.

`dns sync` is the exception: it is not re-run with the key, since the next
run sends different requests, but simply run again. It plans from the live
zone, so a change that did land is not repeated. Its result document marks
the failed change `outcomeUnknown`, and its hint says to run sync again.

Whether the key prevents a duplicate depends on the endpoint. The Core API
declares the header on five operations:

| API operation | namecom command |
|---|---|
| `CreateDomain` | `domain register`, and the register `domain check` offers |
| `ProcessRefund` | `order refund` |
| `VerifyContact` | `contact verify` |
| `ResendContactVerificationEmail` | `contact resend` |
| `PurchasePrivacy` | none (`domain privacy on` uses a different endpoint) |

The API reference describes what a reused key does only for refunds: the
same key returns the original response instead of refunding again. For the
other four the header is declared but what the API does with it is not
documented. Every other write — DNS records, email and URL forwarding,
vanity nameservers, DNSSEC, transfers, renewals, nameserver and contact
changes — ignores it. Two `dns create` requests under one key made two
records in the sandbox. For those, check before you retry.

## Development

```bash
make build      # compile to ./namecom
make test       # go test ./...
make lint       # golangci-lint run
```

The API client is [`github.com/namedotcom/core-api-go`](https://github.com/namedotcom/core-api-go), name.com's own SDK. There is no code generation step.

## Contributing

Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the
setup, the checks CI runs, and what to know before touching the
API client or a command that writes. Participation is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

Security issues should go through
[private vulnerability reporting](https://github.com/patramsey/namecom-cli/security/advisories/new)
rather than a public issue — see [SECURITY.md](SECURITY.md).

Release history is in [CHANGELOG.md](CHANGELOG.md).

## License

MIT — see [LICENSE](LICENSE).
