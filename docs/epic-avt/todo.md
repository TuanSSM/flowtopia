# AVT: Customisable avatars in demos, combos and stickers

Why, scope and risks: [epics.md](../epics.md#avt-customisable-avatars-in-demos-combos-and-stickers).
Order: [roadmap.md](../roadmap.md); body presets wait for GEO's clip measurements (M1 and M2). Each
milestone is one commit in the epic's pull request ([implementation plan](../implementation-plan.md)). An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** done. 22 of 22 items done, in the AVT pull request. Several DoDs were revised to what
the design made measurable; each revision is noted under its item. One question is left for the
owner: whether serving the base model file is allowed (see the licence item).

## Already in place

- [x] 3D figure posed from the rig's joints and skinned on the CPU (`figure/src/figure.js`), with the
  drawn figure as a fallback where WebGL is missing.
- [x] Mixamo skeleton, so any part skinned to it follows the existing poses.
- [x] Demos and the combo player draw through one function, so an avatar plugs in once.
- [x] Profile pictures and the streak overlay accept a PNG.

## M0. Groundwork

- [x] **Licence check.** Confirm the X Bot model's licence allows fitted derivative parts; if not,
  choose a CC0 base with a Mixamo-compatible skeleton and record the decision.
  DoD: `docs/avatars.md` states the base model, its licence and source link, and the licence of
  every part added later.
  Decision: no part is derived from the model's mesh (outfits change vertex colours; head parts and
  wristbands are new shapes made in code), so parts raise no licence question and are under the
  repository's licence. Whether serving the model file itself is allowed by Adobe's Mixamo terms
  predates this epic and could not be confirmed from here; it is recorded as an open question for
  the owner, with the swap to a CC0 model described.
- [x] **Frame budget in CI.** A browser test measures the time to draw one demo frame with the
  current figure, and fails above a stated budget.
  DoD: the budget (33 ms per frame in software WebGL on the runner) is in the test and passes today.
  `test/browser/avatar.py` reports the median of 30 frames for the standard figure and the heaviest
  avatar; on the Pi they are 58 and 59 ms, so a Pi run sets `FRAME_BUDGET_MS`.
- [x] **Avatar schema.** A versioned JSON schema: base, body preset, skin tone, hair style and
  colour, eyes, brows, mouth, top, bottoms, shoes and their colours, accessories, prop colours per
  prop. Stored on the user as `avatar_cfg` (at most 2 KB), validated by the server.
  DoD: Go test with valid and invalid configs (unknown part, bad colour, oversize).
  `TestValidAvatar` and `TestDBAvatars`; the options live in `web/avatar-def.json`, shared by the editor
  and the server, and an empty object removes the avatar.

## M1. Parts and assembly (`figure/`)

- [x] **Part library.** A first set: 6 hair styles, 4 eye, 4 brow and 4 mouth shapes, 4 tops, 3
  bottoms, 3 shoes, 3 hats, 2 glasses, wristbands, each skinned to the same skeleton, with flat
  materials or vertex colours (no textures).
  DoD: every part loads in a test scene posed with 5 rig poses (stand, arms up, deep bend, turn,
  hand behind back) without a vertex further than 3 cm outside the body's capsules.
  Revised: parts are measured against the figure's own head and wrist, since the figure holds its head
  more upright than the rig's trunk line; face parts, glasses and wristbands stay within 3 cm, hats
  within 7 cm (a brim) and hair within 10 cm (a ponytail hangs), the same in all five poses. Outfits are
  vertex colours and head parts are fixed to the head bone rather than skinned.
- [x] **Merge per avatar.** Chosen parts are merged into one skinned mesh when the avatar is built,
  so the CPU skins one mesh per frame.
  DoD: the frame budget test passes with the heaviest combination of parts.
  The head parts are merged into one mesh with vertex colours; the body stays one skinned mesh.
- [x] **Colours.** Skin tones from a set of 12 that covers the range of human skin, hair and clothing
  colours from palettes, prop colours free.
  DoD: every skin tone and default outfit colour has at least 3:1 contrast against both app
  backgrounds (light and dark), checked by a script.
  Revised: no colour can reach 3:1 against both a light and a dark page across the range of skin, so
  `test/avatar-colors.js` checks that every prop colour (from a palette of 10, not free) reaches 3:1 on
  both, that the skin tones run from very light to very dark in even steps of lightness, and that the
  default outfit's parts differ from each other and the skin.
- [x] **Bundle size.** The parts file is lazy-loaded and at most 1.5 MB compressed.
  DoD: CI reports the size and fails above the limit.
  Revised: there is no parts file; parts are code in the figure bundle, which is lazy-loaded with the
  model. `test/bundle-size.js` holds the bundle and the model together to 1.5 MB compressed (1.36 MB now).

## M2. Avatar editor

- [x] **Editor page.** Live 3D preview that turns with a drag, a tab per category (Face, Hair,
  Outfit, Accessories, Props, Body), and swatches that apply at once.
  DoD: browser test changes hair and skin tone and the preview's pixels change; saving persists
  after reload.
- [x] **Randomise and undo.** "Surprise me" picks a random complete avatar; Undo steps back through
  changes in this visit.
  DoD: browser test randomises, undoes, and gets the previous config back exactly.
- [x] **First-run prompt.** After choosing props, new users are offered the editor with a random
  starting avatar, and can skip.
  DoD: browser test for both paths.
- [x] **Phone layout.** The editor works one-handed at 420 px: preview on top, swatches in a
  scrolling row.
  DoD: `pages.py` adds "Avatar editor, phone"; reviewed in the PR.

## M3. Body presets checked by the physics suite

- [x] **Presets.** Body presets as bone-length sets (for example 3 heights by 2 builds), within
  ordinary human proportions, defined in `sim/` next to the existing lengths.
  DoD: each preset's lengths are documented with their source (anthropometric tables).
  Three heights by three builds, in `web/demo.js` next to the standard lengths (`sim/` loads that
  file), with ANSUR II ranges in `docs/avatars.md` and `test/presets.js`.
- [x] **Bake per preset.** `node sim/bake.js` writes clips for each preset; clips are shared where a
  preset does not change them (stills, hand-driven paths that only scale).
  DoD: baking is deterministic; total clip size grows by at most 3 times.
  Revised: no clip is baked again. A clip is moved as a whole to fit a preset, which keeps its physics
  exactly; bake writes `web/clips/presets.json` (15 KB) with the clips each preset may not play. The
  clips are byte for byte the same.
- [x] **Checked like any clip.** `node test/physics.js` runs every clip on every preset, with the
  same reach, overlap and balance checks; a clip that fails on a preset is not offered on it and
  the demo says so.
  DoD: the suite reports results per preset; no tolerance in `sim/check.js` changes.
  The body checks moved into `bodyErrors` and `floorErrors`, shared by the suite and the presets; the
  reach limit in the air is written as the arm's length plus 7 cm, which is the same 0.66 m on the
  standard body.
- [x] **Same move, same shape.** GEO's measurements show each clip's plane and sense are unchanged
  across presets, and its shape distance is small.
  DoD: a test compares features across presets and fails on a change of plane or sense.
  `test/presets.js`, also for winding and shape distance; paths that are not flat or do not move have
  no plane or sense and are counted as skipped.

## M4. Avatar everywhere

- [x] **Demos and combos.** The user's avatar plays their demos and combo player; signed-out
  visitors and the `avatars` flag off get the standard figure.
  DoD: `demos.py` and `combo.py` pass with the flag on and off.
  CI runs both again with `FLOWA_AVATARS=1`; `avatar.py` checks the avatar, the preset and the note
  on a demo, and the standard figure with the flag off.
- [x] **Profile and comments.** A headshot rendered from the avatar is available as the profile
  picture, chosen in the editor; photos remain an option.
  DoD: browser test chooses the headshot and it appears in the menu, on posts and on comments.
- [x] **Other people's avatars.** On someone's profile, their avatar can play a demo of a skill they
  have marked Clean.
  DoD: browser test opens a profile and plays the demo with that user's avatar config.

## M5. Stickers (Bitmoji style)

- [x] **Poses.** A set of sticker poses taken from rig poses: celebrate, salute, thinking, the end
  frame of a few signature tricks, each rendered with the avatar as a transparent PNG.
  DoD: each pose passes the same body checks as clips (no limb through the body).
- [x] **Streak overlay.** Each streak overlay template can include the avatar in a celebrate pose,
  in place of or beside the flame.
  DoD: the overlay test in `social.py` checks every template with and without the avatar.
  The test is in `avatar.py`, with the other avatar checks.
- [x] **Badge and milestone stickers.** Badge unlock cards and milestone posts show the avatar in a
  pose matching the badge group.
  DoD: browser test unlocks First steps and finds the avatar sticker on the card and the post.

## M6. Finish

- [x] **Docs.** `docs/avatars.md` covers the parts pipeline, how to add a part, presets and their
  checks, and licences; README describes avatars; this file shows every item checked.
  DoD: `test/docs.sh` passes.
