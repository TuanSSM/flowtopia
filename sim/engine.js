'use strict';
// The physics behind the demonstrations. It runs at build time (sim/bake.js), not in the
// browser. Three kinds of clip are made here:
//   simulated    position-based dynamics: gravity, drag, cord or tether tension, contact
//   solved       the exact hand path that makes a tethered weight follow a given path
//   hand-driven  rigid props held in the hand, which go where the hand takes them
// Every clip is written for the right hand; the composer mirrors it for the left.
const fs = require('fs'), path = require('path');
const Demo = (0, eval)(fs.readFileSync(path.join(__dirname, '..', 'web', 'demo.js'), 'utf8').replace('const Demo =', 'globalThis.Demo =') + ';Demo');
const { add, sub, mul, dot, cross, len, norm, mix, rot, clamp, smooth, flip } = Demo.v, { rig, caps: capsOf } = Demo;
const TAU = Math.PI * 2, { sin, cos, sqrt } = Math;

/* ---------- physical constants (metres, seconds) ---------- */
const STICK = 0.30, LINK = 0.09;      // 12 inch sticks, 3.5 inch cord
const GRIP = 0.12, BUTT = 0.18;       // fist to link end, fist to tail end of the handle
const GRAVITY = [0, -9.81, 0], DRAG = 0.3, REV = 0.56; // drag per second; seconds per turn
// Two point masses this far apart have the same inertia as a uniform rod of length STICK.
const INNER = STICK / sqrt(3), W1 = (1 + sqrt(3)) / 2, W2 = (1 - sqrt(3)) / 2;

/* ---------- wrist models: where the fist is and where the handle points ---------- */
// World: x toward the figure's left, y up, z forward. The right hand works at x < 0.
// n is the direction the handle points on average; the swing angle grows from a toward b.
const BASIS = {
  wall: { n: [-1, 0, 0], a: [0, 0, 1], b: [0, 1, 0] },
  face: { n: [0, 0, 1], a: [-1, 0, 0], b: [0, 1, 0] },
  table: { n: [0, 1, 0], a: [0, 0, 1], b: [-1, 0, 0] },
};
const CENTRE = { wall: [-0.32, 1.02, 0.2], face: [-0.22, 1.14, 0.48], table: [-0.46, 1.52, 0.45], roof: [-0.1, 1.98, 0.05] };
const spoke = (bs, t) => add(mul(bs.a, cos(t)), mul(bs.b, sin(t)));
const NORMAL = { gamma: 0.61, rh: 0.03 }, POINT = { gamma: 0.5, rh: 0.03 }, PIVOT = { gamma: 0.9, rh: 0.07 };

// A wrist model maps a swing angle to a fist position and a handle direction. The
// simulation feeds it the striker's own angle plus a small lead, the way a hand
// follows the stick and pulls slightly ahead of it. `at` is the point the striker
// circles around, and `cycle` is how much swing angle makes one repeat of the move.

// The handle circles on a cone around n while the fist makes a small circle.
function cone(o) {
  const bs = BASIS[o.plane === 'roof' ? 'table' : o.plane], c = o.c || CENTRE[o.plane];
  return {
    bs, s: o.dir, cycle: TAU, at: () => c,
    pose: th => { const e = spoke(bs, th); return { h: add(c, mul(e, o.rh)), d: norm(add(mul(bs.n, cos(o.gamma)), mul(e, sin(o.gamma)))) }; },
  };
}
// Figure-8 from wall to wall: one turn at each side. The hand crosses the body while
// the striker is in front, and the handle tips over to point outward on the new side.
function eightWall(o) {
  const bs = BASIS.wall, g = 0.7, side = th => sin(th / 2), at = th => [-0.1 - o.a * side(th), o.y || 1.1, o.z || 0.38];
  return {
    bs, s: o.dir, cycle: 2 * TAU, at,
    pose: th => { const e = spoke(bs, th); return { h: add(at(th), mul(e, 0.03)), d: norm(add(mul(bs.n, cos(g) * side(th)), mul(e, sin(g)))) }; },
  };
}
// Figure-8 from roof to floor: a flat turn overhead, then one at shoulder height.
function eightTall(o) {
  const bs = BASIS.table, at = th => mix(CENTRE.roof, CENTRE.table, (1 - cos(th / 2)) / 2);
  return {
    bs, s: o.dir, cycle: 2 * TAU, at,
    pose: th => { const e = spoke(bs, th); return { h: add(at(th), mul(e, 0.03)), d: norm(add(mul(bs.n, cos(0.61)), mul(e, sin(0.61)))) }; },
  };
}


/* ---------- the body, and props touching it ---------- */
// The body is a set of capsules (a line with a radius) taken from the figure's joints. A
// stick is a capsule too, and a poi head is a ball. Contact pushes the prop out, takes away
// the speed going in, gives back a fraction of it (the body absorbs the rest), and adds
// friction along the surface.
const ROD = 0.0225, BALL = 0.05;     // stick radius, poi head radius
const BOUNCE = 0.35, GRIP_MU = 0.3;  // fraction of speed returned by a bounce; friction
const SOFT = 0.4;                    // below this speed (m/s) nothing bounces: it just rests
const cap = (a, b, r, name) => ({ a, b, r, name });
const STILL = capsOf(rig({})).filter(c => !/arm/.test(c.name));
// The whole body for a working hand (k is -1 for the right), the other hand, and a body pose.
const bodyAt = (hand, k = -1, other, pose) => {
  const p = { ...pose }, mine = k < 0 ? 'R' : 'L', theirs = k < 0 ? 'L' : 'R';
  p['h' + mine] = hand; if (other) p['h' + theirs] = other;
  return capsOf(rig(p), { [mine]: true });
};

// Closest points between two line segments. Handles the awkward cases: either segment
// being a single point, and parallel segments (where there is no unique answer).
function closest(p1, q1, p2, q2) {
  const d1 = sub(q1, p1), d2 = sub(q2, p2), r = sub(p1, p2), a = dot(d1, d1), e = dot(d2, d2), f = dot(d2, r), TINY = 1e-12;
  let s = 0, t = 0;
  if (a > TINY && e <= TINY) s = clamp(-dot(d1, r) / a, 0, 1);
  else if (a <= TINY && e > TINY) t = clamp(f / e, 0, 1);
  else if (a > TINY) {
    const c = dot(d1, r), b = dot(d1, d2), den = a * e - b * b;
    s = den > TINY * a * e + TINY ? clamp((b * f - c * e) / den, 0, 1) : 0.5; // parallel: start from the middle
    t = (b * s + f) / e;
    if (t < 0) { t = 0; s = clamp(-c / a, 0, 1); } else if (t > 1) { t = 1; s = clamp((b - c) / a, 0, 1); }
  }
  return { s, t, c1: add(p1, mul(d1, s)), c2: add(p2, mul(d2, t)) };
}
// The speed change a contact gives: stop the approach, return part of it, and drag along the surface.
function response(v, n) {
  const vn = dot(v, n);
  if (vn >= 0) return null; // already moving apart
  const jn = -(1 + (-vn < SOFT ? 0 : BOUNCE)) * vn, vt = sub(v, mul(n, vn)), tl = len(vt);
  return add(mul(n, jn), tl > 1e-6 ? mul(vt, -Math.min(GRIP_MU * jn, tl) / tl) : [0, 0, 0]);
}
// Move a held stick (hand h, direction d) out of the body: a hand cannot push it through.
function clearOf(q, caps) {
  let h = q.h;
  for (let pass = 0; pass < 3; pass++) for (const c of caps) {
    const r = closest(sub(h, mul(q.d, BUTT)), add(h, mul(q.d, GRIP)), c.a, c.b), gap = sub(r.c1, r.c2), dist = len(gap), pen = c.r + ROD + 0.004 - dist;
    if (pen > 0) h = add(h, mul(dist > 1e-9 ? mul(gap, 1 / dist) : [0, 0, 1], pen));
  }
  return { h, d: q.d };
}
// A stick against capsules. m holds its two inner masses (p1, p2) and their previous
// positions (o1, o2). With `bounce` the velocities change too; without, the stick is only
// moved out, which adds no energy. Returns what was hit, and how fast.
function touchRod(m, caps, bounce, H) {
  const hits = [];
  for (const c of caps) {
    const q = closest(add(mul(m.p1, W1), mul(m.p2, W2)), add(mul(m.p1, W2), mul(m.p2, W1)), c.a, c.b);
    const gap = sub(q.c1, q.c2), dist = len(gap), pen = c.r + ROD - dist;
    if (pen <= 0) continue;
    const n = dist > 1e-9 ? mul(gap, 1 / dist) : [0, 0, 1]; // dead centre: any way out will do
    const w1 = W1 * (1 - q.s) + W2 * q.s, w2 = W2 * (1 - q.s) + W1 * q.s, W = w1 * w1 + w2 * w2;
    const d1 = mul(n, w1 * pen / W), d2 = mul(n, w2 * pen / W);
    m.p1 = add(m.p1, d1); m.p2 = add(m.p2, d2); m.o1 = add(m.o1, d1); m.o2 = add(m.o2, d2);
    if (!bounce) continue;
    const v = add(mul(sub(m.p1, m.o1), w1 / H), mul(sub(m.p2, m.o2), w2 / H)), dv = response(v, n);
    if (!dv) continue;
    m.o1 = sub(m.o1, mul(dv, w1 * H / W)); m.o2 = sub(m.o2, mul(dv, w2 * H / W));
    hits.push({ name: c.name, speed: -dot(v, n) });
  }
  return hits;
}
// Two free sticks against each other: each takes half the push, and the impulse is shared.
function touchPair(a, b, bounce, H) {
  const q = closest(add(mul(a.p1, W1), mul(a.p2, W2)), add(mul(a.p1, W2), mul(a.p2, W1)), add(mul(b.p1, W1), mul(b.p2, W2)), add(mul(b.p1, W2), mul(b.p2, W1)));
  const gap = sub(q.c1, q.c2), dist = len(gap), pen = 2 * ROD - dist;
  if (pen <= 0) return false;
  const n = dist > 1e-9 ? mul(gap, 1 / dist) : [1, 0, 0];
  const wa = [W1 * (1 - q.s) + W2 * q.s, W2 * (1 - q.s) + W1 * q.s], wb = [W1 * (1 - q.t) + W2 * q.t, W2 * (1 - q.t) + W1 * q.t];
  const Wa = wa[0] ** 2 + wa[1] ** 2, Wb = wb[0] ** 2 + wb[1] ** 2;
  const shift = (m, w, W, k) => { for (const [p, o, i] of [['p1', 'o1', 0], ['p2', 'o2', 1]]) { const d = mul(n, k * w[i] * pen / 2 / W); m[p] = add(m[p], d); m[o] = add(m[o], d); } };
  shift(a, wa, Wa, 1); shift(b, wb, Wb, -1);
  if (!bounce) return true;
  const vel = (m, w) => add(mul(sub(m.p1, m.o1), w[0] / H), mul(sub(m.p2, m.o2), w[1] / H)), dv = response(sub(vel(a, wa), vel(b, wb)), n);
  if (!dv) return true;
  const j = mul(dv, 1 / (Wa + Wb));
  a.o1 = sub(a.o1, mul(j, wa[0] * H)); a.o2 = sub(a.o2, mul(j, wa[1] * H));
  b.o1 = add(b.o1, mul(j, wb[0] * H)); b.o2 = add(b.o2, mul(j, wb[1] * H));
  return true;
}
// A poi head against capsules.
function touchBall(m, caps, H) {
  const hits = [];
  for (const c of caps) {
    const q = closest(m.p, m.p, c.a, c.b), gap = sub(m.p, q.c2), dist = len(gap), pen = c.r + BALL - dist;
    if (pen <= 0) continue;
    const n = dist > 1e-9 ? mul(gap, 1 / dist) : [0, 0, 1], d = mul(n, pen);
    m.p = add(m.p, d); m.o = add(m.o, d);
    const v = mul(sub(m.p, m.o), 1 / H), dv = response(v, n);
    if (!dv) continue;
    m.o = sub(m.o, mul(dv, H));
    hits.push({ name: c.name, speed: -dot(v, n) });
  }
  return hits;
}

/* ---------- the simulation ---------- */
// Position-based dynamics on the two inner masses of the striker. The cord is a
// one-sided constraint between the handle's link end and the striker's head.
const STEP = 1 / 1500, EASE = 0.25, WANT = TAU / REV;
function makeSim(dr) {
  const { bs } = dr, H = STEP, g = mul(GRAVITY, H * H), contacts = [], k = dr.left ? 1 : -1;
  let s = dr.s, want = WANT, against = 0, phi = 0, lead = 0.05, trim = 0.05, speed = WANT, t = 0, frozen = null;
  // While swinging, the hand tracks the striker. Once frozen, it coasts to rest.
  const handAngle = () => {
    if (!frozen) return phi + s * lead;
    const u = Math.min((t - frozen.t) / EASE, 1);
    return frozen.th + s * frozen.w * EASE * (u - u * u / 2);
  };
  let warp = null; // lets a throw or a catch move the hand away from its swing position
  const held = () => { const q = dr.pose(handAngle()); return warp ? warp(q, t) : q; };
  // A move can turn the plane and the body as the swing goes round.
  const bsAt = th => dr.basis ? dr.basis(th) : bs, bodyNow = () => dr.body ? dr.body(handAngle()) : null;
  const linkEnd = () => { const q = held(); return add(q.h, mul(q.d, GRIP)); };
  // Start already swinging, with the striker pointing outward.
  const out = spoke(bs, 0), head = add(linkEnd(), mul(out, LINK)), mid = add(head, mul(out, STICK / 2));
  let p1 = sub(mid, mul(out, INNER / 2)), p2 = add(mid, mul(out, INNER / 2));
  const om = mul(cross(bs.a, bs.b), s * WANT), vel = p => cross(om, sub(p, dr.at(0)));
  let o1 = sub(p1, mul(vel(p1), H)), o2 = sub(p2, mul(vel(p2), H));
  const angle = () => { const b = bsAt(phi), r = sub(mul(add(p1, p2), 0.5), dr.at(phi)); return Math.atan2(dot(r, b.b), dot(r, b.a)); };
  phi = angle();
  let phi0 = phi;
  return {
    step(damp) {
      const keep = 1 - damp * H, n1 = add(add(p1, mul(sub(p1, o1), keep)), g), n2 = add(add(p2, mul(sub(p2, o2), keep)), g);
      o1 = p1; o2 = p2; p1 = n1; p2 = n2; t += H;
      const q = held(), A = add(q.h, mul(q.d, GRIP));
      // The striker can touch the body, either arm, and its own handle.
      const caps = [...bodyAt(q.h, k, dr.other && dr.other(t, handAngle()), bodyNow()), cap(sub(q.h, mul(q.d, BUTT)), A, ROD, 'handle')];
      const m = { p1, p2, o1, o2 }, hits = touchRod(m, caps, true, H);
      ({ p1, p2, o1, o2 } = m);
      for (const h of hits) contacts.push({ t, ...h });
      for (let i = 0; i < (warp ? 24 : 12); i++) { // a throw or catch moves the hand fast, so solve harder
        const r = sub(add(mul(p1, W1), mul(p2, W2)), A), dist = len(r);
        if (dist > LINK) { const c = mul(r, (dist - LINK) / dist / (W1 * W1 + W2 * W2)); p1 = sub(p1, mul(c, W1)); p2 = sub(p2, mul(c, W2)); }
        const d = sub(p2, p1), e = mul(d, (len(d) - INNER) / len(d) / 2);
        p1 = add(p1, e); p2 = sub(p2, e);
        // While touching, keep the stick out of the body as the cord pulls on it.
        if (hits.length) { const mm = { p1, p2, o1, o2 }; touchRod(mm, caps, false, H); ({ p1, p2, o1, o2 } = mm); }
      }
      // Follow the striker's angle, and lead it more when the swing is slower than wanted.
      // The trim term remembers how much lead it takes to hold the speed.
      let d = angle() - phi; d -= TAU * Math.round(d / TAU); phi += d;
      speed += (s * d / H - speed) * H / 0.2;
      // If a bounce has sent the striker round the other way, the hand goes with it.
      against = !frozen && s * d / H < -2 ? against + 1 : 0;
      if (against > 20) { s = -s; phi0 = phi; speed = Math.max(2, s * d / H); trim = 0.05; against = 0; contacts.push({ t, name: 'turned', speed: 0 }); }
      if (!frozen) {
        trim = Math.max(-0.2, Math.min(0.9, trim + 0.1 * (want - speed) * H));
        lead = Math.max(-0.3, Math.min(1.1, trim + 0.08 * (want - speed)));
      }
    },
    sample() {
      const q = held(), c = mul(add(p1, p2), 0.5), u = norm(sub(p2, p1));
      return { h: q.h, d: q.d, B: sub(c, mul(u, STICK / 2)), T: add(c, mul(u, STICK / 2)), ...bodyNow() };
    },
    rel: () => sub(mul(add(p1, p2), 0.5), linkEnd()),
    time: () => t,
    angle: () => phi,
    turned: () => s * (phi - phi0),
    freeze() { frozen = { t, th: handAngle(), w: speed }; },
    pose: held,
    contacts,
    dir: () => s,
    setWant(w) { want = w; },
    setWarp(f) { warp = f; },
    resync() { phi = angle(); },
    save: () => ({ p1, p2, o1, o2, phi, lead, trim, speed, t }),
    load(v) { ({ p1, p2, o1, o2, phi, lead, trim, speed, t } = v); frozen = null; },
  };
}

// Run until the swing repeats itself. Extra damping at first stands in for the hand
// steadying the stick; the last cycles run with air drag alone.
function settle(sim, cycle, gentle) {
  // A poi has less drive to spare, so it settles under air drag alone, for longer.
  // A cycle of several turns (a weave, a body turn) needs no more turns than a plain circle does.
  const whole = n => Math.max(4, Math.ceil(n * Math.min(1, 2 * TAU / cycle)));
  let upto = 0;
  for (const [cycles, damp] of gentle ? [[45, DRAG]] : [[10, 0.8], [30, DRAG]]) {
    upto = gentle || !upto ? whole(cycles) : Math.max(whole(cycles), upto + 2);
    while (sim.turned() < upto * cycle) {
      sim.step(damp);
      if (sim.time() > 120) throw new Error('the swing did not settle');
    }
  }
}
const blend = (a, b, f) => { const o = {}; for (const k in a) o[k] = mix(a[k], b[k], f); return o; };
// Turn raw simulation steps into one evenly sampled, seamless repeat.
function closeLoop(raw, span, kind) {
  const dur = span * STEP, K = Math.max(96, Math.round(dur * 200));
  const at = x => { const i = Math.min(Math.floor(x), raw.length - 2); return blend(raw[i], raw[i + 1], x - i); };
  const fr = Array.from({ length: K }, (_, i) => at(i * span / K)), last = at(span), err = {};
  // Spread any leftover mismatch between the two ends, so the loop has no seam.
  for (const k in last) err[k] = sub(last[k], fr[0][k]);
  fr.forEach((f, i) => { for (const k in err) f[k] = sub(f[k], mul(err[k], i / K)); });
  return { kind, fr, dur, loop: true, trail: Math.min(0.6, 0.34 / dur), closure: Math.max(...Object.values(err).map(len)) };
}
// A repeating move: let it settle, then record exactly one repeat. How long a repeat
// takes comes out of the simulation, because gravity speeds and slows the swing.
function loopClip(dr, make = makeSim, kind = 'nunchaku') {
  const sim = make(dr), raw = [];
  settle(sim, dr.cycle, kind === 'poi');
  const from = sim.turned(), started = sim.time(), start = sim.angle();
  let before = from;
  while (sim.turned() - from < dr.cycle) {
    raw.push(sim.sample()); before = sim.turned(); sim.step(DRAG);
    if (raw.length > 30000) throw new Error('the swing stalled');
  }
  raw.push(sim.sample());
  // The repeat ends part-way through the last step.
  return { ...closeLoop(raw, raw.length - 2 + (from + dr.cycle - before) / (sim.turned() - before), kind), start, cycle: dr.cycle, dir: dr.s, contacts: sim.contacts.filter(c => c.t >= started) };
}

// A stop: swing, then stop driving at the bottom and bleed off speed. The amount of
// damping is searched for, so the striker runs out of speed exactly at the target angle.
function stopClip(target) {
  const dr = cone({ plane: 'wall', dir: 1, ...NORMAL }), SETTLE = 3.2, TAIL = 2.6;
  const sim = makeSim(dr);
  settle(sim, TAU);
  const start = sim.save(), t0 = sim.time();
  const run = (fade, fr) => {
    sim.load(start);
    let cut = null, apex = null, peak = -1, prev = 0, end = t0 + 4;
    for (let i = 0; sim.time() < end; i++) {
      if (fr && i % 15 === 0) fr.push(sim.sample());
      sim.step(cut === null ? DRAG : apex === null ? fade : SETTLE);
      const r = sim.rel(), psi = Math.atan2(r[2], -r[1]) * 360 / TAU; // 0 at the bottom, 90 in front
      if (cut === null) { if (sim.time() - t0 > 0.6 * REV && prev < 0 && psi >= 0 && psi < 90) { cut = sim.time(); end = cut + TAIL; sim.freeze(); } }
      else if (apex === null) {
        if (psi < prev - 180) { peak = 999; apex = sim.time(); }       // went over the top
        else if (psi > peak) peak = psi;
        else if (psi < peak - 0.5) apex = sim.time();
      }
      prev = psi;
    }
    return { peak, dur: end - t0 };
  };
  let lo = 0, hi = 90;
  for (let i = 0; i < 18; i++) { const c = (lo + hi) / 2; if (run(c).peak > target) lo = c; else hi = c; }
  const fr = [], res = run(hi, fr);
  return { kind: 'nunchaku', fr, dur: res.dur, loop: false, apex: res.peak };
}

/* ---------- poi: a weight on a tether ---------- */
const TETHER = 0.55;
const memo = f => { let c; return () => c || (c = f()); };
// The same idea as makeSim, with one point mass in place of the rod. The hand follows
// the head's angle and leads it slightly. dr.pose gives the hand position for an angle.
function makeMass(dr) {
  const { s, want } = dr, gain = dr.gain || 1, H = STEP, g = mul(GRAVITY, H * H), contacts = [];
  // The plane, the body and the other hand can all depend on the swing angle. `off` puts the
  // point the tether leaves from a little way off the middle of the fist.
  const bsAt = th => dr.basis ? dr.basis(th) : dr.bs, pivot = th => dr.off ? add(dr.pose(th), dr.off(th)) : dr.pose(th);
  let phi = 0, lead = 0.05, trim = 0.05, speed = want, t = 0, th = 0, held = pivot(0);
  let p = add(dr.at(0), mul(spoke(bsAt(0), 0), dr.r0));
  let o = sub(p, mul(cross(mul(cross(bsAt(0).a, bsAt(0).b), s * want), sub(p, dr.at(0))), H));
  const angle = () => { const b = bsAt(phi), r = sub(p, dr.at(phi)); return Math.atan2(dot(r, b.b), dot(r, b.a)); };
  phi = angle();
  const phi0 = phi;
  return {
    step(damp) {
      const n = add(add(p, mul(sub(p, o), 1 - damp * H)), g);
      o = p; p = n; t += H;
      th = phi + s * lead * (dr.behind ? -1 : 1); held = pivot(th);
      const r = sub(p, held), dist = len(r);
      if (dist > TETHER) p = add(held, mul(r, TETHER / dist));
      const m = { p, o };
      for (const h of touchBall(m, bodyAt(dr.pose(th), -1, dr.other && dr.other(th), dr.body && dr.body(th)), H)) contacts.push({ t, ...h });
      ({ p, o } = m);
      let d = angle() - phi; d -= TAU * Math.round(d / TAU); phi += d;
      speed += (s * d / H - speed) * H / 0.2;
      trim = clamp(trim + 0.1 * gain * (want - speed) * H, -0.2, 0.9);
      lead = clamp(trim + 0.08 * gain * (want - speed), -0.3, 1.1);
    },
    sample: () => ({ h: dr.pose(th), ...(dr.off ? { o: held } : null), P: p, ...(dr.body ? dr.body(th) : null) }),
    contacts,
    time: () => t,
    angle: () => phi,
    turned: () => s * (phi - phi0),
  };
}
// Poi planes use the same axes as the nunchaku ones, under their poi names.
const PB = { wheel: BASIS.wall, wall: BASIS.face, flat: BASIS.table };
const FWD = -1, BACK = 1; // forward: the head moves away from you over the top
// Below about 1.2 turns a second a poi cannot stay taut over the top, so spins run at 1.4.
// `off` is where the tether leaves the fist, for moves where the two hands work close together.
const poiSpin = (plane, c, dir, rev = 0.7, off) => memo(() => loopClip({ bs: PB[plane], s: dir, cycle: TAU, want: TAU / rev, r0: TETHER + 0.09, at: () => (off ? add(c, off) : c), pose: th => add(c, mul(spoke(PB[plane], th), 0.09)), off: off && (() => off) }, makeMass, 'poi'));
// Extension: the straight arm circles with the poi, so hand and head share a centre.
const ARM = 0.48, SHOULDER = [-0.26, 1.43, 0];
const poiExtension = dir => memo(() => loopClip({ bs: PB.wheel, s: dir, cycle: TAU, want: TAU / 1.0, gain: 0.5, r0: ARM + TETHER, at: () => SHOULDER, pose: th => add(SHOULDER, mul(spoke(PB.wheel, th), ARM)) }, makeMass, 'poi'));
// Isolation: the hand circles opposite the head, so the middle of the tether stays put.
const ISO = [-0.36, 1.28, 0.2]; // far enough out that the head clears the spinner's own shoulder
const poiIsolation = dir => memo(() => loopClip({ bs: PB.wheel, s: dir, cycle: TAU, want: TAU / 0.55, gain: 0.35, behind: true, r0: TETHER / 2, at: () => ISO, pose: th => sub(ISO, mul(spoke(PB.wheel, th), TETHER / 2)) }, makeMass, 'poi'));
// Pendulum: a still hand and no drive. One full swing out and back is recorded.
const poiPendulum = memo(() => {
  // The pendulum equation, stepped so that energy is kept: the swing returns to its start.
  const c = [-0.3, 1.0, 0.12], k = -GRAVITY[1] / TETHER, raw = [];
  let th = 1.25, w = 0, was = 0, turns = 0;
  while (turns < 2 && raw.length < 20000) {
    raw.push({ h: c, P: add(c, [0, -TETHER * cos(th), TETHER * sin(th)]) });
    const half = w - k * sin(th) * STEP / 2;
    th += half * STEP; w = half - k * sin(th) * STEP / 2;
    if (was && w && Math.sign(w) !== Math.sign(was)) turns++;
    if (w) was = w;
  }
  return closeLoop(raw, raw.length - 1, 'poi');
});

/* ---------- release and catch ---------- */
// A smooth path from one position and velocity to another, over D seconds (s runs 0 to 1).
const herm = (p0, v0, p1, v1, s, D) => { const a = s * s, b = a * s; return add(add(mul(p0, 2 * b - 3 * a + 1), mul(v0, (b - 2 * a + s) * D)), add(mul(p1, 3 * a - 2 * b), mul(v1, (b - a) * D))); };
const ZERO = [0, 0, 0], IDLE = [-0.24, 0.93, -0.04], SHOULDER_R = [-0.2, 1.43, 0];
// Keep a hand within arm's length of its shoulder.
const within = (p, sh) => { const v = sub(p, sh), d = len(v); return d > 0.58 ? add(sh, mul(v, 0.58 / d)) : p; };
// Turn one direction into another along the shortest arc.
const slerp = (a, b, s) => {
  const w = Math.acos(clamp(dot(a, b), -1, 1));
  if (w < 1e-4) return a;
  if (w > 3.1) { const m = norm(cross(a, Math.abs(a[0]) < 0.9 ? [1, 0, 0] : [0, 1, 0])); return s < 0.5 ? slerp(a, m, s * 2) : slerp(m, b, s * 2 - 1); }
  return add(mul(a, sin((1 - s) * w) / sin(w)), mul(b, sin(s * w) / sin(w)));
};
const WIND = 0.18, SETTLE_IN = 0.4; // seconds of hand lift before a release, and to settle after a catch
// The handle as a free stick: the same two inner masses as the striker.
const rodOf = q => { const c = add(q.h, mul(q.d, (GRIP - BUTT) / 2)); return [sub(c, mul(q.d, INNER / 2)), add(c, mul(q.d, INNER / 2))]; };

// Nunchaku aerial. The swing runs normally, the hand lifts and lets go, and both sticks
// fly joined only by the cord. The catching hand goes to where the handle arrives, once it
// has turned the wanted number of times and is within reach. Returns null if that never happens.
//   o: dir, phase (swing angle at release), lift and across (hand speed at release, m/s),
//      turns (full handle turns before the catch), other (catch with the other hand)
function aerialClip(o) {
  const dr = cone({ plane: 'wall', dir: o.dir, ...NORMAL }), H = STEP, EVERY = 10, sim = makeSim(dr), fr = [];
  settle(sim, TAU);
  const base = sim.save(), until = sim.turned() + TAU + o.phase;
  while (sim.turned() < until) sim.step(DRAG);
  const tr = sim.time();
  sim.load(base);
  sim.setWarp((q, t) => { const s = clamp((t - (tr - WIND)) / WIND, 0, 1) ** 2 * WIND / 2; return clearOf({ h: add(q.h, [o.across * s, o.lift * s, 0]), d: q.d }, STILL); });
  const idle2 = flip(IDLE);
  let n = 0, before = sim.pose();
  const keep = f => { if (n++ % EVERY === 0) fr.push(o.other ? { ...f, h2: f.h2 || idle2 } : f); };
  while (sim.time() < tr - 1e-9) { const f = sim.sample(); keep({ ...f, g: f.h }); before = sim.pose(); sim.step(DRAG); }
  // Let go: from here the handle is a free body with whatever motion the hand gave it.
  const now = sim.pose(), hr = now.h, vr = mul(sub(now.h, before.h), 1 / H);
  let [q1, q2] = rodOf(now), [oq1, oq2] = rodOf(before), { p1, p2, o1, o2 } = sim.save();
  const grav = mul(GRAVITY, H * H), drag = 1 - DRAG * H, move = (p, old) => add(add(p, mul(sub(p, old), drag)), grav);
  const rigid = (a, b) => { const d = sub(b, a), e = mul(d, (len(d) - INNER) / len(d) / 2); return [add(a, e), sub(b, e)]; };
  const shoulder = o.other ? flip(SHOULDER_R) : SHOULDER_R, air = [], from = fr.length;
  const handle = () => { const d = norm(sub(q2, q1)); return { d, g: add(mul(add(q1, q2), 0.5), mul(d, (BUTT - GRIP) / 2)) }; };
  const angleOf = d => Math.atan2(dot(d, dr.bs.b), dot(d, dr.bs.a));
  let last = angleOf(now.d), rot = 0, t = tr, caught = null, gPrev = handle().g, touched = false, bumps = 0;
  while (!caught) {
    const a = move(p1, o1), b = move(p2, o2), c = move(q1, oq1), e = move(q2, oq2);
    o1 = p1; o2 = p2; oq1 = q1; oq2 = q2; p1 = a; p2 = b; q1 = c; q2 = e; t += H;
    for (let i = 0; i < 12; i++) {
      const r = sub(add(mul(p1, W1), mul(p2, W2)), add(mul(q2, W1), mul(q1, W2))), dist = len(r);
      if (dist > LINK) { const k = mul(r, (dist - LINK) / dist / (2 * (W1 * W1 + W2 * W2))); p1 = sub(p1, mul(k, W1)); p2 = sub(p2, mul(k, W2)); q2 = add(q2, mul(k, W1)); q1 = add(q1, mul(k, W2)); }
      [p1, p2] = rigid(p1, p2); [q1, q2] = rigid(q1, q2);
      if (i === 0 || touched) {
        // In the air the sticks can hit each other and the body. The arms are left out: they are moving to make the catch.
        const ms = { p1, p2, o1, o2 }, mh = { p1: q2, p2: q1, o1: oq2, o2: oq1 };
        const a1 = touchRod(ms, STILL, i === 0, H), a2 = touchRod(mh, STILL, i === 0, H), a3 = touchPair(ms, mh, i === 0, H);
        if (i === 0) { touched = a1.length + a2.length > 0 || a3; if (touched) bumps++; }
        ({ p1, p2, o1, o2 } = ms); q2 = mh.p1; q1 = mh.p2; oq2 = mh.o1; oq1 = mh.o2;
      }
    }
    const hd = handle(), ang = angleOf(hd.d);
    let step = ang - last; step -= TAU * Math.round(step / TAU); rot += step; last = ang;
    const mid = mul(add(p1, p2), 0.5), u = norm(sub(p2, p1));
    if (air.length % EVERY === 0) air.push({ t, g: hd.g, d: hd.d, B: sub(mid, mul(u, STICK / 2)), T: add(mid, mul(u, STICK / 2)), keep: true }); else air.push(null);
    if (hd.g[1] < 0.35 || t - tr > 1.6) return null;
    if (t - tr > 0.2 && Math.abs(rot) >= TAU * o.turns && len(sub(hd.g, shoulder)) <= 0.56 && hd.g[1] > 0.8)
      caught = { t, g: hd.g, v: mul(sub(hd.g, gPrev), 1 / H), d: hd.d, reach: len(sub(hd.g, shoulder)) };
    gPrev = hd.g;
  }
  // Both hands travel smoothly: the catching hand arrives moving with the handle.
  const span = caught.t - tr;
  for (const f of air) {
    if (!f) continue;
    const s = (f.t - tr) / span;
    fr.push(o.other ? { h: within(herm(hr, vr, IDLE, ZERO, s, span), SHOULDER_R), h2: within(herm(idle2, ZERO, caught.g, caught.v, s, span), shoulder), g: f.g, d: f.d, B: f.B, T: f.T }
      : { h: within(herm(hr, vr, caught.g, caught.v, s, span), SHOULDER_R), g: f.g, d: f.d, B: f.B, T: f.T });
  }
  const to = fr.length;
  // Held again: the hand eases back to its swing position while the striker keeps its motion.
  const dr2 = o.other ? { ...dr, left: true, at: th => flip(dr.at(th)), pose: th => { const q = dr.pose(th); return { h: flip(q.h), d: flip(q.d) }; } } : dr;
  const sim2 = makeSim(dr2);
  sim2.load({ p1, p2, o1, o2, phi: 0, lead: 0.05, trim: 0.05, speed: WANT, t: caught.t });
  sim2.resync();
  // How far the handle points away from where the hand would hold it: a small twist is a clean catch.
  const twist = Math.acos(clamp(dot(caught.d, sim2.pose().d), -1, 1));
  sim2.setWarp((q, tt) => { const s = clamp((tt - caught.t) / SETTLE_IN, 0, 1); return clearOf({ h: within(herm(caught.g, caught.v, q.h, ZERO, s, SETTLE_IN), shoulder), d: slerp(caught.d, q.d, s * s * (3 - 2 * s)) }, STILL); });
  const start = sim2.turned();
  n = 0;
  while (sim2.turned() - start < 2 * TAU && sim2.time() - caught.t < 2.4) {
    const f = sim2.sample();
    if (n++ % EVERY === 0) fr.push(o.other ? { h: IDLE, h2: f.h, g: f.h, d: f.d, B: f.B, T: f.T } : { ...f, g: f.h });
    sim2.step(DRAG);
  }
  if (sim2.turned() - start < 2 * TAU) return null; // the swing did not recover after the catch
  return { kind: 'nunchaku', fr, dur: fr.length * EVERY * H, loop: false, air: { other: !!o.other, from, to, twist, bumps, turns: Math.abs(rot) / TAU, reach: caught.reach, home: len(sub(caught.g, o.other ? flip(dr.pose(0).h) : dr.pose(0).h)), time: span } };
}

// A bounce off the other forearm. The swing stays where it is and is slowed a little; the
// other arm comes up into its path. The striker meets it, rebounds and goes round the other
// way; the swinging hand follows the new direction and the arm drops away.
//   o: dir (swing direction on the plane in front), target (where the other hand goes), on (what gets hit)
function bounceClip(o) {
  const idle2 = flip(IDLE), dr = { ...cone({ plane: 'face', dir: o.dir, ...NORMAL }), other: () => there }, H = STEP, EVERY = 10, fr = [];
  let there = idle2, amount = 0, goal = 0, stage = 0, n = 0, hit = null;
  const sim = makeSim(dr);
  settle(sim, TAU);
  const t0 = sim.time(), first = sim.contacts.length, start = sim.turned();
  while (stage < 3) {
    if (n++ % EVERY === 0) fr.push({ ...sim.sample(), h2: there });
    sim.step(DRAG);
    amount = clamp(amount + clamp(goal - amount, -H / 0.3, H / 0.5), 0, 1);
    there = mix(idle2, o.target, amount * amount * (3 - 2 * amount));
    const seen = sim.contacts.slice(first);
    if (stage === 0 && sim.turned() - start >= TAU) { stage = 1; goal = 1; }   // raise the arm
    else if (stage === 1 && seen.some(c => c.name === 'turned')) {                // it has bounced back
      hit = seen.find(c => c.name === o.on); stage = 2; goal = 0; sim.setWant(WANT);
    } else if (stage === 2 && sim.turned() >= 2 * TAU) stage = 3;                  // two clean turns the other way
    // Fade gently as the arm comes up: a sudden fade slaps the striker into its own handle.
    if (stage === 1) sim.setWant(WANT * (1 - o.fade * amount));
    if (sim.time() - t0 > 7) return o.why ? { stage, seen } : null;
  }
  if (!hit || sim.dir() !== -o.dir) return o.why ? { stage, seen: sim.contacts.slice(first), dir: sim.dir() } : null;
  return { kind: 'nunchaku', fr, dur: fr.length * EVERY * H, loop: false, contacts: sim.contacts.slice(first), bounce: { on: o.on, speed: hit.speed, at: (hit.t - t0) / (fr.length * EVERY * H) } };
}

// Staff throw. A free rigid staff keeps its spin and its centre follows a parabola, so the
// flight is exact. It is released level, which sends it straight up.
//   o: half, c (hand position), bs (plane), dir, rate (turns a second), rise (m), other
function staffToss(o) {
  const G = -GRAVITY[1], w = o.dir * TAU * o.rate, v0 = sqrt(2 * G * o.rise), fps = 150, wind = 0.2;
  const tr = 1 / o.rate, yr = v0 * wind / 2, tf = (v0 + sqrt(v0 * v0 + 2 * G * yr)) / G; // rises, then falls back to hand height
  const end = o.other ? flip(o.c) : o.c, vx = (end[0] - o.c[0]) / tf, vc = [vx, v0 - G * tf, 0], idle2 = flip(IDLE);
  const hr = add(o.c, [0, yr, 0]), total = tr + tf + 1.3 / o.rate, fr = [];
  let from = 0, to = 0;
  for (let i = 0; i < Math.round(total * fps); i++) {
    const t = i / fps, u = mul(spoke(o.bs, w * t), o.half);
    let c, h, h2 = idle2;
    if (t < tr) { const s = clamp((t - (tr - wind)) / wind, 0, 1); c = add(o.c, [0, v0 * wind * s * s / 2, 0]); h = c; from = i + 1; }
    else if (t < tr + tf) {
      const x = t - tr, s = x / tf;
      c = add(hr, [vx * x, v0 * x - G * x * x / 2, 0]);
      // The throwing hand slows to a stop while the staff carries on up; the catching hand waits where it will land.
      const stop = clamp(s / 0.45, 0, 1), come = clamp((s - 0.35) / 0.5, 0, 1);
      h = herm(hr, [0, v0, 0], o.other ? IDLE : end, ZERO, stop, 0.45 * tf);
      h2 = o.other ? herm(idle2, ZERO, end, ZERO, come, 0.5 * tf) : idle2; to = i + 1;
    } else { c = herm(end, vc, end, ZERO, clamp((t - tr - tf) / 0.28, 0, 1), 0.28); h = o.other ? IDLE : c; h2 = o.other ? c : idle2; }
    fr.push(o.other ? { h, h2, a: add(c, u), b: sub(c, u) } : { h, a: add(c, u), b: sub(c, u) });
  }
  return { kind: 'staff', method: 'flight', fr, dur: total, loop: false, air: { other: !!o.other, from, to, rise: o.rise + yr, time: tf } };
}

/* ---------- paths drawn from a definition ---------- */
// Rigid props held in the hand go where the hand puts them, so these are geometry.
const drawn = (kind, dur, fn) => memo(() => { const n = Math.round(dur * 120); return { kind, dur, loop: true, closure: 0, trail: 0.92, method: 'hand', fr: Array.from({ length: n }, (_, i) => fn(i / n)) }; });
const turn = (bs, t) => spoke(bs, t);
// Poi flower: the arm makes one circle while the poi turns `ratio` times. Negative is antispin.
const poiFlower = (ratio, dir = FWD) => drawn('poi', 1.8, p => { const h = add(SHOULDER, mul(turn(PB.wheel, dir * TAU * p), ARM)); return { h, P: add(h, mul(turn(PB.wheel, ratio * dir * TAU * p), TETHER)) }; });
const STAFF = 0.7, SHORT = 0.42;
const rotor = (half, bs, c, dir, dur) => drawn('staff', dur, p => { const u = mul(turn(bs, dir * TAU * p), half); return { h: c, a: add(c, u), b: sub(c, u) }; });
const STAFF_AT = { front: [-0.05, 1.15, 0.45], side: [-0.45, 1.0, 0.1], top: [-0.05, 1.98, 0.05] }, STAFF_BS = { front: BASIS.face, side: BASIS.wall, top: BASIS.table };
const staffRotor = (where, dir) => rotor(STAFF, STAFF_BS[where], STAFF_AT[where], dir, 0.9);
const SHORT_SIDE = [-0.42, 1.05, 0.1];
// One end stays on a spot while the hand and the far end circle it. The spot sits just
// outside the shoulder, so the staff lies beside the arm and not through it.
const ISO_AT = [-0.32, 1.43, 0.02];
const staffIsolation = (dir, pinky) => drawn('staff', 1.4, p => { const u = turn(PB.wheel, dir * TAU * p), far = add(ISO_AT, mul(u, 2 * SHORT)); return { [pinky ? 'b' : 'a']: ISO_AT, h: add(ISO_AT, mul(u, SHORT)), [pinky ? 'a' : 'b']: far, e: add(ISO_AT, mul(turn(PB.wheel, dir * (TAU * p - Math.PI / 2)), 0.9)) }; });
// The hand circles one way and the staff turns the other, once per circle.
const staffAntispin = dir => drawn('staff', 1.4, p => { const h = add(SHOULDER, mul(turn(PB.wheel, dir * TAU * p), SHORT)), c = add(h, [-0.05, 0, 0]), u = mul(turn(PB.wheel, -dir * TAU * p), SHORT); return { h, a: add(c, u), b: sub(c, u), e: add(SHOULDER, mul(turn(PB.wheel, dir * (TAU * p - Math.PI / 2)), 0.9)) }; });
// A buugeng turning about its handle. u and v are the axes of its own plane.
const buugengAt = (c, bs, t) => ({ c, u: add(mul(bs.a, cos(t)), mul(bs.b, sin(t))), v: add(mul(bs.a, -sin(t)), mul(bs.b, cos(t))) });
const buugengSpin = dir => drawn('buugeng', 0.9, p => ({ h: SHORT_SIDE, ...buugengAt(SHORT_SIDE, PB.wheel, dir * TAU * p) }));

module.exports = { Demo, TAU, STEP, DRAG, REV, WANT, STICK, LINK, GRIP, BUTT, INNER, W1, W2, GRAVITY, TETHER, ARM, SHOULDER, ISO, STAFF, SHORT, SHORT_SIDE, STAFF_AT, STAFF_BS,
  BASIS, CENTRE, PB, FWD, BACK, NORMAL, POINT, PIVOT, ROD, BALL, IDLE, ZERO, STILL, spoke, cone, eightWall, eightTall, bodyAt, closest, response, clearOf, touchRod, touchPair, touchBall, cap,
  makeSim, makeMass, settle, closeLoop, loopClip, stopClip, memo, blend, herm, within, slerp, aerialClip, bounceClip, staffToss, drawn, turn, poiSpin, poiExtension, poiIsolation, poiPendulum,
  poiFlower, rotor, staffRotor, staffIsolation, staffAntispin, buugengAt, buugengSpin };
