import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { deleteAdmin, listAdmins, updateAdminRole, type AdminUser } from "../lib/api";

function displayRole(role: string) {
  return role.toLowerCase() === "superadmin" ? "admin" : role;
}

export function Admins() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admins"], queryFn: listAdmins });

  const updateRole = useMutation({
    mutationFn: (p: { user: AdminUser; role: string }) => updateAdminRole(p.user.username, p.role),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admins"] }),
  });

  const remove = useMutation({
    mutationFn: (username: string) => deleteAdmin(username),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admins"] }),
  });

  return (
    <section>
      <div className="page-head">
        <span className="eyebrow">Access</span>
        <h1>Administrators</h1>
        <div className="rule" />
      </div>

      <div className="ledger-toolbar">
        <p className="muted">Local credential accounts. SSO administrators are managed by IAM.</p>
        <Link className="btn btn-primary" to="/admins/new">New admin</Link>
      </div>

      {q.isPending && <p>loading...</p>}
      {q.error && <p className="err">{String(q.error)}</p>}
      {updateRole.error && <p className="err">{String(updateRole.error)}</p>}
      {remove.error && <p className="err">{String(remove.error)}</p>}

      {q.data && (
        <div className="ledger">
          <table>
            <thead>
              <tr>
                <th>Username</th>
                <th>Role</th>
                <th>Created</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {q.data.map((user) => (
                <tr key={user.id}>
                  <td className="mono">{user.username}</td>
                  <td>
                    <select
                      className="role-select"
                      value={displayRole(user.role)}
                      onChange={(e) => updateRole.mutate({ user, role: e.target.value })}
                      disabled={updateRole.isPending}
                      aria-label={"Role for " + user.username}
                    >
                      <option value="admin">admin</option>
                      <option value="readonly">readonly</option>
                    </select>
                  </td>
                  <td className="mono">{new Date(user.created_at).toLocaleString()}</td>
                  <td>
                    <div className="row-actions">
                      <Link to="/admins/$username/password" params={{ username: user.username }}>Reset password</Link>
                      <button
                        type="button"
                        className="danger"
                        onClick={() => {
                          if (window.confirm("Delete admin " + user.username + "?")) remove.mutate(user.username);
                        }}
                        disabled={remove.isPending}
                      >
                        Delete
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {q.data.length === 0 && (
                <tr><td colSpan={4} className="empty-cell">No local administrators.</td></tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
