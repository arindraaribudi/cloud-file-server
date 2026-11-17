import { useState } from "react";
import { toComponentInput } from "../../lib/workflows";
import type { ComponentType, TriggerMode, Workflow, WorkflowInput } from "../../lib/workflows";
import { WorkflowDesigner, type EditableComponent } from "./WorkflowDesigner";
import { TriggerTestPanel } from "./TriggerTestPanel";
import { WorkflowSummary } from "./WorkflowSummary";

export function WorkflowForm({
  mode,
  initial,
  catalog,
  submitting,
  error,
  onSubmit,
  onCancel,
}: {
  mode: "create" | "edit";
  initial?: Workflow;
  catalog: ComponentType[];
  submitting: boolean;
  error?: string | null;
  onSubmit: (input: WorkflowInput) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [enabled, setEnabled] = useState(initial?.enabled ?? false);
  // A workflow has exactly one trigger, so "any" and "all" are equivalent.
  const [triggerMode] = useState<TriggerMode>(initial?.trigger_mode ?? "any");
  const [components, setComponents] = useState<EditableComponent[]>(() =>
    (initial?.components ?? []).map((component) => ({
      clientId: component.id,
      label: component.label,
      stage: component.stage,
      type: component.type,
      type_version: component.type_version,
      config: component.config ?? {},
      enabled: component.enabled,
      parallel_group: component.parallel_group ?? "",
      route: component.route ?? "",
    })),
  );

  function submit() {
    const payload: WorkflowInput = {
      name: name.trim(),
      description: description.trim(),
      enabled,
      version: initial?.version ?? 0,
      trigger_mode: triggerMode,
      components: components.map(toComponentInput),
    };
    onSubmit(payload);
  }

  return (
    <div className="workflow-editor">
      <div className="form-card workflow-form-card">
        <div className="workflow-form-head">
          <div>
            <span className="eyebrow">{mode === "create" ? "New workflow" : "Edit workflow"}</span>
            <h2>{mode === "create" ? "Build a workflow" : initial?.name ?? "Workflow"}</h2>
          </div>
          <label className="switch" title={enabled ? "Disable workflow" : "Enable workflow"}>
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
            <span className="track" />
            <span className="thumb" />
          </label>
        </div>

        <div className="workflow-meta-grid">
          <div className="field">
            <label htmlFor="workflow-name">Name</label>
            <input
              id="workflow-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Archive invoices"
              required
            />
          </div>
          <div className="field">
            <label htmlFor="workflow-description">Description</label>
            <input
              id="workflow-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What this workflow does"
            />
          </div>
        </div>

        <ol className="flow-guide">
          <li>
            <b>1 · Trigger</b>
            <span>When something happens to a file</span>
          </li>
          <li>
            <b>2 · Condition</b>
            <span>Optional filter. Add several for different routes</span>
          </li>
          <li>
            <b>3 · Actions</b>
            <span>Copy to folders, call webhooks / APIs, send email</span>
          </li>
        </ol>
        <p className="flow-hint">
          Drag steps from the <b>Toolbox</b> onto the canvas, then click a step to edit it.
        </p>
        <WorkflowDesigner components={components} catalog={catalog} onChange={setComponents} />
        <WorkflowSummary components={components} catalog={catalog} />

        {error && <p className="err">{error}</p>}
        <div className="form-actions">
          <button type="button" className="btn btn-primary" onClick={submit} disabled={submitting}>
            {submitting ? "Saving..." : "Save workflow"}
          </button>
          <button type="button" className="btn btn-ghost" onClick={onCancel} disabled={submitting}>
            Cancel
          </button>
        </div>
      </div>

      <TriggerTestPanel components={components} catalog={catalog} triggerMode={triggerMode} />
    </div>
  );
}
