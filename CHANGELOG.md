# Changelog

All notable changes to `namecom` are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this project
follows [Semantic Versioning](https://semver.org/).

Releases before `0.2.0` predate this file. Their notes are on the
[releases page](https://github.com/patramsey/namecom-cli/releases).

## [Unreleased]

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

[Unreleased]: https://github.com/patramsey/namecom-cli/compare/v0.2.3...HEAD
[0.2.3]: https://github.com/patramsey/namecom-cli/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/patramsey/namecom-cli/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/patramsey/namecom-cli/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/patramsey/namecom-cli/compare/v0.1.10...v0.2.0
