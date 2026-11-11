# Tasks: Agents send files and images

- **Status**: In progress
- **Revision**: 1

Go goes through `dsh-coder`; the web UI is coded directly.

### T-001 Go: send_file tool, file/sent event, Telegram delivery

- Files: `core` (event kind, payload, FileSender port on ToolCtx), `tools/send_file.go` + tests,
  `tools/module.go`, `agent/tooloffers.go`, `agent/turn.go`, `lana.go`, `cmd/lana/daemon.go`,
  `channels/telegram` (multipart, sendPhoto, sendDocument, SendFile) + tests,
  `internal/agenthome/templates.go`.
- Covers: REQ-AGF-001–004, API-AGF-001/002.

### T-002 Web UI

- Status: Done 2026-11-11.
- Files: `views/turn.tsx` (file/sent → `sentFiles`, "Sent N files" category), `views/message-files.tsx`
  (`AgentFiles`, `ImageViewer`, preview states), `views/room-group.tsx`, `views/room-timeline.tsx`,
  `views/session.tsx`, `styles/pages-gr8r/message-files.css`, tests.
- Covers: REQ-AGF-005–007.

### T-003 Live check

- Scratch daemon: an agent sends a Markdown file and screenshots; web cards, viewer, download;
  Telegram delivery checked only with the owner's approval (the scratch home has no Telegram).
