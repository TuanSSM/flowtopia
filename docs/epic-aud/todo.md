# AUD: Close the stage audit

Why, scope and risks: [epics.md](../epics.md#aud-close-the-stage-audit). Order: [roadmap.md](../roadmap.md).
Category: fix. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#29](https://github.com/TuanSSM/flowtopia/issues/29)). 1 of 7 items done.

The items, their causes, fixes and tests are in [audit-plan.md](../audit-plan.md); this list is the
order and the DoD of each pull request. Nothing is added to a PR beyond its table.

- [x] **PR 1, root certificates.** `fix(build): give the container its root certificates`. DoD:
  `TestRootsPresent` passes with `SSL_CERT_FILE` and `SSL_CERT_DIR` pointed at an empty directory;
  the start log shows `TLS roots: present`. Go has no supported way to count a pool, so the test
  proves trust in two named roots (Let's Encrypt, Google) where the plan asked for a count over 100.
  The card on stage needs a deploy, so it is checked under "The audit run again".
- [ ] **PR 2, limits.** `fix(server): limits count attempts fairly and cannot lock people out`
  (UN-05, SEC-04, SEC-05, SEC-10, UN-06, MAP-03, SEC-16, SEC-15). DoD: the eight tests in the plan
  pass; `TestDBFlagsGateAPI` shows `community` and `game` off give 404 on their routes.
- [ ] **PR 3, nothing lost on the client.** `fix(ui): keep drafts and edits safe across sign-out,
  slow networks and devices` (UN-04, UN-01, UN-02, UN-03, UN-14, UN-07, UN-13, UN-09, UN-10, UN-11).
  DoD: `TestDBIdempotentPost` and `TestDBProfileRev` pass; the `social.py`, `combo.py` and
  `links.py` checks named in the plan pass; a transparent PNG uploads on a white background.
- [ ] **PR 4, admin and moderation correctness.** `fix(admin): every flag listed, fresh account
  states, deliberate role changes` (MAP-02, HP-02, HP-05, HP-03, HP-09, HP-10, UN-17, UN-12, UN-16,
  HP-08, SEC-12, SEC-11). DoD: `node test/flags.js` in lint; migration 5 (`users.last_seen`,
  `users.owner`) applied under the lock; `TestDBRoles` owner cases; `TestPasswordPolicy`; a rehash
  on sign-in for 200 000-round hashes; sign-in time on the Pi recorded in the PR body.
- [ ] **PR 5, phones and reach.** `fix(ui): tap targets, phone layout and the landing page` (UN-15,
  HP-12, HP-11, HP-13, MAP-16, HP-06, MAP-11, MAP-12, UN-08). DoD: `pages.py` at phone width fails
  any tappable control under 44 px on a coarse pointer; `#/avatar` has no horizontal scroll; the
  phone landing shows a demo and a map thumbnail.
- [ ] **PR 6, copy and the surface.** `fix(ui): the server has no Pi, and the words match the pages`
  (HP-04, MAP-15, MAP-09, MAP-10, MAP-08, MAP-17, HP-07; MAP-14 is done in #25). DoD: `FLOWA_HOME_URL`
  documented and empty by default; Staff group in the sidebar footer and both staff pages in the
  palette; visitors see three sidebar items; the combo card's logging and publishing are apart.
- [ ] **The audit run again.** The four journeys on stage after PR 6. DoD: every item in the audit
  report carries its PR number or is reopened with what still fails; the count is in this file; a
  YouTube link posted on stage shows a card and the start log says `TLS roots: present`.
