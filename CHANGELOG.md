# Changelog

All notable changes to `namecom` are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this project
follows [Semantic Versioning](https://semver.org/).

Releases before `0.2.0` predate this file. Their notes are on the
[releases page](https://github.com/patramsey/namecom-cli/releases).

## [Unreleased]

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
  installs need neither, and the formula prints both as caveats during the
  migration.

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

[Unreleased]: https://github.com/patramsey/namecom-cli/compare/v0.4.1...HEAD
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
