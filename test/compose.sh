#!/usr/bin/env bash
# compose.yml as a fresh machine meets it: who can reach the app once `docker compose up` has run.
#   test/compose.sh          what compose.yml resolves to, without starting anything: the app is published
#                            on this machine only unless FLOWA_BIND names another address, and the
#                            database is not published at all. Needs docker compose, not the daemon.
#   test/compose.sh --up     the same question asked of running containers: builds the binary and the
#                            image, starts both containers, and asks GET /healthz from this machine by
#                            loopback (must answer) and by each of its other addresses (must not). Then
#                            starts the app again with FLOWA_BIND=0.0.0.0 and asks the other addresses once
#                            more (must answer), which shows the first refusal was the setting's doing and
#                            not a firewall's.
# --up uses the names and the port of the real service (nunchaku, nunchaku-db, 3169), so it refuses to
# start on a machine where those are taken: it is for CI and a spare machine, never the Pi that serves.
# Nothing is read from .env and nothing is left behind: the build, the data folder and the settings live
# in a temporary folder, and the containers, the volume and the image are removed at the end.
# Prints PASS, FAIL or SKIP per check, TIME per check for the slowest table, and a count at the end.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
export PATH="$HOME/.local/go/bin:$PATH"

pass=0 fail=0 skip=0 last=$(date +%s.%N)
took() { local now; now=$(date +%s.%N); echo "TIME $(awk -v a="$last" -v b="$now" 'BEGIN { printf "%.1f", b - a }') $1"; last=$now; }
ok() { echo "PASS $1"; pass=$((pass + 1)); took "$1"; }
bad() { echo "FAIL $1${2:+: $2}"; fail=$((fail + 1)); took "$1"; }
skipped() { echo "SKIP $1: $2"; skip=$((skip + 1)); }
finish() { echo "$pass passed, $fail failed, $skip skipped"; [ "$fail" -eq 0 ]; }

command -v docker >/dev/null && docker compose version >/dev/null 2>&1 || { echo "docker compose is not installed" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is not installed" >&2; exit 2; }

work=$(mktemp -d)
cleanup() { rm -rf "$work"; }
trap cleanup EXIT
# The settings a new machine would have: a database password and nothing else. An empty --env-file
# keeps the real .env out of it, and FLOWA_BIND is taken out of the caller's environment.
: > "$work/.env"
compose() { docker compose --env-file "$work/.env" --project-directory "$work" -f compose.yml -p flowa-check "$@"; }
resolved() {   # resolved <FLOWA_BIND or "unset">: compose.yml as JSON with that setting
  if [ "$1" = unset ]; then (unset FLOWA_BIND; POSTGRES_PASSWORD=flowa-test-only compose config --format json)
  else FLOWA_BIND="$1" POSTGRES_PASSWORD=flowa-test-only compose config --format json; fi
}

# ---- what compose.yml resolves to ----
published() {   # published <FLOWA_BIND or "unset"> <address expected> <name of the check>
  local got
  got=$(resolved "$1" 2>&1 | jq -r '.services.nunchaku.ports | map("\(.host_ip // "every address"):\(.published)->\(.target)/\(.protocol)") | join(" ")' 2>&1)
  if [ "$got" = "$2:3169->3169/tcp" ]; then ok "$3"; else bad "$3" "got '$got', wanted '$2:3169->3169/tcp'"; fi
}
published unset 127.0.0.1 "without FLOWA_BIND the app is published on this machine only"
published "" 127.0.0.1 "an empty FLOWA_BIND means this machine only too"
published 0.0.0.0 0.0.0.0 "FLOWA_BIND=0.0.0.0 publishes on every address"
published 192.168.1.20 192.168.1.20 "FLOWA_BIND takes one LAN address"
published ::1 ::1 "FLOWA_BIND takes an IPv6 address without brackets"
if out=$(resolved "not an address" 2>&1); then bad "a FLOWA_BIND that is no address stops compose" "compose accepted it"
elif grep -qi 'invalid ip' <<<"$out"; then ok "a FLOWA_BIND that is no address stops compose"
else bad "a FLOWA_BIND that is no address stops compose" "it failed for another reason: $(tail -1 <<<"$out")"; fi
dbports=$(resolved unset 2>/dev/null | jq -c '.services.db.ports // []')
if [ "$dbports" = "[]" ]; then ok "the database is not published"; else bad "the database is not published" "db has ports $dbports"; fi

[ "${1:-}" = --up ] || { finish; exit; }

# ---- the same, asked of running containers ----
docker info >/dev/null 2>&1 || { echo "the Docker daemon is not running, so --up cannot run" >&2; exit 2; }
taken=$(docker ps -a --format '{{.Names}}' | grep -E '^(nunchaku|nunchaku-db)$' | paste -sd' ')
[ -z "$taken" ] || { echo "containers named $taken exist: this machine runs Flowa, and --up would replace them" >&2; exit 2; }
# The build below is tagged nunchaku:latest, the name deploy.sh starts and goes back from.
! docker image inspect nunchaku:latest >/dev/null 2>&1 || { echo "an image nunchaku:latest exists: this machine deploys Flowa, and --up would replace it" >&2; exit 2; }
if curl -s -o /dev/null -m 2 http://127.0.0.1:3169/; then echo "port 3169 is in use on this machine, so --up cannot run" >&2; exit 2; fi

cleanup() {
  POSTGRES_PASSWORD=flowa-test-only compose down --volumes --timeout 5 >/dev/null 2>&1
  docker image rm -f nunchaku:latest >/dev/null 2>&1
  rm -rf "$work"
}
# The build context is the temporary folder: the Dockerfile and the binary deploy.sh would build.
cp Dockerfile "$work/Dockerfile"
mkdir "$work/data" && chmod 777 "$work/data"   # the container runs as user 1000, which need not be ours
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$work/nunchaku" . || { echo "go build failed" >&2; exit 1; }

up() {   # up <FLOWA_BIND or "unset">: start (or start again) and wait for the app's own probe
  if [ "$1" = unset ]; then (unset FLOWA_BIND; POSTGRES_PASSWORD=flowa-test-only VERSION=check compose up -d --build --quiet-pull) >"$work/up.log" 2>&1
  else FLOWA_BIND="$1" POSTGRES_PASSWORD=flowa-test-only VERSION=check compose up -d --quiet-pull >"$work/up.log" 2>&1; fi || { tail -20 "$work/up.log" >&2; return 1; }
  for _ in $(seq 1 90); do
    docker exec nunchaku /nunchaku -healthcheck >/dev/null 2>&1 && return 0
    sleep 1
  done
  docker logs --tail 30 nunchaku >&2 || true
  return 1
}
answers() { [ "$(curl -s -m 3 -o /dev/null -w '%{http_code}' "http://$1:3169/healthz")" = 200 ]; }
# Every IPv4 address of this machine that is not loopback: its network cards and Docker's own bridges.
# IPv6 is left to the check of what compose.yml resolves to.
others=$( (hostname -I 2>/dev/null || ip -4 -o addr show 2>/dev/null | awk '{ sub("/.*", "", $4); print $4 }') | tr ' ' '\n' | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | grep -v '^127\.' | sort -u)

if ! up unset; then
  bad "a fresh compose up becomes healthy" "$(tail -1 "$work/up.log")"
  finish; exit
fi
ok "a fresh compose up becomes healthy"
if answers 127.0.0.1; then ok "the app answers on 127.0.0.1"; else bad "the app answers on 127.0.0.1"; fi
at=$(POSTGRES_PASSWORD=flowa-test-only compose port nunchaku 3169 2>&1)
if [ "$at" = 127.0.0.1:3169 ]; then ok "docker reports the port published on 127.0.0.1:3169"; else bad "docker reports the port published on 127.0.0.1:3169" "it says '$at'"; fi

if [ -z "$others" ]; then
  skipped "the app does not answer on the machine's other addresses" "this machine has no address but loopback"
  skipped "with FLOWA_BIND=0.0.0.0 the other addresses answer" "this machine has no address but loopback"
  finish; exit
fi
open=""
for ip in $others; do answers "$ip" && open+="${open:+ }$ip"; done
if [ -z "$open" ]; then ok "the app does not answer on the machine's other addresses ($(paste -sd' ' <<<"$others"))"
else bad "the app does not answer on the machine's other addresses" "it answered on $open"; fi

if ! up 0.0.0.0; then
  bad "with FLOWA_BIND=0.0.0.0 the app starts again healthy" "$(tail -1 "$work/up.log")"
  finish; exit
fi
closed=""
for ip in $others; do answers "$ip" || closed+="${closed:+ }$ip"; done
if [ -z "$closed" ]; then ok "with FLOWA_BIND=0.0.0.0 the other addresses answer"
else bad "with FLOWA_BIND=0.0.0.0 the other addresses answer" "no answer on $closed, so the refusal above proves nothing here"; fi
if answers 127.0.0.1; then ok "and 127.0.0.1 still answers"; else bad "and 127.0.0.1 still answers"; fi

finish
