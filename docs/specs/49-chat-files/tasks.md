# Tasks: Files in chat messages

- **Status**: In progress
- **Revision**: 1

The web UI is coded directly; Go goes through `dsh-coder`.

### T-001 Web UI

- Status: Done 2026-11-11.
- Files: new `lib/attachment-blocks.ts`, `views/message-files.tsx` (+ test),
  `styles/pages-gr8r/message-files.css`; `lib/format.ts`, `views/turn.tsx`, `views/session.tsx`,
  `views/attachments.tsx`, `views/trajectory-model.ts`, `styles/index.css`.
- Covers: REQ-CHF-001–007.

### T-002 Room list preview (Go)

- Files: `internal/attach` (`IsBlockText` + tests), `web/rooms.go` (`userMessageText`) + tests.
- Covers: REQ-CHF-008.

### T-003 Live check

- Scratch daemon (:18399) through Vite: a session with the five Omnix files and a long pasted log;
  screenshots of the thread, the Markdown and PDF previews, dark mode, and 390 px.
