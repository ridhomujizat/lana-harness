# Tasks: Session titles

- **Status**: Completed 2026-11-08 · branch `wip/session-titles`
- **Requirements**: [requirements.md](requirements.md)

| ID | Slice | Covers | Evidence |
|---|---|---|---|
| T-TTL-01 | `core`: `SessionInfo.TitleSource`, `NewSession.Title/TitleSource`, `SessionTitleStore`, `SessionTitler` | REQ-TTL-005 | `go build ./...` |
| T-TTL-02 | `store`: migration `0009_session_title.sql`, schema 9, `SetSessionTitle`, title on create, fork copies the title; `memstore` the same | REQ-TTL-005, 006, 007 | `go test ./store/...` ok (v8→v9 migration, CHECK, onlyIfUntitled, fork) |
| T-TTL-03 | `agent/titles`: title pass, prompt, cleanup, fallback, background chain, one pass per session, regenerate | REQ-TTL-001..004, 006, 010 | `go test -race ./agent/titles` ok (fake router records source `title`, Claude-Code-only fallback, owner title kept during a running call, 409 busy) |
| T-TTL-04 | `agent`: turn end (native and Claude Code) asks the title service | REQ-TTL-001 | `go test ./agent/...` ok |
| T-TTL-05 | Runtime: `SetSessionTitle`, `RegenerateSessionTitle`, `TitlePending`, workflow and calendar fixed titles, library sessions skipped | REQ-TTL-001, 007..010 | `go test ./ -run Title` ok (SQLite + router: 3 title call records, owner title kept, clear → auto, regenerate; workflow and calendar titles) |
| T-TTL-06 | `web`: `title_source`/`title_pending`, PATCH `title`, regenerate endpoint, stream `title` event; `cmd/lana` backend | REQ-TTL-008..012 | `go test ./web ./cmd/lana` ok |
| T-TTL-07 | UI: History rename/regenerate, Naming… shimmer, pending poll, header click-to-edit, live update | REQ-TTL-011..013 | vitest 386/386 (8 new), `tsc --noEmit`, eslint clean |
| T-TTL-08 | Docs: this spec, index row, CHANGELOG Unreleased | — | — |

`web/dist` is rebuilt by a separate slice (`make web`).
