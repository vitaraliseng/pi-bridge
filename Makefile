.PHONY: build install test lint lint-fix vet clean

PREFIX ?= $(HOME)/go
BINDIR ?= $(PREFIX)/bin
GOLANGCI_LINT ?= golangci-lint

build:
	go build -o bin/pi-bridge ./cmd/pi-bridge

install:
	go install ./cmd/pi-bridge
	@echo "Installed to $$(go env GOBIN)"
	@echo "If that is empty, binary is in $$(go env GOPATH)/bin"
	@echo "Ensure that directory is on your PATH, then run: pi-bridge"

test:
	go test ./...

vet:
	go vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

lint-fix:
	$(GOLANGCI_LINT) run --fix ./...

clean:
	rm -rf bin
