#!/usr/bin/env bash
# The slowest checks of a pull request's integration suites, as a Markdown table for the PR comment.
# Reads what each suite's job uploaded: <dir>/integration-<suite>/out.txt with the "TIME <seconds> <name>"
# lines test/browser/h.py prints (a check's time runs from the check before it), and for the db suite
# <dir>/integration-db/result.txt with go test's "--- PASS: TestName (1.23s)" lines.
#   test/slowest.sh results        the five slowest
#   test/slowest.sh results 10     the ten slowest
#   test/slowest.sh --self-test
# Prints nothing when no suite left a time.
set -euo pipefail

rows() {   # seconds<TAB>suite<TAB>name, one per check
  local d s
  for d in "$1"/integration-*/; do
    [ -d "$d" ] || continue
    s=${d%/}; s=${s##*/integration-}
    if [ -f "$d/out.txt" ]; then
      sed -nE "s/^TIME ([0-9]+(\.[0-9]+)?) (.*)$/\1\t$s\t\3/p" "$d/out.txt"
    fi
    if [ -f "$d/result.txt" ]; then
      sed -nE "s/^--- (PASS|FAIL): ([^ ]+) \(([0-9.]+)s\)$/\3\t$s\t\2/p" "$d/result.txt"
    fi
  done
}

table() {
  local n=${2:-5} out
  out=$(rows "$1" | sort -t$'\t' -k1,1nr -k2,2 -k3,3 | head -n "$n")
  [ -n "$out" ] || return 0
  echo '| Seconds | Suite | Check |'
  echo '| ---: | --- | --- |'
  # The name goes in a code span: a check called "@admin sees the queue" must not notify anyone, and a
  # bar or a backtick in a name must not break the table.
  while IFS=$'\t' read -r secs suite name; do
    name=${name//\`/\'}; name=${name//|/\\|}
    printf '| %s | %s | `%s` |\n' "$secs" "$suite" "${name:0:100}"
  done <<<"$out"
}

self_test() {
  local t fails=0 got want
  t=$(mktemp -d); trap 'rm -rf "$t"' RETURN
  mkdir -p "$t/r/integration-social" "$t/r/integration-db" "$t/r/integration-mod" "$t/r/other" "$t/none/integration-combo"
  printf 'PASS a\nTIME 12.5 @admin sees the queue\nTIME 0.3 a | b `c`\nTIME x not a time\nFAILURES: none\n' > "$t/r/integration-social/out.txt"
  printf '=== RUN   TestDBOne\n--- PASS: TestDBOne (3.25s)\n    --- PASS: TestDBOne/sub (9.00s)\n--- FAIL: TestDBTwo (20.00s)\nPASS\n' > "$t/r/integration-db/result.txt"
  printf '5 passed, 0 failed, 0 skipped\n' > "$t/r/integration-db/out.txt"
  printf 'TIME 3.25 mod check\n' > "$t/r/integration-mod/out.txt"
  printf 'TIME 99 not a suite\n' > "$t/r/other/out.txt"
  printf '3 passed, 0 failed, 0 skipped\n' > "$t/none/integration-combo/out.txt"
  expect() {   # expect "<what>" "<got>" "<want>"
    if [ "$2" = "$3" ]; then echo "PASS $1"; else echo "FAIL $1"; echo "  want: $3"; echo "  got:  $2"; fails=$((fails + 1)); fi
  }
  want='| Seconds | Suite | Check |
| ---: | --- | --- |
| 20.00 | db | `TestDBTwo` |
| 12.5 | social | `@admin sees the queue` |
| 3.25 | db | `TestDBOne` |
| 3.25 | mod | `mod check` |'
  got=$(table "$t/r" 4)
  expect "slowest first, ties by suite, sub-tests and other folders left out" "$got" "$want"
  got=$(table "$t/r" 5 | tail -1)
  expect "a bar is escaped and a backtick replaced" "$got" '| 0.3 | social | `a \| b '"'"'c'"'"'` |'
  expect "five by default" "$(table "$t/r" | wc -l | tr -d ' ')" 7
  expect "no times, no table" "$(table "$t/none")" ""
  expect "no results folder, no table" "$(table "$t/missing")" ""
  echo "FAILURES: $fails"
  [ "$fails" -eq 0 ]
}

case "${1:-}" in
  --self-test) self_test ;;
  ""|-*) echo "usage: test/slowest.sh <results dir> [how many] | --self-test" >&2; exit 2 ;;
  *) table "$@" ;;
esac
