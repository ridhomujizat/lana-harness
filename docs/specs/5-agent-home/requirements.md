# Requirements: Agent home, memory, and onboarding

- **Issue**: —
- **Status**: Approved
- **Revision**: 4 (revision 2: onboarding interview removed, owner decision 2026-10-30; revision 3: all templates get every built-in tool and differ only by persona, owner decision 2026-10-30; revision 4: `USER.md` is shared at `$LANA_HOME/USER.md` with a migration rule, owner decision 2026-10-30, spec #9 rev 2 decision 9)
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (subcommands `lana agent`, files in `$LANA_HOME`, tools `memory_read` and `memory_write`, slash command `/memory`)
- **Related ADR**: [ADR Index](../../adr/README.md) — [0003](../../adr/0003-multi-agent-with-channel-routing.md), [0004](../../adr/0004-permission-mode-per-agent.md), [0010](../../adr/0010-profile-and-config-patch.md)
- **Related diagram**: [Diagram Index](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-and-approval.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-29 | "lana as a worker and assistant agent, like Hermes or OpenClaw; start with agent profile setup" | goal, scope |
| Owner decision 2026-10-29 | memory tools are part of this spec | `REQ-MEM-*` |
| Owner decision 2026-10-29 | creating an agent uses a CLI wizard plus an onboarding chat | `REQ-AGCLI-001`, `REQ-ONB-*` |
| Interview 2026-10-29 | Owner answers for `Q-001` through `Q-007` | Resolution column |
| Owner feedback 2026-10-30 | "the bot always asks name and more on first contact; keep it plain" | `REQ-ONB-001` removed in revision 2 |
| Owner decision 2026-10-30 | dashboard mockup: "About you. Shared by every agent." (spec #9 rev 2, decision 9) | `REQ-HOME-003`, `REQ-HOME-004`, `REQ-MEM-006`, `Q-008`, `Q-009` |
| ADR 0003 | agents are configuration, several agents per install | agent name, `[agents.<name>]` |
| ADR 0004 | permission per agent, approval | memory tool permission |
| ADR 0010 | the word "profile" already means a config profile | the term **agent home** in this spec |
| Comparison reference | Hermes Agent (`SOUL.md`, `MEMORY.md`, `USER.md`, bounded memory tool with add/replace/remove) | file model, memory limits |
| Comparison reference | OpenClaw (agent workspace with `AGENTS.md`, `SOUL.md`, `USER.md`, `BOOTSTRAP.md` onboarding) | onboarding flow |

## Goal

The owner can create several long-lived agents, each with its own persona and role, that know who the
owner is and remember durable facts across sessions. A new agent asks the owner about themselves in its
first chat, so the owner does not have to write prompt files by hand.

## Actors

- **Owner** — the only user. Creates, edits, and chats with agents. Owns every file in `$LANA_HOME`.
- **Agent** — the model acting through the runtime. May read and change only its own memory files, through the memory tools.

## Definitions

- **Agent home** — the folder `$LANA_HOME/agents/<name>/` that holds the persona and memory files of one agent.
- **Persona file** — `AGENT.md` in the agent home. Role, tone, and rules written by the owner. The agent cannot change it.
- **User file** — `USER.md` in `$LANA_HOME`, shared by every agent. Facts about the owner (name, form of address, language, timezone, work, preferences). Since revision 4 it is not in the agent home (`REQ-HOME-003`). Revisions 1 to 3 kept one per agent (`Q-001`).
- **Memory file** — `MEMORY.md` in the agent home. Durable notes the agent keeps for itself (environment facts, conventions, lessons learned).
- **Memory entry** — one item in the user file or memory file. Entries are separated by a line containing only `§`.
- **Template** — a built-in starting point for a new agent: `assistant`, `worker`, or `coder`.
- **Onboarding** — (revision 1 only) an interview to fill the user file; removed in revision 2.
- **Runtime context** — facts the runtime adds to the system prompt: agent name, local date and time with timezone, channel, and workspace.

## Scope

### Included

- Agent home layout and the files `AGENT.md` and `MEMORY.md`; the shared user file `$LANA_HOME/USER.md` and its migration (revision 4).
- System prompt assembly from those files plus runtime context, with size limits.
- `lana agent new`, `lana agent list`, `lana agent edit`, `lana agent show`.
- Built-in templates `assistant`, `worker`, `coder`.
- Tools `memory_read` and `memory_write` with actions `add`, `replace`, `remove`.
- No onboarding interview (revision 2): agents learn facts when the owner mentions them.
- Slash command `/memory` in the TUI.
- Compatibility with agents that only set `prompt_file`.

### Not included

- Skills, the scheduler/cron, and the Telegram channel. Follow-up specs for the "worker" role.
- Memory search over past sessions (FTS5) and semantic or vector memory.
- The agent changing its own persona file.
- Automatic migration of existing `prompt_file` content into `AGENT.md`.
- Per-agent `USER.md` files as a live source. Revision 4 stops reading them after the migration (`REQ-HOME-004`). The files stay on disk.
- Deleting or renaming agents from the CLI. The owner edits `config.toml` and the folder by hand.

## Assumptions

- `$LANA_HOME` defaults to `~/.lana` and is overridden by the env `LANA_HOME`. — Source: spec #1 contract.
- Agents are defined in `[agents.<name>]` in `config.toml`. — Source: ADR 0003.
- Tools run under the permission engine and every tool call is recorded in the session event log. — Source: ADR 0004, ADR 0009.

## Behavior rules

### Agent home

#### REQ-HOME-001: Agent home location

The system shall use `$LANA_HOME/agents/<name>/` as the agent home of agent `<name>`. A missing folder or
missing file is treated as an empty file, not as an error.

- `AC-HOME-001`: Given agent `assistant` with no folder `agents/assistant/` and no `$LANA_HOME/USER.md`, when a turn starts, then the turn runs and the system prompt contains only runtime context.

#### REQ-HOME-002: Persona source and `prompt_file` compatibility

The system shall read the persona from `AGENT.md` in the agent home. If the agent sets `prompt_file`, the
system shall use that file as the persona instead, following `Q-004`.

- `AC-HOME-002`: Given agent `default` with `prompt_file = "agents/default.md"` and no agent home, when a turn starts, then the system prompt contains the text of `agents/default.md`.
- `AC-HOME-003`: Given agent `assistant` with `agents/assistant/AGENT.md` and no `prompt_file`, when a turn starts, then the system prompt contains the text of `AGENT.md`.

#### REQ-HOME-003: Shared user file (rev 4)

The system shall read the user file from `$LANA_HOME/USER.md`. Every agent reads the same file. The system shall not read `agents/<name>/USER.md` after the migration of `REQ-HOME-004`.

- `AC-HOME-004`: Given agents `helper` and `worker` and a `USER.md` with the entry `Name: Ridho`, when each agent builds its system prompt, then both prompts contain `Name: Ridho` under the user heading.
- `AC-HOME-005`: Given `agents/helper/USER.md` holds `Name: Old` and `$LANA_HOME/USER.md` holds `Name: Ridho`, when a turn of `helper` starts, then the prompt contains `Name: Ridho` and does not contain `Name: Old`.

#### REQ-HOME-004: Migration of per-agent user files (rev 4)

The system shall run a migration each time a runtime opens (`lana chat`, `lana daemon`, `lana agent`, and the Go library). When `$LANA_HOME/USER.md` does not exist, the migration shall look at the agents in the effective config whose `agents/<name>/USER.md` has at least one entry. If exactly one agent is a candidate, it shall copy that file. If several agents are candidates and the default agent (`Q-009`) is one of them, it shall copy the default agent's file. Otherwise it shall copy nothing and log a warning. The migration shall never delete or change a per-agent file, and it shall do nothing once `$LANA_HOME/USER.md` exists.

- `AC-HOME-006`: Given `agents/helper/USER.md` with two entries and no `$LANA_HOME/USER.md`, when the owner runs `lana agent show helper`, then `$LANA_HOME/USER.md` exists with the same bytes and `agents/helper/USER.md` is unchanged.
- `AC-HOME-007`: Given candidates `helper` (the default agent) and `worker` and no shared file, when a runtime opens, then the default agent's `USER.md` is copied and the `worker` file is not.
- `AC-HOME-008`: Given candidates `helper` and `worker`, neither of them the default agent, and no shared file, when a runtime opens, then no file is created, a warning names both files, and the prompt has no user section.
- `AC-HOME-009`: Given `$LANA_HOME/USER.md` already exists, when a runtime opens twice, then the file is unchanged after both runs.
- `AC-HOME-010`: Given `agents/worker/USER.md` exists after the migration, when the owner runs `lana config check`, then a warning says that the file is not read and names `$LANA_HOME/USER.md`.

### System prompt

#### REQ-PROMPT-001: Assembly order

The system shall build the system prompt from these sections, in this order, skipping empty sections:
persona, user file, memory file, runtime context.

- `AC-PROMPT-001`: Given all three files have content, when `lana agent show <name> --prompt` runs, then the sections appear in the order persona, user, memory, runtime context, each under its own heading.

#### REQ-PROMPT-002: Files are read at the start of every turn

The system shall read the agent home files at the start of every turn, so changes by the owner or by the
memory tools apply from the next turn without restarting `lana`.

- `AC-PROMPT-002`: Given a running chat, when the owner edits `AGENT.md` and sends a new message, then the next turn uses the new persona.

#### REQ-PROMPT-003: Size limits

The system shall limit the user file to 2,000 characters and the memory file to 4,000 characters (`Q-003`). A file over its limit is
truncated in the prompt to the limit, with a marker line, and `lana config check` reports a warning.

- `AC-PROMPT-003`: Given `MEMORY.md` is larger than its limit after a manual edit, when a turn starts, then the prompt contains the memory file cut at the limit followed by the marker, and the turn runs.

#### REQ-PROMPT-004: Runtime context

The system shall add the agent name, the local date and time with timezone (source per `Q-006`), the
channel, and the workspace path as the last section.

- `AC-PROMPT-004`: Given the chat runs in `/tmp/x`, when a turn starts, then the runtime context contains `workspace: /tmp/x` and the current local date.

### Memory tools

#### REQ-MEM-001: `memory_read`

The system shall provide the tool `memory_read` that returns the entries of the user file or the memory
file of the calling agent.

- `AC-MEM-001`: Given `MEMORY.md` has two entries, when the agent calls `memory_read` with target `memory`, then the result lists both entries and the current size against the limit.

#### REQ-MEM-002: `memory_write`

The system shall provide the tool `memory_write` with action `add` (append an entry), `replace` (replace
the single entry that contains `old_text`), and `remove` (delete the single entry that contains `old_text`),
for target `user` or `memory`.

- `AC-MEM-002`: Given an empty `MEMORY.md`, when the agent calls `memory_write` with action `add` and content `Owner uses fish shell`, then `MEMORY.md` contains that entry and the tool result is success.
- `AC-MEM-003`: Given `old_text` matches two entries, when the agent calls `replace`, then nothing changes and the result is an error that asks for more specific `old_text`.
- `AC-MEM-004`: Given `old_text` matches no entry, when the agent calls `remove`, then nothing changes and the result is an error.

#### REQ-MEM-003: Limits on writes

The system shall reject a write that would make the file exceed its limit, without changing the file, and
the error shall state the current size and the limit so the agent can consolidate entries.

- `AC-MEM-005`: Given `MEMORY.md` is 50 characters under its limit, when the agent adds an entry of 100 characters, then the file is unchanged and the result is an error that states both numbers.

#### REQ-MEM-004: Write scope

The memory tools shall only read and write the shared user file and the memory file of the calling agent (`REQ-MEM-006`). They
shall never write the persona file or any file outside `$LANA_HOME`.

- `AC-MEM-006`: Given agent `worker`, when it calls `memory_write` with target `memory`, then only `agents/worker/MEMORY.md` changes.

#### REQ-MEM-005: Permission

The memory tools shall run under the permission engine without an approval request under policy `ask` or `trust`, and shall be rejected under policy `never` (`Q-002`). Deny rules still apply.

- `AC-MEM-007`: Given policy `ask`, when the agent calls `memory_write`, then no approval prompt appears and the write succeeds.
- `AC-MEM-008`: Given policy `never`, when the agent calls `memory_write`, then the result is `rejected` and the file is unchanged.

#### REQ-MEM-006: Target `user` uses the shared file (rev 4)

The memory tools shall use `$LANA_HOME/USER.md` for target `user`, for every agent. They shall use the calling agent's `MEMORY.md` for target `memory`.

- `AC-MEM-009`: Given agent `worker` with no `agents/worker/USER.md`, when it calls `memory_write` with target `user` and action `add`, then `$LANA_HOME/USER.md` contains the entry, `agents/worker/USER.md` is not created, and the result is success.

### CLI

#### REQ-AGCLI-001: `lana agent new <name>`

The system shall create a new agent: validate the name, ask for the template, model, and permission
(defaults from the template), create the agent home from the template, and add `[agents.<name>]` to
`config.toml`. With `--yes` it shall use the template defaults without asking.

- `AC-AGCLI-001`: Given no agent `helper`, when the owner runs `lana agent new helper --template assistant --yes`, then `agents/helper/AGENT.md` exists, `config.toml` has `[agents.helper]` with the template tools, and the exit code is 0.
- `AC-AGCLI-002`: Given agent `helper` already exists, when the owner runs `lana agent new helper`, then nothing changes and the exit code is 2.
- `AC-AGCLI-003`: Given the name `Helper Bot`, when the owner runs `lana agent new "Helper Bot"`, then nothing changes and the exit code is 2 with a message that names the allowed characters.

#### REQ-AGCLI-002: `lana agent list`

The system shall list every agent in the effective config with its model, permission, persona source, and
the size of the shared user file and its memory file against the limits.

- `AC-AGCLI-004`: Given agents `default` and `helper`, when the owner runs `lana agent list`, then both appear, and `default` shows its `prompt_file` as the persona source.

#### REQ-AGCLI-003: `lana agent edit <name> [persona|user|memory]`

The system shall open the chosen file (default `persona`) in `$VISUAL`, then `$EDITOR`, then `vi`,
creating the agent home and the file if needed. The `user` choice opens `$LANA_HOME/USER.md` (rev 4).

- `AC-AGCLI-005`: Given `EDITOR=true`, when the owner runs `lana agent edit helper memory`, then `agents/helper/MEMORY.md` exists and the exit code is 0.

#### REQ-AGCLI-004: `lana agent show <name> [--prompt]`

The system shall print the agent configuration and file paths, and with `--prompt` the full system prompt
that the next turn would use.

- `AC-AGCLI-006`: Given agent `helper`, when the owner runs `lana agent show helper --prompt`, then stdout contains the assembled prompt from `REQ-PROMPT-001` and the exit code is 0.

### Templates

#### REQ-TPL-001: Built-in templates

The system shall provide the templates `assistant`, `worker`, and `coder`, each with a persona text,
a tool list, and a permission, as listed in `Q-005`. All templates get every built-in tool and permission `ask`;
they differ only by persona text (revision 3).

- `AC-TPL-001`: Given `--template worker --yes`, when `lana agent new w1` runs, then `[agents.w1]` has the tools and permission of the `worker` template.

### Onboarding

#### REQ-ONB-001: Removed in revision 2

The system shall not add onboarding instructions to the prompt. Agents answer normally from the first
message and save facts about the owner only when the owner mentions them.

- `AC-ONB-001`: Given an empty `USER.md`, when `lana agent show helper --prompt` runs, then the prompt contains no onboarding or interview instructions.
- `AC-ONB-002`: Withdrawn in revision 2.

#### REQ-ONB-002: Wizard hands over to chat

After `lana agent new` succeeds in a TTY, the system shall offer to start `lana chat --agent <name>`
right away. With `--yes` or without a TTY it shall only print the command.

- `AC-ONB-003`: Given `--yes`, when `lana agent new helper --yes` finishes, then stdout contains `lana chat --agent helper` and no chat starts.

### TUI

#### REQ-TUI-001: `/memory`

The TUI shall provide `/memory`, which prints the entries of the user file and the memory file of the
active agent with their sizes against the limits.

- `AC-TUI-001`: Given `MEMORY.md` has one entry, when the owner types `/memory`, then the entry is shown with the size line.

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Invalid agent name | No change; message names the rule | exit 2 |
| Agent already exists | No change | exit 2 |
| Unknown template | No change; message lists templates | exit 2 |
| `config.toml` cannot be written | No folder is left half-created | exit 1 |
| Memory file unreadable at turn start | Turn runs without that section; warning in the log | — |
| `memory_write` over limit, ambiguous or missing `old_text` | File unchanged; tool result `error` | — |
| Migration cannot copy `USER.md` to `$LANA_HOME` | Nothing copied; `slog.Warn`; runtime starts | — |
| Several candidate `USER.md` files and no default agent among them | Nothing copied; warning names the files; no user section in the prompt | — |

## Authorization and security

- Only the owner's OS user runs `lana`; files are created with mode `0600` and folders `0700`.
- The memory tools resolve paths from the agent name in the tool context, never from model input.
- The persona file is never writable through tools.
- Memory content is part of the system prompt; the owner can inspect it with `/memory`, `lana agent show`, or the files.

## Data integrity and concurrency

- The shared `USER.md` is written under its own file lock, so writes from different agents serialize and none is lost.
- Two sessions of the same agent may write memory at the same time. Each write reads the current file,
  applies one action, and replaces the file atomically under a file lock, so no write is lost.
- A turn uses the files as read at turn start; writes during the turn apply from the next turn.

## Accessibility

- The wizard works with keyboard only and has a non-interactive `--yes` mode.
- Sizes and warnings are shown as text, not only as color.

## Scenarios

### SC-001: Create a personal assistant

**Given** a configured provider
**When** the owner runs `lana agent new lana --template assistant`, accepts the defaults, and starts the chat
**Then** the agent answers the first message normally; when the owner mentions their name, the agent saves it to the shared `USER.md`, and the next session already knows it.

### SC-002: The agent remembers a fact

**Given** agent `helper` that already knows the owner
**When** the owner says "remember that my server is called kenari"
**Then** the agent calls `memory_write` add on target `memory`, and a new session can answer "what is my server called?" without asking.

## Blocking Open Questions

| ID | Question | Controls | Agent recommendation | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `Q-001` | Is `USER.md` shared by all agents or one per agent? | `REQ-HOME-001`, `REQ-MEM-004` | Shared `$LANA_HOME/USER.md`, one `MEMORY.md` per agent | Ridho | Superseded by `Q-008` (rev 4) | Revisions 1 to 3: one `USER.md` per agent, in the agent home. Revision 4 makes it shared (`REQ-HOME-003`) |
| `Q-002` | Do memory tools need approval under policy `ask`? | `REQ-MEM-005` | No approval under `ask` and `trust`; rejected under `never` | Ridho | Resolved | Agent recommendation accepted: no approval under `ask` and `trust`; rejected under `never` |
| `Q-003` | Size limits for the user file and the memory file | `REQ-PROMPT-003`, `REQ-MEM-003` | `USER.md` 2,000 characters, `MEMORY.md` 4,000 characters | Ridho | Resolved | Agent recommendation accepted: `USER.md` 2,000 and `MEMORY.md` 4,000 characters (Unicode code points) |
| `Q-004` | If both `prompt_file` and `AGENT.md` exist, which wins? | `REQ-HOME-002` | `prompt_file` wins (explicit config); `lana agent list` shows the source | Ridho | Resolved | Agent recommendation accepted |
| `Q-005` | Tools and permission per template | `REQ-TPL-001` | All templates: every built-in tool (`read_file`, `write_file`, `edit_file`, `glob`, `grep`, `bash`, `web_fetch`, `memory_read`, `memory_write`, `skill_write`), `ask`. Behavior differs only by persona | Ridho | Resolved | Revision 3, owner decision 2026-10-30 (was: per-template subsets) |
| `Q-006` | Timezone source for runtime context | `REQ-PROMPT-004` | The system local timezone; `USER.md` may state another, which the model reads as text | Ridho | Resolved | Agent recommendation accepted |
| `Q-007` | Should `lana agent new` offer to make the new agent the default for `lana chat` (the `cli` route)? | `REQ-AGCLI-001` | Yes, ask in the wizard, default No; `--yes` never changes the route | Ridho | Resolved | Agent recommendation accepted |
| `Q-008` | Which per-agent `USER.md` is copied to the shared file when the migration runs? | `REQ-HOME-004` | If exactly one agent has entries in its `USER.md`, copy it. If several do and the default agent is one of them, copy the default agent's file. Otherwise copy nothing and warn; the owner merges by hand. Originals are kept | Ridho | Resolved | Rekomendasi agent: accepted under the gate lifted 2026-10-30 (spec #9 rev 2, decision 9) |
| `Q-009` | When does the migration run, and which agent is the default? | `REQ-HOME-004`, `REQ-MEM-006` | Each time a runtime opens; it is idempotent. The default agent is the agent of the first `[[routes]]` entry with `channel = "cli"` (`Q-007`). With no such route there is no default, and `Q-008` applies without the default rule | Ridho | Resolved | Rekomendasi agent: accepted under the gate lifted 2026-10-30 |

All questions are resolved. Blocking Open Questions: None.

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-HOME-001` | `AC-HOME-001` | Owner request, Hermes/OpenClaw |
| `REQ-HOME-002` | `AC-HOME-002`, `AC-HOME-003` | ADR 0003, compatibility |
| `REQ-HOME-003` | `AC-HOME-004`, `AC-HOME-005` | Owner decision 2026-10-30 |
| `REQ-HOME-004` | `AC-HOME-006` through `AC-HOME-010` | Owner decision 2026-10-30, `Q-008`, `Q-009` |
| `REQ-PROMPT-001` | `AC-PROMPT-001` | Hermes/OpenClaw |
| `REQ-PROMPT-002` | `AC-PROMPT-002` | Owner request |
| `REQ-PROMPT-003` | `AC-PROMPT-003` | Hermes bounded memory |
| `REQ-PROMPT-004` | `AC-PROMPT-004` | Owner request |
| `REQ-MEM-001` | `AC-MEM-001` | Owner decision |
| `REQ-MEM-002` | `AC-MEM-002`, `AC-MEM-003`, `AC-MEM-004` | Owner decision, Hermes |
| `REQ-MEM-003` | `AC-MEM-005` | Hermes bounded memory |
| `REQ-MEM-004` | `AC-MEM-006` | ADR 0004 |
| `REQ-MEM-005` | `AC-MEM-007`, `AC-MEM-008` | ADR 0004 |
| `REQ-MEM-006` | `AC-MEM-009` | Owner decision 2026-10-30 |
| `REQ-AGCLI-001` | `AC-AGCLI-001`, `AC-AGCLI-002`, `AC-AGCLI-003` | Owner decision |
| `REQ-AGCLI-002` | `AC-AGCLI-004` | Owner request |
| `REQ-AGCLI-003` | `AC-AGCLI-005` | Owner request |
| `REQ-AGCLI-004` | `AC-AGCLI-006` | Owner request |
| `REQ-TPL-001` | `AC-TPL-001` | Owner request |
| `REQ-ONB-001` | `AC-ONB-001`, `AC-ONB-002` | Owner decision, OpenClaw |
| `REQ-ONB-002` | `AC-ONB-003` | Owner decision |
| `REQ-TUI-001` | `AC-TUI-001` | Owner request |

## Change log

| Revision | Date | Change |
|---|---|---|
| 1 | 2026-10-29 | First approved version (interview answers `Q-001` to `Q-007`) |
| 2 | 2026-10-30 | Onboarding interview removed (`REQ-ONB-001`) |
| 3 | 2026-10-30 | All templates get every built-in tool and differ only by persona (`REQ-TPL-001`, `Q-005`) |
| 4 | 2026-10-30 | `USER.md` shared at `$LANA_HOME/USER.md`. Added `REQ-HOME-003`, `REQ-HOME-004`, `REQ-MEM-006`, `Q-008`, `Q-009`. Changed `AC-HOME-001`, `REQ-MEM-004`, `REQ-AGCLI-002`, `REQ-AGCLI-003`. `Q-001` superseded |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit or stated as not applicable.
- [x] State transitions and destructive effects are explicit or not applicable.
- [x] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: Ridho (interview answers 2026-10-29); revision 4 by Ridho, lifted gate 2026-10-30 (agent recommendations `Q-008`, `Q-009`)
- **Approved on**: 2026-10-29 (revisions 1 to 3); 2026-10-30 (revision 4)
