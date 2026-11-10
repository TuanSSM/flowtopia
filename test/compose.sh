#!/usr/bin/env bash
# compose.yml as a fresh machine meets it: who can reach the app once `docker compose up` has run,
# directly and through the reverse proxy of the "proxy" profile.
#   test/compose.sh          what compose.yml resolves to, without starting anything: the app is published
#                            on this machine only unless FLOWA_BIND names another address, the database
#                            is not published at all, and FLOWA_DOMAIN makes the app answer to that name
#                            and believe forwarding headers from the proxy's network alone. Needs docker
#                            compose, not the daemon.
#   test/compose.sh --up     the same questions asked of running containers: builds the binary and the
#                            image, starts the containers, and asks GET /healthz from this machine by
#                            loopback (must answer) and by each of its other addresses (must not). Then
#                            starts the app again with FLOWA_BIND=0.0.0.0 and asks the other addresses once
#                            more (must answer), which shows the first refusal was the setting's doing and
#                            not a firewall's. Then starts the proxy for a private name and asks it over
#                            HTTPS: the certificate, the way from HTTP, HSTS, the Secure cookie, a write
#                            from an HTTP page, and an address a visitor made up.
# --up uses the names and the ports of the real service (nunchaku, nunchaku-db, nunchaku-proxy; 3169, 80
# and 443), so it refuses to start on a machine where those are taken: it is for CI and a spare machine,
# never the Pi that serves. Nothing is read from .env and nothing is left behind: the build, the data
# folder and the settings live in a temporary folder, and the containers, the volumes, the networks and
# the image are removed at the end.
# Prints PASS, FAIL or SKIP per check, TIME per check for the slowest table, and a count at the end.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
export PATH="$HOME/.local/go/bin:$PATH"

pass=0 fail=0 skip=0 last=$(date +%s.%N)
took() { local now; now=$(date +%s.%N); echo "TIME $(awk -v a="$last" -v b="$now" 'BEGIN { printf "%.1f", b - a }') $1"; last=$now; }
ok() { echo "PASS $1"; pass=$((pass + 1)); took "$1"; }
bad() { echo "FAIL $1${2:+: $2}"; fail=$((fail + 1)); took "$1"; }
skipped() { echo "SKIP $1: $2"; skip=$((skip + 1)); }
is() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "got '$2', wanted '$3'"; fi; }   # is <name> <got> <wanted>
finish() { echo "$pass passed, $fail failed, $skip skipped"; [ "$fail" -eq 0 ]; }

command -v docker >/dev/null && docker compose version >/dev/null 2>&1 || { echo "docker compose is not installed" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is not installed" >&2; exit 2; }

work=$(mktemp -d)
cleanup() { rm -rf "$work"; }
trap cleanup EXIT
# The settings a new machine would have: a database password and nothing else. An empty --env-file
# keeps the real .env out of it, and `with` takes every setting compose.yml reads out of the caller's
# environment before it adds the ones a check asks for:  with NAME=value ... -- command
: > "$work/.env"
compose() { docker compose --env-file "$work/.env" --project-directory "$work" -f compose.yml -p flowa-check "$@"; }
with() {
  (
    unset FLOWA_BIND FLOWA_DOMAIN FLOWA_HOSTS FLOWA_TRUSTED_PROXIES FLOWA_PROXY_NET FLOWA_HOME_URL COMPOSE_PROFILES ADMIN_USER ADMIN_PASSWORD VERSION
    export POSTGRES_PASSWORD=flowa-test-only
    while [ "$1" != -- ]; do export "${1?}"; shift; done
    shift
    "$@"
  )
}
# resolved [NAME=value ...]: compose.yml as JSON. What docker compose has to say beside it (warnings,
# or the error when it refuses the file) goes to $work/said, so it can neither break the JSON nor be lost.
resolved() { with "$@" -- compose config --format json 2>"$work/said"; }
app_env() { jq -r --arg k "$1" '.services.nunchaku.environment[$k] // ""'; }
ports_of() { jq -r --arg s "$1" '.services[$s].ports // [] | map("\(.host_ip // "every address"):\(.published)->\(.target)/\(.protocol)") | join(" ")'; }

# ---- what compose.yml resolves to: the app's port ----
published() {   # published <address expected> <name of the check> [NAME=value ...]
  local want=$1 name=$2; shift 2
  is "$name" "$(resolved "$@" | ports_of nunchaku 2>&1)" "$want:3169->3169/tcp"
}
published 127.0.0.1 "without FLOWA_BIND the app is published on this machine only"
published 127.0.0.1 "an empty FLOWA_BIND means this machine only too" FLOWA_BIND=
published 0.0.0.0 "FLOWA_BIND=0.0.0.0 publishes on every address" FLOWA_BIND=0.0.0.0
published 192.168.1.20 "FLOWA_BIND takes one LAN address" FLOWA_BIND=192.168.1.20
published ::1 "FLOWA_BIND takes an IPv6 address without brackets" FLOWA_BIND=::1
if resolved "FLOWA_BIND=not an address" >/dev/null; then bad "a FLOWA_BIND that is no address stops compose" "compose accepted it"
elif grep -qi 'invalid ip' "$work/said"; then ok "a FLOWA_BIND that is no address stops compose"
else bad "a FLOWA_BIND that is no address stops compose" "it failed for another reason: $(tail -1 "$work/said")"; fi
is "the database is not published" "$(resolved | ports_of db)" ""
# The pin itself, and that the checks would start their own database from the same image.
is "the database's image is the one test/db-image.sh reads, pinned by version and digest" "$(resolved | jq -r '.services.db.image')" "$(test/db-image.sh 2>&1)"

# ---- what compose.yml resolves to: the proxy profile ----
net='10.169.31.0/29'   # the edge network's range in compose.yml
plain=$(resolved)
# A machine that sets nothing but the password is told nothing: a warning here would be printed by every
# docker compose command on every deployment that does not use the proxy.
is "with only the password set, docker compose has nothing to warn about" "$(paste -sd' ' "$work/said" | cut -c1-300)" ""
is "the proxy is not started unless its profile is asked for" "$(jq -r '.services | keys | join(" ")' <<<"$plain")" "db nunchaku"
is "without FLOWA_DOMAIN the app is left to its own choice of trusted proxies" "$(app_env FLOWA_TRUSTED_PROXIES <<<"$plain")" ""
is "without FLOWA_DOMAIN the app answers to no extra name" "$(app_env FLOWA_HOSTS <<<"$plain")" ""
is "the network the app shares with the proxy has a fixed range" "$(jq -r '.networks.edge.ipam.config | map(.subnet) | join(" ")' <<<"$plain")" "$net"
on=$(resolved COMPOSE_PROFILES=proxy FLOWA_DOMAIN=flowa.example.org)
is "nor with the proxy's two settings" "$(paste -sd' ' "$work/said" | cut -c1-300)" ""
is "COMPOSE_PROFILES=proxy adds the proxy" "$(jq -r '.services | keys | join(" ")' <<<"$on")" "db nunchaku proxy"
is "with FLOWA_DOMAIN the app answers to that name" "$(app_env FLOWA_HOSTS <<<"$on" | tr ',' '\n' | grep -c '^flowa\.example\.org$')" "1"
is "with FLOWA_DOMAIN only the proxy's network is believed about a visitor's address" "$(app_env FLOWA_TRUSTED_PROXIES <<<"$on")" "$net"
is "with the proxy the app's own port stays on this machine" "$(ports_of nunchaku <<<"$on")" "127.0.0.1:3169->3169/tcp"
is "the proxy publishes 80 and 443 and nothing else" "$(ports_of proxy <<<"$on")" "every address:80->80/tcp every address:443->443/tcp every address:443->443/udp"
is "the proxy is given the name" "$(jq -r '.services.proxy.environment.FLOWA_DOMAIN' <<<"$on")" "flowa.example.org"
is "the proxy cannot reach the database's network" "$(jq -r '.services.proxy.networks | keys | join(" ")' <<<"$on")" "edge"
if jq -r '.services.proxy.image' <<<"$on" | grep -Eq '^caddy:[0-9]+\.[0-9]+\.[0-9]+-alpine@sha256:[0-9a-f]{64}$'; then ok "the proxy's image is pinned by version and digest"
else bad "the proxy's image is pinned by version and digest" "it is $(jq -r '.services.proxy.image' <<<"$on")"; fi
kept=$(resolved FLOWA_DOMAIN=flowa.example.org FLOWA_HOSTS=raspi.example,other.example FLOWA_TRUSTED_PROXIES=none)
is "names the owner listed are kept beside the proxy's" "$(app_env FLOWA_HOSTS <<<"$kept")" "raspi.example,other.example,flowa.example.org"
is "the owner's FLOWA_TRUSTED_PROXIES wins over the proxy's network" "$(app_env FLOWA_TRUSTED_PROXIES <<<"$kept")" "none"
moved=$(resolved FLOWA_DOMAIN=flowa.example.org FLOWA_PROXY_NET=10.9.8.0/28)
is "FLOWA_PROXY_NET moves the network and what the app believes together" "$(jq -r '.networks.edge.ipam.config[0].subnet' <<<"$moved") $(app_env FLOWA_TRUSTED_PROXIES <<<"$moved")" "10.9.8.0/28 10.9.8.0/28"

[ "${1:-}" = --up ] || { finish; exit; }

# ---- the same, asked of running containers ----
docker info >/dev/null 2>&1 || { echo "the Docker daemon is not running, so --up cannot run" >&2; exit 2; }
taken=$(docker ps -a --format '{{.Names}}' | grep -E '^(nunchaku|nunchaku-db|nunchaku-proxy)$' | paste -sd' ')
[ -z "$taken" ] || { echo "containers named $taken exist: this machine runs Flowa, and --up would replace them" >&2; exit 2; }
# The build below is tagged nunchaku:latest, the name deploy.sh starts and goes back from.
! docker image inspect nunchaku:latest >/dev/null 2>&1 || { echo "an image nunchaku:latest exists: this machine deploys Flowa, and --up would replace it" >&2; exit 2; }
if curl -s -o /dev/null -m 2 http://127.0.0.1:3169/; then echo "port 3169 is in use on this machine, so --up cannot run" >&2; exit 2; fi

cleanup() {
  with COMPOSE_PROFILES=proxy -- compose down --volumes --remove-orphans --timeout 5 >/dev/null 2>&1
  docker image rm -f nunchaku:latest >/dev/null 2>&1
  rm -rf "$work"
}
# The build context is the temporary folder: the Dockerfile and the binary deploy.sh would build.
cp Dockerfile Caddyfile "$work/"
mkdir "$work/data" && chmod 777 "$work/data"   # the container runs as user 1000, which need not be ours
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$work/nunchaku" . || { echo "go build failed" >&2; exit 1; }

up() {   # up [NAME=value ...]: start (or start again) with these settings and wait for the app's own probe
  with VERSION=check "$@" -- compose up -d --build --quiet-pull >"$work/up.log" 2>&1 || { tail -20 "$work/up.log" >&2; return 1; }
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
addresses() { (hostname -I 2>/dev/null || ip -4 -o addr show 2>/dev/null | awk '{ sub("/.*", "", $4); print $4 }') | tr ' ' '\n' | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | grep -v '^127\.' | sort -u; }

if ! up; then
  bad "a fresh compose up becomes healthy" "$(tail -1 "$work/up.log")"
  finish; exit
fi
ok "a fresh compose up becomes healthy"
if answers 127.0.0.1; then ok "the app answers on 127.0.0.1"; else bad "the app answers on 127.0.0.1"; fi
is "docker reports the port published on 127.0.0.1:3169" "$(with -- compose port nunchaku 3169 2>&1)" "127.0.0.1:3169"

others=$(addresses)
if [ -z "$others" ]; then
  skipped "the app does not answer on the machine's other addresses" "this machine has no address but loopback"
  skipped "with FLOWA_BIND=0.0.0.0 the other addresses answer" "this machine has no address but loopback"
else
  open=""
  for ip in $others; do answers "$ip" && open+="${open:+ }$ip"; done
  if [ -z "$open" ]; then ok "the app does not answer on the machine's other addresses ($(paste -sd' ' <<<"$others"))"
  else bad "the app does not answer on the machine's other addresses" "it answered on $open"; fi

  if ! up FLOWA_BIND=0.0.0.0; then
    bad "with FLOWA_BIND=0.0.0.0 the app starts again healthy" "$(tail -1 "$work/up.log")"
    finish; exit
  fi
  closed=""
  for ip in $others; do answers "$ip" || closed+="${closed:+ }$ip"; done
  if [ -z "$closed" ]; then ok "with FLOWA_BIND=0.0.0.0 the other addresses answer"
  else bad "with FLOWA_BIND=0.0.0.0 the other addresses answer" "no answer on $closed, so the refusal above proves nothing here"; fi
  if answers 127.0.0.1; then ok "and 127.0.0.1 still answers"; else bad "and 127.0.0.1 still answers"; fi
fi

# ---- the proxy profile, for a name only this machine knows ----
# Caddy signs a name under .localhost with its own authority, so no certificate is asked of anyone; curl
# is told where the name lives (--resolve) and whose signature to accept (--cacert), and checks the
# certificate as a browser would.
name=flowa.localhost
for port in 80 443; do
  if curl -s -o /dev/null -m 2 "http://127.0.0.1:$port/"; then
    bad "ports 80 and 443 are free for the proxy" "something answers on $port; the proxy checks cannot run"
    finish; exit
  fi
done
proxy=(COMPOSE_PROFILES=proxy "FLOWA_DOMAIN=$name" ADMIN_USER=admin ADMIN_PASSWORD=flowa-test-only)

# Without a name the proxy must say so and stop, not serve something else.
with COMPOSE_PROFILES=proxy -- compose up -d --quiet-pull proxy >"$work/up.log" 2>&1
said=""
for _ in $(seq 1 20); do
  said=$(docker logs nunchaku-proxy 2>&1 | grep -m1 'FLOWA_DOMAIN is not set') && break
  sleep 0.5
done
if [ -n "$said" ] && ! curl -s -o /dev/null -m 2 http://127.0.0.1:80/; then ok "the proxy without FLOWA_DOMAIN says so and serves nothing"
else bad "the proxy without FLOWA_DOMAIN says so and serves nothing" "log: $(docker logs --tail 3 nunchaku-proxy 2>&1 | paste -sd' ' | cut -c1-200)"; fi

if ! up "${proxy[@]}"; then
  bad "the app and the proxy start" "$(tail -1 "$work/up.log")"
  finish; exit
fi
ca="$work/root.crt"
via() { curl -s -m 10 --resolve "$name:443:127.0.0.1" --resolve "$name:80:127.0.0.1" --cacert "$ca" "$@"; }
code=""
for _ in $(seq 1 60); do   # the certificate is made at start; half a minute is far more than it takes
  docker exec nunchaku-proxy cat /data/caddy/pki/authorities/local/root.crt >"$ca" 2>/dev/null
  code=$(via -o /dev/null -w '%{http_code}' "https://$name/healthz")
  [ "$code" = 200 ] && break
  sleep 0.5
done
if [ "$code" = 200 ]; then ok "https://$name answers with a certificate curl accepts"
else
  bad "https://$name answers with a certificate curl accepts" "status '$code'; proxy log: $(docker logs --tail 3 nunchaku-proxy 2>&1 | paste -sd' ' | cut -c1-300)"
  finish; exit
fi
is "docker reports the app still published on 127.0.0.1:3169" "$(with "${proxy[@]}" -- compose port nunchaku 3169 2>&1)" "127.0.0.1:3169"
is "plain HTTP is sent on to HTTPS" "$(via -o /dev/null -w '%{http_code} %{redirect_url}' "http://$name/healthz")" "308 https://$name/healthz"
head=$(via -D - -o /dev/null "https://$name/" | tr -d '\r')
is "the answer carries HSTS for a year" "$(grep -i '^strict-transport-security:' <<<"$head" | cut -d' ' -f2-)" "max-age=31536000"
# The headers the app sets on its own port, and the same request through the proxy.
sec() { tr 'A-Z' 'a-z' | grep -E '^(content-security-policy|x-frame-options|x-content-type-options|referrer-policy|cross-origin-opener-policy|permissions-policy):' | sort; }
direct=$(curl -s -m 5 -D - -o /dev/null -H "Host: $name" http://127.0.0.1:3169/ | tr -d '\r' | sec)
if [ "$(wc -l <<<"$direct")" -eq 6 ] && [ "$(sec <<<"$head")" = "$direct" ]; then ok "the app's six security headers come through the proxy as the app set them"
else bad "the app's six security headers come through the proxy as the app set them" "direct: $(wc -l <<<"$direct") of 6; through the proxy: $(sec <<<"$head" | wc -l)"; fi
if curl -s -m 5 -o /dev/null --resolve "other.localhost:443:127.0.0.1" --cacert "$ca" https://other.localhost/healthz; then bad "the proxy has no certificate for another name" "it answered other.localhost"
else ok "the proxy has no certificate for another name"; fi

signin() {   # signin <password> [curl options]: the status line and the cookie of POST /api/signin
  local pw=$1; shift
  via -D - -o /dev/null -H 'Content-Type: application/json' "$@" -d "{\"username\":\"admin\",\"password\":\"$pw\"}" "https://$name/api/signin" | tr -d '\r' | grep -iE '^(HTTP|set-cookie:)' | paste -sd' '
}
got=$(signin flowa-test-only -H "Origin: https://$name")
if grep -q '^HTTP/[0-9.]* 200' <<<"$got" && grep -qi '; Secure' <<<"$got" && grep -qi '; HttpOnly' <<<"$got"; then ok "signing in through the proxy gives a Secure cookie"
else bad "signing in through the proxy gives a Secure cookie" "$(cut -c1-60 <<<"$got")"; fi
got=$(signin flowa-test-only -H "Origin: http://$name")
if grep -q '^HTTP/[0-9.]* 403' <<<"$got" && ! grep -qi 'set-cookie' <<<"$got"; then ok "a write from an HTTP page of the same name is refused"
else bad "a write from an HTTP page of the same name is refused" "$(cut -c1-60 <<<"$got")"; fi
# The app on its own port does not take a visitor's word for HTTPS: the machine itself is not the proxy.
got=$(curl -s -m 10 -D - -o /dev/null -H "Host: $name" -H 'X-Forwarded-Proto: https' -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"flowa-test-only"}' http://127.0.0.1:3169/api/signin | tr -d '\r' | grep -iE '^(HTTP|set-cookie:)' | paste -sd' ')
if grep -q '^HTTP/[0-9.]* 200' <<<"$got" && ! grep -qi '; Secure' <<<"$got"; then ok "X-Forwarded-Proto is believed from the proxy's network only"
else bad "X-Forwarded-Proto is believed from the proxy's network only" "$(cut -c1-60 <<<"$got")"; fi
# A wrong password is logged with the address it came from. The visitor sends two made-up ones; the log
# must hold the address the proxy saw, which is neither of them and not the proxy's own.
signin wrong-on-purpose -H 'X-Forwarded-For: 203.0.113.77' -H 'X-Real-IP: 203.0.113.78' >/dev/null
from=""
for _ in $(seq 1 20); do
  from=$(docker logs nunchaku 2>&1 | grep '^{' | jq -r 'select(.refused != null) | .refused' 2>/dev/null | grep 'wrong username or password from ' | tail -1 | sed 's/.* from //')
  [ -n "$from" ] && break
  sleep 0.25
done
own=$(docker inspect nunchaku-proxy --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
case "$from" in
  ""|203.0.113.*) bad "an address the visitor made up is not believed" "the log says '${from:-nothing}'" ;;
  "$own") bad "an address the visitor made up is not believed" "the log has the proxy's own address, $own: its X-Forwarded-For was not read" ;;
  *) ok "an address the visitor made up is not believed (the log has $from, the address the proxy saw)" ;;
esac
is "the app says it believes one range" "$(docker logs nunchaku 2>&1 | grep -o 'believed from [0-9]* trusted proxy ranges' | tail -1)" "believed from 1 trusted proxy ranges"

finish
