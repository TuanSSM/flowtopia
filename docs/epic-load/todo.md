# LOAD: First paint, bytes and queries

Why, scope and risks: [epics.md](../epics.md#load-first-paint-bytes-and-queries). Order: [roadmap.md](../roadmap.md).
Category: perf. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#32](https://github.com/TuanSSM/flowtopia/issues/32)). 0 of 6 items done.

- [ ] **Measure first.** Bytes and requests before first paint, and time to first demo frame, on
  the Pi over a throttled connection in `pages.py`, printed in the PR comment. DoD: the two numbers
  appear for every PR that touches `web/`.
- [ ] **The figure on demand.** `web/figure3d.js` and `human.glb` are fetched when a demo first
  opens; the 2D figure draws until they arrive. DoD: no request for either on the dashboard; a demo
  still opens at once the second time.
- [ ] **Hashed, precompressed assets.** The embedded files get a content hash in their name at
  build time, gzip and brotli variants, `Cache-Control: immutable`; `index.html` stays uncached.
  DoD: `TestStaticHashed`; a changed file gets a new name; the old name still serves for an hour.
- [ ] **Clip index per prop.** `web/clips/index.json` split per prop, loaded with the catalog.
  DoD: opening the nunchaku map fetches no poi clip metadata.
- [ ] **Service worker for the shell.** Caches `index.html`, the scripts, the styles and the
  figure once fetched; checks `VERSION` on start and reloads when it changed. DoD: the second load
  on a phone makes one request (`/api/me`); a deploy is picked up within one reload.
- [ ] **Queries read with EXPLAIN.** The feed, people, notifications, bookmarks and dashboard
  queries on a database seeded with 10 000 posts and 500 members; indexes added where a sequential
  scan shows. DoD: `TestDBQueryPlans` asserts an index scan for each; a timing log line per
  request shows the slowest under 50 ms on the Pi.
