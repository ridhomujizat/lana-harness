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
| [0001](0001-gunakan-rust-single-binary-daemon.md) | Gunakan Rust sebagai single binary daemon | Superseded by 0013 | 2026-10-29 |
| [0002](0002-core-dari-modul-service-dan-hook.md) | Bangun core dari modul, service, dan hook dalam Cargo workspace | Accepted | 2026-10-29 |
| [0003](0003-multi-agent-dengan-routing-channel.md) | Jalankan multi-agent ringan dengan routing dari channel | Accepted | 2026-10-29 |
| [0004](0004-permission-mode-per-agent.md) | Atur permission per agent dan putuskan approval lewat hook | Accepted | 2026-10-29 |
| [0005](0005-sqlite-sebagai-storage-tunggal.md) | Pakai SQLite sebagai storage tunggal | Accepted | 2026-10-29 |
| [0006](0006-ekstensi-lewat-mcp-client-dan-skills.md) | Tambah kemampuan lewat MCP client dan Skills | Accepted | 2026-10-29 |
| [0007](0007-scheduler-cron-di-dalam-daemon.md) | Jalankan scheduler cron di dalam daemon | Accepted | 2026-10-29 |
| [0008](0008-interface-cli-channel-http-dan-web-dashboard.md) | Sediakan interface CLI, channel chat, HTTP API, dan web dashboard | Accepted | 2026-10-29 |
| [0009](0009-session-sebagai-event-log-append-only.md) | Simpan session sebagai event log append-only di SQLite | Accepted | 2026-10-29 |
| [0010](0010-profile-dan-config-patch.md) | Susun runtime dengan profile dan config patch | Accepted | 2026-10-29 |
| [0011](0011-subagent-sebagai-child-session.md) | Jalankan subagent sebagai child session | Accepted | 2026-10-29 |
| [0012](0012-acp-dan-sdk-json-rpc.md) | Sediakan server ACP dan SDK JSON-RPC | Accepted | 2026-10-29 |
| [0013](0013-gunakan-go-sebagai-single-binary.md) | Gunakan Go sebagai single binary dan library | Accepted | 2026-10-29 |
| [0014](0014-integrasi-sebagai-agentic-tool.md) | Sediakan lana sebagai agentic tool untuk service lain | Accepted | 2026-10-29 |
| [0015](0015-runtime-embeddable-untuk-service.md) | Jadikan lana runtime yang bisa di-embed di service | Accepted | 2026-10-29 |

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
