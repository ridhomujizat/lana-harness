# Contract: CLI and TUI chat

- **Issue**: [`#1`](https://github.com/ridhomujizat/lana-harness/issues/1)
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 6
- **Requirements status**: Approved
- **Contract revision**: 5 (revision 5: `deepseek` provider and API key prompt in `lana init`)
- **Applies to**: CLI (subcommands, flags, env, exit codes, stdout/stderr, JSON format, keys, slash commands, files created)
- **Status**: Approved

This file defines the exact form of the terminal interface. Behavior is owned by `requirements.md`.

## Shared conventions

| Topic | Decision |
|---|---|
| Binary name | `lana` |
| Exit codes | `0` success, `1` general/runtime error, `2` incorrect usage or invalid config, `3` provider failure, `4` `max_steps`, `130` cancelled (`REQ-EXIT-001`) |
| stdout | Only the requested result (answer, JSON, `config` output). Never contains logs or approval prompts |
| stderr | Error messages and logs for `run` and `config`. Error messages start with `error: ` |
| Logs during `chat` | File `$LANA_HOME/logs/lana.log`. No logs to stderr while the TUI is active |
| Log level | Env `LANA_LOG` is `debug`, `info`, `warn`, or `error` (`log/slog`). Flag `-v` = `info`, `-vv` = `debug`, `-vvv` = `debug` with source location. Default `warn`. The flag wins over the env. Other values: exit 2 |
| `LANA_HOME` | Default `~/.lana`. The env `LANA_HOME` overrides the location |
| Secret | Read from the process env first, then from `$LANA_HOME/secrets.env`. The process env wins. Never printed; shown as `"***"` |
| Color | Off if the env `NO_COLOR` is set or the output is not a TTY |
| Session ID | 26-character ULID (Crockford base32, uppercase). Input accepts a unique prefix of at least 6 characters, case-insensitive |
| Model format | `<provider>:<model>`, same as `fallback` in config. Example `openai:gpt-5-mini` |
| JSON serialization | UTF-8, one object per line for JSON Lines, `snake_case` fields, times in milliseconds |

## Operation index

| Operation ID | Type | Command | Requirements | Acceptance criteria |
|---|---|---|---|---|
| `API-CLI-001` | CLI | Global flags | `REQ-CONF-001`, `REQ-EXIT-001` | `AC-CONF-003`, `AC-EXIT-001` |
| `API-CLI-002` | CLI | `lana chat` | `REQ-CHAT-001`, `REQ-CHAT-002`, `REQ-CHAT-010`, `REQ-CHAT-011`, `REQ-SESS-001`, `REQ-SESS-002`, `REQ-APPR-002` | `AC-CHAT-001` through `AC-CHAT-005`, `AC-CHAT-022`, `AC-CHAT-023`, `AC-SESS-001` through `AC-SESS-007`, `AC-APPR-006` |
| `API-CLI-003` | TUI | Layout and status bar | `REQ-CHAT-003`, `REQ-CHAT-004`, `REQ-CHAT-005` | `AC-CHAT-006` through `AC-CHAT-011`, `AC-CHAT-024` |
| `API-CLI-004` | TUI | Keys | `REQ-CHAT-006`, `REQ-CHAT-007`, `REQ-CHAT-008`, `REQ-CHAT-009` | `AC-CHAT-012` through `AC-CHAT-021` |
| `API-CLI-005` | TUI | Approval prompt | `REQ-APPR-001` | `AC-APPR-001` through `AC-APPR-005` |
| `API-CLI-006` | TUI | Slash commands | `REQ-CMD-001`, `REQ-APPR-002` | `AC-CMD-001` through `AC-CMD-014`, `AC-APPR-007` |
| `API-CLI-007` | CLI | `lana run` | `REQ-RUN-001`, `REQ-RUN-002`, `REQ-RUN-003` | `AC-RUN-001` through `AC-RUN-010`, `AC-EXIT-002`, `AC-EXIT-003` |
| `API-CLI-008` | CLI | `lana init` | `REQ-INIT-001` | `AC-INIT-001` through `AC-INIT-004` |
| `API-CLI-009` | CLI | `lana config check` | `REQ-CONF-001` | `AC-CONF-001`, `AC-CONF-002` |
| `API-CLI-010` | CLI | `lana config path` | `REQ-CONF-001` | `AC-CONF-004` |
| `API-CLI-011` | CLI | `lana config edit` | `REQ-CONF-001` | `AC-CONF-005`, `AC-CONF-006` |

## API-CLI-001: Global flags

- **Goal**: flags that apply to all subcommands.
- **Stability**: New

| Flag | Type | Default | Meaning |
|---|---|---|---|
| `--profile <name>` | string | depends on subcommand (`chat` = `minimal`, `run` = `headless`) | Base profile |
| `--patch <file>` | path, repeatable | — | Config overlay, applied in order |
| `--dump-config[=toml\|json]` | enum | `toml` | Print the effective config to stdout, then exit `0` |
| `-v`, `-vv`, `-vvv` | count | — | Log level |
| `-h`, `--help` | — | — | Help, exit `0` |
| `-V`, `--version` | — | — | Version, exit `0` |

Format of `--dump-config=toml`:

```toml
# effective lana config · profile: minimal
# layers: base, profile:minimal, ~/.lana/profiles/minimal.toml (not present), ~/.lana/config.toml

# from: ~/.lana/config.toml
[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"

# from: base
[[modules]]
id = "store"
module = "sqlite_store"
config = { path = "lana.db" }
```

- Every table and every `[[modules]]` row is annotated with a `# from: <layer>` comment.
- Secret values are written as `"***"`. Fields that contain env names (such as `api_key_env`) are not redacted.
- `--dump-config=json` writes `{ "profile", "layers": [...], "config": {...}, "origins": { "<path key>": "<layer>" } }`.

| Failure | Exit | stderr message |
|---|---:|---|
| Unknown flag or invalid value | 2 | `error: <flag parser message>` |
| `--patch` file does not exist | 2 | `error: patch file not found: <path>` |
| Unknown profile | 2 | `error: unknown profile: <name> (available: ...)` |

## API-CLI-002: `lana chat`

```text
lana chat [--agent <name>] [--continue | --resume <id>] [--yolo]
```

| Flag | Type | Required | Meaning |
|---|---|---:|---|
| `--agent <name>` | string | No | Session agent. Default from the first `cli` route |
| `--continue`, `-c` | bool | No | Open the last CLI session in the cwd |
| `--resume <id>`, `-r <id>` | ULID or prefix | No | Open a specific session in the cwd |
| `--yolo` | bool | No | Session policy `trust` |

Rules:

- `--continue` and `--resume` must not be used together.
- `--agent` together with `--continue` or `--resume` replaces the agent of the session being opened, same as `/agent <name>`.
- Workspace = the working directory (`os.Getwd`) after symlinks are resolved (`filepath.EvalSymlinks`).
- When `--continue` finds no session, the info line `no previous session in <cwd>, creating a new session` is printed, then chat starts.

| Condition | Exit | stderr message |
|---|---:|---|
| Normal exit (`/exit`, `Ctrl+D`, `Ctrl+C` twice) | 0 | — |
| stdin or stdout is not a TTY | 2 | `error: lana chat requires an interactive terminal; use 'lana run' for scripts` |
| `LANA_HOME` does not exist | 2 | `error: configuration not found at <path>; run 'lana init'` |
| Agent not registered | 2 | `error: unknown agent: <name> (available: ...)` |
| No `cli` route and no `--agent` | 2 | `error: no default agent for CLI; use --agent <name> or add [[routes]] channel = "cli"` |
| `--continue` and `--resume` together | 2 | `error: --continue and --resume cannot be used together` |
| ID not found | 2 | `error: session not found: <id>` |
| Ambiguous ID prefix | 2 | `error: prefix <id> matches several sessions:` followed by one ID per line |
| Session from another cwd | 2 | `error: session <id> belongs to directory <original cwd>; run it from that directory` |
| Session in use by another process | 2 | `error: session <id> is in use by process <pid>` |
| Invalid config | 2 | Same as `API-CLI-009` |
| Provider API key env is missing | 3 | `error: env <ENV_NAME> is not set for provider <provider>` |

A provider error during chat does not stop the process. The message is shown in the scrollback (see `API-CLI-003`).

## API-CLI-003: TUI layout and status bar

Inline TUI (Bubble Tea without alt screen). History is written to the scrollback above the redrawn area (viewport).

```text
lana · default · embedded · ask · openai:gpt-5-mini         ← header, printed once when the session opens
session 01JBX7Q3KM · ~/coding/x

> run the tests

I'll run cargo test.

⏺ bash  cargo test
  ✓ success · 2.3s

All tests pass.
───────────────────────────────────────────────────────────── ← viewport starts here
> _
default · embedded · ask · gpt-5-mini · 12.3k tok
```

Scrollback elements:

| Element | Format |
|---|---|
| User message | `> ` followed by the text; continuation lines indented by 2 spaces |
| Model text | Markdown rendered: headings, lists, bold, italic, inline code, code blocks with language. Tables and images printed as-is |
| Compact tool call | `⏺ <name>  <main argument>` then `  <icon> <status> · <duration>` |
| Status icon | `…` running, `✓` success, `✗` error, `⊘` rejected. The status text is always written: `running`, `success`, `error`, `rejected` |
| Verbose tool call | Like compact, plus an `input:` block (pretty JSON) and an `output:` block (full text), indented by 2 spaces |
| Provider error | `✗ provider error: <message>`; turn closed, chat stays open |
| Info | Line starting with `· ` (example `· switched to session 01JBX…`) |

Main argument per tool:

| Tool | Main argument |
|---|---|
| `bash` | `command` |
| `read_file`, `write_file`, `edit_file` | `path`, relative to the workspace |
| `glob`, `grep` | `pattern` |
| `web_fetch` | `url` |
| `task` | `agent` and `title` |
| `mcp__*` and others | First 60 characters of the JSON input |

Status bar (last line of the viewport), separated by ` · `:

| Position | Content |
|---|---|
| 1 | Agent name |
| 2 | `embedded` |
| 3 | Policy: `trust`, `ask`, or `never` |
| 4 | Model name without provider |
| 5 | Session tokens (input + output), format `12.3k tok` |
| 6 | `verbose` if verbose mode is on |
| 7 | `turn running · ctrl+c cancel` while a turn is running |

While the `trust` policy is active, a warning line is shown above the input for as long as that policy is active:

```text
⚠ YOLO: all tools allowed without approval (deny rules still apply)
```

## API-CLI-004: Keys

| Key | Context | Action |
|---|---|---|
| `Enter` | Input | Send the message. Empty input is not sent |
| `Shift+Enter` | Input, terminal with keyboard protocol (kitty, WezTerm, Ghostty, iTerm2 with CSI u) | New line |
| `Alt+Enter`, `Option+Enter` | Input | New line |
| `Ctrl+J` | Input | New line |
| `\` then `Enter` | Input, `\` at the end of the line | `\` removed, new line |
| Paste | Input | Text enters the input without being sent (bracketed paste) |
| `↑` / `↓` | Input, cursor on the first/last line | Previous/next history entry |
| `Ctrl+R` | Input | Incremental history search. `Enter` selects, `Esc` cancels |
| `Ctrl+O` | Anytime | Turn verbose mode on or off. When turned on, the last tool call in the session is reprinted in full |
| `Ctrl+C` | Turn running or awaiting approval | Cancel the turn |
| `Ctrl+C` | Idle, input has text | Clear the input |
| `Ctrl+C` | Idle, input empty | Print `· press ctrl+c again to exit`; pressing again within 2 seconds exits with `0` |
| `Ctrl+D` | Empty input | Exit `0` |
| `Esc` | Picker or history search | Close |

Messages sent while a turn is running are printed to the scrollback with the `> ` marker and the label `(queued)`.

## API-CLI-005: Approval prompt

Shown in the viewport, above the input:

```text
? Allow bash: cargo test
  [y] yes once   [a] always for this session   [n] reject
```

| Key | Outcome |
|---|---|
| `y` or `Y` | `allowed_once` |
| `a` or `A` | `allowed_session` |
| `n` or `N` | `rejected`, then shows `reason (optional, Enter to skip): ` |
| `Ctrl+C` | `cancelled`, turn cancelled |
| Other keys | Ignored |
| No answer for 10 minutes | `unavailable`, prints `· approval expired` |

- The displayed arguments are taken from `tool/call` via `call_id` (ADR 0004).
- If verbose mode is on, the full JSON input is shown above the options.
- Approval from a subagent is labeled: `? [explorer] Allow read_file: src/main.rs`.
- After answering, a result line is printed to the scrollback: `  ✓ allowed once`, `  ✓ allowed for session`, `  ⊘ rejected`, `  ⊘ cancelled`.

## API-CLI-006: Slash commands

Parsing: input whose first character is `/`. `//text` is sent to the model as `/text`.

| Command | Argument | While a turn is running | Output |
|---|---|---|---|
| `/agent` | — | Allowed | Active agent, then the list `name · provider:model · policy` |
| `/agent <name>` | agent name | Rejected | `· agent: <name>` |
| `/new` | — | Rejected | `· new session <id>` |
| `/sessions` | — | Rejected | Picker (see below) |
| `/permission` | — | Allowed | `· policy: <value>` |
| `/permission <trust\|ask\|never>` | value | Allowed | `· policy: <value>` |
| `/yolo` | — | Allowed | Same as `/permission trust` |
| `/model` | — | Allowed | `· model: <provider>:<model>` |
| `/model <provider>:<model>` | model | Rejected | `· model: <provider>:<model>` |
| `/compact` | — | Rejected | `· context compacted (<tokens before> → <tokens after> tok)` |
| `/fork` | — | Rejected | `· forked from <old id> → session <new id>` |
| `/clear` | — | Allowed | Screen cleared, header printed again |
| `/help` | — | Allowed | List of commands and keys |
| `/exit` | — | Allowed, cancels the turn first | Exit `0` |

The `/compact` command calls `core.Compactor.CompactNow` from spec #4 (`API-COMP-001`) (`Q-021`). The `/model` command is rejected while a turn is running (`AC-CMD-013`). This is a UI choice (`CQ-011`).

The `/sessions` picker in the viewport:

```text
Sessions in ~/coding/x (↑↓ select · enter open · esc cancel)
› 01JBX7Q3KM  fix parser test         default  5 turns   2 min ago   (active)
  01JBW2A9PD  add --output flag       default  12 turns  3 hours ago
```

- Maximum 20 sessions, most recently `updated_at` first.
- Title comes from the `sessions.title` projection. A session without a title uses the first 40 characters of the first user message.

| Failure | Output |
|---|---|
| Unknown command | `✗ unknown command: /<x> · type /help` |
| Wrong argument | `✗ <command>: <reason> · example: <example>` |
| Agent or provider not registered | `✗ unknown agent: <name>` or `✗ unknown provider: <name>` |
| Rejected while a turn is running | `✗ wait for the turn to finish or press ctrl+c first` |
| `/fork` without a completed turn | `✗ no completed turn to fork yet` |

## API-CLI-007: `lana run`

```text
lana run [--agent <name>] [--output text|json|stream-json] [--ask] [prompt...]
```

| Input | Type | Required | Meaning |
|---|---|---:|---|
| `prompt...` | string, remaining arguments joined with spaces | No | Prompt |
| stdin | UTF-8 text | No | Read if stdin is not a TTY. Appended after the argument prompt with two newlines |
| `--agent <name>` | string | No | Default from the first `cli` route |
| `--output`, `-o` | enum | No | Default `text` |
| `--ask` | bool | No | Policy `ask`, approval through `/dev/tty` |

`--output text`: stdout contains the final answer text plus one newline.

`--output json`: stdout contains exactly one object:

```json
{
  "type": "result",
  "session_id": "01JBX7Q3KM8Z4T2V6N0P1R5S9W",
  "agent": "default",
  "model": "openai:gpt-5-mini",
  "cwd": "/Users/ridho/coding/x",
  "stop_reason": "end_turn",
  "text": "Final answer",
  "usage": { "input_tokens": 1200, "output_tokens": 340 },
  "duration_ms": 4210,
  "exit_code": 0,
  "error": null
}
```

| Field | Type | Meaning |
|---|---|---|
| `stop_reason` | `end_turn` \| `max_steps` \| `cancelled` \| `error` | Reason the turn stopped |
| `text` | string | Final answer text; empty string if there is none |
| `error` | `null` or `{ "class": "provider" \| "runtime", "message": string }` | Set if `stop_reason = error` |
| `exit_code` | integer | Same as the process exit code |

`--output stream-json`: JSON Lines. The last line is always `type: "result"` with the same shape
as `--output json`.

| `type` | Fields |
|---|---|
| `session` | `session_id`, `agent`, `model`, `cwd` |
| `text_delta` | `text` |
| `tool_call` | `call_id`, `name`, `input` |
| `approval` | `call_id`, `outcome` (`allowed_once`, `allowed_session`, `rejected`, `cancelled`, `unavailable`) |
| `tool_result` | `call_id`, `status` (`success`, `error`, `rejected`), `output` |
| `result` | See `--output json` |

New types may be added in later versions. Consumers must ignore `type` values they do not recognize.

| Condition | Exit | stdout (`json`/`stream-json`) | stderr |
|---|---:|---|---|
| Success | 0 | `result` | — |
| No prompt and stdin is a TTY | 2 | empty | `error: empty prompt; provide an argument or pipe through stdin` |
| Agent or config invalid | 2 | empty | Same as `API-CLI-002` |
| Provider API key env is missing | 3 | empty | `error: env <ENV_NAME> is not set for provider <provider>` |
| Provider fails after retry and fallback | 3 | `result` with `error.class = "provider"` | `error: <message>` |
| `max_steps` | 4 | `result` with `stop_reason = "max_steps"` | `error: turn stopped because of max_steps (<n>)` |
| `Ctrl+C` or SIGINT | 130 | `result` with `stop_reason = "cancelled"` | — |
| Other errors | 1 | `result` with `error.class = "runtime"` if the session was already created | `error: <message>` |

The approval prompt with `--ask` uses the `API-CLI-005` format in line mode (without the TUI) on `/dev/tty`.

## API-CLI-008: `lana init`

```text
lana init [--force] [--yes] [--provider openai|openrouter|deepseek]
```

Wizard questions (in order):

| Question | Options | Default |
|---|---|---|
| Provider | `openai`, `openrouter`, `deepseek`. `anthropic` is added when the M4 adapter exists (`CQ-009`) | `openai` |
| API key env name | string | `OPENAI_API_KEY` / `OPENROUTER_API_KEY` / `DEEPSEEK_API_KEY` |
| Default model | string | `gpt-5-mini` / `openai/gpt-5-mini` / `deepseek-chat` |
| API key | string, optional; written to `secrets.env` without echo | empty (fill in later) |

`--yes` uses all defaults without asking (the provider comes from `--provider`, and the API key is not asked). Files created with the defaults:

`$LANA_HOME/config.toml`

```toml
[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"

[agents.default]
provider = "openai"
model = "gpt-5-mini"
prompt_file = "agents/default.md"
tools = ["read_file", "write_file", "edit_file", "glob", "grep", "bash"]
permission = "ask"

[[routes]]
channel = "cli"
agent = "default"
```

For `openrouter`: `base_url = "https://openrouter.ai/api/v1"`. For `deepseek`: `base_url = "https://api.deepseek.com/v1"`. For `anthropic` (after M4): `kind = "anthropic"` without `base_url`. In M3 this kind is not offered (`CQ-009`).

`$LANA_HOME/agents/default.md`: general system prompt for a coding and personal assistant (content defined in the design).

`$LANA_HOME/secrets.env` (permission `0600`):

If an API key is entered in the wizard:

```text
# Provider API key; do not commit.
OPENAI_API_KEY=sk-...
```

If it is skipped:

```text
# Fill in the API key here, or set it as an env var in the shell.
# OPENAI_API_KEY=
```

Success output (stdout):

```text
✓ created ~/.lana/config.toml
✓ created ~/.lana/agents/default.md
✓ created ~/.lana/secrets.env (0600, API key saved)
· run: lana chat
```

If the API key is skipped, the last line becomes
`· set OPENAI_API_KEY in ~/.lana/secrets.env or as a shell env var, then run: lana chat`.

| Condition | Exit | Output |
|---|---:|---|
| Success | 0 | As above |
| File exists, without `--force` | 2 | stderr `error: file already exists: <path>; use --force to overwrite`. No file is changed |
| With `--force` | 0 | Line `✓ overwritten <path>` for each existing file |
| No TTY and no `--yes` | 2 | stderr `error: lana init requires a terminal; use --yes for default values` |
| `Ctrl+C` during the wizard | 130 | No files created |

## API-CLI-009: `lana config check`

```text
lana config check
```

| Condition | Exit | Output |
|---|---:|---|
| Valid | 0 | stdout `✓ config valid (<n> layers, <m> agents, <k> providers)` |
| Invalid | 2 | stderr, one line per error: `error: <file>:<line>: <key>: <message>`. The line is omitted if unknown |

Example:

```text
error: ~/.lana/config.toml:12: agents.coder.provider: provider 'anthropik' not registered (available: openai)
```

All errors are reported at once; the check does not stop at the first error.

## API-CLI-010: `lana config path`

```text
lana config path
```

Output on stdout, exit `0`:

```text
LANA_HOME  /Users/ridho/.lana
✓ /Users/ridho/.lana/config.toml
· /Users/ridho/.lana/profiles/minimal.toml (not present)
✓ /Users/ridho/.lana/secrets.env
✓ /Users/ridho/.lana/lana.db
✓ /Users/ridho/.lana/logs/lana.log
```

The profile shown follows `--profile` (default `minimal`).

## API-CLI-011: `lana config edit`

```text
lana config edit
```

- Runs `$VISUAL`, or `$EDITOR` if `$VISUAL` is empty, with the argument `$LANA_HOME/config.toml`. The env value is split the way a shell would split it (`code --wait` is supported).
- After the editor exits, runs `API-CLI-009` and uses its exit code.

| Condition | Exit | Output |
|---|---:|---|
| `$VISUAL` and `$EDITOR` are empty | 2 | `error: set $EDITOR to use lana config edit` |
| Editor exits with a non-zero code | 1 | `error: editor exited with code <n>`; check is not run |
| `config.toml` does not exist | 2 | `error: configuration not found at <path>; run 'lana init'` |

## Contract traceability

| Operation | Requirements | Design component | Verification |
|---|---|---|---|
| `API-CLI-001` | `REQ-CONF-001`, `REQ-EXIT-001` | Defined in design | CLI test (`testscript`) |
| `API-CLI-002` | `REQ-CHAT-001`, `REQ-CHAT-002`, `REQ-CHAT-010`, `REQ-CHAT-011`, `REQ-SESS-001`, `REQ-SESS-002`, `REQ-APPR-002` | Defined in design | CLI test and pseudo-terminal test |
| `API-CLI-003` | `REQ-CHAT-003`, `REQ-CHAT-004`, `REQ-CHAT-005` | Defined in design | Render snapshot |
| `API-CLI-004` | `REQ-CHAT-006` through `REQ-CHAT-009` | Defined in design | Input handler test |
| `API-CLI-005` | `REQ-APPR-001` | Defined in design | Approval answerer test |
| `API-CLI-006` | `REQ-CMD-001`, `REQ-APPR-002` | Defined in design | Command parser and dispatcher test |
| `API-CLI-007` | `REQ-RUN-001` through `REQ-RUN-003`, `REQ-EXIT-001` | Defined in design | CLI test with `MockLlm` |
| `API-CLI-008` | `REQ-INIT-001` | Defined in design | CLI test in a temporary `LANA_HOME` |
| `API-CLI-009` | `REQ-CONF-001` | Defined in design | CLI test with invalid config |
| `API-CLI-010` | `REQ-CONF-001` | Defined in design | CLI test |
| `API-CLI-011` | `REQ-CONF-001` | Defined in design | CLI test with a fake `EDITOR` |

## Blocking Contract Questions

| ID | Question | Controls | Owner | Status | Resolution |
|---|---|---|---|---|---|
| `CQ-001` | How to open tool call details in the inline TUI | `API-CLI-003`, `API-CLI-004` | Ridho | Resolved | Verbose mode with `Ctrl+O` |
| `CQ-002` | Agent for `lana run` without `--agent` | `API-CLI-007` | Ridho | Resolved | Same as chat |
| `CQ-003` | Format of `json` and `stream-json` | `API-CLI-007` | Ridho | Resolved | `result` object and a `type` per line for events |
| `CQ-004` | Format of session IDs | Shared conventions | Ridho | Resolved | ULID, unique prefix of at least 6 characters |
| `CQ-005` | Defaults for `lana init --yes` | `API-CLI-008` | Ridho | Resolved | `openai`, `OPENAI_API_KEY`, `gpt-5-mini` |
| `CQ-006` | Logging during chat | Shared conventions | Ridho | Resolved | Log file, `LANA_LOG`, `-v` |
| `CQ-007` | Format of `--dump-config` | `API-CLI-001` | Ridho | Resolved | TOML with origin comments, JSON option |
| `CQ-008` | Display of `/sessions` | `API-CLI-006` | Ridho | Resolved | Picker in the viewport |
| `CQ-009` | Providers offered by `lana init` | `API-CLI-008` | Ridho | Resolved | Agent recommendation (reconciliation): only kinds supported by the build. M3: `openai`, `openrouter`. `anthropic` after M4 (`CQ-018` in spec #3). Resolves the conflict with `SC-003` and `Q-021` in spec #3. |
| `CQ-010` | Service behind `/compact` | `API-CLI-006` | Ridho | Resolved | Agent recommendation (reconciliation): `/compact` uses `core.Compactor.CompactNow` (`API-COMP-001` in spec #4). Spec #3 has no `CompactNow`. |
| `CQ-011` | `/model` while a turn is running | `API-CLI-006` | Ridho | Resolved | Agent recommendation (reconciliation): the CLI rejects `/model` while a turn is running. This is a UI choice, not a contract constraint. Spec #3 `SetModel` allows it during a turn and it applies from the next step. No contract change. |

None still Open.

Details chosen by the agent without a separate question (please check during review): error and info
message text, status icons, `//` to send text that starts with `/`, the `(queued)` label, the aliases
`-c`/`-r`/`-o`, `$VISUAL` before `$EDITOR`, the `tools` list of the `default` agent, and `/exit`
cancelling the turn first.

## Approval

- [x] Requirements are Approved and their revision is recorded above.
- [x] Contract applicability is determined.
- [x] Every operation has a stable ID and is linked to requirements and acceptance criteria.
- [x] Input, success, failure, authorization, and compatibility are explicit.
- [x] No behavior exists only in this file.
- [x] Blocking Contract Questions is `None`.
- **Approved by**: Ridho (revisions 2 and 3)
- **Approved on**: 2026-10-29 (revision 1; revision 2 adds the session lock error from `DQ-002`). Revision 3 (move to Go, ADR 0013; `LANA_LOG` format changed to `slog` levels) approved 2026-10-29.

Revision 4 (reconciliation by the agent on behalf of the owner, gate lifted 2026-10-29): `CQ-009` through `CQ-011`, `lana init` providers limited to kinds supported by the build, `/compact` via spec #4.
