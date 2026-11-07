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
- `go test ./...` is in CI and currently has no tests. New logic that is not a thin handler
  (parsing, scoring, migrations) gets a `_test.go` next to it.
- The binary is built on the host (`CGO_ENABLED=0`), the image is `FROM scratch`: no cgo, no
  files read from disk except `-data` for backups.
- Before pushing: `gofmt -l . && go vet ./...`. CI also runs staticcheck; treat its findings as
  errors.
