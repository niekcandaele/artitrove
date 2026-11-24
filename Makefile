.PHONY: build test lint run

build:
	CGO_ENABLED=0 go build -trimpath -o bin/artitrove ./cmd/artitrove

test:
	go test -race ./...

lint:
	golangci-lint run

# needs ARTITROVE_TOKEN
run:
	go run ./cmd/artitrove
