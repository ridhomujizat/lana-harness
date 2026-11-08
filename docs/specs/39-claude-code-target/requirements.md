# Requirements: Claude Code as a router target with lana context and tools

- **Issue**: — (owner request 2026-11-08)
- **Status**: Approved (owner: "ya saya ingin tidak ada pilih engine lagi, cuman pakai lana aja"; remaining details are agent recommendations under the owner's standing "ikuti rekomendasimu")
- **Revision**: 3 (2026-11-08: REQ-CCT-012 one-click sign-in)
- **Owner**: ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: [0020](../../adr/0020-claude-code-as-router-target-with-lana-context.md), [0018](../../adr/0018-claude-code-as-external-agent-runtime.md), [0019](../../adr/0019-llm-router-as-embeddable-core.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner | "engine ini bisa cuman lana aja? … saya ingin claude bisa digunakan di semua service lana, termasuk workflow"; "tetep lewat binary claude code tapi jalanin engine lana, seperti monocode di bot monos"; "tidak ada pilih engine lagi, cuman pakai lana aja" | goal |
| MonoCode docs | `docs/agent-access.md`: the agent CLI runs the turn; MonoCode supplies instructions and a local `app` CLI | pattern |
| Claude Code 2.1.319 | `--append-system-prompt`, `--mcp-config`, `--strict-mcp-config` | flags |
| Owner (revision 3) | adding a Claude Code account is too hard; "make it like MonoCode" | REQ-CCT-012 |
| MonoCode (MIT) | `login.rs`: spawn `claude auth login` with the account env, keep a URL-stripped last stderr line; `harness.rs`: `CLAUDE_CONFIG_DIR` + `CLAUDE_SECURESTORAGE_CONFIG_DIR` | REQ-CCT-012 |

## Goal

The owner picks Claude Code like any other model, in an agent, a combo, a workflow, or the calendar. There
is no engine setting. A Claude Code turn knows the agent's persona, memory, and knowledge, and can use
lana's own tools, while the subscription stays inside the official `claude` binary.

## Scope

### Included

- `claude-code/<model>` addresses in agents, combos, session model switch, workflows, calendar.
- A Claude Code card under "Subscriptions" in the Router; its models in every model picker.
- Prompt head and knowledge recall injected into Claude Code turns.
- A loopback MCP server exposing lana tools to Claude Code turns, with lana approvals.
- Fallback from a Claude Code target to the next target before output.
- Background passes use a non-Claude model.
- Removal of the Engine choice from the dashboard; migration of `runtime = "claude-code"`.

### Not included

- Claude Code inside lana's own loop as a text model (forbidden by ADR 0020).
- Exposing lana's file and shell tools to Claude Code (Claude Code has its own).
- MCP access for programs other than Claude Code turns started by lana.

## Behavior rules

### REQ-CCT-001: Address

`claude-code/<model>` shall be a valid model address wherever an address is accepted (agent `model`,
`fallback`, combos, session switch). `<model>` is `opus`, `sonnet`, `haiku`, `default`, or a full model ID
not starting with `-`. The provider `claude-code` is built in and cannot be defined in `[providers]`.

- `AC-CCT-001`: Given `[agents.a] model = "claude-code/opus"`, when config is validated and a turn runs,
  then the fake `claude` gets `--model opus` and no `provider` key is needed.
- `AC-CCT-002`: Given `[combos.c] models = ["claude-code/sonnet", "ds/deepseek-flash"]`, when validated,
  then it is accepted.

### REQ-CCT-002: Legacy runtime key

`runtime = "claude-code"` with `model = "M"` shall be read as `model = "claude-code/M"` (`M` empty →
`default`). The dashboard writes only the new form. `auth`, `api_key_env`, `binary`, `allow_scheduled`
stay agent keys and apply to the agent's Claude Code turns.

- `AC-CCT-003`: Given a legacy agent `runtime = "claude-code"`, `model = "haiku"`, when it runs, then it
  behaves as `model = "claude-code/haiku"`; when the owner saves it in the dashboard, then the file has
  `model = "claude-code/haiku"` and no `runtime`.

### REQ-CCT-003: Engine selection per turn

A turn whose first available chain target is `claude-code/*` shall run through the Claude Code runtime;
otherwise through lana's loop. A session model switch to or from a Claude Code address takes effect on
the next turn; a switch between engines starts a new Claude session (no `--resume` of an older one).

- `AC-CCT-004`: Given agent model `ds/deepseek-flash`, when the owner switches the session to
  `claude-code/sonnet`, then the next turn runs in Claude Code; switching back runs in lana's loop.

### REQ-CCT-004: Fallback before output

If the Claude Code run fails before its first text, thinking, or tool event (binary missing, version too
old, not logged in, a `rate_limit_event` with status `rejected`, process exit), the router records the
attempt and the turn continues with the next chain target in lana's loop. After the first event, errors
end the turn as in spec 38.

- `AC-CCT-005`: Given combo `[claude-code/opus, ds/deepseek-flash]` and a fake `claude` that reports not
  logged in, when a turn runs, then DeepSeek answers, an `assistant/attempt` names `claude-code/opus`, and
  the Logs show a failed `claude-code` call and an ok `ds` call.

### REQ-CCT-005: lana context

Each Claude process shall start with `--append-system-prompt` containing the same prompt head a native
turn of that agent would get (persona, USER.md, memory entries, skill list, tool notes for the lana MCP
tools). When the prompt head changes, the next turn restarts the process with `--resume`. Knowledge recall
for the owner message is prepended to the user message as in native turns.

- `AC-CCT-006`: Given an agent persona "You are Bima", when a Claude Code turn starts, then the fake
  `claude` argv has `--append-system-prompt` containing "You are Bima" and the agent's memory lines.

### REQ-CCT-006: lana tools over MCP

For Claude Code turns lana shall host an MCP server on `127.0.0.1` (random port, streamable HTTP) and pass
`--mcp-config` with a per-session bearer token. It lists the agent's allowed lana tools except file and
shell tools (`read_file`, `write_file`, `edit_file`, `glob`, `grep`, `bash`), plus tools of the agent's
configured MCP servers. Calls run through lana's tool runner with the session's permission policy and
approvals, and appear in the session log as normal `tool/call` / `tool/result` events. A token is valid
only while its session's Claude process lives; other requests get 401.

- `AC-CCT-007`: Given an agent with the `memory` tool, when Claude Code calls `mcp__lana__memory` to save
  a fact, then lana's memory has it and the session log shows the call.
- `AC-CCT-008`: Given a request to the MCP server without a valid token, then it gets 401.

### REQ-CCT-007: Background passes

Compaction, memory capture, knowledge passes, and embeddings shall never use `claude-code/*`. They use the
first non-Claude target of the agent's chain, else `[router] background_model` (an address or combo
without Claude Code targets), else they are skipped with one warning per agent per hour.

- `AC-CCT-009`: Given agent model `claude-code/opus` and `[router] background_model = "ds/deepseek-flash"`,
  when memory capture runs after a turn, then it calls DeepSeek with source `memory_capture`.

### REQ-CCT-008: Scheduled runs

Workflows and calendar items may target any agent; a run whose turn would use Claude Code requires the
agent's `allow_scheduled = true` (default false), else the scheduled run falls back to the next non-Claude
target of the chain, and fails with "claude-code is not allowed for scheduled runs of agent '<name>'" when
there is none.

- `AC-CCT-010`: Given combo `[claude-code/opus, ds/deepseek-flash]` and `allow_scheduled = false`, when a
  workflow runs the agent, then DeepSeek answers.

### REQ-CCT-009: Dashboard

The Engine choice shall be removed. The Router shows a "Claude Code" provider card under Subscriptions
(status, version, plan windows, models, agents and combos using it). Every model picker lists Claude Code
models in their own group with the Claude logo. An agent whose model is a Claude Code address shows a
note that Claude Code uses its own file and shell tools, lana tools come in through MCP, and `max_steps` and
compaction settings do not apply; the Claude-only keys (auth, binary, allow scheduled runs) appear in that
case only.

- `AC-CCT-011`: Given the agent form, when the owner picks "Claude Code · Opus" in the model picker and
  saves, then the file has `model = "claude-code/opus"` and the form shows the Claude Code note and keys.


### REQ-CCT-010: Several Claude Code accounts (owner request 2026-11-08, revision 2)

The owner shall be able to add Claude Code accounts as connections: `[providers.<name>] kind =
"claude-code"` with `config_dir` (default `$LANA_HOME/claude/<name>`), optional `binary`, `auth`
(`subscription` | `api_key`), `api_key_env`, `allow_scheduled`. The built-in `claude-code` provider is the
default account (`~/.claude`, no `config_dir`). Each account runs `claude` with `CLAUDE_CONFIG_DIR` set to
its folder; lana never reads the folder's credential files. Adding an account creates the folder.
*(Revision 3: the login itself is REQ-CCT-012; the terminal command is now
`CLAUDE_CONFIG_DIR=<dir> CLAUDE_SECURESTORAGE_CONFIG_DIR=<dir> claude auth login` and is kept as a
fallback.)*
Addresses are `<account>/<model>` (for example `claude-work/claude-opus-5-5`). lana does not rotate
between accounts automatically; a combo may list several accounts, and the UI warns that using several
personal accounts to get around plan limits can break Anthropic's terms.

- `AC-CCT-012`: Given `[providers.claude-work] kind = "claude-code"`, when an agent with model
  `claude-work/haiku` runs, then the fake `claude` env has `CLAUDE_CONFIG_DIR=$LANA_HOME/claude/claude-work`
  and the call record names provider `claude-work`.

### REQ-CCT-011: Account identity and usage per account

For each Claude Code account the Router shall show login state, email, and plan type taken from that
account's `claude auth status` output (read from the binary's stdout, cached 60 s, never persisted), the
account's plan windows from its own `rate_limit_event`s, its 24-hour usage from call records, and the
agents and combos that use it.

- `AC-CCT-013`: Given two logged-in accounts, when the owner opens `#/router/providers/claude-code`, then
  each account row shows its email and plan, its own 5-hour and weekly windows, and its own usage.

### REQ-CCT-012: One-click sign-in, sign-out, and removal (owner request 2026-11-08, revision 3)

Adding a Claude Code account shall be one click: the dashboard creates the account (name `claude-2`,
`claude-3`, … unless given), creates its folder (0700), and starts the official `claude auth login` for
it; Claude Code opens its own sign-in page in the browser and writes the credentials (ADR 0020 amendment
2026-11-08). The dashboard polls the sign-in and shows the result. Each account row can sign in (when
signed out), sign out (after a confirm; the built-in account needs an explicit confirm because it signs
the owner out of their normal Claude Code), be removed (sign out, delete the folder when it lies inside
`$LANA_HOME/claude/`, delete the table), and get a display label (at most 60 characters). Every `claude`
lana runs for a named account gets `CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR` set to its
folder and none of `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`. lana keeps
only the last stderr line of a failed run, with URLs replaced by "sign-in link" (at most 240
characters), and never relays URLs, OAuth codes, or tokens. One sign-in per account runs at a time;
sign-in stops after 10 minutes or on Cancel (the whole process group), sign-out after 30 s; the
account's cached status is dropped afterwards.

- `AC-CCT-014`: Given the Claude Code page, when the owner clicks "Add account", then
  `[providers.claude-2] kind = "claude-code"` is written, `$LANA_HOME/claude/claude-2` exists with mode
  0700, the fake `claude` runs `auth login` with `CLAUDE_CONFIG_DIR` and
  `CLAUDE_SECURESTORAGE_CONFIG_DIR` = that folder and no token variables, the page shows "Opening Claude
  Code sign-in in your browser…" and polls every 1.5 s, and on success the new row is highlighted with
  "Signed in as <email> · <plan>".
- `AC-CCT-015`: Given a sign-in whose `claude auth login` exits 1 with a stderr line containing a URL,
  then the state is `failed` with that line and the URL replaced by "sign-in link", and the page shows
  it with "Try again" and a collapsed "Sign in from a terminal instead" holding the copyable command.
- `AC-CCT-016`: Given a running sign-in, a second start answers 409 and Cancel ends the process group
  with state `cancelled`; a sign-in past its deadline ends as `failed` ("timed out").
- `AC-CCT-017`: Given `DELETE /v1/providers/claude-2?remove_folder=1`, then the account is signed out,
  its folder is deleted, and the table removed; given an account whose `config_dir` is outside
  `$LANA_HOME/claude/`, the same call answers 409 `folder_outside_home` and deletes nothing.
- `AC-CCT-018`: Given the built-in account, a sign-out without `{"confirm": true}` answers 400
  `confirm_required` and runs nothing.

## Failure cases

| Condition | Behavior |
|---|---|
| Claude Code fails before output and no next target | Turn fails with the Claude error (spec 38 messages) |
| MCP server cannot bind | Claude turn runs without lana tools; warning in log and a `system/message` note |
| Background pass has no non-Claude model | Skipped, warning |
| `claude auth login` fails, times out (10 min), or is cancelled | Sign-in state `failed` (URL-stripped last stderr line) or `cancelled`; the account stays signed out |
| Remove with a folder outside `$LANA_HOME/claude/` | 409 `folder_outside_home`; nothing is signed out or deleted |

## Blocking Open Questions

None. Agent recommendations: address prefix `claude-code`; fallback only before output; MCP over loopback
HTTP with per-session tokens; background model resolution as REQ-CCT-007; scheduled-run fallback as
REQ-CCT-008.

## Approval

- **Approved by**: ridho (direction) + agent recommendations · **Approved on**: 2026-11-08
- **Revision 3** (REQ-CCT-012): owner request "make adding an account like MonoCode", 2026-11-08
