# TST: Tests that wait for what they check

Why, scope and risks: [epics.md](../epics.md#tst-tests-that-wait-for-what-they-check). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#33](https://github.com/TuanSSM/flowtopia/issues/33)). 4 of 6 items done.

There were 204 `time.sleep` calls across `test/browser/*.py`. Seven are left, each with its reason on the line.

- [x] **The helper.** `wait_for(cond, what, timeout=10)` in `test/browser/h.py`, polling at 50 ms
  with backoff to 250 ms, failing with "waited 10 s for <what>". DoD: used by at least one suite;
  documented in `.claude/rules/ci-and-tests.md`.
  Every suite uses it, with `idle()`, `load()`, `nav()`, `figure()`, `frames()` and a `check` that polls.
- [x] **Replace the sleeps.** Suite by suite (`social.py` first, 39 sleeps), each sleep becomes a
  wait for the element, text, count or request it was waiting for, or keeps a comment saying why a
  fixed time is right (an animation of known length). DoD: `grep -c time.sleep` per suite in the
  PR body, before and after.
  All suites at once, since they share the helper. The seven that stay sample something over time or show that
  something did not happen.
- [x] **A lint against bare sleeps.** The lint job fails on `time.sleep` without a `# fixed:`
  comment on the same line. DoD: the rule in `test/ci-plan.sh`'s lint set and in the rules file.
  `test/sleeps.sh`, with its own self-test; any `sleep(` call counts, not only `time.sleep`.
- [ ] **Time per check.** Each suite prints the seconds per check; the PR comment lists the five
  slowest. DoD: visible in the sticky comment.
- [x] **Go tests for the routes the browser only brushes.** Bookmarks, notifications read state,
  the version endpoint, `FLOWA_HOME_URL`. DoD: each route has a `db_test.go` case.
  `TestDBBookmarks`, `TestDBNotificationsRead`, `TestDBVersion` (the version is part of the
  dashboard's answer; there is no route of its own) and `TestDBHomeLink`.
- [ ] **A month of PRs.** Reruns caused by flakes counted from the Actions history. DoD: zero in
  the 30 days after the last suite is converted; the count written here.
