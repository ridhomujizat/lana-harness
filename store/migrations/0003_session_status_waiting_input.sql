-- SQLite cannot alter a CHECK constraint in place, so rebuild the sessions
-- table per the documented table-rebuild procedure. The self-reference points
-- at the final table name because the rebuild runs with foreign keys disabled,
-- which also disables SQLite's automatic REFERENCES rewriting on rename.
CREATE TABLE sessions_new (
  id                TEXT    PRIMARY KEY,
  agent             TEXT    NOT NULL,
  channel           TEXT    NOT NULL,
  address           TEXT    NOT NULL,
  workspace         TEXT    NOT NULL,
  title             TEXT    NOT NULL DEFAULT '',
  status            TEXT    NOT NULL CHECK (status IN ('idle', 'running', 'waiting_input')),
  parent_session_id TEXT    REFERENCES sessions (id) ON DELETE RESTRICT,
  inherited_seq     INTEGER,
  last_seq          INTEGER NOT NULL,
  turn_count        INTEGER NOT NULL DEFAULT 0,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);

INSERT INTO sessions_new (id, agent, channel, address, workspace, title, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at)
SELECT id, agent, channel, address, workspace, title, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at
FROM sessions;

DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;

CREATE INDEX idx_sessions_channel_address_updated ON sessions (channel, address, updated_at DESC);

PRAGMA foreign_key_check;
