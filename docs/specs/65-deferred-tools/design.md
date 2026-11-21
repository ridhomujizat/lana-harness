# Design: Deferred tool loading

- **Requirements**: [requirements.md](requirements.md) (revision 1)
- **Contract**: [api-contract.md](api-contract.md) (revision 1)

## Data flow of one native turn

1. `offeredTools` is unchanged and still returns every offered tool. `agent/external.go` (Claude
   Code) keeps using it as is (REQ-DTL-001, AC-DTL-004).
2. The native turn loop (`agent/turn.go`, the `!promptBuilt` branch) passes the list to a new
   `planDeferral` (`agent/deferred.go`). It returns:
   - `base`: the non-deferred tools, plus `tool_search` when deferral is on, registered, and not in
     `disabled_tools`, sorted by name;
   - `deferrable`: the deferrable tool names, sorted (empty when deferral is off).
3. The turn reads the session chain (`store.ChainEvents`) once and takes the `tools/loaded` names
   after the latest `compaction/summary`, in event order, dropping names not in `deferrable` and
   duplicates (`loadedFromEvents`). `turnTools = base + loaded`.
4. `buildPrompt` gets the `deferrable` names. `agenthome` renders the `deferred_tools` section from
   them (REQ-DTL-003). The section depends only on the deferrable set, never on the loaded set.
5. `toolCtx` gets `DeferredTools`: the `core.ToolSpec` of every deferrable tool, for `tool_search`.
6. Each step sends `Specs(turnTools)`.
7. Tool calls run through `pipeline.Run` with `enabled = turnTools + (deferrable not loaded)`. So a
   direct call to a deferred tool passes the offered check, name repair, input validation, and the
   permission hooks like any tool (REQ-DTL-005).
8. After the step's calls, the turn collects new loads:
   - the `Loaded` names a `tool_search` output reports;
   - the final (repaired) name of every call to a deferrable tool not yet loaded.
   New names, deduplicated and in that order, are appended to `turnTools` and recorded in one
   `tools/loaded` event. The next step sends them (REQ-DTL-002).
9. For a call to a not-yet-loaded deferrable tool whose output is an `invalid input:` error, the turn
   appends the tool's description and input schema to the error text before the result is stored.

## Deferral decision (`planDeferral`)

- Deferrable: a name `mcp__<server>__<tool>` whose server's `[mcp.<server>] defer` is not `false`.
- Off when the agent's `defer_tools` is `false`, or when there are no deferrable tools.
- Absent `defer_tools`: on only when the deferrable count is greater than 15 (`deferThreshold`).
- `tool_search` joins `base` only when deferral is on.

## `tool_search` (`tools/tool_search.go`)

- Reads `tc.DeferredTools`. Without any, it returns an error ("no deferred tools").
- `select:` takes comma-separated names. A full name matches exactly; a bare `<tool>` part matches
  when exactly one deferrable tool has it. Unknown names are reported in the output; known ones are
  returned.
- Keyword query: lowercase, split on non-alphanumerics, drop words shorter than 2 characters. A
  tool's score sums, per query word: 3 when a word of the tool part of its name equals it, 2 when the
  server name contains it, 1 when its description contains it. Ties keep name order.
- Output per API-DTL-003; `core.ToolOutput.Loaded` carries the returned names.

## Persistence

`tools/loaded` (`core.KindToolsLoaded`, payload `core.ToolsLoaded{V, Names}`). The chain includes a
parent's events for a child session; a child keeps only names in its own deferrable set, so this is
harmless. The web chat and any Go session view skip the kind (AC-DTL-016).

## Prefix cache

- The tool list of a turn without a new load equals the previous turn's (AC-DTL-006).
- The `deferred_tools` section does not change on load, so no `<system-update>` (AC-DTL-008).
- A load costs one cache miss on the next step (requirements, Known cost).
