# Tasks: Daemon and Telegram channel

- **Issue**: —
- **Requirements**: [requirements.md](requirements.md)
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Design**: [design.md](design.md)
- **Requirements revision**: 1
- **Contract revision**: 1
- **Design revision**: 1
- **Status**: Completed

## Execution rules

- Work in task order unless the graph allows parallel work.
- Each task keeps `go test ./...` green.
- Required verification is never optional.
- Mark a task complete only after verification evidence has been recorded.

## Coverage

| Acceptance criterion | Contract operation | Evidence owner | Issue/PR | Verification | Evidence |
|---|---|---|---|---|---|
| `AC-TG-008` | `API-TG-003` | `T-001` | — | `internal/tgfmt` tests | Pass |
| `AC-TG-004`, `AC-TG-006` | `API-TG-002` | `T-002` | — | `lana` session method tests | Pass |
| `AC-TG-019` through `AC-TG-021` | `API-TG-006` | `T-003` | — | `channels/telegram` api/poll tests | Pass |
| `AC-TG-001` through `AC-TG-003`, `AC-TG-005`, `AC-TG-007`, `AC-TG-009` through `AC-TG-011`, `AC-TG-015` | `API-TG-001` through `API-TG-003` | `T-004` | — | `channels/telegram` chat tests | Pass |
| `AC-TG-012` through `AC-TG-014` | `API-TG-004` | `T-005` | — | approval tests | Pass |
| `AC-TG-016` through `AC-TG-018` | `API-TG-005` | `T-006` | — | command tests | Pass |
| `AC-DMN-001` through `AC-DMN-004` | `API-DMN-001` | `T-007` | — | `cmd/lana/daemon_test.go` | Pass |

## Task graph

```mermaid
flowchart LR
  T1["T-001: toolfmt + tgfmt"] --> T4["T-004: chat worker and live message"]
  T2["T-002: runtime session methods + lease"] --> T4
  T3["T-003: Bot API client and poll loop"] --> T4
  T4 --> T5["T-005: approval keyboard"]
  T4 --> T6["T-006: commands"]
  T5 --> T7["T-007: lana daemon"]
  T6 --> T7
  T7 --> T8["T-008: final verification"]
```

## Tasks

- [x] `T-001` Move `primaryArg` to `internal/toolfmt` and add `internal/tgfmt` (Markdown to Telegram HTML, split)
  - Issue/PR: `—`
  - Depends on: `None`
  - Requirements: `REQ-TG-007`, `REQ-TG-008`, `REQ-TG-009`
  - Acceptance criteria: `AC-TG-008`
  - Contract operations: `API-TG-003`
  - Scope:
    - TUI uses `toolfmt.PrimaryArg`; existing TUI tests stay green.
    - `tgfmt.ToHTML`, `tgfmt.Split` with code-block reopen.
  - Verification:
    - `go test ./internal/... ./channels/cli/...`
  - Completion evidence:
    - Status: Completed
    - Command/test: `go test ./internal/tgfmt/ ./channels/cli/tui/` (TestToHTML, TestPlain, TestSplitParagraphs, TestSplitReopensCodeBlock, TestSplitHardCutAvoidsEntity; TUI TestPrimaryArg unchanged)
    - Result: pass
    - PR/commit: —
    - Deviation: `toolfmt.PrimaryArg` also summarizes `memory_read`/`memory_write`

- [x] `T-002` Add `Runtime.ChannelSession`, `ActiveAgent`, `CreateChannelSession`, `LeaseSession`; take the lease in `lana chat`
  - Issue/PR: `—`
  - Depends on: `None`
  - Requirements: `REQ-TG-003`, `REQ-TG-005`; spec #1 session lock
  - Acceptance criteria: `AC-TG-004`, `AC-TG-006`
  - Contract operations: `API-TG-002`
  - Scope:
    - Session lookup by channel, address, agent; workspace default `$LANA_HOME/workspaces/<agent>/`.
    - `lana chat` leases its session and sessions it switches to.
  - Verification:
    - Root package tests with SQLite store; `cmd/lana` lease test.
  - Completion evidence:
    - Status: Completed
    - Command/test: `go test .` (TestChannelSessions, 30 runs), `go test ./cmd/lana/` (TestLeaseSetRejectsSecondHolder)
    - Result: pass
    - PR/commit: —
    - Deviation: `lana chat` now leases its session (spec #1 `REQ-SESS` lock, previously not enforced)

- [x] `T-003` Implement the Bot API client, config parsing, and the poll loop
  - Issue/PR: `—`
  - Depends on: `None`
  - Requirements: `REQ-TG-001`, `REQ-TG-014`, `REQ-TG-015`
  - Acceptance criteria: `AC-TG-019` through `AC-TG-021`
  - Contract operations: `API-TG-001`, `API-TG-006`
  - Scope:
    - `api.go`, `config.go`, `channel.go` poll/dispatch, fake Bot API for tests.
  - Verification:
    - `go test ./channels/telegram/` including backoff, 429, 401, offset.
  - Completion evidence:
    - Status: Completed
    - Command/test: `go test -race ./channels/telegram/` (TestParseConfig, TestStartRegistersCommandsAndRejectsBadToken, TestPollBackoffOffsetAndUnauthorized)
    - Result: pass
    - PR/commit: —
    - Deviation: None

- [x] `T-004` Implement the chat worker, live message, groups, inbox, and non-text replies
  - Issue/PR: `—`
  - Depends on: `T-001`, `T-002`, `T-003`
  - Requirements: `REQ-TG-002` through `REQ-TG-010`, `REQ-TG-012`
  - Acceptance criteria: `AC-TG-001` through `AC-TG-003`, `AC-TG-005`, `AC-TG-007`, `AC-TG-009` through `AC-TG-011`, `AC-TG-015`
  - Contract operations: `API-TG-001` through `API-TG-003`
  - Scope:
    - Worker per chat, throttled editor with injectable clock, typing ticker, tool lines, end states, split.
  - Verification:
    - `go test -race ./channels/telegram/`
  - Completion evidence:
    - Status: Completed
    - Command/test: `go test -race -count=3 ./channels/telegram/` (TestPrivateChatTurn, TestStrangerIgnored, TestGroupMentionOnly, TestInboxDuringTurnAndCancel, TestLongAnswerSplit, TestErrorAndParseFallback, TestStreamingEditsAreThrottled, TestNonTextAndCommands)
    - Result: pass
    - PR/commit: —
    - Deviation: None

- [x] `T-005` Implement the approval keyboard
  - Issue/PR: `—`
  - Depends on: `T-004`
  - Requirements: `REQ-TG-011`
  - Acceptance criteria: `AC-TG-012` through `AC-TG-014`
  - Contract operations: `API-TG-004`
  - Scope:
    - `Channel.Approve`, callbacks, owner check, timeout, cancel on shutdown.
  - Verification:
    - Approval tests with the fake Bot API.
  - Completion evidence:
    - Status: Completed
    - Command/test: `go test -race ./channels/telegram/` (TestApprovalFlow, TestApproveUnknownSession)
    - Result: pass
    - PR/commit: —
    - Deviation: The approval label uses the tool line of the live message; `ApprovalRequest.Label` when the call is unknown. Manual test found approvals answered `unavailable`: the runtime only routed `library`/`cli` sessions to `ApproveWith`; fixed with `lana.ApproveChannels` (TestApproveChannels)

- [x] `T-006` Implement bot commands
  - Issue/PR: `—`
  - Depends on: `T-004`
  - Requirements: `REQ-TG-013`
  - Acceptance criteria: `AC-TG-016` through `AC-TG-018`
  - Contract operations: `API-TG-005`
  - Scope:
    - `/start`, `/help`, `/new`, `/agent` (with keyboard), `/memory`, `/compact`, `/cancel`, `/status`; owner-only in groups.
  - Verification:
    - Command tests with the fake Bot API.
  - Completion evidence:
    - Status: Completed
    - Command/test: `go test -race ./channels/telegram/` (TestNonTextAndCommands, TestStartRegistersCommandsAndRejectsBadToken)
    - Result: pass
    - PR/commit: —
    - Deviation: Agent buttons pressed by a non-owner in a group answer `only the owner can switch agents` (added to the contract)

- [x] `T-007` Implement `lana daemon`
  - Issue/PR: `—`
  - Depends on: `T-005`, `T-006`
  - Requirements: `REQ-DMN-001` through `REQ-DMN-004`
  - Acceptance criteria: `AC-DMN-001` through `AC-DMN-004`
  - Contract operations: `API-DMN-001`
  - Scope:
    - Lock file, config checks and exit codes, runtime with `ApproveWith`, backend adapter, signals, help text.
  - Verification:
    - `cmd/lana/daemon_test.go` with the fake Bot API and `MockLlm`.
  - Completion evidence:
    - Status: Completed
    - Command/test: `go test ./cmd/lana/` (TestDaemonConfigErrors, TestDaemonSingleInstance, TestDaemonAnswersAndStops, TestDaemonCancelsRunningTurn)
    - Result: pass
    - PR/commit: —
    - Deviation: `Runtime.Run` does not stop when its ctx ends, so the daemon backend calls `Runtime.Cancel` on shutdown

- [x] `T-008` Run final verification and a manual smoke test with a real bot
  - Issue/PR: `—`
  - Depends on: `T-007`
  - Requirements: all
  - Acceptance criteria: all
  - Contract operations: all
  - Scope:
    - Full suite, race, lint, release build; manual test with the owner's bot token.
  - Verification:
    - `nix develop -c go test -count=1 ./...`
  - Completion evidence:
    - Status: Completed
    - Command/test: `go test -count=1 ./...`, `go test -race` (channels, cmd, internal, root), `golangci-lint run ./...` (0 issues), `make release`; manual smoke with @lana_orc_bot from a private chat in a temp `LANA_HOME`: streamed answers, tool lines, `bash` approval with buttons, `/agent`, `/status`, `/memory`
    - Result: pass
    - PR/commit: —
    - Deviation: The manual test found approvals answered `unavailable` (fixed with `lana.ApproveChannels`) and repeated onboarding interviews (removed in spec #5 revision 2)

## Final verification

- [x] Every acceptance criterion has passing evidence.
- [x] No observable behavior exists without a requirement ID.
- [x] No behavior is outside scope.
- [x] ADRs and diagrams match the implementation.
- [x] `gofmt -l .` is empty, `golangci-lint run ./...`, `go test -race` (changed packages), and the release build pass.
- [x] Deviations are resolved in requirements or design, not only recorded here.

## Approval

- [x] Requirements and design are Approved and their revisions are recorded.
- [x] Contract is Approved.
- [x] Every acceptance criterion has exactly one evidence owner.
- [x] Every task has a limited scope, dependencies, and verification.
- [x] No task introduces new behavior.
- **Approved by**: Ridho ("oke sip lakukan", 2026-10-30)
- **Approved on**: 2026-10-30
