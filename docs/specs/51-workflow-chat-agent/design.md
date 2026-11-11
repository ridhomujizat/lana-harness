# Spec 51 design: the workflow guardian chat is a real agent

The work has three parts with fixed contracts between them:

- **Part A**: storage and engine. Covers `store/`, `internal/workflow` (model, store, memstore, engine, candidate service).
- **Part B**: runtime glue, the `workflow_builder` tool, the assistant flow and the HTTP API. Covers `lana_*.go`, `tools/`, `web/`, `cmd/lana/`, `internal/workflow/assistant*.go`.
- **Part C**: the web UI. Covers `web/ui`.

## Part A: model and store

Migration `store/migrations/0011_workflow_candidates.sql`:

```sql
ALTER TABLE workflows ADD COLUMN assistant_session TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN candidate_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE workflow_chat ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_chat ADD COLUMN turn_id TEXT NOT NULL DEFAULT '';
CREATE TABLE workflow_candidates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  base_version INTEGER NOT NULL, graph_json TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '', author TEXT NOT NULL,
  status TEXT NOT NULL,            -- open | superseded | published | discarded
  published_version INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE INDEX workflow_candidates_wf ON workflow_candidates(workflow_id, status);
```

Go model changes in `internal/workflow/model.go`:

```go
// Workflow gains:
AssistantSession string `json:"assistant_session"` // guardian chat session (spec 51)

// Run gains:
CandidateID int64 `json:"candidate_id,omitempty"` // >0: a test run of this candidate

// ChatMessage gains:
SessionID string `json:"session_id,omitempty"`
TurnID    string `json:"turn_id,omitempty"`

// Candidate is a saved but unpublished graph (spec 51 REQ-WFC-003).
type Candidate struct {
    ID               int64  `json:"id"`
    WorkflowID       string `json:"-"`
    BaseVersion      int    `json:"base_version"`
    Graph            Graph  `json:"graph"`
    Summary          string `json:"summary"`
    Author           string `json:"author"`
    Status           string `json:"status"` // open|superseded|published|discarded
    PublishedVersion int    `json:"published_version,omitempty"`
    CreatedAt        int64  `json:"created_at"`
    UpdatedAt        int64  `json:"updated_at"`
}
```

The `Store` interface gains these methods. Implement them in `store/workflow.go` and in `MemStore`:

```go
// CreateCandidate marks any open candidate of the workflow superseded and
// stores c as the new open candidate; it sets ID, Status "open" and times.
CreateCandidate(ctx context.Context, c Candidate) (Candidate, error)
// GetCandidate returns one candidate by id, or ErrNotFound.
GetCandidate(ctx context.Context, id int64) (Candidate, error)
// OpenCandidate returns the workflow's open candidate, or ErrNotFound.
OpenCandidate(ctx context.Context, workflowID string) (Candidate, error)
// UpdateCandidate writes graph, summary, status, published_version, updated_at.
UpdateCandidate(ctx context.Context, c Candidate) error
```

Existing methods must round-trip the new columns:

- `UpdateWorkflow` and `GetWorkflow` and `ListWorkflows`: `assistant_session`.
- `CreateRun`, `UpdateRun` and the run getters: `candidate_id`.
- `AppendChat` and `ListChat`: `session_id` and `turn_id`.

## Part A: service (`internal/workflow/service_candidate.go`)

```go
// SaveCandidate validates g and stores it as the workflow's open candidate,
// based on the current version. Invalid graphs return the issues and store
// nothing (err nil). The returned change is the diff current → candidate.
func (s *Service) SaveCandidate(ctx context.Context, id string, g Graph, summary, author string) (Candidate, GraphChange, []Issue, error)
// OpenCandidate returns the open candidate (ok=false when none).
func (s *Service) OpenCandidate(ctx context.Context, id string) (Candidate, bool, error)
// TestCandidate starts a run of the open candidate with trigger "test" (or of
// the current version when there is none, candidate_id 0). It refuses with
// ErrRunActive like StartRun. It does not wait.
func (s *Service) TestCandidate(ctx context.Context, id string, input json.RawMessage) (Run, error)
// PublishCandidate saves the open candidate as a new version with
// SaveVersion(base = candidate.BaseVersion, author "guardian"); it returns
// ErrVersionConflict when the current version moved, ErrNotFound when there is
// no open candidate. On success the candidate is "published", the lock/status
// rule of Assist applies (active and lock no longer covers → draft), and the
// change (diff of the base version → new version, From/To set, Summary) is
// returned. It broadcasts the change.
func (s *Service) PublishCandidate(ctx context.Context, id string) (GraphChange, Version, error)
// DiscardCandidate marks the open candidate discarded (no-op when none).
func (s *Service) DiscardCandidate(ctx context.Context, id string) error
// SetAssistantSession stores the guardian chat session id on the workflow.
func (s *Service) SetAssistantSession(ctx context.Context, id, sessionID string) error
// AssistantWorkflow returns the workflow whose assistant_session is sessionID.
func (s *Service) AssistantWorkflow(ctx context.Context, sessionID string) (Workflow, bool)
// AppendChat stores a chat message (exported for the tool's change card).
func (s *Service) AppendChat(ctx context.Context, m ChatMessage) (ChatMessage, error)
```

`RunDetail` must show the graph the run ran. For `candidate_id > 0`, that is the candidate graph.

## Part A: engine rules for candidate runs (`run.CandidateID > 0`)

- **Graph source.** The run's graph is the candidate's graph, not `GetVersion(run.Version)`. `run.Version` holds the candidate's base version.
- **Write-backs.** These normally call `SaveVersion` on `wf.Version`:
  - `saveFixVersion` (engine.go);
  - learned and updated Script Parse scripts (engine_script.go);
  - script revert.

  For a candidate run they instead patch the candidate's graph with `UpdateCandidate` and save no version. Route every one of them through one helper.
- **Failures.**
  - A candidate run does not change `ConsecutiveFailures` or the workflow status.
  - A failed node in a candidate run does not call the guardian diagnosis and makes no ask decision. After the node's own retry policy, the run fails with the node error.
  - The run sends no failure notification to the owner.
- **Approval nodes.** These behave as in any run (they wait). The test tool reports `waiting`.

## Part B: runtime and the guardian session

- **Session.** `Service.Assistant*` uses a new optional Deps extension (`internal/workflow/deps.go`):

  ```go
  type AssistantRuntime interface {
      // CreateAssistantSession creates a session for the guardian on channel
      // "workflow", address = workflow id, titled "<workflow name> · chat".
      CreateAssistantSession(ctx context.Context, wf Workflow) (string, error)
      // RunAssistantTurn appends note as a hidden runtime note, runs text (+
      // attachments) as an owner turn on the session and blocks until the turn
      // ends; it returns the final assistant text and the turn id.
      RunAssistantTurn(ctx context.Context, sessionID, note, text string, attachments []string) (text string, turnID string, tokens int64, err error)
  }
  ```

  The web path does not use `RunAssistantTurn`. It calls `webBackend.SendNote`, so the turn is a normal web turn with stream, cancel and queueing.
- **Context note.** `Service.AssistantNote(ctx, id, sessionID) (string, error)` builds the note.
  - The first note per session is the full guide. "First" is tracked in memory, so a daemon restart sends the full guide again, which is fine.
  - Later notes hold only the delta.
  - The guide replaces `assistantReplyContract`. The guardian answers in plain text, edits only through `workflow_builder`, and follows this order: save → test → read → fix → publish. It uses `ask_user` for real side effects and ambiguity.
- **Tool `workflow_builder`** (`tools/workflow_builder.go`).
  - Bound through `AssistantWorkflow(tc.SessionID)`. Any other session gets an error result.
  - `save {graph, summary}` returns the candidate id, the diff, or the issues.
  - `test {input?}` waits up to 2 min, polling `RunDetail`. It returns the same digest the `workflow` tool's `run` action returns.
  - `publish {}`: on success it appends a guardian `ChatMessage{Text: summary, Change, SessionID: tc.SessionID, TurnID: tc.TurnID}`. A conflict tells the guardian to `get` and `save` again.
  - `get` and `discard`.
  - Risk: `get` is read; `save`, `discard` and `publish` are write; `test` is write (same as `workflow test`).
- **`Assist`.** The sync path used by the main-chat `workflow` tool:
  1. Ensures the session and builds the note.
  2. Calls `RunAssistantTurn`.
  3. Returns `AssistResult{Message: {Role: guardian, Text: final text, Change: the last change card of that turn}}`.

  The owner's message is not appended to `workflow_chat`. It lives in the session.

## Part B: HTTP (`web/workflows.go`)

| Route | Body | Response |
|---|---|---|
| `GET /v1/workflows/{id}/assistant` | | `{session_id, messages: ChatMessage[]}`. `session_id` is `""` until the first use. |
| `POST /v1/workflows/{id}/assistant/session` | | `{session_id}`. Ensures the session (lazy create), so the UI can upload before the first message. |
| `POST /v1/workflows/{id}/assistant` | `{message, attachments?: string[]}` | `202 {session_id, turn, turn_id}`. Ensures the session, builds the note and calls `SendNote`. A turn already running queues the message, as in chat. |
| `POST /v1/workflows/{id}/assistant/new` | | `{session_id}`. A fresh session; the old one stays in History. |

All other session routes are unchanged:

- stream: `/v1/sessions/{sid}/stream`;
- events and turns;
- cancel;
- attachments upload and serve;
- questions;
- approvals.

## Part C: web UI

`WorkflowAssistant` keeps the new-workflow screen as it is: the guardian picker and the idea cards. Once there is a workflow, it shows the session timeline. It reuses the main chat building blocks and does not copy them:

- the turn/step model and `TurnBlock`, from `turn.tsx`;
- the stream client, from `api/client.ts` `openStream`;
- `QuestionCard`;
- the approval UI;
- `AttachButton`, `AttachmentChips`, `useAttachmentDraft`, `useFileDrop` and `pasteImageFiles`;
- `useSlashMenu` / `SlashMenu` with `/new`;
- `ContextMeter`.

Change cards are rendered after the turn whose `turn_id` matches. Legacy messages (no `session_id`) render first.
