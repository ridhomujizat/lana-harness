# Contract: Agent home, memory, and onboarding

- **Issue**: —
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 1
- **Applies to**: CLI (`lana agent`), files under `$LANA_HOME/agents/`, `config.toml` writes, tools `memory_read` and `memory_write`, slash command `/memory`, system prompt layout
- **Status**: Approved

Shared conventions (exit codes, stdout/stderr, `LANA_HOME`, color, `error: ` prefix) follow the
[spec #1 contract](../1-cli-tui/api-contract.md#shared-conventions).

## Operation index

| ID | Operation | Requirements |
|---|---|---|
| `API-HOME-001` | Agent home layout and file format | `REQ-HOME-001`, `REQ-HOME-002`, `REQ-MEM-004` |
| `API-HOME-002` | System prompt layout | `REQ-PROMPT-001` through `REQ-PROMPT-004`, `REQ-ONB-001` |
| `API-AGENT-001` | `lana agent new` | `REQ-AGCLI-001`, `REQ-TPL-001`, `REQ-ONB-002` |
| `API-AGENT-002` | `lana agent list` | `REQ-AGCLI-002` |
| `API-AGENT-003` | `lana agent edit` | `REQ-AGCLI-003` |
| `API-AGENT-004` | `lana agent show` | `REQ-AGCLI-004` |
| `API-MEM-001` | Tool `memory_read` | `REQ-MEM-001`, `REQ-MEM-005` |
| `API-MEM-002` | Tool `memory_write` | `REQ-MEM-002` through `REQ-MEM-005` |
| `API-TUI-001` | Slash command `/memory` | `REQ-TUI-001` |

## API-HOME-001: Agent home layout and file format

```text
$LANA_HOME/agents/<name>/
  AGENT.md     persona (owner only)
  USER.md      user file, limit 2,000 characters
  MEMORY.md    memory file, limit 4,000 characters
```

| Topic | Decision |
|---|---|
| Agent name | `^[a-z][a-z0-9_-]{0,31}$` |
| Folder mode | `0700` |
| File mode | `0600` |
| Encoding | UTF-8. Size is counted in Unicode code points after trimming leading and trailing whitespace |
| Entry separator | A line containing only `§` (with optional surrounding whitespace). Empty entries are ignored |
| Persona source | `prompt_file` when set in `[agents.<name>]`; otherwise `AGENT.md` |
| Missing folder or file | Treated as empty |

Example `MEMORY.md`:

```markdown
Server "kenari" runs Debian 13 and hosts the home lab.
§
Owner prefers short answers with commands first.
```

## API-HOME-002: System prompt layout

The system prompt is the non-empty sections below, in order, separated by one blank line. Headings are fixed.

```markdown
<persona text, unchanged>

# About the user
<USER.md entries joined by "\n§\n">

# Your memory
<MEMORY.md entries joined by "\n§\n">

# Onboarding
<fixed onboarding text; only while USER.md has no entries>

# Runtime context
- agent: <name>
- date: <YYYY-MM-DD Weekday HH:MM> <timezone abbreviation> (UTC<±HH:MM>)
- channel: <cli|library|...>
- workspace: <absolute path>
```

| Topic | Decision |
|---|---|
| Truncation | A section over its limit is cut to the limit and followed by the line `[truncated: <n>/<limit> characters; consolidate with memory_write]` |
| Read time | Once per turn, before the first step. The same text is used for every step of the turn |
| Timezone | Process local timezone (`TZ` env respected) |
| Date precision | Minutes |

## API-AGENT-001: `lana agent new`

```text
lana agent new <name> [--template assistant|worker|coder] [--model <provider:model>] [--yes]
```

| Flag | Default | Meaning |
|---|---|---|
| `--template` | wizard asks; `assistant` with `--yes` | Template to copy |
| `--model` | wizard asks; the model of the first route `channel = "cli"` agent with `--yes` | Model for the agent |
| `--yes` | off | No questions; use defaults; never changes routes; never starts chat |

Wizard questions (TTY only), in order:

1. `Template [assistant/worker/coder]` (default `assistant`)
2. `Model` (default as above; must be `<provider>:<model>` with a configured provider)
3. `Permission [ask/trust/never]` (default from template)
4. `Use <name> as the default agent for lana chat? [y/N]`
5. `Start chat with <name> now? [Y/n]`

Files written:

- `$LANA_HOME/agents/<name>/AGENT.md` from the template, with `{{name}}` replaced by the agent name.
- Empty `USER.md` and `MEMORY.md`.
- `[agents.<name>]` appended to `$LANA_HOME/config.toml` with `provider`, `model`, `tools`, `permission`. No `prompt_file`.
- With answer `y` to question 4: the `agent` value of the first `[[routes]]` entry with `channel = "cli"` is set to `<name>`; if none exists one is appended.

Templates:

| Template | Tools | Permission |
|---|---|---|
| `assistant` | `web_fetch`, `read_file`, `glob`, `grep`, `memory_read`, `memory_write` | `ask` |
| `worker` | `read_file`, `write_file`, `edit_file`, `glob`, `grep`, `bash`, `web_fetch`, `memory_read`, `memory_write` | `ask` |
| `coder` | `read_file`, `write_file`, `edit_file`, `glob`, `grep`, `bash`, `memory_read`, `memory_write` | `ask` |

Output on success (stdout):

```text
created agent <name>
  home:   <path>
  model:  <provider:model>
  tools:  <comma list>
next: lana chat --agent <name>
```

| Condition | stderr | Exit |
|---|---|---|
| Invalid name | `error: invalid agent name "<name>": use a-z, 0-9, - and _, start with a letter, max 32 characters` | 2 |
| Agent exists in config or folder exists | `error: agent "<name>" already exists` | 2 |
| Unknown template | `error: unknown template "<t>" (available: assistant, coder, worker)` | 2 |
| Invalid model or unknown provider | `error: invalid model "<m>": ...` | 2 |
| Write failure | `error: ...`; the created folder is removed | 1 |

## API-AGENT-002: `lana agent list`

```text
lana agent list
```

One line per agent, sorted by name, on stdout:

```text
<name>  <provider:model>  <permission>  persona=<AGENT.md|prompt_file:<path>|none>  user=<n>/2000  memory=<n>/4000
```

A size over its limit is followed by ` (over limit)`. Exit 0.

## API-AGENT-003: `lana agent edit`

```text
lana agent edit <name> [persona|user|memory]
```

Default `persona`. When the persona source is `prompt_file`, `persona` opens that file. Editor: `$VISUAL`, then `$EDITOR`, then `vi`.
Unknown agent: `error: unknown agent "<name>"`, exit 2. Editor exit code other than 0: exit 1.

## API-AGENT-004: `lana agent show`

```text
lana agent show <name> [--prompt]
```

Without `--prompt`: name, provider, model, permission, tools, persona source, home path, and the two size lines.
With `--prompt`: only the assembled system prompt (`API-HOME-002`) with channel `cli` and the current working directory as workspace. Exit 0, or exit 2 for an unknown agent.

## API-MEM-001: Tool `memory_read`

Risk class: `read`.

```json
{
  "name": "memory_read",
  "description": "Read your memory entries. target=user for facts about the user, target=memory for your own notes.",
  "input_schema": {
    "type": "object",
    "properties": {"target": {"type": "string", "enum": ["user", "memory"]}},
    "required": ["target"]
  }
}
```

Output (success):

```text
<target> <n>/<limit> characters, <k> entries
1. <entry>
2. <entry>
```

Empty file: `<target> 0/<limit> characters, 0 entries`.

## API-MEM-002: Tool `memory_write`

Risk class: `memory` (new). Permission: allowed under `ask` and `trust` without approval, rejected under `never`; deny rules apply. In profiles without the `permission` module, the default fail-closed gate allows risk `memory` like `read`.

```json
{
  "name": "memory_write",
  "description": "Save durable facts. target=user for facts about the user, target=memory for your own notes. action=add appends content; replace and remove need old_text that matches exactly one entry. Keep entries short; consolidate when near the limit.",
  "input_schema": {
    "type": "object",
    "properties": {
      "target": {"type": "string", "enum": ["user", "memory"]},
      "action": {"type": "string", "enum": ["add", "replace", "remove"]},
      "content": {"type": "string"},
      "old_text": {"type": "string"}
    },
    "required": ["target", "action"]
  }
}
```

| Case | Status | Output |
|---|---|---|
| `add` | `success` | `added to <target> (<n>/<limit> characters)` |
| `replace` | `success` | `replaced 1 entry in <target> (<n>/<limit> characters)` |
| `remove` | `success` | `removed 1 entry from <target> (<n>/<limit> characters)` |
| Over limit | `error` | `<target> would be <n>/<limit> characters; remove or merge entries first` |
| `old_text` matches no entry | `error` | `no entry in <target> contains old_text` |
| `old_text` matches more than one | `error` | `old_text matches <k> entries in <target>; use more specific text` |
| Missing `content` for add/replace, missing `old_text` for replace/remove, empty content | `error` | `invalid input: <field> is required` |
| Content contains a `§` line | `error` | `invalid input: content must not contain a § line` |
| `policy never` | `rejected` | standard permission reason |

## API-TUI-001: Slash command `/memory`

`/memory` prints, in the scrollback:

```text
· user 312/2000 characters, 3 entries
  1. <entry>
  ...
· memory 0/4000 characters, 0 entries
```

It is listed in `/help` and in slash completion with the description `show this agent's memory`.

## Compatibility

- Agents that set only `prompt_file` behave as before, plus the runtime context section and any user or memory files that exist.
- New risk class `memory` appears in `tool/call` event payloads and in approval requests. Older readers treat it as an unknown string.
- Config validation accepts the new tool names `memory_read` and `memory_write` in `tools`.
