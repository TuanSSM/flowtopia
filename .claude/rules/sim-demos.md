---
paths:
  - "sim/**"
  - "test/physics.js"
  - "test/expect.js"
  - "test/coverage.js"
  - "web/clips/**"
---
# Demo physics

Read `docs/demo-engine.md` first: the kinds of clip (simulated, solved, hand-driven, rolling,
still), what is checked, and what was tried and left out.

- A skill gets a demo only when its motion is fixed by its definition in the sources, and the
  clip passes `node test/physics.js`. Do not invent a motion the sources do not describe; give
  the skill a still picture or leave it without a demo and note why.
- Add the expectation (`test/expect.js`) with the demo: what the definition promises, measured
  on the finished frames.
- Clips are deterministic: `node sim/bake.js` gives the same bytes twice and on any machine. CI
  bakes and fails when `web/clips` or `sim/fits.json` differ from what is committed
  (`node sim/bake.js --diff` says where). The posture search caches in `sim/fits.json`; commit it.
- Do not loosen a tolerance in `sim/check.js` to make a clip pass. Fix the move, or drop it and
  record it under "Tried and left out".
- New families go in `sim/moves.js`, new skills in `sim/specs.js` with a `NOTES` entry the
  caption shows. Captions say plainly what is not simulated.
- After changes: `node test/physics.js` (all of it, not only `ONLY=`), `node sim/bake.js`,
  `node test/coverage.js`, and commit `web/clips`, `docs/demo-coverage.md`.
