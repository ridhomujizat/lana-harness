# Feature Specifications

This folder contains feature specifications that are ready to implement. One spec is one package that
can be reviewed. It contains requirements, the interface contract (if any), design, and tasks:

```text
docs/specs/<issue>-<slug-fitur>/
├── requirements.md
├── api-contract.md       # required if the CLI, config, HTTP, JSON-RPC, or events change
├── design.md
└── tasks.md
```

`<issue>` is the GitHub issue number without `#`, for example `12-cli-tui`. The GitHub issue is the tracker
for status and discussion. The spec in the repository is the versioned source of truth that is read
together with the code.

## Quick start

Start from the index below, then read only the artifacts needed for the current decision:

| Goal | Read |
|---|---|
| Understand scope, rules, or acceptance | `requirements.md` |
| Review a CLI command, flag, config, endpoint, or payload | `requirements.md` + related operations in `api-contract.md` |
| Implementation | assigned requirements + contract operations + related components in `design.md` |
| Working on or verifying work | related entries in `tasks.md` |
| Understanding a durable decision or flow | [ADR](../adr/README.md) or [diagram](../diagram/README.md) linked from `design.md` |

How to create a new package:

1. Create a GitHub issue for the feature.
2. Copy `0000-feature-name/` to `<issue>-<slug-fitur>/` and add a row to the index.
3. Write and approve `requirements.md`. Open questions that are not yet answered still block.
4. Write and approve `api-contract.md` if the externally visible interface changes. If not,
   mark it `Not required` and delete the operations section.
5. Write and approve `design.md` based on the approved requirements and contract.
6. Write `tasks.md`, implement one slice, then record the actual verification evidence.

Do not read all artifacts by default. Follow the links and stable IDs for the current task:
`REQ-*` → `AC-*` → `API-*` (if any) → design component → `T-*` → evidence.

## Artifact responsibilities

| Artifact | Owns | Must not own |
|---|---|---|
| `requirements.md` | observable behavior, rules, scope, acceptance criteria, open questions | code layout, implementation steps |
| `api-contract.md` | the exact form of CLI commands, flags, exit codes, config keys, HTTP endpoints, JSON-RPC methods, events | business reasons, internal mapping |
| `design.md` | repository-specific solution, internal mapping, data model, failure handling, verification strategy | product requirements, duplicated contract schema, task progress |
| `tasks.md` | ordered implementation slices, requirement coverage, completion evidence | new behavior or architecture decisions |
| [`docs/adr/`](../adr/README.md) | cross-feature decisions and tradeoffs | feature task lists |
| [`docs/diagram/`](../diagram/README.md) | durable visual views of flows, boundaries, topology, data models | duplicated spec prose |

## Lifecycle

Each file has its own approval gate:

```text
requirements: Draft → Approved
api-contract: Draft → Approved (if applicable)
design:       Draft → Approved
tasks:        Draft → Approved → In Progress → Completed
```

Order of work: approve requirements, then the contract (if applicable), then design, then tasks.
Implementation must not start while there are still blocking open questions or earlier artifacts
have not been approved.

> **Gate lifted (2026-11-05)** for spec #35 (calendar). The owner answered the interview and said "tulis spec dulu lalu
> implementasi FEnya". Remaining details use `Rekomendasi agent:` in its resolution table.
>
> **Gate lifted (2026-11-02)** for spec #32 (agent rooms). The owner approved the design and said
> "implement approve dengan rekomendasi kamu". Same rules as below.
>
> **Gate lifted (2026-11-01)** for the new specs #29, #30, and #31. The owner said "oke sip implement 3 spec yg kamu usulkan".
> Open questions are resolved with agent recommendations in each resolution table, marked Approved on behalf of the owner.
>
> **Gate lifted again (2026-10-30)** for the revisions of specs #5, #6, and #9, and for the new specs #10 and #11.
> The same rules apply: agent recommendations in the resolution tables, marked Approved on behalf of the owner.
>
> **Gate lifted (2026-10-29).** The owner asked the agent to use its own recommendations for open
> questions and to draft all artifacts at once. Such decisions are recorded with the prefix
> `Rekomendasi agent:` (agent recommendation) in the resolution table, and the artifacts are marked
> Approved on behalf of the owner. The owner can change them at any time through a new revision.
> For the next spec, the gate applies again unless the owner lifts it.

The agent must ask the owner every blocking question and wait for the answer. Recording a
question in a file does not mean design or implementation may proceed. Questions should ask for
references: an issue, a screenshot, an example output, an existing implementation, or a preferred direction.

Architecture artifacts are checked between requirements and design:

```text
Issue → requirements Approved → contract Approved → scan ADR/diagram → design Approved → tasks Approved → code
```

ADRs with status Accepted and the applicable diagrams are inputs to requirements and design. Every
design must complete the Visual Architecture Check. Required ADRs must be Accepted before
the design is approved.

## Traceability

- Requirement ID: `REQ-<KAPABILITAS>-NNN`.
- Acceptance criteria ID: `AC-<KAPABILITAS>-NNN`.
- Task ID: `T-NNN`, with the GitHub issue or PR number if available.
- Contract operation ID: `API-<KAPABILITAS>-NNN`, linked to `REQ-*` and `AC-*`.
- Every acceptance criterion has exactly one evidence owner.
- Every completed task records test or runtime evidence.
- Requirements, contract (if applicable), and design have explicit revision numbers before tasks are approved.

## Rules against making things up

The agent must not invent behavior rules, flags, fields, defaults, permissions, state transitions,
exit codes, error codes, retry behavior, or compatibility guarantees. Missing or conflicting behavior
goes into the Blocking Open Questions section of `requirements.md` and stops design or implementation.

The agent may choose the simplest private implementation detail that fits the repository, as long as
it is reversible and does not change observable behavior.

## Relationship with ADRs and diagrams

- Create an ADR only for decisions that span features, are contractual, or are hard to reverse.
- A diagram is required for new boundaries, external actors or systems, multi-component flows, lifecycles or state
  transitions, entity relations, concurrency/retry/failure flows, dependency direction, or runtime topology.
- A diagram is not required for fields, validation, labels, local refactors, or single-step behavior.
- `design.md` links to ADRs and diagrams, it does not copy their contents.
- A diagram that only explains one feature stays in `design.md`. Move it to `docs/diagram/` if it is reused.
- Reject decorative diagrams: a useful diagram has at least two nodes/states/entities and one relation.

## Index

| Issue | Feature | Requirements | Contract | Design | Tasks |
|---|---|---|---|---|---|
| — | Package template | [Template](0000-feature-name/requirements.md) | [Template](0000-feature-name/api-contract.md) | [Template](0000-feature-name/design.md) | [Template](0000-feature-name/tasks.md) |
| [#1](https://github.com/ridhomujizat/lana-harness/issues/1) | CLI and TUI chat | [Approved r6](1-cli-tui/requirements.md) | [Approved r4](1-cli-tui/api-contract.md) | [Approved r3](1-cli-tui/design.md) | [Approved r2](1-cli-tui/tasks.md) |
| [#2](https://github.com/ridhomujizat/lana-harness/issues/2) | Foundation: Go module, config loader, Context/modules/hooks | [Approved r4](2-foundation/requirements.md) | [Approved r2](2-foundation/api-contract.md) | [Approved r2](2-foundation/design.md) | [Approved r2](2-foundation/tasks.md) |
| [#3](https://github.com/ridhomujizat/lana-harness/issues/3) | Agent loop, provider, tools, Go library | [Approved r2](3-agent-loop/requirements.md) | [Approved r2](3-agent-loop/api-contract.md) | [Approved r2](3-agent-loop/design.md) | [Approved r2](3-agent-loop/tasks.md) |
| [#4](https://github.com/ridhomujizat/lana-harness/issues/4) | Session event log, permission, approval | [Approved r3](4-session-permission/requirements.md) | [Approved r2](4-session-permission/api-contract.md) | [Approved r2](4-session-permission/design.md) | [Approved r2](4-session-permission/tasks.md) |
| — | Agent home, memory, and onboarding | [Approved r4](5-agent-home/requirements.md) | [Approved r3](5-agent-home/api-contract.md) | [Approved r4](5-agent-home/design.md) | [In Progress r2](5-agent-home/tasks.md) |
| — | Daemon and Telegram channel | [Approved r3](6-daemon-telegram/requirements.md) | [Approved r3](6-daemon-telegram/api-contract.md) | [Approved r4](6-daemon-telegram/design.md) | [In Progress r2](6-daemon-telegram/tasks.md) |
| — | MCP client and Skills | [Approved r1](7-mcp-skills/requirements.md) | [Approved r1](7-mcp-skills/api-contract.md) | [Approved r1](7-mcp-skills/design.md) | [Approved r1](7-mcp-skills/tasks.md) |
| — | Web dashboard: home, sessions, agents, skills, MCP, channels, settings | [Approved r2](9-web-settings-chat/requirements.md) | [Approved r2](9-web-settings-chat/api-contract.md) | [Approved r2](9-web-settings-chat/design.md) | [In Progress r2](9-web-settings-chat/tasks.md) |
| — | Agent teams | [Approved r1](10-agent-teams/requirements.md) | [Approved r1](10-agent-teams/api-contract.md) | [Approved r1](10-agent-teams/design.md) | [Approved r1](10-agent-teams/tasks.md) |
| — | Ask-user questions | [Approved r1](11-ask-user-question/requirements.md) | [Approved r1](11-ask-user-question/api-contract.md) | [Approved r1](11-ask-user-question/design.md) | [Approved r1](11-ask-user-question/tasks.md) |
| — | Loop guard escalation and tool-name recovery | [Approved r2](25-loop-guard-escalation/requirements.md) | [Approved r1](25-loop-guard-escalation/api-contract.md) | [Approved r2](25-loop-guard-escalation/design.md) | [Completed r2](25-loop-guard-escalation/tasks.md) |
| — | Images and file attachments | [Approved r1](26-attachments/requirements.md) | [Approved r1](26-attachments/api-contract.md) | [Approved r1](26-attachments/design.md) | [Completed r1](26-attachments/tasks.md) |
| — | Session trajectory view | [Approved r1](27-session-trajectory/requirements.md) | [Not required](27-session-trajectory/api-contract.md) | [Approved r1](27-session-trajectory/design.md) | [In Progress r1](27-session-trajectory/tasks.md) |
| — | Monitoring dashboard | [Approved r2](28-monitoring-dashboard/requirements.md) | [Approved r2](28-monitoring-dashboard/api-contract.md) | [Approved r1](28-monitoring-dashboard/design.md) | [In Progress r1](28-monitoring-dashboard/tasks.md) |
| — | Session search | [Approved r1](29-session-search/requirements.md) | [Not required](29-session-search/api-contract.md) | [Approved r1](29-session-search/design.md) | [Completed r1](29-session-search/tasks.md) |
| — | Knowledge vault | [Approved r1](30-knowledge/requirements.md) | [Approved r1](30-knowledge/api-contract.md) | [Approved r1](30-knowledge/design.md) | [Completed r1](30-knowledge/tasks.md) |
| — | Knowledge upkeep | [Approved r1](31-knowledge-upkeep/requirements.md) | [Approved r1](31-knowledge-upkeep/api-contract.md) | [Approved r1](31-knowledge-upkeep/design.md) | [Completed r1](31-knowledge-upkeep/tasks.md) |
| — | Agent rooms, presence, alive profile and roster | [Approved r1](32-agent-rooms/requirements.md) | [Approved r1](32-agent-rooms/api-contract.md) | [Approved r1](32-agent-rooms/design.md) | [Completed r1](32-agent-rooms/tasks.md) |
| — | Workflows: guarded automation graphs, durable runs, error ladder | [Approved r1](33-workflows/requirements.md) | [Approved r1](33-workflows/api-contract.md) | [Approved r1](33-workflows/design.md) | [Completed r1](33-workflows/tasks.md) |
| — | Calendar: scheduler calendar, items, habits, workflow slots and runs, Google, ICS | [Approved r1](35-calendar/requirements.md) | [Approved r1](35-calendar/api-contract.md) | [Approved r1](35-calendar/design.md) | [Completed r1](35-calendar/tasks.md) |
| — | Self-update: update card, one-click update, trial boot and rollback | [Approved r1](36-self-update/requirements.md) | [Approved r1](36-self-update/api-contract.md) | [Approved r1](36-self-update/design.md) | [In Progress r1](36-self-update/tasks.md) |
