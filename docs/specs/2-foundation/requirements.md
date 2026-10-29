# Requirements: Foundation (workspace, profile loader, Context/module/hook)

- **Issue**: [`#2`](https://github.com/ridhomujizat/lana-harness/issues/2)
- **Status**: Approved
- **Revision**: 5 (revision 3: moved to Go; revision 4: corrections from design; revision 5: library import rules from ADR 0015)
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (config and profile format, public Go API of package `core` and `config`)
- **Related ADRs**: [ADR Index](../../adr/README.md) — [0013](../../adr/0013-use-go-as-single-binary.md), [0014](../../adr/0014-integration-as-agentic-tool.md), [0002](../../adr/0002-core-from-modules-services-and-hooks.md), [0010](../../adr/0010-profile-and-config-patch.md)
- **Related diagrams**: [Diagram Index](../../diagram/README.md) — [0001](../../diagram/0001-system-context-and-roadmap.md), [0004](../../diagram/0004-profile-fork-subagent-and-protocols.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Issue | [`#2`](https://github.com/ridhomujizat/lana-harness/issues/2) | scope |
| ADR 0013 | Go, single binary and library | toolchain, package layout, build, size target and platforms |
| ADR 0014 | Integration as agentic tool | public `core`, config without file for embedding |
| ADR 0002 | Modules, services, and hooks | `Module`, `Context`, hook modes |
| ADR 0010 | Profile and patch | layer order, merge rules, built-in profiles, `--dump-config` |
| Spec #1 | [design: Core interface](../1-cli-tui/design.md#core-interfaces-required-from-specs-2-3-4) | `ProfileLoader` with origin map and span-based errors |
| Machine instruction | Nix development | `nix develop`, no global install |
| Interview 2026-10-29 | Split of core spec into #2, #3, #4 | scope |

## Goal

The developer (owner) has a foundation that can be built and tested: a reproducible dev
environment, a Go module with import rules between packages that are enforced, a layered
config loader that explains the origin of every value and every error, and the module and hook
runtime where other features (agent loop, channel, permission) are plugged in.

## Actors

- **Developer** — runs `nix develop`, `go`, and tests.
- **Go service** — imports `core` and `config` when embedding `lana` (ADR 0014).
- **Other specs (#1, #3, #4)** — use the `core` and `config` packages as dependencies.
- **Owner as user** — writes `config.toml` and patches, reads config error messages.

## Definitions

- **Layer** — a single config source: base, built-in profile, the user's profile patch, `config.toml`, or `--patch`.
- **Module row** — a single `[[modules]]` entry with a unique `id`.
- **Effective config** — the result of merging all layers.
- **Origin map** — pairs of key path to the layer that last set its value.
- **Module** — a `Module` implementation compiled into the binary.
- **Service** — a typed object in `Context` that is installed by a module.
- **Hook** — a typed intercept point with mode `waterfall`, `serial`, or `observe`.

## Scope

### Included

- `flake.nix` with a dev shell.
- Go module with the package layout from ADR 0013, import rules, and static release build.
- Packages `core` and `config` as the public API.
- Profile loader: layers, merge, validation, origin map, secrets.
- `Context`, `Module`, install order from dependencies, shutdown.
- Hooks `waterfall`, `serial`, and `observe` along with their semantics.
- Built-in profiles `minimal` and `headless` as module lists.

### Not included

- Contents of feature modules (agent loop, provider, tools, store, permission, channel). Specs #3 and #4.
- CLI subcommands (`config check`, `--dump-config`). Spec #1 uses the loader from this spec.
- Profiles `daemon`, `acp`, `sdk`. Added by the spec that brings their modules.
- Runtime plugins (ADR 0002).

## Assumptions

- The developer uses macOS arm64 or Linux with Nix flakes enabled. — Source: machine instruction.
- All dev dependencies are provided by Nix, not installed globally. — Source: machine instruction.

## Behavior rules

### Dev environment and build

#### REQ-DEV-001: Nix dev shell

`nix develop` shall provide Go, `gopls`, `golangci-lint`, and `sqlite`. The Go version is pinned by
`flake.lock` (nixpkgs). The dev shell sets `GOTOOLCHAIN=local` and `CGO_ENABLED=0`. Exception: `go test -race` requires cgo, so that command is run with `CGO_ENABLED=1`.

- `AC-DEV-001`: Given a machine with Nix flakes, when the developer runs `nix develop -c go version`, then the printed version satisfies the `go` directive in `go.mod`.
- `AC-DEV-002`: Given the dev shell is active, when the developer runs `golangci-lint version`, then the command succeeds.
- `AC-DEV-003`: Given the dev shell is active on macOS arm64, when the developer runs `GOOS=linux GOARCH=amd64 go build ./cmd/lana`, then the build succeeds without cgo.

#### REQ-CI-001: GitHub Actions CI

Every push and pull request shall run, on macOS arm64 and Linux x86_64 via `nix develop -c`:
`gofmt -l` (must be empty), `golangci-lint run ./...`, `go test -race ./...`, and the release build.
The release binary size is printed in the job summary.

- `AC-CI-001`: Given one test fails, when CI runs, then the job fails.
- `AC-CI-002`: Given all steps pass, when CI completes, then the job summary contains the binary size for both platforms.

#### REQ-BUILD-001: Package layout and import rules

The module shall use the package layout from ADR 0013. `core` shall not import other packages in
this module. Implementation packages may only import `core` and `internal/...`, except what
ADR 0013 allows (`cmd/lana` and package `lana` assemble everything). Library packages (`lana`, `core`,
`agent`, `providers/*`, `tools`, `store/*`, `observability/*`) shall not import `channels/`, `web/`,
or `protocol/` (ADR 0015).

- `AC-BUILD-001`: Given a clean module, when the developer runs `go build ./...`, then the build succeeds.
- `AC-BUILD-002`: Given someone adds an import from `core` to another package in this module, when the import rule test runs, then the test fails and names the violating package.
- `AC-BUILD-005`: Given someone adds an import from `lana` to `channels/cli`, when the import rule test runs, then the test fails and names both packages.

#### REQ-BUILD-002: Release build

The release build shall use `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=<v>"`
for `darwin/arm64`, `linux/amd64`, and `linux/arm64`, and the size of the `lana` binary is recorded. The target
< 40 MB (ADR 0013) is only recorded and does not fail the build.

- `AC-BUILD-003`: Given `make release` (or the build script), when the build completes, then three binaries exist in `dist/` and their sizes are printed.
- `AC-BUILD-004`: Given the Linux binary produced by the build, when checked with `file`, then the binary is reported as statically linked.

### Profile loader

#### REQ-CONF-001: Layer order

The loader shall build the effective config from ordered layers: base, built-in profile,
`$LANA_HOME/profiles/<profile>.toml`, `$LANA_HOME/config.toml`, and then each `--patch` in order.
A user profile file or `config.toml` that does not exist is skipped. A `--patch` file that does not exist is an error (per spec #1 `API-CLI-001`).

- `AC-CONF-001`: Given `config.toml` and `--patch a.toml` both set `agents.x.model`, when the config is loaded, then the value from `a.toml` is used.
- `AC-CONF-002`: Given `profiles/minimal.toml` does not exist, when profile `minimal` is loaded, then the loader succeeds and that layer is marked as absent.

#### REQ-CONF-002: Merge rules

- `[[modules]]` is merged by `id`. A row with the same `id` replaces the entire `config` of that row.
  Other fields written in the upper layer (`module`, `disabled`) override the old values.
- `disabled = true` makes the row not installed.
- A row with a new `id` is appended at the end, or at position `after = "<id>"` / `before = "<id>"`.
- Tables `providers`, `agents`, `mcp`, `channels` are merged recursively per key.
- `[[routes]]` from the last layer that writes it replaces the entire list.

- `AC-CONF-003`: Given base has `id = "store"` with `config = { path = "a.db", wal = true }` and the patch writes `id = "store"` with `config = { path = "b.db" }`, when merged, then the effective `config` is `{ path = "b.db" }`.
- `AC-CONF-004`: Given the patch writes `id = "store"` with `disabled = true`, when booting, then module `store` is not installed.
- `AC-CONF-005`: Given the patch adds `id = "x"` with `after = "store"`, when merged, then `x` is placed right after `store`.
- `AC-CONF-006`: Given base sets `agents.a.model` and `agents.a.tools`, and the patch only sets `agents.a.model`, when merged, then `tools` stays from base.
- `AC-CONF-007`: Given `config.toml` has two routes and the patch has one route, when merged, then only the one route from the patch remains.
- `AC-CONF-008`: Given `after` points to an `id` that does not exist, when merged, then the validation error names that `id`.

#### REQ-CONF-003: Full validation with location

The loader shall collect all errors from all layers before failing. Each error shall
name the file, the line number if known, the key path, and the message.

Minimum validation:

- TOML syntax.
- Value types match the schema.
- References: `agents.*.provider` to `providers`, `routes.*.agent` to `agents`, `agents.*.subagents` to `agents`, `agents.*.mcp` to `mcp`.
- `module` in `[[modules]]` is known to the binary.
- Duplicate `id` within one layer.
- Enum values (`permission`, `kind`) are valid.
- Keys unknown to the schema are errors, with a suggestion of the closest key if one exists.
- `version` higher than what the binary supports.

- `AC-CONF-009`: Given two errors in `config.toml` and one in the patch, when the config is loaded, then all three errors are reported.
- `AC-CONF-010`: Given `agents.coder.provider = "x"` on line 12 and provider `x` does not exist, when the config is loaded, then the error contains file `config.toml`, line 12, key `agents.coder.provider`.
- `AC-CONF-016`: Given `agents.coder.permision = "ask"`, when the config is loaded, then the error names key `agents.coder.permision` and the suggestion `permission`.

#### REQ-CONF-004: Origin map

The loader shall provide an origin map for every table, every leaf key, and every module row.

- `AC-CONF-011`: Given `providers.openai` is written in `config.toml` and the agent's `model` is overridden in `--patch p.toml`, when the origin map is read, then `providers.openai` originates from `config.toml` and `agents.x.model` from `p.toml`.

#### REQ-CONF-005: Secrets

Secret values are read from the process environment first, then from `$LANA_HOME/secrets.env`. The process environment wins.
Secret values shall not appear via `fmt` (`%v`, `%+v`, `%#v`, `%s`), `slog`, JSON serialization, or TOML serialization of the effective config.

- `AC-CONF-012`: Given `OPENAI_API_KEY` is in the env and in `secrets.env` with different values, when the secret is read, then the value from the env is used.
- `AC-CONF-013`: Given the effective config is serialized, when a secret is present, then its value is `"***"`.
- `AC-CONF-014`: Given a secret value, when formatted with `%#v` or logged with `slog`, then the result does not contain the original value.
- `AC-CONF-017`: Given `secrets.env` with permission `0644`, when the config is loaded, then the file is still read, the loader returns a warning containing the current permission and `chmod 600 <path>`, and the warning is logged. The CLI (spec #1) prints it to stderr.

#### REQ-CONF-007: Env interpolation in MCP

`${NAME}` in values of `mcp.*.env` and `mcp.*.headers` is replaced with the env value (process env, then
`secrets.env`). The result is treated as a secret. In other fields, `${` is plain text. An env name
that is not found is a validation error.

- `AC-CONF-018`: Given `mcp.github.env.GITHUB_TOKEN = "${GITHUB_TOKEN}"` and env `GITHUB_TOKEN=abc`, when the config is loaded, then the effective value is `abc` and its serialization is `"***"`.
- `AC-CONF-019`: Given `agents.x.prompt_file = "${HOME}/p.md"`, when the config is loaded, then the value stays `"${HOME}/p.md"`.
- `AC-CONF-020`: Given `${NOT_THERE}` in `mcp.x.env` and that env is not set, when the config is loaded, then the error names `NOT_THERE` and its key.

#### REQ-CONF-008: Config format version

Every layer may write `version = <integer>` at the top level. Without that field, the version is assumed to be 1.

- `AC-CONF-021`: Given `version = 2` and the binary only supports 1, when the config is loaded, then the error names the file's version and the supported version.

#### REQ-CONF-009: Config without file for embedding

The loader shall be able to build the effective config from in-memory layers (TOML text or Go struct) without
reading `LANA_HOME`, using the same merge and validation rules. The origin of an in-memory layer is named by the caller.

- `AC-CONF-022`: Given a Go service provides one in-memory TOML layer named `myservice`, when the config is loaded, then no file is read and the origin map names `myservice`.
- `AC-CONF-023`: Given an in-memory layer contains a provider reference that does not exist, when the config is loaded, then the validation error is the same as for a file layer, with the layer name as the location.

#### REQ-CONF-006: Data location

`LANA_HOME` defaults to `~/.lana`. The env `LANA_HOME` overrides the location. Relative paths in config (for example
`prompt_file`) are resolved from `LANA_HOME`.

- `AC-CONF-015`: Given `LANA_HOME=/tmp/x`, when the config is loaded, then `config.toml` is read from `/tmp/x/config.toml`.

### Modules and Context

#### REQ-MOD-001: Install order from dependencies

Boot shall install active modules in an order that satisfies each module's `requires`. For modules
with no dependency relationship, the row order in config is preserved.

- `AC-MOD-001`: Given module B requires a service installed by module A, and A is written after B, when booting, then A is installed before B.
- `AC-MOD-002`: Given two modules with no dependencies, when booting, then the install order equals the row order.

#### REQ-MOD-002: Boot fails clearly

Boot shall fail without installing any module if a required service is needed but not provided by an active module, or if there is a dependency cycle. The message names the related module and service.

- `AC-MOD-003`: Given a module requires service `llm` and no active module installs it, when booting, then the error names the module name and `llm`.
- `AC-MOD-004`: Given A requires B and B requires A, when booting, then the error names the cycle `A → B → A`.

#### REQ-MOD-003: Ordered shutdown

Shutdown shall stop modules in the reverse order of install, with a total time limit of
30 seconds. A module that exceeds the limit is logged and skipped.

- `AC-MOD-005`: Given A is installed before B, when shutting down, then B stops before A.
- `AC-MOD-006`: Given one module does not stop within 30 seconds, when shutting down, then the other modules are still stopped and a warning is logged.

#### REQ-MOD-004: One installer per service

Only one active module may install a given service.

- `AC-MOD-007`: Given two active modules install service `llm`, when booting, then the error names both modules.

### Hooks

#### REQ-HOOK-001: Waterfall

Listeners of `waterfall` are called in sequence. A listener receives the input and `next`. Calling `next`
passes control to the next listener and returns its result. Not calling `next` breaks the chain
with that listener's result. If all listeners call `next`, the hook's default handler is called.
Listeners registered with `prepend` run before regular listeners.

- `AC-HOOK-001`: Given listeners A and B and a default handler, when the hook is called and both call `next`, then the order is A, B, default.
- `AC-HOOK-002`: Given A does not call `next`, when the hook is called, then B and default do not run and A's result is returned.
- `AC-HOOK-003`: Given A modifies the input before `next`, when B runs, then B receives the modified input.
- `AC-HOOK-004`: Given C is registered with `prepend` after A, when the hook is called, then C runs before A.
- `AC-HOOK-005`: Given A returns an error, when the hook is called, then the caller receives that error and the next listeners do not run.

#### REQ-HOOK-002: Serial

Listeners of `serial` are called in sequence, each awaited, without `next`. The results of all listeners
are returned in order.

- `AC-HOOK-006`: Given A and B on a serial hook, when the hook is called, then B starts after A finishes and the result is `[A, B]`.

#### REQ-HOOK-003: Observe

The `observe` hook sends events to all subscribers without waiting for them. Each subscriber has a
buffer of 1024 events. When the buffer is full, the oldest event is dropped and the subscriber receives `Lagged(n)`
with the number of events dropped before the next event.

- `AC-HOOK-007`: Given a subscriber that does not read, when 10,000 events are sent, then the sender is not blocked.
- `AC-HOOK-010`: Given a subscriber lagged by 1500 events, when the subscriber reads again, then it receives `Lagged(476)` followed by the 1024 most recent events.

#### REQ-HOOK-004: Releasable registration

Every listener registration returns a handle. Releasing the handle removes the listener. When a module
stops, all listeners it owns are released.

- `AC-HOOK-008`: Given a listener's handle is released, when the hook is called, then that listener does not run.
- `AC-HOOK-009`: Given a module stops during shutdown, when the hook is called afterward, then that module's listeners do not run.

### Built-in profiles

#### REQ-PROF-001: Profiles `minimal` and `headless`

The binary shall contain the built-in profiles `minimal` and `headless` as module row lists. An
unknown profile shall fail with a list of the available profiles.

- `AC-PROF-001`: Given profile `minimal`, when the effective config is read, then its module rows are the same as the built-in definition.
- `AC-PROF-002`: Given profile `xyz`, when loaded, then the error names `xyz` and the list of existing profiles.

## Failure cases

| Condition | Expected behavior |
|---|---|
| Config file cannot be read (permission) | Error names the file and the OS reason |
| Invalid TOML | Error with line and column |
| Broken reference | Error per reference with location |
| Unknown module | Error names `module` and the list of existing modules |
| Missing dependency or cycle | Boot fails before installing any module |

## Authorization and security

- Secrets are never printed (`REQ-CONF-005`).
- The loader does not execute code or expressions from config (ADR 0010 rejects `!!js`).

## Data integrity and concurrency

- The loader is pure: the result depends only on the contents of the layers and env.
- `Context` can only be changed during boot. After boot, services cannot be added or replaced.
- Hooks are safe to call from many concurrent tasks.

## Accessibility

Not applicable (no new user interface).

## Question resolution

| ID | Question | Resolution |
|---|---|---|
| `R4-001` | `--patch` file does not exist | Error, per spec #1 (CQ-015 design spec #2) |
| `R4-002` | `secrets.env` warning on stderr | Library returns the warning; CLI prints to stderr (CQ-007) |
| `R4-003` | `CGO_ENABLED=0` and `go test -race` | `-race` is run with `CGO_ENABLED=1` (DQ-010) |
| `Q-001` | Toolchain version pin | Rev 2: `rust-toolchain.toml`. Rev 3: replaced by ADR 0013, Go from nixpkgs pinned by `flake.lock`, `GOTOOLCHAIN=local` |
| `Q-002` | CI | GitHub Actions on macOS arm64 and Linux x86_64 |
| `Q-003` | Unknown key | Error with suggestion of the closest key |
| `Q-004` | `${VAR}` interpolation | Only in `mcp.*.env` and `mcp.*.headers`, the value is a secret |
| `Q-005` | Binary size target | Only recorded |
| `Q-006` | Lagged `observe` subscriber | Buffer 1024, drop oldest event, `Lagged(n)` |
| `Q-007` | `secrets.env` permission | Warning in log and stderr, file is still read |
| `Q-008` | Config format version | Optional `version`, default 1 |

## Blocking Open Questions

None.

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-DEV-001` | `AC-DEV-001`, `AC-DEV-002`, `AC-DEV-003` | Machine instruction, ADR 0013 |
| `REQ-BUILD-001` | `AC-BUILD-001`, `AC-BUILD-002`, `AC-BUILD-005` | ADR 0013, ADR 0015 |
| `REQ-BUILD-002` | `AC-BUILD-003`, `AC-BUILD-004` | ADR 0013, `Q-005` |
| `REQ-CI-001` | `AC-CI-001`, `AC-CI-002` | `Q-002` |
| `REQ-CONF-001` | `AC-CONF-001`, `AC-CONF-002` | ADR 0010 |
| `REQ-CONF-002` | `AC-CONF-003` through `AC-CONF-008` | ADR 0010 |
| `REQ-CONF-003` | `AC-CONF-009`, `AC-CONF-010`, `AC-CONF-016` | ADR 0010, spec #1, `Q-003` |
| `REQ-CONF-004` | `AC-CONF-011` | ADR 0010, spec #1 |
| `REQ-CONF-005` | `AC-CONF-012`, `AC-CONF-013`, `AC-CONF-014`, `AC-CONF-017` | ADR 0005, spec #1 contract, `Q-007` |
| `REQ-CONF-006` | `AC-CONF-015` | ADR 0013, spec #1 contract |
| `REQ-CONF-007` | `AC-CONF-018`, `AC-CONF-019`, `AC-CONF-020` | ADR 0006, `Q-004` |
| `REQ-CONF-008` | `AC-CONF-021` | `Q-008` |
| `REQ-CONF-009` | `AC-CONF-022`, `AC-CONF-023` | ADR 0014 |
| `REQ-MOD-001` | `AC-MOD-001`, `AC-MOD-002` | ADR 0002 |
| `REQ-MOD-002` | `AC-MOD-003`, `AC-MOD-004` | ADR 0010 |
| `REQ-MOD-003` | `AC-MOD-005`, `AC-MOD-006` | ARCHITECTURE §5 |
| `REQ-MOD-004` | `AC-MOD-007` | ADR 0002 |
| `REQ-HOOK-001` | `AC-HOOK-001` through `AC-HOOK-005` | ADR 0002 |
| `REQ-HOOK-002` | `AC-HOOK-006` | ADR 0002 |
| `REQ-HOOK-003` | `AC-HOOK-007`, `AC-HOOK-010` | ADR 0002, `Q-006` |
| `REQ-HOOK-004` | `AC-HOOK-008`, `AC-HOOK-009` | ADR 0002 (releasable registration, `dsh` pattern) |
| `REQ-PROF-001` | `AC-PROF-001`, `AC-PROF-002` | ADR 0010 |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit.
- [x] State transitions and destructive effects are explicit or not applicable.
- [x] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: Ridho
- **Approved on**: 2026-10-29 (revision 3)
