# Design: Friendly web login

- **Requirements**: [requirements.md](requirements.md), revision 1
- **Contract**: [api-contract.md](api-contract.md), revision 1
- **Status**: Approved (owner approved the mockups on 2026-10-31)

## Mockups

Open the HTML files from this folder in a browser. They link the real dashboard CSS in `web/ui/src/styles`. The PNGs are 1440×900 screenshots.

| Screen | Mockup |
|---|---|
| First start: create password (setup step 1) | [1-welcome.png](mockups/1-welcome.png) |
| Daily login, wrong password state | [2-login.png](mockups/2-login.png) |
| Forgot password: Telegram code or restart | [3-forgot.png](mockups/3-forgot.png) |
| Enter Telegram code + new password | [4-code.png](mockups/4-code.png) |

`mockups/mock.css` holds the only new styles. Port them into `web/ui/src/styles/setup.css` (or a new `login.css` imported by `main.tsx`), using tokens only.

## Visual rules

- **Same shell as setup.** Every auth screen uses `SetupShell`: the 400px rail on the left and the form column on the right. The rail shows the setup steps only during first-run setup, because only that is a real sequence. Login, forgot, and code screens show the logo, the door mark, and the place note instead.
- **One memorable element: the door mark.** The two shapes of the logo-mark Λ are drawn very large (about 560px wide) in `--bg-tertiary` and a slightly lighter right half. They sit at the bottom-left of the rail and are cut off by its edges (`overflow: hidden`). The mark is decorative (`aria-hidden`), does not move, and does not appear in the setup rail, where the steps take the space.
- **Place note** at the bottom of the rail: "lana on this computer" in bold, then "Only this computer can open this page."
- **No template chrome.** No centered card, no gradients or glows, no step eyebrow above the title (the rail shows progress), no all-caps labels, no emoji, no "→" in links.
- **Forms.** The password field has an inline eye icon toggle (eye / eye-off, aria-label Show password / Hide password); native browser reveal buttons are hidden. The confirm field is labelled "Type it again". The hint "At least 8 characters." sits under the first field. Errors use the existing `.setup-field-error` and `aria-invalid`.
- **Forgot screen.** The options form a bordered list, not cards. Each row has a title, one sentence, and its action button on the right. Telegram is the primary action; restart is secondary. The Telegram row is hidden when Telegram reset is not available.
- **Code input.** One `inputmode="numeric"` field, monospace, 24px, letter-spaced, about 9.5em wide, so pasting works. `autocomplete="one-time-code"`.
- **Secondary links** sit below the form as plain text followed by an underlined link: "Forgot it? Reset your password", "No message? Send a new code", "Back to log in".

## Copy

| Screen | Title | Lede | Primary button |
|---|---|---|---|
| Welcome | Create your password | You will use it to open lana on this computer. | Create password |
| Reset via link | Set a new password | You will use it to open lana on this computer. | Set new password |
| Login | Log in | Enter the password you made when you set up lana. | Log in |
| Login, no password yet | Set up your password | lana opened a sign-in link in a new browser tab when it started. Switch to that tab, or restart lana to open a new one. | — |
| Forgot | Reset your password | Choose how you want to prove it is you. | Send code / Use restart |
| Code | Enter the code | We sent it to Telegram. It works for 10 minutes. | Set new password |
| Restart chosen | Restart lana | Close lana and start it again. A browser window opens where you can set a new password. | — |
| Expired link | This link has expired | Restart lana to get a new one. | — |

Setup rail step 1: title "Create your password", note "Only you can open lana". It replaces "Unlock the dashboard" / "Paste the owner token from the daemon".

## Settings → General

"Change password" is one settings row in the existing General layout: label on the left, and on the right three fields (Current password, New password, Type it again) plus a "Change password" button. The success toast says "Password changed. Other browsers were signed out."
