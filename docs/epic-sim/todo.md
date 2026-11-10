# SIM: Wraps, weaves and passes

Why, scope and risks: [epics.md](../epics.md#sim-wraps-weaves-and-passes). Order: [roadmap.md](../roadmap.md).
Category: feature. Size: L. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#40](https://github.com/TuanSSM/flowtopia/issues/40)). 0 of 7 items done.

Each item is its own PR, physics test first, no tolerance loosened. Today 103 skills have a demo
and 214 are described but cannot be shown ([demo-coverage.md](../demo-coverage.md)).

- [ ] **Chest against pelvis.** A `twist` in the rig pose (up to 45 degrees), carried to the 3D
  spine bones, capsules follow; body checks cover it. DoD: `test/body3d.js rig` within 25 mm at
  full twist; a contact sheet from above shows the shoulders turned.
- [ ] **Poi weave.** The solved hand with a turning upper body; two and three beat. DoD:
  `node test/physics.js` green; the tether clear of the far shoulder; the caption names what is
  simulated.
- [ ] **A tether that touches.** The tether and cord as a chain of points with contact and friction
  against the capsules. DoD: a known-answer test (a chain draped over a cylinder rests at the right
  wrap angle); existing clips unchanged within 1 mm.
- [ ] **Wraps and rolls.** Poi arm wrap, nunchaku wrist roll from the chain. DoD: physics green; the
  wrap angle and release match the definition in `test/expect.js`.
- [ ] **Passes.** A hand-over as a constraint: the receiving hand steered to the handle, the giving
  hand releasing when it is held; nunchaku shoulder pass and staff hand-to-hand. DoD: both hands
  within reach throughout; no gap where nothing holds the prop.
- [ ] **Rolling contact.** No-slip rolling of a staff on a forearm or neck for the halo. DoD: the
  contact point's speed is zero within tolerance; the staff never sinks past the limit.
- [ ] **Coverage.** `docs/demo-coverage.md` regenerated; the bake and the physics suite timed. DoD:
  at least 60 more skills with a demo; the suite under 4 minutes on the Pi.
