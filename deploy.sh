#!/usr/bin/env bash
# Put a version of Flowa on this machine: get its image, start it, wait until GET /healthz says it works.
#   ./deploy.sh              pull the image of the newest release this checkout knows and start it
#   ./deploy.sh v1.3.0       pull the image of that release (v1.3.0@sha256:... holds it to one digest)
#   ./deploy.sh --build      build the image here from this checkout instead, as the Pi does for staging
#   ./deploy.sh --rollback   go back to the image that ran before the last deploy
# A release's image is built by the release_tag workflow (build-image.sh) and kept at ghcr.io; pulling it
# takes seconds where building takes minutes, and what runs is what the release's checks ran on. Set
# FLOWA_IMAGE to pull from another place. Building needs Go and Node here; pulling needs only Docker.
# If the new container is not healthy within 90 seconds, the script goes back by itself.
# The image that was running is kept as nunchaku:previous. Going back is safe while migrations only
# add tables and columns, as they do now; a migration that removes or rewrites data must say in its
# pull request that going back past it is not possible.
set -euo pipefail
cd "$(dirname "$0")"
export PATH="$HOME/.local/go/bin:$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
began=$SECONDS
image=${FLOWA_IMAGE:-ghcr.io/tuanssm/flowtopia}

# Wait for the app's own probe, run inside the new container: it asks GET /healthz, which answers 200 once
# the server is up, the database is reachable and its schema is what the build needs, and it prints the
# answer (schema version, TLS roots, version). Asking it directly means the wait is as long as the start;
# Docker's own health state only changes at its next check, half a minute later. An image from before the
# probe printed anything (after --rollback) leaves HEALTH empty.
HEALTH=""
healthy() {
  for _ in $(seq 1 90); do
    if HEALTH=$(docker exec nunchaku /nunchaku -healthcheck 2>/dev/null); then return 0; fi
    sleep 1
  done
  return 1
}
rollback() {
  docker image inspect nunchaku:previous >/dev/null 2>&1 || { echo "no previous image to go back to" >&2; exit 1; }
  docker tag nunchaku:previous nunchaku:latest
  docker compose up -d --no-build --force-recreate nunchaku
  healthy && echo "rolled back: nunchaku is healthy on the previous image${HEALTH:+: $HEALTH}" || { echo "the previous image is not healthy either" >&2; exit 1; }
}
# The image that is running now, to go back to. Called once the new image is in hand, so a pull or a
# build that fails changes nothing.
keep_previous() {
  if docker image inspect nunchaku:latest >/dev/null 2>&1; then docker tag nunchaku:latest nunchaku:previous; fi
}

# The app keeps its daily backups in data/ and runs as user 1000. Docker would make a missing folder as
# root, and the app would stop at start because it cannot write there.
data_folder() {
  mkdir -p data/backups
  [ "$(stat -c %u data/backups)" = 1000 ] && return
  [ $(( 0$(stat -c %a data/backups) & 2 )) -ne 0 ] && return   # anyone may write
  chown -R 1000:1000 data 2>/dev/null || {
    echo "data/ must belong to user 1000, which the app runs as: sudo chown -R 1000:1000 data" >&2
    exit 1
  }
}

case "${1:-}" in
  --rollback) rollback; exit 0 ;;
  --build)
    # The checkout's commit is compiled in as the version the admin dashboard shows.
    version="main.$(git rev-parse --short HEAD)"
    # Rebuild the guided-tour bundle (React Joyride) when its dependencies are installed.
    [ -d tour/node_modules ] && (cd tour && npm run --silent build)
    # Rebuild the 3D figure bundle (three.js) when its dependencies are installed.
    [ -d figure/node_modules ] && (cd figure && npm run --silent build)
    # Work out the demo clips (physics simulation and checks happen here, not in the browser).
    node sim/bake.js
    go vet ./...
    go test -count=1 ./...   # the database tests skip without TEST_DATABASE_URL; the rest must pass
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.buildVersion=$version" -o nunchaku .
    keep_previous
    docker compose build nunchaku
    ;;
  ""|v[0-9]*)
    version=${1:-}
    if [ -z "$version" ]; then
      # The newest release this checkout has: the last vX.Y.Z tag at or before its commit.
      version=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null) || {
        echo "this checkout has no release tag. Fetch them (git fetch --tags), name a release (./deploy.sh v1.3.0), or build here (./deploy.sh --build)." >&2
        exit 1
      }
      ahead=$(git rev-list --count "$version..HEAD")
      [ "$ahead" -eq 0 ] || echo "note: this checkout is $ahead commits past $version. The image is $version; compose.yml and the settings are the checkout's. For the checkout itself: ./deploy.sh --build"
    fi
    echo "pulling $image:$version"
    docker pull -q "$image:$version" >/dev/null || {
      echo "no image $image:$version to pull. Releases before the image was built by the release workflow have none (./deploy.sh --build builds here); a private package needs docker login ghcr.io first." >&2
      exit 1
    }
    # What was pulled must be the release it says: the version is compiled into the binary.
    says=$(docker run --rm --network none "$image:$version" -version 2>/dev/null || true)
    [ "$says" = "${version%%@*}" ] || { echo "the image $image:$version says it is '${says:-nothing}', not ${version%%@*}; not starting it" >&2; exit 1; }
    keep_previous
    docker tag "$image:$version" nunchaku:latest
    ;;
  *) echo "usage: ./deploy.sh [vX.Y.Z | --build | --rollback]" >&2; exit 2 ;;
esac

data_folder
# Start what changed: the app, whose image is new, and any other service whose settings in compose.yml
# or .env moved. The database is left running when nothing of its own did. Should docker
# compose have left the app's old container in place, the app alone is made again from the new image.
docker compose up -d --no-build
running() { docker inspect nunchaku --format '{{.Image}}' 2>/dev/null; }
wanted=$(docker image inspect nunchaku:latest --format '{{.Id}}')
[ "$(running)" = "$wanted" ] || docker compose up -d --no-build --no-deps --force-recreate nunchaku
if ! healthy; then
  echo "the new container is not healthy; going back to the previous image" >&2
  docker logs --tail 30 nunchaku >&2 || true
  rollback
  exit 1
fi
# The container that answers must be the image just made or pulled.
[ "$(running)" = "$wanted" ] || { echo "the running container is not the new image" >&2; exit 1; }
echo "nunchaku: ${HEALTH:-healthy}"
# And the published port answers from the host.
curl -fsS -m 5 http://127.0.0.1:3169/healthz >/dev/null
echo "deployed $version in $((SECONDS - began)) s"
