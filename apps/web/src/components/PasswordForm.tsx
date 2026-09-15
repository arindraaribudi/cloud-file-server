import { useState } from "react";
import { generatePassword, validatePassword } from "../lib/password";

export function PasswordForm({
  eyebrow,
  title,
  showCurrent = false,
  submitting,
  error,
  success,
  onSubmit,
  onCancel,
}: {
  eyebrow: string;
  title: string;
  showCurrent?: boolean;
  submitting: boolean;
  error?: string | null;
  success?: string | null;
  onSubmit: (values: { current_password: string; password: string }) => void;
  onCancel: () => void;
}) {
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [mismatch, setMismatch] = useState(false);

  const passwordError = validatePassword(password);

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (password !== confirm) {
      setMismatch(true);
      return;
    }
    setMismatch(false);
    onSubmit({ current_password: current, password });
  }

  return (
    <form className="form-card" onSubmit={submit}>
      <span className="eyebrow">{eyebrow}</span>
      <h2>{title}</h2>

      {showCurrent && (
        <div className="field">
          <label htmlFor="current">Current password</label>
          <input id="current" type="password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
        </div>
      )}

      <div className="field">
        <label htmlFor="password">New password</label>
        <input
          id="password"
          type={showPassword ? "text" : "password"}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
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
              setShowPassword(true);
            }}
          >
            Generate
          </button>
          <button type="button" className="btn btn-ghost" onClick={() => setShowPassword((s) => !s)}>
            {showPassword ? "Hide" : "Show"}
          </button>
        </div>
        {password && passwordError && <p className="err">{passwordError}</p>}
      </div>

      <div className="field">
        <label htmlFor="confirm">Confirm new password</label>
        <input
          id="confirm"
          type={showPassword ? "text" : "password"}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          required
        />
      </div>

      {mismatch && <p className="err">Passwords don&rsquo;t match.</p>}
      {error && <p className="err">{error}</p>}
      {success && <p className="form-note">{success}</p>}

      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={submitting || !!passwordError}>
          {submitting ? "Saving..." : "Reset password"}
        </button>
        <button type="button" className="btn btn-ghost" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
