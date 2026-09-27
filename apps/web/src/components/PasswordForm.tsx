import { useState } from "react";
import { generatePassword, validatePassword } from "../lib/password";

function EyeIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
      <circle cx="12" cy="12" r="3" />
    </svg>
  );
}

function EyeOffIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
      <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
      <path d="M14.12 14.12a3 3 0 1 1-4.24-4.24" />
      <line x1="1" y1="1" x2="23" y2="23" />
    </svg>
  );
}

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
  const [copied, setCopied] = useState(false);
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
        <div className="input-wrap">
          <input
            id="password"
            type={showPassword ? "text" : "password"}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
          <button
            type="button"
            className="input-icon"
            onClick={() => setShowPassword((s) => !s)}
            aria-label={showPassword ? "Hide password" : "Show password"}
            title={showPassword ? "Hide password" : "Show password"}
          >
            {showPassword ? <EyeOffIcon /> : <EyeIcon />}
          </button>
        </div>
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
          {password && (
            <button
              type="button"
              className="btn btn-ghost"
              onClick={async () => {
                await navigator.clipboard.writeText(password);
                setCopied(true);
                setTimeout(() => setCopied(false), 2000);
              }}
              title="Copy password"
            >
              Copy
            </button>
          )}
        </div>
        {copied && <p className="form-note">Password copied to clipboard.</p>}
        {password && passwordError && <p className="err">{passwordError}</p>}
      </div>

      <div className="field">
        <label htmlFor="confirm">Confirm new password</label>
        <div className="input-wrap">
          <input
            id="confirm"
            type={showPassword ? "text" : "password"}
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            required
          />
          <button
            type="button"
            className="input-icon"
            onClick={() => setShowPassword((s) => !s)}
            aria-label={showPassword ? "Hide password" : "Show password"}
            title={showPassword ? "Hide password" : "Show password"}
          >
            {showPassword ? <EyeOffIcon /> : <EyeIcon />}
          </button>
        </div>
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
