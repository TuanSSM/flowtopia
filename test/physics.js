// Checks every demo clip, for every prop, against the physics it claims to model, against
// the definition of the move it shows, and against what a body can do. A move is listed in
// sim/specs.js only if it passes here.
//   node test/physics.js            everything        ONLY='poi:W' node test/physics.js   a subset
const B = require('../sim/bake.js'), E = require('../sim/engine.js'), { Demo } = E, EXPECT = require('./expect.js');
const { STICK, LINK, GRIP, TETHER } = E, { REACH, THIGH, SHIN } = Demo.dim;
const { add, sub, mul, dot, cross, len, norm } = Demo.v, mid = (a, b) => mul(add(a, b), 0.5);
const mm = v => (v * 1000).toFixed(0) + ' mm', deg = r => (r * 180 / Math.PI).toFixed(0);
let bad = 0, total = 0;

// ---------- contact geometry: the awkward cases ----------
if (!process.env.ONLY) {
  const near = (a, b) => Math.abs(a - b) < 1e-9, d = (p, q, r, t) => { const c = E.closest(p, q, r, t); return len(sub(c.c1, c.c2)); };
  const cases = [
    ['crossing segments touch', d([-1, 0, 0], [1, 0, 0], [0, -1, 0], [0, 1, 0]), 0],
    ['skew segments', d([-1, 0, 0], [1, 0, 0], [0, -1, 0.3], [0, 1, 0.3]), 0.3],
    ['parallel, overlapping', d([0, 0, 0], [1, 0, 0], [0.2, 0.5, 0], [0.8, 0.5, 0]), 0.5],
    ['parallel, side by side but apart', d([0, 0, 0], [1, 0, 0], [2, 0.5, 0], [3, 0.5, 0]), Math.hypot(1, 0.5)],
    ['collinear, end to end', d([0, 0, 0], [1, 0, 0], [1.4, 0, 0], [2, 0, 0]), 0.4],
    ['point against segment', d([0.5, 0.2, 0], [0.5, 0.2, 0], [0, 0, 0], [1, 0, 0]), 0.2],
    ['point beyond the end of a segment', d([2, 0, 0], [2, 0, 0], [0, 0, 0], [1, 0, 0]), 1],
    ['two points', d([0, 0, 0], [0, 0, 0], [0, 3, 4], [0, 3, 4]), 5],
    ['identical segments', d([0, 0, 0], [1, 0, 0], [0, 0, 0], [1, 0, 0]), 0],
    ['end touches the middle (T shape)', d([0.5, 0, 0], [0.5, 1, 0], [0, 0, 0], [1, 0, 0]), 0],
  ];
  let wrong = 0;
  for (const [name, got, want] of cases) if (!near(got, want)) { wrong++; bad++; console.log(`BAD closest points, ${name}: got ${got}, wanted ${want}`); }
  // Every answer must lie on its own segment, including for random pairs.
  let seed = 7; const rnd = () => (seed = (seed * 16807) % 2147483647) / 2147483647 * 2 - 1;
  for (let i = 0; i < 2000; i++) {
    const [p, q, r, t] = [0, 1, 2, 3].map(() => [rnd(), rnd(), rnd()]), c = E.closest(p, q, r, t);
    let best = Infinity;
    for (let a = 0; a <= 20; a++) for (let b = 0; b <= 20; b++) best = Math.min(best, len(sub(p.map((x, k) => x + (q[k] - x) * a / 20), r.map((x, k) => x + (t[k] - x) * b / 20))));
    if (!(c.s >= 0 && c.s <= 1 && c.t >= 0 && c.t <= 1) || len(sub(c.c1, c.c2)) > best + 1e-9) { wrong++; bad++; console.log('BAD closest points on a random pair', i); break; }
  }
  console.log(`contact geometry: ${cases.length} special cases and 2000 random pairs, ${wrong} wrong`);
}

const { shapes, overlaps, anatomy } = require('../sim/check.js'), R = { ROD: E.ROD, BALL: E.BALL };

console.log('clip'.padEnd(46) + 'frames  checks');
for (const { key, prop, id, sp, scene: c, error } of B.build(process.env.ONLY)) {
  total++;
  const name = `${key} ${sp.n}`, errs = [], notes = [];
  if (!c) { bad++; console.log(`BAD ${name}: ${error ? error.message : 'the move did not complete (no catch, or no bounce)'}`); continue; }
  const fr = c.fr, N = fr.length, m = c.meta || {}, dt = c.dur / N, k0 = c.props[0] ? c.props[0].kind : '';
  if (!fr.every(f => Object.values(f).every(v => v.every(Number.isFinite)))) errs.push('non-finite numbers');
  if (!fr.every(f => Object.keys(f).length === Object.keys(fr[0]).length)) errs.push('frames do not all have the same parts');
  if (c.loop && m.closure > 0.01) errs.push(`loop seam ${mm(m.closure)}`);
  notes.push(c.still ? 'still picture' : `${c.dur.toFixed(2)} s ${c.loop ? 'loop' : 'clip'}, ${{ sim: 'simulated', flight: 'simulated flight', solved: 'solved', hand: 'hand-driven', roll: 'rolling' }[c.method] || c.method}`);
  c.props.forEach((p, j) => {
    const K = (f, k) => f[j + '.' + k];
    if (p.kind === 'nunchaku') {
      const stick = Math.max(...fr.map(f => Math.abs(len(sub(K(f, 'T'), K(f, 'B'))) - STICK))), cord = Math.max(...fr.map(f => len(sub(K(f, 'B'), add(K(f, 'g'), mul(K(f, 'd'), GRIP))))));
      if (stick > 1e-3) errs.push(`stick length off by ${mm(stick)}`);
      // The swing that recovers after a catch is briefly faster, and the cord solver gives a little more.
      if (cord > LINK + (m.air || m.bounce ? 3.5e-3 : 1.5e-3)) errs.push(`cord stretched to ${(cord * 1000).toFixed(1)} mm`);
      if (Math.min(...fr.map(f => Math.min(K(f, 'T')[1], K(f, 'B')[1]))) < 0.02) errs.push('goes through the floor');
    } else if (p.kind === 'poi') {
      const t = fr.map(f => len(sub(K(f, 'P'), K(f, 'o') || f['h' + p.hand]))), slack = t.filter(x => x < TETHER * 0.97).length / N;
      if (!K(fr[0], 'q1')) {
        if (Math.max(...t) > TETHER + 1.5e-3) errs.push(`tether stretched to ${(Math.max(...t) * 1000).toFixed(1)} mm`);
        if (slack > 0.02 && !sp.slack) errs.push(`tether slack ${(slack * 100).toFixed(0)}% of the time`);
      }
      if (Math.min(...fr.map(f => K(f, 'P')[1])) < 0.08) errs.push('head hits the floor');
    } else if (p.kind === 'staff') {
      const full = fr.map(f => len(sub(K(f, 'a'), K(f, 'b'))));
      if (Math.max(...full) - Math.min(...full) > 2e-4) errs.push('staff length changes');
      if (Math.min(...fr.map(f => Math.min(K(f, 'a')[1], K(f, 'b')[1]))) < 0.03) errs.push('staff hits the floor');
      // Outside a throw, some hand is always on the staff.
      const off = Math.max(...fr.map((f, i) => m.air && i / N >= m.air.from - 0.01 && i / N <= m.air.to + 0.01 ? 0 : Math.min(...['hL', 'hR'].filter(h => f[h]).map(h => { const q = E.closest(f[h], f[h], K(f, 'a'), K(f, 'b')); return len(sub(q.c1, q.c2)); }))));
      if (off > 0.056 && !sp.free) errs.push(`no hand on the staff (nearest is ${mm(off)} away)`); // the staff lies in the palm, up to a hand's thickness from the wrist
    } else if (p.kind === 'buugeng') {
      const sq = Math.max(...fr.map(f => Math.max(Math.abs(len(K(f, 'u')) - 1), Math.abs(len(K(f, 'v')) - 1), Math.abs(dot(K(f, 'u'), K(f, 'v'))))));
      if (sq > 2e-3) errs.push('shape is not rigid');
      if (Math.max(...fr.map(f => len(sub(K(f, 'c'), f['h' + p.hand])))) > 2e-4) errs.push('hand off the handle');
    }
  });
  if (m.bounce) {
    // A bounce must really touch its target, lose speed there, and send the striker back the other way.
    const turned = m.contacts.filter(x => x.name === 'turned').length, others = [...new Set(m.contacts.map(x => x.name))].filter(x => x !== 'turned' && x !== m.bounce.on && x !== 'handle'); // the striker may brush its own handle as it turns
    const tip = fr.map(f => f['0.T']), k = Math.round(m.bounce.at * N), v = i => len(sub(tip[Math.min(i + 1, N - 1)], tip[Math.max(i - 1, 0)])) / (2 * dt);
    const span = Math.max(4, Math.round(0.04 / dt)), before = v(Math.max(k - span, 1)), after = v(Math.min(k + span, N - 2));
    if (turned !== 1) errs.push(`direction changed ${turned} times, wanted once`);
    if (others.length) errs.push(`also touches the ${others.join(', ')}`);
    if (after >= before) errs.push(`tip speed ${before.toFixed(1)} m/s before and ${after.toFixed(1)} after: the bounce added speed`);
    if (m.bounce.speed > 6) errs.push(`hits at ${m.bounce.speed.toFixed(1)} m/s, which is a blow, not a bounce`);
    notes.push(`meets the ${m.bounce.on} at ${m.bounce.speed.toFixed(1)} m/s, tip speed ${before.toFixed(1)} to ${after.toFixed(1)} m/s, then turns the other way`);
  }
  if (m.apex !== undefined) notes.push(`apex ${m.apex.toFixed(0)} deg`);
  // Throws: the flight must be free fall, the prop must really leave the hand, turn as
  // promised, and come back within reach of a hand that does not teleport.
  if (m.air) {
    const from = Math.round(m.air.from * N), to = Math.round(m.air.to * N);
    const mids = fr.map(f => k0 === 'staff' ? mid(f['0.a'], f['0.b']) : mid(add(f['0.g'], mul(f['0.d'], (GRIP - 0.18) / 2)), mid(f['0.B'], f['0.T'])));
    let acc = 0, k = 0;
    for (let i = from + 3; i < to - 3; i++) { acc += (mids[i + 1][1] - 2 * mids[i][1] + mids[i - 1][1]) / (dt * dt); k++; }
    acc /= k;
    if (Math.abs(acc + 9.81) > (k0 === 'staff' ? 0.15 : 1.2)) errs.push(`centre of mass falls at ${acc.toFixed(2)} m/s2, not -9.81`);
    const held = k0 === 'staff' ? f => mid(f['0.a'], f['0.b']) : f => f['0.g'], hs = ['hR', 'hL'].filter(h => fr[0][h]);
    const gap = Math.max(...fr.slice(from, to).map(f => Math.min(...hs.map(h => len(sub(held(f), f[h]))))));
    if (gap < 0.1) errs.push(`prop never gets more than ${mm(gap)} from a hand`);
    const after = fr[Math.min(to + 3, N - 1)], want = m.air.other ? 'hL' : 'hR';
    if (len(sub(held(after), after[want])) > 2e-3) errs.push(`prop is not in the ${want === 'hL' ? 'other' : 'throwing'} hand after the catch`);
    if (k0 === 'nunchaku' && m.air.turns < 1) errs.push(`handle turned only ${m.air.turns.toFixed(2)} times`);
    if (k0 === 'staff' && Math.min(...[-1, 0, 1].map(d => Math.abs(fr[from + d]['0.a'][1] - fr[from + d]['0.b'][1]) / len(sub(fr[from + d]['0.a'], fr[from + d]['0.b'])))) > 0.06) errs.push('staff is not level at release');
    if (k0 === 'nunchaku' && !m.air.other && m.air.twist > 1.3) errs.push(`handle is ${m.air.twist.toFixed(2)} rad away from the hold when caught`);
    if (k0 === 'nunchaku') notes.push(m.air.bumps ? `sticks or body touched ${m.air.bumps} times in the air` : 'nothing touched in the air');
    notes.push(`${m.air.time.toFixed(2)} s in the air, free fall ${acc.toFixed(2)} m/s2${k0 === 'nunchaku' ? `, ${m.air.turns.toFixed(2)} turns, caught ${m.air.reach.toFixed(2)} m from the shoulder` : `, rises ${m.air.rise.toFixed(2)} m`}`);
  }
  // Solved clips: gravity plus a pull along the tether must account for the head's motion.
  if (c.method === 'solved') c.props.forEach((p, j) => {
    if (p.kind !== 'poi') return;
    let worst = 0;
    for (let i = 0; i < N; i++) {
      if (!c.loop && (i < 1 || i > N - 2)) continue;
      const P = k => fr[(i + k + N) % N][j + '.P'], a = mul(add(sub(P(1), mul(P(0), 2)), P(-1)), 1 / (dt * dt)), need = sub(a, [0, -9.81, 0]), t = sub(fr[i]['h' + p.hand], P(0));
      worst = Math.max(worst, Math.acos(Math.min(1, dot(norm(need), norm(t)))));
    }
    if (worst > 0.12) errs.push(`the tether is up to ${deg(worst)} degrees off the line of the force the head needs`);
    else notes.push(`tether within ${deg(worst)} degrees of the force the head needs`);
  });
  // Nothing moves far enough in one simulation step to jump through a stick or a limb.
  if (c.method === 'sim') {
    const tips = fr.map(f => Demo.parts(c.props[0], 0, f).tip), fastest = Math.max(...tips.slice(1).map((q, i) => len(sub(q, tips[i])))) / dt, perStep = fastest / 1500;
    if (perStep > R.ROD * 0.5) errs.push(`moves ${mm(perStep)} per step at ${fastest.toFixed(1)} m/s: fast enough to tunnel through a stick`);
  }
  // Nothing may pass through the body or through another prop, and the limbs stay out of the body.
  if (c.hide) { console.log(`ok  ${name}`.padEnd(46) + String(N).padEnd(8) + 'still picture, close-up'); continue; }
  const o = overlaps(c), tol = c.method === 'sim' || c.method === 'flight' ? 0.008 : 0.003;
  if (o.prop[0] > tol && !(sp.contact && o.prop[0] < 0.02)) errs.push(`${mm(o.prop[0])} ${o.prop[1]}`);
  if (o.grip[0] > 0.02 && !sp.fingers) errs.push(`${mm(o.grip[0])} ${o.grip[1]}`);
  if (o.limb[0] > 0.012) errs.push(`${mm(o.limb[0])} ${o.limb[1]}`);
  const an = anatomy(c, sp);
  errs.push(...an.errs);
  // The figure's hand is a closed fist with no wrist, so how the prop turns inside it is measured, not enforced.
  if (an.twist > 3.6) notes.push(`${deg(an.twist)} degrees of turn inside the hand, which the figure does not show`);
  // A move that is not about contact should not touch anything while it is recorded.
  if (m.contacts && m.contacts.length && !sp.contact) errs.push(`touches the ${[...new Set(m.contacts.map(x => x.name))].join(', ')}`);
  const tests = EXPECT[key], t = tests && (tests[sp.n] || tests['*']);
  if (t) { const r = t(c, sp); if (r !== true) errs.push(r); else notes.push('matches its definition'); }
  // Two-prop moves: check the timing and direction the variant promises, from each marked
  // point's motion around its own hand.
  if (sp.pair && c.loop && sp.pair.time) {
    const n = 192, rel = j => Array.from({ length: n }, (_, i) => { const f = Demo.pose(c, i / n), p = c.props[j]; return sub(p.kind === 'staff' ? f[j + '.' + (p.tip || 'a')] : Demo.parts(p, j, f).tip, f['h' + p.hand]); }), A = rel(0), B = rel(1);
    const corr = A.reduce((t, a, i) => t + a[1] * B[i][1], 0) / Math.sqrt(A.reduce((t, a) => t + a[1] * a[1], 0) * B.reduce((t, a) => t + a[1] * a[1], 0));
    const together = sp.pair.time === 'together';
    if (together ? corr < 0.85 : corr > -0.85) errs.push(`heights of the two marked points match by ${corr.toFixed(2)}, wanted ${together ? 'close to 1 (together)' : 'close to -1 (split)'}`);
    const spread = k => Math.max(...A.map(q => q[k])) - Math.min(...A.map(q => q[k])), ax = spread(0) > spread(2) ? 0 : 2; // wall plane if it spreads more in x than in z
    const tR = EXPECT.turns(A, [0, 0, 0], ax), tL = EXPECT.turns(B, [0, 0, 0], ax), same = Math.sign(tR) === Math.sign(tL);
    if (tR === 0 || tL === 0) errs.push('a marked point does not go round its hand');
    else if (same === !!sp.pair.opposite) errs.push(`the two turn the ${same ? 'same' : 'opposite'} way, wanted ${sp.pair.opposite ? 'opposite' : 'same'}`);
    notes.push(`${together ? 'together' : 'split'} time, ${sp.pair.opposite ? 'opposite' : 'same'} direction`);
  }
  if (errs.length) bad++;
  console.log(`${errs.length ? 'BAD' : 'ok '} ${name}`.padEnd(46) + String(N).padEnd(8) + (errs.length ? errs.join('; ') : notes.join(', ')));
}
console.log(`${total} clips, problems: ${bad}`);
process.exit(bad ? 1 : 0);
