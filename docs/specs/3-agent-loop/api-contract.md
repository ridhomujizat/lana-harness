# Contract: Agent loop, provider, tools, Go library

- **Issue**: [`#3`](https://github.com/ridhomujizat/lana-harness/issues/3)
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 2
- **Requirements status**: Approved
- **Contract revision**: 2 (cross-spec reconciliation, see `CQ-011` through `CQ-019`)
- **Applies to**: Go API (packages `lana` and `agent`), config keys `[providers.<name>]` and `[agents.<name>]`, tool input schemas, session log event payloads, MockLlm script format, `rt.Stream` event types
- **Status**: Approved

This file defines the exact shape of the interfaces. Behavior is owned by `requirements.md`.
The `core` package (interfaces, hooks, message types, and composite types) is owned by spec #2 (`API-CORE-015`).
This spec does not define `core` types. Types used by this spec are referenced by name.

## Shared conventions

| Topic | Decision |
|---|---|
| Exit code | Not applicable in this package. The CLI (spec #1) maps provider errors to exit `3` (`REQ-EXIT-001` of spec #1) |
| stdout and stderr output | Not applicable. This package does not write to stdout or stderr |
| Log | `log/slog`. Level `info` records session ID, turn ID, step count, provider, model, reason, and duration. Message content, model text, and tool output are not recorded at level `info` |
| Config format | TOML. Tables `[providers.<name>]`, `[agents.<name>]`, and `[[routes]]` are read through the spec #2 loader |
| Secret | Read through `api_key_env` from the process env, then `secrets.env` (spec #2). Never printed. The header `Authorization: Bearer <value>` is only sent to the provider's `base_url` |
| Version | Every event payload uses field `v` with value `1`. Go API changes before `v1.0.0` are recorded in CHANGELOG (ADR 0014) |
| Serialization | JSON UTF-8, `snake_case` fields. Session and turn IDs are 26-character ULIDs (spec #1 convention). Event time is filled by the store in `core.SessionEvent.CreatedAt` |
| Go errors | Sentinel errors checked with `errors.Is`. Provider errors are `*core.ProviderError` (alias `lana.ProviderError`) with field `Class` (`API-CORE-015`) |
| Turn stop reason | `end_turn`, `max_steps`, `cancelled`, `error`. The value `interrupted` is provided by spec #4 (ADR 0009) |
| Tool status | `success`, `error`, `rejected` (same as spec #1 `API-CLI-007`) |
| Model format | `<provider>:<model>`, for example `openai:gpt-5-mini` |

## Operation index

| Operation ID | Kind | Name | Requirements | Acceptance criteria |
|---|---|---|---|---|
| `API-LIB-001` | Go | `lana.New`, `lana.Options` | `REQ-LIB-001`, `REQ-LIB-006`, `REQ-LIB-008` | `AC-LIB-001`, `AC-LIB-002`, `AC-LIB-003`, `AC-LIB-013`, `AC-LIB-014` |
| `API-LIB-002` | Go | `(*Runtime).Run` | `REQ-LIB-002`, `REQ-LIB-007` | `AC-LIB-004` through `AC-LIB-008`, `AC-LIB-015`, `AC-LIB-016` |
| `API-LIB-003` | Go | `(*Runtime).Stream` | `REQ-LIB-003` | `AC-LIB-009`, `AC-LIB-010` |
| `API-LIB-004` | Go | `(*Runtime).Close` | `REQ-LIB-004` | `AC-LIB-011` |
| `API-LIB-005` | Config | Module rows of profiles `minimal` and `headless` | `REQ-LIB-008` | `AC-LIB-017` |
| `API-AGENT-001` | Go | `(*Registry).Send` | `REQ-LOOP-001`, `REQ-LOOP-002` | `AC-LOOP-001` through `AC-LOOP-006` |
| `API-AGENT-002` | Go | `(*Registry).Start` | `REQ-LOOP-002`, `REQ-LIB-002` | `AC-LIB-005` |
| `API-AGENT-003` | Go | `(*Registry).Cancel` | `REQ-LOOP-007` | `AC-LOOP-019` through `AC-LOOP-021` |
| `API-AGENT-004` | Go | `(*Registry).SetAgent` | `REQ-LOOP-010` | `AC-LOOP-028` |
| `API-AGENT-005` | Go | `(*Registry).SetModel` | `REQ-LOOP-010` | `AC-LOOP-029`, `AC-LOOP-030` |
| `API-AGENT-006` | Go | `(*Registry).SubscribeLive` | `REQ-LOOP-009` | `AC-LOOP-026`, `AC-LOOP-027` |
| `API-AGENT-007` | Config | `[agents.<name>]` (additional keys) | `REQ-LOOP-004`, `REQ-LOOP-005`, `REQ-LOOP-008`, `REQ-LOOP-010` | `AC-LOOP-011`, `AC-LOOP-015`, `AC-LOOP-016`, `AC-LOOP-023`, `AC-LOOP-031` |
| `API-AGENT-008` | Event log | Event payloads written by this spec | `REQ-LOOP-001`, `REQ-LOOP-003`, `REQ-LOOP-004`, `REQ-LOOP-008`, `REQ-LOOP-010` | `AC-LOOP-001`, `AC-LOOP-009`, `AC-LOOP-010`, `AC-LOOP-022`, `AC-LOOP-024`, `AC-LOOP-028`, `AC-LIB-016` |
| `API-AGENT-009` | Hook | Tool hook pipeline and default handlers | `REQ-TOOL-009`, `REQ-LOOP-003`, `REQ-LOOP-006`, `REQ-LIB-005` | `AC-TOOL-023`, `AC-TOOL-024`, `AC-LOOP-007`, `AC-LOOP-018`, `AC-LIB-012` |
| `API-PROV-001` | Config | `[providers.<name>]` `kind = "openai_compat"` | `REQ-PROV-001` | `AC-PROV-001` through `AC-PROV-004` |
| `API-PROV-002` | Wire | OpenAI request and stream mapping | `REQ-PROV-002`, `REQ-PROV-003` | `AC-PROV-005` through `AC-PROV-009` |
| `API-PROV-003` | Error class | Provider error classes | `REQ-PROV-004`, `REQ-LOOP-008` | `AC-PROV-010` through `AC-PROV-012`, `AC-LOOP-022` through `AC-LOOP-025` |
| `API-PROV-004` | Test | `MockLlm` script format | `REQ-MOCK-001` | `AC-MOCK-001`, `AC-MOCK-002` |
| `API-TOOL-001` | Tool | `read_file` | `REQ-TOOL-001` | `AC-TOOL-001`, `AC-TOOL-002` |
| `API-TOOL-002` | Tool | `write_file` | `REQ-TOOL-002` | `AC-TOOL-003` through `AC-TOOL-005` |
| `API-TOOL-003` | Tool | `edit_file` | `REQ-TOOL-003` | `AC-TOOL-006` through `AC-TOOL-008` |
| `API-TOOL-004` | Tool | `glob` | `REQ-TOOL-004` | `AC-TOOL-009`, `AC-TOOL-010` |
| `API-TOOL-005` | Tool | `grep` | `REQ-TOOL-005` | `AC-TOOL-011` through `AC-TOOL-013` |
| `API-TOOL-006` | Tool | `bash` | `REQ-TOOL-006` | `AC-TOOL-014` through `AC-TOOL-017` |
| `API-TOOL-007` | Tool | `web_fetch` | `REQ-TOOL-007` | `AC-TOOL-018` through `AC-TOOL-020` |

## Core types used (owned by spec #2)

All hook types and supporting types are in `API-CORE-015` (spec #2). Revision 2 moved the
"additional" types that were previously here to there (`CQ-011`). Renamed items:

| Old name (revision 1) | Current name | Notes |
|---|---|---|
| `StepInput` | `PreStepInput` | Added `Reject` through `PreStepDecision` |
| `AgentTurnStopInput` | `TurnStopInput` | Fields `AgentName`, `Steps`, `StopReason` |
| `TurnStopVote` | `StopVote` | Same fields |
| `ToolDecision.Allow` | `ToolDecision.Allowed` | Added `Outcome` and `Status` (`CQ-020` of spec #2) |
| `ToolOutput.Status` | same, type `ToolStatus` | Constants `ToolStatusSuccess`, `ToolStatusError`, `ToolStatusRejected` |
| `ChatEvent.Kind` `tool_use` | `ChatEvent` with `Kind = ChatBlock` and `Block.Kind = tool_use` | Shape from spec #2 (`CQ-019`) |
| `ToolCall.CallID`, `Name`, `Input` | `ToolCall` of spec #2 | Added `SessionID`, `TurnID`, `Risk` |
| `Agent` | `AgentName` | In all core structs |

Hook types used by this spec:

| Hook | Mode | Input | Output | Default handler |
|---|---|---|---|---|
| `AgentPreStep` | waterfall | `PreStepInput` | `PreStepDecision` | Accept input as-is |
| `AgentRequest` | waterfall | `RequestControl` | `RequestControl` | Accept as-is |
| `LLMStream` | waterfall | `LLMStreamInput` | `ChatStream` | Call `Provider.Stream` for `Targets[0]`, fill `Request.Model` |
| `ToolsPreExecute` | waterfall | `ToolsPreExecuteInput` | `ToolDecision` | `Risk = Read` → allow. Otherwise deny `default fail-closed` |
| `ToolsExecute` | waterfall | `ToolsExecuteInput` | `ToolOutput` | Call `Tool.Call`. Error becomes `ToolOutput{error, message}` |
| `ToolsPostExecute` | waterfall | `ToolsPostExecuteInput` | `ToolOutput` | Accept as-is |
| `AgentTurnStopping` | serial | `TurnStopInput` | `StopVote` | Without a listener, the turn stops |
| `SessionEvent` | observe | `core.SessionEvent` | — | Called by the store after `Append` |

## API-LIB-001: `lana.New`

- **Purpose**: builds the library runtime from options.
- **Requirements**: `REQ-LIB-001`, `REQ-LIB-006`, `REQ-LIB-008`
- **Acceptance criteria**: `AC-LIB-001`, `AC-LIB-002`, `AC-LIB-003`, `AC-LIB-013`, `AC-LIB-014`
- **Stability**: New
- **Authorization**: Not applicable. The caller is the process that imports the package

```go
package lana

type ApprovalMode int

const (
    PolicyNever ApprovalMode = iota // default (zero value)
    PolicyTrust                     // allow-all listener on tools.pre_execute
)

type Options struct {
    Home     string            // "" = do not read config files from disk
    Layers   []config.Layer    // in-memory config layers (spec #2 REQ-CONF-009)
    Profile  string            // "" = "minimal"
    Tools    []core.Tool       // tools owned by the service
    Approval ApprovalMode
    Store    core.SessionStore // nil and Home empty = memstore. nil and Home set = store from profile (sqlite_store). Set = provided_store (DQ-016 of spec #2)
}

// Option is an additional option for New.
type Option func(*runtimeOptions)

// ApproveFunc answers approvals for the library session channel (API-APPR-005 of spec #4).
type ApproveFunc = func(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome

// ApproveWith installs the approval responder for the library channel. See API-APPR-005 (spec #4).
func ApproveWith(fn ApproveFunc) Option

func New(ctx context.Context, opts Options, options ...Option) (*Runtime, error)
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Options | `Home` | string | No | Location of `LANA_HOME`. Empty = no files are read |
| Options | `Layers` | `[]config.Layer` | No | In-memory layers, applied after the profile |
| Options | `Profile` | string | No | One of the built-in profiles (`API-LIB-005`). Empty = `minimal` |
| Options | `Tools` | `[]core.Tool` | No | Names must be unique and must not match built-in tools |
| Options | `Approval` | `ApprovalMode` | No | `PolicyNever` (zero value) or `PolicyTrust` |
| Options | `Store` | `core.SessionStore` | No | Nil and `Home` empty: `memstore`. Nil and `Home` set: store from profile. Set: `provided_store` |
| Option | `ApproveWith(fn)` | `ApproveFunc` | No | Approval responder for the `library` channel. See `API-APPR-005` of spec #4 (`CQ-016`) |

### Success

| Condition | Exit or status | Output |
|---|---|---|
| Boot completes and all modules are installed | — | `*Runtime` ready for use, error nil |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---|---|---:|---|
| Custom tool has the same name as a built-in tool | — | boot error `tool_name_conflict` | No | Error names the tool (`AC-LIB-002`) |
| Agent uses a tool that is not registered | — | boot error `unknown_tool` | No | Error names the agent and tool (`AC-LIB-003`) |
| Provider `kind` is not `openai_compat` | — | boot error `unsupported_kind` | No | Error names the provider (`AC-PROV-003`) |
| Env `api_key_env` empty for a provider used by an agent | — | error class `missing_secret` | No | Error names the env and provider (`AC-PROV-004`) |
| Unknown profile or invalid config | — | error from spec #2 | No | Passed through from the loader |

### Side effects and guarantees

- **State transition**: the runtime moves from not-booted to ready. No session is created.
- **Idempotency**: not applicable. Each call creates a new runtime.
- **Concurrency**: safe to use from many goroutines after `New` returns.
- **Data exposure**: API keys do not appear in errors or logs.

### Compatibility

- **Breaking change**: No. New API.
- **Deprecation**: Not applicable.

## API-LIB-002: `(*Runtime).Run`

- **Purpose**: runs one turn and returns the final result.
- **Requirements**: `REQ-LIB-002`, `REQ-LIB-007`
- **Acceptance criteria**: `AC-LIB-004` through `AC-LIB-008`, `AC-LIB-015`, `AC-LIB-016`
- **Stability**: New
- **Authorization**: Not applicable

```go
type Request struct {
    Agent     string // empty = first route with channel "library"
    Prompt    string // required, not empty
    SessionID string // empty = new session; set = open that session
}

type Usage struct {
    InputTokens  int
    OutputTokens int
}

type Result struct {
    Text       string
    SessionID  string
    StopReason string // StopEndTurn, StopMaxSteps, StopCancelled, StopError
    Usage      Usage  // sum of step/end usage for one turn
}

const (
    StopEndTurn   = "end_turn"
    StopMaxSteps  = "max_steps"
    StopCancelled = "cancelled"
    StopError     = "error"
)

func (rt *Runtime) Run(ctx context.Context, req Request) (Result, error)
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `ctx` | `context.Context` | Yes | Cancelling ctx cancels the turn (`StopCancelled`) |
| Request | `Agent` | string | No | Must be registered. Empty = first `library` route |
| Request | `Prompt` | string | Yes | Must not be empty or whitespace only |
| Request | `SessionID` | string | No | ULID or unique prefix of at least 6 characters. Session must exist |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Turn finishes with `end_turn` | — | Full `Result`, error nil |
| Turn finishes with `max_steps` | — | `StopReason` `max_steps`, error nil |
| Turn cancelled | — | `StopReason` `cancelled`, error nil |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| `Prompt` empty | — | `ErrEmptyPrompt` | Yes | No session is created (`AC-LIB-008`) |
| No `library` route and `Agent` empty | — | `ErrNoRoute` | No | (`AC-LIB-015`) |
| Agent not registered | — | `ErrUnknownAgent` | No | Names the agent |
| Session is running | — | `ErrSessionBusy` | Yes | Message does not enter the inbox (`AC-LIB-005`) |
| Session not found | — | `ErrSessionNotFound` | No | Names the ID |
| Provider fails after retry and fallback | — | `*ProviderError{Class}` | Depends on class | `StopReason` `error` (`AC-LIB-007`) |
| Runtime closed | — | `ErrClosed` | No | (`AC-LIB-011`) |

Provider error type:

Type `*core.ProviderError` (`API-CORE-015`), alias `lana.ProviderError`:

```go
type ProviderError struct {
    Class   string // auth | rate_limit | server | network | context_length | client | missing_secret
    Status  int
    Message string
}
// errors.Is(err, core.ErrContextTooLong) is true for Class context_length.
```

### Side effects and guarantees

- **State transition**: creates a new session if `SessionID` is empty. Writes turn and step events.
- **Idempotency**: not applicable. Each call adds one turn.
- **Concurrency**: `Run` and `Stream` for the same session must not run at the same time. The second caller receives `ErrSessionBusy`.
- **Data exposure**: the answer text is in `Result.Text`. No secrets.

### Compatibility

- **Breaking change**: No. New API.
- **Deprecation**: Not applicable.

## API-LIB-003: `(*Runtime).Stream`

- **Purpose**: emits turn events as an iterator so the service can display progress.
- **Requirements**: `REQ-LIB-003`
- **Acceptance criteria**: `AC-LIB-009`, `AC-LIB-010`
- **Stability**: New
- **Authorization**: Not applicable

```go
func (rt *Runtime) Stream(ctx context.Context, req Request) iter.Seq2[Event, error]

type Event interface{ isEvent() }

type Session struct {
    SessionID string
    Agent     string
    Model     string // "provider:model"
}
type TextDelta struct{ Text string }
type ToolCall struct {
    CallID string
    Name   string
    Input  json.RawMessage
}
type ToolResult struct {
    CallID string
    Status string // success | error | rejected
    Output string
}
type Done struct{ Result Result }
```

### Input

Same as `API-LIB-002`.

### Success

| Condition | Event order | Output |
|---|---|---|
| Turn finishes | `Session`, then `TextDelta`, `ToolCall`, `ToolResult` as events occur, then `Done` | `Done.Result` contains the same result as `Run` |

### Failure

| Condition | Last event | Stable code |
|---|---|---|
| Pre-turn error (`ErrEmptyPrompt`, `ErrNoRoute`, `ErrUnknownAgent`, `ErrSessionBusy`, `ErrSessionNotFound`) | `(nil, err)` | Same as `API-LIB-002` |
| Provider fails | `Done` with `StopReason` `error`, then `(nil, *ProviderError)` | `ProviderError.Class` |
| `ctx` cancelled | `Done` with `StopReason` `cancelled` | — |

### Side effects and guarantees

- **State transition**: same as `Run`.
- **Idempotency**: not applicable.
- **Concurrency**: same as `Run`. Live frames are sent without waiting for the iterator to be read; events that are not read are dropped with a log warning (`REQ-LOOP-009`).
- **Data exposure**: same as `Run`.

### Compatibility

- **Breaking change**: No. New API.
- **Deprecation**: Not applicable.

## API-LIB-004: `(*Runtime).Close`

- **Purpose**: stops all turns and releases modules.
- **Requirements**: `REQ-LIB-004`
- **Acceptance criteria**: `AC-LIB-011`
- **Stability**: New
- **Authorization**: Not applicable

```go
func (rt *Runtime) Close() error
```

### Input

None.

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| All turns stop and modules shut down within 30 seconds | — | error nil |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| A turn exceeds 30 seconds | — | error `shutdown_timeout` (contains the module name) | No | The module that timed out is logged and skipped (spec #2 `REQ-MOD-003`) |
| Called twice | — | — | Yes | The second call does nothing and returns error nil |

### Side effects and guarantees

- **State transition**: runtime moves to `closed`. `Run`, `Stream`, `Send`, `Start` afterwards fail with `ErrClosed`.
- **Idempotency**: `Close` is idempotent.
- **Concurrency**: safe to call while turns are running.
- **Data exposure**: not applicable.

### Compatibility

- **Breaking change**: No. New API.
- **Deprecation**: Not applicable.

## API-LIB-005: Module rows of the built-in profiles

- **Purpose**: defines the module rows that `lana.New` uses for the built-in profiles.
- **Requirements**: `REQ-LIB-008`
- **Acceptance criteria**: `AC-LIB-017`
- **Stability**: New
- **Authorization**: Not applicable

Built-in profile rows are owned by spec #2 (`API-CONF-009`, `CQ-031`). This spec uses the rows `llm`, `tools`, `store`, and `agent` from base. `minimal` is the same as base. `headless` is the same as base plus `cli_runner` (spec #1). Two modules belong to this spec:

| `module` | ID | When |
|---|---|---|
| `memstore` | `store` | `Home` empty and `Options.Store` nil (library) |
| `provided_store` | `store` | `Options.Store` set. Installs that instance through `Provide` (DQ-016 of spec #2) |

### Input

None. These rows are built-in profile data (spec #2 `REQ-PROF-001`).

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Profile loaded | — | Base rows per `API-CONF-009`, with origin `profile:minimal` or `profile:headless` |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| `store` row is replaced by a patch with an unknown `module` | — | spec #2 error `REQ-CONF-003` | No | Names the `module` |

### Side effects and guarantees

- **State transition**: none.
- **Idempotency**: not applicable.
- **Concurrency**: not applicable.
- **Data exposure**: not applicable.

### Compatibility

- **Breaking change**: No.
- **Deprecation**: Not applicable.
- **Note**: on the profile, `store` uses `sqlite_store` (spec #4). A library without `Home` uses `memstore` through the row above. Spec #1 adds the `runner` row (`cli_runner`) for `headless`.

## API-AGENT-001: `(*Registry).Send`

- **Purpose**: sends a message to a session. If the session is idle, a new turn starts. If busy, the message enters the inbox.
- **Requirements**: `REQ-LOOP-001`, `REQ-LOOP-002`
- **Acceptance criteria**: `AC-LOOP-001` through `AC-LOOP-006`
- **Stability**: New
- **Authorization**: Not applicable in this package. The caller (CLI or library) has already selected the session.

```go
// Implementation of core.AgentRegistry (signature per CQ-010).
func (r *Registry) Send(ctx context.Context, sessionID, text string) error
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `ctx` | `context.Context` | Yes | Only for validation and cancelling the call. Does not cancel the turn |
| Argument | `sessionID` | string | Yes | Session must exist |
| Argument | `text` | string | Yes | Must not be empty. Text only (`CQ-005`) |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Session idle | — | New turn starts. `turn/start` is written. Error nil |
| Session busy | — | Message enters the inbox. Error nil. Message is claimed in the next step |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Session does not exist | — | `ErrSessionNotFound` | No | No event |
| Text empty | — | `ErrEmptyText` | Yes | No event |
| Runtime closed | — | `ErrClosed` | No | No event |

### Side effects and guarantees

- **State transition**: idle to running turn, or inbox grows by one.
- **Idempotency**: not applicable. Each call adds one message.
- **Concurrency**: messages in the inbox are taken one per step. Send order is preserved per session (`AC-LOOP-004`).
- **Data exposure**: message text is written to `user/message`.

### Compatibility

- **Breaking change**: No. Matches the needs of spec #1 (`ctx.agents.Send`).
- **Deprecation**: Not applicable.

## API-AGENT-002: `(*Registry).Start`

- **Purpose**: starts a new turn for the library and returns the turn ID. Rejects a session that is already running.
- **Requirements**: `REQ-LOOP-002`, `REQ-LIB-002`
- **Acceptance criteria**: `AC-LIB-005`
- **Stability**: New
- **Authorization**: Not applicable.

```go
func (r *Registry) Start(ctx context.Context, sessionID, text string) (turnID string, err error)
```

### Input

Same as `API-AGENT-001`.

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Session idle | — | `turnID` ULID, error nil. Turn starts |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Session is running | — | `ErrSessionBusy` | Yes | No message enters the inbox |
| Other conditions | — | Same as `API-AGENT-001` | See table there | — |

### Side effects and guarantees

- **State transition**: idle to running turn.
- **Idempotency**: not applicable.
- **Concurrency**: the idle check and turn start happen under one lock per session.
- **Data exposure**: same as `API-AGENT-001`.

### Compatibility

- **Breaking change**: No. New API.
- **Deprecation**: Not applicable.

## API-AGENT-003: `(*Registry).Cancel`

- **Purpose**: cancels the running turn in a session.
- **Requirements**: `REQ-LOOP-007`
- **Acceptance criteria**: `AC-LOOP-019`, `AC-LOOP-020`, `AC-LOOP-021`
- **Stability**: New
- **Authorization**: Not applicable.

```go
// Implementation of core.AgentRegistry (API-CORE-010).
func (r *Registry) Cancel(ctx context.Context, sessionID string) error
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `ctx` | `context.Context` | Yes | Only for cancelling the call. Does not cancel the turn through this `ctx` |
| Argument | `sessionID` | string | Yes | Session must exist |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Turn running | — | Turn ends with `turn/end` reason `cancelled`. Error nil |
| Session idle | — | No event. Error nil (`AC-LOOP-020`) |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Session does not exist | — | `ErrSessionNotFound` | No | No event |

### Side effects and guarantees

- **State transition**: running turn to `cancelled`. Inbox unchanged (`AC-LOOP-021`).
- **Idempotency**: a second `Cancel` on the same turn writes no additional event.
- **Concurrency**: safe to call from other goroutines.
- **Data exposure**: not applicable.

### Compatibility

- **Breaking change**: No.
- **Deprecation**: Not applicable.

## API-AGENT-004: `(*Registry).SetAgent`

- **Purpose**: selects the agent for a session. Takes effect from the next turn or step.
- **Requirements**: `REQ-LOOP-010`
- **Acceptance criteria**: `AC-LOOP-028`
- **Stability**: New
- **Authorization**: Not applicable.

```go
// Implementation of core.AgentRegistry.
func (r *Registry) SetAgent(ctx context.Context, sessionID, agent string) error
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `sessionID` | string | Yes | Session must exist |
| Argument | `agent` | string | Yes | Must be registered in the config |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Agent registered | — | `model/selection` event written (payload in `API-AGENT-008`). Error nil |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Agent not registered | — | `ErrUnknownAgent` | Yes | No event. Previous agent remains (`AC-CMD-007` of spec #1 applies to providers) |
| Session does not exist | — | `ErrSessionNotFound` | No | No event |

### Side effects and guarantees

- **State transition**: session agent changes. Takes effect from the next step.
- **Idempotency**: selecting the same agent twice writes two events. Harmless.
- **Concurrency**: read per step.
- **Data exposure**: not applicable.

### Compatibility

- **Breaking change**: No.
- **Deprecation**: Not applicable.

## API-AGENT-005: `(*Registry).SetModel`

- **Purpose**: selects the provider and model for a session. Takes effect from the next step.
- **Requirements**: `REQ-LOOP-010`
- **Acceptance criteria**: `AC-LOOP-029`, `AC-LOOP-030`
- **Stability**: New
- **Authorization**: Not applicable.

```go
// Implementation of core.AgentRegistry (API-CORE-010). model has the form "<provider>:<model>".
func (r *Registry) SetModel(ctx context.Context, sessionID, model string) error
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `sessionID` | string | Yes | Session must exist |
| Argument | `model` | string | Yes | `<provider>:<model>`. The provider part must be registered in `[providers.<name>]`. The model part must not be empty. Not verified against the provider |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Provider registered | — | `model/selection` event written. Error nil |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Provider not registered | — | `ErrUnknownProvider` | Yes | Message says `unknown provider: <name>`. No event. A format without `:` also produces this error |
| Model empty | — | `ErrEmptyModel` | Yes | No event |
| Session does not exist | — | `ErrSessionNotFound` | No | No event |

### Side effects and guarantees

- **State transition**: session model changes from the next step.
- **Idempotency**: same as `SetAgent`.
- **Concurrency**: read per step.
- **Data exposure**: not applicable.

### Compatibility

- **Breaking change**: No.
- **Deprecation**: Not applicable.

## API-AGENT-006: `(*Registry).SubscribeLive`

- **Purpose**: receives temporary text frames for one session.
- **Requirements**: `REQ-LOOP-009`
- **Acceptance criteria**: `AC-LOOP-026`, `AC-LOOP-027`
- **Stability**: New
- **Authorization**: Not applicable.

```go
// Implementation of core.AgentRegistry (API-CORE-010). Subscription uses a core.ObserveBuffer buffer (1024).
func (r *Registry) SubscribeLive(sessionID string) (*core.Subscription[core.LiveFrame], error)
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument | `sessionID` | string | Yes | The session may not have a turn yet |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Subscribe succeeds | — | `*core.Subscription` with buffer 1024. `Close` on the subscription removes the subscriber |

A `text_delta` frame becomes `LiveFrame{SessionID, TurnID, Kind: "text_delta", Text}`.

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Buffer full | — | — | — | Oldest frame is dropped. `Received.Lagged` > 0 and warning `live frame dropped` is written to the log (`AC-LOOP-027`) |

### Side effects and guarantees

- **State transition**: none in the log.
- **Idempotency**: not applicable.
- **Concurrency**: one channel per subscriber. Delivery never blocks the turn.
- **Data exposure**: model text. Not written to the log.

### Compatibility

- **Breaking change**: No.
- **Deprecation**: Not applicable.

## API-AGENT-007: Additional agent config keys

- **Purpose**: the `[agents.<name>]` keys used by the agent loop. Other keys in the same table (`permission`, `rules`, `subagents`, `mcp`, `skills`) are owned by other specs.
- **Requirements**: `REQ-LOOP-004`, `REQ-LOOP-005`, `REQ-LOOP-008`, `REQ-LOOP-010`
- **Acceptance criteria**: `AC-LOOP-011`, `AC-LOOP-015`, `AC-LOOP-016`, `AC-LOOP-023`, `AC-LOOP-031`
- **Stability**: New
- **Authorization**: Not applicable.

```toml
[agents.coder]
provider = "openai"
model = "gpt-5-mini"
fallback = ["openrouter:openai/gpt-5-mini"]
prompt_file = "agents/coder.md"
workspace = "~/coding"
tools = ["read_file", "write_file", "edit_file", "glob", "grep", "bash", "web_fetch"]
max_steps = 25
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Config | `provider` | string | Yes | Must exist in `[providers]` |
| Config | `model` | string | Yes | Not empty |
| Config | `fallback` | array of strings `provider:model` | No | Each provider must exist. Default empty |
| Config | `prompt_file` | string | No | Relative path from `LANA_HOME`. Empty = empty system prompt. Read every step |
| Config | `workspace` | string | No | Absolute path or `~/...`. Empty = cwd at `New` (`CQ-010`) |
| Config | `tools` | array of strings | No | Each name must be a built-in tool (`API-TOOL-*`) or a custom tool. Default empty |
| Config | `max_steps` | integer | No | 1 to 200. Default 25 (`Q-002`) |

The `permission` key (written by `lana init` in spec #1) is read by spec #2 as a known key.
This spec does not use it. Spec #4 uses it.

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| All keys valid | — | Agent registered |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| `max_steps` outside 1 to 200 | — | spec #2 error `REQ-CONF-003` | No | Names the key `agents.<name>.max_steps` |
| Unknown tool | — | `unknown_tool` | No | See `AC-LIB-003` |
| Provider in `fallback` not registered | — | boot error (config reference, ADR 0010) | No | Boot fails and names the provider. No silent fallback |

### Side effects and guarantees

- **State transition**: none.
- **Idempotency**: not applicable.
- **Concurrency**: not applicable.
- **Data exposure**: no secrets in these keys.

### Compatibility

- **Breaking change**: No.
- **Deprecation**: Not applicable.

## API-AGENT-008: Session log event payloads

- **Purpose**: kinds and payloads this spec writes to `core.SessionEvent.Payload`.
- **Requirements**: `REQ-LOOP-001`, `REQ-LOOP-003`, `REQ-LOOP-004`, `REQ-LOOP-008`, `REQ-LOOP-010`
- **Acceptance criteria**: `AC-LOOP-001`, `AC-LOOP-009`, `AC-LOOP-010`, `AC-LOOP-022`, `AC-LOOP-024`, `AC-LOOP-028`, `AC-LIB-016`
- **Stability**: New.
- **Authorization**: Not applicable.

Fields of each payload are owned by `API-CORE-008` (spec #2, `CQ-029`). This spec only writes the following kinds:

| Kind | Struct (`core`) | Description |
|---|---|---|
| `turn/start` | `TurnStart` | Turn ID is in `TurnID` and the payload |
| `turn/end` | `TurnEnd` | `reason`. `error` (`PayloadError`) only if reason is `error` |
| `step/start` | `StepStart` | Recorded before the model request |
| `step/end` | `StepEnd` | `usage` total for the step |
| `system/message` | `SystemMessage` | `text`. Only if the prompt changed from the last event |
| `user/message` | `UserMessage` | `content` contains the `text` block of the claimed message |
| `assistant/message` | `AssistantMessage` | `content` contains `text` and `tool_use` blocks. Revision 1 used `blocks`, replaced by `content` (`CQ-029`) |
| `assistant/attempt` | `AssistantAttempt` | `provider`, `model`, `attempt`, `error` (`PayloadError`). Not part of history |
| `tool/call` | `ToolCallPayload` | `call_id`, `name`, `input`. Written before the pipeline |
| `tool/result` | `ToolResultPayload` | `call_id`, `status`, `output`. Exactly one per `tool/call` |
| `model/selection` | `ModelSelection` | `provider`, `model`, `agent` (only from `SetAgent`, `Q-022`) |

Kind `session/created` is written by spec #4 with `workspace` (`Q-017`). This spec only reads it.

Examples of `turn/end` and `tool/result`:

```json
{"v":1,"reason":"cancelled","error":null}
{"v":1,"call_id":"call_1","status":"rejected","output":"default fail-closed: tool write_file requires permission"}
```

## API-AGENT-009: Tool hook pipeline

- **Purpose**: order of tool hooks and default handlers. Listeners from spec #4 and `PolicyTrust` are added to this chain.
- **Requirements**: `REQ-TOOL-009`, `REQ-LOOP-003`, `REQ-LOOP-006`, `REQ-LIB-005`
- **Acceptance criteria**: `AC-TOOL-023`, `AC-TOOL-024`, `AC-LOOP-007`, `AC-LOOP-018`, `AC-LIB-012`
- **Stability**: New
- **Authorization**: `tools.pre_execute` is the permission point. This spec only provides the default fail-closed behavior.

Order per tool call: `tools.pre_execute`, then `tools.execute`, then `tools.post_execute`. If
`tools.pre_execute` returns `Allowed = false`, the next two hooks do not run and the
`tool/result` has status `rejected` (`AC-TOOL-024`).

The `PolicyTrust` listener (`API-LIB-001`) is installed on `tools.pre_execute` in waterfall mode and
always returns `ToolDecision{Allowed: true}` without calling `next`.

## API-PROV-001: `openai_compat` provider config

- **Purpose**: `[providers.<name>]` keys for the OpenAI-compatible adapter (OpenAI, OpenRouter, Ollama).
- **Requirements**: `REQ-PROV-001`
- **Acceptance criteria**: `AC-PROV-001`, `AC-PROV-002`, `AC-PROV-003`, `AC-PROV-004`
- **Stability**: New
- **Authorization**: The `api_key_env` key points to an env. Its value is never written in the config.

```toml
[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
headers = { "X-Title" = "lana" }
timeout = "300s"

[providers.ollama]
kind = "openai_compat"
base_url = "http://localhost:11434/v1"
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Config | `kind` | string | Yes | Must be `openai_compat`. Other values are rejected (`AC-PROV-003`) |
| Config | `base_url` | string | Yes | Absolute `http` or `https` URL. No `/chat/completions` at the end |
| Config | `api_key_env` | string | No | Env name. Empty = no `Authorization` header (`AC-PROV-002`) |
| Config | `headers` | table of string to string | No | Literal values, not interpolated (`CQ-007`) |
| Config | `timeout` | Go duration string | No | Total limit for one request. Default `300s` (`CQ-008`) |

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Config valid and env set (if used) | — | Provider registered in `LLMRegistry` |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| `kind` is not `openai_compat` | — | `unsupported_kind` | No | Boot fails (`AC-PROV-003`) |
| `base_url` not absolute or scheme not http/https | — | spec #2 error `REQ-CONF-003` | No | Names the key |
| Env `api_key_env` empty for a provider in use | — | `missing_secret` | No | Boot fails (`AC-PROV-004`) |

### Side effects and guarantees

- **State transition**: none.
- **Idempotency**: not applicable.
- **Concurrency**: the adapter is safe to use from many goroutines.
- **Data exposure**: secret values never enter logs, errors, or events.

### Compatibility

- **Breaking change**: No.
- **Deprecation**: Not applicable.

## API-PROV-002: OpenAI wire mapping

- **Purpose**: shape of the request and stream sent to `POST {base_url}/chat/completions`.
- **Requirements**: `REQ-PROV-002`, `REQ-PROV-003`
- **Acceptance criteria**: `AC-PROV-005` through `AC-PROV-009`
- **Stability**: New
- **Authorization**: Header `Authorization: Bearer <secret>` if `api_key_env` is set.

Request body:

```json
{
  "model": "gpt-5-mini",
  "stream": true,
  "stream_options": {"include_usage": true},
  "messages": [],
  "tools": [{"type": "function", "function": {"name": "read_file", "description": "...", "parameters": {}}}],
  "max_tokens": 4096,
  "temperature": 0.2
}
```

`max_tokens` and `temperature` are only present if their values are set in `ChatRequest`.

| `core` | OpenAI shape |
|---|---|
| `ChatRequest.System` | First message with `role = "system"` |
| `text` block in a `user` message | `role = "user"`, `content` string |
| `text` block in an `assistant` message | `role = "assistant"`, `content` string |
| `tool_use` block | `tool_calls: [{id, type: "function", function: {name, arguments: <JSON string>}}]` |
| `tool_result` block | Message with `role = "tool"`, `tool_call_id` = `call_id`, `content` = output |
| `image` block | Fails with `ErrUnsupported` (`AC-PROV-009`, `Q-023`) |

SSE stream:

| Stream part | Meaning |
|---|---|
| `data: {...}` per line | One JSON chunk |
| `choices[0].delta.content` | `text_delta` |
| `choices[0].delta.tool_calls[i]` | Accumulated per `index`: `id`, `function.name`, `function.arguments` |
| `choices[0].finish_reason` | `stop` becomes `end_turn`, `tool_calls` becomes `tool_use`, `length` becomes `max_tokens` |
| `usage.prompt_tokens` and `usage.completion_tokens` | `usage` with `input_tokens` and `output_tokens` |
| `data: [DONE]` | End of stream |

Each `tool_use` is emitted after `finish_reason` is received, ordered by `index`. If `id` is
empty, the adapter uses a new ULID as `CallID`.

### Input

See the conversion table above.

### Success

| Condition | Exit or status | Output |
|---|---:|---|
| Stream finishes with `[DONE]` or `finish_reason` | — | `ChatTextDelta`, `ChatBlock` (`tool_use`), `ChatUsage`, and `ChatStop` events (`API-CORE-006`) |

### Failure

| Condition | Exit or status | Stable code | Retryable | Visible behavior |
|---|---:|---|---:|---|
| Stream cut before `finish_reason` | — | class `network` | Yes, if no delta yet | See `API-PROV-003` |
| Invalid JSON chunk | — | class `server` | Yes, if no delta yet | Error with message `invalid response` |
| Image in history | — | `ErrUnsupported` | No | Request not sent |

### Side effects and guarantees

- **State transition**: none.
- **Idempotency**: not applicable. The request can be repeated by retry (`REQ-LOOP-008`).
- **Concurrency**: one stream per `Stream` call.
- **Data exposure**: message content is sent to `base_url`. Secrets only in the header.

### Compatibility

- **Breaking change**: No.
- **Deprecation**: Not applicable.

## API-PROV-003: Provider error classes

- **Purpose**: mapping of failures to fixed classes for retry, fallback, and Go errors.
- **Requirements**: `REQ-PROV-004`, `REQ-LOOP-008`
- **Acceptance criteria**: `AC-PROV-010` through `AC-PROV-012`, `AC-LOOP-022` through `AC-LOOP-025`
- **Stability**: New
- **Authorization**: Not applicable.

| Class | Source | Retry | Fallback | Turn result |
|---|---|---:|---:|---|
| `auth` | HTTP 401, 403 | No | No | `turn/end` `error` |
| `rate_limit` | HTTP 429 | Yes (3 times) | Yes | `turn/end` `error` when exhausted |
| `server` | HTTP 5xx, broken stream | Yes (3 times) | Yes | `turn/end` `error` when exhausted |
| `network` | Connection failure, timeout, EOF before `finish_reason` | Yes (3 times) | Yes | `turn/end` `error` when exhausted |
| `context_length` | HTTP 400 with `error.code` `context_length_exceeded` | No | No | `turn/end` `error` |
| `client` | HTTP 400, 404, 422 and other 4xx | No | No | `turn/end` `error` |
| `missing_secret` | Env `api_key_env` empty | — | — | Boot fails (`API-PROV-001`) |

Class `context_length` is returned as `*core.ProviderError` for which `errors.Is(err, core.ErrContextTooLong)` is true (`CQ-014`). Without compaction, the turn ends `error` (`Q-005`). With the compaction module (spec #4), the `llm.stream` listener summarizes and retries once.

Retry delays: 1 second, 2 seconds, 4 seconds (`Q-003`). A failure after the first delta is not retried
(`Q-006`). Each failed attempt is written as `assistant/attempt`.

## API-PROV-004: MockLlm script format

- **Purpose**: scripted responses for tests without network.
- **Requirements**: `REQ-MOCK-001`
- **Acceptance criteria**: `AC-MOCK-001`, `AC-MOCK-002`
- **Stability**: New. Test only.
- **Authorization**: Not applicable.

```go
package mock

type Llm struct{ /* ... */ }

func New(script Script) *Llm
func LoadScript(path string) (Script, error) // JSON format below

type Script struct {
    Responses []Response `json:"responses"`
}

type Response struct {
    Events []Event      `json:"events,omitempty"`
    Error  *ScriptError `json:"error,omitempty"` // terminal for this call
}
```

JSON format:

```json
{
  "responses": [
    {"events": [
      {"text": "Hello"},
      {"delay_ms": 50},
      {"text": " world"},
      {"tool_use": {"id": "call_1", "name": "read_file", "input": {"path": "a.txt"}}},
      {"usage": {"input_tokens": 10, "output_tokens": 4}},
      {"stop": "tool_use"}
    ]},
    {"error": {"class": "rate_limit", "message": "429 from mock"}}
  ]
}
```

| Rule | Value |
|---|---|
| One element of `responses` | One `Stream` call |
| One `events` object | Exactly one key: `text`, `delay_ms`, `tool_use`, `usage`, or `stop` |
| `error` | Terminal. May come after other events (error in the middle of a stream) |
| `error.class` | One of the classes in `API-PROV-003` |
| Calls exceed `responses` | Error `script exhausted` |
| Unknown key | Error during `LoadScript` (`AC-MOCK-001`) |

The `stop` value uses `ChatEvent.StopReason` (`end_turn`, `tool_use`, `max_tokens`). The `tool_use` key in the script produces a `ChatBlock` with `Block.Kind = tool_use`.

## API-TOOL-001: `read_file`

- **Purpose**: reads the contents of a text file in the workspace.
- **Requirements**: `REQ-TOOL-001`, `REQ-TOOL-008`
- **Acceptance criteria**: `AC-TOOL-001`, `AC-TOOL-002`, `AC-TOOL-021`, `AC-TOOL-022`
- **Stability**: New
- **Risk**: `Read`
- **Authorization**: Path must be inside the workspace (`REQ-TOOL-008`).

```json
{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":2000}},"required":["path"],"additionalProperties":false}
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Input | `path` | string | Yes | Relative to the workspace or absolute inside it |
| Input | `offset` | integer | No | First line, 1-based. Default 1 |
| Input | `limit` | integer | No | 1 to 2000. Default 2000 |

### Success

| Condition | Status | Output |
|---|---|---|
| Text file read | `success` | Lines from `offset` to `offset + limit - 1`, truncated at 30000 characters with a note |

### Failure

| Condition | Status | Output |
|---|---|---|
| Invalid input | `error` | Input message |
| File does not exist | `error` | `file not found` |
| Binary file (NUL within first 8000 bytes) | `error` | `binary file` |
| Path outside workspace | `rejected` | `outside workspace` |

## API-TOOL-002: `write_file`

- **Purpose**: creates or replaces a text file in the workspace.
- **Requirements**: `REQ-TOOL-002`, `REQ-TOOL-008`
- **Acceptance criteria**: `AC-TOOL-003` through `AC-TOOL-005`
- **Stability**: New
- **Risk**: `Write`
- **Authorization**: `tools.pre_execute` gate (`REQ-TOOL-009`). Path inside the workspace.

```json
{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Input | `path` | string | Yes | Path inside the workspace |
| Input | `content` | string | Yes | May be empty. Replaces the entire file contents |

### Success

| Condition | Status | Output |
|---|---|---|
| File created or replaced | `success` | `wrote <n> bytes` |

### Failure

| Condition | Status | Output |
|---|---|---|
| Parent directory does not exist | `error` | `parent directory does not exist`. No file created |
| Path outside workspace | `rejected` | `outside workspace` |
| Not allowed by gate | `rejected` | Gate reason |

## API-TOOL-003: `edit_file`

- **Purpose**: replaces one exact occurrence of text in a file.
- **Requirements**: `REQ-TOOL-003`, `REQ-TOOL-008`
- **Acceptance criteria**: `AC-TOOL-006` through `AC-TOOL-008`
- **Stability**: New
- **Risk**: `Write`
- **Authorization**: Same as `write_file`.

```json
{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string","minLength":1},"new_string":{"type":"string"}},"required":["path","old_string","new_string"],"additionalProperties":false}
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Input | `path` | string | Yes | Path inside the workspace |
| Input | `old_string` | string | Yes | Must not be empty |
| Input | `new_string` | string | Yes | May be empty (deletes the text) |

### Success

| Condition | Status | Output |
|---|---|---|
| `old_string` occurs exactly once | `success` | `replaced 1 occurrence` |

### Failure

| Condition | Status | Output |
|---|---|---|
| `old_string` not found | `error` | `not found`. File unchanged |
| `old_string` occurs more than once | `error` | `found <n> times; add more context`. File unchanged |
| `old_string` empty | `error` | Invalid input message |

## API-TOOL-004: `glob`

- **Purpose**: searches for paths matching a glob pattern in the workspace.
- **Requirements**: `REQ-TOOL-004`, `REQ-TOOL-008`
- **Acceptance criteria**: `AC-TOOL-009`, `AC-TOOL-010`
- **Stability**: New
- **Risk**: `Read`
- **Authorization**: Path (if set) inside the workspace.

```json
{"type":"object","properties":{"pattern":{"type":"string","minLength":1},"path":{"type":"string"}},"required":["pattern"],"additionalProperties":false}
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Input | `pattern` | string | Yes | Glob with `**` (`doublestar` library) |
| Input | `path` | string | No | Directory inside the workspace. Default is the workspace |

### Success

| Condition | Status | Output |
|---|---|---|
| Results found | `success` | Workspace-relative paths, one per line, sorted. Maximum 1000. If more, a note `of <n> results` |
| No results | `success` | `no results` |

Paths under `.git/` and paths matching the root `.gitignore` do not appear (`Q-013`).

### Failure

| Condition | Status | Output |
|---|---|---|
| Pattern empty or invalid | `error` | Invalid pattern message |
| Path outside workspace | `rejected` | `outside workspace` |

## API-TOOL-005: `grep`

- **Purpose**: searches text with RE2 regex in workspace files.
- **Requirements**: `REQ-TOOL-005`, `REQ-TOOL-008`
- **Acceptance criteria**: `AC-TOOL-011` through `AC-TOOL-013`
- **Stability**: New
- **Risk**: `Read`
- **Authorization**: Path (if set) inside the workspace.

```json
{"type":"object","properties":{"pattern":{"type":"string","minLength":1},"path":{"type":"string"},"glob":{"type":"string"}},"required":["pattern"],"additionalProperties":false}
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Input | `pattern` | string | Yes | Go `regexp` syntax (RE2) |
| Input | `path` | string | No | Directory inside the workspace. Default is the workspace |
| Input | `glob` | string | No | Relative file path filter (`doublestar`) |

### Success

| Condition | Status | Output |
|---|---|---|
| Matches found | `success` | One `path:line: text` line per match. Maximum 200. Line text truncated at 500 characters |
| No matches | `success` | `no matches` |

Binary files and files larger than 1 MiB are skipped (`AC-TOOL-013`). `.git/` and the root `.gitignore` are skipped.

### Failure

| Condition | Status | Output |
|---|---|---|
| Invalid pattern | `error` | `invalid pattern: <message>` |
| Path outside workspace | `rejected` | `outside workspace` |

## API-TOOL-006: `bash`

- **Purpose**: runs a shell command in the workspace.
- **Requirements**: `REQ-TOOL-006`
- **Acceptance criteria**: `AC-TOOL-014` through `AC-TOOL-017`
- **Stability**: New
- **Risk**: `Exec`
- **Authorization**: `tools.pre_execute` gate (`REQ-TOOL-009`). No sandbox (ADR 0004).

```json
{"type":"object","properties":{"command":{"type":"string","minLength":1},"timeout_secs":{"type":"integer","minimum":1,"maximum":600}},"required":["command"],"additionalProperties":false}
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Input | `command` | string | Yes | Run with `sh -c`. Cwd = workspace |
| Input | `timeout_secs` | integer | No | 1 to 600. Default 120 (`CQ-009`) |

### Success

| Condition | Status | Output |
|---|---|---|
| Exit code 0 | `success` | Combined stdout and stderr. Maximum 30000 characters, the rest is truncated with a note of the original character count |

### Failure

| Condition | Status | Output |
|---|---|---|
| Exit code not 0 | `error` | Line `exit code <n>` followed by output |
| Timeout | `error` | `timed out after <n> seconds`. Process group is stopped |
| `timeout_secs` outside 1 to 600 | `error` | `timeout_secs must be 1 to 600`. Command not run |
| Turn cancelled | `error` | `cancelled` |
| Not allowed by gate | `rejected` | Gate reason |

## API-TOOL-007: `web_fetch`

- **Purpose**: fetches a URL and returns its contents as text.
- **Requirements**: `REQ-TOOL-007`
- **Acceptance criteria**: `AC-TOOL-018` through `AC-TOOL-020`
- **Stability**: New
- **Risk**: `Network`
- **Authorization**: `tools.pre_execute` gate (`REQ-TOOL-009`). Hosts are not restricted in this spec (see the risks in the design).

```json
{"type":"object","properties":{"url":{"type":"string","format":"uri","pattern":"^https?://"}},"required":["url"],"additionalProperties":false}
```

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Input | `url` | string | Yes | Must be `http` or `https` |

### Success

| Condition | Status | Output |
|---|---|---|
| `text/html` | `success` | Markdown without `script` and `style`, truncated at 30000 characters |
| `text/plain` or `application/json` | `success` | Contents as-is, truncated at 30000 characters |

Limits: timeout 30 seconds, body maximum 5 MiB, maximum 5 redirects (`Q-014`).

### Failure

| Condition | Status | Output |
|---|---|---|
| URL not `http` or `https` | `error` | `url must be http or https`. No request |
| Other content type | `error` | `unsupported content type: <type>` |
| Body larger than 5 MiB | `error` | `response too large` |
| Timeout after 30 seconds | `error` | `timed out after 30 seconds` |
| HTTP status 4xx or 5xx | `error` | `HTTP <code>` |
| Not allowed by gate | `rejected` | Gate reason |

## Contract traceability

| Operation | Requirements | Acceptance criteria | Design component | Verification |
|---|---|---|---|---|
| `API-LIB-001` | `REQ-LIB-001`, `REQ-LIB-006`, `REQ-LIB-008` | `AC-LIB-001`, `AC-LIB-002`, `AC-LIB-003`, `AC-LIB-013`, `AC-LIB-014` | `lana.go`, `config` loader, `agent` module | Test `lana` with in-memory layers, `MockLlm` |
| `API-LIB-002` | `REQ-LIB-002`, `REQ-LIB-007` | `AC-LIB-004` through `AC-LIB-008`, `AC-LIB-015`, `AC-LIB-016` | `lana.go`, `agent.Registry` | Test `lana` with `MockLlm` and memstore |
| `API-LIB-003` | `REQ-LIB-003` | `AC-LIB-009`, `AC-LIB-010` | `lana.go`, frame hub | Test iterator and context |
| `API-LIB-004` | `REQ-LIB-004` | `AC-LIB-011` | `lana.go`, `agent.Registry` | Test shutdown with a stuck turn |
| `API-LIB-005` | `REQ-LIB-008` | `AC-LIB-017` | `config` (profile data) | Test profiles |
| `API-AGENT-001` | `REQ-LOOP-001`, `REQ-LOOP-002` | `AC-LOOP-001` through `AC-LOOP-006` | `agent.Registry`, `agent.Runner`, `agent.TurnLoop` | Test turns with `MockLlm` |
| `API-AGENT-002` | `REQ-LOOP-002`, `REQ-LIB-002` | `AC-LIB-005` | `agent.Registry` | Test concurrency |
| `API-AGENT-003` | `REQ-LOOP-007` | `AC-LOOP-019` through `AC-LOOP-021` | `agent.Runner` | Test cancel with a stuck `bash` |
| `API-AGENT-004` | `REQ-LOOP-010` | `AC-LOOP-028` | `agent.Registry` | Test selection |
| `API-AGENT-005` | `REQ-LOOP-010` | `AC-LOOP-029`, `AC-LOOP-030` | `agent.Registry` | Test selection |
| `API-AGENT-006` | `REQ-LOOP-009` | `AC-LOOP-026`, `AC-LOOP-027` | `agent.Hub` | Test frames |
| `API-AGENT-007` | `REQ-LOOP-004`, `REQ-LOOP-005`, `REQ-LOOP-008`, `REQ-LOOP-010` | `AC-LOOP-011`, `AC-LOOP-015`, `AC-LOOP-016`, `AC-LOOP-023`, `AC-LOOP-031` | `config`, `agent` | Test config and turns |
| `API-AGENT-008` | `REQ-LOOP-001`, `REQ-LOOP-003`, `REQ-LOOP-004`, `REQ-LOOP-008`, `REQ-LOOP-010` | `AC-LOOP-001`, `AC-LOOP-009`, `AC-LOOP-010`, `AC-LOOP-022`, `AC-LOOP-024`, `AC-LOOP-028`, `AC-LIB-016` | `agent.Events` | Test payload golden files |
| `API-AGENT-009` | `REQ-TOOL-009`, `REQ-LOOP-003`, `REQ-LOOP-006`, `REQ-LIB-005` | `AC-TOOL-023`, `AC-TOOL-024`, `AC-LOOP-007`, `AC-LOOP-018`, `AC-LIB-012` | `tools.Pipeline`, `agent.Hooks` | Test hooks |
| `API-PROV-001` | `REQ-PROV-001` | `AC-PROV-001` through `AC-PROV-004` | `providers/openai` (config) | Test config |
| `API-PROV-002` | `REQ-PROV-002`, `REQ-PROV-003` | `AC-PROV-005` through `AC-PROV-009` | `providers/openai` (conversion, SSE) | Test httptest with SSE fixtures |
| `API-PROV-003` | `REQ-PROV-004`, `REQ-LOOP-008` | `AC-PROV-010` through `AC-PROV-012`, `AC-LOOP-022` through `AC-LOOP-025` | `providers/openai` (classify), `agent.Retry` | Test error table |
| `API-PROV-004` | `REQ-MOCK-001` | `AC-MOCK-001`, `AC-MOCK-002` | `providers/mock` | Unit test |
| `API-TOOL-001` through `API-TOOL-007` | `REQ-TOOL-001` through `REQ-TOOL-008` | `AC-TOOL-001` through `AC-TOOL-022` | `tools/*`, `tools.Guard` | Unit test per tool |

## Blocking Contract Questions

| ID | Question | Controls | Owner | Status | Resolution |
|---|---|---|---|---|---|
| `CQ-001` | Shape of `rt.Stream`: iterator or channel | `API-LIB-003` | Ridho | Resolved | Agent recommendation: `iter.Seq2[Event, error]`. Per ADR 0014 ("iterator event") and Go 1.23+. |
| `CQ-002` | Provider error class names | `API-PROV-003`, `API-LIB-002` | Ridho | Resolved | Agent recommendation: `auth`, `rate_limit`, `server`, `network`, `context_length`, `client`, `missing_secret`. |
| `CQ-003` | MockLlm script format | `API-PROV-004` | Ridho | Resolved | Agent recommendation: JSON, read by `LoadScript`, and matches the Go struct. |
| `CQ-004` | Event payload fields, including `v` and additional fields | `API-AGENT-008` | Ridho | Resolved | Agent recommendation: `v = 1`, `snake_case`. Additional: `model/selection.agent` (`Q-022`) and `session/created.workspace` (`Q-017`). Approved by spec #4. Included in `API-CORE-008` (`CQ-017`, `CQ-029` of spec #2). |
| `CQ-005` | Argument of `Send`: text only or `ContentBlock` | `API-AGENT-001` | Ridho | Resolved | Agent recommendation: text only. Images deferred (`Q-023`). |
| `CQ-006` | Tool status values | `API-TOOL-*` | Ridho | Resolved | Agent recommendation: `success`, `error`, `rejected`, same as spec #1. |
| `CQ-007` | Are `headers` values interpolated or literal | `API-PROV-001` | Ridho | Resolved | Agent recommendation: literal. `${VAR}` interpolation only in `mcp.*` (spec #2 `REQ-CONF-007`). |
| `CQ-008` | Default provider `timeout` | `API-PROV-001` | Ridho | Resolved | Agent recommendation: `300s` total for one request. |
| `CQ-009` | Input name for bash timeout | `API-TOOL-006` | Ridho | Resolved | Agent recommendation: `timeout_secs`, integer, default 120, maximum 600. |
| `CQ-010` | Signature of `core.AgentRegistry` and `core.Provider` | `API-AGENT-*`, `API-PROV-002` | Ridho | Resolved | Agent recommendation: the signatures in this contract are used until the spec #2 contract is Approved. Spec #2 copies these signatures or changes them with a note in spec #2's `CQ`. Revised: see `CQ-012`. |
| `CQ-011` | Names and shapes of additional types added to `core` | Core types section | Ridho | Resolved | Agent recommendation (reconciliation): all "additional" types from revision 1 moved to `API-CORE-015` (spec #2). Renames: `StepInput` to `PreStepInput`, `AgentTurnStopInput` to `TurnStopInput`, `TurnStopVote` to `StopVote`, `ToolDecision.Allow` to `Allowed`, `Agent` to `AgentName`. Shapes of `ChatEvent` and `ToolCall` follow spec #2 (`CQ-019` of spec #2). |
| `CQ-012` | Signature of `AgentRegistry`: `Cancel`, `SetModel`, `SubscribeLive` | `API-AGENT-003`, `API-AGENT-005`, `API-AGENT-006` | Ridho | Resolved | Agent recommendation (reconciliation): signatures of spec #2 (`API-CORE-010`) prevail. `Cancel(ctx, sessionID)`. `SetModel(ctx, sessionID, model)` with model `provider:model`. `SubscribeLive(sessionID)` returns `*core.Subscription[core.LiveFrame]`. |
| `CQ-013` | Model history from the log | `API-AGENT-008`, `Derive` in design | Ridho | Resolved | Agent recommendation (reconciliation): `core.DeriveMessages` (`API-CORE-015`) is used. The store provides `DeriveMessages(ctx, sessionID)`. This spec implements the subset without compaction (`CQ-028` of spec #2). See `Q-024`. |
| `CQ-014` | Context too long error | `API-PROV-003`, `API-LIB-002` | Ridho | Resolved | Agent recommendation (reconciliation): the adapter returns `*core.ProviderError` with class `context_length` for which `errors.Is(err, core.ErrContextTooLong)` (`CQ-026` of spec #2). The turn ends `error` if the compaction of spec #4 is not active (`Q-005`). With compaction, the listener of spec #4 summarizes and retries once. |
| `CQ-015` | Built-in profiles, `store` row, and `headless` | `API-LIB-005`, `API-LIB-001` | Ridho | Resolved | Agent recommendation (reconciliation): built-in profiles are owned by spec #2 (`API-CONF-009`, `CQ-031`). `minimal` is the same as base. `headless` is the same as base plus `cli_runner`. `lana.New` uses `memstore` if `Home` and `Options.Store` are empty (`DQ-016` of spec #2). |
| `CQ-016` | Library approval option `ApproveWith` | `API-LIB-001`, `REQ-LIB-006` | Ridho | Resolved | Agent recommendation (reconciliation): `lana.New(ctx, opts, ...Option)`. `ApproveWith(fn)` is an `Option`. Implementation, contract, and AC are in spec #4 (`REQ-APPR-007`, `AC-APPR-017`, `API-APPR-005`). |
| `CQ-017` | Payload `session/created.workspace` and `model/selection.agent` | `API-AGENT-008` | Ridho | Resolved | Agent recommendation (reconciliation): approved by spec #4. Fields are in `API-CORE-008` (spec #2). |
| `CQ-018` | Kind `anthropic` in this build | `API-PROV-001` | Ridho | Resolved | Agent recommendation (reconciliation): still rejected until M4. `lana init` in spec #1 only offers kinds supported by the build (`CQ-009` of spec #1). |
| `CQ-019` | `CompactNow` on `AgentRegistry` | `API-AGENT-*` | Ridho | Resolved | Agent recommendation (reconciliation): `AgentRegistry` has no `CompactNow`. `/compact` uses `core.Compactor.CompactNow` from spec #4 (`API-COMP-001`, `CQ-027` of spec #2). |

None.

## Approval

- [x] Requirements are Approved and their revisions are recorded above.
- [x] Applicability of the contract is determined.
- [x] Every operation has a stable ID and is linked to requirements and acceptance criteria.
- [x] Input, success, failure, authorization, and compatibility are explicit.
- [x] No behavior exists only in this file.
- [x] Blocking Contract Questions are set to `None`.
- **Approved by**: Ridho (gate released 2026-10-29)
- **Approved on**: 2026-10-29 (revision 1). Revision 2 reconciliation by the agent on behalf of the owner (gate released 2026-10-29).
