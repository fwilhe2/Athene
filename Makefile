# Makefile for Athene — the classic RAD form designer for GTK4.
# Run 'make help' for the list of targets.

BIN := athene

.PHONY: all build run tidy gopls clean help

all: build

## build: compile the Athene IDE to ./athene
build:
	GOFLAGS=-mod=mod go build -o $(BIN) .

## run: build and launch the IDE
run: build
	./$(BIN)

## tidy: resolve dependencies and refresh go.sum
tidy:
	GOFLAGS=-mod=mod go mod tidy

## gopls: install the gopls language server (powers code completion)
gopls:
	go install golang.org/x/tools/gopls@latest

## clean: remove the built binary
clean:
	rm -f $(BIN)

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
