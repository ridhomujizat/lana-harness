VERSION ?= dev
GO ?= go
DIST ?= dist
PLATFORMS := "darwin arm64" "linux amd64" "linux arm64"

.PHONY: release clean web web-check

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
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o "$$out" ./cmd/lana || exit 1; \
		size=$$(wc -c < "$$out" | tr -d ' '); \
		printf '%-32s %s bytes\n' "$$out" "$$size"; \
		if [ "$$size" -gt 41943040 ]; then echo "note: $$out exceeds 40 MB target"; fi; \
	done

clean:
	rm -rf $(DIST)
