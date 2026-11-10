// Known answers for the chain (sim/chain.js): a tether or cord as points that touch the body.
// Each check has a value worked out on paper and a tolerance that follows from how the chain is
// cut up (the link length, the substep), written next to it. None was fitted to a result; the ones
// that were corrected after a first run or a review, because their reasoning was wrong or said
// more than was tested, are listed in docs/demo-engine.md.
//   node test/chain.js          on its own; node test/physics.js runs it too, before the clips
const crypto = require('crypto');
const C = require('../sim/chain.js'), E = require('../sim/engine.js');
const { sin, cos, exp, sqrt, PI, abs, hypot } = Math;
const H = E.STEP, G = 9.81, TAU = 2 * PI, SUB = H / C.SUBSTEPS;
const R = 0.05, RHO = C.THICK, RE = R + RHO, LK = C.LINK_LEN;  // a forearm-sized cylinder, the line's half thickness, and the two together
const TURN = 2 * Math.asin(LK / (2 * RE));                    // the angle one straight link spans on the cylinder: 0.1855 rad
const SAG = RE * (1 - cos(TURN / 2));                         // how far the middle of a link cuts inside: 0.23 mm
const LESS = 1 - sin(TURN / 2) / (TURN / 2);                  // straight links are this much shorter than the arc they span: 0.14%
const Y0 = 1, CYL = { a: [-0.3, Y0, 0], b: [0.3, Y0, 0], r: R };
const LOAD = 0.1, POINT = 0.01 / 54;                          // an end load, and one point of a 10 g tether (kg)
const sub = (a, b) => [a[0] - b[0], a[1] - b[1], a[2] - b[2]], dot = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2], len = a => hypot(a[0], a[1], a[2]);
const mm = (v, d = 3) => (v * 1000).toFixed(d) + ' mm', pct = v => (v * 100).toFixed(2) + '%', deg = r => (r * 180 / PI).toFixed(2) + ' deg';
const smooth = u => { const s = Math.max(0, Math.min(1, u)); return s * s * (3 - 2 * s); };

// A chain laid over the cylinder as it would lie, every link its own length: `hang` whole links up
// the -z side to where the line meets the cylinder, then whole links round it (each a chord), and
// from the last point on it straight on in the direction a line wrapped `wrap` radians leaves in.
// k is how many links lie on the cylinder, and psi the part of the wrap left over after them, which
// may be up to half a link's angle either way: the turn the line makes at that last point is half
// a link's angle and psi.
function drape(n, hang, wrap, x = 0) {
  const pts = [], k = Math.floor(wrap / TURN + 0.5), f = PI - k * TURN, pull = [0, -cos(PI - wrap), sin(PI - wrap)];
  for (let i = 0; i <= n; i++) {
    if (i <= hang) pts.push([x, Y0 - (hang - i) * LK, -RE]);
    else if (i <= hang + k) pts.push([x, Y0 + RE * sin(PI - (i - hang) * TURN), RE * cos(PI - (i - hang) * TURN)]);
    else { const u = (i - hang - k) * LK; pts.push([x, Y0 + RE * sin(f) + u * pull[1], RE * cos(f) + u * pull[2]]); }
  }
  return { pts, pull, k, psi: wrap - k * TURN };
}
const loads = n => Array.from({ length: n + 1 }, (_, i) => i === 0 || i === n ? LOAD : POINT);
// Distance from a point to a capsule's axis (its line, with the round ends).
const off = (p, c) => { const q = E.closest(p, p, c.a, c.b); return len(sub(q.c1, q.c2)); };
// The least distance to the axis over the points, over the middle of every link, and over the whole of every link.
function clearance(ch, c) {
  const P = ch.points();
  let pt = Infinity, mid = Infinity, link = Infinity;
  P.forEach((p, i) => {
    pt = Math.min(pt, off(p, c));
    if (!i) return;
    mid = Math.min(mid, off([(p[0] + P[i - 1][0]) / 2, (p[1] + P[i - 1][1]) / 2, (p[2] + P[i - 1][2]) / 2], c));
    const q = E.closest(P[i - 1], p, c.a, c.b); link = Math.min(link, len(sub(q.c1, q.c2)));
  });
  return { pt, mid, link };
}

// The capstan set-up. One end hangs with a load; the other end is pulled with `ratio` times that
// load, straight down (half a turn) or sideways (a quarter turn). The ideal rope of the capstan
// relation has no weight, so gravity acts on the end loads only; the points keep their mass.
// The pull is raised smoothly over 0.6 s, then held until 1.5 s. It slides if the pulled end
// travels more than 0.5 mm in the last 0.6 s. Over the limit by a fraction e the two loads speed
// up at g * e * limit / 2 from the end of the rise, which is 1.77 m * e * limit in that window:
// 0.03% over the lowest limit here (1.37) is still 0.7 mm.
// o.move: the cylinder's velocity (the chain starts moving with it). o.blind: the engine is not told the cylinder moves.
function capstan(mu, wrap, ratio, o = {}) {
  const n = 55, { pts, pull } = drape(n, 19, wrap), mv = o.move || [0, 0, 0];
  const acc = pts.map((_, i) => i === 0 ? [0, -G, 0] : [0, 0, 0]);
  const ch = C.makeChain({ points: pts, masses: loads(n), length: n * LK, mu, acc, velocities: pts.map(() => mv) });
  const capAt = t => ({ a: CYL.a.map((v, k) => v + mv[k] * t), b: CYL.b.map((v, k) => v + mv[k] * t), r: R });
  const along = t => dot(sub(ch.pos(n), mv.map(v => v * t)), pull), start = along(0), hash = o.hash && crypto.createHash('sha1');
  let was = capAt(0), mark = null, t = 0, lift = 0;
  for (let i = 1; i <= Math.round(1.5 / H); i++) {
    t = i * H;
    const now = capAt(t), r = 1 + (ratio - 1) * smooth(t / 0.6);
    ch.setAcc(n, pull.map(v => v * r * G));
    ch.step(H, [o.keys ? { r: now.r, b: now.b, a: now.a } : now], o.blind ? null : [was]);
    was = now;
    if (hash) hash.update(Buffer.from(new Float64Array(ch.points().flat()).buffer));
    if (mark === null && t >= 0.9) mark = along(t);
    lift = Math.max(lift, abs(dot(sub(ch.pos(27), mv.map(v => v * t)), [1, 0, 0])));
    if (!hash && abs(along(t) - start) > 0.03) return { slides: true, travel: along(t) - start, t, lift };
  }
  const travel = along(t) - mark;
  return { slides: abs(travel) > 0.0005, travel, t, lift, ch, hash: hash && hash.digest('hex') };
}
// The pull a chain of straight links holds, as a multiple of the load. A smooth rope holds
// exp(mu * angle). A point with a link on each side turns the line by one link angle a, with the
// surface's push square to the middle of that turn, and holds (c + mu*s) / (c - mu*s), c and s the
// cos and sin of a/2. The first point on the cylinder is met by the hanging part along the
// surface, so it has only the half turn of its own link: 1 / (c - mu*s). The last one has its
// link's half turn and the left-over psi, which the surface's push does not halve:
// (c + mu*s) / (cos psi - mu*sin psi). With k links on the cylinder the product is the line below.
// It is above exp(mu * angle) by about mu*angle*(1 + mu^2)*a^2/12 for the links and up to a^2/8
// (0.43%) for the left-over, whatever mu is: 0.2% to 0.8% in the cases here.
const polygon = (mu, k, psi) => { const c = cos(TURN / 2), s = sin(TURN / 2); return ((c + mu * s) / (c - mu * s)) ** k / (cos(psi) - mu * sin(psi)); };
// How near the limit found must come to that, as a fraction: the search (11 halvings of 40%:
// 0.01%), the 0.5 mm that counts as sliding (0.03%), and the 0.04 mm the line creeps before it
// slides, which moves psi (0.04%): 0.1% either way.
const NEAR = 0.001;
// And one thing that can only lower it. Links and contact are solved in turn, so each point on the
// cylinder is drawn in by substep^2 * (its pull * link angle) / point mass and put back, which
// leaves the line longer by that times the link angle. Summed over the points, whose pulls rise
// from the load to the limit, the last point moves on by up to substep^2 * link angle * load *
// (limit - 1) / (mu * point mass), 0.21 mm at most here, and psi shrinks by that over the radius.
// The limit follows psi at the rate mu, so it is lower by up to this fraction: 0.02% to 0.19%.
const give = limit => SUB * SUB * TURN * LOAD * G * (limit - 1) / (POINT * RE);

function run(say = console.log) {
  let n = 0, bad = 0, skipped = 0;
  const ok = (name, cond, detail = '') => { n++; if (!cond) bad++; say(`${cond ? 'PASS' : 'FAIL'} ${name}${detail !== '' && detail !== false && detail !== null ? ': ' + detail : ''}`); };
  const steps = (ch, secs, caps, damp = 0, each) => { for (let i = 0, N = Math.round(secs / H); i < N; i++) { ch.step(H, caps, null, damp); if (each) each((i + 1) * H); } };

  // ---------- 1. draped over a smooth cylinder with equal loads ----------
  // Laid flat across the top and let go: it must find the hanging shape itself. No friction, and
  // drag of 3 a second for 6 seconds to bring the swing to rest.
  {
    const N = 55, pts = Array.from({ length: N + 1 }, (_, i) => [0, Y0 + RE, (i - N / 2) * LK]);
    const ch = C.makeChain({ points: pts, masses: loads(N), length: N * LK, mu: 0 });
    let inside = Infinity, insideMid = Infinity;
    steps(ch, 6, [CYL], 3, t => { if (Math.round(t / H) % 50 === 0) { const c = clearance(ch, CYL); inside = Math.min(inside, c.pt); insideMid = Math.min(insideMid, c.mid); } });
    const P = ch.points(), w = ch.wrap(0), touch = ch.touching();
    // The wrap angle is the arc the line lies on. With straight links that is a whole number of
    // links, and which number is fixed: the line can leave the last point it touches up to half a
    // link's angle inside or outside the surface's own direction, the 56 points lie as mirror
    // images, so an even number touch, and only 18 points (17 links, 180.63 degrees) fit both.
    // The 0.09 mm the line gives at rest spreads them 0.1 degree further: 0.2 degrees allowed.
    const at = i => Math.atan2(P[i][2], P[i][1] - Y0), span = w ? at(w.to) - at(w.from) : 0; // angles from straight up, so the half turn does not cross where atan2 jumps
    ok('draped over a smooth cylinder with equal loads, the chain lies on it for half a turn: 18 points, 17 links, 180.63 degrees (within 0.2)', w && w.to - w.from === 17 && touch.length === 18 && abs(span - 17 * TURN) < 0.2 * PI / 180, w && `${deg(span)} over points ${w.from} to ${w.to}, ${touch.length} touching`);
    // The turn of the line between its two hanging parts. Half a turn here says only that both hang
    // straight down, which is that the swing has died: after 6 s of drag at 3 a second it has (e^-18).
    ok('and its two ends hang straight down (the line turns 180 degrees between them, within 0.5)', w && abs(abs(w.angle) - PI) < 0.5 * PI / 180, w && deg(abs(w.angle)));
    // The last point on each side sits 0.3 degrees past the level of the axis (half of 180.63 - 180), 0.3 mm
    // down, and 0.05 mm more with the give: 0.5 mm.
    const low = Math.min(...touch.map(c => P[c.i][1] - Y0));
    ok('the arc of contact is on the top half of the cylinder (within 0.5 mm)', touch.length > 0 && low > -0.0005, mm(low));
    // Mirror symmetry is exact in the equations; the sweeps are not mirror images in their rounding,
    // and a smooth cylinder lets a difference grow e-fold every 0.7 s: 0.01 mm leaves room for 1e-16 to grow for 17 s.
    const skew = Math.max(...P.map((p, i) => Math.max(abs(p[2] + P[N - i][2]), abs(p[1] - P[N - i][1]), abs(p[0]))));
    ok('it hangs symmetric: each point mirrors its partner within 0.01 mm', skew < 1e-5, mm(skew));
    // Points are put on the surface exactly; the middle of a link cuts inside by radius * (1 - cos(half a link angle)) = 0.23 mm.
    ok(`no point is inside the cylinder (1e-6 mm), and no link middle by more than ${mm(SAG)} (plus 0.01 mm)`, inside - RE > -1e-9 && insideMid - RE > -SAG - 1e-5, `points ${mm(inside - RE)}, link middles ${mm(insideMid - RE)}`);
    // 19 links hang on each side, and the last touching point is 0.3 mm below the axis: 190.3 mm, and 0.1 mm for the give.
    const hang = 19 * LK + RE * sin((17 * TURN - PI) / 2), got = Y0 - P[0][1];
    ok('each load hangs 19 links below the last point that touches (within 0.1 mm)', abs(got - hang) < 0.0001, `${mm(got)} against ${mm(hang)}`);
    // At rest the link next to a load carries exactly the load's weight; 0.5% for what is left of the swing.
    ok('the link next to a load carries its weight (within 0.5%)', abs(ch.tension(0) / (LOAD * G) - 1) < 0.005, `${ch.tension(0).toFixed(4)} N against ${(LOAD * G).toFixed(4)} N`);
  }

  // ---------- 2. friction: the capstan relation ----------
  const thresholds = {};
  for (const mu of [0.2, 0.5]) for (const [wrap, turn] of [[PI / 2, 'a quarter turn'], [PI, 'half a turn']]) {
    const { k, psi } = drape(55, 19, wrap), limit = polygon(mu, k, psi), rope = exp(mu * wrap), name = `mu ${mu}, ${turn}`;
    const low = NEAR + give(limit), under = capstan(mu, wrap, limit * (1 - low)), over = capstan(mu, wrap, limit * (1 + NEAR));
    ok(`${name}: holds ${pct(low)} under the pull ${k} straight links and ${(psi / TURN).toFixed(2)} of one hold, ${limit.toFixed(4)} times the load`, !under.slides, `end moved ${mm(under.travel)}`);
    ok(`${name}: slides ${pct(NEAR)} over it`, over.slides, `end moved ${mm(over.travel)}`);
    let lo = limit * 0.8, hi = limit * 1.2;
    if (capstan(mu, wrap, lo).slides || !capstan(mu, wrap, hi).slides) { ok(`${name}: the limit lies within 20% of that`, false); continue; }
    for (let i = 0; i < 11; i++) { const mid = (lo + hi) / 2; if (capstan(mu, wrap, mid).slides) hi = mid; else lo = mid; }
    const found = (lo + hi) / 2;
    thresholds[name] = found / limit - 1;
    ok(`${name}: the limit found by bisection is that, to ${pct(NEAR)} over and ${pct(low)} under (${pct(give(limit))} of it the solver's give)`, found / limit - 1 < NEAR && found / limit - 1 > -low, `${found.toFixed(4)} against ${limit.toFixed(4)} (${found > limit ? '+' : ''}${pct(found / limit - 1)}); a smooth rope holds exp(mu * angle) = ${rope.toFixed(4)}, ${pct(limit / rope - 1)} less than these links`);
  }

  // ---------- 3. friction values: sticking, sliding, and a part with its own ----------
  {
    // Five links lying along the top of a level capsule, sliding along it. Each point presses with
    // its weight, so sliding friction slows it at muSlide * g: from 1 m/s it stops in 1 / (2 * muSlide * g).
    // The step takes speed off before it moves, which makes it half a substep's travel short: 0.08 mm.
    const cap = (extra = {}) => ({ a: [-0.5, Y0, 0], b: [0.5, Y0, 0], r: R, ...extra });
    const line = (o, push = 0, v = 1) => { const pts = Array.from({ length: 6 }, (_, i) => [-0.4 + i * LK, Y0 + RE, 0]); return C.makeChain({ points: pts, masses: pts.map(() => POINT), length: 5 * LK, velocities: pts.map(() => [v, 0, 0]), acc: pts.map(() => [push * G, -G, 0]), ...o }); };
    const slide = (ch, c, secs) => { const x0 = ch.pos(0)[0]; steps(ch, secs, [c]); return ch.pos(0)[0] - x0; };
    const own = slide(line({ mu: 0.6, muSlide: 0.5 }), cap(), 0.4), want = 1 / (2 * 0.5 * G);
    ok('sliding at 1 m/s along a part, a line with sliding friction 0.5 stops in 1 / (2 * 0.5 * g) = 101.94 mm (within 0.2 mm)', abs(own - want) < 0.0002, mm(own));
    const theirs = slide(line({ mu: 0.6, muSlide: 0.5 }), cap({ mu: 0.3, muSlide: 0.2 }), 0.8), want2 = 1 / (2 * 0.2 * G);
    ok('on a part that carries its own friction (0.3 sticking, 0.2 sliding) it stops in 1 / (2 * 0.2 * g) = 254.84 mm (within 0.2 mm)', abs(theirs - want2) < 0.0002, mm(theirs));
    // At rest it stays put while it is pushed along with less than the sticking friction, and slides when
    // pushed with more, then at the push less the sliding friction: (0.35 - 0.2) * g for 0.3 s is 66.22 mm.
    const held = slide(line({ mu: 0 }, 0.25, 0), cap({ mu: 0.3, muSlide: 0.2 }), 0.3), let2 = slide(line({ mu: 0 }, 0.35, 0), cap({ mu: 0.3, muSlide: 0.2 }), 0.3), want3 = 0.15 * G * 0.09 / 2;
    ok('at rest it sticks under a push of 0.25 g where sticking friction is 0.3 (to 0.001 mm)', abs(held) < 1e-6, mm(held, 4));
    ok('and a push of 0.35 g slides it at (0.35 - 0.2) * g: 66.22 mm in 0.3 s (within 0.2 mm)', abs(let2 - want3) < 0.0002, `${mm(let2)} against ${mm(want3)}`);
  }

  // ---------- 4. hanging free beside the cylinder: a pendulum ----------
  // A 90 g head on a 10 g tether, the weights the engine gives a poi. Swinging as one straight
  // line it is a pendulum of the length sum(m * r^2) / sum(m * r) over its points: 98.2% of the
  // tether. The period of a swing of amplitude a is 2*pi*sqrt(length/g) * (1 + a^2/16 + 11*a^4/3072).
  const L = E.TETHER, HEAD = 0.09, TETHER_KG = 0.01, NL = C.links(L);
  const weights = Array.from({ length: NL + 1 }, (_, i) => i === 0 ? Infinity : i === NL ? HEAD : TETHER_KG / (NL - 1));
  const equal = weights.reduce((s, m, i) => i ? s + m * (i * L / NL) ** 2 : s, 0) / weights.reduce((s, m, i) => i ? s + m * i * L / NL : s, 0);
  const period = (a, length) => TAU * sqrt(length / G) * (1 + a * a / 16 + 11 * a ** 4 / 3072);
  // Time between passes through the bottom in one direction, from a series of [t, angle].
  const periodOf = s => { const up = []; for (let i = 1; i < s.length; i++) if (s[i - 1][1] < 0 && s[i][1] >= 0) up.push(s[i - 1][0] + (s[i][0] - s[i - 1][0]) * -s[i - 1][1] / (s[i][1] - s[i - 1][1])); return (up[up.length - 1] - up[0]) / (up.length - 1); };
  const hung = (a, top) => {
    const pts = Array.from({ length: NL + 1 }, (_, i) => [top[0], top[1] - i * LK * cos(a), top[2] + i * LK * sin(a)]);
    return { ch: C.makeChain({ points: pts, masses: weights, length: L, mu: 0.3 }), top, N: NL };
  };
  {
    const a0 = 5 * PI / 180, { ch, top, N } = hung(a0, [0, 1.4, 0.25]), series = [];
    let touched = 0;
    steps(ch, 5, [CYL], 0, t => { const p = sub(ch.pos(N), top); series.push([t, Math.atan2(p[2], -p[1])]); touched += ch.touching().length; });
    // A chain is not a stiff line: it bows a little as it swings, which the formula leaves out. That and the
    // step error, (2*pi/period * substep)^2, are far under 0.1%, which also covers the timing of the passes.
    const got = periodOf(series), want = period(a0, equal);
    ok('hanging free beside the cylinder it touches nothing', touched === 0, touched);
    ok('a 5 degree swing has the period of the equivalent pendulum (within 0.1%)', abs(got / want - 1) < 0.001, `${got.toFixed(5)} s against ${want.toFixed(5)} s (${pct(got / want - 1)}), equivalent length ${mm(equal)}`);
  }
  {
    // Energy over 10 s of a 60 degree swing with no drag, against the energy of the swing (the
    // weight times the drop of its centre). The links are corrected along the directions they had
    // when the substep began, which has no steady loss; 1% is the height of 2.7 mm on a drop of
    // 275 mm, the same order as the 1.5 mm the suite allows a tether to stretch.
    const a0 = PI / 3, { ch, N } = hung(a0, [0, 1.75, 0.3]); // above the cylinder, where a wide swing clears it
    const energy = () => { let e = 0; for (let i = 1; i <= N; i++) { const v = ch.vel(i), p = ch.pos(i); e += weights[i] * (dot(v, v) / 2 + G * p[1]); } return e; };
    const e0 = energy(), scale = (HEAD + TETHER_KG / 2) * G * L * (1 - cos(a0));
    let worst = 0, stretch = 0, pulled = 0;
    steps(ch, 10, [CYL], 0, () => { worst = Math.max(worst, abs(energy() - e0)); stretch = Math.max(stretch, abs(len(sub(ch.pos(N), ch.pos(0))) - L)); pulled = Math.max(pulled, ch.strain()); });
    ok('a 60 degree swing keeps its energy over 10 s (within 1% of the swing)', worst / scale < 0.01, pct(worst / scale));
    // Hand to head in a straight line: the chain bows as it swings, so this may be under the tether's length but not over it.
    ok('and the head is never further from the hand than the tether (within the 1.5 mm the suite allows)', stretch < 1.5e-3, mm(stretch));
    // The most the line may be pulled before its points shake faster than the substep follows: point mass * link / substep^2.
    // At the bottom of the swing the pull is the weight below times (3 - 2 * cos 60), 2 g a kilogram: 1.96 N at the hand.
    const limit = TETHER_KG / (NL - 1) * LK / (SUB * SUB);
    ok('the pull stays far under what the chain can follow (point mass * link / substep^2 = 66.7 N)', abs(ch.limit(H) - limit) < 1e-9 && abs(limit - 66.7) < 0.05 && pulled < limit / 10, `${pulled.toFixed(2)} N at most, limit ${ch.limit(H).toFixed(1)} N`);
  }
  {
    // The same pendulum through the engine: its own tether (one stiff link, no weight of its own)
    // and the chain, on the same hand. Each against its formula, and against each other.
    const a0 = 5 * PI / 180, c = [-0.5, 1.2, 0.3];
    const dr = chain => ({ bs: { n: [-1, 0, 0], a: [0, -cos(a0), sin(a0)], b: [0, sin(a0), cos(a0)] }, s: 1, want: 0, cycle: TAU, r0: L, at: () => c, pose: () => c, chain });
    const swing = chain => { const sim = E.makeMass(dr(chain)), s = []; for (let i = 0; i < Math.round(5 / H); i++) { sim.step(0); const p = sub(sim.sample().P, c); s.push([sim.time(), Math.atan2(p[2], -p[1])]); } return { T: periodOf(s), touched: sim.contacts.length }; };
    const stiff = swing(null), chained = swing({}), want = period(a0, L), wantChain = period(a0, equal);
    ok('the engine\'s own tether swings as a pendulum of the tether\'s length (within 0.1%)', abs(stiff.T / want - 1) < 0.001, `${stiff.T.toFixed(5)} s against ${want.toFixed(5)} s (${pct(stiff.T / want - 1)})`);
    ok('the engine with the chain as its tether swings as the equivalent pendulum (within 0.1%), touching nothing', abs(chained.T / wantChain - 1) < 0.001 && chained.touched === 0, `${chained.T.toFixed(5)} s against ${wantChain.toFixed(5)} s (${pct(chained.T / wantChain - 1)}), ${chained.touched} contacts`);
    ok('so the two differ by the weight of the tether and no more: sqrt(equivalent / tether) (within 0.1%)', abs(chained.T / stiff.T / sqrt(equal / L) - 1) < 0.001, `${pct(chained.T / stiff.T - 1)} against ${pct(sqrt(equal / L) - 1)}`);
  }

  // ---------- 5. a capsule: one full turn pulled tight, and sliding off the round end ----------
  {
    // An upright capsule, no gravity, no friction. Both ends are held, 40 mm apart in height, each
    // 152.5 mm from where the line meets the capsule, and the line is too short for the way round,
    // so it is taut (it gives 1 mm per newton). Unrolled, the capsule's side is flat and the tight
    // line is straight: length sqrt((two tails + 2*pi*(r + line))^2 + rise^2), at one pitch, and
    // 2*pi*(r + line) / cos(pitch angle) of it lies round the capsule. Straight links span that
    // turn in 0.14% less (0.49 mm), which is known, so the chain is held to the smooth figures less that.
    const cap = { a: [0, 0.7, 0], b: [0, 1.5, 0], r: R }, D = 0.1525, RISE = 0.04, N = 64, SOFT = 1e-3;
    const flat = 2 * D + TAU * RE, whole = sqrt(flat * flat + RISE * RISE), pitch = RISE * TAU * RE / flat, around = sqrt((TAU * RE) ** 2 + pitch ** 2), saved = around * LESS;
    // It starts wrong on purpose: all the rise in the first tail and a level turn, so it must slide to even the pitch.
    const path = s => s < D ? [RE, 1.1 + RISE * s / D, -D + s] : s < D + TAU * RE ? [RE * cos((s - D) / RE), 1.1 + RISE, RE * sin((s - D) / RE)] : [RE, 1.1 + RISE, s - D - TAU * RE];
    const pts = Array.from({ length: N + 1 }, (_, i) => path(i * flat / N));
    const ch = C.makeChain({ points: pts, masses: pts.map((_, i) => i === 0 || i === N ? Infinity : POINT), length: N * LK, compliance: SOFT, mu: 0, acc: pts.map(() => [0, 0, 0]) });
    let inside = Infinity, broke = '';
    try { steps(ch, 4, [cap], 5, t => { if (Math.round(t / H) % 50 === 0) inside = Math.min(inside, clearance(ch, cap).pt); }); } catch (e) { broke = e.message; }
    const P = ch.points(), on = ch.touching().map(c => c.i), length = P.slice(1).reduce((s, p, i) => s + len(sub(p, P[i])), 0);
    if (broke || on.length < 3) ok('one turn round a capsule, pulled tight: the line lies on the capsule', false, broke || `${on.length} points touch`);
    else {
      // The first and last links of the turn run from a point off the surface, so each is short of a
      // full link's saving, and the place a tail meets the capsule is known to about as much: four
      // times one link's saving, 0.014 mm each, is 0.06 mm.
      const fine = 4 * LK * LESS;
      ok(`one turn round a capsule, pulled tight: the whole line is the straight way round, unrolled, less the ${mm(saved, 2)} its links save (within ${mm(fine, 2)})`, abs(length - (whole - saved)) < fine, `${mm(length)} against ${mm(whole - saved)}`);
      // Where each tail meets the capsule: the nearest point to the axis on the line from the held end
      // through the last point of that tail that is off the surface.
      const meet = (p, q) => { const d = sub(q, p), k = -(p[0] * d[0] + p[2] * d[2]) / (d[0] * d[0] + d[2] * d[2]); return len(d) * k; };
      const wrapped = length - meet(P[0], P[on[0] - 1]) - meet(P[N], P[on[on.length - 1] + 1]);
      ok(`the length round the capsule is 2*pi*(r + line) with the pitch, less the same saving (within ${mm(fine, 2)}; the pitch adds 0.65 mm)`, abs(wrapped - (around - saved)) < fine, `${mm(wrapped)} against ${mm(around - saved)} (without the pitch ${mm(TAU * RE * (1 - LESS))})`);
      // The points that touch go once round, less what the line turns at the first and the last of
      // them beyond their own links, which is up to half a link's angle each, either way.
      const ang = []; let swept = 0;
      on.forEach((i, k) => { if (k) { let d = Math.atan2(P[i][2], P[i][0]) - Math.atan2(P[on[k - 1]][2], P[on[k - 1]][0]); d -= TAU * Math.round(d / TAU); swept += d; } ang.push(swept); });
      ok('the points that touch go once round the capsule, to within one link angle (10.6 degrees)', abs(swept - TAU) < TURN && on[on.length - 1] - on[0] === on.length - 1, `${deg(swept)} over ${on.length} points in a row`);
      // On a helix height rises evenly with the angle, and so it does here for every point that touches:
      // each is in balance between two links of the turn. 0.005 mm is for what is left of the settling.
      const slope = (P[on[on.length - 1]][1] - P[on[0]][1]) / swept, uneven = Math.max(...on.map((i, k) => abs(P[i][1] - P[on[0]][1] - slope * ang[k])));
      // The pitch itself: the turn carries up to 1 / cos(half a link angle) = 0.43% more pull than the
      // tails (the first and last points again), so it climbs that much less steeply, and its links
      // span 0.14% less: 0.6% of 21.06 mm is 0.12 mm.
      ok('at an even pitch, as a helix (heights within 0.005 mm of even; the pitch within 0.12 mm of the smooth line\'s)', uneven < 5e-6 && abs(slope * TAU - pitch) < 0.00012, `${on.length} points, ${mm(uneven, 4)} off, pitch ${mm(slope * TAU)} against ${mm(pitch)}`);
      // Nothing grips along the axis, so the part of the pull that lies along it is the same in a tail and in the turn. 0.05% for the settling.
      const along = j => ch.tension(j) * (P[j + 1][1] - P[j][1]) / len(sub(P[j + 1], P[j])), tail = along(2), turn = along(N / 2);
      ok('with no friction the pull along the capsule\'s axis is the same in the tail and in the turn (within 0.05%)', abs(turn / tail - 1) < 0.0005, `${tail.toFixed(5)} N and ${turn.toFixed(5)} N`);
      // The tension is what stretches it from its rest length to the way round. Links and contact
      // being solved one after the other lets the line give substep^2 * turn * link angle / point mass
      // for each newton (0.17 mm), on top of its own 1 mm. 3% for the two end points of the turn.
      const give = SUB * SUB * TAU * TURN / POINT, pull = (whole - saved - N * LK) / (SOFT + give), mid = ch.tension(N / 2);
      ok('and its tension is the stretch over its give (within 3%)', abs(mid / pull - 1) < 0.03, `${mid.toFixed(3)} N against ${pull.toFixed(3)} N (${mm(give)} of give per newton from the solver)`);
    }
    ok('no point is inside the capsule (1e-6 mm)', inside - RE > -1e-9, mm(inside - RE));
  }
  {
    // Sliding off the round end. A capsule lying level, smooth, the chain draped over it with equal
    // loads, 200 mm from the seam where the side meets the round end, and every point pushed along
    // the axis at 0.5 m/s2 (the same as tipping the capsule 2.9 degrees). On the side nothing
    // resists, so it moves as 1/2 * a * t^2 and reaches the seam at sqrt(2 * 0.2 / 0.5) = 0.894 s,
    // at 0.45 m/s. Slow enough to stay on: over the round end the circle it lies on shrinks, the
    // loads sink, and at that speed they need 5.8 m/s2 to keep up, which gravity gives.
    const cap = { a: [-0.3, Y0, 0], b: [0.1, Y0, 0], r: R }, AX = 0.5, N = 55, { pts } = drape(N, 19, PI, -0.1);
    const ch = C.makeChain({ points: pts, masses: loads(N), length: N * LK, mu: 0, acc: pts.map(() => [AX, -G, 0]) });
    const total = loads(N).reduce((a, b) => a + b, 0), com = () => { const c = [0, 0, 0]; for (let i = 0; i <= N; i++) { const p = ch.pos(i), m = ch.masses[i] / total; c[0] += p[0] * m; c[1] += p[1] * m; c[2] += p[2] * m; } return c; };
    const vx = () => { let s = 0; for (let i = 0; i <= N; i++) s += ch.vel(i)[0] * ch.masses[i] / total; return s; };
    let at06 = null, lag = 0, inside = Infinity, last = null, back = false, seam = null, dome = 0, offDome = 0, reach = 0, gain = 0;
    const free = [];
    for (let i = 1; i <= Math.round(1.25 / H); i++) {
      ch.step(H, [cap]);
      const t = i * H, touching = ch.touching(), top = ch.pos(27)[0];
      if (abs(t - 0.6) < H / 2) at06 = top + 0.1;
      if (seam === null && top >= cap.b[0]) seam = t;
      // Points that touch beyond the seam are on the round end: they must sit r + line from its centre.
      const past = touching.filter(c => ch.pos(c.i)[0] > cap.b[0] + 1e-6);
      if (past.length) { dome++; last = t; for (const c of past) { const p = ch.pos(c.i); offDome = Math.max(offDome, abs(len(sub(p, cap.b)) - RE)); reach = Math.max(reach, p[0] - cap.b[0]); } gain = vx() - AX * t; }
      if (touching.length && t > 1.05) back = true;
      if (last === null || t < last + 0.02) { lag = Math.max(lag, AX * t - vx()); inside = Math.min(inside, clearance(ch, cap).pt); }
      if (t > 1.1) free.push(com());
    }
    // 1/2 * 0.5 m/s2 * (0.6 s)^2 = 90 mm. The step adds the push before it moves, so it runs half a substep's worth ahead: 0.03 mm.
    ok('on the side of a capsule it slides as 1/2 * a * t^2: 90 mm after 0.6 s (within 0.1 mm)', at06 !== null && abs(at06 - 0.09) < 0.0001, at06 !== null && mm(at06));
    ok('it crosses the seam when that says, at 0.894 s (within 1 ms)', seam !== null && abs(seam - sqrt(0.8)) < 0.001, seam !== null && seam.toFixed(4) + ' s');
    // A point can lift off within the step it touched in, so one named as touching may be up to 0.01 mm off
    // the surface; on a capsule with no round end it would be 0.9 mm out when 10 mm past the seam.
    ok('past the seam it rides the round end: every point that touches is r + line from the end\'s centre (within 0.01 mm), for 30 steps or more', dome >= 30 && offDome < 1e-5, `${dome} steps, ${mm(offDome, 4)} off at most`);
    // The round end leans away, so it can only push the line on. Not snagging: its speed along the axis
    // is never under a * t (to 1 mm/s). And pushed on it is: it ends ahead of a * t. By how much is not
    // known on paper; on the side of the capsule it keeps to a * t within 0.001 mm/s, so 0.1 mm/s is no accident.
    ok('it never loses speed on the seam or the round end (to 1 mm/s), and the round end pushes it on (it leaves over 0.1 mm/s ahead)', dome > 0 && lag < 0.001 && gain > 0.0001, `${(lag * 1000).toFixed(3)} mm/s behind at worst, ${(gain * 1000).toFixed(1)} mm/s ahead when it leaves`);
    // It must leave before the tip: there the circle it lies on shrinks to nothing and the loads would
    // have to fall without limit. Where exactly is not known on paper. A capsule with no round end
    // would carry it the whole 54 mm, to 1.008 s.
    ok('it leaves the round end before its tip, 54 mm past the seam (by 10 mm or more), and after the seam', last !== null && reach < RE - 0.01 && last > sqrt(0.8), last !== null && `last touch at ${last.toFixed(4)} s, ${mm(reach, 1)} past the seam`);
    ok('no point is inside the capsule on the way (1e-6 mm)', inside - RE > -1e-9, mm(inside - RE));
    // Free of the capsule, the centre of mass has the acceleration every point is given, (0.5, -9.81, 0): its second difference over 0.1 s.
    const k = Math.round(0.05 / H), a = free.length > 2 * k + 1 ? [0, 1, 2].map(j => (free[2 * k][j] - 2 * free[k][j] + free[0][j]) / (k * H) ** 2) : null;
    ok('once off the end it touches nothing again and falls freely (acceleration within 0.01 m/s2)', !back && a && hypot(a[0] - AX, a[1] + G, a[2]) < 0.01, a && a.map(v => v.toFixed(4)).join(', '));
  }

  // ---------- 6. too fast to stop? ----------
  {
    // A loose 10 g line thrown broadside across a part, meeting it at its middle or a third of the
    // way along, with friction 0, 0.3 and 0.6. Two parts: the thinnest of the body (the shin above
    // the ankle, 45 mm) and one as thin as a nunchaku handle (22.5 mm), which the engine does not
    // pass a line yet. Three speeds: 9 m/s, the fastest a hand may move in a clip; 16.9 m/s, the
    // fastest a prop may (test/physics.js; the clips' own fastest are 6.1 and 16.6 m/s); and the
    // two together, 25.9 m/s. A link is through a part when it is nearer the axis than the surface,
    // less what its middle cuts in: at the blow a point is stopped dead while the next still flies
    // for one substep, so a link can be longer by that much, (link + speed * substep)^2 / (8 * radius).
    const throws = (r, V) => {
      const cap = { a: [-0.15, 1, 0], b: [0.15, 1, 0], r }, RH = r + RHO, bound = (LK + V * SUB) ** 2 / (8 * RH), N = 55, out = [];
      for (const mu of [0, 0.3, 0.6]) for (const cross of [0.5, 1 / 3]) {
        const pts = Array.from({ length: N + 1 }, (_, i) => [0, 1 + (i - N * cross) * LK + 0.003, -0.1]);
        const ch = C.makeChain({ points: pts, masses: pts.map(() => POINT), length: N * LK, mu, acc: pts.map(() => [0, 0, 0]), velocities: pts.map(() => [0, 0, V]) });
        const o = { mu, cross, near: Infinity, deep: 0, pulled: 0, stopped: '', pushed: 0, told: 0, through: false };
        try {
          for (let i = 0; i < Math.round(0.08 / H); i++) {
            ch.step(H, [cap]);
            o.near = Math.min(o.near, clearance(ch, cap).link - RH); o.deep = Math.max(o.deep, ch.deepest()); o.pulled = Math.max(o.pulled, ch.strain());
            if (ch.deepest() > 0) o.pushed++;
            if (ch.touching().length) o.told++;
          }
        } catch (e) { o.stopped = e.message; }
        o.through = !(o.near > -bound);
        out.push(o);
      }
      return { out, bound, RH };
    };
    for (const [r, what] of [[0.045, 'the thinnest part of the body (45 mm)'], [E.ROD, 'a part as thin as a handle (22.5 mm)']]) {
      const { out, bound, RH } = throws(r, 9), near = Math.min(...out.map(o => o.near)), deep = Math.max(...out.map(o => o.deep)), pulled = Math.max(...out.map(o => o.pulled));
      // A point that goes in less than the part's radius comes out the side it went in; half the radius leaves a margin of two.
      ok(`a line thrown at 9 m/s across ${what} folds round it, six ways: no link is through it (${mm(bound)} allowed for the blow), no point goes in beyond half the radius, and the pull stays under the limit`,
        out.every(o => !o.stopped && !o.through) && deep < RH / 2 && pulled < 66.7, `nearest a link comes to the surface ${mm(near)}, deepest point ${mm(deep, 1)} of ${mm(RH, 1)}, most pull ${pulled.toFixed(1)} N of 66.7 N`);
      // A glancing touch: every step in which a point was pushed out of the part is a step in which touching() names one.
      ok('and in every step in which a point is pushed out of the part, the chain says what touched (a glancing touch is not missed)', out.every(o => o.pushed > 0 && o.told === o.pushed), out.map(o => `${o.told} of ${o.pushed}`).join(', '));
      // Faster, the blow can pull the line past what its chain can follow. Then step() throws, and what
      // matters is that it does so before it has handed back a line that is through the part.
      const fast = [...throws(r, 16.9).out, ...throws(r, 25.9).out], stopped = fast.filter(o => o.stopped).length;
      ok(`at 16.9 and 25.9 m/s, twelve ways, the line either folds round it or is stopped by its limit; none is ever handed back through it`, fast.every(o => !o.through) && fast.every(o => !o.stopped || /more than its chain can follow/.test(o.stopped)), `${stopped} of 12 stopped, nearest a link comes to the surface ${mm(Math.min(...fast.map(o => o.near)))}`);
      if (r === E.ROD) {
        const hard = throws(r, 25.9).out.filter(o => o.mu !== 0.3);
        ok('at 25.9 m/s with friction 0 and 0.6 the limit is what stops it: all four end in its error', hard.length === 4 && hard.every(o => /more than its chain can follow/.test(o.stopped)), hard.map(o => o.stopped ? 'stopped' : 'ran on').join(', '));
      }
    }
    // The limit and a snap. A stiff line stops a head that is moving away from the hand in a single
    // substep, which is a pull of head * speed / substep: 65 N at 0.12 m/s, 238 N at 0.44 m/s (the
    // speed of a 10 mm fall). So a stiff tether takes the first and is stopped at the second. A line
    // with a little give takes the same blow as a spring does: at 0.1 mm a newton the pull peaks at
    // m*g + sqrt((m*g)^2 + m * v^2 / give) = 14.1 N, less the pi * (substep / sqrt(m * give)) / 4 =
    // 4.3% that this way of solving a spring takes off in a quarter of its swing: 13.5 N. 3% for
    // the tether's own 10 g, which shares the blow.
    const snap = (v, soft) => {
      const pts = Array.from({ length: 56 }, (_, i) => [0, 1.6 - i * LK, 0]), masses = pts.map((_, i) => i === 0 ? Infinity : i === 55 ? 0.09 : 0.01 / 54);
      const ch = C.makeChain({ points: pts, masses, length: 55 * LK, compliance: soft, velocities: pts.map((_, i) => [0, i === 55 ? -v : 0, 0]) });
      let peak = 0;
      try { steps(ch, 0.05, [], 0, () => { peak = Math.max(peak, ch.strain()); }); } catch (e) { return { stopped: e.message }; }
      return { peak, speed: len(ch.vel(55)) };
    };
    const soft = 1e-4, mg = 0.09 * G, spring = (mg + sqrt(mg * mg + 0.09 * 0.44 * 0.44 / soft)) * (1 - PI * SUB / sqrt(0.09 * soft) / 4);
    const gentle = snap(0.12, 0), hard = snap(0.44, 0), sprung = snap(0.44, soft);
    ok('a stiff tether stops a head moving away at 0.12 m/s (65 N for a substep, under its limit) and is stopped by the limit at 0.44 m/s (238 N)', !gentle.stopped && /more than its chain can follow/.test(hard.stopped || ''), `${gentle.stopped || 'took the first'}; ${hard.stopped || 'took the second too'}`);
    ok('given 0.1 mm of stretch a newton it takes the 0.44 m/s as a spring does: the pull peaks at 13.5 N (within 3%)', !sprung.stopped && abs(sprung.peak / spring - 1) < 0.03, sprung.stopped || `${sprung.peak.toFixed(2)} N against ${spring.toFixed(2)} N`);
    // A position that is not a number must stop it too, not pass as no pull at all.
    const pts = Array.from({ length: 6 }, (_, i) => [0, 1.5 - i * LK, 0]), ch = C.makeChain({ points: pts, masses: pts.map((_, i) => i ? POINT : Infinity), length: 5 * LK });
    ch.step(H); ch.set(3, [NaN, NaN, NaN]);
    let msg = ''; try { ch.step(H); } catch (e) { msg = e.message; }
    ok('a point that is not a number stops the chain with the same error, and reads as a pull without end', /more than its chain can follow/.test(msg) && ch.strain() === Infinity, msg || 'it ran on');
  }

  // ---------- 7. the same bytes ----------
  {
    // 1.5% over the limit: it sticks while the pull rises, then slips.
    const { k, psi } = drape(55, 19, PI), pull = polygon(0.5, k, psi) * 1.015, one = capstan(0.5, PI, pull, { hash: true }), two = capstan(0.5, PI, pull, { hash: true }), keys = capstan(0.5, PI, pull, { hash: true, keys: true });
    ok('the same run twice gives the same bytes (every point, every step, 1.5 s of sticking, then slipping)', one.hash && one.slides && one.hash === two.hash, `${one.hash}, slipped ${mm(one.travel)} in the last 0.6 s`);
    ok('and so does a capsule whose fields are written in another order', one.hash === keys.hash, keys.hash);
  }

  // ---------- 8. a capsule that moves ----------
  {
    // The capstan again on a cylinder that travels at a steady 2 m/s along its axis and 1.5 m/s
    // across it, the chain starting with it. Steady travel changes nothing, so it must hold and
    // slide at the same pulls, which it only does if friction is taken against the cylinder's
    // own motion where the point touches.
    const mu = 0.5, wrap = PI, { k, psi } = drape(55, 19, wrap), limit = polygon(mu, k, psi), move = [2, 0.9, 1.2], low = NEAR + give(limit);
    const under = capstan(mu, wrap, limit * (1 - low), { move }), over = capstan(mu, wrap, limit * (1 + NEAR), { move });
    ok(`on a cylinder moving at ${len(move).toFixed(1)} m/s the chain still holds ${pct(low)} under the limit, as it does on one that is still`, !under.slides, `end moved ${mm(under.travel)} against the cylinder`);
    ok(`and still slides ${pct(NEAR)} over it`, over.slides, `end moved ${mm(over.travel)}`);
    // It rides along: it has not been left behind along the axis (2 m/s for 1.5 s is 3 m).
    ok('it is carried along the axis with the cylinder (within 0.1 mm over 3 m)', under.lift < 1e-4, mm(under.lift));
    // The check has teeth: told nothing of the motion, the engine sees the surface rushing past and the chain is dragged off.
    const blind = capstan(mu, wrap, limit * (1 - low), { move, blind: true });
    ok('told the cylinder is still when it is not, the same chain does not hold (so the check above means something)', blind.slides || blind.lift > 0.01, `end moved ${mm(blind.travel)}, left behind ${mm(blind.lift)}`);
  }
  {
    // A cylinder that speeds up: rising at 3 m/s2, it must carry the loads at m * (g + a).
    const A = 3, N = 55, { pts } = drape(N, 19, PI), acc = pts.map((_, i) => i === 0 || i === N ? [0, -G, 0] : [0, 0, 0]);
    const ch = C.makeChain({ points: pts, masses: loads(N), length: N * LK, mu: 0.5, acc });
    const capAt = t => ({ a: [CYL.a[0], Y0 + A * t * t / 2, 0], b: [CYL.b[0], Y0 + A * t * t / 2, 0], r: R });
    let was = capAt(0), sum = 0, k = 0, slip = 0;
    for (let i = 1; i <= Math.round(1 / H); i++) {
      const t = i * H, now = capAt(t);
      ch.step(H, [now], [was]); was = now;
      if (t > 0.8) { sum += ch.tension(0); k++; }
      slip = ch.pos(0)[1] - (Y0 - 0.19 + A * t * t / 2);
    }
    // The start is sudden and rings for about 0.1 s; by 0.8 s that is gone. 0.5% for the mean over the last 0.2 s.
    ok('a cylinder rising at 3 m/s2 carries a load at m * (g + a) (within 0.5%)', abs(sum / k / (LOAD * (G + A)) - 1) < 0.005, `${(sum / k).toFixed(4)} N against ${(LOAD * (G + A)).toFixed(4)} N`);
    // The line gives 0.09 mm at rest and (g + a) / g of that here, 0.12 mm, and the capsule is taken to move
    // evenly within a step, which leaves it half a step's gain in speed behind: a * step^2 / 8, far less. 0.3 mm.
    ok('and the load rises with it (within 0.3 mm after 1.5 m)', abs(slip) < 0.0003, mm(slip));
  }

  // ---------- 9. the engine's swings with the chain in place of the stiff link ----------
  {
    // A wheel-plane poi spin, settled and recorded as any clip is, with the chain as its tether.
    // The checks the suite makes on a clip: the tether's length (1.5 mm), never slack, the seam (10 mm).
    const REV = 0.7, at = [-0.42, 1.0, 0.3];
    let clip = null, why = '';
    try { clip = E.loopClip({ bs: E.PB.wheel, s: E.FWD, cycle: TAU, want: TAU / REV, r0: L + 0.09, at: () => at, pose: th => E.Demo.v.add(at, E.Demo.v.mul(E.spoke(E.PB.wheel, th), 0.09)), chain: {} }, E.makeMass, 'poi'); } catch (e) { why = e.message; }
    if (!clip) ok('a poi spin settles into a loop with the chain as its tether', false, why);
    else {
      const t = clip.fr.map(f => len(sub(f.P, f.h))), far = Math.max(...t), near = Math.min(...t);
      ok('a poi spin settles into a loop with the chain as its tether, the head a tether from the hand (within 1.5 mm, never slack)', far < L + 1.5e-3 && near > L * 0.97 && clip.closure < 0.01, `${mm(near)} to ${mm(far)}, seam ${mm(clip.closure)}`);
      // The hand holds the rate it is asked for, as it does with the stiff tether (0.700 s a turn). 2% for the 10 g of line it also carries.
      ok('at the rate the hand is asked for, 0.7 s a turn (within 2%), touching nothing', abs(clip.dur / REV - 1) < 0.02 && clip.contacts.length === 0, `${clip.dur.toFixed(3)} s a turn, ${clip.contacts.length} contacts`);
    }
  }
  {
    // The nunchaku's cord as a chain, the striker on its end.
    let clip = null, why = '';
    try { clip = E.loopClip({ ...E.cone({ plane: 'wall', dir: 1, ...E.NORMAL }), chain: {} }); } catch (e) { why = e.message; }
    if (!clip) ok('a nunchaku swing settles into a loop with the chain as its cord', false, why);
    else {
      const add = E.Demo.v.add, mul = E.Demo.v.mul;
      const cord = Math.max(...clip.fr.map(f => len(sub(f.B, add(f.h, mul(f.d, E.GRIP)))))), stick = Math.max(...clip.fr.map(f => abs(len(sub(f.T, f.B)) - E.STICK)));
      ok('a nunchaku swing settles into a loop with the chain as its cord: cord and stick their lengths (1.5 mm and 1 mm, as the suite asks)', cord < E.LINK + 1.5e-3 && stick < 1e-3 && clip.closure < 0.01, `cord ${mm(cord)}, stick off ${mm(stick)}, seam ${mm(clip.closure)}`);
      ok('at the rate the hand is asked for, 0.56 s a turn (within 2%), touching nothing', abs(clip.dur / E.REV - 1) < 0.02 && clip.contacts.length === 0, `${clip.dur.toFixed(3)} s a turn, ${clip.contacts.length} contacts`);
    }
  }
  {
    // What the engine lets a line touch. A still hand held overhead, the tether hanging from it past
    // the forearm that holds it up. As the body checks do, the engine leaves the holding arm out
    // unless it is asked for with `own`: then the line must lie outside that arm and the engine's
    // contact list must say so, which is what the suite reads to tell that a clip touched something.
    const c = [-0.3, 1.9, 0.1], arm = E.bodyAt(c).filter(b => b.side === 'R' && /arm/.test(b.name));
    const hang = own => {
      const sim = E.makeMass({ bs: { n: [-1, 0, 0], a: [0, -1, 0], b: [0, 0, 1] }, s: 1, want: 0, cycle: TAU, r0: L, at: () => c, pose: () => c, chain: { own } });
      for (let i = 0; i < 750; i++) sim.step(0.3);
      const P = sim.line.points().slice(0, -1); // the head keeps its own contact
      return { names: [...new Set(sim.contacts.map(x => x.name))], gap: Math.min(...arm.map(b => Math.min(...P.map(p => off(p, b) - b.r - RHO)))) };
    };
    const without = hang(false), withArm = hang(true);
    ok('through the engine, a tether hanging past the arm that holds it passes through that arm and is not listed as touching', without.gap < -0.001 && !without.names.some(x => /^tether on/.test(x)), `${mm(without.gap, 1)} from the arm's surface, contacts: ${without.names.join(', ') || 'none'}`);
    ok('asked for its own arm, it lies outside it (to 0.001 mm) and the engine lists the touch', withArm.gap > -1e-6 && withArm.names.includes('tether on the forearm'), `${mm(withArm.gap, 1)} from the arm's surface, contacts: ${withArm.names.join(', ') || 'none'}`);
  }

  // ---------- 10. what it costs ----------
  {
    // One simulated second of the same poi spin with the stiff tether and with the chain, as a clip
    // runs it (the body rebuilt every step). The check is the ratio, which does not depend on the
    // machine; the milliseconds are printed for the record. Where the ratio's limit comes from: the
    // suite took 43 s on the machine this was written on and takes about 150 s on the Pi (3.5 times),
    // and the epic allows 240 s there. A stiff poi clip settles and records in about 33 simulated
    // seconds, 1.6 s on the Pi; six chain clips in 60 s of what is left is 10 s each, 6 times that.
    // Six is not what the chain is hoped to carry (about 30 skills): see docs/demo-engine.md.
    const at = [-0.42, 1.0, 0.3], dr = chain => ({ bs: E.PB.wheel, s: E.FWD, cycle: TAU, want: TAU / 0.7, r0: L + 0.09, at: () => at, pose: th => E.Demo.v.add(at, E.Demo.v.mul(E.spoke(E.PB.wheel, th), 0.09)), chain });
    const cost = chain => { let best = Infinity; for (let r = 0; r < 3; r++) { const sim = E.makeMass(dr(chain)), t0 = process.hrtime.bigint(); for (let i = 0; i < 1500; i++) sim.step(0.3); best = Math.min(best, Number(process.hrtime.bigint() - t0) / 1e6); } return best; };
    const stiff = cost(null), chained = cost({});
    ok('a simulated second of a poi spin with the chain costs under 6 times one with the stiff tether', chained / stiff < 6, `${(chained / stiff).toFixed(1)} times: ${chained.toFixed(1)} ms against ${stiff.toFixed(1)} ms on this machine`);
  }
  return { n, bad, skipped, thresholds };
}

module.exports = { run };
if (require.main === module) {
  const r = run();
  console.log(`${r.n} checks, ${r.bad} failed, ${r.skipped} skipped`);
  process.exit(r.bad ? 1 : 0);
}
