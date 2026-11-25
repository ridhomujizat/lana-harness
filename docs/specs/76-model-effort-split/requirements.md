# Requirements: Model and effort as two choices, for every provider

- **Issue**: — (owner request 2026-11-26)
- **Status**: Approved (owner: "model dan effort dipisah … untuk semua provider juga harusnya"; details are agent recommendations)
- **Revision**: 1
- **Owner**: ridho
- **Related**: spec 21 (model settings, REQ-MDL-001 per-session effort), spec 39 (Claude Code effort), spec 74 (Antigravity)

## Goal

Every model picker shows models only; a separate effort control next to it shows the levels the picked
model supports. One effort vocabulary for every provider. Antigravity stops listing one model per effort
(`gemini-4-neon-low/medium/high/xhigh`) and lists base models.

## Behavior rules

### REQ-MES-001: One vocabulary

`reasoning_effort` (agent config, session switch, workflows, calendar) takes `off`, `low`, `medium`,
`high`, `xhigh`, `max`, or `""` (the model's default) for every provider. Old values stay valid (they are
a subset).

### REQ-MES-002: Levels per model

Every model choice (`GET /v1/router` `choices[]`, `GET /v1/router/models`) carries `efforts: [...]` (the
levels the model supports, in order) and `default_effort` (or `""`), or no `efforts` when the model has no
effort control. Sources:

- API providers: the model catalog (`internal/models`) per model family; unknown models: `[]`.
- Claude Code: `low, medium, high, xhigh, max` (default `xhigh`), `off` not offered.
- Antigravity: the suffixes `agy models` reports for the base model (`gemini-4-neon` → `low, medium,
  high, xhigh`; `gpt-oss-120b` → `medium`), default = agy's (`high` when listed, else the first).

### REQ-MES-003: Nearest level

When the requested level is not supported by the model, the call uses the nearest supported one: the
next lower level, else the next higher; `off` on a model that cannot turn thinking off uses its lowest
level. The session event of the step records the level actually sent (`effort` field), and the UI shows
"uses <level>" next to the control.

### REQ-MES-004: Provider mapping

Adapters map the vocabulary onto their API: OpenAI-style `reasoning_effort` (`off`→`minimal` or omitted,
`low`, `medium`, `high`; `xhigh`/`max` → `high` unless the model lists them), Anthropic adaptive effort
(`low`…`max`; `off` → lowest level, never `thinking: disabled` on models that reject it), DeepSeek and
others as today plus `medium`/`xhigh` mapped by REQ-MES-003. No adapter sends a value its API rejects.

### REQ-MES-005: Antigravity base models

The Antigravity choices list one entry per base model (`antigravity/gemini-4-neon`, label "Gemini 4
Neon"). A turn picks the variant `<base>-<level>` for the agent's effort (REQ-MES-003 on the variants
`agy models` lists), else the base model itself, else agy's current model. A full variant address
(`antigravity/gemini-4-neon-high`) stays valid and wins over the agent's effort.

### REQ-MES-006: UI

Agent form, new-agent form, session model switch, workflow and calendar model fields: model picker
(models only) and an effort control beside it (segmented on desktop, select on phone) with "Default" plus
the model's levels; hidden when the model has none. Changing the model keeps the chosen level when the new
model supports it, else shows the nearest with "uses <level>".

- `AC-MES-001`: Given an agent on `antigravity/gemini-4-neon` with `reasoning_effort = "xhigh"`, the fake
  agy gets `session/set_model` `gemini-4-neon-xhigh`.
- `AC-MES-002`: Given `reasoning_effort = "xhigh"` on `antigravity/gpt-oss-120b`, the fake agy gets
  `gpt-oss-120b-medium`.
- `AC-MES-003`: Given an OpenAI-kind model whose catalog lists `low, medium, high` and effort `max`, the
  request has `reasoning_effort: "high"`.
- `AC-MES-004`: `GET /v1/router` Antigravity choices hold `antigravity/gemini-4-neon` once with `efforts`
  `["low","medium","high","xhigh"]`, and no `-high` variants.

## Approval

- **Approved by**: ridho (direction) + agent recommendations · **Approved on**: 2026-11-26
