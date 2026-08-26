# namecom CLI — build and test targets.

BINARY      := namecom
PKG         := github.com/patramsey/namecom-cli
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -X main.version=$(VERSION)

# There is no codegen step any more. The API client is
# github.com/namedotcom/core-api-go, upstream's own SDK — see #40. What used to
# live here was a vendored OpenAPI spec, a Python preprocessor that downgraded
# it from 3.1 to 3.0, an oapi-codegen invocation, a SHA pin on the spec, and a
# CI check that the committed output still matched its own generator.

.PHONY: all build test test-int lint install release clean fmt

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

test:
	# -count=1 kept deliberately. The reason it was added — a generated package
	# shelling out to a Python preprocessor the cache could not see — is gone,
	# but a cached pass is still a worse default than a slightly slower honest
	# one for a suite this size.
	go test -count=1 ./...

# Integration suite against the sandbox API. Requires sandbox credentials.
test-int:
	NAMECOM_TEST_SANDBOX=1 go test -tags integration ./...

lint:
	golangci-lint run

fmt:
	gofmt -w $(shell find . -name '*.go')

install:
	go install -ldflags "$(LDFLAGS)" .

release:
	goreleaser release --clean

clean:
	rm -f $(BINARY)
