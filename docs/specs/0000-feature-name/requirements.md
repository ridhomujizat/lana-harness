# Requirements: <Feature Name>

- **Issue**: `#<number>`
- **Status**: Draft
- **Revision**: <integer, starting at 1>
- **Owner**: <name>
- **Contract**: [api-contract.md](api-contract.md) — Required / Not required
- **Related ADR**: [ADR Index](../../adr/README.md) — <link or `—`>
- **Related diagram**: [Diagram index](../../diagram/README.md) — <link or `—`>

## Sources

| Source | Reference | Used for |
|---|---|---|
| Issue | `#<number>` | user story, scope |
| ADR | <link or `—`> | binding decisions |
| External reference | <link or `—`> | comparison behavior |

Anything not supported by a source or an explicit approval goes into Blocking Open Questions. Do not
present agent assumptions as requirements.

## Goal

One paragraph about the outcome for the user. Do not describe the solution.

## Actors

- **<Actor>** — <purpose and permission boundary>.

## Definitions

- **<Term>** — <one stable meaning for this feature>.

## Scope

### Included

- <observable capability>

### Not included

- <excluded behavior or follow-up feature>

## Assumptions

- <confirmed assumption> — Source: <issue/ADR/decision>.

## Behavior rules

### REQ-<CAPABILITY>-001: <Rule title>

The system shall <observable behavior>.

#### Acceptance criteria

- `AC-<CAPABILITY>-001`: Given <state>, when <action>, then <measurable result>.

## Contract commitments

Delete this section if the feature does not change the external interface.

| Requirement | Observable commitment | Contract operation |
|---|---|---|
| `REQ-...` | <behavior, exit code, or compatibility guarantee> | `API-...` |

## State and operation matrix

Delete this section if no behavior depends on state.

| Current state | Operation | Result | Allowed |
|---|---|---|---:|
| <state> | <operation> | <new state or effect> | Yes/No |

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| <condition> | <safe, observable result> | <exit code, error code, or question> |

## Authorization and security

- <who may do what; how it is enforced>

## Data integrity and concurrency

- <invariant that must hold during retry, race, or partial failure>

## Accessibility

- <keyboard, focus, no reliance on color, or `Not applicable`>

## Scenarios

### SC-001: <Title>

**Given** <initial state>
**When** <action>
**Then** <observable result>

## Blocking Open Questions

| ID | Question | Controls | Requested reference | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `Q-001` | <decision needed> | <behavior or contract> | <example, screenshot, direction> | <name> | Open | — |

Write `None` only if all questions that affect behavior have been answered. The agent must
ask every Open question and wait for an answer before approval or design.

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-<CAPABILITY>-001` | `AC-<CAPABILITY>-001` | <issue/ADR/decision> |

Each requirement and acceptance criterion appears exactly once.

## Approval

- [ ] Scope and exclusions are explicit.
- [ ] Success and failure behavior is measurable.
- [ ] Authorization is explicit or stated as not applicable.
- [ ] State transitions and destructive effects are explicit or not applicable.
- [ ] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [ ] Every requirement and acceptance criterion has a stable ID and a source.
- [ ] Contract applicability is explicit.
- [ ] Blocking Open Questions is `None`.
- **Approved by**: —
- **Approved on**: —
