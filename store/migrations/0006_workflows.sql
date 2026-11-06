CREATE TABLE workflows (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
  guardian TEXT NOT NULL, status TEXT NOT NULL, version INTEGER NOT NULL,
  lock_json TEXT NOT NULL DEFAULT '', failure_json TEXT NOT NULL DEFAULT '{}',
  webhook_token TEXT NOT NULL DEFAULT '', consecutive_failures INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE workflow_versions (
  workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE, version INTEGER NOT NULL,
  graph_json TEXT NOT NULL, author TEXT NOT NULL, summary TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
  PRIMARY KEY (workflow_id, version));
CREATE TABLE workflow_runs (
  id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  number INTEGER NOT NULL, version INTEGER NOT NULL, trigger TEXT NOT NULL, input_json TEXT NOT NULL DEFAULT 'null',
  status TEXT NOT NULL, waiting_reason TEXT NOT NULL DEFAULT '', wake_at INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '', tokens INTEGER NOT NULL DEFAULT 0, nodes_done INTEGER NOT NULL DEFAULT 0,
  nodes_total INTEGER NOT NULL DEFAULT 0, summary TEXT NOT NULL DEFAULT '', overrides_json TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL DEFAULT 0, UNIQUE (workflow_id, number));
CREATE INDEX workflow_runs_status ON workflow_runs(status);
CREATE TABLE workflow_node_runs (
  run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE, node_id TEXT NOT NULL,
  item_index INTEGER NOT NULL, status TEXT NOT NULL, attempts_json TEXT NOT NULL DEFAULT '[]',
  input_json TEXT NOT NULL DEFAULT 'null', output_json TEXT NOT NULL DEFAULT 'null', port TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '', logs_json TEXT NOT NULL DEFAULT '[]', tokens INTEGER NOT NULL DEFAULT 0,
  session_id TEXT NOT NULL DEFAULT '', started_at INTEGER NOT NULL DEFAULT 0, ended_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (run_id, node_id, item_index));
CREATE TABLE workflow_decisions (
  id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  run_id TEXT NOT NULL, run_number INTEGER NOT NULL, node_id TEXT NOT NULL, item_index INTEGER NOT NULL,
  kind TEXT NOT NULL, action TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', fix_json TEXT NOT NULL DEFAULT '',
  persistent INTEGER NOT NULL DEFAULT 0, confidence REAL NOT NULL DEFAULT 0, tokens INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL, choice TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL DEFAULT 0, resolved_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX workflow_decisions_status ON workflow_decisions(status);
CREATE TABLE workflow_state (
  workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE, key TEXT NOT NULL,
  value_json TEXT NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY (workflow_id, key));
CREATE TABLE workflow_chat (
  id INTEGER PRIMARY KEY AUTOINCREMENT, workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  role TEXT NOT NULL, text TEXT NOT NULL, change_json TEXT NOT NULL DEFAULT '', issues_json TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL);
