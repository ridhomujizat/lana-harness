# Spec 66 API contract: Workbench git routes

Status: Approved r1, 2026-11-21. Requirements: [requirements.md](requirements.md).

All routes are authenticated like the other `/v1/workbench` routes (spec 55 REQ-WB-040) and answer 503 `workbench_unavailable` when the Workbench is off. Errors use the daemon envelope `{"error": {"code": "...", "message": "..."}}`. Paths in requests and responses are absolute. Times are RFC 3339.

Common errors:

| Status | Code | When |
|---|---|---|
| 400 | `invalid_request` | bad JSON, missing field, unknown scope |
| 403 | `forbidden_path` | the path fails the spec 61 path policy (REQ-WP-003) |
| 404 | `not_a_repo` | the path is not inside a git worktree |
| 404 | `not_found` | the file, worktree or commit does not exist |
| 500 | `git_failed` | git exited non-zero; `message` carries its first stderr line |
| 504 | `git_timeout` | git ran over 10 s |

## Resolve

`POST /v1/workbench/git/resolve`

```json
{"dirs": ["/Users/me/code/lana-harness", "/Users/me/code/lana-harness-worktree/router"]}
```

```json
{
  "dirs": {
    "/Users/me/code/lana-harness": {
      "repo": "/Users/me/code/lana-harness/.git",
      "worktree": "/Users/me/code/lana-harness",
      "branch": "perf/chat-load",
      "short": "chat-load",
      "detached": false,
      "head": "b01db02",
      "main": true,
      "color": "#5B7BE0"
    },
    "/Users/me/Downloads": {"repo": null}
  }
}
```

At most 64 dirs per call. A dir that fails the path policy maps to `{"error": "forbidden_path"}` instead of failing the call.

## Status

`GET /v1/workbench/git/status?worktree=<path>&scope=uncommitted|branch|last_commit|commit:<sha>`

Headers: `ETag`. `If-None-Match` with the same value returns 304.

```json
{
  "worktree": "/Users/me/code/lana-harness-worktree/router",
  "branch": "feature/router",
  "upstream": "origin/feature/router",
  "ahead": 2,
  "behind": 0,
  "scope": "branch",
  "base": "origin/development",
  "merge_base": "e2f1c40",
  "commits": [
    {"sha": "a91c2f0", "subject": "feat(web): Files and Changes panel", "time": "2026-11-21T10:02:11Z"}
  ],
  "files": [
    {"path": "web/ui/src/views/workbench.tsx", "status": "M", "add": 41, "del": 22, "binary": false},
    {"path": "web/ui/src/styles/workbench.css", "old_path": "web/ui/src/styles/pages/workbench.css", "status": "R", "add": 64, "del": 18, "binary": false},
    {"path": "design/export/a.png", "status": "U", "add": 0, "del": 0, "binary": true}
  ],
  "totals": {"files": 3, "add": 105, "del": 40}
}
```

`commits` is present only for `scope=branch`. `status` is one of `M`, `A`, `D`, `R`, `U` (untracked), `C` (conflict).

## Diff

`GET /v1/workbench/git/diff?worktree=<path>&path=<relative file>&scope=<scope>`

```json
{
  "path": "web/ui/src/views/workbench.tsx",
  "status": "M",
  "binary": false,
  "truncated": false,
  "hunks": [
    {
      "header": "@@ -412,9 +412,14 @@ export function WorkbenchPage()",
      "old_start": 412,
      "new_start": 412,
      "lines": [
        {"kind": "ctx", "old": 412, "new": 412, "text": "export function WorkbenchPage() {"},
        {"kind": "add", "new": 415, "text": "  const [panel, setPanel] = useState<WbPanel>(null);"},
        {"kind": "del", "old": 421, "text": "  const openPreview = (path: string) => {"}
      ]
    }
  ]
}
```

A line has `old` for `ctx` and `del`, `new` for `ctx` and `add`. `"\\ No newline at end of file"` is folded into the previous line as `"no_newline": true`.

## Files

`GET /v1/workbench/git/tree?worktree=<path>&dir=<relative folder, "" for the root>`

```json
{
  "dir": "internal/workbench",
  "truncated": false,
  "entries": [
    {"name": "preview", "kind": "dir", "changed": false, "ignored": false},
    {"name": "files.go", "kind": "file", "status": "U", "ignored": false, "size": 4210}
  ]
}
```

`GET /v1/workbench/git/search?worktree=<path>&q=<text>` → `{"files": ["web/ui/src/views/workbench.tsx", ...]}` (at most 50).

`GET /v1/workbench/git/file?worktree=<path>&path=<relative file>`

```json
{"path": "web/ui/src/views/workbench.tsx", "kind": "text", "size": 68211, "lines": 1961, "language": "typescript", "text": "..."}
```

For any other kind, the body is the spec 61 preview response (`kind`, `url`, `raw_url`, …) and `text` is absent.

## Worktrees

`GET /v1/workbench/git/worktrees?repo=<repo id or any path in it>`

```json
{
  "repo": "/Users/me/code/lana-harness/.git",
  "base": "origin/development",
  "root": "/Users/me/code/lana-harness-worktree",
  "worktrees": [
    {
      "path": "/Users/me/code/lana-harness-worktree/router",
      "name": "router",
      "branch": "feature/router",
      "head": "b13c197",
      "head_time": "2026-11-08T09:12:00Z",
      "main": false,
      "locked": false,
      "prunable": false,
      "dirty": 0,
      "ahead": 0,
      "merged": true,
      "agent": false,
      "location": "container",
      "color": "#3E9C8F",
      "error": ""
    }
  ]
}
```

`location` is `main`, `container`, `sibling` or `other`.

`GET /v1/workbench/git/worktrees/copy-candidates?repo=<…>` → `{"files": [{"path": ".claude/implementer.local", "size": 4, "default": true}, ...]}`.

`GET /v1/workbench/git/worktrees/name?repo=<…>&branch=feature/router` → `{"path": "/Users/me/code/lana-harness-worktree/router"}` (REQ-WG-030).

### Create

`POST /v1/workbench/git/worktrees`

```json
{
  "repo": "/Users/me/code/lana-harness/.git",
  "branch": "feature/worktree-manager",
  "new_branch": true,
  "base": "origin/development",
  "fetch": true,
  "path": "/Users/me/code/lana-harness-worktree/worktree-manager",
  "copy": [".claude/implementer.local", ".env"]
}
```

201 with the worktree object. Errors: 409 `branch_exists`, 409 `path_exists`, 500 `fetch_failed` (nothing created).

### Remove

`POST /v1/workbench/git/worktrees/remove`

```json
{"path": "/Users/me/code/lana-harness/.claude/worktrees/agent-a2eab87b6ba4", "force": true, "confirm": "agent-a2eab87b6ba4", "delete_branch": false}
```

200 `{"removed": true, "branch_deleted": false}`. Errors: 409 `in_use` (`"sessions": [ids]`), 409 `has_work` (`"dirty": 4, "unpushed": 1`), 400 `confirm_mismatch`, 400 `main_worktree`.

### Clean up, move, prune, lock

`POST /v1/workbench/git/worktrees/cleanup` `{"paths": [...], "delete_branch": true, "delete_remote": false}`
`POST /v1/workbench/git/worktrees/move` `{"moves": [{"path": "...", "to": "..."}]}`

Both answer 200 with one result per item:

```json
{"results": [{"path": "...", "result": "removed"}, {"path": "...", "result": "skipped", "reason": "in_use"}]}
```

`POST /v1/workbench/git/worktrees/prune` `{"repo": "..."}` → `{"pruned": ["..."]}`
`POST /v1/workbench/git/worktrees/lock` `{"path": "...", "locked": true}` → the worktree object.

## Settings

`GET /v1/workbench/git/settings?repo=<…>` → `{"base": "development", "worktree_root": "/Users/me/code/lana-harness-worktree", "warn_shared": true}`
`PUT /v1/workbench/git/settings?repo=<…>` with the same shape; omitted fields keep their value. 400 `invalid_request` when `worktree_root` fails the path policy or `base` is not a branch.
