#!/usr/bin/env bash
# Browser tests against a throwaway database and a dev server on :3170, never the real data.
#   test/browser/run.sh combo.py [demos.py ...]
# Without DATABASE_URL a throwaway Postgres is started in Docker, from the image compose.yml pins
# (test/db-image.sh); one left running from an older pin is replaced. With it (as in CI, where a
# Postgres service is provided) the database must be disposable: its public schema is emptied
# before each suite, so suites never see each other's accounts.
# Exits non-zero when any check fails, so it can gate a pull request.
cd "$(dirname "$0")"; W=/tmp/flowa-test; mkdir -p $W
export PATH="$HOME/.local/go/bin:$PATH"
(cd ../.. && CGO_ENABLED=0 go build -o $W/nunchaku-dev .) || exit 1
[ -f $W/dev.pid ] && kill "$(cat $W/dev.pid)" 2>/dev/null; sleep .5
if [ -z "${DATABASE_URL:-}" ]; then
  img=$(../db-image.sh) || exit 1
  [ "$(docker inspect flowa-test-db --format '{{.Config.Image}}' 2>/dev/null)" = "$img" ] || docker rm -f flowa-test-db >/dev/null 2>&1
  docker ps --format '{{.Names}}' | grep -q '^flowa-test-db$' || { docker run -d --rm --name flowa-test-db -e POSTGRES_PASSWORD=test -p 127.0.0.1:55432:5432 --tmpfs /var/lib/postgresql/data "$img" >/dev/null; for i in $(seq 1 30); do docker exec flowa-test-db pg_isready -U postgres -q && break; sleep 1; done; sleep 1; }
  # Start from an empty public schema, whatever tables earlier runs or branches created.
  docker exec flowa-test-db psql -U postgres -q -c "drop schema if exists public cascade; create schema public" >/dev/null 2>&1
  DATABASE_URL='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable'
elif command -v psql >/dev/null; then
  psql "$DATABASE_URL" -q -c "drop schema if exists public cascade; create schema public" >/dev/null || exit 1
else
  echo "psql is not installed, so the database given in DATABASE_URL cannot be emptied first" >&2; exit 1
fi
rm -rf $W/data; mkdir $W/data
(DATABASE_URL="$DATABASE_URL" ADMIN_USER=admin ADMIN_PASSWORD=flowa-test-only $W/nunchaku-dev -listen :3170 -data $W/data >$W/dev.log 2>&1 & echo $! >$W/dev.pid)
for i in $(seq 1 30); do curl -fsS -m 2 http://127.0.0.1:3170/healthz >/dev/null 2>&1 && break; sleep 1; done
timeout "${LIMIT:-500}" python3 -I "$@" > $W/out.txt 2>&1; code=$?
kill "$(cat $W/dev.pid)" 2>/dev/null
echo "$(grep -c '^PASS' $W/out.txt) passed, $(grep -c '^FAIL ' $W/out.txt) failed, $(grep -c '^SKIP ' $W/out.txt) skipped"
grep -v '^TIME ' $W/out.txt | grep -E '^FAIL|Error|Traceback|FAILURES' -A2 | cut -c1-300 | head -40
# The slowest checks, in seconds: each one's time runs from the check before it.
grep '^TIME ' $W/out.txt | head -5 | sed 's/^TIME /slow: /' | cut -c1-120
[ $code -eq 0 ] && grep -q '^FAILURES: none' $W/out.txt
