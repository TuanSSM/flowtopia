# FPS: Demo frames inside the budget

Why, scope and risks: [epics.md](../epics.md#fps-demo-frames-inside-the-budget). Order: [roadmap.md](../roadmap.md).
Category: perf. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#31](https://github.com/TuanSSM/flowtopia/issues/31)). 2 of 6 items done.

Measured today: about 58 ms a frame on the Pi's Firefox for the standard figure and 59 ms for the
heaviest avatar ([avatars.md](../avatars.md), Cost). Targets: under 33 ms on the Pi, under 16 ms on
a mid-range phone, the same picture.

- [x] **Measure first.** The browser suite records the frame time of one demo on each path and
  prints it in the PR comment (median of 60 frames). DoD: a `frame ms` line in the sticky comment
  for every PR that touches `figure/` or `web/demo.js`.
  Done: `test/browser/avatar.py` times one frame of nunchaku F5 at 720 px on the 3D figure, on the
  3D figure with the heaviest avatar, and on the drawn figure (the path without WebGL), each the
  median of 60 drawings after 20 to warm up (with one warm-up drawing the path timed first read a
third slower than the heavier one timed after it). A pixel is read back after every drawing, because a
  canvas only notes what to draw until someone looks: without that the drawn figure timed at
  0.1 ms. The page's clock is no longer rounded to a millisecond in the test browser. The suite
  prints a `frame ms` line, the workflow keeps it with the suite's results, and the summary job
  writes "Demo frame, ms" into the comment whenever the avatar suite ran. That is every change to
  `web/demo.js`, `web/figure3d.js`, the clips or `sim/`; a change in `figure/` always comes with the
  rebuilt `web/figure3d.js`, which CI checks, so it is covered. The comment on the pull request
  that added this (#67) carries the first line: on the runner, in software, about 25 to 34 ms for
  the 3D figure and 1.1 ms for the drawn one. The paths today are two; the shader path of a later item
  adds a third to the same line.
- [ ] **Skin only on change.** No skinning when the pose is the one already drawn (paused, a still,
  the same frame at a lower play speed). DoD: a paused demo costs under 2 ms a frame on the Pi.
  Built: the figure remembers the last pose it skinned and the last picture it rendered, compares
  each request with them by value (size, every joint, both hands, every prop, the camera), and
  hands the kept picture out. An unchanged frame poses, skins and renders nothing. Measured in
  Chromium with software WebGL at 720 px: 0.5 to 0.8 ms, where a changed frame takes 41 to 52 ms on a quiet machine (up to 95 ms on a busy one),
  of which 38 to 49 ms is the browser copying the picture out of WebGL, 1.6 ms the skinning and
  0.2 ms the posing. A new avatar or body, a staging outside a render and a lost WebGL context drop
  what is kept. `node test/skin-once.js` (24 checks, in the unit job) counts the figure's own work;
  `test/browser/avatar.py` checks that 60 drawings of a drawn frame render nothing, that such a
  frame draws within 5 ms on the runner (`SAME_FRAME_BUDGET_MS`), and that another view, hand,
  figure, avatar, prop colour, size or pose gives another picture and going back gives the first
  again. All 32 tiles of a contact sheet are pixel-identical to before.
  What it does and does not help: a paused demo and a still in the main player already drew no
  frames, so the gain is for pages drawn again while paused, the still steps of a combo and the
  profile and editor previews. "The same frame at a lower play speed" does not occur: the player
  blends between clip frames, so every animation frame is a new pose, and rounding it would change
  the picture.
  Open, and why the box is not ticked: the 2 ms is a number for the Pi and has not been measured
  there. On the Pi: `SAME_FRAME_BUDGET_MS=2 FRAME_BUDGET_MS=80 test/browser/run.sh avatar.py`, and
  write the "unchanged demo frame" line it prints here. Nothing in CI covers a lost WebGL context;
  it was tried by hand with a throwaway script.
- [x] **No allocations per frame.** Vectors, matrices and the skin buffers reused; the player makes
  no new arrays while playing. DoD: a heap snapshot over 300 frames grows by under 100 KB.
  Done: over 300 frames of a playing demo the heap holds between 33 KB less and 6 KB more over
  seven runs, and no more in typed-array buffers (`node --expose-gc test/alloc.js`, in the unit job; limit 100 KB).
  That number was already near zero before, because the garbage was collected; what changed is how
  much garbage a frame makes, which is what costs pauses. A whole frame of nunchaku F5 allocated
  3,073 KB and now allocates 37 to 42 KB (double staff F5: 3,129 KB, now 67 to 81 KB); the figure's share went from 2,988 KB to about 2 KB. The
  largest part, 2.95 MB a frame, was `Math.hypot` in the skinning loop, which puts its arguments on
  the heap for each of 28,374 vertices; it is a written-out square root now, and skinning takes
  0.81 ms where it took 1.74 ms (Node). Vectors and quaternions in posing, props and the camera
  come from a pool taken back at the start of each pose; the trail works a point out from its own
  keys instead of blending a whole frame per point. The test also bounds what one frame may
  allocate (figure 8 KB, the pose path 40 KB, a whole frame 60 KB with one prop and 105 KB with two),
  which is the part that fails
  when the work is undone. Skinned positions and normals of 1,288 frames are the same numbers as
  before, and 32 contact-sheet tiles are pixel-identical.
  Not literally "no new arrays": the player's pose path (`rig`, `handPose`, `parts`, `pose`) still
  works in plain `[x, y, z]` arrays that the build-time physics shares, 16 to 19 KB a frame, and was
  left as it is.
- [ ] **A lighter body mesh.** The X Bot decimated once at build time (target: half the triangles),
  committed like the bundle and checked against its source in CI. DoD: `node test/body3d.js` still
  within 25 mm on the rig and every part proud; contact sheets before and after match.
- [ ] **Shader skinning where it draws.** On load, one frame is drawn to a 16 px canvas with
  shader skinning and a pixel read back; if it drew, the GPU path is used, else the CPU path (CI,
  software WebGL, the Pi if it fails there). DoD: both paths render the same contact sheet within a
  pixel tolerance in `test/browser/avatar.py`; the chosen path is named in the demo's details.
- [ ] **Budget in CI and on the Pi.** `FRAME_BUDGET_MS` stays 33 in CI; the Pi number is recorded in
  `docs/avatars.md` with the date. DoD: under 33 ms on the Pi with the heaviest avatar; under
  16 ms on a phone measured once by hand and written down.
