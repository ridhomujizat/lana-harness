# Design: Web dashboard on React and TypeScript

- **Requirements**: [requirements.md](requirements.md), revision 1, Approved
- **Contract**: Not required
- **Status**: Approved
- **Design revision**: 1

## Layout

```
web/
  ui/                      # new source (Node project)
    package.json           # npm scripts: dev, build, typecheck, test, lint
    package-lock.json
    tsconfig.json          # strict, noUncheckedIndexedAccess, jsx: react-jsx
    vite.config.ts         # base "/assets/", outDir "../dist", modulePreload.polyfill false, dev proxy /v1 → 127.0.0.1:18300
    index.html             # same body as today (skip link, #announcer, #app, noscript)
    public/icons/*.svg     # copied from web/assets/icons
    src/
      main.tsx             # imports all current CSS files in today's order, mounts <App/>
      app/                 # shell: App.tsx, router.ts (route table), Sidebar, theme.ts
      api/                 # client.ts (fetch, CSRF, 401 handler, ApiError) + one file per domain with types
      lib/                 # format.ts, markdown.ts, avatar.ts, any.ts (the only `any` escape file)
      ui/                  # ui.tsx (AnnouncerProvider, useAnnounce, shared bits), components/*
      views/               # one .tsx per current view file, same names
      styles/              # current CSS files, moved unchanged
  dist/                    # committed build output, embedded by Go
  assets/                  # removed in T-007
```

## Decisions

- **Dependencies** (exact versions pinned in `package-lock.json`): `react`, `react-dom` 19; dev: `typescript`, `vite`, `@vitejs/plugin-react`, `vitest`, `jsdom`, `@testing-library/react`, `@testing-library/user-event`, `eslint` + `typescript-eslint` + `eslint-plugin-react-hooks`. No router, state, or UI library: the hash router and state are ported as they are. `@xyflow/*` is not allowed.
- **Conversion rule** — port, do not redesign: `html\`...\`` → JSX with the same elements, classes, attributes, and text; `preact/hooks` → `react` hooks; `class` → `className`, `for` → `htmlFor`, event names to React casing. Keep function names, file split, and comments. Behavior changes only where React requires it (for example controlled inputs), and each such change is listed in the task report.
- **Types** — written by hand from the spec #9/#11/#12/#13 contracts and the Go JSON structs, one domain file each: `sessions`, `turns`, `agents`, `teams`, `skills`, `mcp`, `channels`, `settings`, `secrets`, `setup`, `questions`, `approvals`, `auth`, `daemon`, `audit`, `tools`. Generation from Go is a follow-up.
- **Parallel-safe shell** — T-001 creates every `src/views/*.tsx` file as a stub that exports the final component names and renders "This page is being migrated." App code imports them statically. View tasks replace only their own files and may change only their own domain API file.
- **Build output** — `vite build` writes `web/dist/index.html` and `web/dist/assets/*` with hashed names; no inline script (`modulePreload.polyfill: false`); CSS is extracted. `web/server.go` embeds `dist` and serves `/` from `dist/index.html` and `/assets/*` from `dist/assets`. URL paths stay `/` and `/assets/...`.
- **Make targets** — `make web` (`npm ci && npm run build` in `web/ui`), `make web-check` (build into a temp dir and diff with `web/dist`; non-zero with paths on difference). `release` depends on `web`.
- **Nix** — add `nodejs_22` to `flake.nix`.

## Go test changes

| Old test | New home |
|---|---|
| `TestEmbeddedAssetsLoad`, `TestWebServerAssets` | Go, against `dist` |
| `TestIndexHTMLReferencesOnlyAssets`, `TestWebAssetsSelfContained`, `TestOwnAssetsHaveNoExternalURLs` | Go, against `dist`: `index.html` refs only `/assets/`, no inline script body; CSS has no external `url()`/`@import`; JS has no external `import(`, `src=`, or `fetch(` target. Plain string literals inside React (error-doc links) are allowed. |
| `TestVendoredPreactOnlyHasTheSVGNamespaceURL` | removed (no vendored Preact) |
| `TestShellRoutesMatchRequirement`, `TestHomeViewIsTheEmptyState`, `TestShellPersistsThemeAndSidebarState` | Vitest in `web/ui/src/**.test.ts(x)` with the same assertions |
| `TestWebServerSecurityHeaders` | unchanged |

## Manual parity checklist (AC-WRT-002)

Login and logout; setup wizard all steps; sessions list, filters, new session, chat with streaming, Markdown, tool calls, approvals (allow once/session/reject), ask-user questions, cancel; agents list, detail tabs (general, persona, tools, skills, mcp, rules, workspace), create, duplicate; teams; skills list, editor, new; MCP list, detail, test, restart, add/delete; channels list and the spec #13 Telegram three steps; settings tabs including secrets and daemon restart; theme toggle and sidebar collapse persist after reload; keyboard-only navigation and the skip link; screen-reader announcements.

## Verification

| Acceptance criterion | Method |
|---|---|
| `AC-WRT-001`, `AC-WRT-008` | Vitest |
| `AC-WRT-002`, `AC-WRT-007` | manual in the browser |
| `AC-WRT-003` | `npm run typecheck`, grep |
| `AC-WRT-004`, `AC-WRT-006` | Go tests, clean build |
| `AC-WRT-005` | `make web-check` after touching a source file |

## Risks

- Large mechanical diff (~21k lines). Mitigated by the stub shell, per-group tasks, and the parity checklist.
- Committed build output causes noisy diffs; `make web-check` keeps it honest.
