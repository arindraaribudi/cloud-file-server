import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { WorkflowForm } from "../components/workflow/WorkflowForm";
import {
  createWorkflow,
  getWorkflow,
  listComponentTypes,
  updateWorkflow,
  type WorkflowInput,
} from "../lib/workflows";

export function WorkflowNew() {
  return <WorkflowEditor />;
}

export function WorkflowEdit() {
  const { id } = useParams({ from: "/_authed/workflows/$id/edit" });
  return <WorkflowEditor workflowId={id} />;
}

function WorkflowEditor({ workflowId }: { workflowId?: string }) {
  const nav = useNavigate();
  const qc = useQueryClient();
  const catalogQ = useQuery({
    queryKey: ["workflow-components"],
    queryFn: () => listComponentTypes(),
  });
  const workflowQ = useQuery({
    queryKey: ["workflow", workflowId],
    queryFn: () => getWorkflow(workflowId as string),
    enabled: Boolean(workflowId),
  });

  const mutation = useMutation({
    mutationFn: (input: WorkflowInput) =>
      workflowId ? updateWorkflow(workflowId, input) : createWorkflow(input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["workflows"] });
      if (workflowId) qc.invalidateQueries({ queryKey: ["workflow", workflowId] });
      nav({ to: "/workflows" });
    },
  });

  if (catalogQ.isPending || (workflowId && workflowQ.isPending)) {
    return <p>loading...</p>;
  }
  if (catalogQ.error) return <p className="err">{String(catalogQ.error)}</p>;
  if (workflowQ.error) return <p className="err">{String(workflowQ.error)}</p>;

  return (
    <section>
      <div className="page-head">
        <span className="eyebrow">{workflowId ? "Edit automation" : "New automation"}</span>
        <h1>{workflowId ? "Edit workflow" : "New workflow"}</h1>
        <div className="rule" />
      </div>
      <WorkflowForm
        mode={workflowId ? "edit" : "create"}
        initial={workflowQ.data}
        catalog={catalogQ.data ?? []}
        submitting={mutation.isPending}
        error={mutation.error ? String(mutation.error) : null}
        onSubmit={(input) => mutation.mutate(input)}
        onCancel={() => nav({ to: "/workflows" })}
      />
    </section>
  );
}
