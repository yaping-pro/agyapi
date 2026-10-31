# agyapi — local OpenAI-compatible gateway backed by an Antigravity account.
#
# Everything is standard-library Go; there is no dependency to vendor.

BINARY  := agyapi
PKG     := ./cmd/agyapi
GOFILES := $(shell find . -name '*.go' -not -path './.git/*')

GOFLAGS ?=
LDFLAGS := -s -w

.PHONY: all build run test test-race vet fmt fmt-check check clean tidy

all: check build

## build: compile the gateway into ./agyapi
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

## run: build and start on 127.0.0.1:7862
run: build
	./$(BINARY) -host 127.0.0.1 -port 7862

## test: unit + end-to-end tests
test:
	go test $(GOFLAGS) ./...

## test-race: same, with the race detector
test-race:
	go test $(GOFLAGS) -race ./...

## vet: go vet
vet:
	go vet ./...

## fmt: rewrite sources with gofmt
fmt:
	gofmt -w $(GOFILES)

## fmt-check: fail if any file is not gofmt'd
fmt-check:
	@out=$$(gofmt -l $(GOFILES)); \
	if [ -n "$$out" ]; then echo "not gofmt'd:"; echo "$$out"; exit 1; fi

## check: what CI runs
check: fmt-check vet test

## tidy: normalise go.mod/go.sum (no external deps expected)
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -f $(BINARY)
	go clean -testcache