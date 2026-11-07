# Tasks: Claude Agent runtime

- **Status**: In Progress · branch `feature/router` (worktree `../lana-harness-router`)
- **Requirements** r1 · **Contract** r1 · **Design** r1 (Approved 2026-11-08)

| ID | Slice | Covers | Evidence |
|---|---|---|---|
| T-CCR-01 | `core/runtime.go`; `runtime/claudecode` (process, protocol, pool + semaphore, status, tool classes); fake `claude` + recorded fixtures; archtest | REQ-CCR-002, 003, 006, 007 (cap); AC-CCR-002 (runtime side), 003, 006, 007 | Pending |
| T-CCR-02 | `config` runtime keys; `agent` external turn branch, events, approvals, cancel, child sessions; calendar/workflow `allow_scheduled`; root wiring of usage + limits into the router | REQ-CCR-001, 002, 004, 005, 007, 008; AC-CCR-001, 002, 004, 005, 008, 009 (data side) | Pending |
| T-CCR-03 | Web: `GET /v1/router/claude-agent`, agent endpoints, UI agent form runtime switch, profile label, Router Claude Agent card | REQ-CCR-008, 009; AC-CCR-009, 010 | Pending |
| T-CCR-04 | Runtime smoke with the real logged-in Claude Code through the daemon (chat, one approval, cancel, Router card) | SC smoke | Pending |
