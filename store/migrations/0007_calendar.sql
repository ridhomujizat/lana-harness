CREATE TABLE calendar_items (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, title TEXT NOT NULL, notes TEXT NOT NULL DEFAULT '',
  start_ms INTEGER NOT NULL DEFAULT 0, end_ms INTEGER NOT NULL DEFAULT 0, all_day INTEGER NOT NULL DEFAULT 0,
  date TEXT NOT NULL DEFAULT '', end_date TEXT NOT NULL DEFAULT '', time TEXT NOT NULL DEFAULT '',
  repeat_json TEXT NOT NULL DEFAULT '{}', assignee TEXT NOT NULL DEFAULT '', color TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT '', action_json TEXT NOT NULL DEFAULT 'null',
  google_linked INTEGER NOT NULL DEFAULT 0, google_calendar TEXT NOT NULL DEFAULT '', google_event TEXT NOT NULL DEFAULT '',
  google_etag TEXT NOT NULL DEFAULT '', google_sync TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '', updated_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, deleted_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX calendar_items_live ON calendar_items(deleted_at, created_at, id);
CREATE TABLE calendar_overrides (
  source TEXT NOT NULL, ref TEXT NOT NULL, occurrence INTEGER NOT NULL, op TEXT NOT NULL,
  to_ms INTEGER NOT NULL DEFAULT 0, author TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL DEFAULT 0,
  fired_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (source, ref, occurrence));
CREATE TABLE calendar_firings (
  item_id TEXT NOT NULL, occurrence INTEGER NOT NULL, status TEXT NOT NULL,
  started_at INTEGER NOT NULL DEFAULT 0, ended_at INTEGER NOT NULL DEFAULT 0, session_id TEXT NOT NULL DEFAULT '',
  run_workflow TEXT NOT NULL DEFAULT '', run_number INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (item_id, occurrence));
CREATE INDEX calendar_firings_status ON calendar_firings(status, occurrence);
CREATE INDEX calendar_firings_occurrence ON calendar_firings(occurrence);
CREATE TABLE calendar_habit_checks (
  item_id TEXT NOT NULL, date TEXT NOT NULL, author TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (item_id, date));
CREATE TABLE calendar_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT, item_id TEXT NOT NULL, at INTEGER NOT NULL, author TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '', snapshot TEXT NOT NULL DEFAULT '');
CREATE INDEX calendar_history_item ON calendar_history(item_id, at);
CREATE INDEX calendar_history_at ON calendar_history(at);
CREATE TABLE calendar_google_events (
  calendar_id TEXT NOT NULL, event_id TEXT NOT NULL, etag TEXT NOT NULL DEFAULT '', item_id TEXT NOT NULL DEFAULT '',
  event_json TEXT NOT NULL DEFAULT '', updated_ms INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (calendar_id, event_id));
CREATE TABLE calendar_kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);
