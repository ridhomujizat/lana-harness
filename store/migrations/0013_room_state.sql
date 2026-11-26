-- Spec 85 REQ-ARC-001: per-room archive and clear times next to read_at. A row
-- created only for archive or clear keeps read_at 0, which reads as "never
-- read". 0 archived_at means not archived, 0 cleared_at means never cleared.
ALTER TABLE room_reads ADD COLUMN archived_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE room_reads ADD COLUMN cleared_at INTEGER NOT NULL DEFAULT 0;
