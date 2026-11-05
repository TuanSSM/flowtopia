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
  const UP = [0, 1, 0], U_ARM = 0.31, F_ARM = 0.28, THIGH = 0.47, SHIN = 0.465, REACH = U_ARM + F_ARM;
  const DIM = { pelvis: 0.97, chest: 0.46, head: 0.66, shoulder: 0.2, hip: 0.1, stance: 0.14, ankle: 0.04 };
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
      const hand = p['h' + s] || add(pelvis, add(mul(side, 0.24 * k), add([0, -0.04, 0], mul(face, -0.04))));
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
  // The figure as capsules (a line with a radius), for contact. An arm that holds a prop
  // stops short of the fist, which is wrapped around it.
  const cap = (a, b, r, name, side) => ({ a, b, r, name, side });
  function caps(J, hold = {}) {
    const out = [cap(J.head, J.head, 0.105, 'head'), cap(add(J.pelvis, mul(J.up, 0.08)), add(J.pelvis, mul(J.up, 0.41)), 0.135, 'torso')];
    for (const s of ['L', 'R']) {
      const el = J['el' + s], hand = J['h' + s];
      out.push(cap(J['hip' + s], J['knee' + s], 0.058, 'thigh', s), cap(J['knee' + s], J['f' + s], 0.058, 'shin', s),
        cap(J['sh' + s], el, 0.042, 'upper arm', s), cap(el, hold[s] ? add(hand, mul(norm(sub(el, hand)), 0.07)) : hand, 0.038, 'forearm', s));
    }
    return out;
  }

  /* ---------- props ---------- */
  const GRIP = 0.12, BUTT = 0.18; // nunchaku: fist to link end, fist to tail end of the handle
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
    const n = mul(cross(x, w), pr.hand === 'L' ? -1 : 1), r = pr.r || 0.014, rho = r + 0.01, segs = [];
    const round = (a, off) => add(add(o, mul(x, off)), add(mul(n, -rho * cos(a)), mul(w, rho * sin(a)))); // a point around the stick
    const wrist = add(o, add(mul(w, -0.08), mul(n, -(r + 0.02)))), knuckles = add(o, add(mul(w, 0.012), mul(n, -(r + 0.021))));
    segs.push([[add(wrist, mul(w, -0.2)), wrist], 0.058, 'skin'], [[wrist, knuckles], 0.078, 'skin']);
    for (let i = 0; i < 4; i++) {
      const off = 0.031 - i * 0.021, reach = 0.088 - Math.abs(i - 1) * 0.008;
      if (pr.grip === 'open') { const k = add(knuckles, mul(x, off)); segs.push([[k, add(k, add(mul(w, reach * 0.55), mul(n, 0.004))), add(k, add(mul(w, reach), mul(n, 0.016)))], 0.017, 'skin']); }
      else segs.push([Array.from({ length: 9 }, (_, j) => round(0.3 + j / 8 * reach / rho, off)), 0.017, 'skin']);
    }
    // The thumb comes round the other way and closes over the stick (or over the first finger).
    const base = add(add(o, mul(x, 0.05)), add(mul(w, -0.05), mul(n, -(r + 0.006))));
    segs.push([[base, round(-1.9, 0.044), round(-2.9, 0.03), round(-3.6, 0.016)], 0.021, 'skin']);
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
    return mir ? mirror(f) : f;
  }
  const other = s => s === 'L' ? 'R' : 'L';
  const propsOf = (clip, mir) => mir ? clip.props.map(p => p.hand ? { ...p, hand: other(p.hand) } : p) : clip.props;

  /* ---------- drawing ---------- */
  function draw(ctx, W, clip, ph, view, mir, col, full) {
    const cam = clip.cam, zoom = cam ? cam.zoom : 1, S = W / 2.75 * zoom, base = W * 0.93;
    // Yaw turns the camera toward the working hand's side: 0 is front, 90 degrees is side.
    const yaw = { front: 0, angle: 0.66, side: Math.PI / 2 }[view], tilt = view === 'angle' ? 0.2 : 0;
    const cy = cos(yaw), sy = sin(yaw), k = mir ? -1 : 1;
    const flat = v => { const depth = v[2] * cy - k * v[0] * sy; return [v[0] * cy + k * (v[2] - 0.3 * sy) * sy, v[1] * cos(tilt) - depth * sin(tilt), depth]; };
    const aim = cam ? flat(mir ? flip(cam.at) : cam.at) : null;
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
      const corner = (i, j) => { const v = add(q.c, add(mul(q.a, i * q.k), mul(q.b, j * q.k))); return pr(mir ? flip(v) : v); };
      const c = [corner(1, 1), corner(-1, 1), corner(-1, -1), corner(1, -1)];
      ctx.beginPath(); c.forEach((v, i) => i ? ctx.lineTo(v[0], v[1]) : ctx.moveTo(v[0], v[1])); ctx.closePath();
      ctx.fillStyle = col.plane; ctx.fill(); ctx.strokeStyle = col.prop; ctx.globalAlpha = 0.5; ctx.lineWidth = W / 240; ctx.stroke(); ctx.globalAlpha = 1;
    }
    const f = pose(clip, ph, mir), props = propsOf(clip, mir), busy = {};
    for (const p of props) if (p.hand) busy[p.hand] = true;
    if (!clip.hide) {
      const J = rig(f);
      for (const s of ['L', 'R']) {
        const foot = J['f' + s];
        seg([J['hip' + s], J['knee' + s], foot], 0.115, col.body);
        seg([foot, add(foot, add(mul(J['t' + s], 0.15), [0, -0.02, 0]))], 0.075, col.body);
        seg([J['sh' + s], J['el' + s], J['h' + s]], 0.078, busy[s] ? col.arm : col.body, busy[s] ? 0.02 : 0);
        ball(J['h' + s], 0.052, busy[s] ? col.arm : col.body, busy[s] ? 0.07 : 0.03);
      }
      const hipL = add(J.pelvis, mul(sub(J.hipL, J.pelvis), 1.25)), hipR = add(J.pelvis, mul(sub(J.hipR, J.pelvis), 1.25));
      fill([J.shL, J.shR, hipR, hipL], col.body);
      seg([J.shL, J.shR], 0.1, col.body); seg([hipL, hipR], 0.13, col.body);
      seg([add(J.pelvis, mul(J.up, 0.04)), add(J.pelvis, mul(J.up, 0.4))], 0.2, col.body);
      seg([J.chest, J.head], 0.07, col.body);
      ball(J.head, 0.105, col.body, 0.01);
      ball(J.nose, 0.024, col.body, dot(J.face, [-k * sy, 0, cy]) > 0 ? 0.03 : -0.2);
    }
    const tips = [];
    props.forEach((p, i) => {
      const pt = parts(p, i, f);
      for (const [pts, wd, role] of pt.segs) seg(pts, wd, col[role], 0.05);
      for (const [c, r, role] of pt.balls) ball(c, r, col[role], 0.06);
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
      for (const [w, c] of [[it.w + W / 90, col.bg], [it.w, it.color]]) {
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
  }
  const has = (cid, id) => !!index[cid + ':' + id];
  const variant = () => index[st.id]?.v[st.variant];
  // The clip for the variant on show: undefined while it loads, null if it cannot be loaded.
  function clipNow() {
    const v = variant();
    if (!v) return null;
    if (!cache.has(v.f)) {
      cache.set(v.f, undefined);
      fetch(`clips/${v.f}.json`).then(r => r.ok ? r.json() : Promise.reject(new Error(r.status))).then(c => { cache.set(v.f, unpack(c)); mount(); }, () => { cache.set(v.f, null); paint(); });
    }
    return cache.get(v.f);
  }
  let raf = 0, last = 0;
  function paint() {
    const cv = document.getElementById('demo-cv'), v = variant();
    if (!cv || !v) return false;
    const ctx = cv.getContext('2d'), clip = clipNow();
    if (clip) { draw(ctx, cv.width, clip, st.phase, st.view, v.one && st.hand === 'L', colors(), !st.playing); return true; }
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

  const segBtn = (k, v, label, on, aria) => `<button class="${on ? 'on' : ''}" data-act="demo" data-k="${k}" data-v="${v}" aria-pressed="${on}"${aria ? ` aria-label="${aria}"` : ''}>${label}</button>`;
  const viewName = () => st.view === 'angle' ? 'three-quarter' : st.view;
  const CAPTION = {
    sim: 'Simulated: the prop moves by gravity, air drag, tension in its cord or tether, and contact with the body. The hands follow a model of the move, not motion capture.',
    flight: 'Simulated: once released, the prop keeps its spin and its centre follows a ballistic arc. The catching hand arrives moving with it.',
    solved: 'Solved: the head follows the path the move is defined by, and the hand is placed exactly where gravity and tether tension produce that path.',
    hand: "Hand-driven: a rigid prop goes where the hand takes it, so this traces the move's defined path on a jointed figure. Grip and finger work are not shown.",
    still: 'A still picture. The view buttons turn it.',
  };
  function html(cid, id, dominant, esc) {
    const key = cid + ':' + id;
    if (st.id !== key) { st.id = key; st.variant = 0; st.phase = 0; st.hand = dominant; }
    const e = index[key], v = variant(), pic = v.m === 'still';
    return `<div class="demo">
      <canvas id="demo-cv" width="720" height="720" role="img" aria-label="${pic ? 'Picture' : 'Demonstration'}: ${esc(v.n)}, ${viewName()} view. The steps below describe it in text."></canvas>
      ${e.v.length > 1 ? `<div class="chips demo-var">${e.v.map((s, i) => `<button class="chip ${i === st.variant ? 'on' : ''}" data-act="demo" data-k="variant" data-v="${i}" aria-pressed="${i === st.variant}">${esc(s.n)}</button>`).join('')}</div>` : ''}
      <div class="demo-ctl">
        <div class="seg" role="group" aria-label="Camera view">${segBtn('view', 'angle', '3/4', st.view === 'angle', 'Three-quarter view')}${segBtn('view', 'front', 'Front', st.view === 'front')}${segBtn('view', 'side', 'Side', st.view === 'side')}</div>
        ${v.one ? `<div class="seg" role="group" aria-label="Hand">${segBtn('hand', 'L', 'L', st.hand === 'L', 'Left hand')}${segBtn('hand', 'R', 'R', st.hand === 'R', 'Right hand')}</div>` : ''}
        ${pic ? '' : `<div class="seg" role="group" aria-label="Speed">${[[1, '1×', 'Real speed'], [0.5, '½', 'Half speed'], [0.25, '¼', 'Quarter speed']].map(([x, l, a]) => segBtn('speed', x, l, st.speed === x, a)).join('')}</div>
        <button class="btn sm" data-act="demo" data-k="play">${st.playing ? 'Pause' : 'Play'}</button>
        <button class="btn sm" data-act="demo" data-k="gif" data-tip="Download this clip as a GIF">GIF</button>`}
      </div>
      <p class="tiny">${esc(CAPTION[v.m] || '')} ${esc(v.note || e.note || '')}</p>
    </div>`;
  }
  function act(d) {
    if (d.k === 'play') st.playing = !st.playing;
    else if (d.k === 'gif') download();
    else if (d.k === 'variant') { st.variant = Number(d.v); st.phase = 0; }
    else if (d.k === 'speed') st.speed = Number(d.v);
    else st[d.k] = d.v;
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
    const url = URL.createObjectURL(new Blob([gif(clip, st.view, v.one && st.hand === 'L', colors())], { type: 'image/gif' }));
    const a = document.createElement('a');
    a.href = url; a.download = `flowa-${st.id.replace(':', '-')}-${v.n.toLowerCase().replace(/[^a-z0-9]+/g, '-')}-${viewName()}.gif`; a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  return { init, has, html, mount, act, gif, draw, colors, state: st, rig, caps, bend, parts, pose, pack, unpack, mirror, sPoints,
    index: () => index, clip: () => { const v = variant(); return v && cache.get(v.f); },
    v: { add, sub, mul, dot, cross, len, norm, mix, rot, clamp, smooth, flip }, dim: { ...DIM, U_ARM, F_ARM, REACH, THIGH, SHIN, GRIP, BUTT } };
})();
if (typeof window !== 'undefined') window.Demo = Demo;
