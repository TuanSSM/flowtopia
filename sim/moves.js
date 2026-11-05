'use strict';
// Families of moves built on the engine: body movement, figure-8s, hand changes, unit-circle
// patterns, carried spins (weaves, turns), solved poi paths and still pictures.
const E = require('./engine.js');
const { Demo, TAU, WANT, STEP, GRAVITY, TETHER, SHORT, BASIS, PB, memo, spoke, loopClip, makeMass, makeSim, drawn, buugengAt } = E;
const { add, sub, mul, dot, cross, len, norm, mix, rot, clamp, smooth, flip } = Demo.v, { sin, cos, sqrt, PI } = Math;
const fs = require('fs'), path = require('path'), C = require('./compose.js'), K = require('./check.js');

/* ---------- finding a posture ---------- */
// A hand-driven move is fixed by its definition only up to where the hand is held and how
// the arm is set. `fitted` tries a grid of those and keeps the one that leaves the prop
// clearest of the body and of its own arm. The choice is stored in sim/fits.json, so a
// rebuild gives the same clip without searching again.
const FITS_FILE = path.join(__dirname, 'fits.json'), FITS = fs.existsSync(FITS_FILE) ? JSON.parse(fs.readFileSync(FITS_FILE, 'utf8')) : {};
// Named elbow positions, relative to the hand: under it, out to the side, or behind it.
const ELBOW = { auto: null, under: [0, -0.4, -0.05], out: [-0.4, -0.15, -0.1], behind: [0, -0.12, -0.4], low: [-0.15, -0.4, -0.2] };
const elbowAt = (name, c) => ELBOW[name] ? () => add(c, ELBOW[name]) : null;
function score(clip) {
  const sc = C.scene({ make: () => clip }), o = K.overlaps(sc), a = K.anatomy(sc, {});
  return Math.max(o.prop[0], o.grip[0] - 0.015, o.limb[0] - 0.008, a.errs.length ? 0.5 : 0);
}
function fitted(name, grid, make) {
  return memo(() => {
    if (FITS[name]) return make(FITS[name])();
    const keys = Object.keys(grid), total = keys.reduce((n, k) => n * grid[k].length, 1);
    let best = null, low = Infinity;
    for (let i = 0; i < total; i++) {
      let j = i; const o = {};
      for (const k of keys) { o[k] = grid[k][j % grid[k].length]; j = Math.floor(j / grid[k].length); }
      const s = score(make(o)());
      if (s < low) { low = s; best = o; }
    }
    if (low > 0.002) throw new Error(`no posture keeps ${name} clear: the best still overlaps by ${(low * 1000).toFixed(0)} mm`);
    FITS[name] = best; fs.writeFileSync(FITS_FILE, JSON.stringify(FITS, null, 1));
    return make(best)();
  });
}

/* ---------- the body in motion ---------- */
// Turn a vector about the vertical axis; a positive angle turns the figure toward its left.
const rotY = (v, a) => [v[0] * cos(a) + v[2] * sin(a), v[1], -v[0] * sin(a) + v[2] * cos(a)];
// Feet for a body turned by psi. They take quarter-turn steps one after the other, each
// lifting a little as it moves, so one foot is always on the floor.
function turnFeet(psi) {
  const q = PI / 2, u = psi / q, base = Math.floor(u), fr = u - base, out = { face: rotY([0, 0, 1], psi) };
  for (const [s, k, a, b] of [['L', 1, 0, 0.5], ['R', -1, 0.5, 1]]) {
    const yaw = q * (base + smooth(a, b, fr)), lift = 0.05 * sin(PI * clamp((fr - a) / (b - a), 0, 1));
    out['f' + s] = add(rotY([0.14 * k, 0.04, 0], yaw), [0, lift, 0]); out['t' + s] = rotY([0, 0, 1], yaw);
  }
  return out;
}
// Ways the body can move through one repeat (u runs 0 to 1): how far it has turned, where the
// pelvis goes, and how the spine leans. Hands and props are carried along.
const BODY = {
  // Weight from foot to foot, the pelvis dipping slightly in the middle.
  shift: u => ({ yaw: 0, move: [0.07 * sin(TAU * u), -0.012 * (1 - cos(2 * TAU * u)) / 2, 0] }),
  shiftL: u => ({ yaw: 0, move: [-0.07 * sin(TAU * u), -0.012 * (1 - cos(2 * TAU * u)) / 2, 0] }), // the same, as the mirrored hand sees it
  // Down and up again by bending the knees, with a slight lean forward at the bottom.
  level: u => { const c = (1 - cos(TAU * u)) / 2; return { yaw: 0, move: [0, -0.24 * c, 0.02 * c], spine: norm([0, 1, 0.16 * c]) }; },
  // One full turn on the spot, at an even rate.
  turn: u => ({ yaw: TAU * u, feet: true }),
  turnBack: u => ({ yaw: -TAU * u, feet: true }),
  // Two half-turn pivots with a pause between them.
  pivot: u => ({ yaw: PI * (smooth(0.08, 0.42, u) + smooth(0.58, 0.92, u)), feet: true }),
};
// The pose keys a body movement adds to a frame.
function bodyKeys(m) {
  const out = m.feet ? turnFeet(m.yaw) : {};
  if (m.move) { out.pelvis = add([0, Demo.dim.pelvis, 0], m.move); out.fL = [0.14, 0.04, 0]; out.fR = [-0.14, 0.04, 0]; }
  if (m.spine) out.spine = m.spine;
  return out;
}
const carryPoint = (m, v) => add(rotY(v, m.yaw), m.move || [0, 0, 0]), carryDir = (m, v) => rotY(v, m.yaw);
// A swing driver (nunchaku or poi) carried by a moving body. `turns` is how many swings fit
// in one repeat of the body movement.
function carried(dr, body, turns) {
  const cycle = turns * dr.cycle, at = th => body((((dr.s * th / cycle) % 1) + 1) % 1), rigid = q => q.h ? { h: carryPoint(at.m, q.h), d: carryDir(at.m, q.d) } : carryPoint(at.m, q);
  const wrap = f => th => { at.m = at(th); return f(th); };
  return {
    ...dr, cycle,
    pose: wrap(th => rigid(dr.pose(th))), at: wrap(th => carryPoint(at.m, dr.at(th))),
    basis: wrap(() => ({ n: carryDir(at.m, dr.bs.n || cross(dr.bs.a, dr.bs.b)), a: carryDir(at.m, dr.bs.a), b: carryDir(at.m, dr.bs.b) })),
    body: wrap(() => bodyKeys(at.m)),
  };
}
// The same for a hand-driven clip: play it `turns` times while the body moves once.
const carriedClip = (make, body, turns, DIRS = new Set(['d', 'u', 'v'])) => memo(() => {
  const src = make(), dur = src.dur * turns, n = Math.round(dur * 120), N = src.fr.length;
  const frame = x => { const i = Math.floor(x) % N, j = (i + 1) % N; return E.blend(src.fr[i], src.fr[j], x - Math.floor(x)); };
  return { ...src, dur, fr: Array.from({ length: n }, (_, i) => { const u = i / n, m = body(u), f = frame(u * turns * N), out = bodyKeys(m); for (const k in f) out[k] = DIRS.has(k) ? carryDir(m, f[k]) : carryPoint(m, f[k]); return out; }) };
});

/* ---------- rigid props in the hand ---------- */
// A turn in one hand. The prop sweeps a cone to one side of the forearm, then to the other,
// changing side when it is square to the forearm: that is what lets the wrist keep turning
// it without letting go. Two turns make one repeat, and the marked end draws a figure 8.
//   o: kind, half, c (hand), n (the side the first lobe leans to), a, b (the plane it turns in),
//      dir, gamma (how far the prop leans out of the plane), flip (angle at which it changes side),
//      sway (how far the hand travels to each side), lobes (-1 starts on the other side), stay (no change of side)
function figure8(o) {
  const g = o.gamma || 1.05, at = o.flip === undefined ? PI / 2 : o.flip, k = o.lobes || 1;
  return drawn(o.kind, o.dur || 1.6, p => {
    const th = o.dir * 2 * TAU * p, S = o.stay ? k : k * sin((th - at) / 2), e = add(mul(o.a, cos(th)), mul(o.b, sin(th)));
    const u = norm(add(mul(o.n, cos(g) * S), mul(e, sin(g)))), h = add(o.c, mul(o.n, (o.sway || 0) * S)), aim = o.elbow ? { e: o.elbow(S) } : null;
    // The staff lies in the palm, on the side of the wrist it is leaning to.
    if (o.kind === 'staff') { const c = add(h, mul(o.n, 0.045 * S)); return { h, a: add(c, mul(u, o.half)), b: sub(c, mul(u, o.half)), ...aim }; }
    // A buugeng stays as close to the plane as its long axis allows.
    const w = cross(o.a, o.b), m = norm(sub(w, mul(u, dot(w, u))));
    return { h, c: h, u, v: cross(m, u), ...aim };
  });
}
// A rotor kept turning by changing hands. Each hand works on its own side: it takes the end
// of the staff that is coming round to it, turns it through 150 degrees, lets go, and reaches
// back for the other end. The two hands let go at different moments, so one always holds.
//   o: half, c, bs, dir, dur, right (the angle, in degrees, at which the staff points to the figure's right)
function handRotor(o) {
  const n = cross(o.bs.a, o.bs.b), back = mul(n, Math.sign(dot(n, sub([0, 1.3, 0], o.c))) || 1), D = PI / 180, HOLD = 150;
  // start: the angle at which this hand takes hold; G: how far from the middle it grips.
  const hand = (deg, start, G) => {
    const x = (((deg - start) % 180) + 180) % 180, end = Math.floor((deg - start) / 180) % 2 ? -1 : 1;
    if (x < HOLD) return [add(o.c, mul(spoke(o.bs, o.dir * deg * D), end * G)), true];
    const w = (x - HOLD) / (180 - HOLD), ang = start + HOLD * (1 - smooth(0, 1, w)), lift = sin(PI * w); // reaching back, off the staff
    return [add(add(o.c, mul(spoke(o.bs, o.dir * ang * D), G * (1 + 0.4 * lift))), mul(back, 0.05 * lift)), false];
  };
  return drawn('staff', o.dur, p => {
    const deg = 360 * p, e = spoke(o.bs, o.dir * deg * D), r = (o.right || 0) * o.dir;
    return { h: hand(deg, r - 95, 0.06)[0], h2: hand(deg, r + 115, 0.13)[0], a: add(o.c, mul(e, o.half)), b: sub(o.c, mul(e, o.half)) };
  });
}
// Unit-circle moves: the hand goes once round a circle while the staff turns k times.
//   o: half, c, bs, R (hand circle), k, phase, dir, dur, out (how far the staff sits outside the arm), angle (a function for the staff's own angle)
const unit = o => drawn('staff', o.dur || 1.4, p => {
  const th = o.dir * TAU * p, h = add(o.c, mul(spoke(o.bs, th), o.R)), u = spoke(o.bs, o.angle ? o.dir * o.angle(TAU * p) : o.k * th + (o.phase || 0));
  // The staff lies in the palm, a little to the outside of the line of the arm.
  const c = add(h, mul(o.bs.n, o.out || 0));
  return { h, a: add(c, mul(u, o.half)), b: sub(c, mul(u, o.half)), ...(o.elbow ? { e: o.elbow(th) } : null) };
});
// The staff spinning flat on the open palm: nothing drives it, so it keeps its rate.
const palmSpin = (half, c, dir, dur) => drawn('staff', dur, p => { const u = mul(spoke(BASIS.table, dir * TAU * p), half); return { h: add(c, [0, -0.045, 0]), a: add(c, u), b: sub(c, u) }; });
// A buugeng turning about the middle of one of its curves, so that curve stays on one circle.
const buugengIsolation = (c, bs, dir, dur = 1.2) => drawn('buugeng', dur, p => { const t = dir * TAU * p, e = spoke(bs, t), h = add(c, mul(e, 0.19)); return { h, ...buugengAt(h, bs, t) }; });

/* ---------- poi: carried spins ---------- */
// A circle whose centre travels: the hand is carried from side to side while the poi keeps
// turning, and the head's path comes out of the simulation.
//   o: dir, n (beats: turns between two crossings x 2), A (how far to each side), y, z, at (swing angle at which the hand crosses the middle), w (half the gap between the hands), lift
function weaveDriver(o) {
  const bs = PB.wheel, S = th => sin((th - o.at) / o.n), c = th => [-o.w - o.A * S(th), o.y - (o.lift || 0) * S(th), o.z];
  const pose = th => add(c(th), mul(spoke(bs, th), 0.09));
  // The other hand is the mirror image, half a repeat away, so both are always on the same side.
  return { bs, s: o.dir, cycle: o.n * TAU, want: TAU / (o.rev || 0.7), r0: TETHER + 0.09, at: c, pose, other: o.alone ? null : th => flip(add(c(th + PI - o.n * PI), mul(spoke(bs, th + PI), 0.09))) };
}
const weave = o => memo(() => loopClip(weaveDriver(o), makeMass, 'poi'));
// A plain spin (wheel plane at the side) with the body moving under it.
const poiCarried = (c, dir, body, turns) => memo(() => loopClip(carried({ bs: PB.wheel, s: dir, cycle: TAU, want: TAU / 0.7, r0: TETHER + 0.09, at: () => c, pose: th => add(c, mul(spoke(PB.wheel, th), 0.09)) }, body, turns), makeMass, 'poi'));
const chuckCarried = (dr, body, turns) => memo(() => loopClip(carried(dr, body, turns)));

/* ---------- poi: solved paths ---------- */
// Give the head a path and a timing. The only forces on it are gravity and the pull of the
// tether, so the tether must lie along (acceleration - gravity): that fixes where the hand is.
//   head: p (0 to 1) -> position;  dur: seconds per repeat
const solved = (dur, head, extra) => memo(() => {
  const n = Math.round(dur * 120), dp = 1 / (dur * 500), fr = [];
  let weak = Infinity;
  for (let i = 0; i < n; i++) {
    const p = i / n, P = head(p), a = mul(add(sub(head(p + dp), mul(P, 2)), head(p - dp)), 1 / (dp * dur) ** 2), need = sub(a, GRAVITY);
    weak = Math.min(weak, len(need));
    fr.push({ h: add(P, mul(norm(need), TETHER)), P });
  }
  if (weak < 2.5) throw new Error(`the head is close to free fall (${weak.toFixed(1)} m/s2 of pull): the tether would go slack`);
  return { kind: 'poi', method: 'solved', dur, loop: true, closure: 0, trail: 0.92, fr, pull: weak, ...extra };
});
// Head paths made of two turning arms: R along angle t, L along angle k t (the poi-theory picture).
const twoArm = (c, bs, R, k, dir, phase = 0, L = TETHER) => p => { const t = dir * TAU * p; return add(c, add(mul(spoke(bs, t), R), mul(spoke(bs, k * t + phase), L))); };

/* ---------- still pictures ---------- */
// A still is a one-frame scene with labels. `cam` zooms in on a point; `hide` leaves the figure out.
const still = o => () => ({ scene: true, still: true, method: 'still', dur: 1, loop: false, props: o.props || [], fr: [o.frame || {}], labels: o.labels, quads: o.quads, cam: o.cam, hide: o.hide });

// Still pictures are put together from parts: each is a prop and its points.
function picture(parts, opt = {}) {
  const frame = { ...opt.pose }, props = [];
  parts.forEach((p, i) => { props.push(p.prop); for (const k in p.keys) frame[i + '.' + k] = p.keys[k]; });
  return still({ props, frame, ...opt });
}
const line = (pts, w, role) => ({ prop: { kind: 'line', pts: pts.map((_, i) => 'p' + i), w, role }, keys: Object.fromEntries(pts.map((p, i) => ['p' + i, p])) });
const fistAt = (o, x, w, grip, r) => ({ prop: { kind: 'fist', hand: 'R', grip, r }, keys: { o, x, w } });
const ballAt = (c, r, role) => ({ prop: { kind: 'ball', r, role }, keys: { c } });

module.exports = { picture, line, fistAt, ballAt, fitted, elbowAt, ELBOW, score, rotY, turnFeet, BODY, bodyKeys, carried, carriedClip, figure8, handRotor, unit, palmSpin, buugengIsolation, weaveDriver, weave, poiCarried, chuckCarried, solved, twoArm, still };
