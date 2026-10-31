# Tasks: Model settings, thinking, and the DeepSeek catalog

Runs in the worktree branch `spec-21-model-settings`, merged into `development` after spec #20.
Test command: `nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`; UI tasks
also `cd web/ui && npx vitest run && npm run build`.

## Wave 1

### T-001 Thinking on the wire and in the turn

- Files: `core/chat.go`, `core/config.go` (two keys), `config/validate.go`, `providers/openai/*.go`, `agent/turn.go` (effort resolution, stream collection, storage, live frame), new `internal/models/` (catalog only as needed for the default effort), tests.
- Covers: REQ-MDL-001–005 (turn side; compaction/memory capture requests get the field in T-002), design §1, §3, §4.
- Acceptance: design §7 wire, SSE, and turn cases.

## Wave 2

### T-002 Catalog, compaction window, presets, display filtering

- Files: `internal/models/`, `agent/compaction/*.go`, `core/compaction.go`, `cmd/lana/main.go` presets, Go display collectors (design §5 Go list), tests.
- Covers: REQ-MDL-005 (compaction), REQ-MDL-006 (Go), REQ-MDL-010–012 (catalog, window, presets).
- Note: the memory capture request (spec #20 T-002) gets `ReasoningEffort` during the merge.

### T-003 Web

- Files: `web/agents.go`, new models endpoint, `web/ui/src/views/agent-detail.tsx`, `web/ui/src/views/turn.tsx`, `web/ui/src/views/session.tsx`, `web/ui/src/lib/format.ts`, tests, `web/dist`.
- Covers: REQ-MDL-006 (web), REQ-MDL-012 (banner), REQ-MDL-020.
