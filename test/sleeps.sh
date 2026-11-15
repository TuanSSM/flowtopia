#!/usr/bin/env bash
# A browser test waits for what it checks: wait_for, idle, load, nav and check in test/browser/h.py.
# A fixed sleep is only for a time nothing on the page can report, so each one says why on its own line:
#   time.sleep(3)  # fixed: a tour that was coming back would have opened by now
# This fails on any sleep in test/browser/*.py without such a reason, and on any in test/federation.sh, the suite
# that drives two instances from the shell and polls what it waits for with a deadline (its waitfor):
#   sleep 0.25  # fixed: the pause between two looks at what is awaited
#   test/sleeps.sh              check the suites
#   test/sleeps.sh --self-test  check the rule against examples
set -euo pipefail

# Prints file:line:text for each sleep call with no "# fixed: <reason>" after it on the same line.
bare() {
  grep -nE '(^|[^[:alnum:]_])sleep[[:space:]]*\(' "$@" /dev/null |
    grep -vE '\)[^#]*#[[:space:]]*fixed:[[:space:]]*[^[:space:]]' || true
}

# The same for shell: a sleep command (not in a comment line) with no "# fixed: <reason>" after it.
bare_sh() {
  grep -nE '(^|[;&|({[:space:]])sleep[[:space:]]+[0-9.$"]' "$@" /dev/null |
    grep -vE '^[^:]*:[0-9]+:[[:space:]]*#' | grep -vE '#[[:space:]]*fixed:[[:space:]]*[^[:space:]]' || true
}

self_test() {
  local dir fails=0 got; dir=$(mktemp -d); trap 'rm -rf "$dir"' RETURN
  expect() {   # expect "<a line of Python>" bare|ok
    printf '%s\n' "$1" > "$dir/t.py"
    if [ -n "$(bare "$dir/t.py")" ]; then got=bare; else got=ok; fi
    if [ "$got" = "$2" ]; then echo "PASS $2: $1"; else echo "FAIL wanted $2, got $got: $1"; fails=$((fails + 1)); fi
  }
  expect 'time.sleep(1)' bare
  expect '    click(x);time.sleep(.5)' bare
  expect 'time.sleep(dt)' bare
  expect 'sleep(2)' bare
  expect 'asyncio.sleep (2)' bare
  expect 'time.sleep(1)  # the page needs a moment' bare
  expect 'time.sleep(1)  # fixed:' bare
  expect '# fixed: a comment alone, with no call' ok
  expect 'x=1  # fixed: nothing sleeps here' ok
  expect 'time.sleep(3)  # fixed: a tour that was coming back would have opened by now' ok
  expect 'time.sleep(dt);dt=min(.25,dt*1.5)  # fixed: the pause between two looks' ok
  expect 'wait_for(asleep,"the page to sleep")' ok
  expect 'idle()' ok
  expect_sh() {   # expect_sh "<a line of shell>" bare|ok
    printf '%s\n' "$1" > "$dir/t.sh"
    if [ -n "$(bare_sh "$dir/t.sh")" ]; then got=bare; else got=ok; fi
    if [ "$got" = "$2" ]; then echo "PASS $2: $1"; else echo "FAIL wanted $2, got $got: $1"; fails=$((fails + 1)); fi
  }
  expect_sh 'sleep 5' bare
  expect_sh '  until ready; do sleep 0.5; done' bare
  expect_sh 'sleep "$wait"' bare
  expect_sh 'sleep 2  # long enough' bare
  expect_sh '    sleep 0.25  # fixed: the pause between two looks at what is awaited' ok
  expect_sh '# no sleep 5 here, only words' ok
  expect_sh 'echo "nothing sleeps"' ok
  expect_sh 'waitfor 30 drained one' ok
  echo "FAILURES: $fails"
  [ "$fails" -eq 0 ]
}

case "${1:-}" in
  --self-test) self_test ;;
  "")
    cd "$(dirname "$0")/.."
    found=$(bare test/browser/*.py; bare_sh test/federation.sh)
    if [ -n "$found" ]; then
      while IFS=: read -r f n rest; do
        echo "::error file=$f,line=$n::a sleep with no reason: wait for what the check needs (wait_for, idle, check in h.py), or add '# fixed: why' on this line"
        echo "$f:$n: $rest"
      done <<<"$found"
      exit 1
    fi
    echo "every sleep in test/browser and test/federation.sh says why its time is fixed ($(grep -hcE '(^|[^[:alnum:]_])sleep[[:space:]]*\(' test/browser/*.py | paste -sd+ | bc) and $(grep -cE '(^|[;&|({[:space:]])sleep[[:space:]]+[0-9.$"]' test/federation.sh) of them)" ;;
  *) echo "usage: test/sleeps.sh [--self-test]" >&2; exit 2 ;;
esac
