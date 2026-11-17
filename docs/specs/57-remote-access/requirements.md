# Spec 57: Remote access over Tailscale

- **Status**: Approved (owner, 2026-11-17: "approve dan langsung implement after tulis doc ya")
- **Branch**: `feature/remote-access`, from `development` (0.5.0-beta.7, 7d592c2)
- **Design**: figcraft page "Remote access (Tailscale)" in `design/lana-design-system.figcraft`. Frames: "Settings – Remote access", "Settings – Remote access · setup", "Mobile · Workbench remote – view only", "Mobile · Unlock control (sheet)", "Mobile · Sign in via Tailscale", "Mac · New remote sign-in notification". PNGs: `design/export/remote-access-{1..6}-*.png`.

## Problem

The owner wants to open Lana from their phone, including Workbench terminals. Today the daemon only answers on its bind address (`127.0.0.1:18300`): `checkHost` requires `Host == addr`, and `checkOrigin` and the Workbench WebSocket attach require `Origin == http://addr`. Workbench is a full shell on the Mac, so remote access needs more than the Lana password.

## Decisions (owner, 2026-11-17)

1. Remote access goes only through **Tailscale Serve**: `https://<mac>.<tailnet>.ts.net` → `http://127.0.0.1:18300`. The daemon keeps listening on loopback. Lana never uses Tailscale Funnel.
2. Controls live in **Settings › Remote access**, a new tab after General.
3. The **Enable** button runs `tailscale serve` itself.
4. Remote terminal default: **Control after confirming**. A remote browser sees terminals read-only until it unlocks control with the password or a Telegram code. Control lasts 15 minutes by default.
5. Remote requests must carry an allowed Tailscale login (`Tailscale-User-Login`), then the Lana password.

## How a request is classified

Tailscale Serve proxies to the daemon from tailscaled on the same Mac. It keeps the original `Host`, and adds `X-Forwarded-Host`, `X-Forwarded-Proto: https`, `X-Forwarded-For: <tailnet client IP>`, and, for requests from a user's (not a tagged) device, `Tailscale-User-Login`, `Tailscale-User-Name`. Tailscale strips incoming `Tailscale-User-*` headers from the client.

- **REQ-RA-001** A request is **remote** when its TCP peer is a loopback address and at least one of these holds: it has `X-Forwarded-For`, it has `Tailscale-User-Login`, or its host (below) differs from the bind address. Every other request is **local** and keeps today's behavior. The forwarded and Tailscale headers are read only from a loopback peer; from any other peer they are ignored.
- **REQ-RA-002** The remote host is `X-Forwarded-Host` when present, else `Host`, lower-cased, without a port suffix `:443`.
- **REQ-RA-003** A remote request is refused with 403 `remote_disabled` (JSON for `/v1/*`, a short plain-text page otherwise) when any of these holds:
  - remote access is off;
  - the remote host differs from the configured `host`;
  - `require_identity` is on and `Tailscale-User-Login` is missing or not in `allowed_logins` (case-insensitive).
  This check runs before every route, including `/`, `/assets/`, and the unauthenticated `/v1/auth/*` routes.
- **REQ-RA-004** For an allowed remote request, `checkHost` accepts the configured host, and `checkOrigin` (and the Workbench attach Origin check) accepts exactly `https://<host>`. Local requests keep `http://<addr>`.

## Configuration

- **REQ-RA-010** A new `[web.remote]` table in `config.toml`:

  | key | type | default | meaning |
  |---|---|---|---|
  | `enabled` | bool | `false` | remote requests are allowed |
  | `host` | string | `""` | the ts.net name, e.g. `ridho-mbp.tail3f2a1.ts.net` |
  | `require_identity` | bool | `true` | REQ-RA-003 identity check |
  | `allowed_logins` | []string | `[]` | Tailscale logins, e.g. `name@github` |
  | `terminal` | string | `"confirm"` | `view`, `confirm`, or `full` |
  | `confirm_with` | string | `"password_or_telegram"` | or `password` |
  | `control_minutes` | int | `15` | 1–240 |
  | `idle_minutes` | int | `60` | 5–1440 |
  | `max_age_hours` | int | `168` | 1–2160 |
  | `notify_sign_in` | bool | `true` | notification on a remote sign-in |

  Invalid values fail config validation with the key name. `enabled = true` with an empty `host` is invalid.
- **REQ-RA-011** The daemon holds the remote settings in memory. A save through the API writes `config.toml` (the `[web.remote]` table only, same versioned rewrite as `PUT /v1/web`) and takes effect at once, without a restart.

## Browser sessions

- **REQ-RA-020** A session started by a remote request records `remote: true`, `login` (Tailscale login), `device` (Tailscale machine name from `tailscale whois`, best effort, empty when unknown), `ip` (`X-Forwarded-For`), and `control_until` (Unix ms, 0 = locked). These fields are stored in `web-sessions.json` with `omitempty`; old files load unchanged.
- **REQ-RA-021** A remote session ends after `idle_minutes` without a request, or `max_age_hours` after it started, whichever comes first. Local sessions keep the 12-hour idle limit.
- **REQ-RA-022** A remote session is valid only for remote requests with the same login. A local request with a remote session's cookie, or a remote request whose login differs, gets 401 `unauthenticated`.
- **REQ-RA-023** The session cookie gets `Secure` when the request is remote (it arrived over HTTPS through Serve).
- **REQ-RA-024** `GET /v1/auth/sessions` adds `remote`, `login`, `device`, `ip`, and `control_until` to each row.
- **REQ-RA-025** `POST /v1/auth/sessions/{id}/revoke` ends one session by its 12-hex display id (404 `not_found` if unknown; the caller's own session is allowed and signs it out). `POST /v1/auth/sessions/revoke-remote` ends every remote session and returns `{revoked}`.
- **REQ-RA-026** `GET /v1/auth/session` adds `remote` (bool), `login`, `device`, `terminal` (the policy), `control_until`, and `telegram_code` (true when a Telegram code can be sent: `confirm_with` allows it and the Telegram reset sender is available).
- **REQ-RA-027** `GET /v1/auth/state` adds `remote` (bool) and, for a remote request, `login` and `device`, so the sign-in page can show the identity chip.

## Terminal control from remote

- **REQ-RA-030** For a remote session, Workbench control is allowed when `terminal = full`, or `terminal = confirm` and `control_until > now`. Otherwise it is locked.
- **REQ-RA-031** While control is locked, these answer 403 `control_locked`: `POST /v1/workbench/sessions` (create), `POST /v1/workbench/sessions/{id}/restart`, `DELETE /v1/workbench/sessions/{id}`, `POST /v1/workbench/sessions/{id}/resize`, and the WebSocket attach with `mode=control`. View attach, list, events and layout stay allowed.
- **REQ-RA-032** `POST /v1/auth/control/unlock` with `{password}` or `{code}` sets `control_until = now + control_minutes` on the caller's session and returns `{control_until}`. A wrong password counts toward the login lockout (5 tries in 5 minutes) and answers 401 `invalid_password`. A `code` is accepted only when `confirm_with = password_or_telegram`. It is a 6-digit code from `POST /v1/auth/control/telegram`, valid for 5 minutes, single use, at most 5 tries. A wrong code answers 401 `invalid_code`. Only remote sessions with `terminal = confirm` may call it; others get 409 `not_needed`.
- **REQ-RA-033** `POST /v1/auth/control/telegram` sends a new code to the owner's Telegram private chats. It returns 202 `{sent_to}`, or 409 `telegram_unavailable`. It can be called at most once a minute per session (429 `too_many_attempts`).
- **REQ-RA-034** `POST /v1/auth/control/lock` sets `control_until = 0`.
- **REQ-RA-035** When control expires or is locked, the server closes that session's control attachments with WebSocket close code 4403 and reason `control locked`. The check runs at least every 5 seconds. The client then reattaches in view mode.

## Settings that a remote browser cannot change

- **REQ-RA-040** A remote request gets 403 `remote_forbidden` on: `PUT /v1/web`, `PUT /v1/web/remote`, `POST /v1/web/remote/enable`, `POST /v1/web/remote/disable`, `POST /v1/auth/password`, and `POST /v1/auth/sessions/revoke-others`. A stolen phone cannot loosen remote access or lock the owner out.

## Tailscale integration (`internal/tailscale`)

- **REQ-RA-050** The CLI is found in this order: `tailscale` on `PATH`, `/Applications/Tailscale.app/Contents/MacOS/Tailscale`, `/opt/homebrew/bin/tailscale`, `/usr/local/bin/tailscale`. Every call has a 5-second timeout and runs without a shell.
- **REQ-RA-051** `Status(ctx)` returns:
  - `installed`, `path`, and `version` (from `version`, first line);
  - `running`: `status --json` has `BackendState == "Running"`;
  - `dns_name`: `Self.DNSName` without the trailing dot;
  - `login`: the `LoginName` of `Self.UserID` in `User`;
  - `tailnet`: `CurrentTailnet.Name`;
  - `magic_dns`: `CurrentTailnet.MagicDNSEnabled`;
  - `https_certs`: `CertDomains` is non-empty;
  - `serving`: `serve status --json` has a TCP 443 HTTPS handler whose `/` proxy targets the daemon address;
  - `serve_other`: port 443 already serves something else.

  A missing binary yields `installed: false` with no error.
- **REQ-RA-052** `Serve(ctx, addr)` runs `serve --bg --https=443 http://<addr>`. `Unserve(ctx)` runs `serve --https=443 off`. Errors carry the CLI's stderr (trimmed, at most 500 characters).
- **REQ-RA-053** `Whois(ctx, ip)` runs `whois --json <ip>` and returns the machine name (`Node.ComputedName`, else the first label of `Node.Name`) and `UserProfile.LoginName`. Results are cached for 5 minutes per IP.
- **REQ-RA-054** The package takes a runner interface so tests use canned JSON. No test calls the real CLI.

## Remote access API

- **REQ-RA-060** `GET /v1/web/remote` returns `{config, status, url, checked_at}`. `config` holds the `[web.remote]` values plus `version` (the config file version for `expected_version`). `status` is REQ-RA-051. `url` is `https://<host>` when `host` is set.
- **REQ-RA-061** `PUT /v1/web/remote` with `{expected_version, require_identity, allowed_logins, terminal, confirm_with, control_minutes, idle_minutes, max_age_hours, notify_sign_in}` validates the values and saves them (REQ-RA-011). Errors use the existing `writeWebIssue` shape with the key name.
- **REQ-RA-062** `POST /v1/web/remote/enable` with `{expected_version}`:
  1. It refuses with 409 `tailscale_not_ready` and a message naming the first failing check (not installed, not running, HTTPS certificates off, or port 443 serving something else).
  2. It runs `Serve` for the daemon address.
  3. It saves `enabled = true` and `host = dns_name`. When `allowed_logins` is empty, it also saves the Tailscale `login`.
  4. It answers like GET.
- **REQ-RA-063** `POST /v1/web/remote/disable` with `{expected_version}` runs `Unserve` (an error is logged and returned in `warning`, not fatal), saves `enabled = false`, ends every remote session, and answers like GET.
- **REQ-RA-064** `POST /v1/web/remote/open-app` runs `open -a Tailscale` on macOS, and answers 501 `unsupported` elsewhere.

## Notifications and audit

- **REQ-RA-070** On every successful remote sign-in (login, claim, or reset verify), when `notify_sign_in` is on, the daemon does two things:
  - It publishes a notification bus event: kind `remote_sign_in`, title "New remote sign-in", body "<browser> on <device> (<login>) signed in over Tailscale.", URL `#/settings/remote`, sound, time sensitive. `<browser>` is a short name from the User-Agent, such as "Safari on iPhone". When the device is unknown, the body uses the IP.
  - It sends the same text to the owner's Telegram private chats, best effort.
- **REQ-RA-071** The macOS menu bar app shows `remote_sign_in` events as banners (always on, with sound). A click opens `#/settings/remote`.
- **REQ-RA-072** Audit entries (kind, target):
  - `remote.enable` / `remote.disable` (host);
  - `remote.settings` (config.toml);
  - `remote.sign_in` (login@device) and `remote.sign_in_failed` (login, on a wrong password);
  - `remote.control_unlock` (session id, with method password or telegram);
  - `remote.session_revoke` (session id).

## Web UI

- **REQ-RA-080** Settings gets a **Remote access** tab (`#/settings/remote`) after General, with the sections of the design:
  - **Remote access**: state card, On/Off switch, and status badge.
  - **Address**: URL field with Copy and Open, a QR code of the URL, and three status checks.
  - **Who can sign in**: Require Tailscale identity checkbox, and the allowed login list with remove and add.
  - **Terminal from remote**: three options; *Control after confirming* shows the Confirm with and Control lasts selects.
  - **Remote sessions**: idle and max-age selects, plus the notify switch.
  - **Signed-in devices**: table with Local/Remote badges, device, where, last active, control state, and Sign out per row, plus "Sign out all remote devices".
- **REQ-RA-081** While remote access is off and a check fails, the tab shows the **Setup** checklist instead of the Address and later sections. The steps are:
  1. Installed.
  2. Running, with an **Open Tailscale** button.
  3. HTTPS certificates, with **Open admin console**, which links to `https://login.tailscale.com/admin/dns`.
  4. Serve Lana, with **Enable**, disabled until 1–3 pass.

  Under the list are **Check again** and the "Private to your tailnet" note. When every check passes, the On switch enables directly.
- **REQ-RA-082** When the page is open in a remote browser, the controls that REQ-RA-040 forbids are disabled. A note says "Change these on the Mac."
- **REQ-RA-083** The QR code is drawn on the client from the URL. It uses a small vendored encoder, no CDN (CSP is `default-src 'self'`).
- **REQ-RA-084** The sign-in page in a remote browser shows the "via Tailscale · Verified" chip with login and device (REQ-RA-027) and the tailnet footer note.
- **REQ-RA-085** Workbench in a remote browser with locked control opens panes in view mode. It shows the bottom bar "Remote · view only / Typing is locked on this phone" with **Unlock control**. The button opens the sheet:
  - a password field;
  - "Unlock for N minutes";
  - "Send code to Telegram" when `telegram_code` is true, which switches the field to a 6-digit code;
  - the signed-in identity.

  After unlock, panes reattach in control mode, and the bar shows "Control · N min left" with **Lock**. On close code 4403 the panes reattach in view mode, and the bar returns.
- **REQ-RA-086** Workbench's existing mobile "Take over" stays as it is for local browsers.

## Out of scope

- Agents with shell tools, MCP servers, and workflows can also run commands. A remote browser reaches them after Tailscale identity and password, the same as a local one. Gating them is a later spec.
- An actionable "Sign out device" button inside the macOS banner (the click opens Settings instead).
- Funnel, custom domains, and non-macOS service managers for `tailscale serve`.

## Verification

- `nix develop -c go test ./web/... ./internal/tailscale/... ./internal/webfiles/... ./config/... ./core/...` passes. Tests cover:
  - classification and spoofed headers from a non-loopback peer;
  - Host and Origin for remote;
  - identity refusal;
  - TTLs;
  - control gate, unlock, expiry close, and the forbidden list;
  - enable and disable with a fake runner.
- `nix develop -c golangci-lint run` is clean for the changed packages.
- `cd web/ui && npm test` and `npm run build` pass.
- A manual check with Tailscale running is done once the owner starts the Tailscale app: enable from Settings, open the URL on the phone, sign in, view a terminal, unlock, type, and lock.
