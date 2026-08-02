# Security Policy

## Supported Versions

`namecom` is pre-1.0 (currently in the `0.2.x` series). Only the latest
released version is supported — there's no parallel maintenance of older
minor versions at this stage.

## Reporting a Vulnerability

Please report security issues privately using GitHub's
[private vulnerability reporting](https://github.com/patramsey/namecom-cli/security/advisories/new)
feature (Security tab → Report a vulnerability) rather than opening a
public issue.

Include:
- The affected version (`namecom version`)
- Steps to reproduce
- The impact you believe the issue has

Do **not** include a real API token in a report. If a reproduction needs
one, say so and we'll work out a channel — `--debug` output redacts the
token, but read anything you paste before pasting it.

This is a small, actively-maintained project without a formal SLA, but
reports will be acknowledged and addressed as promptly as possible. A fix
will typically ship as a patch release once confirmed.

## Scope

`namecom` holds name.com API credentials and makes authenticated,
state-changing calls against a live registrar account. Relevant categories
of concern include (but aren't limited to):

- **Credential exposure** — a token reaching stdout, stderr, a log file
  (`--debug` / `--debug-file`), a shell history entry, the process
  environment of an unrelated child process, or a config file written with
  overly permissive modes.
- **`token_cmd` handling** — the credential-helper shell exec in
  `~/.config/namecom/config.yaml`. Anything that lets an untrusted config,
  profile name, or API response influence what gets executed.
- **Unintended mutations** — a path where a command sends a different
  request than its `--dry-run` preview showed, a confirmation prompt is
  bypassed without `--yes`, or a non-idempotent `POST` is retried without an
  idempotency key (that would double-charge a registration or renewal).
- **Sandbox/production confusion** — anything that causes `--sandbox` to hit
  production, or a production profile to be used when a sandbox one was
  selected.
- **Untrusted response data** — terminal escape-sequence injection from API
  response fields rendered to a TTY, or parser panics / resource exhaustion
  triggered by malformed or hostile responses.

Vulnerabilities in third-party dependencies should generally be reported
upstream, but flagging them here is welcome too if you're not sure where
they apply. CI runs `govulncheck` on every PR, which reports only
vulnerabilities whose affected code this binary can actually reach.

Issues in the **name.com API itself** (as opposed to this client) should go
to name.com directly — this project can't fix or embargo those.
