import { useEffect, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { OIDC_AUTHORIZE_URL } from "../lib/api";

type AuthConfig = { local_login: boolean; oidc: boolean };

export function Login() {
  const nav = useNavigate();
  const [config, setConfig] = useState<AuthConfig | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    fetch("/api/v1/auth/config", { credentials: "include" })
      .then(async (res) => {
        if (!res.ok) throw new Error("could not load sign-in options");
        return (await res.json()) as AuthConfig;
      })
      .then((cfg) => {
        if (!cancelled) setConfig(cfg);
      })
      .catch((err) => {
        if (!cancelled) setConfigError(String(err));
      });
    return () => { cancelled = true; };
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const res = await fetch("/api/v1/auth/login", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username, password }),
      });
      if (!res.ok) {
        const text = (await res.text().catch(() => "")).trim();
        throw new Error(text || res.status + " " + res.statusText);
      }
      nav({ to: "/workflows" });
    } catch (err) {
      setError(String(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="login-screen">
      <div className="login-card">
        <span className="eyebrow">Sign in</span>
        <h1>Cloud File Server</h1>
        <div className="form-card">
          {!config && !configError && <p>Loading sign-in options...</p>}
          {configError && <p className="err">{configError}</p>}

          {config?.local_login && (
            <form onSubmit={submit} className="login-form">
              <div className="field">
                <label htmlFor="login-username">Username</label>
                <input
                  id="login-username"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  autoComplete="username"
                  autoFocus
                  required
                />
              </div>
              <div className="field">
                <label htmlFor="login-password">Password</label>
                <input
                  id="login-password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="current-password"
                  required
                />
              </div>
              {error && <p className="err">{error}</p>}
              <button type="submit" className="btn btn-primary" disabled={submitting}>
                {submitting ? "Signing in..." : "Sign in"}
              </button>
            </form>
          )}

          {config?.local_login && config?.oidc && <div className="divider">or</div>}

          {config?.oidc && (
            <a className="btn btn-primary" href={OIDC_AUTHORIZE_URL}>
              Continue with SSO
            </a>
          )}

          {config && !config.local_login && !config.oidc && (
            <p className="err">No sign-in method is configured.</p>
          )}
        </div>
      </div>
    </main>
  );
}
