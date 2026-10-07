# Changelog

All notable changes to `namecom` are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this project
follows [Semantic Versioning](https://semver.org/).

Releases before `0.2.0` predate this file. Their notes are on the
[releases page](https://github.com/patramsey/namecom-cli/releases).

## [Unreleased]

### Fixed
- `email create` for a mailbox that already exists reported "Created … →
  <your --to>" and exited 0, though the API had changed nothing and the
  mailbox still forwarded to its old address (#283). It now fails (exit 1)
  with `mailbox info@example.com already forwards to old@example.org`, and
  suggests the `email update` command. **Scripts**: that case now exits 1
  with a JSON error envelope of type `conflict`, whose `details` is the
  existing entry, where it printed the entry and exited 0. The success line
  shows the address the API stored rather than the `--to` value.
- Email and URL forwarding say what they do to DNS (#286). name.com adds MX
  records (`mx3`–`mx8.name.com`) and an SPF record when the first mailbox is
  created, and an A record for an apex URL forwarding; deleting the
  forwarding leaves them. `email create`'s and `url create`'s help say so,
  and `email create`, `email delete` and apex `url delete` print a note on
  stderr naming the records and the `dns delete` command that removes them.
  No request is added to find out.
- `url create` and `url update` accepted `--title` and `--meta` on a
  redirect or 302 forwarding and stored them, where they do nothing (#286).
  They are now a usage error (exit 2) unless the forwarding is masked; an
  empty value is still allowed, to clear one left on a redirect. Switching a
  masked forwarding to another type warns that its title and meta are kept.
- `email update` and `email delete` on a missing mailbox said only "Not
  Found"; they now say `mailbox info@example.com not found — run 'namecom
  email list example.com' …`, as `dns` and `url` do, and still exit 4
  (#286). `email get` uses the same wording. The `email delete` prompt says
  where the mailbox forwards; it fetches the mailbox for that only when it
  will ask, so `--yes` sends one request as before.

## [0.5.1] - 2026-10-05

Follow-ups to 0.5.0. `namecom api` gains `-X` and works with `-i` and `--jq`
together; purchase prompts show the account balance; `auth login` offers to
open the token page. Fixes `-q` on lists, which ignored `--page` and
`--limit` and could walk an entire history one item per request.

Output a script might notice: `-o tsv` now covers `status`, `version`,
several-domain `domain get` and the `dns sync` plan; `dns list -q` prints
every page, as the other lists do; and dry-run `quote` objects gain
`balance`.

### Added
- `namecom api --include` works with `--jq` and `--fields` (#269). The status
  line and headers print as they came, ahead of the body, and the filter
  applies to the body alone, as with `gh api -i --jq`. The combination was a
  usage error. With `--paginate`, each page's headers print, then the merged
  body.
- `namecom api -X`/`--method` names the method, as `gh api` and `curl` do
  (#270): `namecom api -X DELETE /core/v1/…`. It takes any case and the same
  methods as the first argument. Giving both is a usage error (exit 2)
  unless they agree, and so is `--paginate` with `-X` other than GET. With
  `-X GET`, `-f` and `-F` are query parameters.
- Purchase confirmations show the account balance. `domain register`,
  `domain renew`, `transfer create` and the register offered by
  `domain check` end the line under the prompt with it:
  `production · profile work (acme-corp) · balance $120.00`. When the
  balance is below the price, a `!` warning says so before the prompt; the
  purchase is still offered, since the account may have another way to pay.
  The balance is looked up alongside the pricing, only when a prompt will be
  shown — never under `--yes` — and a failed lookup leaves it out without a
  word (#271).
- **Scripts:** `--dry-run` of those commands reports the balance too: the
  `quote` object in JSON and YAML gains a `balance` key (USD), absent when
  the lookup failed, and the "Would charge" line ends with it.
- `auth login` asks "Open the API token page in your browser?" before the
  form (default No), so someone without a token need not copy the URL out of
  the terminal. Yes opens it with the browser `namecom open` uses; if none
  opens, the URL is printed as a warning and the form follows. It is not
  asked with `--with-token` or `--token-cmd`, under `--yes`, `--dry-run`,
  `-q` or a structured `-o`, or off a terminal. The sandbox has no separate
  token page, so `--sandbox` opens the same one. (#272)

### Fixed
- `-o tsv` now covers the outputs it missed. **Scripts**: each of these
  changes what `-o tsv` prints. `status` and `version` print
  `field<TAB>value` rows, keyed by their JSON names, instead of their text
  report; `status`'s lists of domains are a JSON array in one cell.
  `domain get` with several domains, or `-`, prints one table — a header row
  and a row per domain — instead of a `field<TAB>value` block per domain, and
  its transfer lock is a plain date. The header names are the field names one
  domain's rows print, unchanged (`Domain`, `Renews at`, `Transfer lock`), so
  a script reads the same names for one domain or several (#268).
  `dns sync --dry-run` prints the plan as a row per change (action, type, host, answer, TTL, priority) instead of its
  text report, and a sync with nothing to change prints only its result,
  with `changed` now `false`.
- `-q` on a paged list honours `--page` and `--limit` (#277). It fetched
  every page regardless, so `order list -q --limit 1` walked the whole
  history one order per request, and `--page 3 -q` printed every page.
  An explicit `--page` or `--limit` now fetches that one page, as it does
  without `-q`, and when there are more, `--page N for more` goes to stderr;
  stdout is still one ID per line. `-q` alone still prints every page. This
  covers `domain`, `order`, `dns`, `email`, `url`, `vanity-ns` and
  `transfer list`, and `contact unverified`. **Scripts:** `dns list -q`
  without `--page` or `--limit` now prints every record, not just the first
  page, as the other lists already did.

## [0.5.0] - 2026-10-05

The result of a UX and automation review (#247). One documented JSON
contract and a new exit code for writes whose outcome is unknown; `--jq`,
`--fields` and `-o tsv` on every command; `dns sync`, and DNS commands that
can be run again safely; bulk input (`-` for stdin, several domains or record
IDs, `domain check` of any length); non-interactive `auth login` and
`NAMECOM_BASE_URL` for CI; `namecom api` with `--paginate` and field flags;
price previews and `--max-price`; and clearer prompts, errors, tables and
help throughout.

**This release changes what scripts see.** Read "Breaking for scripts"
first, then the entries below marked **Scripts**. In short:

- **JSON and YAML:** lists are always `{"data": [...]}`, keys are camelCase,
  writes report `changed`, the error envelope has a `type` and carries the
  hint (the top-level `hint` is deprecated), and warnings are part of the
  JSON on stderr.
- **Exit codes:** **6** for a write whose outcome is unknown; **2** for a
  missing required flag and for a write refused for want of `--yes`; **1**
  for a declined or cancelled prompt, which exited 0; **3** for `config show`
  without credentials.
- **New confirmations:** `domain lock off`, `domain privacy off`,
  `domain autorenew on` and `off`, and the matching `domain update` flags ask
  first, so scripts need `--yes`.
- **Premium prices:** premium, aftermarket, expiring and backorder purchases
  need `--accept-premium`; `--yes` alone no longer buys at those prices.
- **`NAMECOM_SANDBOX`:** an unrecognized value is an error. `yes` used to
  count as false and send requests to production.
- **Table mode:** a piped table is plain text, hints and counts go to stderr,
  error lines use `✗` and `→`, and several prompts and success messages are
  reworded.

### Breaking for scripts

JSON and YAML output now follow one documented contract — see "JSON
contract" in the README (#240). Each change below alters output a script may
parse; table output is unchanged.

- **Lists are always `{"data": [...]}`.** `domain check`, `domain search`,
  `config list-profiles` and `dns export` printed a bare array, so
  `dns list | jq .data` worked and `dns export | jq .data` failed.
  Before: `[{"domainName": "a.com", …}]`. After:
  `{"data": [{"domainName": "a.com", …}]}`. `config list-profiles` with no
  profiles printed nothing on stdout; it prints `{"data": []}`. `dns import`
  reads both shapes, so files exported by older versions still import.
- **`dns import --dry-run` wraps its plan the same way.** Before:
  `[{"dry_run": true, "method": "POST", …}, …]`. After:
  `{"dryRun": true, "data": [{"dryRun": true, "method": "POST", …}, …]}`.
- **Keys are camelCase.** The dry-run document's `dry_run` is `dryRun`, in
  every command that previews (`auth login`, `auth logout` and `config use`
  included). `status` renames six keys: `domains_total` → `domainsTotal`,
  `expiring_critical` → `expiringCritical`, `expiring_soon` → `expiringSoon`,
  `pending_transfers` → `pendingTransfers`, `expiring_domains` →
  `expiringDomains`, `pending_transfer_domains` → `pendingTransferDomains`.
- **Writes say whether anything changed.** The `{"success", "message"}`
  document gains `changed`. Before: `{"success": true, "message": "Transfer
  lock is already on for a.com; nothing to change"}`. After:
  `{"success": true, "changed": false, "message": "…"}` — and
  `"changed": true` for every write that did something. `domain lock`,
  `domain autorenew` and `domain privacy` report `false` when the domain was
  already in that state.
- **The error envelope says what kind of error it is, and the hint moved
  into it.** Before:
  `{"error": {"message": "Not Found"}, "hint": "check the name or ID for typos"}`.
  After:
  `{"error": {"type": "not_found", "status": 404, "message": "Not Found", "hint": "check the name or ID for typos"}, "hint": "check the name or ID for typos"}`.
  `type` is one of `usage`, `confirmation_required`, `auth`, `not_found`,
  `rate_limited`, `conflict`, `aborted`, `network` or `api`; `status` is the
  HTTP status when the API answered. The top-level `hint` is kept for this
  release only and is deprecated: read `error.hint`. A rejected
  `auth status` also puts its profile, username, endpoint and config file in
  `error.details`, which was only in the message.
- **The commands added in this release follow the same rules** (they never
  shipped in another shape, but their pull requests described one).
  `domain get` with several domains or `-` prints `{"data": [...]}`, not a
  bare array. A toggle over several domains, and `dns delete` with several
  IDs, print one `{"success", "changed", "message", "data": [...]}` document
  rather than one document per target. `dns sync --dry-run` lists its
  requests under `data`, not `requests`, as every multi-request dry run
  does; its result gains `changed`. `dns create --if-not-exists` adds
  `changed` to the record, and `dns delete --if-exists` and
  `dns import --skip-existing` report `"changed": false` when there was
  nothing to do. A rejected `auth status` puts where each credential came
  from in `error.details` (`usernameSource`, `tokenSource`, …), as the
  successful output does. The `NAMECOM_BASE_URL` notice and the TTL warning
  of `dns create --if-not-exists` are collected warnings, like any other.
- **Warnings are part of the JSON on stderr.** In JSON and YAML modes, a
  warning — the `--base-url` caution on every run, say — was a plain
  `! …` line on stderr, so stderr was not one parseable document. It is now
  in the error envelope's `warnings` array, or, when the command succeeds, a
  `{"warnings": ["…"]}` document on stderr. Before:
  `! --base-url is set: …` followed by the error envelope. After:
  `{"error": {…}, "warnings": ["--base-url is set: …"]}`.
- **Nothing is HTML-escaped.** A TXT record's `"a<b & c>d"` came out with
  `<`, `>` and `&` as `\u` escapes; it prints as written. Both forms decode
  to the same string, so only a script matching the raw text is affected.
- **A write whose outcome is unknown exits 6, and names its idempotency
  key** (#243). When a request that changes something got a 5xx, or timed
  out or lost its connection after it was sent, the CLI exited 1 with
  `Internal Error`, and the `X-Idempotency-Key` it had sent was never shown,
  so `--idempotency-key` helped only if it had been pinned in advance.
  Before: exit 1, `{"error": {"message": "Internal Error"}, "hint": "name.com
  failed while handling this change, …"}`. After: exit 6,
  `{"error": {"type": "api", "status": 500, "message": "Internal Error", "hint": "outcome unknown; re-run with --idempotency-key 1d5973ca-… — but check first whether the change was made, since most endpoints ignore the key", "idempotencyKey": "1d5973ca-…"}, …}`.
  Table mode shows the same hint. A script that treated every non-zero exit
  other than 2–5 as 1 should handle 6. This applies whether or not the
  command marked the request as a write: it is decided from the HTTP
  method, so a read's 5xx still says it is safe to retry and a write's
  never does. The README's new "Idempotency keys" section lists which
  endpoints honour the key — five declare it, and the API documents its
  effect only for `order refund` — and says that the rest ignore it.

### Added
- `--fields a,b,c`, a global flag, keeps only those keys of each list item,
  or of the object a command prints, in that order. A list keeps its
  `{"data": [...]}` envelope. It works with every `-o`: in a table or TSV
  the fields are the columns, so
  `domain list --fields domainName,expireDate -o tsv` prints two columns.
  A field that no item has is a usage error (exit 2) listing the fields
  there are (#241).
- `--jq <expr>`, a global flag, filters the JSON document `-o json` would
  print with an embedded jq ([gojq](https://github.com/itchyny/gojq)), so
  jq need not be installed. A string result prints without quotes, like
  `jq -r`; anything else as compact JSON, one result per line. A malformed
  expression, `--jq` with `-o table`, `yaml` or `tsv`, and `-q` with
  `--jq` or `--fields` are usage errors, caught before anything is sent.
  Errors still go to stderr in the error envelope (#241).
- `-o tsv`: a table's columns as tab-separated values, with a header row
  unless `--no-header`, no colour, dates without the relative phrase, and
  tabs, line breaks and backslashes in a value escaped as `\t`, `\n`, `\r`
  and `\\`. A command that shows one object prints `field<TAB>value` rows
  (#241).
- `namecom help formatting` documents output formats, `--fields`, `--jq`
  and TSV, with examples.
- `domain check` takes any number of names. The API answers at most 50 per
  request, so a longer list is sent 50 at a time, one batch after another,
  and the results come back as one table (or one `{"data": [...]}` list)
  in the order the names were given. 120 names used to fail with "number of items must
  be less than or equal to 50".
- `dns delete <domain> <id>...` takes several record IDs. Every record is
  fetched first (a missing one fails before anything is deleted), one
  confirmation lists them all, and they are deleted in order; the first
  failure stops the rest, and the error says how many were deleted before
  it. Each deleted record still gets its own `Deleted record …` line; in
  JSON and YAML they are one document, with one item per record under
  `data`.
- `domain check --exit-status` exits 1 when any name checked is not
  available, after printing the results as usual, so
  `namecom domain check --exit-status x.com && …` needs no output parsing.
  Without the flag, `check` still exits 0 whatever it finds — including
  with `-q`, which prints only the available names.
- The README explains that the 10 requests/second limit is per process, so
  `xargs -P` multiplies it past the API's own limit, and shows the
  one-process alternatives.
- `-` as an argument reads domain names from stdin, one per line, for
  `domain check`, `domain get`, `domain lock`, `domain autorenew` and
  `domain privacy`; blank lines and `#` comments are skipped. So
  `namecom domain list -q | namecom domain check -` works, where `-` used
  to be rejected as a domain name.
- `domain get`, `domain lock`, `domain autorenew` and `domain privacy` take
  several domains: `namecom domain lock on a.com b.com`. A toggle reads
  each domain first, skips any already in the requested state, and asks
  once, listing every domain it will change; it stops at the first failure
  and says how many were changed before it. **Scripts**: `domain get` with
  more than one domain, or with `-`, prints a `{"data": [...]}` list; with
  one domain named on the command line it prints the same single object
  as before. A toggle over several domains prints one document, with
  `changed` true when any domain changed and one item per domain under
  `data`.
- `namecom dns sync <domain> --file <file>` makes a domain's records match a
  file — the JSON `dns export` writes, or a BIND zone file such as
  `dns export --zone` writes. It prints a plan of creates, updates (a TTL or
  priority change) and deletes, asks once, and applies it creates first.
  Deleting needs `--prune`, and `--prune` never touches NS records at the apex
  or CAA records; `--prune-all` does. An empty file is refused with either.
  `--dry-run` prints the plan, as one
  document with `-o json`, with the requests it would send under `data`. A
  failure stops the run and reports what was applied and what was not;
  running sync again picks up from the live zone, and a run with nothing to
  change sends nothing. The result document's `changed` says whether
  anything was applied. A change that failed with a 5xx, or lost its
  connection after it was sent, is marked `outcomeUnknown` and exits 6, with
  a hint to run sync again.
- `dns import` reads BIND zone files as well as JSON, and `--skip-existing`
  skips records already in the zone instead of stopping at the first one, so
  a partly applied import can be run again. With it, `--dry-run` previews
  only the records that would be created.
- `dns create --if-not-exists` exits 0 and prints the existing record's ID
  when a record with the same host, type and answer is already there. In
  JSON and YAML the record carries `"changed": false` then, and
  `"changed": true` when it was created.
  `dns delete --if-exists` exits 0 when the record is already gone (a missing
  domain still exits 4), reporting `"changed": false`. With several IDs it
  skips the ones already gone, with a note, and deletes the rest.
  `dns import --skip-existing` reports `"changed": false` when every record
  was already there.
- `dns list --host <host>` lists only the records at that host; `@` or the
  domain itself means the apex.
- A dry run of `domain register`, `domain renew` or `transfer create` now
  says what the real run would charge. Table mode ends with a line on stderr,
  `Would charge: $39.98 (2 years) · sandbox · profile default`; JSON and YAML
  add a `quote` object (`total`, `currency`, and `years` or `note` where they
  apply) beside the unchanged `body`. A renewal's body carried only
  `{"years": 2}`, and a standard registration's no price at all. **Scripts**
  reading the dry-run document get one new key; nothing else in it changed.
- Aliases: `ls` for every `list`, `rm` for every `delete`, and `add` for
  `create` in `dns`, `dnssec`, `email`, `url` and `vanity-ns`.
  `config list-profiles` also answers to `config profiles` and `config ls`;
  the old name stays, so scripts that call it keep working.
- Words typed in place of a top-level command now get the command they
  meant: `namecom records` suggests `namecom dns`, `redirect` and `forward`
  suggest `namecom url`, `login` and `logout` suggest `namecom auth login`
  and `auth logout`, and `whoami` suggests `namecom auth status`. They used
  to fail with no suggestion.
- With `-o json` or `-o yaml`, an unknown-command error lists the commands
  it was probably meant to be in `error.suggestions`, as full command lines
  (`["namecom dns delete"]`). **Scripts** get one new key; `message` and
  `hint` are unchanged.
- `namecom help environment` lists every environment variable namecom
  reads (`NAMECOM_USERNAME`, `NAMECOM_TOKEN`, `NAMECOM_PROFILE`,
  `NAMECOM_SANDBOX`, `NAMECOM_CONFIG`, `NAMECOM_NO_UPDATE_NOTIFIER`, plus
  `NO_COLOR`, `CLICOLOR_FORCE` and `BROWSER`) and what overrides each. They
  were documented only in the README. `--sandbox` now names
  `NAMECOM_SANDBOX` in its help, as `--profile` and `--token` already did.
- `auth login` works without a terminal: `--username alice --with-token`
  reads the token from standard input, and `--username alice --token-cmd
  '<command>'` saves a credential helper instead of a token. Both check the
  credentials with the API first, as the interactive login does, and fail
  rather than save when the check cannot be made; `--no-verify` saves them
  unchecked (and does not run the helper). `--profile`, `--sandbox` and
  `--dry-run` apply, and replacing an existing profile needs `--yes`.
  `auth login --token <t>` is refused, since a token on the command line is
  kept in shell history; it used to be ignored silently.
- `NAMECOM_BASE_URL` points every command at another API base URL, as
  `--base-url` does, with the same validation and the same warning when it
  is not name.com. `--base-url` wins when both are set.
- `config show` and `auth status` say where each value came from — `flag
  --username`, `env NAMECOM_TOKEN`, `profile work`, `token_cmd` — as a dimmed
  note in the table and as sibling keys in JSON and YAML (`profileSource`,
  `usernameSource`, `tokenSource`, `endpointSource`; `auth status` also
  `environmentSource`). **Scripts** get new keys only; every existing key
  keeps its name and string value. `auth status`'s table gains a Token row,
  masked.
- The README has a CI section with a GitHub Actions example.
- `namecom api` takes the flags `gh api` users reach for (#245):
  - The method may be left out: `namecom api /core/v1/domains` is a GET,
    and a request with a body is a POST. `namecom api GET /path` still works.
  - `--paginate` follows `nextPage` and prints one document whose lists hold
    every page's items, without `nextPage` and `lastPage`; `--jq` filters
    that merged document. A failed page prints nothing. It is GET only.
  - `-f key=value` and `-F key=value` build a JSON body: `-f` values are
    strings, `-F` keeps `true`, `false`, `null` and numbers as JSON and reads
    `@file` (`@-` for stdin). `a[b]=c` nests and `a[]=x` appends to a list.
    On a GET or HEAD they are query parameters.
  - `--input <file>` (or `-` for stdin) reads the body from a file.
  - `-i`/`--include` prints the response's status line and headers before
    the body.

  Two ways of giving the body at once, `--paginate` on anything but a GET,
  and `--include` with `--jq` or `--fields` are usage errors (exit 2).
  Writes are previewed under `--dry-run` as before, whether or not the
  method was named.

### Changed
- The `domain register` guided form now starts with WHOIS privacy and
  auto-renew turned on. Privacy is free at name.com, and a lapsed domain is
  the costlier mistake. The `--privacy` and `--autorenew` flags still default
  to off, so `--yes` and scripts register exactly what they ask for.
- Help pages show the global flags that apply to the command:
  `--dry-run` and `--yes` on writes, `--quiet`, `--wide` and `--no-header`
  on lists, and `--output` everywhere. Read-only `domain get` listed
  `--dry-run` and `--yes`, and no list mentioned `--wide`. Root help lists
  its flags under Output, Credentials and Advanced headings instead of one
  block of 19. A group's page (`namecom dns --help`) no longer has an `-h`
  flags block, global flags or two footers; every page ends with one
  "Learn More" footer.
- Help wording is consistent. The `url` pages call the feature "URL
  forwarding" throughout, name.com's own term and its API path (the group
  page said "URL redirects"), and `--type` spells out that `redirect`, the
  default, is a 301. `dnssec` is "Manage DS records at the
  registry (DNSSEC)", with "Add/Remove a DS record"; it claimed to enable
  signing, which the DNS host does. `transfer cancel` says it cancels a
  transfer in, `cancel-outbound` that it stops a domain leaving, and
  `internal-in` which way the domain moves. `contact` and `domain contacts`
  point at each other. `status`, `open` and `auth login` use the imperative
  like every other page, and the create, delete and update pages of `dns`,
  `dnssec`, `email`, `url` and `vanity-ns` say more than their one-line
  summary. Examples for `order refund`, `domain set-ns`,
  `domain contacts set` and `dns delete` lead with the plain form; the
  `--yes` form comes second, labelled for scripts. `dns create --type`
  says CAA is read-only through the API, and `--profile` on `auth login`
  and `auth logout` says it overrides the global `--profile`.
- The `--limit` help on `url list` reads "forwarding entries per page".
- Dependencies: `golang.org/x/net` 0.59.0 and `golang.org/x/text` 0.42.0.
  `github.com/itchyny/gojq` (MIT) is new, for `--jq`, and adds about 0.8 MB
  to the binary.
- The `domain register` prompt reads as one sentence and states the choices
  it is sent with: "Register acme.io for 2 years: $35.98 total (renews at
  $17.99/yr), with WHOIS privacy, without auto-renew?". It read "for 2 years at
  $35.98 total for 2 years" and never mentioned privacy or auto-renew. The
  register offer in `domain check` uses the same wording. **Scripts** matching
  the prompt text (it appears in the non-interactive "pass --yes" error) need
  updating.
- The `transfer create` prompt says what the price covers and that privacy
  is free: "Transfer acme.io in for $12.99 (covers the TLD's minimum term,
  typically 1 year), with WHOIS privacy at no charge?". It said "plus WHOIS
  privacy", which read as an extra charge. **Scripts** matching the prompt
  text need updating.
- `transfer cancel` looks the transfer up before asking, and fails with
  not-found (exit 4) without prompting when there is none. The prompt now
  includes its status: "Cancel transfer of acme.io (status: pending_transfer)?".
- `order list` and `order get` show what each order bought. The columns are
  now `DATE | DOMAIN(S) | TYPE | TOTAL | STATUS | ID`, where DOMAIN(S) is the
  first item's name with a count of the others (`acme.io +2`) and is never
  hidden to fit the terminal; they were `ID | STATUS | DATE | TOTAL`.
  `order get` suggests `order refund` only when an item is refundable, and
  names those items. **Scripts** splitting the plain table by column position
  need updating; JSON and YAML are unchanged.
- The `order refund` prompt names what is refunded and for how much:
  "Refund $35.98 for acme.io registration (order 2142141, item 1)? This
  cannot be undone." It read "Refund order 2142141, items [1]?". To word it,
  the command fetches the order when it is about to ask (not under `--yes` or
  `--dry-run`), and fails before asking when the order does not exist (exit
  4) or has no such item (exit 2). **Scripts** matching the prompt text need
  updating.
- `domain search` and `domain check` have a RENEWS column with the yearly
  renewal price, between PRICE and PREMIUM, so a cheap first year that renews
  at much more is visible before buying. A premium name's PRICE cell no
  longer repeats the renewal price in brackets. **Scripts** splitting the
  plain table by column position need updating; JSON and YAML are unchanged.
- `domain pricing` has a heading, `example.org — per term (1 year for most
  TLDs)`, with `(premium)` after the name for a premium domain. The PRICE
  column no longer has a `Premium  no` row. **Scripts** reading the table
  need updating; JSON, YAML and `-q` are unchanged.
- `domain get` shows three more rows when the API returns them: Renews at
  (the renewal price), Transfer lock (`until 2026-11-28 (in 2 months)`, while
  the post-registration or post-transfer lock is in force) and Registrant
  (name, company, and whether its email is verified). **Scripts** reading the
  `Key  value` lines by position need updating; JSON and YAML are unchanged.
- `dns delete` and `url delete` fetch the record first and show it in the
  prompt — "Delete A www → 1.2.3.4 (TTL 300) from example.com?", "Delete URL
  forwarding go.example.com → https://acme.io (redirect) from example.com?" —
  instead of only its ID. A record that does not exist now fails with
  not-found (exit 4) before any prompt, under `--yes` and `--dry-run` too.
  **Scripts** matching the prompt text need updating.
- `dnssec delete` warns, before asking, what removing a DS record can do: if
  other DS records remain and none matches a key the DNS host signs the zone
  with, validating resolvers fail to resolve the domain; with none left,
  validation simply stops. Shown in table mode only, on stderr.
- **Scripts — exit code:** a missing required flag now exits 2 (usage error)
  everywhere. `url create`, `url update`, `email create` and `email update`
  without `--to` off a terminal exited 1, as did `vanity-ns create
  --hostname ""`; `transfer create` already exited 2. The refusal of a write
  that needs `--yes` off a terminal also exits 2 instead of 1, and is worded
  as a statement: `confirmation required for "Delete …?" (production · …) —
  pass --yes to confirm when not running in a terminal`. Flags a terminal
  prompts for say so in their help: "(required; prompted in a terminal)".
- **Scripts — exit code:** declining a confirmation, or cancelling any prompt
  or form with Ctrl-C, now exits 1 and prints `✗ aborted` on stderr (an error
  envelope in JSON and YAML mode), so a declined `dns delete` can be told from
  a completed one. A decline used to print `! aborted` and exit 0, and so did
  Ctrl-C in the `dns create`, `url`, `email`, `transfer` and `auth login`
  forms, while Ctrl-C in the `domain register` form exited 1. Declining
  `auth login`'s offer to replace a profile exits 1 with
  `aborted: profile "…" left unchanged`. The one exception is the register
  offer after `domain check`: the check itself succeeded, so saying no exits
  0, with a note that nothing was registered.
- **Scripts:** error lines use the status symbols in table mode: `✗ <message>`
  and, when there is advice, `→ <hint>` on the next line. Without colour they
  read `error: <message>` and `  hint: <hint>`, the only output that did not
  use the symbols; with colour the hint was a dim `  hint:` line. Scripts
  matching `error:` on stderr need updating. The JSON and YAML error
  envelopes are unchanged.
- `domain contacts set` takes the contacts file as `--contacts-file`, the name
  `domain register` and `transfer create` already use for the same JSON.
  `--from-file` still works but is hidden from help and prints a deprecation
  notice on stderr.
- `domain lock`, `domain autorenew` and `domain privacy` take the domain and
  `on`/`off` in either order, so `namecom domain lock example.com on` works
  like every other command that takes the domain first. Shell completion
  offers `on`/`off` or domains to match.
- Help shows the booleans whose `false` matters — `domain update --autorenew`,
  `--privacy` and `--lock`, `domain register --privacy` and `--autorenew` — as
  `--autorenew=true|false`, and `domain update` has an `=false` example.
  `--autorenew false` sets the flag to true and leaves `false` as an argument;
  that mistake now gets a hint naming `--autorenew=false` instead of only
  "too many arguments".
- **Scripts — exit code:** `domain list` rejects arguments with exit 2, like
  the other list commands. `domain list --all false` used to ignore the
  `false` and list everything.
- Every paged list — `domain`, `dns`, `email`, `url`, `vanity-ns`, `transfer`
  and `order list`, and `contact unverified` — takes `--page` (the page to
  fetch, from 1), `--limit` (results per page) and `--all`, described the same
  way everywhere. Only `domain list` had `--page`, and none could set the
  page size. A list that stops early ends with
  `--page N for more, --all for everything`, as `domain list` already did.
  **Scripts — exit code:** `--page 0` and a negative `--limit` exit 2; `domain
  list --page 0` exited 1.
- `domain list --sort` lists the domain properties it can sort by in its
  help. Any other value is still passed to the API.
- Shell completion offers the values of every flag that takes one of a fixed
  set: `--type` on `dns list`, `dns create`, `dns update`, `url create` and
  `url update`, `order list --status`, `domain claims --purchase-type`, and
  `domain list --sort` and `--sort-dir`. TAB completed filenames there.
- The line under a confirmation no longer says "sandbox" twice: a sandbox
  prompt is tagged `[sandbox]`, and the line now names only the profile and
  account. Under `--base-url` it reads `base URL overridden: <url>` instead of
  production or sandbox, which described only where the credentials came
  from. The same text appears in the refusal off a terminal.
- `--max-price` under `--dry-run` previews the request and warns that the
  real run would refuse it, as the premium gate already did. It used to fail
  the dry run with exit 2, so the request could not be seen.
- Tables that are too wide for the terminal now cut their longest values
  short with `…` (to no less than 20 characters) before hiding any column, and
  never hide the column that carries the point of the table: the DNS answer in
  `dns list`, the domains in `contact unverified`, the item name in
  `order get`. `dns list` with an SPF or DKIM record used to show only ID and
  HOST; `domain list` at 80 columns lost locked, privacy and auto-renew to one
  long domain name. The footer says when values were cut. `--wide` and piped
  output are unchanged: every column, every character.
- **Scripts:** `-o table` with stdout piped or redirected now prints a plain
  table — columns aligned with spaces, no borders — like `gh` does, so `awk`
  and `cut` can split it. Detail views (`domain get`, `auth status`) print
  `Key  value` lines the same way. In a terminal tables keep their borders.
- **Scripts:** in table mode, hints (`→ Run …`), list counts, "No … found"
  messages and the hidden-columns footer now go to stderr, so
  `namecom domain list -o table > domains.txt` saves only the table. The count
  reads `2 domains` rather than `(2 domains)`, and a paginated list prints one
  footer instead of two: `Showing 1–250 of 6,522 domains · --page 2 for more,
  --all for everything`, short enough for 80 columns. JSON and YAML output is
  unchanged.
- `dns list` shows the PRIORITY column only when an MX or SRV record is
  listed; it was always empty for A, CNAME and TXT records. Every table now
  shows a missing value as `—` rather than a blank cell (it was blank in some
  tables and `—` in others), and `order get` shows REFUNDABLE as `yes`/`no`
  where a non-refundable item used to show `—`. **Scripts** splitting a plain
  table on whitespace no longer see later fields shift left when a value is
  missing.
- Less colour, and four status symbols. Yes/no values are plain `yes` and
  `no` rather than a bold green `✓ yes` or red `✗ no`, so a long domain list is
  no longer a column of green and harmless values (Premium no, Privacy no) are
  no longer red. Colour is kept for what needs action: expired and
  soon-expiring dates, `Locked: no`, failed and pending statuses. DNS record
  types are no longer drawn on coloured backgrounds, and `domain check` shows
  a taken name as `taken` rather than a red `✗ taken`. `✓` means success, `!`
  a warning, `✗` an error and `→` a next step; the sandbox note in
  `domain check` is dim text instead of a `→` line, and the `==>` lines in
  `domain register` and `domain renew` are now spinner text. The insecure
  config-permissions warning starts with `!` instead of `warning:`.
- Dates and numbers read the same everywhere. Relative times use days under
  60, months under 24 and years beyond, so nothing reads "in 24 months";
  `status` says an expired domain "expired 2 years ago", as `domain list`
  does, rather than "expired 804 days ago", and its expiring list reads
  "(in 3 days)". The transfer-lock refusal from `domain lock off` shows
  "until 2026-11-28 (in 2 months)" instead of the API's raw timestamp. Prices
  and counts have thousands separators (`$100,000.00`, `6,522 domains`), and
  plurals are spelled out: "Register example.com for 1 year", "Refunded
  $29.98 for 2 items", "Imported 3 records". **Scripts** matching prompt,
  success or error text containing large prices or `(s)` plurals need
  updating; JSON and YAML values are unchanged.
- Success lines say what happened: `dns create` prints
  `Created A www.example.com → 192.0.2.10 (id 12345)`, `dns update` lists each
  changed field (`answer 192.0.2.1 → 192.0.2.2, ttl 300 → 600`), `url update`
  does the same, and `domain update`, `domain set-ns`, `domain contacts set`,
  `vanity-ns create/update` and `dnssec create` name the values they set.
  `domain renew` names the new expiry date when the API returns it. The
  "Run 'namecom … list'" and "… get to confirm" hints after every write are
  gone. `domain get` suggests renewing an expired domain, or one expiring
  within 30 days without auto-renew, instead of always suggesting `dns list`.
  **Scripts** matching the old success text need updating.
- Warning boxes wrap to the terminal width instead of overflowing it; the
  `contact unverified` box was 86 columns wide and broke apart at 80. Its
  wording follows the deadlines: "Verification deadline passed — the registry
  may suspend or lock these domains at any time" when they have passed,
  rather than "may be LOCKED … after the deadline". `transfer eligibility`
  shows REGISTERED AT (`another registrar` or `name.com (an account)`) instead
  of "AT NAME.COM no" beside "SUPPORTS INTERNAL yes", and shows the TLD's
  internal-transfer support only for a domain already at name.com.
- `auth login --help` and the login form say where to create an API token
  (https://www.name.com/account/settings/api) and that sandbox credentials are
  separate, with usernames that usually end in `-test`.
- With no credentials configured, every command — `auth status`, `status`,
  `config show` and API commands alike — now says `Not logged in. Looked in
  <path>.`, naming the config file it searched, and exits **3**. The hint
  says how to log in instead of suggesting `namecom auth status`, which on
  `auth status` pointed at itself. `config show` exited **1** for this and
  for a missing or ambiguous profile; it now exits **3** like the rest, so a
  script branching on its exit code sees a change. The old text was `no
  credentials configured — run 'namecom auth login' …`.
- A bare `namecom` with no credentials prints a short getting-started banner
  (`namecom auth login`, and where to create a token) instead of the full
  command list. `namecom --help` still prints the full help.
- When name.com rejects the credentials typed into `auth login` (HTTP 401 or
  403), it explains why and asks "Try again?", reopening the form with the
  username kept, instead of exiting. Declining, `--yes` and `--dry-run` exit
  **3** as before, and nothing is saved until a check succeeds.
- `auth login` asks before replacing the credentials of a profile that
  already exists; `--yes` replaces them without asking. After logging in to
  a profile that is not the one commands use, the hint says `namecom status
  --profile <name>` or `namecom config use <name>`, rather than a bare
  `namecom status` that showed a different account.
- When `NAMECOM_SANDBOX` sends a profile's requests to the other endpoint — a
  leftover `NAMECOM_SANDBOX=1` turning a production profile into a sandbox
  one, or the reverse — a one-line notice on stderr says so and names the
  endpoint in use. It appears only when stderr is a terminal, so scripts and
  JSON error output are unaffected, and not when `--sandbox` was passed.
- The Homebrew formula installs shell completions for bash, zsh and fish.
  The README's completion instructions now write to directories in your home
  directory instead of `/etc/bash_completion.d` and `${fpath[1]}`, which
  usually need root. `namecom completion` no longer checks for updates.
- The new-release notice prints the command that upgrades your copy —
  `brew upgrade namecom` for a Homebrew install, `go install
  github.com/patramsey/namecom-cli@latest` for one in `GOBIN` or
  `GOPATH/bin`, or the releases page for a downloaded binary — instead of
  only pointing at the releases page. `NAMECOM_NO_UPDATE_NOTIFIER=1` turns
  the notice off, and with it the daily release check.

### Fixed
- `config show` with credentials only in the environment — `NAMECOM_USERNAME`
  and `NAMECOM_TOKEN`, no config file — reported "Not logged in" and exited 3
  while API commands worked. It now resolves credentials exactly as they do,
  and fails only when they would, with the same error; a profile with a
  username but no token is now reported as not logged in, as API commands
  report it.
- Hints no longer send a CI job to `auth login` when its credentials came
  from the environment. A 401 says "check NAMECOM_TOKEN" (or the flag or
  variables actually used), and a token set without a username, or the
  reverse, names the missing variable. `auth status`'s rejection message
  names where the username and token came from.
- `auth login` without a terminal and without `--with-token` or
  `--token-cmd` now exits 2, as a usage error, and names those flags; it
  exited 1.
- `-o table` and `-o yaml` are honoured for errors that happen before the
  command line is parsed: an unknown top-level command
  (`namecom bogus -o table`), or an unknown flag placed before `-o`. In a
  pipe those printed the JSON envelope regardless. **Scripts** that pass
  `-o table` and parsed that envelope anyway get the text form now.
- Help honours `--color`: `--help --color=never` printed colour escapes
  wherever colour was otherwise on, and `--color=always` was ignored in a
  pipe. Help also wraps descriptions and flag help to the terminal width
  (or `$COLUMNS` when not a terminal) instead of running past the edge;
  examples stay one line each so they can be copied.
- The `url create` and `url update` forms check the destination as you type
  it: it must be an `http://` or `https://` URL with a host. Anything else
  used to get through the form and then fail with an error naming `--to`, a
  flag that had not been typed, losing the input. The create form asks where
  `example.com` (or `www.example.com`) should forward to, instead of
  `example.com/@`.
- `domain register`'s guided form shows the quoted price — first year and
  renewal, or the flat price of an aftermarket purchase — before asking for
  the options; it showed none until after the form. The form no longer opens
  under `--dry-run`, which previews the flag defaults, or with `-o json`,
  `-o yaml` or `--quiet` in a terminal, which now fail at once with a usage
  error (exit **2**) saying to pass `--years` (with `--privacy` and
  `--autorenew` as wanted) or `--yes`, before any request is sent.
- `dns create` in a terminal opens its guided form again when `--type` or
  `--answer` is left out. The form had been unreachable: both flags were
  marked required, so the command failed with `required flag(s) "answer",
  "type" not set` before it could ask. The form now checks host, answer and
  priority as you type them (priority must be 0–65535), and Ctrl-C at any
  step, including the MX/SRV priority step, sends nothing and exits 1 as an
  abort (see Changed). Without a terminal a missing `--type` or `--answer` is still a
  usage error (exit **2**); the message now reads `required flag(s) "type",
  "answer" not set — pass them, or run in a terminal for the guided form`.
- `namecom api` reads a piped stdin as the request body when `--data` is not
  given, so the help's `echo '{…}' | namecom api POST …` example works; it
  sent an empty body before, and `--dry-run` showed none. GET and HEAD never
  read stdin, and neither does a terminal or the null device, so
  `</dev/null` in CI does not block. An empty stdin, with or without
  `--data -`, now sends no body and no `Content-Type` rather than an empty
  one. `--data ''` sends no body without reading stdin.
- Requests cancelled by the CLI itself are no longer retried or announced.
  When one of `status`'s parallel requests failed (a bad token, say), each
  of the others printed "retrying (attempt 1, waiting 1s)…" for a retry that
  never happened. Real retries (429, 5xx, dropped connections) now show in
  the spinner's text — `Fetching domain… rate limited, retrying in 2s (2/3)` —
  instead of a line printed over it. Without a spinner they are still one
  line on stderr, now worded the same way; a script matching the old
  "retrying (attempt N, waiting …)" text needs updating.
- Every write confirmation now shows one line under the question saying
  which environment, profile and account it acts on, for example
  `production · profile work (acme-corp)`. A production purchase used to read
  only "Register x?", with nothing to say which account would pay. The
  refusal off a terminal carries the same text in brackets, so a script matching that message exactly will see it change.
- Domain setting changes now confirm according to their risk.
  `domain lock off`, `domain privacy off`, `domain autorenew off` and
  `domain autorenew on` (which commits the account to future renewal charges)
  ask first, and so do the matching `domain update` flags; each prompt says
  what the change does. `domain privacy on` and `domain update --privacy=true`
  no longer ask, since they never charge. **Scripts that run any of the newly
  prompted changes must now pass `--yes`**, or they stop with
  "confirmation required … — pass --yes to confirm when not running in a
  terminal" (exit **2**). A change the domain already has still
  sends nothing and does not ask. `domain update --autorenew`,
  `--privacy=false` and `--lock=false` read the domain first to decide whether
  to ask.
- `domain register`, `domain renew` and `transfer create` take
  `--max-price <amount>`. It refuses, before anything is bought, a price
  above the amount (the total for the term), exiting **2** with both figures
  in the message. It refuses too when no price could be quoted. Under
  `--dry-run` it previews the request and warns instead (see Changed). `--price` was described as a way to cap what you pay, but it
  is only sent as `purchasePrice`; its help now says so and points at
  `--max-price`.
- Premium, aftermarket, expiring and backorder purchases, premium renewals
  and premium transfers now need `--accept-premium` when there is no
  interactive prompt to answer. **`--yes` alone no longer buys at one of
  these prices**: a script gets exit **2** and a message with the price.
  `--yes` does not cover it, as with `--acknowledge-claim`. In a terminal
  without `--yes`, the purchase prompt, which quotes the price, still counts
  as acceptance. `--dry-run` shows a hint instead of failing.
- `NAMECOM_SANDBOX` accepts `yes`/`no`, `on`/`off` and `y`/`n` in any case,
  as well as `true`/`false` and `1`/`0`. Any other value used to count as
  false, so `NAMECOM_SANDBOX=yes` sent requests to **production**, even over a
  profile saved with `sandbox: true`. An unrecognized value is now a usage
  error (exit **2**) naming the variable and value, and nothing is sent.
  `config show` reports the same error.
- `auth login` checks the credentials with the API before saving them, and
  says "Logged in to production as alice (profile default)". Credentials the
  API rejects are not saved: the command exits **3** and, when the username
  and the environment look mismatched, says that sandbox credentials are
  separate and usually end in `-test`. If the API cannot be reached it asks
  whether to save them unverified; with `--yes` it refuses. `--dry-run` runs
  the check too, and still writes nothing. Leading and trailing whitespace is
  trimmed from the username and token; a pasted token with a trailing space
  used to be saved with it.
- Errors say what to do once. `domain get nope.com` printed its own "run
  'namecom domain list'" and then a generic "check the domain name or ID"
  hint; an error that already says what to do now has no `hint:` line (and
  no `hint` key in the JSON/YAML envelope). `order get`, `email get`,
  `url get`, `vanity-ns get`, `dnssec get`, `domain lock`, `autorenew`,
  `privacy`, `contacts get` and `update` name what was not found
  (`order 1 not found — run 'namecom order list' …`) instead of printing
  `Not Found`; they still exit **4**. A missing
  `--profile` is one line, `profile "x" not found in … (available: a, b)`,
  with `auth login --profile x` in the hint. A script matching the old
  messages needs updating.
- Error hints fit the status and whether the command was changing
  something. A 403 (such as "IP not whitelisted") no longer says to run
  `auth login`, which cannot fix it; it says the account lacks permission or
  the API is not accepting your IP address. A 401's "sandbox uses a separate
  API token" note now appears only against the sandbox, and in the hint: it
  is no longer part of the error `message` in JSON output. Every 5xx gets
  the same hint: a read is told nothing changed and retrying later is safe,
  a write that the change may or may not have been made. A read that gets an
  unreadable 200 is no longer warned that a change may have been made. Exit
  codes are unchanged; the `hint` text in the JSON/YAML envelope differs.
- An HTML error page from a proxy is reduced to the status and the page's
  title, `HTTP 502 Bad Gateway (HTML error page)`, instead of up to 400
  characters of markup. The title is kept when it adds something:
  `HTTP 503 Service Unavailable: Down for maintenance (HTML error page)`.
  This is the error `message` in JSON output too.
- Timeouts and connection failures read as one plain line, without Go's
  `Get "https://…":` prefix. A timeout says `request to api.name.com timed
  out after 30s`, with a hint to raise `--timeout`, instead of `context
  deadline exceeded (Client.Timeout exceeded while awaiting headers)`; a
  refused connection says `could not connect to 127.0.0.1:1: connection
  refused`, and an unknown host `could not look up <host>: no such host`.
  When `--base-url` points away from name.com the hint says to check it.
  These still exit **1**; a script matching the old text needs updating.
- Usage errors suggest the fix. `domain set-ns D ns1 ns2` gives the command
  rewritten with `--ns ns1,ns2`; `domain lock example.com on` gives the
  right order, `domain lock on example.com`; an unknown flag names the
  nearest flags (`--nameservers` → `--ns`, `--sandbx` → `--sandbox`) and the
  usage line; an unknown command with no near miss, such as `dns rm`,
  points at `namecom dns --help`; and too many arguments shows the usage
  line. An unknown command's "Did you mean this?" list moves from the
  message, where it took three more lines, into the hint: `did you mean
  'namecom domain'?`. All still exit **2**.

## [0.4.9] - 2026-10-03

One new feature and the last fixes from the bug hunt. `transfer create` and
`transfer internal-in` can now set the contacts a domain gets when it lands in
your account, using the SDK's new support for it.

Some output a script might notice changes:

- `domain lock`, `domain autorenew` and `domain privacy` exit **0** without
  sending anything when the domain is already in the requested state.
- `domain pricing` reports the acquisition price for aftermarket, expiring
  and backorder names. JSON/YAML gain `purchaseType` and `purchaseTypePrice`.
- A bad contacts file now exits **2** for `domain register`,
  `domain contacts set`, `transfer create` and `transfer internal-in`.
- Single-resource `get` commands exit **1** on an empty `{}` response, and
  `order get -q` / `email get -q` no longer echo the requested ID when the
  response has none.
- Internationalized DNS hosts and record targets are sent, and shown, in
  punycode (`xn--…`). Well-formed quoted TXT values are rewritten in canonical
  form on `dns export --zone`.
- `--debug` lines start with a timestamp and include request headers, with
  credentials redacted.

### Added
- `transfer create` and `transfer internal-in` take `--contacts-file`, the
  same JSON file `domain register --contacts-file` reads. The WHOIS contacts
  in it are applied when the domain lands in the account, so a transfer no
  longer needs a follow-up `domain contacts set`. Roles left out of the file
  get the account defaults, and each role given must be complete. Changing
  contacts may start a registrar transfer lock, depending on account
  settings; the confirmation prompt says so. The file is read before the
  auth-code prompt, and a missing or invalid one exits 2. `--dry-run` shows
  the contacts in the previewed body.

### Changed
- The name.com Core SDK is now pinned to v1.35.0. It adds an optional
  `contacts` field to transfer requests and a `warning` field to
  transfer-status webhooks; nothing the CLI sends or prints changes.

### Fixed
- `domain lock`, `domain autorenew` and `domain privacy` read the domain
  first, and when it is already in the requested state they print "… is
  already on/off" and exit 0 without sending anything; `--dry-run` says the
  same. `lock on` for a domain inside its 60-day transfer lock used to fail
  with "Domain can not be unlocked until …". When the API refuses to unlock
  during that window, `domain lock off` and `domain update --lock=false` now
  say so, keeping the API's date; the exit code is still 1. Scripts see one
  extra GET per toggle, and a JSON `message` reading "already" where a
  no-op PATCH used to be sent.
- `domain privacy on` and `domain update --privacy=true` no longer call
  enabling privacy "a billable action": it never charges. It turns on privacy
  already purchased for the domain, and the prompt now says so. When none was
  purchased, the API's 409 "You may need to purchase WHOIS Privacy" now
  explains that the CLI cannot buy it and points to your name.com account
  (https://www.name.com/account); the exit code is still 1. `privacy off` on
  a domain without privacy prints "already off" and exits 0 (see above).
- `domain pricing` no longer under-reports a name that would be bought on the
  aftermarket, as expiring, or by backorder. It showed the standard $17.99 for
  a name `domain check` and `domain register` priced at $8625. It now also
  checks availability, and for such a name its Register row and a warning
  show that price and purchase type, worded as `domain check` shows them.
  Scripts: JSON/YAML gain `purchaseType` and `purchaseTypePrice` for these
  names (existing fields unchanged), and `-q` prints the acquisition price
  instead of the standard one. The command now makes one extra request.
- A missing, unreadable or invalid contacts file now exits 2 (usage error)
  instead of 1, for `domain register --contacts-file` and `domain contacts
  set --from-file`, matching `transfer create`. Scripts that check for exit
  code 1 here need updating. `domain register` also reads the file before
  checking availability and pricing or showing the guided form, so a bad
  path fails immediately.
- `dns create`, `dns update` and `dns import` convert an internationalized
  `--host`, and the hostname a CNAME, ANAME, MX, NS or SRV record points at,
  to punycode before sending, as domain arguments already were. `--host
  bücher` is sent, and shown by `--dry-run`, as `xn--bcher-kva`, so a script
  reading the request or the created record sees the `xn--…` form. A name
  with no valid internationalized form now exits **2** instead of reaching
  the server. TXT, A and AAAA answers are unchanged.
- `dns export --zone` no longer writes a zone that fails to load when a TXT
  value starts and ends with a quote but is not well-formed zone syntax, such
  as `"a"b"` or a single quoted string over 255 bytes. Such a value is now
  quoted and split like any other. A well-formed quoted value is kept, but is
  rewritten with single spaces between strings and only the escapes it needs.
- Every command run in a terminal wrote two terminal queries to stdout
  (`ESC]11;?` for the background colour and `ESC[6n` for the cursor
  position) and waited for the answers, even with `NO_COLOR` or
  `--color never`. A terminal that does not answer stalled the command, and a
  late answer was left in the shell's input. Nothing is queried now: the
  light or dark colour palette comes from `COLORFGBG` when the terminal sets
  it, and is dark otherwise.
- On Windows, colour and the spinner now work in consoles that do not have
  escape-code processing turned on by default: `namecom` turns it on at
  startup. Where the console refuses it (older Windows versions), output is
  plain and there is no spinner, instead of escape codes printed as text.
  `--color always` still colours.
- Key-value tables (`domain get`, `auth status` and other single-object
  views) now fit the terminal: a long value wraps inside its cell instead
  of running past the edge and breaking the borders. `--wide` keeps the old
  one-line layout, and piped or redirected output is unchanged.
- Single-resource reads no longer treat a `200 {}` as success. `domain get`,
  `domain contacts get`, `dnssec get`, `email get`, `order get`,
  `transfer get`, `url get` and `vanity-ns get` printed an empty resource and
  exited 0; a response missing the resource's identifying field (its name,
  ID, mailbox or digest) is now an "unexpected response from the API" error
  and exits **1**. Under `--quiet`, `order get` and `email get` no longer echo
  the requested ID or mailbox back when the response has none. List commands
  are unchanged: an empty list is still a valid answer.
- The `--debug` / `--debug-file` log now starts each entry with an RFC 3339
  timestamp (milliseconds), shows each response's round-trip time, and lists
  the request headers as sent and the useful response headers
  (`Content-Type`, `Retry-After`, `Location`, request IDs and rate-limit
  headers). `Authorization`, `Cookie` and other credential headers are shown
  as `[redacted]`. A failed attempt is now logged too, rather than leaving a
  request line with no outcome. Anything parsing the log should expect the
  timestamp before `→` and `←`.

## [0.4.8] - 2026-10-02

Sixty bug fixes and two security hardening changes. They came from a broad bug
hunt: live checks against the name.com sandbox, fuzz tests, a review of every
command's `--help`, and builds for Linux and Windows. Two fixes stop
credentials leaking: the token is now sent only to the API's exact origin, and
`--debug` no longer logs transfer auth codes. Several crashes are gone,
including on a DNS lookup failure and on unusual API responses.

Much of what a script sees changes, all toward what was documented or
intended. Check any script that relies on these:

- `-q`/`--quiet` follows one rule whatever `--output` says: lists print one
  ID per line, creates print only the new ID, other writes print nothing, and
  single-object reads print one value.
- Input is checked more strictly before anything is sent: domain and
  nameserver names, DNS records, `--priority`, IDs (must be positive),
  `--price` (must be finite and above 0, so `--price 0` is now refused), a
  negative `--timeout`, and extra arguments to `status`, `version` and
  `auth`. These exit **2**.
- An unknown `--profile` exits **3**, not 1.
- Internationalized domain arguments are sent, and shown in dry-runs, in
  punycode (`xn--…`).
- `domain check` exits **1** when it gets no answer for a name.
- `version` JSON/YAML renames `built` to `commitTime`. `auth status` reports
  `verified` as a boolean. `config list-profiles` shows endpoints as URLs.
- Error messages for non-JSON bodies no longer start with the status code,
  and `namecom api -o json` errors carry the response body in
  `error.details` (unchanged from 0.4.7).
- A `null` element in an API list is dropped from JSON/YAML output.
- `namecom open` prints `{"url", "opened"}` in JSON mode and prints the URL
  when it cannot open a browser.
- On Windows, `token_cmd` runs through `cmd.exe`. Wrap sh syntax in
  `sh -c "…"`.

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

### Fixed
- A DNS lookup failure, or a response the API client could not decode, no
  longer crashes `namecom` with a Go panic and exit **2**. It is reported as an
  ordinary error and exits **1**; a DNS failure gets the "could not reach the
  API" hint. This hit every command when offline, and `domain requirements` for
  `eu`, `jp` and `nyc` in the sandbox.
- A successful response whose body is JSON `null`, or a list in a response
  that contains a `null` element, no longer crashes the command. A `null` body
  now fails with "unexpected response from the API" and exits 1; before, about
  40 commands panicked, `status` among them. A `null` list element is skipped:
  it gets no table row, no `--quiet` line, and no entry in JSON or YAML output.
  name.com does not send these itself, but a proxy or captive portal can.
- `auth logout`, `auth login` and `config use` honour `--dry-run`. They
  ignored it and wrote the config file: `auth logout --dry-run` deleted the
  profile. They now print the change they would make and leave the file alone
  — in JSON or YAML mode as one document with `dry_run`, `config`, `action`,
  `profile` and `default` keys (`auth login` adds `username` and `sandbox`,
  never the token). `auth login --dry-run` still asks its questions.
- A write (POST, PUT, PATCH, DELETE) answered with a redirect now fails,
  naming the redirect, instead of following it. A redirected POST used to be
  resent as a GET without its body, so `dns create` could report
  `Created A record (id 0)` and exit 0 when nothing was created. Reads still
  follow redirects. This applies to `namecom api` as well.
- A successful response whose body is empty, not JSON, or JSON of the wrong
  shape now fails with `unexpected response from the API: …` and a hint that
  a change may still have been made, instead of a Go decoder message naming
  internal types (`json: cannot unmarshal array into Go value of type …`,
  `expected a **api.DomainResponsePayload response …`). It still exits 1.
- API errors from every command are now reported the way `namecom api`
  reports them. A 500 whose body explains the failure (such as `Invalid IP`)
  no longer suggests trying again shortly; an HTML error page from a proxy is
  shortened to one line instead of becoming the whole error message; and a 401
  mentions that the sandbox uses a separate API token. The error `message` in
  JSON output changes for non-JSON error bodies: it no longer starts with the
  status code (`502: <html>…`), and an empty body reads as the status text
  (`Service Unavailable`) rather than the bare code.
- A final 429 or 5xx is reported at once instead of after an extra wait. The
  API library slept before returning these even with its retries turned off:
  up to 60 seconds on a 429's `Retry-After`, and a second or two on a 5xx. A
  429 whose `Retry-After` outlasted `--timeout` could also come back as a
  `request canceled` error with exit 1; it now exits 5 as documented.
- A long non-JSON error body (a proxy's error page, say) is no longer cut in
  the middle of a multi-byte character when it is shortened for the error
  message, and invalid UTF-8 in such a body is replaced. The message, including
  the one in the JSON error envelope, is now always valid UTF-8.
- Internationalized domain names work as arguments. `domain get bücher.com`,
  `dns list bücher.com` and every other command that puts the domain in the
  URL path sent it percent-encoded, which name.com's edge answered with an
  HTML 403 — printed in full, exit 3, with advice to run `auth login`. The CLI
  now converts Unicode names to punycode (`xn--bcher-kva.com`) before sending,
  and `--dry-run` previews that form. A name that is not valid IDNA, or that
  contains a character no domain can hold — `?`, `#`, `/`, `%` and the like,
  as in `transfer eligibility 'a?x=1.com'` — is now a usage error (exit 2)
  and nothing is sent. Output that echoes the domain, such as dry-run paths,
  shows the punycode form. This adds `golang.org/x/net` (for its `idna`
  package) as a dependency.
- **Script-visible:** `-q`/`--quiet` now follows one rule, whatever `--output`
  says. Create commands print only the new resource's ID or name, so
  `ID=$(namecom dns create … -q)` works in a pipe; before, `dns create` and
  `url create` printed the whole JSON object there. Update, delete and other
  write commands print nothing. Hints are no longer printed in quiet mode;
  `-o table -q` used to print only a "→ Run …" line. A script that read the
  JSON object from a quiet create or update gets the ID or nothing now; drop
  `-q` to keep the object.
- **Script-visible:** `-q` now applies to the read commands that ignored it.
  Each prints one value per line instead of its full output: `version` the
  version string, `status` the expired or soon-expiring domains, `auth status`
  the username, `config show` the active profile, `config list-profiles` the
  profile names, `domain pricing` the registration price as a bare number,
  `domain contacts get` the registrant's email, `order get` the order ID,
  `email get` the mailbox, and `transfer eligibility` the domain if it can be
  moved by internal transfer (nothing otherwise).
- `dns create` and `url create` report a successful response that does not
  include the new record's or forwarding's ID as an unexpected response (exit
  **1**, with a hint to check before retrying), instead of printing `(id 0)`
  and exiting 0. In JSON and YAML mode the error replaces the printed object.
- `--price` on `domain register`, `domain renew` and `transfer create` must be
  a positive number. `Inf`, `NaN`, zero and negative values now exit **2**
  before anything is sent. Before, `NaN`, zero and negatives were silently
  ignored, and `Inf` was quoted in the prompt as `$+Inf`, gave an empty
  `--dry-run` preview, and failed when sent.
- Record, URL forwarding, order and contact verification IDs must be positive
  whole numbers. `0`, negative numbers and `+5` now exit **2** before anything
  is sent; they used to reach the API (`contact resend -5`). An ID too large
  to be one is reported as "must be a positive whole number" rather than
  "must be a number".
- Domain and nameserver arguments with an empty label (`bad..com`), a label
  over 63 characters, or more than 253 characters in all now exit **2**
  before anything is sent. `transfer eligibility bad..com` used to answer for
  `bad.com`.
- `dns create`, `dns update` and `dns import` check more of a record before
  sending it, and exit **2** on: a host with characters no DNS name has (`"`,
  `;`, `(`, `@`, or `*` other than a leading `*.`); a CNAME, ANAME, MX, NS or
  SRV target with an empty label (`a..example.com`) or such characters; MX
  and SRV answers containing a carriage return or newline; SRV weight or port
  outside 0–65535; and `--priority` outside 0–65535. The API stored some of
  these, and `dns export --zone` then wrote a zone that does not load.
- `vanity-ns` commands refuse a hostname with an empty label
  (`ns1..example.com`), a space, a character no hostname has, or a label over
  63 characters, and exit **2** before anything is sent.
- `domain set-ns` rejects a nameserver containing whitespace, such as
  `--ns "ns1.example .com,ns2.example.com"`, as a usage error (exit 2) before
  sending anything. It was sent, so the mistake came back as an API error
  instead. Spaces around the commas are still trimmed as before. Characters
  no hostname contains (`*`, `@`, `:`) and invalid internationalized names
  are refused the same way, matching the rules for domain arguments.
- `dns update --type CAA` exits **2** with a usage error, as `dns create` and
  `dns import` already did. It used to pass validation, so `--dry-run` showed a
  request the API rejects, and a real run failed with exit 1.
- `dns import` reads files that start with a byte-order mark, including the
  UTF-16 files Windows PowerShell 5.1 writes for
  `namecom dns export X > records.json`. They failed with "invalid character".
  A file that is not valid JSON now exits **2** instead of 1.
- `dns export --zone` writes a newline, tab or other control character in a
  TXT value as an RFC 1035 decimal escape (`\010`). It was written raw, which
  left the quotes unbalanced, so BIND and other parsers refused to load the
  whole zone.
- `domain check` in sandbox mode or with `--authoritative` no longer drops a
  domain the registry returned no result for, such as one with an unknown TLD.
  It gets a row and a warning. On every path, a `domain check` with any domain
  left unanswered now exits **1** after printing its results; it used to exit
  0, so scripts checking the exit code will see this.
- `domain check`'s offer to register and the `domain check` / `domain search`
  PRICE column describe the purchase the way `domain register` does. An
  aftermarket, expiring or backorder price reads as a flat fee
  (`$8625.00 flat (aftermarket_b)`) rather than `/yr`, and a premium price
  shows its renewal price. JSON output is unchanged.
- `domain requirements -q` lists only fields you can pass to
  `--tld-requirement`. It used to include notice entries such as .ca's
  `description`, which take no value, so scripts building flags from it will
  see one name fewer. The table now prints those notices under the
  capabilities instead of hiding them.
- `domain update --lock=false` reports the transfer lock removed only after
  the API accepts the change. During the 60-day transfer lock it printed the
  warning and then the API's refusal.
- **Script-visible:** a `--profile` or `NAMECOM_PROFILE` naming a profile that
  does not exist now exits 3, the authentication code, instead of 1. The error
  lists the profiles that do exist, in sorted order.
- `namecom --help --output json`, `namecom -h --color never` and other global
  flags placed after `--help` at the top level print the root help and exit 0.
  They used to fail with `unknown command "json"` and exit 2; the same flags
  already worked before `--help` and on subcommands.
- The `status` summary line no longer drops the count of domains expiring in
  7–30 days when one expires within 7 days: it shows both, as "1 expiring
  within 7 days  2 more within 30 days". Counts are pluralised ("1 domain",
  "2 transfers pending"). JSON and YAML output, which already carried both
  counts, is unchanged.
- `contact resend` no longer reports a reply without a `sent` field as
  throttled "until 0001-01-01". It fails with "unexpected response from the
  API" instead, still exiting 1. A throttled reply that gives no retry time
  says only "throttled", and with `-o json`/`yaml` prints no payload rather
  than one carrying the zero date.
- `auth login --sandbox` saves the profile with `sandbox: true` and no longer
  asks the sandbox question. The flag was ignored, so the profile was saved for
  production unless you also answered Yes at the prompt.
- On Windows, commands no longer warn that the config file "is accessible by
  other users" on every run. Windows reports every writable file with Unix
  mode `0666`, so the warning could never be cleared; the check now runs only
  on Unix-like systems.
- On Windows, `token_cmd` runs through `cmd.exe` instead of `sh -c`. A stock
  Windows install has no `sh`, so `token_cmd` failed with
  `exec: "sh": executable file not found`. If your helper relied on `sh` (for
  example from Git Bash), wrap it: `token_cmd: sh -c "…"`. macOS and Linux are
  unchanged.
- A `token_cmd` that prints more than one line is now refused (exit **3**)
  with a message saying how many lines it printed, instead of sending all of
  them as the token. The message does not repeat the output.
- A negative `--timeout` now exits **2**. It used to mean no timeout at all.
- `--debug-file` naming a file that already exists now makes it readable only
  by you (mode 0600), as a new file already was. An existing file used to keep
  its mode, often 0644.
- A TLS certificate the client rejects and a host name that does not exist
  (NXDOMAIN) now fail at once. Both used to be retried three times, about
  seven seconds, before the same error. DNS timeouts and refused connections
  are still retried.
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
- Domain-name completion now finds any domain on the account. It fetched only
  the first 250 domains and ignored what had been typed; the typed text is now
  sent to the API as a filter, the same one `domain list --filter` uses.
- Shell completion for `domain claims` no longer offers the domains already
  in your account; claims are checked on names you are about to register.
- Shell completion offers values for `-o`/`--output`, `--color` and
  `--profile` (profile names from the config file, read without running
  `token_cmd`), profile names for `config use`, and HTTP methods for
  `namecom api`. `domain register`, `domain check`, `domain search`,
  `transfer create` and the `api` path no longer fall back to filenames.
- On a credential failure (exit **3**), the `→ Run 'namecom auth status'…` line
  is no longer written to stdout, where `> out.txt` captured it, after the hint
  the error had already printed. Each error now carries one hint, on stderr
  with the error: `hint:` in table mode and the envelope's `hint` key in JSON
  and YAML. A missing or failing credential now gets that hint in the envelope
  too.
- Argument-count errors (`namecom domain get a b`) now honour `-o`, `--color`
  and the other output flags. They were rendered in the default format for the
  terminal, so `-o table` in a pipe still printed the JSON envelope, and
  `-o yaml` printed JSON.
- `--color always` now colours output that is piped or redirected, as
  `CLICOLOR_FORCE=1` already did; it used to print plain text whenever stdout
  was not a terminal. `--color never` likewise guarantees no escape codes.
- `--dry-run` fails with an error (exit **1**) when the request body cannot be
  encoded, for example a `--price` of `Inf`. It used to print nothing in JSON
  and YAML modes, or a request line with an empty body in table mode, and exit
  0.
- `-o yaml` now reads back exactly as `-o json` does for every string. A
  string starting with a line break lost it, a multi-line string starting with
  a tab produced YAML that could not be parsed, and a key named `<<` became a
  merge key. Strings containing a line break, tab or other control character,
  or with leading or trailing whitespace, are now written double-quoted with
  escapes (`"a\nb"`) rather than as `|` block scalars; a YAML parser reads the
  same value either way.
- `url create --dry-run` and `url update --dry-run` no longer print a
  `host=… to=… type=…` summary line after the preview. In JSON or YAML mode
  that line followed the dry-run document, so the output was not valid JSON and
  `| jq` failed. The preview body already shows the host, target and type.
- `email update`, `email delete` and `dnssec delete` with `--dry-run` show
  the mailbox or digest escaped in the path, as it is sent. A `/` or `?` in
  it used to be shown as-is.
- `dns import` no longer defines its own `--dry-run`, which hid the global
  flag from its help. `--dry-run` works as before, before or after
  `dns import`.
- `namecom open` honours `$BROWSER`, and no longer fails where nothing can
  open a browser (headless Linux, SSH sessions, containers). It prints
  `Open this URL in your browser: <url>` and exits 0 instead of exiting 1 with
  an `exec: "xdg-open"` error. In JSON or YAML mode it now always prints
  `{"url": …, "opened": true|false}`; it used to print nothing.
- `namecom open` with more than one argument exits **2** (usage error) instead
  of 1. Any other command that rejects the wrong number of positional
  arguments through cobra's own checks now exits 2 as well.
- A 403 from `transfer internal-in` no longer says to run `namecom auth
  login`. The error says the account needs enterprise reseller approval, and
  the hint now says the credentials are fine. It still exits 3. The same hint
  change applies to `contact verify`, which also printed the "check your
  credentials" line. The `hint` field in the JSON/YAML error envelope changes
  for both.
- `transfer create` and `transfer internal-in` without `--auth-code`, when not
  run in a terminal, now exit **2** (usage error) instead of 1, matching a
  too-short `--auth-code`.
- `status`, `version`, `auth login`, `auth status` and `auth logout` refuse
  extra arguments (exit **2**) instead of ignoring them, and `domain search`
  with an empty term exits **2** instead of printing an empty table.
- `auth status -o json` and `-o yaml` report `verified` as the boolean
  `true` instead of the string `"true"`. A script comparing it to the string
  needs updating.
- `config list-profiles` shows each profile's endpoint as a URL
  (`https://api.name.com`), as `config show` and `auth status` do. The
  `endpoint` field in its JSON and YAML output changes from the bare host to
  the URL.
- `namecom version` labels the timestamp it shows `committed`, since it is
  the time of the commit the binary was built from, not the build time. In
  JSON and YAML output the field is renamed from `built` to `commitTime`.
- `auth status` with rejected credentials names the profile, username,
  endpoint and config file it checked, instead of printing only
  `Unauthorized`. The exit code is unchanged (3); the error `message` in
  JSON/YAML output gains the same details.
- `url update --help` no longer shows `(default "redirect")` for `--type`.
  Leaving `--type` out has always kept the forwarding's current type; the help
  now says so. Root help lists the exit codes, and the `dns delete` example
  that piped into `dns delete` without `--yes`, which failed every time, now
  passes it.

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

[Unreleased]: https://github.com/patramsey/namecom-cli/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/patramsey/namecom-cli/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/patramsey/namecom-cli/compare/v0.4.9...v0.5.0
[0.4.9]: https://github.com/patramsey/namecom-cli/compare/v0.4.8...v0.4.9
[0.4.8]: https://github.com/patramsey/namecom-cli/compare/v0.4.7...v0.4.8
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
