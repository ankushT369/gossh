BINARY := gossh
BIN_DIR := bin
GO := go

.PHONY: all build test clean

all: build

build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BIN_DIR)/$(BINARY) .

test:
	$(GO) test -race ./...

clean:
	rm -rf $(BIN_DIR)

