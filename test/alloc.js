'use strict';
// Playing a demo must not fill the memory: 300 frames leave the heap where it was, and the 3D figure
// makes no vectors, matrices or buffers for a frame. Two things are measured, each on 300 different
// frames of a real clip after a warm-up:
//   growth      what is still held after the frames, the collector having run before and after: the
//               smallest of five rounds, since what the engine does on the side shows in one round and
//               not in all. Under 100 KB, in the heap and in the buffers outside it (typed arrays).
//   per frame   what one frame allocates, kept or not: the heap right after a frame less the heap right
//               before it, the median of the frames in which the collector did not run.
// Three paths are measured: the figure posed and skinned (figure/src/figure.js), what the player hands
// it for a frame (web/demo.js: the clip sampled, the rig, the hands, the props), and a whole frame of a
// playing demo, which adds the trail and the drawing calls: the player draws on a canvas that does
// nothing, and the figure poses and skins but renders nothing, as there is no WebGL here. The figure's
// own bound is tight. The player works in plain [x, y, z] arrays, shared with the physics, so it still
// allocates for a frame; its bounds hold it where it is.
// Last, the trail's shortcut (Demo.tipAt) is held to the full pose it stands in for, on every clip.
// The collector has to be at hand, or nothing can be measured:
//   node --expose-gc test/alloc.js
const fs = require('fs'), path = require('path');
const { Demo } = require('../sim/engine.js'), { figure } = require('./body3d.js');
const DIR = path.join(__dirname, '..', 'web', 'clips');
const FRAMES = 300, ROUNDS = 5, GROWTH = 100e3;
// Bytes one frame may allocate. The figure: about 2 KB is left, numbers that three.js hands back from
// its own functions; one vector made per bone would be 30 KB and Math.hypot in the skinning was 3 MB.
// The player's pose path was 17 to 19 KB. A whole frame was 37 to 42 KB for one prop and 67 to 81 KB for two
// when this was written (86 and 142 KB while each point of the trail still had a whole frame made for it),
// so each clip has its own limit: one limit for both would let the one-prop clip lose the trail's shortcut
// unnoticed. Measured on Node 22; another Node may box numbers differently, and WHOLE_FRAME, FIGURE_FRAME
// and POSE_FRAME in the environment override the limits.
const FIGURE_FRAME = Number(process.env.FIGURE_FRAME || 8e3), POSE_FRAME = Number(process.env.POSE_FRAME || 40e3);
const WHOLE = { 'nunchaku:F5': 60e3, 'doublestaff:F5': 105e3 }, wholeFrame = key => Number(process.env.WHOLE_FRAME || WHOLE[key] || 105e3);
const kb = b => (b / 1000).toFixed(1) + ' KB';
let bad = 0;
const check = (name, ok) => { if (!ok) bad++; console.log(`${ok ? 'PASS' : 'FAIL'} ${name}`); };

// What 300 calls of `frame` leave behind: the smallest growth of the heap over the rounds, with the
// growth of the buffers outside the heap in that same round.
function growth(frame) {
  let best = null;
  for (let r = 0; r < ROUNDS; r++) {
    gc(); gc();
    const a = process.memoryUsage();
    for (let i = 0; i < FRAMES; i++) frame(i);
    gc(); gc();
    const b = process.memoryUsage(), g = { heap: b.heapUsed - a.heapUsed, buffers: b.arrayBuffers - a.arrayBuffers, external: b.external - a.external };
    if (!best || g.heap < best.heap) best = g;
  }
  return best;
}
// What one call of `frame` allocates: the median over the frames. Asking for the heap's size makes an
// object of its own each time, which is measured on an empty frame and taken off.
function perFrame(frame) {
  const used = () => process.memoryUsage().heapUsed, median = a => a.sort((x, y) => x - y)[a.length >> 1];
  const sample = f => { const d = []; for (let i = 0; i < FRAMES; i++) { const a = used(); f(i); const b = used(); if (b >= a) d.push(b - a); } return d; };
  gc();
  const own = median(sample(() => {})), d = sample(frame);
  return { bytes: Math.max(0, median(d) - own), frames: d.length };
}
// Both, for one path, with the two checks.
function measure(what, frame, limit) {
  for (let i = 0; i < FRAMES; i++) frame(i);
  const g = growth(frame), p = perFrame(frame);
  check(`${what}: ${FRAMES} frames hold ${kb(g.heap)} more of the heap and ${kb(g.buffers)} more in buffers (limit ${kb(GROWTH)})`, g.heap < GROWTH && g.buffers < GROWTH && g.external < GROWTH);
  check(`${what}: a frame allocates ${kb(p.bytes)} (limit ${kb(limit)}; ${p.frames} of ${FRAMES} frames measured)`, p.frames >= FRAMES / 2 && p.bytes <= limit);
}
// A canvas that does nothing, so the player can draw without a browser.
const nothing = () => {}, gradient = { addColorStop: nothing }, width = { width: 50 };
const canvas = { fillRect: nothing, beginPath: nothing, moveTo: nothing, lineTo: nothing, stroke: nothing, fill: nothing, arc: nothing, closePath: nothing, save: nothing, restore: nothing, translate: nothing, scale: nothing, drawImage: nothing, strokeText: nothing, fillText: nothing, createRadialGradient: () => gradient, measureText: () => width };
const COL = { bg: '#101216', body: '#8a8f9c', arm: '#a4a9b5', ink: '#f0f1f4', prop: '#4d8df7', link: '#a4a9b5', trail: '#8fb6ff', plane: '#1c2a44', floor: '#2a2e38', skin: '#a4a9b5' };

async function main() {
  if (typeof gc !== 'function') { console.log('FAIL cannot measure: the collector is not at hand. Run: node --expose-gc test/alloc.js'); process.exit(1); }
  const F = await figure();
  const index = JSON.parse(fs.readFileSync(path.join(DIR, 'index.json'), 'utf8'));
  const load = f => Object.assign(Demo.unpack(JSON.parse(fs.readFileSync(path.join(DIR, f + '.json'), 'utf8'))), { f });
  for (const key of ['nunchaku:F5', 'doublestaff:F5']) {
    const clip = load(index[key].v[0].f), props = Demo.propsOf(clip, false);
    // What the player hands the figure for a frame, as in the browser.
    const staged = i => Demo.stage(clip, Demo.pose(clip, i / FRAMES, false), props);
    // The figure alone: the 300 frames are made first, so only its own posing and skinning is measured.
    const made = Array.from({ length: FRAMES }, (_, i) => staged(i)), skinned = F.stats().skins;
    measure(`${key}, the 3D figure posed and skinned`, i => { const s = made[i]; F.stage(s.J, s.hands, s.list); }, FIGURE_FRAME);
    const done = F.stats().skins - skinned, asked = FRAMES * (ROUNDS + 2); // the warm-up, the rounds and the frames one by one
    check(`${key}, the 3D figure: each of those frames was posed and skinned (${done} skinnings for ${asked} frames)`, done === asked);
    measure(`${key}, the player's pose path`, staged, POSE_FRAME);
    // A whole frame: the player's drawing with the 3D figure, which stages what it is handed and renders nothing.
    const render = F.render;
    F.render = (W, J, hands, items) => { F.stage(J, hands, items); return canvas; };
    Demo.figure(true);
    measure(`${key}, a whole frame of a playing demo`, i => Demo.draw(canvas, 720, clip, i / FRAMES, 'angle', false, COL, false, null), wholeFrame(key));
    Demo.figure(false); F.render = render;
    // The same clip with the chest turning from side to side: the spine, the neck and the head are turned
    // with vectors from the pool as well, so the figure's own bound holds.
    const turning = Array.from({ length: FRAMES }, (_, i) => Demo.stage(clip, { ...Demo.pose(clip, i / FRAMES, false), twist: [(i % 2 ? 1 : -1) * (5 + 40 * i / FRAMES), 0, 0] }, props));
    measure(`${key}, the 3D figure posed and skinned with the chest turned`, i => { const s = turning[i]; F.stage(s.J, s.hands, s.list); }, FIGURE_FRAME);
  }
  // The trail's shortcut gives the point the full pose gives, to the last digit: every clip, phases inside
  // and outside one cycle, both hands, the standard body and a taller one (which moves the clip to fit).
  let n = 0, off = 0, first = '';
  for (const [key, e] of Object.entries(index)) for (const v of e.v) {
    const clip = load(v.f);
    for (const sc of [1, 1.035]) {
      Demo.setBody(sc, 1);
      for (const mir of [false, true]) {
        const props = Demo.propsOf(clip, mir);
        for (const ph of [0, 0.137, 0.5, 0.999, 1, -0.21, 1.4]) props.forEach((p, i) => {
          const want = Demo.parts(p, i, Demo.pose(clip, ph, mir)).tip || null, got = Demo.tipAt(clip, p, i, ph, mir);
          n++;
          if (!(want === got || (want && got && want.length === got.length && want.every((x, j) => Object.is(x, got[j]))))) { off++; first = first || `${key} ${v.n}, prop ${i}, phase ${ph}${mir ? ', mirrored' : ''}, body ${sc}: ${JSON.stringify(want)} against ${JSON.stringify(got)}`; }
        });
      }
    }
  }
  Demo.setBody(1, 1);
  check(`the trail's shortcut gives the same point as the full pose (${n} points on every clip, ${off} off${first ? ': ' + first : ''})`, n > 1000 && off === 0);
  console.log(`alloc: ${bad} failed`);
  process.exit(bad ? 1 : 0);
}
main().catch(e => { console.log('FAIL', e); process.exit(1); });
