# Tasks: Turn feedback, graceful restart, and live config changes

- **Requirements**: revision 1 · **Contract**: revision 1
- **Status**: In Progress

## Design notes

- **Drain** lives in `cmd/lana/daemon.go` + `restart.go`: a drain controller with states idle → draining → stopping. It asks the runtime for running turns (`lana.Runtime` already lists them for `GET /v1/daemon`), refuses new turns while draining (a runtime gate checked by `RunTurn`/web send/Telegram), waits, then cancels the rest with cause `interrupted`. SIGTERM/SIGINT use the same controller; a second signal forces.
- **Interrupted reason**: cancel turns with a context cause (`context.WithCancelCause`) carrying a sentinel; `agent/turn.go` maps that cause to reason `interrupted` and the message, instead of `cancelled`.
- **Continue**: `lana.Runtime.Continue(ctx, sessionID)` reads the last `turn/end`, refuses per contract, and starts a turn whose user message carries `continue`/`continue_reason`. Web handler and Telegram callback call it.
- **Live config**: `agent.Registry.ApplyConfig(cfg core.Config)` swaps the agents/teams config snapshot under a lock (resolve reads it per turn) and, for MCP, calls the existing `PutServer`/`RemoveServer` for added/changed/removed `[mcp.*]` and clears per-agent overlays that the new config now covers. `cmd/lana/web.go` calls it after a successful agent/team/MCP save via `config.Check` of the written file.
- **MCP state**: widen `core.MCPAdmin` with `ServerStatus(name string) (state string, tools int, reason string, known bool)` implemented by the registry from `mcp.Manager.Status`.

## Tasks

- [x] `T-001` Drain, interrupted reason, restart API — `cmd/lana/daemon.go`, `restart.go`, `web/daemon.go`, `agent/turn.go` (cause mapping only), `lana.go` (gate + running count), Telegram draining reply in `channels/telegram/chat.go` (one guard), tests. ACs `AC-TFB-001`–`006` (backend parts).
- [x] `T-002` Continue + Telegram end card — `lana.go` (`Continue`), `web/turns.go` (`/continue`), `channels/telegram/chat.go`/`approve.go`-style callback for `c:`, tests. ACs `AC-TFB-010`–`012` (backend), Telegram part of `007`–`009`. Depends on `T-001`.
- [x] `T-003` Live agent/team/MCP config + MCP state — `agent/registry.go`, `agent/turn.go` (resolve snapshot), `core/mcp.go`, `tools/mcp_manage.go`, `cmd/lana/web.go`, `web/mcp.go`, `web/agents.go`, `web/teams.go` (restart_required), tests. ACs `AC-TFB-013`–`017`. Depends on `T-001` (shares `agent/turn.go`).
- [x] `T-006` Agent reports blockers — `internal/agenthome/prompt.go` (+ test), `agent/turn.go` (empty-answer guard, `not loaded` reason), `mcp/tool.go` (reachability error text), tests. ACs `AC-TFB-018`–`021`. Depends on `T-003` (shares `agent/turn.go`).
- [x] `T-004` Web UI: status card + Continue, `↻ Continued` chip, restart dialog waiting state with Restart now / Cancel, drop restart banners for live-applied saves — `web/ui/src/views/session.tsx`, `turn.tsx`, `settings-general.tsx`, `settings-pages.tsx`, `channels.tsx`, `src/api/*`, tests; rebuild `web/dist`. ACs `AC-TFB-005`–`008`, `011` (UI). Depends on spec #14 `T-007` and `T-001`–`T-003`.
- [ ] `T-005` Final verification + owner check.

## Evidence

- `T-001`: `go test -race` on cmd, web, agent, channels, root pass; drain_test, restart_drain_test, draining_test, interrupt_test, lana_drain_test. Daemon restarted with it on 2026-10-30 23:59.
- `T-003`: `go test -race ./...` pass; registry_apply_test, live_config_test, web_live_config_test cover AC-TFB-013..017. Note: SetAgent no longer pins provider/model, so sessions follow a web model change; explicit SetModel still wins. PUT /v1/config and setup still return restart_required true.
- `T-002` + `T-006` part B: `go test -race ./...` pass, golangci-lint 0 issues (continue_test.go, lana_continue_test.go, loop_test.go, mcp_test.go). First implementer run hit its 100-turn budget; a second run fixed two test fixtures.
- `T-004`: two implementer runs (first hit the turn budget). web/ui typecheck, lint, 16 files / 73 Vitest tests pass. Restart dialog shared by Settings and Telegram connect; agent/MCP banners already follow `restart_required`. `make web` pending (run once with spec 18 UI).
- `T-006` part A: `go test -race ./internal/agenthome/... ./mcp/...` pass.
- Whole tree on 2026-10-31: gofmt clean, golangci-lint 0 issues (after three one-line fixes), `go test -race ./...` pass, `make web-check` clean.
