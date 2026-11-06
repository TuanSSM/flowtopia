'use strict';
// A clip from the engine describes one hand. Here the hands are put together into one scene:
// both hands, the body pose and every prop, frame by frame, at a fixed frame rate.
const E = require('./engine.js'), { Demo } = E, { add, sub, mul, dot, len, norm, flip, clamp } = Demo.v;

const FPS = 100, BODY = ['pelvis', 'face', 'spine', 'fL', 'fR', 'tL', 'tR'], DIRS = new Set(['d', 'face', 'spine', 'u', 'v', 'tL', 'tR']);
const KEYS = { nunchaku: ['d', 'B', 'T'], poi: ['P', 'o'], staff: ['a', 'b'], buugeng: ['c', 'u', 'v'] };
const SWAP = { fL: 'fR', fR: 'fL', tL: 'tR', tR: 'tL' };
const clipOf = sp => { if (sp.clip === undefined) sp.clip = sp.make() || null; return sp.clip; };

// One hand's frame at phase ph, mirrored for the left hand and moved onto its own plane.
function sample(clip, ph, side, shift) {
  const N = clip.fr.length;
  let x, i, j;
  if (clip.loop) { x = (((ph % 1) + 1) % 1) * N; i = Math.floor(x) % N; j = (i + 1) % N; }
  else { x = clamp(ph, 0, 1) * (N - 1); i = Math.floor(x); j = Math.min(i + 1, N - 1); }
  const f = E.blend(clip.fr[i], clip.fr[j], x - Math.floor(x)), out = {};
  // Blending two frames shortens anything that is turning, so rigid parts are put back to length.
  const stretch = (a, b, L) => { const m = mul(add(f[a], f[b]), 0.5), u = mul(norm(sub(f[b], f[a])), L / 2); f[a] = sub(m, u); f[b] = add(m, u); };
  if (clip.kind === 'nunchaku') { stretch('B', 'T', E.STICK); f.d = norm(f.d); }
  if (clip.kind === 'staff') stretch('a', 'b', len(sub(clip.fr[i].b, clip.fr[i].a)));
  if (clip.kind === 'buugeng') { f.u = norm(f.u); f.v = norm(sub(f.v, mul(f.u, dot(f.v, f.u)))); }
  for (const k in f) {
    let v = side === 'L' ? flip(f[k]) : f[k];
    if (shift && !DIRS.has(k)) v = add(v, shift);
    out[side === 'L' && SWAP[k] || k] = v;
  }
  return out;
}
// The point of a one-hand frame that leaves the trail.
const tipOf = (kind, f, key) => kind === 'poi' ? f.P : kind === 'staff' ? f[key || 'a'] : kind === 'buugeng' ? Demo.sPoints(f.c, f.u, f.v)[0] : f.T;
// The marked end of a staff is the one that travels further (in an isolation the other end stays put).
function staffTip(clip) {
  if (clip.tip === undefined) { const go = k => clip.fr.reduce((a, f, i) => a + len(sub(f[k], clip.fr[(i + 1) % clip.fr.length][k])), 0); clip.tip = clip.kind === 'staff' && go('b') > go('a') * 1.2 ? 'b' : 'a'; }
  return clip.tip;
}
// How far the left clip must lag so the two marked points rise and fall together, or half a
// turn apart: the lag with the strongest match (or mismatch) between their heights above the hand.
function lagFor(clip, left, together) {
  const n = 96, col = (c, side, f) => Array.from({ length: n }, (_, i) => f(sample(c, i / n, side), c));
  const y = (f, c) => tipOf(c.kind, f, staffTip(c))[1] - f.h[1], a = col(clip, 'R', y), b = col(left, 'R', y);
  // Where the hands themselves travel, they should rise together and stay on the same side of the body.
  const hy = f => f.h[1], hx = f => f.h[0], ay = col(clip, 'R', hy), by = col(left, 'R', hy), ax = col(clip, 'R', hx), bx = col(left, 'L', hx);
  const centred = v => { const m = v.reduce((p, q) => p + q, 0) / n; return v.map(q => q - m); }, size = v => Math.sqrt(v.reduce((p, q) => p + q * q, 0)) || 1;
  const A = centred(a), Bc = centred(b), AY = centred(ay), BY = centred(by), moving = size(AY) > 0.3 && size(BY) > 0.3;
  let best = -Infinity, lag = 0;
  for (let k = 0; k < n; k++) {
    let tip = 0, hand = 0, apart = 0;
    for (let i = 0; i < n; i++) { const j = (i + k) % n; tip += A[i] * Bc[j]; hand += AY[i] * BY[j]; apart += (ax[i] - bx[j]) ** 2; }
    const sc = (together ? 1 : -1) * tip / (size(A) * size(Bc)) + (moving ? 0.5 * hand / (size(AY) * size(BY)) : 0) - 4 * apart / n;
    if (sc > best + 1e-9) { best = sc; lag = k / n; }
  }
  return lag;
}
// Which clip each hand plays: [side, clip, lag (in loops), rate, shift].
function hands(sp) {
  const clip = clipOf(sp);
  if (sp.both !== undefined) return [['R', clip, 0, 1], ['L', clip, sp.both, 1]];
  if (!sp.pair) return [['R', clip, 0, 1]];
  if (sp.pair.free) { const other = sp.pair.free(); return [['R', clip, 0, 1], ['L', other, 0, clip.dur / other.dur]]; }
  const left = sp.pair.left ? clipOf(sp.pair.left) : clip;
  const lag = typeof sp.pair.lag === 'function' ? sp.pair.lag() : sp.pair.lag !== undefined ? sp.pair.lag : clip.loop ? lagFor(clip, left, sp.pair.time !== 'split') : 0;
  return [['R', clip, 0, 1], ['L', left, lag, 1, sp.pair.shift]];
}
function scene(sp) {
  const main = clipOf(sp);
  if (!main) return null;
  if (main.scene) return { method: 'hand', trail: 0.5, ...main };
  const hs = hands(sp), N = main.loop ? clamp(Math.round(main.dur * FPS), 48, 600) : clamp(Math.round(main.dur * FPS) + 1, 2, 900), fr = [];
  for (let i = 0; i < N; i++) {
    const ph = main.loop ? i / N : i / (N - 1), f = {};
    hs.forEach(([side, clip, lag, rate, shift], j) => {
      const s = sample(clip, ph * rate + lag, side, shift);
      f['h' + side] = s.h;
      if (s.h2) f['h' + (side === 'R' ? 'L' : 'R')] = s.h2;
      if (s.e) f['e' + side] = s.e;
      if (j === 0) for (const k of BODY) if (s[k]) f[k] = s[k];
      if (clip.kind === 'nunchaku') f[j + '.g'] = s.g || s.h;
      for (const k of KEYS[clip.kind]) if (s[k]) f[j + '.' + k] = s[k];
    });
    fr.push(f);
  }
  const { bounce, contacts, closure, apex, labels, still } = main, air = main.air && { ...main.air, from: main.air.from / main.fr.length, to: main.air.to / main.fr.length };
  return {
    dur: main.dur, loop: main.loop, trail: main.trail || 0.3, method: sp.method || main.method || 'sim', props: hs.map(([side, clip]) => ({ kind: clip.kind, hand: side, ...(clip.kind === 'staff' && staffTip(clip) === 'b' ? { tip: 'b' } : null) })), fr,
    quads: sp.quads, labels, still, one: hs.length === 1 && !main.fr[0].h2 && !sp.fixed, meta: { air, bounce, contacts, closure, apex, clips: hs.length },
  };
}

module.exports = { scene, hands, sample, tipOf, staffTip, lagFor, clipOf, FPS };
