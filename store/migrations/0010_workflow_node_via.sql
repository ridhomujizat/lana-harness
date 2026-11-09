-- Spec 44: how a workflow script_parse node run was served. '' is every other
-- node type or a run stored before this column existed, 'script' means the
-- embedded script passed, and 'fallback' means an agent answered instead.
ALTER TABLE workflow_node_runs ADD COLUMN via TEXT NOT NULL DEFAULT '';
