# Spec 51 tasks and evidence

| Task | Commit | State |
|---|---|---|
| UI: one focus ring in the composer; Proposed fix values wrap in a code block (REQ-WFC-010) | 51963d7 | done |
| Part B: guardian chat session, context note, `workflow_builder` tool, HTTP routes (REQ-WFC-001 to 004, 006, 009) | d3720b9 | done |
| Part A: candidate versions, candidate test runs, engine write-backs (REQ-WFC-003, 005, 006) | f1a3ea9 | done |
| Part C: web column shows the session (tool rows, `ask_user`, approvals, uploads, `/new`, context meter) (REQ-WFC-007, 008) | 92cc4f6 | done |
| Claude Code: `MCP_TOOL_TIMEOUT` so `ask_user` and tests outlive the 60 s MCP default | 38b5102 | done |
| `ask_user` from a workflow session is recorded as answered on the web | 889256e | done |
| web/dist rebuilt | 33186a0 | done |

## Gates (2026-11-12)

- `go build ./...`: OK.
- `go vet`: OK.
- `go test -race ./...`: all packages pass.
- `golangci-lint` on the changed packages: 0 issues.
- web: `tsc -b` is clean, `eslint` is clean, and `vitest` passes 611/611 (62 files).

## Live smoke test

Setup:
- The daemon was built from this branch.
- It ran on a scratch `LANA_HOME` that copies `~/.lana`, at 127.0.0.1:18399.
- All workflows were paused and all calendar items were removed in the copy.
- The live daemon on :18300 was not touched.
- The guardian was Omnix Implementator (Claude Code, Opus).

1. **"Tampilkan prioritas, urutkan, pastikan sudah dites".** The guardian did the following in this order:
   - Called `get`.
   - Explored the real kerjan data with the MCP tool and bash. It found that kerjan has no `priority` field, only `severity`.
   - Wrote and tested the script locally.
   - Called `save`, which created candidate 1 on v7.
   - Called `test`, which started run #9 with trigger `test`. The run succeeded in 384 ms.
   - Called `publish`, which made v8. The change card appeared under that turn.

   See `evidence/chat-turn-live.png`, `evidence/tool-steps-live.png` and `evidence/change-card-live.png`.
2. **A file upload plus "tanya dulu jamnya".** The guardian did the following in this order:
   - Read the attached `catatan.txt`.
   - Called `ask_user` with the two times from the file. The question card stayed open for more than 60 s (see `evidence/ask-user-live.png`).
   - After it was answered "07:00", added `trigger.schedule` `0 7 * * 1-5`.
   - Called `save`, then `test` (run #10 succeeded), then `publish` (v9).

## Not covered

- Telegram.
- Approval cards in the column. These come from a guardian under an `ask` policy; the live guardian is `trust`.
- `/new` against a live daemon. It is covered only by the vitest test.
