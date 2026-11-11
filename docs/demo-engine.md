# How the demos are made

This note covers the demo engine after the "skills without a demo" pass: what changed, what each
kind of clip means, what is checked, and what was tried and left out.

## The pieces

| File | Runs | Job |
| --- | --- | --- |
| `sim/engine.js` | at build time | The physics: nunchaku (two rods and a cord), poi (weight on a tether), contact, throws, bounces. |
| `sim/chain.js` | at build time | A tether or cord as a chain of points that can lie on the body and grip it. No clip uses it yet. |
| `sim/moves.js` | at build time | Families of moves: body movement, figure-8s, hand-over-hand rotors, unit-circle patterns, solved poi paths, still pictures, and the posture search. |
| `sim/specs.js` | at build time | The list of skills that have a demo, with their notes. |
| `sim/compose.js` | at build time | Puts the hands together into one scene: both hands, the body pose and every prop, 100 frames a second. |
| `sim/check.js` | at build time | Measurements on a scene: overlaps, reach, speed, legs, balance, the chest's turn against the hips, turn inside the hand. |
| `sim/bake.js` | by hand after a change to `sim/`, by `./deploy.sh --build`, and by CI to check the committed clips | Writes `web/clips/<hash>.json` and `web/clips/index.json`. `--diff` says how the clips differ from the last commit. |
| `web/demo.js` | in the browser | The player: the jointed figure, the props, labels, views, GIF export. No physics. |
| `test/physics.js`, `test/expect.js` | `node test/physics.js` | Every clip against its physics, its definition, and the body. |
| `test/chain.js` | `node test/physics.js`, or on its own | Known answers for the chain: 55 checks, each with its tolerance and where it comes from. |
| `test/twist.js` | `node test/physics.js`, or on its own | Known answers for the chest turned against the hips: the rig, the capsules, the body checks and the clip format, 26 checks. |

The browser used to run the simulation itself. It now plays clips that were worked out and checked
ahead of time, so what is tested is exactly what is shown, a demo opens at once, and a simulation
can no longer come out differently on another browser.

## Four kinds of clip

- **Simulated.** Gravity, air drag, cord or tether tension, and contact with the body. The hand
  follows a model of the move. Nunchaku swings, stops, figure-8s, bounces and throws; poi spins,
  extensions, isolations and butterflies; and now swings carried by a body that shifts its weight
  or bends its knees.
- **Solved.** For a weight on a tether the only forces are gravity and the pull of the tether, so
  the tether must lie along (acceleration minus gravity). Give the head the path its definition
  names, and that fixes where the hand must be at every instant. Flowers and the cat-eye are made
  this way. The test recomputes the head's acceleration from the finished frames and checks the
  tether lies within a few degrees of the force it needs.
- **Hand-driven.** A rigid prop goes where the hand takes it. Staff, double staff and buugeng moves
  trace the path their definition gives on the jointed figure.
- **Rolling.** Nunchaku rolls. When the hand lets go, the nunchaku turns about the part of the
  body the cord rests on. It is drawn as two sticks in one line that stays against that pivot: a
  balanced rotor, which gravity does not speed up or slow down, turning at half the swing's rate
  because the same angular momentum now carries both sticks. The cord's grip on the skin, friction
  and the opening and closing of the fingers are not simulated, and the plane and direction were
  taken from the wiki's four small animations as far as they can be made out.
- **Still picture.** One frame with labels, for grips, lengths and planes. The view buttons turn it.

## The figure

`rig()` in `web/demo.js` builds the figure from a pose: pelvis, facing, spine, feet and hands.
Shoulders, elbows, hips and knees follow from bone lengths (upper arm 31 cm, forearm 28 cm, thigh
47 cm, shin 46.5 cm). The elbow goes where an arm would put it: down and back when the hand is low
and in front, out to the side overhead, out and back behind the body, forward when the hand is
close across the chest. A move can also say where it wants the elbow. The same joints give the
capsules used for contact (`caps()`), fitted to the 3D model's skin: a head longer than it is wide at
the model's height, a neck, the shoulders and the upper back, hips and buttocks outside the trunk, and
limbs thicker near the body than at their ends. `node test/body3d.js rig` keeps the skin within 25 mm of
them, so the body that props collide with is the body that is drawn ([docs/avatars.md](avatars.md)).

The chest can turn against the hips. A pose may hold a `twist`, written `[degrees, 0, 0]`: three
numbers, like every value of a frame, so a clip stores it, blends it between frames and mirrors it
with everything else (mirroring changes the sign of the first number, and the mirror image of a turn
to the left is a turn to the right). Positive is to the figure's left, as a positive yaw is for the
whole figure: the right shoulder comes forward and the left goes back. The turn is about the line of
the spine, from the pelvis to the base of the neck, on top of whatever lean the pose has. It stops at
45 degrees each way, a little over the 40 or so a back turns (roughly 5 degrees in the lumbar spine
and 35 in the thoracic): the rig draws no more than that, and the body checks fail a clip that asks
for more.

- **What follows the twist.** The shoulders, the base of the neck, the capsules of the chest top, the
  upper back and the deltoids, by all of it. The head by 40%: it takes back 60%, as the 3D head does
  of a lean, so the figure goes on looking where it works and not at its own shoulder. A hand the
  pose leaves out hangs under its shoulder, so it goes round with the chest.
- **What does not.** The pelvis, the hips, the knees, the feet and their capsules do not move at
  all. The capsule of the waist and ribs is round about the spine's line, so it has nothing to turn.
  A hand the pose gives stays where the move puts it: its elbow is solved again from the turned
  shoulder, by the same rule as before in the chest's own frame, and the reach and overlap checks
  judge what comes out. A hand that was in reach can be out of reach once its shoulder has turned
  away, and the reach check says by how much.

A body movement is a function of the phase of the move: weight from foot to foot, down and up by
bending the knees, a full turn in quarter-turn steps with one foot always on the floor. Hands and
props are carried along, and for simulated props the swing is simulated again on the moving body.

The 3D model (`figure/src/figure.js`) is posed from these joints with the anatomy a skeleton
imposes on top. Each limb bone is pointed at the next joint and then rolled about its own axis:
the upper arm so that the elbow bends toward the forearm (a hinge, not a ball joint), the thigh
so that the knee bends toward the shin and points where the toes point, the shin so that its
front faces the toes. The forearm is turned to follow the palm (pronation and supination), held
within 100 degrees of its rest, and whatever is left is wrist flexion and deviation. The
collarbone lifts as the hand goes above the shoulder. The neck and head take back part of the
trunk's lean, so the head stays nearer upright. The fingers curl round what is held, less for a
thick handle. For a close-up grip picture, which has no body, a standing body is placed with its
shoulder straight behind the fist, and everything beyond a plane 25 cm behind the hand is cut
away, so the picture shows the model's hand and forearm with the prop.

A twist is shared over the three spine bones of the model's Mixamo skeleton, from the lowest up:
`Spine` 15%, `Spine1` 30%, `Spine2` 55%. `Spine` sits in the lumbar region (1.14 m up on the standard
figure), and the whole lumbar spine gives about 5 of a back's 40 degrees. `Spine1` is where the
lumbar spine meets the thoracic (1.24 m) and `Spine2` is in the middle of the ribcage (1.33 m): they
share the thoracic part, more of it higher up, where each vertebra turns more. So the skin of the
hips does not turn, the waist turns by 15% of the twist, the lower chest by 45% and the upper chest
by about 90%, and the collarbones and shoulder blades, which hang from `Spine2`, ride on the ribcage
with all of it. Each bone is carried round the rig's line as it turns, not turned about itself (the
model's spine and neck bones lie up to 5 cm behind that line), which is what keeps the model's
shoulder line on the rig's. The neck and the head each take back half of what the head takes back.
The 15% is the lumbar spine's 5 degrees of 40, rounded up; the split of the rest is a judgement, and
on this model, whose trunk is three stiff pieces, sheets with 10, 15 and 75% and with 20, 40 and 40%
looked nearly the same. The
drawn fallback figure is made from the same joints: its shoulders narrow in the front view and part
in the side view, and its nose turns with the head.

`node test/twist.js` holds the rig, the capsules, the checks and the clip format to this on every
body, and `node test/body3d.js rig` holds the model to the rig at 15, 30 and 45 degrees each way:
the legs to the last digit, the shoulder line within 0.1 degrees, the skin of the standing figure
within the same 25 mm of the capsules as without a twist. No move uses the twist yet.

## What is checked on every clip

Physics, as before: stick, cord, tether and staff lengths; slack; loops with no seam; free fall in
throws; bounces that lose speed; nothing fast enough to tunnel.

New, on the composed scene with both hands in it:

- No prop goes more than 8 mm (simulated) or 3 mm (others) into the body, a limb or another prop.
- A held prop does not pass through the arm that holds it, beyond the wrist.
- Forearms stay out of the torso, the head, the legs and each other.
- Each hand stays within reach of its shoulder and under 9 m/s.
- Legs are not stretched past their length, feet stay on the floor and do not pass through each
  other, and the pelvis stays over the line between the feet.
- The chest is turned no more than 45 degrees against the hips. The reach, overlap and balance
  checks above are made on the body as the twist leaves it: turned shoulders, turned chest capsules.
- How far the prop turns inside the hand is measured and reported, not enforced (see below).

Moving the old clips onto this body found faults the old test did not look for: the bounce demo's
other hand was 7 cm beyond the reach of its arm; the butterfly's hands were 15 cm beyond reach and
its forearms passed through each other; the figure-8 only just missed the thigh. All were fixed.

## The posture search

A hand-driven move is fixed by its definition only up to where the hand is held and how the arm is
set. `fitted()` tries a grid of hand positions, lean angles and elbow positions and keeps the one
that leaves the prop clearest. The choice is stored in `sim/fits.json`, so rebuilding gives the
same clip. If nothing on the grid is clear, the move gets no demo.

## The same clips on any machine

A clip's file is named by the hash of its bytes, and `index.json` and `presets.json` list clips by
that name. So a bake gives the committed files only when every byte comes out the same, on any
machine and any version of Node.

**What differed.** Arithmetic and square roots give the same bits on every JavaScript engine.
Sines, cosines and angles (`Math.sin`, `Math.cos`, `Math.atan2`) need not: an engine may be one
unit in the last place off, and engines differ in where. A bake calls them more than 21 million
times, most of them inside simulated swings where each step feeds the next, so two engines end up
with slightly different swings. Positions are stored to 0.1 mm, and the frames came out the same
to the byte. The length of a simulated loop (`dur`) is found by the simulation, from the part of a
step at which the repeat ends, and was stored as it came. Between the committed clips and a bake
with Node 22 on an x86 machine it differed by up to 7.2e-13 s in all 41 simulated loops that end
part-way through a step, and with it `trail`, the hash, the file name and the entries in the two
lists. Nothing checked this, because CI never baked.

There is one engine at hand and not two, so the second one is stood in for: every sine, cosine and
angle is made wrong by one unit in the last place, up, down or not at all. That gives the same
picture as the committed clips did (the same 41 loops, `dur` off by up to 1e-12 s, every stored
frame the same), and it is where the other figures in this section come from. Positions then
differ by a few millionths of a nanometre as a rule and by 1.5 nm at most.

**What is rounded** when a clip is packed (`pack` in `web/demo.js`), so that a difference in the
last bits does not reach the file:

| Number | Step | Why that is fine enough |
| --- | --- | --- |
| Positions: frames, labels, planes | 0.1 mm | A twentieth of a pixel on a figure drawn 1,000 pixels tall. |
| `dur` | 1 microsecond | The frames fix the timing no finer than 10 microseconds (0.1 mm at 10 m/s). A loop of half a second played for ten minutes ends under 1 ms from where it would be. |
| Every other number: `trail`, sizes, zoom, label offsets | 0.0001 | For `trail`, a part of the loop, that is under 1 mm of the longest path a tip draws. The rest are written in the move list with fewer decimals. |

The lists beside the clips were rounded already: `d` in `index.json` to 0.01 s, every measure in
`features.json` to 0.001 (and measured from the stored clips, not from the simulation), and the
reasons in `presets.json` are text with a depth in whole millimetres and a whole percentage.
`sim/fits.json` holds values from the search grid as the move list writes them, under names made
of a family, a direction and a count, so it reads the same on every engine and a bake with the
committed file never searches.

One thing in `presets.json` was a matter of last bits without being a number. The reason a clip is
not offered on a body names the worst overlap and how far through the clip it is. A loop reaches
the same depth two or four times, and which of them is deeper in the last bit decided the
percentage. The name now moves on to a later moment only when the overlap there is deeper by more
than a micrometre (`note` in `sim/check.js`); the depth that is compared with the tolerances is
still the deepest, to the last bit. That changed the percentage in 32 of the 348 reasons and
nothing else.

**Whole-number decisions** were tested with the same stand-in, several times over.

- How many steps a repeat takes. The repeat ends part-way through a step, and the count of whole
  steps and the part are added, so the length does not jump when the count changes. It does change
  for one clip: the hand model holds `nunchaku:M4` to the rate it wants so exactly that its two
  turns take 3,360 steps to within 2e-11 of a step. One run counts 3,361 samples and nearly a whole
  step more, another 3,362 and nearly nothing more; `dur` is 2.24 s either way, within 1e-12 s.
- How many steps a swing takes to settle. This one would move every frame of the clip by a step.
  The nearest any settling comes to a step is 1.3e-4 rad, against differences of 1e-11 rad.
- How many frames a clip has. The nearest any loop's length comes to another count is 0.03 of a
  frame.

**What rounding cannot rule out.** A number that lies within the engines' difference of the middle
between two steps rounds up on one engine and down on another. Among today's clips the nearest
`dur` is 8e-8 s from such a middle, against a difference of 1e-12 s. No position that differs is
nearer than 1.9 nm to one, and none is nearer than about 12 times its own difference. A later
clip can land closer. The check below will say so.

**The check.** The unit job of CI runs `node sim/bake.js` (about 40 s; the posture search does not
run, because every posture is in `sim/fits.json`) and fails when `git status` shows a change in
`web/clips` or `sim/fits.json`. It then prints `node sim/bake.js --diff`: each clip that differs
by move and variant, with the first field or stored number that differs and both values in full.
`node test/physics.js` compares `presets.json` with its own run of the body checks only for clips
that are baked as they are now; for any other it says `SKIP` on the clip's line and counts it in
the last line. It does not fail for that, because a move being worked on is tested before it is
baked; the bake check is what fails.

**When the check fails.** Run `node sim/bake.js` and look at `git status web/clips sim/fits.json`.

- It shows changes: the clips were not baked after the last change to `sim/` or to `pack`. Commit
  them, with `docs/demo-coverage.md` after `node test/coverage.js`.
- It shows none: your engine and CI's give different bytes from the same sources. The log of the
  failed step names the clip and the number. A stored position that differs by 0.0001 lies on a
  rounding middle: move the clip by something that does not matter (its start, a hand position by
  a millimetre) and bake again. Anything larger, or another frame count, is a decision that falls
  differently on the two engines, and is a fault in the move or the engine to find, not to round
  away.

Baking twice on one machine gives the same bytes, and so does a bake with every sine, cosine and
angle wrong in the last place, or with every `dur` moved by 1e-12 s. Whether Node 24 on CI's
processor gives the committed bytes is what the first run of the check shows.

## A tether that touches: the chain

A wrap needs the line itself to lie on an arm and hold there. The engine's tether is one stiff link
from the hand to the head, and its cord one link from the handle to the striker, and neither can
touch anything. `sim/chain.js` is the line as a chain of points. A wrist model that carries `chain`
gets it in place of the stiff link (`makeMass` for a poi, `makeSim` for a nunchaku, where the
striker's two masses ride on the last link). Without `chain` nothing changes: the bake of this
branch and the bake of the commit before it give the same bytes for all 167 clips.

**What it models.** Points 10 mm apart joined by links that keep their length (or stretch a stated
amount per newton, if asked); gravity and drag; contact with every capsule of the body except the
arm that holds the line (the body checks leave that arm out too, because the line leaves from a
fist with no wrist; `own` in the option adds it); and Coulomb friction, sticking and sliding,
against the capsule's own motion where the point touches, so a line on a moving arm is carried by
it. The head of a poi and the striker meet the body as they did before. What the line touched in
a step, in any of its substeps, goes into the clip's contact list, which the suite reads.

**The method** is position-based dynamics with small steps (XPBD). Each engine step of 1/1500 s is
cut into 4 substeps. In each: the points move; the link lengths are put right; points inside a
capsule are moved out the shortest way (the side or the round end by the same rule, so there is no
seam); then what a point moved along the surface is taken back, in full while it is under the
friction coefficient times how far the point was pushed out (it sticks), and by that much when over
(it slides). Two things differ from the textbook form, both measured:

- The lengths are solved all at once. In a chain a link shares a point only with its two
  neighbours, so the equations are tridiagonal and one sweep solves them exactly. Correcting links
  one after another lets a 90 g head stretch a 10 g tether; this does not.
- The points are moved along the directions the links had when the substep began, not the ones
  they end with. The second way lost 0.47% of a 60 degree swing's energy every second at this
  step (4.7% over 10 s, in proportion to the substep); the first loses none that adds up (0.03%).

Step, substeps, sweeps and the order of points and capsules are fixed, there are no random numbers,
and nothing is read by the order of an object's keys: the same run hashes to the same bytes twice,
and again with a capsule's fields written in another order.

**The numbers, and where they come from.**

| Choice | Value | Bound it comes from |
| --- | --- | --- |
| Link | 10 mm (55 for a tether, 9 for a cord) | The middle of a link cuts inside a round part by link squared over 8 times (part radius + line radius): 0.26 mm on the thinnest part of the body the engine passes (the shin above the ankle, 45 mm), 0.23 mm on a forearm (50 mm). A nunchaku handle (22.5 mm, 0.47 mm) is not passed yet: a cord does not meet its own handle. |
| Substep | 1/6000 s | Links and contact are solved in turn, so a line pressed on a part gives a little: substep squared times wrap angle times link angle over point mass, 0.17 mm per newton for a full turn on a forearm. A poi spinning pulls about 5 N. Three substeps would give 1.8 times that. The limit below also hangs on it. |
| Sweeps | 1 per substep | The substeps are the iterations. What a second sweep would remove is 0.001 mm a link at 5 N on a forearm, a twenty-sixth of the give. Two were tried: every check comes out the same. |
| Line | 4 mm half thickness; 10 g tether with a 90 g head; 5 g cord with 150 g sticks; friction 0.3 | Assumed, not measured. The thickness is the one the body checks use for a tether. |

**The limit, and what keeps a line out of a part.** The points of a taut line shake sideways faster
the harder it is pulled, and the substep follows that only up to point mass times link over substep
squared: 66.7 N for the tether, 225 N for the cord. A substep in which any link is pulled harder,
or comes out not a number, throws, and the clip fails with that error. The chain never hands back a
state worked out past its limit.

That limit, and not a bound on speed, is what keeps a fast line out of a part. Shown, for a loose
10 g line thrown broadside across a part, meeting it at its middle or a third along, with friction
0, 0.3 and 0.6 (six throws for each line of the table), on the thinnest part of the body and on one
as thin as a handle:

| Speed | 45 mm part | 22.5 mm part |
| --- | --- | --- |
| 9 m/s (the fastest a hand may move) | all 6 fold round it; pull 25.7 N at most; points go 3.8 mm in at most; nearest link 0.27 mm inside (0.34 mm allowed) | all 6 fold; 16.4 N; 2.4 mm; 0.52 mm (0.62 mm allowed) |
| 16.9 m/s (the fastest a prop may) | 5 fold, 1 stopped by the limit; nearest link 0.31 mm (0.42 mm) | all 6 fold; 52.3 N; 0.67 mm (0.78 mm) |
| 25.9 m/s (the two together) | all 6 stopped | all 6 stopped |

The allowance is what the middle of a link cuts in when the link is longer by one substep of
flight, which it can be at the blow. In none of the 36 throws was a line handed back with a link
through the part. With the limit switched off the same throws run away and end with links through
the axis; that is the mutation the check is there for. The clips' own fastest prop point moves at
16.6 m/s and their fastest hand at 6.1 m/s. So: up to 9 m/s a line is shown to fold round a part
for any friction from 0 to 0.6; faster, it may be stopped instead, and it is not shown to fold.

A stiff line that snaps taut is the same limit seen another way: it stops a head moving away in a
single substep, a pull of head times speed over substep, 65 N at 0.12 m/s. Faster than that a stiff
90 g poi is stopped (shown at 0.44 m/s, 238 N). A line given some stretch takes the snap as a
spring does: with 0.1 mm per newton the same 0.44 m/s peaks at 13.5 N. Wraps will probably need
that stretch, or more substeps, which raise the limit by their square.

**Known answers** (`node test/chain.js`, 70 checks). Each tolerance is worked out from the link,
the substep or the set-up, in a comment beside the check.

| Set-up | Expected | Got | Allowed |
| --- | --- | --- | --- |
| Laid flat over a smooth cylinder, equal loads, let go: the arc it lies on | 18 points, 17 links, 180.63 degrees (the only count that fits straight links and mirror halves) | 18 points, 180.73 degrees | 0.2 degrees |
| The same: symmetry, hanging length, tension | mirror halves; 190.30 mm; 0.9810 N | within 0.001 mm; 190.34 mm; 0.9805 N | 0.01 mm; 0.1 mm; 0.5% |
| No point or link inside the cylinder | points on the surface; link middles 0.232 mm in | 0.000 mm; 0.233 mm | 0.000001 mm; 0.242 mm |
| Friction 0.2, a quarter turn: pull at which it slides, times the load | 1.3758 (a smooth rope: 1.3691) | 1.3757 (-0.01%) | +0.10%, -0.12% |
| Friction 0.2, half a turn | 1.8781 (1.8745) | 1.8779 (-0.01%) | +0.10%, -0.14% |
| Friction 0.5, a quarter turn | 2.2099 (2.1933) | 2.2084 (-0.07%) | +0.10%, -0.16% |
| Friction 0.5, half a turn | 4.8382 (4.8105) | 4.8283 (-0.21%) | +0.10%, -0.29% |
| Sliding along a part at 1 m/s, sliding friction 0.5; on a part with its own 0.2 | stops in 101.94 mm; 254.84 mm | 101.85 mm; 254.76 mm | 0.2 mm |
| At rest, sticking friction 0.3, pushed at 0.25 g; at 0.35 g | stays; slides 66.22 mm in 0.3 s | 0.0000 mm; 66.25 mm | 0.001 mm; 0.2 mm |
| Hanging free, a 5 degree swing | 1.47509 s, the equivalent pendulum (540.2 mm) | 1.47524 s (+0.01%) | 0.1% |
| The same through the engine, stiff tether against chain | they differ by the tether's weight, -0.90% | -0.89% | 0.1% |
| Energy of a 60 degree swing over 10 s, no drag | kept | 0.03% of the swing | 1% |
| One turn round an upright capsule, pulled tight: whole length; length round it | 645.05 mm; 339.46 mm (338.81 mm without the pitch) | 645.04 mm; 339.42 mm | 0.06 mm |
| The same: evenness of the pitch; the pitch; pull along the axis in tail and turn; tension | even; 21.06 mm; equal; 4.295 N | 0.0006 mm; 21.01 mm; 0.01% apart; 4.302 N | 0.005 mm; 0.12 mm; 0.05%; 3% |
| Pushed along a level capsule at 0.5 m/s2 | 90 mm after 0.6 s; the seam at 0.8944 s | 90.03 mm; 0.8947 s | 0.1 mm; 1 ms |
| Over the round end | every touching point 54 mm from the end's centre; never slowed, and pushed on; off before the tip; then free fall at (0.5, -9.81, 0) m/s2 | within 0.003 mm for 59 steps; 0.000 mm/s lost, 2.0 mm/s gained; off at 23.9 mm of 54 mm, 0.9367 s; (0.5000, -9.8100, 0.0000) | 0.01 mm, 30 steps; 1 mm/s, over 0.1 mm/s; 10 mm short of the tip; 0.01 m/s2 |
| A snap: 90 g moving away at 0.44 m/s, 0.1 mm of stretch per newton | peaks at 13.50 N | 13.52 N | 3% |
| The friction test on a cylinder moving at 2.5 m/s | holds and slides at the same pulls | it does, and stays within 0.000 mm of the cylinder along its axis | 0.1 mm |
| A cylinder rising at 3 m/s2 | carries the load at 1.2810 N, and the load rises with it | 1.2801 N; 0.04 mm behind after 1.5 m | 0.5%; 0.3 mm |
| Through the engine: a poi spin and a nunchaku swing settle and loop; a tether past its own arm | tether and cord their lengths; 0.7 s and 0.56 s a turn; the arm is touched only when asked for, and then listed | 549.99 to 550.00 mm, cord 90.19 mm; 0.700 s, 0.560 s; as expected | 1.5 mm; 2%; exact |

The friction limits are compared with what a chain of straight links holds, not with the smooth
rope's exp(friction times angle), which they approach as links shorten. A point with a link either
side holds (c + mu s) / (c - mu s), c and s the cos and sin of half a link's angle. The first
point on the cylinder has only its own link's half turn. The last has that and whatever of the wrap
is left over after whole links, which the surface's push does not halve, so it holds
(c + mu s) / (cos psi - mu sin psi). That last term is why a quarter turn (8.47 links) sits 0.49%
and 0.76% over the smooth rope while half a turn (16.94 links) sits 0.20% and 0.58% over. What is
left, all on the low side, is the solver's give moving the last point: up to 0.02% to 0.19%, which
is in the allowance and is what was found.

Corrected after a first run or after review, each because the reasoning was wrong or the check said
more than it tested, not to pass:

- The friction limits were first compared with the smooth rope at 0.6% to 1.1%, with a half per
  cent for "the rest"; the comparison is now with the links' own limit at 0.1% and the give.
- The drape was first checked for "180 degrees", which any line with two hanging ends shows; it is
  now the arc it lies on, counted in points.
- The turn round a capsule was first held to the smooth figures at 0.6 mm and 1 mm, wider than the
  0.65 mm the pitch adds; it is now held to the links' figures at 0.06 mm.
- The round end was first tested at 2 m/s2, where the line flies off the seam and never rides it;
  a capsule with no round end passed. It is now ridden at 0.5 m/s2.
- The pendulum is compared with the equivalent pendulum of a tether that has weight, because a
  tether light enough to ignore is past the limit.
- "A line cannot pass through a part" was claimed from one throw at friction 0.3. The table above
  is what is shown, and the limit is now in the chain, strict, and tested.
- The energy bound of 1% was first missed by the textbook form (4.7%), which is why the directions
  are taken as they are.

Each of these was checked by breaking the engine on purpose in a scratch copy and seeing the test
fail: 25 changes (no round end, friction half a per cent off either way, contact 0.02 mm out, the
limit off, contacts read from the last substep only, the holding arm's rule, and so on), all caught.

**What it costs.** One simulated second of a poi spin costs about 50 ms with the chain and 16 ms
with the stiff tether on the machine this was written on, about 3 times (2.5 to 3.4 over several
runs; the test checks that ratio, under 6, and not the milliseconds). That machine ran the suite in
43 s where the Pi takes about 150 s, so times here are multiplied by 3.5 for the Pi; this was not
measured on a Pi. A chain clip that settles and records in 33 simulated seconds is then about 1.6 s
here and 6 s on the Pi. The known answers take 8 s on their own, and `node test/physics.js` took
49 s and 51 s in two runs with them: about 180 s on the Pi, against the 240 s the epic allows.

That leaves about 60 s on the Pi, or ten chain clips at this cost, and the ratio limit of 6 in the
test was set for six. The chain is hoped to carry about 30 skills
([contact-review.md](contact-review.md)); at 6 s each that alone is 180 s and would pass the
allowance. The next item has to settle chain clips for less (a shorter settle, a start nearer the
repeating swing, fewer capsules handed to the line) or the allowance has to be looked at again.

**What it does not model yet.**

- The line touching itself: two turns of a wrap pass through each other.
- A capsule turning about its own axis (a forearm rolling over): only the motion of its two ends
  is seen. Friction on a moving capsule is tested at steady speed and at steady acceleration, not
  on one that swings.
- Twist in the line, and any stiffness in bending.
- The head and the striker on the surface: they keep their own contact (a bounce), so a head lying
  on an arm at the end of a wrap is not yet held there by friction as the line is.
- A line meeting a handle: no handle is passed to it. The cord does not meet its own.
- Stops and throws with a chain: they rewind the swing, and the chain keeps no copy of itself.
- The points of the line in a clip: the frames still hold the hand and the head only, so the
  player draws the line straight. Showing it bent is part of the next item.

## Tried and left out

- **Double staff shoulder reel (W2).** With the body fitted to the drawn model, the short staff
  reeled beside the shoulder sweeps through the chest or its own upper arm from every hand position
  the arm can reach; the hip reel (W1) stays, beside and in front of the hip.
- **Poi weaves and one-poi crossings (tic-tacs).** Carrying the hand from side to side with the
  existing hand model stalls the poi or sends it into the legs. A search over height, distance,
  width and timing (64 settings each for two and three beats) found none that kept turning clear.
  The hand needs to steer the plane of the poi, not only its speed.
- **Poi weaves, second attempt.** Solving for the hand (as for flowers) removes the stalling, and
  the forces come out fine, but with the body standing square no setting kept the poi clear: on
  the far side the tether passes through the other shoulder, or the reaching arm goes through the
  chest (best case 26 mm in, over 1,500 settings for two beats; three beats were always out of
  reach). A real weave turns the upper body toward the side the poi are on. The figure could not
  turn its chest against its hips then. It can now (the `twist` of a pose, see "The figure"), with
  the body checks made on the turned body; no move uses it yet, and the weave with a turning upper
  body is the next item of the SIM epic.
- **Turning with a spinning nunchaku.** The plane has to be turned with the body, and the wrist
  model cannot push it sideways, so the striker falls behind and hits the arm. Turning with a poi
  now works, because the poi's hand is solved for instead.
- **Nunchaku butterfly and alternating figure-8, staff rotor behind the back.** The two nunchaku
  hit each other's handles within arm's reach, and behind the back the staff lies through the
  forearm that holds it.
- **Long-staff figure-8.** With a 1.4 m staff no hand position on the grid kept the staff out of
  the torso (the best was 60 mm in). It does work for the 84 cm double staff.
- **Cat-eye.** Solving for the hand gives exact physics, but the hand then no longer circles
  against the poi as the definition says: with even timing the textbook cat-eye is not a motion a
  tether can produce. A real one is uneven (the sources mention a tug to pop the head up).
- **Horizontal cat-eye and isolation against extension.** The solved hand had to move at 12 to
  15 m/s, which is not a hand.
- **Shoulder-height crossed arms (buugeng), both-side buugeng isolation.** The props or the arms
  passed through each other.

## Where it is still weak

1. **Nothing wraps or rolls yet.** A tether or cord can now be a chain that touches the body (see
   above), but no move uses it, and there is no rolling contact, so wraps, wrist rolls and all
   contact staff are still out. This is the largest group left (about 110 skills).
2. **The hand holds but does not work.** The 3D hand has a thumb and fingers that close round
   the prop, and the forearm turns with the palm, but there is still no finger model in the
   physics: the test measures how far a prop turns inside the hand (often 360 degrees or more
   for staff and buugeng) and the caption says finger work is not shown. The drawn fallback
   figure's hand is a fist.
3. **Hand-driven moves have no forces.** They are checked against the body, not against what a
   hand can push. Timing is even, where a real spinner speeds up and slows down.
4. **The poi hand model only regulates speed.** It cannot steer the plane, which is what blocks
   weaves, turns, crossers, reels and corkscrews: most of the poi list.
5. **Solved clips fix the head, not the hand.** The hand path comes out near the textbook circle
   but not on it, and nothing limits the force, only the speed.
6. **Steps are simple.** Turns use quarter-turn steps on the spot. There is no travel, no pivot on
   one foot. The chest can turn against the hips, but no move asks it to yet, and the families of
   body movement in `sim/moves.js` do not carry a twist.
7. **The body is capsules.** They are fitted to the drawn model within 25 mm, but a flat back or a
   bony knee is still a round shape, so a prop may hover a centimetre or two off the skin.
8. **The posture search is a coarse grid.** It finds a clear posture, not the one a teacher would
   show.
9. **Passes still missing.** Nothing changes hands except by a throw or in the staff rotor.
