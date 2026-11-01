# Requirements: Images and file attachments

- **Issue**: —
- **Status**: Implemented
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: 0009 (session as append-only event log)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-11-01 | "oiya harnesnya udh bisa baca gambar ga ya?", then "ya implement untuk bisa baca multiple gambar dan file" | goal |
| Owner answers 2026-11-01 | Formats: images, text PDF, text/CSV/JSON/code (Word/Excel/PowerPoint not in this revision). Storage: the session workspace. Scanned PDF: tell the owner | `Q-001`–`Q-003` |
| API probe 2026-11-01 | `deepseek-flash` accepts several `image_url` parts in one user message (named two colors correctly; read every field of an invoice image and filled a tool call with thinking on, ~700 input tokens for a 900 px image); accepts images inside a `role: tool` message content array; `deepseek-v4-pro` returns no error but answers "I can't see the images"; `/v1/files` accepts only webp, png, jpeg, gif (a PDF is rejected) | capability facts |
| Code review 2026-11-01 | `core.BlockImage` exists but `providers/openai/wire.go:68,92` return `ErrUnsupported`; Telegram answers media with "Only text messages are supported for now." (`channels/telegram/chat.go:99`); `read_file` rejects binary files (`tools/files.go:83`); every send path is text only (`agent/turn.go:29` `inbox []string`, `Send(ctx, id, text)` in web, Telegram, TUI) | problem |

## Goal

The owner can send several photos and files in one message from Telegram or the web, and the agent
reads them: it sees images (photos, screenshots, invoices) and reads the text of PDFs and text
files. The agent can also open images and PDFs that are already in its workspace.

## Definitions

- **Attachment** — a file the owner sent with a message, saved in the session workspace under
  `attachments/<YYYY-MM-DD>/<name>`.
- **Image** — JPEG, PNG, WebP, or GIF (first frame).
- **Text PDF** — a PDF from which at least one page yields text; **scanned PDF** — none does.
- **Text file** — valid UTF-8 content of type `text/*`, JSON, or a source code extension.
- **Vision model** — a model that can see images: the catalog `vision` flag, or the agent config
  key `vision`, which wins.

## Scope

### Included

- Telegram: photos, albums, and documents with captions.
- Web: attach button, paste, and drag-and-drop of several files; thumbnails in the session view.
- `read_file` on images and PDFs in the workspace.
- A text placeholder for images when the model is not a vision model.

### Not included

- Word, Excel, PowerPoint (later revision); voice, audio, video, stickers.
- Rendering scanned PDF pages to images.
- Attaching from the CLI TUI (the owner names a workspace path; the agent uses `read_file`).
- Sending files from the agent to the owner.

## Behavior rules

### REQ-ATT-001: Saving

An attachment is saved in the session workspace at `attachments/<YYYY-MM-DD>/<name>`, where
`<name>` is the original file name with path separators and control characters removed (Telegram
photos get `photo-<message id>-<n>.jpg`). A name that exists gets `-1`, `-2`, … before the
extension. The file mode is 0600. Limits: 20 MB per file, 10 attachments per message. A file over
a limit is not saved and the owner gets a reply naming the file and the limit.

### REQ-ATT-002: Owner message with attachments

The owner message stored for the turn holds, in order: the owner's text (or caption), then one
part per attachment, in the order sent:
- **Image** — an image block: decoded, scaled down so the long edge is at most 1 568 px (never up),
  re-encoded as JPEG quality 85 (PNG if it has transparency), with its workspace path. The image
  bytes are stored in the event, so later requests send the same bytes (prefix cache).
- **Text PDF** — a text block `Attachment <path> (PDF, <n> pages):` followed by the text with a
  `--- page <k> ---` line before each page.
- **Scanned PDF** — a text block `Attachment <path> (PDF, <n> pages) has no text layer. It is probably a scan: ask the owner to send photos or screenshots of the pages instead.`
- **Text file** — a text block `Attachment <path> (<media type>):` followed by the content.
- **Other types** — a text block `Attachment <path> (<media type>) was saved, but this file type cannot be read.`
Text longer than 16 000 characters is cut, and the block ends with
`(Showing the first 16000 characters. Use read_file on <path> with offset to read more.)`.
An owner message with attachments and no text gets the text `(attachments)`.

- `AC-ATT-001`: A message with 3 photos and the caption "bandingkan" stores the caption then 3
  image blocks in order; the model request carries 3 image parts.
- `AC-ATT-002`: A 2-page text PDF stores one text block with two `--- page` markers.
- `AC-ATT-003`: A PDF with no extractable text stores the scanned-PDF text.

### REQ-ATT-003: Vision placeholder

When the turn's model is not a vision model, every image block of the request (owner messages and
tool results) is sent as the text `[image <path>: the current model cannot see images]`. The stored
events are unchanged, so switching back to a vision model sends the images again. Catalog values:
`deepseek-flash` and its alias `deepseek-v4-flash` are vision models; `deepseek-v4-pro`,
`deepseek-chat`, `deepseek-reasoner` are not. An unknown model is not a vision model unless the
agent sets `vision = true`.

- `AC-ATT-004`: An agent on `deepseek-v4-pro` with an image in history sends the placeholder text
  and no image part.

### REQ-ATT-004: read_file on images and PDFs

`read_file` on an image returns `Image <path> (<width>x<height>).` plus the image, normalized as in
REQ-ATT-002, in the tool result. `read_file` on a PDF returns the text with page markers, numbered
and paged by `offset`/`limit` like other files; a scanned PDF returns the scanned-PDF text as an
error result. Other binary files keep the `binary file` error. The `read_file` description says it
reads images and PDFs.

- `AC-ATT-005`: `read_file` on a PNG in the workspace gives a tool result with one image; the next
  request carries it as an image part of the tool message.

### REQ-ATT-005: Telegram

- A photo message saves the largest photo size; a document message saves the document. The caption
  is the message text.
- Messages with the same `media_group_id` (an album) that arrive within 1.5 s of the previous one
  form one owner message with all their attachments and the first non-empty caption.
- Voice, audio, video, video notes, stickers, and animations get the reply
  `I can read photos, PDFs, and text files. Voice, audio, and video are not supported yet.`
- Files are downloaded with the Bot API `getFile`; a file over 20 MB gets the REQ-ATT-001 reply.
- Group rules stay: media in a group is handled only when it replies to the bot or its caption
  mentions the bot, like text.

- `AC-ATT-006`: An album of 4 photos with one caption becomes one turn with 4 image blocks.

### REQ-ATT-006: Web

- The composer has an attach button (several files), accepts pasted images, and accepts files
  dropped on the session view. Chosen files show as removable chips (thumbnail for images) before
  sending. Uploads use API-ATT-001; the send call carries their paths (API-ATT-002).
- The session view shows image attachments of owner messages as thumbnails (API-ATT-003) and other
  attachments as file chips with name and type.
- Event payloads served to the web replace image bytes with the image path (API-ATT-004).

- `AC-ATT-007`: Attaching 2 images and a PDF and sending "cek ini" starts one turn with 2 image
  blocks and 1 PDF text block; the sent message shows 2 thumbnails and 1 file chip.

### REQ-ATT-007: Other consumers

- The compaction summary request, the memory capture pass, and the TUI history show an image as the
  text `[image <path>]`, not as image bytes.
- When compaction prunes an old tool result that carries images, the images are replaced by the
  same text.

### REQ-ATT-008: Library

`lana.Request` gains `Attachments` (workspace-relative paths). Paths outside the session
workspace's `attachments/` directory or that do not exist are rejected with an error before the
turn starts.

## Open questions

None. Agent recommendations, accepted with the owner's "ya implement" on 2026-11-01:

| ID | Decision | Basis |
|---|---|---|
| `Q-004` | Limits 20 MB per file and 10 per message | Telegram Bot API download limit is 20 MB |
| `Q-005` | Images scaled to ≤ 1 568 px and re-encoded | bounds request size and token use; DeepSeek resizes large images anyway |
| `Q-006` | Image bytes stored in the event log | immutable prefix for the cache even if the workspace file changes |
| `Q-007` | Inline text cut at 16 000 characters | same bound as the tool output spill (`agent/toolrunner.go` `inlineRunes`) |
