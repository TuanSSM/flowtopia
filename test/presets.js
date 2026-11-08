'use strict';
// Body presets keep each move the same move (docs/avatars.md). For every baked clip and every
// preset, the clip moved to fit the preset (Demo.fit) must keep its plane, its sense of turning,
// its winding round the hand and its shape (sim/geometry.js), and web/clips/presets.json must name
// only presets that exist and clips that are baked. On the standard body the fit changes nothing.
//   node test/presets.js
const fs = require('fs'), path = require('path');
const { Demo } = require('../sim/engine.js'), P = require('../sim/presets.js'), F = require('../sim/features.js'), G = require('../sim/geometry.js');
const DIR = path.join(__dirname, '..', 'web', 'clips');
let bad = 0, n = 0;
const undefinedPaths = new Set();
const check = (name, ok, x = '') => { n++; if (!ok) { bad++; console.log(`FAIL ${name}`, x); } };

const index = JSON.parse(fs.readFileSync(path.join(DIR, 'index.json'), 'utf8')), file = P.load();
const baked = new Set(Object.values(index).flatMap(e => e.v.map(v => v.f)));
check('presets.json lists the eight presets', JSON.stringify(Object.keys(file.presets).sort()) === JSON.stringify(P.PRESETS.map(p => p.id).sort()), Object.keys(file.presets));
for (const [f, m] of Object.entries(file.fail)) {
  check(`clip ${f} in presets.json is baked`, baked.has(f));
  for (const id of Object.keys(m)) check(`preset ${id} for ${f} exists`, !!file.presets[id]);
}
// Presets stay within ordinary adult proportions (docs/avatars.md): the standard figure is about
// 1.74 m tall with shoulder joints 0.40 m apart.
for (const p of P.PRESETS) {
  const stature = (Demo.dim.pelvis + Demo.dim.head + 0.105) * p.sc, shoulders = 2 * 0.2 * p.bd;
  check(`${p.id}: stature ${stature.toFixed(2)} m between 1.52 and 1.87 m`, stature >= 1.52 && stature <= 1.87);
  check(`${p.id}: shoulders ${shoulders.toFixed(3)} m between 0.335 and 0.447 m`, shoulders >= 0.335 && shoulders <= 0.447);
}

let clips = 0;
for (const [key, e] of Object.entries(index)) e.v.forEach(v => {
  if (v.m === 'still') return;
  const clip = Demo.unpack(JSON.parse(fs.readFileSync(path.join(DIR, v.f + '.json'), 'utf8')));
  if (clip.hide) return;
  clips++;
  check(`${key} ${v.n}: the standard body leaves frames untouched`, clip.fr.every(f => Demo.fit(f) === f));
  const base = F.measure(clip);
  for (const p of P.PRESETS) {
    Demo.setBody(p.sc, p.bd);
    const moved = { ...clip, fr: clip.fr.map(Demo.fit) };
    Demo.setBody(1, 1);
    const got = F.measure(moved);
    base.forEach((a, i) => {
      const b = got[i], name = `${key} ${v.n} on ${p.id}, prop ${i}`;
      if (!a || !b) return check(`${name}: measured on both bodies`, !a && !b);
      // Plane, sense and winding mean something only for a path that moves and lies close to a plane.
      if (a.size < 0.01 || a.flatness > 0.5) { undefinedPaths.add(`${key} ${v.n} ${i}`); return; }
      const turn = Math.acos(Math.min(1, Math.abs(G.dot(G.norm(a.normal), G.norm(b.normal)))));
      check(`${name}: same plane`, turn < 0.01, turn);
      check(`${name}: same sense`, a.sense === b.sense, [a.sense, b.sense]);
      check(`${name}: same winding`, a.winding === b.winding, [a.winding, b.winding]);
      if (a.closed) {
        const tips = c => c.fr.map(f => Demo.parts(c.props[i], i, f).tip), d = G.shapeDistance(tips(clip), tips(moved));
        check(`${name}: same shape`, d < 0.02, d);
      }
    });
  }
});
console.log(`body presets: ${clips} clips on ${P.PRESETS.length} presets, ${n} checks, ${bad} failed; ${undefinedPaths.size} paths skipped as not flat or not moving`);
process.exit(bad ? 1 : 0);
