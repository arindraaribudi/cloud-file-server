import { api } from "./api";

export type WorkflowStage = "trigger" | "process" | "output";
export type TriggerMode = "any" | "all";

export type JsonSchema = {
  type?: string;
  title?: string;
  description?: string;
  default?: unknown;
  enum?: string[];
  required?: string[];
  properties?: Record<string, JsonSchema>;
  items?: JsonSchema;
  additionalProperties?: boolean | JsonSchema;
  /** Extension: the value references an entity; "user" renders a user picker. */
  "x-ref"?: "user";
  /** Extension: label for the empty choice of an optional x-ref field. */
  "x-empty-label"?: string;
};

export type ComponentType = {
  key: string;
  name: string;
  description: string;
  stages: WorkflowStage[];
  event_type?: string;
  version: number;
  config_schema: JsonSchema;
  default_config: Record<string, unknown>;
};

export type WorkflowComponent = {
  id: string;
  label: string;
  stage: WorkflowStage;
  type: string;
  type_version: number;
  position: number;
  config: Record<string, unknown>;
  enabled: boolean;
  /** Adjacent output components sharing a value here run at the same time. */
  parallel_group?: string;
  /** Steps behind the same Condition share a route; "" is the default route. */
  route?: string;
};

export type WorkflowComponentInput = {
  label: string;
  stage: WorkflowStage;
  type: string;
  type_version?: number;
  config: Record<string, unknown>;
  enabled: boolean;
  parallel_group?: string;
  route?: string;
};

export type Workflow = {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  version: number;
  trigger_mode: TriggerMode;
  components: WorkflowComponent[];
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
};

export type WorkflowInput = {
  name: string;
  description: string;
  enabled: boolean;
  version: number;
  trigger_mode: TriggerMode;
  components: WorkflowComponentInput[];
};

export type Sample = {
  user_id?: number;
  username: string;
  path: string;
  mime: string;
  event_type: "upload" | "remove";
  size?: number;
  is_dir?: boolean;
};

export type TriggerResult = {
  component_id?: string;
  label: string;
  type: string;
  matched: boolean;
  reason: string;
};

export type WorkflowMatch = {
  id: string;
  name: string;
  matched: boolean;
  triggers: TriggerResult[];
};

export type TestMatchResponse = {
  matched: boolean;
  triggers: TriggerResult[];
  workflows?: WorkflowMatch[];
};

export type WorkflowRun = {
  id: string;
  workflow_id: string;
  workflow_version: number;
  trigger_mode: TriggerMode;
  event_id?: number;
  source: string;
  status: string;
  event: unknown;
  context: unknown;
  error: string;
  attempt: number;
  max_attempts: number;
  cancel_requested: boolean;
  created_at: string;
  started_at?: string;
  finished_at?: string;
};

export type RunComponent = {
  id: string;
  run_id: string;
  component_id?: string;
  stage: WorkflowStage;
  label: string;
  type: string;
  type_version: number;
  position: number;
  config: unknown;
  status: string;
  attempt: number;
  started_at?: string;
  finished_at?: string;
  error: string;
  output: unknown;
};

export type RunDetail = {
  run: WorkflowRun;
  components: RunComponent[];
};

export function listWorkflows(search?: string) {
  const q = search ? "?search=" + encodeURIComponent(search) : "";
  return api<Workflow[]>(`/api/v1/workflows${q}`);
}

export function getWorkflow(id: string) {
  return api<Workflow>(`/api/v1/workflows/${encodeURIComponent(id)}`);
}

export function createWorkflow(input: WorkflowInput) {
  return api<Workflow>("/api/v1/workflows", { method: "POST", body: JSON.stringify(input) });
}

export function updateWorkflow(id: string, input: WorkflowInput) {
  return api<Workflow>(`/api/v1/workflows/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify(input),
  });
}

export function deleteWorkflow(id: string) {
  return api<void>(`/api/v1/workflows/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export function setWorkflowEnabled(id: string, enabled: boolean, version: number) {
  return api<{ enabled: boolean; version: number; updated_at: string }>(
    `/api/v1/workflows/${encodeURIComponent(id)}/${enabled ? "enable" : "disable"}`,
    { method: "POST", body: JSON.stringify({ version }) },
  );
}

export function duplicateWorkflow(id: string, name?: string) {
  return api<Workflow>(`/api/v1/workflows/${encodeURIComponent(id)}/duplicate`, {
    method: "POST",
    body: JSON.stringify(name ? { name } : {}),
  });
}

export function listComponentTypes(stage?: WorkflowStage) {
  const q = stage ? "?stage=" + encodeURIComponent(stage) : "";
  return api<ComponentType[]>(`/api/v1/workflow-components${q}`);
}

// The API rejects unknown fields, so callers may pass richer objects (for
// example canvas components with a client-side id) and only the known fields
// are sent.
export function toComponentInput(component: WorkflowComponentInput): WorkflowComponentInput {
  return {
    label: component.label,
    stage: component.stage,
    type: component.type,
    type_version: component.type_version,
    config: component.config,
    enabled: component.enabled,
    parallel_group: component.parallel_group ?? "",
    route: component.route ?? "",
  };
}

export function testMatch(input: {
  sample: Sample;
  trigger_mode: TriggerMode;
  components: WorkflowComponentInput[];
}) {
  return api<TestMatchResponse>("/api/v1/workflows/test-match", {
    method: "POST",
    body: JSON.stringify({ ...input, components: input.components.map(toComponentInput) }),
  });
}

export type PreviewStep = {
  label: string;
  stage: WorkflowStage;
  type: string;
  parallel_group?: string;
  route?: string;
  /** ok = would run, skipped = would not run, failed = would fail */
  status: "ok" | "skipped" | "failed";
  reason?: string;
  output?: Record<string, unknown>;
  error?: string;
};

export type PreviewResponse = {
  matched: boolean;
  triggers: TriggerResult[];
  steps: PreviewStep[];
};

/** Dry-runs the (possibly unsaved) canvas against a sample file; changes nothing. */
export function previewWorkflow(input: { sample: Sample; components: WorkflowComponentInput[] }) {
  return api<PreviewResponse>("/api/v1/workflows/preview", {
    method: "POST",
    body: JSON.stringify({ ...input, components: input.components.map(toComponentInput) }),
  });
}

export function runWorkflow(id: string, input: Sample & { dry_run?: boolean }) {
  return api<WorkflowRun>(`/api/v1/workflows/${encodeURIComponent(id)}/run`, {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function listRuns(id: string) {
  return api<WorkflowRun[]>(`/api/v1/workflows/${encodeURIComponent(id)}/runs`);
}

export function getRun(id: string, runId: string) {
  return api<RunDetail>(
    `/api/v1/workflows/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}`,
  );
}

export function retryRun(id: string, runId: string) {
  return api<{ status: string }>(
    `/api/v1/workflows/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}/retry`,
    { method: "POST", body: JSON.stringify({}) },
  );
}

export function cancelRun(id: string, runId: string) {
  return api<{ status: string }>(
    `/api/v1/workflows/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}/cancel`,
    { method: "POST", body: JSON.stringify({}) },
  );
}
