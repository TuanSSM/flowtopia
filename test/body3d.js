'use strict';
// The 3D figure against every demo clip: no prop goes into the body it is drawn on, and the avatar's
// parts stand out of the skin. The figure is posed and skinned here as in the browser (figure/src/
// figure.js), without WebGL, and the props are measured against its own mesh, not against the rig's
// capsules: the drawn body is what people see.
//   node test/body3d.js             every clip on the standard body
//   ONLY=nunchaku:F5 node ...       one move
//   PRESETS=1 node ...              every body preset too (only clips that pass the body checks there)
//   FRAMES=0 node ...               the neutral frame only (the feminine and masculine frames are on by default)
//   ALL=1 node ...                  list every clip, not only the ones over the limit
//   node test/body3d.js parts       only the avatar parts; `clips` only the clips; `rig` only the rig's fit,
//                                   standing and with the chest turned against the hips
const fs = require('fs'), path = require('path');
const { Demo } = require('../sim/engine.js'), P = require('../sim/presets.js'), { SPECS } = require('../sim/specs.js'), T = require('./twist.js');
const DIR = path.join(__dirname, '..', 'web', 'clips');
// How far a prop's surface may go into the skin, in metres: what the rig's capsules may miss of the skin
// (RIG_SLACK below), since the physics checks (sim/check.js) keep the props clear of the capsules, not of
// the mesh. A clip the checks let touch the body (contact) may go 10 mm further, as the checks allow it 20
// mm against the capsules instead of 8; a clip turned by finger work (fingers) is not measured against the
// arm that holds the prop, as the checks do not measure it either.
const LIMIT = Number(process.env.LIMIT || 0.025), CONTACT = 0.01;
// A held prop against the arm that holds it is the checks' own second measure (grip): they let it sink
// 10 mm into the forearm capsule, which with the capsules' slack is 35 mm of the drawn skin.
const GRIP = 0.035;
const PHASES = 8;
const mm = x => (x * 1000).toFixed(0) + ' mm';
// Avatars with every kind of part, and poses that move the head and the hands about.
const HEAVY = { v: 1, body: 'tall', build: 'broad', skin: '#5b3422', hair: 'curly', hairColor: '#d6b47a', eyes: 'wide', brows: 'thick', mouth: 'grin', top: 'hoodie', topColor: '#c4572e', bottoms: 'trousers', bottomsColor: '#1f2a44', shoes: 'boots', shoesColor: '#2b2b2b', hat: 'bucket', hatColor: '#2f6f4f', glasses: 'square', wristbands: 'yes', wristColor: '#d9a441', props: {} };
const CFGS = [HEAVY, { ...HEAVY, hair: 'ponytail', hat: 'cap', glasses: 'round', eyes: 'sleepy', mouth: 'open', brows: 'arched' }, { ...HEAVY, hair: 'long', hat: 'beanie', glasses: 'none', mouth: 'smile', brows: 'angled', eyes: 'narrow' }, { ...HEAVY, hair: 'bun', hat: 'none', mouth: 'neutral', brows: 'straight', eyes: 'round' }, { ...HEAVY, hair: 'buzz', hat: 'none' }, { ...HEAVY, hair: 'short', hat: 'none' }];
const POSES = { stand: {}, 'arms up': { hL: [0.25, 1.95, 0.05], hR: [-0.25, 1.95, 0.05] }, 'deep bend': { pelvis: [0, 0.78, -0.12], spine: [0, 0.75, 0.66], hL: [0.2, 0.75, 0.35], hR: [-0.2, 0.75, 0.35] }, turn: { face: [0.8, 0, 0.6] }, 'hand behind back': { hR: [-0.1, 1.0, -0.2] } };
// The same with the chest turned against the hips, beside the standing pose they are compared with.
const TURNED = { stand: {}, 'chest turned': { twist: [45, 0, 0] }, 'chest turned, arms up': { twist: [-45, 0, 0], hL: [0.25, 1.95, 0.05], hR: [-0.25, 1.95, 0.05] }, 'chest turned in a bend': { twist: [30, 0, 0], pelvis: [0, 0.78, -0.12], spine: [0, 0.75, 0.66] } };
// How far a part may stand out of the skin: face parts, glasses and wristbands sit on it; hats have
// brims and hair hangs (a ponytail, long hair). Every part must show: at least 2 mm proud of the skin.
const REACH = { hat: 0.07, hair: 0.10 }, SHOW = 0.002, RIG_SLACK = 0.025;

// How far the point p is outside the capsules: its distance from the surface of the nearest one (negative
// inside it), and that capsule's name.
function beyond(p, caps) {
  const { sub, dot, len } = Demo.v;
  let best = Infinity, name = '';
  for (const c of caps) {
    const d = sub(c.b, c.a), L2 = dot(d, d), t = L2 > 1e-12 ? Math.max(0, Math.min(1, dot(sub(p, c.a), d) / L2)) : 0;
    const q = [c.a[0] + d[0] * t, c.a[1] + d[1] * t, c.a[2] + d[2] * t], o = len(sub(p, q)) - c.r;
    if (o < best) { best = o; name = c.name; }
  }
  return [best, name];
}
const NO_CAPSULE = /Hand|Foot|Toe|HeadTop/; // hands and feet have no capsule
// The rig's capsules (Demo.caps, what the physics checks keep the props clear of) against the drawn skin,
// standing: how far the skin reaches outside the capsules, by part. Whatever the checks let a prop come
// to, the drawn figure adds this, so it may be at most RIG_SLACK anywhere. Returns the worst, in metres.
function rig(F) {
  const { J, hands } = Demo.stage({}, {}, []);
  F.probe(J, hands, []);
  const caps = Demo.caps(J, {});
  const out = {};
  for (const [x, y, z, bone] of F.skinPoints()) {
    if (NO_CAPSULE.test(bone)) continue;
    const p = [x, y, z], [best, name] = beyond(p, caps);
    const k = `${name}${/^Left/.test(bone) ? ' (left)' : /^Right/.test(bone) ? ' (right)' : ''}`;
    if (!out[k] || best > out[k].out) out[k] = { out: best, bone, at: p.map(v => Math.round(v * 1000) / 1000) };
  }
  console.log('capsule'.padEnd(20) + 'skin outside it  where');
  for (const [k, v] of Object.entries(out).sort((a, b) => b[1].out - a[1].out)) console.log(`${k.padEnd(20)}${mm(v.out).padEnd(17)}${v.bone} at ${v.at.join(', ')}`);
  return Math.max(...Object.values(out).map(v => v.out));
}

// The chest turned against the hips (the `twist` of a pose): the figure posed from the turned rig, on the
// standard body, the smallest and the largest, in four poses, at 15, 30 and 45 degrees each way. The rig's
// own answers are in test/twist.js; here the model is held to the rig.
//   the hips and legs    bones and skin where they are without the twist, to the last digit.
//   the shoulder line    turned against the hip line by the twist, about the spine, within SHOULDER_LINE degrees.
//   the shoulder joints  within PLACED of where the untwisted pose has them once that is carried round with the
//                        chest, a lifted collarbone included. (The model's shoulder joint is not at the rig's
//                        shoulder, twist or no twist: the rig's is the middle of the deltoid, about 5 cm further
//                        out. The table gives that distance too.)
//   the neck and head    the neck's joint is where the ribcage carries it and, with the spine upright, the head's
//                        joint is where the rig has the head (turned by its share about the spine's line), both
//                        within PLACED; the neck between them is within NECK_GIVE of its length and points at
//                        the head's joint within NECK_AIM degrees; the head is nearer upright than the trunk in
//                        every case.
//   elbows and grips     on the rig's within PLACED. This holds by construction (the forearm bone is put at the
//                        rig's elbow, and a grip is worked back from where its hand was put), so it fails only
//                        if something moves an arm after it was placed, as turning the spine last would.
//   the skin, standing   within RIG_SLACK of the capsules at every twist, as it is without one.
//   the skin, any pose   a twist of itself costs the fit nothing: no part is more than OWN further out than the
//                        larger of RIG_SLACK and what it is on the same upper body without the twist (the hands
//                        and elbows carried back round the spine, so the arms have the same shape). With the
//                        hands held still instead, a twist bends the arms into another shape, and the capsules
//                        fit some shapes of an arm worse than others, twist or no twist (a raised upper arm is
//                        55 mm out untwisted; nothing but the standing pose had been measured before). Every
//                        part that is within RIG_SLACK untwisted and leaves it when the chest turns is listed,
//                        and must be an upper arm that is as far out on the same upper body untwisted.
//   arms carried         standing or leaning with the arms hanging, the arms and hands go round with the chest as
//                        one piece, and standing the head's skin by the head's share (within PLACED), so the
//                        elbow's hinge, the forearm's turn and the fingers are what they are without a twist.
//                        The same with an arm dead straight (its wrist put on the line of the upper arm), where
//                        the elbow has no bend to say which way its crease faces.
//   the trunk, standing  no point of its skin comes nearer the spine's line than PINCH of where it was (blending
//                        two bones 25 degrees apart gives 0.977: a waist that pinched would give less), and the
//                        turn is spread as a back's is: the skin of the hips does not turn, the waist turns a
//                        little (under A_LITTLE of the twist), the upper chest most of it (over MOST), each part
//                        more than the one below.
// Returns the number of failures.
const SHOULDER_LINE = 0.1, PLACED = 1e-5, NECK_GIVE = 0.002, NECK_AIM = 0.01, OWN = 0.001, PINCH = 0.95, A_LITTLE = 0.25, MOST = 0.75;
function twist(F) {
  const { sub, add, mul, dot, cross, len, norm } = Demo.v, UP = [0, 1, 0], SIDES = [['L', 'Left'], ['R', 'Right']];
  const bodies = [{ id: 'standard', sc: 1, bd: 1 }, ...P.PRESETS.filter(p => p.id === 'short-lean' || p.id === 'tall-broad')];
  const LEGS = ['Hips', 'LeftUpLeg', 'RightUpLeg', 'LeftLeg', 'RightLeg', 'LeftFoot', 'RightFoot', 'LeftToeBase', 'RightToeBase'], same = (a, b) => a.every((x, i) => Object.is(x, b[i]));
  const fails = [], left = [], worst = { line: 0, shoulder: 0, neck: 0, head: 0, give: 0, aim: 0, joint: 0, standing: 0, own: -Infinity, carried: 0, straight: 0, pinch: 1 };
  // How far the skin each trunk bone moves most has turned about the spine, as a part of the twist: [bone, what it is called here].
  const TRUNK = [['Hips', 'hips'], ['Spine', 'waist'], ['Spine1', 'lower chest'], ['Spine2', 'upper chest']], turned = Object.fromEntries(TRUNK.map(([n]) => [n, []]));
  let cases = 0, posed = 0;
  // The skin's reach outside the capsules, by the bone that moves it most.
  const outside = (pts, caps) => { const o = {}; for (const q of pts) { if (NO_CAPSULE.test(q[3])) continue; const [d] = beyond(q, caps); if (!(d <= o[q[3]])) o[q[3]] = d; } return o; };
  const staged = (J, hands) => { F.probe(J, hands, []); return { J, hands, B: F.joints(hands), pts: F.skinPoints() }; };
  const stageOf = f => { const st = Demo.stage({}, f, []); return staged(st.J, st.hands); };
  // The most any vertex of the arms and hands is from where the turn carries the one it was.
  const armsOff = (pts, from, about) => pts.reduce((m, q, i) => /Arm$|Hand/.test(q[3]) ? Math.max(m, len(sub(q, about(from[i])))) : m, 0);
  console.log('chest turned'.padEnd(13) + 'pose'.padEnd(13) + 'shoulder line  shoulder joints (from the rig\'s)  skin outside the capsules (untwisted)  more than the same upper body untwisted');
  for (const b of bodies) {
    Demo.setBody(b.sc, b.bd); F.setBody(b.sc, b.bd, 'neutral');
    for (const [name, pose] of Object.entries(T.POSES)) {
      const f0 = Demo.fit(pose), base = stageOf(f0), c = base.J.pelvis, up = base.J.up, out0 = outside(base.pts, Demo.caps(base.J, {})), slack0 = Math.max(...Object.values(out0));
      const line0 = T.angleAbout(sub(base.B.LeftUpLeg, base.B.RightUpLeg), sub(base.B.LeftArm, base.B.RightArm), up), neck0 = len(sub(base.B.Head, base.B.Neck));
      const row = { line: 0, shoulder: 0, apart: [Infinity, 0], slack: 0, own: -Infinity };
      for (const deg of T.TWISTS) {
        cases++; posed++;
        const at = `${b.id}, ${name}, ${deg} degrees`, { J, hands, B, pts } = stageOf(T.twisted(f0, deg)), about = (p, d = deg) => T.turn(p, c, up, d);
        // The hips and the legs.
        if (!LEGS.every(n => same(B[n], base.B[n]))) fails.push(`${at}: a bone of the hips or legs moved`);
        if (!pts.every((q, i) => !/UpLeg|Leg$|Foot|Toe/.test(q[3]) || same(q, base.pts[i]))) fails.push(`${at}: the skin of a leg moved`);
        // The shoulder line against the hip line.
        row.line = Math.max(row.line, Math.abs(T.angleAbout(sub(B.LeftUpLeg, B.RightUpLeg), sub(B.LeftArm, B.RightArm), up) - line0 - deg));
        // Each shoulder joint against the untwisted one carried round; elbows and grips against the rig's.
        for (const [s, S] of SIDES) {
          worst.joint = Math.max(worst.joint, len(sub(B[S + 'ForeArm'], J['el' + s])), len(sub(B['grip' + s], hands[s].o)));
          row.shoulder = Math.max(row.shoulder, len(sub(B[S + 'Arm'], about(base.B[S + 'Arm']))));
          const apart = len(sub(B[S + 'Arm'], J['sh' + s])); row.apart = [Math.min(row.apart[0], apart), Math.max(row.apart[1], apart)];
        }
        // The neck's joint rides on the ribcage. With the spine upright the head's joint is over the spine's line as the rig has the head, and the neck reaches it.
        worst.neck = Math.max(worst.neck, len(sub(B.Neck, about(base.B.Neck))));
        worst.aim = Math.max(worst.aim, Math.asin(Math.min(1, len(cross(B.neckTo, norm(sub(B.Head, B.Neck)))))) * 180 / Math.PI);
        if (!f0.spine) { worst.head = Math.max(worst.head, len(sub(B.Head, about(base.B.Head, deg * (1 - Demo.HEAD_BACK))))); worst.give = Math.max(worst.give, Math.abs(len(sub(B.Head, B.Neck)) - neck0)); }
        if (dot(up, UP) - dot(B.headUp, UP) > 1e-9) fails.push(`${at}: the head leans further than the trunk`);
        // The skin against the capsules, part by part: against the same upper body without the twist, and against the same hands without it.
        const out = outside(pts, Demo.caps(J, {}));
        let outE = out0;
        if (f0.hL) { const e = { ...f0 }; for (const [s] of SIDES) { e['h' + s] = about(J['h' + s], -deg); e['e' + s] = about(J['el' + s], -deg); } const E = stageOf(e); outE = outside(E.pts, Demo.caps(E.J, {})); }
        row.slack = Math.max(row.slack, ...Object.values(out));
        for (const k in out) {
          row.own = Math.max(row.own, out[k] - Math.max(RIG_SLACK, outE[k]));
          if (out0[k] <= RIG_SLACK && out[k] > RIG_SLACK) {
            left.push(`${at}: ${k} ${(out0[k] * 1000).toFixed(1)} mm untwisted, ${(out[k] * 1000).toFixed(1)} mm twisted, ${(outE[k] * 1000).toFixed(1)} mm on the same upper body untwisted`);
            if (!/^(Left|Right)Arm$/.test(k) || Math.abs(out[k] - outE[k]) > OWN) fails.push(`${at}: the skin of ${k} leaves ${mm(RIG_SLACK)} and it is not an upper arm that is as far out untwisted`);
          }
        }
        // With the arms hanging everything above the waist goes round as one piece.
        if (!f0.hL) worst.carried = Math.max(worst.carried, armsOff(pts, base.pts, about));
        if (name === 'standing') {
          const far = v => { const r = sub(v, c); return len(sub(r, mul(up, dot(r, up)))); };
          pts.forEach((q, i) => {
            const o = base.pts[i], n = q[3];
            if (/^Head/.test(n)) worst.carried = Math.max(worst.carried, len(sub(q, about(o, deg * (1 - Demo.HEAD_BACK)))));
            else if (/^(Hips|Spine)/.test(n) && far(o) > 0.04) { worst.pinch = Math.min(worst.pinch, far(q) / far(o)); turned[n].push(T.angleAbout(sub(o, c), sub(q, c), up) / deg); }
          });
          const head = Math.abs(T.angleAbout(base.B.headLeft, B.headLeft, up) - deg * (1 - Demo.HEAD_BACK));
          if (head > SHOULDER_LINE) fails.push(`${at}: the head is turned ${head.toFixed(2)} degrees from its share of the twist`);
          worst.standing = Math.max(worst.standing, row.slack);
        }
      }
      for (const k of ['line', 'shoulder', 'own']) worst[k] = Math.max(worst[k], row[k]);
      console.log(`${b.id.padEnd(13)}${name.padEnd(13)}${(row.line.toFixed(3) + ' deg').padEnd(15)}${`${(row.shoulder * 1000).toFixed(3)} mm (${mm(row.apart[0])}${mm(row.apart[1]) === mm(row.apart[0]) ? '' : ' to ' + mm(row.apart[1])})`.padEnd(34)}${`${mm(row.slack)} (${mm(slack0)})`.padEnd(39)}${(row.own * 1000).toFixed(1)} mm`);
    }
    // An arm dead straight: standing, each wrist is put on the line of its upper arm, 30 cm past the elbow, by
    // giving the figure a hand that lies along that line, its thumb side where the hanging hand has it (so the
    // forearm is not asked to turn further than it can). Then the same, turned: joints and hands carried round.
    const stand = stageOf({}), c = stand.J.pelvis, up = stand.J.up, r = 0.012, hands0 = {};
    for (const [s, S] of SIDES) {
      const el = stand.J['el' + s], along = norm(sub(el, stand.B[S + 'Arm'])), x = norm(sub(stand.hands[s].x, mul(along, dot(stand.hands[s].x, along)))), n = mul(cross(x, along), s === 'L' ? -1 : 1);
      hands0[s] = { o: add(add(el, mul(along, 0.3)), mul(n, r + 0.01)), x, w: along, grip: 'closed', r };
    }
    const straight = staged(stand.J, hands0);
    for (const [s, S] of SIDES) { const bend = len(cross(norm(sub(straight.B[S + 'ForeArm'], straight.B[S + 'Arm'])), norm(sub(straight.B[S + 'Hand'], straight.B[S + 'ForeArm'])))); if (bend > 1e-6) fails.push(`${b.id}: the arm meant to be straight is bent by ${(Math.asin(bend) * 180 / Math.PI).toFixed(4)} degrees`); }
    for (const deg of T.TWISTS) {
      cases++;
      const about = p => T.turn(p, c, up, deg), dir = v => T.turn(v, [0, 0, 0], up, deg), hands = {};
      for (const [s] of SIDES) hands[s] = { ...hands0[s], o: about(hands0[s].o), x: dir(hands0[s].x), w: dir(hands0[s].w) };
      worst.straight = Math.max(worst.straight, armsOff(staged(Demo.rig(T.twisted({}, deg)), hands).pts, straight.pts, about));
    }
  }
  Demo.setBody(1, 1); F.setBody(1, 1);
  // The spread over the trunk: the middle value of each part's turn.
  const mid = Object.fromEntries(TRUNK.map(([n]) => { const v = turned[n].sort((x, y) => x - y); return [n, v[v.length >> 1]]; }));
  const spread = Math.abs(mid.Hips) < 0.01 && mid.Spine > mid.Hips && mid.Spine < A_LITTLE && mid.Spine1 > mid.Spine && mid.Spine2 > mid.Spine1 && mid.Spine2 > MOST;
  const um = v => (v * 1000).toFixed(4) + ' mm', some = re => fails.some(f => re.test(f));
  // What was measured, each with its limit; a line that fails says so.
  const lines = [
    [!some(/moved$/), 'the hips and legs: bones and skin where they are without the twist, to the last digit'],
    [worst.line <= SHOULDER_LINE, `the shoulder line: within ${worst.line.toFixed(3)} degrees of the twist against the hip line (limit ${SHOULDER_LINE})`],
    [worst.shoulder <= PLACED, `shoulder joints: within ${um(worst.shoulder)} of where the turn carries the untwisted ones (limit ${PLACED * 1000} mm)`],
    [worst.neck <= PLACED, `the neck's joint: within ${um(worst.neck)} of where the ribcage carries it (limit ${PLACED * 1000} mm)`],
    [worst.head <= PLACED && worst.give <= NECK_GIVE && worst.aim <= NECK_AIM, `the head's joint, spine upright: within ${um(worst.head)} of where the rig has the head (limit ${PLACED * 1000} mm); the neck gives ${(worst.give * 1000).toFixed(1)} mm of its length to reach it (limit ${NECK_GIVE * 1000} mm) and points at it within ${worst.aim.toFixed(4)} degrees in every pose (limit ${NECK_AIM})`],
    [!some(/further than the trunk$/), 'the head: nearer upright than the trunk in every case'],
    [worst.joint <= PLACED, `elbows and grips: within ${um(worst.joint)} of the rig's, where they were placed (limit ${PLACED * 1000} mm)`],
    [worst.standing <= RIG_SLACK, `the skin, standing: at most ${mm(worst.standing)} outside the rig's capsules (limit ${mm(RIG_SLACK)})`],
    [worst.own <= OWN, `the skin, any pose: no part more than ${(worst.own * 1000).toFixed(1)} mm further out than on the same upper body untwisted, or than ${mm(RIG_SLACK)} (limit ${OWN * 1000} mm)`],
    [!some(/as far out untwisted$/), `the skin, hands held still: ${left.length} times in ${posed} a part within ${mm(RIG_SLACK)} untwisted leaves it when the chest turns${left.length ? ', each an upper arm that is as far out on the same upper body untwisted:' : ''}`],
    ...left.map(l => [true, '  ' + l]),
    [worst.carried <= PLACED && !some(/its share of the twist$/), `arms hanging: they go round with the chest, and standing the head by its share, within ${um(worst.carried)} (limit ${PLACED * 1000} mm)`],
    [worst.straight <= PLACED && !some(/meant to be straight/), `an arm dead straight: it goes round with the chest within ${um(worst.straight)} (limit ${PLACED * 1000} mm)`],
    [spread, `standing, the skin turns by this part of the twist: ${TRUNK.map(([n, word]) => `${word} ${Math.round(mid[n] * 100)}%`).join(', ')} (the waist under ${A_LITTLE * 100}%, the upper chest over ${MOST * 100}%, each more than the one below)`],
    [worst.pinch >= PINCH, `standing, the trunk's skin keeps ${worst.pinch.toFixed(3)} of its distance from the spine (limit ${PINCH})`],
  ];
  for (const f of fails.slice(0, 12)) console.log('FAIL ' + f);
  if (fails.length > 12) console.log(`FAIL and ${fails.length - 12} more`);
  console.log(`chest turned up to ${Demo.TWIST} degrees each way, ${cases} cases (${bodies.length} bodies, ${Object.keys(T.POSES).length} poses and a dead straight arm, ${T.TWISTS.length} twists):`);
  for (const [pass, text] of lines) console.log(`  ${pass ? '' : 'FAIL '}${text}`);
  return lines.filter(l => !l[0]).length;
}

// Every avatar part, in every pose: outside the skin, within reach, and the same in every pose (a part
// is fixed to one bone). Returns the number of failures.
function parts(F, poses = POSES, title = 'part') {
  let bad = 0;
  const seen = {};
  for (const cfg of CFGS) {
    F.setAvatar(cfg);
    for (const [name, pose] of Object.entries(poses)) {
      const { J, hands } = Demo.stage({}, pose, []);
      F.probe(J, hands, []);
      for (const [part, w] of Object.entries(F.partReport())) (seen[part] = seen[part] || []).push({ cfg: CFGS.indexOf(cfg), pose: name, ...w });
    }
  }
  F.setAvatar(null);
  console.log(title.padEnd(12) + 'proud of the skin (least, most)  deepest inside');
  for (const [part, list] of Object.entries(seen)) {
    const outs = list.map(x => x.out), ins = list.map(x => x.in), lo = Math.min(...outs), hi = Math.max(...outs);
    const shows = lo >= SHOW, near = hi <= (REACH[part] || 0.03);
    // The same part on the same avatar must measure the same in every pose, within 3 mm.
    let moved = 0;
    for (let c = 0; c < CFGS.length; c++) { const o = list.filter(x => x.cfg === c).map(x => x.out); if (o.length && Math.max(...o) - Math.min(...o) > 0.003) moved++; }
    console.log(`${part.padEnd(12)}${(mm(lo) + ', ' + mm(hi)).padEnd(33)}${mm(Math.min(...ins))}`);
    if (!shows) { bad++; console.log(`FAIL ${part}: only ${mm(lo)} proud of the skin on ${JSON.stringify(list.find(x => x.out === lo))}`); }
    if (!near) { bad++; console.log(`FAIL ${part}: ${mm(hi)} from the skin, more than ${mm(REACH[part] || 0.03)}`); }
    if (moved) { bad++; console.log(`FAIL ${part}: changes with the pose on ${moved} avatars`); }
  }
  return bad;
}

// The figure, loaded as the browser loads it but without WebGL: for this test and the others that pose it
// in Node (test/skin-once.js, test/alloc.js).
async function figure() {
  globalThis.window = globalThis;
  await import('../figure/src/figure.js');
  const F = window.Figure3D, glb = fs.readFileSync(path.join(__dirname, '..', 'web', 'assets', 'human.glb'));
  if (!await F.load('', glb.buffer.slice(glb.byteOffset, glb.byteOffset + glb.byteLength))) { console.log('FAIL the model did not load:', F.reason); process.exit(1); }
  return F;
}

async function main() {
  const F = await figure();
  const what = process.argv[2] || 'both';
  let partBad = 0;
  if (what !== 'clips' && what !== 'parts') {
    const slack = rig(F), ok = slack <= RIG_SLACK;
    console.log(ok ? `the skin reaches at most ${mm(slack)} outside the rig's capsules (limit ${mm(RIG_SLACK)})` : `FAIL the skin reaches ${mm(slack)} outside the rig's capsules, more than ${mm(RIG_SLACK)}: fit Demo.caps to the model`);
    partBad += ok ? 0 : 1; console.log();
    const turned = twist(F);
    partBad += turned; console.log();
    if (what === 'rig') process.exit(ok && !turned ? 0 : 1);
  }
  if (what !== 'clips') { partBad += parts(F) + parts(F, TURNED, 'part turned'); console.log(partBad ? `FAIL ${partBad} checks` : 'avatar parts: every part shows, stays near the skin and follows its bone'); console.log(); }
  if (what === 'parts') process.exit(partBad ? 1 : 0);
  const index = JSON.parse(fs.readFileSync(path.join(DIR, 'index.json'), 'utf8')), fails = P.load().fail;
  const std = { id: 'standard', sc: 1, bd: 1, frame: 'neutral' };
  const bodies = [std, ...(process.env.FRAMES === '0' ? [] : ['feminine', 'masculine'].map(f => ({ ...std, id: f, frame: f }))), ...(process.env.PRESETS ? P.PRESETS.map(p => ({ ...p, frame: 'neutral' })) : [])];
  const rows = [], t0 = Date.now();
  let frames = 0;
  for (const [key, e] of Object.entries(index)) {
    if (process.env.ONLY && key !== process.env.ONLY) continue;
    for (const v of e.v) {
      const clip = Object.assign(Demo.unpack(JSON.parse(fs.readFileSync(path.join(DIR, v.f + '.json'), 'utf8'))), { f: v.f });
      if (clip.hide) continue; // a close-up shows the hands only
      const [prop, id] = key.split(':'), sp = ((SPECS[prop] || {})[id] || []).find(x => x.n === v.n) || {}, limit = LIMIT + (sp.contact ? CONTACT : 0);
      for (const b of bodies) {
        if (fails[v.f] && fails[v.f][b.id]) continue; // not offered on that body
        Demo.setBody(b.sc, b.bd); F.setBody(b.sc, b.bd, b.frame);
        const phases = v.m === 'still' ? [0] : Array.from({ length: PHASES }, (_, i) => i / PHASES);
        let worst = { depth: 0 }, grip = { grip: 0 };
        for (const ph of phases) {
          const f = Demo.pose(clip, ph, false), props = Demo.propsOf(clip, false), { J, hands, list } = Demo.stage(clip, f, props);
          const w = F.probe(J, hands, list, { fingers: !!sp.fingers });
          frames++;
          if (w.depth > worst.depth) worst = { depth: w.depth, bone: w.bone, at: w.at, ph, role: list[w.item].role };
          if (w.grip > grip.grip) grip = { grip: w.grip, bone: w.gripBone, at: w.gripAt, ph, role: list[w.gripItem].role };
        }
        rows.push({ name: `${key} ${v.n}`, body: b.id, limit, ...worst, g: grip });
      }
    }
  }
  Demo.setBody(1, 1); F.setBody(1, 1);
  rows.sort((a, b) => b.depth - a.depth);
  const over = rows.filter(r => r.depth > r.limit), overGrip = rows.filter(r => r.g.grip > GRIP + (r.limit - LIMIT)), where = r => `${r.role} under ${r.bone} at phase ${r.ph.toFixed(2)}, at ${r.at.join(', ')}`;
  console.log('clip'.padEnd(44) + 'body'.padEnd(12) + 'into the skin  where');
  for (const r of process.env.ALL ? rows : over) console.log(`${r.name.padEnd(44)}${r.body.padEnd(12)}${mm(r.depth).padEnd(15)}${r.bone ? where(r) + (r.limit > LIMIT ? ' (contact)' : '') : 'clear'}`);
  console.log('\nclip'.padEnd(45) + 'body'.padEnd(12) + 'into its own arm  where');
  const byGrip = [...rows].sort((a, b) => b.g.grip - a.g.grip);
  for (const r of process.env.ALL ? byGrip : overGrip) console.log(`${r.name.padEnd(44)}${r.body.padEnd(12)}${mm(r.g.grip).padEnd(18)}${r.g.bone ? where(r.g) : 'clear'}`);
  console.log(`\n${rows.length} clips on ${bodies.length} ${bodies.length === 1 ? 'body' : 'bodies'}, ${frames} frames in ${((Date.now() - t0) / 1000).toFixed(0)} s: ${over.length} over the limit of ${mm(LIMIT)} (${mm(LIMIT + CONTACT)} with contact), worst ${mm(rows[0] ? rows[0].depth : 0)}; ${overGrip.length} with a held prop more than ${mm(GRIP)} into its own arm, worst ${mm(byGrip[0] ? byGrip[0].g.grip : 0)}`);
  over.push(...overGrip);
  process.exit(over.length || partBad ? 1 : 0);
}
if (require.main === module) main().catch(e => { console.log('FAIL', e); process.exit(1); });
module.exports = { figure, HEAVY };
