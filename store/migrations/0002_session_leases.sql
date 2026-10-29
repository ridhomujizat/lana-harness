CREATE TABLE session_leases (
  session_id  TEXT    PRIMARY KEY,
  pid         INTEGER NOT NULL,
  host        TEXT    NOT NULL,
  acquired_at INTEGER NOT NULL
);
