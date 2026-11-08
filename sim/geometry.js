'use strict';
// Measurements of curves, for suggesting which trick can follow another. Pure functions over
// arrays of [x, y, z] points in metres; nothing here knows about props or skills.
//
// Discrete differential geometry: curvature from the turning angle between neighbouring segments,
// torsion from the turning of the binormal about the tangent, a rotation-minimising (parallel
// transport) frame instead of the Frenet frame (which flips where curvature passes through zero),
// a best-fit plane from the covariance of the points, and a shape distance between closed curves
// from their square-root velocity functions (SRVF).
const add = (a, b) => [a[0] + b[0], a[1] + b[1], a[2] + b[2]];
const sub = (a, b) => [a[0] - b[0], a[1] - b[1], a[2] - b[2]];
const mul = (a, k) => [a[0] * k, a[1] * k, a[2] * k];
const dot = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
const cross = (a, b) => [a[1] * b[2] - a[2] * b[1], a[2] * b[0] - a[0] * b[2], a[0] * b[1] - a[1] * b[0]];
const len = a => Math.hypot(a[0], a[1], a[2]);
const norm = a => { const l = len(a); return l > 1e-12 ? mul(a, 1 / l) : [0, 0, 0]; };
const clampU = x => Math.max(-1, Math.min(1, x));
const angle = (a, b) => Math.acos(clampU(dot(norm(a), norm(b))));
const rotate = (v, k, t) => add(add(mul(v, Math.cos(t)), mul(cross(k, v), Math.sin(t))), mul(k, dot(k, v) * (1 - Math.cos(t))));

// Total length; a closed curve includes the segment back to the start.
function arcLength(P, closed = true) {
  let L = 0;
  for (let i = 1; i < P.length; i++) L += len(sub(P[i], P[i - 1]));
  if (closed && P.length > 1) L += len(sub(P[0], P[P.length - 1]));
  return L;
}
// n points evenly spaced along the curve, the first one off (a fraction of a spacing) along it.
function resample(P, n, closed = true, off = 0) {
  const pts = closed ? [...P, P[0]] : P, cum = [0];
  for (let i = 1; i < pts.length; i++) cum.push(cum[i - 1] + len(sub(pts[i], pts[i - 1])));
  const L = cum[cum.length - 1], out = [];
  if (L < 1e-12) return Array.from({ length: n }, () => P[0].slice());
  for (let k = 0, j = 0; k < n; k++) {
    const s = closed ? (((k + off) % n) * L) / n : (k * L) / (n - 1);
    while (j < cum.length - 2 && cum[j + 1] < s) j++;
    const t = (s - cum[j]) / ((cum[j + 1] - cum[j]) || 1);
    out.push(add(pts[j], mul(sub(pts[j + 1], pts[j]), t)));
  }
  return out;
}
// Mean of each value over a window of w points either side, wrapping on a closed curve.
function smoothArr(a, w, closed = true) {
  if (!w) return a.slice();
  const n = a.length;
  return a.map((_, i) => {
    let s = 0, c = 0;
    for (let k = -w; k <= w; k++) { let j = i + k; if (closed) j = (j + n) % n; else if (j < 0 || j >= n) continue; s += a[j]; c++; }
    return s / c;
  });
}
// Curvature (1/m) at each point of an evenly resampled curve: the angle between the segments
// either side, over the mean segment length. Torsion (1/m): the signed angle the binormal turns
// about the tangent between neighbouring points, over the segment length. SMOOTH points either side
// are averaged, to quieten the noise of 0.1 mm quantised clip data.
const SMOOTH = 2;
function curvatureTorsion(P, closed = true, smooth = SMOOTH) {
  const n = P.length, at = i => P[closed ? (i + n) % n : Math.max(0, Math.min(n - 1, i))];
  const kappa = [], B = [], T = [], ds = [];
  for (let i = 0; i < n; i++) {
    const e0 = sub(at(i), at(i - 1)), e1 = sub(at(i + 1), at(i)), d = (len(e0) + len(e1)) / 2 || 1e-12;
    kappa.push(angle(e0, e1) / d); B.push(norm(cross(e0, e1))); T.push(norm(add(e0, e1))); ds.push(d);
  }
  const tau = [];
  for (let i = 0; i < n; i++) {
    const j = closed ? (i + 1) % n : Math.min(n - 1, i + 1), b0 = B[i], b1 = B[j];
    if (len(b0) < 0.5 || len(b1) < 0.5) { tau.push(0); continue; }
    const s = Math.sign(dot(cross(b0, b1), T[i])) || 1;
    tau.push(s * angle(b0, b1) / ds[i]);
  }
  if (!closed) { kappa[0] = kappa[1]; kappa[n - 1] = kappa[n - 2]; tau[n - 1] = tau[n - 2]; tau[0] = tau[1]; }
  return { kappa: smoothArr(kappa, smooth, closed), tau: smoothArr(tau, smooth, closed) };
}
// A rotation-minimising frame: the normal is carried along the curve by the smallest rotation that
// takes each tangent to the next, so it never flips. Returns the frames and, for a closed curve, the
// twist (radians) between the carried normal and the starting one after one lap.
function bishop(P, closed = true) {
  const n = P.length, at = i => P[closed ? (i + n) % n : Math.max(0, Math.min(n - 1, i))];
  const T = P.map((_, i) => norm(sub(at(i + 1), at(i - 1))));
  let N = norm(cross(T[0], Math.abs(T[0][1]) < 0.9 ? [0, 1, 0] : [1, 0, 0]));
  const frames = [{ T: T[0], N, B: cross(T[0], N) }];
  const carry = (N, a, b) => { const ax = cross(a, b), s = len(ax); return s < 1e-12 ? N : rotate(N, mul(ax, 1 / s), Math.atan2(s, dot(a, b))); };
  for (let i = 1; i < n; i++) { N = norm(carry(N, T[i - 1], T[i])); N = norm(sub(N, mul(T[i], dot(N, T[i])))); frames.push({ T: T[i], N, B: cross(T[i], N) }); }
  let twist = 0;
  if (closed) { const back = norm(carry(N, T[n - 1], T[0])); twist = Math.sign(dot(cross(frames[0].N, back), T[0])) * angle(frames[0].N, back); }
  return { frames, twist };
}
// Eigen decomposition of a symmetric 3x3 matrix (Jacobi rotations), values sorted ascending.
function eigSym(A) {
  const a = A.map(r => r.slice()), V = [[1, 0, 0], [0, 1, 0], [0, 0, 1]];
  for (let sweep = 0; sweep < 50; sweep++) {
    let off = 0;
    for (let p = 0; p < 3; p++) for (let q = p + 1; q < 3; q++) off += a[p][q] * a[p][q];
    if (off < 1e-20) break;
    for (let p = 0; p < 3; p++) for (let q = p + 1; q < 3; q++) {
      if (Math.abs(a[p][q]) < 1e-15) continue;
      const th = (a[q][q] - a[p][p]) / (2 * a[p][q]), t = Math.sign(th || 1) / (Math.abs(th) + Math.sqrt(th * th + 1)), c = 1 / Math.sqrt(t * t + 1), s = t * c;
      for (let k = 0; k < 3; k++) { const akp = a[k][p], akq = a[k][q]; a[k][p] = c * akp - s * akq; a[k][q] = s * akp + c * akq; }
      for (let k = 0; k < 3; k++) { const apk = a[p][k], aqk = a[q][k]; a[p][k] = c * apk - s * aqk; a[q][k] = s * apk + c * aqk; }
      for (let k = 0; k < 3; k++) { const vkp = V[k][p], vkq = V[k][q]; V[k][p] = c * vkp - s * vkq; V[k][q] = s * vkp + c * vkq; }
    }
  }
  return [0, 1, 2].map(i => ({ value: a[i][i], vector: [V[0][i], V[1][i], V[2][i]] })).sort((x, y) => x.value - y.value);
}
// A normal is a line, not an arrow; pick the direction whose largest component is positive, so the
// same plane always gives the same vector and the sense of rotation can be read against it.
function canonical(n) {
  const i = [0, 1, 2].reduce((b, k) => Math.abs(n[k]) > Math.abs(n[b]) ? k : b, 0);
  return n[i] < 0 ? mul(n, -1) : n;
}
// The best-fit plane: its normal is the direction the points spread least. Flatness is the ratio of
// the least to the most spread (0 for a perfectly flat path). The sense is +1 or -1: the sign of the
// angular momentum of the motion about the normal (counter-clockwise looking down the normal is +1).
function plane(P) {
  const c = mul(P.reduce(add, [0, 0, 0]), 1 / P.length), C = [[0, 0, 0], [0, 0, 0], [0, 0, 0]];
  for (const p of P) { const d = sub(p, c); for (let i = 0; i < 3; i++) for (let j = 0; j < 3; j++) C[i][j] += d[i] * d[j] / P.length; }
  const e = eigSym(C), n = canonical(norm(e[0].vector));
  let L = 0;
  for (let i = 0; i < P.length; i++) L += dot(cross(sub(P[i], c), sub(P[(i + 1) % P.length], P[i])), n);
  return { normal: n, centre: c, flatness: e[2].value > 1e-12 ? e[0].value / e[2].value : 0, sense: Math.sign(L) || 0, spread: Math.sqrt(Math.max(e[2].value, 0)) };
}
// Coordinates of points in a plane, from two axes perpendicular to its normal.
function inPlane(P, pl) {
  const n = pl.normal, u = norm(cross(n, Math.abs(n[1]) < 0.9 ? [0, 1, 0] : [1, 0, 0])), v = cross(n, u);
  return P.map(p => { const d = sub(p, pl.centre); return [dot(d, u), dot(d, v)]; });
}
// How many times the tip goes round the hand in one cycle (the turning of hand-to-tip in the plane),
// and how many lobes the path has (a figure-8 has two: its curvature in the plane changes sign twice).
function winding(tips, hands, pl) {
  const a = inPlane(tips, pl), h = inPlane(hands, pl);
  let turn = 0, prev = Math.atan2(a[0][1] - h[0][1], a[0][0] - h[0][0]);
  for (let i = 1; i <= a.length; i++) {
    const k = i % a.length, ang = Math.atan2(a[k][1] - h[k][1], a[k][0] - h[k][0]);
    let d = ang - prev; while (d > Math.PI) d -= 2 * Math.PI; while (d < -Math.PI) d += 2 * Math.PI;
    turn += d; prev = ang;
  }
  return Math.round(turn / (2 * Math.PI));
}
function lobes(P, pl) {
  const q = inPlane(resample(P, 96), pl), n = q.length, signed = [];
  for (let i = 0; i < n; i++) {
    const a = q[(i - 1 + n) % n], b = q[i], c = q[(i + 1) % n];
    signed.push((b[0] - a[0]) * (c[1] - b[1]) - (b[1] - a[1]) * (c[0] - b[0]));
  }
  const s = smoothArr(signed, 4), big = Math.max(...s.map(Math.abs)) * 0.05;
  let changes = 0, last = 0;
  for (let i = 0; i < n; i++) { const sg = Math.abs(s[i]) < big ? 0 : Math.sign(s[i]); if (sg && last && sg !== last) changes++; if (sg) last = sg; }
  if (last && changes % 2) changes++;
  return Math.max(1, changes);
}
// The square-root velocity function of an evenly resampled closed planar curve scaled to unit length:
// q(s) = c'(s) / sqrt(|c'(s)|). On these curves |c'| is constant, so speed along the path no longer
// matters; what remains is the shape.
function srvf(P2) {
  const n = P2.length;
  let L = 0;
  for (let i = 0; i < n; i++) L += Math.hypot(P2[(i + 1) % n][0] - P2[i][0], P2[(i + 1) % n][1] - P2[i][1]);
  return P2.map((p, i) => { const q = P2[(i + 1) % n], d = [(q[0] - p[0]) / L * n, (q[1] - p[1]) / L * n], sp = Math.sqrt(Math.hypot(d[0], d[1])) || 1; return [d[0] / sp, d[1] / sp]; });
}
// Shape distance between two closed curves: each is laid in its own plane, resampled by arc length,
// scaled to unit length and turned into its SRVF; the distance is the smallest L2 distance over every
// starting point, the best rotation in the plane, either side of the plane and either direction of
// travel. Arc-length resampling stands in for the full
// search over reparametrisations that elastic shape analysis does, which is enough for paths that
// are traced at a steady pace. 0 means the same shape; distances are at most 2.
const SHAPE_N = 64;
function shapeDistance(A, B) {
  const qa = srvf(inPlane(resample(A, SHAPE_N), plane(A))), n = SHAPE_N, pb = plane(B);
  let best = Infinity;
  // The same path seen from the other side of its plane, or run the other way, is the same shape:
  // which way a move turns is its sense, measured separately.
  const flip = q => q.map(([x, y]) => [x, -y]), back = q => q.map((_, i) => q[n - 1 - i].map(v => -v));
  // B is also sampled a quarter, half and three quarters of a spacing along, so two samplings of the
  // same curve that start between each other's points still line up.
  for (const off of [0, 0.25, 0.5, 0.75]) for (const qb of [srvf(inPlane(resample(B, SHAPE_N, true, off), pb))]) for (const b of [qb, flip(qb), back(qb), back(flip(qb))]) {
    for (let s = 0; s < n; s++) {
      let c = 0, d = 0, aa = 0, bb = 0;
      for (let i = 0; i < n; i++) { const p = qa[i], q = b[(i + s) % n]; c += p[0] * q[0] + p[1] * q[1]; d += p[0] * q[1] - p[1] * q[0]; aa += p[0] * p[0] + p[1] * p[1]; bb += q[0] * q[0] + q[1] * q[1]; }
      // Best rotation by Procrustes in 2D: the distance is |a|^2 + |b|^2 - 2 sqrt(c^2 + d^2).
      best = Math.min(best, Math.sqrt(Math.max(0, aa + bb - 2 * Math.hypot(c, d)) / n));
    }
  }
  return best;
}
// Orientations as unit quaternions [w, x, y, z]. From a rotation whose columns are the axes x, y, z.
function quatFromAxes(x, y, z) {
  const m = [[x[0], y[0], z[0]], [x[1], y[1], z[1]], [x[2], y[2], z[2]]], t = m[0][0] + m[1][1] + m[2][2];
  let q;
  if (t > 0) { const s = Math.sqrt(t + 1) * 2; q = [s / 4, (m[2][1] - m[1][2]) / s, (m[0][2] - m[2][0]) / s, (m[1][0] - m[0][1]) / s]; }
  else if (m[0][0] > m[1][1] && m[0][0] > m[2][2]) { const s = Math.sqrt(1 + m[0][0] - m[1][1] - m[2][2]) * 2; q = [(m[2][1] - m[1][2]) / s, s / 4, (m[0][1] + m[1][0]) / s, (m[0][2] + m[2][0]) / s]; }
  else if (m[1][1] > m[2][2]) { const s = Math.sqrt(1 + m[1][1] - m[0][0] - m[2][2]) * 2; q = [(m[0][2] - m[2][0]) / s, (m[0][1] + m[1][0]) / s, s / 4, (m[1][2] + m[2][1]) / s]; }
  else { const s = Math.sqrt(1 + m[2][2] - m[0][0] - m[1][1]) * 2; q = [(m[1][0] - m[0][1]) / s, (m[0][2] + m[2][0]) / s, (m[1][2] + m[2][1]) / s, s / 4]; }
  const l = Math.hypot(...q);
  q = q.map(v => v / l);
  return q[0] < 0 ? q.map(v => -v) : q;
}
// An orientation from a main direction and a second one (made perpendicular to it).
function quatFrom(main, second) {
  const x = norm(main), y0 = sub(second, mul(x, dot(second, x)));
  const y = len(y0) > 1e-9 ? norm(y0) : norm(cross(x, Math.abs(x[1]) < 0.9 ? [0, 1, 0] : [1, 0, 0]));
  return quatFromAxes(x, y, cross(x, y));
}
const qdot = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2] + a[3] * b[3];
// The angle (radians) of the rotation from one orientation to another, the distance on SO(3).
const quatAngle = (a, b) => 2 * Math.acos(Math.min(1, Math.abs(qdot(a, b))));
const qconj = q => [q[0], -q[1], -q[2], -q[3]];
const qmul = (a, b) => [a[0] * b[0] - a[1] * b[1] - a[2] * b[2] - a[3] * b[3], a[0] * b[1] + a[1] * b[0] + a[2] * b[3] - a[3] * b[2], a[0] * b[2] - a[1] * b[3] + a[2] * b[0] + a[3] * b[1], a[0] * b[3] + a[1] * b[2] - a[2] * b[1] + a[3] * b[0]];
// Angular velocity (rad/s, as a vector) taking orientation a to b in dt seconds.
function angularVelocity(a, b, dt) {
  let d = qmul(b, qconj(a));
  if (d[0] < 0) d = d.map(v => -v);
  const s = Math.hypot(d[1], d[2], d[3]), th = 2 * Math.atan2(s, d[0]);
  return s < 1e-12 ? [0, 0, 0] : mul([d[1] / s, d[2] / s, d[3] / s], th / dt);
}
// The angle between two planes (radians, 0 to 90 degrees): their normals are lines, not arrows.
const planeAngle = (n1, n2) => Math.acos(Math.min(1, Math.abs(dot(norm(n1), norm(n2)))));
module.exports = { quatFromAxes, quatFrom, quatAngle, qmul, qconj, angularVelocity, planeAngle,  add, sub, mul, dot, cross, len, norm, angle, rotate, arcLength, resample, smoothArr, curvatureTorsion, bishop, eigSym, canonical, plane, inPlane, winding, lobes, srvf, shapeDistance, SHAPE_N };
