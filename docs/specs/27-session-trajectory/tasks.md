# Tasks: Session trajectory view

- **Status**: In Progress

Test command: `cd web/ui && npm run typecheck && npm test && npm run lint`.

Runs in parallel with spec 28 (dashboard). Spec 28 owns `web/ui/src/views/home.tsx`,
`web/ui/src/app/Sidebar.tsx`, `web/ui/src/api/dashboard.ts`, `web/ui/src/styles/dashboard.css`, and
Go files. This task must not touch them.

### T-001 Trajectory model, view, and session tab

- Status: Done 2026-11-01. Evidence: `npm run typecheck`, `npm run lint` clean; vitest 27 files / 166 tests
  pass (22 new trajectory tests). `App.tsx` unchanged: `SessionPage` reads the hash query itself.
- Files: new `web/ui/src/views/trajectory-model.ts`, `trajectory-model.test.ts`, `trajectory.tsx`,
  `trajectory.test.tsx`, `web/ui/src/styles/trajectory.css`; edit `web/ui/src/views/session.tsx`,
  `web/ui/src/app/App.tsx` (only if the `view` param must be passed).
- Covers: REQ-TRJ-001–010, AC-TRJ-001–008. Design §1–§4.

### T-002 Rebuild `web/dist` and verify in the browser (main session)

- Status: In Progress. `web/dist` rebuilt 2026-11-01. Browser check by the owner is pending.
