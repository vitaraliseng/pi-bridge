.PHONY: build install test clean

PREFIX ?= $(HOME)/go
BINDIR ?= $(PREFIX)/bin

build:
	go build -o bin/pi-bridge ./cmd/pi-bridge

install:
	go install ./cmd/pi-bridge
	@echo "Installed to $$(go env GOBIN)"
	@echo "If that is empty, binary is in $$(go env GOPATH)/bin"
	@echo "Ensure that directory is on your PATH, then run: pi-bridge"

test:
	go test ./...

clean:
	rm -rf bin
