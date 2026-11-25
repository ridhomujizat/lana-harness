# Contract: Antigravity as a router target

- **Requirements**: [requirements.md](requirements.md) r1 · **Revision**: 1 · **Status**: Approved (2026-11-25)
- **Applies to**: config / HTTP / UI / child process

All HTTP endpoints are behind the dashboard session and, for writes, the CSRF check (owner only). `{name}`
is an Antigravity account: `antigravity` (built in) or a `[providers.<name>]` table of kind
`antigravity`; another name answers 404.

## Config

| Operation ID | Change | Requirements |
|---|---|---|
| `API-AGY-001` | Address `<account>/<model>` for Antigravity accounts in `[agents.*] model`, `fallback`, `[combos.*] models`, session model switch, workflows, calendar. `<model>` per `ValidAntigravityModel` (non-empty, no leading `-`, no whitespace) or `default`. | REQ-AGY-001 |
| `API-AGY-002` | `[providers.<name>] kind = "antigravity"` keys: `home` (default `$LANA_HOME/antigravity/<name>`), `binary`, `label` (≤ 60 characters, one line), `allow_scheduled` (bool), `disabled` (bool). Other keys (`base_url`, `api_key_env`, `config_dir`, `auth`, …) are rejected "not allowed for kind antigravity". | REQ-AGY-001 |
| `API-AGY-003` | Built-in account `antigravity` always exists without a table. A `[providers.antigravity]` table may only hold `kind = "antigravity"`, `binary`, `allow_scheduled`, `disabled`; anything else is rejected "antigravity is built in". | REQ-AGY-001 |
| `API-AGY-004` | `[router] background_model` and every background chain skip Antigravity addresses like Claude Code ones. | REQ-AGY-008 |

## Child process

| Operation ID | Change | Requirements |
|---|---|---|
| `API-AGY-010` | Turn: `<binary> acp`, cwd = agent workspace, own process group. Env = daemon env minus `GEMINI_API_KEY`, `GOOGLE_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`, `GOOGLE_GENAI_USE_VERTEXAI`, `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`, plus `AGY_CLI_DISABLE_AUTO_UPDATE=true`; named accounts add `HOME=<home>` and `USERPROFILE=<home>` (each once). Never `--dangerously-skip-permissions`, `--sandbox`. | REQ-AGY-002 |
| `API-AGY-011` | ACP messages lana sends: `initialize{protocolVersion:1, clientCapabilities:{}, clientInfo:{name:"lana", version}}`; `session/new{cwd, mcpServers, _meta:{antigravity:{systemPrompt:{append}}}}` or `session/load{sessionId, cwd, mcpServers, _meta}`; `session/set_model{sessionId, modelId}`; `session/prompt{sessionId, prompt:[{type:"text",text}|{type:"image",mimeType,data}]}`; notification `session/cancel{sessionId}`; replies to `session/request_permission`. `mcpServers` = `[{"type":"http","name":"lana","url":"http://127.0.0.1:<port>/mcp","headers":[{"name":"Authorization","value":"Bearer <token>"}]}]` or `[]` when the host is not available. | REQ-AGY-004, 006, 007 |
| `API-AGY-012` | Status: `<binary> --version` (stdout `1.5.17`), `<binary> models` (account env), cached per REQ-AGY-009. Usage: `<binary> -p /usage --output-format json --print-timeout 30s`, stdin closed, account env, cwd = a new empty temp folder (removed afterwards), 45 s deadline. | REQ-AGY-009 |
| `API-AGY-013` | Sign-in: `<binary> acp --login`, account env, cwd = home, stdin a pipe, 15-minute deadline. The link is the first `https://accounts.google.com/` URL on stdout. | REQ-AGY-010 |

## HTTP

A sign-in state is `{"state":"idle|starting|waiting_code|running|succeeded|failed|cancelled",
"url":"<sign-in link, waiting_code only>","error":"<URL-stripped line, failed only>",
"started_at":"<RFC 3339>|null","finished_at":"<RFC 3339>|null"}`, kept in memory only. `starting` = agy
started, no link yet; `waiting_code` = link shown, waiting for the code; `running` = code sent, waiting
for agy to exit.

| Operation ID | Change | Requirements |
|---|---|---|
| `API-AGY-020` | `GET /v1/router/antigravity` → `{"installed","binary","version","version_ok","min_version":"1.5.17","accounts":[{name, builtin, label, display_name, home, binary, disabled, allow_scheduled, signed_in, error, tier, models_count, running, queued, windows:[{group, type:"5h"|"weekly", remaining, resets_at, source:"usage_probe"}], windows_updated_at, windows_error, usage_24h:{calls, input_tokens, output_tokens}, agents:[…], combos:[…], sign_in:{state…}}]}`. `display_name` = label, else name. `tier` = last tier reported by `session/new` for the account, else `""`. | REQ-AGY-011 |
| `API-AGY-021` | `POST /v1/router/antigravity/accounts` body `{"name"?: string, "label"?: string}`: writes `[providers.<name>] kind = "antigravity"` (default `agy-2`, `agy-3`, … first free provider name), creates the home (0700) with links, starts the sign-in, answers **202** `{"name","home","version","restart_required","sign_in":{…}}`. 409 `exists`, 409 `builtin` (`antigravity`), 409 `config_missing`, 422 bad name. | REQ-AGY-010 |
| `API-AGY-022` | `POST /v1/router/antigravity/accounts/{name}/sign-in` → **202** state; **409** `sign_in_running` while one runs; **409** `builtin` for `antigravity` (sign in with `agy` in a terminal). `GET …/sign-in` → 200 state. `DELETE …/sign-in` → 200 state after cancelling (process group stopped). | REQ-AGY-010 |
| `API-AGY-023` | `POST /v1/router/antigravity/accounts/{name}/sign-in/code` body `{"code": string}` (trimmed, 1–2048 characters, one line) → **202** state `running`; **409** `not_waiting` unless the state is `waiting_code`; **422** `bad_code`. The code is written once to agy's stdin and dropped. | REQ-AGY-010 |
| `API-AGY-024` | `PUT /v1/router/antigravity/accounts/{name}/label` body `{"label": string, "expected_version"?: string}` → `{"name","label","version","restart_required"}`; 409 `builtin`. | REQ-AGY-011 |
| `API-AGY-025` | `DELETE /v1/providers/{name}?remove_folder=1` for an Antigravity account: in-use 409 rules first, then the folder check (**409** `folder_outside_home` unless `home` lies inside `$LANA_HOME/antigravity/`; nothing changed), a running sign-in is cancelled, the home is deleted, then the table; answer adds `"folder_removed": true`. Without the flag the home stays. | REQ-AGY-010 |
| `API-AGY-026` | `GET /v1/router` `choices` gains group `"Antigravity"` (one group per enabled account when there are several: `"Antigravity · <display_name>"`) with `<account>/<modelId>` and label `"Antigravity · <name from agy models>"`, from the account's cached `agy models` list; nothing when `agy` is not installed. `GET /router/v1/models` never lists them; the proxy answers 404 `model_not_found` for them. | REQ-AGY-011, 012 |
| `API-AGY-027` | `POST /v1/router/limits/refresh` and `GET /v1/router/limits?refresh=1` also run the Antigravity usage probe for every enabled, signed-in account (60 s throttle). `GET /v1/router/limits` lists one snapshot per Antigravity account with `kind: "antigravity"`; window `type` = `<group>/5h` or `<group>/weekly`, `label` = `"<group> · 5 hours"` / `"<group> · weekly"`, `utilization` = 1 − remaining. | REQ-AGY-009 |
| `API-AGY-028` | Router call records for Antigravity turns: provider = account name, model = the address model, usage from the prompt result (`inputTokens`, `outputTokens`, `cachedReadTokens`, `thoughtTokens`). | REQ-AGY-005 |

## UI

| Operation ID | Change | Requirements |
|---|---|---|
| `API-AGY-030` | Router → Subscriptions: card "Antigravity" (logo `antigravity-color.svg`) next to "Claude Code"; `#/router/providers/antigravity` detail page with the account rows of API-AGY-020, Refresh, "Add account", and per row Sign in (named accounts, signed out), label edit, Remove (confirm, with "Also delete the account folder" on). | REQ-AGY-011 |
| `API-AGY-031` | Sign-in panel: while `starting` "Starting Antigravity sign-in…"; at `waiting_code` a primary "Open Google sign-in" link (new tab), the hint "After you allow access, Google shows a code on antigravity.google. Paste it here.", a code field, Send, Cancel; at `running` "Signing in…"; `succeeded` highlights the row; `failed` shows the error and "Try again". Polls every 1.5 s. The built-in account row shows "Signed in with your own agy login" or "Run `agy` in a terminal to sign in". | REQ-AGY-010 |
| `API-AGY-032` | With more than one account the page shows: "Each account runs with its own Google sign-in. Using several personal accounts to get around plan limits can break Google's terms." | REQ-AGY-011 |
| `API-AGY-033` | Model pickers show the Antigravity groups of API-AGY-026; Quota and Usage show the Antigravity windows by label. | REQ-AGY-011 |
