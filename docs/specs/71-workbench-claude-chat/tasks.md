# Spec 71 tasks

Go goes through dsh-coder (CLAUDE.md); web UI is coded in the main session.

| Task | Scope | Requirements | Files |
|---|---|---|---|
| T-001 | Find the conversation: config dirs, pid files, ancestor check, transcript glob, cache | REQ-WC-001–003 | `internal/workbench/claudechat/locate.go`, `proc_darwin.go`, `proc_linux.go`, `proc_other.go`, tests |
| T-002 | Transcript reader and item mapper, meta, turn_end, paging | REQ-WC-010–015 | `internal/workbench/claudechat/transcript.go`, `items.go`, `testdata/*.jsonl`, tests |
| T-003 | Screen mirror (vt) and parsers: status, mode, dialog, input line | REQ-WC-020–022 | `internal/workbench/claudechat/mirror.go`, `parse.go`, tests |
| T-004 | Input encoding (say, keys), mode cycling, model/effort validation, slash command catalog | REQ-WC-031–035 | `internal/workbench/claudechat/input.go`, `commands.go`, tests |
| T-005 | HTTP routes, per-session chat manager (stream fan-out, mirror and control attachments, idle stop), image route | REQ-WC-030, 036, 040, 041 | `web/workbench_chat.go`, `web/workbench.go` (route registration), tests |
| T-006 | Web: API client, chat view (items, groups, markdown, states) | REQ-WC-052, 053, 058 | `web/ui/src/api/workbench-chat.ts`, `web/ui/src/views/workbench-chat.tsx`, `web/ui/src/styles/workbench-chat.css` |
| T-007 | Web: composer, toolbar menus, `/` and `@` menus, attachments | REQ-WC-055–057 | `web/ui/src/views/workbench-chat-composer.tsx` |
| T-008 | Web: needs-you, question and plan cards | REQ-WC-054 | `web/ui/src/views/workbench-chat-cards.tsx` |
| T-009 | Web: pane header button, ⇧⌘J, per-pane view, phone default, light mode | REQ-WC-050, 051, 059, 060 | `web/ui/src/views/workbench.tsx` |
| T-010 | Tests, lint, dist build, live check on a scratch daemon | all | — |
