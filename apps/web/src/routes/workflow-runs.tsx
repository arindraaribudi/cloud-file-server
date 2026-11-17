import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { RunTimeline } from "../components/workflow/RunTimeline";
import { Stamp } from "../components/Stamp";
import { cancelRun, getRun, listRuns, retryRun } from "../lib/workflows";

export function WorkflowRuns() {
  const { id } = useParams({ from: "/_authed/workflows/$id/runs" });
  const q = useQuery({ queryKey: ["workflow-runs", id], queryFn: () => listRuns(id) });

  return (
    <section>
      <div className="page-head">
        <span className="eyebrow">Workflow</span>
        <h1>Run history</h1>
        <div className="rule" />
      </div>
      <div className="ledger-toolbar">
        <Link className="btn btn-ghost" to="/workflows/$id/edit" params={{ id }}>Edit workflow</Link>
        <Link className="btn btn-ghost" to="/workflows">All workflows</Link>
      </div>
      {q.isPending && <p>loading...</p>}
      {q.error && <p className="err">{String(q.error)}</p>}
      {q.data && (
        <div className="ledger">
          <table>
            <thead>
              <tr>
                <th>Run</th>
                <th>Status</th>
                <th>Source</th>
                <th>Started</th>
                <th>Attempt</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {q.data.map((run) => (
                <tr key={run.id}>
                  <td className="mono">{run.id.slice(0, 8)}</td>
                  <td><RunStatus status={run.status} /></td>
                  <td>{run.source}</td>
                  <td className="mono">{run.started_at ? new Date(run.started_at).toLocaleString() : "—"}</td>
                  <td className="mono">{run.attempt}/{run.max_attempts}</td>
                  <td>
                    <div className="row-actions">
                      <Link to="/workflows/$id/runs/$runId" params={{ id, runId: run.id }}>View</Link>
                    </div>
                  </td>
                </tr>
              ))}
              {q.data.length === 0 && (
                <tr><td colSpan={6} className="empty-cell">No runs yet.</td></tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

export function WorkflowRunDetail() {
  const { id, runId } = useParams({ from: "/_authed/workflows/$id/runs/$runId" });
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["workflow-run", id, runId],
    queryFn: () => getRun(id, runId),
    refetchInterval: 3000,
  });
  const retry = useMutation({
    mutationFn: () => retryRun(id, runId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["workflow-run", id, runId] }),
  });
  const cancel = useMutation({
    mutationFn: () => cancelRun(id, runId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["workflow-run", id, runId] }),
  });

  return (
    <section>
      <div className="page-head">
        <span className="eyebrow">Workflow run</span>
        <h1 className="mono">{runId.slice(0, 8)}</h1>
        <div className="rule" />
      </div>
      <div className="ledger-toolbar">
        <Link className="btn btn-ghost" to="/workflows/$id/runs" params={{ id }}>Back to runs</Link>
        <div className="row-actions">
          {q.data && (q.data.run.status === "failed" || q.data.run.status === "canceled") && (
            <button type="button" onClick={() => retry.mutate()} disabled={retry.isPending}>Retry</button>
          )}
          {q.data && (q.data.run.status === "running" || q.data.run.status === "queued") && (
            <button type="button" className="danger" onClick={() => cancel.mutate()} disabled={cancel.isPending}>Cancel</button>
          )}
        </div>
      </div>
      {q.isPending && <p>loading...</p>}
      {q.error && <p className="err">{String(q.error)}</p>}
      {retry.error && <p className="err">{String(retry.error)}</p>}
      {cancel.error && <p className="err">{String(cancel.error)}</p>}
      {q.data && (
        <>
          <div className="run-summary">
            <span><strong>Status:</strong> <RunStatus status={q.data.run.status} /></span>
            <span><strong>Source:</strong> {q.data.run.source}</span>
            <span><strong>Attempt:</strong> {q.data.run.attempt}/{q.data.run.max_attempts}</span>
            <span><strong>Created:</strong> {new Date(q.data.run.created_at).toLocaleString()}</span>
          </div>
          {q.data.run.error && <p className="err">{q.data.run.error}</p>}
          <RunTimeline components={q.data.components} />
        </>
      )}
    </section>
  );
}

function RunStatus({ status }: { status: string }) {
  const tone = status === "succeeded" ? "blue" : status === "failed" ? "red" : "blue";
  return <Stamp label={status} tone={tone} />;
}
