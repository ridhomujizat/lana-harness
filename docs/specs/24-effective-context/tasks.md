# Tasks: Effective context

Test command: `nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`.

T-001, T-003, and T-004 run in parallel worktrees. T-002 starts after T-001 is merged (both touch
`internal/agenthome/prompt.go` and `agent/turn.go`).

## Wave 1

### T-001 Tool guidance, blocker split, wording

- Status: Todo
- Files: `core/tool.go`, `tools/files.go`, `tools/bash.go`, `tools/ask_user.go`, `tools/memory.go`
  (guidance only), `tools/skills.go`, `internal/agenthome/prompt.go`, `agent/turn.go`, tests.
- Covers: REQ-ECX-001, REQ-ECX-010–012. Design §1.

### T-003 Eval tasks and multi-model runs

- Status: Todo
- Files: `cmd/lana-eval/*`.
- Covers: REQ-ECX-040–042. Design §4.

### T-004 Compaction limits

- Status: Todo
- Files: `agent/compaction/compaction.go`, `agent/compaction/compaction_test.go`.
- Covers: REQ-ECX-030. Design §3.

## Wave 2

### T-002 Memory index, relevance, memory_read search, hygiene

- Status: Todo
- Files: new `internal/agenthome/memory.go`, `internal/agenthome/agenthome.go` (limit),
  `internal/agenthome/prompt.go`, `tools/memory.go`, `agent/turn.go`,
  `agent/memcapture/instruction.go`, tests.
- Covers: REQ-ECX-020–026. Design §2.
