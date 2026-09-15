import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, type AuditEvent } from "../lib/api";
import { Stamp } from "../components/Stamp";

export function Audit() {
  const [search, setSearch] = useState("");
  const q = useQuery({
    queryKey: ["audit"],
    queryFn: () => api<AuditEvent[]>("/api/v1/audit"),
  });

  const rows = (q.data ?? []).filter(
    (e) =>
      e.username.toLowerCase().includes(search.toLowerCase()) ||
      e.path.toLowerCase().includes(search.toLowerCase()),
  );

  return (
    <section>
      <div className="page-head">
        <span className="eyebrow">Audit Log</span>
        <h1>Audit trail</h1>
        <div className="rule" />
      </div>

      <div className="ledger-toolbar">
        <input
          type="search"
          placeholder="Search user or path..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      {q.isPending && <p>loading...</p>}
      {q.error && <p className="err">{String(q.error)}</p>}

      {q.data && (
        <div className="ledger">
          <table>
            <thead>
              <tr>
                <th>Time</th>
                <th>User</th>
                <th>IP</th>
                <th>Action</th>
                <th>Path</th>
                <th>Result</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((e, i) => (
                <tr key={i}>
                  <td className="mono">{e.event_time}</td>
                  <td className="mono">{e.username}</td>
                  <td className="mono">{e.client_ip}</td>
                  <td>{e.action}</td>
                  <td className="mono">{e.path}</td>
                  <td>
                    <Stamp label={e.success ? "Success" : "Failed"} tone={e.success ? "blue" : "red"} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
