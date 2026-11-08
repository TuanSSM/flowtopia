# GEO: Next-trick suggestions from the geometry of moves

Why, scope and risks: [epics.md](../epics.md#geo-next-trick-suggestions-from-the-geometry-of-moves).
Order: [roadmap.md](../roadmap.md). Each milestone is one commit in the epic's pull request ([implementation plan](../implementation-plan.md)). An item is checked only when
its DoD holds and the [roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item)
is met. All measurement runs at build time in `sim/`, like the physics; the browser only reads the
results.

**Status:** done. 22 of 22 items done, in the GEO pull request; see the notes on M1, M3 and M4 for what changed from the plan.

## Already in place

- [x] Baked clips for 104 skills (168 variants) with every prop's points at 100 frames a second.
- [x] Combo transitions marked Untested, Works, Needs bridge or Blocked by each user.
- [x] Prerequisite graph and readiness (`nextUp`) per user and prop.

## M1. Curves from clips (`sim/geometry.js`)

- [x] **Tip paths.** For each clip variant, the path of every prop's tip (striker end, poi head,
  staff ends, buugeng tips) as a closed curve over one cycle, resampled by arc length.
  DoD: `node test/geometry.js` runs over every variant; a circle of radius r sampled unevenly comes
  back with even spacing within 0.5%.
- [x] **Speed, curvature, torsion.** Discrete curvature from turning angles and torsion from the
  change of the osculating plane, smoothed over a window that is stated in the code.
  DoD: tests on known curves: circle (curvature 1/r within 1%, torsion 0 within 1e-3), helix
  (constant curvature and torsion within 2%), planar figure-8 (torsion 0).
- [x] **Rotation-minimising frame.** A parallel-transport (Bishop) frame along each path, with the
  closing twist over a cycle recorded.
  DoD: test that the frame stays orthonormal to 1e-6 and does not flip at the inflections of a
  figure-8, where the Frenet frame does.
- [x] **Plane and sense.** Best-fit plane normal (unit vector on the sphere) from the path's
  covariance, how planar the path is (ratio of the smallest to largest spread), and the sense of
  rotation from the sign of angular momentum about the normal.
  DoD: F3's wall, face and table variants come out with normals about 90 degrees apart (within
  10). A mirrored clip keeps its sense in the wall plane and flips it in the face and table planes:
  the plan said "opposite sense", but a reflection in x leaves rotation about x unchanged, and the test
  checks what the geometry says.
- [x] **Winding and period.** Winding number of the tip around the hand per cycle, and the period.
  DoD: a figure-8 gives winding 0 and two lobes; a plain spin gives 1.

## M2. States at entry and exit

- [x] **State vector.** At a clip's first and last frame: hand position relative to the body,
  prop orientation as a unit quaternion, angular velocity, plane normal, and which part is in the
  hand (grip).
  DoD: test that a looping clip's exit state equals its entry state within tolerance.
- [x] **Distances.** Plane change as the geodesic angle between normals; orientation change as the
  angle of the relative rotation on SO(3); angular velocity change; hand travel; grip change.
  DoD: tests on constructed states (identity gives 0; a 90 degree turn about one axis gives 90).
- [x] **Shape of a cycle.** Square-root velocity representation of each closed path, and the
  elastic distance between two paths minimised over starting point and rotation in the plane.
  DoD: the distance is 0 within 1e-3 for the same path started at another point, run faster, made
  bigger or tilted, and under 0.02 run backwards. The plan's second check was wrong: F3 and F5 are all
  the same circle (they differ by plane and sense, not shape), which the test now asserts, along with a
  figure-8 being far from every circle.
- [x] **Features file.** `web/clips/features.json`: per variant, the measurements above, rounded,
  written by `node sim/bake.js`, deterministic.
  DoD: baking twice gives identical bytes; CI checks the file is the one the sources build, as it
  does for the bundles.

## M3. Moves without clips

- [x] **Catalog fields.** Optional `motion` on skills: `plane` (wall, face, table, mixed or unknown),
  `direction`, `hands`, `grip` and `source`. Unknown is a valid, explicit value. Named entry and exit
  states were dropped: no description gives them, so they would all be unknown.
  DoD: `docs/catalog-format.md` documents the fields; the validator accepts them and refuses
  unknown names; every value comes from a source or is `unknown`.
- [x] **Fill the nunchaku map first.** `motion` for the nunchaku skills, from the sources and from
  the clips where a clip exists (the two must agree).
  DoD: every nunchaku skill has `motion`; where a skill has a clip, its catalog plane is the clip's or
  `mixed` (`test/geometry.js`). `node sim/motion.js` found two skills whose text describes a variant in
  another plane than the clip shows (F9, R13); they are `mixed`.
- [x] **Edited in MOD's editor.** The editor shows and validates `motion`.
  DoD: tracked in MOD M6; this item is checked when that editor field ships.

## M4. Transition score

- [x] **Score.** A transition score from the exit state of one trick to the entry state of the
  next, from the distances above, with weights from the physics (a plane change costs more than a
  hand move) as the starting point.
  DoD: a pure function with unit tests; the reason strings ("same plane, same direction") are
  produced from the same terms that make the score.
- [x] **Labels, with consent.** A profile setting "Help improve suggestions" (off by default) lets
  the browser send anonymous counts of transition marks: skill pair, prop, and Works, Needs bridge
  or Blocked. No user id is stored with the counts.
  DoD: Go test that the counts table has no user column and that the endpoint refuses when the
  setting is off.
- [x] **Fit at build time.** A script fits the weights by logistic regression on the counts and
  reports the area under the curve (AUC) for Works against Blocked, on held-out pairs.
  DoD: the script is deterministic for the same counts; the report is committed with the weights.
- [x] **Gate.** With at least 30 shared pairs, the fitted AUC must be at least 0.75 and beat a baseline
  that uses only the prerequisite graph. With fewer, as at launch, there is nothing to fit or measure, so
  the weights must be the physics defaults marked early, the flag stays off by default, and the
  interface labels every suggestion as not yet checked against real combos.
  DoD: `test/suggest-gate.js` in CI fails the build when the committed weights break either rule.
- [x] **Bridges.** When no next trick scores above a threshold, the shortest path through tricks the
  user can do (Dijkstra over transition costs), up to 3 steps.
  DoD: unit test on a small graph; the bridge never includes a Blocked transition the user marked.

## M5. In the combo builder

- [x] **Next trick list.** Under the last skill of an open combo: the 5 best next tricks, ranked by
  score, readiness (level 2 or ready to learn) and not already in the combo, each with its reason.
  Behind the `suggest` flag.
  DoD: `suggest.py` opens a combo ending in F5, finds five suggestions each with a reason, adds one,
  and checks the list is absent with the flag off.
- [x] **Honest confidence.** A suggestion based on catalog fields rather than a clip says "from the
  description"; one with little evidence is marked "untested by anyone yet".
  DoD: browser test checks the label on a skill without a clip.
- [x] **Feedback.** "Not a good fit" on a suggestion hides it for this combo and, with consent,
  counts as Blocked.
  DoD: browser test hides a suggestion and it stays hidden after reload.

## M6. Finish

- [x] **Measure use.** The share of suggestions added, and of those later marked Works, shown to
  admins on MOD's dashboard.
  DoD: the two numbers appear on the dashboard with a Go test on fixture data.
- [x] **Docs.** `docs/suggestions.md` explains the measurements, the score, the data used and its
  limits in plain words; README's Combos section mentions suggestions; this file shows every item
  checked.
  DoD: `test/docs.sh` passes.
