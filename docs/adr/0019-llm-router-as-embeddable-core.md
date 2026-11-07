# 0019. Route all LLM calls through an embeddable router package

- **Status**: Accepted
- **Date**: 2026-11-08
- **Deciders**: Ridho
- **Related ADRs**: [0013](0013-use-go-as-single-binary.md), [0015](0015-embeddable-runtime-for-services.md), [0018](0018-claude-code-as-external-agent-runtime.md)

## Context

Providers were a flat map of `openai_compat` adapters built once at boot. Each agent named one provider,
a model, and a `provider:model` fallback list. The agent's retry hook moved to the next target only after
retryable errors. Background passes (compaction, memory capture, knowledge) called a provider directly
with no fallback. Nothing recorded per-call usage, cost, latency, or provider limits, and provider
changes needed a restart.

The owner wants one place to manage providers, keys, models, and fallback chains, like
[9router-go](https://github.com/luqman-v1/9router-go). The same router must work inside other Go services
(ADR 0015) and as an OpenAI-compatible endpoint for other tools.

## Decision

1. **A library package `router`** owns provider kinds, `provider/model` addressing (split on the first
   `/`), combos (named ordered chains), retry and fallback, cooldown, call records, cost, limit
   snapshots, and model lists. It imports only `core`, `internal/*`, and the standard library.
2. **Adapters register kinds** with `router.RegisterKind` from `init()`. `providers/*` may import
   `router`. The first kinds are `openai`, `deepseek`, `openrouter`, `openai_compat`
   (`providers/openai`), and `anthropic` (`providers/anthropic`, native Messages API).
3. **`core.LLMRouter`** is the interface the agent uses. The router owns retry and fallback; the agent's
   retry hook is removed. Every lana LLM call goes through the router.
4. **Persistence is pluggable.** The router reports through `Recorder` and `LimitStore`. lana stores
   them in SQLite; another service can store them anywhere.
5. **The proxy is a handler** (`router/openaiproxy`) that any service can mount. lana mounts it at
   `/router/v1/` on the web server, off by default, behind proxy keys.
6. **Config stays in `[providers]`**, plus `[combos]` and `[router]`. The legacy agent form keeps working.

## Consequences

**Positive**
- One retry/fallback policy for turns, background passes, the proxy, and embedders.
- Usage, cost, and limits become visible without parsing session events.
- Live provider changes.

**Negative**
- The router is a second place (with the agent) where model metadata is resolved; the catalog in
  `internal/models` stays the single source of model facts.
- Catalog prices need upkeep.

## Alternatives considered

- **Run 9router-go as a sidecar.** Rejected: a second process and database, no in-process use for other
  Go services, and it centres on OAuth subscription accounts that ADR 0018 rules out.
- **Keep fallback in the agent hook.** Rejected: the proxy and library users would need their own copy.
