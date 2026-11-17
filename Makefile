BINARY := gossh
BIN_DIR := bin
GO := go
RELEASE_LDFLAGS := -ldflags="-s -w"

.PHONY: all build vet test linux mac windows release-linux release-mac release-windows clean

all: build

# Build for the machine running make. The build used to be pinned to
# linux/amd64, which meant that on macOS it produced a Linux binary that could
# not run; cross-compiling now lives in the release targets below. Still
# statically linked, as before.
build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build  -o $(BIN_DIR)/$(BINARY) cmd/gossh/main.go

fmt:
	gofmt -w .

fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Not formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet:
	$(GO) vet ./...

# Release artifacts. Go cross-compiles without a C toolchain, so either target
# can be built from any host.
linux:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build \
		-o $(BIN_DIR)/$(BINARY)-linux-amd64 \
		cmd/gossh/main.go

mac:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build \
		-o $(BIN_DIR)/$(BINARY)-darwin-arm64 \
		cmd/gossh/main.go

windows:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build \
		-o $(BIN_DIR)/$(BINARY)-windows-amd64.exe \
		cmd/gossh/main.go

release-linux:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build \
		$(RELEASE_LDFLAGS) \
		-o $(BIN_DIR)/$(BINARY)-linux-amd64 \
		cmd/gossh/main.go

release-mac:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build \
		$(RELEASE_LDFLAGS) \
		-o $(BIN_DIR)/$(BINARY)-darwin-arm64 \
		cmd/gossh/main.go

release-windows:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build \
		$(RELEASE_LDFLAGS) \
		-o $(BIN_DIR)/$(BINARY)-windows-amd64.exe \
		cmd/gossh/main.go

clean:
	rm -rf $(BIN_DIR)
