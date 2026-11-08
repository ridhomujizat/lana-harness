# Requirements: Generated agent avatars

- **Issue**: — (owner request 2026-11-08)
- **Status**: Approved (owner's standing "ikuti rekomendasimu"; follow-up 2026-11-08: no letter avatars)
- **Revision**: 1
- **Owner**: ridho
- **Contract**: in this file (API-AVT-001..007) — Required
- **Related ADR**: —
- **Related diagram**: —

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | "profile agent cuma huruf … button generate avatar seperti gambar ini … dibuat oleh provider terpilih, bisa di-refresh, random avatar dengan 2 lubang sebagai mata, warna di-set oleh user, user bisa regenerate atau undo sementara" | goal, generation, color, undo |
| Owner follow-up | "usahakan nanti semua avatarnya pakai avatar yang telah di-generate, bukan avatar huruf lagi, seperti di workflow dll." | default avatar, every call site |
| Reference image | solid-color blobs (circle, squircle, tilted rounded square, rounded triangle, cloud, ellipse, teardrop) with two white pill/oval eye holes | style guide, local generator |
| Spec 32 | [agent rooms](../32-agent-rooms/api-contract.md) API-ROOM-006/007 | `color` key, agent rows |
| Spec 37/39 | router call records, background chain (REQ-CCT-007) | model choice, call source |

## Goal

Every agent has a face instead of a letter: a solid body in the agent's color with two white eye
holes. The owner can generate new ones with a chosen model, flip through the results, undo and
redo, pick a color, and save one. Until an avatar is saved, the agent shows a default avatar drawn
locally from its name, so no agent ever shows a letter.

## Definitions

- **Avatar** — an SVG with `viewBox="0 0 100 100"`, one or more body shapes filled `currentColor`,
  and exactly two eye shapes filled `#ffffff`. The color comes from the agent's `color` key.
- **Saved avatar** — `<LANA_HOME>/agents/<name>/avatar.svg`, written by the owner.
- **Default avatar** — the local generator's avatar for the seed `fnv64a(name) % (2^31-1) + 1`.
- **Canonical form** — the output of the sanitizer: allowed elements and attributes only, explicit
  fills, numbers rounded to two decimals.

## Requirements

| ID | Requirement |
|---|---|
| REQ-AVT-001 | The agent profile header and the agent General tab have **Generate avatar**, which opens a dialog: a 96–128 px preview, a strip of this session's results, color swatches (6 tokens + 9 vivid presets + custom hex), a model picker (router choices without Claude Code; default "Background model"), and **Generate**, **Undo**, **Redo** (up to 10 results, component state only), **Reset to default**, **Save**, **Cancel**. A color change recolors the preview without a request. Keyboard accessible; works at 390 px. |
| REQ-AVT-002 | Generation goes through the router with call-record source `avatar`, on the requested model or the agent's background chain (`llmcall.AgentBackgroundChain`), never a Claude Code target. The prompt is a fixed style guide plus a body family the server picks at random from `avatar.Families` and the seed; max tokens 1500, reasoning effort off, 60 s per attempt. |
| REQ-AVT-003 | A requested Claude Code address, or a combo with only Claude Code targets, is refused with 400 `invalid_model`; so is an unknown model. |
| REQ-AVT-004 | The sanitizer (`internal/avatar.Sanitize`) parses the XML strictly and allows only `svg`, `g`, `path`, `ellipse`, `circle`, `rect` with `xmlns`, `viewBox` (must be `0 0 100 100`), `d`, `cx`, `cy`, `rx`, `ry`, `r`, `x`, `y`, `width`, `height`, `fill` (`currentColor`, `#fff`, `#ffffff`, `white`), `transform` (`rotate`, `translate`, `scale` with numbers). Other elements and attributes are stripped, and shapes in other fills are dropped. It refuses scripts and other active elements, `on*`, `href`, `src`, `style`, `class` attributes, `url(`/`javascript:`/`data:` values, DOCTYPE, entities, processing instructions, path data over 4 KB, an SVG over 8 KB, coordinates outside −20..120 after transforms, no `currentColor` body, not exactly two white eyes, an eye whose center is outside the body box or whose box is over 25% of the body box. Output is canonical and idempotent. |
| REQ-AVT-005 | A failed model call or an invalid SVG is retried once with a new seed; then the local generator (`avatar.Generate(seed)`) draws the avatar and the result says `source: "local"`. Without any non-Claude model the local generator answers at once. |
| REQ-AVT-006 | Save sanitizes again, writes `avatar.svg` (mode 0600, atomic) in the agent home, and writes the `color` key through the same table rewrite as `PUT /v1/agents/{name}`. Reset deletes the file. |
| REQ-AVT-007 | A saved file that is not a valid avatar is ignored and the default avatar shows. |
| REQ-AVT-008 | An agent without a saved avatar shows its default avatar. Teams show the default avatar of the team name. The default is stable across reloads and machines; changing `Generate` changes every default face, so its output is part of the contract (a test pins one seed). |
| REQ-AVT-009 | Every place the dashboard shows an agent shows its avatar (cards, list, profile, sidebar and rooms, chats and history, session header, room timeline speakers, presence, pickers, team rosters, child rows, workflows, calendar, schedules, knowledge, Router logs and usage, home dashboard, setup wizard). Owner, provider, and channel avatars stay as they are. The letter avatar remains only as the fallback for an unknown name. |
| REQ-AVT-010 | `[agents.<name>] color` accepts `#RRGGBB` as well as the tokens `brand`, `success`, `warning`, `error`, `blue`, `gray` (amends API-ROOM-006). |

## Contract

| ID | Operation | Request | Response |
|---|---|---|---|
| API-AVT-001 | `POST /v1/agents/{name}/avatar/generate` | `{"model"?: string, "seed"?: 1..2^31-1}` | 200 `{"svg", "model", "seed", "source": "model"\|"local"}`; `model` is the served address, `""` for local. 400 `invalid_model`, 400 `invalid_request` (bad seed or body), 404 `not_found` (unknown or team). Nothing is stored. |
| API-AVT-002 | `PUT /v1/agents/{name}/avatar` | `{"svg": string, "color"?: token or "#RRGGBB" or ""}` | 200 `{"name", "avatar_svg", "avatar_source": "saved", "color", "restart_required"}`; 422 `invalid_avatar`; 422 `validation_failed` (key `agents.<name>.color`); 404. Without `color` the key is kept; `""` removes it. |
| API-AVT-003 | `DELETE /v1/agents/{name}/avatar` | — | 200 `{"name", "avatar_svg": <default>, "avatar_source": "default", "color"}`; idempotent; 404. |
| API-AVT-004 | Config `[agents.<name>] color` | token or `#RRGGBB` | `lana config check` and the agent PUT report other values. |
| API-AVT-005 | `GET /v1/agents`, `GET /v1/rooms`, `GET /v1/agents/{name}/overview` | — | rows gain `avatar_svg` (never empty) and `avatar_source` (`saved` or `default`); `color` stays. |
| API-AVT-006 | `GET /v1/agents/{name}/avatar.svg?theme=light\|dark` | session cookie | `image/svg+xml` with the body color baked in (tokens resolve to the dashboard theme hex), `ETag`, `Cache-Control: private, max-age=60`, `Content-Security-Policy: default-src 'none'`, 304 on `If-None-Match`; 404 for an unknown name. |
| API-AVT-007 | Router call records | — | source `avatar`, agent = the agent name, requested = the model or the background value. |

## Acceptance criteria

| ID | Criterion | Evidence |
|---|---|---|
| AC-AVT-001 | Good SVG is accepted and canonical; script, href, style, onload, external refs, DOCTYPE/entities, 3 eyes, 1 eye, no body, out of box, oversized, long path are refused | `go test ./internal/avatar -run Sanitize` |
| AC-AVT-002 | The local generator gives a valid canonical avatar for 500 seeds, covers every family, and different seeds give different shapes | `go test ./internal/avatar -run Generate` |
| AC-AVT-003 | Generate: valid model SVG → `model`; invalid twice → `local`; Claude Code model → error; call record source `avatar` | `go test ./internal/avatar -run Create`, `./web -run TestAvatarGenerate` |
| AC-AVT-004 | PUT/DELETE round trip with hex and token colors, refusals change nothing, tampered file falls back | `go test ./web -run TestAvatarPutDelete` |
| AC-AVT-005 | Default avatar is stable per name and valid; avatar.svg bakes the color, ETag/304, 404 | `go test ./internal/avatar -run Default`, `./web -run TestAvatarImage` |
| AC-AVT-006 | UI: AgentAvatar renders svg and color; dialog generate/undo/redo/color/save/reset; call sites render the avatar | `npm test` in `web/ui` |
