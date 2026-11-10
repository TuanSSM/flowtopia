# LIC: Settle the base model's licence

Why, scope and risks: [epics.md](../epics.md#lic-settle-the-base-models-licence). Order: [roadmap.md](../roadmap.md).
Category: fix. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#30](https://github.com/TuanSSM/flowtopia/issues/30)). 0 of 4 items done.

- [ ] **Ask.** A written question to Adobe (Mixamo support or the licensing contact) on serving the
  X Bot GLB from a web app, with the date and the answer recorded in `docs/avatars.md`. DoD: the
  Licences section has the date sent and either the answer or "no answer after 30 days".
- [ ] **Shortlist.** Three CC0 or CC-BY humanoids on a Mixamo-compatible skeleton (same bone names
  as `figure/src/figure.js` reads), with file size, height and vertex count. DoD: a table in
  `docs/avatars.md`; each file opened in `load()` in Node without error.
- [ ] **Decide.** If serving is allowed: close with a line in the doc and the README. If not, or no
  answer after 30 days: swap. DoD: the decision and its reason in `docs/avatars.md`.
- [ ] **Swap, if needed.** The new model as `web/assets/human.glb`; rig capsules refitted
  (`node test/body3d.js rig` within 25 mm); the head measured again; parts proud of the skin
  (`parts`); every clip clear (`node test/body3d.js`, `node test/physics.js`); bundle under 1.5 MB;
  contact sheets from front, angle and side reviewed. DoD: all of those green in one PR, and the
  sheets attached to it.
