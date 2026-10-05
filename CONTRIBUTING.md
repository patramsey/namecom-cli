# Contributing to namecom-cli

Thanks for considering a contribution. `namecom` is a Go CLI over the
name.com Core API — the bar for a good change here is: it's correct, it's
tested, and it doesn't quietly widen the blast radius of a command that
mutates real domains.

Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md).

## Development setup

Requires Go 1.26.6+. The `go` directive in `go.mod` carries a patch version
because CI's `govulncheck` step resolves the toolchain from it — see the
comment on that step in `.github/workflows/ci.yml`.

```bash
git clone https://github.com/patramsey/namecom-cli.git
cd namecom-cli
make build
make test
```

## Before opening a PR

```bash
go build ./...
go vet ./...
make lint          # golangci-lint — see https://golangci-lint.run/ for install
make test          # go test -count=1 ./...
go test -race -count=1 ./...
```

All of these must be clean. CI runs `go build ./...`, `lint`,
`go test -race -count=1 -coverpkg=./... ./...`, and `govulncheck ./...` on
every pull request.

Keep tests fast. The whole suite runs in well under a minute under
`go test -race -count=1 -coverpkg=./... ./...`, and every PR pays for it, so a
test that sweeps thousands of inputs or sleeps through real backoff belongs
somewhere else: inject the clock or the delay, stub the server with
`httptest`, and pick the few inputs that pin the behavior. A minutes-long
sweep has been turned down before for exactly this reason.

Parsers, validators and output encoders also have native fuzz tests
(`func Fuzz…` in each package's `fuzz_test.go`). A plain `go test` runs only
their seed corpus and the saved failures in `testdata/fuzz`, so they cost the
suite almost nothing. To fuzz one for real, run it by hand, one target at a time:

```bash
go test -run '^$' -fuzz '^FuzzQuoteTXT$' -fuzztime 60s ./cmd/dns
```

A failure is written to that package's `testdata/fuzz`; commit it with the
fix so it stays a regression test. Fuzzer finds not fixed yet are skipped
with a `KNOWN BUG` comment naming them, and `FUZZ_UNSKIP=all` turns them back
on.

CI also reports coverage to Codecov, which will comment on your PR with the
delta. That comment is **informational and never blocks a merge** — a
coverage gate mostly teaches people to pad tests to clear a threshold.

`-count=1` is kept in `make test`. The reason it was originally needed — a
generated package shelling out to a Python preprocessor the cache could not
see — is gone with that package, but an honest run is still a better default
than a cached one for a suite this size.

The integration suite hits the real sandbox API and is excluded from CI. It
needs sandbox credentials:

```bash
make test-int      # NAMECOM_TEST_SANDBOX=1 go test -tags integration ./...
```

## Testing against the API

Use `--sandbox` (`api.dev.name.com`) for anything that mutates state. Without
it, or a sandbox profile, a binary talks to production — be deliberate about
which credentials are loaded when you run a write command by hand.

To see exactly what a command sends without involving name.com at all, point
it at a local stub with `--base-url http://127.0.0.1:PORT`. The credentials
are sent to whatever you name, and the CLI warns on stderr whenever the base
URL is not name.com; use throwaway values (`NAMECOM_USERNAME=x
NAMECOM_TOKEN=x`) rather than a real token.

`--dry-run` prints the request a mutating command would send without
sending it. If you add or change a mutating command, extend the matching
`TestDryRunMatchesRealRequest_*` test in that package — those tests run each
command twice (once with `--dry-run` to capture what is *printed*, once
against an `httptest` stub to capture what is *sent*) and assert the two
agree, body included (`drifttest.AssertDryRunBodyMatches`). Hand-written
dry-run strings drift silently otherwise.

Send the write through `cmdutil.RunWrite` rather than checking `--dry-run`
and calling `Confirm` by hand. Build the request body once and put it in the
`Write`: RunWrite previews that value under `--dry-run` without prompting,
otherwise confirms (when `Prompt` is set) and hands the same value to your
send callback. If the prompt quotes anything from the request — a price, a
year count — format it from the body, so the user approves what is sent.
A preview built separately from the request, or a prompt shown before the
dry-run check, is the bug this repository has fixed most often. `dns import`
(many requests) and the register offered by `domain check` are the deliberate
exceptions.

Off a terminal, a confirmation cannot be answered, so the command fails with
"confirmation required for … — pass --yes to confirm when not running in a
terminal" (a usage error, exit 2). An `Example` that pipes into a write
therefore needs `--yes`. A flag the command would otherwise prompt for is
documented with `cmdutil.PromptedRequired` and, off a terminal, refused with
`cmdutil.RequiredFlags`, which also exits 2.

## JSON output

The README's [JSON contract](README.md#json-contract) is a promise to
scripts, and `TestJSONContract` in `cmd/jsoncontract_test.go` walks a
representative command of each shape against a stub to hold it. When you add
or change a command's JSON:

- Print a list with `out.JSONList` / `out.YAMLList`, never a bare slice, even
  when it is not paged.
- Give any struct you define camelCase `json` tags. Most output is the SDK's
  own types, which already are.
- Report a write that has no resource to print with `out.Success`, or
  `out.Unchanged` when the target was already in the requested state and
  nothing was sent; that is the `changed` field. A write over several
  targets reports through `out.Results()`, so it prints one document, not
  one per target.
- Say things to the user with `out.Warn`, `out.Note` or `out.Hint`, never
  with a bare write to stderr: in JSON mode a warning is collected into the
  `warnings` array, so stderr stays one document.
- A new kind of failure a script would branch on gets a type in `errorInfo`
  (`cmd/root.go`), next to its exit code in `exitCode`, and a row in the
  README's table. Adding a type is a contract change; so is renaming a key.
- Encode through `out.JSON`, not `encoding/json` directly: it turns HTML
  escaping off, including for SDK types that marshal themselves.

Anything that breaks one of these rules goes in the CHANGELOG under
"Breaking for scripts", with the output before and after.

## Working with the API client

The client is [`github.com/namedotcom/core-api-go`](https://github.com/namedotcom/core-api-go),
name.com's own SDK. There is no generated code in this repository and nothing
to regenerate.

The SDK is Fern-generated and has defects this project has already been bitten
by. The ones found while porting to it are written up in
[`docs/upstream/`](docs/upstream/) with a reproduction. Three were fixed
upstream in v1.33.4/v1.33.5 and their workarounds have been removed. Two
workarounds remain:

- Endpoints that take no body are given `&EmptyObject{}`, because a nil body
  marshals to `null`
  ([namedotcom/core-api-go#8](https://github.com/namedotcom/core-api-go/issues/8)).
- The SDK is handed an HTTP client (`finalResponseClient` in
  `internal/api/sdk.go`) that turns a final 429 or 5xx into an error itself,
  because the SDK's retrier sleeps on those even with retries disabled
  ([namedotcom/core-api-go#12](https://github.com/namedotcom/core-api-go/issues/12)).
  Retrying is `retryTransport`'s job alone.

The reports for the fixed three are kept rather than deleted, because the
requests they describe are still pinned by tests — `url update` asserts that no
`host` key is sent, and `domain update` asserts that `autorenewEnabled`,
`privacyEnabled` and `locked` can all be sent together, and that each is sent
only when its flag was passed.

If you change what a command sends, expect a `shape_test.go` or `drift_test.go`
to fail. Those assert the exact method, path, and body, and they were written
against the previous client so they mean "this request did not change" rather
than "this is what the code does".

## Workflow

- Branch off `main`, open a PR — direct pushes to `main` aren't used here.
- Keep commits focused; prefer several small, well-scoped commits over one
  large one.
- Commit messages follow a `type: summary` convention (`feat:`, `fix:`,
  `docs:`, `chore:`, `test:`, `ci:`), with a body explaining *why* when the
  reasoning isn't obvious from the diff alone.
- Add tests for behavior changes. This codebase leans on table-driven tests
  and `httptest` stubs; assert the observable behavior, not merely the
  absence of an error.
- Update [`CHANGELOG.md`](CHANGELOG.md) under `[Unreleased]` for anything
  user-facing.

## Design context

Every API call flows through `internal/api/`: `client.go` wires auth, the
User-Agent, and a 10 req/s rate limiter; `transport.go` buffers request
bodies for replay and retries `429`/`5xx` with exponential backoff;
`apierror.go` normalizes every non-2xx response to an `*APIError`. `--timeout`
is one budget for the whole call, retries and waits included. Commands
receive their client and output config off the command context via the
typed keys in `cmd/cmdutil`, which exists to avoid an import cycle — use
`cmdutil.APIClient(cmd)` / `cmdutil.Out(cmd)` rather than reaching for a
global.

Two behaviors are load-bearing and easy to break by accident:

- **Retries.** A `429` is retried for any method, because a rejected request
  was never processed. A `5xx` is retried only for `GET`, `HEAD`, `PUT` and
  `DELETE`: **`POST` is never retried on a 5xx, with or without an
  idempotency key** (see `idempotent()` in `transport.go` and the test that
  pins it), and neither is `PATCH`. The server may already have committed the
  write, and retrying it can double-register a domain. Don't relax that.
  A wait that would outlast the request deadline is not taken: the response
  is returned at once, so a 429 stays a 429 (exit 5). Nothing sleeps after
  the final attempt either — not the transport, and not the SDK (see above).
  Instead, a write that ends in a 5xx, or fails after it was sent, exits 6
  with its idempotency key in the error (`api.OutcomeUnknownError`). What
  counts as a write there is decided by `retryTransport` from the request
  method, so a new write command gets it without going through `RunWrite`
  or `api.MarkWrite` — those still matter for a write's other hints.
- **Partial updates.** `dns update` is a read-modify-write: it fetches the
  record, merges only the flags that were explicitly changed, and sends the
  full body, because that endpoint is a full `PUT` replacement and a partial
  body drops fields. `domain update` is the opposite: its endpoint is a
  `PATCH`, so it sends only the flags that were passed. Resending an
  unchanged `locked` is rejected during the 60-day transfer lock, so don't
  "helpfully" fill in current values there. It fetches current state only for
  the privacy prompt and the unlock warning.

## Reporting bugs / requesting features

Open an issue. For a bug, include the exact command you ran and the
`--dry-run` output if it's a mutating command. `--debug` output is useful
too — it redacts the token — but read it before pasting.

## Scope

`namecom` covers the name.com Core API surface: domains, DNS, DNSSEC, email
forwarding, URL forwarding, vanity nameservers, transfers, orders, and ICANN
contact verification, plus `namecom api` as a raw passthrough for anything not yet wrapped.

The interactive TUI lives in a separate repository (`namecom-tui`) and is
deliberately not part of this module. Proposals to add a persistent
daemon, a config-file-driven declarative sync mode, or provider plugins for
other registrars are bigger conversations than a typical fix — open an
issue to discuss before sending a PR.
