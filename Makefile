BIN          := mailhook
PKG          := ./cmd/mailhook
PREFIX       ?= $(HOME)/.local
INSTALL_DIR  := $(PREFIX)/bin
STATE_DIR    := $(PREFIX)/var/$(BIN)

GO           := go
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT       ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE         ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS      := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
GOFLAGS      := -trimpath -ldflags="$(LDFLAGS)"

.PHONY: all build install uninstall reinstall clean run test vet fmt snapshot help

all: build

build:
	$(GO) build $(GOFLAGS) -o $(BIN) $(PKG)

install: build
	@mkdir -p $(INSTALL_DIR) $(STATE_DIR)/run $(STATE_DIR)/log
	@install -m 0755 $(BIN) $(INSTALL_DIR)/$(BIN)
	@echo "installed: $(INSTALL_DIR)/$(BIN)"
	@case ":$$PATH:" in \
	  *":$(INSTALL_DIR):"*) ;; \
	  *) echo "WARNING: $(INSTALL_DIR) is not on PATH. Add this to your shell rc:"; \
	     echo "         export PATH=\"$(INSTALL_DIR):\$$PATH\"" ;; \
	esac
	@echo "next:  mailhook start     # then open http://127.0.0.1:7791/"

reinstall: uninstall install

uninstall:
	@rm -f $(INSTALL_DIR)/$(BIN)
	@echo "removed: $(INSTALL_DIR)/$(BIN)"
	@echo "state directory left intact at $(STATE_DIR) — remove manually if desired"

clean:
	@rm -f $(BIN)
	@rm -rf dist

snapshot:
	goreleaser release --snapshot --clean

run: build
	./$(BIN) serve

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

help:
	@echo "Targets:"
	@echo "  make build      - compile ./$(BIN)"
	@echo "  make install    - build + copy to $(INSTALL_DIR)"
	@echo "  make reinstall  - uninstall + install"
	@echo "  make uninstall  - remove installed binary (state dir kept)"
	@echo "  make run        - build + run in foreground"
	@echo "  make vet        - go vet ./..."
	@echo "  make snapshot   - local release build into ./dist (needs goreleaser)"
	@echo "  make clean      - remove local build artifact"
	@echo ""
	@echo "Override install prefix:"
	@echo "  make install PREFIX=/opt/local"
