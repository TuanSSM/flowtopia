---
paths:
  - "figure/**"
  - "web/assets/**"
---
# 3D figure

- `figure/src/figure.js` poses the Mixamo-skeleton model from the same joints as the 2D rig
  and skins on the CPU (shader skinning drew nothing in software WebGL). Keep both: the CPU path
  is what CI and the Pi's Firefox run.
- Anatomy rules already in place and to be kept: elbows and knees are hinges (bone roll aligned
  to the next segment), the forearm pronates/supinates to follow the palm within 100 degrees,
  the collarbone lifts with the hand, the head stays nearer upright than the trunk, fingers
  curl less round a thick handle. A new pose feature must not break these; check a sheet.
- Rebuild with `cd figure && npm run build` and commit `web/figure3d.js`; CI fails if the bundle
  does not match its source.
- To judge a change, render a contact sheet, do not reason from code alone:
  `SZ=400 COLS=4 test/browser/run.sh shots.py /tmp/sheet.png nunchaku,F5,0,angle,0.15 nunchaku,F1,0,angle,0 ...`
  (prop, skill, variant index, view, phase). Include a close-up (a `hide` still) and a full
  figure from front, angle and side.
- The model file is the three.js "X Bot"; any Mixamo-rigged GLB can replace it. Textures are
  blocked by the CSP (`connect-src 'self'`), so use neutral materials.
- Avatar parts (outfits, head parts, wristbands), body presets and frames are made in code in
  `figure/src/figure.js` ([docs/avatars.md](../../docs/avatars.md)). Place a head part with
  `skinPoint` (the measured skin), never on a ball of the head's radius. After changing them, run
  `node test/body3d.js parts`, then `FRAME_BUDGET_MS=80 test/browser/run.sh avatar.py` on the Pi
  (CI uses 33 ms).
- The figure keeps the last pose it skinned and the last picture it rendered, and hands them out
  again when a request is the same by value (`node test/skin-once.js`). Anything that changes the
  figure without passing through the arguments of `render` or `stage` (a new avatar, body or
  material) must call `forget()`, or a stale picture is shown.
- Code that runs every frame takes its vectors and quaternions from the pool (`tv()`, `tq()`,
  `pt()`, `cp()`), never `new` or `.clone()`, and avoids `Math.hypot` in loops (it allocates per
  call). `node --expose-gc test/alloc.js` bounds what a frame allocates.
- `figure/src/figure.js` loads in Node without WebGL (`load(url, bytes)`), which is how
  `test/body3d.js` poses the figure against every clip and measures the props against the skin.
  Keep `probe`, `partReport` and `stage` working there: no `document`, `window` or renderer in the
  posing and measuring code.
