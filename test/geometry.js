// Known-answer tests for sim/geometry.js, then checks on the baked clips.
//   node test/geometry.js
const G = require('../sim/geometry.js');
let bad = 0, n = 0;
const ok = (name, cond, detail = '') => { n++; if (!cond) { bad++; console.log('FAIL ' + name + (detail !== '' ? ': ' + detail : '')); } else console.log('PASS ' + name); };
const near = (a, b, rel) => Math.abs(a - b) <= rel * Math.abs(b);
const mean = a => a.reduce((x, y) => x + y, 0) / a.length;
const TAU = 2 * Math.PI;
// A circle sampled unevenly comes back evenly spaced.
const r = 0.4, uneven = Array.from({ length: 200 }, (_, i) => { const t = TAU * (i / 200) ** 1.6; return [r * Math.cos(t), r * Math.sin(t), 0]; });
const even = G.resample(uneven, 128), gaps = even.map((p, i) => G.len(G.sub(even[(i + 1) % 128], p)));
ok('resampling spaces points evenly (within 0.5%)', Math.max(...gaps) / Math.min(...gaps) < 1.005, (Math.max(...gaps) / Math.min(...gaps)).toFixed(4));
const circle = G.resample(Array.from({ length: 400 }, (_, i) => { const t = TAU * i / 400; return [r * Math.cos(t), r * Math.sin(t), 0.1]; }), 256);
const ct = G.curvatureTorsion(circle);
ok('curvature of a circle is 1/r within 1%', near(mean(ct.kappa), 1 / r, 0.01), mean(ct.kappa).toFixed(4));
ok('torsion of a flat curve is 0', Math.max(...ct.tau.map(Math.abs)) < 1e-3, Math.max(...ct.tau.map(Math.abs)));
// A helix: curvature a/(a^2+b^2), torsion b/(a^2+b^2).
const a = 0.3, b = 0.08, helix = G.resample(Array.from({ length: 1200 }, (_, i) => { const t = 3 * TAU * i / 1200; return [a * Math.cos(t), a * Math.sin(t), b * t]; }), 600, false);
const ht = G.curvatureTorsion(helix, false), mid = x => x.slice(60, -60);
ok('helix curvature within 2%', near(mean(mid(ht.kappa)), a / (a * a + b * b), 0.02), mean(mid(ht.kappa)).toFixed(4));
ok('helix torsion within 2%', near(mean(mid(ht.tau)), b / (a * a + b * b), 0.02), mean(mid(ht.tau)).toFixed(4));
// A figure-8 (lemniscate) in the wall plane: flat, two lobes.
const fig8 = Array.from({ length: 300 }, (_, i) => { const t = TAU * i / 300; return [0, 0.5 * Math.sin(t), 0.3 * Math.sin(2 * t)]; });
const f8 = G.curvatureTorsion(G.resample(fig8, 256));
ok('a figure-8 has no torsion', Math.max(...f8.tau.map(Math.abs)) < 1e-3);
const p8 = G.plane(fig8);
ok('its plane is the wall plane (normal along x)', Math.abs(p8.normal[0]) > 0.999 && p8.flatness < 1e-6, p8.normal.map(x => x.toFixed(3)));
ok('a figure-8 has two lobes; a circle one', G.lobes(fig8, p8) === 2 && G.lobes(circle, G.plane(circle)) === 1, [G.lobes(fig8, p8), G.lobes(circle, G.plane(circle))]);
// The rotation-minimising frame stays orthonormal and does not flip at the figure-8's inflections,
// where the Frenet normal turns over.
const bf = G.bishop(G.resample(fig8, 256));
const ortho = Math.max(...bf.frames.map(f => Math.max(Math.abs(G.dot(f.T, f.N)), Math.abs(G.len(f.N) - 1), Math.abs(G.dot(f.T, f.B)))));
const jumps = Math.max(...bf.frames.map((f, i) => G.angle(f.N, bf.frames[(i + 1) % 256].N)));
// The Frenet binormal of a flat figure-8 points along +x on one lobe and -x on the other: count its reversals.
const frenetFlip = (() => { const P = G.resample(fig8, 256); let flips = 0, last = 0, first = 0; for (let i = 0; i < 256; i++) { const b = G.dot(G.cross(G.sub(P[i], P[(i + 255) % 256]), G.sub(P[(i + 1) % 256], P[i])), [1, 0, 0]); const s = Math.abs(b) < 1e-9 ? 0 : Math.sign(b); if (s && last && s !== last) flips++; if (s) { last = s; first ||= s; } } return flips + (first && last !== first ? 1 : 0); })();
ok('the parallel-transport frame stays orthonormal (1e-6)', ortho < 1e-6, ortho);
ok('it never jumps, while the Frenet binormal reverses at both inflections', jumps < 0.2 && frenetFlip === 2, [jumps.toFixed(3), frenetFlip]);
// Sense: the same circle run backwards has the opposite sense.
ok('reversing a path reverses its sense', G.plane(circle).sense === -G.plane([...circle].reverse()).sense && G.plane(circle).sense !== 0);
// Winding: a tip going round the hand once winds once; a figure-8 about its centre winds zero.
const hands = circle.map(() => [0, 0, 0.1]);
// A figure-8's striker swings round the hand one way and back the other: its angle about the hand rises and falls.
const swing = Array.from({ length: 200 }, (_, i) => { const th = 2.5 * Math.sin(TAU * i / 200); return [0, 0.5 * Math.cos(th), 0.5 * Math.sin(th)]; });
ok('a spin winds once round the hand, a to-and-fro swing zero times', Math.abs(G.winding(circle, hands, G.plane(circle))) === 1 && G.winding(swing, swing.map(() => [0, 0, 0]), G.plane(swing)) === 0, [G.winding(circle, hands, G.plane(circle)), G.winding(swing, swing.map(() => [0, 0, 0]), G.plane(swing))]);
// Shape distance: the same path from another start, or traced unevenly, is the same shape.
const shifted = [...circle.slice(77), ...circle.slice(0, 77)], bigger = circle.map(p => G.mul(p, 1.7));
ok('shape distance ignores where the path starts', G.shapeDistance(circle, shifted) < 1e-3, G.shapeDistance(circle, shifted));
ok('and how fast or how big it is traced', G.shapeDistance(circle, uneven.map(p => [p[0], p[1], 0.1])) < 0.02 && G.shapeDistance(circle, bigger) < 1e-3, [G.shapeDistance(circle, uneven), G.shapeDistance(circle, bigger)]);
const tilted = fig8.map(p => G.rotate(p, G.norm([1, 1, 0]), 0.9));
ok('and how its plane is turned', G.shapeDistance(fig8, tilted) < 1e-3, G.shapeDistance(fig8, tilted));
ok('a circle and a figure-8 are far apart', G.shapeDistance(circle, fig8) > 0.5, G.shapeDistance(circle, fig8));
// Opposite directions of travel are the same shape.
ok('a path run backwards is the same shape', G.shapeDistance(circle, [...circle].reverse()) < 1e-3 && G.shapeDistance(fig8, [...fig8].reverse()) < 0.02, [G.shapeDistance(circle, [...circle].reverse()), G.shapeDistance(fig8, [...fig8].reverse())]);
// Distances between states.
const qI = G.quatFrom([1, 0, 0], [0, 1, 0]), q90 = G.quatFrom([0, 1, 0], [-1, 0, 0]);
ok('the same orientation is 0 apart; a quarter turn about one axis is 90 degrees', G.quatAngle(qI, qI) < 1e-9 && Math.abs(G.quatAngle(qI, q90) * 180 / Math.PI - 90) < 1e-6, G.quatAngle(qI, q90));
ok('planes 90 degrees apart measure 90; a plane and its flipped normal measure 0', Math.abs(G.planeAngle([1, 0, 0], [0, 0, 1]) * 180 / Math.PI - 90) < 1e-9 && G.planeAngle([0, 1, 0], [0, -1, 0]) < 1e-9);
const w = G.angularVelocity(qI, G.quatFrom([Math.cos(0.1), Math.sin(0.1), 0], [-Math.sin(0.1), Math.cos(0.1), 0]), 0.05);
ok('angular velocity of 0.1 rad in 0.05 s about z is 2 rad/s about z', Math.abs(w[2] - 2) < 1e-6 && Math.abs(w[0]) < 1e-9, w);

// On the baked clips (web/clips/features.json, made by node sim/features.js).
const fs = require('fs'), path = require('path');
const FEAT = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'clips', 'features.json'), 'utf8'));
const vOf = (key, name) => (FEAT[key] || []).find(v => v.name === name)?.props[0];
const deg = r => r * 180 / Math.PI;
const wall = vOf('nunchaku:F3', 'Wall plane'), face = vOf('nunchaku:F3', 'Face plane'), table = vOf('nunchaku:F3', 'Table plane');
const apart = [[wall, face], [wall, table], [face, table]].map(([a, b]) => deg(G.planeAngle(a.normal, b.normal)));
ok("F3's wall, face and table planes are about 90 degrees apart (within 10)", apart.every(x => Math.abs(x - 90) < 10), apart.map(x => x.toFixed(1)));
const up = vOf('nunchaku:F5', 'Upspin'), down = vOf('nunchaku:F5', 'Downspin');
ok('upspin and downspin share a plane and turn opposite ways', deg(G.planeAngle(up.normal, down.normal)) < 5 && up.sense === -down.sense);
const shp = p => p.shape.map(q => [q[0], q[1], 0]), f5 = FEAT['nunchaku:F5'].map(v => v.props[0]), f3 = FEAT['nunchaku:F3'].map(v => v.props[0]);
const circles = [...f3, ...f5], maxCircle = Math.max(...circles.flatMap(a => circles.map(b => G.shapeDistance(shp(a), shp(b)))));
ok('the F3 and F5 swings are all the same shape (circles), told apart by plane and sense', maxCircle < 0.05, maxCircle.toFixed(3));
// A mirrored clip (the left hand for the right) reflects x: the sense flips in the face and table
// planes and stays in the wall plane, whose normal is the axis of the reflection.
const D = (0, eval)(fs.readFileSync(path.join(__dirname, '..', 'web', 'demo.js'), 'utf8').replace('const Demo =', 'globalThis.Demo =') + ';Demo');
const index = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'clips', 'index.json'), 'utf8'));
const clipOf = (key, name) => D.unpack(JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'clips', index[key].v.find(v => v.n === name).f + '.json'), 'utf8')));
const senseOf = (clip, mir) => G.plane(clip.fr.map(f => D.parts(clip.props[0], 0, mir ? Object.fromEntries(Object.entries(f).map(([k, v]) => [k, [-v[0], v[1], v[2]]])) : f).tip)).sense;
const mirrored = ['Upspin', 'Outspin', 'Nearspin'].map(n => { const c = clipOf('nunchaku:F5', n); return [senseOf(c, false), senseOf(c, true)]; });
ok('mirroring keeps the sense in the wall plane and flips it in the face and table planes', mirrored[0][0] === mirrored[0][1] && mirrored[1][0] === -mirrored[1][1] && mirrored[2][0] === -mirrored[2][1], JSON.stringify(mirrored));
// A looping clip ends where it starts. Its props are compared as sets of points, because a staff looks
// the same after half a turn and the two sticks of a nunchaku can swap roles in a roll.
const POINTS = { nunchaku: ['g', 'B', 'T'], poi: ['P'], staff: ['a', 'b'], buugeng: ['c'] };
const seams = [];
for (const [key, e] of Object.entries(index)) for (const v of e.v) {
  if (v.m === 'still') continue;
  const c = D.unpack(JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'clips', v.f + '.json'), 'utf8')));
  if (!c.loop) continue;
  c.props.forEach((pr, i) => {
    const pts = f => (POINTS[pr.kind] || []).map(k => f[i + '.' + k]).filter(Boolean);
    const a = pts(c.fr[c.fr.length - 1]), b = pts(c.fr[0]);
    if (!a.length) return;
    const gap = Math.max(...a.map(p => Math.min(...b.map(q => G.len(G.sub(p, q))))));
    let step = 0;
    for (let j = 1; j < c.fr.length; j++) { const x = pts(c.fr[j]), y = pts(c.fr[j - 1]); step = Math.max(step, ...x.map((p, k) => G.len(G.sub(p, y[k])))); }
    seams.push([key + ' ' + v.n, gap, step]);
  });
}
const open = seams.filter(([, gap, step]) => gap > Math.max(2 * step, 0.01));
ok(`every looping clip (${seams.length} props) closes on itself`, open.length === 0, open.slice(0, 3).map(([k, g, s]) => `${k}: ${g.toFixed(3)} m, step ${s.toFixed(3)} m`).join('; '));
const h8 = FEAT['nunchaku:F7']?.[0]?.props[0];
ok('a figure-8 clip is far in shape from every circle', h8 && Math.min(...circles.map(c => G.shapeDistance(shp(h8), shp(c)))) > 0.3, h8 && Math.min(...circles.map(c => G.shapeDistance(shp(h8), shp(c)))).toFixed(3));
// Catalog motion fields (web/catalog/nunchaku.json) against the clips.
const { planeOf } = require('../sim/motion.js');
const NUN = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'catalog', 'nunchaku.json'), 'utf8'));
ok('every nunchaku skill has a motion field', NUN.skills.every(s => s.motion && s.motion.plane && s.motion.direction && s.motion.source), NUN.skills.filter(s => !s.motion).map(s => s.id));
const clash = NUN.skills.filter(s => FEAT['nunchaku:' + s.id]).filter(s => {
  const ps = [...new Set(FEAT['nunchaku:' + s.id].flatMap(v => v.props.map(p => planeOf(p.normal))))], clip = ps.length === 1 ? ps[0] : 'mixed';
  return s.motion.source !== 'clip' || !(s.motion.plane === clip || s.motion.plane === 'mixed');
});
ok('where a skill has a clip, its catalog plane is the clip\'s (or mixed)', clash.length === 0, clash.map(s => s.id));
module.exports = { ok, done: () => { console.log(`${n} checks, ${bad} failed`); process.exit(bad ? 1 : 0); } };
if (require.main === module) module.exports.done();
