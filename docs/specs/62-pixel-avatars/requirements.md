# Requirements: Pixel-art agent avatars

- **Issue**: — (owner request 2026-11-20/21)
- **Status**: Approved (interview 2026-11-21)
- **Revision**: 1
- **Owner**: ridho
- **Contract**: in this file (API-PXA-001..008) — Required
- **Replaces**: [spec 43](../43-agent-avatars/requirements.md) blob avatars (REQ-AVT-001..008, API-AVT-001..006)
- **Related ADR**: —
- **Related diagram**: —

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | "saya ingin mengganti konsep avatar agent yg sekarang berbentuk seperti grokbot" + a GIF of chibi pixel characters | goal, style |
| Owner review | "terlihat seram" (character tiles, `design/prototypes/avatar-tiles.html`); "sip udh oke … implement … dengan style avatar-pixel.html ada fitur acak, lalu bisa generate new dengan ai" | approved style, randomize, AI |
| Owner review | dsh-coder version `design/prototypes/avatar-pixel-dsh.html`: "terlalu besar kepalanya" | proportions are fixed by the template |
| Interview 2026-11-21 | AI: both modes ("Parts" and "Free draw"); old saved blobs: switch all to pixel, keep a backup; dialog: Randomize + per-part pickers; merge to development only | REQ-PXA-004..008, 012 |
| Prototype | `design/prototypes/avatar-pixel.html` | grid, parts, colours, outline, frames, bust/full views |

## Goal

Every agent and team shows a friendly chibi pixel-art character instead of a blob. A character is
assembled from parts (species, hair, hair colour, skin, eyes, accessory, outfit, pants) seeded by
the agent name, wears the agent colour as its shirt, and nods and types while the agent works. The
owner can randomize, pick each part, ask a model to pick parts from a description, or ask a model
to draw a new character pixel by pixel on the same template, then save it.

## Definitions

- **Grid** — 16 columns × 20 rows, x to the right, y down. Cell size is one sprite pixel.
- **Template** — the proportions every avatar keeps: head x 3..12, y 3..11 (top and bottom rows
  x 4..11); a chin line y 12, x 5..10; shirt x 5..10, y 13..14; arms x 4 and 11 at y 13; hands x 4
  and 11 at y 14; pants x 5..10, y 15; legs x 5, 6, 9, 10 at y 16; shoes the same columns at y 17;
  ground bar x 2..13, y 19. Hair and accessories may use x 0..15, y 0..16.
- **Sprite** — `{palette, head, body}`: `palette` maps one-character keys (`[a-zA-Z0-9]`, at most
  24) to `#rrggbb`; `head` and `body` are layers of 20 strings of 16 characters, each `.` (empty) or
  a palette key. The body layer is drawn first, the head layer over it. Key `e` marks eye cells and
  key `s` the main skin (or metal) tone; both are required.
- **Outline** — a cell in −1..16 × −1..18 that is empty and 4-adjacent to a filled cell is drawn
  `#1b1820`. It is computed by the renderer, never stored.
- **Parts** — `{species, hair, hair_color, skin, eyes, accessory, outfit, pants, blush, color,
  accessory_color}`; the values are the catalog (API-PXA-001).
- **Avatar document** — `{"kind": "parts", "parts": Parts, "sprite": Sprite}` or
  `{"kind": "drawn", "sprite": Sprite, "prompt"?: string}`.
- **Frames** — computed by the client from a sprite: 0 rest; 1 head layer 1 row down and the left
  hand cell (4,14) 1 row up; 2 blink (each `e` cell with an `e` cell below takes the colour of its
  left neighbour); 3 head layer 1 row down and the right hand cell (11,14) 1 row up. The working
  cycle is `[0,1,0,3,0,1,2,3]`, 180 ms per step.
- **Bust** — the sprite cropped to rows −1..14 (a 16×16 square) in a rounded tile tinted with the
  agent colour. **Full** — the whole sprite with the ground bar.

## Requirements

| ID | Requirement |
|---|---|
| REQ-PXA-001 | `internal/avatar` renders Parts to a Sprite exactly like the prototype `avatar-pixel.html` (same template, masks, palettes, shading, outfit patterns, accessories, robot), with one change: the shirt colour is the agent colour resolved to hex (`ColorHex(color, "light")`). Rendering is deterministic. |
| REQ-PXA-002 | `RandomParts(seed)` picks every part with the prototype's weights from a seeded PRNG; `DefaultFor(name)` = `RandomParts(SeedForName(name))` rendered, with `color` = the agent colour. The same name gives the same avatar on every machine; a test pins one name. |
| REQ-PXA-003 | `ValidateSprite` accepts only the sprite shape in Definitions, plus the template rules: every filled body cell is in x 3..12, y 12..17; every filled head cell is in y 0..16; the head layer has at least 2 and at most 8 `e` cells, all in y 5..10, and at least 30 filled cells in the head box x 3..12, y 3..11; the body layer has at least 14 filled cells. It refuses anything else with a reason. |
| REQ-PXA-004 | **Randomize** (local, no model): a new seed gives new parts. **Render** (local): given parts (from the pickers) and the agent colour, the server returns the sprite. Unknown part values are refused with 400. |
| REQ-PXA-005 | **AI Parts**: the model gets the catalog and the owner's description (may be empty: "surprise me") and answers JSON parts only. Unknown or missing values are filled from `RandomParts(seed)`. |
| REQ-PXA-006 | **AI Free draw**: the model gets the template as a worked example (the rendered default sprite of the agent as rows), the rules of REQ-PXA-003, and the description, and answers JSON `{palette, head, body}`. The reply must pass `ValidateSprite`. |
| REQ-PXA-007 | AI calls go through the router with call-record source `avatar`, on the requested model or the agent's background chain, never Claude Code (400 `invalid_model`, as spec 43 REQ-AVT-003); reasoning effort off; 90 s per attempt; max tokens 1200 (parts) and 4000 (draw). A failed call or an invalid answer is retried once with a new seed; then the result is `RandomParts(seed)` rendered with `source: "local"`. |
| REQ-PXA-008 | The avatar dialog (agent profile header and General tab) shows the full sprite large (8×) with the working animation, the bust at 32 and 48 px, **Randomize**, per-part pickers (species, hair, hair colour, skin, eyes, accessory, outfit, pants, blush), the agent colour swatches (as spec 43), a **AI** box with a mode toggle **Parts / Free draw**, a description field, the model picker (spec 43 rules) and **Generate**, plus **Undo**, **Redo** (10 results), **Reset to default**, **Save**, **Cancel**. Pickers are disabled for a drawn avatar until Randomize or a picker change replaces it. Keyboard accessible; works at 390 px. |
| REQ-PXA-009 | Save validates again (parts are re-rendered, a drawn sprite re-validated) and writes `<agent home>/avatar.json` (mode 0600, atomic) and the `color` key as spec 43 did. Reset deletes `avatar.json`. A saved file that is not valid is ignored and the default shows. |
| REQ-PXA-010 | Every place that showed the spec 43 avatar shows the pixel bust; the agent profile header shows the full sprite. Teams show the default of the team name. The letter avatar stays only as the fallback of an unknown name. |
| REQ-PXA-011 | Only an avatar whose mood is a working mood (`thinking`, `tool`, `talk`) animates, with the frames above; a large avatar (≥ 64 px) also shows a pixel thought bubble. Everything else is static. `prefers-reduced-motion` shows frame 0. The blob motion (moods, gaze, MoodLayer marks) is removed. |
| REQ-PXA-012 | At daemon start, every agent home with an `avatar.svg` and no `avatar.blob.svg` has `avatar.svg` renamed to `avatar.blob.svg` (backup, never read again). |
| REQ-PXA-013 | Pixels stay crisp: SVG `shape-rendering="crispEdges"`; call sites prefer bust sizes that are multiples of 16 where the layout allows (16, 32, 48, 64). |

## Contract

| ID | Operation | Request | Response |
|---|---|---|---|
| API-PXA-001 | `GET /v1/avatars/catalog` | — | 200 `{"species":[…],"hair":[…],"hair_color":[…],"skin":[…],"eyes":[…],"accessory":[…],"outfit":[…],"pants":[…]}`, each item `{"id", "label"}` (English labels) |
| API-PXA-002 | `POST /v1/agents/{name}/avatar/generate` | `{"mode": "random"\|"render"\|"ai_parts"\|"ai_draw", "seed"?: 1..2^31-1, "parts"?: Parts (render), "color"?: token or "#RRGGBB", "prompt"?: string ≤ 500, "model"?: string}` | 200 `{"avatar": AvatarDocument, "seed", "model", "source": "local"\|"model"}`. `random` without seed picks one. 400 `invalid_request` (bad mode, seed, parts value, prompt), 400 `invalid_model`, 404 `not_found` (unknown agent or team). Nothing is stored. |
| API-PXA-003 | `PUT /v1/agents/{name}/avatar` | `{"avatar": AvatarDocument, "color"?: token or "#RRGGBB" or ""}` | 200 `{"name", "avatar", "avatar_source": "saved", "color", "restart_required"}`; 422 `invalid_avatar` (with the reason); 422 `validation_failed` (color); 404. For `kind: "parts"` the stored sprite is re-rendered with the saved colour. |
| API-PXA-004 | `DELETE /v1/agents/{name}/avatar` | — | 200 `{"name", "avatar": <default>, "avatar_source": "default", "color"}`; idempotent; 404. |
| API-PXA-005 | `GET /v1/agents`, `GET /v1/rooms`, `GET /v1/agents/{name}/overview` | — | rows carry `avatar` (AvatarDocument, never empty) and `avatar_source` (`saved`\|`default`); `avatar_svg` is removed; `color` stays. |
| API-PXA-006 | `GET /v1/agents/{name}/avatar.svg?view=bust\|full` | session cookie | `image/svg+xml` of frame 0 with the outline (bust default; the tile background is not included), `ETag`, `Cache-Control: private, max-age=60`, `Content-Security-Policy: default-src 'none'`, 304 on `If-None-Match`; 404 unknown name. `theme` is accepted and ignored. |
| API-PXA-007 | Router call records | — | source `avatar`, agent = the agent name, requested = the model or the background value. |
| API-PXA-008 | Files | — | `<agent home>/avatar.json` (AvatarDocument); `avatar.blob.svg` backup (REQ-PXA-012). |

## Acceptance criteria

| ID | Criterion | Evidence |
|---|---|---|
| AC-PXA-001 | Every catalog combination renders a sprite that passes `ValidateSprite`; 500 random seeds valid; distinct seeds give distinct sprites; default of one name pinned | `go test ./internal/avatar -run 'Render\|Random\|Default'` |
| AC-PXA-002 | `ValidateSprite` refuses wrong sizes, unknown keys, bad hex, >24 colours, body outside its box, no or too many eyes, too-small head, empty body | `go test ./internal/avatar -run Validate` |
| AC-PXA-003 | AI parts: valid JSON → parts filled; junk twice → local. AI draw: valid sprite → model; invalid twice → local; Claude Code → error; call record source `avatar` | `go test ./internal/avatar -run Create` |
| AC-PXA-004 | API: catalog; generate random/render/ai modes; PUT parts and drawn round trip with colour; invalid avatar 422 changes nothing; DELETE; tampered file → default; rows carry `avatar`; avatar.svg bust/full, ETag/304 | `go test ./web -run 'TestAvatar'` |
| AC-PXA-005 | Startup renames `avatar.svg` to `avatar.blob.svg` once | `go test ./internal/agenthome` or the daemon start test |
| AC-PXA-006 | UI: sprite renderer (outline, frames, bust/full), AgentAvatar from the store and the image fallback, working animation only on working moods, dialog randomize/pickers/AI modes/undo/redo/save/reset | `npm test` in `web/ui` |
| AC-PXA-007 | Live: dashboard at desktop and 390 px; one AI parts and one AI draw generation on a real model | screenshots, call records |
