# Requirements: Antigravity as a router target through `agy acp`

- **Issue**: — (owner request 2026-11-25)
- **Status**: Approved (owner: "terapkan antigravity mengikuti cliproxy-antigravity yang aman dan bisa multi account"; remaining details are agent recommendations)
- **Revision**: 1
- **Owner**: ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: [0022](../../adr/0022-antigravity-as-router-target-through-agy-acp.md), [0020](../../adr/0020-claude-code-as-router-target-with-lana-context.md), [0018](../../adr/0018-claude-code-as-external-agent-runtime.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner | "pelajari cara dapetin dan jalanin subscribe Claude Code dan Antigravity" (t3code, orca); "referensi antigravity udh terbukti aman" (cliproxy-antigravity); "terapkan … yang aman dan bisa multi account" | goal |
| cliproxy-antigravity (MIT, commit 03705b2) | `agy.go` argv, `limits.go` `/usage`, `login.go` sign-in by `$BROWSER` capture and typed code, `config.go` `HOME` per account | runtime, usage, sign-in, accounts |
| T3 Code, Orca | `agy acp` over ACP; Orca bots run turns on the owner's machine | ACP choice |
| agy 1.5.17 on the owner's Mac (2026-11-25) | `HOME` change hides the login Keychain and agy falls back to file storage; ACP `initialize`/`session/new` shapes; `agy -p /usage --output-format json`; `agy models` | ADR 0022 |
| Spec 38, spec 39 | Claude Code runtime, router target, accounts, sign-in, usage probe | shape to mirror |

## Goal

The owner picks an Antigravity model like any other model, in an agent, a combo, a workflow, or the
calendar. The turn runs in the official `agy acp` on the owner's Mac, knows the agent's persona, memory,
and knowledge, uses lana's tools, and asks lana before agy edits files or runs commands. The owner can add
several Google accounts, each signed in separately, without lana ever touching a token.

## Scope

### Included

- `<account>/<model>` addresses for the built-in account `antigravity` and providers of kind
  `antigravity`.
- Runtime `runtime/antigravity`: `agy acp` process per lana session, ACP client, event mapping,
  approvals, cancel, resume, prompt head, lana tools over the MCP host, fallback before output.
- Several accounts, one `HOME` per account; sign-in through `agy acp --login` with a pasted code;
  sign-out and removal.
- Usage windows per account from `agy -p /usage`; model list per account from `agy models`.
- Router "Antigravity" card under Subscriptions; "Antigravity" group in every model picker.

### Not included

- Gemini API key, Gemini Enterprise, Agent Platform (Vertex) auth.
- Serving Antigravity through lana's OpenAI-compatible proxy, or to any program other than lana's own
  turns.
- Automatic account rotation, or moving a chat to another account.
- Installing or updating `agy` (the owner installs it; lana only checks the version).
- Running Antigravity inside lana's own loop as a text model.
- Windows and Linux account isolation (works only where a `HOME` change hides the OS credential store;
  checked on macOS).

## Behavior rules

### REQ-AGY-001: Address and accounts

`antigravity/<model>` shall be a valid address wherever an address is accepted. `<model>` is a model ID
as listed by `agy models` (for example `gemini-4-neon-high`, `claude-sonnet-5-5-medium`) or `default`
(agy's current model), and never starts with `-`. The provider `antigravity` is built in, runs agy with
the daemon's own `HOME`, and cannot be defined in `[providers]`. Further accounts are
`[providers.<name>] kind = "antigravity"` with `home` (default `$LANA_HOME/antigravity/<name>`), optional
`binary`, `label`, `allow_scheduled`, `enabled`. Their addresses are `<name>/<model>`.

- `AC-AGY-001`: Given `[agents.a] model = "antigravity/gemini-3.8-flash-low"`, when a turn runs, then the
  fake `agy` runs `acp` with the daemon's `HOME` and gets `session/set_model` with
  `gemini-3.8-flash-low`.
- `AC-AGY-002`: Given `[providers.agy-work] kind = "antigravity"` and an agent with model
  `agy-work/gemini-4-neon-high`, when a turn runs, then the fake `agy` env has
  `HOME=$LANA_HOME/antigravity/agy-work` and the call record names provider `agy-work`.

### REQ-AGY-002: Process and environment

Each lana session that runs an Antigravity turn shall own one `agy acp` process, started on its first
turn and reused for later turns while the account, binary, and prompt-head hash stay the same; it stops
after 10 minutes idle, on session end, and on daemon shutdown (process group). At most 2 agy processes
run turns at the same time (FIFO wait). The child environment:

- sets `AGY_CLI_DISABLE_AUTO_UPDATE=true`;
- for a named account sets `HOME` (and `USERPROFILE`) to the account's `home`;
- removes `GEMINI_API_KEY`, `GOOGLE_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`,
  `GOOGLE_GENAI_USE_VERTEXAI`, `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`;
- never gets `--dangerously-skip-permissions` or `--sandbox` from lana.

The working folder is the agent's workspace. `agy --version` below 1.5.17 fails the turn early with
"Antigravity CLI 1.5.17 or newer is required".

- `AC-AGY-003`: Given `GEMINI_API_KEY` in the daemon environment, when a turn runs, then the fake `agy`
  env has no `GEMINI_API_KEY` and has `AGY_CLI_DISABLE_AUTO_UPDATE=true`, and its argv has no
  `--dangerously-skip-permissions`.
- `AC-AGY-004`: Given two turns in the same session, then the fake `agy` is started once; given a third
  concurrent session while two run, then it waits until one ends.

### REQ-AGY-003: Credentials stay in agy

lana shall not read, copy, or write any file or Keychain item that holds an agy credential, shall not
refresh tokens, and shall not call any Google endpoint. lana learns about an account only from agy's
stdout: ACP responses, `agy -p /usage`, `agy models`, and `agy --version`.

- `AC-AGY-005`: Given the runtime package and the root package, then no Go file references
  `antigravity-oauth-token`, `acp_token`, `security find-generic-password`, or a `googleapis.com` URL
  (archtest).

### REQ-AGY-004: Session, model, and context

On a new process lana sends `initialize`, then `session/new` with the workspace `cwd`, lana's MCP
server, and the prompt head in `_meta.antigravity.systemPrompt.append`; then `session/set_model` with the
address model unless it is `default`. When lana holds an earlier ACP session ID for this lana session and
the same account, it sends `session/load` instead of `session/new`; a failed load falls back to
`session/new` with a `system/message` note. Knowledge recall goes in front of the user message, as in
native turns. Switching to another engine or account drops the stored ACP session ID.

- `AC-AGY-006`: Given an agent with a persona, when a turn runs, then the fake `agy` receives
  `session/new` whose `_meta.antigravity.systemPrompt.append` contains the persona text, and an `http`
  MCP server pointing at `127.0.0.1` with a bearer token.
- `AC-AGY-007`: Given a session whose earlier turn stored ACP session `S`, when the process restarted
  and a new turn runs, then the fake `agy` receives `session/load` with `S`.

### REQ-AGY-005: Event mapping

`session/update` notifications shall map to runtime events: `agent_message_chunk` → text delta,
`agent_thought_chunk` → thinking delta, `tool_call` → tool call (external), `tool_call_update` with
status `completed`/`failed` → tool result, other kinds ignored. The `session/prompt` response ends the
turn; `stopReason` `end_turn` → end, `cancelled` → cancelled, `max_tokens`/`max_turn_requests`/`refusal`
→ end with that reason. Usage in the response `_meta` or `usage`, when present, becomes the turn usage.

- `AC-AGY-008`: Given a fixture with two text chunks, a tool call, its completed update, and
  `end_turn`, then the session events are `assistant/message` with the joined text, one `tool/call`
  (`external: true`), one `tool/result`, and `turn/end`.

### REQ-AGY-006: Approvals

Each `session/request_permission` shall run through lana's approval path (classifier, rules, ask the
owner), built from the tool call's kind, title, and raw input (`execute` maps to `bash(<command>)`
matching). Allowed → reply with the option of kind `allow_once`; denied → `reject_once`; no matching
option → `cancelled` outcome. Sessions stay in mode `default`.

- `AC-AGY-009`: Given a permission request for `execute` `rm -rf build` and a rule `deny bash(rm *)`,
  then lana answers with the `reject_once` option and the owner is not asked.

### REQ-AGY-007: Cancel

Cancel in lana shall send `session/cancel`; if the prompt has not returned 15 s later, lana ends the
process group and the turn ends as cancelled.

- `AC-AGY-010`: Given a fake `agy` that ignores `session/cancel`, when the owner cancels, then the turn
  ends `cancelled` within 16 s and the process group is gone.

### REQ-AGY-008: Fallback, background, schedules

> Superseded in part by spec 78 (2026-11-26, ADR 0023): background work uses the agent's chain as it is,
> Antigravity included, and the router's OpenAI-compatible endpoint serves Antigravity models as headless
> `agy -p` calls (REQ-RPR-002, REQ-RPR-009). Agent turns are unchanged.

As spec 39 REQ-CCT-004, 007, 008 with "Antigravity" for "Claude Code": fallback to the next chain target
only before the first text or tool event (not installed, too old, not signed in, quota exhausted before
output, process error); background passes and the title pass never run on Antigravity; scheduled runs
need `allow_scheduled` on the account.

- `AC-AGY-011`: Given chain `[antigravity/gemini-4-neon-high, ds/deepseek-flash]` and a fake `agy` that
  reports not signed in, then the turn writes `assistant/attempt` and finishes in lana's loop with
  `ds/deepseek-flash`.

### REQ-AGY-009: Usage windows and models

For each enabled account lana shall run `agy -p /usage --output-format json --print-timeout 30s` (no
model call) at most once per 60 s per account, when the Router page asks and after a turn ends, and
store each group's `5h` and `weekly` bucket (remaining fraction, reset time) in the account's limits
snapshot with source `usage_probe`. A failed probe sets the snapshot error and keeps old values. The
model list of an account comes from `agy models`, cached 10 minutes.

- `AC-AGY-012`: Given the recorded `/usage` fixture, then the snapshot has groups "Gemini Models" and
  "Claude and GPT models", each with a `5h` and a `weekly` window.

### REQ-AGY-010: Sign-in, sign-out, removal

Adding an account shall be one click: lana writes `[providers.agy-2] kind = "antigravity"` (`agy-3`, …
unless named), creates `home` (0700), links the owner's `~/.gitconfig`, `~/.ssh`, and `~/.config/gh`
into it when they exist, and starts `agy acp --login` there (no terminal needed: agy 1.5.17 prints
"Open this link in a browser to log in with Google:" and the `https://accounts.google.com/o/oauth2/…`
link on stdout, then reads the code or the landing-page URL from stdin). The dashboard shows "Open Google
sign-in" with that link and a field for the code Google shows on `antigravity.google/oauth-callback`.
lana writes the pasted text once to agy's stdin followed by a newline and never stores or logs it, or the
link. The state is `succeeded` when agy exits 0. One sign-in per account runs at a time;
it stops after 15 minutes or on Cancel (process group). Sign-out is removal for named accounts: lana
deletes the `home` folder when it lies inside `$LANA_HOME/antigravity/` and the provider table. The
built-in account is never signed in or out by lana (the dashboard shows "Run `agy` in a terminal to sign
in").

- `AC-AGY-013`: Given "Add account", then `[providers.agy-2]` is written, the folder exists with mode
  0700, the fake `agy` runs `acp --login` with `HOME` = that folder, and the page shows the sign-in link
  and the code field; after the code is sent, the fake `agy` reads it on stdin and exits 0, and the state
  is `succeeded`.
- `AC-AGY-014`: Given `DELETE /v1/providers/agy-2?remove_folder=1`, then the folder and the table are
  deleted; given a `home` outside `$LANA_HOME/antigravity/`, it answers 409 `folder_outside_home` and
  deletes nothing.

### REQ-AGY-011: Dashboard

The Router shall show an "Antigravity" card under Subscriptions with one row per account: label, plan
tier (from the last `session/new`), sign-in state, the windows of REQ-AGY-009, 24-hour usage from call
records, and the agents and combos that use it; plus "Add account" and, on accounts with more than one,
the terms warning of ADR 0022. Every model picker shows an "Antigravity" group per enabled account with
that account's models.

- `AC-AGY-015`: Given two signed-in accounts, when the owner opens `#/router/providers/antigravity`, then
  each row shows its label, tier, own windows, and own usage.

### REQ-AGY-012: No sharing

Antigravity addresses shall not appear in, or be served by, lana's OpenAI-compatible proxy, and lana
shall never pick another account by itself.

- `AC-AGY-016`: Given a proxy request for model `antigravity/gemini-4-neon-high`, then it answers 404
  `model_not_found` and no `agy` runs.

## Failure cases

| Condition | Behavior |
|---|---|
| `agy` not found or older than 1.5.17 | Early error; fallback per REQ-AGY-008; Router card says "Install the Antigravity CLI" |
| Account not signed in | Early error "Antigravity account <name> is not signed in"; fallback |
| Quota exhausted before output | Early error with agy's message; fallback |
| MCP host cannot bind | Turn runs without lana tools; warning and `system/message` note (as spec 39) |
| `session/load` fails | New session, `system/message` note "Antigravity could not resume; started a new conversation" |
| Sign-in fails, times out, or is cancelled | State `failed` (last agy output line, URLs replaced by "sign-in link", at most 240 characters) or `cancelled`; account stays signed out |
| Remove with `home` outside `$LANA_HOME/antigravity/` | 409 `folder_outside_home`; nothing deleted |

## Blocking Open Questions

None. Agent recommendations: ACP over print mode (permission callback); `HOME` per account (checked on
macOS); usage only from `/usage`; Google account sign-in only.

## Approval

- **Approved by**: ridho (direction) + agent recommendations · **Approved on**: 2026-11-25
