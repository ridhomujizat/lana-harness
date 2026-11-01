# Requirements: Session search

- **Issue**: —
- **Status**: Approved (on behalf of the owner, gate lifted 2026-11-01)
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Not required
- **Related ADR**: 0009 (session as append-only event log)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-11-01 | "soal knowladge dan memory sekarang hanya ngandelin memory.md kah?", then "nah saya ingin ada berkepanjangan knowladge gitu, jadi seperti secondbraind manusia", then "oke sip implement 3 spec yg kamu usulkan" | goal |
| Review 2026-11-01 | lana recalls past conversations only through `MEMORY.md` and `USER.md`. A detail that was never saved, or that was compacted out of a long Telegram session, is lost to the model. | problem |
| hermes-agent | `tools/session_search_tool.py:1-9` (FTS5 over the session DB, no LLM calls, discovery and scroll shapes, only the top hit hydrated), `hermes_state_common.py:941` (external-content FTS5 table) | reference design |

## Goal

The agent can find and re-read what was said in earlier conversations, including the parts of the
current session that compaction removed from the model view. No LLM call and no setup is needed.

## Definitions

- **Conversation text**: the text blocks of `user/message` and `assistant/message` events. It
  excludes runtime notes (`runtime: true`), the leading `context_blocks` blocks lana injected,
  thinking, tool calls, tool results, and image bytes.
- **Searchable session**: a root session (no parent) or a fork (a child with `inherited_seq`). A
  session started by a tool call (subagent or team member: parent set, no `inherited_seq`) is not
  searchable.

## Requirements

### Index

- **REQ-SSR-001** lana keeps a full-text index of conversation text. A row is written in the same
  database write as the event it comes from, so the index never lags the log.
- **REQ-SSR-002** Pruning a session removes its rows from the index.
- **REQ-SSR-003** On upgrade, a database with existing sessions is indexed once, inside the schema
  migration. A failed backfill fails the migration and leaves the database at the old version.

### Tool

- **REQ-SSR-010** A built-in tool `session_search`, risk `read`, in the tool catalog group "Memory".
  An agent gets it only when its `tools` list names it.
- **REQ-SSR-011** Discovery: given `query`, the tool returns at most `limit` sessions (default 5,
  max 10), best first. A session's rank is the rank of its best matching message. Words of the
  query are matched as alternatives, so a message that holds more of them ranks higher; the query
  never fails on FTS syntax characters.
- **REQ-SSR-012** Each discovery result shows the session id, agent, channel, title, and date of
  the matching message, and the matching message as a snippet with the matched words marked. The
  first result also shows up to 3 conversation messages before and after the match, each cut to
  400 characters.
- **REQ-SSR-013** Scroll: given `session_id` and `around_seq`, the tool returns up to 8
  conversation messages before and after that seq in that session, each cut to 1500 characters.
  `session_id` accepts a unique prefix, like the CLI.
- **REQ-SSR-014** Only searchable sessions are searched. Optional `agent` limits discovery to one
  agent's sessions.
- **REQ-SSR-015** In the calling session, only messages that compaction removed from the model
  view are returned (seq at or below the latest `compaction/summary` `upto_seq` of the session).
  A session without a compaction returns no hits for itself.
- **REQ-SSR-016** Output is plain text, at most 12,000 characters; longer output is cut with a
  marker. An empty or whitespace query without `session_id` is an input error. No hit returns
  `No past conversation matched "<query>".`
- **REQ-SSR-017** The tool has a short prompt note: use `session_search` when the owner refers to an
  earlier conversation or when a needed detail is not in memory or the current context; quote what
  was found instead of guessing.
- **REQ-SSR-018** The loop guard treats `session_search` as a read-only tool.

## Acceptance criteria

- **AC-SSR-001** (REQ-SSR-001, -002) Appending a user and an assistant message makes both
  searchable at once; a runtime note and an injected `<context>` block are not; pruning the session
  removes its rows. Evidence: store unit test.
- **AC-SSR-002** (REQ-SSR-003) A version-3 database with events migrates to version 4 and its old
  messages are found. Evidence: store migration test.
- **AC-SSR-003** (REQ-SSR-011, -012, -014, -015) Two sessions match "deploy staging"; the one
  holding both words ranks first; a subagent session holding the words is absent; the calling
  session's un-compacted messages are absent. Evidence: tool unit test.
- **AC-SSR-004** (REQ-SSR-013, -016) Scroll returns the window in seq order; a query of `"(*` does
  not error; output never exceeds 12,000 characters. Evidence: tool unit test.
- **AC-SSR-005** (REQ-SSR-010, -017, -018) The tool is in the "Memory" catalog group, its prompt note
  renders when offered, and three identical calls hit the read-only loop rule. Evidence: unit tests.

## Out of scope

- Memory flush before compaction: moved to spec 31 (journal pass), because its target is the
  knowledge vault of spec 30.
- Embeddings for transcripts. Vectors belong to the knowledge vault (spec 30).
- Searching tool results and thinking.
- A web search page for sessions.

## Resolved questions

| Question | Resolution |
|---|---|
| Search other agents' sessions? | Rekomendasi agent: yes. lana has one owner, so all agents' sessions are the owner's history. `agent` narrows it. |
| Include subagent sessions? | Rekomendasi agent: no. Their results reach the parent as tool results; their own chatter is noise. |
| AND or OR for words? | Rekomendasi agent: OR with BM25. hermes notes that AND makes recall depend on the model's query wording. |
