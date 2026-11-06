# Contract: Images and file attachments

- **Requirements**: [requirements.md](requirements.md), revision 1, Approved
- **Contract revision**: 1
- **Applies to**: HTTP `/v1`, session events, Go library, config, Telegram
- **Status**: Approved

Spec #9 conventions apply (owner session, CSRF, error envelope).

## Operation index

| ID | Type | Operation | Requirements |
|---|---|---|---|
| `API-ATT-001` | HTTP | `POST /v1/sessions/{id}/attachments` | `REQ-ATT-001`, `REQ-ATT-006` |
| `API-ATT-002` | HTTP | `POST /v1/sessions/{id}/messages` body field `attachments` | `REQ-ATT-002`, `REQ-ATT-006` |
| `API-ATT-003` | HTTP | `GET /v1/sessions/{id}/attachments/{path...}` | `REQ-ATT-006` |
| `API-ATT-004` | Event | image block and tool result images | `REQ-ATT-002`, `REQ-ATT-004` |
| `API-ATT-005` | Config | agent key `vision` | `REQ-ATT-003` |
| `API-ATT-006` | Go library | `lana.Request.Attachments` | `REQ-ATT-008` |

## API-ATT-001: Upload

`multipart/form-data`, one or more parts named `files`. Response `201`:
`{ "attachments": [ { "path": "attachments/2026-11-01/invoice.pdf", "name": "invoice.pdf", "media_type": "application/pdf", "size": 16080 } ] }`.
Errors: `413 file_too_large` (names the file, limit 20 MB), `400 too_many_files` (more than 10),
`404` unknown session. The request body is limited to 10 × 20 MB plus 1 MB.

## API-ATT-002: Send with attachments

Body `{ "text": "cek ini", "attachments": ["attachments/2026-11-01/a.jpg"] }`. `text` may be empty
when `attachments` is not. Each path must be a file under the session workspace `attachments/`
directory (`400 invalid_attachment` names the path otherwise). At most 10 paths.

## API-ATT-003: Serve an attachment

Returns the file under the session workspace `attachments/` with its media type and
`Cache-Control: private, max-age=3600`. `404` when the path is missing or outside `attachments/`.

## API-ATT-004: Events

Image content block, stored in `user/message` and in `tool/result.images`:
`{ "type": "image", "media_type": "image/jpeg", "path": "attachments/2026-11-01/a.jpg", "data": "<base64>" }`.
`tool/result` gains `images` (array of image blocks, omitted when empty). The web event API omits
`data` and keeps `path`.

## API-ATT-005: Config

`[agents.<name>] vision = true|false` (optional). Absent: the catalog flag; unknown model: false.

## API-ATT-006: Go library

`lana.Request{ Agent, Prompt, SessionID, Attachments []string }`, workspace-relative paths.
