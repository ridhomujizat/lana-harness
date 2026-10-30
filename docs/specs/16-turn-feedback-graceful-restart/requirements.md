# Requirements: Turn feedback, graceful restart, and live config changes

- **Issue**: —
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: —

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner report 2026-10-30 | Session `06GRV62P13N2RKW0PJJRV79ZXG`: a turn ended `cancelled` with only a tool error shown; the owner had to send "hi" to get an answer | problem |
| Session diagnosis 2026-10-30 | The cancel came from a daemon restart (SIGTERM). `dbx` was attached to `lana` from the web at 23:15:17, but the runtime did not load it until restart; `mcp_manage list` said `attached`, so the agent called the MCP server by hand through `bash` | problem |
| Owner answers 2026-10-30 | Status card with Continue for abnormal ends, showing the error; tool/MCP failures are explained by the agent as a normal message; restart waits up to 60 s then cuts, with "Restart now" and "Cancel"; web MCP/agent changes apply from the next turn; MCP status is not shown in detail in the UI — the agent tells the owner | `Q-001`–`Q-004` |
| Spec #12 | `core.MCPAdmin`, registry MCP overlay, `mcp_manage` | reuse |

## Goal

The owner always knows why a turn stopped and can continue it with one click; restarting lana does
not silently cut running work; and agent or MCP changes saved in the web apply from the next message.

## Definitions

- **Abnormal end** — a turn ending with reason `cancelled`, `interrupted`, `error`, or `max_steps`.
- **Interrupted** — a turn stopped by lana shutting down or restarting, not by the owner.
- **Drain** — the shutdown phase that waits for running turns before stopping.

## Scope

### Included

- New turn end reason `interrupted` with a message.
- Graceful drain on restart and on SIGTERM/SIGINT (60 s), with force and cancel for web restarts.
- A status card with the reason and a Continue action in the web and Telegram.
- A continue operation that starts a new turn asking the agent to resume.
- Hot apply of agent, team, and MCP changes saved through the web.
- Real runtime state in `mcp_manage list` for the agent.
- Runtime rules and guards so the agent reports unreachable tools and unfinished work.

### Not included

- Hot apply of providers, channels, routes, web address, or secrets (still need a restart).
- Automatic re-run of interrupted turns.
- A detailed MCP state display in the web.

## Behavior rules

### REQ-TFB-001: Interrupted reason

When lana stops while turns run, each running turn shall end with reason `interrupted` and error message "lana restarted while this turn was running" (or "lana stopped …" for a plain stop), recorded in `turn/end`. A tool call cut by it gets status `error` with output `interrupted`.

#### Acceptance criteria

- `AC-TFB-001`: Given a running turn, when the drain times out, then its `turn/end` has `reason = "interrupted"` and the message above, and it is not `cancelled`.

### REQ-TFB-002: Graceful drain

On restart and on SIGTERM/SIGINT, lana shall stop accepting new turns (they get `503 daemon_draining` / a Telegram "lana is restarting, try again in a moment" reply), wait up to 60 s for running turns, then interrupt the rest and stop. A second SIGTERM/SIGINT skips the wait.

#### Acceptance criteria

- `AC-TFB-002`: Given one running turn that finishes in 5 s, when SIGTERM arrives, then the turn ends `end_turn` and the daemon exits after it.
- `AC-TFB-003`: Given a turn that does not finish in 60 s, then it ends `interrupted` and the daemon exits by 62 s.
- `AC-TFB-004`: Given drain in progress, when a new message arrives on web or Telegram, then it is refused with the draining message and no turn starts.

### REQ-TFB-003: Web restart with waiting state

`POST /v1/daemon/restart` shall start the drain and answer at once with the running-turn count. The web dialog shows "Waiting for <n> running turn(s) (<s> s)" with "Restart now" (force) and "Cancel" (abort the pending restart, accept turns again).

#### Acceptance criteria

- `AC-TFB-005`: Given one running turn, when the owner presses Restart, then the dialog shows the waiting state with both buttons, and Cancel returns the daemon to normal with the turn still running.
- `AC-TFB-006`: Given the waiting state, when the owner presses Restart now, then running turns end `interrupted` and the daemon restarts.

### REQ-TFB-004: Status card on abnormal end

After an abnormal end, the web session shall show a status card under the turn with a plain reason and the error text, the last step and tool if known, and buttons "Continue" and "Dismiss". Telegram shall end the final message with the reason line and an inline "Continue" button. Reasons: `cancelled` → "Stopped by you."; `interrupted` → "lana restarted while working."; `error` → "Stopped by an error: <message>"; `max_steps` → "Stopped after the step limit (<n> steps)." A tool or MCP failure inside a turn is not an abnormal end: the agent receives it and explains it in its normal answer.

#### Acceptance criteria

- `AC-TFB-007`: Given a turn that ended `interrupted` after `dbx_list_tables` in step 4, when the session page loads, then the card reads "lana restarted while working." and "Last step: dbx_list_tables (step 4)" with Continue and Dismiss.
- `AC-TFB-008`: Given a provider error, then the card shows "Stopped by an error:" with the provider message.
- `AC-TFB-009`: Given a turn with a failed MCP tool call that ends `end_turn`, then no card is shown.
- `AC-TFB-010`: Given Telegram, when a turn ends `interrupted`, then the final message ends with `⚠ lana restarted while working.` and a "Continue" button; pressing it (owner only) starts the continue turn.

### REQ-TFB-005: Continue

Continue shall start a new turn in the same session whose user message is marked `continue` and whose text to the model is "Continue the previous task from where it stopped. It ended because: <reason>." The web shows it as a small "↻ Continued" chip, not as owner text. Continue is refused while a turn runs.

#### Acceptance criteria

- `AC-TFB-011`: Given the card, when the owner presses Continue, then a new turn starts, the chip shows, the card disappears, and the model input contains the continue text.
- `AC-TFB-012`: Given a running turn, when continue is requested, then `409 turn_running`.

### REQ-TFB-006: Live agent and MCP changes

Saving `[agents.*]`, `[teams.*]`, or `[mcp.*]` through the web shall apply to the running daemon from the next turn, without restart: the agent registry reads the new agent and team config, and the MCP manager gets added, changed, or removed servers (same path as `mcp_manage`). These responses return `restart_required: false`. Provider, channel, route, web, and secret saves keep `restart_required: true`.

#### Acceptance criteria

- `AC-TFB-013`: Given `dbx` attached to `lana` through the web, when the next `lana` turn starts, then `mcp__dbx__*` tools are offered and no restart happened.
- `AC-TFB-014`: Given a web change of `agents.lana.model`, then the next turn uses the new model.
- `AC-TFB-015`: Given a web save of a provider, then the response still has `restart_required: true`.

### REQ-TFB-007: Real MCP state for the agent

`mcp_manage list` shall show each server's runtime state for the calling agent: `running (<n> tools)`, `stopped`, `unavailable: <reason>`, or `not loaded` (configured but not known to the runtime). The web MCP pages do not change.

#### Acceptance criteria

- `AC-TFB-016`: Given `dbx` running with 25 tools, then its list line contains `running (25 tools)`.
- `AC-TFB-017`: Given a server whose last start failed, then its line contains `unavailable:` and the short reason.

### REQ-TFB-008: The agent reports blockers

Like Claude Code, the agent shall tell the owner when it cannot reach a tool or MCP server or cannot finish the request. The system shall support this in four ways:

1. **Runtime rules in every prompt.** `agenthome.Build` adds a fixed "When something blocks you" section to every agent prompt (not to the editable `AGENT.md`), with these rules:
   - If a tool or MCP server you need is unavailable, or a call to it fails, say so in your answer: name it, quote the error, say what you could not do, and say what the owner can do (for example: check the server in Settings → MCP, add a secret, or retry).
   - Do not work around a missing or failing MCP server by calling it or its API by hand (for example with bash).
   - If you cannot finish the request, say so plainly and say what is still needed. Never end a turn without telling the owner the result.
2. **Unavailable servers in the prompt.** The prompt lists every server of the agent that is not running at turn start, with its reason. The reason for a server that the runtime does not know is `not loaded`.
3. **Clear MCP call errors.** When an MCP tool call fails because the server is not reachable (connect, start, or transport error), the tool result says `MCP server <name> is not reachable: <short reason>`.
4. **Empty final answer guard.** If a turn would end `end_turn` with no text in the last assistant message after at least one tool call, the runtime shall run one more model step with the note "Tell the owner what you did, what failed, and what is still needed." It runs at most once per turn, and never after an abnormal end.

#### Acceptance criteria

- `AC-TFB-018`: Given any agent, when its prompt is built, then it contains the "When something blocks you" section with the three rules.
- `AC-TFB-019`: Given agent `lana` with `mcp = ["dbx"]` and `dbx` failing to start with `spawn ENOENT`, when a turn starts, then the prompt lists `dbx (spawn ENOENT)` as unavailable.
- `AC-TFB-020`: Given an MCP tool call whose server connection drops, then the tool result is `MCP server dbx is not reachable: <reason>` with status error.
- `AC-TFB-021`: Given a fake provider whose last assistant message after tool calls has no text, when the turn ends, then exactly one extra step runs with the note, and the turn ends with that step's text.

## Blocking Open Questions

| ID | Question | Status | Resolution |
|---|---|---|---|
| `Q-001` | Feedback on abnormal end | Resolved | Card with reason, error, Continue; tool/MCP failures explained by the agent |
| `Q-002` | Restart with running turns | Resolved | Wait 60 s, then interrupt; Restart now / Cancel |
| `Q-003` | When web changes apply | Resolved | Next turn for agents, teams, MCP |
| `Q-004` | MCP state detail | Resolved | Only to the agent through `mcp_manage list` |
| `Q-005` | MCP not reachable / task not finished | Resolved | The agent tells the owner, like Claude Code (owner, 2026-10-30) |

## Requirement coverage

| Requirement | Acceptance criteria |
|---|---|
| `REQ-TFB-001` | `AC-TFB-001` |
| `REQ-TFB-002` | `AC-TFB-002`–`AC-TFB-004` |
| `REQ-TFB-003` | `AC-TFB-005`, `AC-TFB-006` |
| `REQ-TFB-004` | `AC-TFB-007`–`AC-TFB-010` |
| `REQ-TFB-005` | `AC-TFB-011`, `AC-TFB-012` |
| `REQ-TFB-006` | `AC-TFB-013`–`AC-TFB-015` |
| `REQ-TFB-007` | `AC-TFB-016`, `AC-TFB-017` |
| `REQ-TFB-008` | `AC-TFB-018`–`AC-TFB-021` |

## Approval

- **Approved by**: Ridho
- **Approved on**: 2026-10-30
