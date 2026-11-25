# Design: Antigravity as a router target through `agy acp`

- **Requirements** r1 · **Contract** r1 · **Status**: Approved (2026-11-25) · **Design revision**: 1
- **ADR**: [0022](../../adr/0022-antigravity-as-router-target-through-agy-acp.md) (binding), 0020 and 0018 for the shared shape

## Visual Architecture Check

| Trigger | Present? | View |
|---|---:|---|
| Module boundary / dependency direction | Yes | below |
| External system | Yes | `agy acp` process |
| Concurrency, retry, failure | Yes | below |

```mermaid
sequenceDiagram
  participant R as agent.Runner
  participant X as runtime/antigravity
  participant A as agy acp (HOME = account home)
  participant M as mcphost (loopback)
  R->>X: RunTurn(cfg{Model, Home, AppendSystemPrompt}, input, OpenMCP)
  X->>X: acquire slot (max 2, FIFO)
  X->>A: start or reuse; initialize
  X->>A: session/load(S) or session/new{cwd, mcpServers:[http lana], _meta.antigravity.systemPrompt.append}
  A-->>X: (load only) replayed session/update … ignored until the load result
  X->>A: session/set_model{modelId}
  X->>A: session/prompt{prompt blocks}
  A-->>X: session/update agent_message_chunk / agent_thought_chunk / tool_call / tool_call_update
  X-->>R: emit text_delta / thinking_delta / tool_call / tool_result
  A->>M: MCP tools/call (lana tools)
  A-->>X: session/request_permission{toolCall, options}
  X->>R: Approve(RuntimeApproval)
  R-->>X: allow / deny
  X->>A: result{outcome: selected allow|deny}
  A-->>X: prompt result {stopReason, usage}
  X-->>R: emit usage, end
```

## Recorded protocol (agy 1.5.17, 2026-11-25)

Fixtures in `runtime/antigravity/testdata/` (paths scrubbed): `acp_turn1.jsonl` (new session, model
`gemini-3.8-flash-low`, two `execute` tool calls each with a permission request, text reply, usage),
`acp_load.jsonl` (process restart, `session/load` of the same session, one more turn), `usage.json`,
`models.txt`, `version.txt`. Each fixture line is `{"dir":"send"|"recv","msg":{…}}`. Facts the code relies on:

- Framing: one JSON-RPC 2.0 object per line on stdin/stdout. agy's own requests (`session/request_permission`)
  use **string** ids (`"1"`); lana's requests use integer ids. Unknown agent requests get error `-32601`.
- `initialize` result: `agentCapabilities.loadSession=true`, `_meta.antigravity.systemPrompt.append=true`,
  `authMethods` (lana does not call `authenticate`).
- `session/new` result: `sessionId`, `models.availableModels[{modelId,name}]`, `modes` (lana keeps
  `default`), `_meta.antigravity.tier{name,id}` (the plan tier shown in the dashboard).
- `_meta.antigravity.systemPrompt.append` on `session/new` and `session/load` is honoured (the probe reply
  ended with the appended marker).
- `session/load` replays the old conversation as `session/update` (`user_message_chunk`, completed
  `tool_call`, `agent_message_chunk`) **before** its result. The runtime ignores every update for a
  session until its `session/new`/`session/load` result has arrived.
- `session/set_model{sessionId, modelId}` → `{}`.
- Updates seen: `agent_message_chunk{content{type:text,text}}`, `agent_thought_chunk` (same shape),
  `tool_call{toolCallId,name,kind,title,status,rawInput}`, `tool_call_update{toolCallId,status,
  rawOutput,content[]}` with status `pending|in_progress|completed|failed`, `usage_update{used,size}`
  (context fill), `session_info_update{title}`, `config_option_update`, `available_commands_update`.
- `session/request_permission{sessionId, toolCall{…}, options[{optionId,name,kind}]}`; kinds
  `allow_once`/`reject_once` (ids `allow`/`deny` today; match by kind, not id). Reply
  `{"outcome":{"outcome":"selected","optionId":…}}` or `{"outcome":{"outcome":"cancelled"}}`.
- `session/prompt` result `{stopReason, usage{inputTokens,outputTokens,cachedReadTokens,
  cachedWriteTokens,thoughtTokens,totalTokens}}`.
- Tool kinds: `execute` (`rawInput.CommandLine`), `edit`, `read`, `search`, `fetch`, `think`, `other`.
- `agy acp --login`: prints "Open this link in a browser to log in with Google:", the
  `https://accounts.google.com/o/oauth2/auth?…` link, then "Then paste the authorization code, or the full
  URL of the page you land on, and press Enter:"; reads one line on stdin; exit 0 on success, exit 1 with
  "login cancelled: no authorization code was entered" on EOF.
- `agy models`: lines `<modelId>\t<name>` after "Fetching available models..."; when signed out it
  prints "Error: Please sign in to view available models." (exit non-zero).
- `agy -p /usage --output-format json --print-timeout 30s`: JSON with `command.data.groups[{name,
  description, buckets[{id, window:"weekly"|"5h", remaining_fraction, reset_time}]}]`. A reset time of
  `1970-01-01T00:00:00Z` means "no window yet" (store as unknown).

## Repository fit

Generalize the places that mean "external runtime" (map point 10 of the integration survey), add the
second runtime beside Claude Code, and leave every Claude Code behavior unchanged.

- **core** — `AntigravityProvider = "antigravity"`, `AntigravityKind = "antigravity"`;
  `ProviderConfig.Home` (`home`); `RuntimeConfig.Home` (the `HOME` for the child; empty = daemon's).
  `RuntimeEvent.ClaudeSessionID` and `SystemMessage.ClaudeSessionID` keep their names and JSON keys
  (stored logs) and carry the ACP session ID for Antigravity; `SystemMessage.Runtime` tells them apart.
  `core/capabilities.go` lists Antigravity next to Claude Code.
- **internal/llmcall** — keep every `ClaudeCode*` function. Add a runtime-neutral layer:
  `ExternalRuntime(llm, provider) (runtime string, ok bool)` (built-in names and provider kinds →
  `"claude-code"` | `"antigravity"`), `IsExternal(llm, t)`, `WithoutExternal(llm, chain)`,
  `HasExternal`, `AntigravityHome(name)` (`$LANA_HOME/antigravity/<name>`), `ValidAntigravityModel`
  (non-empty, no leading `-`, no spaces). `AgentChain`, `BackgroundChain`, `AgentBackgroundChain` switch
  from `WithoutClaudeCode` to `WithoutExternal`.
- **config** — kind `antigravity` in `providerKinds`; account keys `home`, `binary`, `label`,
  `allow_scheduled`, `enabled`; the built-in `[providers.antigravity]` table may hold only `binary`,
  `allow_scheduled`, `enabled`; `antigravity` cannot be redefined; address validation through
  `ValidAntigravityModel`. Claude-only agent keys (`auth`, `api_key_env`, effort) stay Claude-only.
- **router** — `snapshot.agy` (accounts by name, built-in included) beside `snapshot.claude`; kind split
  at `router.go:261`; `Resolve` accepts the addresses; `chain.go` returns `not_found` for them (this keeps
  them out of the OpenAI proxy and out of cooldown); `module.go` skips them in `missingSecrets` and
  `usedProviders`; `Models(account)` returns the account's reported list (`SetRuntimeModels(account,
  []RuntimeModel)` / `RuntimeModels(account)`), never a catalog. `Kind()` reports `antigravity`.
- **runtime/antigravity** (new library; imports core, internal) — registers `antigravity`.
  - `acp.go`: line JSON-RPC client: `call(ctx, method, params) (json.RawMessage, error)` with integer
    ids, a reader goroutine dispatching responses, notifications, and agent requests (string or number
    ids echoed back unchanged); 10 MB line cap; stderr ring buffer.
  - `process.go`: argv `agy acp`, `cmd.Dir` = workspace, env per REQ-AGY-002 (`HOME`/`USERPROFILE` from
    `RuntimeConfig.Home`), `Setpgid`, idle timer 10 min, SIGTERM then SIGKILL after 5 s.
  - `runtime.go`: process pool per lana session keyed by (account, binary, prompt-head hash); FIFO
    semaphore 2 (copy `claudecode/semaphore.go`); `RunTurn`: start or reuse → `initialize` once → `load`
    or `new` (ignore updates until its result; a failed load → `new` and a `session_meta` note) →
    `set_model` unless `default` → `prompt` with text and image blocks → map updates → wait for the
    prompt result; early error rules of REQ-AGY-008 (no text or tool event yet). Emits `session_meta`
    with the ACP session ID and the tier.
  - `events.go`: update → `RuntimeEvent` mapping of REQ-AGY-005; a `tool_call` emits `tool_call` once
    per `toolCallId` (`Name` = agy tool `name`, input = `rawInput`); a `tool_call_update` with
    `completed`/`failed` emits `tool_result` (text from `content[].content.text`, else `rawOutput.output`,
    capped like Claude results; `failed` → error result); duplicates ignored.
  - `permission.go`: `request_permission` → `RuntimeApproval` (`ToolName` = name, `Risk` by kind:
    `read`/`search`/`think`/`fetch` low, `edit` write, `execute` shell, else other; `PermTool`/`PermInput`
    = `bash`/`CommandLine` for `execute`, `edit`/path for `edit`, else the agy name and raw input) →
    reply per REQ-AGY-006. A request after cancel → `cancelled`.
  - `mcp.go`: converts `RuntimeTurn.OpenMCP`'s Claude-shaped JSON
    (`{"mcpServers":{"lana":{"type":"http","url":…,"headers":{…}}}}`) into ACP
    `[{"type":"http","name":"lana","url":…,"headers":[{"name":…,"value":…}]}]`. No mcphost change.
  - `status.go`: `agy --version` (≥ 1.5.17), `AccountStatus` = `agy models` exit status and list
    (signed in / signed out / error), cached 60 s per account; models cached 10 min and reported through
    `RuntimeDeps.OnModels`.
  - `usage.go`: `UsageProbe(account)` runs the `/usage` command (45 s timeout) and returns
    `RuntimeUsageReport` windows per group (`<group>` + `5h` / `weekly`).
  - `login.go`: `SignIn(ctx, account, onLink func(url string), code <-chan string) error`: creates the
    home (0700) and the links, runs `agy acp --login`, parses the link from stdout, calls `onLink`, waits
    for one code (or ctx done), writes it plus `\n` to stdin, waits for exit (15 min overall). Output kept
    in memory only for the failure line (URLs → "sign-in link", 240 runes). No sign-out command: removal
    deletes the home.
  - `testdata/fakeagy`: Go helper binary that replays a fixture named by `FAKEAGY_FIXTURE` (answers lana's
    requests with the recorded results in order, sends recorded agent requests and waits for lana's
    reply, records argv/env/stdin lines to `FAKEAGY_RECORD`), and fake `--version`, `models`, `-p /usage`,
    `acp --login` modes driven by env.
- **agent** — `runTurn` branches on `llmcall.IsExternal(chain[0])`; `externalRun` picks
  `Runtime(llmcall.ExternalRuntime(...))`; `applyAccount` fills `Home` for Antigravity accounts
  (`ConfigDir` stays Claude's); resume (`claudeResume` → `externalResume`) filters by the runtime name of
  the target and the account; `SystemMessage.Runtime`, `recordExternalUsage`, and `Limits(provider,
  kind)` use the runtime name instead of the `llmcall.ClaudeCode` literal; the prompt-head tool note gets
  an Antigravity variant (agy's own tools: shell, file edit, search; lana's tools arrive over MCP).
  Background, title, memory capture, compaction, and knowledge passes use `WithoutExternal` chains.
- **mcphost** — unchanged.
- **root package** — `lana_runtime.go`: `RuntimeAgents`, `updateRuntimeModels` (KV key
  `antigravity_models` per account), status for both runtimes; new `lana_agy_signin.go`
  (`StartAntigravitySignIn`, `AntigravitySignInState`, `SubmitAntigravityCode`, `CancelAntigravitySignIn`,
  account create/remove with the folder guard) and `lana_agy_usage.go` (throttle 60 s, merge into the
  limits snapshot with kind `antigravity`, source `usage_probe`). Tier per account kept in KV
  (`antigravity_tier/<account>`) from `session_meta`.
- **web + cmd/lana** — routes per the contract; the providers kinds map gains `antigravity`; the model
  picker gains group "Antigravity" per enabled account; `POST /v1/router/limits/refresh` also refreshes
  Antigravity.
- **web/ui** — `views/router/antigravity-page.tsx` (card + detail, modelled on `claude-code-page.tsx`):
  rows with label, tier, state, `PlanWindows` per group, usage, used-by; "Add account"; sign-in panel with
  the link ("Open Google sign-in"), a code field, Send, Cancel; remove with confirm; terms warning with
  more than one account. Logo `assets/agents/antigravity-color.svg`. Picker group in the shared choice
  code; `isClaudeLimit` → kind check that includes `antigravity`.
- **archtest** — `runtime/antigravity` → core, internal; a test that no Go file outside `testdata`
  mentions `antigravity-oauth-token`, `acp_token`, `find-generic-password`, or `googleapis.com`
  (AC-AGY-005).

## Testing

- Runtime package tests with `fakeagy` and the recorded fixtures: env and argv (AC-AGY-003), one start
  for two turns and the FIFO cap (AC-AGY-004), `session/new` meta and MCP shape (AC-AGY-006), load and
  replay ignored (AC-AGY-007), event mapping (AC-AGY-008), approvals allow/deny/cancelled (AC-AGY-009),
  cancel and kill (AC-AGY-010), usage parse (AC-AGY-012), login link + code on stdin (AC-AGY-013).
- llmcall/config/router tests for addresses, accounts, background chains, proxy 404 (AC-AGY-001, 002,
  011, 016).
- Root and web tests for create/sign-in/code/cancel/remove and the folder guard (AC-AGY-013, 014),
  dashboard status (AC-AGY-015).
- Live: scratch daemon with its own `LANA_HOME` and port; one real turn on the built-in account with a
  tool approval and an `mcp` lana tool call; add a second account with the owner's second Google account;
  check both windows.

## Risks

- agy's `_meta` extensions and `/usage` JSON are not a public contract; minimum version 1.5.17 and the
  fixtures catch drift.
- agy 1.5.17 names lana's MCP tools `mcp_lana_<tool>` (recorded live 2026-11-26); `mcpToolName` accepts
  it next to Claude's `mcp__lana__<tool>`.
- A named account's shell commands run with another `HOME`; git over SSH and `gh` work through the
  links, Keychain-backed helpers do not (ADR 0022).
