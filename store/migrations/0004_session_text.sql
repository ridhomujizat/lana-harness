CREATE TABLE session_text (
  session_id TEXT    NOT NULL,
  seq        INTEGER NOT NULL,
  role       TEXT    NOT NULL CHECK (role IN ('user', 'assistant')),
  created_at INTEGER NOT NULL,
  text       TEXT    NOT NULL,
  UNIQUE (session_id, seq)
);

CREATE VIRTUAL TABLE session_text_fts USING fts5(
  text,
  content = 'session_text',
  content_rowid = 'rowid',
  tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TRIGGER session_text_ai AFTER INSERT ON session_text BEGIN
  INSERT INTO session_text_fts (rowid, text) VALUES (new.rowid, new.text);
END;

CREATE TRIGGER session_text_ad AFTER DELETE ON session_text BEGIN
  INSERT INTO session_text_fts (session_text_fts, rowid, text) VALUES ('delete', old.rowid, old.text);
END;
