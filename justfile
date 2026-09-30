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
    rm -rf ~/.local/bin/nexo | true
    cp nexo ~/.local/bin/nexo
