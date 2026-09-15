# Page List — User CRUD + Password Management + OIDC Login

Scope: extend the existing web app (`apps/web`, TanStack Router + React Query) to support full CRUD on FTP users, password management, and OIDC login alongside the existing username/password login.

Existing pages today: `/login` (username/password only), `/users` (read-only list), `/audit` (read-only), all under the `_authed` layout except `/login`.

## Pages

| # | Route | Page | Purpose | Notes |
|---|-------|------|---------|-------|
| 1 | `/login` | Login | Extend existing form: keep username/password fields, add "Sign in with SSO" button that redirects to the OIDC authorize endpoint | Modify `routes/login.tsx` |
| 2 | `/auth/callback` | OIDC Callback | Receives the OIDC redirect (`code`/`state`), exchanges it server-side, then routes to `/users` on success or `/login?error=` on failure | New route, outside `_authed` |
| 3 | `/users` | Users List | Extend existing table: add "New user" button, row actions (Edit, Reset password, Delete) | Modify `routes/users.tsx` |
| 4 | `/users/new` | Create User | Form: username, root folder, initial password, enabled toggle | New route + `POST /api/v1/users` |
| 5 | `/users/:username/edit` | Edit User | Form: root folder, enabled toggle (username immutable) | New route + `PATCH /api/v1/users/:username` |
| 6 | `/users/:username/password` | Reset Password | Set/reset a user's password (admin-driven) | New route + `POST /api/v1/users/:username/password` |
| 7 | `/account/password` | Change My Password | Self-service password change for the logged-in admin | New route + `POST /api/v1/account/password` |
| 8 | `/audit` | Audit Log | Unchanged | Existing |

## Notes

- Delete user can be a confirm dialog on the Users List (row action) rather than its own page — skip a dedicated `/users/:username/delete` route unless a confirmation page is explicitly wanted.
- OIDC config (issuer, client id, redirect URI) lives server-side; the web app only needs the authorize URL and the callback route.
- `_authed` layout nav (`router.tsx` / `_authed.tsx`) gets no new top-level entries — new/edit/password pages are reached via row actions on `/users`, and `/account/password` via a user menu.
