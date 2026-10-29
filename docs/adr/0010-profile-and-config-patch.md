# 0010. Compose the runtime with profiles and config patches

- **Status**: Accepted (Rust implementation details replaced by [0013](0013-use-go-as-single-binary.md); the architectural decision stands)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-from-modules-services-and-hooks.md), [0012](0012-acp-and-sdk-json-rpc.md)

## Context

The `lana` binary is used in several forms: a daemon with all channels, a one-shot run in scripts,
an ACP server for editors, and an SDK server for other programs. Each form needs a different set of
modules. For example, one-shot runs do not need Telegram or the scheduler; ACP does not need the web dashboard.

`dsh` solves this with **profiles** (named compositions) that stack **bundles**, and then
**patches** that target rows by `id`. The command `dsh --profile web --dump-config`
prints the final tree. The owner asked for the profile and patch pattern to be part of v1.

## Decision

The effective configuration is built from **ordered layers**. Later layers override earlier ones.

1. **Base** — compiled into the binary. Shared modules: provider, tools, store, approval, prompt, compaction.
2. **Built-in profiles** — compiled into the binary: `daemon`, `headless`, `acp`, `sdk`, `minimal`.
3. **User profile patch** — `~/.lana/profiles/<profile>.toml`.
4. **User config** — `~/.lana/config.toml` (provider, agent, route, MCP, channel).
5. **CLI overlay** — `--patch <file>`, can be repeated.

Module row format:

```toml
[[modules]]
id = "channel.telegram"     # row identity, patch target
module = "telegram"         # compiled Module implementation
disabled = false
config = { poll_timeout_secs = 30 }
```

Merge rules:

- `[[modules]]` are merged by `id`. A row with the same `id` **replaces the entire**
  `config` of that row, following `dsh`. `disabled = true` turns a row off. A row with a new `id` is added.
- The field `after = "<id>"` or `before = "<id>"` sets the position of a new row.
- Other tables (`providers`, `agents`, `mcp`, `channels`) are merged recursively per key.
- `[[routes]]` are not merged. The last layer that defines `routes` replaces the entire list.

Built-in profiles:

| Profile | Subcommand | Extra modules on top of base |
|---|---|---|
| `daemon` | `lana daemon` | http, web, telegram, whatsapp, scheduler, router |
| `headless` | `lana run` | one-shot runner, output to stdout. Approval policy `never` unless `--ask` |
| `acp` | `lana acp` | ACP server over stdio (see [0012](0012-acp-and-sdk-json-rpc.md)) |
| `sdk` | `lana sdk` | SDK JSON-RPC server over stdio or a unix socket |
| `minimal` | `lana --profile minimal` | only provider, store, and agent loop. For tests and embedding |

`lana chat` uses the `daemon` profile if the daemon is already running (attaches over HTTP), or `minimal`
plus the CLI channel if the daemon is down.

Tools:

- `lana --profile <p> --dump-config` prints the effective configuration together with the origin of each row.
- `lana config check` validates all layers and reports unknown `id`s.
- An unknown module or a missing service dependency makes boot fail with a clear message.
  There is no silent fallback.

## Consequences

**Positive**
- One binary, many roles, without flags that conflict with each other.
- Users can turn off or replace a single module without copying the whole config.
- `--dump-config` makes the composition inspectable.

**Negative / tradeoffs**
- Five merge layers are harder to understand than one file. Mitigated by `--dump-config`.
- "A patch replaces the entire `config` of a row" can surprise users who expect a deep merge.

**Neutral**
- User-defined profiles (not just patches) can be added later as a `profiles/<name>.toml` file with `extends = "daemon"`.

## Alternatives considered

- **A single `config.toml` without profiles** — simplest. Rejected because ACP and headless need different modules.
- **Cargo feature flags only** — shrinks the binary, but cannot choose at runtime. Still used for
  large modules (WhatsApp, TUI), and profiles choose among the available modules.
- **YAML with `!!js` expressions like `dsh`** — rejected. Static TOML is sufficient and safer.
