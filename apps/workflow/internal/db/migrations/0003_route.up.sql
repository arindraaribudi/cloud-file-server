-- A route groups the steps behind one Condition. When a condition is not met,
-- only the later steps of the same route are skipped. Empty is the default route.
ALTER TABLE workflow.components
  ADD COLUMN route TEXT NOT NULL DEFAULT '';

ALTER TABLE workflow.run_components
  ADD COLUMN route TEXT NOT NULL DEFAULT '';
