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

All of these must be clean. CI runs `lint`, `go test -race -count=1 ./...`,
`make verify-generate`, and `govulncheck ./...` on every pull request.

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

Use `--sandbox` (`api.dev.name.com`) for anything that mutates state. There
is **no base-URL override flag**, so a binary pointed at nothing in
particular talks to production — be deliberate about which credentials are
loaded when you run a write command by hand.

`--dry-run` prints the request a mutating command would send without
sending it. If you add or change a mutating command, extend the matching
`TestDryRunMatchesRealRequest_*` test in that package — those tests run each
command twice (once with `--dry-run` to capture what is *printed*, once
against an `httptest` stub to capture what is *sent*) and assert the two
agree. Hand-written dry-run strings drift silently otherwise.

## Working with the API client

The client is [`github.com/namedotcom/core-api-go`](https://github.com/namedotcom/core-api-go),
name.com's own SDK. There is no generated code in this repository and nothing
to regenerate.

The SDK is Fern-generated and has defects this project has already been bitten
by. Each is written up in [`docs/upstream/`](docs/upstream/) with a
reproduction, and each is pinned by a test that fails if the workaround is
removed:

- `domain update` builds a `map[string]any` rather than the SDK's typed body,
  because that type is an exclusive union that silently drops every field but
  the first — including the transfer lock.
- `option.WithXIdempotencyKey` is never used; it prefixes the key with `*`.
  `internal/api/headers.go` sets that header for every write.
- `url update` sends the host it just read, because the type cannot omit it and
  an empty host means the apex.
- Endpoints that take no body are given `&EmptyObject{}`, because a nil body
  marshals to `null`.

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
`apierror.go` normalizes every non-2xx response to an `*APIError`. Commands
receive their client and output config off the command context via the
typed keys in `cmd/cmdutil`, which exists to avoid an import cycle — use
`cmdutil.APIClient(cmd)` / `cmdutil.Out(cmd)` rather than reaching for a
global.

Two behaviors are load-bearing and easy to break by accident:

- **Retries.** `POST` is only retried when `X-Idempotency-Key` is set. Don't
  relax that; unconditional POST retries can double-register a domain.
- **Read-modify-write.** `dns update` and `domain update` fetch the current
  record first and merge only the flags that were explicitly changed,
  because the API does full `PUT` replacement. Sending a partial body drops
  fields.

## Reporting bugs / requesting features

Open an issue. For a bug, include the exact command you ran and the
`--dry-run` output if it's a mutating command. `--debug` output is useful
too — it redacts the token — but read it before pasting.

## Scope

`namecom` covers the name.com Core API surface: domains, DNS, DNSSEC, email
forwarding, URL forwarding, vanity nameservers, transfers, and orders, plus
`namecom api` as a raw passthrough for anything not yet wrapped.

The interactive TUI lives in a separate repository (`namecom-tui`) and is
deliberately not part of this module. Proposals to add a persistent
daemon, a config-file-driven declarative sync mode, or provider plugins for
other registrars are bigger conversations than a typical fix — open an
issue to discuss before sending a PR.
