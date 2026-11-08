# Contract: LLM router

- **Issue**: — (owner request 2026-11-08)
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 2
- **Applies to**: config / Go library / HTTP
- **Status**: Approved (agent recommendation on the owner's instruction, 2026-11-08)

## Shared conventions

| Topic | Decision or authority |
|---|---|
| Config format | `config.toml`, tables below; unknown keys are validation errors as today |
| Authentication | Dashboard: session cookie + `X-Lana-CSRF` (ADR 0016). Proxy: `Authorization: Bearer <proxy key>` |
| Serialization | JSON, RFC 3339 UTC times, money as float USD, token counts as integers |
| Versioning | Config writes use `expected_version` like the existing provider endpoints |

## Operation index

| Operation ID | Type | Command/method | Path/key | Requirements | Acceptance criteria |
|---|---|---|---|---|---|
| `API-RTR-001` | config | `[providers.<name>]` | `kind` and defaults | `REQ-RTR-001`, `REQ-RTR-002` | `AC-RTR-001` |
| `API-RTR-002` | config | `[combos.<name>]` | `models`, `description` | `REQ-RTR-004` | `AC-RTR-006` |
| `API-RTR-003` | config | `[agents.<name>]` | `model` address or combo | `REQ-RTR-003` | `AC-RTR-004`, `AC-RTR-005` |
| `API-RTR-004` | config | `[router]` | `proxy`, `cooldown` | `REQ-RTR-006`, `REQ-RTR-015` | `AC-RTR-009`, `AC-RTR-020` |
| `API-RTR-005` | Go | package `router` | `New`, `Stream`, `Apply`, ... | `REQ-RTR-017` | `AC-RTR-023` |
| `API-RTR-006` | HTTP | `GET/PUT/DELETE /v1/providers...` | kinds extended, live | `REQ-RTR-008`, `REQ-RTR-014` | `AC-RTR-011` |
| `API-RTR-007` | HTTP | `PUT/DELETE /v1/router/combos/{name}` | combos | `REQ-RTR-014` | `AC-RTR-019` |
| `API-RTR-008` | HTTP | `GET /v1/router` | overview of config | `REQ-RTR-014`, `REQ-RTR-018` | `AC-RTR-024` |
| `API-RTR-009` | HTTP | `GET /v1/router/models` | model list | `REQ-RTR-013` | `AC-RTR-018` |
| `API-RTR-010` | HTTP | `POST /v1/router/test` | test one address | `REQ-RTR-013` | `AC-RTR-018` |
| `API-RTR-011` | HTTP | `GET /v1/router/usage` | usage aggregate | `REQ-RTR-010`, `REQ-RTR-011` | `AC-RTR-014`, `AC-RTR-015` |
| `API-RTR-012` | HTTP | `GET /v1/router/calls` | request log | `REQ-RTR-009` | `AC-RTR-012`, `AC-RTR-013` |
| `API-RTR-013` | HTTP | `GET /v1/router/limits` | limit snapshots | `REQ-RTR-012` | `AC-RTR-016`, `AC-RTR-017` |
| `API-RTR-014` | HTTP | `/v1/router/proxy`, `/v1/router/keys` | proxy switch and keys | `REQ-RTR-015`, `REQ-RTR-016` | `AC-RTR-022` |
| `API-RTR-015` | HTTP | `POST /router/v1/chat/completions` | proxy | `REQ-RTR-015` | `AC-RTR-020`, `AC-RTR-021` |
| `API-RTR-016` | HTTP | `GET /router/v1/models` | proxy | `REQ-RTR-015` | `AC-RTR-020` |
| `API-RTR-017` | UI route | `#/router/<tab>` | Router page | `REQ-RTR-011`..`018` | `AC-RTR-015` |

## Operations

### API-RTR-001: `[providers.<name>]`

| Key | Type | Default | Rule |
|---|---|---|---|
| `kind` | string | — (required) | `openai`, `deepseek`, `openrouter`, `anthropic`, `openai_compat` |
| `base_url` | string | per kind: `https://api.openai.com/v1`, `https://api.deepseek.com/v1`, `https://openrouter.ai/api/v1`, `https://api.anthropic.com` | required for `openai_compat`; `http(s)` URL |
| `api_key_env` | string | `<NAME>_API_KEY` upper-cased, `-` → `_` | env name; value from env, then `secrets.env` |
| `headers`, `timeout`, `stream_idle_timeout`, `thinking_format` | unchanged | | `thinking_format` applies to OpenAI-wire kinds only |

Existing `kind = "openai_compat"` entries keep working unchanged. `thinking_format` defaults to
`deepseek` for kind `deepseek` as well as for the `api.deepseek.com` host.

**Revision 2 (2026-11-08, owner request: 9router-style Providers page).** New key:

| Key | Type | Default | Rule |
|---|---|---|---|
| `disabled` | boolean | `false` | `true` keeps the entry but takes it out of routing: a chain target on it fails with `not_found` (no call is made) and the chain moves on; it is left out of `GET /router/v1/models`, the agent model choices of `GET /v1/router`, and the boot key check. Combos and agents may still name it. |

### API-RTR-002: `[combos.<name>]`

```toml
[combos.coding]
description = "Claude first, DeepSeek when it is down"
models = ["anthropic/claude-sonnet-5-5", "openrouter/openai/gpt-5-mini", "deepseek/deepseek-flash"]
```

Name: `[a-z0-9][a-z0-9_-]{0,63}`, not equal to a provider name. `models`: 1–10 entries, each
`<provider>/<model>` with a known provider; duplicates rejected.

### API-RTR-003: `[agents.<name>] model`

- `provider` empty: `model` is a combo name or a model address. Required.
- `provider` set: unchanged legacy form; `model` is the raw model ID.
- `fallback` entries: `provider:model` or `provider/model` (address), or a combo name is **not** allowed
  in `fallback`.
- Session model switch (`model/selection`) accepts a combo name, an address, or `provider:model`.

### API-RTR-004: `[router]`

| Key | Type | Default | Rule |
|---|---|---|---|
| `proxy` | bool | `false` | serves `/router/v1/*` when true and `[web] enabled` |
| `cooldown` | duration string | `"5m"` | `0s` turns cooldown off; max `1h` |

### API-RTR-005: Go package `github.com/ridhomujizat/lana-harness/router`

```go
type Config struct {
    Providers map[string]core.ProviderConfig
    Combos    map[string]Combo
    Cooldown  time.Duration // 0 = default 5m; negative = off
}
type Combo struct { Description string; Models []string }
type Target struct { Provider, Model string }

type CallMeta struct { Source, Agent, SessionID, TurnID string }
type Attempt struct { Target Target; N int; Err *core.ProviderError } // N is 1-based

type Option func(*options)
func WithSecrets(core.Secrets) Option
func WithRecorder(Recorder) Option
func WithLimitStore(LimitStore) Option
func WithHTTPClient(*http.Client) Option
func WithClock(func() time.Time) Option
func WithSleep(func(context.Context, time.Duration) error) Option // tests

func New(cfg Config, opts ...Option) (*Router, error)
func (r *Router) Apply(cfg Config) error                        // atomic swap
func (r *Router) Resolve(address string) ([]Target, error)       // combo or address
func (r *Router) Stream(ctx context.Context, address string, req core.ChatRequest, meta CallMeta) (core.ChatStream, error)
func (r *Router) StreamChain(ctx context.Context, chain []Target, req core.ChatRequest, meta CallMeta, onAttempt func(Attempt)) (core.ChatStream, Target, error)
func (r *Router) Embed(ctx context.Context, address string, inputs []string, meta CallMeta) ([][]float32, error)
func (r *Router) Provider(name string) (core.Provider, bool)    // core.LLMRegistry
func (r *Router) Names() []string                                // core.LLMRegistry
func (r *Router) Models(ctx context.Context, provider string, refresh bool) ([]ModelInfo, error)
func (r *Router) Limits(ctx context.Context, refresh bool) []LimitSnapshot
func (r *Router) Cooling() map[Target]time.Time

// Kinds: adapters register themselves from their init().
type Factory func(name string, cfg core.ProviderConfig, deps Deps) (Adapter, error)
func RegisterKind(kind string, f Factory)
type Adapter interface {
    core.Provider
    ListModels(ctx context.Context) ([]ModelInfo, error)  // may return ErrNotSupported
}
// Optional adapter interfaces: core.Embedder, BalanceFetcher.
type Deps struct { Secrets core.Secrets; HTTP *http.Client; Limits func(LimitUpdate) }

type Recorder interface { Record(CallRecord) }      // must not block
type LimitStore interface { Load() []LimitSnapshot; Save([]LimitSnapshot) }

// Proxy, in package router/openaiproxy:
func NewHandler(r *router.Router, auth func(bearer string) (keyID string, ok bool)) http.Handler
```

`ModelInfo`, `CallRecord`, `LimitSnapshot`, `LimitUpdate` field names match the JSON shapes below.

### API-RTR-006: provider endpoints (extended)

The existing `GET /v1/providers`, `POST /v1/providers/test`, `PUT /v1/providers/{name}`, and
`DELETE /v1/providers/{name}` keep their shapes, with:

- `kind` accepts the five kinds; `base_url` may be omitted for preset kinds.
- `PUT` and `DELETE` apply live: the response `restart_required` is `false` unless another pending
  change needs a restart.
- `DELETE` is refused with 409 `{"error":"in_use","message":"used by agents: a; combos: c"}` when used
  by an agent, a fallback, a combo, or `knowledge.embedding_provider`.
- `GET` items gain `default_base_url` and `models_count`.

**Revision 2 (2026-11-08).** `GET` items gain `disabled` (boolean). `PUT` accepts an optional
boolean `disabled`; when the key is absent the stored value is kept, so an edit that does not send
it never switches a provider back on. It applies live like the other keys. `GET /v1/router`
provider rows gain `disabled` too. When a `PUT` with `api_key` tests the key, a base URL host of a
dashboard preset (`generativelanguage.googleapis.com`, `api.x.ai`, `api.groq.com`,
`api.mistral.ai`, `api.together.xyz`, `api.fireworks.ai`, `api.cerebras.ai`) supplies a known test
model; with no known model the test model is a guess, and a 404 for that guess saves the key (the
server accepted it).

### API-RTR-007: combos

`PUT /v1/router/combos/{name}` body `{"description":"","models":["a/x"],"expected_version":"..."}` →
200 `{"version":"...","restart_required":false}`; 422 `{"error":"invalid","issues":[{"path","message"}]}`;
409 `version_conflict`. `DELETE /v1/router/combos/{name}` body `{"expected_version"}` → 200, 404, or
409 `in_use` as above.

### API-RTR-008: `GET /v1/router`

```json
{
  "version": "…",
  "providers": [{"name":"deepseek","kind":"deepseek","base_url":"https://api.deepseek.com/v1","key_state":"set","used_by":["lana"]}],
  "combos": [{"name":"coding","description":"","models":["anthropic/claude-sonnet-5-5"],"used_by":["lana"]}],
  "cooling": [{"target":"anthropic/claude-sonnet-5-5","until":"2026-11-08T10:05:00Z"}],
  "proxy": {"enabled": false, "base_url": "http://127.0.0.1:18300/router/v1"},
  "choices": [{"value":"coding","label":"coding","group":"Combos"},{"value":"deepseek/deepseek-flash","label":"DeepSeek Flash","group":"deepseek"}]
}
```

### API-RTR-009: `GET /v1/router/models?provider=<name>&refresh=1`

```json
{"provider":"anthropic","fetched_at":"…","fetch_error":"","models":[
  {"id":"claude-sonnet-5-5","address":"anthropic/claude-sonnet-5-5","name":"Claude Sonnet 5.5",
   "context_window":200000,"max_output":64000,"vision":true,"thinking":true,
   "price":{"input":3,"cached_input":0.3,"output":15},"source":"catalog|provider|both",
   "used_by":{"agents":["lana"],"combos":["coding"]}}]}
```

`price` is `null` when unknown. Without `provider`, all providers are returned as `{"providers":[…]}`.

### API-RTR-010: `POST /v1/router/test`

Body `{"address":"anthropic/claude-sonnet-5-5"}` → `{"ok":true,"latency_ms":812}` or
`{"ok":false,"class":"auth","status":401,"message":"…"}`. Sends one "ping" with `max_tokens` 1, 15 s
timeout, records source `test`.

### API-RTR-011: `GET /v1/router/usage?range=24h|7d|30d`

```json
{"range":"7d","totals":{"calls":120,"errors":3,"input_tokens":1,"output_tokens":1,"cached_tokens":1,
  "cost_usd":1.23,"cost_partial":false,"ttft_p50_ms":900,"ttft_p95_ms":2400},
 "series":[{"t":"2026-11-08T00:00:00Z","input_tokens":1,"output_tokens":1,"cost_usd":0.1,"calls":3,"errors":0}],
 "by_provider":[{"provider":"deepseek","calls":1,"errors":0,"input_tokens":1,"output_tokens":1,"cached_tokens":1,"cost_usd":0.1,"cost_partial":false,"ttft_p50_ms":1}],
 "by_model":[{"address":"deepseek/deepseek-flash", "…": "same fields"}],
 "by_source":[{"source":"turn","calls":1,"cost_usd":0.1}]}
```

Series buckets: 1 hour for 24h, 1 day otherwise.

### API-RTR-012: `GET /v1/router/calls?limit=50&before=<id>&provider=&status=ok|error&source=`

```json
{"calls":[{"id":123,"at":"…","source":"turn","agent":"lana","session_id":"…","requested":"coding",
  "provider":"deepseek","model":"deepseek-flash","attempt":1,"outcome":"ok","input_tokens":1,
  "output_tokens":1,"cached_tokens":1,"cost_usd":0.001,"ttft_ms":900,"duration_ms":3000,"error":""}],
 "next_before":122}
```

`limit` max 200. Newest first.

### API-RTR-013: `GET /v1/router/limits?refresh=1`

```json
{"limits":[{"provider":"openai","kind":"openai","updated_at":"…","error":"",
  "rate":{"requests":{"limit":500,"remaining":499,"reset_at":"…"},"tokens":{"limit":150000,"remaining":149000,"reset_at":"…"},
          "input_tokens":null,"output_tokens":null},
  "credit":null,"balance":null,"windows":[]},
 {"provider":"openrouter","credit":{"limit_usd":10,"used_usd":2.5,"remaining_usd":7.5}},
 {"provider":"deepseek","balance":{"currency":"USD","total":4.2,"granted":0,"topped_up":4.2}},
 {"provider":"claude-agent","kind":"claude-code","windows":[{"type":"five_hour","status":"allowed","utilization":0.42,"resets_at":"…"}]}]}
```

`refresh=1` fetches balances/credits now (still at most once per 5 minutes per provider).

### API-RTR-014: proxy switch and keys

- `PUT /v1/router/proxy` body `{"enabled":true,"expected_version"}` → writes `[router] proxy`, applies live.
- `GET /v1/router/keys` → `{"keys":[{"id":"k_…","name":"cursor","prefix":"lrk_ab12","created_at","last_used_at"}]}`
- `POST /v1/router/keys` body `{"name":"cursor"}` → 201 `{"id","name","prefix","key":"lrk_<43 base62>"}`
  (the key appears only here).
- `DELETE /v1/router/keys/{id}` → 204.

### API-RTR-015: `POST /router/v1/chat/completions`

OpenAI Chat Completions request subset: `model`, `messages` (`system|developer|user|assistant|tool`,
string or content parts `text|image_url` data URLs), `tools` (function), `tool_choice` ignored,
`max_tokens`/`max_completion_tokens`, `temperature`, `reasoning_effort` (`none|minimal|low|medium|high|xhigh`
→ off/low/low/high/high/max), `stream`, `stream_options.include_usage`.

- Streaming: `chat.completion.chunk` SSE with `delta.content`, `delta.reasoning_content`,
  `delta.tool_calls`, final chunk with `finish_reason` and `usage`, then `data: [DONE]`.
- Non-streaming: one `chat.completion` object.
- Response header `X-Lana-Served-By: <provider>/<model>`.
- Errors: `{"error":{"message","type","code"}}`; 401 `invalid_api_key`, 404 `model_not_found`,
  400 `invalid_request_error`, upstream classes map to 429/502/504 with the class in `code`.

### API-RTR-016: `GET /router/v1/models`

`{"object":"list","data":[{"id":"coding","object":"model","owned_by":"combo"},{"id":"deepseek/deepseek-flash","object":"model","owned_by":"deepseek"}]}`

### API-RTR-017: UI routes

`#/router` (Overview), `#/router/providers`, `#/router/models`, `#/router/combos`, `#/router/logs`,
`#/router/proxy`. `#/settings/providers` redirects to `#/router/providers`. Sidebar item
`{key:"router", label:"Router", icon:"route"}` in the Configure group, before Agents.

**Revision 2 (2026-11-08).** The tabs became their own pages in a sidebar group "Router", between
Observe and Configure: Providers `#/router/providers` (the default for `#/router`), Combos
`#/router/combos`, Usage `#/router/usage`, Quota `#/router/quota`, Logs `#/router/logs`, Endpoint
`#/router/endpoint`. One provider's page is `#/router/providers/<preset id or provider name>`
(`claude-code` for Claude Code). Old hashes redirect: `#/router/overview` → `#/router/usage`,
`#/router/proxy` → `#/router/endpoint`, `#/router/models` → `#/router/providers`.
