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
//   node test/body3d.js parts       only the avatar parts; `clips` only the clips; `rig` only the rig's fit
const fs = require('fs'), path = require('path');
const { Demo } = require('../sim/engine.js'), P = require('../sim/presets.js'), { SPECS } = require('../sim/specs.js');
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
// How far a part may stand out of the skin: face parts, glasses and wristbands sit on it; hats have
// brims and hair hangs (a ponytail, long hair). Every part must show: at least 2 mm proud of the skin.
const REACH = { hat: 0.07, hair: 0.10 }, SHOW = 0.002, RIG_SLACK = 0.025;

// The rig's capsules (Demo.caps, what the physics checks keep the props clear of) against the drawn skin,
// standing: how far the skin reaches outside the capsules, by part. Whatever the checks let a prop come
// to, the drawn figure adds this, so it may be at most RIG_SLACK anywhere. Returns the worst, in metres.
function rig(F) {
  const { J, hands } = Demo.stage({}, {}, []);
  F.probe(J, hands, []);
  const caps = Demo.caps(J, {}), { sub, dot, len } = Demo.v;
  const out = {};
  for (const [x, y, z, bone] of F.skinPoints()) {
    if (/Hand|Foot|Toe|HeadTop/.test(bone)) continue; // hands and feet have no capsule
    const p = [x, y, z];
    let best = Infinity, name = '';
    for (const c of caps) {
      const d = sub(c.b, c.a), L2 = dot(d, d), t = L2 > 1e-12 ? Math.max(0, Math.min(1, dot(sub(p, c.a), d) / L2)) : 0;
      const q = [c.a[0] + d[0] * t, c.a[1] + d[1] * t, c.a[2] + d[2] * t], o = len(sub(p, q)) - c.r;
      if (o < best) { best = o; name = c.name; }
    }
    const k = `${name}${/^Left/.test(bone) ? ' (left)' : /^Right/.test(bone) ? ' (right)' : ''}`;
    if (!out[k] || best > out[k].out) out[k] = { out: best, bone, at: p.map(v => Math.round(v * 1000) / 1000) };
  }
  console.log('capsule'.padEnd(20) + 'skin outside it  where');
  for (const [k, v] of Object.entries(out).sort((a, b) => b[1].out - a[1].out)) console.log(`${k.padEnd(20)}${mm(v.out).padEnd(17)}${v.bone} at ${v.at.join(', ')}`);
  return Math.max(...Object.values(out).map(v => v.out));
}

// Every avatar part, in every pose: outside the skin, within reach, and the same in every pose (a part
// is fixed to one bone). Returns the number of failures.
function parts(F) {
  let bad = 0;
  const seen = {};
  for (const cfg of CFGS) {
    F.setAvatar(cfg);
    for (const [name, pose] of Object.entries(POSES)) {
      const { J, hands } = Demo.stage({}, pose, []);
      F.probe(J, hands, []);
      for (const [part, w] of Object.entries(F.partReport())) (seen[part] = seen[part] || []).push({ cfg: CFGS.indexOf(cfg), pose: name, ...w });
    }
  }
  F.setAvatar(null);
  console.log('part'.padEnd(12) + 'proud of the skin (least, most)  deepest inside');
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
    if (what === 'rig') process.exit(ok ? 0 : 1);
  }
  if (what !== 'clips') { partBad += parts(F); console.log(partBad ? `FAIL ${partBad} checks` : 'avatar parts: every part shows, stays near the skin and follows its bone'); console.log(); }
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
