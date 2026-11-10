'use strict';
// Trick demonstrations: the player.
//
// The motion itself is worked out ahead of time by sim/bake.js and stored in web/clips.
// A clip is either simulated (gravity, drag, tension, contact), solved (the hand path that
// makes a tethered weight follow a given path exactly), hand-driven (a rigid prop goes
// where the hand takes it) or a still picture. This file draws a jointed figure and its
// props from those clips, lets you change the view, and can save a clip as a GIF.
const Demo = (() => {
  const TAU = Math.PI * 2, { sin, cos, sqrt } = Math;
  const add = (a, b) => [a[0] + b[0], a[1] + b[1], a[2] + b[2]];
  const sub = (a, b) => [a[0] - b[0], a[1] - b[1], a[2] - b[2]];
  const mul = (a, k) => [a[0] * k, a[1] * k, a[2] * k];
  const dot = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
  const cross = (a, b) => [a[1] * b[2] - a[2] * b[1], a[2] * b[0] - a[0] * b[2], a[0] * b[1] - a[1] * b[0]];
  const len = a => Math.hypot(a[0], a[1], a[2]);
  const norm = a => mul(a, 1 / (len(a) || 1));
  const mix = (a, b, t) => add(mul(a, 1 - t), mul(b, t));
  const rot = (v, k, ang) => add(add(mul(v, cos(ang)), mul(cross(k, v), sin(ang))), mul(k, dot(k, v) * (1 - cos(ang))));
  const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, v));
  const smooth = (a, b, x) => { const t = clamp((x - a) / (b - a), 0, 1); return t * t * (3 - 2 * t); };
  const flip = v => [-v[0], v[1], v[2]];

  /* ---------- the figure ---------- */
  // World: x toward the figure's left, y up, z forward, in metres. A pose gives the pelvis,
  // the way the chest faces, the line of the spine, the feet and the hands; everything
  // else (shoulders, elbows, knees) follows from bone lengths. Missing parts stand at rest.
  const UP = [0, 1, 0], BASE = { U_ARM: 0.31, F_ARM: 0.28, THIGH: 0.47, SHIN: 0.465 };
  // head is the centre of the head above the pelvis: where the 3D model's head is (it stands 1.79 m).
  const DIM0 = { pelvis: 0.97, chest: 0.46, head: 0.718, shoulder: 0.2, hip: 0.1, stance: 0.14, ankle: 0.04 };
  let U_ARM = BASE.U_ARM, F_ARM = BASE.F_ARM, THIGH = BASE.THIGH, SHIN = BASE.SHIN, REACH = U_ARM + F_ARM, DIM = { ...DIM0 };
  // Body presets for avatars: a height (it scales lengths) and a build (it scales widths: shoulders,
  // hips, trunk and limbs; breadth follows height only loosely in people, so height leaves it). The
  // standard body (1, 1) is the one every clip was simulated for; sim/presets.js checks each clip on
  // each preset, and a clip is moved as a whole to fit a preset (fit), which leaves its physics as it was.
  const BODY = { sc: 1, bd: 1, frame: 'neutral' }, FRAMES = ['neutral', 'feminine', 'masculine'];
  const HEIGHTS = { short: 0.9, average: 1, tall: 1.035 }, BUILDS = { lean: 0.92, average: 1, broad: 1.1 };
  function setBody(sc = 1, bd = 1) {
    BODY.sc = sc; BODY.bd = bd;
    U_ARM = BASE.U_ARM * sc; F_ARM = BASE.F_ARM * sc; THIGH = BASE.THIGH * sc; SHIN = BASE.SHIN * sc; REACH = U_ARM + F_ARM;
    DIM = { pelvis: DIM0.pelvis * sc, chest: DIM0.chest * sc, head: DIM0.head * sc, shoulder: DIM0.shoulder * bd, hip: DIM0.hip * bd, stance: DIM0.stance, ankle: DIM0.ankle };
    Object.assign(dim, DIM, { U_ARM, F_ARM, REACH, THIGH, SHIN });
  }
  // A frame moved to fit the current body. The hands, elbows and props rise or sink with the
  // shoulders (lift), so they keep their place relative to them; the pelvis rises with the legs; the
  // feet stay on the floor; directions do not move. A pure shift keeps the physics as simulated.
  const DIRKEY = /(^|\.)(d|u|v|x|w|face|spine|tL|tR)$/;
  const lift = () => (DIM0.pelvis + DIM0.chest) * (BODY.sc - 1);
  function fit(f) {
    if (BODY.sc === 1) return f;
    const dy = lift(), dp = DIM0.pelvis * (BODY.sc - 1), o = {};
    for (const k in f) {
      const v = f[k];
      o[k] = k === 'fL' || k === 'fR' || DIRKEY.test(k) || !Array.isArray(v) ? v : [v[0], v[1] + (k === 'pelvis' ? dp : dy), v[2]];
    }
    return o;
  }
  // The middle joint of a two-bone limb, bent toward the hint direction.
  function bend(S, H, U, F, hint) {
    const v = sub(H, S), D = clamp(len(v), 0.02, U + F - 0.002), dir = norm(v);
    const a = (U * U - F * F + D * D) / (2 * D), h = sqrt(Math.max(U * U - a * a, 0));
    return add(S, add(mul(dir, a), mul(norm(sub(hint, mul(dir, dot(hint, dir)))), h)));
  }
  function rig(p) {
    const pelvis = p.pelvis || [0, DIM.pelvis, 0], face = norm(p.face ? [p.face[0], 0, p.face[2]] : [0, 0, 1]), up = norm(p.spine || UP);
    const side = cross(UP, face), left = norm(cross(up, face)), fwd = cross(left, up);
    const chest = add(pelvis, mul(up, DIM.chest)), head = add(pelvis, mul(up, DIM.head));
    const J = { pelvis, chest, head, face, up, left, nose: add(head, mul(fwd, 0.1)) };
    for (const [s, k] of [['L', 1], ['R', -1]]) {
      const sh = add(chest, mul(left, DIM.shoulder * k)), hip = add(pelvis, mul(side, DIM.hip * k));
      const hand = p['h' + s] || add(pelvis, add(mul(side, 0.26 * BODY.bd * k), add([0, -0.04, 0], mul(face, -0.04)))); // at rest, beside the hip, further out on a broad build
      // Where the elbow goes. Low and in front it hangs down and back; overhead it points out
      // to the side; behind the back it points out and back; close across the chest it comes forward.
      const r = sub(hand, chest), own = dot(r, left) * k, front = dot(r, fwd);
      const high = smooth(1.5, 1.7, hand[1] - pelvis[1] + DIM.pelvis), behind = smooth(-0.02, -0.14, front) * (1 - high);
      const across = smooth(0.08, -0.12, own) * smooth(0.52, 0.38, len(sub(hand, sh))) * (1 - high) * (1 - behind);
      let hint = mix([0.4, -0.6, -0.7], [1, 0, -0.3], high);
      hint = mix(mix(hint, [1, -0.1, -0.4], behind), [0.6, -0.7, 0.3], across);
      // A move can say where it wants the elbow; otherwise the rule above decides.
      const el = bend(sh, hand, U_ARM, F_ARM, p['e' + s] ? sub(p['e' + s], sh) : add(add(mul(left, hint[0] * k), mul(up, hint[1])), mul(fwd, hint[2])));
      const foot = p['f' + s] || [pelvis[0] + side[0] * DIM.stance * k, DIM.ankle, pelvis[2] + side[2] * DIM.stance * k];
      const toe = norm(p['t' + s] ? [p['t' + s][0], 0, p['t' + s][2]] : face), knee = bend(hip, foot, THIGH, SHIN, add(toe, mul(side, 0.2 * k)));
      Object.assign(J, { ['sh' + s]: sh, ['el' + s]: el, ['h' + s]: hand, ['hip' + s]: hip, ['knee' + s]: knee, ['f' + s]: foot, ['t' + s]: toe });
    }
    return J;
  }
  // The figure as capsules (a line with a radius), for contact: what the physics and the body checks
  // keep the props clear of. They are fitted to the 3D model's skin (node test/body3d.js rig measures
  // how far the skin reaches outside them), so what the checks pass is what the drawn figure shows:
  // the head is longer than it is wide, the hips and the buttocks reach well outside the trunk, each
  // limb is thicker near the body than near its end, and the shoulders and the neck have their own.
  // An arm that holds a prop stops 10 cm short of the fist: the wrist and the hand are wrapped around it.
  const cap = (a, b, r, name, side) => ({ a, b, r, name, side });
  function caps(J, hold = {}) {
    const sc = BODY.sc, bd = BODY.bd, up = J.up, fwd = cross(J.left, up), at = (v, h, f = 0) => add(add(v, mul(up, h * sc)), mul(fwd, f));
    const out = [
      cap(at(J.head, 0, -0.035), at(J.head, -0.02, 0.04), 0.105, 'head'),
      cap(at(J.chest, 0.03, -0.01), at(J.head, -0.07, 0), 0.075 * bd, 'neck'),
      cap(at(J.chest, 0.15 - 0.46), J.chest, 0.145 * bd, 'torso'), // waist to chest
      // Belly, pelvis and buttocks: two upright capsules side by side, as the hips are wider than they are deep.
      cap(at(add(J.pelvis, mul(J.left, 0.06 * bd)), -0.1, -0.02), at(add(J.pelvis, mul(J.left, 0.06 * bd)), 0.15, -0.02), 0.14 * bd, 'torso'),
      cap(at(add(J.pelvis, mul(J.left, -0.06 * bd)), -0.1, -0.02), at(add(J.pelvis, mul(J.left, -0.06 * bd)), 0.15, -0.02), 0.14 * bd, 'torso'),
      cap(at(add(J.shL, mul(J.left, -0.06 * bd)), 0, -0.035), at(add(J.shR, mul(J.left, 0.06 * bd)), 0, -0.035), 0.095 * bd, 'torso'), // chest top and trapezius, short of the deltoids
      cap(at(add(J.shL, mul(J.left, -0.1 * bd)), -0.1, -0.085), at(add(J.shR, mul(J.left, 0.1 * bd)), -0.1, -0.085), 0.07 * bd, 'torso'), // the upper back, flat and broad
    ];
    for (const s of ['L', 'R']) {
      const el = J['el' + s], hand = J['h' + s], hip = J['hip' + s], knee = J['knee' + s], foot = J['f' + s], sh = J['sh' + s];
      const t1 = mix(hip, knee, 0.35), t2 = mix(hip, knee, 0.7), midS = mix(knee, foot, 0.45), midA = mix(sh, el, 0.5), midF = mix(el, hand, 0.5);
      const hipOut = at(add(hip, mul(J.left, 0.07 * bd * (s === 'L' ? 1 : -1))), -0.11); // the hip joint's outer bulge
      out.push(cap(hipOut, hipOut, 0.085 * bd, 'thigh', s), cap(hip, t1, 0.1 * bd, 'thigh', s), cap(t1, t2, 0.075 * bd, 'thigh', s), cap(t2, knee, 0.065 * bd, 'thigh', s),
        cap(knee, knee, 0.085 * bd, 'shin', s), cap(knee, midS, 0.072 * bd, 'shin', s), cap(midS, foot, 0.045 * bd, 'shin', s),
        cap(add(sh, mul(J.left, (s === 'L' ? -0.03 : 0.03) * bd)), sh, 0.06 * bd, 'upper arm', s), cap(sh, midA, 0.062 * bd, 'upper arm', s), cap(midA, el, 0.055 * bd, 'upper arm', s), // the deltoid, then the arm
        cap(el, midF, 0.063 * bd, 'forearm', s), cap(midF, hold[s] ? add(hand, mul(norm(sub(el, hand)), 0.1)) : hand, 0.05 * bd, 'forearm', s));
    }
    return out;
  }

  /* ---------- props ---------- */
  const GRIP = 0.12, BUTT = 0.18; // nunchaku: fist to link end, fist to tail end of the handle
  const dim = { ...DIM, U_ARM, F_ARM, REACH, THIGH, SHIN, GRIP, BUTT }; // live: setBody() updates it
  // A buugeng as two half-circles joined into an S, in its own plane.
  const S_SHAPE = (() => { const r = 0.19, pts = []; for (let i = 0; i <= 6; i++) { const t = Math.PI * i / 6; pts.push([-r - r * cos(t), r * sin(t)]); } for (let i = 1; i <= 6; i++) { const t = Math.PI * i / 6; pts.push([r - r * cos(t), -r * sin(t)]); } return pts; })();
  const sPoints = (c, u, v) => S_SHAPE.map(([x, y]) => add(c, add(mul(u, x), mul(v, y))));
  // What to draw for prop number i of a frame, and the point that leaves a trail.
  function parts(pr, i, f) {
    const K = k => f[i + '.' + k];
    if (pr.kind === 'poi') {
      const pts = [K('o') || f['h' + pr.hand]];
      for (let j = 1; K('q' + j); j++) pts.push(K('q' + j));
      return { tip: K('P'), segs: [[[...pts, K('P')], 0.012, 'link']], balls: [[K('P'), 0.05, 'prop']] };
    }
    if (pr.kind === 'staff') { const t = K(pr.tip || 'a'); return { tip: t, segs: [[[K('a'), K('b')], 0.034, 'prop']], balls: [[t, 0.032, 'trail']] }; }
    if (pr.kind === 'buugeng') { const pts = sPoints(K('c'), K('u'), K('v')); return { tip: pts[0], segs: [[pts, 0.036, 'prop']], balls: [[pts[0], 0.03, 'trail']] }; }
    if (pr.kind === 'nunchaku') {
      const g = K('g'), d = K('d'), top = add(g, mul(d, GRIP));
      return { tip: K('T'), segs: [[[sub(g, mul(d, BUTT)), top], 0.045, 'prop'], [[top, K('B')], 0.012, 'link'], [[K('B'), K('T')], 0.045, 'prop']], balls: [] };
    }
    if (pr.kind === 'fist') return fist(pr, K('o'), K('x'), K('w'));
    // Plain shapes, used by still pictures: a line or a ball, in a given colour role.
    if (pr.kind === 'line') return { segs: [[pr.pts.map(k => K(k)), pr.w, pr.role || 'prop']], balls: [] };
    if (pr.kind === 'ball') return { segs: [], balls: [[K('c'), pr.r, pr.role || 'prop']] };
    return { segs: [], balls: [] };
  }

  // A hand in close-up, for grip pictures. o is the middle of the grip on the stick's axis,
  // x points along the stick out of the thumb side of the fist, and w runs from the wrist
  // toward the knuckles. grip: 'closed' wraps the fingers round, 'open' lays them flat.
  function fist(pr, o, x, w) {
    // On the whole figure the same hand is drawn a little larger than life (k), without its own forearm.
    const k = pr.k || 1, n = mul(cross(x, w), pr.hand === 'L' ? -1 : 1), r = (pr.r || 0.014), rho = r + 0.01 * k, segs = [], role = pr.role || 'skin';
    const round = (a, off) => add(add(o, mul(x, off * k)), add(mul(n, -rho * cos(a)), mul(w, rho * sin(a)))); // a point around the stick
    const wrist = add(o, add(mul(w, -0.08 * k), mul(n, -(r + 0.02 * k)))), knuckles = add(o, add(mul(w, 0.012 * k), mul(n, -(r + 0.021 * k))));
    if (!pr.bare) segs.push([[add(wrist, mul(w, -0.2)), wrist], 0.058, role]);
    segs.push([[wrist, knuckles], 0.078 * k, role]);
    for (let i = 0; i < 4; i++) {
      const off = 0.031 - i * 0.021, reach = (0.088 - Math.abs(i - 1) * 0.008) * k;
      if (pr.grip === 'open') { const q = add(knuckles, mul(x, off * k)); segs.push([[q, add(q, add(mul(w, reach * 0.55), mul(n, 0.006 * k))), add(q, add(mul(w, reach), mul(n, 0.022 * k)))], 0.017 * k, role]); }
      else segs.push([Array.from({ length: 9 }, (_, j) => round(0.3 + j / 8 * reach / rho, off)), 0.017 * k, role]);
    }
    // The thumb comes round the other way and closes over the stick (or over the first finger). On an open hand it stands out to the side.
    const base = add(add(o, mul(x, 0.05 * k)), add(mul(w, -0.05 * k), mul(n, -(r + 0.006 * k))));
    segs.push([pr.grip === 'open' && pr.bare ? [base, add(base, add(mul(x, 0.03 * k), mul(w, 0.035 * k))), add(base, add(mul(x, 0.04 * k), mul(w, 0.07 * k)))] : [base, round(-1.9, 0.044), round(-2.9, 0.03), round(-3.6, 0.016)], 0.021 * k, role]);
    return { segs, balls: [], tip: null };
  }

  /* ---------- clips ---------- */
  const SWAP = { hR: 'hL', hL: 'hR', fR: 'fL', fL: 'fR', tR: 'tL', tL: 'tR', eR: 'eL', eL: 'eR' };
  const mirror = f => { const o = {}; for (const k in f) o[SWAP[k] || k] = flip(f[k]); return o; };
  const blend = (a, b, t) => { const o = {}; for (const k in a) o[k] = a[k] === b[k] ? a[k] : mix(a[k], b[k], t); return o; };
  // Stored clips keep the numbers that change in one flat list, and the rest once.
  function unpack(c) {
    const n = c.keys.length;
    c.fr = Array.from({ length: c.n }, (_, i) => { const f = { ...c.const }; c.keys.forEach((k, j) => { const o = (i * n + j) * 3; f[k] = [c.data[o] / 1e4, c.data[o + 1] / 1e4, c.data[o + 2] / 1e4]; }); return f; });
    delete c.data;
    return c;
  }
  function pack(c) {
    const first = c.fr[0], keys = Object.keys(first).filter(k => c.fr.some(f => len(sub(f[k], first[k])) > 5e-5)), q = x => Math.round(x * 1e4);
    const out = { ...c, n: c.fr.length, keys, const: Object.fromEntries(Object.keys(first).filter(k => !keys.includes(k)).map(k => [k, first[k].map(x => q(x) / 1e4)])), data: c.fr.flatMap(f => keys.flatMap(k => f[k].map(q))) };
    delete out.fr;
    return out;
  }
  // The frame at phase ph (0 to 1), mirrored for the left hand when asked.
  function pose(clip, ph, mir) {
    const fr = clip.fr, N = fr.length;
    let x, i, j;
    if (clip.loop) { x = (((ph % 1) + 1) % 1) * N; i = Math.floor(x) % N; j = (i + 1) % N; }
    else { x = clamp(ph, 0, 1) * (N - 1); i = Math.floor(x); j = Math.min(i + 1, N - 1); }
    const f = blend(fr[i], fr[j], x - Math.floor(x));
    return fit(mir ? mirror(f) : f);
  }
  const other = s => s === 'L' ? 'R' : 'L';
  const propsOf = (clip, mir) => mir ? clip.props.map(p => p.hand ? { ...p, hand: other(p.hand) } : p) : clip.props;

  // The unit vector `a` turned toward `b` by at most `max` radians (or onto it when it is nearer).
  function turnToward(a, b, max) {
    const ang = Math.acos(clamp(dot(a, b), -1, 1));
    if (ang <= max) return b;
    let axis = cross(a, b);
    if (len(axis) < 1e-6) axis = cross(a, Math.abs(a[1]) < 0.9 ? UP : [1, 0, 0]);
    return norm(rot(a, norm(axis), max));
  }
  // The hand: a fist round whatever it holds, or an open hand with the thumb forward.
  // Returns where the grip is (o), the axis out of the thumb side (x), wrist-to-knuckles (w), and how big the thing held is.
  function handPose(J, s, props, f, HAND = 1) {
    const H = J['h' + s], fa = norm(sub(H, J['el' + s])), side = s === 'L' ? 1 : -1;
    // What the hand is wrapped round, if anything: the axis of the prop where it passes through the fist.
    let g = null;
    for (const [i, p] of props.entries()) {
      const K = q => f[i + '.' + q];
      if (p.kind === 'nunchaku') {
        // Either stick can be the one in the hand: after a roll or a pass it is the other one.
        const top = add(K('g'), mul(K('d'), GRIP)), near = (a, b) => { const d = sub(b, a), t = clamp(dot(sub(H, a), d) / dot(d, d), 0, 1); return len(sub(H, add(a, mul(d, t)))); };
        if (near(sub(K('g'), mul(K('d'), BUTT)), top) < 0.035) g = { x: K('d'), r: 0.0225 };
        else if (near(K('B'), K('T')) < 0.035) g = { x: norm(sub(K('B'), K('T'))), r: 0.0225 };
      }
      if (p.kind === 'staff') { const d = sub(K('a'), K('b')), t = clamp(dot(sub(H, K('b')), d) / dot(d, d), 0, 1); if (len(sub(H, add(K('b'), mul(d, t)))) < 0.065) g = { x: norm(d), r: 0.017, at: add(K('b'), mul(d, t)) }; }
      if (p.kind === 'buugeng' && len(sub(H, K('c'))) < 0.03) g = { x: K('v'), r: 0.018 };
      if (p.kind === 'poi' && p.hand === s) g = { x: null, r: 0.008, to: norm(sub(K('q1') || K('P'), K('o') || H)) };
      if (g) break;
    }
    let x, w, o = H, grip = 'closed';
    if (g && g.x) { x = g.x; w = sub(fa, mul(x, dot(fa, x))); if (len(w) < 0.25) w = cross(x, cross(fa, J.left)); if (len(w) < 0.1) w = cross(x, UP); w = norm(w); o = g.at || H; }
    else {
      // A poi's tether leaves between the fingers, so the fist turns toward it, as far as a wrist bends
      // (about 75 degrees); with the knuckles along the forearm the tether would run through the arm.
      w = g && g.to ? turnToward(fa, g.to, 1.3) : fa;
      x = g ? cross(w, g.to) : sub(J.face, mul(fa, dot(J.face, fa)));
      if (g && len(x) < 0.2) x = cross(fa, g.to);
      if (len(x) < 0.2) x = cross(fa, mul(J.left, side)); x = norm(x);
      if (!g) { grip = 'open'; o = add(H, mul(w, 0.07 * HAND)); } else o = add(H, mul(w, 0.03));
    }
    return { o, x, w, grip, r: g ? g.r : 0.012 };
  }

  /* ---------- avatars ---------- */
  // The viewer's avatar (web/avatar-def.json lists the options), or null for the standard figure.
  // Its body preset applies to each clip that passed the body checks on that preset (sim/presets.js
  // writes web/clips/presets.json); any other clip plays on the standard body and its caption says so.
  let av = null, presetFails = {};
  const presetOf = a => a ? `${HEIGHTS[a.body] ? a.body : 'average'}-${BUILDS[a.build] ? a.build : 'average'}` : 'average-average';
  const STANDARD = 'average-average';
  // Why a clip does not play on the viewer's body, or '' when it does (or the viewer has the standard body).
  const unfit = (f, who = av) => {
    const id = presetOf(who);
    if (id === STANDARD) return '';
    if (!presetFails) return 'the results of the body checks could not be loaded';
    return (presetFails[f] && presetFails[f][id]) || '';
  };
  // The 3D figure wears one avatar at a time, and rebuilding it costs a few milliseconds, so it changes
  // only when a different person's avatar is drawn (someone's profile, a milestone sticker).
  let worn = 'none';
  function wear(who) {
    const k = who ? JSON.stringify(who) : 'none';
    if (k === worn || typeof window === 'undefined' || !window.Figure3D || !window.Figure3D.ready) return;
    worn = k; window.Figure3D.setAvatar(who || null);
  }
  // The frame (neutral, feminine or masculine) shapes the 3D figure only; the rig is the same.
  const frameOf = who => who && FRAMES.includes(who.frame) ? who.frame : 'neutral';
  function useBody(clip, who = av) {
    const [h, b] = (unfit(clip.f, who) ? STANDARD : presetOf(who)).split('-'), sc = HEIGHTS[h], bd = BUILDS[b], frame = frameOf(who);
    if (sc === BODY.sc && bd === BODY.bd && frame === BODY.frame) return;
    setBody(sc, bd); BODY.frame = frame;
    if (typeof window !== 'undefined' && window.Figure3D && window.Figure3D.ready) window.Figure3D.setBody(sc, bd, frame);
  }
  function avatar(cfg) {
    av = cfg && cfg.v ? cfg : null;
    if (typeof document !== 'undefined') { mount(); comboMount(); }
  }
  // The prop in the avatar's colour for that prop, if one was chosen.
  const tint = (col, cid, who = av) => who && who.props && who.props[cid] ? { ...col, prop: who.props[cid] } : col;

  /* ---------- the 3D figure ---------- */
  // A rigged human model drawn by web/figure3d.js, loaded only when wanted. Until it is ready,
  // or where WebGL is missing, the drawn figure is used.
  let want3d = false, asked3d = false;
  function figure(on) {
    want3d = !!on;
    if (!on || asked3d || typeof document === 'undefined') return;
    asked3d = true;
    const sc = document.createElement('script'); sc.src = 'figure3d.js';
    sc.onload = () => window.Figure3D.load('assets/human.glb').then(ok => { if (ok) { window.Figure3D.setBody(BODY.sc, BODY.bd, BODY.frame); worn = ''; wear(av); mount(); comboMount(); } });
    document.head.appendChild(sc);
  }
  const use3d = clip => want3d && typeof window !== 'undefined' && window.Figure3D && window.Figure3D.ready;
  // A grip picture has no body of its own. For the model, stand one behind it: the fists in
  // the picture become the model's hands, held out in front of the chest, and a hand the
  // picture does not show hangs open. Returns the pose and the hand frames.
  function closeUp(props, f) {
    const fists = props.map((p, i) => [p, i]).filter(([p]) => p.kind === 'fist'), K = (i, k) => f[i + '.' + k];
    const at = fists.length ? mul(fists.reduce((a, [, i]) => add(a, K(i, 'o')), [0, 0, 0]), 1 / fists.length) : [0, 0, 0];
    // One hand: its shoulder stands straight behind it, the arm out level. Two hands: the body is centred.
    const one = fists.length === 1 ? (fists[0][0].hand === 'L' ? -0.2 : 0.2) : 0;
    const pelvis = add(at, [one, -DIM.chest, one ? -0.56 : -0.52]), p = { pelvis, face: [0, 0, 1] };
    for (const [s, k] of [['L', 1], ['R', -1]]) p['f' + s] = add(pelvis, [DIM.stance * k, DIM.ankle - DIM.pelvis, 0]);
    const hands = {};
    for (const [pr, i] of fists) { p['h' + pr.hand] = K(i, 'o'); hands[pr.hand] = { o: K(i, 'o'), x: K(i, 'x'), w: K(i, 'w'), grip: pr.grip || 'closed', r: pr.r || 0.012 }; }
    const J = rig(p);
    for (const s of ['L', 'R']) if (!hands[s]) hands[s] = handPose(J, s, [], f);
    return { J, hands };
  }

  // What the 3D figure is given for one frame: the joints, the two hands and the props as tubes and
  // balls with their colour roles. A close-up has no body of its own, so one stands behind the hands.
  function stage(clip, f, props) {
    const near = clip.hide ? closeUp(props, f) : null, J = near ? near.J : rig(f), list = [];
    props.forEach((p, i) => { if (p.kind === 'fist') return; const pt = parts(p, i, f); for (const [pts, wd, role] of pt.segs) list.push({ pts, w: wd, role, hand: p.hand }); for (const [c, r, role] of pt.balls) list.push({ c, r, role, hand: p.hand }); });
    return { J, hands: near ? near.hands : { L: handPose(J, 'L', props, f), R: handPose(J, 'R', props, f) }, list, near };
  }

  /* ---------- drawing ---------- */
  // who: whose avatar (default the viewer's). view: 'front', 'angle', 'side', or a yaw in radians.
  function draw(ctx, W, clip, ph, view, mir, col, full, who = av) {
    if (use3d(clip)) wear(who);
    useBody(clip, who);
    const cam = clip.cam, zoom = cam ? cam.zoom : 1, S = W / 2.75 * zoom, base = W * 0.93;
    // Yaw turns the camera toward the working hand's side: 0 is front, 90 degrees is side.
    const yaw = typeof view === 'number' ? view : { front: 0, angle: 0.66, side: Math.PI / 2 }[view], tilt = view === 'angle' ? 0.2 : typeof view === 'number' ? 0.12 : 0;
    const cy = cos(yaw), sy = sin(yaw), k = mir ? -1 : 1;
    const flat = v => { const depth = v[2] * cy - k * v[0] * sy; return [v[0] * cy + k * (v[2] - 0.3 * sy) * sy, v[1] * cos(tilt) - depth * sin(tilt), depth]; };
    const up = lift(), at0 = cam ? [cam.at[0], cam.at[1] + up, cam.at[2]] : null; // the camera and planes move with the clip (fit)
    const aim = cam ? flat(mir ? flip(at0) : at0) : null;
    const pr = v => { const q = flat(v); return aim ? [W / 2 + (q[0] - aim[0]) * S, W / 2 - (q[1] - aim[1]) * S, q[2]] : [W / 2 + q[0] * S, base - q[1] * S, q[2]]; };
    const items = [];
    const seg = (pts, w, color, lift = 0) => { const q = pts.map(pr); items.push({ z: q.reduce((a, b) => a + b[2], 0) / q.length + lift, q, w: w * S, color }); };
    const ball = (c, r, color, lift = 0) => { const q = pr(c); items.push({ z: q[2] + lift, dot: q, r: r * S, color }); };
    const fill = (pts, color) => { const q = pts.map(pr); items.push({ z: q.reduce((a, b) => a + b[2], 0) / q.length, q, fill: color }); };
    ctx.fillStyle = col.bg; ctx.fillRect(0, 0, W, W);
    ctx.lineCap = 'round'; ctx.lineJoin = 'round';
    if (!cam) {
      ctx.strokeStyle = col.floor; ctx.lineWidth = W / 360;
      ctx.beginPath(); ctx.moveTo(W * 0.08, base + 0.02 * S); ctx.lineTo(W * 0.92, base + 0.02 * S); ctx.stroke();
    }
    for (const q of clip.quads || []) {
      const corner = (i, j) => { const v = add([q.c[0], q.c[1] + up, q.c[2]], add(mul(q.a, i * q.k), mul(q.b, j * q.k))); return pr(mir ? flip(v) : v); };
      const c = [corner(1, 1), corner(-1, 1), corner(-1, -1), corner(1, -1)];
      ctx.beginPath(); c.forEach((v, i) => i ? ctx.lineTo(v[0], v[1]) : ctx.moveTo(v[0], v[1])); ctx.closePath();
      ctx.fillStyle = col.plane; ctx.fill(); ctx.strokeStyle = col.prop; ctx.globalAlpha = 0.5; ctx.lineWidth = W / 240; ctx.stroke(); ctx.globalAlpha = 1;
    }
    const f = pose(clip, ph, mir), props = propsOf(clip, mir), busy = {}, solid = use3d(clip);
    for (const p of props) if (p.hand) busy[p.hand] = true;
    if (solid) {
      // The model, its props and their shading come from three.js; trails and labels are drawn over it here.
      const sg = stage(clip, f, props), { J, hands, near } = sg, list = sg.list.map(it => ({ ...it, color: col[it.role] })), R = [cy, 0, k * sy], D = [-k * sy, 0, cy], U = sub([0, cos(tilt), 0], mul(D, sin(tilt)));
      if (!near) {
        const sh = pr([J.pelvis[0], 0, J.pelvis[2]]), g = ctx.createRadialGradient(sh[0], sh[1], 0, sh[0], sh[1], 0.42 * S);
        g.addColorStop(0, 'rgba(0,0,0,.35)'); g.addColorStop(1, 'rgba(0,0,0,0)');
        ctx.save(); ctx.translate(sh[0], sh[1]); ctx.scale(1, 0.28); ctx.translate(-sh[0], -sh[1]); ctx.fillStyle = g; ctx.fillRect(sh[0] - S, sh[1] - S, 2 * S, 2 * S); ctx.restore();
      }
      const gl = window.Figure3D.render(W, J, hands, list,
        { right: R, up: U, toward: cross(R, U), cx: (aim ? aim[0] : 0) + 0.3 * k * sy * sy, cy: aim ? aim[1] : (base - W / 2) / S, half: W / (2 * S), cut: near && view !== 'side' ? 0.25 : 0 });
      ctx.drawImage(gl, 0, 0, W, W);
    } else if (!clip.hide) {
      const J = rig(f), HAND = 1.3;
      for (const s of ['L', 'R']) {
        const foot = J['f' + s], knee = J['knee' + s], el = J['el' + s], H = J['h' + s], tone = busy[s] ? col.arm : col.body, lift = busy[s] ? 0.02 : 0;
        // Limbs taper: thigh to shin, upper arm to forearm.
        seg([J['hip' + s], knee], 0.135, col.body); seg([knee, foot], 0.1, col.body); ball(knee, 0.052, col.body, -0.01);
        seg([add(foot, [0, -0.01, 0]), add(foot, add(mul(J['t' + s], 0.17), [0, -0.025, 0]))], 0.07, col.body);
        seg([J['sh' + s], el], 0.088, tone, lift); seg([el, H], 0.068, tone, lift); ball(J['sh' + s], 0.052, tone, lift - 0.01);
        const hp = handPose(J, s, props, f, HAND);
        for (const [pts, wd] of fist({ hand: s, grip: hp.grip, r: hp.r, k: HAND, bare: true }, hp.o, hp.x, hp.w).segs) seg(pts, wd, busy[s] ? col.arm : col.body, (busy[s] ? 0.075 : 0.035));
      }
      // Trunk: shoulders, a narrower waist, hips.
      const hipL = add(J.pelvis, mul(sub(J.hipL, J.pelvis), 1.3)), hipR = add(J.pelvis, mul(sub(J.hipR, J.pelvis), 1.3)), mid = add(J.pelvis, mul(J.up, 0.2)), wl = mul(J.left, 0.125);
      fill([J.shL, J.shR, sub(mid, wl), hipR, hipL, add(mid, wl)], col.body);
      seg([J.shL, J.shR], 0.1, col.body); seg([hipL, hipR], 0.13, col.body);
      seg([add(J.pelvis, mul(J.up, 0.05)), add(J.pelvis, mul(J.up, 0.2))], 0.19, col.body); seg([add(J.pelvis, mul(J.up, 0.24)), add(J.pelvis, mul(J.up, 0.4))], 0.215, col.body);
      seg([J.chest, add(J.head, mul(J.up, -0.09))], 0.075, col.body); // the neck, up to the head
      // Head: a skull with a jaw below and in front, and a nose to show which way it faces.
      const fw = cross(J.left, J.up);
      ball(add(J.head, mul(J.up, 0.012)), 0.098, col.body, 0.01); ball(add(J.head, add(mul(J.up, -0.05), mul(fw, 0.022))), 0.07, col.body, 0.005);
      ball(J.nose, 0.022, col.body, dot(J.face, [-k * sy, 0, cy]) > 0 ? 0.03 : -0.2);
    }
    const tips = [];
    props.forEach((p, i) => {
      const pt = parts(p, i, f);
      if (!solid) { for (const [pts, wd, role] of pt.segs) seg(pts, wd, col[role], 0.05); for (const [c, r, role] of pt.balls) ball(c, r, col[role], 0.06); }
      if (!pt.tip || clip.still) return;
      // Trail of the prop's marked point, newest first.
      const span = full ? (clip.loop ? 1 : 0.3) : clip.loop ? clip.trail : 0.07, n = full || span > 0.6 ? 96 : 26;
      tips.push(Array.from({ length: n }, (_, j) => pr(parts(p, i, pose(clip, clip.loop ? ph - j / n * span : Math.max(ph - j / n * span, 0), mir)).tip)));
    });
    items.sort((a, b) => a.z - b.z);
    for (const it of items) {
      if (it.fill) { ctx.fillStyle = it.fill; ctx.beginPath(); it.q.forEach((v, i) => i ? ctx.lineTo(v[0], v[1]) : ctx.moveTo(v[0], v[1])); ctx.closePath(); ctx.fill(); continue; }
      if (it.dot) {
        ctx.fillStyle = col.bg; ctx.beginPath(); ctx.arc(it.dot[0], it.dot[1], it.r + W / 180, 0, TAU); ctx.fill();
        ctx.fillStyle = it.color; ctx.beginPath(); ctx.arc(it.dot[0], it.dot[1], it.r, 0, TAU); ctx.fill();
        continue;
      }
      // An outline in the background colour keeps overlapping limbs readable.
      for (const [w, c] of [[it.w + Math.min(W / 90, it.w * 0.7 + 1), col.bg], [it.w, it.color]]) {
        ctx.strokeStyle = c; ctx.lineWidth = w; ctx.beginPath();
        it.q.forEach((v, i) => i ? ctx.lineTo(v[0], v[1]) : ctx.moveTo(v[0], v[1])); ctx.stroke();
      }
    }
    ctx.strokeStyle = col.trail; ctx.lineWidth = W / 150;
    for (const t of tips) for (let i = 0; i < t.length - 1; i++) {
      ctx.globalAlpha = full ? 0.55 : 0.8 * (1 - i / t.length);
      ctx.beginPath(); ctx.moveTo(t[i][0], t[i][1]); ctx.lineTo(t[i + 1][0], t[i + 1][1]); ctx.stroke();
    }
    ctx.globalAlpha = 1;
    // Labels on still pictures: a short line from the point to its name.
    ctx.font = `600 ${W / 30}px system-ui, sans-serif`; ctx.textBaseline = 'middle';
    for (const l of clip.labels || []) {
      const at = l.key ? f[l.key] : mir ? flip(l.at) : l.at, p = pr(at), dx = (l.dx || 0) * (mir ? -1 : 1), q = [p[0] + dx * W, p[1] + (l.dy || 0) * W];
      ctx.strokeStyle = col.ink; ctx.lineWidth = W / 400; ctx.beginPath(); ctx.moveTo(p[0], p[1]); ctx.lineTo(q[0], q[1]); ctx.stroke();
      ctx.fillStyle = col.ink; ctx.beginPath(); ctx.arc(p[0], p[1], W / 180, 0, TAU); ctx.fill();
      // Keep the words inside the picture, whichever way the view is turned.
      const wide = ctx.measureText(l.text).width, left = dx < 0 ? q[0] - W / 90 - wide : dx > 0 ? q[0] + W / 90 : q[0] - wide / 2;
      const tx = clamp(left, W / 60, W - W / 60 - wide), ty = q[1] + (dx ? 0 : Math.sign(l.dy || 0) * W / 45);
      ctx.textAlign = 'left';
      ctx.strokeStyle = col.bg; ctx.lineWidth = W / 90; ctx.strokeText(l.text, tx, ty);
      ctx.fillText(l.text, tx, ty);
    }
  }

  function colors() {
    const cs = getComputedStyle(document.documentElement), v = n => cs.getPropertyValue(n).trim();
    return { bg: v('--side'), body: v('--ink-3'), arm: v('--ink-2'), ink: v('--ink'), prop: v('--accent'), link: v('--ink-2'), trail: v('--l4'), plane: v('--accent-soft'), floor: v('--line-2'), skin: v('--ink-2') };
  }

  /* ---------- player ---------- */
  const still = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
  const st = { id: '', variant: 0, view: 'angle', hand: 'R', speed: 0.5, playing: !still, phase: 0 };
  let index = {}; const cache = new Map();
  // The list of demos is small and loaded once; each clip is fetched when it is first shown.
  async function init() {
    try { const r = await fetch('clips/index.json'); if (r.ok) index = await r.json(); } catch { /* no demos */ }
    // Without the results no clip is known to fit another body, so every clip plays on the standard one.
    try { const r = await fetch('clips/presets.json'); presetFails = r.ok ? (await r.json()).fail || {} : null; } catch { presetFails = null; }
  }
  const has = (cid, id) => !!index[cid + ':' + id];
  const variant = () => index[st.id]?.v[st.variant];
  // The clip for the variant on show: undefined while it loads, null if it cannot be loaded.
  function clipNow() {
    const v = variant();
    if (!v) return null;
    return fetchClip(v.f, mount);
  }
  function fetchClip(f, then) {
    if (!cache.has(f)) {
      cache.set(f, undefined);
      fetch(`clips/${f}.json`).then(r => r.ok ? r.json() : Promise.reject(new Error(r.status))).then(c => { cache.set(f, Object.assign(unpack(c), { f })); then(); }, () => { cache.set(f, null); then(); });
    }
    return cache.get(f);
  }
  let raf = 0, last = 0;
  function paint() {
    const cv = document.getElementById('demo-cv'), v = variant();
    if (!cv || !v) return false;
    const ctx = cv.getContext('2d'), clip = clipNow();
    if (clip) { draw(ctx, cv.width, clip, st.phase, st.view, v.one && st.hand === 'L', tint(colors(), st.id.split(':')[0]), !st.playing); return true; }
    const col = colors();
    ctx.fillStyle = col.bg; ctx.fillRect(0, 0, cv.width, cv.width);
    ctx.fillStyle = col.link; ctx.font = `${cv.width / 26}px system-ui, sans-serif`; ctx.textAlign = 'center';
    ctx.fillText(clip === undefined ? 'Loading the demo…' : 'This demo could not be loaded.', cv.width / 2, cv.width / 2);
    return false;
  }
  function tick(ts) {
    raf = 0;
    const clip = variant() && clipNow();
    if (!clip || !st.playing || clip.still) { paint(); return; }
    st.phase = (st.phase + Math.min(ts - last, 100) / 1000 * st.speed / clip.dur) % 1;
    last = ts;
    if (paint()) raf = requestAnimationFrame(tick);
  }
  function mount() {
    if (!paint()) return;
    if (st.playing && !raf) { last = performance.now(); raf = requestAnimationFrame(tick); }
  }

  const segBtn = (k, v, label, on, aria, act = 'demo') => `<button class="${on ? 'on' : ''}" data-act="${act}" data-k="${k}" data-v="${v}" aria-pressed="${on}"${aria ? ` aria-label="${aria}"` : ''}>${label}</button>`;
  const viewName = () => st.view === 'angle' ? 'three-quarter' : st.view;
  const CAPTION = {
    sim: 'Simulated: the prop moves by gravity, air drag, tension in its cord or tether, and contact with the body. The hands follow a model of the move, not motion capture.',
    flight: 'Simulated: once released, the prop keeps its spin and its centre follows a ballistic arc. The catching hand arrives moving with it.',
    solved: 'Solved: the head follows the path the move is defined by, and the hand is placed exactly where gravity and tether tension produce that path.',
    roll: 'Rolling: the hand lets go and the nunchaku turns about the part of the body it rests on, both sticks out, at a steady rate, as a balanced rotor does. How the cord grips the skin and how the fingers open and close are not simulated.',
    hand: "Hand-driven: a rigid prop goes where the hand takes it, so this traces the move's defined path on a jointed figure. Grip and finger work are not shown.",
    still: 'A still picture. The view buttons turn it.',
  };
  // The canvas is a picture for a screen reader. Its name is its own label (which clip, seen from where)
  // followed by the caption under it, so what the page says of the clip is said of the picture too.
  function html(cid, id, dominant, esc) {
    const key = cid + ':' + id;
    if (st.id !== key) { st.id = key; st.variant = 0; st.phase = 0; st.hand = dominant; }
    const e = index[key], v = variant(), pic = v.m === 'still';
    return `<div class="demo">
      <canvas id="demo-cv" width="720" height="720" role="img" aria-label="${pic ? 'Picture' : 'Demonstration'}: ${esc(v.n)}, ${viewName()} view. The steps below describe it in text." aria-labelledby="demo-cv demo-cap${unfit(v.f) ? ' demo-body' : ''}"></canvas>
      ${e.v.length > 1 ? `<div class="chips demo-var">${e.v.map((s, i) => `<button class="chip ${i === st.variant ? 'on' : ''}" data-act="demo" data-k="variant" data-v="${i}" aria-pressed="${i === st.variant}">${esc(s.n)}</button>`).join('')}</div>` : ''}
      <div class="demo-ctl">
        <div class="seg" role="group" aria-label="Camera view">${segBtn('view', 'angle', '3/4', st.view === 'angle', 'Three-quarter view')}${segBtn('view', 'front', 'Front', st.view === 'front')}${segBtn('view', 'side', 'Side', st.view === 'side')}</div>
        ${v.one ? `<div class="seg" role="group" aria-label="Hand">${segBtn('hand', 'L', 'L', st.hand === 'L', 'Left hand')}${segBtn('hand', 'R', 'R', st.hand === 'R', 'Right hand')}</div>` : ''}
        ${pic ? '' : `<div class="seg" role="group" aria-label="Speed">${[[1, '1×', 'Real speed'], [0.5, '½', 'Half speed'], [0.25, '¼', 'Quarter speed']].map(([x, l, a]) => segBtn('speed', x, l, st.speed === x, a)).join('')}</div>
        <button class="btn sm" data-act="demo" data-k="play">${st.playing ? 'Pause' : 'Play'}</button>
        <button class="btn sm" data-act="demo" data-k="gif" data-tip="Download this clip as a GIF">GIF</button>`}
      </div>
      <p class="tiny demo-cap" id="demo-cap">${esc(CAPTION[v.m] || '')} ${esc(v.note || e.note || '')}</p>
      ${unfit(v.f) ? `<p class="tiny demo-body" id="demo-body">Shown on the standard body: on your ${esc(presetOf(av).replace('-', ', '))} avatar this clip does not pass the body checks yet (${esc(unfit(v.f))}).</p>` : ''}
    </div>`;
  }
  function act(d) {
    if (d.k === 'play') st.playing = !st.playing;
    else if (d.k === 'gif') download();
    else if (d.k === 'variant') { st.variant = Number(d.v); st.phase = 0; }
    else if (d.k === 'speed') st.speed = Number(d.v);
    else st[d.k] = d.v;
  }

  /* ---------- combos: each skill's demo, one after another ---------- */
  // The combo builder strings skills together. This plays their demos in that order on one
  // figure: every skill runs for whole cycles of its move, as many times as the combo asks.
  // A skill with no demo gets a card with its name. The links between skills are not
  // animated: each demo starts from its own position.
  const cs = { sig: '', steps: [], i: 0, t: 0, playing: !still, hand: 'R' };
  let craf = 0, clast = 0;
  const CARD = 1.6, PICTURE = 2.4; // seconds a name card or a still picture stays up
  // What the combo page needs to know about a skill's demo before any clip is loaded.
  const info = (cid, sid) => { const v = index[cid + ':' + sid]?.v[0]; return v ? { has: true, still: v.m === 'still', d: v.d || 1, n: v.n } : { has: false }; };
  const stepSecs = x => !x.v ? CARD : x.v.m === 'still' ? PICTURE : (x.v.d || 1) * x.reps;
  const comboSecs = (cid, seq, reps) => seq.reduce((t, sid, i) => t + stepSecs({ v: index[cid + ':' + sid]?.v[0], reps: reps[i] || 1 }), 0);
  const comboClip = step => step.v ? fetchClip(step.v.f, comboMount) : null;
  const clock = t => `${Math.floor(t / 60)}:${String(Math.floor(t % 60)).padStart(2, '0')}`;
  function comboPaint() {
    const cv = document.getElementById('combo-cv'), step = cs.steps[cs.i];
    if (!cv || !step) return false;
    const ctx = cv.getContext('2d'), W = cv.width, col = tint(colors(), cs.sig.split('|')[1]), clip = comboClip(step), d = step.v && step.v.d || 1;
    if (clip) draw(ctx, W, clip, clip.still ? 0 : clip.loop ? cs.t / d : Math.min((cs.t % d) / d, 0.999), st.view, step.v.one && cs.hand === 'L', col, false);
    else {
      ctx.fillStyle = col.bg; ctx.fillRect(0, 0, W, W); ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillStyle = col.ink; ctx.font = `600 ${W / 14}px system-ui, sans-serif`; ctx.fillText(step.sid, W / 2, W * 0.43);
      ctx.fillStyle = col.link; ctx.font = `${W / 26}px system-ui, sans-serif`; ctx.fillText(step.name.length > 42 ? step.name.slice(0, 40) + '…' : step.name, W / 2, W * 0.53);
      ctx.fillText(step.v ? (clip === undefined ? 'Loading the demo…' : 'This demo could not be loaded.') : 'No demo for this skill yet', W / 2, W * 0.61);
    }
    // Keep the timeline, the progress bar and the caption in step without redrawing the page.
    const secs = stepSecs(step), before = cs.steps.slice(0, cs.i).reduce((t, x) => t + stepSecs(x), 0), total = cs.steps.reduce((t, x) => t + stepSecs(x), 0);
    document.querySelectorAll('.cb-step').forEach(r => r.classList.toggle('playing', Number(r.dataset.step) === cs.i));
    document.querySelectorAll('#combo-prog i').forEach((sgm, i) => { sgm.firstChild.style.width = (i < cs.i ? 100 : i > cs.i ? 0 : Math.min(cs.t / secs, 1) * 100) + '%'; });
    const now = document.getElementById('combo-now'), time = document.getElementById('combo-time');
    if (now) now.textContent = `${step.sid} ${step.name}${step.v && step.v.m !== 'still' && step.reps > 1 ? ` · ${Math.min(Math.floor(cs.t / d) + 1, step.reps)} of ${step.reps}` : ''}`;
    if (time) time.textContent = `${clock(before + Math.min(cs.t, secs))} / ${clock(total)}`;
    return true;
  }
  function comboTick(ts) {
    craf = 0;
    const step = cs.steps[cs.i];
    if (!step || !document.getElementById('combo-cv')) return;
    if (cs.playing) {
      cs.t += Math.min(ts - clast, 100) / 1000 * st.speed;
      if (cs.t >= stepSecs(step)) { cs.i = (cs.i + 1) % cs.steps.length; cs.t = 0; }
    }
    clast = ts;
    comboPaint();
    if (cs.playing) craf = requestAnimationFrame(comboTick);
  }
  function comboMount() {
    if (!comboPaint()) return;
    if (cs.playing && !craf) { clast = performance.now(); craf = requestAnimationFrame(comboTick); }
  }
  // The stage of the open combo. seq: skill ids in order; reps: how many cycles each plays; nameOf: id -> name;
  // foot: what the page puts under the player (the buttons that log a run).
  function comboStage(id, cid, seq, reps, nameOf, dominant, esc, foot = '') {
    const sig = id + '|' + cid + '|' + seq.join(','), same = cs.sig === sig;
    const steps = seq.map((sid, i) => ({ sid, name: nameOf(sid), v: index[cid + ':' + sid]?.v[0], reps: reps[i] || 1 }));
    if (!same) Object.assign(cs, { sig, i: cs.sig.split('|')[0] === id ? Math.min(cs.i, Math.max(seq.length - 1, 0)) : 0, t: 0, hand: dominant });
    cs.steps = steps;
    if (!steps.length) return `<div class="cb-stage"><div class="cb-empty"><div><b>Nothing to play yet</b><span>Add a skill and it plays here.</span></div></div>${foot}</div>`;
    const missing = steps.filter(x => !x.v).length;
    return `<div class="cb-stage">
      <canvas id="combo-cv" width="720" height="720" role="img" aria-label="The combo's skills, demonstrated one after another, ${viewName()} view. Now:" aria-labelledby="combo-cv combo-now combo-cap"></canvas>
      <div class="cb-prog" id="combo-prog" aria-hidden="true">${steps.map(x => `<i style="flex-grow:${stepSecs(x)}"><b></b></i>`).join('')}</div>
      <div class="cb-now"><span id="combo-now" aria-live="off"></span><span id="combo-time" class="num"></span></div>
      <div class="demo-ctl">
        <button class="btn sm primary" data-act="combo-demo" data-k="play" style="min-width:64px">${cs.playing ? 'Pause' : 'Play'}</button>
        <button class="btn sm" data-act="combo-demo" data-k="restart">Restart</button>
        <span class="ctl-l"><span class="tiny" aria-hidden="true">Speed</span><div class="seg" role="group" aria-label="Speed">${[[1, '1×', 'Real speed'], [0.5, '½', 'Half speed'], [0.25, '¼', 'Quarter speed']].map(([x, l, a]) => segBtn('speed', x, l, st.speed === x, a, 'combo-demo')).join('')}</div></span>
        <span class="ctl-l"><span class="tiny" aria-hidden="true">View</span><div class="seg" role="group" aria-label="Camera view">${segBtn('view', 'angle', '3/4', st.view === 'angle', 'Three-quarter view', 'combo-demo')}${segBtn('view', 'front', 'Front', st.view === 'front', '', 'combo-demo')}${segBtn('view', 'side', 'Side', st.view === 'side', '', 'combo-demo')}</div></span>
      </div>
      <p class="tiny" id="combo-cap">Each skill plays whole cycles of its demo, as many as you set. The links between skills are not animated.${missing ? ` ${missing} ${missing === 1 ? 'skill has' : 'skills have'} no demo yet and ${missing === 1 ? 'shows' : 'show'} as a name card.` : ''}</p>
      ${foot}
    </div>`;
  }
  function comboAct(d) {
    if (d.k === 'play') cs.playing = !cs.playing;
    else if (d.k === 'restart') { cs.i = 0; cs.t = 0; }
    else if (d.k === 'step') { cs.i = Number(d.v); cs.t = 0; }
    else if (d.k === 'speed') st.speed = Number(d.v);
    else st[d.k] = d.v;
  }

  /* ---------- a small stage: the avatar editor's preview and someone's profile ---------- */
  // Plays one clip (or the figure standing) in a canvas, at a yaw the page can turn with a drag,
  // in a given person's avatar.
  const STAND = { fr: [{}], props: [], dur: 1, still: true };
  const pv = { cv: null, key: '', yaw: 0.5, t: 0, raf: 0, last: 0, who: undefined, playing: true };
  function preview(cv, key, opts = {}) {
    Object.assign(pv, { cv, key: key || '', who: 'who' in opts ? opts.who : undefined });
    if (opts.yaw != null) pv.yaw = opts.yaw;
    if (!pv.raf && cv) { pv.last = performance.now(); pv.raf = requestAnimationFrame(previewTick); }
  }
  // The first clip of a prop that moves, for the editor's preview.
  const firstClip = cid => Object.keys(index).find(k => k.startsWith(cid + ':') && index[k].v[0].m !== 'still') || '';
  function previewTick(ts) {
    pv.raf = 0;
    if (!pv.cv || !pv.cv.isConnected) { pv.cv = null; return; }
    const v = pv.key && index[pv.key]?.v[0], clip = (v && fetchClip(v.f, () => {})) || STAND, who = pv.who === undefined ? av : pv.who;
    if (pv.playing) pv.t += Math.min(ts - pv.last, 100) / 1000 * 0.5;
    pv.last = ts;
    draw(pv.cv.getContext('2d'), pv.cv.width, clip, clip.still ? 0 : (pv.t / clip.dur) % 1, pv.yaw, false, tint(colors(), pv.key.split(':')[0], who), false, who);
    pv.raf = requestAnimationFrame(previewTick);
  }

  /* ---------- stickers ---------- */
  // Poses for stickers, as rig poses on the standard body (test/stickers.js puts each through the
  // body checks on every preset), and frames of a few signature moves, which the physics suite checks.
  const STICKERS = {
    celebrate: { name: 'Celebrate', pose: { hL: [0.3, 1.93, 0.06], hR: [-0.3, 1.93, 0.06], eL: [0.55, 1.62, 0], eR: [-0.55, 1.62, 0] } },
    salute: { name: 'Salute', pose: { hR: [-0.08, 1.7, 0.12], eR: [-0.42, 1.52, 0.08] } },
    thinking: { name: 'Thinking', pose: { hR: [-0.03, 1.52, 0.22], eR: [-0.14, 1.24, 0.32], hL: [-0.06, 1.14, 0.36], eL: [0.25, 1.08, 0.26] } },
    wave: { name: 'Wave', pose: { hR: [-0.42, 1.78, 0.08], eR: [-0.5, 1.45, 0] } },
    flex: { name: 'Strong', pose: { hL: [0.32, 1.62, 0.04], hR: [-0.32, 1.62, 0.04], eL: [0.5, 1.38, -0.02], eR: [-0.5, 1.38, -0.02] } },
  };
  const SIGNATURE = [['nunchaku:F5', 'Upspin'], ['poi:B1', 'Butterfly'], ['staff:F4', 'One direction'], ['buugeng:F3', 'Wrist spin'], ['doublestaff:F5', 'Figure 8']];
  // A sticker as a canvas with a transparent background, or null where the 3D figure is not ready.
  function sticker(pose, W = 256, who = av) {
    if (typeof document === 'undefined' || !use3d()) return null;
    const cv = document.createElement('canvas'); cv.width = cv.height = W;
    const col = { ...colors(), bg: 'rgba(0,0,0,0)', floor: 'rgba(0,0,0,0)' };
    const st0 = STICKERS[pose];
    if (st0) draw(cv.getContext('2d'), W, { ...STAND, fr: [st0.pose] }, 0, 0.35, false, col, true, who);
    else { const v = index[pose]?.v[0], clip = v && cache.get(v.f); if (!clip) return null; draw(cv.getContext('2d'), W, clip, 0, 0.35, false, tint(col, pose.split(':')[0], who), true, who); }
    return cv;
  }
  // The head and shoulders, for a profile picture (JPEG data URL).
  function headshot(who = av, W = 256) {
    if (typeof document === 'undefined' || !use3d()) return '';
    const cv = document.createElement('canvas'); cv.width = cv.height = W;
    const ctx = cv.getContext('2d'); ctx.fillStyle = '#c9d4e8'; ctx.fillRect(0, 0, W, W);
    draw(ctx, W, { ...STAND, cam: { zoom: 3.4, at: [0, 1.58, 0] } }, 0, 0.25, false, { ...colors(), bg: 'rgba(0,0,0,0)', floor: 'rgba(0,0,0,0)' }, true, who);
    return cv.toDataURL('image/jpeg', 0.88);
  }

  /* ---------- GIF export ---------- */
  function lzw(minCode, px) {
    const out = []; let cur = 0, bits = 0;
    const emit = (c, size) => { cur |= c << bits; bits += size; while (bits >= 8) { out.push(cur & 255); cur >>>= 8; bits -= 8; } };
    const clear = 1 << minCode, eoi = clear + 1;
    let size = minCode + 1, next = eoi + 1, dict = new Map(), prefix = px[0];
    emit(clear, size);
    for (let i = 1; i < px.length; i++) {
      const k = px[i], key = prefix << 8 | k, hit = dict.get(key);
      if (hit !== undefined) { prefix = hit; continue; }
      emit(prefix, size);
      if (next < 4096) { dict.set(key, next++); if (next > (1 << size) && size < 12) size++; }
      else { emit(clear, size); dict = new Map(); next = eoi + 1; size = minCode + 1; }
      prefix = k;
    }
    emit(prefix, size); emit(eoi, size);
    if (bits) out.push(cur & 255);
    return out;
  }
  function gif(clip, view, mir, col, W = 360) {
    const cv = document.createElement('canvas'); cv.width = cv.height = W;
    const ctx = cv.getContext('2d', { willReadFrequently: true });
    const secs = clip.dur / st.speed, n = Math.min(Math.round(secs * 20), 90), delay = Math.max(2, Math.round(secs * 100 / n));
    const pal = [], seen = new Map(), nearest = new Map(), frames = [];
    for (let f = 0; f < n; f++) {
      draw(ctx, W, clip, f / n, view, mir, col, false);
      const d = ctx.getImageData(0, 0, W, W).data, px = new Uint8Array(W * W);
      for (let i = 0, j = 0; j < px.length; i += 4, j++) {
        const key = (d[i] >> 3) << 10 | (d[i + 1] >> 3) << 5 | d[i + 2] >> 3;
        let c = seen.get(key);
        if (c === undefined) {
          if (pal.length < 256) { c = pal.length; pal.push([d[i], d[i + 1], d[i + 2]]); seen.set(key, c); }
          else if ((c = nearest.get(key)) === undefined) {
            let best = 1e9;
            for (let q = 0; q < 256; q++) { const e = (pal[q][0] - d[i]) ** 2 + (pal[q][1] - d[i + 1]) ** 2 + (pal[q][2] - d[i + 2]) ** 2; if (e < best) { best = e; c = q; } }
            nearest.set(key, c);
          }
        }
        px[j] = c;
      }
      frames.push(px);
    }
    const out = [...'GIF89a'].map(c => c.charCodeAt(0));
    out.push(W & 255, W >> 8, W & 255, W >> 8, 0xF7, 0, 0);
    for (let i = 0; i < 256; i++) out.push(...(pal[i] || [0, 0, 0]));
    out.push(0x21, 0xFF, 11, ...[...'NETSCAPE2.0'].map(c => c.charCodeAt(0)), 3, 1, 0, 0, 0);
    for (const px of frames) {
      out.push(0x21, 0xF9, 4, 0, delay, 0, 0, 0, 0x2C, 0, 0, 0, 0, W & 255, W >> 8, W & 255, W >> 8, 0, 8);
      const data = lzw(8, px);
      for (let i = 0; i < data.length; i += 255) { const chunk = data.slice(i, i + 255); out.push(chunk.length, ...chunk); }
      out.push(0);
    }
    out.push(0x3B);
    return new Uint8Array(out);
  }
  function download() {
    const v = variant(), clip = v && cache.get(v.f);
    if (!clip || clip.still) return;
    const url = URL.createObjectURL(new Blob([gif(clip, st.view, v.one && st.hand === 'L', tint(colors(), st.id.split(':')[0]))], { type: 'image/gif' }));
    const a = document.createElement('a');
    a.href = url; a.download = `flowa-${st.id.replace(':', '-')}-${v.n.toLowerCase().replace(/[^a-z0-9]+/g, '-')}-${viewName()}.gif`; a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  return { init, has, figure, html, mount, act, comboStage, comboMount, comboAct, comboSecs, info, clock, combo: cs, gif, draw, colors, state: st, rig, caps, bend, parts, pose, pack, unpack, mirror, sPoints, propsOf, stage,
    index: () => index, clip: () => { const v = variant(); return v && cache.get(v.f); },
    v: { add, sub, mul, dot, cross, len, norm, mix, rot, clamp, smooth, flip }, dim, setBody, fit, lift, body: BODY, HEIGHTS, BUILDS, FRAMES, avatar, presetOf, preview, previewState: pv, firstClip, STICKERS, SIGNATURE, sticker, headshot, wear, fetchClip, ready3d: () => use3d(), firstVariant: k => index[k]?.v[0]?.f, cached: k => { const f = index[k]?.v[0]?.f; return !!(f && cache.get(f)); } };
})();
if (typeof window !== 'undefined') window.Demo = Demo;
