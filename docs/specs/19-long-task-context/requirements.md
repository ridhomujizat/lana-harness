# Requirements: Long tasks, prefix cache, and context management

- **Issue**: —
- **Status**: Implemented
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: —
- **Related ADR**: 0009 (session as append-only event log)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-31 | "Build the foundation like deepseek-harness so lana can run long tasks, cache hits do not miss often, and context is handled" | goal |
| Review 2026-10-31 | `agent/turn.go:405` runs `agent.pre_step` only for claimed messages, so auto compaction never runs inside a tool loop; `agent/turn.go:408` passes `AgentName: ""`, so `context_window` is ignored; `core/derive.go:25` drops all history before a summary (no retained tail); `internal/agenthome/prompt.go:94` puts the time to the minute in the system prompt; `providers/openai/adapter.go:38` total HTTP timeout cuts long streams; `agent/retry.go` retries only before the first event, 1/2/4 s, no `Retry-After`; `providers/openai/classify.go:30` detects only `context_length_exceeded` | problem |
| deepseek-harness | `packages/compaction/compaction-basic/README.md`, `packages/compaction/compaction-tool-result-pruner/README.md`, `packages/spill/spill-policy/README.md`, `docs/subsystems/spill.md`, `packages/core/agent-loop/README.md` (KV cache sections) at `/Users/ridho/coding/project/rnd/deepseek-harness` | reference design |

## Goal

One agent turn can run for hundreds of steps without failing on context size, provider hiccups, or
large tool output, and repeated requests reuse the provider prefix cache (DeepSeek bills a cache hit
at a fraction of a miss).

## Definitions

- **Prefix** — the request bytes from the start: system prompt, tool declarations, then messages.
  The provider cache reuses the longest unchanged prefix.
- **Head prompt** — the system prompt sent as `ChatRequest.System`. It changes only at defined points.
- **Pressure** — estimated request tokens above the compaction threshold of the active model.
- **Retained tail** — the newest history kept verbatim after a compaction.
- **Spill** — saving a full tool output to a private file and keeping only a preview plus the path in history.

## Requirements

### Prefix cache

- **REQ-LTC-001** The head prompt contains no value that changes per turn or per minute. The date,
  time, and unavailable MCP servers move to a `<context>` text block that is added to the first user
  message of each turn and is stored in that message's event.
- **REQ-LTC-002** Inside a session the head prompt is rewritten only when (a) the session has no head
  prompt, or (b) a compaction summary landed after the current head. Any other prompt change
  (memory, user file, persona, skills, team) is sent as a `<system-update>` text block in the first
  user message of the turn. The block holds only the sections that changed or were removed.
- **REQ-LTC-003** Tool declarations are sent in a deterministic order (sorted by name). The same
  tool set yields byte-identical declarations across steps and turns.
- **REQ-LTC-004** History is append-only between compactions: no step rewrites an earlier message.
- **REQ-LTC-005** Usage records cached input tokens. The DeepSeek fields `prompt_cache_hit_tokens`
  and `prompt_cache_miss_tokens` and the OpenAI field `prompt_tokens_details.cached_tokens` are
  parsed. `step/end` stores them. The per-step log line and the session usage API report them.

### Context management

- **REQ-LTC-010** The compaction check runs before every model call of a turn, including steps that
  only carry tool results, with the real agent name.
- **REQ-LTC-011** Threshold `T = floor(min(W × 0.8, W − O − B))`: `W` = agent `context_window`
  (default 128000), `O` = output reservation (request `max_tokens`, default 8192), `B` = headroom
  (default 16384). Retained tail budget `R = floor(0.16 × (W − O))`.
- **REQ-LTC-012** Under pressure, old tool results are pruned first, without a model call: a result
  outside the retained tail with more than 8192 characters keeps its first 4096 and last 1024
  characters joined by a marker. The original stays in the log. If the estimate is then below `T`,
  no summary is made.
- **REQ-LTC-013** If pressure remains, the oldest balanced span is replaced by one summary and the
  retained tail stays verbatim. A span never splits an assistant tool call from its result.
- **REQ-LTC-014** The summary request reuses the warm prefix: same head prompt, same tool
  declarations, the span's messages exactly as derived for the conversation, then one final user
  message with the structured compaction instruction. Only returned text is kept.
- **REQ-LTC-015** The summary follows the 8-section structure (request, concepts, files, errors,
  pending work, current work, next step, critical context) and merges a prior summary instead of
  copying it. A summary that is not smaller than its source is rejected.
- **REQ-LTC-016** A provider context-length error (including DeepSeek wording) triggers one forced
  compaction that ignores the threshold, then one retry of the same step.

### Large tool output

- **REQ-LTC-020** A tool result above the inline budget (default 16000 characters) is spilled: the
  full text is saved under `<home>/spill/<session>/`, mode 0600, and the model sees a head and tail
  preview plus `(Omitted N bytes. Full output stored at: <path>. Use read_file with offset/limit, or grep this path.)`.
  `read_file` results are never spilled. A storage failure keeps the inline result.
- **REQ-LTC-021** `read_file`, `grep`, and `glob` may read spill files of the same session even when
  the path is outside the workspace.

### Provider robustness

- **REQ-LTC-030** A stream is cut only when no bytes arrive for the idle timeout (default 120 s),
  not by a total request time limit. Response headers have their own timeout (provider `timeout`,
  default 300 s).
- **REQ-LTC-031** Retryable errors use exponential backoff with jitter (2, 4, 8, 16, 32 s; 5 retries)
  and honor `Retry-After` up to 120 s.
- **REQ-LTC-032** A retryable error after the first stream event and before the assistant message is
  stored retries the step (up to 2 times). The live UI receives a frame that discards the partial text.

### Step budget

- **REQ-LTC-040** No built-in turn budget, as in deepseek-harness (`agent-loop` README, "No built-in
  turn budget"). A turn runs until the model stops calling tools, the owner cancels, or an error
  ends it. `max_steps` becomes opt-in: unset or `0` means no limit for root agents and subagents;
  a positive value (no upper bound) still caps the turn. When a configured cap is reached, lana runs
  one more step without tools asking for a status report (done, remaining work, next step), then
  ends with reason `max_steps`.
- **REQ-LTC-041** Runaway control for unattended work belongs to the next spec (scheduler), modeled
  on deepseek-harness goals: a cap on rounds (turns) per objective, default 256, not on steps.

## Out of scope

- The scheduler / cron (next spec).
- Exact tokenization; the estimate stays at about 4 characters per token, corrected by the last real usage.
- Image offload.
