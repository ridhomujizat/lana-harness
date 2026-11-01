# Tasks: Agent rooms

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Design**: [design.md](design.md)
- **Requirements revision**: 1
- **Contract revision**: 1
- **Design revision**: 1
- **Status**: In Progress

## Execution rules

- Implement only the requirements and acceptance criteria assigned to the task.
- Stop if requirements, design, or contract conflict with each other.
- Every acceptance criterion has exactly one evidence owner.
- Mark a task complete only after verification evidence has been recorded.
- Work happens on branch `feature/agent-rooms`. It is not merged into `development` without the
  owner.
- Two chains run in parallel: backend (Go, `T-001`–`T-005`) and web (`web/ui`, `T-006`–`T-009`).
  The chains do not edit each other's files. `T-010` joins them.

## Coverage

| Acceptance criterion | Contract operation | Evidence owner | Verification | Evidence |
|---|---|---|---|---|
| `AC-ROOM-001` | `API-ROOM-001` | `T-003` | `go test ./web -run TestRooms` | Pending |
| `AC-ROOM-002` | `API-ROOM-002` | `T-007` | `npx vitest run src/views/chats.test.tsx` | Pending |
| `AC-ROOM-003` | `API-ROOM-002` | `T-007` | `npx vitest run src/views/chats.test.tsx` | Pending |
| `AC-ROOM-004` | `API-ROOM-003` | `T-003` | `go test ./web -run TestRoomSend` | Pending |
| `AC-ROOM-005` | `API-ROOM-003` | `T-003` | `go test ./web -run TestRoomSend` | Pending |
| `AC-ROOM-006` | Not applicable | `T-007` | `npx vitest run src/views/chats.test.tsx` | Pending |
| `AC-ROOM-007` | `API-ROOM-004` | `T-003` | `go test ./web -run TestRoomUnread` | Pending |
| `AC-ROOM-008` | `API-ROOM-001` | `T-003` | `go test ./web -run TestPresence` | Pending |
| `AC-ROOM-009` | `API-ROOM-001` | `T-003` | `go test ./web -run TestPresence` | Pending |
| `AC-ROOM-010` | `API-ROOM-001` | `T-003` | `go test ./web -run TestPresence` | Pending |
| `AC-ROOM-011` | `API-ROOM-005` | `T-004` | `go test ./web -run TestRoomStream` | Pending |
| `AC-ROOM-012` | Not applicable | `T-008` | `npx vitest run src/views/room-group.test.tsx` | Pending |
| `AC-ROOM-013` | `API-ROOM-003` | `T-003` | `go test ./web -run TestRoomSendMention` | Pending |
| `AC-ROOM-014` | `API-ROOM-006/007` | `T-001` | `go test ./config ./web -run 'Title|Color'` | Pending |
| `AC-ROOM-015` | `API-ROOM-006/007` | `T-001` | `go test ./config ./web -run 'Title|Color'` | Pending |
| `AC-ROOM-016` | `API-ROOM-007` | `T-001` | `go test ./web -run TestAgentFileJob` | Pending |
| `AC-ROOM-017` | `API-ROOM-008` | `T-005` | `go test ./web -run TestOverview` | Pending |
| `AC-ROOM-018` | `API-ROOM-008` | `T-005` | `go test ./web -run TestOverview` | Pending |
| `AC-ROOM-019` | Not applicable | `T-009` | `npx vitest run src/views/agents.test.tsx` | Pending |
| `AC-ROOM-020` | Not applicable | `T-006` | `npx vitest run src/app` | Pending |
| `AC-ROOM-021` | Not applicable | `T-006` | `npx vitest run src/views/presence.test.tsx` + CSS review | Pending |

## Task graph

```mermaid
flowchart LR
  T1["T-001 config title/color + JOB.md"] --> T2["T-002 store queries + room_reads"]
  T2 --> T3["T-003 room service + endpoints"]
  T3 --> T4["T-004 room stream hub"]
  T4 --> T5["T-005 agent overview"]
  T6["T-006 web nav, api, presence kit, motion"] --> T7["T-007 Chats page"]
  T7 --> T8["T-008 group room"]
  T8 --> T9["T-009 roster + profile"]
  T5 --> T10["T-010 build, verify, smoke"]
  T9 --> T10
```

## Tasks

- [ ] `T-001` Add agent title, color and JOB.md
  - Depends on: `None`
  - Requirements: `REQ-ROOM-009`
  - Acceptance criteria: `AC-ROOM-014`, `AC-ROOM-015`, `AC-ROOM-016`
  - Contract operations: `API-ROOM-006`, `API-ROOM-007`
  - Scope: `core/config.go`, `config/` validation, `internal/agenthome`, `web/agents.go`, `web/agentfiles.go`, tests.
  - Completion evidence: Pending

- [ ] `T-002` Add store queries and the room_reads table
  - Depends on: `T-001`
  - Requirements: `REQ-ROOM-002`, `REQ-ROOM-005`
  - Acceptance criteria: covered by `T-003`
  - Contract operations: Not applicable (internal)
  - Scope: `SessionFilter.Agent` (+ `Before` if missing), `CountAgentEvents`, migration `0005_room_reads.sql`, `RoomReadAt`/`SetRoomRead`, `Backend` plumbing in `cmd/lana/web.go`, store tests.
  - Completion evidence: Pending

- [ ] `T-003` Build the room service and room endpoints
  - Depends on: `T-002`
  - Requirements: `REQ-ROOM-001`, `REQ-ROOM-003`, `REQ-ROOM-005`, `REQ-ROOM-006`, `REQ-ROOM-008`
  - Acceptance criteria: `AC-ROOM-001`, `AC-ROOM-004`, `AC-ROOM-005`, `AC-ROOM-007`–`AC-ROOM-010`, `AC-ROOM-013`
  - Contract operations: `API-ROOM-001`–`API-ROOM-004`
  - Scope: `web/rooms.go`, `web/rooms_test.go`, route registration.
  - Completion evidence: Pending

- [ ] `T-004` Stream room changes
  - Depends on: `T-003`
  - Requirements: `REQ-ROOM-007`
  - Acceptance criteria: `AC-ROOM-011`
  - Contract operations: `API-ROOM-005`
  - Scope: room hub in `web/rooms.go` (or `web/roomstream.go`), tests.
  - Completion evidence: Pending

- [ ] `T-005` Serve the agent overview
  - Depends on: `T-004`
  - Requirements: `REQ-ROOM-010`, `REQ-ROOM-011`
  - Acceptance criteria: `AC-ROOM-017`, `AC-ROOM-018`
  - Contract operations: `API-ROOM-008`
  - Scope: `web/overview.go`, `web/overview_test.go`, backend plumbing.
  - Completion evidence: Pending

- [ ] `T-006` Add web navigation, rooms API client, presence kit and motion
  - Depends on: `None`
  - Requirements: `REQ-ROOM-012`, `REQ-ROOM-013`
  - Acceptance criteria: `AC-ROOM-020`, `AC-ROOM-021`
  - Contract operations: client side of `API-ROOM-001`–`API-ROOM-008`
  - Scope: `app/Sidebar.tsx`, `app/router.ts`, `App.tsx`, `api/rooms.ts`, `api/agents.ts`, `views/presence.tsx`, `styles/rooms.css`, tests.
  - Completion evidence: Pending

- [ ] `T-007` Build the Chats page
  - Depends on: `T-006`
  - Requirements: `REQ-ROOM-001`–`REQ-ROOM-005`
  - Acceptance criteria: `AC-ROOM-002`, `AC-ROOM-003`, `AC-ROOM-006`
  - Scope: `views/chats.tsx`, `views/room-timeline.tsx`, tests.
  - Completion evidence: Pending

- [ ] `T-008` Render group rooms
  - Depends on: `T-007`
  - Requirements: `REQ-ROOM-008`
  - Acceptance criteria: `AC-ROOM-012`
  - Scope: `views/room-group.tsx`, tests.
  - Completion evidence: Pending

- [ ] `T-009` Build the alive roster and agent profile
  - Depends on: `T-008`
  - Requirements: `REQ-ROOM-009`, `REQ-ROOM-010`, `REQ-ROOM-011`
  - Acceptance criteria: `AC-ROOM-019`
  - Scope: `views/agents.tsx`, `views/agent-overview.tsx`, `views/agent-detail.tsx` (Overview tab, title/color fields, JOB.md), tests.
  - Completion evidence: Pending

- [ ] `T-010` Build the dashboard and verify end to end
  - Depends on: `T-005`, `T-009`
  - Scope: `make web`, `go build ./...`, `go test ./...`, `gofmt -l .`, `golangci-lint run ./...`, `npx vitest run`, local daemon smoke.
  - Completion evidence: Pending

## Final verification

- [ ] Every acceptance criterion has passing evidence.
- [ ] No observable behavior exists without a requirement ID.
- [ ] `gofmt -l .` is empty, `golangci-lint run ./...`, `go test -race ./...`, and the release build pass.

## Approval

- [x] Requirements and design are Approved and their revisions are recorded.
- [x] Contract is Approved.
- [x] Every acceptance criterion has exactly one evidence owner.
- [x] Every task has a limited scope, dependencies, and verification.
- [x] No task introduces new behavior.
- **Approved by**: agent on behalf of the owner (gate lifted 2026-11-02)
- **Approved on**: 2026-11-02
