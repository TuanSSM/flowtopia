#!/usr/bin/env bash
# Browser tests against a throwaway database and a dev server on :3170, never the real data.
#   test/browser/run.sh combo.py [demos.py ...]
# Without DATABASE_URL a throwaway Postgres is started in Docker. With it (as in CI, where a
# Postgres service is provided) the database is used as given and must be empty or disposable.
# Exits non-zero when any check fails, so it can gate a pull request.
cd "$(dirname "$0")"; W=/tmp/flowa-test; mkdir -p $W
export PATH="$HOME/.local/go/bin:$PATH"
(cd ../.. && CGO_ENABLED=0 go build -o $W/nunchaku-dev .) || exit 1
[ -f $W/dev.pid ] && kill "$(cat $W/dev.pid)" 2>/dev/null; sleep .5
if [ -z "${DATABASE_URL:-}" ]; then
  docker ps --format '{{.Names}}' | grep -q '^flowa-test-db$' || { docker run -d --rm --name flowa-test-db -e POSTGRES_PASSWORD=test -p 127.0.0.1:55432:5432 --tmpfs /var/lib/postgresql/data postgres:16-alpine >/dev/null; for i in $(seq 1 30); do docker exec flowa-test-db pg_isready -U postgres -q && break; sleep 1; done; sleep 1; }
  docker exec flowa-test-db psql -U postgres -q -c "drop table if exists notifications, comments, likes, posts, achievements, sessions, progress, flags, users cascade" >/dev/null 2>&1
  DATABASE_URL='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable'
fi
rm -rf $W/data; mkdir $W/data
(DATABASE_URL="$DATABASE_URL" ADMIN_USER=admin ADMIN_PASSWORD=flowa-test-only $W/nunchaku-dev -listen :3170 -data $W/data >$W/dev.log 2>&1 & echo $! >$W/dev.pid)
for i in $(seq 1 30); do curl -fsS -m 2 http://127.0.0.1:3170/healthz >/dev/null 2>&1 && break; sleep 1; done
timeout "${LIMIT:-500}" python3 -I "$@" > $W/out.txt 2>&1; code=$?
kill "$(cat $W/dev.pid)" 2>/dev/null
echo "$(grep -c '^PASS' $W/out.txt) passed, $(grep -c '^FAIL ' $W/out.txt) failed, $(grep -c '^SKIP ' $W/out.txt) skipped"
grep -E '^FAIL|Error|Traceback|FAILURES' -A2 $W/out.txt | cut -c1-300 | head -40
[ $code -eq 0 ] && grep -q '^FAILURES: none' $W/out.txt
