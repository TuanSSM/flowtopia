#!/usr/bin/env bash
# Build the Flowa image for a release, for the Pi (arm64) and for an ordinary server (amd64), from the
# Dockerfile deploy.sh --build uses: the static binary and nothing else. The release workflow runs this
# and deploy.sh pulls what it pushed, so the Pi no longer builds.
#   ./build-image.sh ghcr.io/tuanssm/flowtopia v1.3.0 --push   build both, push them under one name, print name@digest
#   ./build-image.sh ghcr.io/tuanssm/flowtopia v1.3.0          build both and keep nothing: shows that it builds
#   ./build-image.sh flowtopia v1.3.0 --load                   this machine's architecture only, into Docker, to try it
# The version is compiled into the binary, so the image says what it is on the admin dashboard and in
# `/nunchaku -healthcheck` without being told.
# The same commit gives the same bytes: Go builds are reproducible with -trimpath, and every time stamp in
# the image is the commit's (SOURCE_DATE_EPOCH), so the digest printed for a tag can be built again and
# compared. For that reason no build record (provenance) is attached: it would carry the time of the build.
# The page's files are compressed with brotli first (brotli.js) and compiled in with the rest. The copies
# depend on the commit's files and on the brotli in Node, nothing else: copies another brotli made are made
# again, so the same commit builds the same bytes wherever the same Node does (the workflow pins it).
# Needs Go, Node, and Docker with buildx; --push needs a builder that can push more than one architecture
# (the workflow makes one: docker buildx create --driver docker-container --use).
set -euo pipefail
cd "$(dirname "$0")"
export PATH="$HOME/.local/go/bin:$PATH"

image=${1:-} version=${2:-} mode=${3:-}
usage() { echo "usage: ./build-image.sh <image> <version> [--push | --load]" >&2; exit 2; }
[ -n "$image" ] && [ -n "$version" ] || usage
case "$mode" in ""|--push|--load) ;; *) usage ;; esac
# A registry name is lower case (ghcr.io/TuanSSM/... is refused), and the version becomes a tag.
[[ "$image" =~ ^[a-z0-9][a-z0-9._/:-]*$ ]] || { echo "the image name '$image' must be lower case, like ghcr.io/tuanssm/flowtopia" >&2; exit 2; }
[[ "$version" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]] || { echo "'$version' cannot be an image tag" >&2; exit 2; }

# What is pushed is labelled with the commit, so it must be the commit: no changes on top of it.
if [ "$mode" = --push ] && ! git diff --quiet HEAD; then
  echo "this checkout has changes that are not committed; an image pushed from it would not be what its commit builds" >&2
  exit 1
fi
command -v node >/dev/null || { echo "node is not installed; brotli.js needs it to compress the page's files before the binary is compiled" >&2; exit 1; }
node brotli.js >&2
commit=$(git rev-parse HEAD)
SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)
export SOURCE_DATE_EPOCH
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

archs="arm64 amd64"
[ "$mode" = --load ] && archs=$(go env GOARCH)
refs=()
for arch in $archs; do
  # One folder per architecture with the Dockerfile and the binary it copies, as on the Pi.
  mkdir "$work/$arch"
  cp Dockerfile "$work/$arch/"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X main.buildVersion=$version" -o "$work/$arch/nunchaku" .
  common=(--platform "linux/$arch" --provenance=false --sbom=false
    --label "org.opencontainers.image.title=Flowa"
    --label "org.opencontainers.image.source=https://github.com/TuanSSM/flowtopia"
    --label "org.opencontainers.image.version=$version"
    --label "org.opencontainers.image.revision=$commit")
  case "$mode" in
    --load) docker buildx build "${common[@]}" --load -t "$image:$version" "$work/$arch" ;;
    --push)
      # Pushed by digest, without a tag of its own: the one tag is put on the pair below.
      docker buildx build "${common[@]}" --metadata-file "$work/$arch.json" \
        --output "type=image,name=$image,push-by-digest=true,name-canonical=true,push=true,rewrite-timestamp=true" "$work/$arch"
      digest=$(sed -n 's/.*"containerimage.digest": *"\(sha256:[0-9a-f]\{64\}\)".*/\1/p' "$work/$arch.json")
      [ -n "$digest" ] || { echo "no digest reported for linux/$arch" >&2; exit 1; }
      refs+=("$image@$digest")
      echo "linux/$arch: $image@$digest" >&2
      ;;
    "") docker buildx build "${common[@]}" --output type=cacheonly "$work/$arch" ;;
  esac
done

if [ "$mode" = --push ]; then
  docker buildx imagetools create -t "$image:$version" "${refs[@]}" >&2
  digest=$(docker buildx imagetools inspect "$image:$version" --format '{{json .Manifest}}' | sed -n 's/.*"digest": *"\(sha256:[0-9a-f]\{64\}\)".*/\1/p' | head -1)
  [ -n "$digest" ] || { echo "the pushed image $image:$version reports no digest" >&2; exit 1; }
  echo "$image:$version@$digest"
fi
