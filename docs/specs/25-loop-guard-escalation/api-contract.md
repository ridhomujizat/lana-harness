# Contract: Loop guard escalation and tool-name recovery

- **Requirements**: [requirements.md](requirements.md), revision 1, Approved
- **Contract revision**: 1
- **Applies to**: session events, Go library `Result.StopReason`, web, Telegram, TUI
- **Status**: Approved

## Operation index

| ID | Type | Operation | Requirements |
|---|---|---|---|
| `API-LPG-001` | Event | `turn/end` reason `loop` | `REQ-LPG-009` |
| `API-LPG-002` | Go library | `lana.StopLoop = "loop"` | `REQ-LPG-009` |
| `API-LPG-003` | UI text | end card, status badge, Telegram end line, TUI line | `REQ-LPG-010` |
| `API-LPG-004` | Tool result | blocked-call and unknown-name texts | `REQ-LPG-008`, `REQ-LPG-021` |

## API-LPG-001: `turn/end` reason `loop`

`turn/end` payload `{ "reason": "loop", ... }`. `error` is absent. Other reasons are unchanged.
Readers that do not know `loop` treat it like any unknown reason.

## API-LPG-002: Go library

`lana.go` adds `StopLoop = "loop"` next to `StopMaxSteps`. `Result.StopReason` carries it.

## API-LPG-003: Surface texts

| Surface | Where | Text |
|---|---|---|
| Web turn card | `web/ui/src/views/turn.tsx` `turnEndCard` | `Stopped: lana kept repeating a step without progress.` + Continue |
| Web session line | `web/ui/src/views/session.tsx` | `Turn stopped: repeated a step without progress.` |
| Web team badge | `web/ui/src/views/team.tsx` | tone `gray`, `Stopped · <n> steps` |
| Web reason words | `web/ui/src/lib/format.ts` `stopReasonWords` | `stopped: no progress` |
| Telegram end line | `channels/telegram/chat.go` `endLine` | `⚠ Stopped: lana kept repeating a step without progress.` + Continue |
| TUI | `channels/cli/tui/model.go` | `⚠ turn stopped: repeated a step without progress` |

## API-LPG-004: Tool result texts

The texts in REQ-LPG-008 and REQ-LPG-021 are exact. Status is `error`.
