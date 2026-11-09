# Avatars

Members build an avatar that plays their demos and combos, shows on their profile, and appears on
stickers: the badge unlock card, milestone posts and the streak overlay. The `avatars` flag turns it
all on (on by default); it needs the 3D figure (`model3d`). Signed-out visitors, members without an
avatar and anyone where the flag is off see the standard figure.

## What an avatar is

A small JSON object (`users.avatar_cfg`, at most 2 KB), versioned with `v`:

| Field | Values |
| --- | --- |
| `body`, `build`, `frame` | body preset: short, average or tall; lean, average or broad; frame: neutral, feminine or masculine |
| `skin` | one of 12 skin tones |
| `hair`, `hairColor` | none, buzz, short, long, ponytail, bun or curly; one of 8 hair colours |
| `eyes`, `brows`, `mouth` | 4 eye shapes, 4 brow shapes or none, 4 mouths |
| `top`, `bottoms`, `shoes` | 4 tops, 3 bottoms, trainers, high-tops, boots or barefoot, each with a colour |
| `hat`, `glasses`, `wristbands` | none or 3 hats, none or 2 glasses, wristbands or not, with colours |
| `props` | a colour per prop, from 10 prop colours; a prop left out uses the theme colour |

Every option and palette is listed once, in `web/avatar-def.json`. The editor reads it, and the
server (`validAvatar` in `main.go`) refuses anything not in it: an unknown field or option, a colour
off its palette, a wrong version, or more than 2 KB. An empty object removes the avatar.

## The parts pipeline

There are no model files or textures for parts. Everything is made in code in the figure bundle
(`figure/src/figure.js`):

- **Outfits** colour the body mesh per vertex. When the model loads, each vertex gets a region from
  the bone that moves it most (trunk, upper arm, forearm, hips and thighs, shin, ankle band, foot).
  A top colours the trunk and, by sleeve length, the arms; bottoms the hips and, unless shorts, the
  legs; shoes the feet and, for high-tops and boots, 6 or 13 cm up the shin.
- **Head parts** (hair, eyes, brows, mouth, hats, glasses) are simple shapes placed on the head and
  merged into one mesh with vertex colours, fixed to the head bone. The head is measured from the
  skinned mesh at rest: its centre, its three semi-axes (8.8 cm to each side, 10.6 cm up, 12.4 cm front
  to back) and, for any direction, how far the skin is. Face parts and glasses are placed a few
  millimetres outside that measured skin, so they show (they used to be placed on a ball of the head's
  height and sat inside the mannequin's longer face); caps, crowns and bands are stretched to the
  semi-axes; glasses have temples back to the ears. **Wristbands** are fixed to the hand bones. A part
  fixed to a bone costs nothing to skin, so the figure still skins one body mesh per frame.
- **Body presets** scale the figure: height scales the whole model; build widens the trunk out from
  the spine, the limbs out from their bones, and the collarbones, by moving rest-pose vertices.
  Bones are never scaled unevenly, since posing reads each parent's rotation.

To add a part: add its option to `web/avatar-def.json`, draw it in `buildHead` (head parts, with a
`tag` so the parts test can find it) or as a mesh fixed to a bone with `attach`, rebuild the bundle
(`cd figure && npm run build`), and run `node test/body3d.js parts` (the same check runs in the
browser in `test/browser/avatar.py`). It measures every part against the mesh itself, by the nearest
triangle of the skin, in five poses: every part stands at least 2 mm proud of the skin, so it shows;
face parts, glasses and wristbands stay within 3 cm of it, hats within 7 cm (a brim) and hair within
10 cm (a ponytail hangs); and each measures the same in every pose, which shows the part follows its
bone. Measured now: eyes 10 to 12 mm proud, brows 14 to 16, mouth 8 to 12, glasses 25 to 28,
wristbands 15, hats up to 62, hair up to 66.

## Body presets and their checks

| Preset | Height factor | Build factor |
| --- | --- | --- |
| short, average, tall | 0.90, 1, 1.035 | |
| lean, average, broad | | 0.92, 1, 1.1 |

Height scales lengths: arms, legs, pelvis and chest height. Build scales widths: shoulders, hips, the
trunk and the limbs. Breadth follows height only loosely in people, so height leaves widths alone.
The standard figure is 1.79 m tall (the 3D model's height; the rig's head is where the model's is) with
its shoulder joints 0.40 m apart, so the presets run from about 1.61 to 1.86 m and from 0.37 to 0.44 m
across the shoulders. The 2012 US Army
anthropometric survey (ANSUR II; Gordon et al., *2012 Anthropometric Survey of U.S. Army Personnel:
Methods and Summary Statistics*, NATICK/TR-15/007,
[DTIC](https://apps.dtic.mil/sti/tr/pdf/ADA611869.pdf)) gives stature from about 1.52 m (5th
percentile, women) to 1.87 m (95th percentile, men) and biacromial breadth from about 0.335 m to
0.447 m, converted from the inch values in the
[NCSU summary tables](https://www.ergocenter.ncsu.edu/wp-content/uploads/sites/18/2017/09/Anthropometric-Summary-Data-Tables.pdf).
`test/presets.js` holds every preset inside those ranges.

Clips are not simulated again per preset. A clip is moved as a whole to fit the body (`Demo.fit` in
`web/demo.js`): hands, elbows and props rise or sink with the shoulders, the pelvis with the legs, and
the feet stay on the floor. A pure shift leaves the physics exactly as simulated, since gravity is the
same everywhere. Then the same floor, overlap, reach, leg and balance checks as for the standard body
run on each preset (`bodyErrors` and `floorErrors` in `sim/check.js`), with no tolerance changed:

- `node test/physics.js` runs them for every clip on every preset and prints how many clips each
  preset offers.
- `node sim/bake.js` writes the results to `web/clips/presets.json`: each clip that fails on a preset,
  with the first reason. `test/physics.js` fails if that file disagrees with what it finds.
- The player plays a clip on the member's preset only if it passed there. Otherwise it plays on the
  standard body, and the demo says so with the reason.
- `node test/presets.js` checks that every clip keeps its plane, sense of turning, winding round the
  hand and shape (from `sim/geometry.js`) on every preset. Paths that are not flat or do not move have
  no plane or sense, and are counted as skipped.

Most failures are real: shorter arms cannot reach where the standard body's hands go, wider
shoulders put a hand further from a cross-body grip, and longer forearms bend the elbow so a staff
passes through it.

## Frames

The frame (`frame`: neutral, feminine or masculine) shapes the drawn figure. The mannequin as it comes
is the neutral frame. The other two move its rest-pose vertices as the build does, out from the spine
for the trunk and from the bone for the limbs, by a factor that changes along the trunk, plus a bust for
the feminine frame; the collarbones follow the shoulder factor so the arms start at the shoulders.

| Frame | Shoulders | Chest | Waist | Hips | Neck | Arms | Legs | Bust |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| feminine | 0.93 | 0.97 | 0.90 | 1.06 | 0.88 | 0.94 | 1.02 | 3.5 cm forward |
| masculine | 1.06 | 1.04 | 1.02 | 0.97 | 1.08 | 1.05 | 1.00 | none |

The direction and rough size of each come from the ANSUR II tables cited above, as the ratio of the
women's to the men's means: shoulders (biacromial breadth) about 11% narrower, waist girth about 12%
smaller, neck girth about 15% smaller, hip breadth a few percent wider. The neutral mannequin sits
between the two, so each frame moves about half of that away from it.

A frame changes the figure, not the rig: the rig the physics and body checks use (`sim/check.js`) is
the same for every frame, so the clips, their checks and `web/clips/presets.json` do not change. What
does change is how close the drawn skin comes to the props, which `node test/body3d.js` measures on
every clip for the neutral, feminine and masculine frames (below).

## The drawn body and the props

The physics and the body checks keep every prop clear of the rig's capsules (`Demo.caps` in
`web/demo.js`), so those capsules must be the drawn body. They are fitted to the 3D model's skin: a head
longer than it is wide and at the model's height, a neck, the shoulders and the upper back, hips and
buttocks reaching well outside the trunk, and limbs thicker near the body than near their ends (the X Bot
is a chunky mannequin). `node test/body3d.js rig` measures, standing, how far the skin reaches outside
the capsules anywhere: at most 25 mm is allowed, and it is 22 mm now, on the flat of the upper back. Before
the fit the gap was up to 70 mm (the hips), the head sat 6 cm lower than the model's, and 43 of 158 clips
drew a prop through the figure, the worst 86 mm deep.

Fitting the body changed what the physics sees, so the clips were baked again and the moves that no
longer passed were set right at their source rather than by loosening a check: the resting hand hangs
at 0.26 m from the middle (0.24 put it inside the hips); the wall-plane nunchaku swing centre moved out
to 0.42 m and forward to 0.26 m (the tosses were catching the hip); the horizontal figure 8 and the
staff rotor moved forward; the poi flowers' arm is held out at 0.36 m and the butterfly's elbows are
given (out at the sides, so the forearms lie in front of the belly); the unit circles' centre is a little
out from the shoulder and the staff rests on the back of a 5 cm forearm; the hip reel is beside and in
front of the hip with the elbow behind the hand; the neck roll's sticks slide over the chest; a wrist
roll's radius is the forearm's plus the stick's; the catching hand of a cross toss keeps its elbow low
and arrives no faster than 1.2 m/s; and three still pictures were moved by a centimetre or two. Three
tolerances did change, each for a reason the fit made true: a limb may press 20 mm into the body (the
capsules are now the skin, and skin and muscle give about that much); a held stick may sink 10 mm into
the forearm it rests along, not 20; and a staff counts as in the hand up to 72 mm from the fist (on the
back of the forearm in a unit circle). The posture cache
(`sim/fits.json`) was emptied, so every searched posture was chosen against the new body.

`node test/body3d.js` also poses and skins the figure in Node exactly as the browser does
(`figure/src/figure.js` loads without WebGL) and, for every clip at 8 phases on the neutral, feminine
and masculine frames, samples every prop's tubes and balls and measures each sample against the nearest
triangle of the skin. It measures what the checks measure, in two channels: a prop against the body and
the other arm may go at most 25 mm into the skin, which is what the capsules may miss of it (the checks
keep the props clear of the capsules, not of the mesh); a held prop against the arm that holds it, which
the checks let sink 10 mm into the forearm capsule, may go 35 mm. The allowances mirror the checks' own: a
clip the checks let touch the body (`contact`) may go 10 mm further; a clip turned by finger work
(`fingers`) is not measured against the arm that holds the prop; the hands are left out, as the fingers
wrap round what is held, and so is the forearm within 13 cm of the wrist, where a held stick may lie; and
a poi's tether or a nunchaku's cord is not measured against the arm of the hand it leaves from, as the hand
model cannot steer a poi's plane ([demo-engine.md](demo-engine.md), limitation 4) and the checks let the
tether cross that arm. Within those rules the drawn figure shows what the physics allows and no more. The
test lists every clip over the limit with the depth, the prop, the bone under it and the phase; `ALL=1`
lists every clip, `PRESETS=1` runs the body presets too, `FRAMES=0` the neutral frame only.

## Stickers

`Demo.STICKERS` in `web/demo.js` holds five poses (celebrate, salute, thinking, wave, strong) as rig
poses; `node test/stickers.js` puts each through the body checks on the standard body and all eight
presets. Signature-move stickers (`Demo.SIGNATURE`, one per prop) are the first frame of a clip, which
the physics suite checks. A sticker is the avatar rendered on a transparent canvas, kept for the
visit. Badge groups pick the pose: Skills strong, Practice and Streaks celebrate, Community wave, and
Combos the prop's signature move.

## Colours

`node test/avatar-colors.js` checks the palettes: every prop colour has at least 3:1 contrast with the
demo background in both themes; the 12 skin tones run from very light to very dark in even steps of
lightness (CIE L*); the default top, bottoms and skin differ by at least 1.4:1. A skin tone cannot
reach 3:1 against both a light and a dark page (a pale tone on a light page), so the figure reads by
its shading there and the props carry the contrast.

## Cost

| | |
| --- | --- |
| The 3D figure, bundle and model, compressed | 1.36 MB (limit 1.5 MB, `node test/bundle-size.js`) |
| What avatars added to the bundle, compressed | about 6 KB |
| One demo frame on the Pi, standard figure and heaviest avatar | about 58 and 59 ms |

The frame budget in CI is 33 ms for one demo frame with the heaviest avatar, in the runner's
software WebGL (`FRAME_BUDGET_MS` in `test/browser/avatar.py`).

## Licences

- **Base model.** `web/assets/human.glb` is the "X Bot" mannequin from the three.js examples
  ([three.js repository](https://github.com/mrdoob/three.js/tree/dev/examples/models/gltf)), a Mixamo
  character ([mixamo.com](https://www.mixamo.com)). The three.js code is MIT licensed; the model's
  terms are Adobe's Mixamo terms. Community answers about the Mixamo FAQ say characters may be used
  royalty-free in personal and commercial projects but the raw files may not be redistributed on
  their own ([Adobe community](https://community.adobe.com/t5/mixamo-discussions/mixamo-faq-licensing-royalties-ownership-eula-and-tos/m-p/13234775),
  [itch.io](https://itch.io/post/7765268)). Flowa served this file before avatars, and any visitor's
  browser can download it. **Open question for the owner:** whether serving it this way is allowed has
  not been confirmed with Adobe. If it is not, replace it with a CC0 model on a Mixamo-compatible
  skeleton; nothing else needs to change.
- **Parts.** Every part is generated by code in this repository (`figure/src/figure.js`) and is under
  the repository's licence. No part is derived from the model's mesh: outfits change only vertex
  colours, and head parts and wristbands are new shapes.
