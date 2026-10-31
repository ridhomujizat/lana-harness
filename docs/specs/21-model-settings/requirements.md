# Requirements: Model settings, thinking, and the DeepSeek catalog

- **Issue**: —
- **Status**: Implemented
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: — (config keys, one block kind, one live frame; listed below)
- **Related ADR**: 0009 (session as append-only event log)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-31 | "untuk pengaturan model di evonic gmn? sepertinya model lana ini belum ok" | goal |
| Review 2026-10-31 | `~/.lana/config.toml` runs agents on `deepseek-chat`, a legacy name served without thinking. `providers/openai/wire.go` has no thinking, `reasoning_effort`, or `reasoning_content` support. Compaction assumes a 128 000-token window. | problem |
| DeepSeek docs (fetched 2026-10-31) | `api-docs.deepseek.com/guides/thinking_mode`: `{"thinking":{"type":"enabled"|"disabled"}}`, enabled by default; `reasoning_effort` `low`/`high`/`max` (default `high`); with `tools`, `reasoning_content` must be passed back in all later requests or the API returns 400; `temperature` has no effect in thinking mode. `quick_start/pricing`: `deepseek-flash` (V4.1-Flash) and `deepseek-v4-pro`, 1M context, max output 384K; `deepseek-v4-flash` still accepted. | wire format |
| evonic | `models/schema.py:927` (per-model `thinking`, `thinking_budget`, `temperature`, `max_tokens`, `context_window`); `backend/llm_client.py:1056` (round-trip `reasoning_content`, empty string on assistant messages without it); `:1006` (double the output budget when thinking) | reference design |
| deepseek-harness | `packages/llm/llm-deepseek/README.md` (`reasoningEffort` default `high`, `off` sends `thinking.type: disabled`), `src/models.ts` (default catalog: `deepseek-flash`, `deepseek-v4-pro`, 1 048 576 context) | reference design |

## Requirements

### Thinking

- **REQ-MDL-001** Agent key `reasoning_effort`: `off`, `low`, `high`, or `max`. Unset means the
  model's catalog default (`high` for DeepSeek thinking models; none for other models).
- **REQ-MDL-002** A provider whose thinking format is `deepseek` sends
  `"thinking":{"type":"disabled"}` for `off`, and `"thinking":{"type":"enabled"},"reasoning_effort":"<effort>"`
  otherwise. The format comes from provider key `thinking_format` (`deepseek` or `none`); unset
  means `deepseek` when the base URL host is `api.deepseek.com`, else `none`. A `none` provider
  sends neither field, whatever the agent setting.
- **REQ-MDL-003** Streamed `reasoning_content` deltas are collected into one `thinking` content
  block, stored first in the `assistant/message` content. Live consumers get `thinking_delta`
  frames; Telegram ignores them.
- **REQ-MDL-004** A `deepseek`-format request with thinking on sends each assistant message's
  thinking block back as `reasoning_content`, and `""` for assistant messages that have none
  (history from before thinking, or another model). With thinking off, `reasoning_content` is
  omitted. The derived history stays append-only, so the prefix cache still holds (REQ-LTC-004).
- **REQ-MDL-005** Compaction summaries and memory capture passes send the same thinking settings
  as the turn, so their requests share the turn's prefix.
- **REQ-MDL-006** Thinking blocks are not model-visible text for display: the answer text, the
  session preview, Telegram, and the TUI ignore them. The web turn view shows them in a collapsed
  "Thinking" section.

### Model catalog

- **REQ-MDL-010** lana ships a catalog of known models: `deepseek-flash` and `deepseek-v4-pro`
  (context 1 048 576, max output 384 000, thinking format `deepseek`, default effort `high`), and
  the accepted legacy names `deepseek-v4-flash` (same as `deepseek-flash`), `deepseek-chat` and
  `deepseek-reasoner` (marked legacy). Unknown models stay usable with the current defaults.
- **REQ-MDL-011** The compaction window `W` is agent `context_window`, else the catalog value,
  else 128 000. The output reservation `O` is the request `max_tokens`, else 32 000 for a thinking
  model (thinking uses the same output budget), else 8 192.
- **REQ-MDL-012** `lana init` and the web provider setup default DeepSeek agents to
  `deepseek-flash`. An existing config is never rewritten. The web agent page marks a legacy model
  with "Legacy model — switch to deepseek-flash" and a one-click switch.

### Web

- **REQ-MDL-020** The web agent page has a model picker listing the catalog models of the agent's
  provider (plus a free-text option) and a "Thinking" select (Off, Low, High, Max, with
  descriptions from the deepseek-harness effort list). Saving updates the live config like the
  other agent fields.

## Contract additions

- Agent config key `reasoning_effort` (`off`|`low`|`high`|`max`). Web agent API field of the same name.
- Provider config key `thinking_format` (`deepseek`|`none`).
- Content block kind `thinking` (`{"type":"thinking","text":"..."}`) in `assistant/message`.
- Live frame `thinking_delta` (`{text}`).
- Web API `GET /v1/models?provider=<name>` (alias `/api/models`) returns the catalog entries for that provider kind.

## Out of scope

- OpenAI `reasoning_effort` and other providers' thinking formats.
- Sticky fallback with primary probing (evonic); the existing `fallback` list is unchanged.
- Separate vision, summarizer, or classifier models.
- Rewriting the owner's `config.toml` from `deepseek-chat` to `deepseek-flash` (owner decides in the web page).
