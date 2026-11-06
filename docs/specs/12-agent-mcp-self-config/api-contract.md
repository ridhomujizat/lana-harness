# Contract: Agent MCP self-configuration and workspace repair

- **Issue**: —
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 1
- **Applies to**: tool schema, risk class, config, tool output text
- **Status**: Approved

## Shared conventions

| Topic | Decision or authority |
|---|---|
| Config format | `$LANA_HOME/config.toml`, spec #7 `[mcp.<name>]`, spec #9 write rules |
| Secret references | `${NAME}`, resolved by `config` from the environment and `secrets.env` |
| Tool output | plain text; `ToolStatusError` for failures, `ToolStatusRejected` from the permission engine |
| Audit | `webfiles.AppendAudit`, kinds as in spec #9 |

## Operation index

| Operation ID | Type | Command/method | Path/flag/topic | Requirements | Acceptance criteria |
|---|---|---|---|---|---|
| `API-AMCP-001` | Tool | `mcp_manage` | `action` | `REQ-AMCP-001` through `REQ-AMCP-005`, `REQ-AMCP-007` | `AC-AMCP-001` through `AC-AMCP-011`, `AC-AMCP-016` |
| `API-AMCP-002` | Risk | `admin` | permission engine | `REQ-AMCP-006` | `AC-AMCP-012` through `AC-AMCP-015` |
| `API-AMCP-003` | Config | `config.toml`, audit | `[mcp.<name>]`, `agents.<agent>.mcp` | `REQ-AMCP-008` | `AC-AMCP-017`, `AC-AMCP-018` |
| `API-WS-001` | Tool output | `bash`, `glob`, `grep` | workspace | `REQ-WS-001`, `REQ-WS-002` | `AC-WS-001`, `AC-WS-002` |

## API-AMCP-001: `mcp_manage`

- **Goal**: let the calling agent list, add, attach, and remove its MCP servers.
- **Stability**: New
- **Authorization**: `list` risk `read`; `add`, `remove` risk `admin`.

### Spec

- **Name**: `mcp_manage`
- **Description**: `Lists, adds, or removes MCP servers for you. add and remove need the owner's approval every time. Put secrets as ${NAME} references; the owner sets the values in Settings → Secrets. New tools are available from the next turn.`

### Input

| Name | Type | Required | Validation and meaning |
|---|---|---:|---|
| `action` | string | Yes | `list`, `add`, or `remove` |
| `name` | string | for `add`, `remove` | `^[a-z0-9-]+$`, 1–64 characters |
| `command` | string | No | stdio command. Not with `url`. Max 1024 characters, one line |
| `args` | string[] | No | only with `command`; max 64 items, each one line |
| `url` | string | No | `http` or `https` URL. Not with `command` |
| `env` | object string→string | No | only with `command`; keys `^[A-Za-z_][A-Za-z0-9_]*$`; max 32 |
| `headers` | object string→string | No | only with `url`; max 32 |

Unknown fields fail with `invalid input: unknown field <name>`.

### Success

| Condition | Output |
|---|---|
| `list`, no servers | `no MCP servers configured` |
| `list` | one line per server, sorted by name: `<name>  stdio\|http  <target>  attached\|available  <state>  env=<k1,k2\|->  headers=<k1\|->`. `<target>` is `command args...` or `url` |
| `add`, new or changed definition | `added mcp server <name> for agent <agent> (<n> tools). Its tools are available from the next turn.` |
| `add`, attach only | `attached mcp server <name> to agent <agent>. Its tools are available from the next turn.` |
| `add`, already attached and identical | `mcp server <name> is already attached to agent <agent>` (nothing written) |
| `remove` | `removed mcp server <name> from agent <agent>` plus `; deleted [mcp.<name>]` when the table was deleted |

### Failure

All are `ToolStatusError` with these prefixes:

| Condition | Output |
|---|---|
| Bad input | `invalid input: <reason>` |
| No home | `mcp_manage is not available: no home folder` |
| No admin service | `mcp_manage is not available in this runtime` |
| No `config.toml` | `config.toml does not exist` |
| `add` without definition, server unknown | `mcp server <name> is not configured; give command or url` |
| Conflict | `conflict: mcp server <name> is used by <agent1,agent2>` |
| Secret missing | `secret <NAME> is not set; ask the owner to add it in Settings → Secrets` |
| Test fails | `connection test failed: <redacted reason>` |
| Not attached on `remove` | `mcp server <name> is not attached to agent <agent>` |
| Invalid candidate | `config.toml is not valid: <issues>` |
| Write fails | `failed to write config.toml: <error>` |

### Side effects and guarantees

- **State transition**: config and in-memory MCP state change only on success.
- **Idempotency**: repeating an identical `add` writes nothing.
- **Concurrency**: serialized with web config writes by `webfiles.ConfigWriteMu`.
- **Data exposure**: never prints env or header values, resolved or raw.

## API-AMCP-002: Risk `admin`

- `core.RiskAdmin = "admin"`.
- Order in the permission engine: deny rules → child session reject (`child sessions may not use admin tools`) → policy `never` reject (`policy never`) → owner approval. Allow rules, grants, and `trust` are skipped. `allowed_session` counts as allowed once and creates no grant for `admin` calls.
- No approval service: reject `approval is not available`.

## API-AMCP-003: Config effects

- `add` writes:

  ```toml
  [mcp.<name>]
  command = "npx"
  args = ["-y", "@modelcontextprotocol/server-filesystem", "/Users/ridho/notes"]
  env = { TOKEN = "${TOKEN}" }
  ```

  in the same format as `PUT /v1/mcp/{name}`, and sets `mcp = ["...", "<name>"]` in `[agents.<agent>]`, keeping the existing order and appending the new name.
- `remove` rewrites the agent's `mcp` list without `<name>` and deletes `[mcp.<name>]` when no other agent lists it.
- Audit kinds: `mcp.save` (target `<name>`), `mcp.delete` (target `<name>`), `agent.save` (target `<agent>`).

## API-WS-001: Workspace errors

| Condition | Tool | Output (`ToolStatusError`) |
|---|---|---|
| Workspace path missing | `bash`, `glob`, `grep` | `workspace does not exist: <absolute path>` |
| Workspace path is a file, or a parent is a file | `bash`, `glob`, `grep` | `workspace is not a directory: <absolute path>` |
| Any other stat error (for example permission denied) | `bash`, `glob`, `grep` | `workspace is not accessible: <absolute path>: <error>` |

## Contract traceability

| Operation | Requirements | Acceptance criteria | Design component | Verification |
|---|---|---|---|---|
| `API-AMCP-001` | `REQ-AMCP-001` through `REQ-AMCP-005`, `REQ-AMCP-007` | `AC-AMCP-001` through `AC-AMCP-011`, `AC-AMCP-016` | `tools.mcpManageTool`, `agent.Registry`, `mcp.Manager` | `tools`, `agent`, `mcp` tests |
| `API-AMCP-002` | `REQ-AMCP-006` | `AC-AMCP-012` through `AC-AMCP-015` | permission engine | `tools/permission` tests |
| `API-AMCP-003` | `REQ-AMCP-008` | `AC-AMCP-017`, `AC-AMCP-018` | `internal/mcpconfig`, `internal/webfiles` | unit tests |
| `API-WS-001` | `REQ-WS-001`, `REQ-WS-002` | `AC-WS-001`, `AC-WS-002` | `agent/turn.go`, `tools/guard.go` | `agent`, `tools` tests |

## Blocking Contract Questions

None.

## Approval

- [x] Requirements are Approved and their revision is recorded above.
- [x] Contract applicability has been determined.
- [x] Every operation has a stable ID and is linked to requirements and acceptance criteria.
- [x] Input, success, failure, authorization, and compatibility are explicit.
- [x] No behavior exists only in this file.
- [x] Blocking Contract Questions is `None`.
- **Approved by**: Ridho
- **Approved on**: 2026-10-30
