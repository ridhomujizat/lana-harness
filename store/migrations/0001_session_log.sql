CREATE TABLE session_events (
  session_id TEXT    NOT NULL,
  seq        INTEGER NOT NULL,
  kind       TEXT    NOT NULL,
  payload    TEXT    NOT NULL,
  turn_id    TEXT,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;

CREATE TABLE sessions (
  id                TEXT    PRIMARY KEY,
  agent             TEXT    NOT NULL,
  channel           TEXT    NOT NULL,
  address           TEXT    NOT NULL,
  workspace         TEXT    NOT NULL,
  title             TEXT    NOT NULL DEFAULT '',
  status            TEXT    NOT NULL CHECK (status IN ('idle', 'running')),
  parent_session_id TEXT    REFERENCES sessions (id) ON DELETE RESTRICT,
  inherited_seq     INTEGER,
  last_seq          INTEGER NOT NULL,
  turn_count        INTEGER NOT NULL DEFAULT 0,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);

CREATE INDEX idx_sessions_channel_address_updated ON sessions (channel, address, updated_at DESC);
CREATE INDEX idx_session_events_kind_created ON session_events (kind, created_at);

CREATE TABLE session_prune_allow (session_id TEXT PRIMARY KEY) WITHOUT ROWID;

CREATE TRIGGER session_events_no_update
BEFORE UPDATE ON session_events
WHEN NOT EXISTS (SELECT 1 FROM session_prune_allow p WHERE p.session_id = OLD.session_id)
BEGIN
  SELECT RAISE(ABORT, 'session_events is append-only');
END;

CREATE TRIGGER session_events_no_delete
BEFORE DELETE ON session_events
WHEN NOT EXISTS (SELECT 1 FROM session_prune_allow p WHERE p.session_id = OLD.session_id)
BEGIN
  SELECT RAISE(ABORT, 'session_events is append-only');
END;
