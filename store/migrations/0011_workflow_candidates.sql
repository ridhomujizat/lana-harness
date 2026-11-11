-- Spec 51: the workflow guardian chat is a real agent session. A workflow keeps
-- its guardian session id, a run may test an unpublished candidate graph, and a
-- chat change card points at the session turn that published it.
ALTER TABLE workflows ADD COLUMN assistant_session TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN candidate_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE workflow_chat ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_chat ADD COLUMN turn_id TEXT NOT NULL DEFAULT '';
CREATE TABLE workflow_candidates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  base_version INTEGER NOT NULL, graph_json TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '', author TEXT NOT NULL,
  status TEXT NOT NULL,
  published_version INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE INDEX workflow_candidates_wf ON workflow_candidates(workflow_id, status);
