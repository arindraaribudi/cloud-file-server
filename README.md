# cloud-file-server

Monorepo: COS-backed FTP server (Go) + admin BFF (Bun + Hono) + admin web UI (Bun + React + TanStack Router).

## Layout

```
apps/
  ftp/      Go FTP server (cmd/, internal/, migrations/, manifests/)
  web/      Bun + Vite + React + TanStack Router admin SPA (calls FTP admin direct)
```

## Prerequisites

- [Bun 1.4](https://bun.com) — workspace root + `apps/web`
- Go 1.23 — `apps/ftp`
- Docker / docker-compose — optional, for local Postgres

## Install

```sh
bun install                # installs all workspaces
(cd apps/ftp && go mod download)
```

## Run

```sh
# Stack — Postgres, FTP server (admin :7000), web (:6000)
docker compose up --build

# Web dev (against host-side FTP admin if running it via `make run`)
bun run dev:web            # :6000, proxies /api → :7000
```

Open <http://localhost:6000>. Sign in with the bootstrap admin user (see `apps/ftp/scripts/smoke.sh`).

## Build

```sh
bun run build              # builds web
(cd apps/ftp && make build)
```

## Test

```sh
bun run typecheck
bun run test:ftp           # Go race tests
bun run lint:ftp           # golangci-lint
```

## Smoke test

```sh
apps/ftp/scripts/smoke.sh  # exercises FTP + admin login + user list
```

## Credentials (`.env`)

```
FTP_PUBLIC_IP=
COS_STATIC_SECRET_ID=...
COS_STATIC_SECRET_KEY=...
COS_STATIC_SESSION_TOKEN=
```

`.env` is git-ignored. See `apps/ftp/.env.example` for the FTP server side.

## Schema

See `apps/ftp/migrations/` and `apps/ftp/docs/spec.md`.
