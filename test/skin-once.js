'use strict';
// The 3D figure is posed and skinned only when the pose changed. A paused demo, a still picture or a
// page drawn again asks for the pose it already has, and skinning is the costly part of a frame that
// JavaScript does (figure/src/figure.js, "nothing done twice"). The figure counts its own work
// (Figure3D.stats()), and this counts with it: the same pose twice skins once, anything that changes
// the body skins again, and only one pose is remembered. No browser is needed: the figure is posed here
// as test/body3d.js poses it, without WebGL, so the picture itself is checked in test/browser/avatar.py.
//   node test/skin-once.js
const fs = require('fs'), path = require('path');
const { Demo } = require('../sim/engine.js'), { figure, HEAVY } = require('./body3d.js');
const DIR = path.join(__dirname, '..', 'web', 'clips');
let bad = 0, n = 0;
const check = (name, ok, got) => { n++; if (!ok) bad++; console.log(`${ok ? 'PASS' : 'FAIL'} ${name}${ok ? '' : ': ' + JSON.stringify(got)}`); };

async function main() {
  const F = await figure();
  const index = JSON.parse(fs.readFileSync(path.join(DIR, 'index.json'), 'utf8'));
  const load = key => { const v = index[key].v[0]; return Object.assign(Demo.unpack(JSON.parse(fs.readFileSync(path.join(DIR, v.f + '.json'), 'utf8'))), { f: v.f }); };
  const clip = load('nunchaku:F5'), two = load('doublestaff:F5');
  // One frame as the player hands it over: everything is made anew each time, as in the browser.
  const frame = (c, ph, mir = false) => { const f = Demo.pose(c, ph, mir), props = Demo.propsOf(c, mir); return Demo.stage(c, f, props); };
  // How many times `what` posed and skinned the figure.
  const work = what => { const a = F.stats(); what(); const b = F.stats(); return [b.poses - a.poses, b.skins - a.skins]; };
  const show = (c, ph, mir) => work(() => { const { J, hands, list } = frame(c, ph, mir); F.stage(J, hands, list); });
  const once = [1, 1], none = [0, 0], is = (a, b) => a[0] === b[0] && a[1] === b[1];
  let w;

  w = show(clip, 0.2); check('a pose is posed and skinned', is(w, once), w);
  w = show(clip, 0.2); check('the same pose asked again is not skinned again', is(w, none), w);
  w = work(() => { for (let i = 0; i < 60; i++) { const { J, hands, list } = frame(clip, 0.2); F.stage(J, hands, list); } });
  check('nor on any of 60 more frames of it', is(w, none), w);
  w = show(clip, 0.6); check('another phase is skinned', is(w, once), w);
  w = show(clip, 0.2); check('going back to the earlier pose skins again: one pose is remembered, not a history', is(w, once), w);
  w = show(clip, 0.2 + 1e-9); check('a phase a billionth of a cycle further is another pose', is(w, once), w);
  w = show(clip, 0.2, true); check('the mirrored pose (the other hand) is skinned', is(w, once), w);
  w = show(clip, 0.2, true); check('and not again', is(w, none), w);

  // What is in the hands changes the pose only through the hands.
  show(clip, 0.2);
  w = work(() => { const { J, hands } = frame(clip, 0.2); F.stage(J, hands, []); });
  check('the same pose with the props left out is not skinned again', is(w, none), w);
  w = work(() => { const { J, hands, list } = frame(clip, 0.2); F.stage(J, { ...hands, R: { ...hands.R, grip: hands.R.grip === 'open' ? 'closed' : 'open' } }, list); });
  check('a hand that opens or closes is another pose', is(w, once), w);
  w = work(() => { const { J, hands, list } = frame(clip, 0.2); F.stage(J, { ...hands, R: { ...hands.R, r: hands.R.r + 0.004 } }, list); });
  check('so is a thicker handle in the hand', is(w, once), w);
  show(two, 0.3);
  w = show(two, 0.3); check('a two-prop move asked again is not skinned again', is(w, none), w);

  // The chest turned against the hips (the `twist` of a pose) is part of what is remembered, twice over: it
  // moves the shoulders, which are joints, and it is itself a number among them (J.twist).
  const turned = (deg, change = J => J) => work(() => { const f = Demo.pose(clip, 0.2, false), props = Demo.propsOf(clip, false), { J, hands, list } = Demo.stage(clip, deg === null ? f : { ...f, twist: [deg, 0, 0] }, props); F.stage(change(J), hands, list); });
  show(clip, 0.2);
  w = turned(0); check('the same pose with a twist of 0 degrees is the pose it had, and is not skinned again', is(w, none), w);
  w = turned(15); check('the same pose with the chest turned 15 degrees is skinned', is(w, once), w);
  w = turned(15); check('and not again', is(w, none), w);
  w = turned(-15); check('turned the other way it is another pose', is(w, once), w);
  w = turned(15 + 1e-9); check('so is a twist a billionth of a degree further', is(w, once), w);
  w = turned(null); check('and the pose without its twist again', is(w, once), w);
  {
    const f = Demo.pose(clip, 0.2, false), a = Demo.rig(f), b = Demo.rig({ ...f, twist: [15, 0, 0] });
    check('a twist moves joints (both shoulders and an elbow), so a turned pose can never be taken for the unturned one', ['shL', 'shR', 'elR'].every(k => a[k].some((x, i) => x !== b[k][i])), [a.shR, b.shR]);
  }
  turned(15);
  w = turned(15, J => ({ ...J, twist: 20 })); check('the twist itself is compared too: the same joints said to be turned 20 degrees are skinned again', is(w, once), w);

  // The body and the avatar are not part of a pose, so changing them has to drop what was remembered.
  show(clip, 0.2);
  w = work(() => { F.setBody(1.035, 1.1, 'neutral'); const { J, hands, list } = frame(clip, 0.2); F.stage(J, hands, list); });
  check('another body preset skins the same pose again', is(w, once), w);
  w = work(() => { F.setBody(1.035, 1.1, 'neutral'); const { J, hands, list } = frame(clip, 0.2); F.stage(J, hands, list); });
  check('the same preset set again does not', is(w, none), w);
  w = work(() => { F.setBody(1.035, 1.1, 'feminine'); const { J, hands, list } = frame(clip, 0.2); F.stage(J, hands, list); });
  check('another frame of the body (feminine) skins again', is(w, once), w);
  w = work(() => { F.setBody(1, 1, 'neutral'); const { J, hands, list } = frame(clip, 0.2); F.stage(J, hands, list); });
  check('back on the standard body it skins again', is(w, once), w);
  w = work(() => { F.setAvatar(HEAVY); const { J, hands, list } = frame(clip, 0.2); F.stage(J, hands, list); });
  check('putting on an avatar skins again, so its parts are placed', is(w, once), w);
  w = show(clip, 0.2); check('the same pose in the same avatar is not skinned again', is(w, none), w);
  const worn = JSON.stringify(F.partPoints());
  show(clip, 0.6); show(clip, 0.2);
  check('and its parts (hat, glasses, wristbands and the rest) are where a fresh skinning puts them', Object.keys(JSON.parse(worn)).length >= 6 && worn === JSON.stringify(F.partPoints()), Object.keys(JSON.parse(worn)));
  w = work(() => { F.setAvatar({ ...HEAVY, hat: 'cap' }); const { J, hands, list } = frame(clip, 0.2); F.stage(J, hands, list); });
  check('another avatar skins again', is(w, once), w);
  w = work(() => { F.setAvatar(null); const { J, hands, list } = frame(clip, 0.2); F.stage(J, hands, list); });
  check('taking the avatar off skins again', is(w, once), w);

  // The skin of a pose asked twice is where a fresh skinning puts it.
  const flat = () => F.skinPoints().map(p => p.slice(0, 3)).flat();
  show(clip, 0.6); show(clip, 0.2); const fresh = flat();
  show(clip, 0.2); const again = flat();
  check('the skin of a pose asked twice is the freshly skinned one, to the last digit', fresh.length > 1000 && fresh.every((v, i) => v === again[i]), fresh.length);
  // The measuring that test/body3d.js does goes through the same staging.
  w = work(() => { const { J, hands, list } = frame(clip, 0.2); F.probe(J, hands, list); });
  check('measuring a pose that is already skinned does not skin it again', is(w, none), w);
  w = work(() => { const { J, hands, list } = frame(clip, 0.45); F.probe(J, hands, list); });
  check('measuring another pose skins it', is(w, once), w);

  console.log(`skin once: ${n} checks, ${bad} failed`);
  process.exit(bad ? 1 : 0);
}
main().catch(e => { console.log('FAIL', e); process.exit(1); });
