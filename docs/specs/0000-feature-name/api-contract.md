# Contract: <Feature Name>

- **Issue**: `#<number>`
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: <integer>
- **Requirements status**: Draft
- **Contract revision**: <integer, starting at 1>
- **Applies to**: CLI / config / HTTP / JSON-RPC / ACP / events / Not required
- **Status**: Draft

Do not define new behavior here. Requirements own behavior; this file owns the exact form of the
interface. Do not write a contract against requirements that are not yet approved.

If the feature does not change the external interface, set **Applies to** to `Not required`, write
the reason, and delete the operations section.

## Contract rules

- Document only new or changed operations. Link shared conventions, do not copy them.
- Every operation has a stable ID `API-<CAPABILITY>-NNN`.
- Every operation is linked to at least one `REQ-*` and `AC-*`.
- Write the input, successful output, failure codes, authorization, and compatibility.
- Use examples only if the shape or edge case is not clear from the tables.

## Shared conventions

| Topic | Decision or authority |
|---|---|
| CLI exit codes | <table or `Not applicable`> |
| stdout and stderr output | <rule or `Not applicable`> |
| Config format | <file and section or `Not applicable`> |
| Authentication | <token, socket permission, or `Not applicable`> |
| Versioning | <versioning rule or `Not applicable`> |
| Serialization | <JSON, time, IDs, or `Repository convention`> |

## Operation index

| Operation ID | Type | Command/method | Path/flag/topic | Requirements | Acceptance criteria |
|---|---|---|---|---|---|
| `API-<CAPABILITY>-001` | CLI | `lana <subcommand>` | `--flag` | `REQ-...` | `AC-...` |

## API-<CAPABILITY>-001: <Operation title>

- **Goal**: <one sentence>
- **Requirements**: `REQ-...`
- **Acceptance criteria**: `AC-...`
- **Stability**: New / Existing / Compatibility-sensitive
- **Authorization**: <permission and how it is enforced>

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Argument/flag/env/config/body | `<name>` | `<type>` | Yes/No | <limits and meaning> |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| <condition> | `<code>` | <stdout, payload, or effect> |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| <condition> | `<code>` | `<code>` | Yes/No | <safe result> |

### Side effects and guarantees

- **State transition**: <change or `None`>
- **Idempotency**: <retry behavior or `Not applicable`>
- **Concurrency**: <rule or `Not applicable`>
- **Data exposure**: <what is hidden or redacted>

### Compatibility

- **Breaking change**: Yes/No — <reason>
- **Deprecation**: <replacement or `Not applicable`>

## Contract traceability

| Operation | Requirements | Acceptance criteria | Design component | Verification |
|---|---|---|---|---|
| `API-...` | `REQ-...` | `AC-...` | <component> | <test or probe> |

## Blocking Contract Questions

| ID | Question | Controls | Requested reference | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `CQ-001` | <interface decision> | <flag/exit code/format> | <example or direction> | <name> | Open | — |

Write `None` only if the contract is complete or the contract is `Not required`.

## Approval

- [ ] Requirements are Approved and their revision is recorded above.
- [ ] Contract applicability has been determined.
- [ ] Every operation has a stable ID and is linked to requirements and acceptance criteria.
- [ ] Input, success, failure, authorization, and compatibility are explicit.
- [ ] No behavior exists only in this file.
- [ ] Blocking Contract Questions is `None`.
- **Approved by**: —
- **Approved on**: —
