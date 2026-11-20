# Tasks: Pixel-art agent avatars

- **Status**: Completed 2026-11-21 · branch `feature/pixel-avatars` (worktree `../lana-harness-pixel`), merged into `development`
- **Requirements**: [requirements.md](requirements.md)

| ID | Slice | Covers | Evidence |
|---|---|---|---|
| T-PXA-01 | `internal/avatar`: Parts, Sprite, Document types; catalog; `Render`; `RandomParts`; `DefaultFor`; `ValidateSprite`; `Normalize`; static `SVG(sprite, view)`; blob generator and sanitizer removed | REQ-PXA-001..003, 013, AC-PXA-001, 002 | `go test ./internal/avatar` ok; the Go render matched the prototype script cell for cell on 17 cases; every catalog value, 500 seeds, refusal table, pinned default |
| T-PXA-02 | `avatar.Create` AI modes (parts, draw), prompts, first-JSON parsing, retry with `NextSeed`, local fallback, `ErrInvalidMode`; failed attempts logged at warn | REQ-PXA-005..007, AC-PXA-003 | `go test ./internal/avatar -run Create` (fake router: prose/fenced JSON, partial parts, junk twice, valid/invalid draw, Claude Code, unknown mode, call meta) |
| T-PXA-03 | `web/avatars.go` + rows + `cmd/lana` + `lana_avatar.go`: catalog, generate modes, PUT/DELETE with `avatar.json`, avatar.svg bust/full; startup rename `avatar.svg` → `avatar.blob.svg` | API-PXA-001..008, REQ-PXA-004, 009, 012, AC-PXA-004, 005 | `go test ./web ./internal/agenthome ./cmd/... .` ok; `go build ./...`, `go vet ./...`, golangci-lint 0 issues |
| T-PXA-04 | UI: `lib/pixel-avatar.ts` (checks, frames, outline, runs), `views/pixel-sprite.tsx`, store, AgentAvatar/AgentFace/Gr8r Avatar, CSS-steps working animation and bubble, dialog (Randomize, part pickers, color, AI Parts/Free draw, undo/redo, save/reset), preview fixtures dumped from Go, blob renderer and motion removed | REQ-PXA-008, 010, 011, 013, AC-PXA-006 | vitest 682/682, `tsc --noEmit`, eslint clean; preview screenshots (chat list, chat header, profile full body, dialog) |
| T-PXA-05 | Dist build, live check on a scratch daemon (127.0.0.1:18452, fresh home), docs | AC-PXA-007 | rename at start logged and file moved; catalog, rows, random, render (bad part 400), PUT parts → `avatar.json` 0600, avatar.svg full + ETag 304, invalid avatar 422. AI modes reached the provider and fell back to local on HTTP 401 (the shell's DeepSeek key is invalid); a real-model generation is still to be checked by the owner. |
