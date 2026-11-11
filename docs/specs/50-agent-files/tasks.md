# Tasks: Agents send files and images

- **Status**: Completed (Telegram delivery not yet checked live)
- **Revision**: 1

Go goes through `dsh-coder`; the web UI is coded directly.

### T-001 Go: send_file tool, file/sent event, Telegram delivery

- Files: `core` (event kind, payload, FileSender port on ToolCtx), `tools/send_file.go` + tests,
  `tools/module.go`, `agent/tooloffers.go`, `agent/turn.go`, `lana.go`, `cmd/lana/daemon.go`,
  `channels/telegram` (multipart, sendPhoto, sendDocument, SendFile) + tests,
  `internal/agenthome/templates.go`.
- Status: Done 2026-11-11 (3cb3634). Evidence: `go test -race ./...` and golangci-lint clean; tests for the tool (copy, rejections, symlink escape, 20 MB), the service (API-AGF-001 JSON), Telegram multipart (sendPhoto/sendDocument, 429 retry, 2-minute upload timeout), FileRouter.
- Covers: REQ-AGF-001–004, API-AGF-001/002.

### T-002 Web UI

- Status: Done 2026-11-11.
- Files: `views/turn.tsx` (file/sent → `sentFiles`, "Sent N files" category), `views/message-files.tsx`
  (`AgentFiles`, `ImageViewer`, preview states), `views/room-group.tsx`, `views/room-timeline.tsx`,
  `views/session.tsx`, `styles/pages-gr8r/message-files.css`, tests.
- Covers: REQ-AGF-005–007.

### T-003 Live check

- Status: Done 2026-11-11 for the web; Telegram pending owner approval.
- Scratch daemon (:18399), agent dana on `claude-code/sonnet`: asked to send two PNGs and one
  Markdown file. Claude Code found `mcp__lana__send_file`, three `file/sent` events were written
  with the files copied to `attachments/2026-11-11/`, the reply showed the image grid and the card;
  the viewer (1440 × 1024, caption, thumbnails) and the Markdown preview (table) opened; 390 px
  checked. Fixed during the check: the image grid collapsed to 48 px (grid items with
  `aspect-ratio` do not stretch; the ratio moved to the image).

- Scratch daemon: an agent sends a Markdown file and screenshots; web cards, viewer, download;
  Telegram delivery checked only with the owner's approval (the scratch home has no Telegram).
