# Architecture Decision Records

This directory holds the Architecture Decision Records (ADRs) for this service template. Each ADR
captures one durable architectural decision: the problem it solves, the option chosen, and the
tradeoffs accepted.

## Purpose

ADRs are the **immutable historical record** — they explain *why* the spec is shaped the way it is. When the PRD changes a decision, the old ADR is marked `Superseded` and a new ADR is written; ADRs are never edited after they reach `Accepted` status.

If you are a new engineer, read the ADR index below before diving into code or a large PRD. The ADRs are short on purpose.

## Index

| #    | Title                                                                                     | Status   | Date       |
|------|-------------------------------------------------------------------------------------------|----------|------------|
| [0001](0001-use-rust-single-binary-daemon.md) | Use Rust as a single-binary daemon | Superseded by 0013 | 2026-10-29 |
| [0002](0002-core-from-modules-services-and-hooks.md) | Build the core from modules, services, and hooks in a Cargo workspace | Accepted | 2026-10-29 |
| [0003](0003-multi-agent-with-channel-routing.md) | Run lightweight multi-agent with routing from channels | Accepted | 2026-10-29 |
| [0004](0004-permission-mode-per-agent.md) | Set permissions per agent and decide approvals through hooks | Accepted | 2026-10-29 |
| [0005](0005-sqlite-as-single-storage.md) | Use SQLite as the single storage | Accepted | 2026-10-29 |
| [0006](0006-extend-via-mcp-client-and-skills.md) | Add capabilities through an MCP client and Skills | Accepted | 2026-10-29 |
| [0007](0007-cron-scheduler-inside-daemon.md) | Run the cron scheduler inside the daemon | Accepted | 2026-10-29 |
| [0008](0008-cli-channel-http-and-web-dashboard-interfaces.md) | Provide CLI, chat channels, HTTP API, and web dashboard interfaces | Accepted | 2026-10-29 |
| [0009](0009-session-as-append-only-event-log.md) | Store sessions as an append-only event log in SQLite | Accepted | 2026-10-29 |
| [0010](0010-profile-and-config-patch.md) | Compose the runtime with profiles and config patches | Accepted | 2026-10-29 |
| [0011](0011-subagent-as-child-session.md) | Run subagents as child sessions | Accepted | 2026-10-29 |
| [0012](0012-acp-and-sdk-json-rpc.md) | Provide an ACP server and an SDK JSON-RPC server | Accepted | 2026-10-29 |
| [0013](0013-use-go-as-single-binary.md) | Use Go as a single binary and library | Accepted | 2026-10-29 |
| [0014](0014-integration-as-agentic-tool.md) | Provide lana as an agentic tool for other services | Accepted | 2026-10-29 |
| [0015](0015-embeddable-runtime-for-services.md) | Make lana an embeddable runtime for services | Accepted | 2026-10-29 |
| [0016](0016-owner-auth-for-browser.md) | Authenticate the browser with an owner token and a session cookie | Accepted | 2026-10-30 |
| [0017](0017-agent-teams-as-roster-over-subagents.md) | Model agent teams as a roster of agents over subagent child sessions | Accepted | 2026-10-30 |
| [0018](0018-claude-code-as-external-agent-runtime.md) | Run Claude Code as an external agent runtime over headless stream-json | Accepted | 2026-11-08 |
| [0019](0019-llm-router-as-embeddable-core.md) | Route all LLM calls through an embeddable router package | Accepted | 2026-11-08 |
| [0020](0020-claude-code-as-router-target-with-lana-context.md) | Offer Claude Code as a router target that runs with lana's context and tools | Accepted | 2026-11-08 |

## How to add an ADR

1. Copy [`0000-adr-template.md`](0000-adr-template.md).
2. Rename to `NNNN-kebab-case-title.md`, where `NNNN` is the next free 4-digit number.
3. Fill in Context, Decision, Consequences, Alternatives. Aim for 50–150 lines.
4. Set `Status: Proposed` while it is under review. Flip to `Accepted` when merged.
5. Add a row to the index table above.
6. If this ADR replaces an older one, set the old ADR's status to `Superseded by NNNN` and link both ways.

## Status lifecycle

```
Proposed  ──► Accepted ──► Deprecated
              │
              └─────────► Superseded by NNNN
```

- **Proposed** — under review, not yet binding.
- **Accepted** — the decision is in effect. ADR is immutable from this point.
- **Deprecated** — the decision is no longer recommended but no replacement exists yet.
- **Superseded by NNNN** — a new ADR replaces this one. Both records remain in the repo.

## Conventions

- Filenames: `NNNN-kebab-case-title.md`, zero-padded to 4 digits.
- One decision per ADR. If a decision splits naturally into two, write two ADRs.
- No edits to `Accepted` ADRs except metadata flips (status, links). Substantive changes require a new ADR.
- Cite the PRD section that codifies the decision.
- Cross-link related ADRs in the header.
