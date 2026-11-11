# Requirements: Files in chat messages

- **Issue**: —
- **Status**: Approved (owner approved the figcraft design 2026-11-11: "sip boleh implement")
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: Not required (no new route; the room preview text changes, its field does not)
- **Depends on**: spec 9 attachments (API-ATT-001..004, internal/attach block forms), spec 32 rooms

## Sources

- Owner, 2026-11-11: an owner message with files in the Omnix Implementator chat ("pelajari
  materi ini" plus five files) rendered as one very long bubble.
- Design: `design/lana-design-system.figcraft`, page "Chat files" (frames "Chat – Files in a
  message", "Chat – File preview", "Owner message with files – states", and two mobile frames);
  exports `design/export/chat-files-*.png`.

## Problem

`internal/attach` gives the model each non-image attachment as one text block that starts with
`Attachment <path> (<type>):` and holds the whole extracted text. The dashboard joined every text
block of an owner message into the bubble, so the bubble printed the files. The room list preview
did the same.

## Requirements

- **REQ-CHF-001** The dashboard recognises the five attachment block forms written by
  `internal/attach` (text, PDF with text, scanned PDF, unsupported type, could not be read) and
  keeps them out of the owner text everywhere it shows owner text (chat bubble, Trajectory title).
  Owner text that merely starts with "Attachment" stays text.
- **REQ-CHF-002** An owner message shows its files as cards above the text bubble, right aligned,
  up to two per row. A card shows a type tile (MD, PDF, CSV …), the file name (cut with an
  ellipsis), and a meta line: type and size for text, page count for a PDF, the reason in red for a
  file the agent could not read. Images stay thumbnails. A message with files and no text has no
  empty bubble.
- **REQ-CHF-003** More than six files fold into four cards and one "+ N more files" tile that
  expands in place.
- **REQ-CHF-004** A stored file's card opens a preview dialog: the file served by API-ATT-003,
  rendered as Markdown (with a Preview/Source toggle) or as plain text; a PDF shows the text the
  agent read page by page and an "Open PDF" link, because the dashboard forbids framing. The dialog
  has Copy path, Download, and previous/next through the files of that message (arrow keys too).
  When the file cannot be fetched, the dialog shows the text the agent read and says so.
- **REQ-CHF-005** On phones the preview is a bottom sheet and cards are one column.
- **REQ-CHF-006** Owner text over 12 lines or 1,200 characters folds with a fade and a
  "Show more · N lines" control.
- **REQ-CHF-007** The composer shows chosen files as the same cards (type tile, name, type and
  size) with a remove button.
- **REQ-CHF-009** Files dropped anywhere on a chat room become draft files, like the paperclip,
  with the same drop overlay as Sessions. A room with no web session yet says "Send a message
  first to attach files." A files-only send's stored placeholder text "(attachments)" is not shown
  in the bubble or the room preview.
- **REQ-CHF-008** The room list preview of an owner message leaves out attachment text and adds
  " · N files" (or "N files" alone when there is no text).

## Acceptance

- **AC-CHF-001** The Omnix message from the owner's report shows "pelajari materi ini" in the
  bubble and five cards above it; no file text appears in the thread or the chat list.
- **AC-CHF-002** Clicking a Markdown card renders the served file; previous/next walks the five
  files; the PDF shows its pages.
- **AC-CHF-003** Unit tests cover the block parser (every form, a name with "(1)", the cut notice,
  owner text), the bubble, folding, the preview and its fallback; Go tests cover the preview text.
