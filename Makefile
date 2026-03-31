VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)"

.PHONY: build test lint install clean

build:
	go build $(LDFLAGS) -o bin/surfbot ./cmd/surfbot

test:
	go test ./... -v -race

lint:
	golangci-lint run ./...

install:
	go install $(LDFLAGS) ./cmd/surfbot

clean:
	rm -rf bin/
