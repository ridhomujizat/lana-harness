# Tasks: Friendly web login

- **Requirements**: revision 1 · **Contract**: revision 1
- **Status**: In Progress

## Design notes

- `web/auth.go`: token store → credential store (`web.json` v2), argon2id via `golang.org/x/crypto/argon2`, in-memory sign-in codes and reset codes (hashed, expiring, attempt counters), login lockout, session invalidation on password set/change.
- Browser opener: a small `internal/browser` package (`Open(url) error`) with an injectable func for tests; called from `cmd/lana/daemon.go` after the web server listens.
- Telegram reset: a port on the web server (`TelegramNotifier.SendOwnerCode`) implemented in `cmd/lana` with the running Telegram channel.
- Local control for `lana web reset-login`: reuse the daemon lock/control path the CLI already uses to talk to the daemon (check `cmd/lana` for the existing mechanism); add a `link` operation.
- UI (`web/ui`): routes `#/welcome` (create / set new password), login page (password, forgot → Telegram code or restart), Settings → General "Change password"; setup step 1 becomes "Create your password" when arriving with a code. Remove every "owner token" / "daemon output" text.

## Tasks

- [x] `T-001` Backend: credential store, claim, login, lockout, reset (Telegram, restart flag), change password, sessions; opener on start; `[web] open_browser` — ACs `AC-WLG-001`–`010`, `012`, `013`.
- [x] `T-002` CLI `lana web reset-login` + local `link` control — `AC-WLG-011`.
- [x] `T-003` UI per [design.md](design.md) and the mockups: welcome, login, forgot flows, change password, setup step texts; rebuild `web/dist` — `AC-WLG-003`–`009` (UI), `AC-WLG-014`.
- [ ] `T-004` Final verification + owner check on a fresh `LANA_HOME` and on the existing one (migration).

## Evidence

- `T-001`: two implementer runs (first hit the 100-turn budget). `go test -race ./...` pass, golangci-lint 0 issues. Reviewed: argon2id m=64MiB t=3 p=2, constant-time compares, crypto/rand codes, lockout on password login only (token login only while no password is set).
- `T-002`: `go test -race ./...` pass, lint 0 issues. Local control: loopback `POST /v1/local/sign-in-link` guarded by `X-Lana-Local-Secret` (constant-time) from `$LANA_HOME/daemon.secret` (0600, written after the listener starts, removed on stop, rewritten after restart-in-place).
- `T-003`: three implementer runs (A screens, B change password + copy cleanup, eye-icon / no-password fix). web/ui typecheck, lint, 21 files / 87 Vitest tests pass; `make web-check` clean. Eye icon strokes set to #98A2B3 like the other icons (icons render as <img>, so currentColor does not apply).
- Live check 2026-10-31: daemon start printed the sign-in link; `lana web reset-login` printed a fresh link; the owner created a password (web.json v2).
