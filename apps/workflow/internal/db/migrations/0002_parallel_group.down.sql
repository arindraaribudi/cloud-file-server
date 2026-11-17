ALTER TABLE workflow.run_components DROP COLUMN IF EXISTS parallel_group;
ALTER TABLE workflow.components DROP COLUMN IF EXISTS parallel_group;
