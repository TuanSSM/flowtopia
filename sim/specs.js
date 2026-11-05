'use strict';
// The list of moves that have a demonstration, and how each one is made. A move is listed
// here only once its clip passes test/physics.js.
const E = require('./engine.js');
const { Demo, TAU, WANT, GRIP, TETHER, ARM, SHOULDER, ISO, STAFF, SHORT, SHORT_SIDE, STAFF_AT, STAFF_BS, BASIS, CENTRE, PB, FWD, BACK, NORMAL, POINT, PIVOT, IDLE,
  spoke, cone, eightWall, eightTall, loopClip, stopClip, memo, herm, aerialClip, bounceClip, staffToss, drawn, turn, poiSpin, poiExtension, poiIsolation, poiPendulum,
  poiFlower, rotor, staffRotor, staffIsolation, staffAntispin, buugengAt, buugengSpin } = E;
const M = require('./moves.js'), { fitted, elbowAt, BODY, figure8, handRotor, unit, palmSpin, buugengIsolation, carriedClip, weave, poiCarried, chuckCarried, solved, twoArm, still, picture, line, fistAt, ballAt } = M;
const { add, sub, mul, dot, cross, len, norm, mix, rot, clamp, smooth, flip } = Demo.v, { sin, cos, sqrt, PI } = Math;

const circle = (n, plane, dir, x) => { const o = { plane, dir, ...NORMAL, ...x }; return { n, quads: x && x.showPlane ? quad(add(CENTRE[plane], mul(BASIS[plane].n, GRIP * cos(o.gamma))), BASIS[plane], 0.58) : undefined, make: memo(() => loopClip(cone(o))), ...x }; };
// Two props: the left hand mirrors the right clip, or a second clip, together or split in time.
const pair = (n, make, left, split, shift) => ({ n, make, pair: { left: left ? { make: left } : null, time: split ? 'split' : 'together', opposite: !!left, shift } });
const modes = (same, other) => [pair('Together, same direction', same, null, false), pair('Split, same direction', same, null, true), pair('Together, opposite direction', same, other, false), pair('Split, opposite direction', same, other, true)];
const KNUCKLES = [0, 0, 0.09]; // in a butterfly the tethers leave from the front of the fists, clear of the wrists behind
const P_SIDE = [-0.3, 1.0, 0.12], P_FRONT = [-0.2, 1.2, 0.5], P_HANDS = [-0.07, 1.15, 0.32], P_TOP = [-0.1, 1.93, 0.05];
const sideF = poiSpin('wheel', P_SIDE, FWD), sideB = poiSpin('wheel', P_SIDE, BACK), fly = poiSpin('wall', P_HANDS, 1, 0.7, KNUCKLES), flyR = poiSpin('wall', P_HANDS, -1, 0.7, KNUCKLES);
const hd = (n, make, x) => ({ n, make, ...x });
// Staff. A rotor is kept turning hand over hand; a figure-8 is turned by one wrist.
const ROTOR = { front: [0, 1.18, 0.4], top: [0, 1.9, 0.05] };
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
const reel = (c, lobes, elbow) => figure8({ kind: 'staff', half: SHORT, c, ...WALL8, dir: 1, gamma: 0.9, stay: true, lobes, dur: 1.6, elbow: () => elbow });
const SH = [-0.24, 1.43, 0], OUT = 0.05; // unit-circle moves turn about the shoulder, with the staff just outside the arm
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
const FLOWER_AT = [-0.24, 1.43, 0], flower = (k, dir = FWD) => solved(1.8, twoArm(FLOWER_AT, PB.wheel, 0.4, k, dir));
const CAT_AT = [-0.02, 1.25, 0.4], cateye = (dir, phase, dur = 0.85, R = 0.2) => solved(dur, twoArm(CAT_AT, PB.wall, R, -1, dir, phase));
const petalF = flower(-3, FWD), petalB = flower(-3, BACK);
const weaveOf = (n, dir, at = 1.0, x) => weave({ dir, n, A: 0.21, y: 1.16, z: 0.36, at, w: 0.05, lift: 0.04, ...x });
const P_WIDE = [-0.38, 1.05, 0.12]; // far enough out that a shifting body stays clear of the poi
const swing = () => cone({ plane: 'wall', dir: 1, ...NORMAL });

/* ---------- still pictures ---------- */
const UPV = [0, 1, 0], OUTV = [0, 0, 1], O = [0, 0, 0];
// A nunchaku held upright in a fist, the other stick hanging from the cord.
function chuckGrip(grip, thumbUp, labels) {
  const top = [0, 0.12, 0], hang = [0.085, 0.1, 0.01];
  return picture([line([[0, -0.18, 0], top], 0.03, 'prop'), line([top, [0.035, 0.16, 0.005], hang], 0.007, 'link'), line([hang, add(hang, [0.01, -0.3, 0])], 0.03, 'prop'), fistAt(O, thumbUp ? UPV : [0, -1, 0], OUTV, grip)],
    { hide: true, cam: { at: [0.03, -0.03, 0], zoom: 4.4 }, labels });
}
const STILLS = {
  nunchaku: {
    F1: [{ n: 'Front grip', make: chuckGrip('closed', true, [{ at: [0, 0.12, 0], text: 'link end', dx: -0.14, dy: -0.04 }, { at: [0.01, 0.045, 0.01], text: 'thumb toward the link', dx: -0.16, dy: 0.02 }, { at: [0, -0.18, 0], text: 'tail end', dx: -0.12, dy: 0.02 }]) },
      { n: 'Back grip', make: chuckGrip('closed', false, [{ at: [0, 0.12, 0], text: 'link end', dx: -0.14, dy: -0.04 }, { at: [0.01, -0.045, 0.01], text: 'thumb toward the tail', dx: -0.16, dy: 0.04 }, { at: [0.0, 0.04, 0.02], text: 'little finger toward the link', dx: 0.1, dy: 0.2 }]) }],
    F2: [{ n: 'Open grip', make: chuckGrip('open', true, [{ at: [0.012, 0.02, 0.012], text: 'thumb holds the stick on the palm', dx: -0.1, dy: 0.26 }, { at: [0, 0.0, 0.09], text: 'fingers open', dx: 0.16, dy: 0.1 }]) }],
  },
  poi: {
    F1: [{ n: 'Tether length', make: picture([{ prop: { kind: 'poi', hand: 'R' }, keys: { P: [-0.228, 1.345, 0.05] } }], { pose: { hR: [-0.77, 1.43, 0] }, cam: { at: [-0.3, 1.2, 0], zoom: 1.7 }, labels: [{ at: [-0.228, 1.345, 0.05], text: 'the head just reaches the armpit', dx: 0.02, dy: -0.2 }, { at: [-0.77, 1.43, 0], text: 'handle', dx: 0, dy: -0.08 }] }) },
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
      { n: 'Upspin H8', make: () => loopClip(eightWall({ dir: 1, a: 0.22, y: 1.16, z: 0.42 })) },
    ],
    F9: [{ n: 'Stretched H8', make: () => loopClip(eightWall({ dir: 1, a: 0.26, y: 1.16, z: 0.42 })) }],
    M1: [hd('Weight shifts', chuckCarried(swing(), BODY.shift, 2))],
    M4: [hd('Down and up', chuckCarried(swing(), BODY.level, 4))],
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
    P1: [hd('Passing during a slow rotor', rotorAt('front', 1, 2.6))],
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
    W1: [hd('Hip reel', reel([-0.34, 0.99, 0.02], 1, [-0.7, 1.2, 0]))],
    W2: [hd('Shoulder reel', reel([-0.42, 1.45, 0.02], -1, [-0.6, 1.1, 0]))],
    I1: [hd('One direction', isoF), hd('Reverse', isoB)],
    I2: modes(isoF, isoB),
    I3: [hd('Pinky end fixed', staffIsolation(FWD, true)), { n: 'One on each end', make: isoF, pair: { left: { make: staffIsolation(FWD, true) }, lag: 0 } }],
    I4: [hd('Extension', extF), hd('Reverse', circleMove(1, BACK))],
    A1: [hd('Antispin', anti1)],
    A2: modes(antiF, antiB),
    A3: [hd('Inspin', circleMove(3, FWD))],
    A8: [hd('Cateye', circleMove(-1, FWD, { R: 0.26 }))],
    A10: [hd('Eight petals', circleMove(-3, FWD, { dur: 2 }))],
    A11: [hd('Opening and closing the book', circleMove(0, FWD, { angle: t => 0.75 * PI * (1 - cos(2 * t)), dur: 2.4 }))],
    H1: [{ n: 'Same time, same direction', make: isoF, pair: { left: { make: anti1 }, lag: 0 } }],
    H6: [{ n: 'Each hand keeps its move', make: isoF, pair: { left: { make: anti1 }, lag: 0.25 } }],
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
  'doublestaff:W1': 'One staff shown. The thumb end circles in front of the body and the pinky end behind it.', 'doublestaff:W2': 'One staff shown. Here the thumb end is the one behind the shoulder.',
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
