GO ?= go
GOLANGCI_LINT ?= golangci-lint
BINARY ?= bin/pgkeeper
CONFIG ?= /etc/pgkeeper/pgkeeper.toml

.PHONY: all help build run test race vet lint integration check clean

all: build

help:
	@printf '%s\n' \
		'Available targets:' \
		'  build        Build the pgkeeper binary' \
		'  run          Run pgkeeper with CONFIG=$(CONFIG)' \
		'  test         Run all unit tests' \
		'  race         Run all unit tests with the race detector' \
		'  vet          Run go vet' \
		'  lint         Run golangci-lint' \
		'  integration  Run the restic integration test' \
		'  check        Run unit tests and go vet' \
		'  clean        Remove the built binary'

build:
	mkdir -p "$(dir $(BINARY))"
	$(GO) build -o "$(BINARY)" ./cmd/pgkeeper

run:
	$(GO) run ./cmd/pgkeeper -config "$(CONFIG)"

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

integration:
	$(GO) test -tags=integration ./internal/restic -run '^TestIntegrationBackupRetentionRestore$$'

check: test vet

clean:
	rm -f "$(BINARY)"
