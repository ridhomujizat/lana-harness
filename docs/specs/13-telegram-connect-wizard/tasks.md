# Tasks: Telegram connect flow for non-technical owners

- **Requirements**: revision 1 · **Contract**: revision 1 · **Design**: revision 1
- **Status**: In Progress

`T-001` and `T-002` touch separate files and run in parallel. `T-002` codes against the contract.

- [x] `T-001` Backend: token save, discover, connect
  - Requirements: `REQ-TGC-001` through `REQ-TGC-003`
  - Acceptance criteria: `AC-TGC-001` through `AC-TGC-007`, `AC-TGC-009`
  - Contract: `API-TGC-001` through `API-TGC-003`
  - Files: `web/channels.go`, `web/secrets.go` (shared helper only), new `web/channels_connect_test.go`, `channels/telegram/api.go` (+ test), `cmd/lana/web_channels.go` (+ test), `cmd/lana/web.go` only if the port must be wired there.
  - Verification: `nix develop -c go test ./web/... ./channels/... ./cmd/...`
  - Evidence: `go test ./web/... ./channels/... ./cmd/...` pass (channels_connect_test.go, channels/telegram/api_test.go, cmd/lana/web_channels_test.go). Deviation: polling check lives in handleDiscoverTelegram; optional TelegramBotNamer port.

- [x] `T-002` Page: three-step Telegram connect
  - Requirements: `REQ-TGC-002` through `REQ-TGC-004` (UI parts)
  - Acceptance criteria: `AC-TGC-005`, `AC-TGC-008`, `AC-TGC-010`, `AC-TGC-011`
  - Files: `web/assets/views/channels.js`, `web/assets/api.js`, `web/assets/app.css` (only new classes), `web/assets_test.go` only if it asserts asset content.
  - Verification: `nix develop -c go test ./web/...`; manual check in the browser.
  - Evidence: `go test ./web/...` pass incl. asset tests after the `bot_url` follow-up (contract "Bot link"). Browser check pending (owner).

- [ ] `T-003` Final verification
  - `gofmt -l`, `golangci-lint run ./...`, `go test -race ./...`; owner connects a real bot from the page without a terminal.
  - Evidence: Partial: gofmt clean, golangci-lint 0 issues, `go test -race ./...` pass; real-bot check by the owner pending.
