# Spec 70 API contract: root folders of several repos

Status: Draft r1, 2026-11-22. Requirements: [requirements.md](requirements.md).

Same authentication, error envelope and common errors as the spec 66 contract. Paths are absolute.

## Resolve on a root

`POST /v1/workbench/git/resolve` (spec 66) answers a root like this:

```json
{
  "dirs": {
    "/Users/me/coding/sxored": {
      "repo": null,
      "root": {"path": "/Users/me/coding/sxored", "name": "sxored", "repos": 27}
    }
  }
}
```

## Root summary

`GET /v1/workbench/git/root?dir=<root>`

Headers: `ETag`. `If-None-Match` with the same value returns 304.

```json
{
  "root": "/Users/me/coding/sxored",
  "name": "sxored",
  "repos": [
    {
      "name": "sxored-be-ocrai",
      "worktree": "/Users/me/coding/sxored/sxored-be-ocrai",
      "branch": "feature/ocr-v2",
      "short": "ocr-v2",
      "detached": false,
      "color": "#E0805B",
      "changed": 9,
      "additions": 248,
      "deletions": 12,
      "ahead": 2,
      "behind": 0
    },
    {"name": "sxored-be-billing", "worktree": "/Users/me/coding/sxored/sxored-be-billing", "error": "git_timeout"}
  ],
  "other": [
    {"name": "docs", "kind": "dir"},
    {"name": "CLAUDE.md", "kind": "file"},
    {"name": "flake.nix", "kind": "file"}
  ],
  "changed_repos": 4,
  "changed_files": 23
}
```

| Status | Code | When |
|---|---|---|
| 404 | `not_a_root` | the folder is inside a repo, or has no child repos |
| 403 | `forbidden_path` | the spec 61 path policy refuses the folder |

## Subscribe to a root

`PUT /v1/workbench/git/root/subscribe`

```json
{"root": "/Users/me/coding/sxored", "client": "c-7f3a"}
```

204. Renew every 30 s while the panel is open on that root. `DELETE` with the same body unsubscribes. A subscription that is not renewed for 45 s ends. The root watcher (REQ-WM-080) runs while it has at least one subscription.

## `git.root` event

On `GET /v1/workbench/events`, next to the session events:

```json
{"type": "git.root", "root": "/Users/me/coding/sxored", "etag": "r-91c2", "repos": {"sxored-be-ocrengine": "e-1a0f", "sxored-apigateway": "e-77b3"}}
```

Sent when the summary of a subscribed root changes. `repos` holds the ETag of each repo with changes, so a client reloads only the opened groups whose ETag changed.

## Folder listing outside a repo

`GET /v1/workbench/files/tree?dir=<folder>`

```json
{
  "dir": "/Users/me/coding/sxored",
  "entries": [
    {"name": "docs", "kind": "dir", "modified": "2026-11-22T10:40:00+07:00"},
    {"name": "sxored-be-ocrai", "kind": "dir", "repo": true, "short": "ocr-v2", "color": "#E0805B", "changed": 9},
    {"name": "CLAUDE.md", "kind": "file", "size": 4120, "modified": "2026-11-20T08:00:00+07:00"}
  ],
  "truncated": false
}
```

A folder inside a repo answers 400 `inside_repo`, with the worktree in `message`, so the client uses the spec 66 tree route.

## File outside a repo

`GET /v1/workbench/files/file?path=<file>`

The same response shapes as the spec 66 file route (text body up to 1 MB, or a spec 61 preview token), without git fields.
