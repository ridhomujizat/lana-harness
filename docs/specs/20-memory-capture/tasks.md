# Tasks: Memory capture and tool-call safety nets

Each task is one `dsh-coder` run. Test command for every task:
`nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`; UI tasks also
`cd web/ui && npx vitest run && npm run build`.

## Wave 1

### T-001 Prompt section, runtime notes, tool-call-text nudge

- Status: Done 2026-10-31. Evidence: `go vet ./...` clean, `go test -count=1 ./...` all ok.

- Files: `internal/agenthome/prompt.go`, `agent/turn.go`, new `agent/toolcalltext.go`, `core/event.go` (`UserMessage.Runtime`), tests.
- Covers: REQ-MEM-001, REQ-MEM-020, REQ-MEM-021 (Go side), REQ-MEM-022. Design §1, §4, §5.
- Acceptance:
  - An agent with `memory_write` has the `# Memory capture` section; without it, none. Two turns send byte-identical `System`.
  - Empty-answer, step-limit, and tool-call-text notes are stored with `runtime: true`.
  - An assistant reply with tool-call markup and no tool call gets one nudge step; at most 2 per turn; prose that mentions a tool name does not.

## Wave 2

### T-002 Capture service

- Files: new `core/memcapture.go`, new `agent/memcapture/` (module, service, instruction, tests), `agent/registry.go` (sink + getter), `agent/turn.go` (call at `finished` only), `core/event.go` (`MemoryCaptured`, kind), `core/config.go` (`MemoryCapture *bool`), `config/validate.go`, `store/store.go` (`validKinds`), module registration where other modules are imported.
- Covers: REQ-MEM-010–015. Design §2, §3 (config only).
- Acceptance: design §7 capture cases.

### T-003 Web: toggle, runtime hiding, memory line

- Files: `web/agents.go`, `web/agents_test.go`, `web/ui/src/views/agent-detail.tsx`, `web/ui/src/lib/format.ts`, `web/ui/src/views/turn.tsx`, tests, `channels/cli/tui/model.go` (runtime skip), `web/dist`.
- Covers: REQ-MEM-021 (display), REQ-MEM-030, design §3 (web).
- Acceptance: `memory_capture` round-trips through the agent API; runtime notes hidden in web and TUI restore; `memory/captured` renders `Saved to memory: …`.
