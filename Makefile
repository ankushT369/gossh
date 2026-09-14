BINARY := gossh
BIN_DIR := bin
GO := go

.PHONY: all build vet test linux mac clean

all: build

# Build for the machine running make. The build used to be pinned to
# linux/amd64, which meant that on macOS it produced a Linux binary that could
# not run; cross-compiling now lives in the release targets below. Still
# statically linked, as before.
build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/$(BINARY) .

vet:
	$(GO) vet ./...

test:
	$(GO) test -race ./...

# Release artifacts. Go cross-compiles without a C toolchain, so either target
# can be built from any host.
linux:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BIN_DIR)/$(BINARY)-linux-amd64 .

mac:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -o $(BIN_DIR)/$(BINARY)-darwin-arm64 .

clean:
	rm -rf $(BIN_DIR)
