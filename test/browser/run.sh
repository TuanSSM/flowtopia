#!/usr/bin/env bash
# Browser tests against a throwaway database and a dev server on :3170, never the real data.
#   test/browser/run.sh combo.py
cd "$(dirname "$0")"; W=/tmp/flowa-test; mkdir -p $W
export PATH="$HOME/.local/go/bin:$PATH"
(cd ../.. && CGO_ENABLED=0 go build -o $W/nunchaku-dev .) || exit 1
[ -f $W/dev.pid ] && kill "$(cat $W/dev.pid)" 2>/dev/null; sleep .5
docker ps --format '{{.Names}}' | grep -q '^flowa-test-db$' || { docker run -d --rm --name flowa-test-db -e POSTGRES_PASSWORD=test -p 127.0.0.1:55432:5432 --tmpfs /var/lib/postgresql/data postgres:16-alpine >/dev/null; for i in $(seq 1 30); do docker exec flowa-test-db pg_isready -U postgres -q && break; sleep 1; done; sleep 1; }
docker exec flowa-test-db psql -U postgres -q -c "drop table if exists sessions, progress, flags, users cascade" >/dev/null 2>&1
rm -rf $W/data; mkdir $W/data
(DATABASE_URL='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable' ADMIN_USER=admin ADMIN_PASSWORD=flowa-test-only $W/nunchaku-dev -listen :3170 -data $W/data >$W/dev.log 2>&1 & echo $! >$W/dev.pid)
sleep 3
timeout "${LIMIT:-500}" python3 -I "$@" > $W/out.txt 2>&1
kill "$(cat $W/dev.pid)" 2>/dev/null
echo "$(grep -c '^PASS' $W/out.txt) passed"; grep -E '^FAIL|Error|Traceback|FAILURES' -A2 $W/out.txt | cut -c1-300 | head -30
