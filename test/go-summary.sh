#!/usr/bin/env bash
# The one-line result of a `go test -v` run for the pull request's comment, from its output and its exit code.
#   test/go-summary.sh <output file> <exit code>   prints "N passed, M failed, K skipped" and a reason when needed
#   test/go-summary.sh --self-test
# A run can fail with no "--- FAIL" line: the test binary panicked, or the whole run passed go test's -timeout
# ("panic: test timed out after 25m0s", the test running then named), or the package failed to build. Such a run is
# one failure with its reason, never "0 failed": a job that failed must not read as passed.
set -euo pipefail

summary() {
  local file=$1 code=$2 pass fail skip why=""
  pass=$(grep -c -- '^--- PASS' "$file" || true)
  fail=$(grep -c -- '^--- FAIL' "$file" || true)
  skip=$(grep -c -- '^--- SKIP' "$file" || true)
  if [ "$code" != 0 ] && [ "$fail" -eq 0 ]; then
    fail=1
    if grep -q '^panic: test timed out after' "$file"; then
      why="timed out after $(sed -nE 's/^panic: test timed out after ([^ ]+).*/\1/p' "$file" | head -1)"
      local running
      # A timeout names the tests running then in a "running tests:" block; an older go, or a cut log, has none.
      running=$( (grep -A2 '^running tests:' "$file" || true) | sed -nE '2s/^[[:space:]]+([^ ]+).*/\1/p')
      [ -n "$running" ] && why+=" in $running"
    elif grep -q '^panic:' "$file"; then
      why="a panic: $(grep -m1 '^panic:' "$file" | cut -c8-120)"
    elif grep -q '\[build failed\]\|\[setup failed\]' "$file"; then
      why="the package did not build"
    else
      why="go test exited with $code and no test failed"
    fi
  fi
  echo "$pass passed, $fail failed, $skip skipped${why:+ ($why)}"
}

self_test() {
  local t fails=0
  t=$(mktemp -d); trap 'rm -rf "$t"' RETURN
  expect() {   # expect "<what>" "<got>" "<want>"
    if [ "$2" = "$3" ]; then echo "PASS $1"; else echo "FAIL $1"; echo "  want: $3"; echo "  got:  $2"; fails=$((fails + 1)); fi
  }
  printf -- '--- PASS: TestDBOne (1.00s)\n--- SKIP: TestDBTwo (0.00s)\nPASS\nok  \tnunchaku\t3.1s\n' > "$t/ok"
  expect "a run that passed" "$(summary "$t/ok" 0)" "1 passed, 0 failed, 1 skipped"
  printf -- '--- PASS: TestDBOne (1.00s)\n--- FAIL: TestDBTwo (2.00s)\nFAIL\nFAIL\tnunchaku\t3.1s\n' > "$t/failed"
  expect "a test that failed" "$(summary "$t/failed" 1)" "1 passed, 1 failed, 0 skipped"
  printf -- '--- PASS: TestDBOne (1.00s)\n=== RUN   TestDBRemoteRowsStayRemote\npanic: test timed out after 10m0s\nrunning tests:\n\tTestDBRemoteRowsStayRemote (4s)\n\ngoroutine 1 [running]:\nFAIL\tnunchaku\t601.244s\nFAIL\n' > "$t/timeout"
  expect "a run past its timeout" "$(summary "$t/timeout" 1)" "1 passed, 1 failed, 0 skipped (timed out after 10m0s in TestDBRemoteRowsStayRemote)"
  printf -- '--- PASS: TestDBOne (1.00s)\npanic: test timed out after 25m0s\n\ngoroutine 1 [running]:\nFAIL\tnunchaku\t1501.2s\n' > "$t/timeout-bare"
  # Run as the workflow runs it, as its own process under set -e and pipefail, where a grep that finds nothing ends it.
  expect "a timeout with no list of the tests running" "$(bash "$0" "$t/timeout-bare" 1)" "1 passed, 1 failed, 0 skipped (timed out after 25m0s)"
  expect "a run past its timeout, as its own process" "$(bash "$0" "$t/timeout" 1)" "1 passed, 1 failed, 0 skipped (timed out after 10m0s in TestDBRemoteRowsStayRemote)"
  printf -- '--- PASS: TestDBOne (1.00s)\npanic: runtime error: index out of range [0] with length 0\nFAIL\tnunchaku\t3.1s\n' > "$t/panic"
  expect "a panic outside a test's report" "$(summary "$t/panic" 2)" "1 passed, 1 failed, 0 skipped (a panic: runtime error: index out of range [0] with length 0)"
  printf -- '# nunchaku\n./main.go:1:1: syntax error\nFAIL\tnunchaku [build failed]\n' > "$t/build"
  expect "a package that did not build" "$(summary "$t/build" 1)" "0 passed, 1 failed, 0 skipped (the package did not build)"
  printf -- 'PASS\n' > "$t/odd"
  expect "a failed exit with nothing to show for it" "$(summary "$t/odd" 3)" "0 passed, 1 failed, 0 skipped (go test exited with 3 and no test failed)"
  echo "FAILURES: $fails"
  [ "$fails" -eq 0 ]
}

case "${1:-}" in
  --self-test) self_test ;;
  ""|-*) echo "usage: test/go-summary.sh <go test -v output> <exit code> | --self-test" >&2; exit 2 ;;
  *) summary "$1" "${2:?the exit code of go test}" ;;
esac
