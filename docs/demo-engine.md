# How the demos are made

This note covers the demo engine after the "skills without a demo" pass: what changed, what each
kind of clip means, what is checked, and what was tried and left out.

## The pieces

| File | Runs | Job |
| --- | --- | --- |
| `sim/engine.js` | at build time | The physics: nunchaku (two rods and a cord), poi (weight on a tether), contact, throws, bounces. |
| `sim/moves.js` | at build time | Families of moves: body movement, figure-8s, hand-over-hand rotors, unit-circle patterns, solved poi paths, still pictures, and the posture search. |
| `sim/specs.js` | at build time | The list of skills that have a demo, with their notes. |
| `sim/compose.js` | at build time | Puts the hands together into one scene: both hands, the body pose and every prop, 100 frames a second. |
| `sim/check.js` | at build time | Measurements on a scene: overlaps, reach, speed, legs, balance, turn inside the hand. |
| `sim/bake.js` | `./deploy.sh` | Writes `web/clips/<hash>.json` and `web/clips/index.json`. |
| `web/demo.js` | in the browser | The player: the jointed figure, the props, labels, views, GIF export. No physics. |
| `test/physics.js`, `test/expect.js` | `node test/physics.js` | Every clip against its physics, its definition, and the body. |

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
capsules used for contact, so the body that props collide with is the body that is drawn.

A body movement is a function of the phase of the move: weight from foot to foot, down and up by
bending the knees, a full turn in quarter-turn steps with one foot always on the floor. Hands and
props are carried along, and for simulated props the swing is simulated again on the moving body.

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
- How far the prop turns inside the hand is measured and reported, not enforced (see below).

Moving the old clips onto this body found faults the old test did not look for: the bounce demo's
other hand was 7 cm beyond the reach of its arm; the butterfly's hands were 15 cm beyond reach and
its forearms passed through each other; the figure-8 only just missed the thigh. All were fixed.

## The posture search

A hand-driven move is fixed by its definition only up to where the hand is held and how the arm is
set. `fitted()` tries a grid of hand positions, lean angles and elbow positions and keeps the one
that leaves the prop clearest. The choice is stored in `sim/fits.json`, so rebuilding gives the
same clip. If nothing on the grid is clear, the move gets no demo.

## Tried and left out

- **Poi weaves and one-poi crossings (tic-tacs).** Carrying the hand from side to side with the
  existing hand model stalls the poi or sends it into the legs. A search over height, distance,
  width and timing (64 settings each for two and three beats) found none that kept turning clear.
  The hand needs to steer the plane of the poi, not only its speed.
- **Poi weaves, second attempt.** Solving for the hand (as for flowers) removes the stalling, and
  the forces come out fine, but with the body standing square no setting kept the poi clear: on
  the far side the tether passes through the other shoulder, or the reaching arm goes through the
  chest (best case 26 mm in, over 1,500 settings for two beats; three beats were always out of
  reach). A real weave turns the upper body toward the side the poi are on. The figure cannot turn
  its chest against its hips yet, and that is the next thing to add.
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

1. **Nothing wraps or rolls.** Tethers and cords are not colliders and there is no rolling
   contact, so wraps, wrist rolls and all contact staff are still out. This is the largest group
   left (about 110 skills).
2. **The hand is a ball.** There is no wrist or finger model. The test measures how far a prop
   turns inside the hand (often 360 degrees or more for staff and buugeng) and the caption says
   finger work is not shown, but the figure cannot show how the grip changes.
3. **Hand-driven moves have no forces.** They are checked against the body, not against what a
   hand can push. Timing is even, where a real spinner speeds up and slows down.
4. **The poi hand model only regulates speed.** It cannot steer the plane, which is what blocks
   weaves, turns, crossers, reels and corkscrews: most of the poi list.
5. **Solved clips fix the head, not the hand.** The hand path comes out near the textbook circle
   but not on it, and nothing limits the force, only the speed.
6. **Steps are simple.** Turns use quarter-turn steps on the spot. There is no travel, no pivot on
   one foot, and the upper body does not turn against the hips.
7. **The torso is one capsule.** Shoulders and hips are wider than what props collide with.
8. **The posture search is a coarse grid.** It finds a clear posture, not the one a teacher would
   show.
9. **Passes still missing.** Nothing changes hands except by a throw or in the staff rotor.
