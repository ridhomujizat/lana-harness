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
