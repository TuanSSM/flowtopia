# SIM: Wraps, weaves and passes

Why, scope and risks: [epics.md](../epics.md#sim-wraps-weaves-and-passes). Order: [roadmap.md](../roadmap.md).
Category: feature. Size: L. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#40](https://github.com/TuanSSM/flowtopia/issues/40)). 2 of 7 items done.

Each item is its own PR, physics test first, no tolerance loosened. Today 103 skills have a demo
and 214 are described but cannot be shown ([demo-coverage.md](../demo-coverage.md)).

- [x] **Chest against pelvis.** A `twist` in the rig pose (up to 45 degrees), carried to the 3D
  spine bones, capsules follow; body checks cover it. DoD: `test/body3d.js rig` within 25 mm at
  full twist; a contact sheet from above shows the shoulders turned.
  - The pose's `twist` is `[degrees, 0, 0]`, positive to the figure's left, about the line of the
    spine. The rig stops at 45 degrees (clamped), and the body checks fail a clip that asks for
    more. The shoulders, the base of the neck, the chest's capsules and a hand the pose leaves out
    follow all of it and the head 40%; the hips, the legs and a hand the pose gives do not move
    ([demo-engine.md](../demo-engine.md#the-figure)). On the model `Spine`, `Spine1` and `Spine2`
    take 15%, 30% and 55% of it. No move has a twist yet, and no clip changed.
  - `test/twist.js`, run by `node test/physics.js`: 26 known answers, 0 failed, 0 skipped, each
    worked out from the definition with the test's own rotation, on 9 bodies, 4 poses and 15, 30
    and 45 degrees each way (216 cases). The shoulder line is turned against the hip line by the
    twist within 0.00000000000002 degrees (0.000001 allowed); the hips, knees, feet and their capsules are the same to the last digit;
    the chest's capsules are carried within 0.000001 mm. 12 of the 432 hands go out of reach and
    the reach check names each; a staff 30 mm clear in front of the right shoulder is 28 mm inside
    it once the chest has turned 45 degrees, and the overlap check says so.
  - `node test/body3d.js rig`, on the standard, the short lean and the tall broad body: standing,
    the skin reaches 23 mm outside the capsules at every twist, as it does without one (25 mm
    allowed); the model's shoulder line is within 0.001 degrees of the twist (0.1 allowed); elbows
    and grips are on the rig's; the bones and skin of the legs are the same to the last digit; the
    waist's skin turns 15% of the twist, the lower chest 45%, the upper chest 89%; the trunk keeps
    0.967 of its girth (0.95 allowed).
  - Two things that measuring found and the twist did not cause. The model's shoulder joint is 50
    to 59 mm inside the rig's shoulder standing and 90 to 95 mm with the arms up, twisted or not
    (the rig's shoulder is the middle of the deltoid), so for shoulders "within 25 mm" is measured
    as the joint's distance from where the turn carries the untwisted one: 0 mm. And with the arms
    up or in a deep bend the untwisted skin is already 44 to 70 mm outside the capsules, which only
    the standing pose had been measured for; a twist adds at most 2 mm there (5 mm allowed).
  - Without a twist nothing moves: the joints, hands, capsules, skin and the drawn figure's canvas
    calls, hashed to the last bit over every clip on three bodies (3,696 frames), are the same
    before and after; the 167 clips pass with the same lines; a bake leaves `web/clips` as it is.
    Broken 19 ways in a scratch copy (the sign, the limit, the shares, a capsule left behind, a
    bone turned about itself and so on), the tests fail 18 times. The one they miss is which way
    the crease of a perfectly straight elbow faces: the rig never makes an arm that straight (it
    stops 2 mm short of full length, which leaves about 9 degrees at the elbow), so no pose reaches
    that line of the model.
  - Looked at on contact sheets from the front, the side and above at 0, 15, 30 and 45 degrees, on
    the standard figure and the heaviest avatar: from above the shoulders are turned and the feet
    are not. The player has no view from above, so those tiles hand the staged pose to
    `Figure3D.render` with a camera over the head. They were made on Chromium with a stand-in for
    Firefox, which the machine used does not have, and are not in the repository.
  - Left for the weave: the body movements in `sim/moves.js` (`bodyKeys`, `carriedClip`) do not
    carry a twist. A twist moved like a point there would fail the body checks, which accept only
    `[degrees, 0, 0]`.
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
