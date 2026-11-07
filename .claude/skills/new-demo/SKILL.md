---
name: new-demo
description: Add a demo for a skill end to end - check the sources, choose the clip kind, write the spec and expectation, pass the physics suite, rebake, update coverage docs, and look at the result. Use when asked to "add a demo for <skill>" or "make <skill> animated".
---
Delegate to the `demo-physics` agent with the skill key (`<prop>:<id>`) and this checklist, or
follow it yourself:

1. Read the skill's definition (`web/data.js` + `web/details.js`, or `web/catalog/<prop>.json`)
   and `docs/nunchaku-research.json`. Write down, in one line each: plane, direction, which hand,
   where the body is, what starts and ends the move. Anything the sources leave open means no
   demo, or a still picture; say which.
2. Choose the kind (`docs/demo-engine.md`): simulated, solved, hand-driven, rolling, still.
   Reuse a family from `sim/moves.js`.
3. Add to `sim/specs.js` with a `NOTES` sentence; add the promise to `test/expect.js`.
4. `ONLY='<prop>:<id>' node test/physics.js` until it passes, then the full suite.
5. `node sim/bake.js && node test/coverage.js`; commit `web/clips`, `sim/fits.json`,
   `docs/demo-coverage.md`, and update the README numbers if the totals changed.
6. `SZ=400 COLS=3 test/browser/run.sh shots.py /tmp/sheet.png <prop>,<id>,0,angle,0.2 <prop>,<id>,0,front,0.5 <prop>,<id>,0,side,0.8`
   and read the image. Describe the motion you see and whether it matches the definition.
7. If it could not be done, add it to "Tried and left out" in `docs/demo-engine.md` with the
   measured reason (how far into the body, how fast the hand had to move).
