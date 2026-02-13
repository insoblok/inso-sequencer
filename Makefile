.PHONY: build run test lint clean

BINARY := inso-sequencer
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

## build: Build the sequencer binary
build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/sequencer

## run: Build and run the sequencer
run: build
	./bin/$(BINARY) --config config.yaml

## test: Run all tests
test:
	go test -race -count=1 ./...

## test-cover: Run tests with coverage
test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

## lint: Run linter
lint:
	golangci-lint run ./...

## docker-build: Build Docker image
docker-build:
	docker build -t inso-sequencer:$(VERSION) .

## docker-run: Run in Docker
docker-run: docker-build
	docker run -p 8545:8545 -p 8546:8546 inso-sequencer:$(VERSION)

## clean: Remove build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html

## help: Show this help
help:
	@grep -E '^## ' Makefile | sed 's/## //' | column -t -s ':'
