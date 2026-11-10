#!/usr/bin/env bash
# A release seen from both ends on one machine: build-image.sh pushes images to a registry started
# here, as the release_tag workflow pushes them to ghcr.io, and deploy.sh pulls and starts them, as the
# Pi does.
#   test/release.sh            the image checks, then the deploy checks
#   test/release.sh --image    the image checks only, for a machine that must not be deployed to
# The image checks: both architectures under one name, the digest printed, the same commit giving the
# same bytes (also from brotli copies another brotli made), the version compiled in, a brotli copy of
# each page file made for it, nothing pushed without --push.
# The deploy checks: a release is pulled and started and says its version; it sends a script under its
# hashed name with brotli and leaves its page's files in data/ for the release after it; the next one
# replaces it and leaves the database running; --rollback goes back; a release that does not exist, or an
# image that says it is another version, is refused and what runs is left alone; --build builds from the
# checkout.
# The deploy checks run deploy.sh for real, with the names and the port of the real service, so they
# refuse to start where Flowa runs: this is for CI and a spare machine, never the Pi that serves. Nothing
# is read from .env. The registry, the builder, the containers, the volume and the images are removed at
# the end, and data/ too if this script made it.
# REGISTRY_IMAGE and BUILDKIT_IMAGE name other sources for the two helper images (a mirror).
# Prints PASS, FAIL or SKIP per check, TIME per check for the slowest table, and a count at the end.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
export PATH="$HOME/.local/go/bin:$PATH"

pass=0 fail=0 skip=0 last=$(date +%s.%N)
took() { local now; now=$(date +%s.%N); echo "TIME $(awk -v a="$last" -v b="$now" 'BEGIN { printf "%.1f", b - a }') $1"; last=$now; }
ok() { echo "PASS $1"; pass=$((pass + 1)); took "$1"; }
bad() { echo "FAIL $1${2:+: $2}"; fail=$((fail + 1)); took "$1"; }
is() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "got '$2', wanted '$3'"; fi; }   # is <name> <got> <wanted>
finish() { echo "$pass passed, $fail failed, $skip skipped"; [ "$fail" -eq 0 ]; }

only_image=false
case "${1:-}" in "") ;; --image) only_image=true ;; *) echo "usage: test/release.sh [--image]" >&2; exit 2 ;; esac
docker info >/dev/null 2>&1 || { echo "the Docker daemon is not running" >&2; exit 2; }
docker buildx version >/dev/null 2>&1 || { echo "docker buildx is not installed" >&2; exit 2; }
if curl -s -o /dev/null -m 2 http://127.0.0.1:5000/v2/; then echo "port 5000 is in use on this machine; the registry for the checks cannot start" >&2; exit 2; fi
if ! $only_image; then
  taken=$(docker ps -a --format '{{.Names}}' | grep -E '^(nunchaku|nunchaku-db)$' | paste -sd' ')
  [ -z "$taken" ] || { echo "containers named $taken exist: this machine runs Flowa, and the deploy checks would replace them (--image runs the rest)" >&2; exit 2; }
  ! docker image inspect nunchaku:latest >/dev/null 2>&1 || { echo "an image nunchaku:latest exists: this machine deploys Flowa (--image runs the rest)" >&2; exit 2; }
  if curl -s -o /dev/null -m 2 http://127.0.0.1:3169/; then echo "port 3169 is in use on this machine (--image runs the rest)" >&2; exit 2; fi
fi

repo=localhost:5000/flowtopia
work=$(mktemp -d)
made_data=false
export BUILDX_BUILDER=flowa-release-check
# deploy.sh as a new machine would run it: one password, nothing from the caller's environment or .env,
# and names of its own for the volume and the networks.
deploy() {
  (
    unset FLOWA_HOSTS FLOWA_TRUSTED_PROXIES FLOWA_HOME_URL FLOWA_COUNTS_TOKEN COMPOSE_PROFILES ADMIN_USER ADMIN_PASSWORD VERSION
    export POSTGRES_PASSWORD=flowa-test-only COMPOSE_PROJECT_NAME=flowa-check COMPOSE_ENV_FILES="$work/.env" FLOWA_IMAGE="$repo"
    ./deploy.sh "$@"
  ) >"$work/deploy.log" 2>&1
}
cleanup() {
  if ! $only_image; then
    POSTGRES_PASSWORD=x COMPOSE_PROJECT_NAME=flowa-check COMPOSE_ENV_FILES="$work/.env" docker compose down --volumes --remove-orphans --timeout 5 >/dev/null 2>&1
    docker image rm -f nunchaku:latest nunchaku:previous >/dev/null 2>&1
    rm -f nunchaku
    $made_data && rm -rf data
  fi
  docker image rm -f "$repo:v9.9.6" "$repo:v9.9.8" "$repo:v9.9.9" >/dev/null 2>&1
  docker buildx rm -f "$BUILDX_BUILDER" >/dev/null 2>&1
  docker rm -f flowa-registry-check >/dev/null 2>&1
  rm -f brotli/not-a-copy   # should a build have failed before brotli.js removed it
  rm -rf "$work"
}
trap cleanup EXIT
: > "$work/.env"

docker run -d --name flowa-registry-check -p 127.0.0.1:5000:5000 "${REGISTRY_IMAGE:-registry:3}" >/dev/null || { echo "the registry did not start" >&2; exit 1; }
for _ in $(seq 1 30); do curl -s -o /dev/null -m 2 http://127.0.0.1:5000/v2/ && break; sleep 0.5; done
# A builder that can push two architectures, on the host's network so that it reaches the registry.
docker buildx create --name "$BUILDX_BUILDER" --driver docker-container --driver-opt network=host \
  ${BUILDKIT_IMAGE:+--driver-opt "image=$BUILDKIT_IMAGE"} >/dev/null || { echo "the builder was not created" >&2; exit 1; }

# ---- the image ----
if ! git diff --quiet HEAD; then
  echo "this checkout has changes that are not committed; build-image.sh --push refuses that, so commit first" >&2; exit 2
fi
platforms() { docker buildx imagetools inspect "$1" --format '{{range .Manifest.Manifests}}{{.Platform.OS}}/{{.Platform.Architecture}} {{end}}' 2>/dev/null | tr ' ' '\n' | grep -v '^$' | sort | paste -sd' '; }

first=$(./build-image.sh "$repo" v9.9.8 --push 2>"$work/build.log")
if [[ "$first" =~ ^$repo:v9\.9\.8@sha256:[0-9a-f]{64}$ ]]; then ok "build-image.sh --push prints the image with its digest"
else
  bad "build-image.sh --push prints the image with its digest" "it printed '$first'; $(tail -3 "$work/build.log" | paste -sd' ' | cut -c1-300)"
  finish; exit
fi
is "the image is for the Pi and for an ordinary server, and nothing else" "$(platforms "$repo:v9.9.8")" "linux/amd64 linux/arm64"
docker pull -q "$repo:v9.9.8" >/dev/null 2>&1
is "the binary in the image says the version it was built as" "$(docker run --rm --network none "$repo:v9.9.8" -version 2>&1)" "v9.9.8"
is "the image names the commit it was built from" "$(docker image inspect "$repo:v9.9.8" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" "$(git rev-parse HEAD)"
is "the image's time is the commit's, not the build's" "$(date -u -d "$(docker image inspect "$repo:v9.9.8" --format '{{.Created}}')" +%s)" "$(git log -1 --format=%ct)"
is "the image runs as user 1000" "$(docker image inspect "$repo:v9.9.8" --format '{{.Config.User}}')" "1000:1000"
# build-image.sh compresses the page's files before it compiles (brotli.js): each copy unpacks to its file.
if said=$(node brotli.js --check 2>&1); then ok "the build made a brotli copy of every page file that gains by one"
else bad "the build made a brotli copy of every page file that gains by one" "$(tail -3 <<<"$said" | paste -sd' ' | cut -c1-300)"; fi
# The same commit again, with nothing kept from the first build: the same bytes, so the same digest.
# The brotli copies are left where they are, under a stamp that says another brotli made them and next
# to a file that is no copy. brotli.js must make every copy again and remove the file: the binary embeds
# the whole folder, and would otherwise be other bytes than a clean checkout builds.
docker buildx prune -af >/dev/null 2>&1
go clean -cache >/dev/null 2>&1
echo "brotli 0.0.0, made elsewhere" > brotli.stamp
echo "not a copy" > brotli/not-a-copy
is "the same commit builds the same bytes" "$(./build-image.sh "$repo" v9.9.8 --push 2>/dev/null)" "$first"
if [ ! -e brotli/not-a-copy ] && said=$(node brotli.js --check 2>&1); then ok "copies another brotli made, and a file that is no copy, do not get into the build"
else bad "copies another brotli made, and a file that is no copy, do not get into the build" "$(tail -3 <<<"${said:-brotli/not-a-copy is still there}" | paste -sd' ' | cut -c1-300)"; fi
second=$(./build-image.sh "$repo" v9.9.9 --push 2>/dev/null)
if [[ "$second" =~ ^$repo:v9\.9\.9@sha256:[0-9a-f]{64}$ ]] && [ "${second##*@}" != "${first##*@}" ]; then ok "another version is another image"
else bad "another version is another image" "$second"; fi
./build-image.sh "$repo" v9.9.7 >/dev/null 2>&1; built=$?
if [ "$built" -eq 0 ] && ! docker buildx imagetools inspect "$repo:v9.9.7" >/dev/null 2>&1; then ok "without --push both architectures build and nothing is pushed"
else bad "without --push both architectures build and nothing is pushed" "exit $built"; fi
./build-image.sh ghcr.io/TuanSSM/flowtopia v9.9.7 >/dev/null 2>"$work/said"; code=$?
if [ "$code" -eq 2 ] && grep -q 'lower case' "$work/said"; then ok "a name a registry would refuse is refused before anything is built"
else bad "a name a registry would refuse is refused before anything is built" "exit $code: $(cat "$work/said")"; fi

$only_image && { finish; exit; }

# ---- the deploy ----
# The app is user 1000, which need not be ours: where this script makes data/, anyone may write there.
[ -d data ] || { mkdir -p data/backups && chmod 777 data data/backups && made_data=true; }
says() { docker exec nunchaku /nunchaku -healthcheck 2>/dev/null | sed -n 's/.*"version":"\([^"]*\)".*/\1/p'; }
log() { tail -4 "$work/deploy.log" | paste -sd' ' | cut -c1-400; }

if deploy v9.9.8; then ok "deploy.sh pulls a release and starts it"
else
  bad "deploy.sh pulls a release and starts it" "$(log)"
  finish; exit
fi
is "what runs says it is that release" "$(says)" "v9.9.8"
# What a browser gets from it: the page names its scripts by the hash of their bytes, and such a name
# is sent with brotli, to be kept for good. The copy is unpacked here and compared with the file.
script=$(curl -fsS -m 5 http://127.0.0.1:3169/ | grep -oE 'src="app\.[0-9a-f]{16}\.js"' | cut -d'"' -f2)
head=$(curl -fsS -m 5 -o "$work/app.br" -D - -H 'Accept-Encoding: gzip, br' "http://127.0.0.1:3169/${script:-app.js}" | tr -d '\r')
if [ -n "$script" ] && grep -qix 'content-encoding: br' <<<"$head" && grep -qix 'cache-control: public, max-age=31536000, immutable' <<<"$head" &&
  node -e 'const fs = require("fs"); process.exit(require("zlib").brotliDecompressSync(fs.readFileSync(process.argv[1])).equals(fs.readFileSync("web/app.js")) ? 0 : 1)' "$work/app.br"; then
  ok "the image sends a script under its hashed name with brotli, to be kept for good"
else bad "the image sends a script under its hashed name with brotli, to be kept for good" "${script:-the page names no app.<hash>.js}: $(paste -sd' ' <<<"$head" | cut -c1-300)"; fi
# The read-only container can write the one file the release after this one needs: the files this
# page names, to go on answering them for an hour.
if [ -s data/static-kept.json.gz ]; then ok "the release leaves its page's files in data/ for the one after it"
else bad "the release leaves its page's files in data/ for the one after it" "$(docker logs nunchaku 2>&1 | grep 'static files' | paste -sd' ' | cut -c1-300)"; fi
if grep -Eq '^deployed v9\.9\.8 in [0-9]+ s$' "$work/deploy.log"; then ok "deploy.sh says what it deployed and how long it took ($(grep '^deployed' "$work/deploy.log"))"
else bad "deploy.sh says what it deployed and how long it took" "$(log)"; fi
db=$(docker inspect nunchaku-db --format '{{.Id}} {{.State.StartedAt}}')

if deploy v9.9.9; then is "the next release replaces it" "$(says)" "v9.9.9"; else bad "the next release replaces it" "$(log)"; fi
is "the database was left running" "$(docker inspect nunchaku-db --format '{{.Id}} {{.State.StartedAt}}')" "$db"
if deploy --rollback; then is "--rollback goes back to the release before" "$(says)" "v9.9.8"; else bad "--rollback goes back to the release before" "$(log)"; fi

app=$(docker inspect nunchaku --format '{{.Id}}')
if deploy v9.9.5; then bad "a release with no image is refused" "deploy.sh went on"
elif grep -q "no image $repo:v9.9.5 to pull" "$work/deploy.log"; then ok "a release with no image is refused"
else bad "a release with no image is refused" "$(log)"; fi
# An image under one release's name whose binary says it is another: the registry's word is not taken.
docker tag "$repo:v9.9.9" "$repo:v9.9.6" && docker push -q "$repo:v9.9.6" >/dev/null 2>&1
if deploy v9.9.6; then bad "an image that says it is another version is refused" "deploy.sh went on"
elif grep -q "says it is 'v9.9.9', not v9.9.6" "$work/deploy.log"; then ok "an image that says it is another version is refused"
else bad "an image that says it is another version is refused" "$(log)"; fi
if deploy; then bad "without a version, a checkout that knows no such release is told so" "deploy.sh went on"
elif grep -Eq 'has no release tag|no image .* to pull' "$work/deploy.log"; then ok "without a version, a checkout that knows no such release is told so"
else bad "without a version, a checkout that knows no such release is told so" "$(log)"; fi
deploy --nonsense; code=$?
is "an argument deploy.sh does not know is a usage error" "$code" "2"
is "after four refusals the same container is still running" "$(docker inspect nunchaku --format '{{.Id}}') $(says)" "$app v9.9.8"

if command -v node >/dev/null; then
  if deploy --build; then is "--build builds the checkout and says so" "$(says)" "main.$(git rev-parse --short HEAD)"
  else bad "--build builds the checkout and says so" "$(log)"; fi
  if deploy --rollback; then is "and --rollback goes back from a build to the release" "$(says)" "v9.9.8"; else bad "and --rollback goes back from a build to the release" "$(log)"; fi
else
  echo "SKIP --build builds the checkout and says so: node is not installed, and --build bakes the clips with it"; skip=$((skip + 1))
fi

finish
