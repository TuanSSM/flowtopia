#!/usr/bin/env bash
# A browser test waits for what it checks: wait_for, idle, load, nav and check in test/browser/h.py.
# A fixed sleep is only for a time nothing on the page can report, so each one says why on its own line:
#   time.sleep(3)  # fixed: a tour that was coming back would have opened by now
# This fails on any sleep in test/browser/*.py without such a reason.
#   test/sleeps.sh              check the suites
#   test/sleeps.sh --self-test  check the rule against examples
set -euo pipefail

# Prints file:line:text for each sleep call with no "# fixed: <reason>" after it on the same line.
bare() {
  grep -nE '(^|[^[:alnum:]_])sleep[[:space:]]*\(' "$@" /dev/null |
    grep -vE '\)[^#]*#[[:space:]]*fixed:[[:space:]]*[^[:space:]]' || true
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
  echo "FAILURES: $fails"
  [ "$fails" -eq 0 ]
}

case "${1:-}" in
  --self-test) self_test ;;
  "")
    cd "$(dirname "$0")/.."
    found=$(bare test/browser/*.py)
    if [ -n "$found" ]; then
      while IFS=: read -r f n rest; do
        echo "::error file=$f,line=$n::a sleep with no reason: wait for what the check needs (wait_for, idle, check in h.py), or add '# fixed: why' on this line"
        echo "$f:$n: $rest"
      done <<<"$found"
      exit 1
    fi
    echo "every sleep in test/browser says why its time is fixed ($(grep -hcE '(^|[^[:alnum:]_])sleep[[:space:]]*\(' test/browser/*.py | paste -sd+ | bc) of them)" ;;
  *) echo "usage: test/sleeps.sh [--self-test]" >&2; exit 2 ;;
esac
