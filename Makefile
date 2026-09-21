BINARY := bin/sufler
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS := -trimpath -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build check clean

build:
	go build $(GOFLAGS) -o $(BINARY) ./cmd/sufler

check: build
	./$(BINARY) --check

clean:
	rm -rf bin
