BINARY     := smux
CMD        := ./cmd/smux
BIN_DIR    := ./bin
OUTPUT     := $(BIN_DIR)/$(BINARY)
INSTALL_DIR := /usr/local/bin

VERSION    := dev
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE       := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS    := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test clean install

build:
	mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(OUTPUT) $(CMD)

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)

install: build
	install -m 0755 $(OUTPUT) $(INSTALL_DIR)/$(BINARY)
