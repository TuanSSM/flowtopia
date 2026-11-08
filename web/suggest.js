'use strict';
// Next-trick suggestions. How well one trick flows into the next, from the measured state at the end
// of the first and the start of the second (web/clips/features.json, made by sim/features.js), or
// from the skills' catalog motion fields where there is no clip. Shared by the combo builder and the
// tests (node test/suggest.js); the weights come from web/suggest-weights.json.
const Suggest = (() => {
  const D = Math.PI / 180, { acos, abs, min, exp, hypot } = Math;
  // Starting weights from the physics: a change of plane costs most, then a reversal of the spin,
  // then moving the hand far; a change in spin speed costs least. Fitted weights replace these once
  // enough transitions have been marked (sim/fit-suggest.js).
  const DEFAULT = { plane: 2.0, sense: 1.2, hand: 1.5, omega: 0.4, grip: 1.0 };
  const PLANE_N = { wall: [1, 0, 0], face: [0, 0, 1], table: [0, 1, 0] };
  const dot = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
  const planeName = n => { const a = n.map(abs), i = a.indexOf(Math.max(...a)); return a[i] < 0.9 ? 'tilted' : ['wall', 'table', 'face'][i]; };
  const mirrorV = v => [-v[0], v[1], v[2]];
  // The states a skill can be in, one per variant: from its clip if it has one, else from its catalog
  // motion, else nothing known. Clips are recorded for the right hand; `side` 'L' mirrors them.
  function states(skill, feats, side = 'R') {
    const vs = feats || [];
    if (vs.length) return vs.map(v => {
      const p = v.props[0], m = side === 'L';
      const n0 = m ? mirrorV(p.normal) : p.normal, flipSense = m && abs(p.normal[0]) < 0.9 ? -1 : 1;
      const st = s => ({ hand: m ? mirrorV(s.hand) : s.hand, omega: m ? mirrorV(s.omega) : s.omega, grip: s.grip });
      return { from: 'clip', variant: v.name, normal: n0, plane: planeName(n0), sense: p.sense * flipSense, entry: st(p.entry), exit: st(p.exit) };
    });
    const mo = skill.motion || {};
    if (!mo.plane && !mo.direction) return [{ from: 'none' }];
    return [{ from: mo.plane === 'unknown' && mo.direction === 'unknown' ? 'none' : 'description', plane: mo.plane, direction: mo.direction, grip: mo.grip }];
  }
  const fmtCm = m => `${Math.round(m * 100)} cm`;
  // The terms of one transition, each 0 (no change) to about 1, with the words that explain them.
  function terms(a, b) {
    const t = {}, why = [];
    if (a.from === 'clip' && b.from === 'clip') {
      const ang = acos(min(1, abs(dot(a.normal, b.normal)))) / D;
      t.plane = ang / 90;
      why.push(ang < 15 ? `same ${a.plane} plane` : `plane turns ${Math.round(ang)} degrees, ${a.plane} to ${b.plane}`);
      if (ang < 30) { t.sense = a.sense && b.sense && a.sense !== b.sense ? 1 : 0; why.push(t.sense ? 'the spin reverses' : 'the spin keeps its direction'); }
      else t.sense = 0.5;
      const travel = hypot(a.exit.hand[0] - b.entry.hand[0], a.exit.hand[1] - b.entry.hand[1], a.exit.hand[2] - b.entry.hand[2]);
      t.hand = min(1, travel / 0.5);
      why.push(travel < 0.12 ? 'the hand is already in place' : `the hand moves ${fmtCm(travel)}`);
      const dw = hypot(a.exit.omega[0] - b.entry.omega[0], a.exit.omega[1] - b.entry.omega[1], a.exit.omega[2] - b.entry.omega[2]);
      t.omega = min(1, dw / (4 * Math.PI));
      if (dw > 6) why.push('the speed changes a lot');
      t.grip = a.exit.grip !== b.entry.grip ? 1 : 0;
      if (t.grip) why.push(`grip changes from ${a.exit.grip} to ${b.entry.grip}`);
      return { t, why, evidence: 'clip' };
    }
    // From the catalog: coarser, and said to be.
    const pa = a.plane || 'unknown', pb = b.plane || 'unknown';
    if (pa !== 'unknown' && pb !== 'unknown' && pa !== 'mixed' && pb !== 'mixed' && pa !== 'tilted' && pb !== 'tilted') { t.plane = pa === pb ? 0 : 1; why.push(pa === pb ? `same ${pa} plane` : `plane changes, ${pa} to ${pb}`); }
    else if ((pa === 'mixed' || pb === 'mixed') && pa !== 'unknown' && pb !== 'unknown') { t.plane = 0.3; why.push('can share a plane'); }
    else t.plane = 0.5;
    const da = a.direction || (a.sense ? 'known' : 'unknown'), db = b.direction || 'unknown';
    if (['forward', 'reverse'].includes(da) && ['forward', 'reverse'].includes(db)) { t.sense = da === db ? 0 : 1; why.push(da === db ? 'same direction' : 'the direction reverses'); }
    else t.sense = da === 'either' || db === 'either' ? 0.2 : 0.5;
    t.hand = 0.4; t.omega = 0.3;
    const ga = a.grip || (a.exit && a.exit.grip), gb = b.grip || (b.entry && b.entry.grip);
    t.grip = ga && gb && ga !== 'unknown' && gb !== 'unknown' ? (ga !== gb ? 1 : 0) : 0.3;
    const known = [a, b].filter(x => x.from !== 'none').length;
    return { t, why, evidence: known === 2 ? 'description' : 'little' };
  }
  // The best pairing of variants: the score (0 to 1, higher flows better), the reason and the evidence.
  function transition(skillA, featsA, skillB, featsB, weights = DEFAULT, side = 'R') {
    let best = null;
    for (const a of states(skillA, featsA, side)) for (const b of states(skillB, featsB, side)) {
      const { t, why, evidence } = terms(a, b), cost = Object.entries(t).reduce((s, [k, v]) => s + (weights[k] ?? DEFAULT[k]) * v, 0);
      const r = { score: exp(-cost), cost, terms: t, why, evidence, from: a.variant || '', to: b.variant || '' };
      if (!best || r.cost < best.cost) best = r;
    }
    return best;
  }
  // The cheapest chain of known tricks from one skill to another, at most `steps` links, never through a
  // transition the user marked Blocked. cost(a, b) gives the cost of a link (Infinity for none).
  function bridge(from, to, nodes, cost, steps = 3) {
    const dist = new Map([[from, 0]]), prev = new Map(), hops = new Map([[from, 0]]), todo = new Set([from]);
    while (todo.size) {
      let u = null;
      for (const x of todo) if (u === null || dist.get(x) < dist.get(u)) u = x;
      todo.delete(u);
      if (u === to) break;
      if (hops.get(u) >= steps) continue;
      for (const v of nodes) {
        if (v === u) continue;
        const c = cost(u, v);
        if (!isFinite(c)) continue;
        const d = dist.get(u) + c;
        if (!dist.has(v) || d < dist.get(v) - 1e-12) { dist.set(v, d); prev.set(v, u); hops.set(v, hops.get(u) + 1); todo.add(v); }
      }
    }
    if (!dist.has(to)) return null;
    const pathOut = [to];
    while (pathOut[0] !== from) pathOut.unshift(prev.get(pathOut[0]));
    return { path: pathOut, cost: dist.get(to) };
  }
  return { DEFAULT, states, terms, transition, bridge, planeName };
})();
if (typeof module !== 'undefined') module.exports = Suggest;
