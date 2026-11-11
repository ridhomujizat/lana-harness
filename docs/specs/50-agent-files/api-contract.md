# API contract: Agents send files and images

- **Status**: Approved
- **Revision**: 1

## API-AGF-001 Session event `file/sent`

Appended by the `send_file` tool in the current turn (`turn_id` set), served unchanged by the
session events routes and the SSE stream.

```json
{
  "kind": "file/sent",
  "turn_id": "06GW…",
  "payload": {
    "v": 1,
    "call_id": "toolu_…",
    "path": "attachments/2026-11-11/staging-dashboard.png",
    "name": "staging-dashboard.png",
    "media_type": "image/png",
    "size": 319488,
    "caption": "Dashboard staging, error 502 mulai 06:48."
  }
}
```

`path` is servable by `GET /v1/sessions/{id}/attachments/{path without "attachments/"}`
(API-ATT-003). `caption` is omitted when empty. `size` is bytes.

## API-AGF-002 Tool `send_file`

Input schema: `{"path": string (required), "caption": string (optional)}`.
Output (text): `Sent staging-dashboard.png (312 KB) to the owner.`, plus
` Telegram delivery failed: <reason>.` when that happened. Rejections: `outside workspace`,
`not a regular file`, `file is larger than 20 MB`, `file not found`.
