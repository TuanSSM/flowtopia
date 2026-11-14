#!/usr/bin/env bash
# The restore drill: can Flowa come back from its own backup? A backup that has never been restored is
# a hope, so this takes one with backup.sh and restores it with backup.sh --restore, every week in CI
# (.github/workflows/restore_drill.yml) and whenever a pull request changes what a restore depends on.
#   test/restore.sh              the drill
#   test/restore.sh --fixture    the drill, and keep its dump as testdata/backups/flowa-schema<N>.dump
# What it does:
#   1. starts a Postgres from the image compose.yml pins and the app built from this checkout against
#      it, and puts something in every part a person would miss: the admin, a member, their progress, a
#      post, a comment, a like, the catalogs;
#   2. takes the backup with backup.sh, with the app stopped so that nothing changes behind it;
#   3. restores it with backup.sh --restore into a second, empty Postgres, and compares every table of
#      the two, row for row;
#   4. starts the app on the restored database: it must be healthy on the schema this build needs, the
#      admin and the member must be able to sign in, and the post, the comment and the progress must be
#      what was written;
#   5. checks that a restore is refused while the app runs, and that a broken file changes nothing;
#   6. runs the Go database tests against the restored server;
#   7. restores every dump kept in testdata/backups, each taken by an earlier version of Flowa, and
#      starts today's app on it: a backup from last month must still open after an update.
# Nothing here uses the names or the port of the real service: the containers are flowa-drill-src and
# flowa-drill-dst, the app listens on 3181 and 3182, and everything is removed at the end.
# Prints PASS, FAIL or SKIP per check, TIME per check for the slowest table, and a count at the end.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
export PATH="$HOME/.local/go/bin:$PATH"

pass=0 fail=0 skip=0 last=$(date +%s.%N)
took() { local now; now=$(date +%s.%N); echo "TIME $(awk -v a="$last" -v b="$now" 'BEGIN { printf "%.1f", b - a }') $1"; last=$now; }
ok() { echo "PASS $1"; pass=$((pass + 1)); took "$1"; }
bad() { echo "FAIL $1${2:+: $2}"; fail=$((fail + 1)); took "$1"; }
is() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "got '$(cut -c1-200 <<<"$2")', wanted '$(cut -c1-200 <<<"$3")'"; fi; }
finish() { echo "$pass passed, $fail failed, $skip skipped"; [ "$fail" -eq 0 ]; }

keep_fixture=false
case "${1:-}" in "") ;; --fixture) keep_fixture=true ;; *) echo "usage: test/restore.sh [--fixture]" >&2; exit 2 ;; esac
docker info >/dev/null 2>&1 || { echo "the Docker daemon is not running" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is not installed" >&2; exit 2; }
img=$(test/db-image.sh) || exit 2
for c in flowa-drill-src flowa-drill-dst; do
  ! docker inspect "$c" >/dev/null 2>&1 || { echo "a container named $c exists: another drill is running, or one was cut short (docker rm -f $c)" >&2; exit 2; }
done

work=$(mktemp -d)
pids=()
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null; done
  docker rm -f flowa-drill-src flowa-drill-dst >/dev/null 2>&1
  rm -rf "$work"
}
trap cleanup EXIT

# ---- two databases and the app ----
db() {   # db <container> <port>: a Postgres like the one compose.yml starts, published on this machine only
  docker run -d --name "$1" -e POSTGRES_USER=flowa -e POSTGRES_DB=flowa -e POSTGRES_PASSWORD=drill \
    -p "127.0.0.1:$2:5432" --tmpfs /var/lib/postgresql/data "$img" >/dev/null || return 1
  for _ in $(seq 1 60); do
    # The image starts Postgres twice (once to set it up); only the second start listens on the network.
    PGPASSWORD=drill PGCONNECT_TIMEOUT=2 docker exec "$1" psql -h 127.0.0.1 -U flowa -d flowa -Atqc 'select 1' >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  return 1
}
sql() { docker exec -i "$1" psql -U flowa -d flowa -v ON_ERROR_STOP=1 -AtqX -c "$2"; }   # sql <container> <statement>
url() { echo "postgres://flowa:drill@127.0.0.1:$1/flowa?sslmode=disable"; }
app() {   # app <database port> <listen port>: start the app, wait for it, leave its pid in $app_pid
  mkdir -p "$work/data-$2"
  DATABASE_URL=$(url "$1") ADMIN_USER=admin ADMIN_PASSWORD=flowa-test-only FLOWA_PREVIEW_FETCH=off \
    "$work/nunchaku" -listen "127.0.0.1:$2" -data "$work/data-$2" >"$work/app-$2.log" 2>&1 &
  app_pid=$!
  pids+=("$app_pid")
  for _ in $(seq 1 60); do
    curl -fsS -m 2 "http://127.0.0.1:$2/healthz" >/dev/null 2>&1 && return 0
    kill -0 "$app_pid" 2>/dev/null || return 1
    sleep 0.5
  done
  return 1
}
stop() { kill "$1" 2>/dev/null; wait "$1" 2>/dev/null; }
# call <listen port> <cookie jar> <method> <path> [json]: the answer's body, then its status on a last line
call() {
  local port=$1 jar=$2 method=$3 path=$4 body=${5:-}
  curl -s -m 20 -b "$jar" -c "$jar" -X "$method" -H 'Content-Type: application/json' ${body:+-d "$body"} \
    -w '\n%{http_code}' "http://127.0.0.1:$port$path"
}
status() { tail -1 <<<"$1"; }
body() { sed '$d' <<<"$1"; }
# Every table of the public schema with its number of rows and a fingerprint of all of them.
picture() {
  local t
  for t in $(sql "$1" "select tablename from pg_tables where schemaname = 'public' order by 1"); do
    echo "$t $(sql "$1" "select count(*) || ' ' || md5(coalesce(string_agg(t::text, E'\n' order by t::text), '')) from \"$t\" t")"
  done
}

CGO_ENABLED=0 go build -o "$work/nunchaku" . || { echo "go build failed" >&2; exit 1; }
if ! db flowa-drill-src 55441 || ! db flowa-drill-dst 55442; then
  bad "two databases start from the image compose.yml pins" "$(docker logs --tail 3 flowa-drill-src 2>&1 | paste -sd' ' | cut -c1-200)"
  finish; exit
fi
if ! app 55441 3181; then
  bad "the app starts on an empty database" "$(tail -3 "$work/app-3181.log" | paste -sd' ' | cut -c1-300)"
  finish; exit
fi
src_pid=$app_pid
ok "two databases start from the image compose.yml pins, and the app on the first"

# ---- 1. something to lose ----
admin="$work/admin.jar" member="$work/member.jar"
state='{"levels":{"F1":3,"F5":2},"sessions":[{"date":"2026-11-03","minutes":25,"skills":["F1","F5"]}],"drill":"kept"}'
r=$(call 3181 "$admin" POST /api/signin '{"username":"admin","password":"flowa-test-only"}'); s1=$(status "$r")
r=$(call 3181 "$member" POST /api/signup '{"username":"drillmember","password":"a-long-drill-phrase","displayName":"Drill Member"}'); s2=$(status "$r")
r=$(call 3181 "$member" PUT /api/progress/nunchaku "{\"rev\":0,\"state\":$state}"); s3=$(status "$r")
r=$(call 3181 "$member" POST /api/posts '{"body":"The drill post: restore me, and @admin too."}'); s4=$(status "$r")
post=$(body "$r" | jq -r '.posts[0].id // empty')
r=$(call 3181 "$admin" POST "/api/posts/$post/comments" '{"body":"The drill comment."}'); s5=$(status "$r")
r=$(call 3181 "$admin" POST "/api/posts/$post/like" '{}'); s6=$(status "$r")
is "the admin signs in, a member joins, saves progress and posts, the admin comments and likes" "$s1 $s2 $s3 $s4 $s5 $s6" "200 200 200 200 200 200"
[ -n "$post" ] || { bad "the post has an id to find it by after the restore" "the answer was $(body "$r" | cut -c1-200)"; finish; exit; }
stop "$src_pid"   # nothing may change between the backup and the comparison
before=$(picture flowa-drill-src)
filled=$(awk '$2 > 0 { n++ } END { print n + 0 }' <<<"$before")
if [ "$filled" -ge 9 ]; then ok "$filled of $(wc -l <<<"$before") tables hold rows before the backup"
else bad "at least 9 tables hold rows before the backup" "only $filled: $(awk '$2 > 0 { print $1 }' <<<"$before" | paste -sd' ')"; fi

# ---- 2. the backup ----
export PGUSER=flowa PGDB=flowa OUT="$work/backups" KEEP=14 APP=flowa-drill-app
if CONTAINER=flowa-drill-src ./backup.sh >"$work/backup.log" 2>&1; then ok "backup.sh takes the backup"
else bad "backup.sh takes the backup" "$(tail -2 "$work/backup.log" | paste -sd' ')"; finish; exit; fi
dump=$(ls "$OUT"/flowa-*.dump 2>/dev/null | head -1)
is "the backup is one file that only its owner can read" "$(stat -c %a "$dump" 2>/dev/null)" "600"

# ---- 3. the restore, and the same rows ----
if CONTAINER=flowa-drill-dst ./backup.sh --restore "$dump" >"$work/restore.log" 2>&1; then ok "backup.sh --restore puts it into an empty database"
else bad "backup.sh --restore puts it into an empty database" "$(tail -3 "$work/restore.log" | paste -sd' ' | cut -c1-300)"; finish; exit; fi
after=$(picture flowa-drill-dst)
if [ "$after" = "$before" ]; then ok "every table has the same rows after the restore ($(wc -l <<<"$before") tables, $(awk '{ n += $2 } END { print n }' <<<"$before") rows)"
else bad "every table has the same rows after the restore" "$(diff <(echo "$before") <(echo "$after") | grep '^[<>]' | head -4 | paste -sd' ')"; fi

# ---- 4. the app on what was restored ----
if app 55442 3182; then ok "the app starts on the restored database"
else bad "the app starts on the restored database" "$(tail -3 "$work/app-3182.log" | paste -sd' ' | cut -c1-300)"; finish; exit; fi
dst_pid=$app_pid
health=$(curl -s -m 5 http://127.0.0.1:3182/healthz)
is "it is healthy, on the schema this build needs" "$(jq -r '"\(.ok) \(.database) \(.schema == .migrations)"' <<<"$health" 2>/dev/null)" "true true true"
rm -f "$admin" "$member"
r=$(call 3182 "$admin" POST /api/signin '{"username":"admin","password":"flowa-test-only"}'); s1=$(status "$r")
r=$(call 3182 "$member" POST /api/signin '{"username":"drillmember","password":"a-long-drill-phrase"}'); s2=$(status "$r")
is "the admin and the member sign in with the passwords they had" "$s1 $s2" "200 200"
r=$(call 3182 "$member" GET /api/progress/nunchaku)
is "the member's progress is what was saved" "$(body "$r" | jq -cS '.state' 2>/dev/null)" "$(jq -cS . <<<"$state")"
r=$(call 3182 "$member" GET "/api/posts/$post")
is "the post is there with its words, its comment and its like" \
  "$(body "$r" | jq -r '.posts[0] | "\(.body) | \(.commentCount) | \(.likes)"' 2>/dev/null)" \
  "The drill post: restore me, and @admin too. | 1 | 1"

# ---- 5. what a restore refuses ----
stop "$dst_pid"
now=$(picture flowa-drill-dst)   # the sign-ins above added sessions, so this is what must not change
# The app is running: stand in for its container with one that only sleeps, under the name backup.sh asks.
docker run -d --name flowa-drill-app --entrypoint sleep "$img" 300 >/dev/null 2>&1
CONTAINER=flowa-drill-dst ./backup.sh --restore "$dump" >"$work/refused.log" 2>&1; code=$?
docker rm -f flowa-drill-app >/dev/null 2>&1
if [ "$code" -ne 0 ] && grep -q 'stop it first' "$work/refused.log" && [ "$(picture flowa-drill-dst)" = "$now" ]; then ok "a restore is refused while the app is running, and nothing is touched"
else bad "a restore is refused while the app is running, and nothing is touched" "exit $code: $(tail -1 "$work/refused.log")"; fi
# A dump cut off in the middle, as a copy that ran out of disk would leave it.
head -c "$(( $(stat -c %s "$dump") / 2 ))" "$dump" > "$work/cut.dump"
CONTAINER=flowa-drill-dst ./backup.sh --restore "$work/cut.dump" >"$work/cut.log" 2>&1; code=$?
if [ "$code" -ne 0 ] && [ "$(picture flowa-drill-dst)" = "$now" ]; then ok "a backup cut short fails and the database is left as it was"
else bad "a backup cut short fails and the database is left as it was" "exit $code: $(tail -1 "$work/cut.log")"; fi
printf 'not a dump at all\n' > "$work/text.dump"
CONTAINER=flowa-drill-dst ./backup.sh --restore "$work/text.dump" >"$work/text.log" 2>&1; code=$?
if [ "$code" -ne 0 ] && grep -q 'not a dump pg_restore can read' "$work/text.log" && [ "$(picture flowa-drill-dst)" = "$now" ]; then ok "a file that is no dump is turned down before anything is touched"
else bad "a file that is no dump is turned down before anything is touched" "exit $code: $(tail -1 "$work/text.log")"; fi
CONTAINER=flowa-drill-dst ./backup.sh --restore "$work/no-such.dump" >/dev/null 2>&1; code=$?
is "a file that is not there is a usage error" "$code" "2"

# ---- 6. the Go tests on the restored server ----
if TEST_DATABASE_URL=$(url 55442) go test -count=1 -run DB ./... >"$work/go.log" 2>&1; then ok "the Go database tests pass against the restored server"
else bad "the Go database tests pass against the restored server" "$(grep -E '^(--- FAIL|FAIL|panic)|^ +[a-z_0-9]+_test\.go:[0-9]+:' "$work/go.log" | head -4 | paste -sd' ' | cut -c1-400)"; fi

# ---- 7. backups from earlier versions ----
if $keep_fixture; then
  n=$(sql flowa-drill-src "select max(version) from schema_migrations")
  mkdir -p testdata/backups
  if [ -e "testdata/backups/flowa-schema$n.dump" ]; then echo "testdata/backups/flowa-schema$n.dump is already there; it is kept as it is" >&2
  else cp "$dump" "testdata/backups/flowa-schema$n.dump" && chmod 644 "testdata/backups/flowa-schema$n.dump" && echo "kept testdata/backups/flowa-schema$n.dump" >&2; fi
fi
found=0
for old in testdata/backups/flowa-schema*.dump; do
  [ -e "$old" ] || continue
  found=$((found + 1))
  name="a backup from schema $(sed 's/.*flowa-schema\([0-9]*\)\.dump/\1/' <<<"$old")"
  docker exec flowa-drill-dst dropdb -U flowa --force flowa && docker exec flowa-drill-dst createdb -U flowa -O flowa flowa
  if ! CONTAINER=flowa-drill-dst ./backup.sh --restore "$old" >"$work/old.log" 2>&1; then bad "$name restores" "$(tail -2 "$work/old.log" | paste -sd' ' | cut -c1-300)"; continue; fi
  if ! app 55442 3182; then bad "$name restores, and today's app starts on it" "$(tail -3 "$work/app-3182.log" | paste -sd' ' | cut -c1-300)"; continue; fi
  health=$(curl -s -m 5 http://127.0.0.1:3182/healthz)
  rm -f "$admin" "$member"
  r=$(call 3182 "$admin" POST /api/signin '{"username":"admin","password":"flowa-test-only"}'); s1=$(status "$r")
  r=$(call 3182 "$member" POST /api/signin '{"username":"drillmember","password":"a-long-drill-phrase"}'); s2=$(status "$r")
  feed=$(body "$(call 3182 "$member" GET /api/posts)" | jq '[.posts[] | select(.body | startswith("The drill post: restore me"))] | length' 2>/dev/null)
  is "$name restores, today's app brings it up to date, both accounts sign in and the post is in the feed" \
    "$(jq -r '"\(.ok) \(.schema == .migrations)"' <<<"$health" 2>/dev/null) $s1 $s2 $feed" "true true 200 200 1"
  stop "$app_pid"
done
if [ "$found" -eq 0 ]; then echo "SKIP a backup from an earlier version restores: testdata/backups holds none (test/restore.sh --fixture keeps one)"; skip=$((skip + 1)); fi

finish
