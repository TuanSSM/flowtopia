'use strict';
// The list of moves that have a demonstration, and how each one is made. A move is listed
// here only once its clip passes test/physics.js.
const E = require('./engine.js');
const { Demo, TAU, WANT, GRIP, TETHER, ARM, SHOULDER, ISO, STAFF, SHORT, SHORT_SIDE, STAFF_AT, STAFF_BS, BASIS, CENTRE, PB, FWD, BACK, NORMAL, POINT, PIVOT, IDLE,
  spoke, cone, eightWall, eightTall, loopClip, stopClip, memo, herm, aerialClip, bounceClip, staffToss, drawn, turn, poiSpin, poiExtension, poiIsolation, poiPendulum,
  poiFlower, rotor, staffRotor, staffIsolation, staffAntispin, buugengAt, buugengSpin } = E;
const M = require('./moves.js'), { fitted, elbowAt, BODY, figure8, handRotor, unit, palmSpin, buugengIsolation, roll, carriedClip, weave, poiCarried, chuckCarried, solved, twoArm, still, picture, line, fistAt, ballAt } = M;
const { add, sub, mul, dot, cross, len, norm, mix, rot, clamp, smooth, flip } = Demo.v, { sin, cos, sqrt, PI } = Math;

const circle = (n, plane, dir, x) => { const o = { plane, dir, ...NORMAL, ...x }; return { n, quads: x && x.showPlane ? quad(add(CENTRE[plane], mul(BASIS[plane].n, GRIP * cos(o.gamma))), BASIS[plane], 0.58) : undefined, make: memo(() => loopClip(cone(o))), ...x }; };
// Two props: the left hand mirrors the right clip, or a second clip, together or split in time.
const pair = (n, make, left, split, shift) => ({ n, make, pair: { left: left ? { make: left } : null, time: split ? 'split' : 'together', opposite: !!left, shift } });
const modes = (same, other) => [pair('Together, same direction', same, null, false), pair('Split, same direction', same, null, true), pair('Together, opposite direction', same, other, false), pair('Split, opposite direction', same, other, true)];
const KNUCKLES = [0, 0, 0.1]; // in a butterfly the tethers leave from the front of the fists, clear of the wrists behind
const P_SIDE = [-0.3, 1.0, 0.12], P_FRONT = [-0.2, 1.2, 0.5], P_HANDS = [-0.12, 1.15, 0.35], P_TOP = [-0.1, 1.93, 0.05];
// A clip with the elbow told where to go in every frame (the composer mirrors it for the left hand).
const withElbow = (make, e) => memo(() => { const c = make(); c.fr.forEach(f => { f.e = e; }); return c; });
const BUTTERFLY_ELBOW = [-0.3, 1.1, 0.18]; // elbows out at the sides, so the forearms lie in front of the belly, not through it
const sideF = poiSpin('wheel', P_SIDE, FWD), sideB = poiSpin('wheel', P_SIDE, BACK), fly = withElbow(poiSpin('wall', P_HANDS, 1, 0.7, KNUCKLES), BUTTERFLY_ELBOW), flyR = withElbow(poiSpin('wall', P_HANDS, -1, 0.7, KNUCKLES), BUTTERFLY_ELBOW);
const hd = (n, make, x) => ({ n, make, ...x });
// Staff. A rotor is kept turning hand over hand; a figure-8 is turned by one wrist.
const ROTOR = { front: [0, 1.25, 0.46], top: [0, 1.9, 0.05] }; // in front of the chest, the forearms clear of it and within reach
const rotorAt = (where, dir, dur = 1.4) => handRotor({ half: STAFF, c: ROTOR[where], bs: STAFF_BS[where], dir, dur, right: where === 'top' ? 90 : 0 });
// A figure-8 changes side when the prop is square to the forearm, and leans most when it lies along it.
// With the arm stretched forward that is when the prop is upright; with the forearm upright, when it is level.
const WHEEL8 = { n: [-1, 0, 0], a: [0, 0, 1], b: [0, 1, 0] }, WALL8 = { n: [0, 0, 1], a: [-1, 0, 0], b: [0, 1, 0] };
const HANDS = { x: [-0.3, -0.38, -0.46], y: [1.15, 1.3, 1.45, 1.58], z: [0.3, 0.42, 0.54], gamma: [1.0, 1.15, 1.3], elbow: ['auto', 'under', 'out', 'behind', 'low'] };
const eight = (name, base, grid) => fitted(name, { ...HANDS, ...grid }, o => { const c = [o.x, o.y, o.z]; return figure8({ ...base, c, gamma: o.gamma, flip: o.flip, sway: o.sway, elbow: elbowAt(o.elbow, c) }); });
// The long staff circles on each side of the body, so the hand travels across; the search covers how far.
const staff8 = dir => eight('staff8' + dir, { kind: 'staff', half: STAFF, ...WHEEL8, dir, dur: 1.9 }, { x: [-0.04, -0.1], sway: [0.2, 0.26], flip: [PI / 2, 0, PI] });
// Double staff: the sources say each staff changes side when it is level.
const short8 = (dir, lobes = 1) => eight('short8' + dir + lobes, { kind: 'staff', half: SHORT, ...WHEEL8, dir, lobes, dur: 1.5 }, { flip: [0, PI] });
const wall8 = dir => eight('wall8' + dir, { kind: 'staff', half: SHORT, ...WALL8, dir, dur: 1.5 }, { x: [-0.46, -0.52], flip: [0, PI] });
const reel = (c, lobes, elbow) => figure8({ kind: 'staff', half: SHORT, c, ...WALL8, dir: 1, gamma: 0, stay: true, lobes, dur: 1.6, elbow: () => elbow }); // flat in its plane: a lean would send the inner end into the belly
const SH = [-0.26, 1.43, 0], OUT = 0.08; // the circle a little out from the shoulder, so the arm clears the hips; the staff rests on a 6 cm arm // unit-circle moves turn about the shoulder, with the staff just outside the arm
// The elbow bends within the plane of the arm circle, so the arm stays beside the staff.
const inPlane = (c, bs, dir) => th => add(c, mul(spoke(bs, th - dir * PI / 2), 0.6));
const circleMove = (k, dir, x) => unit({ half: SHORT, c: SH, bs: PB.wheel, R: 0.5, k, dir, out: OUT, elbow: inPlane(SH, PB.wheel, dir), ...x });
const s8F = short8(FWD), s8B = short8(BACK), extF = circleMove(1, FWD), antiF = circleMove(-1, FWD), antiB = circleMove(-1, BACK);
const isoF = staffIsolation(FWD), isoB = staffIsolation(BACK), anti1 = staffAntispin(FWD);
// Buugeng are turned by the wrist in the same two-sided way.
const bu8 = dir => eight('bu8' + dir, { kind: 'buugeng', ...WHEEL8, dir, dur: 1.4 }, { flip: [PI / 4, 3 * PI / 4, 5 * PI / 4, 7 * PI / 4, PI / 2] });
const buF = bu8(FWD), buB = bu8(BACK), BU_ISO = [-0.28, 1.3, 0.45];
const quad = (c, bs, k) => [{ c, a: bs.a, b: bs.b, k }];
const APART = [0, 0, 0.11]; // butterfly planes sit a little apart, so the heads pass each other
// Throws. The nunchaku settings come from a search for a release that turns the handle once
// and brings it back within reach (test/search-throws.js); the simulation repeats exactly.
// A flight is sensitive to tiny numerical differences, so each throw has a short list of
// releases that worked in the search; the first one that ends in a clean catch is used.
const firstCatch = (base, list) => memo(() => { for (const [dir, phase, lift, across] of list) { const c = aerialClip({ ...base, dir, phase, lift, across }); if (c) return c; } return null; });
const aerial = firstCatch({ turns: 1 }, [[1, 0, 2.4, 0], [-1, Math.PI, 2.4, 0], [1, 0, 2.7, 0], [1, 0, 3.0, 0]]);
const aerialAcross = firstCatch({ turns: 1, other: true }, [[1, 0, 2.1, 0.8], [1, 0, 2.1, 1.0], [1, 0, 2.4, 1.0], [1, 0, 2.4, 0.8], [1, 0, 2.4, 0.6]]);
const swingOn = memo(() => loopClip(cone({ plane: 'wall', dir: 1, ...NORMAL })));
const tossSide = memo(() => staffToss({ half: SHORT, c: SHORT_SIDE, bs: PB.wheel, dir: FWD, rate: 1.25, rise: 0.3 }));
const tossAcross = memo(() => staffToss({ half: SHORT, c: [-0.22, 1.15, 0.4], bs: BASIS.face, dir: 1, rate: 1.25, rise: 0.3, other: true }));
const tossLong = memo(() => staffToss({ half: STAFF, c: [-0.45, 1.1, 0.1], bs: PB.wheel, dir: FWD, rate: 1.1, rise: 0.3 }));
// Poi patterns from poi theory: two turning arms give the head's path, and the hand is solved for.
const FLOWER_AT = [-0.36, 1.43, 0], flower = (k, dir = FWD) => solved(1.8, twoArm(FLOWER_AT, PB.wheel, 0.4, k, dir));
const CAT_AT = [-0.02, 1.25, 0.4], cateye = (dir, phase, dur = 0.85, R = 0.2) => solved(dur, twoArm(CAT_AT, PB.wall, R, -1, dir, phase));
const petalF = flower(-3, FWD), petalB = flower(-3, BACK);
const weaveOf = (n, dir, at = 1.0, x) => weave({ dir, n, A: 0.21, y: 1.16, z: 0.36, at, w: 0.05, lift: 0.04, ...x });
const P_WIDE = [-0.38, 1.05, 0.12]; // far enough out that a shifting body stays clear of the poi
// A plain spin at the side on a body that turns once: the head keeps its circle beside the
// turning body, and the hand is solved for.
const turningPoi = (dir, back) => solved(0.7 * 8, p => M.rotY(add(P_WIDE, mul(spoke(PB.wheel, dir * TAU * 8 * p), 0.62)), (back ? -1 : 1) * TAU * p), null, p => M.bodyKeys((back ? BODY.turnBack : BODY.turn)(p)));
const C_ = require('./compose.js');
// A two-hand pattern carried by a moving body: the left hand's clip is built mirrored, so it sees the opposite movement.
const carriedPair = (n, a, b, time, bodyR, bodyL, k, x) => ({ n, make: carriedClip(a, bodyR, k), pair: { left: { make: carriedClip(b, bodyL, k) }, lag: () => C_.lagFor(a(), b(), time !== 'split') / k, ...x } });
const swing = () => cone({ plane: 'wall', dir: 1, ...NORMAL });

/* ---------- still pictures ---------- */
const UPV = [0, 1, 0], OUTV = [0, 0, 1], O = [0, 0, 0];
// A nunchaku held upright in a fist, the other stick hanging from the cord.
function chuckGrip(grip, thumbUp, labels) {
  const top = [0, 0.12, 0], hang = [0.085, 0.1, 0.01];
  return picture([line([[0, -0.18, 0], top], 0.03, 'prop'), line([top, [0.035, 0.16, 0.005], hang], 0.007, 'link'), line([hang, add(hang, [0.01, -0.3, 0])], 0.03, 'prop'), fistAt(O, thumbUp ? UPV : [0, -1, 0], OUTV, grip)],
    { hide: true, cam: { at: [0.03, -0.03, 0], zoom: 4.4 }, labels });
}
// The wheel: the nunchaku turns about its link, which rests on one finger of each hand.
const WHEEL_AT = [0, 1.25, 0.52];
const wheel = dir => drawn('nunchaku', 0.6, p => { const e = spoke(PB.wheel, dir * TAU * p), at = k => add(WHEEL_AT, mul(e, k)); return { h: add(WHEEL_AT, [-0.06, 0, 0]), h2: add(WHEEL_AT, [0.06, 0, 0]), g: at(0.045 + GRIP), d: mul(e, -1), B: at(-0.045), T: at(-0.345) }; });
// A nunchaku placed on the figure for a still: g is the fist on the handle, d the handle's direction.
// Two sticks side by side with a hand on each, to show the grips of a pass.
const passGrips = (leftThumbUp, labels) => picture([line([[-0.06, -0.18, 0], [-0.06, 0.12, 0]], 0.03, 'prop'), line([[0.06, -0.18, 0], [0.06, 0.12, 0]], 0.03, 'prop'), line([[-0.06, 0.12, 0], [-0.03, 0.155, 0], [0.03, 0.155, 0], [0.06, 0.12, 0]], 0.007, 'link'),
  fistAt([-0.06, 0, 0], UPV, OUTV, 'closed'), fistAt([0.06, 0, 0], leftThumbUp ? UPV : [0, -1, 0], OUTV, 'closed', 0.014, 'L')], { hide: true, cam: { at: [0, -0.02, 0], zoom: 4.2 }, labels });
// Rolls. The forearm points forward, so the roll goes round it on the plane in front (the face plane).
const ROLL_AT = [-0.24, 1.15, 0.44], FWDV = [0, 0, 1], ROLL = { at: ROLL_AT, a: [-1, 0, 0], b: [0, 1, 0], elbow: [-0.26, 1.08, 0.1] };
// rho is the limb's radius there plus the stick's (the forearm is 5 cm thick at the wrist, 5.5 higher up).
const ROLL_ON = { hand: { off: [0, 0, 0], rho: 0.064 }, wrist: { off: [0, 0, -0.09], rho: 0.085 }, forearm: { off: [0, 0, -0.2], rho: 0.08 }, finger: { off: [0, 0, 0.17], rho: 0.03 }, thumb: { off: [0.07, 0.03, 0.1], rho: 0.03 } };
const rollOf = (pivot, seq, x) => memo(() => roll({ ...ROLL, ...ROLL_ON[pivot], seq, ...x }));
const once = (pivot, half, x) => rollOf(pivot, [{ swing: 1 }, { roll: half }, { swing: 2 }], x), chain = (pivot, halves, x) => rollOf(pivot, halves.flatMap(h => [{ swing: 1 }, { roll: h }]), { loop: true, ...x });
const RL = { contact: 1, fingers: 1 };
// Disconnected wheel: three turns of the wheel, with the left finger taken away for the middle one.
const looseWheel = memo(() => { const c = wheel(1)(), n = c.fr.length, fr = []; for (let i = 0; i < 3 * n; i++) { const f = c.fr[i % n], k = smooth(0.9, 1.1, i / n) * (1 - smooth(1.9, 2.1, i / n)); fr.push({ ...f, h2: add(f.h2, [0.12 * k, -0.06 * k, -0.1 * k]) }); } return { ...c, fr, dur: c.dur * 3, loop: false }; });
// Intercept: a finger roll on one hand; the other hand's finger comes in beside it and takes the roll over.
const intercept = memo(() => {
  const A = [-0.06, 1.2, 0.42], Bq = [0.06, 1.2, 0.42], W = TAU / 1.12, dt = 1 / 150, fr = [], total = 3.6;
  for (let t = 0; t < total; t += dt) {
    const k = smooth(1.4, 2.0, t), P = add(mix(A, Bq, k), [0, 0, 0.17]), d = spoke(PB.wall, W * t), m = add(P, mul(spoke(PB.wall, W * t + PI / 2), -0.03));
    const hb = sub(m, mul(d, 0.045)), ha = add(m, mul(d, 0.045)), inn = smooth(0.7, 1.3, t), out = smooth(2.1, 2.7, t);
    // The catching arm comes in from the side with its elbow out, so the forearm stays off the chest.
    fr.push({ h: add(A, [-0.1 * out, -0.12 * out, -0.14 * out]), h2: mix([0.26, 0.93, -0.04], Bq, inn), e2: mix([0.33, 1.1, 0.02], [0.34, 1.12, 0.22], inn), g: add(ha, mul(d, GRIP)), d: mul(d, -1), B: hb, T: sub(hb, mul(d, 0.3)) });
  }
  return { kind: 'nunchaku', method: 'roll', dur: total, loop: false, trail: 0.5, fr };
});
// Isolation: the head of the handle stays on one spot; the hand and the tail go round it, level, and the other stick hangs still below.
const ISO_Q = [-0.3, 1.22, 0.42];
const chuckIso = dir => drawn('nunchaku', 1.0, p => { const e = spoke(PB.flat, dir * TAU * p); return { h: add(ISO_Q, mul(e, GRIP)), g: add(ISO_Q, mul(e, GRIP)), d: mul(e, -1), B: add(ISO_Q, [0, -0.09, 0]), T: add(ISO_Q, [0, -0.39, 0]) }; });
const chuckOn = (g, d, B, T) => ({ prop: { kind: 'nunchaku', hand: 'R' }, keys: { g, d: norm(d), B, T } });
const CLOCK_AT = [-0.22, 1.2, 0.5], clock = [12, 3, 6, 9].map((n, i) => ({ at: add(CLOCK_AT, mul(spoke(PB.wall, PI / 2 + i * PI / 2), 0.5)), text: String(n), dx: 0, dy: i === 2 ? 0.04 : -0.04 }));
const STILLS = {
  nunchaku: {
    F1: [{ n: 'Front grip', make: chuckGrip('closed', true, [{ at: [0, 0.12, 0], text: 'link end', dx: -0.14, dy: -0.04 }, { at: [0.01, 0.045, 0.01], text: 'thumb toward the link', dx: -0.16, dy: 0.02 }, { at: [0, -0.18, 0], text: 'tail end', dx: -0.12, dy: 0.02 }]) },
      { n: 'Back grip', make: chuckGrip('closed', false, [{ at: [0, 0.12, 0], text: 'link end', dx: -0.14, dy: -0.04 }, { at: [0.01, -0.045, 0.01], text: 'thumb toward the tail', dx: -0.16, dy: 0.04 }, { at: [0.0, 0.04, 0.02], text: 'little finger toward the link', dx: 0.1, dy: 0.2 }]) }],
    A7: [{ n: 'Clock on the face plane', make: picture([chuckOn(CLOCK_AT, [0, 0, 1], add(CLOCK_AT, [0, 0.085, 0.14]), add(CLOCK_AT, [0, 0.3835, 0.17]))], { pose: { hR: CLOCK_AT }, quads: quad(add(CLOCK_AT, [0, 0, 0.15]), PB.wall, 0.56), labels: clock }) }],
    C1: [{ n: 'Trapped under the arm', contact: 1, make: picture([chuckOn([-0.2, 1.205, 0.265], [0.035, 0.105, -0.175], [-0.15, 1.29, 0.09], [-0.15, 1.25, -0.207])], { pose: { hR: [-0.2, 1.205, 0.265], eR: [-0.27, 1.1, -0.03] }, cam: { at: [-0.15, 1.25, 0], zoom: 2.2 }, labels: [{ at: [-0.165, 1.29, -0.06], text: 'striker pinned between arm and ribs', dx: -0.02, dy: -0.2 }, { at: [-0.2, 1.205, 0.265], text: 'hand keeps the handle in front', dx: 0.02, dy: 0.16 }] }) }],
    S2: [{ n: 'Over the shoulder', contact: 1, make: picture([chuckOn([-0.22, 1.52, 0.07], [0, 0.05, -0.19], [-0.22, 1.57, -0.12], [-0.22, 1.275, -0.175])], { pose: { hR: [-0.22, 1.52, 0.07], hL: [-0.1, 1.2, 0.3], eL: [0.3, 1.15, 0.1] }, labels: [{ at: [-0.22, 1.42, -0.15], text: 'striker hangs down the back', dx: -0.04, dy: -0.2 }, { at: [-0.12, 1.25, 0.24], text: 'other hand reaches across', dx: 0.1, dy: 0.12 }] }) }],
    S4: [{ n: 'Slick: same grip', make: passGrips(true, [{ at: [-0.06, 0.05, 0.02], text: 'thumb toward the link', dx: -0.1, dy: -0.22 }, { at: [0.06, 0.05, 0.02], text: 'thumb toward the link', dx: 0.1, dy: 0.26 }]) },
      { n: 'Gritch: different grip', make: passGrips(false, [{ at: [-0.06, 0.05, 0.02], text: 'thumb toward the link', dx: -0.1, dy: -0.22 }, { at: [0.06, -0.05, 0.02], text: 'thumb toward the tail', dx: 0.1, dy: 0.26 }]) }],
    S6: [{ n: 'Hand positions', make: picture([chuckOn([-0.06, 1.08, -0.2], [0.2, 0.6, -0.1], [0.02, 1.26, -0.2], [0.13, 1.535, -0.245])], { pose: { hR: [-0.06, 1.08, -0.2], hL: [0.2, 1.5, -0.12] }, labels: [{ at: [-0.06, 1.08, -0.2], text: 'swinging hand at the small of the back', dx: -0.03, dy: 0.2 }, { at: [0.2, 1.5, -0.12], text: 'empty hand close to the shoulder', dx: 0.03, dy: -0.2 }] }) }],
    C11: [{ n: 'Handwrap', make: picture([line([[0, -0.035, 0], [0, 0.265, 0]], 0.03, 'prop'), line([[0.012, -0.03, 0.012], [0.05, -0.2, 0.26]], 0.03, 'prop'), line([[0, -0.035, 0], [-0.045, -0.05, 0.0], [-0.06, 0.0, 0.01], [-0.045, 0.045, 0.02], [0.012, -0.03, 0.012]], 0.007, 'link'), fistAt(O, UPV, OUTV, 'closed')], { hide: true, cam: { at: [0, 0.02, 0.05], zoom: 4 }, labels: [{ at: [-0.06, 0.0, 0.01], text: 'link round the back of the hand', dx: -0.1, dy: -0.16 }, { at: [0.006, -0.03, 0.006], text: 'both heads crossed in the palm', dx: 0.1, dy: 0.2 }] }) }],
    F2: [{ n: 'Open grip', make: chuckGrip('open', true, [{ at: [0.012, 0.02, 0.012], text: 'thumb holds the stick on the palm', dx: -0.1, dy: 0.26 }, { at: [0, 0.0, 0.09], text: 'fingers open', dx: 0.16, dy: 0.1 }]) }],
  },
  poi: {
    F1: [{ n: 'Tether length', make: picture([{ prop: { kind: 'poi', hand: 'R' }, keys: { P: [-0.243, 1.302, 0.079] } }], { pose: { hR: [-0.77, 1.43, 0] }, cam: { at: [-0.3, 1.2, 0], zoom: 1.7 }, labels: [{ at: [-0.243, 1.302, 0.079], text: 'the head just reaches the armpit', dx: 0.02, dy: -0.2 }, { at: [-0.77, 1.43, 0], text: 'handle', dx: 0, dy: -0.08 }] }) },
      { n: 'Finger loops', make: picture([fistAt(O, [-1, 0, 0], OUTV, 'closed', 0.008), line([[0, -0.02, 0.02], [0.004, -0.3, 0.02]], 0.007, 'link'), ballAt([0.004, -0.33, 0.02], 0.045, 'prop')], { hide: true, cam: { at: [0, -0.14, 0], zoom: 4.4 }, labels: [{ at: [0, -0.01, 0.03], text: 'a finger through each loop', dx: 0.12, dy: -0.12 }, { at: [0.004, -0.33, 0.02], text: 'head', dx: 0.12, dy: 0 }] }) }],
  },
  staff: {
    F1: [{ n: 'Length', make: picture([{ prop: { kind: 'staff', hand: 'R' }, keys: { a: [-0.44, 1.43, 0.14], b: [-0.44, 0.04, 0.14] } }], { pose: { hR: [-0.44, 1.05, 0.14] }, labels: [{ at: [-0.44, 1.43, 0.14], text: 'shoulder height', dx: -0.1, dy: -0.06 }] }) }],
    F3: [{ n: 'Centre grip', make: picture([{ prop: { kind: 'staff', hand: 'R' }, keys: { a: [-0.95, 1.12, 0.42], b: [0.45, 1.12, 0.42] } }], { pose: { hR: [-0.25, 1.12, 0.42] }, labels: [{ at: [-0.25, 1.12, 0.42], text: 'the middle, where it balances', dx: 0.02, dy: 0.14 }, { at: [-0.95, 1.12, 0.42], text: 'thumb end', dx: 0, dy: -0.08 }] }) },
      { n: 'Close up', make: picture([line([[0, -0.3, 0], [0, 0.3, 0]], 0.028, 'prop'), fistAt(O, UPV, OUTV, 'closed')], { hide: true, cam: { at: O, zoom: 4 }, labels: [{ at: [0, 0.3, 0], text: 'thumb end', dx: -0.12, dy: 0 }, { at: [0, 0.0, 0.03], text: 'a light grip at the middle', dx: 0.12, dy: 0.2 }] }) }],
  },
  doublestaff: {
    F1: [{ n: 'Thumb end and pinky end', make: picture([line([[0, -0.42, 0], [0, 0.42, 0]], 0.028, 'prop'), ballAt([0, 0.42, 0], 0.026, 'trail'), fistAt(O, UPV, OUTV, 'closed')], { hide: true, cam: { at: O, zoom: 2.9 }, labels: [{ at: [0, 0.42, 0], text: 'thumb end (marked)', dx: -0.12, dy: 0.02 }, { at: [0, -0.42, 0], text: 'pinky end', dx: -0.12, dy: -0.02 }] }) }],
    F3: [['Thumbs in', 1, 1], ['Thumbs out', -1, -1], ['Mixed', 1, -1]].map(([n, r, l]) => ({ n, make: picture([
      { prop: { kind: 'staff', hand: 'R' }, keys: r > 0 ? { a: [-0.02, 1.3, 0.42], b: [-0.86, 1.3, 0.42] } : { b: [-0.02, 1.3, 0.42], a: [-0.86, 1.3, 0.42] } },
      { prop: { kind: 'staff', hand: 'L' }, keys: l > 0 ? { a: [0.02, 1.3, 0.42], b: [0.86, 1.3, 0.42] } : { b: [0.02, 1.3, 0.42], a: [0.86, 1.3, 0.42] } }],
      { pose: { hR: [-0.44, 1.3, 0.42], hL: [0.44, 1.3, 0.42] }, labels: [{ at: [0, 1.3, 0.42], text: 'centre point', dx: 0, dy: -0.1 }] }) })),
  },
  buugeng: {
    F1: [{ n: 'Parts and hold', make: picture([{ prop: { kind: 'buugeng', hand: 'R' }, keys: { c: O, u: UPV, v: [1, 0, 0] } }, fistAt(O, [1, 0, 0], OUTV, 'closed', 0.012)], { hide: true, pose: { hR: O }, cam: { at: O, zoom: 2.9 }, labels: [{ at: [0, -0.38, 0], text: 'marked tip', dx: 0.1, dy: 0 }, { at: [-0.19, 0.19, 0], text: 'blade', dx: -0.08, dy: 0 }, { at: [0, 0.0, 0.03], text: 'handle, held loosely', dx: 0.14, dy: 0.12 }] }) }],
    F2: [{ n: 'Planes', make: picture([], { quads: [quad([-0.48, 1.1, 0.1], PB.wheel, 0.5)[0], quad([0.48, 1.1, 0.1], PB.wheel, 0.5)[0], quad([0, 1.1, 0.6], PB.wall, 0.5)[0], quad([0, 1.97, 0.1], PB.flat, 0.45)[0]],
      labels: [{ at: [-0.48, 1.55, 0.1], text: 'wheel plane', dx: -0.02, dy: -0.07 }, { at: [0.48, 1.55, 0.1], text: 'wheel plane', dx: 0.02, dy: -0.07 }, { at: [0.3, 0.75, 0.6], text: 'wall plane', dx: 0.06, dy: 0.08 }, { at: [0.3, 1.97, 0.1], text: 'horizontal plane', dx: 0.08, dy: -0.02 }] }) }],
  },
};
const SPECS = {
  nunchaku: {
    F3: [circle('Wall plane', 'wall', 1, { showPlane: 1 }), circle('Face plane', 'face', 1, { showPlane: 1 }), circle('Table plane', 'table', 1, { showPlane: 1 })],
    F4: [circle('Point swing', 'wall', -1, POINT), circle('Pivot swing', 'wall', -1, PIVOT)],
    F5: [circle('Upspin', 'wall', 1), circle('Downspin', 'wall', -1), circle('Outspin', 'face', -1), circle('Inspin', 'face', 1), circle('Nearspin', 'table', 1), circle('Farspin', 'table', -1), circle('Overhead', 'roof', 1)],
    F6: [{ n: 'Down stop', make: () => stopClip(25) }, { n: 'Side stop', make: () => stopClip(90) }, { n: 'Up stop', make: () => stopClip(150) }],
    F7: [
      { n: 'Upspin H8', make: () => loopClip(eightWall({ dir: 1, a: 0.22, y: 1.2, z: 0.47 })) },
    ],
    F9: [{ n: 'Stretched H8', make: () => loopClip(eightWall({ dir: 1, a: 0.26, y: 1.2, z: 0.47 })) }],
    M1: [hd('Weight shifts', chuckCarried(swing(), BODY.shift, 2))],
    M4: [hd('Down and up', chuckCarried(swing(), BODY.level, 4))],
    F8: [{ n: 'Upspin H8', make: () => loopClip(eightWall({ dir: 1, a: 0.22, y: 1.2, z: 0.47 })) }],
    R1: [hd('One half turn round the hand', once('hand', 1), RL), hd('The other way', once('hand', 1, { dir: -1 }), RL)],
    R2: [hd('Hand rolls in a chain', chain('hand', [1, 1]), RL)],
    R3: [hd('Once round the wrist', once('wrist', 2), RL)],
    R4: [hd('Once round the wrist', once('wrist', 2, { dir: -1 }), RL)],
    R5: [hd('Wrist rolls in a chain', chain('wrist', [2]), RL), hd('The other way', chain('wrist', [2], { dir: -1 }), RL)],
    R6: [hd('One and a half turns', once('wrist', 3), RL)],
    R7: [hd('Two turns (720)', once('wrist', 4), RL), hd('Three turns (1080)', once('wrist', 6), RL)],
    R8: [hd('Round a finger', rollOf('finger', [{ swing: 1 }, { roll: 4 }, { swing: 2 }]), RL)],
    R9: [hd('Round the thumb', rollOf('thumb', [{ swing: 1 }, { roll: 4 }, { swing: 2 }]), RL)],
    R11: [hd('Intercept', intercept, { ...RL, fixed: 1 })],
    R12: [hd('Upward', wheel(1), { fixed: 1, fingers: 1 }), hd('Downward', wheel(-1), { fixed: 1, fingers: 1 })],
    R13: [hd('Disconnected wheel', looseWheel, { fixed: 1, fingers: 1 })],
    R15: [hd('Round the forearm', once('forearm', 2), RL)],
    R16: [hd('Round the neck', memo(() => roll({ at: [0, 1.58, 0], a: [0, 0, 1], b: [-1, 0, 0], rho: 0.17, free: true, loop: true, seq: [{ roll: 2 }] })), RL)], // the sticks slide over the chest and shoulders, so their line sits out from the neck
    R17: [hd('One way', chuckIso(1), { fingers: 1 }), hd('The other way', chuckIso(-1), { fingers: 1 })],
    D13: [{ n: 'Upspin and nearspin', make: swingOn, pair: { free: memo(() => loopClip(cone({ plane: 'table', dir: 1, ...NORMAL }))) } }],
    C2: [{ n: 'Forearm bounce', make: memo(() => bounceClip({ dir: -1, target: [0.02, 1.25, 0.55], fade: 0.2, on: 'forearm' })), contact: 1 }],
    A1: [{ n: 'Low toss', make: aerial }],
    A3: [{ n: 'One rotation', make: aerial }],
    A12: [{ n: 'Catch in the other hand', make: aerialAcross }],
    D14: [{ n: 'One in the air', make: aerial, pair: { free: swingOn } }],
    D16: [pair('Both in the air', aerial, null, false)],
    D4: [circle('Synchronized', 'wall', -1, { both: 0 })],
    D5: [circle('Unsynchronized', 'wall', -1, { both: 0.5 })],
  },
  poi: {
    F2: [{ n: 'Forward', make: sideF }, { n: 'Backward', make: sideB }],
    F3: [{ n: 'Wheel plane', make: sideF, quads: quad(P_SIDE, PB.wheel, 0.72) }, { n: 'Wall plane', make: poiSpin('wall', P_FRONT, 1), quads: quad(P_FRONT, PB.wall, 0.72) }, { n: 'Horizontal plane', make: poiSpin('flat', P_TOP, 1) }],
    F4: [pair('Forward', sideF, null, false), pair('Backward', sideB, null, false)],
    F5: [pair('Forward', sideF, null, true), pair('Backward', sideB, null, true)],
    F6: modes(sideF, sideB),
    B1: [{ ...pair('Butterfly', fly, null, false, APART), pair: { time: 'together', opposite: true, shift: APART } }, { ...pair('Reverse', flyR, null, false, APART), pair: { time: 'together', opposite: true, shift: APART } }],
    B8: [{ n: 'Split-time butterfly', make: fly, pair: { time: 'split', opposite: true, shift: APART } }],
    S3: [{ n: 'Pendulum', make: poiPendulum }],
    I1: [{ n: 'Forward', make: poiExtension(FWD) }, { n: 'Backward', make: poiExtension(BACK) }],
    I2: [{ n: 'Forward', make: poiIsolation(FWD) }, { n: 'Backward', make: poiIsolation(BACK) }],
    R1: [hd('Forward', turningPoi(FWD)), hd('Backward', turningPoi(BACK)), { n: 'Both poi', make: turningPoi(FWD), pair: { left: { make: turningPoi(FWD, true) }, lag: 0 } }],
    F8: [{ n: 'Weight shifts', make: poiCarried(P_WIDE, FWD, BODY.shift, 2), pair: { left: { make: poiCarried(P_WIDE, FWD, BODY.shiftL, 2) }, time: 'together', opposite: false } }],
    L1: [hd('One loop', flower(2)), hd('Two loops', flower(3))],
    L2: [hd('Four petals', petalF)],
    L3: [hd('Three petals', flower(-2))],
    L4: [pair('Split time, same direction', petalF, null, true), pair('Same time, opposite direction', petalF, petalB, false), pair('Split time, opposite direction', petalF, petalB, true)],
    L5: [{ n: 'Antispin', make: petalF, pair: { left: { make: petalB }, time: 'together', opposite: true } }],
  },
  staff: {
    F2: [hd('Front plane', rotorAt('front', 1), { quads: quad(ROTOR.front, BASIS.face, 0.78) }), hd('Side plane', staffRotor('side', FWD), { quads: quad(STAFF_AT.side, BASIS.wall, 0.78), fingers: 1, note: 'On the side plane the figure shows one closed hand: the hand changes are left out.' }), hd('Top plane', rotorAt('top', 1))],
    F4: [hd('One direction', rotorAt('front', 1)), hd('Other direction', rotorAt('front', -1))],
    F6: [hd('One direction', staffRotor('side', FWD), { fingers: 1 }), hd('Other direction', staffRotor('side', BACK), { fingers: 1 })],
    F7: [hd('One direction', rotorAt('top', 1)), hd('Other direction', rotorAt('top', -1))],
    P1: [hd('Passing during a slow rotor', rotorAt('front', 1, 2.6), { contact: 1 })], // the staff slides along the hands as it passes
    M1: [hd('Turning one way', carriedClip(rotorAt('front', 1), BODY.turn, 4)), hd('Turning the other way', carriedClip(rotorAt('front', 1), BODY.turnBack, 4))],
    C3: [hd('Palm spin', palmSpin(STAFF, [-0.22, 1.93, 0.1], 1, 1.3), { free: 1 })],
    T2: [{ n: 'Thumb toss', make: tossLong, fingers: 1 }],
  },
  doublestaff: {
    F5: [hd('Figure 8', s8F)],
    F6: [hd('Reverse figure 8', s8B)],
    T1: modes(s8F, s8B),
    T2: [{ n: 'Butterfly figure 8', make: wall8(1), pair: { time: 'together', opposite: true } }],
    T3: [{ n: 'Same direction', make: wall8(1), pair: { left: { make: wall8(-1) }, time: 'together', opposite: false } }],
    T4: [{ n: 'Same time', make: s8F, pair: { left: { make: short8(FWD, -1) }, time: 'together', opposite: false } }, { n: 'Split time', make: s8F, pair: { left: { make: short8(FWD, -1) }, time: 'split', opposite: false } }],
    T5: [{ n: 'Butterfly weave', make: s8F, pair: { left: { make: short8(BACK, -1) }, time: 'together', opposite: true } }],
    T6: [{ n: 'Quarter time, same direction', make: extF, pair: { lag: 0.25 } }, { n: 'Quarter time, opposite direction', make: extF, pair: { left: { make: circleMove(1, BACK) }, lag: 0.25 } }],
    W1: [hd('Hip reel', reel([-0.44, 1.05, 0.34], 1, [-0.38, 1.15, -0.3]))], // beside and in front of the hip, the elbow bent behind the hand so the forearm points forward, out of the staff's plane
    I1: [hd('One direction', isoF), hd('Reverse', isoB)],
    I2: modes(isoF, isoB),
    I3: [hd('Pinky end fixed', staffIsolation(FWD, true)), { n: 'One on each end', make: isoF, pair: { left: { make: staffIsolation(FWD, true) }, lag: 0 } }],
    I4: [hd('Extension', extF), hd('Reverse', circleMove(1, BACK))],
    A1: [hd('Antispin', anti1, { fingers: 1 })], // the fingers roll the staff against the arm's turn, so it rides on the forearm
    A2: modes(antiF, antiB),
    A3: [hd('Inspin', circleMove(3, FWD))],
    A8: [hd('Cateye', circleMove(-1, FWD, { R: 0.3 }))], // a wider circle keeps the straighter arm off the staff
    A10: [hd('Eight petals', circleMove(-3, FWD, { dur: 2 }))],
    A11: [hd('Opening and closing the book', circleMove(0, FWD, { angle: t => 0.75 * PI * (1 - cos(2 * t)), dur: 2.4 }))],
    H1: [{ n: 'Same time, same direction', make: isoF, fingers: 1, pair: { left: { make: anti1 }, lag: 0 } }],
    H6: [{ n: 'Each hand keeps its move', make: isoF, fingers: 1, pair: { left: { make: anti1 }, lag: 0.25 } }],
    L1: [carriedPair('Weight shifts under a weave', s8F, short8(FWD, -1), 'together', BODY.shift, BODY.shiftL, 2)],
    L2: [carriedPair('Turning with a weave', s8F, short8(FWD, -1), 'together', BODY.turn, BODY.turnBack, 4)],
    J1: [{ n: 'Low throw', make: tossSide, fingers: 1 }],
    J2: [{ n: 'To the other hand', make: tossAcross }],
  },
  buugeng: {
    F3: [hd('Wrist spin', buF)],
    F4: [hd('Forward', buF), hd('Backward', buB)],
    T1: modes(buF, buB),
    T2: [pair('Forward', buF, null, false), pair('Backward', buB, null, false)],
    I1: [hd('One direction', buugengIsolation(BU_ISO, PB.wall, FWD), { fingers: 1 }), hd('Reverse', buugengIsolation(BU_ISO, PB.wall, BACK), { fingers: 1 })],
  },
};
// Moves are only listed here once they pass test/physics.js.
const NOTES = {
  'nunchaku:F1': 'Thumb toward the link is the front grip; little finger toward the link is the back grip.',
  'nunchaku:F2': 'The fingers are open and the thumb alone holds the stick against the palm.',
  'nunchaku:F7': 'Only the upspin H8 is shown. The downspin version clips the forearm and the V8 hits the head, so both were removed. Inspin and outspin H8 are not simulated yet.',
  'nunchaku:F8': 'The path is the same in front grip and back grip, and the figure\'s closed hand does not show which is used. The two grips are pictured under F1. The sources say nothing about how the wrist leads differently.',
  'nunchaku:R1': 'The swinging stick comes over the hand, the hand opens, the nunchaku goes half a turn round it and the other stick is caught. The hand does not show front or back grip, so the change of grip is not visible. Plane and direction follow the wiki animations as far as they can be made out.',
  'nunchaku:R2': 'A half-turn hand roll, one swing, and another half-turn roll, repeated. The change of plane that makes it a figure-8 is not shown.',
  'nunchaku:R3': 'Released, the nunchaku goes once round the wrist and the same stick is caught again. The hand does not show that this is back grip to back grip.',
  'nunchaku:R4': 'The same full turn round the wrist, shown turning the other way. The hand does not show that this is front grip to front grip, and the sources do not say how it differs from the back-grip roll beyond the grip.',
  'nunchaku:R5': 'One swing and one full wrist roll, repeated without a break.',
  'nunchaku:R6': 'One and a half turns round the wrist, so the other stick is caught and the grip changes.',
  'nunchaku:R7': 'More turns before the catch. A real roll slows as it goes; here the rate is kept even.',
  'nunchaku:R8': 'The roll taken round one finger. The sources say any finger can be used and that many finger rolls start from a midgrip; neither is shown.',
  'nunchaku:R9': 'A finger roll on the thumb. The sources say no more than that, so the position of the thumb is my reading.',
  'nunchaku:R11': 'From the surviving snippets: after a roll on one hand, a finger of the other hand is put in the path of the chain and takes the spin over. How the first trick ends is not described.',
  'nunchaku:R13': 'The disconnected wheel: one finger is taken away for a turn, so the wheel becomes a finger roll, then comes back. The horizontal wheel is not shown: with the sticks turning level, neither arm can get past them to reach the link from below.',
  'nunchaku:R15': 'The same roll moved up onto the forearm. The sources only name arm and leg rolls; the leg roll is not shown.',
  'nunchaku:R16': 'The nunchaku turning round the neck with both sticks out. The sources only name the move, so the entry and the catch are left out. Learn it on foam.',
  'nunchaku:R17': 'As the wiki defines it: the hand and the tail of the held stick go round its head, which stays on one spot. Shown level, with the other stick hanging still below.',
  'nunchaku:R12': 'One finger of each hand sits on opposite sides of the link and only the link is touched. Upward and downward name the way the side nearest you travels. Which fingers, and how they drive the spin, the sources do not say.',
  'nunchaku:S4': 'The moment of a pass when a hand is on each stick. Caught in the same grip as the handle it is a slick pass; caught in the other grip it is a gritch (grip-switch) pass.',
  'nunchaku:S6': 'Seen best from the side or with the figure turned: the swinging hand is at the small of the back and the empty hand waits close to its shoulder, as the written advice says. The swing and the catch are not animated.',
  'nunchaku:A7': 'Twelve is the top of the circle, and the numbers run on in the direction of the spin (shown for one direction). On a table plane twelve is the point furthest from you.',
  'nunchaku:S2': 'The first moment of the written steps: the striker is flipped back over the shoulder while the other hand reaches across the front. That hand then catches the striker below the arm and the first hand lets go. The catch is not pictured: the figure cannot turn its chest to reach it.',
  'nunchaku:C1': 'The end position only: the striker is pinned in the armpit by pinching the elbow to the ribs, with the hand still holding the handle in front. The swing into it and the release are not animated.',
  'nunchaku:C11': 'The handwrap as the wiki defines it: both heads crossed in the palm with the link round the back of the hand. The throw that wraps it and the release are not shown.',
  'nunchaku:F9': 'The two planes are 52 cm apart, against 44 cm in the basic figure-8. The twisted figure-8 is not shown.',
  'nunchaku:C2': 'The swing is slowed a little, the other forearm comes up into its path, and the striker comes back the other way. How much speed the body returns (35%) is an assumed value, not a measured one.',
  'nunchaku:A1': 'The release was chosen by search so the handle turns once and returns within reach.',
  'nunchaku:A3': 'The release was chosen by search so the handle turns once and returns within reach.',
  'nunchaku:A12': 'The release was chosen by search so the handle turns once and reaches the other hand, which then turns it to its own grip.',
  'nunchaku:M1': 'The weight goes from foot to foot once every two swings, and the swing is simulated on the moving body.',
  'nunchaku:M4': 'Down and up once every four swings, by bending the knees. The swing is simulated on the moving body.',
  'nunchaku:D13': 'An upspin in one hand and a flat nearspin in the other, each simulated on its own.',
  'nunchaku:D14': 'The other nunchaku keeps its normal swing.', 'nunchaku:D16': 'Both are released at the same moment, mirrored.',
  'poi:F1': 'With the arm stretched out, the head just reaches the armpit. The other hand, which pulls the head along the arm, is left out.',
  'poi:F3': 'The horizontal plane is shown overhead.', 'poi:B1': 'The hands are side by side with one a little in front, so the two planes sit 11 cm apart and the heads pass each other.',
  'poi:R1': 'The head keeps a steady circle beside the body while the body turns once in eight spins. The hand is solved for.',
  'doublestaff:L1': 'A same-direction weave while the weight goes from foot to foot.', 'doublestaff:L2': 'One body turn for every four repeats of the weave.',
  'poi:F8': 'Both poi keep spinning forward while the weight goes from foot to foot. Turns and steps are not shown.',
  'poi:L1': 'Loop counts follow the Home of Poi convention. The hand path is solved, so it is close to a circle but not exactly one.',
  'poi:L2': 'Petal counts follow the Home of Poi convention. The hand path is solved, so it is close to a circle but not exactly one.',
  'poi:L3': 'Petal counts follow the Home of Poi convention. The hand path is solved, so it is close to a circle but not exactly one.',
  'poi:L4': 'Shown with the four-petal antispin flower at each side.', 'poi:L5': 'Shown with the four-petal antispin flower: the arms circle opposite ways and meet at the top and the bottom.',
  'staff:F1': 'Ground to shoulder height is the starting length the sources give.', 'staff:F3': 'The marked end is the thumb end.',
  'staff:F2': 'In front and overhead the rotor is kept turning hand over hand. At the side one closed hand is shown.',
  'staff:F4': 'Kept turning hand over hand: each hand turns the staff a little under half a turn and reaches back. The sources do not say which direction is called forward.',
  'staff:F6': 'One closed hand is shown: the hand changes on the side plane are left out.', 'staff:F7': 'Kept turning hand over hand above the head.',
  'staff:P1': 'The staff changes hands twice in every turn.', 'staff:M1': 'The body makes one turn, in quarter-turn steps, for every four turns of the staff.',
  'staff:C3': 'A staff turning freely on the palm keeps its rate. Friction and the small corrections of the hand are left out.',
  'staff:T2': 'The roll over the thumb is not shown: the staff is released level.',
  'doublestaff:F1': 'The thumb end is the one usually marked.', 'doublestaff:F3': 'One end of each staff points at a centre point in front of the chest.',
  'doublestaff:F5': 'The staff changes side each time it is level, as the sources describe. Where the hand is held was chosen by search to keep the staff clear of the arm and the body.',
  'doublestaff:F6': 'The staff changes side each time it is level, as the sources describe.',
  'doublestaff:T1': 'Shown with a one-hand figure 8 in each hand.', 'doublestaff:T2': 'On the wall plane, each staff changing side when it is level.', 'doublestaff:T3': 'On the wall plane; the staffs stay parallel.',
  'doublestaff:T4': 'Each staff alternates between the inside and the outside of its arm, and the two stay parallel.', 'doublestaff:T5': 'The same weave with the staffs turning opposite ways.',
  'doublestaff:T6': 'Shown with extensions, one hand a quarter of a circle ahead of the other.',
  'doublestaff:W1': 'One staff shown. The thumb end circles in front of the body and the pinky end behind it.',
  'doublestaff:I1': 'The marked end is the one that stays still. Finger work and the body turn are not shown.', 'doublestaff:I2': 'Shown on the wheel plane at each side.',
  'doublestaff:I3': 'The marked end is the one that travels.', 'doublestaff:I4': 'Read from the poi definition: the staff stays in line with the arm.',
  'doublestaff:A1': 'Shown on the wheel plane with the hand circle as wide as half the staff, so the marked end travels along one line.',
  'doublestaff:A2': 'For a staff, four petals means one turn against each circle of the hand: each end draws two.',
  'doublestaff:A3': 'Shown with three turns of the staff for each circle of the hand. The sources do not fix that number.',
  'doublestaff:A8': 'The antispin with a smaller hand circle, so the ends draw ellipses.', 'doublestaff:A10': 'Three turns against each circle of the hand, so the same end points in at all four points.',
  'doublestaff:A11': 'Three quarters of a turn for each quarter of the hand circle, one way and then the other. The change of direction is rounded off.',
  'doublestaff:H1': 'The right hand isolates and the left hand antispins.', 'doublestaff:H6': 'The same hybrid with the hands a quarter of a circle apart.',
  'doublestaff:J1': 'Shown with one staff, at the side. The sources do not say what makes this throw "inside".', 'doublestaff:J2': 'Shown with one staff and an empty catching hand, on the wall plane.',
  'buugeng:F1': 'Two curved blades meet at the handle. One tip is marked.', 'buugeng:F2': 'The same plane names as poi.',
  'buugeng:F3': 'The arm is stretched forward and the buugeng passes on the inside of the arm, then on the outside.',
  'buugeng:F4': 'Turned by the wrist. Which way the curves face is not specified by the sources.', 'buugeng:T1': 'Turned by the wrist. Which way the curves face is not specified by the sources.', 'buugeng:T2': 'Turned by the wrist. Which way the curves face is not specified by the sources.',
  'buugeng:I1': 'The circle of one curve stays in one place while the hand goes round it. The finger rolls that drive it are not shown.',
};

for (const p in STILLS) Object.assign(SPECS[p], STILLS[p]);
module.exports = { SPECS, NOTES };
