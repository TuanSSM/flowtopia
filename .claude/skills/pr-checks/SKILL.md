---
name: pr-checks
description: Run the same checks as the process_pr workflow locally, cheapest first, and stop at the first failure. Use before pushing a branch or opening a PR, or when asked "does it pass".
---
Run these in order from the repository root and stop at the first failure. Print each result
line as it is, not a paraphrase.

1. Lint: `gofmt -l .` (must print nothing), `go vet ./...`,
   `for f in sim/*.js web/*.js test/*.js; do node --check "$f" || echo "SYNTAX $f"; done`,
   `node test/catalog.js web/catalog/*.json`, `python3 -m py_compile test/browser/*.py`.
2. Bundles: if `figure/src` or `tour/src` changed, rebuild (`cd figure && npm run build`,
   `cd tour && npm run build`) and confirm `git status` shows the bundle changed too.
3. Unit: `CGO_ENABLED=0 go build ./... && go test ./...`, then `node test/physics.js`
   (about 2.5 minutes on the Pi; use `ONLY='<prop>:<id>'` only while iterating, the full run
   before pushing). Expect `<n> clips, problems: 0`.
4. Integration, only when `web/`, `main.go` or `test/browser/` changed:
   `test/browser/run.sh combo.py` and `test/browser/run.sh demos.py`, both `0 failed`.
5. Remove the throwaway database when done: `docker rm -f flowa-test-db`.

Staticcheck is not installed on the Pi; CI runs it. Say so in the report rather than claiming it
passed. Finish with a short table: check, result, time.
