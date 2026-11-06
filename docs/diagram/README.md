# Diagram Index

This directory holds architecture and workflow diagrams for the service template. Each
diagram should explain one durable structure or flow that is easier to understand visually
than in prose.

## Purpose

Diagrams are the **visual companion** to the architecture docs — they explain *how* major
parts relate, move, or depend on each other. Keep them general, implementation-focused, and
free of product-specific naming unless the diagram is documenting a concrete example domain.

If you are a new engineer, scan this index after reading the top-level README and ADRs. The
diagrams should help you understand boundaries, request paths, dependency direction, and
runtime topology quickly.

## Index

| #  | Title | ADR |
|----|-------|-----|
| [0001](0001-system-context-and-roadmap.md) | System Context and Roadmap | [0013](../adr/0013-use-go-as-single-binary.md), [0014](../adr/0014-integration-as-agentic-tool.md), [0002](../adr/0002-core-from-modules-services-and-hooks.md), [0008](../adr/0008-cli-channel-http-and-web-dashboard-interfaces.md) |
| [0002](0002-agent-loop-and-approval.md) | Agent Loop and Approval | [0002](../adr/0002-core-from-modules-services-and-hooks.md), [0004](../adr/0004-permission-mode-per-agent.md), [0009](../adr/0009-session-as-append-only-event-log.md) |
| [0003](0003-data-model-and-scheduler.md) | Data Model and Scheduler | [0005](../adr/0005-sqlite-as-single-storage.md), [0007](../adr/0007-cron-scheduler-inside-daemon.md), [0009](../adr/0009-session-as-append-only-event-log.md) |
| [0004](0004-profile-fork-subagent-and-protocols.md) | Profile, Fork, Subagent, and Protocols | [0010](../adr/0010-profile-and-config-patch.md), [0011](../adr/0011-subagent-as-child-session.md), [0012](../adr/0012-acp-and-sdk-json-rpc.md) |
| [0005](0005-agentic-tool-integration.md) | Agentic Tool Integration | [0014](../adr/0014-integration-as-agentic-tool.md) |

## How to add a diagram

1. Copy `0000-diagram-template.md` to `NNNN-kebab-case-title.md`, where `NNNN` is the
   next free 4-digit number.
2. Replace the template heading with the same number, for example `# 0001. Request Flow`.
3. Keep every Mermaid block renderable while drafting. Use plain sample labels, never
   `<placeholder>` tokens inside a Mermaid fence.
4. Give the diagram a short title, purpose, and legend when symbols are not obvious.
5. Link the ADR that explains the decision, or use `—` when no ADR exists yet.
6. Add a row to the index table above.
7. Run `make agentic-check`, then update the diagram when the documented structure changes.

## Conventions

- Filenames: `NNNN-kebab-case-title.md`, zero-padded to 4 digits.
- The first heading uses the same 4-digit number as the filename.
- One primary idea per diagram. Split unrelated flows into separate diagrams.
- Prefer generic service-template terms over product or customer names.
- Keep diagrams small enough to review in a pull request.
- Cross-link related ADRs, README sections, and code entry points.
- Store generated images only when Mermaid or text source is not practical.
