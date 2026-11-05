# Tasks: Tools (installable programs)

- **Requirements**: [requirements.md](requirements.md) (Approved r1) · **Design**: [design.md](design.md) (Approved r1)
- **Status**: P1 completed 2026-11-05 · branch `feature/workflows`

- [x] T1 `internal/toolbox`: manifest, store, detection, install/uninstall jobs, command runs — `go test -race ./internal/toolbox` · 24b… see `git log`
- [x] T2 runtime glue: commands as tools (`<pkg>_<cmd>`), registry Unregister, "Installed tools" catalog group, `tools_request` (admin risk), browser node via Tools — `go test -race . ./tools/...` · 3b54404
- [x] T3 web API `/v1/toolbox*` + stream — `go test ./web` · 148ecd9
- [x] T4 daemon wiring — 563856d
- [x] T5 web UI: Tools list, Add (manifest editor), detail (install methods, live log, Try it, manifest, Used by), workflow palette group — vitest 258 pass · 664cbc8, 36fd880
- [x] T6 verify: `golangci-lint run ./...` 0 issues, `go test -race ./...` pass, `make web-check`

## Runtime smoke (2026-11-05, scratch LANA_HOME, port 18398)

| Check | Result |
|---|---|
| Add the Obscura manifest (POST /v1/toolbox) | 201, detected on PATH: installed 0.2.2 |
| Try it `fetch https://example.com` | output "Example Domain …" |
| `/v1/tools?agent=default` | group `installed` with `obscura_fetch` (read) |
| Enable for agent `default`, chat "pakai obscura_fetch …" | the turn called `obscura_fetch`, no approval (read risk) |
| Workflow `tool` node `obscura_fetch` + `browser` node | both succeeded |
| Tool detail Used by | agent default, workflow "Smoke tool node" |

Not exercised live: a real install command (Obscura was already installed), download installs (unit tests with httptest),
`tools_request` approval in a chat (unit test only).

## Revision 2 (catalog, one-click install, MCP packages)

- [x] R2-T1 built-in catalog (obscura, yt-dlp, pandoc, ffmpeg), auto method pick, whole-archive extract, `[mcp]` packages — 898a8e1
- [x] R2-T2 web: app list with one-click Install, detail with "Use with agents" switches, MCP server sync — 63c3efd, e018e6f, aaf8f8b
- [x] R2-T3 fixes found in the smoke: a missing agent tool warns instead of stopping lana (b017cba); permission changes
  apply without a restart (d1a12de); cancelled reads no longer poison pooled SQLite connections, turn/end retried on
  a busy store (dfffac5)
- [x] R2-T4 verify: `golangci-lint run ./...` 0 issues, `go test -race ./...` pass, vitest 258 pass

### Runtime smoke r2 (2026-11-05, scratch LANA_HOME, port 18398, build dfffac5)

| Check | Result |
|---|---|
| `GET /v1/toolbox` | 4 catalog apps; Obscura detected on PATH |
| "Use with agents" on for `default` | config gains `[mcp.obscura]` and `mcp = ['obscura']`; MCP connects with 37 tools |
| Chat: open example.com, click the link, report title + URL | `browser_navigate` → `browser_interactive_elements` (`e1` "Learn more") → `browser_click e1` → `browser_snapshot`: "Example Domains", https://www.iana.org/help/example-domains; turn ended, session idle, no approval prompt (trust) |

Not exercised live: a real catalog install (needs the owner to click Install), `tools_request` approval in a chat.
