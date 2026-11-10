# OPS: Safe beyond the LAN

Why, scope and risks: [epics.md](../epics.md#ops-safe-beyond-the-lan). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#35](https://github.com/TuanSSM/flowtopia/issues/35)). 0 of 7 items done.

- [ ] **Health.** `GET /healthz`: schema version, TLS root count, database reachable, `VERSION`.
  DoD: `TestHealthz`; `deploy.sh` waits on it instead of the port.
- [ ] **Structured request log.** One line per request: method, route, status, duration, bytes,
  the limit key when refused. DoD: `TestRequestLog`; the log cap in `compose.yml` reviewed.
- [ ] **Loopback by default.** The app publishes on `127.0.0.1:3169` unless `FLOWA_BIND` says
  otherwise; the README says why. DoD: a fresh `compose up` is reachable from the host only.
- [ ] **Reverse proxy recipe.** A `proxy` profile in `compose.yml` with Caddy, automatic
  certificates for `FLOWA_DOMAIN`, `FLOWA_TRUSTED_PROXIES` set to the proxy network, HSTS. DoD: the
  README's steps from clone to HTTPS run on a clean machine (recorded once by hand); `TestSameOrigin`
  covers `https` behind the proxy.
- [ ] **Postgres by digest.** The image pinned by digest with a documented update step and a
  Dependabot or weekly check that opens a PR for a new digest. DoD: `compose.yml` carries the digest;
  the update PR has been seen once.
- [ ] **Image built by the release.** `release_tag.yml` builds the arm64 image from the tag and
  pushes it to GHCR with the version as its tag; `deploy.sh` pulls by default and builds only with
  `--build`; the digest is in the release notes. DoD: a deploy on the Pi takes under a minute and
  the dashboard shows the tag.
- [ ] **Restore drill.** A weekly workflow restores the newest backup format (a fixture produced by
  `backup.sh`) into a scratch Postgres and runs the Go tests against it. DoD: green weekly; a
  failure opens an issue.
