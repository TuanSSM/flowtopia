# LOAD: First paint, bytes and queries

Why, scope and risks: [epics.md](../epics.md#load-first-paint-bytes-and-queries). Order: [roadmap.md](../roadmap.md).
Category: perf. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#32](https://github.com/TuanSSM/flowtopia/issues/32)). 0 of 6 items done.
The queries are read and indexed; that box waits for the time on the Pi.

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
