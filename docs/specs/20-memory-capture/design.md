# Design: Memory capture and tool-call safety nets

- **Status**: Approved
- **Requirements**: [requirements.md](requirements.md)

## 1. Prompt section (REQ-MEM-001)

- `internal/agenthome/prompt.go`: new `PromptInput.MemoryCapture bool` and a constant
  `memoryCaptureRules`. `BuildSections` adds `Section{Name: SectionMemoryCapture ("memory_capture"), Text: memoryCaptureRules}`
  right after the memory section (before skills) when `MemoryCapture` is true. The text is
  constant, so REQ-LTC-001/002 hold: the section only appears or disappears when the tool set
  changes, and that change goes out as a `<system-update>`.
- `agent/turn.go` (`buildPrompt` input): set `MemoryCapture` when the turn's effective tool set
  contains `memory_write`.
- Suggested text:

```text
# Memory capture
After every message from the owner, check it for a new durable fact: how they want to be addressed, their name or role, language or style preferences, standing instructions ("always", "never", "from now on"), decisions, or corrections of something you got wrong. This includes facts that are only implied or said in passing.
- Save it at once with memory_write before you answer: target=user for facts about the owner, target=memory for your own notes about the environment.
- If it contradicts an entry already shown above, use action=replace with old_text from that entry. Do not add a second entry for the same thing.
- Keep each entry one short sentence. Never save passwords, API keys, or tokens.
```

## 2. Capture service (REQ-MEM-010–015)

New package `agent/memcapture`, wired like `agent/compaction`: a module (`memcapture`) that
requires `ServiceSessions`, `ServiceLLM`, `ServiceAgents`, builds a `Service`, and installs it on
the registry through a small sink interface (`SetMemoryCapturer(core.MemoryCapturer)`,
`AgentConfig(name)`), so it does not import `agent` (internal/archtest).

- `core/memcapture.go`: `type MemoryCapturer interface { Capture(CaptureRequest) }` and
  `CaptureRequest{SessionID, TurnID, AgentName, Home, Provider, Model string; System string; Tools []ToolSpec}`.
  `Capture` returns at once; work runs in a goroutine.
- `agent/turn.go` at `finished`: when `reason` is `end_turn` or `max_steps`, the session has no
  `ParentSessionID`, at least one owner message was stored in this turn (not a runtime note), the
  tool set has `memory_write`, and `AgentConfig.MemoryCapture` is not explicitly `false`, call
  `rn.reg.memoryCapturer().Capture(...)` with the last `prompt` and `specs` used by the turn.
- `Service.Capture`:
  1. Per-session in-flight flag: if a pass is running, return (REQ-MEM-015).
  2. Context: service base context (cancelled by `Module.Stop`) with a 90 s timeout.
  3. Read events, `core.DeriveMessages`, then append one user message with the instruction
     (§2.1). Request `{Model, System, Messages, Tools, MaxTokens: 1024}` through the provider from
     the LLM registry (same path as compaction `run`).
  4. Collect text. A reply with any tool-use block, or text that is not a JSON array after
     stripping a ```` ```json ```` fence, drops the pass (debug log).
  5. Validate each op (max 5): target in {user, memory}, action in {add, replace, remove},
     content ≤ 300 runes for add/replace, no `§`, not secret-like (regex: `sk-[A-Za-z0-9]{16,}`,
     `ghp_[A-Za-z0-9]{20,}`, `xox[bp]-`, `AKIA[0-9A-Z]{16}`, `-----BEGIN`, `(?i)(password|passwd|api[_-]?key|token)\s*[:=]\s*\S{6,}`).
  6. Apply each valid op with `agenthome.Update(path, func(e) { return agenthome.Apply(e, action, content, oldText, limit) })`
     on `agenthome.SharedUser(home)` / `agenthome.Paths(home, agent).Memory`, with the same limits
     as `tools/memory.go`. An op error is logged and skipped; others still apply.
  7. If any op applied, append `memory/captured` (`core.MemoryCaptured{V, TurnID, Ops, Usage}`)
     with `TurnID` of the captured turn. Log `memory captured` with agent, session, count, usage,
     `cached_input_tokens`.
- `store/store.go` `validKinds`: add `memory/captured`. `core.DeriveMessages` already ignores
  unknown non-message kinds; add a test that proves it.

### 2.1 Capture instruction

```text
<memory-capture>
This is an automatic memory check, not a message from the owner. Do not call tools.
Look only at what the owner said in the latest turn. Find durable facts worth keeping for future conversations: identity, how to address the owner, language or style preferences, standing instructions, decisions, lasting facts about their environment. Skip task status, one-off requests, and anything temporary. Never include passwords, API keys, or tokens.

Current owner file (target=user):
<entries, one per line, or "(empty)">

Current agent memory (target=memory):
<entries, or "(empty)">

Rules:
- A fact already present: skip it.
- A fact that contradicts or updates an entry: {"target":..., "action":"replace", "old_text":"<exact text from that entry>", "content":"<new entry>"}.
- A new fact: {"target":..., "action":"add", "content":"<one short sentence>"}.
- An entry the owner says is wrong and has no replacement: {"target":..., "action":"remove", "old_text":"..."}.
Reply with only a JSON array, at most 5 items. Reply [] when there is nothing to save.
</memory-capture>
```

Entries are written in the language the owner used (the existing files mix Indonesian and
English; keep the file's language).

## 3. Config (contract)

- `core.AgentConfig.MemoryCapture *bool` `toml:"memory_capture"` (nil = true).
- `config/validate.go`: accept the key. `web/agents.go`: read/write the field like the other
  agent booleans. `web/ui/src/views/agent-detail.tsx`: one toggle "Remember things automatically"
  with help text "After each turn, lana checks the conversation for preferences and corrections and
  saves them to memory." Shown only when the agent has `memory_write`.

## 4. Malformed tool-call nudge (REQ-MEM-020)

- `agent/toolcalltext.go`: `func looksLikeToolCallText(text string) bool`. Matches:
  `<｜tool▁calls▁begin｜>`, `<｜tool▁call▁begin｜>`, `<｜DSML｜`, `<tool_call>`, `<function_call>`,
  a fenced block whose info string is `tool`, `tool_call`, or `tool_code`, or a trimmed text that
  is a single JSON object (or array of objects) with keys `name` and (`arguments` or `parameters`).
  Plain prose mentioning tool names does not match.
- `agent/turn.go`: in the `default:` branch where the empty-answer guard sits, before it: if the
  last assistant message had no tool call, `looksLikeToolCallText(lastAssistantText)` and
  `toolTextNudges < 2`, increment and claim `toolCallTextNote`:
  `"Your last reply wrote a tool call as plain text, so nothing ran. Call the tool again through the tool-calling interface, or answer without tools."`

## 5. Runtime notes (REQ-MEM-021)

- `core.UserMessage.Runtime bool` `json:"runtime,omitempty"`.
- `agent/turn.go`: set `Runtime: true` when the claimed text is `emptyAnswerNote`,
  `stepLimitNote`, or `toolCallTextNote` (track with a `runtimeNote bool` next to `claimed`).
  A runtime note does not count as an owner message for REQ-MEM-010.
- `core.UserMessage.DisplayContent()`: unchanged. Display surfaces skip the whole message when
  `Runtime` is true: `channels/cli/tui/model.go restoreLines`, `web/ui/src/lib/format.ts`
  (`user/message` in `buildHistory`), `web/ui/src/views/turn.tsx` (`user/message` in `buildTurns`).

## 6. Display (REQ-MEM-030)

- `web/ui/src/views/turn.tsx`: `memory/captured` adds a muted note to its turn: one line per op,
  `Saved to memory: <content>` (add/replace) or `Removed from memory: <old_text>` (remove).
- `web/ui/src/lib/format.ts` history view: same text as a `decision` item.
- Rebuild `web/dist`.

## 7. Verification

- Unit: section present only with `memory_write`; byte-identical across two turns.
- Unit: capture with a fake provider returning
  `[{"target":"user","action":"replace","old_text":"Dipanggil \"Pak Ridho\"","content":"Dipanggil \"boss\""}]`
  rewrites the user file and appends one `memory/captured`; `[]` writes nothing; a tool-use reply,
  bad JSON, secret-like content, 6 ops (only the first 5 considered), and a provider error change
  nothing; second `Capture` while one is in flight is ignored; child session skipped;
  `memory_capture=false` skipped; request `System`/`Tools` equal the turn's and messages start
  with the derived history.
- Unit: `looksLikeToolCallText` table (positive and negative cases); a turn whose assistant writes
  `<｜tool▁calls▁begin｜>...` gets one nudge step with a `runtime: true` user message, max 2.
- UI: vitest for runtime-message hiding and the `memory/captured` line.
