# Spec 71 API contract: Workbench chat

All routes are authenticated (cookie + CSRF on writes) like the other `/v1/workbench` routes. Errors use the daemon envelope `{"error": {"code", "message"}}`.

| Route | Purpose |
|---|---|
| `GET /v1/workbench/sessions/{id}/chat` | Snapshot (or a page back with `?before=<item id>`). |
| `GET /v1/workbench/sessions/{id}/chat/stream` | SSE stream of chat updates. |
| `POST /v1/workbench/sessions/{id}/chat/say` | Send a message. |
| `POST /v1/workbench/sessions/{id}/chat/keys` | Send keys. |
| `POST /v1/workbench/sessions/{id}/chat/mode` | Set the permission mode. |
| `POST /v1/workbench/sessions/{id}/chat/model` | Set model and/or effort. |
| `GET /v1/workbench/sessions/{id}/chat/commands` | Slash command list. |
| `GET /v1/workbench/sessions/{id}/chat/image?item=&i=` | An image from the transcript. |
| `GET /v1/workbench/sessions/{id}/chat/agents/{agent_id}` | A subagent's items, meta and step count. |

Common errors: 404 `not_found` (session), 503 `workbench_unavailable`, 403 `control_locked` on POST routes (REQ-RA-031), 409 `not_claude` on POST routes when the session runs no Claude Code.

## Chat body

`GET …/chat` returns, and every SSE `event: chat` carries, a `ChatUpdate`:

```json
{
  "available": true,
  "reason": "",
  "agent": "claude",
  "conversation": { "id": "3f2a9c1e-…", "path": "~/.claude/projects/-Users-…/3f2a9c1e-….jsonl", "started": "2026-11-23T12:58:01Z" },
  "reset": true,
  "truncated_before": false,
  "items": [ /* Item, in transcript order; with reset=false these are upserts by id */ ],
  "meta": {
    "model": "claude-opus-5-5",
    "effort": "high",
    "mode": "acceptEdits",
    "context": { "used": 84200, "window": 200000, "cache_read": 71000, "input": 13200, "output": 2300 },
    "unknown_lines": 0,
    "version": "2.1.330",
    "tasks": [
      { "id": "bogk0ac0c", "kind": "shell", "tool_use_id": "toolu_…", "title": "Run dsh-implement for T-002", "status": "completed", "summary": "Background command … completed (exit code 0)", "started": "…", "ended": "…" },
      { "id": "a56a9a94b0e1dbb18", "kind": "agent", "tool_use_id": "toolu_…", "title": "T-002 transcript reader", "agent_type": "dsh-coder", "status": "running", "summary": "", "started": "…" }
    ]
  },
  "live": {
    "state": "working",
    "status": "✻ Running tests… (24s · ↓ 1.2k tokens · esc to interrupt)",
    "mode": "acceptEdits",
    "input_empty": true,
    "dialog": null
  }
}
```

- `reason` (when `available` is false): `no_agent`, `unsupported_agent`, `waiting`.
- `meta.mode` is from the transcript; `live.mode` is from the screen and wins in the UI when present.
- `live` is `null` when no mirror runs (the plain GET without a stream).
- An SSE event omits `meta` / `live` when they did not change, and `items` when nothing changed.

### Item

Every item has `id`, `kind`, and `at` (RFC 3339, may be empty).

```json
{ "id": "u1", "kind": "user", "text": "add Files and Changes panels", "images": [{ "ref": "u1:1", "media_type": "image/png" }] }
{ "id": "c1", "kind": "command", "name": "/compact", "args": "" }
{ "id": "o1", "kind": "output", "text": "Compacted." }
{ "id": "a1:0", "kind": "assistant", "text": "I'll check …", "model": "claude-opus-5-5" }
{ "id": "a1:1", "kind": "thinking", "text": "…" }
{ "id": "a2:0", "kind": "tool", "tool_use_id": "toolu_…", "name": "Edit",
  "input": { "file_path": "/…/workbench.tsx", "old_string": "…", "new_string": "…" },
  "summary": { "verb": "Edit", "target": "web/ui/src/views/workbench.tsx", "meta": "+41 −22" },
  "diff": { "old": "…", "new": "…" },
  "status": "done",
  "result": { "text": "The file … has been updated.", "lines": 1, "truncated": false, "is_error": false } }
{ "id": "a3:0", "kind": "question", "tool_use_id": "toolu_…",
  "questions": [{ "question": "Default scope?", "header": "Scope", "multi_select": false,
                  "options": [{ "label": "Uncommitted", "description": "…" }] }],
  "answered": false, "answer": "" }
{ "id": "a4:0", "kind": "plan", "tool_use_id": "toolu_…", "plan": "# Chat view\n1. …", "state": "pending" }
{ "id": "d1", "kind": "divider", "text": "Conversation compacted" }
{ "id": "t1", "kind": "turn_end", "duration_ms": 134000, "output_tokens": 38200 }
```

`summary.target` is relative to the conversation cwd when inside it. `summary.meta` for Write is `+<lines>`, for Edit `+<added> −<removed>` counted from the diff, for Bash empty.

### Dialog

```json
{
  "kind": "permission",
  "title": "Do you want to proceed?",
  "lines": ["Bash command", "rm -rf web/dist && make web", "Delete the old dist and rebuild"],
  "options": [
    { "key": "1", "label": "Yes", "selected": true },
    { "key": "2", "label": "Yes, and don't ask again for make commands in /Users/…", "selected": false },
    { "key": "3", "label": "No, and tell Claude what to do differently (esc)", "selected": false }
  ]
}
```

`kind`: `permission`, `question`, `plan`, `other`.

## Writes

`POST …/chat/say` `{"text": "…", "paths": ["/Users/…/drop/a.png"], "force": false}` → 204. 409 `dialog_open`, 400 `invalid_request` (empty or over 64 KB).

`POST …/chat/keys` `{"keys": ["1"]}` → 204. 400 `invalid_key`.

`POST …/chat/mode` `{"mode": "plan"}` → 200 `{"mode": "plan"}`. 409 `mode_unavailable` `{"seen": ["default", "acceptEdits"]}` in the error message.

`POST …/chat/model` `{"model": "sonnet", "effort": "high"}` → 204. 409 `busy`, 400 `invalid_request`.

`GET …/chat/commands` → `{"commands": [{"name": "/compact", "description": "Free up context by summarizing the conversation so far", "source": "built-in"}]}`.

`GET …/chat/image?item=u1&i=1` → image bytes.

`GET …/chat/agents/{agent_id}` → `{"agent_id": "a56a…", "agent_type": "dsh-coder", "description": "T-002 transcript reader", "model": "claude-haiku-5-5", "finished": true, "steps": 12, "items": [Item…]}`. 404 `not_found` when the file does not exist.

Agent tool items also carry `"agent": {"agent_id": "a56a…", "agent_type": "dsh-coder", "background": true}`.
