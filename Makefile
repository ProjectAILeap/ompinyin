VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PKG     := github.com/ProjectAILeap/ompinyin/internal/catalog
LDFLAGS := -X $(PKG).Version=$(VERSION)

.PHONY: build test lint fmt clean cover release-check

build:
	go build -ldflags "$(LDFLAGS)" -o bin/ompinyin ./cmd/ompinyin

test:
	go test -race ./...

lint:
	golangci-lint run --timeout 5m ./...

fmt:
	gofmt -w .

# coverage report per package (T0 stub suite, §15)
cover:
	go test -cover ./...

# goreleaser is not a build dependency of the tool; check the config only
release-check:
	goreleaser check .goreleaser.yaml

clean:
	rm -rf bin dist
