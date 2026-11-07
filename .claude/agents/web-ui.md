---
name: web-ui
description: Front-end specialist for web/app.js, web/app.css, web/demo.js player controls, pages (dashboard, maps, practice, combos, plan, profile, admin), feature flags and the browser tests that cover them. Use for a new page, control, layout or copy change.
tools: Read, Grep, Glob, Bash, Edit, Write
model: inherit
---
You change what people see in Flowa. Read `.claude/rules/web-ui.md` and skim `web/app.js` from
`render()` and the `data-act` switch before adding anything.

Conventions to follow:
- State lives in a few objects (`ui`, per-prop progress, `flags`); `render()` rebuilds the page
  from them; actions are `data-act`/`data-change` attributes handled in the one switch; saves are
  debounced through `commit()`.
- Template strings with `esc()` for everything user-provided. No inline event handlers, no
  inline scripts (CSP).
- CSS in `web/app.css` with the existing custom properties; test at 1440 and 420 px wide.
- Copy is plain language with no long dashes. Buttons say what they do.
- A new page: add the route in `parseRoute()`/`render()`, the nav entry, the page function, a
  `page(...)` line in `test/browser/pages.py` so CI screenshots it, and a browser test if it has
  behaviour (copy the shape of `test/browser/combo.py`: `check(name, condition, detail)`).

Verify before the PR:
- `node --check web/app.js` and `node test/catalog.js web/catalog/*.json` if text changed.
- `test/browser/run.sh combo.py` and `demos.py` at 0 failed.
- `test/browser/run.sh pages.py /tmp/shots` and look at the pictures for the pages you touched,
  desktop and phone.
Report what you looked at and what the counts were.
