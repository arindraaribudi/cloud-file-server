# SFTP Touchpoint (Web UI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let admins toggle FTP/SFTP access per user and register a user's SFTP public key from `apps/web`.

**Architecture:** Two additions to the existing Users management UI: two switches in the create/edit `UserForm` (`ftp_enabled`/`sftp_enabled`), and a dedicated "set SFTP public key" page reachable from a new row-action button, mirroring the existing reset-password action.

**Tech Stack:** React 19, TanStack Router/Query/Table, TypeScript, Vite. No component test framework exists in this project (only Playwright e2e) — verification is `tsc -b` (typecheck) plus a manual dev-server check.

**Design doc:** `docs/superpowers/specs/2026-09-26-sftp-touchpoint-design.md`

**Depends on:** `docs/superpowers/plans/2026-09-26-sftp-touchpoint-backend.md` (Task 9 — `POST /api/v1/users/{username}/sftp-key`, and `ftp_enabled`/`sftp_enabled` on the user create/update/list endpoints). This plan's UI will call those endpoints; run the backend plan first, or point `vite`'s dev proxy at a backend build that already has them.

---

## Task 1: API client — protocol flags and SFTP key endpoint

**Files:**
- Modify: `apps/web/src/lib/api.ts`

- [ ] **Step 1: Update types and add `setUserSFTPKey`**

In `apps/web/src/lib/api.ts`, replace the `FTPUser` type:

```ts
export type FTPUser = {
  id: number;
  username: string;
  root_folder: string;
  enabled: boolean;
  ftp_enabled: boolean;
  sftp_enabled: boolean;
  created_at: string;
  last_login: string | null;
};
```

Replace `createUser` and `updateUser`:

```ts
export function createUser(payload: {
  username: string;
  root_folder: string;
  password: string;
  enabled: boolean;
  ftp_enabled: boolean;
  sftp_enabled: boolean;
}) {
  return api<FTPUser>("/api/v1/users", { method: "POST", body: JSON.stringify(payload) });
}

export function updateUser(
  username: string,
  payload: { root_folder: string; enabled: boolean; ftp_enabled: boolean; sftp_enabled: boolean },
) {
  return api<FTPUser>(`/api/v1/users/${encodeURIComponent(username)}`, { method: "PATCH", body: JSON.stringify(payload) });
}
```

Add, near `resetUserPassword`:

```ts
export function setUserSFTPKey(username: string, publicKey: string) {
  return api<void>(`/api/v1/users/${encodeURIComponent(username)}/sftp-key`, {
    method: "POST",
    body: JSON.stringify({ public_key: publicKey }),
  });
}
```

- [ ] **Step 2: Typecheck**

Run: `cd apps/web && npm run typecheck`
Expected: FAILS — `UserForm.tsx`/`user-edit.tsx` don't yet supply `ftp_enabled`/`sftp_enabled`, and nothing calls `setUserSFTPKey` yet (unused export is fine, but the payload-shape mismatches in Task 2's callers won't exist until that task — this step is here to confirm the mismatch, so re-run it after Task 2 too).

- [ ] **Step 3: Commit**

```bash
git add apps/web/src/lib/api.ts
git commit -m "feat(web): add ftp_enabled/sftp_enabled and setUserSFTPKey to the API client"
```

---

## Task 2: `UserForm` — FTP/SFTP access switches

**Files:**
- Modify: `apps/web/src/components/UserForm.tsx`
- Modify: `apps/web/src/routes/user-edit.tsx`

- [ ] **Step 1: Add the two switches to `UserForm`**

In `apps/web/src/components/UserForm.tsx`, replace the `UserFormValues` type:

```ts
export type UserFormValues = {
  username: string;
  root_folder: string;
  password: string;
  enabled: boolean;
  ftp_enabled: boolean;
  sftp_enabled: boolean;
};
```

Add state, right after the existing `enabled` state declaration:

```ts
  const [ftpEnabled, setFtpEnabled] = useState(initial?.ftp_enabled ?? true);
  const [sftpEnabled, setSftpEnabled] = useState(initial?.sftp_enabled ?? false);
```

Update `submit`:

```ts
  function submit(e: React.FormEvent) {
    e.preventDefault();
    onSubmit({
      username,
      root_folder: "/" + combinedPath,
      password,
      enabled,
      ftp_enabled: ftpEnabled,
      sftp_enabled: sftpEnabled,
    });
  }
```

Add two more switch rows right after the existing `enabled` switch-row (`<div className="switch-row">...Enabled/Disabled...</div>`):

```tsx
      <div className="switch-row">
        <span className="switch">
          <input type="checkbox" checked={ftpEnabled} onChange={(e) => setFtpEnabled(e.target.checked)} />
          <span className="track" />
          <span className="thumb" />
        </span>
        <label>FTP access {ftpEnabled ? "enabled" : "disabled"}</label>
      </div>

      <div className="switch-row">
        <span className="switch">
          <input type="checkbox" checked={sftpEnabled} onChange={(e) => setSftpEnabled(e.target.checked)} />
          <span className="track" />
          <span className="thumb" />
        </span>
        <label>SFTP access {sftpEnabled ? "enabled" : "disabled"}</label>
      </div>
```

- [ ] **Step 2: Wire `UserEdit`'s mutation to the new fields**

In `apps/web/src/routes/user-edit.tsx`, replace the mutation and its `onSubmit`:

```tsx
  const mutation = useMutation({
    mutationFn: (values: { root_folder: string; enabled: boolean; ftp_enabled: boolean; sftp_enabled: boolean }) =>
      updateUser(username, values),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      nav({ to: "/users" });
    },
  });
```

```tsx
      onSubmit={(values) =>
        mutation.mutate({
          root_folder: values.root_folder,
          enabled: values.enabled,
          ftp_enabled: values.ftp_enabled,
          sftp_enabled: values.sftp_enabled,
        })
      }
```

`apps/web/src/routes/user-new.tsx` needs no change: it already forwards the whole `values` object (`onSubmit={(values) => mutation.mutate(values)}`) straight to `createUser`, whose payload type now includes the two new fields.

- [ ] **Step 3: Typecheck**

Run: `cd apps/web && npm run typecheck`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add apps/web/src/components/UserForm.tsx apps/web/src/routes/user-edit.tsx
git commit -m "feat(web): add FTP/SFTP access switches to the user form"
```

---

## Task 3: "Set SFTP public key" page

**Files:**
- Create: `apps/web/src/routes/user-sftp-key.tsx`

- [ ] **Step 1: Create the route component**

Create `apps/web/src/routes/user-sftp-key.tsx`:

```tsx
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
```

This intentionally doesn't reuse `PasswordForm` — the input shape (a multi-line key, no generate/show-hide affordances) doesn't fit it, and it has exactly one caller.

- [ ] **Step 2: Typecheck**

Run: `cd apps/web && npm run typecheck`
Expected: FAILS — `useParams({ from: "/_authed/users/$username/sftp-key" })` references a route that doesn't exist in the router tree yet. That's expected; Task 4 registers it.

- [ ] **Step 3: Commit**

```bash
git add apps/web/src/routes/user-sftp-key.tsx
git commit -m "feat(web): add the SFTP public key management page"
```

---

## Task 4: Register the route

**Files:**
- Modify: `apps/web/src/router.tsx`

- [ ] **Step 1: Add the route**

In `apps/web/src/router.tsx`, add to the imports:

```ts
import { UserSFTPKey } from "./routes/user-sftp-key";
```

Add, after `userPasswordRoute`:

```ts
const userSFTPKeyRoute = createRoute({
  getParentRoute: () => authedRoute,
  path: "/users/$username/sftp-key",
  component: UserSFTPKey,
});
```

Add `userSFTPKeyRoute` to the `authedRoute.addChildren([...])` list, after `userPasswordRoute`:

```ts
    authedRoute.addChildren([
      usersRoute,
      userNewRoute,
      userEditRoute,
      userPasswordRoute,
      userSFTPKeyRoute,
      auditRoute,
      filesRoute,
    ]),
```

- [ ] **Step 2: Typecheck**

Run: `cd apps/web && npm run typecheck`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add apps/web/src/router.tsx
git commit -m "feat(web): register the /users/:username/sftp-key route"
```

---

## Task 5: Row action button on the Users list

**Files:**
- Modify: `apps/web/src/routes/users.tsx`

- [ ] **Step 1: Add a `KeyIcon` and the row-action button**

In `apps/web/src/routes/users.tsx`, add a new icon component next to the existing `ResetIcon`:

```tsx
function KeyIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="15" r="4" />
      <path d="m10.5 12.5 8-8M16 5l3 3m-6.5 1.5L15 12" />
    </svg>
  );
}
```

In the `actions` column's `cell` function, add a button right after the "Reset password" button and before the "Enable/Disable" button:

```tsx
              <button
                aria-label={`Set SFTP key for ${u.username}`}
                title="Set SFTP public key"
                onClick={() =>
                  nav({ to: "/users/$username/sftp-key", from: "/users", params: { username: u.username } })
                }
              >
                <KeyIcon />
              </button>
```

- [ ] **Step 2: Typecheck and build**

Run: `cd apps/web && npm run typecheck && npm run build`
Expected: both succeed.

- [ ] **Step 3: Commit**

```bash
git add apps/web/src/routes/users.tsx
git commit -m "feat(web): add a row action to manage a user's SFTP public key"
```

---

## Task 6: Manual verification

No component test framework exists in this project — verify by hand in a browser before calling this done.

- [ ] **Step 1: Start the backend and frontend dev servers**

Backend (from the backend plan, with `SFTP_ENABLED=true` if you want to test an actual SFTP login too):

```bash
cd apps/ftp && DATABASE_URL=postgres://user:pass@localhost:5432/ftp go run ./cmd/ftp-server
```

Frontend:

```bash
cd apps/web && npm run dev
```

- [ ] **Step 2: Check the edit form**

Open the app, go to Users, edit an existing user. Confirm:
- "FTP access" and "SFTP access" switches appear below "Enabled", with the values matching that user's current `ftp_enabled`/`sftp_enabled`.
- Toggling SFTP access on and saving persists — re-opening the edit form shows it still on.

- [ ] **Step 3: Check the new-user form**

Create a new user without touching the two new switches. Confirm the created user has FTP access on and SFTP access off (check via the edit form, or `GET /api/v1/users`).

- [ ] **Step 4: Check the SFTP key action**

From the Users list, click the new key icon for a user. Confirm:
- The page loads at `/users/<username>/sftp-key`.
- Pasting an invalid string and saving shows an error (from the backend's `ssh.ParseAuthorizedKey` validation) and does not navigate away.
- Pasting a real `ssh-ed25519 AAAA...` line and saving shows "Saved." with no error.
- Saving an empty value clears the key without erroring.

- [ ] **Step 5: No commit for this task** — it's verification only. If any check fails, fix the relevant task above and re-verify.

---

## Done criteria

- `npm run typecheck` and `npm run build` pass from `apps/web/`.
- The user edit/create form shows and persists FTP/SFTP access switches.
- A dedicated page lets an admin set or clear a user's SFTP public key, reachable from a row action on the Users list.
