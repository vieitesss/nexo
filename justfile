set shell := ["bash", "-euo", "pipefail", "-c"]

test:
    go test ./...

lint:
    test -z "$(gofmt -l .)"
    go vet ./...
    golangci-lint run

build:
    go build ./cmd/nexo
