GO ?= go
GOLANGCI_LINT ?= golangci-lint
BINARY ?= bin/pgkeeper
CONFIG ?= /etc/pgkeeper/pgkeeper.toml

.PHONY: all help web-build build run test race vet lint integration check clean

all: build

help:
	@printf '%s\n' \
		'Available targets:' \
		'  web-build    Build the frontend assets' \
		'  build        Build the pgkeeper binary' \
		'  run          Run pgkeeper with CONFIG=$(CONFIG)' \
		'  test         Run all unit tests' \
		'  race         Run all unit tests with the race detector' \
		'  vet          Run go vet' \
		'  lint         Run golangci-lint' \
		'  integration  Run the restic integration test' \
		'  check        Run unit tests and go vet' \
		'  clean        Remove the built binary'

web-build:
	cd web && yarn build

build: web-build
	mkdir -p "$(dir $(BINARY))"
	$(GO) build -o "$(BINARY)" ./cmd/pgkeeper

run: web-build
	$(GO) run ./cmd/pgkeeper -config "$(CONFIG)"

test: web-build
	$(GO) test ./...

race: web-build
	$(GO) test -race ./...

vet: web-build
	$(GO) vet ./...

lint: web-build
	$(GOLANGCI_LINT) run ./...

integration:
	$(GO) test -tags=integration ./internal/restic -run '^TestIntegrationBackupRetentionRestore$$'

check: test vet

clean:
	rm -f "$(BINARY)"
