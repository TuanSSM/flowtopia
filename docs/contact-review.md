# Contact physics: what it does, and a review of it

Props can now touch the body and each other. This note records what was built, the awkward
cases it handles, what the first runs exposed, and where it is still weak.

## What was built (`web/demo.js`)

- **A body made of capsules:** head, torso, thighs, shins, and both arms, which follow the
  hands. A stick is a capsule too; a poi head is a ball.
- **`closest()`:** the nearest points between two line segments. Everything else rests on it.
- **Contact response (`touchRod`, `touchBall`, `touchPair`):** push the prop out, stop the
  speed going in, give back 35% of it, and apply friction along the surface.
- **Where it runs:** the nunchaku striker in every swing, both sticks in flight (against the
  body and against each other), and the poi head.
- **`clearOf()`:** a hand cannot push the stick it holds into the body.
- **The hand follows a reversal:** if a bounce sends the striker round the other way, the
  wrist model changes direction with it.

## Awkward cases, and how each is handled

| Case | Handling |
| --- | --- |
| Parallel or collinear segments (no unique nearest point) | Start from the middle of one, then clamp; unit-tested |
| A segment that is a single point (the head, a poi ball) | Separate branch; unit-tested |
| Prop starts inside the body | Moved out with no speed added, so it does not "explode" |
| Prop axis exactly on a capsule axis (no direction to push) | A fixed fallback direction |
| Resting or sliding on the body | Below 0.4 m/s nothing bounces, which stops jitter |
| Several body parts touched at once (elbow, hip) | Resolved one after another, every solver pass |
| The cord pulling the stick back into the body | Contact is re-applied inside the cord solver while touching |
| Energy creeping in from position fixes | Pushes move the old position too, so they add no speed |
| Moving fast enough to jump through a limb | Tested: no clip moves more than half a stick radius per step |
| The same bounce being applied many times in one step | Speed is changed once per step; later passes only push out |
| A throw that misses its catch, or a swing that never settles | Returns nothing; the player says so instead of showing a blank |
| Different maths in different browsers | Throws try a short list of known-good releases; every clip is run in Firefox as well as Node |

## What the first runs exposed

Switching contact on immediately showed that existing clips were wrong:

- The throws passed up to 8 cm into the torso. They were searched again with contact on.
- The horizontal swing clipped the spinner's own shoulder. It was raised.
- In the figure-8 the held handle was driven into the torso at the crossing. The hand now
  travels further forward.
- The poi isolation ran through the spinner's own upper arm. It was moved outward.
- Butterfly poi passed through each other. Their planes now sit 12 cm apart.
- Two clips could not be fixed and were **removed**: the downspin figure-8 (the striker hits
  the forearm) and the V8 (it hits the head on the way down).

It also taught something about technique: slowing the swing suddenly makes the striker fold
back and hit its own handle. The bounce demo fades gradually for that reason.

## Tests (`node test/physics.js`)

- 10 special cases and 2,000 random pairs for `closest()`, checked against brute force.
- Every clip: nothing may be more than 8 mm inside the body or the other hand's prop
  (1 mm for drawn clips), over the whole stick, against every body part.
- Moves that are not about contact must touch nothing while recorded.
- A bounce must touch its target, lose speed there, change direction once, and arrive
  slower than 6 m/s.
- No clip may move far enough per step to tunnel.

## Where it is weak (improvement areas, most valuable first)

1. **The tether and cord are not colliders.** A poi tether passes through an arm, so wraps
   (arm wrap, leg wrap, air wrap, hyperloop) cannot be shown, and neither can nunchaku wrist
   and finger rolls. This needs the tether modelled as a chain of points with contact and
   friction. It is the single biggest unlock: about 30 skills.
2. **No rolling or resting contact for rigid props.** Contact staff (halo, fishtail, steve,
   conveyor) needs a staff rolling on a moving body without slipping. Nothing here does that.
3. **The body does not push back with its own motion.** A moving arm is treated as still at
   the moment of contact, so a rising forearm hits more softly than it should.
4. **Bounce and friction are assumed values** (35% and 0.3), not measured, and the same for
   every body part. The thigh should absorb more than the forearm.
5. **Absorption is instant.** A real bounce gives over a few centimetres; here it happens in
   one step. That is why the fade has to be done by slowing the swing.
6. **Throws are sensitive.** A change of one part in a million can turn a catch into a miss.
   The fallback list hides this; it does not remove it. A catch that steers the hand toward
   the handle during the flight would be more robust than choosing the release.
7. **Arms are left out during a flight**, because they are moving to make the catch. A stick
   can still pass through a reaching arm.
8. **Staff flights do not collide at all.** They are exact free flight and are only checked
   afterwards for passing through the body.
9. **Two hands' props are simulated separately.** They are checked for overlap but cannot
   knock each other.
10. **`clearOf()` can move a hand past arm's length**, and only knows the still parts of the body.
11. **Cost.** A plain swing takes about 1.2 s to simulate on the Pi, roughly twice what it did
    before contact, because every body part is tested on every step. Caching the arms was
    tried and gave nothing; a cheap "is anything near" test before the exact one should.
12. **The body is coarse and fixed.** No feet, hands or neck, and the figure never leans,
    turns or steps out of the way, which is how real spinners make room.
13. **Only one contact skill has a demo** (the forearm bounce). The thigh bounce was tried and
    dropped: bringing the swing across the body made the striker hit its own handle first.
