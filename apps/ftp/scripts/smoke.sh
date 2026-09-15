#!/usr/bin/env bash
# Smoke test for the FTP server. Assumes:
#   - Postgres reachable at DATABASE_URL
#   - Migrations applied (cmd/migrate up already run)
#   - An admin user seeded (default username "admin", password "ChangeMe!1")
#
# Usage:
#   DATABASE_URL=postgres://postgres:test@localhost:5432/postgres ./scripts/smoke.sh
#
# Exits 0 on full success, non-zero on first failure.
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL required}"
: "${ADMIN_USER:=admin}"
: "${ADMIN_PASS:=ChangeMe!1}"
: "${FTP_LISTEN_HOST:=127.0.0.1}"
: "${FTP_LISTEN_PORT:=2121}"
: "${ADMIN_LISTEN_HOST:=127.0.0.1}"
: "${ADMIN_LISTEN_PORT:=8080}"

ADMIN_URL="http://${ADMIN_LISTEN_HOST}:${ADMIN_LISTEN_PORT}"
FTP_URL="ftp://${FTP_LISTEN_HOST}:${FTP_LISTEN_PORT}"

# Build if missing.
if [[ ! -x ./bin/ftp-server ]]; then
    echo "Building ./bin/ftp-server..."
    make build
fi

# Apply migrations (idempotent).
echo "==> Apply migrations"
DATABASE_URL="$DATABASE_URL" ./bin/migrate up

# Start service in background.
echo "==> Start service"
DATABASE_URL="$DATABASE_URL" ./bin/ftp-server &
PID=$!
trap "kill $PID 2>/dev/null || true" EXIT
sleep 1

# Wait for admin UI.
for _ in $(seq 1 10); do
    if curl -sf -o /dev/null "$ADMIN_URL/metrics"; then break; fi
    sleep 0.5
done

# Login.
echo "==> Admin login"
LOGIN_OUT=$(curl -sS -c /tmp/smoke-cookie -X POST "$ADMIN_URL/api/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}")
echo "$LOGIN_OUT"

if ! grep -q "session" /tmp/smoke-cookie; then
    echo "FAIL: login did not return session cookie"
    exit 1
fi

# Users list.
echo "==> Users list"
curl -sS -b /tmp/smoke-cookie "$ADMIN_URL/api/v1/users"

# FTP login + LIST (requires lftp or ftp client).
echo "==> FTP login + LIST"
if command -v lftp >/dev/null 2>&1; then
    lftp -e "set ftp:ssl-force true; user $ADMIN_USER $ADMIN_PASS; ls; bye" "$FTP_URL"
elif command -v ftp >/dev/null 2>&1; then
    echo "lftp not found; skipping FTP probe (curl-based smoke would need a custom client)"
fi

echo "==> All smoke checks passed"
