# API contract: Session trajectory view

- **Status**: Not required
- **Revision**: 1

The view reads only `GET /v1/sessions/{id}/events` and `GET /v1/sessions/{id}/stream`, which exist.
No CLI, config, HTTP, JSON-RPC, or event change.

The only new externally visible form is the web route query `#/sessions/{id}?view=trajectory`
(REQ-TRJ-001). `view` has one recognized value, `trajectory`; any other value or no value opens Chat.
