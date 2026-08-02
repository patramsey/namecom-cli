## Summary

<!-- What changed, and why. -->

## Test plan

<!-- Commands you ran and their results. -->

- [ ] `go build ./... && go vet ./... && make lint` clean
- [ ] `go test -race -count=1 ./...` passing
- [ ] `make verify-generate` clean, if this touches `namecom.api.yaml`,
      `scripts/spec_to_30.py`, or the `oapi-codegen` version
- [ ] `CHANGELOG.md` updated under `[Unreleased]`, if this is user-facing

## Mutating commands

<!-- Delete this section if the change doesn't touch a command that writes. -->

- [ ] `--dry-run` output still matches the request actually sent
      (`TestDryRunMatchesRealRequest_*` covers this)
- [ ] Confirmation behavior unchanged, or the change is called out above
