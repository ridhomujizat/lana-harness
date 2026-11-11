# Spec 51: The workflow guardian chat is a real agent

- **Status**: Approved (owner interview 2026-11-12)
- **Branch**: `feature/workflow-chat`, from `origin/development` 2c0a639

## Problem

The guardian assistant column in the workflow editor makes one LLM call. That call may not use tools and must answer in JSON (`internal/workflow/assistant.go`). This causes four problems:

- The guardian proposes graphs without testing them. Owner: "agent selalu generate tapi ga test dulu".
- The owner sees no tool calls. They want it to "implement kaya di chat aja".
- The owner cannot upload files.
- The guardian cannot ask the owner a structured question the way `ask_user` does in the main chat.

## Decisions (owner, 2026-11-12)

1. **One persistent guardian session per workflow.** The chat column is a real agent session on channel `workflow` (address = workflow id). It reuses the main chat machinery: SSE stream, tool rows, `ask_user` question card, approvals, attachments and the context meter. `/new` starts a fresh session.
2. **Candidate versions.** The guardian saves its graph as a candidate. A candidate is not the current version. The guardian tests the candidate, then publishes it to make it current or discards it. The current version never breaks while the guardian experiments.
3. **Coding.** Opus writes the code directly. dsh-coder is not used.

## Requirements

- **REQ-WFC-001.** Each workflow has at most one live guardian chat session. Its id is stored on the workflow as `assistant_session`. The session is created lazily on the first message or upload. `POST /v1/workflows/{id}/assistant/new` replaces it with a fresh session.
- **REQ-WFC-002.** An owner message to the guardian:
  - starts a turn on that session;
  - is preceded by a hidden runtime note with the workflow context. The first note of a session holds the full guide: goal, node catalog, rules, environment, current graph and last run. Later notes hold only what changed: the graph if the version moved, and the last run if it is new.
  - may carry attachments uploaded with `POST /v1/sessions/{session}/attachments`.
- **REQ-WFC-003.** The guardian edits the workflow only through the `workflow_builder` tool. The tool has these actions:
  - `get`: current graph and open candidate.
  - `save`: validate, then store a candidate.
  - `test`: run the candidate, or the current version when there is no candidate, and wait for the result.
  - `publish`: make the candidate the current version.
  - `discard`: drop the candidate.

  The tool works only in that workflow's guardian chat session.
- **REQ-WFC-004.** The guardian tests before it publishes. The prompt tells it to:
  - save the candidate, then test it, read the result, and fix it until the test passes;
  - publish only after that, then report.

  When a test would cause an unwanted real side effect, such as messaging people or paying, the guardian asks the owner with `ask_user` first. It also uses `ask_user` when a requirement is ambiguous.
- **REQ-WFC-005.** A candidate test run is a normal run with trigger `test` and `candidate_id` set.
  - It runs the candidate graph.
  - It does not count toward `consecutive_failures` and does not set the workflow to Error.
  - It does not pause for guardian diagnosis: a failed node fails the run, and the guardian in the chat reads the error.
  - Write-backs during the run patch the candidate, not the current version. These are Script Parse learned scripts and script updates.
- **REQ-WFC-006.** Publishing saves the candidate graph as a new version with `SaveVersion(base = candidate.base_version)`.
  - It refuses with a version conflict when the current version moved, for example because the owner edited the canvas. The guardian then re-reads and saves again.
  - On success, it appends a change card to the workflow chat. The card's `session_id` and `turn_id` point to the turn that published it. Undo works as today.
  - An active workflow whose lock no longer covers the graph goes back to draft, as before.
- **REQ-WFC-007.** The web chat column shows the guardian session the way the main chat does. That covers owner messages, attachment chips, turns with tool steps, the final markdown reply, `ask_user` cards, approval cards and a running indicator with Stop. Change cards appear after the turn that published them. Messages from before this spec (no session) still render above the session timeline.
- **REQ-WFC-008.** The composer has an attach button, paste and drop for images and files, a `/new` slash command and the context meter. It shows one focus ring.
- **REQ-WFC-009.** The main-chat `workflow` tool's `create` and `edit` actions still work. They send the request to the guardian session, wait for the turn, and report its reply and the last change.
- **REQ-WFC-010.** UI fixes:
  - The composer's double focus ring is gone.
  - Long Proposed fix values wrap in a scrollable code block and no longer overflow the side panel.
