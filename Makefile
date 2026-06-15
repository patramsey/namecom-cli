# namecom CLI — build, codegen, and test targets.

BINARY      := namecom
PKG         := github.com/namedotcom/namecom-cli
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -X main.version=$(VERSION)

# OpenAPI spec provenance.
#   Source:  https://namedotcom-cdn.name.tools/api-info/namecom.api.yaml
#   Upstream version: 1.27.1 (see info.version in the spec)
#   SHA256:  bee62aaf8a8e7a3b980779da71af330735e30703b562523ca2954c6dfa2aa4cb
SPEC        := namecom.api.yaml
SPEC_SHA    := bee62aaf8a8e7a3b980779da71af330735e30703b562523ca2954c6dfa2aa4cb
SPEC_30     := $(shell mktemp -t namecom.api.30.XXXX.yaml)

# Pinned via the `tool` directive in go.mod (oapi-codegen v2.4.1).
OAPI_CODEGEN := go tool oapi-codegen

.PHONY: all build test test-int lint generate verify-spec install release clean fmt

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

# Regenerate the API client from the vendored spec. The vendored spec is
# OpenAPI 3.1; scripts/spec_to_30.py downgrades it to a 3.0-compatible form
# (oapi-codegen v2.4.x lacks full 3.1 support) before generation.
generate: verify-spec
	python3 scripts/spec_to_30.py $(SPEC) $(SPEC_30)
	$(OAPI_CODEGEN) -config internal/api/gen/codegen.yaml $(SPEC_30)
	gofmt -w internal/api/gen/zz_generated.go
	@rm -f $(SPEC_30)

# Fail loudly if the vendored spec drifts from the recorded SHA256 — forces a
# deliberate re-vendor + provenance update rather than a silent change.
verify-spec:
	@echo "$(SPEC_SHA)  $(SPEC)" | shasum -a 256 -c - >/dev/null \
		|| { echo "ERROR: $(SPEC) does not match recorded SHA256. Re-vendor deliberately and update SPEC_SHA in the Makefile."; exit 1; }

test:
	go test ./...

# Integration suite against the sandbox API. Requires sandbox credentials.
test-int:
	NAMECOM_TEST_SANDBOX=1 go test -tags integration ./...

lint:
	golangci-lint run

fmt:
	gofmt -w $(shell find . -name '*.go' -not -path './internal/api/gen/*')

install:
	go install -ldflags "$(LDFLAGS)" .

release:
	goreleaser release --clean

clean:
	rm -f $(BINARY)
