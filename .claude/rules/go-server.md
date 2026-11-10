---
paths:
  - "main.go"
  - "**/*.go"
  - "go.mod"
---
# Go server

- One file, `main.go`, on purpose: handlers, storage and the embedded `web/` filesystem. Split it
  only when a second binary or package is needed, not for size alone.
- Standard library plus `lib/pq`. No ORM, no router library. Routes use Go 1.22 method patterns
  (`mux.HandleFunc("GET /api/...")`).
- Every query is parameterised. Every handler checks the session first and returns JSON errors
  with the right status; never leak SQL or stack text to the client.
- Passwords: PBKDF2-SHA256 with a per-user salt. Sessions: random tokens in HttpOnly cookies.
- Keep the CSP strict (`script-src 'self'`): no inline scripts, no CDN. If a feature needs a new
  source, say so in the PR; do not loosen the policy quietly.
- `go test ./...` is in CI; `main_test.go` covers link cleaning, mentions, stats, tags and
  combos. New logic that is not a thin handler (parsing, scoring, migrations) gets a test there.
- The binary is built on the host (`CGO_ENABLED=0`), the image is `FROM scratch`: no cgo, no
  files read from disk except `-data` for backups.
- Before pushing: `gofmt -l . && go vet ./...`. CI also runs staticcheck; treat its findings as
  errors.
- Schema changes are new entries appended to `migrations` in `main.go`; never edit or reorder one
  that has shipped. `TestDBMigrations` checks they apply once, and the restore drill
  (`test/restore.sh`) restores a backup of each earlier schema kept in `testdata/backups` and starts
  the app on it: keep one of the schema before yours (`test/restore.sh --fixture` before your
  change). A migration that removes or rewrites data must say in its PR that `./deploy.sh --rollback`
  cannot go back past it.
- New write endpoints get an allowance in `limits` (`allowed(w, a.lim.x, u, "...")`); pictures go
  through `cleanImage`; acting on someone else's content checks `outranks`. Responses already carry
  the security headers from `secure`; do not set weaker ones per handler.
