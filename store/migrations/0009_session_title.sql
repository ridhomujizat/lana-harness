-- Spec 40: who set the session title. '' means no title yet, 'auto' the
-- background title pass, 'owner' a rename (never overwritten by the pass), and
-- 'system' a fixed title given at creation (workflow and calendar sessions).
ALTER TABLE sessions ADD COLUMN title_source TEXT NOT NULL DEFAULT '' CHECK (title_source IN ('', 'auto', 'owner', 'system'));
UPDATE sessions SET title_source = 'auto' WHERE title <> '';
