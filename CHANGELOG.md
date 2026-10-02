# Changelog

All notable changes to `namecom` are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this project
follows [Semantic Versioning](https://semver.org/).

Releases before `0.2.0` predate this file. Their notes are on the
[releases page](https://github.com/patramsey/namecom-cli/releases).

## [Unreleased]

### Fixed
- `url create --dry-run` and `url update --dry-run` no longer print a
  `host=… to=… type=…` summary line after the preview. In JSON or YAML mode
  that line followed the dry-run document, so the output was not valid JSON and
  `| jq` failed. The preview body already shows the host, target and type.
- A 403 from `transfer internal-in` no longer says to run `namecom auth
  login`. The error says the account needs enterprise reseller approval, and
  the hint now says the credentials are fine. It still exits 3. The same hint
  change applies to `contact verify`, which also printed the "check your
  credentials" line. The `hint` field in the JSON/YAML error envelope changes
  for both.
- `transfer create` and `transfer internal-in` without `--auth-code`, when not
  run in a terminal, now exit **2** (usage error) instead of 1, matching a
  too-short `--auth-code`.
- `--price` on `domain register`, `domain renew` and `transfer create` must be
  a positive number. `Inf`, `NaN`, zero and negative values now exit **2**
  before anything is sent. Before, `NaN`, zero and negatives were silently
  ignored, and `Inf` was quoted in the prompt as `$+Inf`, gave an empty
  `--dry-run` preview, and failed when sent.
- A long non-JSON error body (a proxy's error page, say) is no longer cut in
  the middle of a multi-byte character when it is shortened for the error
  message, and invalid UTF-8 in such a body is replaced. The message, including
  the one in the JSON error envelope, is now always valid UTF-8.
- API errors from every command are now reported the way `namecom api`
  reports them. A 500 whose body explains the failure (such as `Invalid IP`)
  no longer suggests trying again shortly; an HTML error page from a proxy is
  shortened to one line instead of becoming the whole error message; and a 401
  mentions that the sandbox uses a separate API token. The error `message` in
  JSON output changes for non-JSON error bodies: it no longer starts with the
  status code (`502: <html>…`), and an empty body reads as the status text
  (`Service Unavailable`) rather than the bare code.
- A successful response whose body is empty, not JSON, or JSON of the wrong
  shape now fails with `unexpected response from the API: …` and a hint that
  a change may still have been made, instead of a Go decoder message naming
  internal types (`json: cannot unmarshal array into Go value of type …`,
  `expected a **api.DomainResponsePayload response …`). It still exits 1.
- A final 429 or 5xx is reported at once instead of after an extra wait. The
  API library slept before returning these even with its retries turned off:
  up to 60 seconds on a 429's `Retry-After`, and a second or two on a 5xx. A
  429 whose `Retry-After` outlasted `--timeout` could also come back as a
  `request canceled` error with exit 1; it now exits 5 as documented.
- A write (POST, PUT, PATCH, DELETE) answered with a redirect now fails,
  naming the redirect, instead of following it. A redirected POST used to be
  resent as a GET without its body, so `dns create` could report
  `Created A record (id 0)` and exit 0 when nothing was created. Reads still
  follow redirects. This applies to `namecom api` as well.

### Security
- Your credentials are sent only to the API's exact origin: the same scheme,
  host and port as the endpoint in use. A redirect from the API to another
  port on the same host, or from `https` to plain `http`, used to carry the
  `Authorization` header along, because only the hostname was compared. It is
  now removed, including one passed with `namecom api --header`.
- `--debug` and `--debug-file` no longer write domain transfer auth codes.
  The request bodies of `transfer create` and `transfer internal-in`, and the
  response of `domain auth-code`, logged the code in full; it now appears as
  `[redacted]`, as in the `--dry-run` preview. Password- and token-like fields
  are redacted the same way, at any depth in a request or response body. A
  log that pasted an auth code into a bug report or a CI log before this
  release still has it; if one may have been shared, treat that code as
  exposed.
  Bodies with something redacted are logged re-encoded, so their key order
  and spacing can differ from what was sent.
- `auth logout`, `auth login` and `config use` honour `--dry-run`. They
  ignored it and wrote the config file: `auth logout --dry-run` deleted the
  profile. They now print the change they would make and leave the file alone
  — in JSON or YAML mode as one document with `dry_run`, `config`, `action`,
  `profile` and `default` keys (`auth login` adds `username` and `sandbox`,
  never the token). `auth login --dry-run` still asks its questions.
- On Windows, commands no longer warn that the config file "is accessible by
  other users" on every run. Windows reports every writable file with Unix
  mode `0666`, so the warning could never be cleared; the check now runs only
  on Unix-like systems.
- On Windows, `token_cmd` runs through `cmd.exe` instead of `sh -c`. A stock
  Windows install has no `sh`, so `token_cmd` failed with
  `exec: "sh": executable file not found`. If your helper relied on `sh` (for
  example from Git Bash), wrap it: `token_cmd: sh -c "…"`. macOS and Linux are
  unchanged.
- `auth login --sandbox` saves the profile with `sandbox: true` and no longer
  asks the sandbox question. The flag was ignored, so the profile was saved for
  production unless you also answered Yes at the prompt.
- Shell completion now honors `--profile`, `--token`, `--base-url`,
  `--sandbox` and `--timeout` typed on the command line. They were ignored, so
  `namecom --profile prod dns list <TAB>` offered the default profile's
  domains.
- Shell completion no longer runs a profile's `token_cmd` on every TAB.
  Credentials are resolved only when a completion needs the API (domain names,
  record IDs), so completing subcommand and flag names no longer invokes a
  password-manager helper.
- Shell completion gives up after 2 seconds (or `--timeout`, if shorter) and
  does not retry. An API that accepted connections but never answered froze
  the shell for the full 30-second timeout on every TAB.

## [0.4.7] - 2026-09-29

Eleven bug fixes and two changes in how write commands behave. Most came from
a second pass over the sandbox smoke-test findings and from live checks
against the sandbox API.

Two changes affect scripts directly:

- **`domain set-ns` and `domain contacts set` now ask for confirmation.**
  Scripts must pass `--yes`.
- **`--dry-run` prints a JSON document whenever the output format is JSON**,
  including the default when stdout is piped. Pass `-o table` for the old
  `METHOD /path` text.

Other output a script might see change, all toward what was documented or
intended:

- `order list --until` and `domain list --expiring-before` now include the date
  they name.
- `namecom api -o json` writes only the error envelope to stderr, with the
  response body in `error.details`. An unknown HTTP method exits **2**.
- `dns create --type CAA` (and a CAA record in `dns import`) exits **2**.
- `config show` prints the endpoint with `https://`, and `config
  list-profiles` marks the active profile rather than the `default:` key.
- `dns export` of an empty zone prints `[]` instead of `null`.
- The register confirmation prompt and the hint on some 5xx errors are
  worded differently.

### Fixed
- `order list --until DATE` now includes orders placed on DATE, as its help
  says. The API treats `createDateEnd` as exclusive — midnight at the start of
  the day — so the command left that day out, and `--since D --until D`
  returned nothing. The CLI now sends the following day as `createDateEnd`;
  a script that worked around this by passing the next day will now get one
  extra day of orders.
- `domain list --expiring-before DATE` now includes domains that expire on
  DATE, as its help says. The API treats the end of the range as exclusive, so
  a domain expiring on the named day was left out; the CLI now sends the day
  after. A script that passed the next day to work around this will see one
  more day of domains.
- `dns create` offered `CAA` in its `--type` help, its "--type is required"
  message and its interactive type picker, but the API rejects CAA on create.
  `dns create --type CAA`, and a CAA record in a `dns import` file, are now a
  usage error (exit 2) saying the API does not accept CAA records, before any
  request is sent. `dns list --type CAA` is unchanged.
- `order refund` drops repeated `--item-ids` before sending, keeping the
  first-seen order, and warns on stderr naming the IDs it dropped. It sent
  them as given, so `--item-ids 9,9` refunded item 9, then reported the second
  copy as failed ("already refunded") and exited 1. The `--dry-run` preview
  and the confirmation prompt show the deduplicated list.
- `domain register` no longer labels a registry premium price "/yr". The
  prompt reads e.g. `at $1000.00 (premium; renews at $24.99/yr)`, since the
  premium is charged on the purchase and the name renews at its own price. For
  aftermarket, expiring and backorder names it drops "for N year(s)", which the
  API does not guarantee for those purchase types, and notes when a
  non-default `--years` may not apply. The prompt text changed, so a script
  that parses the non-interactive "pass --yes to confirm" error will see the
  new wording.
- `namecom api -o json` (and `-o yaml`) writes only the error envelope to
  stderr on a non-2xx response. It also wrote `HTTP <status>` and the raw
  response body ahead of the envelope, so stderr was not one parseable
  document. The body now appears in the envelope as `error.details` — parsed
  if it is JSON, as a string if not. Table mode is unchanged, as are exit codes.
- `namecom api` rejects an unknown HTTP method as a usage error (exit **2**)
  naming the accepted ones — GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, in
  any case — before sending anything. `api FOO /x` was sent, drew a 403 from
  the server, and exited 3 with advice to run `auth login`.
- `config list-profiles` marks the profile API commands would use, resolved
  the same way as `config show` and `auth status`: `--profile`, then
  `NAMECOM_PROFILE`, then the `default:` key, then the lone profile. It
  compared against the `default:` key alone, so with `NAMECOM_PROFILE` set it
  marked a profile other than the one in use, and with one profile and no key
  it marked none. The DEFAULT column and the `default` field in JSON and YAML
  keep their names but now mean "active".
- `config show` prints the endpoint with its scheme
  (`https://api.dev.name.com`), as `auth status` does. It printed a bare host,
  so the two commands showed the same value in different forms. The
  `endpoint` field in `-o json` and `-o yaml` changes accordingly; a script
  that compares it to a bare host needs updating.
- `dns export` printed `null` for a zone with no records, in both JSON and
  YAML. It now prints `[]`, like the list commands. `dns import` already
  treated both as an empty file, so older exports still import as a no-op.
- A 5xx whose body explains the failure no longer gets the hint "try again
  shortly". The API answers 500 for some validation errors — `vanity-ns
  create` with a reserved IP, for one — so the hint now reads "name.com
  returned a server error; if it persists, the request itself may be invalid".
  An empty or non-JSON 5xx body, or a bare "Server error", keeps the old
  wording. The error message and exit code are unchanged; only the `hint`
  text (and the `hint` field of the JSON/YAML error envelope) differs.

### Changed
- `domain set-ns` and `domain contacts set` now ask for confirmation, like
  other destructive writes. **Scripts that run either command must now pass
  `--yes`**: without it, a non-interactive run exits with an error and changes
  nothing. The prompt names what is being sent — the nameservers, or the
  contact roles being replaced — and warns when the registrant is among them,
  since that can trigger ICANN verification or a transfer lock. `--dry-run`
  still never prompts.
- `--dry-run` prints a JSON or YAML document in those output modes:
  `{"dry_run": true, "method": "POST", "path": "/core/v1/…", "body": {…}}`,
  with `body` left out for a request that has none. `dns import --dry-run`
  prints one array of them, one per record. This applies wherever JSON is the
  format, including the default when stdout is not a terminal, so **a script
  that reads the `METHOD /path` line from a piped `--dry-run` must now parse
  the document, or pass `-o table` to keep the text form.** Table mode is
  unchanged, except that `dns import --dry-run` now indents each body like
  every other command's preview.

### Documentation
- `order list` and `order get` help, and the `--since`/`--until` flag help, now
  note that name.com's order timestamps currently run several hours behind UTC
  despite the `Z` suffix (about 6h, most likely US Mountain time), and that the
  server-side date filters use the same clock, so orders placed near midnight
  UTC can land on the previous day. This is an API issue; the CLI prints the
  timestamps as the API returns them and does not shift them (#134).

## [0.4.6] - 2026-09-29

Twelve bug fixes, most found by running every command against the name.com
sandbox. The most serious: `namecom api --dry-run` sent the request anyway, and
`url create --host ""` could replace, then later delete, every apex A record.
Every command that writes now shares one dry-run / confirm / send path, so a
`--dry-run` preview is the request that would be sent and never prompts.

Several change output a script might depend on, all toward what was documented
or intended. Check any script that relies on these:

- `-o yaml` uses the same keys as `-o json` (`domainName`, not `domainname`;
  `domains_total`, not `domainstotal`) and leaves out the same empty fields.
- Empty lists print `[]` instead of `null` in JSON and YAML.
- An unparseable ID or on/off argument exits **2** (usage), not 1.
- `domain update` with no flags is a usage error, and its `--dry-run` body
  shows only the settings you pass.
- `--dry-run` on `dnssec create`, `email create`/`update`, and
  `vanity-ns create`/`update` prints the JSON body instead of a `key=value`
  summary line.
- `dns export --zone` writes hostname targets with a trailing dot.
- `namecom api --dry-run` no longer sends POST, PUT, PATCH, or DELETE requests.

### Fixed
- `namecom api` now honours `--dry-run`. It ignored the flag and sent the
  request, so `api POST /core/v1/domains --data … --dry-run` would register
  the domain. Every method other than GET and HEAD now prints the method, path
  with its query string, and body (indented if it is JSON, quoted if not)
  instead of sending it; GET and HEAD still run, as the flag's help says.
- `domain renew --price` and `transfer create --price` now confirm the price
  they send. Both prompts quoted the standard price while the request carried
  the `--price` override — the same mismatch fixed for `domain register` in
  0.4.5.
- `url create --host ""` is now a usage error (exit 2) pointing at `@`, as it
  already was for `dns create`. It sent an empty host, which the API treats
  as distinct from `@`: the forwarding replaced every apex A record, and
  deleting it removed them all. A whitespace-only `--host` is refused the same
  way on both commands. The `--host` help now notes that a forwarding on a
  subdomain replaces that host's existing A records.
- `domain update` now sends only the settings you pass. It resent all three,
  including the current transfer lock, and during the 60-day lock after
  registration or transfer the API rejects any request that mentions the lock —
  so `domain update --autorenew=false` failed with "Domain can not be unlocked
  until …". The `--dry-run` body now carries only the passed fields, and
  `domain update` with no flags is a usage error (exit 2) instead of a request
  that changed nothing.
- `order refund` now shows each item's reason when every item fails. The API
  answers that case with HTTP 409 rather than 200, and the command printed the
  raw response body as its error. It now prints the same per-item warnings and
  "N of N item(s) were not refunded" error as a partial failure, and `-o json`
  / `-o yaml` emit the refund result on stdout instead of an error envelope on
  stderr. The exit code is still 1.
- `vanity-ns get`, `update`, and `delete` now accept a bare label (`ns1`) as
  `vanity-ns create --hostname` does, qualifying it against the domain. They
  passed it through unchanged and the API answered "Hostname not found." A
  trailing dot or upper case is also normalized, and a hostname under another
  domain is rejected before any request. The hostname in `--dry-run` output
  and in the update/delete success message is now the qualified one.
- `dns export --zone` writes CNAME, NS, MX, and SRV targets (and the target in
  an ANAME comment) with a trailing dot. The API strips the dot on storage, so
  a CNAME to `example.net` was exported as `example.net`, which a zone file
  reads as relative: `example.net.example.com.`. A target that already ends in
  `.`, and the root `.` of a null MX or SRV, are left as they are. The zone
  output changes for any script that parses it; JSON export is unchanged.
- A positional argument that cannot be parsed now exits **2**, the documented
  usage code, instead of 1: a non-numeric ID in `dns delete|update`,
  `url get|update|delete`, `order get` and `contact resend|verify`, and an
  on/off value other than `on` or `off` in `domain lock|autorenew|privacy`. A
  script that treated exit 1 from these as a usage mistake needs to check for
  2. The messages are unchanged.
- `-o yaml` uses the same keys as `-o json`. It named keys after the
  lowercased Go fields (`domainname`, `emailto`, `domainstotal`) instead of
  the JSON names (`domainName`, `emailTo`, `domains_total`), and printed
  fields JSON leaves out as `null` (`priority: null`, `meta: null`). YAML is
  now derived from the JSON encoding, so keys, their order, and omissions
  match on every command. **This changes YAML keys: scripts that parse
  `-o yaml` output must switch to the JSON names.**
- An empty list prints `"data": []` in JSON and `data: []` in YAML. `transfer
  list`, `vanity-ns list`, `email list`, and `dns list` printed `"data": null`,
  so `jq '.data[]'` failed with "Cannot iterate over null" on an account or
  zone with nothing in it.
- `--dry-run` on `dnssec create`, `email create`, `email update`,
  `vanity-ns create`, and `vanity-ns update` now prints the JSON body the
  command would send, like every other write command. It printed no body, only
  a hand-written `key=value` summary — for `vanity-ns` the raw `--ips` string
  rather than the list actually sent. Anything parsing that summary line needs
  to read the JSON instead.
- `domain register` now reads `--contacts-file` before asking you to confirm
  the purchase. A missing or malformed file was reported only after you had
  approved the price.

## [0.4.5] - 2026-09-28

Nineteen bug fixes from a review of every command. Four of them could cost
money or credentials: `domain register` could confirm one price and charge
another, `auth logout` could remove the wrong profile, saving the config could
leave a cleared plaintext token on disk, and `order refund` reported success
for items it did not refund. Nothing changes what any command asks you for
beyond the price in the register prompt.

Several change behaviour a script might depend on, all toward what was
documented or intended. Check any script that relies on these:

- `order refund` exits **1** when any item is not refunded. It exited 0.
- `--quiet` on list commands prints **every page**. It stopped after the first
  page without warning, so quiet output can be longer and make more requests.
- `auth logout`, `auth status`, `status`, and `config show` honour
  `NAMECOM_PROFILE` and the implied default profile, so they can now act on or
  report a different profile than before.
- `dns import` defaults a missing (or zero) TTL to 300 instead of sending 0.
- `transfer create --dry-run` and `transfer internal-in --dry-run` no longer
  prompt, and `domain set-ns --dry-run` no longer prints an `ns=` line.

### Fixed
- `domain register` now confirms the price it actually sends. For aftermarket,
  expiring, and backorder names, and whenever `--price` was passed, the prompt
  quoted the standard registration price while the request carried a
  different one — `at $12.99/yr?` could submit a $2500 purchase. The prompt
  now shows the sent price, and an acquisition price reads as a flat fee
  (`$2500.00 flat (aftermarket_b, not per year)`), since the API does not
  multiply it by `--years`.
- `auth logout`, `auth status`, `status` and `config show` now pick the
  active profile exactly as API commands do: `--profile`, then
  `NAMECOM_PROFILE`, then the `default:` key, then a profile named `default`
  or the only profile. They ignored `NAMECOM_PROFILE`, so
  `NAMECOM_PROFILE=staging namecom auth logout` removed the **production**
  profile, and `auth status` authenticated as staging while reporting prod.
  With a single profile not named `default`, logout and `config show` failed
  and the status commands reported the wrong profile.
- `config show --profile <name>` describes that profile. The flag never
  reached the command, which described the default profile instead.
  `config show` also reflects `--sandbox`, `NAMECOM_SANDBOX` and
  `NAMECOM_USERNAME` in the endpoint and username it reports.
- Saving the config file now removes a `token`, `token_cmd`, `sandbox`, or
  `icons` value that was cleared, instead of leaving the old one on disk.
  Answering No to sandbox in `auth login` on a sandbox profile kept
  `sandbox: true`, so the new production token was sent to the sandbox API and
  rejected; switching a profile to `token_cmd` kept the plaintext `token`,
  which still took precedence. Unknown keys and comments are still preserved.
- `order refund` no longer reports success for items the API refused. It
  counted every item in the response as refunded, so an item outside the
  refund grace period printed `✓ Refunded $0.00 for 1 item(s)` and exited 0.
  Only refunded items are counted now; each failed or canceled item is
  printed with the server's reason, and the command exits 1 if any item was
  not refunded. `-o json` still prints the full per-item result.
- `--quiet` on `url list`, `email list`, `vanity-ns list`, `transfer list`,
  `order list`, and `domain list` now prints every page. Without `--all` it
  stopped after the first page, and the "showing first page" hint it would
  have printed is suppressed in quiet mode, so a script piping the output got
  a truncated list with no warning.
- `domain list --all` no longer loops forever against a server that keeps
  reporting the same next page without a last page. Each repeat fetched the
  same page again and added its domains to the output a second time.
- `dns export old.com | dns import new.com --file -` works for zones with
  apex records. The API writes the apex host as `""`, and import rejected it
  as an empty `--host` before creating anything; it now imports as `@`.
- `dns import` gives a record with no `ttl` the same 300-second default as
  `dns create`, and rejects a TTL under 300 before writing any record. A
  missing TTL was sent as 0 and failed partway through, after the records
  before it had already been created.
- `dns export --zone` output loads in standard zone parsers. TXT values over
  255 bytes, such as a 2048-bit DKIM key, are split into several quoted
  strings as RFC 1035 requires, and ANAME records, which have no standard
  zone-file form, are written as comments instead of as records.
- Interactive `dns create` sends the MX or SRV priority you enter. The value
  was dropped from the request, and the command then warned that the
  priority was 0.
- `transfer create --dry-run` and `transfer internal-in --dry-run` no longer
  ask for confirmation. In a terminal they asked you to approve a transfer
  that would not be sent; in a script they failed with "pass --yes to confirm
  in non-interactive mode". `domain register --dry-run` already skipped the
  prompt.
- `domain contacts set --dry-run` and `domain set-ns --dry-run` print the body
  the real request sends. `contacts set` showed the contacts without their
  `{"contacts": ...}` wrapper, and `set-ns` showed no body at all, followed by
  the `--ns` value as typed rather than the trimmed list that is sent.
- `url update --title ""` and `--meta ""` clear the field. An empty value was
  treated as unset, so the old title or meta was sent back and there was no
  way to remove either from a masked forwarding.
- `url update --dry-run` shows the forwarding type that will be sent. Its
  summary line printed the `--type` default of `redirect`, so a masked
  forwarding looked as if it was about to be converted.
- A `token_cmd` that prompts on the terminal — a password manager asking for
  its passphrase, for example — can now read your answer. It was started in a
  background process group, so reading the terminal stopped it until the 15s
  timeout. When the CLI has no terminal, a timeout still kills the helper's
  whole pipeline; with one, it kills the shell, and the CLI still stops
  waiting two seconds later.
- `domain contacts get` and `domain requirements` no longer crash when the API
  leaves out the contacts, TLD info, or requirements object. `contacts get`
  prints the empty result; `requirements` shows dashes for capabilities it was
  not given, and `-q` prints nothing.
- `email get`, `url get`, `vanity-ns get`, and `dnssec get` check the domain
  argument before starting the spinner. An invalid domain left the spinner
  running while the error was printed.
- A 429 whose `Retry-After` is absurdly large (more than about 292 years)
  now waits the longest retry backoff, 30 seconds, as any other long
  `Retry-After` does. The number overflowed, so the CLI retried at once, and
  the error hint left out how long the API had asked you to wait.

## [0.4.4] - 2026-09-24

A patch release for one bug and a dependency refresh. Nothing changes what any
command asks you for or what it prints.

### Fixed
- `vanity-ns update --ips ""` clears a vanity nameserver's glue records, as
  documented. It sent an empty request body instead, so nothing was cleared
  and the command still reported success. The SDK tagged the field
  `omitempty`, which drops an empty list just as it drops a missing one; SDK
  v1.33.6 removed the tag.

### Changed
- The name.com Core SDK is now pinned to v1.34.0. Apart from the fix above,
  nothing changes what any command sends or prints.
- `golang.org/x/mod`, `x/sync`, `x/term`, and `x/time` each moved up one minor
  version. They handle the version check, concurrency, terminal detection, and
  rate limiting; none changes what the CLI sends or prints.

## [0.4.3] - 2026-09-24

Bug fixes found by running every command against the sandbox rather than by
reading the code. Nothing changes what any command asks you for.

Three of them change output a script might depend on, all toward what was
documented or intended: lookup commands now exit 4 on not-found instead of 1,
`order list` returns newest first, and `status` JSON no longer counts expired
domains in `expiring_critical`. Check any script that parses those.

### Fixed
- Commands that look something up now exit **4** when it does not exist, as
  the exit-code table documents. Nine exited 1 instead — `domain get`,
  `domain contacts get`, `domain auth-code`, `dnssec get`, `url get`,
  `vanity-ns get`, `transfer get`, `order get`, and `dns list` — so a script
  checking `$? -eq 4` could not tell "not found" from any other failure. Most of
  them also printed the raw response body, `404: {"message":"Not Found"}`, as
  their error; `domain get` and `transfer get` had friendlier not-found
  messages that never appeared.

  This regressed in v0.4.0 with the move to the Core SDK, whose errors had to be
  converted at each call site to be recognised. They are now converted once for
  every command.
- `status` no longer reports expired domains as "expiring within 7 days". Its
  query had no lower date bound, so every already-expired domain came back and
  counted as critical — a domain that expired two years ago showed as
  `1 expiring within 7 days` with `(-793 days)`, a red alarm that could never
  clear. Expired domains now get their own count, an **Expired** section, and
  `(expired 793 days ago)`. In JSON, `expiring_critical` no longer includes
  them and a new `expired` count does.
- `namecom api` can send a query string. `?` was escaped into the path, so
  `namecom api GET "/core/v1/orders?perPage=2"` returned a 403 and every filter,
  sort, and page parameter was unreachable. The protection against a path
  redirecting the request to another host is unchanged.
- `order list` shows the newest orders first. It used the API's ascending
  default, so on a long history the first page was its oldest orders and
  anything recent — including everything `order refund` can still act on — sat
  behind every other page. The DATE column also now reads `2026-04-06` like
  every other command, rather than a raw `2026-04-06T11:39:11Z`.
- `dns list`, `url list`, and `url get` show the apex host as `@`, the same
  spelling `--host` accepts, rather than an empty cell.

### Removed
- The Homebrew formula no longer prints the cask-migration caveat. It told
  anyone upgrading from the cask (v0.2.4-v0.3.1) to run `brew uninstall --cask
  --force namecom && brew link namecom`, which was necessary when the formula
  came back in v0.3.2 — but caveats print on every install, and the cask was
  last published five releases ago. Effectively everyone who reads it now is a
  fresh installer being handed recovery steps for a path they were never on.

  The steps remain in the v0.3.2 entry below.

## [0.4.2] - 2026-09-03

A dependency release. Nothing changes about what you type or what comes back;
two commands put a slightly different request on the wire, and one of those is a
request the CLI had wanted to send all along.

### Changed
- The name.com Core SDK is now pinned to v1.33.5, which fixes three defects this
  project had been working around. Two workarounds are gone as a result.

  `domain update` no longer assembles its request as a raw map. The SDK's typed
  body used to be an exclusive union that silently transmitted one field and
  dropped the rest — including `locked`, the transfer lock — so the command
  bypassed it. The three fields are now flat on the request and the typed call
  sends all of them. The bytes on the wire are unchanged.

  `url update` no longer sends a `host` key. The type it shared with `create`
  could not omit the field, so the command sent back the host it had just
  fetched — a restatement, since an empty host means the apex rather than
  "unchanged". Update now has its own type where the field can be omitted, which
  is what the API means by leaving the host alone, and it removes the small race
  in re-sending a value read moments earlier.

  Neither change alters what you type or what comes back.

- `golang.org/x/mod` moved to v0.40.0. It parses the version strings behind the
  "a newer version is available" check; nothing about that check changes.

## [0.4.1] - 2026-08-29

A patch release: two visible bugs, no change to what any command asks for or
returns. Both were found after v0.4.0 shipped — one by a user reading a table,
one by a test sweep.

### Fixed
- `domain check` and `domain search` now state whether a domain is premium
  instead of leaving the PREMIUM column blank. The column rendered an empty
  cell for every non-premium domain, which read as a column that had failed to
  render rather than as an answer — and it was the only boolean in the CLI that
  did not print an explicit yes/no.

  A purchasable domain now reads `yes` or `no`; an unavailable one reads `—`,
  matching how the PRICE column already treats a domain you cannot buy. The
  distinction follows the API contract: `premium` is returned only for
  purchasable domains, so its absence there means "not premium" rather than
  "unknown".

  This is worth stating plainly because premium status changes what registering
  costs and what the request must carry — a premium registration has to send
  `purchasePrice`.

- The "a newer version is available" notice now works for binaries built with
  `make build` or `make install`. It compared versions by prepending `v` to
  both sides, so a version string that already had one — which is what `git
  describe` produces — became `vv0.4.0`, failed to parse, and reported "no
  update" every time. Release binaries were unaffected; the two build paths
  formatted the version differently.

  Builds from an untagged commit now say nothing at all rather than offering an
  "upgrade" to the release they are already ahead of.

## [0.4.0] - 2026-08-26

The CLI now runs on [`github.com/namedotcom/core-api-go`](https://github.com/namedotcom/core-api-go),
name.com's own SDK, instead of a client generated from a vendored copy of the
OpenAPI spec. About 40,000 lines left the repository and Python left the build.

Minor rather than patch because two commands send a slightly different request
than before — both unavoidable, both detailed under **Changed** — and because
three bugs the previous client had been hiding are fixed. Nothing changes what
any command asks you for, prints on success, or returns as an exit code.

### Added
- `--dry-run` previews the request body for `order refund`, `transfer create`,
  `transfer internal-in`, `url create`, and `url update`. These printed only a
  method and path, so `transfer create --dry-run` gave no indication of what was
  being transferred or at what price, and `order refund --dry-run` paraphrased
  the request rather than showing it.

  **The transfer auth code is redacted.** It authorises moving a domain between
  registrars and `--dry-run` output reaches terminal scrollback and CI logs, so
  it appears as `[redacted]` in the preview and is sent normally on the real
  request.

### Fixed
- `contact resend --dry-run` and `contact verify --dry-run` print the request
  instead of sending it. Both ignored the flag entirely, so
  `contact resend --dry-run` **delivered the verification email to the
  registrant** — previewing and doing differed by an email arriving in someone
  else's inbox. They were the only writes in the CLI that did not honour
  `--dry-run`.
- `transfer internal-in` no longer prints an always-empty `(status: )`. That
  endpoint returns a domain payload, which has no status field, but the response
  was decoded into a transfer struct — so the status was blank on every
  successful run. `transfer get` is where the status comes from, and the command
  already says so.
- `domain check`, `domain register`, and `transfer create` no longer risk a
  panic on a response missing an optional object. In `domain check` the affected
  code is the safety net that reports "could not determine availability" rather
  than implying a domain is taken, so the crash would have removed exactly the
  protection it exists to provide.
- The `Authorization` header is bound to the API's hostname and is not sent
  anywhere else. `net/http` strips it when following a cross-host redirect, but
  header injection moved into the transport during this work and a transport
  runs again for the redirected request — which would have handed the credential
  to whatever host the redirect named. Caught by an existing test before
  release; the property is now enforced explicitly rather than inherited.

### Changed
- Every command calls the Core SDK. Requests and output were compared against
  the previous client command by command and are byte-identical, with two
  exceptions that the SDK's types make unavoidable:

  - **`url update` sends a `host` field it previously omitted.** The SDK models
    create and update with one input type whose `host` has no `omitempty`. The
    value sent is the host read from the record being updated — a restatement of
    what is already stored, not a change — because an empty host on a URL
    forwarding means the apex and would silently move the forwarding.
  - **`contact verify`, `contact resend`, `transfer cancel`, and
    `transfer cancel-outbound` send `{}` where they previously sent no body.**
    These endpoints take no body, but the SDK marshals its body field
    regardless; left unset it sends the literal `null`, which a strict parser
    rejects for an object-typed body.

  Both are documented in [`docs/upstream/`](docs/upstream/) with reproductions,
  and both are pinned by tests so they cannot drift further.

### Removed
- The vendored OpenAPI spec, the Python preprocessor that downgraded it from 3.1
  to 3.0, the 23,381-line generated client, `make generate`, `make verify-spec`,
  the `verify-generate` CI step, and the `oapi-codegen` tool dependency.
  **Building no longer requires Python.**

## [0.3.2] - 2026-08-23

### Changed
- Homebrew installs are a **formula** again rather than a cask. Casks apply
  `com.apple.quarantine` and formulas do not, which is the entire reason
  v0.2.4-v0.3.1 tripped Gatekeeper on macOS. Nothing required a cask: the
  migration was made because GoReleaser deprecated `brews`, not because
  Homebrew asked for it. Binary-installing formulas in third-party taps are
  ordinary, and this puts `namecom` back in the same shape as before v0.2.4.

  **Upgrading from v0.2.4-v0.3.1 takes two manual steps.** `brew update`
  installs the formula for you, but Homebrew will not link it while a cask of
  the same name is present, and the instruction it prints stops short:

  ```bash
  brew uninstall --cask --force namecom
  brew link namecom
  ```

  Without the second command `namecom` will not be on your PATH. Fresh
  installs need neither. The formula printed both as caveats from v0.3.2
  through v0.4.2; that notice was removed once fresh installers outnumbered
  the people it was written for, so this entry is now where the steps live.

### Removed
- The cask's `postflight` hook that stripped `com.apple.quarantine`. It
  disabled a Gatekeeper check for every user to work around a problem that
  only existed because of the cask. No longer needed, and not replaced —
  formula installs are never quarantined.

## [0.3.1] - 2026-08-23

### Fixed
- `brew install namecom` no longer produces *"Apple could not verify 'namecom'
  is free of malware"* on macOS. Homebrew casks apply `com.apple.quarantine` on
  install where the formula this project used before 0.2.4 did not, and these
  binaries carry only Go's ad-hoc signature, so Gatekeeper refused to run them.
  The cask now clears the attribute in a `postflight` hook.

  Affects every macOS `brew` install since 0.2.4. Already-installed copies are
  fixed by reinstalling (`brew reinstall namecom`) or by clearing the attribute
  directly: `xattr -d com.apple.quarantine "$(readlink -f "$(which namecom)")"`.

  This is not code signing. The binaries remain unnotarized, so a tarball
  downloaded through a browser is still quarantined; the README now says so.
  `curl` does not set the attribute, so the documented download commands are
  unaffected.

## [0.3.0] - 2026-08-23

Minor rather than patch because of one behavioural change worth checking before
upgrading: **invocation mistakes now exit 2 instead of 1.** A bad flag value, an
unknown record type, a missing required flag — all of these previously exited 1,
the same code as an API or runtime failure, and now exit 2 as the documented
table has always said. A script branching on exit 1 to detect "the command was
wrong" needs to look for 2. Nothing else in this release changes an exit code,
and no command changes what it sends to the API.

### Added
- `--wide` keeps every table column even when the table is wider than the
  terminal.

### Changed
- `--timeout` is described as the total budget for one API call including
  retries, which is what it has always been (`http.Client.Timeout`), rather
  than "per-request timeout".

### Fixed
- `dns import` sent the **same idempotency key on every record**. The key was
  minted once per invocation, but an invocation can perform many operations —
  import posts once per record — so a 50-record zone file went out under one
  key. An API honouring keys as documented ("reusing the same key returns the
  original result instead of repeating the operation") would create the first
  record, echo it back for the other 49, and let the CLI report the whole file
  as imported. Each write now gets its own key. `--idempotency-key` still pins
  every write in an invocation to one value, which is what makes re-running a
  failed command collapse onto the original.
- Credentials that existed were reported as missing. A config file with no
  top-level `default:` key resolved to no profile at all, so `auth status` said
  "no credentials configured — run 'namecom auth login'" (which would have
  overwritten them) while `config list-profiles` printed the profile it was
  refusing to use. A profile named `default` is now used without the key, as is
  a lone profile under any name; two or more with no default is an error that
  names them and suggests `--profile`.
- Seven list commands could page forever. Only `domain list` bounded its walk
  with `lastPage`; the rest trusted the server to stop saying "there is more",
  so one that kept answering `nextPage: 2` made `dns list --all` run
  indefinitely at the full client rate limit. All paginated walks — including
  record-ID shell completion and `namecom status` — now stop unless the page
  number advances and stays within `lastPage`.
- A 429 carrying a long `Retry-After` was swallowed. The CLI slept on it until
  the request deadline expired and then reported `context deadline exceeded
  (Client.Timeout exceeded while awaiting headers)` with exit 1 — a transport
  error, hiding the rate-limit answer the server had already given. A wait that
  cannot fit the remaining budget is no longer taken: the 429 is returned as-is,
  with exit 5 and a hint naming the wait the API asked for. A server-supplied
  wait is also capped at 30s, matching the cap computed backoff always had.
- Error bodies that are not the API's JSON envelope are summarized instead of
  echoed. A 502 HTML page from a proxy became a single 20 KB error message; it
  is now collapsed to one line and truncated to 400 characters with the dropped
  byte count disclosed.
- A mistyped subcommand now fails instead of succeeding. Cobra checks for
  unknown commands only on the root command, so `namecom domain regsiter
  example.com` printed the group's help and exited **0** — meaning
  `namecom domain regsiter foo.com && deploy` ran `deploy`. Every command
  group now rejects an unknown subcommand as a usage error (exit 2) and
  offers the same "Did you mean this?" suggestion the root command does.
  Invoking a group bare still prints its help and exits 0.
- Invocation mistakes now exit **2**, as the documented exit-code table has
  always claimed. Flag-value validation returned unclassified errors and
  cobra's own required-flag check runs where `SetFlagErrorFunc` cannot see
  it, so `--type ZZZ` and a missing `--answer` both exited 1 — the same code
  a script uses to detect a server error — while `--badflag` beside them
  exited 2.
- Tables no longer overflow the terminal. They rendered at natural width
  regardless of it (`domain list` came to 113 columns, `order list` 99,
  `dns list` 87), so in an 80-column pane the rounded borders wrapped into
  fragments. Trailing columns are now dropped until the table fits, with a
  footer naming what was hidden; `--wide` restores them, and piped output is
  unaffected.
- Relative dates widen their unit past a quarter, so a domain paid through
  2034 reads `in 8 years` rather than `in 2750 days`.
- `--dry-run` said it printed "the API request that would be sent without
  executing it", but only write operations honour it; reads always called the
  API. The flag now says so rather than implying an invocation touches
  nothing.
- `dns create --type` omitted `CAA` from its list of record types, which the
  validator has always accepted.
- Help pages put `Examples:` directly under the usage line instead of below
  the flag tables and footer, command groups show `namecom <group> <command>`
  instead of the uninvokable `namecom <group> [flags]`, and non-string flag
  defaults print unquoted (`default 300`, not `default "300"`).

### Documentation
- `CLAUDE.md` claimed `transport.go` retries a POST when `X-Idempotency-Key` is
  set. It never has: `idempotent()` covers GET/HEAD/PUT/DELETE only, and
  `transport_test.go` pins that a key does not make a POST retryable on 5xx.

## [0.2.4] - 2026-08-17

### Changed
- Homebrew now installs `namecom` as a **cask** rather than a formula.
  GoReleaser deprecated the `brews:` key it had been built with. The
  install command is unchanged (`brew install namecom`), and the tap
  carries a migration entry, so an existing install moves itself over on
  the next `brew upgrade` and prints how to drop the old keg.

### Security
- The Go toolchain moves to 1.26.6, clearing four standard-library
  advisories that `govulncheck` found reachable from this binary:
  `GO-2026-6218` (quadratic complexity in `net/url.resolvePath`),
  `GO-2026-6090` (unbounded post-handshake messages in `crypto/tls`),
  `GO-2026-5972` (recursion depth in `encoding/asn1`), and `GO-2026-5026`
  (ASCII-only Punycode labels in `net/http`'s IDNA handling). Three were
  filed on 2026-08-13, after the last green build; no code here changed.
  Building now needs Go 1.26.6.

## [0.2.3] - 2026-08-02

### Added
- Project documentation for outside contributors: `CONTRIBUTING.md`,
  `CODE_OF_CONDUCT.md`, `SECURITY.md`, this changelog, and GitHub issue and
  pull-request templates.
- A demo recording in the README, plus `docs/demo.tape` to regenerate it.

### Changed
- Dependabot's `github-actions` group is restricted to `minor` and `patch`,
  matching the `gomod` group. Majors still get a PR — their own, rather than
  batched with others.
- `namecom open` now rejects an argument that isn't a plausible domain name,
  instead of passing it through. `namecom open example.com` is unaffected.

### Security
- `namecom open <domain>` validates its argument before handing it to the
  platform browser opener. The argument was previously interpolated straight
  into a URL and passed to `open`/`xdg-open`/`rundll32`, which read a leading
  `-` as a flag — so `namecom open -e` supplied an argument to that program
  rather than opening a page. No shell was ever involved, so this was
  argument confusion rather than command injection.
- `gosec` added to the lint set, and golangci-lint's default output
  truncation disabled. `max-same-issues` defaults to 3 per distinct message,
  which had been hiding roughly a quarter of the findings on any run that
  produced several of the same kind.

### Fixed
- The documented location of the config file. `README.md` said
  `~/.config/namecom/config.yaml` on every platform, but the CLI uses
  `os.UserConfigDir()` — `~/Library/Application Support` on macOS and
  `%AppData%` on Windows. Only Linux matched. `namecom auth status` prints
  the path actually in use. No behavior change; credentials do not move.

## [0.2.2] - 2026-08-02

### Fixed
- `internal/api/gen/zz_generated.go` regenerated against `oapi-codegen`
  v2.7.1. Bumping the generator in 0.2.1 regenerated nothing, so the
  committed client had silently stopped matching its own generator.
- `GO-2026-5856` in the shipped release binaries, resolved by the
  accompanying dependency updates.

### Security
- All GitHub Actions in CI and release are pinned to commit SHAs instead of
  floating major tags. The release job holds the Homebrew tap token, so a
  repointed upstream tag would have run with credentials that reach other
  people's machines.
- CI runs `govulncheck ./...` on every PR — reachability-aware, so it
  reports only vulnerabilities this binary can actually reach.
- CI runs `make verify-generate`, failing the build when the committed
  generated client drifts from what the current generator produces.

## [0.2.1] - 2026-08-02

### Fixed
- Test-suite correctness only; no user-facing behavior changed. Several
  tests asserted only that no error was returned, and two URL-forwarding
  tests branched on `PUT` when the endpoint uses `PATCH` — so they served
  the pre-update record as the update response and could never fail.

## [0.2.0] - 2026-08-01

### Fixed
- A batch of bug fixes across the command surface. See
  [#9](https://github.com/patramsey/namecom-cli/pull/9) and
  [#10](https://github.com/patramsey/namecom-cli/pull/10) for the commits.

[Unreleased]: https://github.com/patramsey/namecom-cli/compare/v0.4.7...HEAD
[0.4.7]: https://github.com/patramsey/namecom-cli/compare/v0.4.6...v0.4.7
[0.4.6]: https://github.com/patramsey/namecom-cli/compare/v0.4.5...v0.4.6
[0.4.5]: https://github.com/patramsey/namecom-cli/compare/v0.4.4...v0.4.5
[0.4.4]: https://github.com/patramsey/namecom-cli/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/patramsey/namecom-cli/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/patramsey/namecom-cli/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/patramsey/namecom-cli/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/patramsey/namecom-cli/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/patramsey/namecom-cli/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/patramsey/namecom-cli/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/patramsey/namecom-cli/compare/v0.2.4...v0.3.0
[0.2.4]: https://github.com/patramsey/namecom-cli/compare/v0.2.3...v0.2.4
[0.2.3]: https://github.com/patramsey/namecom-cli/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/patramsey/namecom-cli/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/patramsey/namecom-cli/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/patramsey/namecom-cli/compare/v0.1.10...v0.2.0
