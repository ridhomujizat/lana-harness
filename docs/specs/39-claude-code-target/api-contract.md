# Contract: Claude Code as a router target

- **Requirements**: [requirements.md](requirements.md) r3 · **Revision**: 3 · **Status**: Approved (2026-11-08)
- **Applies to**: config / HTTP / UI

| Operation ID | Change | Requirements |
|---|---|---|
| `API-CCT-001` | Address `claude-code/<model>` accepted in `[agents.*] model`, `fallback`, `[combos.*] models`, session model switch; `[providers.claude-code]` is rejected ("claude-code is built in") | REQ-CCT-001 |
| `API-CCT-002` | `[agents.*] runtime` deprecated: `"claude-code"` + `model = "M"` ≡ `model = "claude-code/M"`; dashboard writes drop `runtime` | REQ-CCT-002 |
| `API-CCT-003` | `[router] background_model` (string, address or combo without `claude-code/*`, default "") | REQ-CCT-007 |
| `API-CCT-004` | `GET /v1/router` `choices` gains group `"Claude Code"` with the aliases `claude-code/opus`, `claude-code/sonnet`, `claude-code/haiku` (labels "Claude Code · Opus (latest)" …) **plus every specific model Claude Code reports in its `initialize` response** (e.g. `claude-code/claude-opus-5-5`, label "Claude Code · Opus 5.5"; owner request 2026-11-08), falling back to the catalog's Anthropic models when no list has been captured yet, when the binary is found; `GET /router/v1/models` does **not** list them (the proxy never routes to Claude Code) | REQ-CCT-009 |
| `API-CCT-005` | `GET /v1/router/claude-agent` keeps its shape; `configured_agents` lists agents and combos whose chains contain `claude-code/*` (agents as `name`, combos as `combo:<name>`) | REQ-CCT-009 |
| `API-CCT-006` | Agent list/detail: `engine_label` removed from the UI's use; items keep `runtime` only for legacy files (always `""` after a dashboard save) and gain `claude_code: true` when the agent's first target is `claude-code/*` | REQ-CCT-009 |
| `API-CCT-007` | Loopback MCP (internal, not public API): `POST http://127.0.0.1:<port>/mcp` streamable HTTP, header `Authorization: Bearer <token>`; server name `lana`; Claude sees tools as `mcp__lana__<tool>` | REQ-CCT-006 |
| `API-CCT-008` | Router call records for Claude Code turns: provider `claude-code`, `requested` = the chain's requested value, model from `system/init` (renamed from `claude-agent`; limits snapshot provider renamed to `claude-code`; old rows keep `claude-agent`) | REQ-CCT-004 |
| `API-CCT-009` | UI: no Engine radio; Claude Code models in pickers; Router Subscriptions card "Claude Code" | REQ-CCT-009 |

## Revision 2 (2026-11-08): accounts, custom models

| Operation ID | Change | Requirements |
|---|---|---|
| `API-CCT-010` | `[providers.<name>] kind = "claude-code"` with keys `config_dir` (default `$LANA_HOME/claude/<name>`), `binary`, `auth` (`subscription`/`api_key`), `api_key_env`, `allow_scheduled`, `disabled`. The built-in account `claude-code` (no table, `~/.claude`) always exists. `GET /v1/providers` lists every account with `kind: "claude-code"`, `builtin: true` for the built-in one, `config_dir`, `allow_scheduled`. `PUT /v1/providers/{name}` with `kind: "claude-code"` creates the folder (0700) and answers `login_command: "CLAUDE_CONFIG_DIR='<dir>' claude"` (then `/login`). `DELETE` removes the table only (the folder stays; the answer says where). | REQ-CCT-010 |
| `API-CCT-011` | `GET /v1/router/claude-agent` gains `accounts: [{name, builtin, config_dir, binary, version, version_ok, logged_in, email, plan, auth_method, error, running, queued, login_command, windows:[{type,status,utilization,resets_at}], agents:[…], combos:[…]}]`. `email` and `plan` (`subscriptionType`, else `planTier`) come from that account's `claude auth status` stdout, cached 60 s, never stored. Top-level fields keep describing the built-in account. | REQ-CCT-011 |
| `API-CCT-012` | Limits: one snapshot per account, provider = account name. Records: provider = account name. Addresses `<account>/<model>`; `GET /v1/router/models?provider=<account>` returns the same Claude Code model list for every account. | REQ-CCT-010, 011 |
| `API-CCT-013` | `[router] claude_code_models` (custom IDs) and `PUT /v1/router/claude-code/models` `{"models":[…],"expected_version"}` — already implemented in T-CCT-01. | owner request |
| `API-CCT-014` | The `--mcp-config` value is a path to a 0600 temp file removed when the process exits, so the session token is not visible in the process list. | REQ-CCT-006 |

## Revision 3 (2026-11-08): one-click sign-in (REQ-CCT-012)

All endpoints are behind the dashboard session and CSRF check (owner only). `{name}` is a Claude Code
account: `claude-code` (built in) or a `[providers.<name>]` table of kind `claude-code`; another name
answers 404. A sign-in state is `{"state":"idle|running|succeeded|failed|cancelled","error":"<URL-stripped
line, failed only>","started_at":"<RFC 3339>|null","finished_at":"<RFC 3339>|null"}`; it lives in memory only.

| Operation ID | Change | Requirements |
|---|---|---|
| `API-CCT-015` | `POST /v1/router/claude-code/accounts` body `{"name"?: string}` (empty body allowed): writes `[providers.<name>] kind = "claude-code"` (name default `claude-2`, `claude-3`, … first free provider name), creates `$LANA_HOME/claude/<name>` (0700), starts the sign-in, answers **202** `{"name","config_dir","version","restart_required","login_command","sign_in":{"state":"running",…}}`. 409 `exists` for a taken name, 409 `builtin` for `claude-code`, 409 `config_missing`, 422 for a bad name. | REQ-CCT-012 |
| `API-CCT-016` | `POST /v1/router/claude-code/accounts/{name}/sign-in` → **202** state (`running`), **409** `sign_in_running` while one runs. `GET …/sign-in` → 200 state. `DELETE …/sign-in` → 200 state after cancelling (process group stopped). The daemon runs `claude auth login` with the account env, stdin closed, stdout discarded, 10-minute deadline; success is exit 0. The built-in account signs in to the owner's own `~/.claude`. | REQ-CCT-012 |
| `API-CCT-017` | `POST /v1/router/claude-code/accounts/{name}/sign-out` body `{"confirm"?: bool}` runs `claude auth logout` (30 s) and answers 200 with the refreshed account row (as in `accounts[]` below). The built-in account needs `{"confirm": true}`, else **400** `confirm_required`. 409 `sign_in_running`; 422 `sign_out_failed` with the URL-stripped line. | REQ-CCT-012 |
| `API-CCT-018` | `DELETE /v1/providers/{name}?remove_folder=1` for a Claude Code account: the in-use 409 rules first, then the folder check (**409** `folder_outside_home` unless the folder lies inside `$LANA_HOME/claude/`; nothing is changed), then sign-out (a failure is reported in `message`, not fatal), then the folder is deleted, then the table. The answer adds `"folder_removed": true`. Without the flag the folder stays as before. Other values than `1`/`0`/`true`/`false` answer 400. | REQ-CCT-012 |
| `API-CCT-019` | `PUT /v1/router/claude-code/accounts/{name}/label` body `{"label": string, "expected_version"?: string}` stores `label` in the account table (trimmed, one line, at most 60 characters; `""` removes it) and answers `{"name","label","version","restart_required"}`. The built-in account answers 409 `builtin`. Config: `[providers.<name>] label` is accepted for kind `claude-code` only. `GET /v1/providers` rows of kind `claude-code` add `label`. | REQ-CCT-012 |
| `API-CCT-020` | `GET /v1/router/claude-agent` `accounts[]` items add `label` (stored label or `""`), `display_name` (label, else e-mail, else name), and `sign_in` (the state above; `idle` when none). `login_command` becomes `CLAUDE_CONFIG_DIR='<dir>' CLAUDE_SECURESTORAGE_CONFIG_DIR='<dir>' claude auth login` (`claude auth login` for the built-in account). | REQ-CCT-012 |
| `API-CCT-021` | Process environment of every `claude` run for a named account (turns, `auth status`, test turns, `auth login`/`logout`): `CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR` = the folder (each once), without `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`. The built-in account keeps the daemon's folder variables. | REQ-CCT-012 |
