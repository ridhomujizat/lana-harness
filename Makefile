VERSION ?= $(shell cat VERSION 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DIRTY := $(shell git diff --quiet HEAD -- . ':!design' 2>/dev/null || echo -dirty)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)$(DIRTY) -X main.buildDate=$(BUILD_DATE)
GO ?= go
DIST ?= dist
PREFIX ?= $(HOME)/.local
PLATFORMS := "darwin arm64" "darwin amd64" "linux amd64" "linux arm64"

.PHONY: build install release checksums clean web web-check tag

# build writes ./lana for daily use, with the version, commit, and build date.
build:
	$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o lana ./cmd/lana
	@./lana --version

# install builds ./lana and copies it to $(PREFIX)/bin (default ~/.local/bin).
install: build
	@mkdir -p "$(PREFIX)/bin"
	install -m 0755 lana "$(PREFIX)/bin/lana"
	@echo "installed $(PREFIX)/bin/lana"

# tag creates the annotated git tag v$(VERSION) for the current commit. It
# refuses a dirty tree and a tag that already exists.
tag:
	@git diff --quiet HEAD -- . ':!design' || { echo "tag: commit your changes first" >&2; exit 1; }
	@git rev-parse -q --verify "refs/tags/v$(VERSION)" >/dev/null && { echo "tag: v$(VERSION) exists; bump VERSION" >&2; exit 1; } || true
	git tag -a "v$(VERSION)" -m "lana $(VERSION)"
	@echo "tagged v$(VERSION)"

web:
	cd web/ui && npm ci && npm run build

web-check:
	@set -e; tmp=$$(mktemp -d); log=$$(mktemp); trap 'rm -rf "$$tmp" "$$log"' EXIT; \
	if ! (cd web/ui && npx vite build --outDir "$$tmp") >"$$log" 2>&1; then \
		cat "$$log" >&2; \
		exit 1; \
	fi; \
	if ! diff -rq "$$tmp" web/dist; then \
		echo "web-check: web/dist is stale; run make web and commit the rebuilt dashboard" >&2; \
		exit 1; \
	fi

release: web
	@mkdir -p $(DIST)
	@for target in $(PLATFORMS); do \
		set -- $$target; os=$$1; arch=$$2; \
		out="$(DIST)/lana-$$os-$$arch"; \
		echo "build $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags="-s -w $(LDFLAGS)" -o "$$out" ./cmd/lana || exit 1; \
		size=$$(wc -c < "$$out" | tr -d ' '); \
		printf '%-32s %s bytes\n' "$$out" "$$size"; \
		if [ "$$size" -gt 41943040 ]; then echo "note: $$out exceeds 40 MB target"; fi; \
	done

# checksums writes dist/SHA256SUMS for the release binaries (install.sh checks it).
checksums:
	cd $(DIST) && (command -v sha256sum >/dev/null && sha256sum lana-* || shasum -a 256 lana-*) > SHA256SUMS
	@cat $(DIST)/SHA256SUMS

clean:
	rm -rf $(DIST)
