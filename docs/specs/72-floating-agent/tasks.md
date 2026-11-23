# Spec 72 tasks

Web only; coded in the main session (CLAUDE.md lets web UI be edited directly).

| Task | Scope | Requirements | Files |
|---|---|---|---|
| T-001 | Shared Chats row component | REQ-FA-050 | `web/ui/src/views/room-list-row.tsx`, `views/chats.tsx` |
| T-002 | Store: state, persistence, current room, notifications | REQ-FA-001, 002, 030, 031 | `web/ui/src/app/floating-agent-store.ts` |
| T-003 | Desktop pill, tucked edge, panel list and room, Esc / ⌥Space | REQ-FA-010–024, 032 | `web/ui/src/views/floating-agent.tsx` |
| T-004 | Phone bubble, drag and snap, hide target, stack and fan, sheet | REQ-FA-040–044 | `web/ui/src/views/floating-agent-phone.tsx` |
| T-005 | Styles | all | `web/ui/src/styles/pages-gr8r/floating-agent.css` |
| T-006 | Remove the dock, mount in App, preview harness, phone menu item | REQ-FA-042, 050, 051 | `app/App.tsx`, `preview/main.tsx`, `styles/index.css` |
| T-007 | Tests, lint, dist build, browser check | all | `views/floating-agent.test.tsx`, `app/floating-agent-store.test.ts` |
