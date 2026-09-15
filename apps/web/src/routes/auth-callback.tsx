import { useEffect, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Stamp } from "../components/Stamp";
import { api } from "../lib/api";

export function AuthCallback() {
  const nav = useNavigate();
  const [status, setStatus] = useState<"checking" | "cleared" | "denied">("checking");

  useEffect(() => {
    let cancelled = false;
    api("/api/v1/auth/session")
      .then(() => {
        if (cancelled) return;
        setStatus("cleared");
        setTimeout(() => nav({ to: "/users" }), 650);
      })
      .catch(() => {
        if (cancelled) return;
        setStatus("denied");
        setTimeout(() => nav({ to: "/login" }), 900);
      });
    return () => {
      cancelled = true;
    };
  }, [nav]);

  return (
    <main className="callback-screen">
      {status === "checking" && <p>Signing you in...</p>}
      {status === "cleared" && (
        <>
          <Stamp label="Signed in" tone="blue" size="lg" />
          <p>You're signed in</p>
        </>
      )}
      {status === "denied" && (
        <>
          <Stamp label="Access denied" tone="red" size="lg" />
          <p>Redirecting to sign-in page</p>
        </>
      )}
    </main>
  );
}
