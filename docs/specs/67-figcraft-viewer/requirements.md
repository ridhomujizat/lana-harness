# Spec 67: FigCraft viewer in Workbench Files

Status: Approved r1, 2026-11-22. Owner: approved in chat ("ya boleh lanjut kerjakan"), including the live mode. Design: FigCraft page "Workbench · FigCraft viewer" (id 74177) in `design/lana-design-system.figcraft`, PNGs `design/export/figcraft-viewer-*.png`.

## Problem

The owner designs Lana in FigCraft (`.figcraft` files in the repo), often with an agent editing the file. They want to see those designs from Lana web, Lana Desktop and the phone over Tailscale. Today the Workbench Files tab shows a `.figcraft` file as "binary".

They only need the canvas and the page and layer lists. They do not need the properties panel or editing.

## Decisions (owner, 2026-11-22)

1. **A read-only viewer in Lana, not the FigCraft web build.**
   - The web build was checked and works in host mode. It was rejected for three reasons:
     - it always shows its full editor UI;
     - it is a 25 MB wasm download;
     - running third-party code on the Lana origin would reach the Lana API.
   - Instead, the daemon renders with `figcraft-cli`, and the browser only shows PNGs and lists.
2. **Location.** The viewer replaces the code viewer when a `.figcraft` file is selected in Workbench Files (spec 66). It has two layouts:
   - **Compact** (normal panel): canvas plus a page dropdown. A Layers button opens the Pages + Layers column.
   - **Wide** (maximized panel): the Pages + Layers column is shown by default.

   On a phone, the canvas fills the viewer and Pages | Layers open as a sheet.
3. **Always the latest.** Refreshing, or reloading the browser, shows the file as it is on disk now. The viewer also re-renders by itself when the file changes. A file that is half written is never shown.
4. **Live mode.**
   - When the FigCraft app has the file open with a control port (`figcraft --control PORT`), the viewer reads the open document instead of the file. Edits that are not saved yet then show too, including an agent's edits made over `figcraft-cli mcp --attach`.
   - Live mode is on by default and can be switched off.

## Requirements

### Daemon: FigCraft access (Go, new package `internal/workbench/figcraft`)

- **REQ-FV-001 CLI location.** The CLI is found in this order:
  1. the `cli_path` setting (REQ-FV-050);
  2. `~/.local/share/figcraft-cli/figcraft-cli`;
  3. `figcraft-cli` on the daemon's `PATH` (`internal/pathenv`).

  The version comes from `figcraft-cli --version` (`figcraft-cli X.Y.Z`). The result is cached for 60 s.
- **REQ-FV-002 App version.** On macOS, the version of `/Applications/FigCraft.app` comes from its `Info.plist` (`CFBundleShortVersionString`), cached for 60 s. When the CLI is older than the app (semver compare), Info carries the warning `cli_older_than_app`.
- **REQ-FV-003 Running the CLI.**
  - Every run uses `exec.CommandContext`, never a shell, with a 30 s timeout.
  - Every run passes `--automation-read-root` for the file's folder and for a private temp folder of the daemon, and `--automation-write-root` for that temp folder only. Script files and outputs live in that temp folder and are removed afterwards.
  - At most 2 CLI processes run at once. Identical requests in flight share one run (singleflight).
- **REQ-FV-004 File mode, pages and top-level layers.** One `figcraft-cli run` with `page.list` followed by `node.get` for each page returns:
  - the pages (`id`, `name`, `divider`, `children`);
  - each page's top-level layers (`id`, `name`, `type`, `bounds`: absolute x/y/width/height, `child_count`, `visible`).

  The page's **origin** is the top-left corner of the union of its visible top-level layer bounds. A render's pixel (0,0) is that origin.
- **REQ-FV-005 File mode, children.** One `figcraft-cli run` with `node.get` lists one node's children, with the same fields.
- **REQ-FV-006 File mode, render.**
  - A page: `figcraft-cli render <file> -o <tmp>.png --page <name> --scale <s>`.
  - One layer: `figcraft-cli export <file> -o <tmp>.png --format png --node <id> --scale <s>`.
  - The scale is clamped to 0.1–4. For a page, it is lowered further so the longer side stays at or under 8,192 px.
  - The response carries the origin (page: REQ-FV-004; node: its bounds' x and y), the scale and the pixel size.
- **REQ-FV-007 Stable file.** The file fingerprint is `mtime` (ns) plus size.
  - If the file changed less than 400 ms ago, the daemon waits until two stats 200 ms apart agree, for at most 3 s, before running the CLI.
  - When a CLI run fails on a file that is changing (a zip being written), the daemon answers with the last good result for that file and `stale: true`, and the client tries again on its next poll.
- **REQ-FV-008 Cache.** Pages, children and renders are cached in memory, keyed by the absolute path, the fingerprint and the request (page or node, scale). An LRU caps the total at 128 MB. A new fingerprint makes the old entries unused; they age out.
- **REQ-FV-010 Live mode, discovery.** Control ports come from the `live_ports` setting plus, on macOS, every TCP port a process named `FigCraft` listens on at 127.0.0.1 (`lsof -nP -a -iTCP -sTCP:LISTEN -c FigCraft -Fn`), cached for 5 s. For each port, `engine.execute {command: "doc.list"}` gives the open files and their `path`. A file whose path (after `filepath.EvalSymlinks`) is open in an app is in live mode with that port and that document.
- **REQ-FV-011 Live mode, protocol.**
  - The client speaks the FigCraft control protocol: newline-delimited JSON over TCP to 127.0.0.1 only.
  - It may send only these read-only requests; anything else is refused in code before it reaches the socket:
    - `engine.execute` with the commands `doc.list` and `page.list`;
    - `document.inspect`;
    - `node.get`;
    - `ui.render`.
  - Every request names the `document` by path.
  - Timeouts: 10 s per request, 60 s for `ui.render` (sent as `timeoutMs`). One connection per port is reused, and it is redialled after an error.
- **REQ-FV-012 Live mode, data.**
  - Pages come from `page.list`.
  - Layers come from `node.get {id, depth: 1, fields: ["name","type","absoluteBounds","visible"]}`.
  - Renders come from `ui.render {ids, scale}` (PNG base64). The `ids` are the page's visible top-level layers, or the one node.
  - The fingerprint is a hash of `document.inspect {depth: 0, fields: []}`'s `dirty` and `history` (undo labels and redo count) and of the page summaries. `dirty` is returned as `unsaved`.
- **REQ-FV-013 Fallback.** If live mode fails (app closed, port gone, timeout), the request is answered in file mode and Info carries `live_error`. Live mode is never used when the `live` setting is off.

### Daemon: HTTP (`web` package)

- **REQ-FV-020 Routes.** New authenticated routes under `/v1/workbench/figcraft/…`, listed in [api-contract.md](api-contract.md).
  - They return 503 `workbench_unavailable` when the Workbench is off, like the other Workbench routes.
  - Requests name the file as `worktree` + `path` (relative), the same as the spec 66 file route. The resolved absolute path must:
    - pass the spec 61 path policy (403 `forbidden_path`);
    - lie inside the worktree;
    - end in `.figcraft` (400 `not_figcraft`).
  - A missing CLI is 424 `figcraft_cli_missing`, with the paths that were tried.
- **REQ-FV-021 Info.** Info returns:
  - the source (`file` or `live`, with the port);
  - the fingerprint, `unsaved` and `stale`;
  - the pages with their top-level layers and origin;
  - the CLI path and version, and the app version;
  - the warnings.

  `ETag` is the fingerprint. `If-None-Match` with the same value is 304, so polling is cheap.
- **REQ-FV-022 Render.** `GET …/render` answers `image/png` with these headers:
  - `X-Figcraft-Origin-X`, `X-Figcraft-Origin-Y`, `X-Figcraft-Scale`, `X-Figcraft-Fingerprint`;
  - `X-Figcraft-Stale: 1` when it is the last good render.

  With `fp=<fingerprint>` in the URL and a fresh result, it is `Cache-Control: private, max-age=86400, immutable`. Otherwise it is `no-store`.
- **REQ-FV-023 Open in FigCraft.** `POST …/open` runs `open -a FigCraft <file>` on the daemon's Mac (macOS only, else 501 `unsupported`). It is logged at info level.
- **REQ-FV-024 Logging.** Every CLI and live request is logged at debug level with the file, the action and the duration. Failures are logged at warn level.

### Settings

- **REQ-FV-050** The settings live in `<LANA_HOME>/workbench-figcraft.json`: `{cli_path, live, live_ports}`. `GET` and `PUT /v1/workbench/figcraft/settings` read and write them. The defaults are `"" , true, []`. A `cli_path` that is set must be an absolute path to an executable file (400 `invalid_cli_path`).

### Web (TypeScript)

- **REQ-FV-060 Entry.** In Workbench Files, selecting a file whose name ends in `.figcraft` shows the FigCraft viewer instead of the file viewer. The tree shows such files with a pen-tool icon in violet. The spec 66 file route is not called for them.
- **REQ-FV-061 Header.** The header shows:
  - the pen-tool icon, the file name, the folder and the git status letter;
  - a **Live** chip when the source is live (amber "Live · unsaved" while `unsaved`);
  - the Layers toggle (compact layout only);
  - ⋯, with the items Refresh, Open in FigCraft (Mac), Copy path, "Live from the FigCraft app" (checkbox, REQ-FV-050) and "figcraft-cli path…".
- **REQ-FV-062 Toolbar.**
  - A page dropdown: page name and `n/N`. It lists every page; dividers are shown as separators.
  - Zoom out, the zoom percentage (click = 100%), zoom in, and Fit.
  - In the wide layout, the toolbar also shows the breadcrumb of the page and the selected layer with its size.
- **REQ-FV-063 Pages + Layers column (248 px).**
  - **Pages:** the list with the current one highlighted.
  - **Layers:** a lazy tree of the current page.
    - Top-level layers come from Info. Children load on expand (REQ-FV-005, through …/layers).
    - Rows show a chevron, a type icon (frame `#`, text `T`, rectangle, ellipse, image, component, instance, group, other) and the name, truncated.
    - Hidden layers are dimmed.

  The column is shown by default when the viewer is at least 900 px wide. Narrower, the Layers button opens it over the canvas from the left. On a phone (under 640 px), it is a bottom sheet with Pages | Layers segments.
- **REQ-FV-064 Canvas.**
  - **Background:** the canvas uses the page's background colour when Info has it, else `#E4E7EC`.
  - **Image:** the page render is drawn at `zoom / renderScale`, positioned from the page origin.
  - **Pan:** drag, wheel or trackpad scroll.
  - **Zoom:** ⌘/Ctrl + wheel, trackpad pinch, or two-finger pinch on touch. Zoom stays between 2% and 800%, centred on the pointer.
  - **Keys:** Shift+1 fits the page, Shift+2 zooms to the selection, Shift+0 zooms to 100%.
  - **Default view:** the page fitted with 24 px padding.
  - **Labels:** top-level frame names are drawn above their frames in 11 px; the selected one is in violet.
- **REQ-FV-065 Selection.**
  - Clicking a layer row, or clicking the canvas, selects a layer.
  - A canvas click picks the deepest loaded visible layer under the point; top-level layers are always loaded.
  - Double-click, or the row's zoom action, zooms to that layer.
  - The selection is drawn as a 2 px violet outline, with a size chip (`W × H`) under it.
  - Selection, page and zoom are kept per file in session storage, so a refresh lands in the same place.
- **REQ-FV-066 Detail render.** When the zoom makes the page render blurry (zoom × devicePixelRatio > renderScale × 1.25) and one top-level frame covers most of the visible canvas, the client requests that frame as a node render at `min(4, zoom × dpr)`, rounded up to 0.5 steps. It draws that render over the page render. The request is debounced by 250 ms, and only the latest one is kept.
- **REQ-FV-067 Freshness.**
  - While the viewer is visible, the client polls Info every 2 s with the ETag; it does not poll while hidden.
  - On a new fingerprint, it fetches the new render, keeps the old image until the new one is loaded, keeps the page, the selection and the zoom, and shows a toast for 2.5 s: "Updated from the file" or "Updated from FigCraft (unsaved)".
  - The panel's refresh button and the ⋯ Refresh item force an Info request without the ETag.
- **REQ-FV-068 States.**
  - **Loading:** skeleton blocks at the top-level layer positions and a spinner with "Rendering <page>…".
  - **Stale:** the last good image stays, and a small "Retrying…" note shows in the footer.
  - **CLI missing:** an alert icon, the message, the tried paths, a "Set path…" button (REQ-FV-061) and Retry.
  - **CLI older than app:** an amber banner, "figcraft-cli X is older than FigCraft Y; the render can differ", with a link that copies the update instructions.
  - **Error:** the message and Retry.
- **REQ-FV-069 Footer.** The footer shows "N frames · render T s · read-only", the source when live, and "rendered HH:MM".

## Tests

- **REQ-FV-100 Go, `internal/workbench/figcraft`.** These tests use a fake CLI (a test binary or script that writes canned JSON or PNG and records its arguments) and a fake control server (`net.Listen` on 127.0.0.1). They cover:
  - CLI location order;
  - version parse and compare;
  - the automation roots on every run;
  - script and output cleanup;
  - the page origin;
  - the scale clamp;
  - the stable-file wait;
  - stale fallback on failure;
  - the cache keyed by fingerprint;
  - singleflight;
  - live discovery from `live_ports`;
  - the method allowlist (a refused method never reaches the server);
  - the fingerprint changing with the history;
  - live-to-file fallback.
- **REQ-FV-101 Go, `web`.** These tests cover:
  - each route's happy path with a fake runner;
  - 403 `forbidden_path`, 400 `not_figcraft`, a path outside the worktree, 424 `figcraft_cli_missing`;
  - the ETag 304;
  - the render headers;
  - settings validation.
- **REQ-FV-102 Web, vitest.** The pure model is tested:
  - fit, zoom around a point, and the zoom clamp;
  - page-to-screen transforms;
  - hit-testing that picks the deepest layer;
  - when to request a detail render;
  - the layer type icon map.

  `tsc` and the web build pass.

## Out of scope

- Editing.
- The properties panel.
- Comments.
- Prototype playback.
- Multiplayer.
- Formats other than `.figcraft` (`.fig` and `.sketch` could use the same CLI later).
- Starting FigCraft with a control port for the owner.
