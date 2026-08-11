DIST := dist
GO ?= go
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
DESTDIR ?=
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || printf dev)
LDFLAGS := -X github.com/tseiman/SecretTUIVault/internal/ui.buildVersion=$(VERSION)

ifeq ($(OS),Windows_NT)
BINARY := secretvault.exe
INSTALL_DIR ?= $(LOCALAPPDATA)/Programs/SecretTUIVault
else
BINARY := secretvault
endif

.PHONY: all build install test race vet fmt-check check cross-build clean

all: check build

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/secretvault

ifeq ($(OS),Windows_NT)
install:
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./scripts/install-windows.ps1 -Source "$(BINARY)" -Destination "$(INSTALL_DIR)"
else
install:
	@test -f "$(BINARY)" || (printf 'Missing %s; run make build first.\n' "$(BINARY)"; exit 1)
	install -d -m 0755 "$(DESTDIR)$(BINDIR)"
	install -m 0755 "$(BINARY)" "$(DESTDIR)$(BINDIR)/$(BINARY)"
endif

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$(gofmt -l cmd/secretvault/*.go internal/*/*.go scripts/*.go)" || \
		(printf 'Run gofmt on:\n%s\n' "$$(gofmt -l cmd/secretvault/*.go internal/*/*.go scripts/*.go)"; exit 1)

check: fmt-check test race vet

cross-build: clean
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/secretvault-linux-amd64 ./cmd/secretvault
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/secretvault-linux-arm64 ./cmd/secretvault
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/secretvault-darwin-amd64 ./cmd/secretvault
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/secretvault-darwin-arm64 ./cmd/secretvault
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/secretvault-windows-amd64.exe ./cmd/secretvault
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/secretvault-windows-arm64.exe ./cmd/secretvault

clean:
	rm -rf $(DIST) $(BINARY)
