import { OIDC_AUTHORIZE_URL } from "../lib/api";

export function Login() {
  return (
    <main className="login-screen">
      <div className="login-card">
        <span className="eyebrow">Sign in</span>
        <h1>Cloud File Server</h1>
        <div className="form-card">
          <p>Sign in with your single sign-on account to continue.</p>
          <a className="btn btn-primary" href={OIDC_AUTHORIZE_URL}>
            Continue with SSO
          </a>
        </div>
      </div>
    </main>
  );
}
