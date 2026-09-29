import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { setUserSFTPKey } from "../lib/api";

export function UserSFTPKey() {
  const { username } = useParams({ from: "/_authed/users/$username/sftp-key" });
  const nav = useNavigate();
  const [publicKey, setPublicKey] = useState("");

  const mutation = useMutation({
    mutationFn: (key: string) => setUserSFTPKey(username, key),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    mutation.mutate(publicKey.trim());
  }

  return (
    <form className="form-card" onSubmit={submit}>
      <span className="eyebrow">SFTP access</span>
      <h2>Set SFTP public key for {username}</h2>

      <div className="field">
        <label htmlFor="public_key">Public key (authorized_keys format)</label>
        <textarea
          id="public_key"
          rows={4}
          placeholder="ssh-ed25519 AAAA... user@host"
          value={publicKey}
          onChange={(e) => setPublicKey(e.target.value)}
        />
        <p className="form-note">Leave empty and save to remove the key (falls back to password-only login).</p>
      </div>

      {mutation.isError && <p className="err">{String(mutation.error)}</p>}
      {mutation.isSuccess && <p className="form-note">Saved.</p>}

      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={mutation.isPending}>
          {mutation.isPending ? "Saving..." : "Save"}
        </button>
        <button type="button" className="btn btn-ghost" onClick={() => nav({ to: "/users" })}>
          Cancel
        </button>
      </div>
    </form>
  );
}
