#!/usr/bin/env bash
# Two Flowa instances federating with each other, each from the image a release builds, on a Docker network of
# their own: the federation suite (docs/federation.md 13, "As built (FWR 8)").
#   test/federation.sh                 the suite
#   test/federation.sh --logs <dir>    and keep both apps' logs, both proxies' logs and both queues' state there
# What runs:
#   - the image, built once by build-image.sh --load as a release builds it, and the Postgres image compose.yml pins;
#   - on one new network, for each of one.flowa.test and two.flowa.test: a Postgres, the app in federated mode with
#     its own origin and a key secret made for this run, and a small TLS proxy (test/fedproxy) that answers for the
#     name over HTTPS on port 443, with a certificate signed by a certificate authority made for this run. The apps
#     trust that authority through SSL_CERT_FILE, as any Go program on Linux does, and FLOWA_FEDERATION_ALLOW_PRIVATE
#     lets their requests reach the network's private addresses (still https, port 443 and a verified certificate);
#   - every step through the apps' own APIs, over HTTPS through the proxies, as members and admins would, each step
#     checked on the other instance: what a person would see there.
# No fixed waits: each check polls what it needs with a deadline and says how long it took. Nothing here uses the
# names or the port of the real service: every container, network and volume is named flowa-fed-<random>, the
# proxies are published on free ports of 127.0.0.1 only, and everything is removed at the end, on a failure and on
# Ctrl-C too. The key secrets live in this run's temporary folder alone and are never printed.
# Prints PASS, FAIL or SKIP per check, TIME per check for the slowest table, and a count at the end.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
export PATH="$HOME/.local/go/bin:$PATH"

# planned is the number of checks a whole run makes. A run that stops early counts the ones it never reached as skipped,
# with the reason, so the count at the end always adds up to it; a whole run that does not is a failure of the script.
planned=75
# The whole run's deadline, below the CI job's 15 minutes and the step's 13, so that the logs are always written.
limit=${FLOWA_FED_LIMIT:-660}
pass=0 fail=0 skip=0 last=$(date +%s.%N)
took() { local now; now=$(date +%s.%N); echo "TIME $(awk -v a="$last" -v b="$now" 'BEGIN { printf "%.1f", b - a }') $1"; last=$now; }
ok() { echo "PASS $1"; pass=$((pass + 1)); took "$1"; }
bad() { echo "FAIL $1${2:+: $2}"; fail=$((fail + 1)); took "$1"; }
finish() { echo "$pass passed, $fail failed, $skip skipped ($planned planned)"; [ "$fail" -eq 0 ]; }
# stop <why>: the checks not reached are not run; one SKIP line says how many and why, and they count as skipped.
stop() {
  local rest=$((planned - pass - fail - skip))
  [ "$rest" -gt 0 ] || rest=0
  echo "SKIP $rest checks not run: $1"; skip=$((skip + rest)); took "$rest checks not run"
  finish; exit
}

logs=""
case "${1:-}" in
  "") ;;
  --logs) logs=${2:-}; [ -n "$logs" ] || { echo "usage: test/federation.sh [--logs <dir>]" >&2; exit 2; } ;;
  *) echo "usage: test/federation.sh [--logs <dir>]" >&2; exit 2 ;;
esac
docker info >/dev/null 2>&1 || { echo "the Docker daemon is not running" >&2; exit 2; }
docker buildx version >/dev/null 2>&1 || { echo "docker buildx is not installed; build-image.sh needs it" >&2; exit 2; }
for c in go node jq openssl curl; do command -v "$c" >/dev/null || { echo "$c is not installed" >&2; exit 2; }; done
pg=$(test/db-image.sh) || exit 2

id=$(openssl rand -hex 3)
p=flowa-fed-$id
image=flowa-fed-check
version=fed-$id
work=$(mktemp -d)
names=(one two)
declare -A port=()
collected=false

# ---- helpers ----
# waitfor <seconds> <command...>: asks the command until it succeeds or the time runs out. The pause between two
# looks is the only sleep here, and nothing waits for a guessed time.
waitfor() {
  local end=$((SECONDS + $1)); shift
  [ "$end" -le "$deadline" ] || end=$deadline   # never past the whole run's deadline
  until "$@"; do
    [ "$SECONDS" -lt "$end" ] || return 1
    sleep 0.25  # fixed: the pause between two looks at what is awaited
  done
}
since() { echo "$((SECONDS - $1)) s"; }
# api <instance> <who> <method> <path> [json]: one request through the instance's proxy, over HTTPS to its own
# name, with that person's cookies. Leaves the status in $code and the answer in $body.
api() {
  local n=$1 who=$2 m=$3 path=$4 data=${5:-}
  local args=(-sS -m 20 --noproxy '*' --cacert "$work/ca.pem" --resolve "$n.flowa.test:${port[$n]}:127.0.0.1"
    -b "$work/jar-$n-$who" -c "$work/jar-$n-$who" -X "$m" -o "$work/answer" -w '%{http_code}')
  if [ "$m" != GET ]; then args+=(-H 'Content-Type: application/json' --data-binary "${data:-{\}}"); fi
  : >"$work/answer"   # a failed request leaves no earlier answer behind to be read as this one's
  code=$(curl "${args[@]}" "https://$n.flowa.test:${port[$n]}$path" 2>"$work/curl.err") || code=000
  body=$(cat "$work/answer" 2>/dev/null)
}
# ok200 <jq test>: the last answer was a 200 and the test, which names the fields it reads, holds. Every condition below
# goes through it or compares a status, so an unreachable server, an error object or an empty answer never passes.
ok200() { [ "$code" = 200 ] && [ "$(jq -r "$1" <<<"$body" 2>/dev/null)" = true ]; }
# j <jq filter>: reads the last answer.
j() { jq -r "$1" <<<"$body" 2>/dev/null; }
# health <instance>: the app's own /healthz asked from inside its container, the local probe that carries the
# queue's numbers (deploy.sh asks it the same way).
health() { docker exec "$p-$1-app" /nunchaku -healthcheck 2>/dev/null; }
sql() { docker exec -i "$p-$1-db" psql -U flowa -d flowa -AtqX -c "$2" 2>&1; }

collect() {   # the logs and the queues' state, into $work/logs, then into --logs if given
  $collected && return
  collected=true
  mkdir -p "$work/logs"
  local n
  for n in "${names[@]}"; do
    docker logs "$p-$n-app" >"$work/logs/$n-app.log" 2>&1
    docker logs "$p-$n-tls" >"$work/logs/$n-proxy.log" 2>&1
    {
      echo "-- deliveries waiting or failed (a delivered row is removed)"
      sql "$n" "SELECT d.id, d.state, d.attempts, d.next_at, d.inbox, d.last_error, o.ap_id FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id ORDER BY d.id"
      echo "-- inbox by kind, state and outcome"; sql "$n" "SELECT kind, state, outcome, count(*) FROM ap_inbox GROUP BY 1, 2, 3 ORDER BY 1, 2, 3"
      echo "-- hosts"; sql "$n" "SELECT host, state, failures, sig, delivered, dropped, last_success, last_failure, last_error FROM ap_hosts ORDER BY host"
      echo "-- health"; health "$n"
    } >"$work/logs/$n-queue.txt" 2>&1
  done
  if [ -n "$logs" ]; then mkdir -p "$logs" && cp "$work"/logs/* "$logs"/; fi
}
cleanup() {
  [ -d "$work" ] || return
  docker inspect "$p-one-app" >/dev/null 2>&1 && collect
  local c
  for c in $(docker ps -aq --filter "name=^$p-"); do docker rm -f "$c" >/dev/null 2>&1; done
  docker network rm "$p" >/dev/null 2>&1
  docker volume rm "$p-one-data" "$p-two-data" >/dev/null 2>&1
  docker image rm -f "$image:$version" >/dev/null 2>&1
  rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# ---- the image, the certificates, the network ----
started_all=$SECONDS started=$SECONDS deadline=$((SECONDS + limit))
if ./build-image.sh "$image" "$version" --load >"$work/build.log" 2>&1 && CGO_ENABLED=0 go build -trimpath -o "$work/fedproxy" ./test/fedproxy 2>>"$work/build.log"; then
  ok "the image builds as a release builds it (build-image.sh --load, $(since "$started")), and the TLS proxy"
else
  bad "the image builds as a release builds it" "$(tail -3 "$work/build.log" | paste -sd' ' | cut -c1-300)"
  stop "each needs it"
fi
# A certificate authority for this run alone, and a certificate for each name signed by it. The keys stay in the
# temporary folder and go with it.
tls() {
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 1 -subj "/CN=Flowa federation test $id" \
    -addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign,cRLSign \
    -keyout "$work/ca-key.pem" -out "$work/ca.pem" || return 1
  local n
  for n in "${names[@]}"; do
    mkdir -p "$work/tls-$n"
    openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -subj "/CN=$n.flowa.test" \
      -keyout "$work/tls-$n/key.pem" -out "$work/tls-$n/req.pem" || return 1
    printf 'subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth\nkeyUsage=critical,digitalSignature\n' "$n.flowa.test" >"$work/tls-$n/ext.cnf"
    openssl x509 -req -in "$work/tls-$n/req.pem" -CA "$work/ca.pem" -CAkey "$work/ca-key.pem" -CAcreateserial -days 1 \
      -extfile "$work/tls-$n/ext.cnf" -out "$work/tls-$n/cert.pem" || return 1
    chmod 644 "$work/tls-$n/key.pem"   # read by the proxy's own user inside its container; a throwaway key
  done
  chmod 644 "$work/ca.pem"
  chmod 755 "$work/tls-one" "$work/tls-two"
}
if tls >"$work/tls.log" 2>&1 && docker network create "$p" >/dev/null; then
  subnet=$(docker network inspect "$p" --format '{{(index .IPAM.Config 0).Subnet}}')
  ok "a certificate authority and two certificates made for this run, and a network of its own ($subnet)"
else
  bad "a certificate authority and two certificates made for this run, and a network of its own" "$(tail -3 "$work/tls.log" | paste -sd' ' | cut -c1-300)"
  stop "each needs it"
fi

# db_up <instance>: its Postgres (left running when the app restarts) and its data volume, and its settings.
db_up() {
  docker run -d --name "$p-$1-db" --network "$p" -e POSTGRES_USER=flowa -e POSTGRES_DB=flowa -e "POSTGRES_PASSWORD=$dbpw" \
    --tmpfs /var/lib/postgresql/data "$pg" postgres -c shared_buffers=32MB -c max_connections=20 >/dev/null || return 1
  docker volume create "$p-$1-data" >/dev/null || return 1
  # The volume starts as root's; the app is user 1000, as in compose.yml. The Postgres image has the tool to say so.
  docker run --rm -v "$p-$1-data:/data" --entrypoint chown "$pg" 1000:1000 /data || return 1
  # The settings, in a file in the temporary folder only: the key secret is never on a command line or in a log.
  {
    echo "DATABASE_URL=postgres://flowa:$dbpw@$p-$1-db:5432/flowa?sslmode=disable"
    echo "ADMIN_USER=admin"
    echo "ADMIN_PASSWORD=flowa-test-only"
    echo "FLOWA_MODE=federated"
    echo "FLOWA_ORIGIN=https://$1.flowa.test"
    echo "FLOWA_KEY_SECRET=$(openssl rand -hex 32)"
    echo "FLOWA_FEDERATION_ALLOW_PRIVATE=on"
    echo "FLOWA_TRUSTED_PROXIES=$subnet"
    echo "FLOWA_PREVIEW_FETCH=off"
    echo "SSL_CERT_FILE=/etc/flowa-test/ca.pem"
  } >"$work/$1.env"
  chmod 600 "$work/$1.env"
}
# app_up <instance>: the app as compose.yml runs it (read-only, no capabilities, 384 MB), with the run's authority.
app_up() {
  docker run -d --name "$p-$1-app" --network "$p" --env-file "$work/$1.env" -v "$p-$1-data:/data" \
    -v "$work/ca.pem:/etc/flowa-test/ca.pem:ro" --read-only --cap-drop ALL --security-opt no-new-privileges:true \
    --memory 384m --pids-limit 256 "$image:$version" >/dev/null
}
# tls_up <instance>: the proxy that answers for the instance's name on the network, published on a free port here.
tls_up() {
  docker run -d --name "$p-$1-tls" --network "$p" --network-alias "$1.flowa.test" -p 127.0.0.1::443 \
    -v "$work/fedproxy:/fedproxy:ro" -v "$work/tls-$1:/tls:ro" --entrypoint /fedproxy --user 65534:65534 \
    --read-only --cap-drop ALL --security-opt no-new-privileges:true --no-healthcheck \
    "$image:$version" -to "http://$p-$1-app:3169" >/dev/null || return 1
  port[$1]=$(docker port "$p-$1-tls" 443/tcp | head -1 | sed 's/.*://')
}
healthy() { api "$1" visitor GET /healthz && [ "$code" = 200 ] && [ "$(j .ok)" = true ]; }
dbpw=$(openssl rand -hex 16)
for n in "${names[@]}"; do
  db_up "$n" || { bad "the database of $n.flowa.test starts" "$(docker logs --tail 3 "$p-$n-db" 2>&1 | paste -sd' ')"; stop "each needs it"; }
done
# The image starts Postgres twice (once to set it up); only the second start listens on the network.
dbready() { docker exec "$p-$1-db" psql -h 127.0.0.1 -U flowa -d flowa -Atqc 'select 1' >/dev/null 2>&1; }
for n in "${names[@]}"; do
  waitfor 60 dbready "$n" || { bad "the database of $n.flowa.test starts" "$(docker logs --tail 3 "$p-$n-db" 2>&1 | paste -sd' ')"; stop "each needs it"; }
  { app_up "$n" && tls_up "$n"; } || { bad "the app and the proxy of $n.flowa.test start" "see the logs"; stop "each needs them"; }
done
started=$SECONDS
for n in "${names[@]}"; do
  if waitfor 60 healthy "$n"; then ok "$n.flowa.test answers over HTTPS through its proxy, healthy ($(since "$started"))"
  else bad "$n.flowa.test answers over HTTPS through its proxy, healthy" "$code $(cut -c1-200 <<<"$body") $(docker logs --tail 5 "$p-$n-app" 2>&1 | paste -sd' ' | cut -c1-300)"; stop "each needs both instances"; fi
done
said=$(docker logs "$p-one-app" 2>&1)
if grep -q 'FLOWA_FEDERATION_ALLOW_PRIVATE=on: FOR THE TWO-INSTANCE TEST ONLY' <<<"$said" && grep -q 'mode federated' <<<"$said" &&
  grep -q 'TLS roots: present, from SSL_CERT_FILE' <<<"$said"; then
  ok "the app starts in federated mode, says loudly that private addresses are allowed for this test, and that its roots come from SSL_CERT_FILE"
else bad "the app starts in federated mode and says loudly that private addresses are allowed for this test" "$(head -12 <<<"$said" | paste -sd' ' | cut -c1-400)"; fi

# within <seconds> <check> <condition...>: asks the condition until it holds; PASS with the time it took, or FAIL
# with the last answer. Every condition below is read on the instance where a person would look.
within() {
  local secs=$1 name=$2 t0=$SECONDS; shift 2
  if waitfor "$secs" "$@"; then ok "$name ($(since "$t0"))"; return 0; fi
  bad "$name" "not within $secs s; last answer $code $(cut -c1-300 <<<"$body")"
  overtime
  return 1
}
# overtime: past the whole run's deadline, a counted FAIL and the rest not run (the logs are written on the way out).
overtime() {
  [ "$SECONDS" -ge "$deadline" ] || return 0
  bad "the whole run finishes within $limit s" "$(since "$started_all") so far"
  stop "the run is past its deadline"
}
# need <what>: after a failed check that every later one needs, they are not run, rather than each waiting out its time.
need() { stop "each needs $1, which failed"; }
# all <check> <wanted> <got>: one PASS or FAIL for several statuses at once.
all() { if [ "$3" = "$2" ]; then ok "$1"; else bad "$1" "got $3, wanted $2; last answer $(cut -c1-300 <<<"$body")"; fi; }
other() { if [ "$1" = one ]; then echo two; else echo one; fi; }
queued() { health "$1" | jq -r '.federation.queued // "none"'; }
drained() { [ "$(queued "$1")" = 0 ]; }

# ---- the admins open the way, the members join and share ----
s=""
for n in "${names[@]}"; do
  api "$n" admin POST /api/signin '{"username":"admin","password":"flowa-test-only"}'; s+="$code "
  # The Federation switch (off by default, the pause) and follows across servers.
  api "$n" admin PUT /api/flags '{"flags":{"federation":true,"follows":true}}'; s+="$code "
  # In allowlist mode, the default, only the servers an admin allowed are asked or listened to.
  api "$n" admin PUT "/api/admin/federation/domains/$(other "$n").flowa.test" '{"policy":"allow"}'; s+="$code "
done
all "each admin turns federation and follows on and allows the other server" "200 200 200 200 200 200 " "$s"
s=""
for m in one:ana two:bob two:cyd; do
  api "${m%%:*}" "${m#*:}" POST /api/signup "{\"username\":\"${m#*:}\",\"password\":\"flowa-test-only\"}"; s+="$code "
  api "${m%%:*}" "${m#*:}" PUT /api/me '{"federates":true}'; s+="$code$(j '.federation.optedIn') "
done
all "ana joins one, bob and cyd join two, and each shares their profile beyond their server" "200 200true 200 200true 200 200true " "$s"

# ---- follows: a request, approved by hand ----
api one ana PUT /api/me '{"locked":true}'
found() { api "$1" "$2" POST /api/fed/lookup "{\"q\":\"$3\"}" && [ "$code" = 200 ] && [ "$(j .result.kind)" = remote ]; }
if found two bob ana@one.flowa.test && [ "$(j .result.handle)" = ana@one.flowa.test ] && [ "$(j .result.locked)" = true ]; then
  ana2=$(j .result.account)
  ok "bob finds ana by her handle from two (WebFinger, her actor and her outbox, signed, over HTTPS), and that she approves by hand"
else bad "bob finds ana by her handle from two" "$code $(cut -c1-300 <<<"$body")"; need "the lookup by handle"; fi
s=""
api two bob PUT "/api/fed/accounts/$ana2/follow"; s+="$code $(j .view.follow) "
found two cyd ana@one.flowa.test; api two cyd PUT "/api/fed/accounts/$ana2/follow"; s+="$code $(j .view.follow) "
all "bob and cyd follow her: requested until she answers" "200 requested 200 requested " "$s"
requests() { api one ana GET /api/fed/requests && ok200 '[.requests[].user.remote.handle] | sort | join(",") == "bob@two.flowa.test,cyd@two.flowa.test"'; }
within 30 "both requests reach ana on one, waiting for her, with their handles" requests
s="" ids=$(j '[.requests[] | .user.id] | join(" ")')
for rid in $ids; do api one ana PUT "/api/fed/requests/$rid"; s+="$code$(j .approved) "; done
all "ana approves both" "200true 200true " "$s"
follows() { api two "$1" GET "/api/fed/accounts/$ana2" && ok200 '.view.follow == "following"'; }
within 30 "her approval reaches two: bob follows her" follows bob || need "a follow across the two servers"
within 30 "and cyd follows her" follows cyd
api one ana GET /api/followers
all "ana's followers on one are bob and cyd of two" "200 bob@two.flowa.test,cyd@two.flowa.test" "$code $(j '[.followers[].user.remote.handle] | sort | join(",")')"
api one ana PUT /api/me '{"locked":false}'
bob1=0
if found one ana bob@two.flowa.test; then bob1=$(j .result.account); api one ana PUT "/api/fed/accounts/$bob1/follow"; fi
anafollows() { api one ana GET "/api/fed/accounts/$bob1" && ok200 '.view.follow == "following"'; }
within 30 "ana follows bob back, accepted at once by two" anafollows || need "a follow both ways"
api two bob GET /api/followers
all "bob's followers on two are ana of one" "200 ana@one.flowa.test" "$code $(j '[.followers[].user.remote.handle] | join(",")')"

# A delivery two's inbox must refuse, sent over the same HTTPS: unsigned, and signed in ana's name with a signature
# that is not hers (her real key id, which two holds by now, the right digest and date, and bytes that are no
# signature). Only a verifier that checks the signature itself refuses the second.
forged='{"@context":"https://www.w3.org/ns/activitystreams","id":"https://one.flowa.test/users/ana/activities/forged-'"$id"'","type":"Like","actor":"https://one.flowa.test/users/ana","object":"https://two.flowa.test/users/bob"}'
deliver() {   # deliver [Signature header value]: POSTs $forged to two's shared inbox; leaves the status in $code
  local date digest h=()
  date=$(LC_ALL=C TZ=GMT date '+%a, %d %b %Y %H:%M:%S GMT')
  digest="SHA-256=$(printf '%s' "$forged" | openssl dgst -sha256 -binary | base64)"
  [ -n "${1:-}" ] && h=(-H "Signature: $1")
  code=$(curl -sS -m 20 --noproxy '*' --cacert "$work/ca.pem" --resolve "two.flowa.test:${port[two]}:127.0.0.1" -o /dev/null -w '%{http_code}' \
    -H 'Content-Type: application/activity+json' -H "Date: $date" -H "Digest: $digest" "${h[@]}" --data-binary "$forged" \
    "https://two.flowa.test:${port[two]}/inbox" 2>/dev/null) || code=000
}
deliver; unsigned=$code
deliver "keyId=\"https://one.flowa.test/users/ana#main-key\",algorithm=\"rsa-sha256\",headers=\"(request-target) host date digest\",signature=\"$(openssl rand -base64 256 | tr -d '\n')\""
all "two's inbox refuses an unsigned delivery and one signed in ana's name with a signature that is not hers" "401 401" "$unsigned $code"

# ---- a Public post with a trick chip and a picture ----
png='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'
alt="A colour gradient, red across and green down"
api one ana POST /api/posts "{\"body\":\"Six directions, clean at last\",\"audience\":\"public\",\"tags\":[{\"prop\":\"nunchaku\",\"skill\":\"F5\"}],\"image\":\"data:image/png;base64,$png\",\"alt\":\"$alt\"}"
p1=$(j '.posts[0].id')
all "ana posts Public on one with a trick chip and a picture with alt text" "200 public $alt" "$code $(j '.posts[0].audience') $(j '.posts[0].alt')"
link1="https://one.flowa.test/users/ana/posts/$p1"
inhome() {   # inhome <instance> <who> <link of the original>: the post is in that member's Home; leaves it in $post
  api "$1" "$2" GET /api/home && [ "$code" = 200 ] && post=$(j ".posts[] | select(.remote.link == \"$3\")") && [ -n "$post" ]
}
within 30 "the post reaches bob's Home on two, with ana's handle and the link to the original" inhome two bob "$link1" || need "a post across the two servers"
p1two=$(jq -r .id <<<"$post")
author=$(j ".users[\"$(jq -r .userId <<<"$post")\"].remote.handle")
all "it is shown as ana@one.flowa.test's" "ana@one.flowa.test" "$author"
if grep -q 'Tricks: Six directions (Nunchaku)' <<<"$(jq -r .body <<<"$post")" && grep -q '#nunchaku' <<<"$(jq -r .body <<<"$post")"; then
  ok "its trick chip arrives as the text fallback with the prop's hashtag"
else bad "its trick chip arrives as the text fallback with the prop's hashtag" "$(jq -c .body <<<"$post")"; fi
cached() { inhome two bob "$link1" && [[ "$(jq -r '.remote.media[0].src // ""' <<<"$post")" == /api/fed/media/*.jpg ]]; }
within 30 "its picture is copied into two's own cache" cached
src=$(jq -r '.remote.media[0].src // ""' <<<"$post")
all "the picture keeps its alt text and links to the original on one" "$alt https://one.flowa.test/users/ana/posts/$p1/picture.png" \
  "$(jq -r '.remote.media[0].alt' <<<"$post") $(jq -r '.remote.media[0].link' <<<"$post")"
got=$(curl -sS -m 20 --noproxy '*' --cacert "$work/ca.pem" --resolve "two.flowa.test:${port[two]}:127.0.0.1" -b "$work/jar-two-bob" \
  -o "$work/picture" -w '%{http_code} %{content_type}' "https://two.flowa.test:${port[two]}$src" 2>&1)
if [ "$got" = "200 image/jpeg" ] && [ "$(head -c 3 "$work/picture" | od -An -tx1 | tr -d ' ')" = ffd8ff ]; then
  ok "two serves the picture itself, a JPEG of $(wc -c <"$work/picture") bytes from its cache"
else bad "two serves the picture itself from its cache" "$got"; fi
api two bob GET /api/posts
all "the post is in no Community timeline on two" "200 0" "$code $(j '[.posts[] | select(.remote != null)] | length')"

# ---- likes, both ways, and a reply with an edit and a deletion ----
likes() { api one ana GET "/api/posts/$p1" && ok200 ".posts[0].likes == $1"; }
api two bob POST "/api/posts/$p1two/like"
within 30 "bob likes it on two, and one counts the like" likes 1
api two bob POST "/api/posts/$p1two/like"
within 30 "bob takes the like back, and one counts none" likes 0
api two bob POST "/api/posts/$p1two/like"
within 30 "bob likes it again, and one counts it again" likes 1
api two bob POST "/api/posts/$p1two/comments" '{"body":"Clean indeed, from two"}'
c1=$(j '.comment.id')
replied() {   # replied <body>: the one remote answer under ana's post on one has this text
  api one ana GET "/api/posts/$p1/comments" && ok200 '[.comments[] | select(.remote.link == "'"https://two.flowa.test/users/bob/comments/$c1"'")] | length == 1' &&
    [ "$(j '.comments[0].body')" = "$1" ]
}
within 30 "bob replies on two, and the reply is under ana's post on one" replied "Clean indeed, from two"
told() { api one ana GET /api/notifications && ok200 "[.items[] | select(.kind == \"comment\" and .commentId == $c1)] | length == 1"; }
within 30 "ana is told of the reply on one" told
api two bob PUT "/api/comments/$c1" '{"body":"Clean indeed, from two (edited)"}'
all "bob edits the reply within his 15 minutes" "200 true" "$code $(j .comment.edited)"
# A comment sends at most one Update a minute (federation.md 6.4), so the edit can take up to a minute to leave.
editarrived() { replied "Clean indeed, from two (edited)" && [ "$(j '.comments[0].edited')" = true ]; }
within 100 "the edit reaches one, marked edited (a comment sends at most one Update a minute)" editarrived
api two bob DELETE "/api/comments/$c1"
gone() { api one ana GET "/api/posts/$p1/comments" && ok200 '(.comments | type) == "array" and (.comments | length) == 0'; }
within 30 "bob deletes the reply, and it is gone from ana's post on one" gone

# ---- the other way: bob posts, ana comments and likes ----
api two bob POST /api/posts '{"body":"Hello from two","audience":"public"}'
p2=$(j '.posts[0].id')
within 30 "bob's Public post reaches ana's Home on one" inhome one ana "https://two.flowa.test/users/bob/posts/$p2"
p2one=$(jq -r .id <<<"$post")
api one ana POST "/api/posts/$p2one/comments" '{"body":"Hello back from one"}'
c2=$(j '.comment.id')
api one ana POST "/api/posts/$p2one/like"
answered() { api two bob GET "/api/posts/$p2/comments" && ok200 '.comments[0].body == "Hello back from one" and .comments[0].remote.link == "'"https://one.flowa.test/users/ana/comments/$c2"'"'; }
within 30 "ana's comment from one is under bob's post on two" answered
liked2() { api two bob GET "/api/posts/$p2" && ok200 '.posts[0].likes == 1'; }
within 30 "and her like is counted there" liked2

# ---- what only two real processes show ----
for n in "${names[@]}"; do
  within 30 "$n's delivery queue drains: 0 queued on its /healthz" drained "$n"
done
for n in "${names[@]}"; do
  api "$n" admin GET /api/admin/federation
  # Refused: only the two deliveries made to fail above, at two.
  want='{}'; [ "$n" = two ] && want='{"not signed":1,"the signature is refused":1}'
  acc=$(j '.inbox.answers.accepted // 0') ref=$(jq -cS '.inbox.refused' <<<"$body") sent=$(j '.deliveries.counts.sent // 0')
  if [ "$code" = 200 ] && [ "$acc" -ge 4 ] && [ "$ref" = "$want" ] && [ "$(j '.inbox.failed')" = 0 ] && [ "$(j '.deliveries.failed')" = 0 ] && [ "$sent" -ge 4 ]; then
    ok "$n's Federation tab: $acc deliveries in, each signature verified, none refused but the test's own; $sent sent, none failed"
  else bad "$n's Federation tab: every delivery in verified, none refused but the test's own, none failed" "$code accepted $acc, refused $ref, sent $sent, failed $(j .deliveries.failed)"; fi
done
for n in "${names[@]}"; do
  shared=$(docker logs "$p-$n-tls" 2>&1 | grep -c ' POST /inbox 202 ')
  own=$(docker logs "$p-$n-tls" 2>&1 | grep -cE ' POST /users/[a-z]+/inbox 202 ')
  if [ "$shared" -ge 1 ]; then ok "deliveries reach $n's shared inbox over HTTPS ($shared to /inbox, $own to a member's own inbox, each answered 202)"
  else bad "deliveries reach $n's shared inbox over HTTPS" "$shared to /inbox, $own to a member's own inbox"; fi
done
# The Federation tab's counts of what was not sent: activities cut at the address cap, deletions not taken back,
# comments not sent, forwards skipped and activities that could not be made. A clean run leaves each at 0.
undropped() {   # undropped <instance>: says the counts in $dropped; true when every one is 0 (left out when 0)
  dropped=""
  api "$1" admin GET /api/admin/federation && ok200 '(.deliveries | type) == "object" and (.deliveries.queueMax | type) == "number"' &&
    dropped=$(j '.deliveries | "cut \(.cut // 0), not taken back \(.notTakenBack // 0), comments not sent \(.commentsNotSent // 0), forwards skipped \(.forwardsSkipped // 0), not made \(.outNotMade // 0)"') &&
    [ "$(j '.deliveries | [.cut, .notTakenBack, .commentsNotSent, .forwardsSkipped, .outNotMade] | map(. // 0) | add')" = 0 ]
}
for n in "${names[@]}"; do
  if undropped "$n"; then ok "$n's Federation tab counts nothing left unsent ($dropped)"
  else bad "$n's Federation tab counts nothing left unsent" "$code ${dropped:-$(cut -c1-300 <<<"$body")}"; fi
done

# ---- two away, one restarted, nothing lost ----
t0=$SECONDS
docker stop -t 5 "$p-two-tls" "$p-two-app" >/dev/null
api one ana POST /api/posts '{"body":"Posted while two was away","audience":"public"}'
p3=$(j '.posts[0].id')
waiting() { [ "$(health one | jq -r '"\(.federation.queued) \(.federation.failingHosts)"')" = "1 1" ]; }
within 30 "with two stopped, ana's next post waits in one's queue, its host failing" waiting
retry=$(sql one "SELECT attempts || ' attempt, next at ' || to_char(next_at, 'HH24:MI:SS') || ' (in ' || round(extract(epoch FROM next_at - now())) || ' s): ' || last_error FROM ap_deliveries WHERE state = 'queued'")
if [[ "$retry" == "1 attempt, next at "* ]]; then ok "the row records its failed attempt and when it is tried again: $retry"
else bad "the row records its failed attempt and when it is tried again" "$retry"; fi
docker restart -t 10 "$p-one-app" >/dev/null
appup() { [ -n "$(health one)" ]; }
if waitfor 60 appup && [ "$(queued one)" = 1 ]; then ok "one restarts with the delivery still waiting: nothing lost"
else bad "one restarts with the delivery still waiting: nothing lost" "$(health one)"; fi
docker start "$p-two-app" "$p-two-tls" >/dev/null
port[two]=$(docker port "$p-two-tls" 443/tcp | head -1 | sed 's/.*://')
within 60 "two comes back" healthy two || need "two running again"
# The first retry is 1 minute after the failure, give or take 20% (federation.md 8.3).
within 120 "one tries again on its schedule and the post reaches bob's Home on two" inhome two bob "https://one.flowa.test/users/ana/posts/$p3"
within 30 "one's queue is empty again" drained one
echo "two was away and back, with one restarted, in $(since "$t0")"

# ---- unfollow, block, leaving, and an account deleted ----
api two bob DELETE "/api/fed/accounts/$ana2/follow"
followers() { api one ana GET /api/followers && ok200 '[.followers[].user.remote.handle] | join(",") == "'"$1"'"'; }
within 30 "bob unfollows ana on two, and only cyd follows her on one" followers cyd@two.flowa.test
api one ana PUT "/api/fed/accounts/$bob1/block"
unfollowed() { api two bob GET /api/followers && ok200 '.total == 0' && api two bob GET "/api/fed/accounts/$ana2" && ok200 '(.view | type) == "object" and (.view.followsYou // false) == false'; }
within 30 "ana blocks bob on one: on two her follow of him is gone" unfollowed
api one ana GET /api/home
all "and bob's posts are hidden from ana's Home on one" "200 0" "$code $(j '[.posts[] | select(.remote.link // "" | startswith("https://two.flowa.test/users/bob/"))] | length')"
# Leaving, with one stopped first so that bob's removal waits in two's queue: the card says it is being sent, sharing
# again is refused with that sentence, the address shows "Profile no longer shared", and an unlike stays on two. Once one
# is back and two's queue is empty, the removal has reached one and bob may share again at once.
docker stop -t 5 "$p-one-tls" "$p-one-app" >/dev/null
api two bob POST /api/me/federation/leave '{"confirm":true}'
all "with one stopped, bob asks to be removed from other servers on two, and one server is to be told" "200 false" "$code $(j .federation.optedIn)"
api two bob GET /api/me
leaving=$(j '.federation.leaving // ""')
if [ "$code" = 200 ] && [ -n "$leaving" ]; then ok "bob's card on two says the removal is being sent: $leaving"
else bad "bob's card on two says the removal is being sent" "$code $(jq -c .federation <<<"$body")"; fi
api two bob PUT /api/me '{"federates":true}'
all "and sharing again is refused with that sentence while it is" "409 $leaving" "$code $(j .error)"
: >"$work/page"
got=$(curl -sS -m 20 --noproxy '*' --cacert "$work/ca.pem" --resolve "two.flowa.test:${port[two]}:127.0.0.1" -H 'Accept: text/html' \
  -o "$work/page" -w '%{http_code}' "https://two.flowa.test:${port[two]}/users/bob" 2>/dev/null) || got=000
if [ "$got" = 410 ] && grep -q 'Profile no longer shared' "$work/page"; then ok "bob's address on two shows \"Profile no longer shared\" (410)"
else bad "bob's address on two shows \"Profile no longer shared\"" "$got $(head -c 300 "$work/page")"; fi
# Leaving cleared the ids of bob's likes, so his like of ana's post is a like of this server now: an unlike removes it
# here and enqueues nothing (his removal takes it back on one): two's queue holds the removal alone.
api two bob POST "/api/posts/$p1two/like"
liked=$code$(j .liked)
api two cyd GET "/api/posts/$p1two"
all "bob's unlike after leaving removes his like on two and sends nothing" "200false 200 0 1" "$liked $code $(j '.posts[0].likes') $(queued two)"
docker start "$p-one-app" "$p-one-tls" >/dev/null
port[one]=$(docker port "$p-one-tls" 443/tcp | head -1 | sed 's/.*://')
within 60 "one comes back" healthy one || need "one running again"
# The first retry is 1 minute after the failure, give or take 20% (federation.md 8.3).
within 120 "two's queue sends the removal once one is back, and is empty" drained two
bobgone() { api one ana GET "/api/fed/accounts/$bob1" && ok200 '.account.state == "gone"'; }
within 30 "on one bob's account is gone" bobgone
within 30 "and his like of ana's post is taken away there" likes 0
api two bob GET /api/me
all "with the removal sent, bob's card no longer says it is being sent" "200 none" "$code $(j '.federation.leaving // "none"')"
api two bob PUT /api/me '{"federates":true}'
all "and he may share his profile again at once" "200 true" "$code $(j .federation.optedIn)"
api one admin GET /api/admin/users
anaid=$(j '.users[] | select(.username == "ana") | .id')
api one admin DELETE "/api/admin/users/$anaid"
all "the admin of one deletes ana's account, and the servers that know her are told" "200 true" "$code $(j .ok)"
anagone() { api two cyd GET /api/following && ok200 '.total == 0'; }
within 30 "on two cyd no longer follows her" anagone
api two cyd GET /api/home
all "and her posts are gone from cyd's Home there" "200 0" "$code $(j '[.posts[] | select(.remote.link // "" | startswith("https://one.flowa.test/users/ana/"))] | length')"
api two cyd GET "/api/fed/accounts/$ana2"
all "and her account shows as gone on two" "200 gone" "$code $(j .account.state)"
for n in "${names[@]}"; do within 30 "$n's queue is empty at the end, nothing failed" drained "$n"; done
for n in "${names[@]}"; do
  if undropped "$n"; then ok "$n's Federation tab still counts nothing left unsent since its last start ($dropped)"
  else bad "$n's Federation tab still counts nothing left unsent since its last start" "$code ${dropped:-$(cut -c1-300 <<<"$body")}"; fi
done

# ---- memory, and what is kept ----
# docker stats shows the container's memory as the kernel counts it for the cgroup, without the page cache it can
# give back (inactive files): the process's resident memory and the files it is actively using.
# It prints MiB (2^20 bytes); the budget is 300 MB (10^6 bytes), so the figure is turned into MB to be compared.
for n in "${names[@]}"; do
  mem=$(docker stats --no-stream --format '{{.MemUsage}}' "$p-$n-app" | cut -d/ -f1 | tr -d ' ')
  mib=$(awk -v m="$mem" 'BEGIN { v = m + 0; if (m ~ /GiB/) v *= 1024; else if (m ~ /KiB/) v /= 1024; else if (m ~ /[0-9]B$/) v /= 1048576; printf "%.1f", v }')
  mb=$(awk -v v="$mib" 'BEGIN { printf "%.1f", v * 1.048576 }')
  if awk -v v="$mb" 'BEGIN { exit !(v > 0 && v < 300) }'; then ok "$n's app uses $mib MiB ($mb MB) after the whole run (docker stats), under 300 MB"
  else bad "$n's app uses under 300 MB after the whole run (docker stats)" "$mem"; fi
done
collect
# The secrets of the run: both key secrets, the databases' password, and each line of the certificate authority's key.
leaked=""
for n in "${names[@]}"; do
  grep -rqF "$(sed -n 's/^FLOWA_KEY_SECRET=//p' "$work/$n.env")" "$work/logs" && leaked+="$n's key secret, "
done
grep -rqF "$dbpw" "$work/logs" && leaked+="the database password, "
while IFS= read -r line; do
  [[ "$line" == -----* ]] && continue
  grep -rqF "$line" "$work/logs" && { leaked+="the CA key, "; break; }
done <"$work/ca-key.pem"
if [ -z "$leaked" ]; then ok "the logs and queue state kept for this run hold no secret of the run (key secrets, database password, CA key)"
else bad "the logs and queue state kept for this run hold no secret of the run" "found ${leaked%, }"; fi
echo "wall time $(since "$started_all")"
# A whole run makes every planned check: if the count differs, the plan at the top is out of date.
if [ "$((pass + fail + skip))" -ne "$planned" ]; then
  echo "FAIL the run made $((pass + fail + skip)) checks, the plan at the top says $planned"; fail=$((fail + 1))
fi
finish
