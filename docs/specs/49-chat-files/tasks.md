# Tasks: Files in chat messages

- **Status**: Completed
- **Revision**: 1

The web UI is coded directly; Go goes through `dsh-coder`.

### T-001 Web UI

- Status: Done 2026-11-11 (576d73f). Evidence: vitest 582/582 (11 new cases), tsc and eslint clean.
- Files: new `lib/attachment-blocks.ts`, `views/message-files.tsx` (+ test),
  `styles/pages-gr8r/message-files.css`; `lib/format.ts`, `views/turn.tsx`, `views/session.tsx`,
  `views/attachments.tsx`, `views/trajectory-model.ts`, `styles/index.css`.
- Covers: REQ-CHF-001–007.

### T-002 Room list preview (Go)

- Status: Done 2026-11-11 (2ac227b). Evidence: `go test -race ./...` and golangci-lint clean; live `/v1/rooms` preview "ringkas dua ini · 2 files".

- Files: `internal/attach` (`IsBlockText` + tests), `web/rooms.go` (`userMessageText`) + tests.
- Covers: REQ-CHF-008.

### T-003 Live check

- Status: Done 2026-11-11.

- Scratch daemon (:18399) through Vite: a session with the five Omnix files and a long pasted log;
  screenshots of the thread, the Markdown and PDF previews, dark mode, and 390 px.

## Live check 2026-11-11

- Scratch daemon (:18399) rebuilt from this branch, viewed through Vite. A session for
  omnix-implementator got the owner's five files plus "pelajari materi ini"; turns were cancelled
  right after the send.
- Sessions and Chats show five cards above the bubble; the file text is gone from both.
- The Markdown preview renders the served file (342 lines), next/previous walks all five; the PDF
  shows its pages and "Open PDF". Dark mode and 390 px (bottom sheet) checked.
- A pasted 32-line log folds with "Show more · 32 lines".
- Changed during the check: the PDF iframe was dropped because the dashboard sends
  `frame-ancestors 'none'`; the PDF preview shows its text instead.
