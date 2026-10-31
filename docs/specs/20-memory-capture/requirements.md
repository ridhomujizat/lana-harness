# Requirements: Memory capture and tool-call safety nets

- **Issue**: —
- **Status**: Implemented
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: — (config key and one event kind, listed below)
- **Related ADR**: 0009 (session as append-only event log)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-31 | "kenapa agent selalu sulit untuk memanggil tool? pelajari https://github.com/anvie/evonic" then "lanjut semuanya seperti evonic" | goal |
| Incident 2026-10-31 | Session `06GS2SGJJSEVXER9PGXKGS0YZG`: the owner said to call them "boss"; the agent replied "Siap, bosss" and made no tool call. `~/.lana/USER.md` kept `Dipanggil "Pak Ridho"`, so session `06GS2SKVCH70CEBAVX4WFTDN64` still said "Pak". | problem |
| Review 2026-10-31 | The only memory guidance in the prompt is the worker template line "Save lessons about the environment ... with memory_write". Nothing tells the model to save user preferences or corrections. | problem |
| evonic | `backend/agent_runtime/context.py:323` (Message Wrapper Protocol, on by default); `runtime.py:2770` (background summarize after every turn); `memory_manager.py:60` (extract prompt: user_info, preference, decision, context, instruction), `:80` (dedup: identical = skip, contradicting = update); `llm_loop.py:2110` (malformed tool-call nudge), `:2150` (empty-response follow-up), `:2166` (continuation nudge, disabled because of false positives) | reference design |

## Goal

Durable facts about the owner (how to address them, language, style, standing instructions) are
saved even when the model does not call `memory_write` on its own, and a contradicting fact
replaces the old entry instead of piling up next to it.

## Requirements

### Prompt protocol

- **REQ-MEM-001** An agent with the `memory_write` tool gets a fixed `# Memory capture` section in
  its head prompt. It tells the model: after every owner message, check for a new durable
  preference, instruction, correction, or personal fact, explicit or implied; save it at once with
  `memory_write` (`target=user` for facts about the owner, `target=memory` for its own notes);
  when it contradicts an existing entry, use `action=replace` with `old_text` of that entry;
  never save secrets (passwords, API keys, tokens). The text is constant, so it does not change the
  prompt prefix between turns (REQ-LTC-001).

### Background capture

- **REQ-MEM-010** After a root-session turn ends with reason `end_turn` or `max_steps`, and the
  turn stored at least one owner message, lana runs one background capture pass for that turn
  when the agent has `memory_write` and `memory_capture` is not `false` (default `true`). Child
  sessions (subagents, team members) are skipped. The turn result is never delayed by capture.
- **REQ-MEM-011** The capture request reuses the warm prefix: the turn's last head prompt, the
  same sorted tool declarations, the derived history, then one user message with the capture
  instruction. The instruction includes the current entries of the user file and the agent memory
  file, and asks only about the owner messages of the latest turn.
- **REQ-MEM-012** The model answers with a JSON array of at most 5 operations
  `{"target":"user"|"memory","action":"add"|"replace"|"remove","content":"...","old_text":"..."}`,
  or `[]`. Only durable facts qualify: identity, how to address the owner, language and style
  preferences, standing instructions, decisions, and lasting environment facts. Task status and
  one-off details do not. A fact already present is skipped; a fact that contradicts an entry
  becomes `replace`.
- **REQ-MEM-013** Each operation goes through the same validation and file lock as the
  `memory_write` tool (limits, separator, exact-one `old_text` match). An operation whose content
  is over 300 characters or looks like a secret is dropped. Invalid JSON, tool calls in the reply,
  or a provider error drop the whole pass. Failures are logged and never surface as a turn error.
- **REQ-MEM-014** A pass that applied at least one operation appends one `memory/captured` event
  to the session with the applied operations and the pass usage. The event is not part of the
  model history. The next turn picks up the changed files through the existing `<system-update>`
  path (REQ-LTC-002).
- **REQ-MEM-015** At most one pass runs per session at a time. A pass that is still running when
  the next turn ends is not duplicated; the next turn's pass covers the newer messages. A pass
  has a 90 s timeout and is cancelled at daemon shutdown.

### Loop safety nets

- **REQ-MEM-020** When an assistant message has no native tool call but its text contains
  tool-call markup (DeepSeek `<｜tool▁calls▁begin｜>` / `<｜DSML｜` tokens, `<tool_call>` tags,
  a fenced `tool`/`tool_call` block, or a bare JSON object with `name` and `arguments`), lana
  runs one more step whose input is a runtime note telling the model to call the tool through the
  native tool-calling interface. At most 2 such notes per turn.
- **REQ-MEM-021** Runtime notes (this note, the empty-answer note REQ-TFB-008, and the step-limit
  note REQ-LTC-040) are stored with `runtime: true` on the `user/message` event. Display surfaces
  hide them from the owner's chat bubbles.
- **REQ-MEM-022** No "I will continue" nudge: evonic disabled it because it held back finished
  reports.

### Display

- **REQ-MEM-030** The web session view shows a `memory/captured` event as a muted line under the
  turn, for example `Saved to memory: Dipanggil "boss"`.

## Contract additions

- Agent config key `memory_capture` (bool, default `true`). Web agent API field of the same name.
- Event kind `memory/captured`, payload `{v, turn_id, ops:[{target,action,content,old_text}], usage}`.
- `user/message` payload field `runtime` (bool, omitted when false).

## Out of scope

- Semantic or graph memory search (evomem). lana keeps the two small files in the prompt.
- Channel-provided display names overriding memory (evonic "Current User"); it would conflict
  with an owner-chosen form of address.
- Capture for scheduled turns: the scheduler spec decides.
