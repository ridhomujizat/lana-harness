VERSION ?= $(shell cat VERSION 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DIRTY := $(shell git diff --quiet HEAD -- . ':!design' 2>/dev/null || echo -dirty)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)$(DIRTY) -X main.buildDate=$(BUILD_DATE)
GO ?= go
DIST ?= dist
PREFIX ?= $(HOME)/.local
PLATFORMS := "darwin arm64" "darwin amd64" "linux amd64" "linux arm64"

.PHONY: build install release checksums clean web web-check tag macos-app macos-app-test

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

# macos-app builds the macOS menu bar app (spec #41) into dist/Lana.app. It is
# a separate Swift package under apps/macos/LanaMenu built with the Xcode
# toolchain, so the Go build stays CGO-free (ADR 0013) and `make build` never
# needs Swift. The bundle is ad-hoc signed. Inside `nix develop` the Nix SDK
# variables are cleared for swift so the Xcode SDK is used.
MACOS_PKG := apps/macos/LanaMenu
MACOS_APP := $(DIST)/Lana.app
MACOS_SHORT_VERSION := $(shell echo '$(VERSION)' | sed 's/-.*//')
SWIFT ?= env -u SDKROOT -u DEVELOPER_DIR -u NIX_CFLAGS_COMPILE -u NIX_LDFLAGS -u MACOSX_DEPLOYMENT_TARGET -u CC -u CXX -u LD /usr/bin/xcrun swift

macos-app:
	@test "$$(uname -s)" = Darwin || { echo "macos-app: needs macOS" >&2; exit 1; }
	cd $(MACOS_PKG) && $(SWIFT) build -c release --product LanaMenu
	rm -rf "$(MACOS_APP)"
	mkdir -p "$(MACOS_APP)/Contents/MacOS" "$(MACOS_APP)/Contents/Resources"
	cp "$$(cd $(MACOS_PKG) && $(SWIFT) build -c release --show-bin-path)/LanaMenu" "$(MACOS_APP)/Contents/MacOS/LanaMenu"
	sed -e 's/@VERSION@/$(VERSION)/' -e 's/@SHORT_VERSION@/$(MACOS_SHORT_VERSION)/' $(MACOS_PKG)/Info.plist.in > "$(MACOS_APP)/Contents/Info.plist"
	printf 'APPL????' > "$(MACOS_APP)/Contents/PkgInfo"
	@if command -v iconutil >/dev/null; then \
		mkdir -p "$(DIST)/.lana-iconset" && \
		"$(MACOS_APP)/Contents/MacOS/LanaMenu" --write-iconset "$(DIST)/.lana-iconset/AppIcon.iconset" && \
		iconutil -c icns -o "$(MACOS_APP)/Contents/Resources/AppIcon.icns" "$(DIST)/.lana-iconset/AppIcon.iconset" && \
		rm -r "$(DIST)/.lana-iconset"; \
	else echo "macos-app: iconutil not found; the app has no icon"; fi
	codesign -s - --force --deep "$(MACOS_APP)"
	codesign --verify "$(MACOS_APP)"
	@echo "built $(MACOS_APP) ($(VERSION))"

# macos-app-test runs the Swift unit tests of the menu bar app.
macos-app-test:
	cd $(MACOS_PKG) && $(SWIFT) test
