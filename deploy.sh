#!/usr/bin/env bash
# Build the static binary, rebuild the image, recreate the container, wait for healthy.
#   ./deploy.sh              build and deploy; if the new container is not healthy, go back
#   ./deploy.sh --rollback   go back to the image that ran before the last deploy
# The image that was running is kept as nunchaku:previous. Going back is safe while migrations only
# add tables and columns, as they do now; a migration that removes or rewrites data must say in its
# pull request that going back past it is not possible.
set -euo pipefail
cd "$(dirname "$0")"
export PATH="$HOME/.local/go/bin:$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"

healthy() {
  for _ in $(seq 1 90); do
    [ "$(docker inspect -f '{{.State.Health.Status}}' nunchaku 2>/dev/null)" = healthy ] && return 0
    sleep 1
  done
  return 1
}
rollback() {
  docker image inspect nunchaku:previous >/dev/null 2>&1 || { echo "no previous image to go back to" >&2; exit 1; }
  docker tag nunchaku:previous nunchaku:latest
  docker compose up -d --no-build --force-recreate nunchaku
  healthy && echo "rolled back: nunchaku is healthy on the previous image" || { echo "the previous image is not healthy either" >&2; exit 1; }
}
if [ "${1:-}" = "--rollback" ]; then rollback; exit 0; fi

# Rebuild the guided-tour bundle (React Joyride) when its dependencies are installed.
[ -d tour/node_modules ] && (cd tour && npm run --silent build)
# Rebuild the 3D figure bundle (three.js) when its dependencies are installed.
[ -d figure/node_modules ] && (cd figure && npm run --silent build)
# Work out the demo clips (physics simulation and checks happen here, not in the browser).
node sim/bake.js
go vet ./...
go test -count=1 ./...   # the database tests skip without TEST_DATABASE_URL; the rest must pass
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o nunchaku .
# Keep what is running now, to go back to.
docker image inspect nunchaku:latest >/dev/null 2>&1 && docker tag nunchaku:latest nunchaku:previous
docker compose up -d --build --force-recreate
if ! healthy; then
  echo "the new container is not healthy; going back to the previous image" >&2
  docker logs --tail 30 nunchaku >&2 || true
  rollback
  exit 1
fi
docker inspect -f 'nunchaku: {{.State.Health.Status}}' nunchaku
curl -fsS -m 5 http://127.0.0.1:3169/healthz >/dev/null
