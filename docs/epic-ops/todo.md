# OPS: Safe beyond the LAN

Why, scope and risks: [epics.md](../epics.md#ops-safe-beyond-the-lan). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#35](https://github.com/TuanSSM/flowtopia/issues/35)). 3 of 7 items done.
Three more are built and tested and wait for something that cannot be done from a pull request: the
reverse proxy recipe for the owner's recorded run with a public name, Postgres by digest for
Dependabot's first update pull request, and the release's image for a first release and a deploy of
it on the Pi.

- [x] **Health.** `GET /healthz`: schema version, whether TLS roots are present, database reachable,
  `VERSION`. DoD: `TestHealthz`; `deploy.sh` waits on it instead of the port.
  Done: 200 or 503 for anyone, the detail only for a probe from the machine itself (the version is not
  for visitors); `TestHealthz`, `TestDBHealthz`, `TestProbeURL`; `deploy.sh` waits on
  `docker exec nunchaku /nunchaku -healthcheck`. The number of TLS roots is not something Go reports, so
  the answer says whether there are any.
- [x] **Structured request log.** One line per request: method, route, status, duration, bytes,
  the limit key when refused. DoD: `TestRequestLog`; the log cap in `compose.yml` reviewed.
  Done: one line of JSON with the route's pattern, never the path; `TestRequestLog`,
  `TestRequestLogHandlers`, `TestDBRequestLog`. Healthy `/healthz` probes and page files served without
  an error get no line, and a minute holds at most 240 lines with the rest counted. The log cap went
  from 5 files of 5 MB to 10 of 10 MB: a day of the most the request lines and the refusal lines can
  write together.
- [x] **Loopback by default.** The app publishes on `127.0.0.1:3169` unless `FLOWA_BIND` says
  otherwise; the README says why. DoD: a fresh `compose up` is reachable from the host only.
  Done: `compose.yml` publishes on `${FLOWA_BIND:-127.0.0.1}` in the long form, which takes an IPv6
  address as written. `test/compose.sh` in lint reads what the file resolves to (unset, empty, every
  address, one LAN address, IPv6, a value that is no address, and no port on the database); the new
  `compose` integration suite runs `test/compose.sh --up`, which builds and starts both containers and
  asks `/healthz` by loopback (answers) and by the machine's other addresses (does not), then starts the
  app with `FLOWA_BIND=0.0.0.0` and asks again (answers), so the refusal is shown to be the setting's
  doing. `deploy.sh` checks the address the port is published on, not always loopback, and says where
  that is. This breaks a deployment that is opened from the LAN or through a proxy in another
  container until `FLOWA_BIND` is set in `.env`, so the change is marked breaking. IPv6 is checked in
  what the file resolves to, not on running containers: the runner's Docker has no IPv6.
- [ ] **Reverse proxy recipe.** A `proxy` profile in `compose.yml` with Caddy, automatic
  certificates for `FLOWA_DOMAIN`, `FLOWA_TRUSTED_PROXIES` set to the proxy network, HSTS. DoD: the
  README's steps from clone to HTTPS run on a clean machine (recorded once by hand); `TestSameOrigin`
  covers `https` behind the proxy.
  Built: the `proxy` service (Caddy 2.11.9, pinned by version and digest, read-only, one capability,
  with limits) and the `Caddyfile`; `COMPOSE_PROFILES=proxy` and `FLOWA_DOMAIN` in `.env` are the whole
  switch. With `FLOWA_DOMAIN` the app answers to the name and believes forwarding headers from the
  proxy's network alone (`10.169.31.0/29`, moved with `FLOWA_PROXY_NET`), unless the owner set
  `FLOWA_TRUSTED_PROXIES`. HSTS for a year, without subdomains. `TestSameOrigin` and `TestClientIP` have
  the cases for that network; `test/compose.sh` reads what the file resolves to (15 more checks in
  lint) and, in the `compose` suite, starts the proxy for a private name and asks it over HTTPS: the
  certificate, the way from HTTP, HSTS, the app's own headers, the Secure cookie, a write from an HTTP
  page, and an address the visitor made up. That last check found a fault that is fixed here: beside a
  proxy's own `X-Forwarded-For`, the app took a visitor's `X-Real-IP` when the proxy passed it on, as
  Caddy does. The app now reads `X-Real-IP` only when there is no `X-Forwarded-For`, and the
  `Caddyfile` overwrites it. `deploy.sh` names the HTTPS address and fails when the proxy is not serving.
  Open, and why the box is not ticked: the README's steps have not been run from clone to HTTPS on a
  clean machine. That needs a public name and a machine the internet can reach, for the certificate
  from Let's Encrypt; the checks here use a private name and Caddy's own authority. The owner runs the
  five steps once and writes the date here.
- [ ] **Postgres by digest.** The image pinned by digest with a documented update step and a
  Dependabot or weekly check that opens a PR for a new digest. DoD: `compose.yml` carries the digest;
  the update PR has been seen once.
  Built: `postgres:16.15-alpine@sha256:aa6cb33c...` in `compose.yml`, and that line is the one place the
  image is written: `test/db-image.sh` reads it for the pull request checks (the `changes` job hands
  it to every job with a database) and for `test/browser/run.sh`, and refuses an image that is not
  pinned by version and digest or is not Postgres 16. So the checks run on what is deployed, and a
  pull request that moves the pin runs every integration suite on the image it moves to.
  `.github/dependabot.yml` looks weekly at the images in `compose.yml` (Caddy too) and skips a new
  major of Postgres. The README has the update by hand and why a major is a dump and a restore.
  Open, and why the box is not ticked: no update pull request has been seen. Both pins were the
  newest on Docker Hub on 10 November 2026, so Dependabot has nothing to offer yet. Tick this when its
  first pull request for `compose.yml` has passed `process_pr`, and write its number here.
- [ ] **Image built by the release.** `release_tag.yml` builds the arm64 image from the tag and
  pushes it to GHCR with the version as its tag; `deploy.sh` pulls by default and builds only with
  `--build`; the digest is in the release notes. DoD: a deploy on the Pi takes under a minute and
  the dashboard shows the tag.
  Built: `build-image.sh` builds the image for arm64 and amd64 from the one Dockerfile, with the
  version compiled into the binary (`/nunchaku -version`), and the same commit gives the same digest.
  `release_tag.yml` runs it before it tags, pushes to `ghcr.io/tuanssm/flowtopia:<tag>`, asks the
  pushed image for its version, and ends the notes with the name and digest; a dry run builds without
  pushing. `deploy.sh` pulls the newest release the checkout knows, or the one named, refuses an image
  that says it is another version, starts only what changed (the database keeps running), makes
  `data/` for the app's user on a new machine, and ends with the version and the seconds it took;
  `--build` is the old way. The new `release` suite (`test/release.sh`) does both ends on the runner
  against a registry of its own: the image checks, then deploy, next release, rollback, the refusals
  and `--build`. This changes what `./deploy.sh` without an argument does, so the change is marked
  breaking.
  Open, and why the box is not ticked: no release has been made with it, so there is no image at
  ghcr.io yet and nothing has been pulled on the Pi. After the next release: make the package public
  once (README, Releases), run `./deploy.sh` on the Pi, and write the seconds it prints and the version
  the dashboard shows here.
- [ ] **Restore drill.** A weekly workflow restores the newest backup format (a fixture produced by
  `backup.sh`) into a scratch Postgres and runs the Go tests against it. DoD: green weekly; a
  failure opens an issue.
