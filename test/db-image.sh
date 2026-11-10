#!/usr/bin/env bash
# The Postgres image compose.yml runs, on one line: the one place its version and digest are written.
# The pull request checks start their database from this (the `changes` job hands it to the jobs that
# need one) and so does test/browser/run.sh, so a test never passes on a Postgres other than the one
# deployed, and a pull request that moves the pin is tested on the image it moves to.
#   test/db-image.sh              postgres:16.15-alpine@sha256:...
#   test/db-image.sh --self-test
# Fails when the image is not pinned by version and digest, or is not Postgres 16: the data folder of
# one major version does not open in another, so a new major is a dump and a restore, never a new pin.
set -euo pipefail

image() {   # image <compose file>: the db service's image, checked
  local img
  img=$(awk '
    /^  [a-z][a-z0-9_-]*:[[:space:]]*$/ { svc = $1 }
    svc == "db:" && $1 == "image:" { print $2; exit }' "$1")
  if [[ ! "$img" =~ ^postgres:16\.[0-9]+-alpine@sha256:[0-9a-f]{64}$ ]]; then
    echo "the db image in $1 is '${img:-missing}': it must be postgres:16.<minor>-alpine@sha256:<64 hex digits>" >&2
    return 1
  fi
  echo "$img"
}

self_test() {
  local t fails=0 d=sha256:aa6cb33cf4c19c765cefcf48d96176258800870a9a1e930ad126200f9d825139
  t=$(mktemp -d); trap 'rm -rf "$t"' RETURN
  expect() {   # expect <name> <image line of the db service> <output wanted, or FAIL>
    printf 'services:\n  db:\n    container_name: x\n%s\n  proxy:\n    image: caddy:2.11.9-alpine@%s\n' "$2" "$d" > "$t/c.yml"
    local got; got=$(image "$t/c.yml" 2>/dev/null) || got=FAIL
    if [ "$got" = "$3" ]; then echo "PASS $1"; else echo "FAIL $1"; echo "  want: $3"; echo "  got:  $got"; fails=$((fails + 1)); fi
  }
  expect "version and digest" "    image: postgres:16.15-alpine@$d" "postgres:16.15-alpine@$d"
  expect "a comment after the image" "    image: postgres:16.2-alpine@$d   # pinned" "postgres:16.2-alpine@$d"
  expect "a tag without a digest" "    image: postgres:16.15-alpine" FAIL
  expect "a floating tag with a digest" "    image: postgres:16-alpine@$d" FAIL
  expect "a short digest" "    image: postgres:16.15-alpine@sha256:aa6cb33c" FAIL
  expect "another major version" "    image: postgres:17.1-alpine@$d" FAIL
  expect "no image on the db service: another service's is not taken" "    restart: always" FAIL
  echo "FAILURES: $fails"
  [ "$fails" -eq 0 ]
}

case "${1:-}" in
  --self-test) self_test ;;
  "") image "$(dirname "$0")/../compose.yml" ;;
  *) echo "usage: test/db-image.sh [--self-test]" >&2; exit 2 ;;
esac
