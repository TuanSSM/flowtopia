# Next-trick suggestions

How the combo builder decides what can follow the last trick, what it measures, what data it uses,
and where it is weak. Behind the `suggest` flag, on by default (until enough marks are shared, every suggestion says it is early).

## What is measured, and where

Everything is measured at build time from the baked clips, so the browser only reads numbers.
`node sim/features.js` (also run at the end of `node sim/bake.js`) writes `web/clips/features.json`;
CI fails if the committed file is not what the clips give. The maths is in `sim/geometry.js` and is
checked on known curves first (`node test/geometry.js`): a circle's curvature is 1/r, a helix has the
textbook curvature and torsion, a flat figure-8 has none.

For each prop of each clip variant:

| Measure | How | Why it matters |
| --- | --- | --- |
| Plane | Normal of the best-fit plane of the tip's path (smallest spread of its covariance), as a line, not an arrow | A trick in another plane needs the wrist to turn the prop over |
| Flatness | Smallest spread over largest | Whether "the plane" means much |
| Sense | Sign of the angular momentum about the normal | Which way it spins; reversing costs speed |
| Curvature, torsion | Turning angle between neighbouring segments; turning of the binormal about the tangent | How tight and how twisted the path is |
| Frame twist | Rotation-minimising (parallel transport) frame carried round one lap; the Frenet frame is not used because it flips at every inflection of a figure-8 | Zero for flat loops; non-zero paths are hard to link |
| Winding, lobes | How often the tip goes round the hand; sign changes of the in-plane curvature | Spins wind once; figure-8s wind zero with two lobes |
| Entry and exit | Hand position from the pelvis, prop orientation as a unit quaternion, angular velocity over one frame, grip | What the next trick must start from |
| Shape | 32 points of the path in its own plane | Comparing whole cycles |

Shape distance compares square-root velocity functions of two closed paths, each resampled by arc length
and scaled to unit length, minimised over the starting point, the rotation in the plane, the side of the
plane and the direction of travel. Arc-length resampling stands in for the full search over
reparametrisations that elastic shape analysis does; for paths traced at a steady pace that is enough.
The tests found that the F3 and F5 swings are all the same circle, so they are told apart by plane and
sense, not shape; a figure-8 is far from every circle.

Skills without a clip use the `motion` field in their catalog: plane, direction, hands and grip, each a
value from a fixed list or `unknown`. `node sim/motion.js` proposed them for the nunchaku map from the
clips and from each skill's own text; nothing came from general knowledge. Where a skill's text describes
variants in more than one plane (F9, R13) the plane is `mixed`. Most values are unknown because the
descriptions do not say; that is reported, not filled in.

## The score

`web/suggest.js`, shared by the browser and `node test/suggest.js`. For each pair of variants it takes
the exit state of the first trick and the entry state of the second and adds weighted terms, each from 0
(no change) to about 1:

- **plane**: the angle between the planes over 90 degrees;
- **sense**: 1 when the spin reverses in the same plane;
- **hand**: how far the hand must move, over 50 cm;
- **omega**: the change in angular velocity, over two turns a second;
- **grip**: 1 when the grip changes.

The score is `exp(-cost)`, and the best pairing of variants is used and named ("Upspin into Upspin").
The reasons shown are made from the same terms ("same wall plane", "the spin reverses", "the hand moves
40 cm"). Without clips the terms come from the catalog fields and are coarser; such suggestions say
"from the description", or "little is known about it" when one side is unknown. In the combo builder
the score is multiplied by readiness (1 for a clean skill, 0.9 learning, 0.8 ready to learn, 0.35 locked)
and by the shared marks for the pair.

**Bridges**: when nothing links well, the cheapest chain of up to three links through skills the user has
clean, never through a transition they marked Blocked (Dijkstra over the transition costs).

## The data, and consent

People who tick "Help improve next-trick suggestions" in their profile share their transition marks.
The browser keeps what it last shared in the progress document and sends only the difference, so a
changed mark moves a count, a deleted combo takes its marks back, and unticking takes everything back.
Without consent the server accepts only those withdrawals (a change back to untested), since the page
sends them after the profile has already turned sharing off.
The server keeps counts per pair of skills (`transition_counts`) with no user column; a test checks the
table has none. "Not a good fit" on a suggestion counts as Blocked when sharing is on.

Usage (suggestions shown, added, and later marked Works) is counted per week with no user, for the
dashboard.

## Fitting, and the gate

`node sim/fit-suggest.js counts.json --write` fits the weights by logistic regression (weights kept at
zero or above, so more change never helps) on the counts an admin exports from `GET /api/suggest/counts`,
holds out a fifth of the pairs (chosen by a hash, so it is deterministic), and reports the area under the
ROC curve for Works against Blocked, next to a baseline that knows only the prerequisite graph.

`node test/suggest-gate.js` runs in CI. With at least 30 shared pairs, the committed weights must reach an
AUC of 0.75 and beat the baseline. With fewer, as now, nothing can be fitted or measured: the weights must
be the starting ones from the physics and marked early, and the combo builder says its suggestions are not
yet checked against real combos.

The admin dashboard counts toward that gate: the pairs of skills that someone marked Works or Blocked, of
the 30 needed, and the week 30 would be reached at the pace of the last four weeks (or that there is no
date, when none were first marked in them). A pair keeps the week it first counted and nothing finer.

## Limits

- 89 of 411 skills have clip measurements (104 have demos; stills have no motion). The rest rely on
  catalog fields, and most of those are unknown.
- Clips are recorded for the right hand; the left hand mirrors them (a reflection keeps the sense in the
  wall plane and flips it in the face and table planes, which a test checks).
- Hand position is measured from the pelvis of a standard body; avatars' body presets do not change it.
- The fitted weights describe what this community marks, which may differ elsewhere.
