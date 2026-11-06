# 0015. Make lana an embeddable runtime for services

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0005](0005-sqlite-as-single-storage.md), [0009](0009-session-as-append-only-event-log.md), [0013](0013-use-go-as-single-binary.md), [0014](0014-integration-as-agentic-tool.md)

## Context

ADR 0014 provides a Go library as one of the integration surfaces. The owner wants to use
`lana` as the foundation for services such as `sxored-be-scrapper-engine`: job-based services that run an agent per task
with domain tools (browser, parser, database), run across several replicas on Kubernetes, and use Postgres.

The current plan is still shaped for a personal harness:

- Storage is only a single-writer SQLite file, and session locks use the PID and hostname.
- Agents are defined only through TOML.
- There is no cost or time limit per run.
- Observability is only `slog`.
- Nothing prevents library packages from pulling in TUI, Telegram, or web dependencies.

Load considerations: in embedded mode there is no separate lana service. The agent loop runs inside the service
process, and most of its time is spent waiting for the LLM provider. Heavy computation happens in the service's own
domain tools, and it scales with the service's replicas. The central lana daemon still exists for personal use and MCP.

## Decision

**1. Two roles, two repositories.**

| Role | Location | Contents |
|---|---|---|
| Agent runtime SDK | This repo (`lana-harness`) | Packages `lana`, `core`, `agent`, `providers/*`, `tools`, `store/*`, plus the `lana` binary for personal use |
| Service template | Separate repo, based on the owner's Go service framework | HTTP, Postgres, job queue, deployment, example domain tools. Imports `lana` |

This repo does not become a service boilerplate. The service template is created in its own repo after the embeddable
runtime is finished.

**2. Library packages stay lean.** The packages `lana`, `core`, `agent`, `providers/*`, `tools`,
`store/*`, and `observability/*` must not import `channels/`, `web/`, or `protocol/`.
This rule is tested by the same import-rule test as spec #2.

**3. The store is swappable.** `lana.Options.Store` accepts any implementation of `core.SessionStore`:

| Store | Package | Used for |
|---|---|---|
| SQLite | `store/sqlite` | The `lana` binary, a single machine (ADR 0005) |
| Postgres | `store/postgres` | Services with many replicas |
| Memory | `store/memstore` | Temporary jobs and tests, no persistence |

`store/postgres` follows the semantics of ADR 0009: the `session_events` table is append-only (triggers reject
`UPDATE` and `DELETE`), the `sessions` projection, and forks without copying. Session locks use
`pg_try_advisory_lock` on the connection held while the session is open, so the lock is released automatically
when the process or connection dies. The PID and hostname check is not used on Postgres.
The Postgres schema name can be configured so lana's tables do not mix with the service's tables.

**4. Agents can be defined in code.**

```go
rt, err := lana.New(ctx, lana.Options{
    Store: pgstore,
    Agents: []lana.Agent{{
        Name: "scraper", Provider: "openai", Model: "gpt-5-mini",
        Prompt: scraperPrompt, Tools: []core.Tool{browserTool, parseTool},
        Permission: core.PolicyNever, MaxSteps: 40,
    }},
})
```

Definitions in code are mapped to an in-memory config layer named `code` (REQ-CONF-009 spec #2), so validation,
origin maps, and merge rules stay the same. Tools known only in code may be referenced by agents in the `code` layer.

**5. Budget per run.**

```go
res, err := rt.Run(ctx, lana.Request{
    Agent: "scraper", Prompt: p,
    Budget: lana.Budget{MaxTokens: 200_000, MaxDuration: 3 * time.Minute, MaxSteps: 40},
})
```

When the budget is exhausted, the turn stops with the `turn/end` reason `budget` (a new value alongside
`end_turn`, `max_steps`, `cancelled`, `interrupted`, `error`). Token checks use the usage from
`step/end`.

**6. Concurrency per process.** One `lana.Runtime` per process, safe to use from many goroutines.
`Options.MaxConcurrentRuns` limits how many runs execute at the same time. Runs that exceed the limit wait
until a slot is free or `ctx` is cancelled.

**7. Optional OpenTelemetry.** The package `observability/otel` attaches listeners to the `agent.*`,
`llm.stream`, and `tools.*` hooks to create spans per turn, step, and tool call, as well as token and duration
metrics. It is enabled through `lana.Options{Tracer, Meter}`. Without that option, no OpenTelemetry dependency is linked in.

**8. Deploy modes.**

| Mode | Where the load runs | Suitable for |
|---|---|---|
| Embedded in a service | The service process, scales with replicas | Service jobs with domain tools |
| `lana` daemon or `lana mcp` | One lana process | Personal use, Claude Code via MCP, lightweight shared tools |

## Consequences

**Positive**
- A new service that needs an agent does not copy the agent loop; it only writes domain tools and calls `rt.Run`.
- There is no central lana service that becomes a bottleneck for job load.
- The event log is still used for audit and debugging in services, now on Postgres.

**Negative / tradeoffs**
- Two persistent store implementations (SQLite and Postgres) must be tested with the same contract tests.
- The public API of package `lana` grows (`Agent`, `Budget`, `MaxConcurrentRuns`, stores), so more must be kept stable.
- TypeScript services (such as the scraper engine today) cannot embed the Go library; they use MCP or REST.

**Neutral**
- The service template is built in a separate repo and is not part of this repo's roadmap.
- The Postgres driver is chosen in the design spec that introduces it (candidate: `github.com/jackc/pgx/v5`).

## Alternatives considered

- **This repo as the service boilerplate** — one repo. Rejected because the personal harness and services have different needs, and every service would carry unused code.
- **A central lana daemon for all services** — one place to operate. Rejected for job load because it becomes a bottleneck and domain tools would have to travel back and forth over the network.
- **Postgres lease with a table and heartbeat** — does not depend on the connection. Rejected because advisory locks are simpler and are released automatically when the process dies.
