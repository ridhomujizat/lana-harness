# Tasks: Claude Code as a router target

- **Status**: In Progress · branch `feature/router`
- **Requirements** r1 · **Contract** r1 · **Design** r1 (Approved 2026-11-08)

| ID | Slice | Covers | Evidence |
|---|---|---|---|
| T-CCT-01 | Backend: address + config + legacy mapping + background_model; per-turn engine dispatch + early fallback; prompt head + recall injection; `mcphost` loopback MCP with tokens and tool-runner dispatch; claudecode args; records/limits rename; tests | REQ-CCT-001..008; AC-CCT-001..010 | Pending |
| T-CCT-02 | UI: remove Engine choice; Claude Code models in pickers; agent form note + Claude-only keys; Router Subscriptions card; legacy save drops runtime; `make web` | REQ-CCT-009; AC-CCT-011 | Pending |
| T-CCT-03 | Runtime smoke with the real `claude` (persona, MCP memory tool + approval, combo fallback) and screenshots | all | Pending |
