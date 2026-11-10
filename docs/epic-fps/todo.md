# FPS: Demo frames inside the budget

Why, scope and risks: [epics.md](../epics.md#fps-demo-frames-inside-the-budget). Order: [roadmap.md](../roadmap.md).
Category: perf. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#31](https://github.com/TuanSSM/flowtopia/issues/31)). 0 of 6 items done.

Measured today: about 58 ms a frame on the Pi's Firefox for the standard figure and 59 ms for the
heaviest avatar ([avatars.md](../avatars.md), Cost). Targets: under 33 ms on the Pi, under 16 ms on
a mid-range phone, the same picture.

- [ ] **Measure first.** The browser suite records the frame time of one demo on each path and
  prints it in the PR comment (median of 60 frames). DoD: a `frame ms` line in the sticky comment
  for every PR that touches `figure/` or `web/demo.js`.
- [ ] **Skin only on change.** No skinning when the pose is the one already drawn (paused, a still,
  the same frame at a lower play speed). DoD: a paused demo costs under 2 ms a frame on the Pi.
- [ ] **No allocations per frame.** Vectors, matrices and the skin buffers reused; the player makes
  no new arrays while playing. DoD: a heap snapshot over 300 frames grows by under 100 KB.
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
