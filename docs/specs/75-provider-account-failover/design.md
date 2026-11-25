# Design: Account failover inside a provider

- **Requirements** r1 · **Status**: Approved (2026-11-25)

## Config (additions)

| Key | Where | Meaning |
|---|---|---|
| `priority` (int, ≥ 0) | `kind = "claude-code"` / `"antigravity"` tables, and the optional built-in `[providers.claude-code]` / `[providers.antigravity]` tables | Pool order, lower first. Default 0 built-in, 100 others; ties by name. |
| `pool` (bool, default true) | same tables | false = never used by a pooled address. |
| `api_key_envs` (array of secret names) | API kinds (`anthropic`, `openai`, `openai_compat`, `openrouter`, `deepseek`, `commandcode`) | Backup keys after `api_key_env`. Each must be a valid secret name; duplicates rejected; `missingSecrets` reports each missing one but a provider with at least one present key works. |

## Subscription pools (agent package)

- `llmcall.Pool(llm, target) []core.LLMTarget`: for a pooled address (`claude-code/m`, `antigravity/m`)
  returns one target per pooled account of that runtime, in order, same model; for a pinned address or a
  non-external target returns `[target]`. Router implements `PoolAccounts(runtime) []string` (enabled,
  `pool != false`, sorted by priority then name).
- `agent/external.go` `runExternalTurn`: candidates = `Pool(chain[0])` reordered so the session's sticky
  account (latest `system/message` runtime payload with this runtime that ended in a completed turn) comes
  first, minus accounts cooling down (all of them if every one is). For each candidate: run; success →
  done; early error of class `auth` / `rate_limit` / `quota` → write `assistant/attempt`, record, cool the
  account down, next candidate; other early error → stop the pool and fall back to the native chain as
  today. After the pool is exhausted → native fallback as today.
- Resume: `externalResume` returns the stored runtime session only when runtime **and** account match;
  otherwise the turn starts fresh and, when the session has earlier turns, prepends the context block of
  REQ-PAF-002 (built by a helper `transcriptTail(events, 12000)` from `user/message` and
  `assistant/message` text).
- Cooldowns: in-memory map `account → until` in the agent registry (shared by both runtimes);
  `RuntimeEarlyError` gains `ResetAt time.Time` (Claude: `rate_limit_event.resetsAt`; Antigravity: the
  reset time of the model's group from the last `/usage` snapshot when the error says quota, else zero) →
  until = ResetAt, else now+15 min (`rate_limit`/`quota`) or now+5 min (`auth`). Exposed read-only for the
  dashboard.
- Error classes from the runtimes: Claude already reports `rate_limit` for a rejected window and
  not-logged-in as `auth`; Antigravity maps "not signed in" → `auth`, agy quota/"resource exhausted"/429 text
  → `quota`. Add `core.ErrorClassQuota = "quota"`.

## API key pools (router)

- Adapter build: one client per key (`api_key_env` + `api_key_envs`), held as `snap.keys[provider] []keyClient`
  with the secret name. `attempt` picks the first key not cooling down (key cooldowns: map
  `provider+"\x00"+secretName → until`; all cooling → first key).
- On `rate_limit` past retries, `auth`, or `quota` from a key: cool that key down (rate limit: max(cooldown,
  RetryAfter); auth 5 min; quota 1 h) and retry the same target with the next key at once (no backoff).
  When no key is left, the existing per-target logic decides (cool target, next chain target).
- `quota` class: adapters (or `asProviderError`) classify HTTP 402, and 400/403/429 bodies containing
  "insufficient balance", "insufficient_quota", "credit balance is too low", "exceeded your current quota"
  as `quota`; `effectiveClass` treats `quota` like `auth` for chain movement.
- Records gain `key` (secret name) when the provider has more than one key.

## HTTP

| Method + path | Body / answer |
|---|---|
| `PUT /v1/router/pool-order` | `{"runtime":"claude-code"\|"antigravity","order":[names…],"unpooled":[names…],"expected_version"?}` → rewrites `priority` (10, 20, …) for `order`, sets `pool = false` for `unpooled` and `pool` removed (true) for `order`, one config write; answers `{"version","restart_required":false,"pool":[…]}`. Unknown or missing names → 422; every account must appear exactly once. |
| `PUT /v1/providers/{name}/keys` | `{"keys":[secret names…],"expected_version"?}` (≥ 1) → `api_key_env` = first, `api_key_envs` = rest (removed when empty) → `{"version","keys":[{name, present, cooling_until}]}` |
| `GET /v1/router/claude-agent`, `GET /v1/router/antigravity` | accounts gain `priority`, `pool`, `cooling_until` (RFC 3339 or null), and the list is in pool order; top level gains `pool_warning` (bool, more than one pooled personal account). |
| `GET /v1/providers` | API rows gain `keys:[{name, present, cooling_until}]`. |

## UI

- Shared component `AccountOrderList` (web/ui/src/views/router/account-order.tsx): rows with drag handle
  (pointer drag with a drop indicator line; keyboard ↑/↓ on the focused handle with an aria-live
  announcement), label, plan/tier, mini window bar, cooldown text, "Use as backup" switch; a "Not used as
  backup" divider for `pool = false`; saves on drop and on switch, shows "Saved" / the error and rolls back
  on failure. No new dependency: native pointer events (the repo's existing drag code, if any, is reused).
- Used on the Claude Code page, the Antigravity page (both "Account order" section), and the API provider
  detail ("Keys" section, with "Add backup key" opening the existing secret picker).
- The ToS note of REQ-PAF-006 when `pool_warning`.

## Testing

- llmcall/router: Pool order by priority/name, `pool = false`, built-in priority; key failover 402 → next
  key (AC-PAF-005), 429 on all keys → next target (AC-PAF-006), key cooldown skip, record `key`.
- agent: fake runtime with per-account scripted early errors: AC-PAF-001, 002, 003, 004; cooldown skip and
  all-cooling fallback.
- web: pool-order write (AC-PAF-007), keys reorder (AC-PAF-008), 422 cases.
- UI: drag and keyboard reorder call the API with the new order; rollback on error.
