# Design: Model settings, thinking, and the DeepSeek catalog

- **Status**: Approved
- **Requirements**: [requirements.md](requirements.md)

## 1. Core types

- `core/chat.go`: `BlockThinking BlockKind = "thinking"` (text in `ContentBlock.Text`);
  `ChatThinkingDelta ChatEventKind = "thinking_delta"`; `ChatRequest.ReasoningEffort string`
  (`""` = provider default, `off`, `low`, `high`, `max`).
- `core/config.go`: `AgentConfig.ReasoningEffort string toml:"reasoning_effort"`;
  `ProviderConfig.ThinkingFormat string toml:"thinking_format"`.
- `config/validate.go`: accept both keys, reject other values.

## 2. Catalog (REQ-MDL-010, -011)

- New `internal/models/catalog.go`:
  `type Model struct { ID, Name, Description string; ContextWindow, MaxOutput int; Thinking string /* "deepseek" or "" */; DefaultEffort string; Legacy bool; Replacement string }`,
  `func Lookup(model string) (Model, bool)`, `func ForProviderKind(baseURL string) []Model`
  (the DeepSeek list when the host is `api.deepseek.com`, else empty).
  Entries: `deepseek-flash`, `deepseek-v4-pro`, `deepseek-v4-flash` (alias, not legacy-flagged),
  `deepseek-chat` and `deepseek-reasoner` (Legacy, Replacement `deepseek-flash`, ContextWindow
  128 000, Thinking "" for chat and "deepseek" for reasoner).
- Effort list with descriptions (deepseek-harness wording): Off "Use for simple tasks that do
  not need reasoning.", Low "Prefer for routine or latency-sensitive tasks.", High "The default
  balance for most tasks.", Max "Reserve for the hardest quality-first tasks."
- `agent/compaction` `resolvePolicy`: `W` = agent `context_window` > catalog > 128 000; `O` =
  request `max_tokens` > 32 000 when the effective effort is not `off` and the model thinks >
  8 192.

## 3. Effort resolution

- `agent/turn.go` resolve step: `effort = agent.ReasoningEffort`, else catalog `DefaultEffort`,
  else `""`. Set `ChatRequest.ReasoningEffort` on every step request. Pass the same value into
  `core.CompactRequest` and the memory capture request (new field `ReasoningEffort` on each), and
  set it on their `ChatRequest` (REQ-MDL-005).

## 4. Adapter (REQ-MDL-002–004)

- `providers/openai/module.go`: resolve thinking format per provider: `thinking_format` if set,
  else `deepseek` when the base URL host is `api.deepseek.com`, else `none`. Store it on the adapter.
- `providers/openai/wire.go` `buildRequestBody(req, format)`:
  - `wireRequest` gains `Thinking map[string]string json:"thinking,omitempty"` and
    `ReasoningEffort string json:"reasoning_effort,omitempty"`.
  - format `deepseek`: effort `off` sends `thinking:{type:disabled}`; `low`/`high`/`max` send
    `thinking:{type:enabled}` + `reasoning_effort`; `""` sends neither (server default).
  - thinking on (effort not `off`, format `deepseek`, and effort non-empty or the request default
    thinks): every assistant message gets `"reasoning_content"` = joined thinking block text, or
    `""`. Thinking off or format `none`: thinking blocks are dropped from the wire.
  - Thinking blocks never become `content` text.
- `providers/openai/sse.go`: `delta.reasoning_content` yields `ChatThinkingDelta` events and is
  accumulated; at stream end a `ChatBlock` with `BlockThinking` is emitted before the text block.
- `agent/turn.go` stream loop: collect thinking deltas; store the thinking block first in the
  `assistant/message` content; publish `thinking_delta` live frames. `lastAssistantText` and every
  text extraction skip thinking blocks.

## 5. Display (REQ-MDL-006)

- Go: `core` helper or existing text helpers skip `BlockThinking` (Telegram final answer, TUI,
  web session preview). Check every `BlockText` collector: `channels/telegram`, `channels/cli/tui`,
  `web/sessions.go`, `cmd/lana/chat.go`, `cmd/lana/daemon.go` (result text).
- Web: `contentText` in `web/ui/src/lib/format.ts` already filters `type === "text"`; verify.
  `turn.tsx` renders a thinking block as a collapsed `<details>` "Thinking" above the answer.
  `session.tsx` handles `thinking_delta` by showing a muted "Thinking…" indicator (no text).
- Telegram ignores `thinking_delta`.

## 6. Web settings (REQ-MDL-012, -020)

- `web/agents.go`: field `reasoning_effort` read/write + validation; live config like `max_steps`.
- New `GET /api/models?provider=<name>`: catalog for that provider (id, name, description,
  context_window, legacy, replacement, thinking, default_effort, efforts with descriptions).
- `web/ui/src/views/agent-detail.tsx`: model picker (catalog + "Other…" free text), Thinking
  select shown when the selected model thinks, legacy banner with one-click switch to the
  replacement.
- `cmd/lana/main.go` presets and the web provider setup: DeepSeek default model `deepseek-flash`.
- Rebuild `web/dist`.

## 7. Verification

- Wire: effort `high` on DeepSeek sends `thinking.type=enabled` and `reasoning_effort=high`;
  `off` sends `disabled` and no `reasoning_content`; format `none` sends neither field.
- Wire: history with one assistant message carrying a thinking block and one without sends
  `reasoning_content` on both (text and `""`).
- SSE: a stream with `reasoning_content` deltas then `content` yields thinking deltas, a thinking
  block, then the text block; usage still parsed.
- Turn: stored `assistant/message` has the thinking block first; the next step request carries it
  back; `System`/earlier messages byte-identical across steps.
- Compaction: `W` from catalog for `deepseek-flash` is 1 048 576.
- Web: `reasoning_effort` round-trips; `/api/models?provider=deepseek` lists the catalog;
  vitest for the thinking `<details>` and the legacy banner.
