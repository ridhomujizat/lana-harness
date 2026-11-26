# Spec 83: Antigravity panes resume and switch accounts like Claude Code

- **Status**: Approved (owner 2026-11-26: "yg antigravity ga sekalian skemanya di buat seperti update claude?")
- **Revision**: 1
- **Branch**: feature/account-switch (with spec 82)
- **Related**: spec 60 (restore), spec 77 (Workbench accounts, REQ-WRA-009..011), spec 82 (Switch here)

## Context

Spec 82 lets a Claude Code pane continue its conversation on another account in place. Antigravity (`agy`)
panes still only have Beside, and a restored agy pane comes back as a bare shell: the restore adapters of
spec 60 know claude and omp only. An agy account is a HOME (`<LANA_HOME>/antigravity/<name>`, the built-in
`antigravity` uses the owner's HOME); its conversations and settings, `mcp_config.json` included, are links
to the owner's (spec 77 REQ-WRA-010, REQ-WRA-011), so `agy --conversation <id>` finds a conversation under any
account.
Whether a conversation made under one Google account resumes under another is unverified (spec 77
REQ-WRA-010: conversations hold `cascade_id`/`gen_metadata` blobs); this spec needs the owner's live check.

## Requirements

### REQ-WAG-001: The Workbench learns an agy pane's conversation

agy gives every hook its `conversationId` on stdin (REQ-WRA-009). Inside a Workbench pane, Lana's agy
SessionStart and post-edit hooks record it for the pane: `<LANA_HOME>/workbench/agents/<pane id>.json`
(0600, atomic) holding `agent: "agy"`, the conversation id (`^[A-Za-z0-9][A-Za-z0-9_-]{7,127}$`, anything else
is not written), the hook's `HOME`, and the time. The core gives each pane `LANA_WORKBENCH_HOME` (its Lana
home), so a hook running under an account HOME finds the right folder. The core's agy adapter reads the record
of the pane, newer than the moment it first saw that agy process; the record's HOME is the pane's account
(`""` when it is the owner's HOME). Recording never changes the hook's answer and never fails it.

### REQ-WAG-002: Restore

A restored agy pane is a shell with the resume line typed (spec 60): `agy --conversation <id>` for a validated
id, else `agy --continue`; for a named account it runs under that account exactly as the popup launches it:
`env HOME='<home>' SSH_CONNECTION='127.0.0.1 0 127.0.0.1 0' agy …`. A home that does not exist or cannot be
quoted falls back to the plain form.

### REQ-WAG-003: Switch here for Antigravity

The popup's Antigravity **Beside** is replaced by **Switch here**, with spec 82's rules: it acts on the
focused pane, is off with the reason as its tooltip ("No Antigravity conversation in this pane", "This pane was
opened before account switching; use New tab", or the pane's own account), and asks first when the pane is
working ("Antigravity is working in this pane. Stop it and switch to <account>?").
`POST /v1/workbench/sessions/{id}/switch-account` takes `{"agy_account": "<name>"}` (`antigravity` for the
built-in account) instead of `claude_account`; the core stops agy with Ctrl+C exactly as spec 82 stops claude,
then types the REQ-WAG-002 line for the new account with `--conversation <id>` and records the new home. The
409 codes are spec 82's; `still_running` reads "Antigravity did not exit. Exit it in the pane and try again."

### REQ-WAG-004: MCP servers stay shared

`agy mcp add` may write `mcp_config.json` by replacing the account's link with a file. Before the daemon or
the core starts agy for a named account (New tab, Switch here, restore), Lana relinks
`<home>/.gemini/config/mcp_config.json` to the owner's file the way REQ-WRA-011 does: an existing link is kept,
a real file is renamed `mcp_config.json.before-share-<unix>` and linked, and nothing happens when the owner has
no such file. Fail-open: a failure is logged and the launch goes on.

## Acceptance

- `AC-WAG-001`: a post-edit or SessionStart hook in a pane writes the record; outside a pane, or with an
  invalid id, nothing is written and the answer is unchanged.
- `AC-WAG-002`: a restored agy record of a named account types `env HOME='<home>' SSH_CONNECTION=… agy
  --conversation <id>`; without an id `… agy --continue`; the built-in account types `agy --conversation <id>`.
- `AC-WAG-003`: core with a fake shell pane whose foreground child is a fake agy that exits on Ctrl+C: switch
  to a named account types the account's line with `--conversation <id>`; the record's home is the new one;
  a claude-account switch of an agy pane is `no_conversation`.
- `AC-WAG-004`: the popup shows Switch here (not Beside) for Antigravity accounts with spec 82's flow and
  calls the endpoint with `agy_account`.
- `AC-WAG-005`: an account `mcp_config.json` that became a file is renamed and relinked on switch; a link is
  kept.
- `go test ./...`, `golangci-lint run`, web typecheck, tests and eslint green. Tests never run a real `agy` or
  `claude`.

## Out of scope

Antigravity conversations started outside the Workbench, a conversation changed inside a running agy (the first
recorded id of that agy process wins), a custom agy binary path in the typed lines (they run `agy` from PATH).
