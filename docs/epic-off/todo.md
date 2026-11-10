# OFF: Practice without a signal

Why, scope and risks: [epics.md](../epics.md#off-practice-without-a-signal). Order: [roadmap.md](../roadmap.md).
Category: feature. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#42](https://github.com/TuanSSM/flowtopia/issues/42)). 0 of 5 items done.

Depends on LOAD's service worker and hashed assets.

- [ ] **Offline shell.** The worker also caches the member's catalogs, their progress documents
  as last fetched, and every clip opened. DoD: airplane mode: the dashboard, the map and an opened
  demo render.
- [ ] **The write queue.** Session logs, level changes and combo edits go to an IndexedDB queue
  when offline and replay in order on reconnect through the existing `rev` flow. DoD: a browser test
  blocks the network, logs a session, unblocks, and finds it on the server.
- [ ] **Say what is waiting.** A header line "2 changes waiting to sync" with Retry; conflicts
  shown as today. DoD: `pages.py` text check with the network blocked.
- [ ] **What stays online.** Posting, comments, likes, link cards and the people list need the
  server and say so without an error toast. DoD: the composer is disabled offline with one line.
- [ ] **Two devices.** Both offline, both log; the second replay hits 409 on the same skill and asks,
  on different skills merges. DoD: the `combo.py` two-session check extended.
