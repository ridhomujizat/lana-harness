# Requirements: Agents send files and images

- **Issue**: —
- **Status**: Approved (owner approved the figcraft design 2026-11-11: "sip implement")
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (new session event kind)
- **Depends on**: spec 49 (file cards, preview), spec 9 attachments (API-ATT-003 route)

## Sources

- Owner, 2026-11-11: agents could not send a file or an image to the owner.
- Design: `design/lana-design-system.figcraft`, page "Chat files", rows 2–3 ("Chat – Agent sends
  files", "Agent files – states", "Chat – Agent sends images", "Chat – Image viewer", mobile
  frames); exports `design/export/chat-files-6..12-*.png`.

## Requirements

- **REQ-AGF-001** A built-in tool `send_file` lets an agent send one workspace file to the owner,
  with an optional caption. Input: `path` (workspace-relative or absolute inside the workspace),
  `caption` (optional, ≤ 1,000 characters). The path must resolve inside the agent's workspace
  (symlinks resolved), be a regular file, and be at most 20 MB. It runs without approval.
- **REQ-AGF-002** Every agent is offered `send_file` unless it lists it in `disabled_tools`, like
  `calendar`. It appears in the dashboard tool catalog.
- **REQ-AGF-003** The tool copies the file into `attachments/<date>/` with the attachment naming
  rules (unless it already lives under `attachments/`), so API-ATT-003 serves it, and records one
  `file/sent` session event (API-AGF-001) in the current turn. The tool output tells the model what
  was sent; the file bytes never go back to the model.
- **REQ-AGF-004** A session on Telegram delivers the file to the session's chat right away: images
  (JPEG, PNG, WebP up to 10 MB) with `sendPhoto`, everything else with `sendDocument`, the caption
  attached. A child session delivers to its parent's chat. A delivery failure is logged and the
  tool output says so; the event stays.
- **REQ-AGF-005** The web chat shows a turn's sent files under its reply text, left aligned: images
  as a grid (the first wide, the rest two per row), other files as cards with a type tile, name,
  meta, and a download button. A file card opens the spec 49 preview; an image opens a full-screen
  viewer with name, type, size, sender and time, Open original, Download, close, previous/next,
  a thumbnail strip, and keyboard ← → Esc. Phones get a full-screen viewer.
- **REQ-AGF-006** The tool line of the turn counts sends as their own category ("Sent 2 files").
- **REQ-AGF-007** A sent file that is no longer on disk keeps its card; preview and download say it
  was moved or deleted.

## Acceptance

- **AC-AGF-001** An agent asked to send a Markdown file and a screenshot shows a card and an image
  under its reply on the web; both open (preview, viewer) and download.
- **AC-AGF-002** The same on Telegram arrives as a document and a photo in the session's chat.
- **AC-AGF-003** A path outside the workspace, a directory, or a file over 20 MB is rejected with a
  clear tool output and no event.
