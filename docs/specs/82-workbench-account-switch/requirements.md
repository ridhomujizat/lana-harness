# Spec 82: Switch a Workbench pane's Claude Code conversation to another account

- **Status**: Approved (owner 2026-11-26: "bisa ga sih beside itu replace session bukan bikin session baru di
  sampingnya. di ganti dengan cred account tersebut")
- **Revision**: 1
- **Branch**: feature/account-switch (carries the uncommitted fix/workbench-account-shell patch and spec 81)
- **Related**: spec 77 (Workbench accounts), spec 60 (restore, `resumeLine`), spec 81 (MCP mirror)

## Context

The Workbench Accounts popup's **Beside** opens a new pane next to the focused one and runs `claude
--continue` on the chosen account. The owner wants it to replace the focused pane's conversation instead:
the same conversation continues in the same pane, now on the chosen account's login, like switching with
claude-swap. Every account's `projects/` (where Claude Code keeps conversations) links to the same folder
(spec 77 REQ-WRA-001; claude-swap profiles do the same), so `claude --resume <id>` finds the conversation
under any account.

## Requirements

### REQ-WAS-001: Switch action

For a Claude Code account, the popup's **Beside** action is replaced by **Switch here** ("Continue this
pane's conversation on <account>"). It acts on the focused pane. It is enabled when the focused pane is a
Claude Code pane the Workbench knows a conversation id for (the agent session id that the hooks report,
spec 60) and whose process is a shell that runs claude (not a pane whose own command is claude); otherwise
it is disabled with the reason as its tooltip ("No Claude conversation in this pane", "This pane was opened
before account switching; use New tab"). Switching to the account the pane already runs on is disabled.
Antigravity accounts keep **Beside** as it is. **New tab** stays for both.

When the focused pane is working (a turn is running), the popup asks first: "Claude is working in this
pane. Stop it and switch to <account>?" with Switch / Cancel.

### REQ-WAS-002: Switch in the core

`POST /v1/workbench/sessions/{id}/switch-account` with `{"claude_account": "<name>"}` (`claude-code` for the
built-in account):

1. The daemon checks the account (a Claude Code account of the config, enabled or not), runs the spec 81
   MCP mirror for a named account (fail-open), and asks the Workbench core to switch.
2. The core checks the session: claude agent, a valid conversation id, a shell process. Else 409 with a
   code (`no_conversation`, `not_switchable`) and the message above; the pane is not touched.
3. The core stops claude: it writes Ctrl+C (0x03) to the pane, then again every 400 ms, until the pty's
   foreground process group is the shell's own (claude has exited and the shell owns the terminal again),
   at most 6 times / 5 s. If claude is still in the foreground then, 409 `still_running` ("Claude did not
   exit. Exit it in the pane and try again."); nothing is typed.
4. The core types the resume line for the new account and Enter: for a named account the spec 60
   `claudeResumeLine` form with its config dir (`CLAUDE_CONFIG_DIR='<dir>'
   CLAUDE_SECURESTORAGE_CONFIG_DIR='<dir>' claude --resume <id>`); for the built-in account `env -u
   CLAUDE_CONFIG_DIR -u CLAUDE_SECURESTORAGE_CONFIG_DIR claude --resume <id>`. Only a validated id is typed.
5. The pane's recorded agent config dir becomes the new account's (so a later restore resumes on it); the
   hooks of the new claude confirm it as usual.

The answer is 200 with the session. A core without the switch op (older running core, see
`Hello.Features`) answers 409 `core_outdated` ("Restart the Workbench to switch accounts in place"); the
popup then offers New tab.

### REQ-WAS-003: Docs

Spec 77's Workbench accounts section and the CHANGELOG (Unreleased → Changed) describe Switch here.

## Acceptance

- `AC-WAS-001`: core with a fake shell pane whose foreground child is a fake `claude` that exits on the
  second Ctrl+C: switch to a named account types `CLAUDE_CONFIG_DIR='…' … claude --resume <id>` once claude
  exited; the record's config dir is the new one.
- `AC-WAS-002`: a fake claude that ignores Ctrl+C → 409 `still_running` after ≤ 5 s, nothing typed.
- `AC-WAS-003`: pane without a conversation id → 409 `no_conversation`; a pane whose command is claude →
  409 `not_switchable`.
- `AC-WAS-004`: the popup shows Switch here (not Beside) for Claude accounts, disabled with the reason when
  the focused pane has no conversation, asks first when the pane is working, and calls the endpoint with
  the account; agy accounts still show Beside.
- `go test ./...`, `golangci-lint run`, web typecheck and tests green. Tests never run a real `claude`.

## Out of scope

Switching Antigravity panes, switching non-Workbench sessions, moving a conversation between different
`projects/` folders.
