import { useEffect, useState } from "react";
import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import type { Session } from "../lib/api";

export function AuthedLayout() {
  const nav = useNavigate();
  const [session, setSession] = useState<Session | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetch("/api/v1/auth/session", { credentials: "include" })
      .then(async (r) => {
        if (cancelled) return;
        if (!r.ok) nav({ to: "/login" });
        else setSession(await r.json());
      })
      .catch(() => {
        if (!cancelled) nav({ to: "/login" });
      });
    return () => {
      cancelled = true;
    };
  }, [nav]);

  async function signOut() {
    await fetch("/api/v1/auth/logout", { method: "POST", credentials: "include" });
    nav({ to: "/login" });
  }

  if (!session) {
    return (
      <main className="callback-screen">
        <p>Signing you in...</p>
      </main>
    );
  }

  const fullName = [session.first_name, session.last_name].filter(Boolean).join(" ") || session.username;
  const initial = (session.first_name || session.username || "?")[0]?.toUpperCase();

  return (
    <div className="shell">
      <header className="shell-header">
        <div className="brand">
          <span className="brand-name">Cloud File Server</span>
          {(session.ftp_address || session.ftp_public_address || session.cos_address) && (
            <div className="brand-meta">
              {[
                session.ftp_address && `ftp://${session.ftp_address}`,
                session.ftp_public_address && `ftp://${session.ftp_public_address}`,
                session.cos_address,
              ]
                .filter(Boolean)
                .join(" | ")}
            </div>
          )}
        </div>
        <nav className="shell-nav">
          <Link to="/users">Users</Link>
          <Link to="/audit">Audit Log</Link>
          <details className="profile-menu">
            <summary className="profile-avatar" title={fullName}>
              {initial}
            </summary>
            <div className="profile-dropdown">
              <div className="profile-name">{fullName}</div>
              {session.email && <div className="profile-email">{session.email}</div>}
              <div className="profile-role">{session.role}</div>
              <button type="button" className="link profile-signout" onClick={signOut}>
                Sign out
              </button>
            </div>
          </details>
        </nav>
      </header>
      <main className="shell-main">
        <Outlet />
      </main>
    </div>
  );
}
