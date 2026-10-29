VERSION ?= dev
GO ?= go
DIST ?= dist
PLATFORMS := "darwin arm64" "linux amd64" "linux arm64"

.PHONY: release clean

release:
	@mkdir -p $(DIST)
	@for target in $(PLATFORMS); do \
		set -- $$target; os=$$1; arch=$$2; \
		out="$(DIST)/lana-$$os-$$arch"; \
		echo "build $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o "$$out" ./cmd/lana || exit 1; \
		size=$$(wc -c < "$$out" | tr -d ' '); \
		printf '%-32s %s bytes\n' "$$out" "$$size"; \
		if [ "$$size" -gt 41943040 ]; then echo "catatan: $$out melebihi target 40 MB"; fi; \
	done

clean:
	rm -rf $(DIST)
