# Tasks: Images and file attachments

- **Status**: Completed

Test command: `nix develop -c go vet ./... && nix develop -c golangci-lint run ./... && nix develop -c sh -c 'CGO_ENABLED=1 go test -race ./...'`.

## Wave 1 (parallel, disjoint files)

### T-001 `internal/attach`

- Status: Done 2026-11-01. Evidence: internal/attach 16+1 tests, race ok, CGO_ENABLED=0 build ok.
- Files: new `internal/attach/attach.go`, `internal/attach/attach_test.go`, `go.mod`, `go.sum`.
- Covers: REQ-ATT-001, -002 (block building), -004 (helpers). Design §2.

### T-002 Core types, store, wire, vision flag

- Status: Done 2026-11-01. Evidence: wire, derive, store, catalog, config tests ok.
- Files: `core/chat.go`, `core/tool.go`, `core/event.go`, `core/config.go` (AgentConfig), `store/store.go`,
  `store/memstore/memstore.go`, `providers/openai/wire.go`, `internal/models/catalog.go`,
  `config/validate.go`, their tests.
- Covers: REQ-ATT-003 (flag), API-ATT-004, -005. Design §1, §3, §4 (flag and config).

## Wave 2 (after wave 1; parallel)

### T-003 Agent and library

- Status: Done 2026-11-01. Evidence: agent, compaction, memcapture, lana tests ok (race).
- Files: `agent/turn.go`, `agent/registry.go`, new `agent/attachments.go`, `agent/compaction/*.go`,
  `agent/memcapture/*.go`, `lana.go`, their tests.
- Covers: REQ-ATT-002, -003 (placeholder), -007 (compaction, capture), -008. Design §4–§6.

### T-004 `read_file` on images and PDFs

- Status: Done 2026-11-01. Evidence: tools tests ok (images, PDF, scanned PDF).
- Files: `tools/files.go`, `tools/files_test.go`; the turn already copies `ToolOutput.Images` (T-003
  owns that line).
- Covers: REQ-ATT-004. Design §7.

## Wave 3 (after T-003; parallel)

### T-005 Telegram

- Status: Done 2026-11-01. Evidence: telegram tests ok (race x3); album race regression test fails 27/50 on the old code, passes 50/50.
- Files: `channels/telegram/*.go`, `cmd/lana/daemon.go` (Send), tests.
- Covers: REQ-ATT-005. Design §8.

### T-006 Web

- Status: Done 2026-11-01. Evidence: web and cmd/lana tests ok; vitest 135/135; tsc, eslint clean; make web-check ok.
- Files: new `web/attachments.go`, `web/turns.go`, `web/sessions.go` (backend interface, event
  filter), `cmd/lana/web.go`, `web/ui/src/**`, `web/dist`, tests.
- Covers: REQ-ATT-006, API-ATT-001–004. Design §9.

### T-007 TUI history

- Status: Done 2026-11-01. Evidence: tui restore tests and derive prune test ok.
- Files: `channels/cli/tui/*.go` (history rendering only), tests.
- Covers: REQ-ATT-007 (TUI). Design §6.

## Final evidence (2026-11-01)

gofmt clean, go vet ok, golangci-lint 0 issues, `CGO_ENABLED=1 go test -race ./...` 31 packages ok,
`CGO_ENABLED=0 go build ./...` ok, `make web-check` ok, vitest 135/135, tsc clean. Real model
(`deepseek-flash`): one owner message with 3 images and 1 PDF through `lana.Request.Attachments`;
every value read correctly (invoice Rp 1.275.000, receipt Rp 58.000, menu prices, PDF receipt
Rp 2.500.000); 5 796 input tokens.
