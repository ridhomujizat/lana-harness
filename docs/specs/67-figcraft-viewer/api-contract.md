# Spec 67 API contract: FigCraft viewer routes

Status: Approved r1, 2026-11-22. Requirements: [requirements.md](requirements.md).

All routes are authenticated like the other `/v1/workbench` routes and answer 503 `workbench_unavailable` when the Workbench is off. Errors use the daemon envelope `{"error": {"code": "...", "message": "..."}}`.

The file is named by `worktree` (an absolute path) and `path` (relative to the worktree), the same as `GET /v1/workbench/git/file` (spec 66).

Common errors:

| Status | Code | When |
|---|---|---|
| 400 | `invalid_request` | a missing or bad parameter |
| 400 | `not_figcraft` | the file does not end in `.figcraft` |
| 403 | `forbidden_path` | the path fails the spec 61 policy or lies outside the worktree |
| 404 | `not_found` | the file, page or node does not exist |
| 424 | `figcraft_cli_missing` | no figcraft-cli found; `message` lists the paths tried |
| 500 | `figcraft_failed` | the CLI or the app answered with an error; `message` carries it |
| 504 | `figcraft_timeout` | the CLI ran over 30 s, or the app over its timeout |

## Info

`GET /v1/workbench/figcraft/info?worktree=<abs>&path=<rel>`

Headers: `ETag: "<fingerprint>"`. `If-None-Match` with the same value returns 304.

```json
{
  "path": "design/lana-design-system.figcraft",
  "source": "live",
  "live_port": 47810,
  "live_error": "",
  "fingerprint": "l-3f9a1c0e",
  "unsaved": true,
  "stale": false,
  "cli": {"path": "/Users/me/.local/share/figcraft-cli/figcraft-cli", "version": "0.18.1"},
  "app": {"version": "0.18.1"},
  "warnings": [],
  "pages": [
    {
      "id": 53932,
      "name": "Remote access (Tailscale)",
      "divider": false,
      "background": "#E4E7EC",
      "origin": {"x": 0, "y": 0},
      "size": {"width": 4970, "height": 1625},
      "layers": [
        {"id": 53933, "name": "Settings – Remote access", "type": "FRAME",
         "bounds": {"x": 0, "y": 0, "width": 1440, "height": 1625}, "child_count": 2, "visible": true}
      ]
    }
  ]
}
```

- `source` is `file` or `live`. In file mode `live_port` is 0 and `unsaved` is false.
- The fingerprint starts with `f-` (file: mtime + size) or `l-` (live: the history hash).
- `warnings` can hold `cli_older_than_app`.
- `stale: true` means the CLI failed on a changing file, so this is the last good result.
- A page with no visible layers has `origin` `{0,0}` and `size` `{0,0}`.

## Layers

`GET /v1/workbench/figcraft/layers?worktree=<abs>&path=<rel>&node=<id>`

```json
{
  "node": 53933,
  "fingerprint": "f-1763754300123456789-2940667",
  "children": [
    {"id": 54066, "name": "Sidebar", "type": "FRAME", "bounds": {"x": 0, "y": 0, "width": 280, "height": 1625}, "child_count": 2, "visible": true}
  ]
}
```

## Render

`GET /v1/workbench/figcraft/render?worktree=<abs>&path=<rel>&page=<id>&scale=<n>[&fp=<fingerprint>]`
`GET /v1/workbench/figcraft/render?worktree=<abs>&path=<rel>&node=<id>&scale=<n>[&fp=<fingerprint>]`

The body is `image/png`. Headers:

| Header | Value |
|---|---|
| `X-Figcraft-Origin-X`, `X-Figcraft-Origin-Y` | page units of the image's top-left pixel |
| `X-Figcraft-Scale` | the scale used (after clamping) |
| `X-Figcraft-Fingerprint` | the fingerprint the image belongs to |
| `X-Figcraft-Stale` | `1` when this is the last good render |
| `X-Figcraft-Render-Ms` | how long the render took (0 from the cache) |
| `Cache-Control` | `private, max-age=86400, immutable` when `fp` matches and the image is not stale, else `no-store` |

`scale` defaults to 1 and is clamped to 0.1–4. For a page, it is also lowered so that the longer side is at most 8,192 px.

## Open in FigCraft

`POST /v1/workbench/figcraft/open`

```json
{"worktree": "/Users/me/code/lana-harness", "path": "design/lana-design-system.figcraft"}
```

The answer is `204`. Off macOS it is 501 `unsupported`.

## Settings

`GET /v1/workbench/figcraft/settings`
`PUT /v1/workbench/figcraft/settings`

```json
{"cli_path": "", "live": true, "live_ports": [47810]}
```

PUT replaces the whole object and returns it. `cli_path` is empty, or an absolute path to an executable file (400 `invalid_cli_path`). `live_ports` holds 1–65535, at most 16 entries.
