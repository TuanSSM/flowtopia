#!/usr/bin/env bash
# Which process_pr jobs a pull request needs, from the files it changes (one path per line on stdin).
# Prints key=value lines for $GITHUB_OUTPUT:
#   code         true when anything but documentation changed
#   lint, unit, screenshots   true or false
#   suites       the integration suites to run, as a JSON list for the job matrix
# A path no rule knows runs everything: a missed test costs more than a slow run. So does a change
# to process_pr.yml or to this file, which decide what runs.
#   git diff --name-only origin/main...HEAD | test/ci-plan.sh
#   test/ci-plan.sh --all          everything (a run that is not for a pull request)
#   test/ci-plan.sh --self-test    check the rules against examples
set -euo pipefail

# Every suite, in the order the summary lists them. Each one is a job in the integration matrix.
ALL="db combo demos social mod editor suggest links avatar demos-avatars combo-avatars"
BROWSER="combo demos social mod editor suggest links avatar demos-avatars combo-avatars"
# Suites that play demo clips or draw the figure, for changes to the clips or the 3D figure.
FIGURE="combo demos suggest avatar demos-avatars combo-avatars"

plan() {
  local lint=false unit=false shots=false docs_only=true f s
  local -A want=()
  everything() { lint=true; unit=true; shots=true; for s in $ALL; do want[$s]=1; done; }
  suites() { for s in "$@"; do want[$s]=1; done; }
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    case "$f" in
      # Documentation: the docs job, which always runs, covers it.
      *.md|docs/*|.claude/*|.gitignore|.editorconfig|LICENSE) continue ;;
    esac
    docs_only=false
    case "$f" in
      .github/workflows/process_pr.yml|test/ci-plan.sh) everything ;;
      # Release, deploy and container files: their checks are in lint (actionlint, the git-cliff
      # preview, shellcheck); nothing the tests run reads them.
      .github/*|cliff.toml|deploy.sh|backup.sh|compose.yml|Dockerfile|test/docs.sh) lint=true ;;
      # Go tests only: the unit job runs them, and the db suite the ones that need Postgres.
      *_test.go) lint=true; unit=true; suites db ;;
      *.go|go.mod|go.sum) everything ;;
      # Node checks of the catalogs, clips and bundles, run by the unit job.
      test/catalog-fixtures/*|test/*.js) lint=true; unit=true ;;
      # Bundle sources: the unit job rebuilds them and checks web/tour.js and web/figure3d.js match.
      tour/*|figure/*) lint=true; unit=true ;;
      sim/*|web/clips/*|web/figure3d.js|web/avatar.js|web/avatar-def.json|web/demo.js)
        lint=true; unit=true; shots=true; suites $FIGURE ;;
      # Any other page file is embedded in the server and can change any page.
      web/*) lint=true; unit=true; shots=true; suites $BROWSER ;;
      test/browser/h.py|test/browser/run.sh) lint=true; shots=true; suites $BROWSER ;;
      test/browser/pages.py|test/browser/shots.py) lint=true; shots=true ;;
      test/browser/stub.py) lint=true; shots=true; suites links ;;
      test/browser/combo.py) lint=true; suites combo combo-avatars ;;
      test/browser/demos.py) lint=true; suites demos demos-avatars ;;
      test/browser/*.py) lint=true; s=${f#test/browser/}; s=${s%.py}
        if [[ " $BROWSER " == *" $s "* ]]; then suites "$s"; else everything; fi ;;
      *) everything ;;
    esac
  done
  local list=""
  for s in $ALL; do [ -n "${want[$s]:-}" ] && list+="${list:+,}\"$s\""; done
  if $docs_only; then echo "code=false"; else echo "code=true"; fi
  echo "lint=$lint"; echo "unit=$unit"; echo "screenshots=$shots"; echo "suites=[$list]"
}

self_test() {
  local fails=0
  expect() {   # expect "<files, comma separated>" "<expected output on one line>"
    local got; got=$(tr ',' '\n' <<<"$1" | plan | paste -sd' ')
    if [ "$got" = "$2" ]; then echo "PASS $1"; else echo "FAIL $1"; echo "  want: $2"; echo "  got:  $got"; fails=$((fails + 1)); fi
  }
  local all='["db","combo","demos","social","mod","editor","suggest","links","avatar","demos-avatars","combo-avatars"]'
  local browser='["combo","demos","social","mod","editor","suggest","links","avatar","demos-avatars","combo-avatars"]'
  expect "README.md,docs/epics.md,.claude/rules/go-server.md" 'code=false lint=false unit=false screenshots=false suites=[]'
  expect "main.go" "code=true lint=true unit=true screenshots=true suites=$all"
  expect "docs/x.md,go.sum" "code=true lint=true unit=true screenshots=true suites=$all"
  expect ".github/workflows/process_pr.yml" "code=true lint=true unit=true screenshots=true suites=$all"
  expect ".github/workflows/release_tag.yml,cliff.toml,CLAUDE.md" 'code=true lint=true unit=false screenshots=false suites=[]'
  expect "deploy.sh,backup.sh,compose.yml" 'code=true lint=true unit=false screenshots=false suites=[]'
  expect "db_test.go" 'code=true lint=true unit=true screenshots=false suites=["db"]'
  expect "test/physics.js,test/catalog-fixtures/bad-cycle.json" 'code=true lint=true unit=true screenshots=false suites=[]'
  expect "figure/src/body.js" 'code=true lint=true unit=true screenshots=false suites=[]'
  expect "web/figure3d.js,figure/src/body.js" 'code=true lint=true unit=true screenshots=true suites=["combo","demos","suggest","avatar","demos-avatars","combo-avatars"]'
  expect "web/clips/nunchaku/F5.json" 'code=true lint=true unit=true screenshots=true suites=["combo","demos","suggest","avatar","demos-avatars","combo-avatars"]'
  expect "web/social.js" "code=true lint=true unit=true screenshots=true suites=$browser"
  expect "test/browser/mod.py" 'code=true lint=true unit=false screenshots=false suites=["mod"]'
  expect "test/browser/avatar.py,test/browser/demos.py" 'code=true lint=true unit=false screenshots=false suites=["demos","avatar","demos-avatars"]'
  expect "test/browser/stub.py" 'code=true lint=true unit=false screenshots=true suites=["links"]'
  expect "test/browser/pages.py" 'code=true lint=true unit=false screenshots=true suites=[]'
  expect "test/browser/h.py" "code=true lint=true unit=false screenshots=true suites=$browser"
  expect "test/browser/new-suite.py" "code=true lint=true unit=true screenshots=true suites=$all"
  expect "something/new.txt" "code=true lint=true unit=true screenshots=true suites=$all"
  expect "" 'code=false lint=false unit=false screenshots=false suites=[]'
  # Every suite in test/browser must be in the plan, or CI would never run it.
  local f s
  for f in "$(dirname "$0")"/browser/*.py; do
    s=$(basename "$f" .py)
    case "$s" in h|pages|shots|stub) continue ;; esac
    if [[ " $BROWSER " == *" $s "* ]]; then echo "PASS test/browser/$s.py is planned"
    else echo "FAIL test/browser/$s.py is not in BROWSER, so CI never runs it"; fails=$((fails + 1)); fi
  done
  echo "FAILURES: $fails"
  [ "$fails" -eq 0 ]
}

case "${1:-}" in
  --self-test) self_test ;;
  --all) echo "code=true"; echo "lint=true"; echo "unit=true"; echo "screenshots=true"
         printf 'suites=[%s]\n' "$(printf '"%s",' $ALL | sed 's/,$//')" ;;
  "") plan ;;
  *) echo "usage: test/ci-plan.sh [--all | --self-test] < changed-files" >&2; exit 2 ;;
esac
