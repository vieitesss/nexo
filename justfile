set shell := ["bash", "-euo", "pipefail", "-c"]

test:
    go test ./...

lint:
    test -z "$(gofmt -l .)"
    go vet ./...
    go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run

build:
    go build ./cmd/nexo

install: build
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p ~/.local/bin
    tmp=$(mktemp ~/.local/bin/.nexo.XXXXXX)
    trap 'rm -f "$tmp"' EXIT
    cp nexo "$tmp"
    chmod 755 "$tmp"
    mv -f "$tmp" ~/.local/bin/nexo
    trap - EXIT
