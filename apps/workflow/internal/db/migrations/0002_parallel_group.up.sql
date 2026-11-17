-- Output components that share a non-empty parallel_group and are adjacent run
-- at the same time. Empty means "run on its own, in order".
ALTER TABLE workflow.components
  ADD COLUMN parallel_group TEXT NOT NULL DEFAULT '';

ALTER TABLE workflow.run_components
  ADD COLUMN parallel_group TEXT NOT NULL DEFAULT '';
