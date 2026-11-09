# Requirements: LLM router

- **Issue**: — (owner request 2026-11-08)
- **Status**: Approved (owner interview 2026-11-08; the owner said "ya ikuti rekomendasimu" for the rest, so the remaining details are agent recommendations)
- **Revision**: 1
- **Owner**: ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: [0019](../../adr/0019-llm-router-as-embeddable-core.md), [0015](../../adr/0015-embeddable-runtime-for-services.md)
- **Related diagram**: — (feature-local view in [design.md](design.md))

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | "saya mau buat core router. jadi model llm dan provider di atur di situ … bisa di implement di project go lain … seperti 9router-go … openai, deepseek, anthropic, openrouter api key … agentnya setting berdasarkan router proxy tersebut … punya menu sendiri Router di sidebar … di halaman routernya juga bisa melihat usage dan limit" | goal, providers, Router page, usage and limits |
| Owner interview | 2026-11-08: library **and** HTTP proxy; agents pick `provider/model` or a combo; native Anthropic adapter; Router page has providers & keys, model list, combos, usage & request log | form, addressing, Anthropic, page scope |
| External reference | [9router-go](https://github.com/luqman-v1/9router-go) | `provider/model` addressing, combos with fallback, cooldown, OpenAI-compatible proxy, usage dashboard |
| ADR | [0015](../../adr/0015-embeddable-runtime-for-services.md) | library packages stay lean |

## Goal

The owner manages every LLM provider, API key, model, and fallback chain in one place, the Router. lana's
agents pick their model from the Router, and calls fail over to the next model when a provider is down
or rate limited. The Router page shows how many tokens each provider and model used, what it cost, how
fast it answered, recent requests, and the limits the provider reports. The same router is a Go package
that another Go service can import, and the lana daemon can also expose it as an OpenAI-compatible
endpoint for other tools.

## Actors

- **Owner** — the signed-in dashboard user and the author of `config.toml`. Manages providers, combos,
  proxy keys.
- **Agent** — a lana agent whose turns call the router.
- **Proxy client** — any program that calls the daemon's OpenAI-compatible endpoint with a proxy key.
- **Go service** — another Go program that imports the router package.

## Definitions

- **Provider** — one `[providers.<name>]` entry: a kind, a base URL, and an API key.
- **Kind** — the wire protocol and preset of a provider: `openai`, `deepseek`, `openrouter`,
  `anthropic`, or `openai_compat` (any other OpenAI-compatible endpoint).
- **Model address** — `<provider>/<model>`, split on the **first** `/`. `openrouter/openai/gpt-5-mini`
  is provider `openrouter`, model `openai/gpt-5-mini`.
- **Combo** — a named, ordered list of model addresses (`[combos.<name>]`). A call to a combo tries the
  addresses in order.
- **Chain** — the ordered targets of one call: the combo's addresses, or an agent's model followed by
  its `fallback` entries.
- **Attempt** — one HTTP call to one target.
- **Cooldown** — a period in which a target that just failed with `rate_limit` or `auth` is skipped.
- **Call record** — one stored row per attempt, used for usage, cost, and the request log.
- **Limit snapshot** — the latest limit values a provider reported for a key.
- **Proxy key** — a bearer secret that lets a proxy client use the proxy endpoint.

## Scope

### Included

- A router Go package with provider kinds, model addressing, combos, fallback, cooldown, call
  recording, and limit snapshots, importable by other Go modules.
- Kinds `openai`, `deepseek`, `openrouter`, `anthropic` (native Messages API), and the existing
  `openai_compat`.
- Agents set `model = "<provider>/<model>"` or `model = "<combo>"`. The existing `provider` +
  `model` + `fallback` form keeps working.
- Every LLM call in lana (turns, compaction, memory capture, knowledge passes, embeddings) goes through
  the router.
- Provider and combo changes apply without a daemon restart.
- An OpenAI-compatible proxy endpoint (`chat/completions`, `models`) on the daemon, off by default,
  guarded by proxy keys.
- A Router page in the sidebar: Overview (usage and limits), Providers, Models, Combos, Logs, Proxy.
- The Settings → Providers tab moves to the Router page.

### Not included

- OAuth or subscription login for any provider (ADR 0018 owns the Claude subscription path).
- Spending budgets or caps set by the owner (follow-up).
- Proxy endpoints other than `chat/completions` and `models` (no `/messages`, `/responses`,
  `/embeddings`, images, audio).
- Round-robin, sticky, or capacity strategies, multiple keys per provider, proxy pools.
- Token compression of tool results.
- Providers other than the five kinds above (Gemini, Bedrock, Vertex can be added later as kinds).

## Assumptions

- The four first providers authenticate with one API key each. — Source: owner request.
- The lana daemon is a personal, single-owner process. — Source: ADR 0016.

## Behavior rules

### REQ-RTR-001: Provider kinds and presets

The router shall support the kinds `openai`, `deepseek`, `openrouter`, `anthropic`, and
`openai_compat`. For the first four, `base_url` is optional and defaults to the provider's public API
URL; `api_key_env` defaults to `<NAME>_API_KEY` in upper case. `openai_compat` needs `base_url`.

#### Acceptance criteria

- `AC-RTR-001`: Given `[providers.ds] kind = "deepseek"` with no `base_url`, when an agent calls
  `ds/deepseek-flash`, then the request goes to `https://api.deepseek.com/v1/chat/completions` with the
  key from `DS_API_KEY`.

### REQ-RTR-002: Native Anthropic adapter

A provider of kind `anthropic` shall call the Anthropic Messages API with streaming, and support system
prompts, tool use and tool results, image input, extended thinking mapped from the reasoning effort,
prompt caching of the system prompt and tools, cached-token usage, and error classes equal to the other
kinds.

#### Acceptance criteria

- `AC-RTR-002`: Given a recorded Anthropic SSE stream with thinking, text, and a tool call, when the
  adapter replays it, then lana receives thinking deltas, text deltas, one tool_use block, usage with
  cached input tokens, and stop reason `tool_use`.
- `AC-RTR-003`: Given an Anthropic 429 with `retry-after: 7` and a 529 overloaded answer, when the
  adapter classifies them, then the classes are `rate_limit` (RetryAfter 7s) and `server`.

### REQ-RTR-003: Model addressing

An agent's `model` without `provider` shall be read as a combo name when a combo has that name, else as
a model address split on the first `/`. When `provider` is set, `model` is the provider's model ID as
before. Fallback entries accept `provider:model` and `provider/model`.

#### Acceptance criteria

- `AC-RTR-004`: Given combos `coding` and provider `openrouter`, when agents use `model = "coding"`,
  `model = "openrouter/openai/gpt-5-mini"`, and the legacy `provider = "openrouter"` +
  `model = "openai/gpt-5-mini"`, then the chains are the combo list, `[openrouter · openai/gpt-5-mini]`,
  and `[openrouter · openai/gpt-5-mini]`.
- `AC-RTR-005`: Given an agent `model = "nope/x"` where `nope` is not a provider, when config is
  validated, then validation fails at `agents.<name>.model` with "unknown provider or combo".

### REQ-RTR-004: Combos

A combo shall have a non-empty ordered `models` list of model addresses and an optional
`description`. A combo name must not contain `/`, must not equal a provider name, and a combo must not
list another combo.

#### Acceptance criteria

- `AC-RTR-006`: Given `[combos.coding] models = ["a/x", "coding2"]` where `coding2` is a combo, when
  config is validated, then validation fails at `combos.coding.models[1]`.

### REQ-RTR-005: Fallback through a chain

The router shall try the chain's targets in order, only before the first stream event of an attempt:

- `rate_limit`, `server`, `network`: retry the same target with the existing backoff, at most 2 retries
  when later targets remain, at most 5 on the last target. A `rate_limit` whose Retry-After exceeds 20s
  moves to the next target at once when one remains.
- `auth`, `missing_secret`, and HTTP 404 (`not_found`): move to the next target at once.
- `client` and `context_length`: stop and return the error; no fallback.

Each attempt is reported to the caller (lana writes `assistant/attempt`) and recorded.

#### Acceptance criteria

- `AC-RTR-007`: Given a chain `[a/x, b/y]` where `a` returns 401, when a turn runs, then `b/y` answers,
  one `assistant/attempt` event names `a/x` with class `auth`, and `step/end` names provider `b`, model `y`.
- `AC-RTR-008`: Given a chain `[a/x, b/y]` where `a` returns 400 `client`, when a turn runs, then the
  turn fails with the client error and `b` is never called.

### REQ-RTR-006: Cooldown

A target that ends its attempts with `rate_limit` or `auth` shall be skipped by later calls for the
cooldown (default 5 minutes, or the provider's Retry-After when longer). When every target in a chain is
cooling down, the router tries the full chain in order. Cooldown state lives in memory.

#### Acceptance criteria

- `AC-RTR-009`: Given `a/x` hit a 429 at 10:00 with cooldown 5m, when a call on `[a/x, b/y]` starts at
  10:02, then `a` is not called and `b/y` answers; at 10:06 `a/x` is tried first again.

### REQ-RTR-007: All lana LLM calls go through the router

Turns, compaction, memory capture, knowledge compile/journal/lint, and knowledge embeddings shall call
the router, so all of them get fallback, cooldown, and call records. Embeddings use providers whose kind
supports them (`openai`, `openrouter`, `openai_compat`, `deepseek` when the API offers it).

#### Acceptance criteria

- `AC-RTR-010`: Given an agent on combo `[a/x, b/y]` where `a` is down, when compaction runs for that
  agent, then compaction is answered by `b/y` and a call record with source `compaction` exists.

### REQ-RTR-008: Live changes

Saving a provider or combo from the dashboard shall take effect for the next LLM call without a
restart. Calls already streaming finish on the old settings.

#### Acceptance criteria

- `AC-RTR-011`: Given the daemon running, when the owner adds `[providers.oa] kind = "openai"` and
  switches an agent to `oa/gpt-5-mini` in the dashboard, then the next turn uses OpenAI and the response
  of the save reports `restart_required: false`.

### REQ-RTR-009: Call records

The router shall record one call record per attempt with: time, source (`turn`, `compaction`,
`memory_capture`, `knowledge`, `embedding`, `proxy`, `test`), agent, session, requested address, served
provider and model, attempt number, outcome (`ok` or the error class), input/output/cached tokens, cost
in USD when the price is known, time to first event, duration, and a redacted error message of at most
500 characters. Records older than 30 days are deleted.

#### Acceptance criteria

- `AC-RTR-012`: Given a turn answered by `deepseek/deepseek-flash` with 1,000 input (400 cached) and 200
  output tokens, when the Logs tab loads, then a row shows source `turn`, the agent, `deepseek/deepseek-flash`,
  `ok`, the three token counts, the cost from the catalog price, and the latency.
- `AC-RTR-013`: Given an error body that contains the API key, when the record is stored, then the key
  does not appear in the stored message.

### REQ-RTR-010: Prices and cost

Cost shall be computed per call record from per-model USD prices (input, cached input, output per
million tokens). Prices come from the built-in catalog, and for OpenRouter from its models API. When no
price is known, cost is empty and shown as "—", never as 0.

#### Acceptance criteria

- `AC-RTR-014`: Given a model without a known price, when its usage shows on the Overview, then cost
  shows "—" and the total cost line says "partial".

### REQ-RTR-011: Usage overview

The Router Overview shall show, for 24h, 7d, or 30d: total calls, error rate, input/output/cached
tokens, cost, median and p95 time to first event; a series of tokens and cost over time; and a table by
provider and by model.

#### Acceptance criteria

- `AC-RTR-015`: Given call records on two providers, when the owner selects 7d, then the totals equal
  the sum of records in the last 7 days and each provider row matches its own records.

### REQ-RTR-012: Limit snapshots

The router shall keep, per provider, the latest limits the provider reports:

- request and token limits, remaining, and reset time from rate-limit response headers (OpenAI and
  OpenRouter `x-ratelimit-*`, Anthropic `anthropic-ratelimit-*`), updated on every response;
- credit limit, usage, and remaining from OpenRouter's key endpoint;
- account balance from DeepSeek's balance endpoint.

Balances are fetched when the Router page asks and at most every 5 minutes; snapshots survive a restart.
A provider that reports nothing shows "Not reported by this provider". The Claude Agent plan windows
from spec 38 appear in the same list.

#### Acceptance criteria

- `AC-RTR-016`: Given an OpenAI response with `x-ratelimit-remaining-tokens: 149000` of `150000`, when
  the Overview loads, then the OpenAI card shows 149,000 of 150,000 tokens left and the reset time.
- `AC-RTR-017`: Given an OpenRouter key with limit $10 and usage $2.50, when the owner presses Refresh,
  then the card shows $7.50 of $10 left.

### REQ-RTR-013: Model list

The Models tab shall list, per provider, the built-in catalog models and the models the provider's
models endpoint returns (cached for 24 hours, refreshable), with context window, max output, vision,
thinking, prices when known, and which agents and combos use the model. Each model has a Test action.

#### Acceptance criteria

- `AC-RTR-018`: Given an `anthropic` provider with a valid key, when the owner presses Refresh on its
  model list, then the list contains the models from `GET /v1/models` and a Test on one returns ok with
  latency.

### REQ-RTR-014: Providers and combos in the dashboard

The owner shall add, edit, test, and delete providers and combos from the Router page. A provider or
combo in use by an agent or combo cannot be deleted; the error names the users. The API key is written
to `secrets.env` and never returned.

#### Acceptance criteria

- `AC-RTR-019`: Given combo `coding` used by agent `lana`, when the owner deletes it, then the delete is
  refused with "used by agents: lana".

### REQ-RTR-015: OpenAI-compatible proxy

When `[router] proxy = true` and the web server runs, the daemon shall serve `POST
/router/v1/chat/completions` (streaming and non-streaming, tools, images, `reasoning_effort`) and `GET
/router/v1/models` (every address and combo). The `model` field takes an address or a combo, so proxy
calls get fallback and cooldown. Requests need `Authorization: Bearer <proxy key>`.

#### Acceptance criteria

- `AC-RTR-020`: Given a proxy key and combo `coding`, when `curl` posts `{"model":"coding","stream":true,...}`,
  then an OpenAI-style SSE stream with `data: [DONE]` returns and a call record with source `proxy` exists.
- `AC-RTR-021`: Given no or a wrong bearer key, when a client calls the proxy, then it gets 401 with an
  OpenAI-style error body and nothing is called upstream.

### REQ-RTR-016: Proxy keys

The owner shall create named proxy keys (shown once), list them with name, prefix, created and last-used
time, and revoke them. Keys are stored only as a hash.

#### Acceptance criteria

- `AC-RTR-022`: Given a key was revoked, when a client uses it, then the proxy answers 401.

### REQ-RTR-017: Router as a Go library

The router package shall be usable from another Go module without lana's config loader, web, channels,
store, or agent packages: construct it from a Go struct, stream by address, plug a recorder and a
limit store, and mount the proxy as an `http.Handler`.

#### Acceptance criteria

- `AC-RTR-023`: Given `examples/router` builds a router from a struct with a mock kind, when it runs,
  then it streams a reply by combo name and its recorder receives one record; the archtest confirms the
  router packages import only `core`, `internal/*`, and the standard library.

### REQ-RTR-018: Agent model picker

The agent settings model picker shall offer combos and model addresses grouped by provider, and the
session model switch accepts the same values.

#### Acceptance criteria

- `AC-RTR-024`: Given combos and three providers, when the owner opens an agent's model picker, then
  combos are listed first, then models grouped by provider, and saving writes `model = "<value>"` with no
  `provider` key.

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Every target in a chain fails | The turn fails with the last error; each attempt is visible | last error class |
| API key env empty for a provider in use | Validation warning at boot; the target fails with `missing_secret` and the chain moves on | `missing_secret` |
| Provider models endpoint fails | The list shows catalog models and the error; no crash | HTTP 200 with `fetch_error` |
| Balance endpoint fails | Card keeps the last snapshot with its time and shows the error | — |
| Proxy disabled | `/router/v1/*` answers 404 | 404 |
| Proxy request names an unknown model | OpenAI-style 404 `model_not_found` | 404 |

## Authorization and security

- Dashboard endpoints use the existing session cookie and CSRF rules.
- The proxy uses only proxy keys (bearer), not the session cookie; it keeps the host check.
- API keys and proxy keys are never logged, returned, or stored in call records; error messages are
  redacted.
- Proxy keys are stored as SHA-256 hashes.

## Data integrity and concurrency

- Applying a new router config swaps an immutable snapshot atomically; a call keeps the snapshot it
  started with.
- Call records are written off the stream path; a failed write never fails a call.

## Accessibility

- Router tabs are keyboard reachable; status (ok, error, cooling down) is shown with text, not only
  color.

## Scenarios

### SC-001: Provider down, combo saves the turn

**Given** agent `lana` uses combo `coding = [anthropic/claude-sonnet-5-5, deepseek/deepseek-flash]` and
Anthropic returns 529 three times
**When** the owner sends a message
**Then** the reply comes from DeepSeek, the Logs tab shows three failed Anthropic attempts and one ok
DeepSeek call, and Anthropic is not cooling down (529 is `server`).

## Blocking Open Questions

None. Resolutions:

| ID | Question | Resolution |
|---|---|---|
| `Q-001` | Library, proxy, or both? | Owner: both. |
| `Q-002` | How do agents pick a model? | Owner: `provider/model` or a combo. |
| `Q-003` | Anthropic native or OpenAI-compat? | Owner: native. |
| `Q-004` | Router page contents | Owner: providers & keys, models, combos, usage & request log; plus usage and limits. |
| `Q-005` | Where do providers live in config? | Rekomendasi agent: keep `[providers.<name>]`, add kinds; add `[combos.<name>]` and `[router]`. No migration needed. |
| `Q-006` | Proxy path, given `/v1/models` is taken | Rekomendasi agent: `/router/v1/...`; clients use base URL `http://<addr>/router/v1`. |
| `Q-007` | Proxy on by default? | Rekomendasi agent: off; enabled from the Proxy tab. |
| `Q-008` | What does "limit" mean? | Rekomendasi agent: provider-reported limits only; owner-set budgets are a follow-up. |
| `Q-009` | Fallback on auth errors? | Rekomendasi agent: yes, move on at once (the old loop stopped). Client and context errors still stop. |
| `Q-010` | Call record retention | Rekomendasi agent: 30 days. |

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-RTR-001` | `AC-RTR-001` | owner request |
| `REQ-RTR-002` | `AC-RTR-002`, `AC-RTR-003` | Q-003 |
| `REQ-RTR-003` | `AC-RTR-004`, `AC-RTR-005` | Q-002 |
| `REQ-RTR-004` | `AC-RTR-006` | Q-002 |
| `REQ-RTR-005` | `AC-RTR-007`, `AC-RTR-008` | 9router-go, Q-009 |
| `REQ-RTR-006` | `AC-RTR-009` | 9router-go |
| `REQ-RTR-007` | `AC-RTR-010` | owner request |
| `REQ-RTR-008` | `AC-RTR-011` | Q-005 |
| `REQ-RTR-009` | `AC-RTR-012`, `AC-RTR-013` | Q-004, Q-010 |
| `REQ-RTR-010` | `AC-RTR-014` | Q-004 |
| `REQ-RTR-011` | `AC-RTR-015` | owner request |
| `REQ-RTR-012` | `AC-RTR-016`, `AC-RTR-017` | Q-008 |
| `REQ-RTR-013` | `AC-RTR-018` | Q-004 |
| `REQ-RTR-014` | `AC-RTR-019` | Q-004 |
| `REQ-RTR-015` | `AC-RTR-020`, `AC-RTR-021` | Q-001, Q-006, Q-007 |
| `REQ-RTR-016` | `AC-RTR-022` | Q-001 |
| `REQ-RTR-017` | `AC-RTR-023` | Q-001, ADR 0015 |
| `REQ-RTR-018` | `AC-RTR-024` | Q-002 |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit.
- [x] Defaults, ordering, retry, and idempotency are explicit.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: ridho (interview) + agent recommendations on the owner's instruction
- **Approved on**: 2026-11-08
