# 0022. Offer Antigravity as a router target that runs through the official `agy acp`

- **Status**: Proposed
- **Date**: 2026-11-25
- **Deciders**: Ridho
- **Related ADRs**: [0018](0018-claude-code-as-external-agent-runtime.md), [0020](0020-claude-code-as-router-target-with-lana-context.md), [0012](0012-acp-and-sdk-json-rpc.md)

## Context

The owner has a Google AI Pro subscription for Antigravity and wants it usable in lana the way Claude
Code is (ADR 0020): picked like any model, with lana's persona, memory, knowledge, and tools, and with
several Google accounts.

Three open-source harnesses were studied (2026-11-25): T3 Code (`pingdotgg/t3code`), Orca
(`stablyai/orca`), and `adeebahmad01/cliproxy-antigravity`. All three run turns through Google's official
`agy` CLI. They differ in how much they touch the account:

- T3 Code refreshes the CLI's OAuth token itself and calls Google's private
  `cloudcode-pa…/v1internal:retrieveUserQuotaSummary` for usage, and sets a fake `SSH_CONNECTION` to move
  the login out of the OS keyring.
- Orca rewrites agy's macOS Keychain item to switch accounts.
- cliproxy-antigravity never reads or sends a token and never calls a Google endpoint. It runs `agy` with
  a separate `HOME` per account, reads usage with the CLI's own `/usage`, and signs in by running agy's
  own login and typing the code the person pastes. It also adds things lana does not want: an
  OpenAI-compatible endpoint for other programs, round-robin across accounts, and moving a chat to
  another account when a quota runs out.

Checked on the owner's Mac with agy 1.5.17 (2026-11-25):

- agy keeps the owner's Google login in the login Keychain (service `gemini`, account `antigravity`).
- With `HOME` set to another folder, macOS no longer finds the login Keychain (`security list-keychains`
  shows only `System.keychain`), agy reports "not logged in", and agy's own code falls back to file
  storage under that `HOME` ("file storage when keyring storage is bypassed or unreachable"). So one
  `HOME` per account keeps logins apart without lana touching the Keychain or any token.
- `agy acp` advertises `loadSession`, session `resume`/`close`/`list`, `auth.logout`, and
  `_meta.antigravity.systemPrompt.append`; `session/new` accepts an `http` MCP server and returns the
  model list, the modes (`default` asks before edits and commands), and the plan tier.
- `agy -p /usage --output-format json` returns the 5-hour and weekly windows per model group with no
  model call.

## Decision

1. **Antigravity is a router target.** Addresses are `<account>/<model>`, with `<model>` a model ID from
   `agy models` (for example `antigravity/gemini-4-neon-high`). The built-in account `antigravity` runs
   agy with the owner's own `HOME` and login. Other accounts are providers of kind `antigravity`, each with
   its own `HOME` folder under `$LANA_HOME/antigravity/<name>`.
2. **Turns run in `agy acp`.** When the first target of a turn's chain is an Antigravity address, lana
   runs the turn through a new runtime `runtime/antigravity`, the same way as Claude Code (ADR 0020):
   one `agy acp` process per lana session, lana's prompt head appended to agy's system prompt, lana's
   tools over the existing loopback MCP host, fallback to the next target only before output, background
   passes never on Antigravity.
3. **agy asks, lana decides.** Sessions stay in agy's `default` mode. Every `session/request_permission`
   goes through lana's permission engine and approvals. lana never passes
   `--dangerously-skip-permissions`.
4. **lana never handles the account.** lana does not read agy's token files or Keychain items, does not
   refresh tokens, and does not call any Google endpoint. Identity shown in the dashboard is the owner's
   label and the plan tier agy reports in `session/new`. Usage comes from `agy -p /usage`. Sign-in runs
   `agy acp --login` in the account's `HOME`; agy shows the Google page, and the code the owner pastes is
   written to agy's input once and never stored or logged.
5. **No sharing, no rotation.** Antigravity addresses are never served through lana's OpenAI-compatible
   proxy. lana never moves a turn or a chat to another account on its own. A combo may list several
   accounts, and the dashboard warns that using several personal accounts to get around plan limits can
   break Google's terms.
6. **Google account sign-in only.** Gemini API key, Gemini Enterprise, and Agent Platform (Vertex) auth
   are out of scope; their variables (`GEMINI_API_KEY`, `GOOGLE_API_KEY`,
   `GOOGLE_APPLICATION_CREDENTIALS`, `GOOGLE_GENAI_USE_VERTEXAI`, `GOOGLE_CLOUD_PROJECT`) are removed
   from agy's environment so agy always uses the account's own login.

## Consequences

- One more external runtime with the same shape as Claude Code. `core`, `router`, `internal/llmcall`, and
  `agent` stop assuming that "external" means "Claude Code".
- A named account's `HOME` is not the owner's home. Shell commands agy runs for that account see a
  different `~`, so lana links the owner's `.gitconfig`, `.ssh`, and `.config/gh` into it. The macOS
  Keychain, and with it Keychain-backed git credential helpers, is not reachable from those commands.
  The built-in account has none of this.
- ACP is a public protocol, but agy's extensions (`_meta.antigravity.*`, `/usage` JSON) are not. A
  minimum agy version and recorded fixtures catch drift, as for Claude Code.

## Alternatives considered

- **`agy -p --input-format stream-json` (print mode).** Simpler, and cliproxy's default, but it has no
  permission callback, so tools would run under agy's own rules or `--dangerously-skip-permissions`.
- **`--gemini-dir` with the owner's `HOME` (T3 Code).** Does not separate Google logins on macOS: the
  Keychain stays reachable and agy keeps one entry for every profile.
- **Switching the Keychain item (Orca) or a fake `SSH_CONNECTION` (T3 Code).** Both work, but lana would
  be changing how the vendor CLI stores credentials. A separate `HOME` only changes where the CLI looks.
