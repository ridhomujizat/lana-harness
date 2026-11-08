# Requirements: macOS menu bar app

- **Issue**: — (owner request 2026-11-08)
- **Status**: Approved (owner request; the details are agent recommendations, approved under the
  owner's standing "ikuti rekomendasimu")
- **Revision**: 1
- **Owner**: ridho
- **Contract**: in this file ([Contract](#contract)) — one optional query parameter on the local
  control endpoint, one dashboard hash parameter, and the app's files and flags
- **Related ADR**: [0013](../../adr/README.md) (the lana binary stays CGO-free)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | "untuk macos bisa di setup ada logo lana di navbar? pas di click ada menu chat, workflow dan calendar. lalu ada usage ram dan cpu dalam penggunaan lana di laptop ini." | goal, menu items, usage |
| Spec 18 | [friendly web login](../18-friendly-web-login/requirements.md) REQ-WLG-006, API-WLG-008 | `daemon.secret`, `POST /v1/local/sign-in-link` |
| Spec 36 | [self-update](../36-self-update/requirements.md) | in-place restart keeps the daemon PID |

## Goal

On a Mac, the lana mark sits in the menu bar. One click shows whether lana runs, how much CPU and
memory lana uses on this Mac, and opens Chat, Workflows, or Calendar in the browser. The owner can
start and stop the daemon from there.

## Agent recommendations (approved)

| Topic | Recommendation |
|---|---|
| Where the code lives | A separate Swift package, `apps/macos/LanaMenu`, built with the Xcode toolchain. No Go systray library: the lana binary stays `CGO_ENABLED=0` (ADR 0013) and `make build` never needs Swift |
| How it talks to lana | Only `$LANA_HOME` files (`config.toml`, `daemon.lock`, `daemon.secret`) and the loopback HTTP API. No new daemon endpoint |
| Menu style | A SwiftUI `MenuBarExtra` window (macOS 13+), styled like a native menu, because the usage block and sparklines do not fit an `NSMenu` |
| Usage source | libproc (`proc_pidinfo`, `proc_listchildpids`, `proc_pidpath`) — no `ps` per tick |
| What "lana" counts | The daemon process and all its descendants (Claude Code, MCP servers, tool commands) |
| Version | `<daemon executable> --version`, read once per executable and modification time, so it is the running binary's version |
| Sign-in | A separate "Sign In with One-Time Link…" item, and ⌥-click on a page item. The app cannot see the browser cookie, so it never guesses whether the browser is signed in |
| Binary | The setting, else `lana` on the login shell PATH, else `~/.local/bin`, `~/go/bin`, `/opt/homebrew/bin`, `/usr/local/bin` |
| Packaging | `make macos-app` → `dist/Lana.app`, ad-hoc signed; nothing is installed or copied to `/Applications` |

## Behavior rules

### REQ-MAC-001: Menu bar item

The app shall show the lana mark as a monochrome template image in the menu bar, with no Dock icon
(`LSUIElement`). The bundle identifier is `dev.lana.menubar` and the display name is `lana`.

### REQ-MAC-002: Status header

The menu shall show "Running · v<version> · up <uptime>" while the daemon runs, else "Not running".
The daemon runs when the PID in `$LANA_HOME/daemon.lock` is alive and started no later than the lock
file was last written (a reused PID never counts). Uptime is the daemon process age.

- `AC-MAC-001`: Given the demo daemon on 127.0.0.1:18399, the header shows Running, its version, and
  its uptime.
- `AC-MAC-002`: Given a lock file whose PID is not running, the header shows Not running.

### REQ-MAC-003: Usage on this Mac

While the daemon runs, the menu shall show CPU % and resident memory of the daemon plus all its
descendant processes, RAM as a share of physical memory, and the descendant count with Claude Code
processes named. CPU % is the CPU time delta of the tree over the wall-clock delta between two
samples (100 % is one core). The app samples every 2 seconds while the menu is open and stops when it
closes. Sparklines show the last minute of samples.

- `AC-MAC-003`: RAM matches the sum of `ps -o rss=` over the same processes within 10 %.
- `AC-MAC-004`: A child process named `claude` reads "+ 1 Claude Code process".

### REQ-MAC-004: Pages

Chat, Workflows, and Calendar (then Open Dashboard and Router) shall open
`http://<addr>/#/chats`, `#/workflows`, `#/calendar`, `#/home`, `#/router` in the default browser.
`<addr>` is the bound address from `daemon.secret` while the daemon runs, else `[web] addr` from
`config.toml` (re-read on every menu open), else `127.0.0.1:18300`.

### REQ-MAC-005: Sign-in link

"Sign In with One-Time Link…" shall request `POST /v1/local/sign-in-link` with the secret from
`daemon.secret` and open the returned link. ⌥-click on a page item does the same with `next` set to
that page. The link sets a new dashboard password; a browser that is already signed in goes straight
to `next`.

### REQ-MAC-006: Start and stop

Start lana shall run `<binary> daemon` in its own session with `LANA_HOME` and the login shell PATH,
stdin from `/dev/null`, stdout and stderr appended to `$LANA_HOME/menubar-daemon.log`. Stop lana
shall ask for confirmation and send SIGTERM to the daemon PID (the daemon drains and stops).

### REQ-MAC-007: Settings

A settings window shall set `LANA_HOME` (default: the `LANA_HOME` the app was launched with, else
`~/.lana`), the lana binary path, and Launch at login (`SMAppService.mainApp`).

### REQ-MAC-008: Packaging

`make macos-app` shall build the release binary and assemble `dist/Lana.app` with `Info.plist`
(version from `VERSION`), an `.icns` icon drawn from the logo when `iconutil` exists, and an ad-hoc
signature. It shall not change `make build` or the CGO-free Go build.

## Contract

### API-MAC-001: `next` on the local sign-in link (extends API-WLG-008)

`POST /v1/local/sign-in-link?next=<hash>` — `next` is optional. When `web.SafeNextHash` accepts it,
the link is `http://<addr>/#/welcome?code=<code>&next=<form-encoded hash>`; otherwise `next` is
ignored and the link is unchanged. Accepted: starts with `#/`, at most 256 bytes, only
`A-Z a-z 0-9 / - _ . ~ ? = & % +`, not `#//…`, and not `#/welcome`, `#/login`, or `#/forgot`.

### API-MAC-002: Dashboard `next`

After a successful claim on `#/welcome?code=…&next=…`, the dashboard opens `next` when
`safeNextHash` (same rule) accepts it, else `#/chats`. A browser that is already signed in and opens
such a link goes to `next` the same way.

### API-MAC-003: App flags

| Flag | Effect |
|---|---|
| (none) | Run the menu bar app |
| `--dump-status [--samples N]` | Print the status, usage, resolved binary, and page links as JSON |
| `--snapshot PNG [--dark] [--seconds N]` | Render the open menu with live data to a PNG |
| `--write-iconset DIR` | Write the icon PNGs for `iconutil` |

## Not included

- An Intel or universal build (the app builds for the host architecture).
- Notarization or a signed release asset.
- Showing whether the browser is signed in.
- Windows and Linux tray apps.
