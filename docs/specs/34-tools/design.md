# Design: Tools (installable programs)

- **Requirements**: [requirements.md](requirements.md) (Approved r1)
- **Status**: Approved (agent, on the owner's go-ahead 2026-11-05)
- **Design revision**: 1

## Packages and files

| Where | What |
|---|---|
| `internal/toolbox` | Manifest types, TOML parse + validation, file store under `$LANA_HOME/tools`, detection, install/uninstall jobs with logs, command execution, `Service` with change broadcast. Imports only stdlib, `core`, `internal/*` and the TOML library already in go.mod. |
| `lana_toolbox.go` | Runtime glue: `Runtime.Toolbox()`, command tools registered in the tool registry (`<pkg>_<cmd>`), refresh on every change, the `tools_request` built-in tool, the "Installed tools" ToolCatalog group, Obscura resolution for the workflow browser node. |
| `web/toolbox.go` | HTTP API (`/v1/toolbox/*`) and the SSE stream. |
| `web/ui` | `api/toolbox.ts`, `views/tools.tsx` (list + add), `views/tool-detail.tsx`, palette group in the workflow editor. |

## Manifest (`$LANA_HOME/tools/<id>/tool.toml`)

```toml
id = "obscura"                       # ^[a-z0-9][a-z0-9-]{0,39}$
name = "Obscura"
description = "Headless browser for scraping and automation"
homepage = "https://www.obscura.sh"

[detect]
binary = "obscura"                   # looked up in <home>/tools/<id>/bin, then PATH
version_args = ["--version"]         # optional; first line of stdout is the version
min_version = ""                     # optional, dotted numbers

[[install]]                          # tried in order; the owner may pick another
label = "Homebrew"
command = "brew install obscura"     # run with bash -c in <home>/tools/<id>/, PATH + BIN_DIR=<home>/tools/<id>/bin

[[install]]
label = "Download (macOS arm64)"
url = "https://…/obscura-darwin-arm64.tar.gz"
sha256 = ""                          # optional; verified when set
os = "darwin"                        # optional filter: darwin|linux
arch = "arm64"                       # optional filter: arm64|amd64
extract = "obscura"                  # path of the binary inside the archive (tar.gz, zip) or "" for a raw binary

[uninstall]
command = ""                         # optional; default removes bin/

[[commands]]
name = "fetch"                       # ^[a-z][a-z0-9_]{0,29}$ → tool "obscura_fetch"
description = "Load a page in a real browser and return its text"
args = ["fetch", "--dump", "{{format}}", "{{url}}"]   # {{field}} = input value; a field that renders empty drops its arg
risk = "read"                        # read|write|admin
timeout_secs = 60
output = "text"                      # text|json
workdir = "data"                     # data (<home>/tools/<id>/data) | workspace (the calling session workspace)

[commands.input]                     # field → {type, description, required, default, enum}
url = { type = "string", description = "Page URL", required = true }
format = { type = "string", enum = ["markdown", "text", "html"], default = "markdown" }

[env]                                # VAR = "secret:<NAME>" or a literal
```

Validation: id/name/command name patterns, at least one detect binary, args reference only declared fields,
`risk`/`output`/`workdir` enums, `install` entries have exactly one of `command` or `url`.

## Status and detection

`Status`: `missing`, `installing`, `installed`, `too_old`, `broken`, `failed` (last install failed). Detection resolves
the binary (package `bin/` first, then PATH), runs `version_args` with a 10 s timeout, and compares `min_version`.
Results are cached and recomputed on start, after each install/uninstall, and on "Check again".

## Jobs

Install and uninstall run one at a time per package as a goroutine writing `<home>/tools/<id>/logs/<job>.log` and a
`Job{ID, Package, Kind, Method, Status (running|succeeded|failed|cancelled), StartedAt, EndedAt, Error}`. Shell
commands run with `bash -c` (no login shell), env = daemon env + `BIN_DIR`, `PKG_DIR`, `DATA_DIR`, timeout 15 min,
cancellable. Downloads stream to a temp file, verify sha256 when set, extract the binary into `bin/` with mode 0755.

## Command execution

`Run(ctx, pkg, command, input map, workspace)`: render args (every `{{field}}` replaced by the input value as a
string; missing optional fields with no default drop the whole arg), exec the resolved binary (no shell, so no
injection), env from `[env]` (secrets resolved through an injected resolver), timeout, combined output capped at
64 KiB; exit ≠ 0 → error with the last 500 chars. Output `json` → must parse.

## Tools in sessions and workflows

- Each `installed` command becomes a `core.Tool` named `<pkg>_<cmd>` with an input schema built from `[commands.input]`
  and risk from the manifest. The registry gains `Replace(prefix-group)`/`Unregister` so the set refreshes on change.
- `ToolCatalog` adds a group `installed` ("Installed tools") listing them; enabling one adds it to the agent's `tools`.
- `tools_request` (built-in, risk admin, always asks): input `{package, reason}`; with approval it starts the install
  job of an existing package and waits up to 10 minutes; unknown package → error telling the agent to ask the owner to
  add it on the Tools page.
- Workflows: the `tool` node already calls any registered tool; the browser node resolves the `obscura` binary through
  the toolbox (any installed package whose detect binary is `obscura`), else PATH, else the error
  "Obscura is not installed. Add it on the Tools page."

## Web API

| Method | Path | Body / result |
|---|---|---|
| GET | `/v1/toolbox` | `{packages: [PackageView]}` |
| POST | `/v1/toolbox` | `{manifest: "<toml>"}` → 201 PackageView; 400 `invalid_manifest` with `details[0].issues` |
| GET | `/v1/toolbox/{id}` | PackageView + `manifest` (TOML text) + `jobs` (last 10) + `used_by` |
| PUT | `/v1/toolbox/{id}` | `{manifest}` → PackageView |
| DELETE | `/v1/toolbox/{id}` | 204 (refused 409 `package_installed` unless `?uninstall=1`) |
| POST | `/v1/toolbox/{id}/install` | `{method: index}` → 202 Job |
| POST | `/v1/toolbox/{id}/uninstall` | 202 Job |
| POST | `/v1/toolbox/{id}/check` | PackageView |
| GET | `/v1/toolbox/{id}/jobs/{job}/log` | `{text, done}` (tail 64 KiB) |
| POST | `/v1/toolbox/{id}/run` | `{command, input}` → `{output, exit_code, duration_ms}` (Try it; owner only) |
| GET | `/v1/toolbox/stream` | SSE `changed` `{package}` |

`PackageView`: `{id, name, description, homepage, status, version, binary_path, error, installing, commands:
[{name, tool, description, risk, input: [{name, type, description, required, default, enum}]}], install:
[{label, kind: command|download, detail}], updated_at}`.
