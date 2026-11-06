# Requirements: Web dashboard on React and TypeScript

- **Issue**: —
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: Not required — every `/v1` operation, route, and page text stays the same
- **Related ADR**: —
- **Related diagram**: —

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner decision 2026-10-30 | "oke migrasi ke react typescript tapi jangan tulis component React Flow, nanti takutnya next implement pakai itu" | scope, React Flow exclusion |
| Owner goal 2026-10-30 | type safety; a later workflow-automation feature with a canvas | why React + TS |
| Spec #9, #11, #12, #13 | current routes, pages, `/v1` contracts, asset rules, CSP | parity baseline |

## Goal

The web dashboard runs on React and strict TypeScript with the same pages, behavior, and look as today,
so later features (such as a workflow canvas) can be built with type checking and the React ecosystem.
lana stays one binary that serves the dashboard without Node at runtime.

## Actors

- **Owner** — uses the dashboard; must see no change.
- **Developer** — builds the dashboard; gets type errors at build time.

## Scope

### Included

- Vite + React + TypeScript (`strict`) source under `web/ui/`.
- All 39 current modules converted: shell, router, API client, UI helpers, markdown, format, theme, and every view.
- Typed API client for every `/v1` call the dashboard makes today.
- The built dashboard embedded in the Go binary; `go build` works without Node.
- Node in `flake.nix`; one command to build the dashboard; a check that the committed build is current.
- Asset and source tests moved to the new setup with the same intent.

### Not included

- React Flow or any canvas or workflow UI (`@xyflow/*` must not be added).
- New features, page redesigns, or text changes.
- A component library (shadcn, MUI, Radix) or CSS rewrite. Current CSS files and class names stay.
- Changes to `/v1` handlers.
- Generating TypeScript types from Go structs (follow-up).

## Behavior rules

### REQ-WRT-001: Same dashboard

The system shall serve the same routes (`#/home` … `#/setup/{step}`), page text, controls, keyboard behavior, focus handling, announcements, and styles as before the migration.

#### Acceptance criteria

- `AC-WRT-001`: Given the route list in spec #9 (`TestShellRoutesMatchRequirement`), when each route is opened, then the same view renders and unknown routes show the not-found page.
- `AC-WRT-002`: Given the manual parity checklist in design.md, when the owner walks it, then every item behaves as before.

### REQ-WRT-002: Strict TypeScript

All dashboard source shall be `.ts`/`.tsx` with `strict: true`, `noUncheckedIndexedAccess: true`, and no `any` outside one documented escape file. Every `/v1` request and response used by the dashboard shall have a named type.

#### Acceptance criteria

- `AC-WRT-003`: Given the source tree, when `npm run typecheck` runs, then it passes, no `.js`/`.jsx` source remains under `web/ui/src`, and `grep -RE ": any|as any" web/ui/src --include="*.ts" --include="*.tsx"` matches only the escape file (CSS is excluded; `overflow-wrap: anywhere` is valid).

### REQ-WRT-003: One binary, no runtime Node

The Go binary shall embed the built dashboard. `go build ./...` and `go test ./...` shall work from a clean checkout without running Node.

#### Acceptance criteria

- `AC-WRT-004`: Given a clean checkout, when `nix develop -c go build ./cmd/lana` runs and the binary serves `/`, then the dashboard loads.
- `AC-WRT-005`: Given a source change without rebuilding, when `make web-check` runs, then it fails and names the stale build.

### REQ-WRT-004: Self-contained and CSP-safe

The served dashboard shall load only same-origin resources under `default-src 'self'`: no inline scripts, no external URLs in `src`, `href`, `url()`, `@import`, `import()`, or `fetch` targets.

#### Acceptance criteria

- `AC-WRT-006`: Given the embedded build, when the Go asset tests run, then `index.html` references only `/assets/…`, has no inline `<script>` body, and no built file loads an external resource.
- `AC-WRT-007`: Given the dashboard in a browser with the current CSP header, when every route is opened, then the console shows no CSP violation.

### REQ-WRT-005: Tests keep their intent

Tests that read dashboard source (route table, theme and sidebar persistence, home empty state) shall move to Vitest in `web/ui`. Go tests shall keep checking served output and headers.

#### Acceptance criteria

- `AC-WRT-008`: Given `npm test` in `web/ui`, then tests for the route table, theme persistence, sidebar persistence, home empty state, markdown rendering, and the API client error mapping pass.

## Failure cases

| Condition | Expected behavior |
|---|---|
| Type error | `npm run build` fails; no output written |
| Stale committed build | `make web-check` fails with the stale paths |
| Unknown route | not-found page, as today |

## Authorization and security

- Unchanged: cookie session, CSRF header, loopback-only server, CSP `default-src 'self'; frame-ancestors 'none'`.

## Accessibility

- Same as today: skip link, `#announcer` live region, focus management on route change, labels, keyboard access.

## Blocking Open Questions

| ID | Question | Controls | Owner | Status | Resolution |
|---|---|---|---|---|---|
| `Q-001` | React or Preact + TS? | all | Ridho | Resolved | React, for the later canvas |
| `Q-002` | Add React Flow now? | scope | Ridho | Resolved | No |
| `Q-003` | Commit the build output? | `REQ-WRT-003` | Ridho | Resolved | Yes, so `go build` needs no Node; `make web-check` guards freshness |

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-WRT-001` | `AC-WRT-001`, `AC-WRT-002` | owner decision |
| `REQ-WRT-002` | `AC-WRT-003` | owner goal |
| `REQ-WRT-003` | `AC-WRT-004`, `AC-WRT-005` | `Q-003` |
| `REQ-WRT-004` | `AC-WRT-006`, `AC-WRT-007` | spec #9 asset rules |
| `REQ-WRT-005` | `AC-WRT-008` | spec #9 tests |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Blocking Open Questions is `None`.
- **Approved by**: Ridho
- **Approved on**: 2026-10-30
