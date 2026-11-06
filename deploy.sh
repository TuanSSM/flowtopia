#!/usr/bin/env bash
# Build the static binary, rebuild the image, recreate the container, wait for healthy.
set -euo pipefail
cd "$(dirname "$0")"
export PATH="$HOME/.local/go/bin:$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
# Rebuild the guided-tour bundle (React Joyride) when its dependencies are installed.
[ -d tour/node_modules ] && (cd tour && npm run --silent build)
# Rebuild the 3D figure bundle (three.js) when its dependencies are installed.
[ -d figure/node_modules ] && (cd figure && npm run --silent build)
# Work out the demo clips (physics simulation and checks happen here, not in the browser).
node sim/bake.js
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o nunchaku .
go vet ./...
docker compose up -d --build --force-recreate
for _ in $(seq 1 90); do
  [ "$(docker inspect -f '{{.State.Health.Status}}' nunchaku)" = healthy ] && break
  sleep 1
done
docker inspect -f 'nunchaku: {{.State.Health.Status}}' nunchaku
curl -fsS -m 5 http://127.0.0.1:3169/healthz >/dev/null
