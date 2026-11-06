# Requirements: Agent loop, provider, tools, Go library

- **Issue**: [`#3`](https://github.com/ridhomujizat/lana-harness/issues/3)
- **Status**: Approved
- **Revision**: 2 (cross-spec reconciliation: context error type, derive in `core`, base profile, `ApproveWith` in spec #4, see Q-027 through Q-032)
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (Go API `lana` and `agent`, config key `[providers.<name>]`, tool input schemas, MockLlm format, `rt.Stream` event types)
- **Related ADRs**: [ADR index](../../adr/README.md) — [0002](../../adr/0002-core-from-modules-services-and-hooks.md), [0003](../../adr/0003-multi-agent-with-channel-routing.md), [0004](../../adr/0004-permission-mode-per-agent.md), [0006](../../adr/0006-extend-via-mcp-client-and-skills.md), [0009](../../adr/0009-session-as-append-only-event-log.md), [0010](../../adr/0010-profile-and-config-patch.md), [0013](../../adr/0013-use-go-as-single-binary.md), [0014](../../adr/0014-integration-as-agentic-tool.md)
- **Related diagrams**: [Diagram index](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-and-approval.md), [0005](../../diagram/0005-agentic-tool-integration.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Issue | [`#3`](https://github.com/ridhomujizat/lana-harness/issues/3) | story and scope of this spec |
| ADR 0002 | Turn, step, hook, `tool/call` and `tool/result` invariants | `REQ-LOOP-001` through `REQ-LOOP-006` |
| ADR 0003 | Inbox per session, claim one message per step, routing | `REQ-LOOP-002`, `REQ-LIB-007` |
| ADR 0004 | Policy `never`, default fail-closed, outcome `unavailable` | `REQ-TOOL-009`, `REQ-LIB-006` |
| ADR 0006 | Risk per tool (`Read`, `Exec`, `Network`) | `REQ-TOOL-009` |
| ADR 0009 | Event log, `assistant/attempt`, `model/selection`, live frames | `REQ-LOOP-003`, `REQ-LOOP-009`, `REQ-LOOP-010`, `REQ-MEM-001` |
| ADR 0010 | Profiles `minimal` and `headless`, `[[modules]]` rows | `REQ-LIB-008` |
| ADR 0013 | Package layout `agent/`, `providers/openai/`, `tools/`, `lana.go` | entire spec |
| ADR 0014 | Go library, `Options`, `Run`, `Stream`, `Close`, `library` channel session | `REQ-LIB-*` |
| Diagram 0002 | Turn and step order, failure handling (retry 3 times then fallback) | `REQ-LOOP-001`, `REQ-LOOP-008` |
| Diagram 0005 | Embedding the Go library and the service-owned tool flow | `REQ-LIB-005` |
| Spec #1 | Section `Required core interface`, tool argument names, exit code 3 for provider | `REQ-LOOP-007`, `REQ-LOOP-009`, `REQ-TOOL-*`, `REQ-PROV-004` |
| Spec #2 | Core: `Message`, `ContentBlock`, `SessionEvent`, `Provider`, `Tool`, `SessionStore`, `AgentRegistry`, hook, `config.Load` | dependency of this spec |
| Owner decision 2026-10-29 | Split of core specs (#2, #3, #4), tool list, `PolicyTrust`, `MockLlm`, memstore | scope |
| Agent resolution | Questions the agent resolved under the owner's permission (see Question resolution) | default values and details missing from the sources |

## Goal

The owner and Go services can run one agent turn from message to final answer. The turn
uses an OpenAI-compatible provider, built-in tools, and an in-memory session. The same loop
is used by the CLI (spec #1) and by the `lana` library. This spec does not add full
permissions, approval, SQLite storage, compaction, or fork.

## Actors

- **Owner** — runs `lana` through the CLI (spec #1). Selects the agent and model. Cannot yet
  allow tools other than `Read` without a `PolicyTrust` listener (default fail-closed).
- **Go service** — imports the `lana` package. Registers its own tools and selects approval
  options.
- **LLM model** — sends text and tool calls. A tool call runs only if it passes the gate and the schema.
- **Developer** — runs tests with `MockLlm` and a fake HTTP server.

## Definitions

- **Turn** — a sequence of zero or more steps for one claimed message, until there is nothing
  left to answer (ADR 0002).
- **Step** — one request to the model, plus the tool calls invoked from that answer.
- **Inbox** — the message queue per session. Messages arrive while a turn is running.
- **Claim** — taking one message from the inbox at the start of a step.
- **Target** — a pair of `provider` and `model`. The first target is the primary, the rest are fallbacks.
- **Default gate** — the behavior of the default `tools.pre_execute` handler: the `Read` tool runs, other tools are rejected.
- **Live frame** — temporary text delta for display. Frames are not written to the log (ADR 0009).
- **Workspace** — the working directory where file tools and `bash` operate.
- **Error class** — a provider failure category: `auth`, `rate_limit`, `server`, `network`,
  `context_length`, `client`, and `missing_secret` (at boot).
- **Tool status** — the result of a tool: `success`, `error`, or `rejected` (rejected by the gate or the workspace boundary).

## Scope

### Included

- Agent loop: turn, step, inbox, claim, `agent.*` hooks, step limit, stopping, cancellation,
  retry, fallback, agent routing, `SetAgent`, `SetModel`, live frames.
- `openai_compat` provider (OpenAI, OpenRouter, Ollama) with SSE streaming and tool calls.
- `MockLlm` for tests.
- Built-in tools: `read_file`, `write_file`, `edit_file`, `glob`, `grep`, `bash`, `web_fetch`.
- Workspace boundary helper and the default `tools.pre_execute` gate.
- In-memory store `memstore` (implementation of `core.SessionStore`) for tests and the library.
- `lana` package: `New`, `Run`, `Stream`, `Close`, custom tools, approval options `PolicyNever` and `PolicyTrust`.
- Built-in profiles `minimal` and `headless` as module rows.

### Not included

- Compaction and the `compaction/summary` event (ADR 0009). Spec #4.
- SQLite store, resume after crash, fork, cross-process lease, and full `derive_messages`. Spec #4.
- Permission engine: `allow` and `deny` rules, session policy, approval service, `approval.request` hook,
  and `lana.ApproveWith`. Spec #4.
- Subagent tools `task` and `task_continue` (ADR 0011). M4.
- Native Anthropic adapter (ADR 0013, `providers/anthropic/`). M4.
- MCP client, skills, and the `skill_load` tool (ADR 0006). M4.
- CLI, TUI, subcommands, and one-shot runner. Spec #1.
- OS sandbox (ADR 0004).
- Image input from the user (`Image` block in user messages).

## Assumptions

| Assumption | Source |
|---|---|
| The types `core.Message`, `core.ContentBlock`, `core.SessionEvent`, `core.Secret`, the interfaces `core.Provider`, `core.Tool`, `core.SessionStore`, `core.AgentRegistry`, typed hooks, and `core.Module` are owned by spec #2 | Owner decision 2026-10-29 |
| The effective config, secrets, and reference validation are loaded by `config.Load` in spec #2 | Spec #2 `REQ-CONF-001`, `REQ-CONF-005`, `REQ-CONF-009` |
| Providers, agents, and routes are read from the profile's effective config | ADR 0010, ADR 0003 |
| Messages that arrive while a turn runs are claimed at the start of the next step | ADR 0003, spec #1 `AC-CHAT-012` |
| Every `tool/call` has a `tool/result`, including when it is rejected, cancelled, or errors | ADR 0002 invariant |
| Every model request can be reconstructed from the log (*model-visible means logged*) | ADR 0009 |
| Text delta frames are not written to the log | ADR 0009 |
| Model format `<provider>:<model>` for `fallback` and `SetModel` | Spec #1 shared convention, ADR 0003 |
| Secrets are never printed, including in logs and errors | Spec #2 `REQ-CONF-005`, spec #1 secret convention |

## Behavior rules

### Agent loop

#### REQ-LOOP-001: Turn and step

The system shall run a turn as zero or more steps according to ADR 0002. The turn is opened with
`turn/start` before the first input is claimed and closed with `turn/end`. Each step claims at most one
message from the inbox.

- `AC-LOOP-001`: Given the session is idle and one message is sent, when the model answers without a tool call, then the log contains `turn/start`, `user/message`, `step/start`, `assistant/message`, `step/end`, and `turn/end` in order, with reason `end_turn`.
- `AC-LOOP-002`: Given the `agent.pre_step` hook rejects the message in the first step, when the turn runs, then there is no `step/start` and the log contains `turn/end`.
- `AC-LOOP-003`: Given two messages are queued when the next step starts, when that step claims input, then only one message is claimed and recorded as `user/message`, and the second message stays in the inbox for the next step.

#### REQ-LOOP-002: Inbox and one active turn per session

The system shall accept messages while a turn is running and place them in the session inbox. Each session
has at most one active turn. Different sessions run in parallel.

- `AC-LOOP-004`: Given a turn is running, when a message is sent to the same session, then the message enters the inbox, no second turn starts, and the sender does not wait for the turn to finish.
- `AC-LOOP-005`: Given two different sessions receive messages at the same time, when both run, then each finishes with its own log and no events are mixed between sessions.
- `AC-LOOP-006`: Given a message arrives while a turn is closing, when the turn ends, then the message is not lost and is answered by the next turn.

#### REQ-LOOP-003: Model call

The system shall build the request from history derived from the log, call the `agent.request` and `llm.stream`
hooks, and record the model's answer as `assistant/message`. Text deltas are not written to the log.

- `AC-LOOP-007`: Given an `agent.request` listener changes `max_tokens`, when the step calls the model, then the provider receives the value returned by the listener.
- `AC-LOOP-008`: Given the last `system/message` contains the same prompt as the current agent prompt, when the next step starts, then no new `system/message` is written. Given the prompt changes, then a new `system/message` is written before `user/message`.
- `AC-LOOP-009`: Given the provider sends several text deltas and one tool call, when the stream finishes, then one `assistant/message` contains a text block and a tool_use block, and there is no log event for each delta.

#### REQ-LOOP-004: Tool call and tool result

The system shall record each tool call as `tool/call`, run the `tools.pre_execute`, `tools.execute`, and
`tools.post_execute` pipeline, and then record `tool/result`. Every `tool/call` has exactly one `tool/result`.
`Read` tools within one step run in parallel. `Write` and `Exec` tools run one at a time.

- `AC-LOOP-010`: Given the model calls three tools, where one is rejected by the gate, one fails, and one succeeds, when the step finishes, then every `tool/call` has exactly one `tool/result` with the same `call_id` and a status matching its outcome.
- `AC-LOOP-011`: Given the model calls a tool that is not in the agent's `tools` list, when the step runs, then the tool is not executed and the `tool/result` has status `error` with the message `tool is not enabled for agent`.
- `AC-LOOP-012`: Given three `Read` tools in one step, when they run, then all three run concurrently and the `tool/result` entries are written in the order of the `tool/call` entries.
- `AC-LOOP-013`: Given a `Write` or `Exec` tool in one step, when it runs, then it runs alone and not concurrently with other tools.
- `AC-LOOP-014`: Given the tool input does not match the tool schema, when the tool is called, then the tool does not run and the `tool/result` has status `error`.

#### REQ-LOOP-005: Step limit

The system shall stop the turn with reason `max_steps` if another step is required but the agent's `max_steps`
limit has been reached. The default `max_steps` is 25.

- `AC-LOOP-015`: Given an agent without `max_steps` and a model that always requests tools, when step 25 finishes, then the turn ends with `turn/end` reason `max_steps`.
- `AC-LOOP-016`: Given an agent with `max_steps = 3` and a turn that needs step 4, when step 3 finishes, then step 4 does not start and the turn ends with reason `max_steps`.

#### REQ-LOOP-006: Turn stopping

The system shall call the `agent.turn_stopping` hook before a turn is closed with `end_turn`. If a
listener returns `Continue`, the turn continues with a new step and no new message, until the step limit is reached.

- `AC-LOOP-017`: Given there is no `agent.turn_stopping` listener and a step finishes without a tool call, when the inbox is empty, then the turn ends with reason `end_turn`.
- `AC-LOOP-018`: Given an `agent.turn_stopping` listener returns `Continue` once, when the step finishes, then a new step starts without a new `user/message`, and the turn ends after that step without another `Continue`.

#### REQ-LOOP-007: Turn cancellation

The system shall stop the turn when `Cancel` is called. A tool that is running is stopped and receives an error
`tool/result`. Messages still in the inbox stay in the inbox.

- `AC-LOOP-019`: Given the `bash` tool is running, when `Cancel` is called, then the process is stopped, a `tool/result` with status `error` and output `cancelled` is written, and `turn/end` with reason `cancelled` is written.
- `AC-LOOP-020`: Given the session is idle, when `Cancel` is called, then no event is written and the result is nil.
- `AC-LOOP-021`: Given a message is in the inbox when the turn is cancelled, when the turn ends, then the message stays in the inbox and is answered by the next turn.

#### REQ-LOOP-008: Provider retry and fallback

The system shall retry the request to the same target up to 3 times for error classes `rate_limit`,
`server`, and `network`, with delays of 1 second, 2 seconds, and 4 seconds. If the target still fails, the system
moves to the next fallback under the same rules. Other error classes are not retried and do not move to a fallback.

- `AC-LOOP-022`: Given the primary replies 429 twice and then succeeds, when the step runs, then two failed `assistant/attempt` entries are recorded, a 1-second and then a 2-second delay occur, and the step finishes with `step/end`.
- `AC-LOOP-023`: Given the primary fails with 5xx four times and the first fallback succeeds, when the step runs, then `step/start` and `step/end` record the fallback provider and model.
- `AC-LOOP-024`: Given all targets fail, when the fallbacks are exhausted, then `turn/end` with reason `error` is written with the last error class, and every attempt is recorded as `assistant/attempt`.
- `AC-LOOP-025`: Given the provider replies with error class `auth`, `client`, or `context_length`, when it occurs, then there is no retry, no fallback, and `turn/end` with reason `error` is written.

#### REQ-LOOP-009: Live frames

The system shall send each text delta to `SubscribeLive` subscribers before the turn finishes. Live frames
are not written to the log. A slow subscriber must not block the turn.

- `AC-LOOP-026`: Given a subscriber is registered and the provider sends three deltas, when the stream runs, then the subscriber receives three `text_delta` frames in order before `turn/end`.
- `AC-LOOP-027`: Given the subscriber buffer is full, when a new frame is sent, then the turn keeps running, the frame is dropped, and a warning is recorded in the log.

#### REQ-LOOP-010: Agent, model, and workspace per session

The system shall use the agent and model from the last `model/selection` in the log, then from the `agent`
in `session/created`. `SetAgent` and `SetModel` take effect from the next step. The workspace is taken from
`workspace` in `session/created`, then the agent's `workspace`, then the cwd when the runtime is created.

- `AC-LOOP-028`: Given the session is idle, when `SetAgent` selects agent `assistant`, then the next turn uses agent `assistant` and `model/selection` contains `agent`, `provider`, and `model` of the new agent.
- `AC-LOOP-029`: Given a turn is running, when `SetModel` is called, then the step currently running does not change, the next step uses the new provider and model, and `model/selection` is written.
- `AC-LOOP-030`: Given `SetModel` with an unregistered provider, when called, then the error names the provider, no event is written, and the model does not change.
- `AC-LOOP-031`: Given `session/created` contains `workspace` `W1` and the agent has `workspace` `W2`, when a file tool is called, then the workspace used is `W1`.

### Provider

#### REQ-PROV-001: `openai_compat` provider configuration

The system shall register each `[providers.<name>]` with `kind = "openai_compat"` as a provider. The provider uses `base_url`, `api_key_env` (optional), `headers`, and `timeout`. Other kinds are rejected in this build.

- `AC-PROV-001`: Given `[providers.openrouter]` with `kind = "openai_compat"` and a valid `base_url`, when the config is loaded, then provider `openrouter` is registered and requests are sent to `base_url` plus `/chat/completions`.
- `AC-PROV-002`: Given `api_key_env` is empty, when a request is sent, then the `Authorization` header is not sent.
- `AC-PROV-003`: Given `kind = "anthropic"`, when booting, then boot fails and the message names the provider and states that the kind is not yet supported in this build.
- `AC-PROV-004`: Given a provider is used by an agent (primary or fallback) and the env `api_key_env` is not set, when booting, then boot fails with error class `missing_secret` naming the env and the provider.

#### REQ-PROV-002: Streaming chat completions

The system shall read the SSE stream from `chat/completions` and produce `text_delta`, `tool_use`,
`usage`, and `stop` events.

- `AC-PROV-005`: Given a stream with three `delta.content` chunks, when it is read, then three `text_delta` events are emitted in order before `stop`.
- `AC-PROV-006`: Given `tool_calls` whose JSON arguments are split across three chunks, when the stream finishes, then one `tool_use` event with complete JSON input is emitted.
- `AC-PROV-007`: Given the last usage chunk contains input and output tokens, when the stream finishes, then the `usage` event carries both values.

#### REQ-PROV-003: Message conversion

The system shall convert `core.Message` (Anthropic Messages API shape) into an OpenAI request and convert
the OpenAI answer back into `core.Message`.

- `AC-PROV-008`: Given history containing system, user, assistant with tool_use, and tool_result, when converted, then the request uses role `system`, `assistant` with `tool_calls`, and `tool` with the same `tool_call_id`.
- `AC-PROV-009`: Given a `ContentBlock` of type Image in history, when converted, then the conversion fails with `ErrUnsupported` and the request is not sent.

#### REQ-PROV-004: Provider error classification

The system shall map provider failures to fixed error classes. Retry, fallback, and exit codes
use these classes.

- `AC-PROV-010`: Given HTTP 401 or 403, when classified, then the class is `auth`.
- `AC-PROV-011`: Given HTTP 429, HTTP 5xx, or a connection failure or timeout, when classified, then the classes are `rate_limit`, `server`, and `network` respectively.
- `AC-PROV-012`: Given HTTP 400 with `error.code` `context_length_exceeded`, when classified, then the class is `context_length`. Other HTTP 4xx become class `client`.

### Mock provider

#### REQ-MOCK-001: MockLlm

The system shall provide a fake provider `MockLlm` for tests. Each `Stream` call plays the next
scripted response.

- `AC-MOCK-001`: Given a script containing delta, tool_use, delay, and error, when `Stream` is called repeatedly, then each call uses the next response in order, the delay runs, and the error is returned with the class written in the script.
- `AC-MOCK-002`: Given the script is exhausted, when `Stream` is called, then an error containing the text `script exhausted` is returned.

### Built-in tools

#### REQ-TOOL-001: `read_file`

The system shall read text files inside the workspace.

- `AC-TOOL-001`: Given a 3000-line text file in the workspace, when `read_file` is called without `offset` and `limit`, then the first 2000 lines are returned with status `success`. Given `offset = 10` and `limit = 5`, then lines 10 through 14 are returned.
- `AC-TOOL-002`: Given a binary file (a NUL byte within the first 8000 bytes), when `read_file` is called, then status `error` with the message `binary file`.

#### REQ-TOOL-002: `write_file`

The system shall create or replace the contents of a text file inside the workspace.

- `AC-TOOL-003`: Given a new path whose parent directory exists in the workspace, when `write_file` is called, then the file is created with the `content` and the output states the number of bytes.
- `AC-TOOL-004`: Given the file already exists, when `write_file` is called, then the old contents are replaced entirely.
- `AC-TOOL-005`: Given the parent directory does not exist, when `write_file` is called, then status `error` and no file is created.

#### REQ-TOOL-003: `edit_file`

The system shall perform an exact string replace within one file and may only replace an occurrence that is unique.

- `AC-TOOL-006`: Given `old_string` appears exactly once, when `edit_file` is called, then only that occurrence is replaced and status is `success`.
- `AC-TOOL-007`: Given `old_string` appears zero times or more than once, when `edit_file` is called, then status `error` with the number of occurrences and the file is unchanged.
- `AC-TOOL-008`: Given `old_string` is empty, when `edit_file` is called, then status `error` with the message invalid input.

#### REQ-TOOL-004: `glob`

The system shall search for paths using a glob pattern (supports `**`) inside the workspace and respect `.gitignore`.

- `AC-TOOL-009`: Given the pattern `**/*.go` and some files match `.gitignore`, when `glob` is called, then the results are sorted workspace-relative paths, and ignored files do not appear.
- `AC-TOOL-010`: Given more than 1000 files match, when `glob` is called, then the first 1000 paths are returned and the output records the total count.

#### REQ-TOOL-005: `grep`

The system shall search text with RE2 regex inside the workspace and respect `.gitignore`.

- `AC-TOOL-011`: Given a valid RE2 pattern and there are matches, when `grep` is called, then each match is shown as `path:line: text` with a limit of 200 matches.
- `AC-TOOL-012`: Given a pattern that is not a valid RE2 pattern, when `grep` is called, then status `error` with the message invalid pattern.
- `AC-TOOL-013`: Given a binary file or a file larger than 1 MiB, when `grep` is called, then that file is skipped.

#### REQ-TOOL-006: `bash`

The system shall run shell commands with the cwd in the workspace, a time limit, and an output limit.

- `AC-TOOL-014`: Given the `command` `pwd`, when `bash` is called, then the command runs with the workspace as cwd and stdout and stderr are returned.
- `AC-TOOL-015`: Given the command has not finished after `timeout_secs` (default 120), when the time runs out, then the process group is stopped and status `error` with a timeout message is returned.
- `AC-TOOL-016`: Given `timeout_secs` greater than 600, when `bash` is called, then status `error` with the 600-second limit message and the command is not run.
- `AC-TOOL-017`: Given the command exits with a non-zero code, when it finishes, then status `error` and the output includes the exit code. Given the output is longer than 30000 characters, then the output is truncated and annotated with the original character count.

#### REQ-TOOL-007: `web_fetch`

The system shall fetch a URL and return its content as text, with size and time limits.

- `AC-TOOL-018`: Given an `https` URL that returns HTML, when `web_fetch` is called, then the output is markdown without `script` and `style` elements, and is truncated to the 30000-character output limit.
- `AC-TOOL-019`: Given content-type `text/plain` or `application/json`, when `web_fetch` is called, then the content is returned as-is. Given another content-type (for example an image or PDF), then status `error`.
- `AC-TOOL-020`: Given a URL that is not `http` or `https`, when `web_fetch` is called, then status `error` without a network request. Given a response larger than 5 MiB or not finished within 30 seconds, then status `error`.

#### REQ-TOOL-008: Workspace boundary for file tools

The system shall reject paths that, after symlink resolution, fall outside the workspace.

- `AC-TOOL-021`: Given an absolute or relative path that, after `EvalSymlinks`, is outside the workspace, when `read_file`, `write_file`, `edit_file`, `glob`, or `grep` is called, then status `rejected` and the file is not touched.
- `AC-TOOL-022`: Given a symlink inside the workspace that points outside the workspace, when `read_file` is called on that symlink, then status `rejected`.

#### REQ-TOOL-009: Default `tools.pre_execute` gate

The system shall reject tools other than `Read` if no `tools.pre_execute` listener allows them (fail-closed). A listener that rejects stops the chain and the tool does not run.

- `AC-TOOL-023`: Given there is no `tools.pre_execute` listener, when the `Read` tool is called, then the tool runs. When the `Write`, `Exec`, or `Network` tool is called, then status `rejected` with reason `default fail-closed` and the tool does not run.
- `AC-TOOL-024`: Given a `tools.pre_execute` listener returns deny, when the tool is called, then the default handler does not run and status is `rejected` with the reason from the listener.

### In-memory session

#### REQ-MEM-001: `memstore`

The system shall provide an in-memory `core.SessionStore` for tests and the library without SQLite. Data
lives only while the process runs.

- `AC-MEM-001`: Given a new session and two events are appended, when `Events(from seq)` is called, then `seq` starts at 1 and increases by 1, and the order matches the order of `Append`.
- `AC-MEM-002`: Given memstore, when `Fork` is called, then error `ErrNotSupported` is returned. When `TryLease` is called, then the lease always succeeds within the same process.
- `AC-MEM-003`: Given the library runtime is closed, when a new runtime is created, then the old session is not found.

### Go library

#### REQ-LIB-001: `lana.New` library boot

The system shall build the runtime from `Options` with a profile, an in-memory config layer, custom tools, and an optional store.

- `AC-LIB-001`: Given `lana.New` with profile `minimal`, an in-memory config layer, and an empty `Home`, when called, then the runtime is ready, no config file is read from disk, and modules `store`, `llm`, `tools`, and `agent` are installed.
- `AC-LIB-002`: Given `Options.Tools` contains a tool named `read_file`, when `lana.New` is called, then the error names `read_file` as the conflicting name.
- `AC-LIB-003`: Given an agent in the config uses a tool that is not registered, when `lana.New` is called, then boot fails and the error names the agent and the tool.

#### REQ-LIB-002: `rt.Run`

The system shall run one turn and return the final result.

- `AC-LIB-004`: Given agent `assistant` and prompt `hello`, when `rt.Run` finishes with an answer, then `Result` contains the final answer `Text`, `SessionID`, `StopReason` `end_turn`, and `Usage`, and the error is nil.
- `AC-LIB-005`: Given a session is running a turn, when `rt.Run` or `Start` is called for that session, then error `ErrSessionBusy` is returned and the message does not enter the inbox.
- `AC-LIB-006`: Given a turn ends with reason `max_steps` or `cancelled`, when `rt.Run` finishes, then `StopReason` matches that reason and the error is nil.
- `AC-LIB-007`: Given the provider fails after retries and fallbacks are exhausted, when `rt.Run` finishes, then `StopReason` is `error` and the error is of type `*lana.ProviderError` carrying the error class.
- `AC-LIB-008`: Given `Request.Prompt` is empty, when `rt.Run` is called, then error `ErrEmptyPrompt` is returned and no session is created.

#### REQ-LIB-003: `rt.Stream`

The system shall emit turn events as an iterator so the service can display progress.

- `AC-LIB-009`: Given a turn with one tool call, when the iterator is read, then the events `Session`, `TextDelta`, `ToolCall`, `ToolResult`, and `Done` (containing `Result`) are emitted in order, and then iteration ends.
- `AC-LIB-010`: Given the `Stream` context is cancelled while a turn is running, when the cancellation happens, then the turn ends with reason `cancelled` and the `Done` event is still emitted with `StopReason` `cancelled`.

#### REQ-LIB-004: `rt.Close`

The system shall close the runtime by stopping running turns within the shutdown time limit.

- `AC-LIB-011`: Given a turn is running, when `rt.Close` is called, then the turn is cancelled, `Close` returns after `turn/end` is recorded or after 30 seconds, and a subsequent `Run` fails with `ErrClosed`.

#### REQ-LIB-005: Service custom tools

The system shall run service-owned tools through the same pipeline as built-in tools.

- `AC-LIB-012`: Given a custom tool `lookup_order` in `Options.Tools` and the agent lists it, when the model calls it, then the tool passes through `tools.pre_execute`, `tools.execute`, and `tools.post_execute`, and the result is recorded as `tool/result`.

#### REQ-LIB-006: Library approval options

The system shall provide two initial options: `PolicyNever` (default) and `PolicyTrust`. Both use the default
gate or an allow-all listener on `tools.pre_execute`.

- `AC-LIB-013`: Given `Approval` is not set or is `PolicyNever`, when the model calls `write_file`, then status `rejected`. When the model calls `read_file`, then the tool runs.
- `AC-LIB-014`: Given `Approval: PolicyTrust`, when the model calls `bash` or `write_file`, then the allow-all listener permits it and the tool runs.

#### REQ-LIB-007: Library routing and channel

The system shall select the agent from the `library` route when `Request.Agent` is empty and record the
`library` channel on the new session.

- `AC-LIB-015`: Given `Request.Agent` is empty and the first route with `channel = "library"` points to `assistant`, when `rt.Run` is called, then agent `assistant` is used. Given there is no `library` route, then error `ErrNoRoute` is returned.
- `AC-LIB-016`: Given a new session is created by `rt.Run`, when `session/created` is written, then the `channel` field is `library`.

#### REQ-LIB-008: Built-in profiles `minimal` and `headless`

The system shall provide the built-in profiles `minimal` and `headless` as lists of module rows that use the modules of this spec.

- `AC-LIB-017`: Given profile `minimal` or `headless`, when the effective config is read, then the base module rows used by this spec (`store`, `llm`, `tools`, `agent`) match the definitions in the contract (`API-LIB-005`) and `API-CONF-009` of spec #2.

## State and operation matrix

| Current state | Operation | Result | Allowed |
|---|---|---|---:|
| Idle | `Send` with text | New turn starts | Yes |
| Idle | `Start` (library) | New turn starts, `turn_id` returned | Yes |
| Idle | `Cancel` | No event, result nil | Yes |
| Idle | `SetAgent` or `SetModel` | `model/selection` written, applies to next turn | Yes |
| Turn running | `Send` | Message enters inbox | Yes |
| Turn running | `Start` (library) | Error `ErrSessionBusy` | No |
| Turn running | `Cancel` | Turn ends `cancelled`; inbox unchanged | Yes |
| Turn running | `SetAgent` or `SetModel` | Applies from next step | Yes |
| Turn closing | `Send` | Message answered by next turn (`AC-LOOP-006`) | Yes |
| Runtime closed | `Send`, `Start`, `Run`, `Stream` | Error `ErrClosed` | No |

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Provider `auth` (401, 403) | No retry, no fallback, turn ends `error` | Error class `auth`, exit 3 in CLI |
| Provider `rate_limit` or `server` or `network` until exhausted | Retry 3 times per target, then fallback, then `error` | Last error class |
| Provider `client` or `context_length` | No retry, turn ends `error` | Error class `client` or `context_length` |
| Tool rejected by default gate | Status `rejected` to model, turn keeps running | `tool/result` status `rejected` |
| Tool failed (timeout, invalid pattern, binary file) | Status `error` to model | `tool/result` status `error` |
| Path outside workspace | Status `rejected` | `tool/result` status `rejected` |
| Model calls a tool that is not enabled | Status `error`, tool does not run | `tool/result` status `error` |
| `SetModel` to an unregistered provider | Error with provider name, model unchanged | Go error |
| Provider kind `anthropic` in this build | Boot fails | Boot error |
| Env `api_key_env` empty for a provider in use | Boot fails | Error class `missing_secret` |
| Custom tool name conflicts with a built-in tool | `lana.New` fails | Boot error |
| `web_fetch` body larger than 5 MiB | Status `error` | `tool/result` status `error` |

## Authorization and security

- Tools other than `Read` (`write_file`, `edit_file`, `bash`, `web_fetch`) run only if a
  `tools.pre_execute` listener allows them (`REQ-TOOL-009`). This spec only provides
  `PolicyTrust` as a full-permission listener.
- File tools cannot read or write outside the workspace after symlink resolution (`REQ-TOOL-008`).
- `bash` does not use a sandbox. Commands run as the user who runs `lana` (ADR 0004).
- `web_fetch` accepts only `http` and `https`. Response content is treated as data, not instructions (ADR 0006).
- Secrets (`core.Secret`) are not printed to events, logs, tool output, or errors (spec #2 `REQ-CONF-005`).

## Data integrity and concurrency

- Every `tool/call` has exactly one `tool/result` (`AC-LOOP-010`).
- Events of one session are written in order. There are never two active turns in the same session (`AC-LOOP-004`).
- Messages in the inbox are not lost when a turn ends, is cancelled, or closes (`AC-LOOP-006`, `AC-LOOP-021`).
- Live frames are temporary. The final answer is always in `assistant/message` (ADR 0009).
- Cancellation does not leave running tool processes behind (`AC-LOOP-019`).

## Accessibility

Not applicable. This spec has no user interface. Live frame display and CLI output are
governed by spec #1.

## Scenarios

### SC-001: Answer in one turn without tools

**Given** session `S` is idle with agent `assistant` and the provider returns a text answer
**When** the message `hello` is sent to `S`
**Then** the log contains `turn/start`, `user/message`, `step/start`, `assistant/message`, `step/end`, and `turn/end` with reason `end_turn`

### SC-002: Tool rejected by the gate, then the model continues

**Given** agent `coder` has no permission listener and the model calls `bash` and `read_file` in one step
**When** the first step finishes
**Then** `bash` produces a `tool/result` with status `rejected`, `read_file` runs and produces a `tool/result` with status `success`, and the next step starts with both results

### SC-003: Message arrives while a turn is running

**Given** turn `T1` is running in session `S` and the model calls `read_file`
**When** the user sends the message `summarize the result`
**Then** the message enters the inbox, is claimed at the start of the next step, and is recorded as `user/message` in that step

### SC-004: Provider rate-limited, then fallback

**Given** primary `openai:gpt-5-mini` replies 429 four times and fallback `openrouter:openai/gpt-5-mini` succeeds
**When** the step runs
**Then** the log contains four `assistant/attempt` entries from the primary, the next `step/start` records provider `openrouter`, and the turn finishes with `end_turn`

### SC-005: Library cancelled through context

**Given** a Go service calls `rt.Stream` with a context that is cancelled while `bash` is running
**When** the context is cancelled
**Then** the `bash` process stops, `tool/result` has status `error`, and the `Done` event is emitted with `StopReason` `cancelled`

## Question resolution

The owner released the approval gate on 2026-10-29. For each open question, the agent selects the
simplest recommended option and records it here.

| ID | Question | Controls | Requested reference | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `Q-001` | Library approval option: whether `ApproveWith` is part of this spec | `API-LIB-001`, `REQ-LIB-006` | Not requested (gate released) | Ridho | Resolved | Agent recommendation: only `PolicyNever` (default) and `PolicyTrust`. `lana.ApproveWith` is assigned to spec #4 (`REQ-APPR-007`, `AC-APPR-017`, `API-APPR-005`) because it needs `approval.request`. Revised: see `Q-030`. |
| `Q-002` | Step limit per agent and config name | `REQ-LOOP-005`, `API-AGENT-007` | Not requested | Ridho | Resolved | Agent recommendation: optional key `agents.<name>.max_steps`, integer 1 to 200, default 25. |
| `Q-003` | Retry and backoff rules | `REQ-LOOP-008` | Not requested | Ridho | Resolved | Agent recommendation: 3 retries per target with delays of 1 s, 2 s, 4 s. Each target (primary and each fallback) uses the same rules. |
| `Q-004` | Network errors (dropped connection, timeout) | `REQ-PROV-004`, `REQ-LOOP-008` | Not requested | Ridho | Resolved | Agent recommendation: class `network` is treated like `server` (retry, then fallback). The owner's request mentions 429 and 5xx; the agent adds `network` as it is the same kind of transient failure. |
| `Q-005` | Context too long | `REQ-LOOP-008`, `REQ-PROV-004` | Not requested | Ridho | Resolved | Agent recommendation: without the compaction module, the turn ends `error` with class `context_length`. With the compaction module (spec #4), the `llm.stream` listener summarizes and retries once (`AC-COMP-006`, `AC-COMP-007`). Both behaviors apply according to `Q-027`. |
| `Q-006` | Failure mid-stream after deltas were sent | `REQ-LOOP-008` | Not requested | Ridho | Resolved | Agent recommendation: not retried, because the deltas have already been shown. The attempt is recorded as `assistant/attempt` and the turn ends `error`. |
| `Q-007` | Messages queued during `Cancel` | `REQ-LOOP-007` | Not requested | Ridho | Resolved | Agent recommendation: messages stay in the inbox and are answered by the next turn. No message is silently dropped. |
| `Q-008` | Message rejected by `agent.pre_step` in a step other than the first | `REQ-LOOP-001` | Not requested | Ridho | Resolved | Agent recommendation: same as the first step. The message is not written to the log, the turn ends `end_turn`, and the reason is written to the application log. |
| `Q-009` | Parallelism of `Read` tools | `REQ-LOOP-004` | Not requested | Ridho | Resolved | Agent recommendation: at most 8 `Read` tools run concurrently in one batch. |
| `Q-010` | Tool output and result limits | `REQ-TOOL-001`, `REQ-TOOL-004`, `REQ-TOOL-005`, `REQ-TOOL-006`, `REQ-TOOL-007` | Not requested | Ridho | Resolved | Agent recommendation: text output of 30000 characters (`bash`, `read_file`, `web_fetch`). `glob` maximum 1000 results. `grep` maximum 200 matches and skips files larger than 1 MiB or binary. The rest is truncated with an annotation. |
| `Q-011` | `bash` behavior for exit code and timeout | `REQ-TOOL-006` | Not requested | Ridho | Resolved | Agent recommendation: non-zero exit code gives status `error` with the code in the output. `timeout_secs` outside 1 to 600 is rejected before execution. Default 120. |
| `Q-012` | Input shape of `read_file`, `write_file`, `edit_file` | `REQ-TOOL-001` through `REQ-TOOL-003` | Not requested | Ridho | Resolved | Agent recommendation: `read_file` uses `offset` (1-based) and `limit` (1 to 2000, default 2000 lines). `write_file` overwrites the file and does not create the parent directory. `edit_file` has no `replace_all`. |
| `Q-013` | `.gitignore` rules for `glob` and `grep` | `REQ-TOOL-004`, `REQ-TOOL-005` | Not requested | Ridho | Resolved | Agent recommendation: only the `.gitignore` at the workspace root is used, and the `.git` directory is always skipped. |
| `Q-014` | `web_fetch` limits | `REQ-TOOL-007` | Not requested | Ridho | Resolved | Agent recommendation: timeout 30 seconds, body maximum 5 MiB (larger is an error), maximum 5 redirects, only `http` and `https`, HTML converted to markdown, types other than text or JSON rejected. |
| `Q-015` | Contents of `Result.Text` and the `rt.Run` error | `REQ-LIB-002` | Not requested | Ridho | Resolved | Agent recommendation: `Text` is the `text` block content of the last `assistant/message` in the turn. An error is returned only if `StopReason` is `error`. `max_steps` and `cancelled` do not produce an error. |
| `Q-016` | Default profile and meaning of an empty `Home` in the library | `REQ-LIB-001` | Not requested | Ridho | Resolved | Agent recommendation: default profile `minimal`. An empty `Home` means no config file is read and there is no fallback to `~/.lana`. |
| `Q-017` | Workspace source | `REQ-LOOP-010`, `REQ-TOOL-008` | Not requested | Ridho | Resolved | Agent recommendation: `session/created.workspace`, then `agents.<name>.workspace`, then the cwd at `New`. Each path is normalized with `filepath.EvalSymlinks`. |
| `Q-018` | Library calls a session that is already running | `REQ-LIB-002` | Not requested | Ridho | Resolved | Agent recommendation: `Run` and `Stream` reject with `ErrSessionBusy`. Queued sending is only available through `Send` (CLI path). |
| `Q-019` | Slow live frame subscriber | `REQ-LOOP-009` | Not requested | Ridho | Resolved | Agent recommendation: buffer of 1024 frames per subscriber. Frames that do not fit are dropped with a warning in the log. The final answer stays in the log. |
| `Q-020` | Provider used by an agent without an env key | `REQ-PROV-004`, `REQ-PROV-001` | Not requested | Ridho | Resolved | Agent recommendation: checked at boot for the primary and fallback providers used by the agent. Fails with class `missing_secret`. Providers not used by any agent are not checked. |
| `Q-021` | Kind `anthropic` in this build | `REQ-PROV-001` | Not requested | Ridho | Resolved | Agent recommendation: rejected at boot until M4. The conflict with spec #1 (`lana init` offers Anthropic, `SC-003` of spec #1) must be resolved in spec #1. |
| `Q-022` | Content of `model/selection` on `SetAgent` | `REQ-LOOP-010` | Not requested | Ridho | Resolved | Agent recommendation: the `model/selection` payload gets an extra field `agent`. The field is present only on `SetAgent`. Approved by spec #4 and included in `API-CORE-008` (`Q-032`). |
| `Q-023` | Image blocks in history | `REQ-PROV-003` | Not requested | Ridho | Resolved | Agent recommendation: not supported in this spec. Conversion fails with `ErrUnsupported`. `Send` input is text only. |
| `Q-024` | Who builds the history (`derive`) | `REQ-LOOP-003` | Not requested | Ridho | Resolved | Agent recommendation: this spec builds a minimal derive (user, assistant, tool result) as a subset of `core.DeriveMessages`. Compaction and `inherited_seq` for fork are included in spec #4. Revised: see `Q-028`. |
| `Q-025` | Tool status for paths outside the workspace | `REQ-TOOL-008` | Not requested | Ridho | Resolved | Agent recommendation: `rejected`, consistent with `AC-CHAT-022` in spec #1. |
| `Q-026` | `headless` profile before the runner | `REQ-LIB-008` | Not requested | Ridho | Resolved | Agent recommendation: `headless` = base plus `cli_runner` (spec #1). Permission and approval modules come from base (spec #2 `CQ-031`). Revised: see `Q-029`. |
| `Q-027` | Context error type too long | `REQ-LOOP-008`, `REQ-PROV-004` | Not requested | Ridho | Resolved | Agent recommendation (reconciliation): the adapter creates `*core.ProviderError` with class `context_length` that matches `core.ErrContextTooLong` (`CQ-026` of spec #2). The turn ends `error` without compaction. With compaction from spec #4, retry once. Consistent with `Q-005`. |
| `Q-028` | Location of the history derive function | `REQ-LOOP-003`, `REQ-STORE-011` | Not requested | Ridho | Resolved | Agent recommendation (reconciliation): `core.DeriveMessages` in `core` (`CQ-028` of spec #2). This spec implements the non-compaction subset. `system/message` is not part of the history. That message is used as `ChatRequest.System`. |
| `Q-029` | Profiles and library before specs #4 and #1 | `REQ-LIB-008`, `REQ-LIB-001` | Not requested | Ridho | Resolved | Agent recommendation (reconciliation): built-in profiles come from spec #2. `minimal` = base. `headless` = base plus `cli_runner`. A library without `Home` uses `memstore`. Before specs #4 and #1 are done, tests use `Layers` without built-in profiles (`DQ-011`). |
| `Q-030` | Library approval option `ApproveWith` | `REQ-LIB-006` | Not requested | Ridho | Resolved | Agent recommendation (reconciliation): `ApproveWith` is assigned to spec #4 (`REQ-APPR-007`, `AC-APPR-017`). This spec provides the `Option` type and `PolicyNever`/`PolicyTrust`. Revision of `Q-001`. |
| `Q-031` | Kind `anthropic` and conflict with spec #1 | `REQ-PROV-001` | Not requested | Ridho | Resolved | Agent recommendation (reconciliation): conflict `Q-021` resolved in spec #1 (`REQ-INIT-001` revision 6). `lana init` offers only kinds supported by the build. `anthropic` stays rejected at boot until M4. |
| `Q-032` | Payload of `session/created.workspace` and `model/selection.agent` | `REQ-LOOP-010` | Not requested | Ridho | Resolved | Agent recommendation (reconciliation): approved by spec #4. Both fields are included in `API-CORE-008` (spec #2, `CQ-029`). |

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-LOOP-001` | `AC-LOOP-001`, `AC-LOOP-002`, `AC-LOOP-003` | ADR 0002, ADR 0003 |
| `REQ-LOOP-002` | `AC-LOOP-004`, `AC-LOOP-005`, `AC-LOOP-006` | ADR 0002, ADR 0003, `Q-007` |
| `REQ-LOOP-003` | `AC-LOOP-007`, `AC-LOOP-008`, `AC-LOOP-009` | ADR 0002, ADR 0009, `Q-024` |
| `REQ-LOOP-004` | `AC-LOOP-010`, `AC-LOOP-011`, `AC-LOOP-012`, `AC-LOOP-013`, `AC-LOOP-014` | ADR 0002, ADR 0006, `Q-009` |
| `REQ-LOOP-005` | `AC-LOOP-015`, `AC-LOOP-016` | ADR 0002, `Q-002` |
| `REQ-LOOP-006` | `AC-LOOP-017`, `AC-LOOP-018` | ADR 0002, Diagram 0002 |
| `REQ-LOOP-007` | `AC-LOOP-019`, `AC-LOOP-020`, `AC-LOOP-021` | Spec #1 `AC-CHAT-016`, `Q-007` |
| `REQ-LOOP-008` | `AC-LOOP-022`, `AC-LOOP-023`, `AC-LOOP-024`, `AC-LOOP-025` | Diagram 0002, `Q-003` through `Q-006` |
| `REQ-LOOP-009` | `AC-LOOP-026`, `AC-LOOP-027` | ADR 0009, Spec #1 `AC-CHAT-009`, `Q-019` |
| `REQ-LOOP-010` | `AC-LOOP-028`, `AC-LOOP-029`, `AC-LOOP-030`, `AC-LOOP-031` | ADR 0003, ADR 0009, `Q-017`, `Q-022` |
| `REQ-PROV-001` | `AC-PROV-001`, `AC-PROV-002`, `AC-PROV-003`, `AC-PROV-004` | ADR 0014, ADR 0013, `Q-020`, `Q-021` |
| `REQ-PROV-002` | `AC-PROV-005`, `AC-PROV-006`, `AC-PROV-007` | ADR 0013, Owner decision 2026-10-29 |
| `REQ-PROV-003` | `AC-PROV-008`, `AC-PROV-009` | ADR 0002 (Anthropic shape), `Q-023` |
| `REQ-PROV-004` | `AC-PROV-010`, `AC-PROV-011`, `AC-PROV-012` | Diagram 0002, `Q-004`, `Q-005` |
| `REQ-MOCK-001` | `AC-MOCK-001`, `AC-MOCK-002` | Owner decision 2026-10-29 |
| `REQ-TOOL-001` | `AC-TOOL-001`, `AC-TOOL-002` | Owner decision 2026-10-29, `Q-010`, `Q-012` |
| `REQ-TOOL-002` | `AC-TOOL-003`, `AC-TOOL-004`, `AC-TOOL-005` | Owner decision 2026-10-29, `Q-012` |
| `REQ-TOOL-003` | `AC-TOOL-006`, `AC-TOOL-007`, `AC-TOOL-008` | Owner decision 2026-10-29 (exact string replace) |
| `REQ-TOOL-004` | `AC-TOOL-009`, `AC-TOOL-010` | Owner decision 2026-10-29, `Q-010`, `Q-013` |
| `REQ-TOOL-005` | `AC-TOOL-011`, `AC-TOOL-012`, `AC-TOOL-013` | Owner decision 2026-10-29, `Q-010`, `Q-013` |
| `REQ-TOOL-006` | `AC-TOOL-014`, `AC-TOOL-015`, `AC-TOOL-016`, `AC-TOOL-017` | Owner decision 2026-10-29, `Q-010`, `Q-011` |
| `REQ-TOOL-007` | `AC-TOOL-018`, `AC-TOOL-019`, `AC-TOOL-020` | Owner decision 2026-10-29, `Q-014` |
| `REQ-TOOL-008` | `AC-TOOL-021`, `AC-TOOL-022` | ADR 0004, Spec #1 `AC-CHAT-022`, `Q-025` |
| `REQ-TOOL-009` | `AC-TOOL-023`, `AC-TOOL-024` | ADR 0004, ADR 0006, Owner decision 2026-10-29 |
| `REQ-MEM-001` | `AC-MEM-001`, `AC-MEM-002`, `AC-MEM-003` | Owner decision 2026-10-29, ADR 0009 |
| `REQ-LIB-001` | `AC-LIB-001`, `AC-LIB-002`, `AC-LIB-003` | ADR 0014, `Q-016` |
| `REQ-LIB-002` | `AC-LIB-004`, `AC-LIB-005`, `AC-LIB-006`, `AC-LIB-007`, `AC-LIB-008` | ADR 0014, `Q-015`, `Q-018` |
| `REQ-LIB-003` | `AC-LIB-009`, `AC-LIB-010` | ADR 0014 |
| `REQ-LIB-004` | `AC-LIB-011` | ADR 0014, spec #2 `REQ-MOD-003` (30-second limit) |
| `REQ-LIB-005` | `AC-LIB-012` | ADR 0014, Diagram 0005 |
| `REQ-LIB-006` | `AC-LIB-013`, `AC-LIB-014` | ADR 0004, ADR 0014, `Q-001` |
| `REQ-LIB-007` | `AC-LIB-015`, `AC-LIB-016` | ADR 0003, ADR 0014 |
| `REQ-LIB-008` | `AC-LIB-017` | ADR 0010, `Q-026` |

Every requirement and acceptance criterion appears exactly once.

## Blocking Open Questions

None.

## Contract commitments

| Requirement | Visible commitment | Contract operation |
|---|---|---|
| `REQ-LOOP-001`, `REQ-LOOP-002` | Order of turn events, one active turn per session, queued messages | `API-AGENT-001` |
| `REQ-LOOP-002` | Library Start rejects a running session | `API-AGENT-002` |
| `REQ-LOOP-007` | Cancellation and `cancelled` event | `API-AGENT-003` |
| `REQ-LOOP-009` | `text_delta` frames per session | `API-AGENT-006` |
| `REQ-LOOP-010` | Agent, model, workspace per session | `API-AGENT-004`, `API-AGENT-005` |
| `REQ-LOOP-003`, `REQ-LOOP-004`, `REQ-LOOP-005`, `REQ-LOOP-008`, `REQ-LOOP-010` | Event payload shapes in the log and agent config keys | `API-AGENT-007`, `API-AGENT-008` |
| `REQ-TOOL-009` | Default gate hook and pipeline order | `API-AGENT-009` |
| `REQ-PROV-001`, `REQ-PROV-004` | Provider config keys and error classes | `API-PROV-001`, `API-PROV-003` |
| `REQ-PROV-002`, `REQ-PROV-003` | OpenAI wire format | `API-PROV-002` |
| `REQ-MOCK-001` | MockLlm script format | `API-PROV-004` |
| `REQ-TOOL-001` through `REQ-TOOL-008` | Name, input, and output of each tool | `API-TOOL-001` through `API-TOOL-007` |
| `REQ-LIB-001` through `REQ-LIB-008` | Go API `lana`, errors, event stream, profiles | `API-LIB-001` through `API-LIB-005` |

## Approval

- [x] Scope and explicit exclusions.
- [x] Measurable success and failure behavior.
- [x] Explicit authorization or declared not applicable.
- [x] Explicit state transitions and destructive effects.
- [x] Explicit defaults, ordering, retry, and idempotency.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Explicit applicability of the contract.
- [x] Blocking Open Questions set to `None`.
- **Approved by**: Ridho (gate released 2026-10-29)
- **Approved on**: 2026-10-29 (revision 1). Revision 2 reconciliation by the agent on behalf of the owner (gate released 2026-10-29).
