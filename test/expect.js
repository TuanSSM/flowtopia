// What each move's definition promises, checked on the finished clip. A check returns true,
// or a sentence saying what is wrong. Keys are 'prop:skill', then the variant name or '*'.
const E = require('../sim/engine.js'), { Demo } = E, { SHOULDER, ISO, TETHER } = E;
const { add, sub, mul, dot, len, norm } = Demo.v, mid = (a, b) => mul(add(a, b), 0.5);
const mm = v => (v * 1000).toFixed(0) + ' mm';
// Count the peaks of a looping series: used for flower petals.
function peaks(v) {
  let n = 0;
  for (let i = 0; i < v.length; i++) { const a = v[(i + v.length - 1) % v.length], b = v[i], c = v[(i + 1) % v.length]; if (b > a && b >= c) n++; }
  return n;
}
// Signed turns of a point around a centre over one loop, in the y-z plane (ax 2) or the x-y plane (ax 0).
function turns(pts, c, ax) {
  let t = 0;
  for (let i = 0; i < pts.length; i++) {
    const a = sub(pts[i], c), b = sub(pts[(i + 1) % pts.length], c);
    let d = Math.atan2(b[1], b[ax]) - Math.atan2(a[1], a[ax]);
    d -= 2 * Math.PI * Math.round(d / (2 * Math.PI)); t += d;
  }
  return Math.round(t / (2 * Math.PI) * 100) / 100;
}
const P = (c, j = 0) => c.fr.map(f => f[j + '.P']), H = (c, s = 'R') => c.fr.map(f => f['h' + s]);
// Forward means the head moves away from the spinner (toward +z) as it passes over the top.
function fwd(c, sign, j = 0) { const p = P(c, j), i = p.reduce((b, q, k) => q[1] > p[b][1] ? k : b, 0), v = p[(i + 1) % p.length][2] - p[i][2]; return Math.sign(v) === sign || 'wrong direction over the top'; }
function petals(c, want, ratio, centre = SHOULDER) {
  const p = P(c), h = H(c), n = peaks(p.map(q => len(sub(q, centre)))), arm = turns(h, centre, 2), poi = turns(p.map((q, i) => add(sub(q, h[i]), centre)), centre, 2);
  return (n === want && Math.abs(arm) === 1 && poi / arm === ratio) || `${n} petals, poi turns ${poi / arm} per arm circle; wanted ${want} and ${ratio}`;
}
const X = module.exports = {
  'poi:F2': { Forward: c => fwd(c, 1), Backward: c => fwd(c, -1) },
  'poi:S3': { Pendulum: c => { const top = Math.max(...P(c).map(q => q[1])), hand = c.fr[0].hR[1]; return top < hand - 0.05 || `head rises to within ${mm(hand - top)} of hand height`; } },
  'poi:I1': { '*': c => { const worst = Math.max(...c.fr.map(f => { const a = sub(f.hR, SHOULDER), b = sub(f['0.P'], f.hR); return Math.acos(Math.min(1, dot(a, b) / (len(a) * len(b)))); })) * 180 / Math.PI; const r = P(c).map(q => len(sub(q, SHOULDER))); return (worst < 20 && Math.max(...r) - Math.min(...r) < 0.08) || `poi up to ${worst.toFixed(0)} degrees off the arm line, radius varies ${mm(Math.max(...r) - Math.min(...r))}`; } },
  'poi:I2': { '*': c => { const off = Math.max(...c.fr.map(f => len(sub(mid(f.hR, f['0.P']), ISO)))); return off < 0.04 || `middle of the tether wanders ${mm(off)}`; } },
  'poi:L1': { 'One loop': c => petals(c, 1, 2), 'Two loops': c => petals(c, 2, 3) },
  'poi:L2': { '*': c => petals(c, 4, -3) },
  'poi:L3': { '*': c => petals(c, 3, -2) },
  'doublestaff:I1': { '*': c => { const a = c.fr.map(f => f['0.a']), off = Math.max(...a.map(q => len(sub(q, a[0])))); return off < 2e-4 || `fixed end moves ${mm(off)}`; } },
  'doublestaff:A1': { '*': c => { const a = c.fr.map(f => f['0.a']), h = H(c), y = a.map(q => q[1]); const st = turns(a.map((q, i) => add(sub(q, h[i]), SHOULDER)), SHOULDER, 2), hd = turns(h, SHOULDER, 2); return (Math.max(...y) - Math.min(...y) < 2e-4 && st === -hd && Math.abs(hd) === 1) || `end leaves its line by ${mm(Math.max(...y) - Math.min(...y))}, staff turns ${st} per hand circle ${hd}`; } },
};

// ---- hand-driven props ----
const A = (c, j = 0) => c.fr.map(f => f[j + '.a']), Bq = (c, j = 0) => c.fr.map(f => f[j + '.b']);
// Turns of the staff's direction over one loop, about the axis ax (0: x, 1: y, 2: z).
function staffTurns(c, ax, j = 0) {
  const [p, q] = [[2, 1], [2, 0], [0, 1]][ax]; let t = 0; // the same sense as turns() above
  const ang = c.fr.map(f => { const d = sub(f[j + '.a'], f[j + '.b']); return Math.atan2(d[q], d[p]); });
  ang.forEach((a, i) => { let d = ang[(i + 1) % ang.length] - a; d -= 2 * Math.PI * Math.round(d / (2 * Math.PI)); t += d; });
  return Math.round(t / (2 * Math.PI) * 100) / 100;
}
const handTurns = (c, centre, ax) => turns(H(c), centre, ax);
// A figure-8: two full turns per repeat, with the marked end on one side of the hand for one and the other side for the next.
function eight(c, ax, sideAx) {
  const n = staffTurns(c, ax), side = c.fr.map(f => f['0.a'][sideAx] - f.hR[sideAx]).filter(v => Math.abs(v) > 0.01).map(Math.sign), flips = side.filter((v, i) => v !== side[(i + 1) % side.length]).length;
  return (Math.abs(n) === 2 && flips === 2) || `staff turns ${n} times and its marked end changes side ${flips} times per repeat; wanted 2 and 2`;
}
// Unit-circle staff moves: turns of the staff per circle of the hand.
function ratio(c, want, centre) { const st = staffTurns(c, 0), hd = handTurns(c, centre, 2); return (Math.abs(hd) === 1 && st / hd === want) || `staff turns ${st / hd} per hand circle; wanted ${want}`; }
const SH = [-0.24, 1.43, 0];
const span = v => Math.max(...v) - Math.min(...v);
Object.assign(X, {
  'staff:F4': { '*': c => Math.abs(staffTurns(c, 2)) === 1 || 'the staff does not make one turn per repeat' },
  'staff:F7': { '*': c => (Math.abs(staffTurns(c, 1)) === 1 && span(A(c).map(q => q[1])) < 1e-3) || 'the staff does not turn level above the head' },
  'staff:M1': { '*': c => { const f = c.fr.map(q => q.face), t = turns(f.map(v => [0, v[0], v[2]]), [0, 0, 0], 2); const front = Math.min(...c.fr.map(q => dot(sub(mul(add(q['0.a'], q['0.b']), 0.5), q.pelvis || [0, 0.97, 0]), q.face))); return (Math.abs(t) === 1 && front > 0.3) || `body turns ${t} times and the staff comes within ${mm(front)} of the body's front`; } },
  'staff:C3': { '*': c => (span(A(c).map(q => q[1])) < 1e-3 && A(c)[0][1] > 1.75) || 'the staff is not level above the head' },
  'doublestaff:F5': { '*': c => eight(c, 0, 0) }, 'doublestaff:F6': { '*': c => eight(c, 0, 0) },
  'doublestaff:T2': { '*': c => eight(c, 2, 2) }, 'doublestaff:T3': { '*': c => eight(c, 2, 2) },
  'doublestaff:W1': { '*': c => (c.fr.every(f => f['0.a'][2] > f.hR[2] && f['0.b'][2] < f.hR[2]) && Math.abs(staffTurns(c, 2)) === 2) || 'the thumb end is not always in front of the hand and the pinky end behind it' },
  'doublestaff:W2': { '*': c => (c.fr.every(f => f['0.a'][2] < f.hR[2] && f['0.b'][2] > f.hR[2]) && Math.abs(staffTurns(c, 2)) === 2) || 'the thumb end is not always behind the hand and the pinky end in front of it' },
  'doublestaff:I4': { '*': c => { const worst = Math.max(...c.fr.map(f => { const a = norm(sub(f.hR, SH)), b = norm(sub(f['0.a'], f['0.b'])); return Math.acos(Math.min(1, Math.abs(dot(a, b)))); })); return (worst < 0.03 && ratio(c, 1, SH) === true) || `staff up to ${(worst * 57.3).toFixed(0)} degrees off the line of the arm`; } },
  'doublestaff:A2': { 'Together, same direction': c => ratio(c, -1, SH) },
  'doublestaff:A3': { '*': c => ratio(c, 3, SH) },
  'doublestaff:A8': { '*': c => { const a = A(c), w = span(a.map(q => q[1])), d = span(a.map(q => q[2])); return (ratio(c, -1, SH) === true && Math.min(w, d) > 0.2 && Math.max(w, d) / Math.min(w, d) > 1.5) || `end path is ${mm(w)} by ${mm(d)}: wanted an ellipse`; } },
  'doublestaff:A10': { '*': c => ratio(c, -3, SH) },
  'doublestaff:A11': { '*': c => { const ang = c.fr.map(f => { const d = sub(f['0.a'], f['0.b']); return Math.atan2(d[1], d[2]); }); let t = 0, lo = 0, hi = 0; ang.forEach((a, i) => { let d = ang[(i + 1) % ang.length] - a; d -= 2 * Math.PI * Math.round(d / (2 * Math.PI)); t += d; lo = Math.min(lo, t); hi = Math.max(hi, t); }); const sw = (hi - lo) / (2 * Math.PI); return (Math.abs(sw - 0.75) < 0.02 && Math.abs(t) < 0.01) || `staff swings ${sw.toFixed(2)} of a turn each way; wanted 0.75`; } },
  'buugeng:I1': { '*': c => { const q = c.fr.map(f => sub(f['0.c'], mul(f['0.u'], 0.19))), off = Math.max(...q.map(v => len(sub(v, q[0])))); return off < 1e-3 || `the middle of the curve moves ${mm(off)}`; } },
  'buugeng:F3': { '*': c => { const u = c.fr.map(f => f['0.u']), side = u.map(v => Math.sign(v[0])), flips = side.filter((v, i) => v !== side[(i + 1) % side.length]).length; return flips === 2 || `the buugeng changes side of the arm ${flips} times per repeat; wanted 2`; } },
  // ---- poi ----
  'poi:L4': { '*': c => petals(c, 4, -3, [-0.24, 1.43, 0]) }, 'poi:L5': { '*': c => petals(c, 4, -3, [-0.24, 1.43, 0]) },
  'poi:F8': { '*': c => { const x = c.fr.map(f => f.pelvis[0]), t = turns(P(c).map((q, i) => sub(q, H(c)[i])), [0, 0, 0], 2); return (span(x) > 0.1 && Math.abs(t) === 2 && c.fr.every(f => f.fL[1] < 0.05 && f.fR[1] < 0.05)) || `pelvis moves ${mm(span(x))}, poi turns ${t} per repeat`; } },
  // ---- nunchaku with the body moving ----
  'nunchaku:M1': { '*': c => span(c.fr.map(f => f.pelvis[0])) > 0.1 || 'the weight does not shift' },
  'nunchaku:M4': { '*': c => span(c.fr.map(f => f.pelvis[1])) > 0.2 || 'the body does not go down and up' },
});
// Flowers are now solved about this centre.
X['poi:L1'] = { 'One loop': c => petals(c, 1, 2, [-0.24, 1.43, 0]), 'Two loops': c => petals(c, 2, 3, [-0.24, 1.43, 0]) };
X['poi:L2'] = { '*': c => petals(c, 4, -3, [-0.24, 1.43, 0]) };
X['poi:L3'] = { '*': c => petals(c, 3, -2, [-0.24, 1.43, 0]) };
Object.defineProperty(X, 'turns', { value: turns }); Object.defineProperty(X, 'peaks', { value: peaks }); Object.defineProperty(X, 'fwd', { value: fwd }); Object.defineProperty(X, 'petals', { value: petals });
