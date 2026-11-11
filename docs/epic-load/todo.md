# LOAD: First paint, bytes and queries

Why, scope and risks: [epics.md](../epics.md#load-first-paint-bytes-and-queries). Order: [roadmap.md](../roadmap.md).
Category: perf. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#32](https://github.com/TuanSSM/flowtopia/issues/32)). 1 of 6 items done.
The page's files are hashed and compressed. The queries are read and indexed; that box waits for the
time on the Pi.

- [ ] **Measure first.** Bytes and requests before first paint, and time to first demo frame, on
  the Pi over a throttled connection in `pages.py`, printed in the PR comment. DoD: the two numbers
  appear for every PR that touches `web/`.
- [ ] **The figure on demand.** `web/figure3d.js` and `human.glb` are fetched when a demo first
  opens; the 2D figure draws until they arrive. DoD: no request for either on the dashboard; a demo
  still opens at once the second time.
- [x] **Hashed, precompressed assets.** The embedded files get a content hash in their name at
  build time, gzip and brotli variants, `Cache-Control: immutable`; `index.html` stays uncached.
  DoD: `TestStaticHashed`; a changed file gets a new name; the old name still serves for an hour.
  Built: every embedded file but `index.html` and the catalogs answers under
  `name.<16 hex of its SHA-256>.ext` with `immutable` for a year, and `index.html` (`no-cache`, with
  an `ETag` that changes when any file it names does) is
  rewritten by the server to name its 17 scripts, its style sheet and its icon that way, so no file
  in `web/` changed. The 159 baked clips are already named by the hash of their bytes; the server
  checks that and sends them as `immutable` under the name they have. Each file is sent as brotli,
  gzip or as it is, by what the browser takes. The first load, counted as `index.html` and the 20
  files it names, went from 426 KB to 139 KB with gzip and 120 KB with brotli, and a second visit
  asks for two of the 21. The groups, the numbers and the reasons are in the README under
  [Static files](../../README.md#static-files).
  It differs from the plan's wording in two ways. Hashing and gzip happen when the server starts,
  from the embedded bytes (147 ms for 188 files on a four-core x86 machine; the Pi logs its own
  time), so `go run` and `go test` need nothing generated; only brotli is made at build time, by
  `brotli.js`, because Go's standard library cannot write it. And "the old name still serves" is
  kept to the letter for scripts and the style sheet: the build before leaves them in
  `data/static-kept.json.gz`, and the new build sends those bytes under the old names for one hour
  after it first started, to be kept five minutes and not for good, because a file in `data/` is
  not trusted as the binary is. The icon's old name gets 404, for the same reason. Sending
  the new file under the old name, or a redirect to it, was the cheaper choice and was not taken: a
  page would then run scripts of two builds together. The page has no check of the version yet (the
  service worker item below is to add it), so an old page keeps running old scripts until it is
  reloaded, as it did before this change.
  Left for later: the files the page's scripts fetch by name (`figure3d.js`, `assets/human.glb`,
  `tour.js`, the three lists in `clips/`, `avatar-def.json`, `suggest-weights.json`) are compressed
  but still asked about on each visit (a 304); hashed names for them need a change in those scripts,
  which fits "The figure on demand" and the service worker. The catalogs come from the database
  (414 KB for the five, sent as they are unless a proxy compresses them) and are not part of this.
- [ ] **Clip index per prop.** `web/clips/index.json` split per prop, loaded with the catalog.
  DoD: opening the nunchaku map fetches no poi clip metadata.
- [ ] **Service worker for the shell.** Caches `index.html`, the scripts, the styles and the
  figure once fetched; checks `VERSION` on start and reloads when it changed. DoD: the second load
  on a phone makes one request (`/api/me`); a deploy is picked up within one reload.
- [ ] **Queries read with EXPLAIN.** The feed, people, notifications, bookmarks and dashboard
  queries on a database seeded with 10 000 posts and 500 members; indexes added where a sequential
  scan shows. DoD: `TestDBQueryPlans` asserts an index scan for each; a timing log line per
  request shows the slowest under 50 ms on the Pi.
  Built: read with `EXPLAIN ANALYZE` on 500 members, 10,000 posts, 36,000 comments, 100,000 likes and
  20,000 notifications. Two things read a whole table that grows with use. The timeline looked for
  the viewer's own likes, of posts and of comments, by reading every like there is (6.2 ms a page
  here, now 0.8 ms). The admin dashboard counted posts and comments per week with
  `date_trunc('week', created_at) = week`, which no index answers, so each of twelve weeks read every
  post and comment, and unpacked every progress document again (324 ms here, now 5 ms). Migration 6
  adds `likes_user`, `comment_likes_user`, `posts_created` and `comments_created`; the weekly counts
  are asked as a range; the sessions are unpacked once. The notifications, the bell's count, the
  bookmarks and a member's posts already used their indexes.
  `TestDBQueryPlans` runs sixteen queries, the SQL the handlers run, on that database and fails when
  one reads a growing table from end to end or reads more rows than its bound. It differs from the
  plan's wording in one way: the people list and three dashboard counts read `users`, `sessions`,
  `progress` or `reports` whole, on purpose. Those hold a row or a few per member, the question is
  about all of them, and Postgres rightly prefers that to an index at this size; the test names which
  query may read which, and bounds the rows. With migration 6 taken out, thirteen of its checks fail.
  The request log from #56 already writes the milliseconds of every request.
  Open, and why the box is not ticked: the slowest request on the Pi has not been read from its log.
  After the next deploy: `docker logs nunchaku 2>&1 | grep '^{' | jq -s 'max_by(.ms) | {route, ms}'`
  over a day of use, and write the number here.
