# 0021. Run guardian-written workflow scripts in an embedded JavaScript sandbox

- **Status**: Accepted
- **Date**: 2026-11-08
- **Deciders**: Ridho
- **Related ADRs**: [0013](0013-use-go-as-single-binary.md)

## Context

Many workflow steps only extract or reshape data whose shape rarely changes: a trending page, a
Jira or ClickUp list from an MCP tool, a JSON API. Today such a step is an agent node, so every run
pays tokens for work that a small function can do. Spec 44 adds the Script Parse node. The guardian
writes the function once, every run executes it, and an agent fallback plus regeneration handles
changes in the source shape.

The function needs a runtime that:
- the guardian (an LLM) writes reliably;
- runs inside the single lana binary with no host runtime (ADR 0013);
- cannot touch files, the network, processes, or tools, because the script is written by a model that
  has read untrusted input;
- can be stopped on time.

Options:
1. **Embedded JavaScript (goja)**: pure Go, ES2020 in practice, no I/O unless the host exposes it, and
   `Interrupt` for timeouts. LLMs write JavaScript parsing code well.
2. **jq (gojq)**: pure Go and safe, but weak for Markdown and HTML text.
3. **Python or Node through the `bash` node**: needs a host runtime, has no sandbox, and starts slowly.
4. **Starlark**: safe and deterministic, but LLMs write it less reliably, and it has no regex.

## Decision

1. Workflow scripts are JavaScript run by **goja** in a new package `internal/scriptrt`.
2. Each run gets a fresh VM. The host exposes only `console.log` and an `html()` helper backed by
   **goquery**, which parses strings and fetches nothing.
3. The limits are 32 KiB of script, 2 MiB of input, 1 MiB of output, and 100 ms..10 s of time. They are
   enforced by size checks and `vm.Interrupt`.
4. A script is data in the workflow graph. Changes are workflow versions, so every change can be
   audited and reverted.

## Consequences

**Positive**
- Fixed-shape steps cost zero tokens per run, and they finish in milliseconds.
- No new host dependency; the binary stays self-contained.
- The worst a malicious or wrong script can do is return a wrong value or burn up to 10 s of CPU.
  Contract checks catch most wrong values and send the run to the agent fallback.

**Negative**
- goja has no memory limit. A script can allocate a lot within its time limit. The size caps reduce
  this risk; a hard limit needs a separate process, which this ADR does not choose.
- Two new modules (goja, goquery with cascadia) enlarge the binary by about 3 MB.
- Script output that passes the contract can still be wrong, for example in a field that is not
  checked. The owner sees the output in Test and in run detail.
