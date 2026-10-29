# Contract: Session event log, permission, approval

- **Issue**: [`#4`](https://github.com/ridhomujizat/lana-harness/issues/4)
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 2 (REQ-APPR-007 and AC-APPR-017 added)
- **Requirements status**: Approved
- **Contract revision**: 2 (reconciliation, see `CQ-011` to `CQ-022`)
- **Applies to**: config (`[agents.<name>]`, `[agents.<name>.rules]`, `[[modules]]` row `store`), event log (`session_events`, kinds and payloads), and the Go API of packages `store`, `core`, permission, approval, and compaction (ADR 0014)
- **Status**: Approved

This file defines the exact shape of storage, events, config, and the Go API. Behavior is owned by
`requirements.md`. Operations here have no CLI commands. Commands are owned by spec #1.

## Shared Conventions

| Item | Decision |
|---|---|
| CLI exit code | Not applicable. Spec #1 maps errors to exit codes |
| stdout and stderr output | Not applicable. No direct output |
| Config format | `$LANA_HOME/config.toml` and profiles. See [Config](#config). Loader and validation are owned by spec #2 |
| Authentication | Not applicable. Access is through the OS user's process. File `lana.db` has mode `0600` (`REQ-STORE-001`) |
| Schema version | `PRAGMA user_version`, sequential migration numbers starting from 1. Migrations only add, they do not rewrite data |
| Payload version | Integer field `v` in every event payload, starting from 1. Older versions are decoded to the current version in memory (`REQ-STORE-013`) |
| Session ID | 26-character ULID, uppercase (spec #1 convention) |
| Time | Unix milliseconds, integer. Columns `created_at`, `updated_at`, `acquired_at` |
| Serialization | UTF-8 JSON. `snake_case` fields. Event payloads are JSON objects |
| Compatibility of the whole file | All operations are new. No breaking change. No deprecation |

## SQL Schema

Migrations run in order. Each file runs in one transaction, then `user_version` is set
to the migration number. Files `store/migrations/0001_session_log.sql` and `0002_session_leases.sql`.

`store/migrations/0001_session_log.sql`:

```sql
CREATE TABLE session_events (
  session_id TEXT    NOT NULL,
  seq        INTEGER NOT NULL,
  kind       TEXT    NOT NULL,
  payload    TEXT    NOT NULL,
  turn_id    TEXT,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;

CREATE TABLE sessions (
  id                TEXT    PRIMARY KEY,
  agent             TEXT    NOT NULL,
  channel           TEXT    NOT NULL,
  address           TEXT    NOT NULL,
  workspace         TEXT    NOT NULL,
  title             TEXT    NOT NULL DEFAULT '',
  status            TEXT    NOT NULL CHECK (status IN ('idle', 'running')),
  parent_session_id TEXT    REFERENCES sessions (id) ON DELETE RESTRICT,
  inherited_seq     INTEGER,
  last_seq          INTEGER NOT NULL,
  turn_count        INTEGER NOT NULL DEFAULT 0,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);

CREATE INDEX idx_sessions_channel_address_updated
  ON sessions (channel, address, updated_at DESC);

CREATE INDEX idx_session_events_kind_created
  ON session_events (kind, created_at);

CREATE TABLE session_prune_allow (
  session_id TEXT PRIMARY KEY
) WITHOUT ROWID;

CREATE TRIGGER session_events_no_update
BEFORE UPDATE ON session_events
WHEN NOT EXISTS (SELECT 1 FROM session_prune_allow p WHERE p.session_id = OLD.session_id)
BEGIN
  SELECT RAISE(ABORT, 'session_events is append-only');
END;

CREATE TRIGGER session_events_no_delete
BEFORE DELETE ON session_events
WHEN NOT EXISTS (SELECT 1 FROM session_prune_allow p WHERE p.session_id = OLD.session_id)
BEGIN
  SELECT RAISE(ABORT, 'session_events is append-only');
END;
```

`store/migrations/0002_session_leases.sql`:

```sql
CREATE TABLE session_leases (
  session_id  TEXT    PRIMARY KEY,
  pid         INTEGER NOT NULL,
  host        TEXT    NOT NULL,
  acquired_at INTEGER NOT NULL
);
```

DDL usage rules:

- `session_prune_allow` is filled only inside the `Prune` transaction, then deleted in the same transaction (`API-STORE-011`).
- Column `sessions.parent_session_id` uses `ON DELETE RESTRICT`. Pruning a session that still has children fails with `ErrSessionHasChildren`.
- There is no `messages` table. There are no `embedding`, `memories`, or `cron_*` columns in this spec (M5).

## Event Kinds and Payloads

The fields and type of each payload are owned by `API-CORE-008` (spec #2, `CQ-029`). Spec #4 only writes
kinds and fields in this table. All payloads contain `"v": 1`.

| Kind | Payload fields (v = 1, see `API-CORE-008`) | Written by | Enters model history |
|---|---|---|---|
| `session/created` | `agent`, `channel`, `address`, `workspace`, `parent_session_id` (optional), `parent_call_id` (optional), `inherited_seq` (optional) | `Create`, `Fork` | No |
| `turn/start` | empty, `turn_id` in column | Agent loop | No |
| `turn/end` | `reason`: `end_turn`, `max_steps`, `cancelled`, `interrupted`, `error`. `error` (`PayloadError`, optional) | Agent loop, resume repair | No |
| `step/start` | `step` (integer), `provider`, `model` | Agent loop | No |
| `step/end` | `step`, `provider`, `model`, `usage` `{input_tokens, output_tokens}` | Agent loop | No (used for the compaction threshold) |
| `system/message` | `text` (string) | Agent loop | No. Its last message is used as `ChatRequest.System` (`Q-028`) |
| `user/message` | `content` (`[]core.ContentBlock`) | Agent loop | Yes |
| `assistant/message` | `content` (`[]core.ContentBlock` containing `text` and `tool_use`), `stop_reason` | Agent loop | Yes |
| `assistant/attempt` | `provider`, `model`, `attempt`, `error` (`PayloadError`) | Agent loop | No |
| `tool/call` | `call_id`, `name`, `input` | Agent loop | No |
| `tool/result` | `call_id`, `status` (`success`, `error`, `rejected`), `output` | Agent loop, resume repair | Yes |
| `approval/asked` | `request_id`, `call_id`, `tool`, `risk`, `label` (optional). No arguments | Approval service | No |
| `approval/decided` | `request_id`, `call_id`, `outcome`, `reason` (optional) | Approval service, resume repair | No |
| `approval/policy` | `policy` (`trust`, `ask`, `never`) | `SetPolicy` | No |
| `model/selection` | `provider`, `model`, `agent` (optional, only from `SetAgent`) | `SetModel`, `SetAgent` (spec #3) | No |
| `compaction/summary` | `summary`, `upto_seq`, `tokens_before`, `tokens_after`, `model` | Compaction | Yes (as a `user` message, `Q-013`) |

Derive rules (details in design, `API-STORE-010`):

- `tool/call` does not enter history. The `tool_use` block already exists in `assistant/message`.
- `system/message` does not enter `DeriveMessages` history. Its last message is used as `ChatRequest.System` (`Q-028`).
- The `allowed_session` grant has no kind of its own. A grant is an `approval/decided` with `outcome = allowed_session`. The grant pattern is computed from the `tool/call` referenced by `call_id`.

## Config

Read by the spec #2 loader. Validation here follows rule `REQ-CONF-003` of spec #2.

| Key | Type | Default | Validation |
|---|---|---|---|
| `[[modules]]` row `id = "store"`, `module = "sqlite_store"`, `config.path` | string | `lana.db` | Not empty. Relative to `LANA_HOME` if not absolute |
| `[agents.<name>] permission` | enum | `ask` | `trust`, `ask`, or `never`. Any other value is a validation error with the closest suggestion |
| `[agents.<name>.rules] allow` | string array | empty | Each item must pass `ParsePattern` (`API-PERM-002`) |
| `[agents.<name>.rules] deny` | string array | empty | Same as `allow` |
| `[agents.<name>] context_window` | integer | `128000` | Greater than 0 |

Validation of `rules` patterns uses `internal/pattern.ParsePattern` (`API-PERM-001`, `CQ-017`). Keys `permission` and `context_window` are listed in the spec #2 key list (`API-CONF-004`).

Example (does not replace the example in ADR 0004):

```toml
[agents.coder]
permission = "ask"
context_window = 128000

[agents.coder.rules]
allow = ["read_file", "bash(git status*)"]
deny  = ["write_file(~/.ssh/**)"]
```

New modules in the profile. All four modules are in base (`API-CONF-009` of spec #2, `CQ-031`, `Q-031`):

| Module (`module`) | Installed service | Requires | Hook |
|---|---|---|---|
| `sqlite_store` | `sessions` | — | `session.event` (observe, as sender) |
| `approval` | `approval` | `sessions` | `approval.request` (default: `unavailable`) |
| `permission` | — | `sessions`, `approval` | listener on `tools.pre_execute` |
| `compaction` | `compaction` | `sessions`, `llm` | listener on `agent.pre_step`, `llm.stream` |

## Go Signatures

This block defines the API shape. Package names follow ADR 0013.

### Core types (owned by spec #2)

The types used by this spec are defined in spec #2. This table records the revisions from revision 1:

| Type | Defined in | Revision in this spec |
|---|---|---|
| `core.NewSession` | `API-CORE-009` | Fields `Workspace` and `ParentCallID` added (`CQ-011`) |
| `core.EventDraft` | `API-CORE-008` | Replaces `NewEvent` (`CQ-024` of spec #2) |
| `core.SessionInfo` | `API-CORE-009` | Combined fields of spec #2 and spec #4 |
| `core.SessionFilter` | `API-CORE-009` | `Limit` 0 = no limit (`CQ-022` of spec #2) |
| `core.ApprovalRequest` | `API-CORE-011` | `ToolName`, `Label`. No `Input` and `Summary` (`CQ-021` of spec #2) |
| `core.ToolDecision` | `API-CORE-015` | `Allowed`, `Outcome`, `Status` (`ToolStatus`), `Reason` (`CQ-020` of spec #2) |
| `core.ToolsPreExecuteInput` | `API-CORE-015` | Input for the permission listener (`CQ-017` of spec #2) |
| `core.CompactionResult`, `core.Compactor` | `API-CORE-015` | No change |
| `core.ProviderError`, `core.ErrContextTooLong` | `API-CORE-015`, `API-CORE-014` | No change (`CQ-026` of spec #2) |
| `core.LeaseHeldError` | `API-CORE-014` | Replaces `ErrLeaseHeld` |
| `core.ErrPrefixTooShort`, `core.ErrAmbiguousPrefix`, `core.ResolvePrefix` | `API-CORE-014`, `API-CORE-015` | Moved from package `store` (`CQ-023` of spec #2) |
| `core.Lease` | Removed | `TryLease` returns `release func() error` (`CQ-023` of spec #2) |

The interfaces `core.SessionStore` and `core.ApprovalService` are in `API-CORE-009` and `API-CORE-011`.
Methods used by this spec:

```go
// core.SessionStore (API-CORE-009). Methods Info, ChainEvents, DeriveMessages added by this spec (CQ-022 of spec #2).
Create(ctx context.Context, s NewSession) (SessionInfo, error)
Open(ctx context.Context, id string) (SessionInfo, error) // with resume repair, requires lease
Info(ctx context.Context, id string) (SessionInfo, error) // read only, without repair
Append(ctx context.Context, id string, drafts []EventDraft) ([]SessionEvent, error)
Events(ctx context.Context, id string, fromSeq int64) iter.Seq2[SessionEvent, error]
ChainEvents(ctx context.Context, id string) ([]SessionEvent, error)
DeriveMessages(ctx context.Context, id string) ([]Message, error)
List(ctx context.Context, f SessionFilter) ([]SessionInfo, error)
FindPrefix(ctx context.Context, prefix string) ([]SessionInfo, error)
Fork(ctx context.Context, parentID string, atSeq int64) (SessionInfo, error)
TryLease(ctx context.Context, id string) (release func() error, err error)
```

Note for `Fork`: the child inherits `Agent`, `Channel`, `Address`, and `Workspace` from the parent. `ParentSessionID` and `InheritedSeq` are filled by the store. A child with a different agent (subagent, M4) is created through `Create` with `ParentSessionID` and `ParentCallID` (`CQ-011`).

### Package `store` (outside the core interface)

```go
func Open(ctx context.Context, path string) (*Store, error) // absolute path
func (s *Store) Close() error
func (s *Store) Prune(ctx context.Context, sessionID string) error
func (s *Store) RebuildProjections(ctx context.Context) error
func NormalizePayload(kind string, raw json.RawMessage) (json.RawMessage, error)

var (
	ErrSessionNotFound       = errors.New("session not found")
	ErrForkNotAtTurnBoundary = errors.New("fork is only allowed at turn/end")
	ErrTurnActive            = errors.New("a turn is still running")
	ErrLeaseNotHeld          = errors.New("session is not held by this process")
	ErrSessionHasChildren    = errors.New("session still has children")
	ErrSchemaTooNew          = errors.New("database schema is newer than the binary")
	ErrStoreClosed           = errors.New("store is closed")
	ErrSessionChanged        = errors.New("session changed during compaction")
	ErrNothingToCompact      // store sentinel. Message defined by implementation
	ErrInvalidPolicy         // store sentinel. Message defined by implementation
)

// ErrUnknownPayloadVersion = core.ErrUnknownPayloadVersion (API-CORE-014).
// ErrPrefixTooShort, ErrAmbiguousPrefix, and LeaseHeldError are in core (API-CORE-014).
```

### Rule patterns (`internal/pattern`, used by `config` and `tools/permission`)

```go
type Pattern struct {
	Raw  string
	Tool string
	Arg  *string // nil if the pattern has no argument
}

func ParsePattern(s string) (Pattern, error) // error includes position and reason
func (p Pattern) Match(tool string, arg Argument) bool

type ArgKind int // ArgCommand, ArgPath, ArgURL, ArgAgent, ArgRaw
type Argument struct {
	Kind  ArgKind
	Value string // path already resolved to absolute and cleaned
	Alt   string // actual path after EvalSymlinks, empty if not a path
}
```

Path matching (`REQ-PERM-001`): a `deny` rule matches if `Value` or `Alt` matches. An `allow` rule
matches only if both `Value` and `Alt` match.

### Package permission (`tools/permission`)

The `tools.pre_execute` listener is installed as `core.WaterfallListener[core.ToolsPreExecuteInput, core.ToolDecision]`.
Input is owned by spec #2 (`API-CORE-015`). Fields used: `AgentName`, and from `Call`:
`SessionID`, `TurnID`, `CallID`, `Name`, `Input` (`json.RawMessage`), `Risk`. `Label` is computed from
`AgentName` if the session has a parent (`ParentSessionID`), empty otherwise.

### Packages approval and compaction

```go
// package tools/permission (approval service)
// Implementation of core.ApprovalService. Clock and timeout can be replaced for tests.
// Default timeout 10 minutes.

// package agent/compaction
type Service struct{ /* ... */ }

// Implementation of core.Compactor (API-CORE-015). Installed with key core.ServiceCompaction.
func (s *Service) CompactNow(ctx context.Context, sessionID string) (core.CompactionResult, error)
```

### Library answerer (package `lana`)

```go
// See API-APPR-005.
func ApproveWith(fn func(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome) lana.Option
```

## Operation Index

| Operation ID | Type | Name | Requirements | Acceptance criteria |
|---|---|---|---|---|
| `API-STORE-001` | Go | Open store, migrations, DDL, triggers | `REQ-STORE-001`, `REQ-STORE-002`, `REQ-STORE-004`, `REQ-STORE-016` | `AC-STORE-001` to `AC-STORE-004`, `AC-STORE-007`, `AC-STORE-008`, `AC-STORE-038` |
| `API-STORE-002` | Go | `Create` | `REQ-STORE-006` | `AC-STORE-012` |
| `API-STORE-003` | Go | `Open` with resume repair, and `Info` (read only) | `REQ-STORE-006` | `AC-STORE-013` to `AC-STORE-016` |
| `API-STORE-004` | Go | `Append` | `REQ-STORE-003`, `REQ-STORE-005`, `REQ-STORE-015` | `AC-STORE-005` to `AC-STORE-006`, `AC-STORE-009` to `AC-STORE-011`, `AC-STORE-036`, `AC-STORE-037` |
| `API-STORE-005` | Go | `Events` | `REQ-STORE-008` | `AC-STORE-019` |
| `API-STORE-006` | Go | `List` | `REQ-STORE-008` | `AC-STORE-020` |
| `API-STORE-007` | Go | `FindPrefix` and `core.ResolvePrefix` | `REQ-STORE-009` | `AC-STORE-021` to `AC-STORE-023` |
| `API-STORE-008` | Go | `Fork` | `REQ-STORE-007` | `AC-STORE-017`, `AC-STORE-018` |
| `API-STORE-009` | Go | `TryLease` (returns `release`) | `REQ-STORE-010` | `AC-STORE-024` to `AC-STORE-027` |
| `API-STORE-010` | Go | `DeriveMessages` and `ChainEvents` | `REQ-STORE-011` | `AC-STORE-028` to `AC-STORE-030` |
| `API-STORE-011` | Go | `Prune` | `REQ-STORE-014` | `AC-STORE-034`, `AC-STORE-035` |
| `API-STORE-012` | Go | `RebuildProjections` | `REQ-STORE-012` | `AC-STORE-031` |
| `API-STORE-013` | Go | `NormalizePayload` | `REQ-STORE-013` | `AC-STORE-032`, `AC-STORE-033` |
| `API-PERM-001` | Go | `ParsePattern` and `Match` | `REQ-PERM-001` | `AC-PERM-001` to `AC-PERM-003` |
| `API-PERM-002` | Config | Validation of `permission` and `rules` | `REQ-PERM-001` | `AC-PERM-004` |
| `API-PERM-003` | Go | Listener on `tools.pre_execute` | `REQ-PERM-002` to `REQ-PERM-006` | `AC-PERM-005` to `AC-PERM-019` |
| `API-APPR-001` | Go | `SetPolicy` and `EffectivePolicy` | `REQ-APPR-001` | `AC-APPR-001` to `AC-APPR-004` |
| `API-APPR-002` | Go | `Request` (audit, timeout, cancel, fail-closed) | `REQ-APPR-002`, `REQ-APPR-004`, `REQ-APPR-005` | `AC-APPR-005`, `AC-APPR-006`, `AC-APPR-011` to `AC-APPR-014` |
| `API-APPR-003` | Go | Hook `approval.request` (answerer) | `REQ-APPR-003` | `AC-APPR-007` to `AC-APPR-010` |
| `API-APPR-004` | Go | Subagent permission bound function | `REQ-APPR-006` | `AC-APPR-015`, `AC-APPR-016` |
| `API-APPR-005` | Go | `lana.ApproveWith` (library answerer) | `REQ-APPR-007` | `AC-APPR-017` |
| `API-COMP-001` | Go | `CompactNow` | `REQ-COMP-002` | `AC-COMP-004`, `AC-COMP-005` |
| `API-COMP-002` | Go | Automatic compaction and retry | `REQ-COMP-001`, `REQ-COMP-003` | `AC-COMP-001` to `AC-COMP-003`, `AC-COMP-006`, `AC-COMP-007` |

## API-STORE-001: Open store, migrations, DDL, triggers

- **Purpose**: open `lana.db`, run migrations, and install the writer and read pool.
- **Requirements**: `REQ-STORE-001`, `REQ-STORE-002`, `REQ-STORE-004`, `REQ-STORE-016`
- **Acceptance criteria**: `AC-STORE-001` to `AC-STORE-004`, `AC-STORE-007`, `AC-STORE-008`, `AC-STORE-038`
- **Stability**: New
- **Authorization**: Process that owns `LANA_HOME`. No additional authentication.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Config | `[[modules]]` `store` `config.path` | string | No | Default `lana.db`. Relative to `LANA_HOME` |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| New database or existing one, migrations complete | Not applicable | `*Store` ready. `user_version` = 2. No events |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Path cannot be opened or folder does not exist | Not applicable | plain error | Yes | Boot fails, message names the path |
| `user_version` higher than the highest migration | Not applicable | `ErrSchemaTooNew` | No | Database not changed |
| Migration fails midway | Not applicable | plain error | Yes | Migration transaction rolled back, `user_version` not increased |

### Side effects and guarantees

- **State transition**: No events. Creates the file with `0600` if it does not exist, and creates `-wal` and `-shm`.
- **Idempotency**: Reopening does not run migrations again.
- **Concurrency**: Pragmas `journal_mode = WAL`, `synchronous = NORMAL`, `foreign_keys = ON`, `busy_timeout = 5000`.
- **Data exposure**: No secrets in the database (ADR 0005).

## API-STORE-002: `Create`

- **Purpose**: create a session and write `session/created`.
- **Requirements**: `REQ-STORE-006`
- **Acceptance criteria**: `AC-STORE-012`
- **Stability**: New
- **Authorization**: Process that owns the store.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `in` | `core.NewSession` | Yes | `Agent`, `Channel`, `Address`, `Workspace` are required and not empty. Parent is optional |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Session created | Not applicable | `core.SessionInfo` with a new ULID ID. Event `session/created` seq 1. Status `idle` |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Required field empty | Not applicable | plain error | No | Session not created |
| `ParentSessionID` does not exist | Not applicable | `ErrSessionNotFound` | No | Session not created |
| Store closed | Not applicable | `ErrStoreClosed` | Yes | No events |

### Side effects and guarantees

- **State transition**: New `sessions` row with status `idle`. Lease is not taken (`Q-015`).
- **Idempotency**: Not applicable. Each call creates a new session.
- **Concurrency**: Through the writer and `BEGIN IMMEDIATE`.
- **Data exposure**: None.

## API-STORE-003: `Open` with resume repair

- **Purpose**: open a session for the lease holder and close orphan turns.
- **Requirements**: `REQ-STORE-006`
- **Acceptance criteria**: `AC-STORE-013` to `AC-STORE-016`
- **Stability**: New
- **Authorization**: Process holding the lease for `session_id` (checks `pid` and `host` in `session_leases`).

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `sessionID` | string | Yes | Full ULID, not a prefix |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Session idle | Not applicable | `SessionInfo`. No events |
| Orphan turn closed | Not applicable | `SessionInfo` with status `idle`. Repair events in the order defined in design |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Process does not hold the lease | Not applicable | `ErrLeaseNotHeld` | Yes, after lease | No events |
| Session does not exist | Not applicable | `ErrSessionNotFound` | No | No events |

### Side effects and guarantees

- **State transition**: Orphan turn becomes `idle`. Event order: `approval/decided` for orphan approvals, error `tool/result` for orphan calls, then `turn/end` reason `interrupted`.
- **Info**: same as `Open` without a lease and without repair. Only reads `SessionInfo`.
- **Idempotency**: A second call writes no events.
- **Concurrency**: Repair is one `BEGIN IMMEDIATE` transaction. The lease must be held before repair.
- **Data exposure**: Repair output contains fixed text `interrupted`, without tool content.

## API-STORE-004: `Append`

- **Purpose**: write an atomic batch of events to a session.
- **Requirements**: `REQ-STORE-003`, `REQ-STORE-005`, `REQ-STORE-015`
- **Acceptance criteria**: `AC-STORE-005`, `AC-STORE-006`, `AC-STORE-009` to `AC-STORE-011`, `AC-STORE-036`, `AC-STORE-037`
- **Stability**: New
- **Authorization**: Process holding the lease. Append without a lease is allowed only from internal paths (repair, `Create`).

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `sessionID` | string | Yes | Session must exist |
| Argument | `drafts` | `[]core.EventDraft` | Yes | Not empty. `Kind` must be known. `turn/start` is rejected if a turn is open |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Batch stored | Not applicable | `[]core.SessionEvent` with contiguous seqs. Projection updated. Hook `session.event` sent after commit |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| `turn/start` while a turn is open | Not applicable | `ErrTurnActive` | No | No events |
| Unknown kind or invalid payload | Not applicable | plain error | No | No events |
| `SQLITE_BUSY` for more than 5 seconds | Not applicable | plain error | Yes | No events |
| Store closed | Not applicable | `ErrStoreClosed` | Yes | No events |

### Side effects and guarantees

- **State transition**: `turn/start` sets status `running`. `turn/end` for an open turn sets status `idle`. `turn_count` increases on `turn/start`.
- **Idempotency**: Not applicable. Each call adds events.
- **Concurrency**: Single writer per process. `BEGIN IMMEDIATE` across processes.
- **Data exposure**: Payloads are stored as is. The store adds no secrets.

## API-STORE-005: `Events`

- **Purpose**: read a session's own events starting from a given seq.
- **Requirements**: `REQ-STORE-008`
- **Acceptance criteria**: `AC-STORE-019`
- **Stability**: New
- **Authorization**: Process that owns the store.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `sessionID` | string | Yes | Session must exist |
| Argument | `fromSeq` | int64 | Yes | Minimum seq, inclusive. Values below 1 are read as 1 |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Session exists | Not applicable | `iter.Seq2[core.SessionEvent, error]` in ascending seq. Empty if there are no events |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Session does not exist | Not applicable | `ErrSessionNotFound` | No | No results |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Repeated calls produce the same result.
- **Concurrency**: Read from the read pool in one snapshot.
- **Data exposure**: Payloads are returned as is.

## API-STORE-006: `List`

- **Purpose**: list sessions per channel and address for `--continue` and `/sessions`.
- **Requirements**: `REQ-STORE-008`
- **Acceptance criteria**: `AC-STORE-020`
- **Stability**: New
- **Authorization**: Process that owns the store.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `f` | `core.SessionFilter` | Yes | `Channel` and `Address` match exactly. `Limit` 0 = no limit (`CQ-022` of spec #2). Negative `Limit` is rejected |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Results found | Not applicable | `[]core.SessionInfo` in descending `updated_at`, at most `Limit` (0 = all) |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Negative `Limit` | Not applicable | plain error | No | No results |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Repeated calls produce the same result if there are no writes.
- **Concurrency**: Read pool.
- **Data exposure**: None.

## API-STORE-007: `FindPrefix`

- **Purpose**: find sessions from a prefix typed by the user. The rule for selecting a single session is in `core.ResolvePrefix`.
- **Requirements**: `REQ-STORE-009`
- **Acceptance criteria**: `AC-STORE-021` to `AC-STORE-023`
- **Stability**: New
- **Authorization**: Process that owns the store.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `prefix` | string | Yes | Compared case-insensitively. Minimum length checked by `core.ResolvePrefix` |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Matches found | Not applicable | `[]core.SessionInfo` (may contain more than one). Caller calls `core.ResolvePrefix` |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---|---|---:|---|
| Prefix shorter than 6 characters (via `core.ResolvePrefix`) | Not applicable | `core.ErrPrefixTooShort` | No | No results |
| No match (via `core.ResolvePrefix`) | Not applicable | `core.ErrNotFound` | No | No results |
| More than one match (via `core.ResolvePrefix`) | Not applicable | `*core.AmbiguousPrefixError` with `Candidates` in ascending order, `Is(core.ErrAmbiguousPrefix)` | No | Candidate list returned to caller |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Repeated calls produce the same result.
- **Concurrency**: Read pool.
- **Data exposure**: None.

## API-STORE-008: `Fork`

- **Purpose**: create a derived session from the parent's `turn/end` without copying events.
- **Requirements**: `REQ-STORE-007`
- **Acceptance criteria**: `AC-STORE-017`, `AC-STORE-018`
- **Stability**: New
- **Authorization**: Process that owns the store. The caller above is responsible for the lease of the new session. `Fork` has no agent argument (`CQ-022` of spec #2).

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `parentID` | string | Yes | Session must exist |
| Argument | `atSeq` | int64 | Yes | Must be the seq of the parent's `turn/end` event |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| `atSeq` valid | Not applicable | New session `core.SessionInfo`. Event `session/created` contains `parent_session_id` and `inherited_seq`. Agent, channel, address, and workspace from the parent |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| `atSeq` is not `turn/end` | Not applicable | `ErrForkNotAtTurnBoundary` | No | No new session |
| Parent does not exist | Not applicable | `ErrSessionNotFound` | No | No new session |

### Side effects and guarantees

- **State transition**: New session is `idle`. Parent unchanged.
- **Idempotency**: Not applicable. Each call creates a new session.
- **Concurrency**: Through the writer.
- **Data exposure**: None.

## API-STORE-009: `TryLease`

- **Purpose**: give one session to one process (`session_leases`).
- **Requirements**: `REQ-STORE-010`
- **Acceptance criteria**: `AC-STORE-024` to `AC-STORE-027`
- **Stability**: New
- **Authorization**: Calling process. The lease is taken on behalf of `os.Getpid()` and `os.Hostname()`.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `id` | string | Yes | Session must exist |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Lease empty, or holder is dead on the same host | Not applicable | `release func() error`. `session_leases` row contains PID, host, time |
| `release` called | Not applicable | Lease row deleted if it still belongs to this process |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Holder alive on the same host, or holder on another host | Not applicable | `*core.LeaseHeldError{PID, Host}` (`errors.As`) | Yes, after release | Lease unchanged |
| Session does not exist | Not applicable | `ErrSessionNotFound` | No | No lease |

### Side effects and guarantees

- **State transition**: `session_leases` row is added or replaced. No events.
- **Idempotency**: A second call from the same process is also rejected with `LeaseHeldError`, because the holder's PID is still alive. This matches the design of spec #1.
- **Concurrency**: `BEGIN IMMEDIATE`. PID check via `syscall.Kill(pid, 0)`: `nil` or `EPERM` means alive, `ESRCH` means dead.
- **Data exposure**: PID and host are visible in the `LeaseHeldError` message.

## API-STORE-010: `DeriveMessages` and `ChainEvents`

- **Purpose**: build model history and the event chain from a session (used by the agent loop, permission, approval, and compaction).
- **Requirements**: `REQ-STORE-011`
- **Acceptance criteria**: `AC-STORE-028` to `AC-STORE-030`
- **Stability**: New
- **Authorization**: Process that owns the store.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `sessionID` | string | Yes | Session must exist |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Session exists | Not applicable | `[]core.Message` from `core.DeriveMessages` (`API-CORE-015`) applied to `ChainEvents`, following the mapping in design (`Q-012`, `Q-013`) |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Session does not exist | Not applicable | `ErrSessionNotFound` | No | No results |
| Unknown payload version | Not applicable | `core.ErrUnknownPayloadVersion` | No | Derive fails, no partial results |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Pure function of the log contents. Same result if the log is the same.
- **Concurrency**: One read transaction for the whole chain.
- **Data exposure**: Model content. Not written to the log.

## API-STORE-011: `Prune`

- **Purpose**: delete one whole session with its events, projection, and lease.
- **Requirements**: `REQ-STORE-014`
- **Acceptance criteria**: `AC-STORE-034`, `AC-STORE-035`
- **Stability**: New. Store function only. No CLI command (`Q-010`).
- **Authorization**: Process that owns the store. The caller must hold the session lease.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `sessionID` | string | Yes | Session must exist and have no children |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Session without children | Not applicable | Events, `sessions` row, and session lease removed in one transaction |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Session has children | Not applicable | `ErrSessionHasChildren` | No | Nothing deleted |
| Session does not exist | Not applicable | `ErrSessionNotFound` | No | No change |

### Side effects and guarantees

- **State transition**: Session removed. This is the only exception to append-only.
- **Idempotency**: A second call returns `ErrSessionNotFound`.
- **Concurrency**: One `BEGIN IMMEDIATE` transaction. `session_prune_allow` is filled and deleted within that transaction.
- **Data exposure**: None.

## API-STORE-012: `RebuildProjections`

- **Purpose**: rebuild the `sessions` table from `session_events`.
- **Requirements**: `REQ-STORE-012`
- **Acceptance criteria**: `AC-STORE-031`
- **Stability**: New
- **Authorization**: Process that owns the store.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | (none) | — | — | — |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Rebuild complete | Not applicable | `sessions` table contains results derived from the events |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Payload cannot be decoded | Not applicable | `core.ErrUnknownPayloadVersion` | No | Rebuild rolled back |

### Side effects and guarantees

- **State transition**: Only the `sessions` table. No events.
- **Idempotency**: Same result for the same log.
- **Concurrency**: One `BEGIN IMMEDIATE` transaction.
- **Data exposure**: None.

## API-STORE-013: `NormalizePayload`

- **Purpose**: decode a payload to the current version in memory.
- **Requirements**: `REQ-STORE-013`
- **Acceptance criteria**: `AC-STORE-032`, `AC-STORE-033`
- **Stability**: New
- **Authorization**: Not applicable. Pure function.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `kind` | string | Yes | A known kind |
| Argument | `raw` | `json.RawMessage` | Yes | Must contain integer `v` |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| `v` known | Not applicable | Payload at the current version. Database row not changed |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| `v` unknown | Not applicable | `core.ErrUnknownPayloadVersion` | No | No results |
| `v` missing or not an integer | Not applicable | plain error | No | No results |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Repeated calls produce the same result.
- **Concurrency**: Not applicable.
- **Data exposure**: None.

## API-PERM-001: `ParsePattern` and `Match`

- **Purpose**: parse and match a pattern `tool` or `tool(arg-glob)`.
- **Requirements**: `REQ-PERM-001`
- **Acceptance criteria**: `AC-PERM-001` to `AC-PERM-003`
- **Stability**: New
- **Authorization**: Not applicable. Pure function.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `s` | string | Yes | Format `tool` or `tool(...)`. Tool name `[a-z_][a-z0-9_]*` or `mcp__<server>__<tool>` |

Pattern grammar:

```text
pattern  := tool | tool "(" arg ")"
tool     := tool name, matches exactly
arg      := remaining text up to the last ")", may contain "(" and ")"
wildcard := "*" (sequence of characters) | "?" (one character) | "**" (path arguments only)
escape   := "\" followed by "*", "?", "\", "(", or ")" then that character literally
```

Matching rules (`REQ-PERM-001`):

| Argument | `*` and `?` | `**` | Value source |
|---|---|---|---|
| `command` | Crosses `/` | Not applicable | `command` input as is |
| `path` | Does not cross `/` | Crosses `/` | Absolute path from `filepath.Clean`, `~` replaced with home |
| `url`, `agent`, `raw` | Matches any character | Not applicable | Input as is, compact JSON for `raw` |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Valid pattern | Not applicable | `Pattern` |
| `Match` | Not applicable | `true` if the tool is the same and the argument matches |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Unclosed parenthesis | Not applicable | plain error (position and reason) | No | Config rejected |
| Invalid tool name | Not applicable | plain error | No | Config rejected |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Pure function.
- **Concurrency**: Not applicable.
- **Data exposure**: None.

## API-PERM-002: Validation of `permission` and `rules`

- **Purpose**: ensure permission config is valid when loaded.
- **Requirements**: `REQ-PERM-001`
- **Acceptance criteria**: `AC-PERM-004`
- **Stability**: New
- **Authorization**: Not applicable.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Config | `agents.<name>.permission` | enum | No | Default `ask` |
| Config | `agents.<name>.rules.allow`, `.deny` | string array | No | Each item passes `ParsePattern` |
| Config | `agents.<name>.context_window` | integer | No | Default 128000, greater than 0 |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| All values valid | Not applicable | Config accepted |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Invalid pattern | Not applicable | spec #2 validation error | No | Error names the key (for example `agents.coder.rules.allow`), the pattern value, and the reason |
| `permission` outside the enum | Not applicable | spec #2 validation error | No | Error names the key and the closest suggestion |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Not applicable.
- **Concurrency**: Not applicable.
- **Data exposure**: None.

## API-PERM-003: Listener on `tools.pre_execute`

- **Purpose**: decide whether a tool may run.
- **Requirements**: `REQ-PERM-002` to `REQ-PERM-006`
- **Acceptance criteria**: `AC-PERM-005` to `AC-PERM-019`
- **Stability**: New. Security-sensitive
- **Authorization**: Decisions are formed only in this listener. No other path creates `ToolDecision.Allowed = true` for a tool that passed through the hook.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Hook | `core.ToolsPreExecuteInput` | struct (spec #2, `API-CORE-015`) | Yes | `AgentName`, `Call.SessionID`, `Call.TurnID`, `Call.CallID`, `Call.Name`, `Call.Input`, `Call.Risk` |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| No deny match, workspace safe, and one allow path matches | Not applicable | `ToolDecision{Allowed: true}`. Outcome empty, or `allowed_once` or `allowed_session` if approval was used |
| Rejected by rule, workspace, policy, or approval outcome | Not applicable | `ToolDecision{Allowed: false, Status: ToolStatusRejected, Reason}` |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Store read fails (policy, grant, session) | Not applicable | plain error | Yes | `Allowed: false`, `Reason` names the read failure |
| `EvalSymlinks` fails for reasons other than a missing path | Not applicable | plain error | Yes | `Allowed: false` |
| `permission` module not active | Not applicable | spec #3 default (fail-closed) | No | Tool rejected |

### Side effects and guarantees

- **State transition**: May write `approval/asked` and `approval/decided` through `API-APPR-002`.
- **Idempotency**: Not applicable. Each `tool/call` is decided once.
- **Concurrency**: Reads the latest policy each time (`Q-014`). The result of one call does not affect other calls in the same turn except through the log.
- **Data exposure**: `Reason` names the tool and the matched pattern. Does not copy tool arguments (ADR 0004).

## API-APPR-001: `SetPolicy` and `EffectivePolicy`

- **Purpose**: set and read the session policy.
- **Requirements**: `REQ-APPR-001`
- **Acceptance criteria**: `AC-APPR-001` to `AC-APPR-004`
- **Stability**: New
- **Authorization**: Process that owns the store and callers from the session owner's channel (CLI `/permission`, `/yolo`, `--yolo`). No other path.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `sessionID` | string | Yes | Session must exist |
| Argument | `policy` | `core.Policy` | Yes (`SetPolicy`) | `trust`, `ask`, or `never` |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| `SetPolicy` | Not applicable | `approval/policy` event written |
| `EffectivePolicy` | Not applicable | Last `approval/policy` value in the chain. If none, the agent's `permission` |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Invalid policy value | Not applicable | `ErrInvalidPolicy` | No | No event |
| Session does not exist | Not applicable | `ErrSessionNotFound` | No | No event |

### Side effects and guarantees

- **State transition**: `approval/policy` recorded. Takes effect from the next tool evaluation.
- **Idempotency**: Not applicable. Each `SetPolicy` adds an event.
- **Concurrency**: Through the writer.
- **Data exposure**: None.

## API-APPR-002: `Request` (audit, timeout, cancel, fail-closed)

- **Purpose**: request an approval answer and record its audit trail.
- **Requirements**: `REQ-APPR-002`, `REQ-APPR-004`, `REQ-APPR-005`
- **Acceptance criteria**: `AC-APPR-005`, `AC-APPR-006`, `AC-APPR-011` to `AC-APPR-014`
- **Stability**: New. Security-sensitive
- **Authorization**: Called only by the `tools.pre_execute` listener in API-PERM-003.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `req` | `core.ApprovalRequest` | Yes | `CallID` must reference an existing `tool/call` in the session |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Answerer answers with a valid outcome | Not applicable | Outcome from the answerer. `approval/asked` and `approval/decided` stored |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| `approval/asked` fails to write | Not applicable | plain error | Yes | Answerer not called. Outcome `unavailable` |
| 10 minute deadline passed | Not applicable | `unavailable` (reason `timeout`) | No | `approval/decided` written |
| Turn context cancelled | Not applicable | `cancelled` | No | `approval/decided` written |
| `approval/decided` fails to write | Not applicable | plain error | Yes | Outcome treated as `unavailable`. Tool does not run |

### Side effects and guarantees

- **State transition**: `approval/asked` then `approval/decided`. Exactly one `approval/decided` per `request_id`.
- **Idempotency**: Not applicable. Each request has a new ULID `request_id`.
- **Concurrency**: `approval/decided` is written with a non-cancelling context with a 5 second limit, so the audit is stored even when the turn is cancelled.
- **Data exposure**: No tool arguments in `approval/asked`. The `info` log level records only `call_id` and outcome.

## API-APPR-003: Hook `approval.request` (answerer)

- **Purpose**: give a channel the chance to answer approvals for sessions it owns.
- **Requirements**: `REQ-APPR-003`
- **Acceptance criteria**: `AC-APPR-007` to `AC-APPR-010`
- **Stability**: New
- **Authorization**: An answerer only answers sessions it owns. An answerer that does not own the session calls `next`.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Hook | `core.ApprovalRequest` | struct (spec #2, `API-CORE-011`) | Yes | No tool arguments |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Answerer owns the session and answers | Not applicable | `core.ApprovalOutcome` from the closed set: `allowed_once`, `allowed_session`, `rejected`, `cancelled`, `unavailable` |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| No answerer | Not applicable | `unavailable` (default handler) | No | Tool rejected |
| Answerer returns an error | Not applicable | `unavailable` | No | Next listener does not run |
| Outcome outside the closed set | Not applicable | `unavailable` | No | Tool rejected |

### Side effects and guarantees

- **State transition**: None. Audit writing is owned by API-APPR-002.
- **Idempotency**: Not applicable.
- **Concurrency**: Waterfall per spec #2 (`REQ-HOOK-001`).
- **Data exposure**: The answerer may read `tool/call` through `call_id`. The store does not send arguments through the hook.

## API-APPR-004: Subagent permission bound function

- **Purpose**: compute a child's policy that does not exceed the parent's.
- **Requirements**: `REQ-APPR-006`
- **Acceptance criteria**: `AC-APPR-015`, `AC-APPR-016`
- **Stability**: New. Used by the `task` tool in the subagent spec (M4).
- **Authorization**: Not applicable. Pure function.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `parent`, `child` | `core.Policy` | Yes | Valid values |
| Argument | `parentDeny`, `childDeny` | `[]Pattern` | Yes | Result of `ParsePattern` |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Computed | Not applicable | Strictest policy. Merged deny list |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Invalid policy | Not applicable | `ErrInvalidPolicy` | No | No results |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Pure function.
- **Concurrency**: Not applicable.
- **Data exposure**: None.

## API-APPR-005: `lana.ApproveWith` (library answerer)

- **Purpose**: give approval answers for `library` channel sessions through a Go function.
- **Requirements**: `REQ-APPR-007`
- **Acceptance criteria**: `AC-APPR-017`
- **Stability**: New. Security-sensitive
- **Authorization**: Installed through the `lana.New` option (spec #3 `API-LIB-001`). Answers only sessions with channel `library`. Sessions on other channels are passed on with `next`.

```go
package lana

// ApproveWith registers an answerer for the library channel on the core.ApprovalRequest hook.
// This option is installed through New(ctx, opts, ApproveWith(fn)).
func ApproveWith(fn func(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome) Option
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `fn` | func | Yes | Called for each approval request in a `library` channel session. `nil` means there is no answerer, so the result is `unavailable` (fail-closed) |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Session on channel `library` | — | Outcome from `fn` is used (`API-APPR-003`). Audit written by `API-APPR-002` |
| Session on another channel | — | `fn` is not called. Listener calls `next` |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| `fn` panics | — | `unavailable` | No | Panic caught with `recover`. Tool rejected |
| `fn` returns an outcome outside the closed set | — | `unavailable` | No | Tool rejected (`API-APPR-003`) |

### Side effects and guarantees

- **State transition**: None. Audit writing is owned by `API-APPR-002`.
- **Idempotency**: Not applicable.
- **Concurrency**: `fn` may be called in parallel for `Read` tools. `fn` must be safe for use by many goroutines.
- **Data exposure**: `fn` may read `tool/call` through `call_id`. The store does not send arguments through the hook.

### Compatibility

- **Breaking change**: No. New API.
- **Deprecation**: Not applicable.

## API-COMP-001: `CompactNow`

- **Purpose**: summarize a session's history on the owner's request (`/compact`).
- **Requirements**: `REQ-COMP-002`
- **Acceptance criteria**: `AC-COMP-004`, `AC-COMP-005`
- **Stability**: New
- **Authorization**: Called from the session owner's channel (spec #1) through `core.Compactor` (`API-CORE-015`). Spec #3 has no `CompactNow` (`CQ-019` of spec #3).

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `sessionID` | string | Yes | Session must exist and have no open turn |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Summary written | Not applicable | `core.CompactionResult` with `UptoSeq`, `TokensBefore`, `TokensAfter`. `compaction/summary` event written |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Turn is running | Not applicable | `ErrTurnActive` | Yes, after the turn ends | No events |
| Session has no events | Not applicable | `ErrNothingToCompact` | No | No events |
| Summary fails from the provider | Not applicable | provider error | Yes | No events |
| `last_seq` changed since it was read | Not applicable | `ErrSessionChanged` | Yes | No events |

### Side effects and guarantees

- **State transition**: `compaction/summary` event written. Old events are not deleted.
- **Idempotency**: Not applicable. Each call creates a new summary.
- **Concurrency**: Optimistic: `last_seq` is verified against the value when the summary was created, in the same append transaction. This keeps the invariant of `REQ-STORE-005`.
- **Data exposure**: Summary contents are stored in the log. Not written to the `info` log level.

## API-COMP-002: Automatic compaction and retry

- **Purpose**: keep the model context under the limit and retry once when the provider rejects it as too long.
- **Requirements**: `REQ-COMP-001`, `REQ-COMP-003`
- **Acceptance criteria**: `AC-COMP-001` to `AC-COMP-003`, `AC-COMP-006`, `AC-COMP-007`
- **Stability**: New
- **Authorization**: Hook listener of the `compaction` module.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Config | `agents.<name>.context_window` | integer | No | Default 128000 |
| Hook | `agent.pre_step` | input from spec #3 | Yes | Active session and turn |
| Hook | `llm.stream` error | `core.ErrContextTooLong` | Yes (retry) | Error from the provider |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Context tokens above 70% | Not applicable | `compaction/summary` written before `step/start` |
| Retry succeeds after `ErrContextTooLong` | Not applicable | Second request returns a result. Turn continues |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Automatic summary fails | Not applicable | Log level `warn` | Yes, next step | Turn continues without compaction |
| Retry also returns `ErrContextTooLong` | Not applicable | `core.ErrContextTooLong` | No | Turn ends with `turn/end` reason `error` |

### Side effects and guarantees

- **State transition**: `compaction/summary` event written within the turn. `upto_seq` = seq exactly before the `step/start` of the active step.
- **Idempotency**: Not applicable. Retry happens only once per step.
- **Concurrency**: Runs in the turn goroutine. The summary uses the provider adapter directly, not the `llm.stream` hook (`Q-009`).
- **Data exposure**: Same as API-COMP-001.

## Contract Traceability

| Operation | Requirements | Acceptance criteria | Design component | Verification |
|---|---|---|---|---|
| `API-STORE-001` | `REQ-STORE-001`, `REQ-STORE-002`, `REQ-STORE-004`, `REQ-STORE-016` | `AC-STORE-001` to `AC-STORE-004`, `AC-STORE-007`, `AC-STORE-008`, `AC-STORE-038` | `store.Open`, migrator | `store` test in `t.TempDir()` |
| `API-STORE-002` | `REQ-STORE-006` | `AC-STORE-012` | `store.writer`, projection | `store` test |
| `API-STORE-003` | `REQ-STORE-006` | `AC-STORE-013` to `AC-STORE-016` | `repair`, `session_leases` | `store` test and crash simulation |
| `API-STORE-004` | `REQ-STORE-003`, `REQ-STORE-005`, `REQ-STORE-015` | `AC-STORE-005`, `AC-STORE-006`, `AC-STORE-009` to `AC-STORE-011`, `AC-STORE-036`, `AC-STORE-037` | `store.writer`, observe hook | Concurrency test, two-instance test |
| `API-STORE-005` | `REQ-STORE-008` | `AC-STORE-019` | read pool | `store` test |
| `API-STORE-006` | `REQ-STORE-008` | `AC-STORE-020` | read pool, index | `store` test |
| `API-STORE-007` | `REQ-STORE-009` | `AC-STORE-021` to `AC-STORE-023` | read pool | `store` test |
| `API-STORE-008` | `REQ-STORE-007` | `AC-STORE-017`, `AC-STORE-018` | `store.writer` | `store` test |
| `API-STORE-009` | `REQ-STORE-010` | `AC-STORE-024` to `AC-STORE-027` | lease | `store` test with fake PID |
| `API-STORE-010` | `REQ-STORE-011` | `AC-STORE-028` to `AC-STORE-030` | derive, chain | Golden test |
| `API-STORE-011` | `REQ-STORE-014` | `AC-STORE-034`, `AC-STORE-035` | prune | `store` test |
| `API-STORE-012` | `REQ-STORE-012` | `AC-STORE-031` | projection | `store` test |
| `API-STORE-013` | `REQ-STORE-013` | `AC-STORE-032`, `AC-STORE-033` | payload | Version fixture test |
| `API-PERM-001` | `REQ-PERM-001` | `AC-PERM-001` to `AC-PERM-003` | pattern | Unit and property tests |
| `API-PERM-002` | `REQ-PERM-001` | `AC-PERM-004` | config validation | Config test |
| `API-PERM-003` | `REQ-PERM-002` to `REQ-PERM-006` | `AC-PERM-005` to `AC-PERM-019` | evaluator, workspace, grant | Unit and integration tests |
| `API-APPR-001` | `REQ-APPR-001` | `AC-APPR-001` to `AC-APPR-004` | policy | `store` and approval tests |
| `API-APPR-002` | `REQ-APPR-002`, `REQ-APPR-004`, `REQ-APPR-005` | `AC-APPR-005`, `AC-APPR-006`, `AC-APPR-011` to `AC-APPR-014` | approval service | Test with fake clock |
| `API-APPR-003` | `REQ-APPR-003` | `AC-APPR-007` to `AC-APPR-010` | waterfall | Hook test |
| `API-APPR-004` | `REQ-APPR-006` | `AC-APPR-015`, `AC-APPR-016` | bound function | Unit and integration tests |
| `API-APPR-005` | `REQ-APPR-007` | `AC-APPR-017` | library answerer (`lana`) | Integration test with `lana.New` and MockLlm |
| `API-COMP-001` | `REQ-COMP-002` | `AC-COMP-004`, `AC-COMP-005` | compaction service | Test with fake `llm` |
| `API-COMP-002` | `REQ-COMP-001`, `REQ-COMP-003` | `AC-COMP-001` to `AC-COMP-003`, `AC-COMP-006`, `AC-COMP-007` | compaction listener | Test with fake `llm` |

## Blocking Contract Questions

| ID | Question | Controls | Requested reference | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `CQ-001` | Shape of `Lease`: a `Release` function or a separate token | `API-STORE-009` | None | Ridho | Resolved | Agent recommendation: `core.Lease` with method `SessionID()` and `Release(ctx)`. Simplest, and consistent with spec #1, which releases the lease when switching sessions. Revised: see `CQ-012`. |
| `CQ-002` | Signature of `Open` and `Create` | `API-STORE-002`, `API-STORE-003` | None | Ridho | Resolved | Agent recommendation: `Open` returns `SessionInfo`. `Create` returns the ID only. No in-memory session handle. Revised: see `CQ-012`. |
| `CQ-003` | Output of hook `tools.pre_execute` | `API-PERM-003` | Confirmation from spec #3 | Ridho | Resolved | Agent recommendation: `core.ToolDecision`. The listener does not call `next`, so the fail-closed default does not run when a decision exists. |
| `CQ-004` | Fields of `core.ApprovalRequest` | `API-APPR-002`, `API-APPR-003` | None | Ridho | Resolved | Agent recommendation: `SessionID`, `TurnID`, `CallID`, `Tool`, `Label`, `Risk`. No arguments (ADR 0004). Revised: see `CQ-011` (`Tool` becomes `ToolName`). |
| `CQ-005` | Signature of `Compactor` and its result | `API-COMP-001` | None | Ridho | Resolved | Agent recommendation: `CompactNow(ctx, sessionID)` returns `core.CompactionResult`. Spec #1 uses `core.Compactor.CompactNow(ctx, session)`. The delegation proposal from spec #3 was withdrawn (see `CQ-014`). |
| `CQ-006` | Error class for context too long | `API-COMP-002` | Confirmation from spec #3 | Ridho | Resolved | Agent recommendation: sentinel `core.ErrContextTooLong` wrapped by the provider adapter so that `errors.Is` matches. |
| `CQ-007` | Fields of `approval/*` and `compaction/summary` payloads | Event contract | Confirmation from spec #2 | Ridho | Resolved | Agent recommendation: fields in the event contract table. If structs do not exist in `core`, they are added as core additions owned by spec #4. Revised: fields owned by spec #2 (`CQ-016`). |
| `CQ-008` | Size and meaning of the `sessions` projection columns | `API-STORE-001` | None | Ridho | Resolved | Agent recommendation: `workspace` and `turn_count` added compared with ADR 0009. Both are needed by spec #1 (`/sessions`, `session/created` workspace). |
| `CQ-009` | Default `context_window` | `API-PERM-002`, `API-COMP-002` | None | Ridho | Resolved | Agent recommendation: 128000, per agent. See `Q-006` in requirements. |
| `CQ-010` | Profile that loads modules `permission` and `approval` | Module config | Confirmation from spec #2 and ARCHITECTURE §3 | Ridho | Resolved | Agent recommendation: base profile and `chat` (`minimal` plus channel cli) must load `permission`, `approval`, and `compaction`. The `minimal` profile in ARCHITECTURE §3 needs revision. Without this, `chat` falls back to fail-closed. |
| `CQ-011` | `core` types used by spec #4 | Go Signatures section | Ridho | Resolved | Agent recommendation (reconciliation): types `NewSession`, `SessionInfo`, `ApprovalRequest`, `ToolDecision`, `CompactionResult`, `Compactor`, `ErrContextTooLong` moved to `API-CORE-015`, `API-CORE-009`, and `API-CORE-011` (spec #2). Changes: `NewEvent` becomes `EventDraft`. `Lease` removed. `ApprovalRequest.Tool` becomes `ToolName`, `Input` and `Summary` removed, `Label` added. Fields `NewSession.Workspace` and `ParentCallID` kept. |
| `CQ-012` | Signature of `SessionStore` and `TryLease` | `API-STORE-002`, `API-STORE-003`, `API-STORE-005`, `API-STORE-006`, `API-STORE-007`, `API-STORE-008`, `API-STORE-009` | Ridho | Resolved | Agent recommendation (reconciliation): signature of spec #2 wins (`CQ-022` of spec #2). `Create` and `Open` return `SessionInfo` (revises `CQ-002`). `TryLease` returns `release func() error` (revises `CQ-001`). `Events` returns an iterator. `List` uses `SessionFilter`. `FindPrefix` returns a list. `Fork` without the `in` argument. |
| `CQ-013` | Model history and `system/message` | `API-STORE-010`, event table | Ridho | Resolved | Agent recommendation (reconciliation): `DeriveMessages` (store) wraps `core.DeriveMessages` (`API-CORE-015`, `CQ-028` of spec #2). `system/message` does not enter history. Its last message is used as `ChatRequest.System` (`Q-028`). Compaction stays as a `user` message (`Q-013`). |
| `CQ-014` | `CompactNow` and `/compact` | `API-COMP-001` | Ridho | Resolved | Agent recommendation (reconciliation): `core.Compactor.CompactNow` from this spec is used by `/compact` in spec #1. Spec #3 has no `CompactNow` (`CQ-019` of spec #3). The delegation proposal from spec #3 was withdrawn. |
| `CQ-015` | Context too long and retry | `API-COMP-002`, `API-PROV-003` of spec #3 | Ridho | Resolved | Agent recommendation (reconciliation): the adapter creates `*core.ProviderError` of class `context_length` that matches `errors.Is(err, core.ErrContextTooLong)` (`CQ-026` of spec #2). The compaction `llm.stream` listener retries once. Without this module, the turn ends with `error` (`Q-005` of spec #3). |
| `CQ-016` | Owner of payload fields and `workspace`, `agent` | `API-STORE-002`, event table | Ridho | Resolved | Agent recommendation (reconciliation): field table in `API-CORE-008` (spec #2, `CQ-029`). `session/created.workspace` and `model/selection.agent` are approved. |
| `CQ-017` | Location of the `rules` pattern grammar | `API-PERM-001`, `API-PERM-002` | Ridho | Resolved | Agent recommendation (reconciliation): package `internal/pattern` (`DQ-015` of spec #2). Used by `config` for validating `rules` and by `tools/permission` for evaluation. Grammar unchanged. |
| `CQ-018` | Contents of the base profile | `API-PERM-003`, `API-APPR-001`, `API-COMP-002` | Ridho | Resolved | Agent recommendation (reconciliation): `sqlite_store`, `permission`, `approval`, and `compaction` are in base (`API-CONF-009` of spec #2, `CQ-031`). `minimal` equals base. Revises `CQ-010`: `chat` and `run` load them through base. |
| `CQ-019` | Library answerer `lana.ApproveWith` | `API-APPR-005`, `REQ-APPR-007` | Ridho | Resolved | Agent recommendation (reconciliation): `lana.ApproveWith(fn)` is an `Option` (spec #3 `API-LIB-001`). Answerer specific to the `library` channel. Added as `REQ-APPR-007`, `AC-APPR-017`, `API-APPR-005`, and `T-021`. |
| `CQ-020` | Input of hook `tools.pre_execute` | `API-PERM-003` | Ridho | Resolved | Agent recommendation (reconciliation): input `core.ToolsPreExecuteInput` (spec #2, `CQ-017`), output `core.ToolDecision`. `Label` is computed from `AgentName` if the session has a parent. |
| `CQ-021` | Sentinels that moved to `core` | `API-STORE-007`, `API-STORE-009` | Ridho | Resolved | Agent recommendation (reconciliation): `ErrPrefixTooShort` and `ErrAmbiguousPrefix` moved to `core` (`core.ResolvePrefix`). `ErrLeaseHeld` becomes `*core.LeaseHeldError` used with `errors.As`. `ErrUnknownPayloadVersion` is in `core`. Behavior unchanged. |
| `CQ-022` | Limit on the session list | `API-STORE-006` | Ridho | Resolved | Agent recommendation (reconciliation): `SessionFilter.Limit` 0 = no limit (spec #2). The 1 to 200 limit from revision 1 is removed from the contract. Requirements do not mention that limit, so requirements are unchanged. |

None.

## Approval

- [x] Requirements are Approved and their revisions are recorded above.
- [x] Contract applicability is determined.
- [x] Every operation has a stable ID and links to requirements and acceptance criteria.
- [x] Input, success, failure, authorization, and compatibility are explicit.
- [x] No behavior exists only in this file.
- [x] Blocking Contract Questions is `None`.
- **Approved by**: Ridho (gate released 2026-10-29)
- **Approved on**: 2026-10-29 (revision 1). Revision 2 reconciled by the agent on behalf of the owner (gate released 2026-10-29).
