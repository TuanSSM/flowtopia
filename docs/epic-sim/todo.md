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
  - Built. The pose's `twist` is `[degrees, 0, 0]`, positive to the figure's left, about the line
    of the spine. The rig stops at 45 degrees (clamped), and the body checks fail a clip that asks
    for more. The shoulders, the base of the neck, the chest's capsules and a hand the pose leaves
    out follow all of it and the head 40%; the hips, the legs and a hand the pose gives do not move
    ([demo-engine.md](../demo-engine.md#the-figure)). On the model `Spine`, `Spine1` and `Spine2`
    take 15%, 30% and 55% of it, the neck's base rides on the ribcage, and the head stays over the
    spine's line. No move has a twist yet, and no clip changed.
  - Open, and why the box is not ticked. "Within 25 mm at full twist" holds for the standing
    figure (23 mm outside the capsules at every twist on three bodies, as without one) and not in
    every pose. In a deep bend with the hands held still, the right upper arm of the tall broad
    body is 21.7 mm outside its capsules untwisted and 26.7 mm with the chest turned 45 degrees
    (25.5 mm at 30 degrees; the left arm the same at -45 and -30); on the standard body it is
    23.3 mm untwisted, 25.8 mm at 15 degrees and 25.7 mm at 45. That is 8 of 72 cases, 1.7 mm over
    at most. The twist does not put the skin there: the same upper body without a twist (the hands
    and elbows carried back round the spine) is as far out to 0.1 mm. Hands held still while the
    shoulder turns bend the arm into another shape, and the capsules fit some shapes of an arm
    worse than others, twist or no twist: a raised upper arm is 44 to 57 mm out untwisted, the
    head in a deep bend 41 to 70 mm, a thigh in a lean of 10 degrees 25.4 to 29.4 mm. Only the
    standing pose had been measured before this item. Nothing that leaves the untwisted figure as
    it is can close the gap, so the box waits for one of two things: arm capsules fitted to a bent
    arm, which moves what every clip is checked against and is a piece of work of its own, or a
    decision that the 25 mm is meant for the standing pose, which is what `rig` measured when the
    line was written. `node test/body3d.js rig` lists the 8 cases and fails on any that is not an
    upper arm as far out untwisted.
  - `test/twist.js`, run by `node test/physics.js`: 26 known answers, 0 failed, 0 skipped, each
    worked out from the definition with the test's own rotation, on 9 bodies, 4 poses and 15, 30
    and 45 degrees each way (216 cases). The shoulder line is turned against the hip line by the
    twist within 0.00000000000002 degrees (0.000001 allowed); the hips, knees, feet and their
    capsules are the same to the last digit; the chest's capsules are carried within 0.000001 mm;
    a hand the pose leaves out goes round the spine with its shoulder, leaning or upright. 12 of
    the 432 hands go out of reach and the reach check names each; a staff 30 mm clear in front of
    the right shoulder is 28 mm inside it once the chest has turned 45 degrees, and the overlap
    check says so.
  - `node test/body3d.js rig`, on the standard, the short lean and the tall broad body, 90 cases:
    the model's shoulder line is within 0.001 degrees of the twist (0.1 allowed); the bones and
    skin of the legs are the same to the last digit; the neck's joint is where the ribcage carries
    it and the head's joint where the rig has the head, within 0.01 mm, the neck leaning 8 degrees
    and giving 1.7 mm of its length at a full twist (2 mm allowed); the hanging arms, and an arm
    put dead straight, go round with the chest within 0.0003 mm; no part is more than 0.7 mm
    further out than the larger of 25 mm and what it is on the same upper body untwisted (1 mm
    allowed); the waist's skin turns 15% of the twist, the lower chest 45%, the upper chest 89%;
    the trunk keeps 0.967 of its girth (0.95 allowed). Elbows and grips are on the rig's by
    construction, and the test says so.
  - The model's shoulder joint is 50 to 59 mm inside the rig's shoulder standing and 90 to 95 mm
    with the arms up, twisted or not (the rig's shoulder is the middle of the deltoid), so the
    joint is measured against where the turn carries the untwisted one: 0 mm.
  - Without a twist nothing moves: the joints, hands, capsules, skin and the drawn figure's canvas
    calls, hashed to the last bit over every clip on three bodies (3,696 frames), are the same
    before and after; the 167 clips pass with the same lines; a bake leaves `web/clips` as it is.
    Broken 26 ways in a scratch copy (the sign, the limit, the shares, a capsule left behind, a
    bone turned about itself, the neck off the ribcage, a straight arm's crease, a rounded message
    and so on), the tests fail each time. A reviewer's own list found two more that the first
    version missed: the straight arm's crease, now covered, and a key for the twist in the scene
    builder that nothing read, now removed.
  - Looked at on contact sheets from the front, the side and above at 0, 15, 30 and 45 degrees, on
    the standard figure and the heaviest avatar: from above the shoulders are turned and the feet
    are not; the neck's ball stays seated in the collar and the head sits over the hips. The
    player has no view from above, so those tiles hand the staged pose to `Figure3D.render` with a
    camera over the head. They were made on Chromium with a stand-in for Firefox, which the
    machine used does not have, and are not in the repository.
- [ ] **Poi weave.** The solved hand with a turning upper body; two and three beat. DoD:
  `node test/physics.js` green; the tether clear of the far shoulder; the caption names what is
  simulated.
  - Needed first: a limit on how fast the chest turns, with its test. No body check has one. With
    the hands given and the twist going from -30 to +30 degrees between two frames 10 ms apart,
    the shoulders move at 20 m/s and no check says so: the hand speed limit (9 m/s) does not see
    it, because a hand the pose gives stays where it is. None was added with the twist, since the
    number has to come from a source and no limit the checks already have is of that kind.
  - Also needed: the body movements in `sim/moves.js` (`bodyKeys`, `carriedClip`) do not carry a
    twist. A twist moved like a point there fails the body checks, which accept only
    `[degrees, 0, 0]`.
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
