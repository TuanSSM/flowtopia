---
paths:
  - ".github/**"
  - "test/browser/**"
  - "deploy.sh"
---
# CI and browser tests

- `process_pr.yml` is the one workflow that checks pull requests; `release_tag.yml` only tags releases,
  started by hand (workflow_dispatch), with versions from `cliff.toml`. Keep the git-cliff version the
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
- Use `if: ${{ !cancelled() }}` for steps that must run after a failure, never `always()`: an
  `always()` step keeps running in a run that a newer push cancelled. Only `process_pr_result` uses
  `always()`.
- Pin runners (`ubuntu-24.04`) and use the Node 24 releases of actions. Permissions are
  `contents: read` except where a job needs more; say why in a comment.
- Browser tests use `test/browser/h.py` (Marionette, headless Firefox). In CI they run under
  `xvfb-run`, because Firefox has WebGL only with a display. Locally `run.sh` starts a throwaway
  Postgres in Docker; in CI it is given `DATABASE_URL` for a service container.
- A test prints `PASS`, `FAIL` or `SKIP` per check and `FAILURES:` at the end; `run.sh` exits
  non-zero on a failure and counts skips. A check that cannot run in an environment is skipped
  with a reason, never passed.
- Screenshots: `pages.py` writes the pages and a demo sheet plus `index.json`; the job pushes
  them to the `ci-screenshots` branch under `pr-<n>/<sha>/` and the summary embeds them. Add a
  page there when you add a page to the app.
- The test admin password is `flowa-test-only`. The real one never appears in the repo.
- `run.sh` must stay runnable on the Pi (bash, python3, firefox-esr, docker) and on the runner
  (bash, python3, firefox, no docker).
