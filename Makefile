# VibeBridge Makefile
#
# Targets:
#   build       — build all Go binaries (agent + relay)
#   web-build   — build the web client (pnpm build)
#   test        — run all Go tests
#   web-test    — run all web tests
#   test-all    — run Go + web tests
#   lint        — go vet + tsc --noEmit
#   release     — build signed release tarballs for all platforms
#   sign        — sign a release tarball with Ed25519
#   verify      — verify a signed release tarball
#   docker      — build the relay Docker image
#   clean       — remove build artifacts

GO ?= go
NODE ?= node
PNPM ?= pnpm
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DIR ?= dist
SIGNING_KEY ?= release-signing.key

# Reproducible build flags: strip embedded paths, use a fixed GOROOT.
GO_BUILD_FLAGS := -trimpath -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build web-build test web-test test-all lint release sign verify docker clean

build:
	$(GO) build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/vibebridge ./cmd/vibebridge
	$(GO) build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/viberelay ./cmd/viberelay

web-build:
	cd web && $(PNPM) install --frozen-lockfile && $(PNPM) build

test:
	$(GO) test -count=1 ./...

web-test:
	cd web && $(PNPM) test

test-all: test web-test

lint:
	$(GO) vet ./...
	cd web && npx tsc --noEmit

# Release builds for multiple platforms. Each target produces a
# statically linked binary (CGO_ENABLED=0) wrapped in a tarball.
RELEASE_PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64
RELEASE_TARGETS := $(addprefix release-,$(RELEASE_PLATFORMS))

release: $(RELEASE_TARGETS) sign-all

release-%:
	@mkdir -p $(BUILD_DIR)/release
	$(eval GOOS := $(word 2,$(subst -, ,$*)))
	$(eval GOARCH := $(word 3,$(subst -, ,$*)))
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build $(GO_BUILD_FLAGS) \
		-o $(BUILD_DIR)/release/vibebridge-$(VERSION)-$(GOOS)-$(GOARCH)/vibebridge \
		./cmd/vibebridge
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build $(GO_BUILD_FLAGS) \
		-o $(BUILD_DIR)/release/vibebridge-$(VERSION)-$(GOOS)-$(GOARCH)/viberelay \
		./cmd/viberelay
	cp README.md LICENSE $(BUILD_DIR)/release/vibebridge-$(VERSION)-$(GOOS)-$(GOARCH)/ 2>/dev/null || true
	tar czf $(BUILD_DIR)/release/vibebridge-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz \
		-C $(BUILD_DIR)/release \
		vibebridge-$(VERSION)-$(GOOS)-$(GOARCH)

sign-all:
	@for f in $(BUILD_DIR)/release/vibebridge-$(VERSION)-*.tar.gz; do \
		$(MAKE) sign FILE=$$f; \
	done

# Sign a file with Ed25519. Generates a key pair if none exists.
sign:
	@if [ ! -f $(SIGNING_KEY) ]; then \
		echo "Generating new Ed25519 signing key pair..."; \
		$(GO) run ./cmd/vibebridge/sign genkey --out $(SIGNING_KEY); \
	fi
	$(GO) run ./cmd/vibebridge/sign sign --key $(SIGNING_KEY) --file $(FILE)

verify:
	$(GO) run ./cmd/vibebridge/sign verify --key $(SIGNING_KEY).pub --file $(FILE) --signature $(FILE).sig

docker:
	docker build -t vibebridge/viberelay:$(VERSION) .

clean:
	rm -rf $(BUILD_DIR)
