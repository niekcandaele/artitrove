.PHONY: build test lint run

# Built with the local toolchain: a prebuilt binary older than the Go release cannot read its export data.
GOLANGCI_LINT = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

build:
	CGO_ENABLED=0 go build -trimpath -o bin/artitrove ./cmd/artitrove

test:
	go test -race ./...

lint:
	$(GOLANGCI_LINT) run

# needs ARTITROVE_TOKEN
run:
	go run ./cmd/artitrove
