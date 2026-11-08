# Flowa

A progress tracker for flow arts props: a Go server (`main.go`) with the UI embedded, plain JS in
`web/`, Postgres, deployed on a Raspberry Pi with Docker on port 3169. Demos are physics worked out
at build time (`sim/`), checked (`test/`), baked to `web/clips`, and played in the browser on a 3D
humanoid (`figure/` → `web/figure3d.js`) with a drawn 2D fallback.

Start with `README.md`, then the doc for the area you are in (`docs/`). Planned work is in
`docs/roadmap.md` and `docs/epics.md`; when a change completes an item in a
`docs/epic-<code>/todo.md`, tick it in the same PR and update the status line. The rules in
`.claude/rules/` apply by path; the agents in `.claude/agents/` are specialists for each area;
the skills in `.claude/skills/` are the recurring workflows.

## How work flows

- `main` is protected. Every change goes on a branch, through a pull request, and merges only when
  the `process_pr_result` check passes (`.github/workflows/process_pr.yml`). CI runs lint, unit,
  integration (headless Firefox under Xvfb) and screenshots; the PR gets one sticky comment with
  the results and the screenshots inline.
- Keep the Pi cheap: run the quick checks locally (`/pr-checks`) and let CI do the rest.
- Commit messages: a short title, then plain prose on what changed and why. No model names, no
  tool names, no emoji. End with the Co-Authored-By line the session gives you.
- Never put the real admin password in a file. Tests use `flowa-test-only` against a throwaway
  database. `.env` is secret; do not read or print it.
- `./deploy.sh` touches the running service on the Pi. Only run it when asked.

## Programming principles

1. **Correct by construction, then proven.** A demo is listed only when its clip passes
   `node test/physics.js` against its own physics, its definition (`test/expect.js`) and the
   body (reach, overlaps, balance). Add the check before the feature when you can.
2. **Work at build time, not in the browser.** Anything deterministic (physics, posture search,
   bundles) is computed once and committed; the browser plays, it does not compute.
3. **Plain things.** The UI is HTML, CSS and JS with no framework and no CDN. The only build steps
   are two esbuild bundles (`tour/`, `figure/`). Add a dependency only when it removes more code
   than it adds, and pin it.
4. **Explain in words a person can read.** Comments and docs say what and why in ordinary
   sentences, not how. No long dashes in user-facing text (`test/catalog.js` enforces it in
   catalogs). Numbers carry units.
5. **Honest results.** Say what passed, what failed, and what was skipped, with the output. A
   check that cannot run is skipped and counted, never silently passed.
6. **Deterministic and reproducible.** Same inputs, same bytes: baked clips are hashed, bundles
   are checked against their sources in CI, fits are cached in `sim/fits.json`.
7. **Small, scoped changes.** One PR, one purpose. Leave the file better than you found it only
   in the lines you touch.
8. **Nothing is faked.** No placeholder data, no `TODO` code paths that pretend to work. If a
   move cannot be shown yet, it says so in its caption and in `docs/demo-engine.md`.
9. **Match the surrounding style.** Dense but readable JS with short names where the maths is
   short-lived; Go that passes `gofmt`, `go vet` and staticcheck with no exceptions.
10. **Security by default.** Parameterised SQL, PBKDF2 passwords, CSP with no inline scripts,
    read-only container, no secrets in the repo or the logs.

## Quick commands

```sh
gofmt -l . && go vet ./...                 # Go
node --check web/*.js sim/*.js test/*.js   # JS syntax
node test/catalog.js web/catalog/*.json    # catalog format
node test/physics.js                       # every clip (~2.5 min on the Pi); ONLY='nunchaku:F5' for one
node sim/bake.js                           # rebake web/clips after changing sim/
(cd figure && npm run build)               # rebuild web/figure3d.js after changing figure/src
test/browser/run.sh combo.py               # browser tests (throwaway DB in Docker, server on :3170)
test/browser/run.sh pages.py /tmp/shots    # the screenshots CI takes
```
