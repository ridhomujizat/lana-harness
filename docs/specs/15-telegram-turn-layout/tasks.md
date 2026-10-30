# Tasks: Cleaner Telegram turn layout

- **Requirements**: revision 1 · **Contract**: Not required
- **Status**: Done (owner check pending)

## Design notes

- `channels/telegram/chat.go`: replace the per-tool `toolLine` rendering with turn state: current tool, step counter (rule in requirements "Step"), tool-call counter. `render` builds status line + answer while running, and answer + end line + footer when final. `toolArg` keeps working for approvals (it reads the call's argument).
- `channels/telegram/approve.go`: per-turn bubble on `chat` (message ID, FIFO of waiting requests, outcome tallies). `Approve` enqueues and waits for its turn at the head; the head is rendered into the bubble (send on first use, edit afterwards). Timeouts and cancellation start when a request reaches the head, as today per request.
- `finish`: summary edit, then send the final answer and `deleteMessage` the live message when a bubble exists; else edit in place. Reset bubble state for the next turn.
- `channels/telegram/api.go`: add `DeleteMessage(ctx, chatID, messageID)`; add it to the API interface used by the fake in tests.

## Tasks

- [x] `T-001` Implement the layout
  - Files: `channels/telegram/chat.go`, `approve.go`, `api.go`, `fake_test.go`, `telegram_test.go` (update expectations that assert 🔧 lines or per-approval messages), new `layout_test.go`.
  - Acceptance criteria: `AC-TGL-001`–`AC-TGL-009`.
  - Verification: `nix develop -c go test -race ./channels/telegram/... ./cmd/...`
  - Evidence: `go test -race ./channels/telegram/... ./internal/tgfmt/... ./cmd/...` pass (layout_test.go covers AC-TGL-001..009); gofmt clean.
- [x] `T-003` Markdown tables (revision 2)
  - Files: `internal/tgfmt/tgfmt.go`, `internal/tgfmt/tgfmt_test.go`.
  - Acceptance criteria: `AC-TGL-010`–`AC-TGL-012`.
  - Verification: `nix develop -c go test ./internal/tgfmt/... ./channels/telegram/...`
  - Evidence: `go test ./internal/tgfmt/... ./channels/telegram/...` pass (AC-TGL-010 exact `<pre>`, AC-TGL-011 screenshot table wide form, AC-TGL-012 fenced table untouched, escaping, padding, Split on plain `<pre>`).
- [ ] `T-002` Owner check in a real chat (MCP turn with several approvals)
  - Evidence: Pending
