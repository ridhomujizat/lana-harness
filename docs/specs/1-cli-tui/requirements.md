# Requirements: CLI and TUI chat

- **Issue**: [`#1`](https://github.com/ridhomujizat/lana-harness/issues/1)
- **Status**: Approved
- **Revision**: 6
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (subcommands, flags, exit codes, output format, slash commands, keys)
- **Related ADR**: [ADR Index](../../adr/README.md) — [0003](../../adr/0003-multi-agent-with-channel-routing.md), [0004](../../adr/0004-permission-mode-per-agent.md), [0008](../../adr/0008-cli-channel-http-and-web-dashboard-interfaces.md), [0009](../../adr/0009-session-as-append-only-event-log.md), [0010](../../adr/0010-profile-and-config-patch.md)
- **Related diagram**: [Diagram Index](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-and-approval.md), [0004](../../diagram/0004-profile-fork-subagent-and-protocols.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Issue | [`#1`](https://github.com/ridhomujizat/lana-harness/issues/1) | feature scope |
| ADR 0008 | CLI/TUI interface | token streaming, approval prompt `[y]es / [a]lways / [n]o`, feature `tui` |
| ADR 0003 | Multi-agent | route for channel `cli`, `/agent`, inbox per session |
| ADR 0004 | Permission | session policy, approval outcome, 10-minute timeout, `--yolo` |
| ADR 0009 | Event log | session, fork, resume, compaction |
| ADR 0010 | Profile | `headless` for `lana run`, `minimal` for embedded chat, `--dump-config`, `config check` |
| Interview 2026-10-29 | Owner answers for `Q-001` through `Q-019` | all decisions in the Resolution section |
| Comparison reference | Claude Code | inline TUI, multi-line input, `--continue` per directory |

## Goal

The owner can use the `lana` agent from the terminal: set up the initial configuration, chat
interactively with streamed answers, view and approve risky actions, manage sessions, and run
one-shot commands from scripts.

## Actors

- **Owner** — the only user. Runs `lana` as their own OS user on a local machine.
- **Script** — a program or shell pipeline that calls `lana run` without interaction.

## Definitions

- **Chat** — the `lana chat` process that reads input from the terminal until the user exits.
- **One-shot** — a single `lana run` invocation that runs one turn and then exits.
- **Embedded mode** — the agent runs inside the `lana chat` or `lana run` process, without a daemon.
- **Inline TUI** — a display that writes the chat history to the terminal scrollback and only redraws
  the input area and status bar at the bottom. Does not use the alternate screen.
- **CLI session** — a session with channel `cli` bound to one working directory (cwd).
- **Slash command** — input that starts with `/` and is handled by the CLI without a request to the model.

## Scope

### Included

- `lana chat`: inline TUI, streaming, tool call display, approval, slash commands, multi-line input, input history.
- Sessions: new by default, `--continue`, `--resume <id>`, `/new`, `/sessions`, `/fork`, `/compact`.
- `lana run`: one-shot with `text`, `json`, and `stream-json` output, and a prompt from stdin.
- `lana init`: initial configuration wizard.
- `lana config check`, `lana config path`, `lana config edit`, and the global flag `--dump-config`.
- Exit codes per failure class.

### Not included

- Attach mode to a daemon. Added in the daemon spec.
- Telegram and WhatsApp channels, web dashboard, ACP, and SDK.
- The `session`, `cron`, `db`, and `service` subcommands.
- Implementation of the agent loop, provider, permission engine, and event log. This spec uses those components as dependencies.
- Alternate screen display (full screen).

## Assumptions

- Agent, provider, and route are read from the effective config of the profile. — Source: ADR 0010.
- Messages sent while a turn is running enter the inbox and are claimed at the start of the next step. — Source: ADR 0003.
- The initial session policy is taken from the agent's `permission`. — Source: ADR 0004.
- Fork is only allowed at a turn boundary. — Source: ADR 0009.

## Behavior rules

### Chat

#### REQ-CHAT-001: Choosing the agent when chat starts

Without `--agent`, the system shall use the agent from the first route with `channel = "cli"`. With
`--agent <name>`, the system shall use that agent.

- `AC-CHAT-001`: Given a `cli` route to `coder`, when the user runs `lana chat`, then the status bar shows the agent `coder`.
- `AC-CHAT-002`: Given the agent `assistant` is registered, when the user runs `lana chat --agent assistant`, then the session uses `assistant`.
- `AC-CHAT-003`: Given the agent `xyz` is not registered, when the user runs `lana chat --agent xyz`, then the process exits with exit code 2 and a message naming `xyz`.
- `AC-CHAT-004`: Given there is no `cli` route and no `--agent`, when the user runs `lana chat`, then the process exits with exit code 2 and a message suggesting `--agent` or adding a route.

#### REQ-CHAT-002: Embedded mode

The system shall run the agent inside the `lana chat` process, and the status bar shall show the mode `embedded`.

- `AC-CHAT-005`: Given the daemon is not running, when the user runs `lana chat`, then chat can be used and the status bar shows `embedded`.

#### REQ-CHAT-003: Inline TUI

The system shall write the chat history to the terminal scrollback and only redraw the input area and
the status bar at the bottom. The status bar shall show the agent, mode, session policy, and session token count.

- `AC-CHAT-006`: Given chat is running, when several messages have completed, then those messages can be scrolled and copied with native terminal features.
- `AC-CHAT-007`: Given the user exits chat, when the process ends, then the chat history remains in the terminal scrollback.
- `AC-CHAT-008`: Given chat is running, when the status changes (agent, policy, token), then the status bar shows the new value.

#### REQ-CHAT-004: Streaming answers

The system shall display the model's answer text progressively as it is received.

- `AC-CHAT-009`: Given the provider sends several text deltas, when the turn runs, then each delta is displayed before the turn completes.

#### REQ-CHAT-005: Compact tool calls and verbose mode

The system shall display each tool call as one line containing the tool name, the main argument, and the
status (running, success, rejected, error). The user shall be able to turn on verbose mode: the last tool
call is reprinted in full, and subsequent tool calls are shown with full input and output until verbose
mode is turned off.

- `AC-CHAT-010`: Given the model calls `bash` with `cargo test`, when the tool completes, then one line shows `bash`, `cargo test`, and the status.
- `AC-CHAT-011`: Given a tool call has been shown and verbose mode is off, when the user turns on verbose mode, then the full input and output of the last tool call are printed to the scrollback.
- `AC-CHAT-024`: Given verbose mode is on, when the model calls a tool, then the user turns verbose mode off and the model calls a tool again; the first tool call is shown in full and the second is shown compactly.

#### REQ-CHAT-006: Input while a turn is running

The system shall accept input while a turn is running and put it into the session inbox.

- `AC-CHAT-012`: Given a turn is running, when the user sends a message, then the message is claimed at the start of the next step and recorded as `user/message`.

#### REQ-CHAT-007: Multi-line input

`Enter` shall send the message. The system shall provide a new line through `Shift+Enter` (in terminals
that support the keyboard protocol), `Alt+Enter`/`Option+Enter`, `Ctrl+J`, and `\` at the end of a line
followed by `Enter`. A multi-line paste must not send the message immediately.

- `AC-CHAT-013`: Given the input contains text, when the user presses `Alt+Enter`, then a new line is added and the message is not yet sent.
- `AC-CHAT-014`: Given the input ends with `\`, when the user presses `Enter`, then `\` is replaced with a new line and the message is not yet sent.
- `AC-CHAT-015`: Given the user pastes three lines, when the paste completes, then all three lines are in the input and the message is not yet sent.

#### REQ-CHAT-008: Cancel and exit

While a turn is running, `Ctrl+C` shall cancel the turn. While idle, `Ctrl+C` shall clear the input, and
pressing `Ctrl+C` twice within 2 seconds shall exit. `Ctrl+D` on empty input shall exit.

- `AC-CHAT-016`: Given a turn is running, when the user presses `Ctrl+C`, then the turn stops with the `turn/end` reason `cancelled` and chat stays open.
- `AC-CHAT-017`: Given idle with text in the input, when the user presses `Ctrl+C`, then the input is cleared and chat stays open.
- `AC-CHAT-018`: Given idle, when the user presses `Ctrl+C` twice within 2 seconds, then the process exits with exit code 0.
- `AC-CHAT-019`: Given empty input, when the user presses `Ctrl+D`, then the process exits with exit code 0.

#### REQ-CHAT-009: Input history

The system shall store sent input in `~/.lana/history` (maximum 1000 entries, permission `0600`). The up
and down arrows browse the history. `Ctrl+R` searches the history.

- `AC-CHAT-020`: Given the user sends a message and exits, when a new chat opens and the user presses the up arrow, then that message appears in the input.
- `AC-CHAT-021`: Given the history contains 1000 entries, when a new message is sent, then the oldest entry is deleted.

#### REQ-CHAT-010: Workspace from the working directory

For `lana chat` and `lana run`, the session workspace shall be the working directory (cwd) at the time the
process runs, not the `workspace` from the agent config.

- `AC-CHAT-022`: Given `lana chat` is run in `~/coding/x`, when the model reads a file outside `~/coding/x`, then the tool is rejected because it is outside the workspace.

#### REQ-CHAT-011: Chat requires a terminal

`lana chat` shall run only if stdin and stdout are both TTYs.

- `AC-CHAT-023`: Given stdin is not a TTY, when the user runs `lana chat`, then the process exits with exit code 2 and a message suggesting `lana run`.

### Session

#### REQ-SESS-001: New, continued, and reopened sessions

By default, `lana chat` creates a new CLI session for the cwd. `--continue` shall open the last CLI session
for the same cwd. `--resume <id>` shall open that session if its cwd is the same.

- `AC-SESS-001`: Given a previous CLI session exists in this cwd, when the user runs `lana chat`, then a new session is created.
- `AC-SESS-002`: Given a previous CLI session exists in this cwd, when the user runs `lana chat --continue`, then the last session in this cwd opens and its history is shown.
- `AC-SESS-003`: Given no CLI session exists in this cwd, when the user runs `lana chat --continue`, then a new session is created and the message says there was no previous session.
- `AC-SESS-004`: Given session `S` was created in another cwd, when the user runs `lana chat --resume S`, then the process exits with exit code 2 and a message naming the original cwd of `S`.
- `AC-SESS-005`: Given the last session was interrupted in the middle of a turn, when the session is reopened, then the session can accept new messages (resume per ADR 0009).

#### REQ-SESS-002: One process per session

A session that is currently opened by another `lana` process shall be rejected. The lock of a process that
has stopped shall be released automatically.

- `AC-SESS-006`: Given process A has opened session `S`, when process B runs `lana chat --resume S`, then process B exits with exit code 2 and a message naming process A's PID.
- `AC-SESS-007`: Given process A that opened `S` died without exiting normally, when process B opens `S`, then `S` opens.

### Approval

#### REQ-APPR-001: Approval prompt in the terminal

When the session policy is `ask` and a tool requires approval, the system shall display the related tool
call and ask for `[y]es / [a]lways / [n]o`.

- `AC-APPR-001`: Given approval is requested, when the user answers `y`, then the outcome is `allowed_once` and the tool runs.
- `AC-APPR-002`: Given approval is requested, when the user answers `a`, then the outcome is `allowed_session` and subsequent tool calls with the same pattern in that session do not request approval.
- `AC-APPR-003`: Given approval is requested, when the user answers `n`, then the outcome is `rejected` and the model receives an error `tool_result`.
- `AC-APPR-004`: Given approval is requested, when 10 minutes pass without an answer, then the outcome is `unavailable` and the tool does not run.
- `AC-APPR-005`: Given approval is requested, when the user presses `Ctrl+C`, then the outcome is `cancelled` and the turn is cancelled.

#### REQ-APPR-002: Yolo mode

`--yolo` and `/yolo` shall change the session policy to `trust` and display a visible warning while that
policy is active. `deny` rules and the workspace boundary remain in effect.

- `AC-APPR-006`: Given `lana chat --yolo`, when the session starts, then the warning is shown and the session log contains `approval/policy` with the value `trust`.
- `AC-APPR-007`: Given policy `trust` and a `deny` rule matches, when the model calls that tool, then the tool is rejected.

### Slash commands

#### REQ-CMD-001: Slash command list

The system shall handle the following slash commands without a request to the model:

| Command | Behavior |
|---|---|
| `/agent` | Show the active agent and the agent list |
| `/agent <name>` | Switch the agent for this session |
| `/new` | Create a new CLI session in this cwd |
| `/sessions` | Show the CLI sessions in this cwd and switch to the selected one |
| `/permission` | Show the session policy |
| `/permission <trust\|ask\|never>` | Change the session policy |
| `/yolo` | Same as `/permission trust`, with a warning |
| `/model` | Show the active provider and model |
| `/model <provider>:<model>` | Switch the model for this session |
| `/compact` | Run compaction now |
| `/fork` | Fork at the last `turn/end` and switch to the new session |
| `/clear` | Clear the terminal display; the session and context do not change |
| `/help` | Show the list of commands and keys |
| `/exit` | Exit with exit code 0 |

- `AC-CMD-001`: Given the agent `assistant` is registered, when the user types `/agent assistant`, then the next message is answered by `assistant`.
- `AC-CMD-002`: Given the user types `/agent`, when the command is processed, then the active agent and the agent list are shown, and there is no request to the model.
- `AC-CMD-003`: Given session `A` is active, when the user types `/new`, then a new session is created and `A` is unchanged.
- `AC-CMD-004`: Given several CLI sessions exist in this cwd, when the user types `/sessions` and selects one, then chat switches to that session and its history is shown.
- `AC-CMD-005`: Given the user types `/permission never`, when the command is processed, then the session log contains `approval/policy` with the value `never` and the status bar shows `never`.
- `AC-CMD-006`: Given provider `openrouter` is registered, when the user types `/model openrouter:openai/gpt-5-mini`, then the session log contains `model/selection` and the next request uses that model.
- `AC-CMD-007`: Given provider `abc` is not registered, when the user types `/model abc:x`, then an error message is shown and the model does not change.
- `AC-CMD-008`: Given the session has history, when the user types `/compact`, then the session log contains `compaction/summary`.
- `AC-CMD-009`: Given session `A` has at least one completed turn, when the user types `/fork`, then a new session `B` is created with `parent_session_id = A`, chat switches to `B`, and the ID `A` is shown.
- `AC-CMD-010`: Given the session has no completed turn, when the user types `/fork`, then an error message is shown and the session is unchanged.
- `AC-CMD-011`: Given the user types `/clear`, when the command is processed, then the screen is cleared and the next message still uses the same session context.
- `AC-CMD-012`: Given the user types `/xyz`, when the command is processed, then the message "unknown command" and the `/help` suggestion are shown, and there is no request to the model.
- `AC-CMD-013`: Given a turn is running, when the user types `/new`, `/sessions`, `/fork`, `/agent <name>`, `/model <...>`, or `/compact`, then the command is rejected with a message to wait for or cancel the turn.
- `AC-CMD-014`: Given a turn is running, when the user types `/permission <trust|ask|never>` or `/yolo`, then `approval/policy` is written and the new policy applies starting from the next tool evaluation in the same turn.

### One-shot

#### REQ-RUN-001: Running one turn

`lana run [--agent <name>]` shall run one turn with the `headless` profile in the cwd, then exit. The prompt
is taken from the arguments. If there are no arguments and stdin is not a TTY, the prompt is taken from
stdin. If both are present, the stdin content is appended after the argument prompt.

- `AC-RUN-001`: Given `lana run --agent assistant "hello"`, when the turn completes, then the final answer is on stdout and the exit code is 0.
- `AC-RUN-002`: Given `cat notes.md | lana run --agent assistant`, when the turn runs, then the contents of `notes.md` become the prompt.
- `AC-RUN-003`: Given there is no prompt argument and stdin is a TTY, when the user runs `lana run`, then the process exits with exit code 2.
- `AC-RUN-010`: Given no `--agent`, when `lana run` is run, then the agent is chosen with the same rules as `REQ-CHAT-001`; without a `cli` route, exit code 2.

#### REQ-RUN-002: Output format

`--output text` (default) writes only the final answer to stdout. `--output json` writes a single JSON object
at the end containing the answer, the session ID, the stop reason, and token usage. `--output stream-json`
writes one event per line (JSON Lines) while the turn runs. Logs always go to stderr.

- `AC-RUN-004`: Given `--output text`, when the turn completes, then stdout contains only the final answer.
- `AC-RUN-005`: Given `--output json`, when the turn completes, then stdout contains exactly one valid JSON object.
- `AC-RUN-006`: Given `--output stream-json`, when the turn runs, then each stdout line is a valid JSON object and the last line marks the end of the turn.

#### REQ-RUN-003: Approval in one-shot

Without `--ask`, the session policy is `never`. With `--ask`, the policy is `ask` and the approval prompt
is shown through `/dev/tty`. If `/dev/tty` is not available, the outcome is `unavailable`.

- `AC-RUN-007`: Given no `--ask`, when the model calls a tool that requires approval, then the tool is rejected without a prompt.
- `AC-RUN-008`: Given `--ask` and `/dev/tty` is available, when a tool requires approval, then the prompt is shown in the terminal and stdout is not mixed with the prompt.
- `AC-RUN-009`: Given `--ask` and `/dev/tty` is not available, when a tool requires approval, then the outcome is `unavailable` and the tool is rejected.

### Exit codes

#### REQ-EXIT-001: Exit code per class

All subcommands in this spec shall use the following exit codes:

| Code | Meaning |
|---:|---|
| 0 | Success |
| 1 | General or runtime error |
| 2 | Incorrect usage or invalid config |
| 3 | Provider failure (authentication, or retries and fallback exhausted) |
| 4 | Turn stopped because of `max_steps` |
| 130 | Cancelled with `Ctrl+C` |

- `AC-EXIT-001`: Given the provider API key is wrong, when `lana run` runs, then the exit code is 3.
- `AC-EXIT-002`: Given the turn reaches `max_steps`, when `lana run` completes, then the exit code is 4.
- `AC-EXIT-003`: Given `lana run` is running, when the user presses `Ctrl+C`, then the exit code is 130.

### Init

#### REQ-INIT-001: Initial configuration wizard

`lana init` shall ask for the provider (OpenAI and OpenRouter in build M3; Anthropic appears when its adapter
exists in M4), the API key env name, and the default model, then create `LANA_HOME` (default `~/.lana/`)
containing `config.toml`, `agents/default.md`, and `secrets.env` with permission `0600`. Existing files must
not be overwritten unless `--force` is given. `--yes` runs the wizard without questions using default values.

- `AC-INIT-001`: Given `LANA_HOME` does not exist, when the user completes the wizard, then all three files are created and `lana config check` passes.
- `AC-INIT-002`: Given `config.toml` already exists, when the user runs `lana init` without `--force`, then the file is unchanged and the message names the existing file.
- `AC-INIT-003`: Given the wizard completes, when `secrets.env` is checked, then its permission is `0600`.
- `AC-INIT-004`: Given `lana init --yes`, when it is run without a TTY, then the files are created without questions.

Default values for `--yes` are defined in the contract.

### Config

#### REQ-CONF-001: Config commands

| Command | Behavior |
|---|---|
| `lana config check` | Validate all config layers. Report every error with its file and key |
| `lana --profile <p> --dump-config` | Print the effective config and the origin of each line |
| `lana config path` | Print `LANA_HOME` and the list of files read, with whether each exists |
| `lana config edit` | Open `config.toml` in `$EDITOR`, then run the check after the editor closes |

- `AC-CONF-001`: Given a valid config, when `lana config check` runs, then the exit code is 0.
- `AC-CONF-002`: Given `agents.coder.provider` refers to a provider that does not exist, when `lana config check` runs, then the exit code is 2 and the message names the file and the key `agents.coder.provider`.
- `AC-CONF-003`: Given a patch overrides a module line, when `--dump-config` runs, then that line shows its origin layer.
- `AC-CONF-004`: Given `LANA_HOME=/tmp/x`, when `lana config path` runs, then the output names `/tmp/x` and the files that were searched.
- `AC-CONF-005`: Given `$EDITOR` is empty, when `lana config edit` runs, then the process exits with exit code 2 and a message asking for `$EDITOR` to be set.
- `AC-CONF-006`: Given the user saves an invalid config in the editor, when the editor closes, then the check result is shown and the exit code is 2.

## Chat state matrix

| State | Input | Result | Allowed |
|---|---|---|---:|
| Idle | Message | Turn starts | Yes |
| Idle | Slash command | Command runs | Yes |
| Turn running | Message | Enters the inbox | Yes |
| Turn running | `/help`, `/clear`, `/permission`, `/model`, `/agent` without arguments | Read-only command runs | Yes |
| Turn running | `/permission <x>`, `/yolo` | Policy changes starting from the next tool evaluation | Yes |
| Turn running | Commands that change the session (see `AC-CMD-013`) | Rejected with a message | No |
| Turn running | `Ctrl+C` | Turn cancelled | Yes |
| Awaiting approval | `y`, `a`, `n` | Outcome sent | Yes |
| Awaiting approval | `Ctrl+C` | Outcome `cancelled`, turn cancelled | Yes |

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Agent not registered | Message names the agent | 2 |
| Invalid config | Message from config validation with file and key | 2 |
| `LANA_HOME` does not exist | Message suggests `lana init` | 2 |
| Provider API key env is missing | Message names the env | 3 |
| Provider fails after retry and fallback | Chat: error message, chat stays open. `run`: exits | Chat: keeps running. `run`: 3 |
| `--resume` to a session in another cwd | Message names the original cwd | 2 |
| `--resume` to an ID that does not exist | Message names the ID | 2 |
| `lana chat` without a TTY | Message suggests `lana run` | 2 |

## Authorization and security

- The CLI runs as the OS user who starts it. No additional login in embedded mode.
- Approval fails closed: without a valid answer, the tool does not run.
- Secrets are never printed to the terminal, stdout, or stderr. `--dump-config` redacts secret values.
- `~/.lana/history` and `secrets.env` use permission `0600`.

## Data integrity and concurrency

- All messages, tool calls, policy changes, model changes, and approvals from the CLI are recorded in the session event log.
- A session has only one active turn at a time.
- A session may be opened by only one `lana chat` or `lana run` process at a time (`REQ-SESS-002`).

## Accessibility

- Every action can be performed with the keyboard.
- Important status (approval, error, yolo, policy) is marked with text, not only color.
- `NO_COLOR` disables color.
- `lana run` and `lana config` remain readable when stdout is not a TTY.

## Scenarios

### SC-001: Coding with approval

**Given** agent `coder` with policy `ask` and cwd `~/coding/x`
**When** the user asks "run the tests" and the model calls `bash(cargo test)`, which is not in `allow`
**Then** one tool call line and the approval prompt are shown; after `y`, the tool status becomes success and the model continues its answer

### SC-002: One-shot from a script

**Given** agent `assistant` is registered
**When** a script runs `lana run --agent assistant --output json "summarize today's news"`
**Then** stdout contains one JSON object and the exit code is 0

### SC-003: First installation

**Given** `~/.lana/` does not exist
**When** the user runs `lana init`, chooses OpenAI, and then runs `lana chat`
**Then** chat opens with the agent `default`

### SC-004: Try another direction with fork

**Given** session `A` has three turns
**When** the user types `/fork` and then sends a new message
**Then** the message goes to the new session `B`, and `A` still has three turns

## Question resolutions

| ID | Question | Resolution |
|---|---|---|
| `Q-001` | GitHub issue number | `#1` in `ridhomujizat/lana-harness` (private) |
| `Q-002` | Subcommands in scope | `chat`, `run`, `init`, `config` |
| `Q-003` | Attach or embedded | Embedded only |
| `Q-004` | Line mode or TUI | Inline TUI. The library follows ADR 0013 (Bubble Tea); editorial note after the move to Go |
| `Q-005` | Session lifecycle | New by default, `--continue`, `--resume <id>` |
| `Q-006` | Slash commands | `/agent`, `/new`, `/sessions`, `/help`, `/exit`, `/permission`, `/yolo`, `/model`, `/compact`, `/fork`, `/clear` |
| `Q-007` | `Ctrl+C` and `Ctrl+D` | `Ctrl+C` cancels the turn; idle: clear the input, twice within 2 seconds exits; `Ctrl+D` on empty input exits |
| `Q-008` | Tool call details | One compact line, expandable |
| `Q-009` | Multi-line input | Like Claude Code: `Shift+Enter`, `Alt/Option+Enter`, `Ctrl+J`, `\` + `Enter`; paste is not sent immediately |
| `Q-010` | Output and exit codes for `run` | `text`, `json`, `stream-json`; prompt from stdin; exit code per class |
| `Q-011` | Workspace | cwd |
| `Q-012` | Input history | `~/.lana/history`, 1000 entries, `0600`, `Ctrl+R` |
| `Q-013` | `lana init` | Interactive wizard, `--force`, `--yes` |
| `Q-014` | `lana config` | `check`, `--dump-config`, `path`, `edit` |
| `Q-015` | Alternate screen or inline | Inline, like Claude Code |
| `Q-016` | Scope of `--continue` and `/sessions` | Per directory |
| `Q-017` | Meaning of `/clear` | Clear the screen only |
| `Q-018` | Meaning of `/fork` | Fork at the last turn, then switch |
| `Q-020` | `/permission` and `/yolo` while a turn is running | Allowed; applies starting from the next tool evaluation (agent proposal, approved with revision 3) |
| `CQ-001` | How to open tool call details in the inline TUI | Verbose mode with `Ctrl+O` (revision 4) |
| `CQ-002` | Agent for `lana run` without `--agent` | Same as chat (revision 4) |
| `DQ-002` | Two processes open the same session | Reject the second process; the lock of a dead process is released (revision 5) |
| `Q-019` | Without a TTY | `chat` requires a TTY (exit 2); `run --ask` goes through `/dev/tty`, and without a TTY the outcome is `unavailable` |
| `Q-020` | Providers offered by `lana init` (reconciliation revision 6) | Agent recommendation (reconciliation): only kinds supported by the build. M3: `openai` and `openrouter`. `anthropic` appears after the M4 adapter exists (spec #3 `Q-021`, `CQ-018`). This resolves the conflict with `SC-003` in spec #3. |
| `Q-021` | Service behind `/compact` (reconciliation revision 6) | Agent recommendation (reconciliation): `/compact` calls `core.Compactor.CompactNow` from spec #4 (`API-COMP-001`). Spec #3 has no `CompactNow` (`CQ-019` in spec #3). |
| `Q-022` | Changing the model while a turn is running (reconciliation revision 6) | Agent recommendation (reconciliation): the CLI rejects `/model` while a turn is running (`AC-CMD-013`). This is a UI choice. Spec #3 still allows `SetModel` during a turn and it applies from the next step. No contract change. |

## Blocking Open Questions

None.

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-CHAT-001` | `AC-CHAT-001`, `AC-CHAT-002`, `AC-CHAT-003`, `AC-CHAT-004` | ADR 0003, `Q-010` |
| `REQ-CHAT-002` | `AC-CHAT-005` | `Q-003` |
| `REQ-CHAT-003` | `AC-CHAT-006`, `AC-CHAT-007`, `AC-CHAT-008` | `Q-004`, `Q-015` |
| `REQ-CHAT-004` | `AC-CHAT-009` | ADR 0008 |
| `REQ-CHAT-005` | `AC-CHAT-010`, `AC-CHAT-011`, `AC-CHAT-024` | `Q-008`, `CQ-001` |
| `REQ-CHAT-006` | `AC-CHAT-012` | ADR 0003 |
| `REQ-CHAT-007` | `AC-CHAT-013`, `AC-CHAT-014`, `AC-CHAT-015` | `Q-009` |
| `REQ-CHAT-008` | `AC-CHAT-016`, `AC-CHAT-017`, `AC-CHAT-018`, `AC-CHAT-019` | `Q-007` |
| `REQ-CHAT-009` | `AC-CHAT-020`, `AC-CHAT-021` | `Q-012` |
| `REQ-CHAT-010` | `AC-CHAT-022` | `Q-011` |
| `REQ-CHAT-011` | `AC-CHAT-023` | `Q-019` |
| `REQ-SESS-001` | `AC-SESS-001`, `AC-SESS-002`, `AC-SESS-003`, `AC-SESS-004`, `AC-SESS-005` | `Q-005`, `Q-016`, ADR 0009 |
| `REQ-SESS-002` | `AC-SESS-006`, `AC-SESS-007` | `DQ-002` |
| `REQ-APPR-001` | `AC-APPR-001`, `AC-APPR-002`, `AC-APPR-003`, `AC-APPR-004`, `AC-APPR-005` | ADR 0004, ADR 0008 |
| `REQ-APPR-002` | `AC-APPR-006`, `AC-APPR-007` | ADR 0004, `Q-006` |
| `REQ-CMD-001` | `AC-CMD-001` through `AC-CMD-014` | `Q-006`, `Q-017`, `Q-018`, ADR 0003, ADR 0009 |
| `REQ-RUN-001` | `AC-RUN-001`, `AC-RUN-002`, `AC-RUN-003`, `AC-RUN-010` | `Q-010`, ADR 0010, `CQ-002` |
| `REQ-RUN-002` | `AC-RUN-004`, `AC-RUN-005`, `AC-RUN-006` | `Q-010` |
| `REQ-RUN-003` | `AC-RUN-007`, `AC-RUN-008`, `AC-RUN-009` | ADR 0010, `Q-019` |
| `REQ-EXIT-001` | `AC-EXIT-001`, `AC-EXIT-002`, `AC-EXIT-003` | `Q-010` |
| `REQ-INIT-001` | `AC-INIT-001`, `AC-INIT-002`, `AC-INIT-003`, `AC-INIT-004` | `Q-013` |
| `REQ-CONF-001` | `AC-CONF-001` through `AC-CONF-006` | `Q-014`, ADR 0010 |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit.
- [x] State transitions and destructive effects are explicit.
- [x] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: Ridho
- **Approved on**: 2026-10-29 (revision 3; revision 4 for `CQ-001` and `CQ-002`; revision 5 for `DQ-002`; revision 6 reconciliation by the agent on behalf of the owner, gate lifted 2026-10-29)
