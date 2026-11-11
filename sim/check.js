'use strict';
// Measurements on a finished scene: how far props and limbs overlap, and what the body is
// asked to do. Used by the tests, and by the search for a posture that keeps a prop clear.
const E = require('./engine.js'), { Demo } = E, { GRIP } = E;
// Body lengths are read on each call: they change when a body preset is checked (sim/presets.js).
const D = () => Demo.dim;
const { add, sub, mul, dot, cross, len, norm } = Demo.v;
const mm = v => (v * 1000).toFixed(0) + ' mm', deg = r => (r * 180 / Math.PI).toFixed(0);

const R = { ROD: E.ROD, BALL: E.BALL };
// The solid parts of prop i in frame f: [end, end, radius, name, held in the fist].
function shapes(p, i, f) {
  const K = k => f[i + '.' + k];
  if (p.kind === 'poi') return [[K('P'), K('P'), R.BALL, 'head of the poi', false], [K('o') || f['h' + p.hand], K('P'), 0.004, 'tether', true]];
  if (p.kind === 'staff') return [[K('a'), K('b'), 0.017, 'staff', true]];
  if (p.kind === 'buugeng') { const pts = Demo.sPoints(K('c'), K('u'), K('v')); return pts.slice(1).map((q, j) => [pts[j], q, 0.018, 'buugeng', true]); }
  if (p.kind === 'nunchaku') return [[add(K('g'), mul(K('d'), GRIP)), sub(K('g'), mul(K('d'), 0.18)), R.ROD, 'handle', true], [K('B'), K('T'), R.ROD, 'striker', false]];
  return [];
}
// The hand that has hold of a prop in this frame: the nearer one, since a throw can change it.
function holder(p, i, f) {
  if (p.kind === 'poi' || !f.hL || !f.hR) return p.hand;
  const [a, b] = shapes(p, i, f)[0], d = h => { const q = E.closest(h, h, a, b); return len(sub(q.c1, q.c2)); };
  return d(f.hL) < d(f.hR) - 1e-9 ? 'L' : d(f.hR) < d(f.hL) - 1e-9 ? 'R' : p.hand;
}
// How far the props go into the body or into each other, and how far the limbs go into the
// body or each other. Returns the worst case of each.
function overlaps(sc) {
  const N = sc.fr.length, n = Math.min(N, 300), air = sc.meta && sc.meta.air, prop = [0, ''], limb = [0, ''], grip = [0, ''];
  let at = 0;
  // The worst case is the deepest there is, to the last bit. What is named with it (which part, and how
  // far through the clip) moves on to a later moment only when it is deeper there by more than a
  // micrometre, so the moment named is within a micrometre of the worst. A loop often reaches the same
  // depth two or four times, which of them is deeper in the last bit differs from one JavaScript engine
  // to the next, and the name goes into web/clips/presets.json. w[2] is the depth at the moment named.
  const note = (w, pen, where) => { if (pen > w[0]) { w[0] = pen; if (!(pen <= w[2] + 1e-6)) { w[2] = pen; w[1] = `${where} (${Math.round(at * 100)}% through)`; } } };
  for (let s = 0; s < n; s++) {
    const i = Math.floor(s * N / n); at = i / N;
    const f = sc.fr[i], J = Demo.rig(f), hold = {}, inAir = air && i / N >= air.from && i / N < air.to;
    // Every hand that is on a prop holds it: usually one, but two during a hand change.
    const who = sc.props.map((p, j) => {
      if (inAir && j === 0) return '';
      if (p.kind === 'poi') return p.hand;
      // For a nunchaku either stick may be the one in the hand (after a roll it is the other one).
      const rods = shapes(p, j, f).slice(0, p.kind === 'nunchaku' ? 2 : 1), on = ['L', 'R'].filter(h => rods.some(([a, b]) => { const q = E.closest(f['h' + h] || J['h' + h], f['h' + h] || J['h' + h], a, b); return len(sub(q.c1, q.c2)) < 0.06; }));
      return on.join('') || holder(p, j, f);
    });
    for (const w of who) for (const h of w) hold[h] = true;
    const caps = Demo.caps(J, hold), all = sc.props.map((p, j) => shapes(p, j, f));
    sc.props.forEach((p, j) => {
      for (const [a, b, r, name, held] of all[j]) for (const c of caps) {
        const arm = /arm/.test(c.name), own = arm && who[j].includes(c.side);
        if (inAir && j === 0 && arm) continue;                       // the arms are moving to make the catch
        if (own && (name === 'head of the poi' || name === 'tether') && c.name === 'forearm') continue; // the tether leaves from the fist
        if (name === 'tether' && own) continue;
        const q = E.closest(a, b, c.a, c.b), pen = c.r + r - len(sub(q.c1, q.c2));
        if (own && name === 'handle') continue;
        if (own && name === 'striker') { const hd = J['h' + c.side], q0 = E.closest(hd, hd, a, b); if (len(sub(q0.c1, q0.c2)) < 0.06) continue; } // the striker is the stick in this hand                      // the tail of the handle lies along the forearm
        if (own && held) {
          // A held stick lies in the palm and may rest along the wrist. Further up the forearm it must be clear.
          if (c.name !== 'forearm') { note(grip, pen, `${name} through its own upper arm`); continue; }
          const el = J['el' + c.side], hand = J['h' + c.side], q2 = E.closest(a, b, el, add(hand, mul(norm(sub(el, hand)), 0.13)));
          note(grip, c.r + r - len(sub(q2.c1, q2.c2)), `${name} through the forearm of the hand that holds it`);
        }
        else note(prop, pen, `${name} into the ${c.side ? (c.side === 'L' ? 'left ' : 'right ') : ''}${c.name}`);
      }
      for (let m = j + 1; m < all.length; m++) for (const [a, b, r, name] of all[j]) for (const [a2, b2, r2, name2] of all[m]) {
        if (name === 'tether' && name2 === 'tether' && sc.props[j].hand === sc.props[m].hand) continue; // two poi in one fist
        const q = E.closest(a, b, a2, b2);
        note(prop, r + r2 - len(sub(q.c1, q.c2)), `${name} into the other ${name2}`);
      }
    });
    // Limbs: each arm against the torso, the head, the legs and the other arm.
    // Forearms are measured to the wrist, so two hands may touch.
    const free = Demo.caps(J, { L: true, R: true }), arms = free.filter(c => /arm/.test(c.name));
    for (const a of arms) for (const c of free) {
      if (c === a || (c.side === a.side && /arm/.test(c.name))) continue;
      if (/arm/.test(c.name) && c.side < a.side) continue; // each pair once
      // An upper arm hangs against the ribs, and a hand may rest on the body: only the forearm line counts there.
      if (a.name === 'upper arm' && (c.name === 'torso' || c.name === 'head')) continue;
      const q = E.closest(a.a, a.b, c.a, c.b);
      note(limb, a.r + c.r - len(sub(q.c1, q.c2)), `${a.side === 'L' ? 'left' : 'right'} ${a.name} into the ${c.side ? (c.side === 'L' ? 'left ' : 'right ') : ''}${c.name}`);
    }
  }
  return { prop, limb, grip };
}
// What the body is asked to do: reach, hand speed, leg length, feet on the floor, balance,
// and how far the wrist would have to turn to keep hold of a rigid prop.
function anatomy(sc, sp) {
  const errs = [], N = sc.fr.length, dt = sc.dur / N, js = sc.fr.map(f => Demo.rig(f)), air = sc.meta && sc.meta.air;
  for (const s of ['L', 'R']) {
    const REACH = D().REACH, far = Math.max(...js.map(J => len(sub(J['h' + s], J['sh' + s])))), limit = air ? REACH + 0.07 : REACH + 0.03;
    if (far > limit) errs.push(`${s} hand ${far.toFixed(2)} m from its shoulder, and an arm is ${REACH.toFixed(2)} m`);
    const sp1 = i => len(sub(js[(i + 1) % N]['h' + s], js[i]['h' + s])) / dt, fast = Math.max(...js.map((_, i) => i < N - 1 || sc.loop ? sp1(i) : 0));
    if (fast > 9) errs.push(`${s} hand moves at ${fast.toFixed(1)} m/s`);
    const leg = Math.max(...js.map(J => len(sub(J['f' + s], J['hip' + s]))));
    if (leg > D().THIGH + D().SHIN + 0.012) errs.push(`${s} leg stretched to ${leg.toFixed(2)} m`);
    if (Math.min(...js.map(J => J['f' + s][1])) < 0.03) errs.push(`${s} foot goes into the floor`);
  }
  // Balance: the pelvis stays over the feet (within a hand's width of the line between them).
  const lean = Math.max(...js.map(J => { const q = E.closest(J.pelvis, J.pelvis, [J.fL[0], J.pelvis[1], J.fL[2]], [J.fR[0], J.pelvis[1], J.fR[2]]); return len(sub(q.c1, q.c2)); }));
  if (lean > 0.14) errs.push(`pelvis ${mm(lean)} away from the line between the feet`);
  if (Math.min(...js.map(J => len(sub(J.fL, J.fR)))) < 0.09) errs.push('feet pass through each other');
  // Wrist: follow the prop's direction around the forearm. A full turn needs a hand change or finger work.
  let twist = 0;
  sc.props.forEach((p, j) => {
    if ((p.kind !== 'staff' && p.kind !== 'buugeng') || air) return;
    const dir = f => p.kind === 'staff' ? norm(sub(f[j + '.a'], f[j + '.b'])) : f[j + '.u'];
    let acc = 0, prev = null, lo = 0, hi = 0;
    for (let i = 0; i <= N; i++) {
      const f = sc.fr[i % N], J = js[i % N], s = holder(p, j, f), fa = norm(sub(J['h' + s], J['el' + s])), u = dir(f), up = norm(sub(J['el' + s], J['sh' + s]));
      const perp = v => sub(v, mul(fa, dot(v, fa))), a = perp(u), b = perp(up);
      if (len(a) < 0.3 || len(b) < 0.2) { continue; }
      const ang = Math.atan2(dot(cross(b, a), fa), dot(a, b)) * (p.kind === 'staff' ? 2 : 1); // a staff looks the same after half a turn
      if (prev !== null) { let d = ang - prev; d -= 2 * Math.PI * Math.round(d / (2 * Math.PI)); acc += d; lo = Math.min(lo, acc); hi = Math.max(hi, acc); }
      prev = ang;
      if (!sc.loop && i === N - 1) break;
    }
    twist = Math.max(twist, (hi - lo) / (p.kind === 'staff' ? 2 : 1));
  });
  return { errs, twist };
}

// The floor: how low each prop may go (metres).
function floorErrors(c) {
  const errs = [];
  c.props.forEach((p, j) => {
    const lo = k => Math.min(...c.fr.map(f => f[j + '.' + k][1]));
    if (p.kind === 'nunchaku' && Math.min(lo('T'), lo('B')) < 0.02) errs.push('goes through the floor');
    if (p.kind === 'poi' && lo('P') < 0.08) errs.push('head hits the floor');
    if (p.kind === 'staff' && Math.min(lo('a'), lo('b')) < 0.03) errs.push('staff hits the floor');
  });
  return errs;
}
// What the body checks find wrong with a clip that shows the body: props through the body or each
// other, a held stick through its own arm, limbs through the body, and anatomy. sp is the clip's spec
// (contact and fingers relax two of them). Used by test/physics.js and, per body preset, sim/presets.js.
function bodyErrors(c, sp) {
  const errs = [], o = overlaps(c), tol = c.method === 'sim' || c.method === 'flight' ? 0.008 : 0.003;
  if (o.prop[0] > tol && !(sp.contact && o.prop[0] < 0.02)) errs.push(`${mm(o.prop[0])} ${o.prop[1]}`);
  // A held stick may rest on the forearm, not sink into it: the capsules are the drawn skin.
  if (o.grip[0] > 0.01 && !sp.fingers) errs.push(`${mm(o.grip[0])} ${o.grip[1]}`);
  // The capsules are the drawn skin, and skin and muscle give about 2 cm when an arm presses on the body.
  if (o.limb[0] > 0.02) errs.push(`${mm(o.limb[0])} ${o.limb[1]}`);
  const an = anatomy(c, sp);
  errs.push(...an.errs);
  return { errs, twist: an.twist };
}

module.exports = { shapes, holder, overlaps, anatomy, floorErrors, bodyErrors, mm, deg };
