# TRU: Progress the server can vouch for

Why, scope and risks: [epics.md](../epics.md#tru-progress-the-server-can-vouch-for). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: L. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#38](https://github.com/TuanSSM/flowtopia/issues/38)). 0 of 6 items done.

- [ ] **Design.** A note in `docs/` on the tables (`sessions`, `levels`), the rules moved to the
  server (XP, streak, badges), the migration, the flag and allowlist, and how the document and the
  tables stay in sync during the transition. DoD: reviewed by the owner; linked from the README.
- [ ] **Tables and API.** `sessions(user, prop, at, minutes, skills, note)` and
  `levels(user, prop, skill, variant, level, at)`; `POST /api/sessions`, `PUT /api/levels`, with the
  existing `rev` flow. DoD: `db_test.go` cases; migration under the lock.
- [ ] **Rules in Go.** XP, levels, streak and badges computed from the tables; a test keeps the Go
  rules equal to `web/game.js` on a fixture of 100 members until the page's copy is removed. DoD:
  the equality test green.
- [ ] **Migration.** Every document converted once, with a per-member report (levels, sessions,
  XP, streak before and after). DoD: zero members with a changed level or streak on stage; the
  report attached to the PR.
- [ ] **Behind the flag.** `progressServer` plus an allowlist; the page writes both ways while on;
  the dashboard shows the mode per member. DoD: the owner's account runs on it for two weeks with
  no difference.
- [ ] **Switch and remove.** Flag on for all; the client's rules and the self-reported endpoints
  removed; `/api/achievements` accepts nothing from the client. DoD: no badge or stat written by the
  client; the feed renders no self-declared number.
