'use strict';
// The chest turned against the hips: the `twist` of a pose (web/demo.js), and what the rig, the capsules,
// the body checks and a clip make of it. Each check has an answer that follows from the definition, a
// turn of the shoulder girdle about the line of the spine, worked out here with this file's own rotation
// and not the rig's, and a tolerance written next to it. The 3D figure is held to the same rig in
// `node test/body3d.js rig`.
//   node test/twist.js          on its own; node test/physics.js runs it too, before the clips
const E = require('../sim/engine.js'), { Demo } = E, C = require('../sim/check.js'), P = require('../sim/presets.js'), { scene } = require('../sim/compose.js');
const { add, sub, mul, dot, cross, len, norm } = Demo.v;
const RAD = Math.PI / 180, mm = (v, d = 0) => (v * 1000).toFixed(d) + ' mm';

// Every body, four poses and six twists: 216 cases. The poses are given for the standard body and moved
// to fit the others (Demo.fit), as a clip is.
const BODIES = [{ id: 'standard', sc: 1, bd: 1 }, ...P.PRESETS];
const POSES = { standing: {}, 'arms up': { hL: [0.25, 1.95, 0.05], hR: [-0.25, 1.95, 0.05] }, 'a deep bend': { pelvis: [0, 0.78, -0.12], spine: [0, 0.75, 0.66], hL: [0.2, 0.75, 0.35], hR: [-0.2, 0.75, 0.35] }, 'a lean': { spine: [0.1, 0.98, 0.15] } };
const TWISTS = [15, -15, 30, -30, 45, -45];
// A pose with a twist of `deg` degrees: three numbers, like every value of a frame.
const twisted = (pose, deg) => ({ ...pose, twist: [deg, 0, 0] });
// The point p turned by `deg` degrees about the line through c along the unit vector u (right-handed:
// about a spine that points up, a positive turn takes what faces forward toward the figure's left).
function turn(p, c, u, deg) {
  const v = sub(p, c), co = Math.cos(deg * RAD), si = Math.sin(deg * RAD);
  return add(c, add(add(mul(v, co), mul(cross(u, v), si)), mul(u, dot(u, v) * (1 - co))));
}
// How far b is turned from a about the unit vector u, in degrees: both are flattened across u first.
function angleAbout(a, b, u) {
  const fa = sub(a, mul(u, dot(a, u))), fb = sub(b, mul(u, dot(b, u)));
  return Math.atan2(dot(cross(fa, fb), u), dot(fa, fb)) / RAD;
}
const same = (a, b) => a.length === b.length && a.every((x, i) => Object.is(x, b[i])); // to the last digit
// How clear a ball of radius r at p is of the capsules: the least gap between the two surfaces.
const gap = (p, r, caps) => Math.min(...caps.map(c => { const q = E.closest(p, p, c.a, c.b); return len(sub(q.c1, q.c2)) - c.r - r; }));
// The capsules by place in Demo.caps: seven for the head and trunk, then twelve for each side, left first.
const HEAD = 0, NECK = 1, CHEST_TOP = 5, UPPER_BACK = 6, SIDE = { L: 7, R: 19 }, DELTOID = 7, UPPER_ARM = [8, 9];
const still = caps => [2, 3, 4, ...[SIDE.L, SIDE.R].flatMap(s => [0, 1, 2, 3, 4, 5, 6].map(i => s + i))].map(i => caps[i]); // the waist, the belly, the hips and the legs
const oneFrame = (f, props = []) => ({ fr: [f], props, dur: 1, method: 'hand', loop: false });

function run(say = console.log) {
  let n = 0, bad = 0;
  const ok = (name, pass, got = '') => { n++; if (!pass) bad++; say(`${pass ? 'PASS' : 'FAIL'} ${name}${pass || got === '' ? '' : ': ' + got}`); }; // got: what came out, said when the check fails
  const { TWIST, HEAD_BACK } = Demo;
  // What is measured over the 216 cases: the most of each, and how many cases broke a rule that has no size.
  const most = {}, broke = {}, note = (k, v) => { most[k] = Math.max(most[k] || 0, v); }, must = (k, pass) => { broke[k] = (broke[k] || 0) + (pass ? 0 : 1); };
  let cases = 0, outOfReach = 0;
  try {
    for (const b of BODIES) {
      Demo.setBody(b.sc, b.bd);
      const D = Demo.dim;
      for (const [name, pose] of Object.entries(POSES)) {
        const f0 = Demo.fit(pose), J0 = Demo.rig(f0), caps0 = Demo.caps(J0, {}), c = J0.pelvis, up = J0.up;
        // No twist, and a twist of nothing, are the same figure.
        const Jz = Demo.rig(twisted(f0, 0));
        must('zero', Object.keys(J0).every(k => typeof J0[k] === 'number' ? Object.is(J0[k], Jz[k]) : same(J0[k], Jz[k])) && J0.twist === 0 && J0.cleft === J0.left && J0.hleft === J0.left && J0.cface === J0.face);
        for (const deg of TWISTS) {
          cases++;
          const f = twisted(f0, deg), J = Demo.rig(f), caps = Demo.caps(J, {}), about = (p, d = deg) => turn(p, c, up, d);
          // Below the chest nothing moves, and the chest and the head stay on the line they turn about.
          for (const k of ['pelvis', 'face', 'up', 'left', 'hipL', 'hipR', 'kneeL', 'kneeR', 'fL', 'fR', 'tL', 'tR', 'chest', 'head']) must('below', same(J[k], J0[k]));
          still(caps).forEach((cp, i) => { const o = still(caps0)[i]; must('below', same(cp.a, o.a) && same(cp.b, o.b) && cp.r === o.r && cp.name === o.name); });
          // The shoulders are the untwisted ones turned about the spine by the whole twist.
          for (const s of ['L', 'R']) note('shoulder', len(sub(J['sh' + s], about(J0['sh' + s]))));
          const hips = sub(J.hipL, J.hipR), line0 = angleAbout(hips, sub(J0.shL, J0.shR), up), line = angleAbout(hips, sub(J.shL, J.shR), up);
          note('line', Math.abs(line - line0 - deg));
          // Positive is to the figure's left: the right shoulder comes forward, the left goes back, the chest faces left of the hips.
          const fwd = cross(J.left, up), sign = Math.sign(deg);
          must('sign', sign * dot(sub(J.shR, J0.shR), fwd) > 0 && sign * dot(sub(J.shL, J0.shL), fwd) < 0 && sign * dot(J.cface, cross([0, 1, 0], J.face)) > 0 && J.twist === deg);
          // The head turns by what it does not take back.
          note('head', Math.abs(angleAbout(sub(J0.nose, J0.head), sub(J.nose, J.head), up) - deg * (1 - HEAD_BACK)));
          for (const s of ['L', 'R']) {
            const sh = J['sh' + s], el = J['el' + s], h = J['h' + s], far = len(sub(h, sh));
            if (f0['h' + s]) {
              // A hand the pose gives stays where it is; the elbow is solved again from the turned shoulder.
              must('hand', same(h, f0['h' + s]));
              if (far < D.REACH - 0.002) { note('arm', Math.abs(len(sub(el, sh)) - D.U_ARM)); note('arm', Math.abs(len(sub(h, el)) - D.F_ARM)); }
            } else {
              // A hand the pose does not give hangs from its shoulder, so it goes round the spine with it, upright or
              // leaning, and is as far from its shoulder as without the twist.
              note('rest', len(sub(h, about(J0['h' + s])))); note('rest', Math.abs(far - len(sub(J0['h' + s], J0['sh' + s]))));
              must('restReach', far < D.REACH);
            }
            // The reach check sees the turned shoulder: it fails the pose when the hand is too far from where the shoulder now is, and only then.
            const out = len(sub(h, about(J0['sh' + s]))) > D.REACH + 0.03, said = C.anatomy(oneFrame(f), {}).errs.some(e => e.startsWith(s + ' hand') && /from its shoulder/.test(e));
            must('reach', out === said); if (out) outOfReach++;
            // The capsules of the shoulder and the upper arm.
            const i = SIDE[s];
            note('caps', len(sub(caps[i + DELTOID].a, about(caps0[i + DELTOID].a)))); note('caps', len(sub(caps[i + DELTOID].b, sh)));
            must('upperArm', same(caps[i + UPPER_ARM[0]].a, sh) && same(caps[i + UPPER_ARM[1]].b, el) && same(caps[i + UPPER_ARM[0]].b, caps[i + UPPER_ARM[1]].a));
          }
          // The chest's capsules turn with it; the head's by the head's share; the neck from the chest at its base to the head at its top.
          for (const i of [CHEST_TOP, UPPER_BACK]) for (const e of ['a', 'b']) note('caps', len(sub(caps[i][e], about(caps0[i][e]))));
          for (const e of ['a', 'b']) note('caps', len(sub(caps[HEAD][e], about(caps0[HEAD][e], deg * (1 - HEAD_BACK)))));
          note('caps', len(sub(caps[NECK].a, about(caps0[NECK].a)))); must('below', same(caps[NECK].b, caps0[NECK].b));
          // Standing or leaning with the arms hanging, the turned body passes every body check.
          if (!f0.hL) must('clean', C.bodyErrors(oneFrame(f), {}).errs.length === 0);
        }
      }
    }
  } finally { Demo.setBody(1, 1); }
  const none = k => broke[k] === 0, cs = `${cases} cases`;
  ok(`no twist and a twist of 0 degrees give the same joints to the last digit (${BODIES.length} bodies, ${Object.keys(POSES).length} poses)`, none('zero'));
  ok(`the pelvis, hips, knees, feet and toes, their capsules, and the chest and head on the spine's line do not move at all (${cs})`, none('below'));
  ok(`each shoulder is where the untwisted one is carried by the twist about the spine (most ${mm(most.shoulder, 9)} off, 0.000001 mm allowed)`, most.shoulder < 1e-9);
  ok(`the shoulder line is turned against the hip line by the twist, about the spine (most ${most.line.toExponential(1)} degrees off, 0.000001 allowed)`, most.line < 1e-6);
  ok('a positive twist is to the figure\'s left: the right shoulder forward, the left back, the chest facing left of the hips', none('sign'));
  ok(`the head turns by ${Math.round((1 - HEAD_BACK) * 100)}% of the twist and takes back the rest (most ${most.head.toExponential(1)} degrees off, 0.000001 allowed)`, most.head < 1e-6 && HEAD_BACK === 0.6);
  ok('a hand the pose gives stays where the pose puts it', none('hand'));
  ok(`its elbow is solved again from the turned shoulder: both arm bones keep their length while the hand is in reach (most ${mm(most.arm, 9)} off, 0.000001 mm allowed)`, most.arm < 1e-9);
  ok(`a hand the pose leaves out goes round the spine with its shoulder, standing or leaning, and stays as far from it (most ${mm(most.rest, 9)} off, 0.000001 mm allowed)`, most.rest < 1e-9 && none('restReach'));
  ok(`the reach check fails a hand that is too far from the turned shoulder, and no other (${outOfReach} of ${cases * 2} hands went out of reach)`, none('reach') && outOfReach > 0);
  ok(`the capsules of the chest top, the upper back, the deltoids, the neck's base and the head are carried by the same turn (most ${mm(most.caps, 9)} off, 0.000001 mm allowed)`, most.caps < 1e-9);
  ok('the upper arm\'s capsules run from the turned shoulder to the elbow solved from it', none('upperArm'));
  ok('standing or leaning with the arms hanging, every twist on every body passes the body checks (overlaps, reach, legs, balance)', none('clean'));

  // A known place: the standard figure standing, turned 45 degrees to its left, numbers written out.
  {
    const J = Demo.rig(twisted({}, 45)), r = 0.2 * Math.SQRT1_2, near = (a, b) => len(sub(a, b)) < 1e-12;
    ok('standing turned 45 degrees: the shoulders are at (0.1414, 1.43, -0.1414) and (-0.1414, 1.43, 0.1414), 0.4 m apart', near(J.shL, [r, 1.43, -r]) && near(J.shR, [-r, 1.43, r]) && Math.abs(len(sub(J.shL, J.shR)) - 0.4) < 1e-12, JSON.stringify([J.shL, J.shR]));
    ok('and the nose is 18 degrees to the left of straight ahead', near(J.nose, [0.1 * Math.sin(18 * RAD), 0.97 + 0.718, 0.1 * Math.cos(18 * RAD)]), JSON.stringify(J.nose));
  }

  // The checks judge a prop against the turned chest. A short piece of staff is set 30 mm clear of the
  // standing body, straight in front of the right shoulder. Carried round with the chest it stays 30 mm
  // clear at every twist; left where it is, the right shoulder comes forward onto it when the chest turns left.
  {
    const R = 0.017, J0 = Demo.rig({}), caps0 = Demo.caps(J0, {}), c = J0.pelvis, up = J0.up;
    let lo = 0.1, hi = 0.4; // metres in front of the shoulder: inside the chest at one end, well clear at the other
    for (let i = 0; i < 60; i++) { const z = (lo + hi) / 2; if (gap(add(J0.shR, [0, 0, z]), R, caps0) < 0.03) lo = z; else hi = z; }
    const Q = add(J0.shR, [0, 0, hi]), staff = q => ({ '0.a': add(q, [0, 0.001, 0]), '0.b': add(q, [0, -0.001, 0]) }), prop = [{ kind: 'staff', hand: 'L' }];
    const errs = (deg, q) => C.bodyErrors(oneFrame({ ...twisted({}, deg), ...staff(q) }, prop), {}).errs;
    let carried = 0, clean = true, fixed = 0;
    for (const deg of TWISTS) {
      const caps = Demo.caps(Demo.rig(twisted({}, deg)), {});
      carried = Math.max(carried, Math.abs(gap(turn(Q, c, up, deg), R, caps) - 0.03));
      clean = clean && errs(deg, turn(Q, c, up, deg)).length === 0;
      // Left where it is, it is as clear of the turned body as it would be of the untwisted one from where the turn takes it back to.
      fixed = Math.max(fixed, Math.abs(gap(Q, R, caps) - gap(turn(Q, c, up, -deg), R, caps0)));
    }
    ok(`a staff 30 mm clear in front of the right shoulder is still 30 mm clear when it is carried round with the chest, and the checks pass it (most ${mm(carried, 9)} off, 0.000001 mm allowed)`, Math.abs(gap(Q, R, caps0) - 0.03) < 1e-9 && carried < 1e-9 && clean);
    ok(`left where it is, it is judged against the turned chest, not the unturned one (most ${mm(fixed, 9)} from what the turn alone gives, 0.000001 mm allowed)`, fixed < 1e-9);
    const hit = errs(45, Q), depth = -gap(Q, R, Demo.caps(Demo.rig(twisted({}, 45)), {}));
    ok(`untwisted the checks pass it; turned 45 degrees to the left they fail it, ${mm(depth)} into the right shoulder; turned to the right they pass it`, errs(0, Q).length === 0 && hit.length === 1 && hit[0].startsWith(`${mm(depth)} staff into the`) && depth > 0.02 && errs(-45, Q).length === 0, JSON.stringify(hit));
  }

  // Reach: a hand straight in front of the right shoulder at the arm's full length is in reach until
  // the chest turns to the right and takes that shoulder back.
  {
    let fine = true, first = '';
    try {
      for (const b of BODIES) {
        Demo.setBody(b.sc, b.bd);
        const D = Demo.dim, J0 = Demo.rig({}), h = add(J0.shR, [0, 0, D.REACH]), errs = deg => C.anatomy(oneFrame({ ...twisted({}, deg), hR: h }), {}).errs;
        const far = len(sub(h, turn(J0.shR, J0.pelvis, J0.up, -45))), want = `R hand ${far.toFixed(2)} m from its shoulder, and an arm is ${D.REACH.toFixed(2)} m`;
        const got = errs(-45), pass = errs(0).length === 0 && errs(45).length === 0 && got.length === 1 && got[0] === want && far > D.REACH + 0.1;
        if (!pass) { fine = false; first = first || `${b.id}: ${JSON.stringify(got)}, wanted ${want}`; }
      }
    } finally { Demo.setBody(1, 1); }
    ok(`a hand at arm's length in front of its shoulder goes out of reach when the chest turns that shoulder back 45 degrees, and the reach check says by how much (${BODIES.length} bodies)`, fine, first);
  }

  // The limit: the rig draws no more than TWIST degrees, and the body checks fail a clip that asks for more.
  {
    const eq = (a, b) => Object.keys(a).every(k => typeof a[k] === 'number' ? a[k] === b[k] : same(a[k], b[k])), errs = t => C.bodyErrors(oneFrame({ twist: t }), {}).errs;
    ok(`the rig stops at ${TWIST} degrees: a twist of 60 is drawn as ${TWIST}, to either side`, TWIST === 45 && eq(Demo.rig(twisted({}, 60)), Demo.rig(twisted({}, 45))) && eq(Demo.rig(twisted({}, -60)), Demo.rig(twisted({}, -45))) && Demo.rig(twisted({}, 60)).twist === 45);
    ok('the body checks pass 45 degrees to either side and fail 45.004, 45.01 and 60, each named with all its digits', errs([45, 0, 0]).length === 0 && errs([-45, 0, 0]).length === 0 && errs([45.01, 0, 0]).length === 1 && errs([45.004, 0, 0])[0] === 'chest turned 45.004 degrees against the hips, and it turns 45 at most' && errs([-60, 0, 0])[0] === 'chest turned 60 degrees against the hips, and it turns 45 at most', JSON.stringify([errs([45.004, 0, 0]), errs([-60, 0, 0])]));
    ok('and a twist with anything in its other two numbers, which is what moving it like a point would leave', errs([30, 0, 0.2])[0] === 'the twist is [degrees, 0, 0], and this one is [30, 0, 0.2]', JSON.stringify(errs([30, 0, 0.2])));
  }

  // One convention everywhere: the mirror image of a pose turned left is the mirrored pose turned right.
  {
    const f = { ...twisted(POSES['arms up'], 30), hR: [-0.3, 1.2, 0.35] }, m = Demo.mirror(f), A = Demo.rig(f), B = Demo.rig(m);
    let off = 0;
    for (const [k, q] of [['pelvis', 'pelvis'], ['chest', 'chest'], ['head', 'head'], ['nose', 'nose'], ['shL', 'shR'], ['shR', 'shL'], ['elL', 'elR'], ['elR', 'elL'], ['hL', 'hR'], ['hR', 'hL'], ['kneeL', 'kneeR'], ['fL', 'fR'], ['cleft', 'cleft'], ['hleft', 'hleft']]) off = Math.max(off, len(sub(B[q], k.endsWith('left') ? [A[k][0], -A[k][1], -A[k][2]] : Demo.v.flip(A[k]))));
    ok(`a mirrored pose has the opposite twist and gives the mirror image of every joint (most ${mm(off, 9)} off, 0.000001 mm allowed)`, same(m.twist, [-30, 0, 0]) && B.twist === -30 && off < 1e-9);
  }

  // A clip can carry it: stored, read back, blended between frames, moved to fit a body, composed into a scene.
  {
    const fr = Array.from({ length: 5 }, (_, i) => ({ hR: [-0.3, 1.1 + 0.01 * i, 0.3], twist: [45 * Math.sin(i * 1.3), 0, 0] }));
    const back = Demo.unpack(JSON.parse(JSON.stringify(Demo.pack({ fr, props: [], dur: 1, loop: false }))));
    const lost = Math.max(...fr.map((f, i) => Math.abs(back.fr[i].twist[0] - f.twist[0])));
    const flat = Demo.unpack(JSON.parse(JSON.stringify(Demo.pack({ fr: fr.map(f => ({ ...f, twist: [-22.5, 0, 0] })), props: [], dur: 1, loop: false }))));
    ok(`a stored clip gives back the twist of each frame (most ${lost.toExponential(1)} degrees off, 0.00005 allowed: a clip keeps four decimals), and one that does not change exactly`, lost <= 5.0001e-5 && back.fr.every(f => f.twist[1] === 0 && f.twist[2] === 0) && flat.fr.every(f => same(f.twist, [-22.5, 0, 0])));
    const half = Demo.pose({ fr: [twisted({}, 10), twisted({}, 30)], props: [], dur: 1, loop: false }, 0.5, false), mir = Demo.pose({ fr: [twisted({}, 10), twisted({}, 30)], props: [], dur: 1, loop: false }, 0.5, true);
    Demo.setBody(1.035, 1.1);
    const tall = Demo.fit(twisted({ hR: [-0.3, 1.1, 0.3] }, 30));
    Demo.setBody(1, 1);
    ok('half way between frames of 10 and 30 degrees the player shows 20, mirrored -20, and a taller body does not lift it', same(half.twist, [20, 0, 0]) && same(mir.twist, [-20, 0, 0]) && same(tall.twist, [30, 0, 0]) && tall.hR[1] > 1.1, JSON.stringify([half.twist, mir.twist, tall.twist]));
    // A hand-driven clip whose frames hold a twist, put together into a scene as every move is.
    const hand = { kind: 'staff', dur: 1, loop: true, method: 'hand', fr: Array.from({ length: 40 }, (_, i) => { const h = [-0.25, 1.15, 0.4]; return { h, a: add(h, [0, 0.42, 0]), b: add(h, [0, -0.42, 0]), twist: [30 * Math.sin(i / 40 * 2 * Math.PI), 0, 0] }; }) };
    const sc = scene({ n: 'twist', make: () => hand }), tw = sc.fr.map(f => f.twist);
    const want = i => 30 * Math.sin(i / sc.fr.length * 2 * Math.PI), err = Math.max(...tw.map((t, i) => t ? Math.abs(t[0] - want(i)) : Infinity));
    // Blending straight between 40 frames of a swing of 30 degrees cuts each bend short by up to 30 * (1 - cos(180 / 40 degrees)) = 0.093 degrees.
    ok(`a scene carries the twist of its move into every frame (${sc.fr.length} frames, most ${err.toFixed(3)} degrees from the move's own, 0.1 allowed: blending between its 40 frames loses 0.093)`, tw.every(t => t && t[1] === 0 && t[2] === 0) && err < 0.1 && C.bodyErrors(sc, {}).errs.length === 0, JSON.stringify(C.bodyErrors(sc, {}).errs));
  }
  return { n, bad, skipped: 0 };
}

module.exports = { run, POSES, TWISTS, twisted, turn, angleAbout };
if (require.main === module) {
  const r = run();
  console.log(`${r.n} checks, ${r.bad} failed, ${r.skipped} skipped`);
  process.exit(r.bad ? 1 : 0);
}
