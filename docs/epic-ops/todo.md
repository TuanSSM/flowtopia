# OPS: Safe beyond the LAN

Why, scope and risks: [epics.md](../epics.md#ops-safe-beyond-the-lan). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#35](https://github.com/TuanSSM/flowtopia/issues/35)). 2 of 7 items done.
Three more are built and tested and wait for something that cannot be done from a pull request:
Postgres by digest for Dependabot's first update pull request, the release's image for a first
release and a deploy of it on the Pi, and the restore drill for its first runs on `main`. Two were
built and then taken out again at the owner's request: loopback by default and the reverse proxy
recipe.

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
- [ ] **Loopback by default.** The app publishes on `127.0.0.1:3169` unless `FLOWA_BIND` says
  otherwise; the README says why. DoD: a fresh `compose up` is reachable from the host only.
  Built in #57 and taken out again on 10 November 2026, at the owner's request: the deployment was
  working on port 3169 as it was, opened from other devices and through the owner's own reverse
  proxy, and the new default closed that port to everything but the machine itself until a setting
  was added. `compose.yml` publishes `3169:3169` as before. Not to be done again unless the owner
  asks; if it is, the default must not change a deployment that works.
- [ ] **Reverse proxy recipe.** A `proxy` profile in `compose.yml` with Caddy, automatic
  certificates for `FLOWA_DOMAIN`, `FLOWA_TRUSTED_PROXIES` set to the proxy network, HSTS. DoD: the
  README's steps from clone to HTTPS run on a clean machine (recorded once by hand); `TestSameOrigin`
  covers `https` behind the proxy.
  Built in #58 and taken out again on 10 November 2026, at the owner's request, together with the
  item above: the `proxy` service, the `Caddyfile`, `FLOWA_DOMAIN`, the fixed proxy network and the
  `compose` checks are gone, and `FLOWA_HOSTS` and `FLOWA_TRUSTED_PROXIES` mean what they meant
  before. The owner's own reverse proxy stays in front, as the README describes under "Behind a
  reverse proxy". One thing went with it that was not a setting: #58 made the app read `X-Real-IP`
  only when there is no `X-Forwarded-For`. That is back to the earlier order too; it matters only
  for a visitor on a trusted proxy's own network and can come back as a small change of its own.
- [ ] **Postgres by digest.** The image pinned by digest with a documented update step and a
  Dependabot or weekly check that opens a PR for a new digest. DoD: `compose.yml` carries the digest;
  the update PR has been seen once.
  Built: `postgres:16.15-alpine@sha256:aa6cb33c...` in `compose.yml`, and that line is the one place the
  image is written: `test/db-image.sh` reads it for the pull request checks (the `changes` job hands
  it to every job with a database) and for `test/browser/run.sh`, and refuses an image that is not
  pinned by version and digest or is not Postgres 16. So the checks run on what is deployed, and a
  pull request that moves the pin runs every integration suite on the image it moves to.
  `.github/dependabot.yml` looks weekly at the image in `compose.yml` and skips a new
  major of Postgres. The README has the update by hand and why a major is a dump and a restore.
  Open, and why the box is not ticked: no update pull request has been seen. The pin was the
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
  Built: `backup.sh --restore FILE` is the restore, in one transaction, refused while the app runs,
  so the drill runs the command the README gives and not a copy of it. `test/restore.sh` fills a
  database through the app (admin, member, progress, post, comment, like), takes the backup with
  `backup.sh`, restores it into an empty Postgres from the pinned image, compares every table row
  for row, starts the app on it, signs both accounts in and reads the post and the progress back,
  shows that a restore is refused while the app runs and that a dump cut short or a file that is no
  dump changes nothing, runs the Go database tests against the restored server, and restores each
  backup of an earlier schema kept in `testdata/backups` (schema 5 is there) under today's app.
  `restore_drill.yml` runs it on Tuesdays and by hand; a failed run opens "The restore drill failed"
  or adds to it, and the next green run closes it. `process_pr` runs the same script as the `restore`
  suite for the backup, the Postgres pin and `main.go`.
  Open, and why the box is not ticked: the workflow has not run on `main`. A schedule and the step
  that opens the issue only exist there. After the merge: one run by hand, one with
  `rehearse_failure` to see the issue opened, one more to see it closed; then tick this with the run
  numbers.
