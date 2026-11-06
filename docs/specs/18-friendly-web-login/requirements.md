# Requirements: Friendly web login for non-technical owners

- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related**: spec #9 (owner token, browser sessions, setup unlock step), spec #13 (Telegram owners), spec #16 (restart)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Diagnosis 2026-10-31 | The owner token is printed once to the daemon stderr (`web: owner token created … shown only once`); the login page says "Copy it from the daemon start output"; setup step 1 says "Paste the owner token from the daemon" | problem |
| Owner answers 2026-10-31 | Password login; the browser opens automatically on first start with a one-time link; reset by Telegram code, by restarting lana, and by a terminal command; local access only | `Q-001`–`Q-004` |
| Memory: lana users are non-technical | Setup must work in the web UI without a terminal or files | principle |

## Goal

A non-technical owner starts lana, a browser opens, they create a password, and from then on they log in
with that password. If they forget it, they can reset it without a terminal.

## Definitions

- **Password** — the owner's dashboard password. Stored only as an argon2id hash.
- **Sign-in link** — a one-time URL `http://<web addr>/#/welcome?code=<code>`. The code is 32 random bytes (base64url), valid 15 minutes, single use, held in daemon memory only as a SHA-256 hash.
- **Reset code** — a 6-digit code sent to the owner's Telegram, valid 10 minutes, at most 5 wrong tries.

## Scope

### Included

- Password create, login, change, and reset (Telegram code, sign-in link after restart, terminal command).
- Automatic browser opening with a sign-in link on first start and after a requested reset.
- Migration for owners who already have the token.
- Setup wizard and login page texts without "token" or "daemon output".

### Not included

- Access from other devices (non-loopback), HTTPS, multiple users, 2FA.

## Behavior rules

### REQ-WLG-001: First start opens the browser

When the daemon starts and no password is set, it shall create a sign-in link and open it in the default browser (`open` on macOS, `xdg-open` on Linux when a display is present). It also writes the link once to stderr as a fallback. `[web] open_browser = false` turns the opening off. A daemon that restarts in place (spec #16) does not open the browser again unless REQ-WLG-001 or REQ-WLG-005 applies.

#### Acceptance criteria

- `AC-WLG-001`: Given a new `LANA_HOME`, when the daemon starts, then the browser opener is called once with `http://127.0.0.1:18300/#/welcome?code=…`, and the link opens "Create your password".
- `AC-WLG-002`: Given `open_browser = false`, then the opener is not called, and the link is still on stderr.

### REQ-WLG-002: Create a password from the link

`#/welcome?code=` shall show "Create your password" (password + confirm, at least 8 characters, show/hide toggle). Submitting it with a valid code sets the password, consumes the code, signs this browser in, and continues to the setup wizard or the dashboard. An expired or used code shows "This link has expired. Restart lana to get a new one." with no form.

#### Acceptance criteria

- `AC-WLG-003`: Given a valid code, when the owner submits a matching 8+ character password, then `web.json` has `password_hash` (argon2id), the browser has a session cookie, and the same code is rejected afterwards.
- `AC-WLG-004`: Given a code older than 15 minutes, then the page shows the expired text.

### REQ-WLG-003: Log in with the password

The login page shall show "Password" and "Log in", and "Forgot password?". Wrong passwords answer "That password is not right." Five wrong tries within 5 minutes lock login for 5 minutes ("Too many tries. Wait 5 minutes or reset your password.").

#### Acceptance criteria

- `AC-WLG-005`: Given a set password, when the owner logs in with it, then a session cookie is set; with a wrong one, `401 invalid_password`.
- `AC-WLG-006`: Given 5 wrong tries, then the 6th try within 5 minutes gets `429 too_many_attempts` even with the right password.

### REQ-WLG-004: Reset by Telegram code

"Forgot password?" shall offer "Send a code to Telegram" when the Telegram channel is on and has at least one owner with a private chat route. It sends `🔐 Your lana reset code: 123456. It expires in 10 minutes. If you did not ask for it, ignore this message.` to each owner's private chat. Entering the code plus a new password sets the password and signs in.

#### Acceptance criteria

- `AC-WLG-007`: Given Telegram on with owner 42, when the owner asks for a code, then chat 42 gets the message, and the right code with a new password resets it.
- `AC-WLG-008`: Given 5 wrong codes, then the code is void and the page says "Ask for a new code."
- `AC-WLG-009`: Given Telegram off, then the Telegram option is not shown.

### REQ-WLG-005: Reset by restarting lana

"Forgot password?" shall also offer "Restart lana to get a sign-in link". Choosing it marks `reset_requested` in `web.json` and shows: "Close and start lana again from your computer. A browser window will open where you can set a new password." On the next daemon start with `reset_requested`, the daemon opens a sign-in link (REQ-WLG-001) that leads to "Set a new password", then clears the flag.

#### Acceptance criteria

- `AC-WLG-010`: Given `reset_requested`, when the daemon starts, then the opener is called with a sign-in link, and after the new password is set the flag is cleared.

### REQ-WLG-006: Reset from the terminal

`lana web reset-login` shall create a sign-in link on the running daemon (through its local control path) or, when no daemon runs, set `reset_requested`; it prints the link or the restart hint, and opens the browser when possible.

#### Acceptance criteria

- `AC-WLG-011`: Given a running daemon, when the command runs, then it prints a sign-in link that works once.

### REQ-WLG-007: Change password and sessions

Settings → General shall have "Change password" (current + new + confirm). Setting or changing the password ends every other browser session.

#### Acceptance criteria

- `AC-WLG-012`: Given two signed-in browsers, when one changes the password, then the other gets `401` on its next request.

### REQ-WLG-008: Migration from the token

When `web.json` has a token and no password, the daemon treats it as "no password": it opens a sign-in link (REQ-WLG-001). The old token still logs in until a password is set, then stops working. All texts that mention the token or daemon output are removed from the login page and setup wizard.

#### Acceptance criteria

- `AC-WLG-013`: Given a token-only `web.json`, when the daemon starts, then the browser opens a sign-in link; after the password is set, `POST /v1/auth/login` with the token gets `401`.
- `AC-WLG-014`: Given the built dashboard, then no view text contains "owner token" or "daemon output".

## Authorization and security

- Loopback only (unchanged). Host/Origin/CSRF checks unchanged.
- Codes are compared in constant time and stored as hashes; links use the URL fragment so the code is not sent in the page request or logged by the server.
- argon2id with per-password salt; parameters stored with the hash.
- Login, link, and reset attempts are rate-limited as above and logged without secrets.

## Blocking Open Questions

| ID | Question | Status | Resolution |
|---|---|---|---|
| `Q-001` | Daily login | Resolved | Password |
| `Q-002` | First access | Resolved | Browser opens with a one-time link |
| `Q-003` | Reset | Resolved | Telegram code, restart link, terminal command |
| `Q-004` | Remote access | Resolved | Not in this spec |

## Requirement coverage

| Requirement | Acceptance criteria |
|---|---|
| `REQ-WLG-001` | `AC-WLG-001`, `AC-WLG-002` |
| `REQ-WLG-002` | `AC-WLG-003`, `AC-WLG-004` |
| `REQ-WLG-003` | `AC-WLG-005`, `AC-WLG-006` |
| `REQ-WLG-004` | `AC-WLG-007`–`AC-WLG-009` |
| `REQ-WLG-005` | `AC-WLG-010` |
| `REQ-WLG-006` | `AC-WLG-011` |
| `REQ-WLG-007` | `AC-WLG-012` |
| `REQ-WLG-008` | `AC-WLG-013`, `AC-WLG-014` |

## Approval

- **Approved by**: Ridho
- **Approved on**: 2026-10-31
