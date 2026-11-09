# Tasks: Generated agent avatars

- **Status**: Completed 2026-11-08 · branch `feature/agent-avatars`
- **Requirements**: [requirements.md](requirements.md)

| ID | Slice | Covers | Evidence |
|---|---|---|---|
| T-AVT-01 | `internal/avatar`: strict sanitizer (`Sanitize`), path and transform parsing, canonical output | REQ-AVT-004, AC-AVT-001 | `go test ./internal/avatar` ok (28 refusal cases, idempotence, arc flags) |
| T-AVT-02 | Local generator (`Generate`, `GenerateFamily`, 8 families: circle, ellipse, squircle, tilted rounded square, rounded triangle, cloud, teardrop, blob) with seeded eyes kept inside the body; default per name (`SeedForName`, `DefaultFor`); `Colorize`/`ColorHex` | REQ-AVT-005, 008, AC-AVT-002, 005 | property test over 500 seeds (valid, canonical, every family); distinct shapes; seed of "coder" pinned; gallery screenshots compared with the reference |
| T-AVT-03 | `avatar.Create`: router call with source `avatar`, agent background chain or requested model, Claude Code refused, one retry with `NextSeed`, local fallback; `lana.Runtime.GenerateAvatar`; `llmcall.SourceAvatar` | REQ-AVT-002, 003, 005, API-AVT-007 | `go test ./internal/avatar -run Create` (fake router: valid, invalid → local, retry, claude-code, combo without Claude Code, nil LLM) |
| T-AVT-04 | `web/avatars.go`: generate, PUT, DELETE, `avatar.svg`; `avatar_svg`/`avatar_source` on agents, rooms, overview; `AvatarBackend` in `cmd/lana`; `agenthome.Home.Avatar` | API-AVT-001..006, REQ-AVT-006, 007 | `go test ./web -run 'TestAvatar|TestAgentsHexColor'` |
| T-AVT-05 | Hex agent colors: `core.ValidAgentColor` used by `config` validation and the agent PUT | REQ-AVT-010, API-AVT-004 | `go test ./config ./web` ok |
| T-AVT-06 | UI: `lib/avatar-svg.tsx` (allowlist renderer, no innerHTML), `lib/avatar-store.ts`, `views/agent-avatar.tsx` (svg → store → avatar.svg image → letter), `views/avatar-dialog.tsx`, every agent call site, preview fixtures, router source label "Avatar" | REQ-AVT-001, 009, AC-AVT-006 | vitest 478/478 (52 files), `tsc --noEmit`, eslint, `make web-check`; call-site grep test |
| T-AVT-07 | Live check on a temporary daemon (127.0.0.1:18441, DeepSeek `ds/deepseek-flash`) | AC-AVT-003 | 3 API generations, all `source: model`, valid on the first try, call records `source=avatar`; Claude Code model → 400; PUT/avatar.svg/DELETE round trip; dashboard dialog drew cloud, triangle, tilted square (desktop and 390 px screenshots) |
| T-AVT-08 | Docs: this spec, index row, spec 32 API-ROOM-006 note, `CHANGELOG.md` Unreleased | — | — |
