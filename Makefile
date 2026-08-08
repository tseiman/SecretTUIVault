BINARY := secretvault
DIST := dist
GO ?= go

.PHONY: all build test race vet fmt-check check cross-build clean

all: check build

build:
	$(GO) build -trimpath -o $(BINARY) ./cmd/secretvault

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$(gofmt -l cmd/secretvault/*.go internal/*/*.go)" || \
		(printf 'Run gofmt on:\n%s\n' "$$(gofmt -l cmd/secretvault/*.go internal/*/*.go)"; exit 1)

check: fmt-check test race vet

cross-build: clean
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -o $(DIST)/secretvault-linux-amd64 ./cmd/secretvault
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -o $(DIST)/secretvault-linux-arm64 ./cmd/secretvault
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -o $(DIST)/secretvault-darwin-amd64 ./cmd/secretvault
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -o $(DIST)/secretvault-darwin-arm64 ./cmd/secretvault
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -o $(DIST)/secretvault-windows-amd64.exe ./cmd/secretvault
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build -trimpath -o $(DIST)/secretvault-windows-arm64.exe ./cmd/secretvault

clean:
	rm -rf $(DIST) $(BINARY)
