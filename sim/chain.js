'use strict';
// A tether or a cord as a chain of points that can touch the body. The engine's own tether is
// one stiff link from the hand to the head, which is all a spin needs; a wrap needs the line
// itself to lie on an arm and grip it. This file is that line. It runs at build time only.
//
// The method is position-based dynamics with small steps (XPBD). Every engine step is cut into
// SUBSTEPS; in each one the points move under gravity and drag, the link lengths are put right,
// then the points are moved out of the capsules and friction acts along the surface:
//   links     A Newton step on all the lengths at once. In a chain each link shares a point only
//             with its two neighbours, so the equations are tridiagonal and are solved exactly
//             in one sweep. A light tether with a heavy head on it therefore does not stretch,
//             as it would if the links were corrected one after another. The points are moved
//             along the directions the links had when the substep began (as SHAKE does), not the
//             ones they end with (as plain position-based dynamics does): the second way loses
//             energy in proportion to the substep, 0.47% a second on a 60 degree swing at 1/6000 s;
//             the first loses none (test/chain.js measures 0.03%, going up and down).
//   contact   A point inside a capsule is moved out along the shortest way (the capsule's side
//             or its round end: the same rule, so there is no seam between them).
//   friction  Coulomb: what the point moved along the surface, measured against the surface's
//             own motion at that spot, is taken back in full while it is under mu times the
//             distance the point was pushed out (it sticks), and by that much when over (it slides).
// Everything is fixed: step, substeps, sweeps, the order of points and capsules. There are no
// random numbers and no object is read by its keys' order, so the same input gives the same bytes.
// The chain is good only up to a pull it can name (see SUBSTEPS). Past it, step() throws, and the
// chain is to be thrown away: it never hands back a state worked out beyond that pull.
const { sqrt, min, max, atan2, PI } = Math;

// Link length. Between two points the line is straight, so on a round part it cuts inside by
// link^2 / (8 * (part radius + line radius)). The thinnest part of the body the engine passes is
// the shin above the ankle (45 mm), where 10 mm links cut in 0.26 mm; on a forearm (50 mm) it is
// 0.23 mm. A nunchaku handle (22.5 mm) is not passed yet: a cord does not meet its own. There it
// would be 0.47 mm, and test/chain.js throws lines at a part that thin.
const LINK_LEN = 0.01;
// Substeps per engine step (1/1500 s), so 1/6000 s each. Three things hang on it:
//   give at rest     Links and contact are solved one after the other, so a load hanging over a
//                    part stretches the line by about g * h^2 * (load / link mass) * (wrap angle *
//                    link angle). For a 90 g head on 0.18 g links half round a forearm that is
//                    0.08 mm at 1/6000 s. Three substeps would give 0.14 mm.
//   the limit        The points of a taut line shake sideways at up to 2 * sqrt(tension / (point
//                    mass * link)), and the step follows that only while it is under 2 / substep.
//                    So a line is good up to point mass * link / substep^2 (limit, below): 66 N for
//                    a 10 g tether, 225 N for a 5 g cord; a spinning poi pulls about 5 N. A substep
//                    in which any link is pulled harder, or is not a number, throws. A stiff line
//                    snapping taut can pass the limit for a substep without harm (a 90 g head at
//                    over 0.12 m/s does), but that cannot be told from the start of a runaway,
//                    which puts a link through a part within a substep or two, so both stop. More
//                    substeps raise the limit, and a line given some stretch (compliance) takes a
//                    snap as a spring does, over many substeps.
//   passing through  A point pushed out along the shortest way comes out on the side it went in
//                    while it is less than the part's radius deep. At 9 m/s, the fastest a hand may
//                    move, a point travels 1.5 mm a substep and a line thrown across a part goes at
//                    most 3.8 mm in, friction 0 to 0.6, and never passes the limit. Faster than
//                    that a thrown line can pass the limit (in one throw of twelve at 16.9 m/s,
//                    in all twelve at 25.9 m/s) and is stopped. That is what keeps a line out of
//                    a part: the bound while the pull is under the limit, the limit when it is
//                    not. See test/chain.js, "too fast to stop?".
const SUBSTEPS = 4;
// Newton steps on the lengths per substep. One: the substeps are the iterations. What a second would
// remove is (how far a point is drawn in)^2 / (2 * link): 0.001 mm a link at 5 N on a forearm, a
// twenty-sixth of the give above. A correction large enough for that to matter is a pull past the
// limit, which stops the chain anyway. Two were tried: every check in test/chain.js comes out the same.
const SWEEPS = 1;
const THICK = 0.004;   // half the thickness of a tether or cord, the same 4 mm the body checks use
const GRAVITY = [0, -9.81, 0];
const links = length => Math.max(1, Math.round(length / LINK_LEN));

// o.points      where each point starts, [x, y, z] in metres, the held end first
// o.masses      kg for each point; Infinity for a point that is held (hold() says where it goes)
// o.length      rest length of the whole chain (default: as laid out); o.compliance: metres of stretch
//               per newton over the whole chain (default 0: it does not stretch). A chain held at both
//               ends must be given some, or its length has no answer.
// o.radius      half thickness, one number or one per point; a negative radius touches nothing
// o.mu, o.muSlide   friction while sticking and while sliding (default the same); a capsule may carry its own mu
// o.velocities, o.acc   starting velocities; the acceleration of each point (default gravity)
// o.rod         { points, inner, a, b, mass }: a stick on the far end, as the engine models one (two masses
//               at `points`, `inner` apart; the line is tied to the spot a * first + b * second). The chain then
//               has o.points.length links, and the last one ends on that spot.
function makeChain(o) {
  const rod = o.rod || null, n = rod ? o.points.length : o.points.length - 1, NP = n + (rod ? 2 : 1), NC = n + (rod ? 1 : 0);
  const K = o.substeps || SUBSTEPS, last = rod ? n - 1 : n; // last: the last point that is part of the line
  const x = new Float64Array(3 * NP), xo = new Float64Array(3 * NP), v = new Float64Array(3 * NP), goal = new Float64Array(3 * NP), acc = new Float64Array(3 * NP);
  const w = new Float64Array(NP), mass = new Float64Array(NP), rad = new Float64Array(NP);
  const pts = rod ? [...o.points, ...o.rod.points] : o.points, ms = rod ? [...o.masses, o.rod.mass / 2, o.rod.mass / 2] : o.masses;
  for (let i = 0; i < NP; i++) {
    for (let k = 0; k < 3; k++) { x[3 * i + k] = goal[3 * i + k] = pts[i][k]; v[3 * i + k] = o.velocities ? o.velocities[i][k] : 0; acc[3 * i + k] = o.acc ? o.acc[i][k] : GRAVITY[k]; }
    mass[i] = ms[i]; w[i] = Number.isFinite(ms[i]) ? 1 / ms[i] : 0;
    rad[i] = i > last ? -1 : typeof o.radius === 'number' ? o.radius : o.radius ? o.radius[i] : THICK;
  }
  const ra = rod ? rod.a : 0, rb = rod ? rod.b : 0;
  // Where link j ends: the next point, or the spot on the rod.
  const endOf = (j, k) => rod && j === n - 1 ? ra * x[3 * n + k] + rb * x[3 * n + 3 + k] : x[3 * j + 3 + k];
  let laid = 0;
  for (let j = 0; j < n; j++) laid += Math.hypot(endOf(j, 0) - x[3 * j], endOf(j, 1) - x[3 * j + 1], endOf(j, 2) - x[3 * j + 2]);
  const rest = new Float64Array(NC).fill((o.length || laid) / n), soft = new Float64Array(NC).fill((o.compliance || 0) / n);
  if (rod) { rest[n] = rod.inner; soft[n] = 0; }
  const mu = o.mu || 0, muSlide = o.muSlide === undefined ? mu : o.muSlide;
  const lam = new Float64Array(NC), dir = new Float64Array(3 * NC), dir0 = new Float64Array(3 * NC), val = new Float64Array(NC), dg = new Float64Array(NC), up = new Float64Array(NC), lo = new Float64Array(NC), cp = new Float64Array(NC), dp = new Float64Array(NC);
  let h = 0, cs = new Float64Array(0), ncap = 0, deepest = 0;
  // What has touched in this engine step, in any of its substeps: one flag and one depth for each capsule and point.
  let mark = new Uint8Array(0), depth = new Float64Array(0), marked = 0, lightest = Infinity;
  for (let i = 0; i <= last; i++) if (w[i] > 0 && mass[i] < lightest) lightest = mass[i];

  // The direction of every link, into d (three numbers each), with its length over its rest length into val.
  const PLAIN = rod ? n - 1 : n; // links from one point to the next; with a rod, the last link and the stick follow
  function aimOne(d, j, ex, ey, ez) {
    const L = sqrt(ex * ex + ey * ey + ez * ez), a = 3 * j;
    if (L > 1e-12) { d[a] = ex / L; d[a + 1] = ey / L; d[a + 2] = ez / L; } // a link of no length keeps the direction it had
    val[j] = L - rest[j];
  }
  function aim(d) {
    for (let j = 0, a = 0; j < PLAIN; j++, a += 3) {
      const ex = x[a + 3] - x[a], ey = x[a + 4] - x[a + 1], ez = x[a + 5] - x[a + 2], L = sqrt(ex * ex + ey * ey + ez * ez);
      if (L > 1e-12) { d[a] = ex / L; d[a + 1] = ey / L; d[a + 2] = ez / L; }
      val[j] = L - rest[j];
    }
    if (rod) {
      const a = 3 * n - 3, r = 3 * n;
      aimOne(d, n - 1, ra * x[r] + rb * x[r + 3] - x[a], ra * x[r + 1] + rb * x[r + 4] - x[a + 1], ra * x[r + 2] + rb * x[r + 5] - x[a + 2]);
      aimOne(d, n, x[r + 3] - x[r], x[r + 4] - x[r + 1], x[r + 5] - x[r + 2]);
    }
  }
  // One Newton step on every length at once. The points are moved along the directions the links had
  // at the start of the substep (dir0) until the lengths are right where the points are now (dir).
  // lam adds up over the sweeps of a substep; -lam / h^2 is the tension.
  function lengths() {
    const at = 1 / (h * h), from = dir0;
    aim(dir);
    // The equations: each row is one link, tied to the link before (lo) and after (up) through the point they share.
    for (let j = 0, a = 0; j < NC; j++, a += 3) {
      const own = dir[a] * from[a] + dir[a + 1] * from[a + 1] + dir[a + 2] * from[a + 2];
      const fore = j + 1 < NC ? dir[a] * from[a + 3] + dir[a + 1] * from[a + 4] + dir[a + 2] * from[a + 5] : 0, back = j + 1 < NC ? dir[a + 3] * from[a] + dir[a + 4] * from[a + 1] + dir[a + 5] * from[a + 2] : 0;
      if (j < PLAIN) { dg[j] = (w[j] + w[j + 1]) * own + soft[j] * at; up[j] = -w[j + 1] * fore; lo[j] = -w[j + 1] * back; }
      else if (j === n - 1) { const k = rb * w[n + 1] - ra * w[n]; dg[j] = (w[j] + ra * ra * w[n] + rb * rb * w[n + 1]) * own + soft[j] * at; up[j] = k * fore; lo[j] = k * back; }
      else { dg[j] = (w[n] + w[n + 1]) * own; up[j] = 0; lo[j] = 0; }
    }
    // The tridiagonal sweep. A link with nothing free at either end has an empty row and is left alone.
    for (let j = 0; j < NC; j++) {
      const below = j ? lo[j - 1] : 0, m = dg[j] - (j ? below * cp[j - 1] : 0), r = -val[j] - soft[j] * at * lam[j] - (j ? below * dp[j - 1] : 0);
      if (m > 1e-300) { cp[j] = up[j] / m; dp[j] = r / m; } else { cp[j] = 0; dp[j] = 0; }
    }
    for (let j = NC - 1; j >= 0; j--) {
      const dl = dp[j] - (j + 1 < NC ? cp[j] * dp[j + 1] : 0);
      dp[j] = dl; lam[j] += dl;
    }
    for (let j = 0, a = 0; j < PLAIN; j++, a += 3) {
      const dl = dp[j], nx = from[a] * dl, ny = from[a + 1] * dl, nz = from[a + 2] * dl, p = w[j], q = w[j + 1];
      x[a] -= p * nx; x[a + 1] -= p * ny; x[a + 2] -= p * nz; x[a + 3] += q * nx; x[a + 4] += q * ny; x[a + 5] += q * nz;
    }
    if (rod) {
      // The last link pulls its point one way and the stick's two masses the other, in the shares that place the tie; then the stick's own length.
      let a = 3 * n - 3, r = 3 * n, dl = dp[n - 1], nx = from[a] * dl, ny = from[a + 1] * dl, nz = from[a + 2] * dl;
      const p = w[n - 1], q1 = ra * w[n], q2 = rb * w[n + 1];
      x[a] -= p * nx; x[a + 1] -= p * ny; x[a + 2] -= p * nz;
      x[r] += q1 * nx; x[r + 1] += q1 * ny; x[r + 2] += q1 * nz; x[r + 3] += q2 * nx; x[r + 4] += q2 * ny; x[r + 5] += q2 * nz;
      a = 3 * n; dl = dp[n]; nx = from[a] * dl; ny = from[a + 1] * dl; nz = from[a + 2] * dl;
      x[r] -= w[n] * nx; x[r + 1] -= w[n] * ny; x[r + 2] -= w[n] * nz; x[r + 3] += w[n + 1] * nx; x[r + 4] += w[n + 1] * ny; x[r + 5] += w[n + 1] * nz;
    }
  }

  // Capsules for this step, 15 numbers each: both ends at the start of the step, how far each end
  // moves over the step, the radius, and the two friction values.
  const CAP = 15;
  function load(caps, was) {
    ncap = caps.length;
    if (cs.length < CAP * ncap) cs = new Float64Array(CAP * ncap);
    if (mark.length < NP * ncap) { mark = new Uint8Array(NP * ncap); depth = new Float64Array(NP * ncap); } else if (marked) { mark.fill(0, 0, NP * ncap); depth.fill(0, 0, NP * ncap); }
    marked = 0;
    for (let c = 0; c < ncap; c++) {
      const now = caps[c], old = was ? was[c] : now, b = CAP * c;
      for (let k = 0; k < 3; k++) { cs[b + k] = old.a[k]; cs[b + 3 + k] = old.b[k]; cs[b + 6 + k] = now.a[k] - old.a[k]; cs[b + 9 + k] = now.b[k] - old.b[k]; }
      cs[b + 12] = now.r; cs[b + 13] = now.mu === undefined ? mu : now.mu; cs[b + 14] = now.mu === undefined ? muSlide : now.muSlide === undefined ? now.mu : now.muSlide;
    }
  }
  // Move the points out of the capsules, with friction. f is how far through the step this substep ends.
  function contact(f) {
    if (!ncap) return;
    let x0 = Infinity, y0 = Infinity, z0 = Infinity, x1 = -Infinity, y1 = -Infinity, z1 = -Infinity, big = 0;
    for (let i = 0; i <= last; i++) {
      if (rad[i] < 0 || w[i] === 0) continue;
      const px = x[3 * i], py = x[3 * i + 1], pz = x[3 * i + 2];
      if (px < x0) x0 = px; if (px > x1) x1 = px; if (py < y0) y0 = py; if (py > y1) y1 = py; if (pz < z0) z0 = pz; if (pz > z1) z1 = pz;
      if (rad[i] > big) big = rad[i];
    }
    for (let c = 0; c < ncap; c++) {
      const b = CAP * c, r = cs[b + 12], reach = r + big;
      const ax = cs[b] + cs[b + 6] * f, ay = cs[b + 1] + cs[b + 7] * f, az = cs[b + 2] + cs[b + 8] * f;
      const bx = cs[b + 3] + cs[b + 9] * f, by = cs[b + 4] + cs[b + 10] * f, bz = cs[b + 5] + cs[b + 11] * f;
      // Nothing of the chain is near this capsule: skip it.
      if (min(ax, bx) - reach > x1 || max(ax, bx) + reach < x0 || min(ay, by) - reach > y1 || max(ay, by) + reach < y0 || min(az, bz) - reach > z1 || max(az, bz) + reach < z0) continue;
      const dx = bx - ax, dy = by - ay, dz = bz - az, dd = dx * dx + dy * dy + dz * dz, ms = cs[b + 13], mk = cs[b + 14];
      for (let i = 0; i <= last; i++) {
        if (rad[i] < 0 || w[i] === 0) continue;
        const px = x[3 * i], py = x[3 * i + 1], pz = x[3 * i + 2], R = r + rad[i];
        let t = dd > 1e-18 ? ((px - ax) * dx + (py - ay) * dy + (pz - az) * dz) / dd : 0;
        t = t < 0 ? 0 : t > 1 ? 1 : t;
        let gx = px - (ax + t * dx), gy = py - (ay + t * dy), gz = pz - (az + t * dz);
        const d2 = gx * gx + gy * gy + gz * gz;
        if (d2 >= R * R) continue;
        const dist = sqrt(d2);
        if (dist > 1e-12) { gx /= dist; gy /= dist; gz /= dist; }
        else {
          // Dead on the axis there is no shortest way out: take a fixed direction square to the axis.
          gx = 0; gy = -dz; gz = dy; let l = sqrt(gy * gy + gz * gz);
          if (l < 1e-9 * sqrt(dd)) { gx = dz; gy = 0; gz = -dx; l = sqrt(gx * gx + gz * gz); }
          if (l > 0) { gx /= l; gy /= l; gz /= l; } else { gx = 0; gy = 0; gz = 1; }
        }
        const pen = R - dist;
        let qx = px + gx * pen, qy = py + gy * pen, qz = pz + gz * pen;
        if (ms > 0 || mk > 0) {
          // How far the point moved along the surface this substep, against the surface's own motion there.
          const rx = qx - xo[3 * i] - (cs[b + 6] + t * (cs[b + 9] - cs[b + 6])) / K, ry = qy - xo[3 * i + 1] - (cs[b + 7] + t * (cs[b + 10] - cs[b + 7])) / K, rz = qz - xo[3 * i + 2] - (cs[b + 8] + t * (cs[b + 11] - cs[b + 8])) / K;
          const rn = rx * gx + ry * gy + rz * gz, sx = rx - gx * rn, sy = ry - gy * rn, sz = rz - gz * rn, sl = sqrt(sx * sx + sy * sy + sz * sz);
          if (sl > 0) { const back = sl <= ms * pen ? 1 : min(1, mk * pen / sl); qx -= sx * back; qy -= sy * back; qz -= sz * back; }
        }
        x[3 * i] = qx; x[3 * i + 1] = qy; x[3 * i + 2] = qz;
        if (pen > deepest) deepest = pen;
        if (!mark[c * NP + i]) { mark[c * NP + i] = 1; marked++; }
        if (pen > depth[c * NP + i]) depth[c * NP + i] = pen;
      }
    }
  }

  const get = (a, i) => [a[3 * i], a[3 * i + 1], a[3 * i + 2]];
  // The points of the line in order; with a rod, the last is the spot on the rod the line is tied to.
  const points = () => { const out = []; for (let i = 0; i < n; i++) out.push(get(x, i)); out.push([endOf(n - 1, 0), endOf(n - 1, 1), endOf(n - 1, 2)]); return out; };
  return {
    n, count: NP, substeps: K, link: rest[0], masses: mass,
    // The most tension (newtons) the line follows at an engine step of H: see SUBSTEPS. step() throws past it.
    limit: H => lightest * rest[0] * K * K / (H * H),
    // The most tension in any link of the line, as of the last substep. A pull that is not a number counts as without end.
    strain() { let m = 0; for (let j = 0; j < n; j++) { const t = -lam[j]; if (!(t <= m)) m = t === t ? t : Infinity; } return m / (h * h); },
    // Advance by H seconds. caps are the capsules now, was the same capsules one step ago (left out: they are still).
    // Throws when a link is pulled past the limit; the chain is then part-way through a step and is to be dropped.
    step(H, caps = [], was = null, damp = 0) {
      h = H / K; deepest = 0;
      load(caps, was);
      const keep = 1 - damp * h, most = lightest * rest[0]; // lam is tension * h^2, so the limit in the same measure is point mass * link
      for (let s = 0; s < K; s++) {
        aim(dir0);
        for (let i = 0; i < NP; i++) for (let k = 3 * i; k < 3 * i + 3; k++) {
          xo[k] = x[k];
          if (w[i] === 0) { x[k] += (goal[k] - x[k]) / (K - s); continue; } // a held point goes straight to where it is wanted
          v[k] = (v[k] + acc[k] * h) * keep; x[k] += v[k] * h;
        }
        lam.fill(0);
        for (let it = 0; it < SWEEPS; it++) lengths();
        for (let j = 0; j < n; j++) if (!(-lam[j] <= most)) throw new Error(`the line is pulled at ${(-lam[j] / (h * h)).toFixed(0)} N, more than its chain can follow (${(most / (h * h)).toFixed(0)} N)`);
        contact((s + 1) / K);
        for (let k = 0; k < 3 * NP; k++) v[k] = (x[k] - xo[k]) / h;
      }
    },
    hold(i, p) { goal[3 * i] = p[0]; goal[3 * i + 1] = p[1]; goal[3 * i + 2] = p[2]; },
    pos: i => get(x, i), vel: i => get(v, i), points,
    set(i, p, vel) { for (let k = 0; k < 3; k++) { x[3 * i + k] = p[k]; if (vel) v[3 * i + k] = vel[k]; } },
    setAcc(i, a) { acc[3 * i] = a[0]; acc[3 * i + 1] = a[1]; acc[3 * i + 2] = a[2]; },
    // Tension in link j, newtons, as of the last substep.
    tension: j => -lam[j] / (h * h),
    // What touched during the last step, in any of its substeps, so that a glancing touch is not missed:
    // point, capsule (its place in caps) and the furthest it was pushed out in one substep. By capsule, then by point.
    touching() { const out = []; if (marked) for (let c = 0; c < ncap; c++) for (let i = 0; i <= last; i++) if (mark[c * NP + i]) out.push({ i, c, depth: depth[c * NP + i] }); return out; },
    deepest: () => deepest, // the furthest any point was pushed out in one substep of the last step
    // How far the line is wrapped round capsule c: the turn of its direction about the capsule's axis
    // from the link before the first point that touches to the link after the last. Radians, signed
    // (right-handed about the axis from a to b), with the points it runs between. null when nothing touches.
    wrap(c) {
      let from = -1, to = -1;
      if (c < ncap) for (let i = 0; i <= last; i++) if (mark[c * NP + i]) { if (from < 0) from = i; to = i; }
      if (from < 0) return null;
      const b = CAP * c, ux = cs[b + 3] + cs[b + 9] - cs[b] - cs[b + 6], uy = cs[b + 4] + cs[b + 10] - cs[b + 1] - cs[b + 7], uz = cs[b + 5] + cs[b + 11] - cs[b + 2] - cs[b + 8], ul = sqrt(ux * ux + uy * uy + uz * uz) || 1;
      const P = points(), ax = [ux / ul, uy / ul, uz / ul];
      const flat = (p, q) => { const d = [q[0] - p[0], q[1] - p[1], q[2] - p[2]], a = d[0] * ax[0] + d[1] * ax[1] + d[2] * ax[2]; return [d[0] - ax[0] * a, d[1] - ax[1] * a, d[2] - ax[2] * a]; };
      let angle = 0;
      for (let i = max(from, 1); i <= min(to, n - 1); i++) {
        const p = flat(P[i - 1], P[i]), q = flat(P[i], P[i + 1]);
        angle += atan2((p[1] * q[2] - p[2] * q[1]) * ax[0] + (p[2] * q[0] - p[0] * q[2]) * ax[1] + (p[0] * q[1] - p[1] * q[0]) * ax[2], p[0] * q[0] + p[1] * q[1] + p[2] * q[2]);
      }
      return { angle, turns: angle / (2 * PI), from, to };
    },
  };
}

module.exports = { makeChain, links, LINK_LEN, SUBSTEPS, SWEEPS, THICK };
