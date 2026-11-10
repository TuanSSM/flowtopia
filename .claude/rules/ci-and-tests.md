---
paths:
  - ".github/**"
  - "test/browser/**"
  - "deploy.sh"
---
# CI and browser tests

- `process_pr.yml` is the one workflow that checks pull requests; `release_tag.yml` only makes releases
  (the image by `build-image.sh`, then the tag and the notes), started by hand (workflow_dispatch),
  with versions from `cliff.toml`. Keep the git-cliff version the
  same in both files.
- PR titles are Conventional Commits titles (the `changes` job checks them): with squash merges the
  title is the commit `cliff.toml` reads. When adding a commit type, change the list in CLAUDE.md, the
  title check in `process_pr.yml` and the parsers in `cliff.toml` together.
- `process_pr.yml`: `process_pr_result` is the required check and must `need` every job and fail
  when any job failed or was cancelled, or was skipped though the plan needed it. Never path-filter
  the workflow: a required check that does not run blocks the PR. Jobs are chosen inside it instead,
  by `test/ci-plan.sh` in the `changes` job.
- `test/ci-plan.sh` maps changed paths to jobs and integration suites. When in doubt it runs
  everything; keep it that way. A new browser suite goes into its `ALL` list and the self-test (the
  lint job runs `test/ci-plan.sh --self-test`), or it never runs in CI.
- The Postgres image is written once, in `compose.yml`, by version and digest. `test/db-image.sh`
  reads it: the `changes` job hands it to the jobs with a Postgres service
  (`needs.changes.outputs.postgres`) and `run.sh` starts its throwaway database from it. Never name
  a Postgres image anywhere else. Dependabot (`.github/dependabot.yml`) moves the pins; its pull
  requests cannot push screenshots, which is one reason `compose.yml` alone does not ask for them.
- `test/release.sh` is the `release` suite: `build-image.sh` against a registry on the runner, then
  `deploy.sh` for real (pull, next release, rollback, refusals, `--build`). It uses the real names
  and port (3169), so it refuses to run where Flowa is deployed; `--image` runs the image checks
  alone. What `deploy.sh` or `build-image.sh` learn to do gets a check there. A new setting in
  `compose.yml` goes into the list its `deploy()` unsets, or a value in the caller's environment
  leaks into the checks.
- `test/restore.sh` is the restore drill and the `restore` suite; `restore_drill.yml` runs it weekly and
  opens an issue when it fails. It uses names and ports of its own, so it may run anywhere. A pull
  request that adds a migration also adds the backup of the schema before it to `testdata/backups`
  (`test/restore.sh --fixture` on the commit before), so the drill keeps restoring old backups.
- Use `if: ${{ !cancelled() }}` for steps that must run after a failure, never `always()`: an
  `always()` step keeps running in a run that a newer push cancelled. Only `process_pr_result` uses
  `always()`.
- Pin runners (`ubuntu-24.04`) and use the Node 24 releases of actions. Permissions are
  `contents: read` except where a job needs more; say why in a comment.
- Browser tests use `test/browser/h.py` (Marionette, headless Firefox). In CI they run under
  `xvfb-run`, because Firefox has WebGL only with a display. Locally `run.sh` starts a throwaway
  Postgres in Docker; in CI it is given `DATABASE_URL` for a service container.
- A browser test waits for what it checks, never for a guessed time. The helpers are in `h.py`:
  `load(url)` and `nav(hash)` return when the page has drawn and gone quiet; `idle()` waits until no
  request is unanswered, no timer under a second is pending and no transition is running;
  `wait_for(cond, what, timeout=10)` polls a function or a line of page JS (every 50 ms, backing off to
  250 ms) and fails with "waited 10 s for <what>"; `check(name, ok)` polls when `ok` is a function, so a
  check of something that arrives late needs no wait before it; `figure()` waits for the 3D figure and
  `frames()` for the next drawings before a picture. Wait for the thing itself (the element, the text,
  the count, the saved value read back through the API), not for `idle()` alone, when a check depends
  on it.
- A fixed `time.sleep` is only for a time nothing on the page can report: showing that something did
  not happen (a tour that must not open), or sampling a picture that should be moving or still. It
  carries `# fixed: <why>` on the same line. `test/sleeps.sh` in the lint job fails on a sleep without
  one; run it before pushing a change to a suite.
- A test prints `PASS`, `FAIL` or `SKIP` per check, then `TIME <seconds> <name>` per check with the
  slowest first, and `FAILURES:` at the end. A check's time runs from the check before it, so the steps
  that led to it are counted in. `run.sh` shows the slowest five; `test/slowest.sh` makes the table of
  the five slowest across all suites for the PR comment. `run.sh` exits
  non-zero on a failure and counts skips. A check that cannot run in an environment is skipped
  with a reason, never passed.
- Screenshots: `pages.py` writes the pages and a demo sheet plus `index.json`; the job pushes
  them to the `ci-screenshots` branch under `pr-<n>/<sha>/` and the summary embeds them. Add a
  page there when you add a page to the app.
- The test admin password is `flowa-test-only`. The real one never appears in the repo.
- `run.sh` must stay runnable on the Pi (bash, python3, firefox-esr, docker) and on the runner
  (bash, python3, firefox, no docker).
