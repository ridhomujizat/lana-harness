# Requirements: Model and effort as two choices, for every provider

- **Issue**: — (owner request 2026-11-26)
- **Status**: Approved (owner: "model dan effort dipisah … untuk semua provider juga harusnya"; details are agent recommendations)
- **Revision**: 2 (2026-11-26: owner "ga harus seragam … ikuti model effortnya aja … ketika ganti, effortnya default ke value sebelumnya")
- **Owner**: ridho
- **Related**: spec 21 (model settings, REQ-MDL-001 per-session effort), spec 39 (Claude Code effort), spec 74 (Antigravity)

## Goal

Every model picker shows models only; a separate effort control next to it shows the levels the picked
model offers, in that model's own terms. Antigravity stops listing one model per effort
(`gemini-4-neon-low/medium/high/xhigh`) and lists base models.

## Behavior rules

### REQ-MES-001: Each model's own levels (revision 2)

There is no shared effort vocabulary. A model's levels are the values its provider takes (OpenAI
`minimal`/`low`/`medium`/`high`, Anthropic `low`…`max`, Claude Code `low`…`max`, Antigravity the suffixes
`agy models` lists, …). `reasoning_effort` stores the chosen value as-is; `""` is the model's default.

### REQ-MES-002: Levels per model

Every model choice (`GET /v1/router` `choices[]`, `GET /v1/router/models`) carries `efforts: [...]` (the
levels the model supports, in order) and `default_effort` (or `""`), or no `efforts` when the model has no
effort control. Sources:

- API providers: the model catalog (`internal/models`) per model family; unknown models: `[]`.
- Claude Code: `low, medium, high, xhigh, max` (default `xhigh`), `off` not offered.
- Antigravity: the suffixes `agy models` reports for the base model (`gemini-4-neon` → `low, medium,
  high, xhigh`; `gpt-oss-120b` → `medium`), default = agy's (`high` when listed, else the first).

### REQ-MES-003: A value the model does not offer (revision 2)

The UI offers only the model's own levels. When a call meets a stored value its model does not list
(fallback chain, combo, account pool), the call uses the model's default (no cross-provider mapping); the
step event records the level actually sent (`effort`).

### REQ-MES-004: Provider mapping (revision 2)

Adapters send a listed value as their API spells it and never send an unlisted one.

### REQ-MES-005: Antigravity base models

The Antigravity choices list one entry per base model (`antigravity/gemini-4-neon`, label "Gemini 4
Neon"). A turn picks the variant `<base>-<level>` for the agent's effort (REQ-MES-003 on the variants
`agy models` lists), else the base model itself, else agy's current model. A full variant address
(`antigravity/gemini-4-neon-high`) stays valid and wins over the agent's effort.

### REQ-MES-006: UI

Agent form, new-agent form, session model switch, workflow and calendar model fields: model picker
(models only) and an effort control beside it (segmented on desktop, select on phone) with "Default" plus
the model's levels; hidden when the model has none. Changing the model keeps the previous level when the new
model lists it, else resets to "Default". A saved value the model does not list shows as "(not offered
by this model)" until changed.

- `AC-MES-001`: Given an agent on `antigravity/gemini-4-neon` with `reasoning_effort = "xhigh"`, the fake
  agy gets `session/set_model` `gemini-4-neon-xhigh`.
- `AC-MES-002`: Given `reasoning_effort = "xhigh"` on `antigravity/gpt-oss-120b` (only `medium`), the fake
  agy gets `gpt-oss-120b-medium` (its default).
- `AC-MES-003`: Given an OpenAI-kind model whose catalog lists `low, medium, high` and effort `max`, the
  request carries no `reasoning_effort` (model default).
- `AC-MES-004`: `GET /v1/router` Antigravity choices hold `antigravity/gemini-4-neon` once with `efforts`
  `["low","medium","high","xhigh"]`, and no `-high` variants.

## Approval

- **Approved by**: ridho (direction) + agent recommendations · **Approved on**: 2026-11-26
