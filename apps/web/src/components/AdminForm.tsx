import { useState } from "react";
import { generatePassword, validatePassword } from "../lib/password";

export function AdminForm({
  submitting,
  error,
  onSubmit,
  onCancel,
}: {
  submitting: boolean;
  error?: string | null;
  onSubmit: (values: { username: string; password: string; role: string }) => void;
  onCancel: () => void;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [role, setRole] = useState("admin");
  const [mismatch, setMismatch] = useState(false);

  const passwordError = validatePassword(password);

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (password !== confirm) {
      setMismatch(true);
      return;
    }
    setMismatch(false);
    onSubmit({ username: username.trim(), password, role });
  }

  return (
    <form className="form-card" onSubmit={submit}>
      <span className="eyebrow">New administrator</span>
      <h2>Add local admin</h2>
      <p className="muted">Local admins can sign in without SSO. SSO admins are managed in IAM.</p>

      <div className="field">
        <label htmlFor="admin-username">Username</label>
        <input
          id="admin-username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoComplete="off"
          required
        />
      </div>

      <div className="field">
        <label htmlFor="admin-role">Role</label>
        <select id="admin-role" value={role} onChange={(e) => setRole(e.target.value)}>
          <option value="admin">admin</option>
          <option value="readonly">readonly</option>
        </select>
      </div>

      <div className="field">
        <label htmlFor="admin-password">Initial password</label>
        <input
          id="admin-password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="new-password"
          required
        />
        <div className="form-actions">
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => {
              const generated = generatePassword();
              setPassword(generated);
              setConfirm(generated);
            }}
          >
            Generate
          </button>
        </div>
        {password && passwordError && <p className="err">{passwordError}</p>}
      </div>

      <div className="field">
        <label htmlFor="admin-confirm">Confirm password</label>
        <input
          id="admin-confirm"
          type="password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          autoComplete="new-password"
          required
        />
      </div>

      {mismatch && <p className="err">Passwords don&rsquo;t match.</p>}
      {error && <p className="err">{error}</p>}

      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={submitting || !!passwordError}>
          {submitting ? "Saving..." : "Create admin"}
        </button>
        <button type="button" className="btn btn-ghost" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
