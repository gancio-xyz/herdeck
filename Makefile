VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint app golden clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/herdeck ./cmd/herdeck

test:
	go test -race ./...

lint:
	go vet ./...
	gofmt -l . | (! grep .)

# Regenerate render golden images after an intentional visual change.
golden:
	go test ./internal/render -update

# macOS menu bar app bundle (no Dock icon), ad-hoc signed for local use.
app:
	VERSION=$(VERSION) scripts/macos-app.sh

clean:
	rm -rf bin dist
