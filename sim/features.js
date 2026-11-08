'use strict';
// Measures every baked clip (web/clips) and writes web/clips/features.json: for each variant, the
// path of each prop's tip and the state at its start and end, for next-trick suggestions.
//   node sim/features.js          (also run by sim/bake.js after baking)
// Deterministic: the same clips give the same bytes.
const fs = require('fs'), path = require('path');
const G = require('./geometry.js');
const Demo = (0, eval)(fs.readFileSync(path.join(__dirname, '..', 'web', 'demo.js'), 'utf8').replace('const Demo =', 'globalThis.Demo =') + ';Demo');
const DIR = path.join(__dirname, '..', 'web', 'clips');
const r3 = v => Math.round(v * 1000) / 1000, rv = v => v.map(r3);
const PELVIS = [0, 0.97, 0];

// The two directions that fix a prop's orientation in a frame.
function axes(pr, i, f) {
  const K = k => f[i + '.' + k];
  switch (pr.kind) {
    case 'nunchaku': return [K('d'), G.sub(K('T'), K('B'))];               // handle, striker
    case 'poi': return [G.sub(K('P'), K('o') || f['h' + pr.hand]), [0, 1, 0]]; // tether
    case 'staff': return [G.sub(K('b'), K('a')), [0, 1, 0]];
    case 'buugeng': return [K('u'), K('v')];
    default: return null;
  }
}
// Where along the prop the hand holds it: nunchaku and poi by the handle; a staff at an end or in
// the middle; a buugeng in the middle.
function grip(pr, i, f) {
  if (pr.kind !== 'staff') return pr.kind === 'buugeng' ? 'middle' : 'handle';
  const a = f[i + '.a'], b = f[i + '.b'], h = f['h' + pr.hand] || a, ab = G.sub(b, a), t = G.dot(G.sub(h, a), ab) / (G.dot(ab, ab) || 1);
  return t < 0.2 || t > 0.8 ? 'end' : 'middle';
}
// The state at frame `at`; its angular velocity is measured over one frame step, from frame a to the
// frame b that follows it in time (the last frame of a loop is followed by the first).
function state(clip, pr, i, at, a, b, dt) {
  const f = clip.fr[at], qOf = k => { const ax = axes(pr, i, clip.fr[k]); return G.quatFrom(ax[0], ax[1]); };
  const pelvis = f.pelvis || PELVIS, hand = f['h' + pr.hand] || f.hR || f.hL;
  return { hand: rv(G.sub(hand, pelvis)), q: rv(qOf(at)), omega: rv(G.angularVelocity(qOf(a), qOf(b), dt)), grip: grip(pr, i, f) };
}
function measure(clip) {
  const N = clip.fr.length, dt = clip.dur / N, closed = !!clip.loop;
  return clip.props.map((pr, i) => {
    if (!axes(pr, i, clip.fr[0])) return null;
    const tips = clip.fr.map(f => Demo.parts(pr, i, f).tip), hands = clip.fr.map(f => f['h' + pr.hand] || f.hR || f.hL);
    if (tips.some(t => !t)) return null;
    const pl = G.plane(tips), pts = G.resample(tips, 128, closed), ct = G.curvatureTorsion(pts, closed);
    const mean = a => a.reduce((x, y) => x + y, 0) / a.length;
    return {
      kind: pr.kind, hand: pr.hand, closed,
      normal: rv(pl.normal), flatness: r3(pl.flatness), sense: pl.sense,
      length: r3(G.arcLength(tips, closed)), size: r3(pl.spread),
      kappa: r3(mean(ct.kappa)), kappaMax: r3(Math.max(...ct.kappa)), tau: r3(mean(ct.tau.map(Math.abs))),
      twist: r3(G.bishop(pts, closed).twist), winding: closed ? G.winding(tips, hands, pl) : null, lobes: closed ? G.lobes(tips, pl) : null,
      entry: state(clip, pr, i, 0, 0, 1, dt), exit: closed ? state(clip, pr, i, N - 1, N - 1, 0, dt) : state(clip, pr, i, N - 1, N - 2, N - 1, dt),
      shape: closed ? G.inPlane(G.resample(tips, 32), pl).map(p => p.map(r3)) : null,
    };
  });
}
function build() {
  const index = JSON.parse(fs.readFileSync(path.join(DIR, 'index.json'), 'utf8')), out = {};
  for (const key of Object.keys(index).sort()) {
    const vs = [];
    index[key].v.forEach((v, n) => {
      if (v.m === 'still') return;
      const clip = Demo.unpack(JSON.parse(fs.readFileSync(path.join(DIR, v.f + '.json'), 'utf8')));
      const props = measure(clip).filter(Boolean);
      if (props.length) vs.push({ n, name: v.n, method: v.m, dur: r3(clip.dur), loop: !!clip.loop, props });
    });
    if (vs.length) out[key] = vs;
  }
  return out;
}
if (require.main === module) {
  const out = build(), file = path.join(DIR, 'features.json');
  fs.writeFileSync(file, JSON.stringify(out));
  const nv = Object.values(out).reduce((a, v) => a + v.length, 0);
  console.log(`features for ${nv} variants of ${Object.keys(out).length} skills written to web/clips/features.json (${(fs.statSync(file).size / 1024).toFixed(0)} KB)`);
}
module.exports = { build, measure };
