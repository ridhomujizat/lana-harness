# Contract: Foundation (workspace, profile loader, Context/module/hook)

- **Issue**: [`#2`](https://github.com/ridhomujizat/lana-harness/issues/2)
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 4
- **Requirements status**: Approved
- **Contract revision**: 2 (cross-spec reconciliation #2 through #4, see `CQ-016` through `CQ-032`)
- **Applies to**: config and profile file format (TOML), config error message format, public Go API of packages `config` and `core`. There are no CLI subcommands in this spec (CLI is in spec #1).
- **Status**: Approved

This file defines the exact form of the config format and the public Go API. Behavior is owned by `requirements.md`.

## Shared conventions

| Item | Decision |
|---|---|
| Go module | `github.com/ridhomujizat/lana-harness` (ADR 0013) |
| Go version | Directive `go 1.24` in `go.mod`. Toolchain from `flake.lock`, `GOTOOLCHAIN=local` |
| Build | `CGO_ENABLED=0`, no cgo |
| Encoding | UTF-8. TOML v1.0 |
| Key names | `snake_case` in TOML and JSON. Go identifier names follow Go rules |
| Time | Fields `created_at` and `updated_at` in Unix milliseconds (`int64`) |
| `LANA_HOME` | Default `~/.lana`. The env `LANA_HOME` overrides the location |
| Secret | Type `core.Secret`. Shows `***` in all output (`fmt`, `slog`, JSON, TOML) |
| Stability | Before `v1.0.0`, public API changes are recorded in the CHANGELOG (ADR 0014) |
| Public packages | `config` and `core`. Other packages in the module are not guaranteed stable |

## Operation index

| Operation ID | Kind | Subject | Requirements | Acceptance criteria |
|---|---|---|---|---|
| `API-CONF-001` | File | File location and layer order | `REQ-CONF-001`, `REQ-CONF-006` | `AC-CONF-001`, `AC-CONF-002`, `AC-CONF-015` |
| `API-CONF-002` | File | Top-level schema and `version` | `REQ-CONF-003`, `REQ-CONF-008` | `AC-CONF-009`, `AC-CONF-021` |
| `API-CONF-003` | File | `[providers.<name>]` | `REQ-CONF-003` | `AC-CONF-010`, `AC-CONF-023` |
| `API-CONF-004` | File | `[agents.<name>]` and `rules` | `REQ-CONF-002`, `REQ-CONF-003` | `AC-CONF-006`, `AC-CONF-016` |
| `API-CONF-005` | File | `[[routes]]` | `REQ-CONF-002`, `REQ-CONF-003` | `AC-CONF-007` |
| `API-CONF-006` | File | `[mcp.<name>]` | `REQ-CONF-003`, `REQ-CONF-007` | `AC-CONF-018`, `AC-CONF-020` |
| `API-CONF-007` | File | `[channels.<name>]` | `REQ-CONF-002` | No specific AC; general merge rule `AC-CONF-006` |
| `API-CONF-008` | File | `[[modules]]`, position, and `disabled` | `REQ-CONF-002`, `REQ-MOD-001` | `AC-CONF-003`, `AC-CONF-004`, `AC-CONF-005`, `AC-CONF-008` |
| `API-CONF-009` | File | Built-in profiles and profile files | `REQ-PROF-001`, `REQ-CONF-001` | `AC-PROF-001`, `AC-PROF-002`, `AC-CONF-002` |
| `API-CONF-010` | File | `secrets.env` and lookup order | `REQ-CONF-005` | `AC-CONF-012`, `AC-CONF-017` |
| `API-CONF-011` | File | Interpolation of `${NAME}` | `REQ-CONF-007` | `AC-CONF-018`, `AC-CONF-019`, `AC-CONF-020` |
| `API-CONF-012` | Output | Config error message format | `REQ-CONF-003` | `AC-CONF-009`, `AC-CONF-010`, `AC-CONF-016` |
| `API-CONF-013` | Output | Origin map (origins) | `REQ-CONF-004` | `AC-CONF-011` |
| `API-CONF-014` | Go | `config.Load` and supporting types | `REQ-CONF-001` through `REQ-CONF-009` | `AC-CONF-022`, `AC-CONF-023` and references above |
| `API-CORE-001` | Go | `Module`, `ModuleConfig`, `RegisterModule` | `REQ-MOD-001`, `REQ-MOD-004` | `AC-MOD-001`, `AC-MOD-007` |
| `API-CORE-002` | Go | `Builder`, `Boot`, `Shutdown` | `REQ-MOD-001` through `REQ-MOD-004`, `REQ-HOOK-004` | `AC-MOD-001` through `AC-MOD-006`, `AC-HOOK-009` |
| `API-CORE-003` | Go | `Context`, `ServiceKey` | `REQ-MOD-001`, `REQ-MOD-004` | `AC-MOD-007` |
| `API-CORE-004` | Go | Generic hooks `Waterfall`, `Serial`, `Observe` | `REQ-HOOK-001` through `REQ-HOOK-004` | `AC-HOOK-001` through `AC-HOOK-008`, `AC-HOOK-010` |
| `API-CORE-005` | Go | Named hooks and input-output types | `REQ-HOOK-001`, `REQ-HOOK-002`, `REQ-HOOK-003` | Derived from `AC-HOOK-*`; list of names per ADR 0002. I and O types in `API-CORE-015` |
| `API-CORE-006` | Go | Messages, `ChatRequest`, `ChatEvent`, `Provider` | Not applicable | Types for spec #3 |
| `API-CORE-007` | Go | `Tool`, `Risk`, `ToolRegistry` | Not applicable | Types for spec #3 |
| `API-CORE-008` | Go | `SessionEvent`, kind, and payload | Not applicable | Events from ADR 0009, types for spec #3 and #4 |
| `API-CORE-009` | Go | `SessionStore`, `SessionInfo`, `NewSession`, `SessionFilter` | Not applicable | Interface for spec #4 (with `Info`, `ChainEvents`, `DeriveMessages`) |
| `API-CORE-010` | Go | `AgentRegistry`, `LiveFrame` | Not applicable | Interface for spec #3 |
| `API-CORE-011` | Go | `ApprovalService`, `Policy`, `ApprovalOutcome` | Not applicable | Interface for spec #4 |
| `API-CORE-012` | Go | `Secret`, `Secrets` | `REQ-CONF-005` | `AC-CONF-012`, `AC-CONF-013`, `AC-CONF-014` |
| `API-CORE-013` | Go | `Config` schema types | `REQ-CONF-003` | `AC-CONF-009`, `AC-CONF-010` |
| `API-CORE-014` | Go | Sentinel errors and `LeaseHeldError` | `REQ-MOD-002`, `REQ-HOOK-001` | `AC-MOD-003`, `AC-MOD-004`, `AC-HOOK-005` |
| `API-CORE-015` | Go | List of combined `core` types (single source): hook I/O types, additional types from spec #3 and #4, `DeriveMessages`, `ResolvePrefix` | Not applicable | `Required by spec #N` column in the API-CORE-015 table |

---

## API-CONF-001: File location and layer order

- **Purpose**: defines the files that are read and the layer order that forms the effective config.
- **Stability**: New

| Order | Layer name (origin) | Source | If missing |
|---:|---|---|---|
| 1 | `base` | Built-in rows (`API-CONF-009`) | Always present |
| 2 | `profile:<name>` | Built-in profile (`API-CONF-009`) | Unknown name: error (`AC-PROF-002`) |
| 3 | `<LANA_HOME>/profiles/<name>.toml` | File | Skipped (`AC-CONF-002`) |
| 4 | `<LANA_HOME>/config.toml` | File | Skipped |
| 5 | Each `--patch <file>`, in order | File | Error: `patch file not found: <path>` |
| 6 | Each `Layers` entry, in order | Memory | Not applicable |

- The `LANA_HOME` path is used only if `Options.Home` is set (`API-CONF-014`).
- Relative paths in config (for example `prompt_file`) are resolved from `LANA_HOME` (`AC-CONF-015`).
- The layer name of a file for origins and errors is its absolute path. The layer name of a memory layer is `Layer.Name`.

## API-CONF-002: Top-level schema and `version`

Known top-level tables. Other keys are an error `unknown key`.

| Key | TOML type | Required | Meaning |
|---|---|---:|---|
| `version` | integer | No | Format version. Without the field, the version is assumed to be `1`. Versions > `1` are rejected (`AC-CONF-021`) |
| `providers` | table | No | See `API-CONF-003` |
| `agents` | table | No | See `API-CONF-004` |
| `routes` | array of tables | No | See `API-CONF-005` |
| `mcp` | table | No | See `API-CONF-006` |
| `channels` | table | No | See `API-CONF-007` |
| `modules` | array of tables | No | See `API-CONF-008` |

- `version` is only at the top level. A non-integer value is a type error.
- The `version` error names the file version and the supported version: `config version 2 is higher than the supported version (1)`.

## API-CONF-003: `[providers.<name>]`

```toml
[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
```

| Key | Type | Required | Values | Rules |
|---|---|---:|---|---|
| `kind` | string | Yes | `openai_compat`, `anthropic` | Enum. Other values are an error (`REQ-CONF-003`) |
| `base_url` | string | No | URL | Format is not validated in this spec |
| `api_key_env` | string | No | Env name | Name only. The value is read through `core.Secrets` (`API-CONF-010`) |
| `headers` | table string to string | No | Literal values | Not interpolated. Owner: spec #3 (`API-PROV-001`) |
| `timeout` | Go duration string | No | Example `300s` | Validated with `time.ParseDuration`. Default in spec #3 (`API-PROV-001`) |

- The existence of the env name in `api_key_env` is not checked at load time. The check at run time is in spec #1 (exit 3).
- The `providers` table is merged per key (`API-CONF-013`).

## API-CONF-004: `[agents.<name>]` and `rules`

```toml
[agents.coder]
provider = "anthropic"
model = "claude-sonnet-5-5"
fallback = ["openrouter:anthropic/claude-haiku-5-5"]
prompt_file = "agents/coder.md"
workspace = "~/coding"
tools = ["read_file", "write_file", "edit_file", "glob", "grep", "bash", "task"]
subagents = ["explorer"]
mcp = ["github"]
skills = ["git-workflow"]
permission = "ask"

[agents.coder.rules]
allow = ["bash(git status*)", "bash(go test*)"]
deny = ["bash(rm -rf*)", "bash(sudo*)"]
```

| Key | Type | Required | Validation in this spec |
|---|---|---:|---|
| `provider` | string | No | Must exist in `providers` (`REQ-CONF-003`) |
| `model` | string | No | String type |
| `fallback` | array of strings | No | Array of strings type. Contents in `provider:model` form are not checked in this spec |
| `prompt_file` | string | No | String type. Relative path from `LANA_HOME` |
| `workspace` | string | No | String type |
| `tools` | array of strings | No | Array of strings type. Tool names are checked by spec #3 |
| `subagents` | array of strings | No | Each value must exist in `agents` |
| `mcp` | array of strings | No | Each value must exist in `mcp` |
| `skills` | array of strings | No | Array of strings type |
| `permission` | string | No | `trust`, `ask`, or `never`. Default `ask` if not written |
| `max_steps` | integer | No | 1 to 200. Default 25 in spec #3 (`API-AGENT-007`) |
| `context_window` | integer | No | Greater than 0. Default 128000 in spec #4 (`API-PERM-002`) |
| `workspace` | string | No | String type. Owner: spec #3 |
| `rules.allow` | array of strings | No | Array of strings type. Contents are pattern-validated after spec #4 is done (`CQ-030`) |
| `rules.deny` | array of strings | No | Array of strings type. Same as `allow` |

- Other keys inside `agents.<name>` are an error `unknown key`, with a suggestion of the closest key.
- Patterns in `rules.allow` and `rules.deny` are validated with `internal/pattern.ParsePattern` (grammar from spec #4 `API-PERM-001`, `CQ-030`). That package is used by `config` and `tools/permission`. Until spec #4 is done, the values are only arrays of strings.
- The keys below are known to this spec. Content validation is owned by the owning spec:

| Key | Owner | Validation in spec #2 |
|---|---|---|
| `agents.<name>.max_steps` | Spec #3 (`API-AGENT-007`) | Integer 1 to 200 (`REQ-CONF-003`) |
| `agents.<name>.context_window` | Spec #4 (`API-PERM-002`) | Integer greater than 0 |
| `agents.<name>.permission` | Spec #4 (`API-PERM-002`) | Enum `trust`, `ask`, `never` |
| `agents.<name>.rules.allow`, `.deny` | Spec #4 (`API-PERM-002`) | Array of strings. Patterns in spec #4 after `CQ-030` |
| `agents.<name>.workspace` | Spec #3 (`API-AGENT-007`) | String type |
| `providers.<name>.headers` | Spec #3 (`API-PROV-001`) | Table string to string |
| `providers.<name>.timeout` | Spec #3 (`API-PROV-001`) | Go duration |
| `[[modules]]` `store`, `config.path` | Spec #4 (`API-STORE-001`) | Not empty |
| `[[routes]]`, `channels.*`, `module.config` | Spec #1, channel modules | Opaque (`CQ-012`) |
- The `model` and `provider` fields are not required at config level. The check at run time is in spec #3.

## API-CONF-005: `[[routes]]`

```toml
[[routes]]
channel = "telegram"
match = { chat_id = 123456789 }
agent = "assistant"

[[routes]]
channel = "cli"
agent = "coder"
```

| Key | Type | Required | Rules |
|---|---|---:|---|
| `channel` | string | Yes | Channel name. Not checked against a channel list in this spec |
| `agent` | string | Yes | Must exist in `agents` (`REQ-CONF-003`) |
| `match` | table | No | Opaque in this spec. Content validation is in the channel module |

- The order of `[[routes]]` is preserved. The first `cli` route is the default for `lana chat` (spec #1).
- The `routes` list from the last layer that writes it replaces the entire list (`AC-CONF-007`).

## API-CONF-006: `[mcp.<name>]`

```toml
[mcp.github]
command = "github-mcp-server"
args = ["stdio"]
env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }

[mcp.notion]
url = "https://mcp.notion.com/mcp"
headers = { Authorization = "Bearer ${NOTION_TOKEN}" }
```

| Key | Type | Required | Rules |
|---|---|---:|---|
| `command` | string | One of `command` or `url` | stdio server |
| `args` | array of strings | No | Not interpolated |
| `url` | string | One of `command` or `url` | Streamable HTTP server |
| `env` | table string | No | Values are interpolated (`API-CONF-011`). All values are of type `core.Secret` |
| `headers` | table string | No | Same as `env` |

- Having both `command` and `url` is a validation error.
- Having neither is a validation error.
- Every name in `agents.<name>.mcp` must exist in `mcp`.

## API-CONF-007: `[channels.<name>]`

- Each `[channels.<name>]` must be a table. Its contents are opaque in this spec; the channel module validates its keys.
- The table is merged recursively per key (`API-CONF-013`).

```toml
[channels.telegram]
token_env = "TELEGRAM_BOT_TOKEN"
```

## API-CONF-008: `[[modules]]`, position, and `disabled`

```toml
[[modules]]
id = "store"
module = "sqlite_store"
config = { path = "lana.db" }

[[modules]]
id = "channel.telegram"
module = "telegram"
after = "store"
config = { poll_timeout_secs = 30 }

[[modules]]
id = "scheduler"
disabled = true
```

| Key | Type | Required | Rules |
|---|---|---:|---|
| `id` | string | Yes | Not empty. Unique per layer. Duplicate is an error `REQ-CONF-003` |
| `module` | string | Yes for new rows | Must be a module name registered in the binary (`REQ-CONF-003`) |
| `disabled` | bool | No | Default `false`. `true` makes the row not installed (`AC-CONF-004`) |
| `after` | string | No | Another `id`. Must exist (`AC-CONF-008`) |
| `before` | string | No | Another `id`. Must exist |
| `config` | table | No | Opaque. Validated by the module at install time through `ModuleConfig.Decode` |

Row merge rules:

| Case | Result |
|---|---|
| `id` not yet present | Row is added. At the end, or right after `after`, or right before `before` |
| `id` already present | Entire old `config` is replaced (`AC-CONF-003`). Written `module` and `disabled` override the old values |
| `after` or `before` for an existing `id` | Ignored. The value is still validated as an `id` that must exist |
| `after` and `before` written together | Validation error |
| `disabled = true` | Row remains in the effective config with `disabled = true`, not installed |

- The effective config keeps all rows, including `disabled` ones, so `--dump-config` can show them.

## API-CONF-009: Built-in profiles and profile files

Built-in profiles are lists of `[[modules]]` rows compiled into the binary.

| Profile | Layer | Rows |
|---|---|---|
| `minimal` | `profile:minimal` | Empty. Only the `base` layer (`CQ-031`) |
| `headless` | `profile:headless` | `{ id = "runner", module = "cli_runner" }`. Base plus a one-shot runner (module owned by spec #1) |

Layer `base` (compiled into the binary). Used by `chat` and `run`:

| `id` | `module` | `config` | Implemented in |
|---|---|---|---|
| `llm` | `llm` | `{}` | Spec #3 (`API-PROV-001`) |
| `tools` | `tools` | `{}` | Spec #3 (`API-AGENT-009`) |
| `store` | `sqlite_store` | `{ path = "lana.db" }` | Spec #4 (`API-STORE-001`) |
| `agent` | `agent` | `{}` | Spec #3 (`API-AGENT-007`) |
| `permission` | `permission` | `{}` | Spec #4 (`API-PERM-003`) |
| `approval` | `approval` | `{}` | Spec #4 (`API-APPR-001`) |
| `compaction` | `compaction` | `{}` | Spec #4 (`API-COMP-002`) |

- The previously existing `prompt` row is removed. `prompt_file` is read by the `agent` module (spec #3).
- `store` uses `sqlite_store`. A library without `Home` uses `memstore` (spec #3 `API-LIB-001`). The `Store` option uses `provided_store`. See design `DQ-016`.
- The module names above are implemented in specs #3 and #4. Until then, the built-in config fails `module` validation as unknown (see `CQ-032`). Tests use fake modules with the same names.
- Profiles outside the built-in list (`daemon`, `acp`, `sdk`, `mcp`) are added by the spec that brings their modules.
- The file `<LANA_HOME>/profiles/<name>.toml` uses the same schema as `config.toml`.
- Unknown profile: error `unknown profile: <name> (available: <list, sorted>)`.

## API-CONF-010: `secrets.env` and lookup order

```text
# Put API keys here, or set them as env vars in the shell.
OPENAI_API_KEY=sk-...
```

| Rule | Value |
|---|---|
| Location | `<LANA_HOME>/secrets.env` |
| Format | `NAME=value` per line. Empty lines and lines starting with `#` are ignored |
| Quotes | Surrounding `"..."` or `'...'` in the value is removed |
| Other lines | Validation error with line number |
| Lookup order | Process env, then `secrets.env`. Process env wins (`AC-CONF-012`) |
| Empty value | Treated as not present, both in env and in the file |
| Permission | If the mode is not `0600`, a warning (`AC-CONF-017`) |

Permission warning message:

```text
secrets.env has permission 0644 (should be 0600); run: chmod 600 /Users/ridho/.lana/secrets.env
```

- The file is still read after the warning.
- The warning goes to `Result.Warnings` and is logged with `slog` at level `warn`.

## API-CONF-011: Interpolation of `${NAME}`

| Rule | Value |
|---|---|
| Applies to | Values of `mcp.<name>.env` and `mcp.<name>.headers` only |
| Name pattern | `${NAME}` where `NAME` starts with a letter or `_` and contains letters, digits, or `_` |
| Source | Process env, then `secrets.env` |
| Name not found | Error: `variable ${NAME} not found` with key `mcp.<name>.env.<KEY>` |
| Result | Of type `core.Secret` |
| In other fields | Plain text. `${HOME}` stays `${HOME}` |
| Empty `${}` or invalid name | Validation error |
| Escape | None. `${` is always treated as a variable in applicable locations |

## API-CONF-012: Config error message format

Every error is a `config.Error` (`API-CONF-014`). Format of `Error()`:

| Condition | Format |
|---|---|
| `File` and `Line` set, `Key` set | `<file>:<line>: <key>: <message>` |
| `File` and `Line` set, `Key` empty | `<file>:<line>: <message>` |
| `File` set, `Line` zero, `Key` set | `<file>: <key>: <message>` |
| `File` empty | `<key>: <message>`, or `<message>` if `Key` is empty |

Example (per `API-CLI-009` in spec #1):

```text
/Users/ridho/.lana/config.toml:12: agents.coder.provider: provider 'anthropik' is not registered (available: openai)
/Users/ridho/.lana/config.toml:4: agents.coder.permision: unknown key (did you mean: permission?)
unknown profile: xyz (available: headless, minimal)
```

Required validation messages (fixed text):

| Condition | Message |
|---|---|
| TOML syntax | TOML parser message, with line and column |
| Wrong type | `must be <type>, got <type>` |
| Unknown key | `unknown key` and, if available, `(did you mean: <key>?)` |
| Provider missing | `provider '<name>' is not registered (available: <list>)` |
| Route agent missing | `agent '<name>' is not registered (available: <list>)` |
| Subagent missing | `subagent '<name>' is not registered` |
| MCP missing | `mcp server '<name>' is not registered` |
| Unknown module | `module '<name>' is unknown (available: <list>)` |
| Duplicate `id` in one layer | `id '<id>' is already used in row <row>` |
| Invalid enum | `value '<value>' is invalid (options: <list>)` |
| `after` or `before` missing | `id '<id>' does not exist` |
| Version too high | `config version <n> is higher than the supported version (1)` |
| `${}` not found | `variable ${<NAME>} not found` |

- The `<list>` is sorted alphabetically. The key suggestion uses the single key with the closest edit distance ≤ 2 in the same table.
- All errors from all layers are returned at once (`AC-CONF-009`).

## API-CONF-013: Origin map (origins)

- Each of the following key path types is in the map:

| Key path type | Example | Value |
|---|---|---|
| Table | `providers.openai` | Name of the layer that wrote that table |
| Leaf key | `agents.x.model` | Name of the layer that last wrote it |
| Module row | `modules.store` | Name of the layer that wrote or changed the row |
| `routes` list | `routes` | Name of the layer that last wrote it |
| Key inside a secret map | `mcp.github.env.GITHUB_TOKEN` | Layer name |
| Version | `version` | Layer name |

- The origin value is the layer name (`API-CONF-001`).
- The origin map stores the value from the layer that last wrote it.

## API-CONF-014: Go API `config`

```go
package config

import (
	"context"

	"github.com/ridhomujizat/lana-harness/core"
)

// Options controls config reading. Empty fields use defaults.
type Options struct {
	Home    string                            // "" = no file from LANA_HOME
	Profile string                            // "" = "minimal"
	Patches []string                          // --patch file paths, in order
	Layers  []Layer                           // memory layers, after Patches
	Env     func(name string) (string, bool) // nil = os.LookupEnv
}

// Layer is a layer from memory. TOML or Value content, exactly one.
type Layer struct {
	Name  string // required; used as origin and error location
	TOML  []byte // TOML text
	Value any    // Go struct; marshaled to TOML and then processed the same as TOML
}

// Load builds the effective config. Validation errors are returned as Errors.
// Other errors (e.g. ctx canceled) are returned as-is.
func Load(ctx context.Context, opts Options) (*Result, error)

// Result is the valid result of Load.
type Result struct {
	Profile  string
	Home     string           // "" if Options.Home is empty
	Config   core.Config      // effective config
	Origins  Origins
	Layers   []LayerInfo      // all layers in order, including missing ones
	Warnings []Warning
	Secrets  core.Secrets
}

// LayerInfo describes one layer.
type LayerInfo struct {
	Name    string // "base", "profile:<name>", absolute path, or Layer.Name
	Path    string // "" for layers without a file
	Present bool
}

// Origins maps key paths to layer names (API-CONF-013).
type Origins map[string]string

// Of returns the layer name for a key path.
func (o Origins) Of(key string) (layer string, ok bool)

// Warning is a warning that does not fail Load.
type Warning struct {
	File string
	Msg  string
}

// Error is a single validation error (API-CONF-012).
type Error struct {
	File string
	Line int // 0 if unknown
	Col  int // 0 if unknown
	Key  string
	Msg  string
}

func (e Error) Error() string

// Errors is the collection of all errors from all layers, ordered by layer then line.
type Errors []Error

func (e Errors) Error() string // lines separated by "\n"

// HomeDir returns LANA_HOME or ~/.lana.
func HomeDir(env func(name string) (string, bool)) (string, error)

// ProfileNames returns the names of the built-in profiles, sorted alphabetically.
func ProfileNames() []string
```

Rules:

- `Load` does not write to stdout or stderr. Warnings are returned in `Result.Warnings`. The caller (spec #1) prints them to stderr.
- `Load` does not read files if `Home` is empty, except `Patches`.
- Missing patch: `Errors` contains `Error{Msg: "patch file not found: <path>"}`.

## API-CORE-001: `Module`, `ModuleConfig`, `RegisterModule`

```go
package core

// Module is a feature unit compiled into the binary.
type Module interface {
	ID() string                 // implementation name, same as the registered name
	Requires() []ServiceKey     // services that must be installed before Install
	Provides() []ServiceKey     // services installed by this module
	Install(b *Builder, cfg ModuleConfig) error
	Stop(ctx context.Context) error
}

// ModuleConfig is the content of one [[modules]] row after merge.
type ModuleConfig struct {
	ID     string         // row id, unique in the effective config
	Module string         // implementation name
	Values map[string]any // row config table
}

// Decode copies Values into the destination struct via TOML.
func (c ModuleConfig) Decode(into any) error

// RegisterModule registers a module factory. A duplicate name triggers a panic.
// Called from init() or before Boot.
func RegisterModule(name string, factory func() Module)
```

- The factory is called once per active row. The instance is used for one boot.
- `ID()` returns the implementation name. Error messages use the row `id` (`ModuleConfig.ID`).

## API-CORE-002: `Builder`, `Boot`, `Shutdown`

```go
// BootInput is the input to Boot.
type BootInput struct {
	Config  Config   // effective config from config.Load
	Secrets Secrets  // from config.Result.Secrets
	Profile string
}

// Boot creates a Context. No module is installed if the boot plan fails.
func Boot(ctx context.Context, in BootInput) (*Context, error)

// ShutdownTimeout is the total limit for Shutdown.
const ShutdownTimeout = 30 * time.Second

// Shutdown stops modules in the reverse order of install.
func (c *Context) Shutdown(ctx context.Context) error

// Builder is used by modules during Install. Not used after Boot completes.
type Builder struct{ /* not exported */ }

// Provide installs a service. Error if the key is already installed by another module.
func (b *Builder) Provide(key ServiceKey, svc any) error

// Lookup gets a service already installed by a previous module.
func (b *Builder) Lookup(key ServiceKey) (any, bool)

// Own registers a release function (from a Use hook). Called when the module stops.
func (b *Builder) Own(release func())

// Hooks returns the shared hooks.
func (b *Builder) Hooks() *Hooks

// Config returns a copy of the effective config (read-only).
func (b *Builder) Config() Config
```

Boot order:

1. Read active rows (`Disabled = false`). Create module instances via the factory.
2. Validate the plan: module names are known, the service of each `Requires` is supplied, no duplicate `Provides`, no cycle.
3. Topologically sort, stable by row order (`AC-MOD-001`, `AC-MOD-002`).
4. Install each module in sequence. If `Install` fails, modules already installed are stopped in reverse order, then Boot fails.
5. Build `Context`. Core service fields are filled from `Provide`.

Boot errors (fixed text, naming the module and service):

| Condition | Message |
|---|---|
| Unknown module | `module '<name>' (row <id>) is unknown (available: <list>)` |
| Service not installed | `module <id> requires service '<key>' which is not installed by any active module` |
| Duplicate service | `service '<key>' is installed by more than one module: <id1>, <id2>` |
| Cycle | `dependency cycle: <id1> → <id2> → <id1>` |
| `Install` fails | `module <id> failed to install: <err>` |

Shutdown:

- Modules are `Stop`ped in the reverse order of install.
- Total limit = `ShutdownTimeout` or the remaining `ctx` deadline, whichever is smaller.
- Modules not finished when the limit runs out are logged as an `slog` warning and abandoned.
- The next modules are still called `Stop`, with an already-expired `ctx`. That call is not waited on.
- After all modules have stopped, hook listeners still registered are released and `Observe` is closed.

## API-CORE-003: `Context` and `ServiceKey`

```go
// ServiceKey is the name of a service in the Builder registry.
type ServiceKey string

const (
	ServiceLLM      ServiceKey = "llm"
	ServiceTools    ServiceKey = "tools"
	ServiceSessions ServiceKey = "sessions"
	ServiceAgents   ServiceKey = "agents"
	ServiceApproval ServiceKey = "approval"
	ServiceCompaction ServiceKey = "compaction" // installed by the compaction module (spec #4)
)

// Context is the wiring result of boot. Cannot be changed after Boot.
type Context struct {
	LLM      LLMRegistry      // nil if no module installs it
	Tools    ToolRegistry
	Sessions SessionStore
	Agents   AgentRegistry
	Approval ApprovalService
	Hooks    *Hooks
	Config   Config           // copy of the effective config
	Secrets  Secrets
	Profile  string
}

// Lookup gets any service (including keys beyond the five fields above).
func (c *Context) Lookup(key ServiceKey) (any, bool)
```

- Services other than the five fields above (for example `compaction`) are installed with `Provide` and retrieved with `Lookup`. The key `compaction` is defined as `ServiceCompaction` (`CQ-027`). Other keys are defined by specs #3 and #4.

## API-CORE-004: Generic hooks

```go
// Next passes to the next listener, or to the default handler at the end of the chain.
type Next[I, O any] func(ctx context.Context, in I) (O, error)

// WaterfallListener receives input and next.
type WaterfallListener[I, O any] func(ctx context.Context, in I, next Next[I, O]) (O, error)

// SerialListener receives input and returns one result.
type SerialListener[I, O any] func(ctx context.Context, in I) (O, error)

// UseOption configures listener registration.
type UseOption func(*useOptions)

// WithPrepend registers the listener before regular listeners.
func WithPrepend() UseOption

// Waterfall is a sequential middleware with a default handler.
type Waterfall[I, O any] struct{ /* not exported */ }

func NewWaterfall[I, O any]() *Waterfall[I, O]

// SetDefault replaces the default handler. Without a default, Call returns ErrNoDefault.
func (w *Waterfall[I, O]) SetDefault(h func(ctx context.Context, in I) (O, error))

// Use registers a listener. Release removes that listener.
func (w *Waterfall[I, O]) Use(l WaterfallListener[I, O], opts ...UseOption) (release func())

// Call runs the chain. The result of the first listener that does not call next is returned.
func (w *Waterfall[I, O]) Call(ctx context.Context, in I) (O, error)

// Serial calls all listeners in sequence and collects their results.
type Serial[I, O any] struct{ /* not exported */ }

func NewSerial[I, O any]() *Serial[I, O]

// Use registers a listener. Release removes that listener.
func (s *Serial[I, O]) Use(l SerialListener[I, O]) (release func())

// Call returns the result of each listener in order. The first error stops the chain.
func (s *Serial[I, O]) Call(ctx context.Context, in I) ([]O, error)

// Observe sends events to all subscribers without waiting.
type Observe[T any] struct{ /* not exported */ }

func NewObserve[T any]() *Observe[T]

// Publish sends v to every subscriber. Never blocks.
func (o *Observe[T]) Publish(v T)

// Subscribe creates a subscriber with a buffer of ObserveBuffer.
func (o *Observe[T]) Subscribe() *Subscription[T]

// Close closes all subscribers.
func (o *Observe[T]) Close()

// ObserveBuffer is the number of events per subscriber.
const ObserveBuffer = 1024

// Received is the result of Recv. Lagged > 0 means Value is not filled; call Recv again.
type Received[T any] struct {
	Lagged int // number of events dropped before the next event
	Value  T
}

// Subscription is the reader for one subscriber.
type Subscription[T any] struct{ /* not exported */ }

// Recv waits for the next event. Error ErrClosed after Close, or ctx.Err().
func (s *Subscription[T]) Recv(ctx context.Context) (Received[T], error)

// Close releases the subscriber.
func (s *Subscription[T]) Close()
```

- `Use` and `Subscribe` are safe to call from many goroutines. The listener list is copied when `Call` starts.
- `Observe` reduces the buffer by dropping the oldest event. The drop count is kept until `Recv` reads it as `Lagged`.

## API-CORE-005: Named hooks and input-output types

The hooks in `Hooks` (ADR 0002). The I and O types below are the final types. The definition of each type is in `API-CORE-015` (combined types). Default behavior is owned by specs #3 and #4.

| Field | Type | Mode | Input (I) | Output (O) | Default |
|---|---|---|---|---|---|
| `AgentPreStep` | `*Waterfall[PreStepInput, PreStepDecision]` | waterfall | `PreStepInput` | `PreStepDecision` | Spec #3 |
| `AgentRequest` | `*Waterfall[RequestControl, RequestControl]` | waterfall | `RequestControl` | `RequestControl` | Spec #3 |
| `LLMStream` | `*Waterfall[LLMStreamInput, ChatStream]` | waterfall | `LLMStreamInput` | `ChatStream` | Provider (spec #3). Retry (spec #3), compaction and `ErrContextTooLong` retry (spec #4) |
| `ToolsPreExecute` | `*Waterfall[ToolsPreExecuteInput, ToolDecision]` | waterfall | `ToolsPreExecuteInput` | `ToolDecision` | Spec #3 (fail-closed). Permission in spec #4 |
| `ToolsExecute` | `*Waterfall[ToolsExecuteInput, ToolOutput]` | waterfall | `ToolsExecuteInput` | `ToolOutput` | Spec #3 |
| `ToolsPostExecute` | `*Waterfall[ToolsPostExecuteInput, ToolOutput]` | waterfall | `ToolsPostExecuteInput` | `ToolOutput` | Spec #3 |
| `ApprovalRequest` | `*Waterfall[ApprovalRequest, ApprovalOutcome]` | waterfall | `ApprovalRequest` | `ApprovalOutcome` | `unavailable` (fail-closed). Responder in spec #4 |
| `AgentTurnStopping` | `*Serial[TurnStopInput, StopVote]` | serial | `TurnStopInput` | `StopVote` | None |
| `SessionEvent` | `*Observe[SessionEvent]` | observe | `SessionEvent` | — | None |

```go
// Hooks is the set of typed hooks. Created by Boot.
type Hooks struct {
	AgentPreStep      *Waterfall[PreStepInput, PreStepDecision]
	AgentRequest      *Waterfall[RequestControl, RequestControl]
	LLMStream         *Waterfall[LLMStreamInput, ChatStream]
	ToolsPreExecute   *Waterfall[ToolsPreExecuteInput, ToolDecision]
	ToolsExecute      *Waterfall[ToolsExecuteInput, ToolOutput]
	ToolsPostExecute  *Waterfall[ToolsPostExecuteInput, ToolOutput]
	ApprovalRequest   *Waterfall[ApprovalRequest, ApprovalOutcome]
	AgentTurnStopping *Serial[TurnStopInput, StopVote]
	SessionEvent      *Observe[SessionEvent]
}
```

- A tool rejected by a `ToolsPreExecute` listener returns `ToolDecision{Allowed: false, Status: ToolStatusRejected}`. There is no sentinel error for rejection.
- The types `ChatStream`, `TurnStopInput`, `StopVote`, and all other hook input types are in `API-CORE-015`.


## API-CORE-006: Messages, `ChatRequest`, `ChatEvent`, `Provider`

```go
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single message. Tool results are sent as tool_result blocks in a user message.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

type BlockKind string

const (
	BlockText       BlockKind = "text"
	BlockToolUse    BlockKind = "tool_use"
	BlockToolResult BlockKind = "tool_result"
	BlockImage      BlockKind = "image"
)

// ContentBlock is one content block. Fields not relevant to Kind are empty.
type ContentBlock struct {
	Kind      BlockKind       `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use: call id; tool_result: tool_use_id
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use
	Output    string          `json:"output,omitempty"`      // tool_result
	IsError   bool            `json:"is_error,omitempty"`    // tool_result
	MediaType string          `json:"media_type,omitempty"`  // image
	Data      []byte          `json:"data,omitempty"`        // image, base64 in JSON
}

// Usage is the token count.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// ChatRequest is a request to the provider.
type ChatRequest struct {
	Model       string     // "<model name>" without provider prefix
	System      string
	Messages    []Message
	Tools       []ToolSpec
	MaxTokens   int
	Temperature *float64
}

type ChatEventKind string

const (
	ChatTextDelta ChatEventKind = "text_delta"
	ChatBlock     ChatEventKind = "block"  // complete block (tool_use or text)
	ChatUsage     ChatEventKind = "usage"
	ChatStop      ChatEventKind = "stop"
)

// ChatEvent is one stream chunk. Fields depend on Kind.
type ChatEvent struct {
	Kind       ChatEventKind
	Text       string       // text_delta
	Block      ContentBlock // block
	Usage      Usage        // usage
	StopReason string       // stop: end_turn, tool_use, max_tokens, or the provider's value
}

// Provider is the model adapter. Errors before the stream starts are returned from Stream.
// Errors in the middle of the stream are sent as errors in the iterator.
type Provider interface {
	Stream(ctx context.Context, req ChatRequest) (ChatStream, error)
}

// LLMRegistry stores providers by name in [providers.<name>].
type LLMRegistry interface {
	Provider(name string) (Provider, bool)
	Names() []string
}
```

## API-CORE-007: `Tool`, `Risk`, `ToolRegistry`

```go
// Risk is the risk class of a tool.
type Risk string

const (
	RiskRead    Risk = "read"
	RiskWrite   Risk = "write"
	RiskExec    Risk = "exec"
	RiskNetwork Risk = "network"
)

// ToolSpec is the tool description for the model.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolCtx is the context of a single tool call.
type ToolCtx struct {
	SessionID string
	TurnID    string
	CallID    string
	AgentName string
	Workspace string
}

// ToolStatus is the status of a tool result (same as the status of tool/result in spec #1).
type ToolStatus string

const (
	ToolStatusSuccess  ToolStatus = "success"
	ToolStatusError    ToolStatus = "error"
	ToolStatusRejected ToolStatus = "rejected"
)

// ToolOutput is the tool result. Status error means the tool failed, rejected means the gate refused it.
type ToolOutput struct {
	Status ToolStatus
	Output string // text for the model; truncated per the tool limit
}

// ToolCall is a single tool call. Risk is filled before the ToolsPreExecute hook.
type ToolCall struct {
	SessionID string
	TurnID    string
	CallID    string
	Name      string
	Input     json.RawMessage
	Risk      Risk
}

// Tool is a tool the model can call.
type Tool interface {
	Spec() ToolSpec
	Risk(input json.RawMessage) Risk
	Call(ctx context.Context, tc ToolCtx, input json.RawMessage) (ToolOutput, error)
}

// ToolRegistry stores tools by name.
type ToolRegistry interface {
	Register(t Tool) error                          // error if the name already exists
	Get(name string) (Tool, bool)
	Specs(names []string) ([]ToolSpec, error)       // error if any name is not registered
}
```

## API-CORE-008: `SessionEvent`, kind, and payload

```go
// PayloadVersion is the value of field v in every payload (ADR 0009).
const PayloadVersion = 1

// SessionEvent is one row of session_events.
type SessionEvent struct {
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	TurnID    string          `json:"turn_id,omitempty"`
	CreatedAt int64           `json:"created_at"` // unix ms
}

// EventDraft is an event before it is written. Payload is marshaled to JSON.
type EventDraft struct {
	Kind    string
	TurnID  string
	Payload any
}

const (
	KindSessionCreated   = "session/created"
	KindTurnStart        = "turn/start"
	KindTurnEnd          = "turn/end"
	KindStepStart        = "step/start"
	KindStepEnd          = "step/end"
	KindSystemMessage    = "system/message"
	KindUserMessage      = "user/message"
	KindAssistantMessage = "assistant/message"
	KindAssistantAttempt = "assistant/attempt"
	KindToolCall         = "tool/call"
	KindToolResult       = "tool/result"
	KindApprovalAsked    = "approval/asked"
	KindApprovalDecided  = "approval/decided"
	KindApprovalPolicy   = "approval/policy"
	KindModelSelection   = "model/selection"
	KindCompactionSummary = "compaction/summary"
)
```

Payload per kind (all JSON fields are required, `v` = `PayloadVersion`):

| Kind | Struct | JSON fields |
|---|---|---|
| `session/created` | `SessionCreated` | `v`, `agent`, `channel`, `address`, `workspace`, `parent_session_id`, `parent_call_id`, `inherited_seq` |
| `turn/start` | `TurnStart` | `v`, `turn_id` |
| `turn/end` | `TurnEnd` | `v`, `turn_id`, `reason` (`end_turn`, `max_steps`, `cancelled`, `interrupted`, `error`, `budget`; `budget` is used starting with spec #5, ADR 0015), `error` (`PayloadError`, `null` if none) |
| `step/start` | `StepStart` | `v`, `step`, `provider`, `model` |
| `step/end` | `StepEnd` | `v`, `step`, `provider`, `model`, `usage` (`Usage`) |
| `system/message` | `SystemMessage` | `v`, `text` |
| `user/message` | `UserMessage` | `v`, `content` (`[]ContentBlock`) |
| `assistant/message` | `AssistantMessage` | `v`, `content` (`[]ContentBlock`), `stop_reason` |
| `assistant/attempt` | `AssistantAttempt` | `v`, `step`, `attempt`, `provider`, `model`, `error` (`PayloadError`) |
| `tool/call` | `ToolCallPayload` | `v`, `call_id`, `name`, `input` |
| `tool/result` | `ToolResultPayload` | `v`, `call_id`, `status` (`success`, `error`, `rejected`), `output` |
| `approval/asked` | `ApprovalAsked` | `v`, `request_id`, `call_id`, `tool`, `risk`, `label` (optional). No arguments |
| `approval/decided` | `ApprovalDecided` | `v`, `request_id`, `call_id`, `outcome`, `reason` (optional) |
| `approval/policy` | `ApprovalPolicy` | `v`, `policy` |
| `model/selection` | `ModelSelection` | `v`, `provider`, `model`, `agent` (optional, only from `SetAgent`) |
| `compaction/summary` | `CompactionSummary` | `v`, `summary`, `upto_seq`, `tokens_before`, `tokens_after`, `model` |

- This table is the single source of payload fields. Spec #3 (`API-AGENT-008`) and spec #4 (event table) refer here (`CQ-029`).
- The `error` field uses `PayloadError` (`API-CORE-015`).
- `approval/*` payloads do not carry tool arguments (ADR 0004). Arguments are read from `tool/call` via `call_id`.
- Go struct names use the suffix `Payload` when they clash with another type (`ToolCallPayload`, `ToolResultPayload`).
- The field `v` is set to `PayloadVersion` when writing. Migration of older versions happens at decode (ADR 0009).

## API-CORE-009: `SessionStore`, `SessionInfo`, `NewSession`, `SessionFilter`

```go
// SessionInfo is the session projection.
type SessionInfo struct {
	ID              string
	Agent           string
	Channel         string
	Address         string
	Workspace       string // absolute, already passed through filepath.EvalSymlinks
	Title           string
	Status          string // "idle" or "running"
	ParentSessionID string
	InheritedSeq    int64 // 0 if not a fork
	LastSeq         int64
	TurnCount       int64
	CreatedAt       int64 // unix ms
	UpdatedAt       int64 // unix ms
}

// NewSession is the input to Create.
type NewSession struct {
	Agent           string
	Channel         string
	Address         string
	Workspace       string // absolute; required
	ParentSessionID string // empty if not a child
	ParentCallID    string // empty if not a subagent child
	InheritedSeq    int64
}

// SessionFilter limits List.
type SessionFilter struct {
	Channel string
	Address string
	Limit   int // 0 = no limit; ordered by most recent updated_at
}

// SessionStore is the append-only event log (ADR 0009).
type SessionStore interface {
	Create(ctx context.Context, s NewSession) (SessionInfo, error)
	Open(ctx context.Context, id string) (SessionInfo, error) // with resume repair, requires lease
	Info(ctx context.Context, id string) (SessionInfo, error) // read-only, no repair
	Append(ctx context.Context, id string, drafts []EventDraft) ([]SessionEvent, error)
	Events(ctx context.Context, id string, fromSeq int64) iter.Seq2[SessionEvent, error]
	ChainEvents(ctx context.Context, id string) ([]SessionEvent, error) // parent chain is cut at inherited_seq
	DeriveMessages(ctx context.Context, id string) ([]Message, error)   // ChainEvents then DeriveMessages (API-CORE-015)
	List(ctx context.Context, f SessionFilter) ([]SessionInfo, error)
	FindPrefix(ctx context.Context, prefix string) ([]SessionInfo, error) // case-insensitive
	Fork(ctx context.Context, parentID string, atSeq int64) (SessionInfo, error)
	// TryLease acquires the session lock. release releases it. LeaseHeldError if held by another process.
	TryLease(ctx context.Context, id string) (release func() error, err error)
}
```

- `Events` returns events with `seq >= fromSeq`.
- `Append` writes drafts in one transaction and returns the events that have been assigned `Seq`.
- `Fork` fills `Agent`, `Channel`, `Address`, and `Workspace` from the parent. `ParentSessionID` is set to the parent and `InheritedSeq` = `atSeq`. Fork does not change the agent.
- `ResolvePrefix` (`API-CORE-015`) applies the prefix rule: at least 6 characters and exactly one match.
- `Info`, `ChainEvents`, and `DeriveMessages` are added by spec #4 (`CQ-022`, `CQ-028`).


## API-CORE-010: `AgentRegistry`, `LiveFrame`

```go
// LiveFrame is a transient frame (streaming), not written to the log.
type LiveFrame struct {
	SessionID string
	TurnID    string
	Kind      string // "text_delta"
	Text      string
}

// AgentRegistry manages live agents and their inboxes.
type AgentRegistry interface {
	Send(ctx context.Context, sessionID string, text string) error
	Cancel(ctx context.Context, sessionID string) error
	SetAgent(ctx context.Context, sessionID string, agent string) error
	SetModel(ctx context.Context, sessionID string, model string) error // "<provider>:<model>"
	SubscribeLive(sessionID string) (*Subscription[LiveFrame], error)
}
```

## API-CORE-011: `ApprovalService`, `Policy`, `ApprovalOutcome`

```go
// Policy is the session approval mode.
type Policy string

const (
	PolicyTrust Policy = "trust"
	PolicyAsk   Policy = "ask"
	PolicyNever Policy = "never"
)

// ApprovalOutcome is the approval answer. Apart from allowed_once and allowed_session, the tool is rejected (fail-closed).
type ApprovalOutcome string

const (
	OutcomeAllowedOnce    ApprovalOutcome = "allowed_once"
	OutcomeAllowedSession ApprovalOutcome = "allowed_session"
	OutcomeRejected       ApprovalOutcome = "rejected"
	OutcomeCancelled      ApprovalOutcome = "cancelled"
	OutcomeUnavailable    ApprovalOutcome = "unavailable"
)

// ApprovalRequest is an approval request for a single tool call. Carries no arguments (ADR 0004).
type ApprovalRequest struct {
	SessionID string
	TurnID    string
	CallID    string // reference to tool/call
	AgentName string
	ToolName  string
	Risk      Risk
	Label     string // child agent name if the session has a parent, empty otherwise
}

// ApprovalService runs approval through the ApprovalRequest hook.
type ApprovalService interface {
	Request(ctx context.Context, req ApprovalRequest) (ApprovalOutcome, error)
	SetPolicy(ctx context.Context, sessionID string, p Policy) error
	EffectivePolicy(ctx context.Context, sessionID string) (Policy, error)
}
```

## API-CORE-012: `Secret` and `Secrets`

```go
// Secret stores a secret value. All formats and encoders write "***".
type Secret struct{ /* v not exported */ }

func NewSecret(v string) Secret

// Reveal returns the original value. Only use when sending to an authorized party (e.g. a provider HTTP header).
func (s Secret) Reveal() string

func (s Secret) String() string                // "***"
func (s Secret) GoString() string              // "***"
func (s Secret) Format(f fmt.State, verb rune) // "***" for all verbs
func (s Secret) LogValue() slog.Value          // "***"
func (s Secret) MarshalJSON() ([]byte, error)  // "\"***\""
func (s Secret) MarshalText() ([]byte, error)  // "***"

// Secrets gets secret values by env name.
type Secrets interface {
	Lookup(name string) (Secret, bool)
}
```

## API-CORE-013: `Config` schema types

```go
// Config is the effective config. Secret values are of type Secret.
type Config struct {
	Version   int                       `toml:"version"`
	Providers map[string]ProviderConfig `toml:"providers"`
	Agents    map[string]AgentConfig    `toml:"agents"`
	Routes    []RouteConfig             `toml:"routes"`
	MCP       map[string]MCPConfig      `toml:"mcp"`
	Channels  map[string]map[string]any `toml:"channels"`
	Modules   []ModuleRow               `toml:"modules"`
}

type ProviderConfig struct {
	Kind      string `toml:"kind"`
	BaseURL   string `toml:"base_url"`
	APIKeyEnv string `toml:"api_key_env"`
}

type AgentConfig struct {
	Provider   string      `toml:"provider"`
	Model      string      `toml:"model"`
	Fallback   []string    `toml:"fallback"`
	PromptFile string      `toml:"prompt_file"`
	Workspace  string      `toml:"workspace"`
	Tools      []string    `toml:"tools"`
	Subagents  []string    `toml:"subagents"`
	MCP        []string    `toml:"mcp"`
	Skills     []string    `toml:"skills"`
	Permission string      `toml:"permission"`
	Rules      AgentRules  `toml:"rules"`
}

type AgentRules struct {
	Allow []string `toml:"allow"`
	Deny  []string `toml:"deny"`
}

type RouteConfig struct {
	Channel string         `toml:"channel"`
	Agent   string         `toml:"agent"`
	Match   map[string]any `toml:"match"`
}

type MCPConfig struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	URL     string            `toml:"url"`
	Env     map[string]Secret `toml:"env"`
	Headers map[string]Secret `toml:"headers"`
}

// ModuleRow is an effective [[modules]] row.
type ModuleRow struct {
	ID       string         `toml:"id"`
	Module   string         `toml:"module"`
	Disabled bool           `toml:"disabled"`
	Config   map[string]any `toml:"config"`
}
```

- The `toml` tag is used to marshal the effective config (`--dump-config` in spec #1).
- The `Env` and `Headers` fields are all of type `Secret`. Literal values also show `***` (see `Agent recommendation` in design).

## API-CORE-014: Sentinel errors

```go
var (
	ErrNotFound              = errors.New("not found")
	ErrClosed                = errors.New("subscription closed") // lana.ErrClosed (spec #3) is different
	ErrNoDefault             = errors.New("default handler is not set")
	ErrContextTooLong        = errors.New("context too long")
	ErrPrefixTooShort        = errors.New("session prefix must be at least 6 characters")
	ErrAmbiguousPrefix       = errors.New("session prefix matches more than one session")
	ErrUnknownPayloadVersion = errors.New("unknown payload version")
)

// LeaseHeldError is returned by TryLease when the session is used by another process.
type LeaseHeldError struct {
	SessionID string
	PID       int
	Host      string
}

func (e *LeaseHeldError) Error() string
```

- `ErrContextTooLong` is wrapped in `ProviderError` with class `context_length` (`API-CORE-015`).
- `ErrRejected` (version before revision 2) is removed. Tool rejection uses `ToolDecision` (`CQ-020`).

## API-CORE-015: Combined `core` type list (single source)

This list contains all `core` types used by specs #2, #3, and #4. Names and signatures here take precedence over names in other specs. Specs #3 and #4 refer to these IDs and do not redefine them.

| Name | Kind | Section | Needed from spec #2 | Needed from spec #3 | Needed from spec #4 |
|---|---|---|---|---|---|
| `Message`, `ContentBlock`, `BlockKind`, `Role`, `Usage` | Type | `API-CORE-006` | Yes | Yes | Yes |
| `ChatRequest`, `ChatEvent`, `ChatEventKind`, `Provider`, `LLMRegistry` | Type | `API-CORE-006` | Yes | Yes | — |
| `ChatStream` | Alias | `API-CORE-015` | Yes | Yes | — |
| `LLMTarget`, `LLMStreamInput` | Type | `API-CORE-015` | — | Yes | — |
| `RequestControl` | Type | `API-CORE-015` | — | Yes | — |
| `PreStepInput`, `PreStepDecision` | Type | `API-CORE-015` | — | Yes (replaces `StepInput`) | — |
| `TurnStopInput`, `StopVote` | Type | `API-CORE-015` | Yes | Yes (replaces `AgentTurnStopInput`, `TurnStopVote`) | — |
| `ToolSpec`, `ToolCtx`, `ToolCall`, `Tool`, `Risk`, `ToolRegistry` | Type | `API-CORE-007` | Yes | Yes | Yes |
| `ToolStatus` | Type and constants | `API-CORE-007` | — | Yes | — |
| `ToolOutput` | Type | `API-CORE-007` | Yes (with `Status`) | Yes | — |
| `ToolDecision` | Type | `API-CORE-015` | — | Yes | Yes (combined, `CQ-020`) |
| `ToolsPreExecuteInput`, `ToolsExecuteInput`, `ToolsPostExecuteInput` | Type | `API-CORE-015` | — | Yes (`ToolPostInput` from #2 replaced, `CQ-017`) | Yes (permission listener input) |
| `ToolPostInput` | Type | Removed | Yes (replaced) | — | — |
| `SessionEvent`, `EventDraft`, kind constants, payload structs, `PayloadVersion` | Type | `API-CORE-008` | Yes | Yes | Yes |
| `PayloadError` | Type | `API-CORE-015` | — | Yes | Yes |
| `SessionInfo`, `NewSession`, `SessionFilter`, `SessionStore` | Type and interface | `API-CORE-009` | Yes | Yes | Yes (`Info`, `ChainEvents`, `DeriveMessages`) |
| `AgentRegistry`, `LiveFrame` | Interface and type | `API-CORE-010` | Yes | Yes | Yes (`SubscribeLive`, `Cancel`) |
| `Policy`, `ApprovalOutcome`, `ApprovalRequest`, `ApprovalService` | Type and interface | `API-CORE-011` | Yes | Yes | Yes |
| `CompactionResult`, `Compactor` | Type and interface | `API-CORE-015` | — | — | Yes |
| `ProviderError` | Type | `API-CORE-015` | — | Yes | Yes |
| `Secret`, `Secrets`, `Config` and its derivatives | Type | `API-CORE-012`, `API-CORE-013` | Yes | Yes | — |
| `ServiceKey` (adds `ServiceCompaction`) | Constant | `API-CORE-003` | Yes | — | Yes |
| `DeriveMessages` | Pure function | `API-CORE-015` | — | Yes (subset without compaction) | Yes (compaction, `CQ-028`) |
| `ResolvePrefix`, `AmbiguousPrefixError` | Function and type | `API-CORE-015` | — | — | Yes (`CQ-023`) |
| Sentinel errors | Variable | `API-CORE-014` | Yes | Yes | Yes |

```go
// ---- Hook input and output ----

// PreStepInput is the input of AgentPreStep: one message claimed in one step.
type PreStepInput struct {
	SessionID string
	TurnID    string
	AgentName string
	Step      int
	Input     Message
}

// PreStepDecision is the result of AgentPreStep. Input = the message modified by listeners (default: original Input).
type PreStepDecision struct {
	Input  Message
	Reject bool
	Reason string
}

// RequestControl is the request control that AgentRequest can modify.
type RequestControl struct {
	MaxTokens   int
	Temperature *float64
}

// LLMTarget is one provider target. Primary is at index 0, followed by fallbacks in order.
type LLMTarget struct {
	Provider string
	Model    string
}

// LLMStreamInput is the input of LLMStream.
type LLMStreamInput struct {
	SessionID string
	TurnID    string
	Step      int
	AgentName string
	Targets   []LLMTarget
	Request   ChatRequest
}

// ChatStream is the provider stream result.
type ChatStream = iter.Seq2[ChatEvent, error]

// ToolsPreExecuteInput is the input of ToolsPreExecute. Risk is in Call.Risk.
type ToolsPreExecuteInput struct {
	AgentName string
	Call      ToolCall
}

// ToolDecision is the decision of ToolsPreExecute.
// Allowed false means Status = ToolStatusRejected. Outcome is empty if there is no approval.
type ToolDecision struct {
	Allowed bool
	Outcome ApprovalOutcome
	Status  ToolStatus
	Reason  string // text read by the model
}

// ToolsExecuteInput is the input of ToolsExecute.
type ToolsExecuteInput struct {
	Ctx  ToolCtx
	Call ToolCall
	Tool Tool
}

// ToolsPostExecuteInput is the input of ToolsPostExecute.
type ToolsPostExecuteInput struct {
	Call   ToolCall
	Output ToolOutput
}

// TurnStopInput is the input of AgentTurnStopping.
type TurnStopInput struct {
	SessionID  string
	TurnID     string
	AgentName  string
	Steps      int
	StopReason string // reason to use if the turn stops
}

// StopVote is the vote of one AgentTurnStopping listener.
type StopVote struct {
	Continue bool
	Reason   string
}

// ---- Provider error ----

// ProviderError is a provider error. Class: auth, rate_limit, server, network,
// context_length, client, missing_secret.
type ProviderError struct {
	Class   string
	Status  int // HTTP code, 0 if none
	Message string
}

func (e *ProviderError) Error() string

// Is makes errors.Is(err, ErrContextTooLong) true if Class = context_length.
func (e *ProviderError) Is(target error) bool

// PayloadError is the error in the turn/end and assistant/attempt payloads.
type PayloadError struct {
	Class   string `json:"class"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message"`
}

// ---- Compaction ----

// CompactionResult is the result of CompactNow.
type CompactionResult struct {
	UptoSeq      int64
	TokensBefore int64
	TokensAfter  int64
}

// Compactor summarizes session history on request. Installed by the compaction module (spec #4)
// under the key ServiceCompaction.
type Compactor interface {
	CompactNow(ctx context.Context, sessionID string) (CompactionResult, error)
}

// ---- Session and prefix ----

// AmbiguousPrefixError is returned by ResolvePrefix if the prefix matches more than one session.
type AmbiguousPrefixError struct {
	Candidates []string // ascending order
}

func (e *AmbiguousPrefixError) Error() string

// Is makes errors.Is(err, ErrAmbiguousPrefix) true.
func (e *AmbiguousPrefixError) Is(target error) bool

// ResolvePrefix picks one session from FindPrefix results. A prefix shorter than 6 characters
// yields ErrPrefixTooShort. No match yields ErrNotFound. More than one yields *AmbiguousPrefixError.
func ResolvePrefix(matches []SessionInfo, prefix string) (SessionInfo, error)

// ---- Model history ----

// DeriveMessages builds the model history from the event chain. Pure function without I/O.
// Ignores system/message that is not the last one, assistant/attempt, and tool/call.
// Spec #3 implements the subset without compaction. Spec #4 adds compaction/summary.
func DeriveMessages(events []SessionEvent) ([]Message, error)
```


---

## Contract traceability

| Operation | Requirements | Design component | Verification |
|---|---|---|---|
| `API-CONF-001` | `REQ-CONF-001`, `REQ-CONF-006` | `config` loader (layer) | Unit test of `config` with temporary `Home` |
| `API-CONF-002` | `REQ-CONF-003`, `REQ-CONF-008` | `config` validator | Unit test of error table |
| `API-CONF-003` | `REQ-CONF-003` | `config` validator | Unit test |
| `API-CONF-004` | `REQ-CONF-002`, `REQ-CONF-003` | `config` merge and validator | Unit test |
| `API-CONF-005` | `REQ-CONF-002`, `REQ-CONF-003` | `config` merge and validator | Unit test |
| `API-CONF-006` | `REQ-CONF-003`, `REQ-CONF-007` | `config` interpolation | Unit test |
| `API-CONF-007` | `REQ-CONF-002` | `config` merge | Unit test |
| `API-CONF-008` | `REQ-CONF-002`, `REQ-MOD-001` | `config` merge, `core` plan | Unit test |
| `API-CONF-009` | `REQ-PROF-001`, `REQ-CONF-001` | `config` profile table | Unit test |
| `API-CONF-010` | `REQ-CONF-005` | `config` secrets | Unit test |
| `API-CONF-011` | `REQ-CONF-007` | `config` interpolation | Unit test |
| `API-CONF-012` | `REQ-CONF-003` | `config.Error` | Unit test |
| `API-CONF-013` | `REQ-CONF-004` | `config` origins | Unit test |
| `API-CONF-014` | `REQ-CONF-009` | `config.Load` | Unit and integration test |
| `API-CORE-001` | `REQ-MOD-001`, `REQ-MOD-004` | `core` registry | Unit test |
| `API-CORE-002` | `REQ-MOD-001` through `REQ-MOD-004` | `core` boot and shutdown | Unit test with fake modules |
| `API-CORE-003` | `REQ-MOD-001`, `REQ-MOD-004` | `core` Context | Unit test |
| `API-CORE-004` | `REQ-HOOK-001` through `REQ-HOOK-004` | `core` generic hooks | Unit and race test |
| `API-CORE-012` | `REQ-CONF-005` | `core.Secret` | Unit test of format |
| `API-CORE-013` | `REQ-CONF-003` | `core.Config` | Unit test |
| `API-CORE-014` | `REQ-MOD-002`, `REQ-HOOK-001` | `core` errors | Unit test |
| `API-CORE-015` | Not applicable (types) | `core` combined types, `DeriveMessages`, `ResolvePrefix` | Compile test and unit tests in specs #3 and #4 (`TestDeriveGolden`, `TestResolvePrefix`) |

## Blocking Contract Questions

| ID | Question | Controls | Owner | Status | Resolution |
|---|---|---|---|---|---|
| `CQ-001` | Position of memory layer relative to `--patch` | `API-CONF-001`, `API-CONF-014` | Ridho | Resolved | Agent recommendation: memory layers are applied after all `Patches`, in the order of the `Layers` slice. |
| `CQ-002` | Meaning of empty `Options.Home` | `API-CONF-014` | Ridho | Resolved | Agent recommendation: no file from home is read. The CLI calls `HomeDir` first. This satisfies `REQ-CONF-009`. |
| `CQ-003` | Empty profile | `API-CONF-014` | Ridho | Resolved | Agent recommendation: same as `minimal`. |
| `CQ-004` | Contents of the `base` layer and built-in profiles | `API-CONF-009` | Ridho | Resolved | Agent recommendation: table in `API-CONF-009`. `minimal` has no extra rows, `headless` adds `runner`. Module names follow specs #3 and #4. |
| `CQ-005` | Env that exists but is empty | `API-CONF-010`, `API-CONF-011` | Ridho | Resolved | Agent recommendation: an empty value is treated as not present. Lookup continues to `secrets.env`. |
| `CQ-006` | Secret for literal values in `mcp.*.env` | `API-CORE-013`, `API-CONF-006` | Ridho | Resolved | Agent recommendation: all `env` and `headers` values are of type `Secret`. Literal values also show `***`. This choice is the simplest and safest. |
| `CQ-007` | Who prints the `secrets.env` permission warning | `API-CONF-010`, `API-CONF-014` | Ridho | Resolved | Agent recommendation: `Load` returns `Result.Warnings` and logs a `slog` warn. Spec #1 prints to stderr. The library does not write to stderr. |
| `CQ-008` | Closest key suggestion | `API-CONF-012` | Ridho | Resolved | Agent recommendation: one suggestion with edit distance ≤ 2 in the same table. If none, no suggestion. |
| `CQ-009` | Agent default `permission` | `API-CONF-004` | Ridho | Resolved | Agent recommendation: `ask`, following the fail-closed principle (ADR 0004). |
| `CQ-010` | Invalid `secrets.env` rows | `API-CONF-010` | Ridho | Resolved | Agent recommendation: validation error with line number. Other rows are still processed. |
| `CQ-011` | `after` or `before` for an existing `id` | `API-CONF-008` | Ridho | Resolved | Agent recommendation: ignored and still validated. `after` and `before` together is an error. |
| `CQ-012` | Contents of `channels.*`, `routes.*.match`, and `module.config` | `API-CONF-005`, `API-CONF-007`, `API-CONF-008` | Ridho | Resolved | Agent recommendation: opaque in this spec. Content validation is in the owning module. |
| `CQ-013` | `fallback` and `prompt_file` | `API-CONF-004` | Ridho | Resolved | Agent recommendation: only array-of-string and string types. Provider in `fallback` and existence of the prompt file are checked by spec #3. |
| `CQ-014` | Escape for literal `${` | `API-CONF-011` | Ridho | Resolved | Agent recommendation: no escape. In applicable locations, `${` is always a variable. |
| `CQ-015` | Missing `--patch`: skipped (`REQ-CONF-001`) or error (`API-CLI-001` spec #1) | `API-CONF-001`, `API-CONF-014` | Ridho | Resolved | Agent recommendation: error. Spec #1 already sets exit 2 and the message `patch file not found`. The "skipped" rule in `REQ-CONF-001` applies to profile and `config.toml`. This note goes into the design. |
| `CQ-016` | Combined `core` type list: single source and names | `API-CORE-005` through `API-CORE-015` | Ridho | Resolved | Agent recommendation (reconciliation): `API-CORE-015` is the combined list. For the same type, the spec #2 name is used. The spec #2 signature wins (see `CQ-022`). The per-spec requirement columns record the users. |
| `CQ-017` | Names of tool hook inputs | `API-CORE-005`, `API-CORE-015` | Ridho | Resolved | Agent recommendation (reconciliation): `ToolsPreExecuteInput`, `ToolsExecuteInput`, `ToolsPostExecuteInput`. `ToolPostInput` (spec #2) is replaced so the three tool inputs are uniform. Struct contents are the same. |
| `CQ-018` | Inputs and outputs of step and stop hooks | `API-CORE-005`, `API-CORE-015` | Ridho | Resolved | Agent recommendation (reconciliation): `PreStepInput` and `PreStepDecision` replace `StepInput`, because `Reject` is needed (spec #3 `Q-008`). `TurnStopInput` and `StopVote` from spec #2 are used. `AgentTurnStopInput` and `TurnStopVote` are removed. |
| `CQ-019` | Shape of `ChatEvent` and `ToolCall` | `API-CORE-006`, `API-CORE-007` | Ridho | Resolved | Agent recommendation (reconciliation): `ChatEvent` uses the spec #2 shape (`ChatBlock` carries `Block ContentBlock`, `Usage` is a struct). The `tool_use` kind from spec #3 becomes `ChatBlock` with `Block.Kind = tool_use`. `ToolCall` uses the spec #2 fields (`SessionID`, `TurnID`, `CallID`, `Name`, `Input`, `Risk`). |
| `CQ-020` | Tool status and `ToolDecision` | `API-CORE-007`, `API-CORE-015` | Ridho | Resolved | Agent recommendation (reconciliation): `ToolOutput{Status ToolStatus, Output}` replaces `IsError`. `ToolDecision{Allowed, Outcome, Status, Reason}` merges `Allow` (spec #3) and `Allowed` (spec #4). The name `Allowed` is used. `ErrRejected` is removed; rejection goes through `ToolDecision`. |
| `CQ-021` | Contents of `ApprovalRequest` | `API-CORE-011` | Ridho | Resolved | Agent recommendation (reconciliation): spec #2 fields without `Input` and `Summary`, plus `Label` (spec #4). The tool name uses `ToolName`. No arguments (ADR 0004). |
| `CQ-022` | `SessionStore` signature | `API-CORE-009` | Ridho | Resolved | Agent recommendation (reconciliation): spec #2 signature wins. `Create` and `Open` return `SessionInfo`. `Events` returns an iterator. `List` uses `SessionFilter` with `Limit` 0 = no limit. `FindPrefix` returns a list. `Fork` has no `in` argument: the child inherits the parent's agent, channel, address, and workspace. Added from spec #4: `Info`, `ChainEvents`, `DeriveMessages`. |
| `CQ-023` | `Lease` and prefix rule | `API-CORE-009`, `API-CORE-014`, `API-CORE-015` | Ridho | Resolved | Agent recommendation (reconciliation): `TryLease` returns `release func() error`. `core.Lease` is removed. `LeaseHeldError` from spec #2 is used with `errors.As`. The prefix rule (at least 6 characters, exactly one) is in `core.ResolvePrefix` with `ErrPrefixTooShort`, `ErrNotFound`, and `AmbiguousPrefixError`. |
| `CQ-024` | Argument type of `Append` | `API-CORE-009` | Ridho | Resolved | Agent recommendation (reconciliation): `EventDraft` from spec #2 is used. `NewEvent` from spec #4 is removed. |
| `CQ-025` | Hook I/O shape and minimum shape | `API-CORE-005` | Ridho | Resolved | Agent recommendation (reconciliation): I/O types in `API-CORE-005` were raised from the minimum shape to the final types. Spec #2 says specs #3 and #4 may fill in behavior. This type change is recorded here and in the CHANGELOG before v1.0.0 (ADR 0014). |
| `CQ-026` | `ProviderError` and context too long | `API-CORE-014`, `API-CORE-015` | Ridho | Resolved | Agent recommendation (reconciliation): `core.ProviderError{Class, Status, Message}` and sentinel `core.ErrContextTooLong`. `Is(ErrContextTooLong)` is true for class `context_length`. The spec #3 adapter creates it. Spec #4 compaction uses `errors.Is`. `lana.ProviderError` in spec #3 becomes an alias. |
| `CQ-027` | Compaction in `core` | `API-CORE-003`, `API-CORE-015` | Ridho | Resolved | Agent recommendation (reconciliation): `Compactor` and `CompactionResult` are in `core`. `ServiceCompaction = "compaction"` is added in `API-CORE-003`. `AgentRegistry` (spec #3) has no `CompactNow`. |
| `CQ-028` | History derivation in `core` | `API-CORE-009`, `API-CORE-015` | Ridho | Resolved | Agent recommendation (reconciliation): `core.DeriveMessages(events) ([]Message, error)` is a pure function without I/O. The store wraps it: `ChainEvents` then `DeriveMessages`. Spec #3 implements the subset without compaction. Spec #4 adds compaction and does not change results for logs without compaction. |
| `CQ-029` | Owner of event payload fields | `API-CORE-008` | Ridho | Resolved | Agent recommendation (reconciliation): `API-CORE-008` is the single table of payload fields. Spec #3 (`API-AGENT-008`) and spec #4 (event table) refer here. Additions: `session/created.workspace` and `parent_call_id`, `model/selection.agent`, `turn/end.error`, `assistant/attempt.model`, `approval/*.request_id`, `compaction/summary.summary`. `system/message` uses `text`. |
| `CQ-030` | Additional config keys and `rules` pattern validation | `API-CONF-003`, `API-CONF-004` | Ridho | Resolved | Agent recommendation (reconciliation): keys owned by specs #3 and #4 are registered in the table "The keys below are known to this spec" in `API-CONF-004`. `rules` patterns are validated through `internal/pattern.ParsePattern` after spec #4 is done. Until then, only arrays of strings. |
| `CQ-031` | Profile `base` and module names | `API-CONF-009` | Ridho | Resolved | Agent recommendation (reconciliation): `base` contains `llm`, `tools`, `store` (`sqlite_store`), `agent`, `permission`, `approval`, and `compaction`. `minimal` is the same as `base`. `headless` is the same as `base` plus `cli_runner` (spec #1). The `prompt` row is removed. Module names use the names from specs #3 and #4. |
| `CQ-032` | Profile before modules exist | `API-CONF-009` | Ridho | Resolved | Agent recommendation (reconciliation): this is a note on build order, not a new profile. Until spec #4 and `cli_runner` exist, built-in profiles fail `module` validation. Spec #3 tests use `Layers` without a built-in profile (see design of spec #3). |

None remaining open.

## Approval

- [x] Requirements are Approved and their revision is recorded above.
- [x] Contract applicability is determined.
- [x] Every operation has a stable ID and is linked to requirements and acceptance criteria.
- [x] Input, success, failure, authorization, and compatibility are explicit.
- [x] No behavior exists only in this file. Behavior not yet in requirements is recorded in `design.md` under Notes for requirements.
- [x] Blocking Contract Questions is `None`.
- **Approved by**: Ridho (gate released 2026-10-29)
- **Approved on**: 2026-10-29 (revision 1). Revision 2 reconciled `CQ-016` through `CQ-032` by the agent on behalf of the owner (gate released 2026-10-29).
