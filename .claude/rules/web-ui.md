---
paths:
  - "web/**"
---
# Web UI

- Plain HTML, CSS and JS; no framework, no build step, no CDN. The two bundles (`web/tour.js`,
  `web/figure3d.js`) are built from `tour/` and `figure/`; never edit them by hand.
- `web/app.js` renders from state with template strings; actions go through `data-act` and
  `data-change` attributes handled in one place. Follow that pattern rather than adding
  listeners per element.
- Everything the user reads is plain language, no long dashes, numbers with units. Catalog text
  (`web/catalog/*.json`) must pass `node test/catalog.js`; once published, catalogs are edited in
  the skill map editor and exported back to these files.
- `web/demo.js` is the player only: it reads baked clips and draws. Physics or posture logic
  belongs in `sim/`.
- Keep pages usable at 420 px wide (CI screenshots a phone width) and with
  `prefers-reduced-motion`.
- After a UI change, run `test/browser/run.sh combo.py` and `demos.py`, and look at
  `test/browser/run.sh pages.py /tmp/shots` before opening the PR; CI will post those pictures.
- Feature flags live in `flagDefaults` in `main.go` and are read as `flags.<name>` in the UI; a
  new feature that could break the page ships behind one.
