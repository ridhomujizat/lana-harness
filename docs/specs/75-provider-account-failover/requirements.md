# Requirements: Account failover inside a provider

- **Issue**: — (owner request 2026-11-25)
- **Status**: Approved (owner: "kalau agentnya di setting di model antigravity dia bisa fallback ke akun satu lagi. jadi bukan di combo. ini diterapin di semua provider"; scope answer "Subscription + API key")
- **Revision**: 1
- **Owner**: ridho
- **Related**: spec 74 (Antigravity), spec 39 (Claude Code accounts), spec 37 (router)

## Goal

An agent set to a provider keeps working when one account of that provider runs out. An agent on
`antigravity/<model>` moves to the next Antigravity account, one on `claude-code/<model>` to the next
Claude Code account, and an API provider with several API keys moves to its next key, without the owner
building a combo. Combos keep working as before, across providers.

## Definitions

- **Pool**: the accounts of one provider that a pooled address may use, in order.
  - Claude Code: the built-in `claude-code` account and every enabled `kind = "claude-code"` account,
    ordered by `priority` (lower first; default 0 for the built-in account, 100 for the others), then
    name. The owner sets the order by drag and drop (REQ-PAF-006); the built-in account can be moved too
    (its optional `[providers.claude-code]` table holds `priority`).
  - Antigravity: the built-in `antigravity` account and every enabled `kind = "antigravity"` account, same
    rule (`[providers.antigravity] priority`).
  - API provider: its `api_key_env` key, then each key in `api_key_envs`, in list order; reordering by drag
    and drop rewrites `api_key_env` (the first) and `api_key_envs` (the rest).
- **Pooled address**: `claude-code/<model>`, `antigravity/<model>`, or `<api-provider>/<model>`.
- **Pinned address**: a named subscription account, `<account>/<model>` (for example `agy-2/…`); it never
  moves to another account.

## Behavior rules

### REQ-PAF-001: Subscription pools

A turn whose first target is a pooled subscription address shall try the pool's accounts in order,
starting at the session's sticky account (REQ-PAF-003), skipping accounts that are cooling down
(REQ-PAF-004). It moves to the next account only on an early failure (before the first text or tool
event) of class `auth` (not signed in), `rate_limit`/`quota` (plan window exhausted, "rejected" rate
limit event, agy quota error). Other early failures (binary missing, too old, process error) skip the rest
of the pool and go to the next chain target as before. When every account failed, the turn continues
with the next chain target (spec 39 REQ-CCT-004). An account with `pool = false` is never used by a pooled
address; the built-in account can be left out the same way through its optional table.

- `AC-PAF-001`: Given accounts `antigravity` (quota error) and `agy-2` (works), an agent on
  `antigravity/gemini-4-neon-high` gets its reply from `agy-2`; the session shows an `assistant/attempt`
  for `antigravity` and the call record names `agy-2`.
- `AC-PAF-002`: Given an agent on `agy-2/gemini-4-neon-high` and `agy-2` out of quota, the turn does not
  use `antigravity`; it goes to the next chain target.

### REQ-PAF-002: Context when the account changes

The ACP or Claude session of one account cannot be resumed on another. When a turn runs on an account
other than the one holding the session's stored runtime session, lana starts a new runtime session and
puts a context block in front of the user message: "Conversation so far (moved from <old account>):"
followed by the session's last turns from lana's own event log (owner and assistant text only, newest
last, at most 12 000 characters, oldest turns dropped first). No model call is made to build it.

- `AC-PAF-003`: Given a session with two earlier turns on `antigravity` and a third turn that fails over
  to `agy-2`, the fake `agy` for `agy-2` gets `session/new` (not `load`) and a prompt whose first block
  holds both earlier owner messages and replies.

### REQ-PAF-003: Sticky account

After a turn succeeds on an account, later turns of the same lana session start at that account. A new
session starts at the first account of the pool.

- `AC-PAF-004`: Given a session that failed over to `agy-2`, the next turn runs on `agy-2` first and
  resumes its ACP session there.

### REQ-PAF-004: Cooldown

An account that failed with `rate_limit`/`quota` cools down until its window reset time when known
(Claude `rate_limit_event` `resetsAt`, agy `/usage` reset of the model's group), else 15 minutes; with
`auth`, 5 minutes. Cooldowns live in memory. When every account of a pool is cooling down, the pool is
tried in order anyway (as the router does for chains).

### REQ-PAF-005: API key pools

`[providers.<name>]` of an API kind may list extra keys: `api_key_envs = ["DS_KEY_2", "DS_KEY_3"]` (names
of secrets, like `api_key_env`). The router builds one client per key. An attempt on a target uses the
first key that is not cooling down; on `rate_limit` (after the usual retries), `auth`, or `quota` (HTTP
402, or a body saying the balance or credit is insufficient) it cools that key down and tries the next key
of the same target at once, before the chain moves on. A key cooling down is skipped. Call records name
the provider; the key is recorded by its secret name only (`key` field), never its value.

- `AC-PAF-005`: Given `[providers.ds] api_key_env = "K1"`, `api_key_envs = ["K2"]` and a fake server that
  answers 402 for K1, a call on `ds/deepseek-flash` succeeds with K2 and the record has `key = "K2"`.
- `AC-PAF-006`: Given both keys answering 429 past their retries, the chain moves to its next target.

### REQ-PAF-006: Dashboard and account order (owner request 2026-11-25: "urutannya bisa diatur akun mana yang pertama dipakai … drag n drop khusus mengatur akunnya")

The Claude Code page and the Antigravity page shall each have an "Account order" list: one row per
account of the pool (label or name, plan/tier, its windows as a small bar, cooldown "Resting until
14:05", a "Use as backup" switch for `pool`), with a drag handle. The top row is used first. Dropping a row
saves at once: lana rewrites `priority` of every account of that provider as 10, 20, 30, … in the new
order in one config write (`expected_version` guarded), and the list shows "Saved". Keyboard: the handle
takes focus, ↑/↓ move the row, and the move is announced. Accounts with `pool = false` stay in the list,
dimmed, below a "Not used as backup" divider, and can be dragged back. An API provider's page has the
same list for its keys (secret names only, never values) with "Add backup key". With more than one
personal subscription account in a pool the page shows: "Lana moves to the next account when one runs
out. Using several personal accounts to get around plan limits can break the provider's terms."

- `AC-PAF-007`: Given accounts `antigravity`, `agy-2`, `agy-3`, when the owner drags `agy-3` to the top,
  then the config has `priority` 10 for `agy-3`, 20 for `antigravity` (in `[providers.antigravity]`), 30
  for `agy-2`, and the next pooled turn tries `agy-3` first.
- `AC-PAF-008`: Given `api_key_env = "K1"`, `api_key_envs = ["K2"]`, when the owner drags K2 above K1,
  then the config has `api_key_env = "K2"`, `api_key_envs = ["K1"]`.

## Not included

- Round-robin or load spreading: the first usable account is always used.
- Moving a running turn after its first output.
- Pools across providers (combos do that).

## Approval

- **Approved by**: ridho (direction, scope) + agent recommendations · **Approved on**: 2026-11-25
