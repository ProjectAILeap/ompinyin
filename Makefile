VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# goreleaser injects {{.Version}} (no leading `v`); strip the tag prefix here too
# so `ompinyin version` does not depend on which route built the binary.
VERSION := $(VERSION:v%=%)
PKG     := github.com/ProjectAILeap/ompinyin/internal/catalog
LDFLAGS := -X $(PKG).Version=$(VERSION)

.PHONY: build test lint fmt clean cover release-check smoke

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

# T4 pre-tag smoke: the real-host checks T0 cannot make (§15). Converges this
# machine and briefly restarts fcitx5 — run it before tagging, on your own host.
smoke:
	scripts/t4-smoke.sh

# goreleaser is not a build dependency of the tool; check the config only
release-check:
	goreleaser check .goreleaser.yaml

clean:
	rm -rf bin dist
