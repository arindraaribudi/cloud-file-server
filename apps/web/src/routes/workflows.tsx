import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import {
  deleteWorkflow,
  duplicateWorkflow,
  listWorkflows,
  setWorkflowEnabled,
  type Workflow,
} from "../lib/workflows";
import { Stamp } from "../components/Stamp";

export function Workflows() {
  const qc = useQueryClient();
  const [search, setSearch] = useState("");
  const q = useQuery({
    queryKey: ["workflows", search],
    queryFn: () => listWorkflows(search || undefined),
  });

  const toggle = useMutation({
    mutationFn: (wf: Workflow) => setWorkflowEnabled(wf.id, !wf.enabled, wf.version),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["workflows"] }),
  });

  const remove = useMutation({
    mutationFn: (wf: Workflow) => deleteWorkflow(wf.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["workflows"] }),
  });

  const duplicate = useMutation({
    mutationFn: (wf: Workflow) => duplicateWorkflow(wf.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["workflows"] }),
  });

  function counts(wf: Workflow) {
    return {
      trigger: wf.components.filter((c) => c.stage === "trigger").length,
      process: wf.components.filter((c) => c.stage === "process").length,
      output: wf.components.filter((c) => c.stage === "output").length,
    };
  }

  return (
    <section>
      <div className="page-head">
        <span className="eyebrow">Automation</span>
        <h1>Workflows</h1>
        <div className="rule" />
      </div>

      <div className="ledger-toolbar">
        <input
          type="search"
          placeholder="Search workflows"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Link className="btn btn-primary" to="/workflows/new">New workflow</Link>
      </div>

      {q.isPending && <p>loading...</p>}
      {q.error && <p className="err">{String(q.error)}</p>}
      {toggle.error && <p className="err">{String(toggle.error)}</p>}
      {remove.error && <p className="err">{String(remove.error)}</p>}
      {duplicate.error && <p className="err">{String(duplicate.error)}</p>}

      {q.data && (
        <div className="ledger">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Components</th>
                <th>Status</th>
                <th>Updated</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {q.data.map((wf) => {
                const c = counts(wf);
                return (
                  <tr key={wf.id}>
                    <td>
                      <Link to="/workflows/$id/edit" params={{ id: wf.id }}>
                        <strong>{wf.name}</strong>
                      </Link>
                      {wf.description && <div className="muted">{wf.description}</div>}
                    </td>
                    <td className="mono">
                      {c.trigger} trigger{c.trigger === 1 ? "" : "s"} · {c.process} process · {c.output} output
                    </td>
                    <td>
                      <div className="status-cell">
                        <Stamp label={wf.enabled ? "Enabled" : "Disabled"} tone={wf.enabled ? "blue" : "red"} />
                        <button
                          type="button"
                          className={"proto-toggle " + (wf.enabled ? "on" : "off")}
                          onClick={() => toggle.mutate(wf)}
                          disabled={toggle.isPending}
                        >
                          {wf.enabled ? "ON" : "OFF"}
                        </button>
                      </div>
                    </td>
                    <td className="mono">{new Date(wf.updated_at).toLocaleString()}</td>
                    <td>
                      <div className="row-actions">
                        <Link to="/workflows/$id/runs" params={{ id: wf.id }}>Runs</Link>
                        <button type="button" onClick={() => duplicate.mutate(wf)} disabled={duplicate.isPending}>
                          Duplicate
                        </button>
                        <button
                          type="button"
                          className="danger"
                          onClick={() => {
                            if (window.confirm("Delete workflow " + wf.name + "?")) remove.mutate(wf);
                          }}
                          disabled={remove.isPending}
                        >
                          Delete
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
              {q.data.length === 0 && (
                <tr>
                  <td colSpan={5} className="empty-cell">No workflows yet.</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
