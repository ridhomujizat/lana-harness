# Spec 61: Workbench file preview, pane polish and program tiles (web, daemon, desktop)

Status: web and daemon implemented on feature/workbench-preview, 2026-11-21; desktop (REQ-WP-040, 041) on feature/desktop-workbench. Owner: interview in chat, all three scopes chosen.

## Problem

The native Workbench app (spec 58, `feature/desktop-workbench`) got a file preview (⌘+click a path in a terminal) and a round of pane polish. The owner wants the same in the web Workbench, which is the main client and is also used remotely from a phone or laptop over Tailscale (spec 57). The native preview reads files itself, so it cannot work against a remote daemon. Program and agent icons also differ between the clients. The owner wants them in sync, as small landscape tiles like GoRex.

## Decisions (owner, 2026-11-21)

1. File preview in the web Workbench, working over Tailscale: the daemon reads the files.
2. Port the native app's pane polish to the web Workbench.
3. The native app's preview uses the same daemon endpoint, so it works with a remote daemon later.
4. Program and agent icons are the same landscape tiles in both clients, for every known agent (claude, codex, cursor, gemini, antigravity, grok, opencode, omp, pi, …).

## Requirements

### Daemon: preview endpoint (Go)

- **REQ-WP-001** `POST /v1/workbench/preview` (authenticated like the other `/v1/workbench` routes). Body: `{"session": "<id>", "link": "<text>"}`. `link` is what the terminal reports: an absolute path, `~/…`, a relative path, or a `file://` URL, with an optional `:line` or `:line:col` suffix. A relative path is tried, in order, against the session's working directory, then the git root of that directory, then `dir` if the body gives one (the client sends the space folder). The first regular file that exists wins.
- **REQ-WP-002** The response is `{"path", "name", "dir", "kind", "size", "line", "col", "url", "raw_url"}`. `kind` is one of `html`, `markdown`, `image`, `pdf`, `text`, `binary`. Images are png, jpg, jpeg, gif, webp, svg. Text is decided by extension or a UTF-8 sniff of the first 8 KB. Files over 5 MB that are not images or PDF are `binary`. Errors use the daemon envelope `{"error": {"code": "...", "message": "..."}}`:
  - 400 `invalid_link` for a link that is not a local file (for example `http(s)://`);
  - 403 `forbidden_path` on POST for a refused or not-allowed path (GET answers 404 instead, REQ-WP-004);
  - 404 `not_found`: on POST the message is `no such file: <path>` for a missing file or a directory; on GET the message is `not found` (REQ-WP-004);
  - 500 `internal_error` for a token or render failure.

  An empty `session` (after trimming) skips the session lookup: `cwd` is empty, an absolute, `~/` or `file://` link resolves as usual, and a relative link is tried against `dir` alone (404 `no such file: <path>` when `dir` is empty). A non-empty unknown session keeps its not-found error.
- **REQ-WP-003** Allowed paths: under the daemon user's home folder and the temp folders (`/tmp`, `/private/tmp`, `$TMPDIR`). Always refused, with 403: anything under `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.config/gcloud`, any `secrets.env`, and any `.env` / `.env.*` file. The check runs on the path with symlinks resolved.
- **REQ-WP-004** `url` and `raw_url` point at `GET /v1/workbench/preview/{token}/{name}`. The token is random (128 bit), scoped to the file's folder, and kept in memory for 30 minutes. It is the only credential: the route needs no cookie, so a sandboxed iframe (opaque origin) can load the page and its relative assets. Requests outside the token's folder, with an unknown or expired token, or for a refused path (REQ-WP-003) get 404. There are no directory listings. Symlinks that leave the folder are not followed.
- **REQ-WP-005** `url` has `?view=1` for markdown, text and image kinds. The daemon then returns a generated HTML page:
  - markdown rendered with goldmark (GFM), dark style: background `#1D1C1B`, text `#ECEAE5`, code blocks `#262523`, Geist or system font;
  - text as a `<pre>` with line numbers in a monospace font, the `#L<n>` line highlighted and scrolled into view (the response's `url` carries `#L<line>` when a line was given);
  - an image centred on the dark background, max 100% width and height.

  HTML, PDF and raw images are served as they are, with their content type.
- **REQ-WP-006** Every preview response carries `Content-Security-Policy: sandbox allow-scripts allow-forms allow-popups; frame-ancestors 'self'`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store` and `Referrer-Policy: no-referrer`. A previewed page therefore runs in an opaque origin even when it is opened directly, and it cannot read Lana's cookies or call its API. The global security headers must not block same-origin framing of this route; this route alone may be framed by `'self'`.
- **REQ-WP-007** The rendering code (link parsing, kinds, markdown, text and image pages) lives in one package of the root module, `internal/workbench/preview`, with unit tests.

### Web: file preview (TypeScript)

- **REQ-WP-010** In a web Workbench terminal, ⌘+click (Ctrl+click on non-macOS; a tap on touch) on a file path opens it in the preview. Paths are found by an xterm.js link provider using the same rules as the native app: absolute, `~/`, `./`, `../`, one with a slash, or a file name with an extension, with an optional `:line[:col]` suffix. OSC 8 `file://` links go to the preview too. Other OSC 8 links and URLs keep their current behaviour.
- **REQ-WP-011** The client calls REQ-WP-001 with the pane's session and the space folder. A 404 or 403 shows a short toast and opens nothing.
- **REQ-WP-012** On desktop widths the preview is a panel at the right of the panes, 45% wide by default, with a draggable divider; the width is kept per browser. On a phone (the existing `PHONE_QUERY`) it is a full-screen sheet with a close button. One file shows at a time; a new click replaces it. Esc or the close button closes it. The panel is not part of the shared layout document.
- **REQ-WP-013** Panel header: file name, folder (`~`-shortened), buttons Open in new tab (opens `raw_url`) and Download (the raw file), and Close. The body is an `<iframe sandbox="allow-scripts allow-forms allow-popups">` (never `allow-same-origin`) showing `url`. A `binary` kind shows the name, size and a Download button instead.

### Web: pane polish (TypeScript, CSS)

These match what the native app has now (spec 58 branch, 2026-11-21).

- **REQ-WP-020** The focused pane is clearly brighter than the others: unfocused card `#1A1918`, focused card `#2A2826`, focused border `rgba(255,255,255,0.14)` against `0.05`. The unfocused terminal text is dimmed to about 0.62 opacity. Focus never shows as a coloured border.
- **REQ-WP-021** A new terminal (new tab or split) takes the focus and the keyboard at once.
- **REQ-WP-022** Closing the focused pane moves the focus at once to the pane the user focused last in that tab. The client keeps a per-tab focus history in memory. With no history, the focus goes to the previous pane in tree order, else the next. Closing an unfocused pane leaves the focus where it is. The same rule applies when a session disappears through an event.
- **REQ-WP-023** Panes fade in when created (about 180 ms, opacity 0 to 1, scale 0.98 to 1) and fade out when closed (about 160 ms) before the kill request goes out. Closing a whole tab has no fade. `prefers-reduced-motion` turns both off.
- **REQ-WP-024** Splits are equal, as the web already does: a pane added to a split, or closed in it, leaves its siblings equal shares.
- **REQ-WP-025** Tabs in the tab bar have a fixed width of 176 px. They shrink only when the bar has no room, down to 96 px. Labels are ellipsized.
- **REQ-WP-026** The Agents list in the side panel shows the row of the focused pane's session as active, styled like the active space row.

### Program and agent tiles (web and desktop)

- **REQ-WP-030** A program is shown as a landscape tile, height h and width round(1.32·h): 21×16 in tabs, 24×18 in pane headers and the Agents list. Corner radius 0.28·h, a top sheen (white 18% to transparent over the top 60%), and a soft shadow. The glyph is centred at about 0.62·h for a stroke icon and 0.66·h for a logo.
- **REQ-WP-031** In a tab of several panes, the focused pane's tile is in front with its glyph. Up to two other panes' tiles peek out behind it, each 4 px further right and 0.8 px smaller at the top and bottom per step, without a glyph.
- **REQ-WP-032** The tile table is the same in both clients. It lists a background and glyph colour for each program:
  - plain shells (zsh, bash, fish, sh, …): `#2E2D2B` with a 1 px `rgba(255,255,255,0.08)` border and a green `#5CBA86` `>_` prompt glyph;
  - claude: `#D97757`, white Claude sparkle;
  - codex: `#F7F7F7` with a thin dark border, `#1A1A1A` OpenAI/Codex mark;
  - cursor, grok, opencode, cline, mastracode, hermes: `#1A1A1A`, white mono mark;
  - copilot: `#24292F`, white mark;
  - agents whose logo is multi-colour (gemini, antigravity, devin, kimi, kiro, amp, qwen, qodercli, muse, kilo): neutral `#2E2D2B` tile with the colour logo;
  - pi and omp (Oh My Pi): `#1A1A1A` tile with a white `π` glyph; omp has a small purple `#7652C2` dot or accent to tell them apart;
  - agents without a logo (droid, letta, maki, …) and any other program: a monogram on a tile coloured by a hash of the name, from a fixed palette.

  The logos come from the LobeHub set already in both clients (MIT). A monochrome variant is used wherever a white or dark glyph is drawn.
- **REQ-WP-033** The tile is the same in tabs, pane headers, the Agents list and the space/tab switcher, in both clients.

### Desktop app (Go, `apps/desktop`, spec 58 branch)

- **REQ-WP-040** The native app's preview calls REQ-WP-001 on the daemon it is connected to, and shows `url` in its web view. Its own loopback server and local file reading are removed. Open in default app and Reveal in Finder stay, for a local daemon only: they are hidden when the daemon is remote.
- **REQ-WP-041** The native tile table follows REQ-WP-030…033.

## Tests

- **REQ-WP-050** Go: `internal/workbench/preview` covers link parsing, kinds, markdown, text and image pages. `web` handler tests cover: POST with relative, `~/`, absolute and `:line` links; the git-root fallback; 404 for a missing file; 403 for refused paths, including through a symlink; token scope (a sibling folder gives 404), expiry and an unknown token; the response headers of REQ-WP-006; and that the GET route works without a cookie while the POST route needs one.
- **REQ-WP-051** Web: vitest covers the link matcher (the path rules, `:line:col`, URLs not matched), the focus-history choice on close, and the tile table (shell, claude, a mono agent, a colour-logo agent, pi/omp, an unknown program). `tsc` and the web build pass.
- **REQ-WP-052** Desktop: `go test ./...` in `apps/desktop` passes. The preview tests use an `httptest` daemon.

## Out of scope

Editing files in the preview. Previewing a directory. Following links inside a previewed page to other local files outside its folder.
