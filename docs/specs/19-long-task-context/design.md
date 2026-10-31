# Design: Long tasks, prefix cache, and context management

Reference design: deepseek-harness (`/Users/ridho/coding/project/rnd/deepseek-harness`),
packages `compaction-basic`, `compaction-tool-result-pruner`, `spill-policy`, `agent-loop`.
This design keeps lana's shape (Go, SQLite event log, hooks) and copies the behavior, not the code.

## 1. Request layout and the prefix cache

DeepSeek caches by prefix. Every request is:

```
System (head prompt) | Tools (sorted) | summary? | retained/appended history ... | newest input
```

Anything that changes early in this layout makes the rest a cache miss. Rules:

| Part | Changes when | Cache effect |
|---|---|---|
| Head prompt | session start, or the first turn after a compaction | full miss (once) |
| Tools | tool set changes (MCP attach/detach, config edit) | full miss (rare) |
| Summary | compaction | miss from the summary on |
| History | append only | hit up to the new tail |
| `<context>` / `<system-update>` blocks | inside the new user message | no effect on earlier bytes |

### 1.1 Prompt sections (`internal/agenthome/prompt.go`)

`Build` returns the same string as today minus the volatile lines. Add
`BuildSections(in) []Section` (`Section{Name, Text string}`) so the turn loop can diff sections.
`Build` joins the sections. The runtime section keeps `agent`, `channel`, `workspace` only.

New `ContextBlock(in) string` renders:

```
<context>
date: 2026-10-31 Saturday 14:05 WIB (UTC+07:00)
unavailable MCP servers: a (reason), b (reason)
</context>
```

### 1.2 Head prompt and updates (`agent/turn.go`, `core/event.go`)

`core.SystemMessage` gains `Sections []PromptSection` (name + text) so a later turn can diff
without re-parsing. Rules at the first step of a turn (the place where `promptBuilt` is set today):

1. Let `head` = the latest `system_message` event; `compacted` = a `compaction_summary` event with
   a seq greater than `head.seq`.
2. No `head`, or `compacted`: append a new `system_message` with the full prompt and sections.
   This becomes the head.
3. Otherwise compare the new sections with the effective sections (head sections overlaid with
   every `system_update` recorded since the head). Changed or removed sections are rendered as:

   ```
   <system-update>
   The following sections of your instructions changed. They replace the earlier versions.
   # Your memory
   ...
   </system-update>
   ```

   The block is added to the turn's first user message content, and the new section set is stored
   on that user message event (`UserMessage.PromptSections`, omitted when unchanged) so the next
   diff is exact.
4. `ChatRequest.System` is always the head text, never the newest build.

The `<context>` block is added on every turn's first user message (also for continue messages).
Inbox messages claimed mid-turn get no block.

### 1.3 Tool order

After `filterDisabledTools`, `sort.Strings(turnTools)`. `tools.Registry.Specs` keeps input order.

### 1.4 Cache metrics

`core.Usage` gains `CachedInputTokens int64 json:"cached_input_tokens,omitempty"`.
`InputTokens` keeps meaning "all prompt tokens" (hit + miss). The OpenAI adapter
(`providers/openai/sse.go`) parses `prompt_cache_hit_tokens` (DeepSeek) or
`prompt_tokens_details.cached_tokens` (OpenAI). The turn loop sums it like the other fields, and the
step log line adds `cache_hit_pct`. The web session usage response adds `cached_input_tokens`.

## 2. Context management (`agent/compaction`)

### 2.1 Where it runs

Replace the `agent.pre_step` listener with a direct call in the turn loop, right before
`DeriveMessages` for every step:

```go
if c := rn.reg.compactor(); c != nil {
    c.CompactIfNeeded(ctx, core.CompactRequest{SessionID, TurnID, AgentName: resolved.name,
        Provider, Model, System: headText, Tools: specs, Trigger: "pressure"})
}
```

`core.Compactor` gains `CompactIfNeeded`. The `llm.stream` overflow listener stays and calls the
same engine with `Trigger: "overflow"`. Failure logs a warning and the step continues.

### 2.2 Measuring

`estimate = lastStep.Usage.InputTokens + lastStep.Usage.OutputTokens + chars(events after that step_end)/4`.
Without a usable `step_end` after the latest compaction, use `chars(system + tools JSON + derived messages)/4`.
`W`, `O`, `B`, ratios come from the agent config:

| Agent key | Default |
|---|---|
| `context_window` | 128000 |
| `compaction_headroom` | 16384 |
| `compaction_threshold` | 0.8 |
| `compaction_retain` | 0.16 |

`O` = request `max_tokens` when set, else 8192. Validation: `T > R`, all positive.

### 2.3 Units and balanced boundaries

`core.DeriveMessages` already produces the model view. Group derived history into **units**:
a user message is one unit; an assistant message plus all tool results answering its calls is one
unit. A cut is only allowed between units. Each unit records the event seqs it came from.

### 2.4 Step 1: prune (no model call)

For each unit outside the retained tail, each tool result with more than 8192 characters is
replaced by `head 4096 + "\n…[middle pruned: N characters]…\n" + tail 1024`. Recorded as a new event
`tool_result_pruned {v, call_id, source_seq, output}`; `DeriveMessages` uses the latest
replacement for that `call_id`. Remeasure (char estimate of the change). Stop when below `T`.

### 2.5 Step 2: summarize

- Retained tail: walk units from the newest back while their char estimate fits `R`. Always keep at
  least the newest unit. The span is every unit before the tail (including a prior summary).
- If the span is empty: nothing to do (log, return).
- Request: `System` = head text, `Tools` = the same specs, `Messages` = the span's messages exactly
  as derived, then one user message with the instruction in §2.7. `MaxTokens` 8192. Same provider
  and model as the turn (prefix reuse). Ignore tool calls in the reply; keep text only.
- Reject an empty summary or one with a char estimate not below the span's.
- Append `compaction_summary {v, summary, from_seq, upto_seq, retained_from_seq, tokens_before, tokens_after, model, usage}`.
  `upto_seq` = last event seq of the span.

### 2.6 Derive with a summary

`DeriveMessages`: find the latest summary. Emit one user message:

```
This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context. Treat the captured context as established background and build on it without restating it. Continue the task directly from the messages that follow, without acknowledging this checkpoint.

<compacted-summary>
...summary...
</compacted-summary>
```

then every message-producing event with `seq > upto_seq`, skipping the summary event itself.
Old summaries without `retained_from_seq` keep today's behavior (drop everything up to the summary
event seq) for compatibility.

### 2.7 Compaction instruction (final user message)

Use the deepseek-harness instruction (see `compaction-basic/README.md`, "Compaction instruction"),
with "AI coding assistant" changed to "AI assistant", and the same eight sections and rules.

### 2.8 Overflow recovery

`providers/openai/classify.go`: class `context_length` when status 400 (or 413) and the code is
`context_length_exceeded`, or the message matches (case-insensitive) `maximum context length`,
`context length`, `context window`, `too many tokens`, or `prompt is too long`.
On `ErrContextTooLong` the engine runs with `Trigger: "overflow"`: skip the threshold, retained
tail = newest unit only, prune and summarize, then the listener retries the stream once.

### 2.9 Locking

The engine holds a per-session mutex. Manual `/compact` keeps returning `ErrTurnActive` while a
turn runs; the in-turn path passes `allowActive`.

## 3. Spill (`internal/spill`, `agent/toolrunner.go`)

- `spill.Store{Root string}`; `Save(sessionID, tool, callID, text) (path string, err error)`.
  Path: `<home>/spill/<sha256(session)[:16]>/<random8>-<tool>.txt`, dir 0700, file opened with
  `O_CREATE|O_EXCL|O_WRONLY`, 0600.
- Policy in `ToolRunner.executeOne` after the tool returns: if the tool is not `read_file` and the
  output is longer than `inline_chars` (16000), keep the first 2/3 and last 1/3 of
  `inline_chars - len(notice)`, add the notice, record `Spilled: path` on the tool output.
- Raise the per-tool hard caps so spill sees the full text: bash `limitedWriter` 30000 → 4 MiB,
  `webfetch`/`search` 30000 → 4 MiB, MCP `maxOutputLength` 30000 → 4 MiB. `read_file` keeps 30000.
- Guard: `read_file`, `grep`, `glob` accept a path inside `<home>/spill/<hash of current session>/`.

## 4. Provider robustness

### 4.1 Timeouts (`providers/openai/adapter.go`, `sse.go`)

`http.Client{Timeout: 0}` with a `Transport`: dial 30 s, TLS 15 s, `ResponseHeaderTimeout` =
provider `timeout` (default 300 s). The SSE reader wraps the body: each `Read` arms a timer of
`stream_idle_timeout` (new provider key, default 120 s); on expiry it closes the body and returns a
`network` class error `stream idle for 120s`.

### 4.2 Backoff (`agent/retry.go`, `core/provider.go`)

`ProviderError.RetryAfter time.Duration`, parsed from the `Retry-After` header (seconds or HTTP
date). Delays 2, 4, 8, 16, 32 s with ±20 % jitter, 5 retries per target; a `RetryAfter` replaces
the delay, capped at 120 s. Delays stay injectable for tests.

### 4.3 Mid-stream retry (`agent/turn.go`)

When `consumeStream` fails with a retryable class and `ctx` is live, the assistant message is not
yet stored, so the step can be repeated: write the attempt, publish a live frame
`{kind: "step_retry", step}`, wait the backoff, and call `LLMStream` again (max 2 extra attempts).
Web and Telegram drop the partial text of that step on `step_retry`.

## 5. Step budget

Follow deepseek-harness: no default turn budget. In `agent/turn.go`, `maxSteps == 0` means
unlimited for root and child sessions (remove the 25 / 15 defaults). `config/validate.go` accepts
`max_steps >= 0` with no upper bound (today: 1–200). The web agent form shows an empty field as
"no limit". A configured cap keeps working: on reaching it, run one extra step with no tools and the
user text `You reached the step limit for this turn. Stop working and report: what is done, what remains, and the next step.`,
then end with reason `max_steps`. Cancel (web, Telegram, CLI) stays the owner's stop button.
Round caps for scheduled objectives are left to the scheduler spec (deepseek-harness `goal`,
`defaultMaxGoalRounds: 256`).

## Risks

- One full cache miss on the first turn after deploy (prompt layout change) — expected.
- The 4-chars-per-token estimate under-counts CJK and JSON; the last real usage corrects it each step.
- A single unit larger than the window cannot be compacted; spill keeps tool units small, so this
  needs one huge user message.
