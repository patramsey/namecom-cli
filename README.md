<div align="center">

# namecom

**The command-line interface for [name.com](https://www.name.com)**

[![CI](https://github.com/patramsey/namecom-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/patramsey/namecom-cli/actions/workflows/ci.yml)
[![Latest Release](https://img.shields.io/github/v/release/patramsey/namecom-cli)](https://github.com/patramsey/namecom-cli/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/patramsey/namecom-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Register domains, manage DNS, and run your entire domain portfolio — without leaving the terminal.

</div>

---

```
$ namecom status

  Profile   default → https://api.name.com
  ────────────────────────────────────────────
  47 domains   3 expiring soon   1 transfer pending

  Expiring soon
    acme.io          2026-07-01   (in 16 days)
    staging.dev      2026-07-18   (in 33 days)
    oldsite.net      2026-08-02   (in 48 days)

→ Run 'namecom domain renew acme.io' to renew now
```

## Why

The name.com web UI is great for humans. The CLI is for agents and terminal wizards

- **Automate** domain renewals, DNS changes, and email forwards in CI/CD pipelines
- **Script** bulk operations across dozens of domains at once
- **Integrate** with secret managers via `token_cmd` — no credentials in shell history
- **Pipe** JSON output directly into `jq`, `grep`, and other tools
- **Stay fast** — tab completion, `--dry-run`, and `--yes` flags for confident automation

## Installation

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

**Go install:**
```bash
go install github.com/patramsey/namecom-cli@latest
mv "$(go env GOPATH)/bin/namecom-cli" "$(go env GOPATH)/bin/namecom"
```
`go install` names the binary after the module path (`namecom-cli`); the `mv` renames it to `namecom` to match the rest of this README.

## Quick start

```bash
# 1. Authenticate (grab your API token from name.com → Account → API)
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
| `domain` | `list` `get` `search` `check` `register` `renew` `lock` `autorenew` `privacy` `set-ns` `contacts` `auth-code` `pricing` `update` |
| `dns` | `list` `create` `update` `delete` `export` `import` |
| `dnssec` | `list` `get` `create` `delete` |
| `transfer` | `list` `get` `create` `cancel` `eligibility` `internal-in` `cancel-outbound` |
| `email` | `list` `get` `create` `update` `delete` |
| `url` | `list` `get` `create` `update` `delete` |
| `vanity-ns` | `list` `get` `create` `update` `delete` |
| `auth` | `login` `logout` `status` |
| `order` | `list` `get` `refund` |
| `config` | `list-profiles` `use` `show` |
| `api` | raw HTTP passthrough with auth applied |
| `open` | open name.com in a browser |

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
# List all domains expiring within 60 days
namecom domain list --output json | jq -r '.[] | select(.expireDate != null and .expireDate < "2026-08-01") | .domainName'

# Bulk-create an A record across all domains
namecom domain list -q | xargs -I{} namecom dns create {} --type A --answer 1.2.3.4

# Dry-run first, then apply
namecom dns create acme.io --type TXT --answer "v=spf1 include:sendgrid.net ~all" --dry-run
namecom dns create acme.io --type TXT --answer "v=spf1 include:sendgrid.net ~all" --yes
```

## Output formats

Every command supports `--output table` (default), `--output json`, and `--output yaml`:

```bash
namecom domain list                     # rich table with colors and expiry urgency
namecom domain list --output json       # machine-readable JSON
namecom domain list --quiet             # one domain per line, for scripting
```

## Configuration

Credentials live at `~/.config/namecom/config.yaml` after `namecom auth login`. Multiple profiles are supported for managing separate accounts:

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
Omit `--profile` to use your default (production) profile.

**Environment variables** (useful in CI):
```bash
export NAMECOM_USERNAME=yourname
export NAMECOM_TOKEN=yourtoken
export NAMECOM_SANDBOX=true        # target sandbox API
export NAMECOM_PROFILE=staging     # select a profile
export NAMECOM_CONFIG=~/.config/namecom/ci.yaml  # alternate config file
namecom domain list
```

**Secret manager integration** — add `token_cmd` to your config and credentials are fetched at runtime, never stored on disk:
```yaml
profiles:
  default:
    username: yourname
    token_cmd: "op read op://vault/namecom/token"  # 1Password example
```

## Shell completion

```bash
namecom completion bash  > /etc/bash_completion.d/namecom
namecom completion zsh   > "${fpath[1]}/_namecom"
namecom completion fish  > ~/.config/fish/completions/namecom.fish
```

## Global flags

| Flag | Default | Description |
|---|---|---|
| `-o, --output` | `table` in TTY, `json` otherwise | Output format: `table`, `json`, `yaml` |
| `-q, --quiet` | | One result per line — for piping and scripting |
| `-y, --yes` | | Skip all confirmation prompts |
| `--dry-run` | | Print the API request without sending it |
| `--profile` | | Use a named credential profile |
| `--sandbox` | | Target the sandbox API (`api.dev.name.com`) |
| `--color` | `auto` | Colorize output: `auto`, `always`, `never` |
| `--timeout` | `30s` | Per-request timeout |
| `--debug` | | Log HTTP requests/responses to stderr (token redacted) |
| `--debug-file` | | Log HTTP requests/responses to a file (appends; useful as an audit log) |
| `--no-header` | | Omit the header row from table output |
| `--idempotency-key` | auto-generated | Idempotency key for write operations — auto-generated per invocation; override to make cross-invocation retries safe |
| `--username` | | API username (overrides config and `NAMECOM_USERNAME`) |
| `--token` | | API token (overrides config and `NAMECOM_TOKEN`) |

## Development

```bash
make build      # compile to ./namecom
make test       # go test ./...
make lint       # golangci-lint run
make generate   # regenerate internal/api/gen/ from namecom.api.yaml
```

The API client in `internal/api/gen/` is code-generated from `namecom.api.yaml` (a vendored OpenAPI 3.1 spec). See [`internal/api/gen/README.md`](internal/api/gen/README.md) for details on the codegen pipeline.

Contributions welcome — open an issue or PR.

## License

MIT — see [LICENSE](LICENSE).
