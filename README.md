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
- [Configuration](#configuration)
- [Shell completion](#shell-completion)
- [Global flags](#global-flags)
- [Exit codes](#exit-codes)
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
| `dns` | `list` `create` `update` `delete` `export` `import` |
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
```

**Transfer a domain in:**
```bash
namecom transfer eligibility acme.io                      # confirm it's eligible
namecom transfer create acme.io --auth-code XXXXXX
namecom transfer get acme.io                              # check status
# set WHOIS contacts on arrival (same JSON as domain register --contacts-file);
# changing contacts may start a registrar transfer lock
namecom transfer create acme.io --auth-code XXXXXX --contacts-file contacts.json
```

**Set up email and URL forwarding:**
```bash
namecom email create acme.io hello --to you@gmail.com     # hello@acme.io → you@gmail.com
namecom email list acme.io
namecom url create acme.io --to https://new-site.com      # redirect apex to another URL
```

**Enable DNSSEC:**
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

**Scripting and automation:**
```bash
# List every domain expiring within 60 days (GNU date; on macOS: date -v+60d +%F)
namecom domain list --all --expiring-before "$(date -d '+60 days' +%F)" -q

# Bulk-create an A record across all domains
namecom domain list --all -q | xargs -I{} namecom dns create {} --type A --answer 1.2.3.4

# Dry-run first, then apply
namecom dns create acme.io --type TXT --answer "v=spf1 include:sendgrid.net ~all" --dry-run
namecom dns create acme.io --type TXT --answer "v=spf1 include:sendgrid.net ~all" --yes

# Capture the new record's ID
ID=$(namecom dns create acme.io --type A --host api --answer 1.2.3.4 -q)
namecom dns delete acme.io "$ID" --yes
```

Commands that change something ask first when run in a terminal. In a script
or a pipe there is no one to ask, so they stop with *"confirmation required
for … — pass --yes to confirm when not running in a terminal"* and exit 2
until you pass `--yes`.

## Output formats

Every command supports `--output table`, `--output json`, and `--output yaml`.
The default is `table` in a terminal and `json` when output is piped or
redirected:

```bash
namecom domain list                     # rich table with colors and expiry urgency
namecom domain list --output json       # machine-readable JSON
namecom domain list --quiet             # one domain per line, for scripting
```

`-q`/`--quiet` follows one rule whatever `--output` says: lists print one ID
or name per line, create commands print the new resource's ID, other writes
print nothing, and other reads print the one value a script most likely wants
(`version` the version, `auth status` the username, `domain pricing` the
price). Errors still go to stderr.

Tables drop their rightmost columns to fit a narrow terminal and say which
they hid; `--wide` keeps them all.

`--dry-run` prints the request a write would send, and sends nothing. In JSON
mode — including the default when piped — that is a JSON document with
`dry_run`, `method`, `path` and `body` keys; `-o table` prints
`METHOD /path` and the body instead.

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

**Environment variables** (useful in CI):
```bash
export NAMECOM_USERNAME=yourname
export NAMECOM_TOKEN=yourtoken
export NAMECOM_SANDBOX=true        # target sandbox API (true/false, yes/no, on/off, 1/0)
export NAMECOM_PROFILE=staging     # select a profile
export NAMECOM_CONFIG=~/namecom-ci.yaml    # use this file instead of the default
export NAMECOM_NO_UPDATE_NOTIFIER=1        # never print the "new release" notice
namecom domain list
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
| `-o, --output` | `table` in TTY, `json` otherwise | Output format: `table`, `json`, `yaml` |
| `-q, --quiet` | | Script output: lists print one ID or name per line, creates the new ID, other writes nothing — see [Output formats](#output-formats) |
| `-y, --yes` | | Skip all confirmation prompts; required for writes when not in a terminal |
| `--dry-run` | | Print the request a write would send, without sending it — a JSON document in JSON mode. Reads are unaffected |
| `--profile` | | Use a named credential profile |
| `--sandbox` | | Target the sandbox API (`api.dev.name.com`) |
| `--base-url` | | Send requests to another API base URL, such as a local stub or a proxy. Your credentials go wherever it points; a warning says so when it is not name.com |
| `--color` | `auto` | Colorize output: `auto`, `always`, `never` |
| `--wide` | | Keep every table column, even when the table is wider than the terminal |
| `--timeout` | `30s` | Total time budget for one API call, retries included |
| `--debug` | | Log HTTP requests/responses to stderr (token and auth codes redacted) |
| `--debug-file` | | Log HTTP requests/responses to a file (appends; useful as an audit log) |
| `--no-header` | | Omit the header row from table output |
| `--idempotency-key` | a fresh key per write | Pin every write in this invocation to one key, so re-running the same command after a failure can be recognized as a retry by endpoints that honor idempotency keys |
| `--username` | | API username (overrides config and `NAMECOM_USERNAME`) |
| `--token` | | API token (overrides config and `NAMECOM_TOKEN`) |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | API or other runtime error, or a confirmation declined or a prompt cancelled (Ctrl-C) |
| `2` | Usage error: an unknown command or flag, a wrong number of arguments, or an invalid value |
| `3` | Authentication: credentials missing (an unknown `--profile` included), failing or rejected, or access denied (HTTP 401/403) |
| `4` | Not found (HTTP 404) |
| `5` | Rate limited (HTTP 429), after the CLI's own retries |

With `--output json` or `yaml` — including the JSON default when piped — an
error is written to stderr as one document, an `error` object with a
`message` and, where there is one, a `hint`. An unknown command also lists
the commands it was probably meant to be in `error.suggestions`, as full
command lines (`["namecom dns delete"]`).

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
