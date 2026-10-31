# Tasks: Long tasks, prefix cache, and context management

Each task is one `dsh-coder` run. Tasks in the same wave touch disjoint files and may run in parallel.
Test command for every task: `nix develop -c go build ./... && nix develop -c go test ./...`.

## Wave 1

### T-001 Provider: cache usage, overflow class, timeouts, backoff

- Files: `core/chat.go`, `core/provider.go`, `providers/openai/*.go`, `agent/retry.go`, `config/validate.go` (provider key `stream_idle_timeout` only).
- Covers: REQ-LTC-005 (parsing only), REQ-LTC-016 (classification), REQ-LTC-030, REQ-LTC-031. Design §1.4, §2.8, §4.1, §4.2.
- Do not touch `agent/turn.go`.
- Acceptance:
  - SSE usage with `prompt_cache_hit_tokens` and with `prompt_tokens_details.cached_tokens` fills `Usage.CachedInputTokens`.
  - A 400 body `{"error":{"message":"This model's maximum context length is 131072 tokens...","type":"invalid_request_error","code":"invalid_request_error"}}` classifies as `context_length`.
  - A stream that stops sending bytes fails after the idle timeout (test with a short timeout); a slow but steady stream longer than `timeout` succeeds.
  - `Retry-After: 3` sets `RetryAfter` to 3 s; retry uses it; default delays are 2, 4, 8, 16, 32 s with jitter, injectable in tests.

### T-002 Stable head prompt and tool order

- Files: `internal/agenthome/prompt.go`, `agent/turn.go` (prompt/system message and tool list code only), `core/event.go` (`SystemMessage.Sections`, `UserMessage.PromptSections`), tests.
- Covers: REQ-LTC-001–004. Design §1.1–§1.3.
- Acceptance:
  - The head prompt has no date or unavailable-MCP line; the first user message of every turn has a `<context>` block with them.
  - Two turns in one session with unchanged config send byte-identical `ChatRequest.System` and `Tools`.
  - Changing the memory file between turns keeps `System` unchanged and adds a `<system-update>` block with only `# Your memory`.
  - After a `compaction_summary` event, the next turn writes a new head prompt.
  - Tool specs are sorted by name.

### T-003 Spill large tool output

- Files: new `internal/spill/`, `agent/toolrunner.go`, `tools/bash.go`, `tools/webfetch.go`, `tools/search.go`, `tools/guard.go`, `tools/files.go` (guard use only), `mcp/tool.go`, tests.
- Covers: REQ-LTC-020, REQ-LTC-021. Design §3.
- Acceptance:
  - A 100 000-character bash output yields an inline result of at most 16 000 characters with the notice and path; the file holds the full text, mode 0600, dir 0700.
  - `read_file` on that path works from a session whose workspace is elsewhere; another session's spill path is refused.
  - `read_file` output is never spilled; a failing store keeps the inline output.

## Wave 2 (after wave 1 is merged into the working tree)

### T-004 Compaction engine

- Files: `agent/compaction/*`, `core/derive.go`, `core/event.go` (`CompactionSummary` fields, `ToolResultPruned`), the `core.Compactor` interface, `agent/turn.go` (call site before `DeriveMessages` only), `core/config.go` and `config/validate.go` (agent keys `compaction_headroom`, `compaction_threshold`, `compaction_retain`), tests.
- Covers: REQ-LTC-010–016. Design §2.
- Acceptance:
  - In a mock session with 60 tool steps of 6000 characters each and `context_window = 32000`, the turn finishes; compaction runs inside the turn; derived history = preamble+summary, then a retained tail that starts at a unit boundary.
  - Pruning alone resolves pressure when large tool results dominate (no summary call made).
  - The summary request has the same `System`, `Tools`, and leading messages as the last conversation request, plus one final user message with the 8-section instruction.
  - A `context_length` error on a step triggers one forced compaction and one retry.
  - Old sessions with a summary lacking `retained_from_seq` derive as before.
  - `context_window` set on the agent is used (the agent name reaches the engine).

## Wave 3

### T-005 Turn robustness and metrics

- Files: `agent/turn.go`, `config/validate.go` (`max_steps` range), the web agent form (`max_steps` empty = no limit), `channels/telegram/chat.go`, `web/sse.go` and the web UI chat view, `web/sessions.go` (usage response), tests.
- Covers: REQ-LTC-005 (summing, log, API), REQ-LTC-032, REQ-LTC-040. Design §1.4, §4.3, §5.
- Acceptance:
  - A stream that fails after its first text event is retried; the stored history has one assistant message for the step; the live UI receives `step_retry`.
  - With `max_steps` unset, a root and a child turn run past 200 steps (mock provider) and end only when the model stops calling tools.
  - `max_steps = 300` passes validation; a configured cap, when reached, produces one final report step without tools and reason `max_steps`.
  - Step log lines include `cached_input_tokens` and `cache_hit_pct`; the session usage API returns `cached_input_tokens`.
