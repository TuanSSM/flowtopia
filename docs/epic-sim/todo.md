# SIM: Wraps, weaves and passes

Why, scope and risks: [epics.md](../epics.md#sim-wraps-weaves-and-passes). Order: [roadmap.md](../roadmap.md).
Category: feature. Size: L. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#40](https://github.com/TuanSSM/flowtopia/issues/40)). 1 of 7 items done.

Each item is its own PR, physics test first, no tolerance loosened. Today 103 skills have a demo
and 214 are described but cannot be shown ([demo-coverage.md](../demo-coverage.md)).

- [ ] **Chest against pelvis.** A `twist` in the rig pose (up to 45 degrees), carried to the 3D
  spine bones, capsules follow; body checks cover it. DoD: `test/body3d.js rig` within 25 mm at
  full twist; a contact sheet from above shows the shoulders turned.
- [ ] **Poi weave.** The solved hand with a turning upper body; two and three beat. DoD:
  `node test/physics.js` green; the tether clear of the far shoulder; the caption names what is
  simulated.
- [x] **A tether that touches.** The tether and cord as a chain of points with contact and friction
  against the capsules. DoD: a known-answer test (a chain draped over a cylinder rests at the right
  wrap angle); existing clips unchanged within 1 mm.
  - `sim/chain.js`: 10 mm links, 4 substeps of 1/6000 s, lengths solved all at once, contact and
    Coulomb friction against moving capsules, and a limit on the pull (66.7 N for a tether) past
    which it stops the clip. An option (`chain`) of the poi and nunchaku simulations; no move uses
    it yet ([demo-engine.md](../demo-engine.md#a-tether-that-touches-the-chain)).
  - `test/chain.js`, run by `node test/physics.js`: 70 known answers, 0 failed, 0 skipped. Draped
    over a cylinder it lies on 18 points, 180.73 degrees where 180.63 is expected (0.2 allowed);
    the pull at which it slides is what straight links hold within 0.01% to 0.21% for friction 0.2
    and 0.5 at a quarter and half a turn (0.12% to 0.29% allowed), which is 0.2% to 0.8% over
    exp(friction * angle); a free swing has its pendulum's period within 0.01% and keeps its
    energy within 0.03% over 10 s. Breaking the engine 25 ways in a scratch copy fails the test
    each time.
  - Clips: the bake with this change and the bake without it give the same bytes for all 167. On
    the machine used, both differ from the committed files in 41 simulated loops, in the last
    digits of the loop's duration (all 41, under 0.000000000001 s) and of the trail length worked
    out from it (24 of them); every frame position is the same. That is for its own pull request.
  - Cost: about 3 times the stiff tether per simulated second; the suite went from 43 s to 49 to 51 s
    there (about 180 s on the Pi at the 3.5 times assumed, not measured on one). At about 6 s a
    chain clip on the Pi, ten more fit in the 240 s allowed, not the 30 skills hoped for: the
    next item has to make a chain clip cheaper.
- [ ] **Wraps and rolls.** Poi arm wrap, nunchaku wrist roll from the chain. DoD: physics green; the
  wrap angle and release match the definition in `test/expect.js`.
- [ ] **Passes.** A hand-over as a constraint: the receiving hand steered to the handle, the giving
  hand releasing when it is held; nunchaku shoulder pass and staff hand-to-hand. DoD: both hands
  within reach throughout; no gap where nothing holds the prop.
- [ ] **Rolling contact.** No-slip rolling of a staff on a forearm or neck for the halo. DoD: the
  contact point's speed is zero within tolerance; the staff never sinks past the limit.
- [ ] **Coverage.** `docs/demo-coverage.md` regenerated; the bake and the physics suite timed. DoD:
  at least 60 more skills with a demo; the suite under 4 minutes on the Pi.
