BINARY     := smux
CMD        := ./cmd/smux
BIN_DIR    := ./bin
OUTPUT     := $(BIN_DIR)/$(BINARY)
INSTALL_DIR := /usr/local/bin

VERSION    := dev
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE       := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS    := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

RELEASE_DIR := $(BIN_DIR)/release
PLATFORMS   := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: build test clean install release

build:
	mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(OUTPUT) $(CMD)

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)

install: build
	install -m 0755 $(OUTPUT) $(INSTALL_DIR)/$(BINARY)

release:
	@if [ "$(VERSION)" = "dev" ]; then echo "ERROR: set VERSION, e.g. make release VERSION=v0.0.2"; exit 1; fi
	@mkdir -p $(RELEASE_DIR)
	@for platform in $(PLATFORMS); do \
		OS=$${platform%%/*}; \
		ARCH=$${platform##*/}; \
		EXT=""; \
		if [ "$$OS" = "windows" ]; then EXT=".exe"; fi; \
		OUT=$(RELEASE_DIR)/$(BINARY)-$$OS-$$ARCH$$EXT; \
		echo "Building $$OS/$$ARCH..."; \
		GOOS=$$OS GOARCH=$$ARCH go build -ldflags "$(LDFLAGS)" -o $$OUT $(CMD) || exit 1; \
	done
	@echo "Uploading to $(VERSION)..."
	gh release upload $(VERSION) $(RELEASE_DIR)/*
