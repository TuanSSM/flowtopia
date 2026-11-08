'use strict';
// Proposes the `motion` field of each nunchaku skill (docs/catalog-format.md) and reports where the
// clips and the skills' own text disagree.
//   node sim/motion.js           report only
//   node sim/motion.js --write   write the fields into web/catalog/nunchaku.json
// Values come from the baked clips where a skill has one (its plane), and otherwise from words in the
// skill's own name, description and steps. Anything neither settles is "unknown". Nothing is guessed
// from general knowledge.
const fs = require('fs'), path = require('path');
const FILE = path.join(__dirname, '..', 'web', 'catalog', 'nunchaku.json');
const FEAT = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'clips', 'features.json'), 'utf8'));
const cat = JSON.parse(fs.readFileSync(FILE, 'utf8'));
// A clip's plane from its normal: x is the wall plane (side to side), z the face plane, y the table plane.
const planeOf = n => { const a = n.map(Math.abs), i = a.indexOf(Math.max(...a)); return a[i] < 0.9 ? 'mixed' : ['wall', 'table', 'face'][i]; };
const textOf = s => [s.name, s.desc, ...(s.steps || [])].join(' ').toLowerCase();
function fromText(t) {
  const planes = [['wall', /\bwall plane|\bwall-plane|\bside plane/], ['face', /\bface plane|\bfront plane|\bface-plane/], ['table', /\btable plane|\bhorizontal|\btable-plane|\bflat (circle|plane)/]].filter(([, re]) => re.test(t)).map(([p]) => p);
  const fwd = /\bforward\b|\bupspin\b|\bup spin\b/.test(t), rev = /\breverse\b|\bbackward\b|\bdownspin\b|\bdown spin\b/.test(t), either = /\beither direction|\bboth directions|\beach direction/.test(t);
  return { plane: planes.length === 1 ? planes[0] : planes.length > 1 ? 'mixed' : 'unknown', direction: either || (fwd && rev) ? 'either' : fwd ? 'forward' : rev ? 'reverse' : 'unknown' };
}
const report = [], out = {};
for (const s of cat.skills) {
  const t = textOf(s), txt = fromText(t), clips = FEAT['nunchaku:' + s.id];
  let plane = txt.plane, source = txt.plane !== 'unknown' || txt.direction !== 'unknown' ? 'description' : 'unknown';
  if (clips) {
    const ps = [...new Set(clips.flatMap(v => v.props.map(p => planeOf(p.normal))))];
    const clipPlane = ps.length === 1 ? ps[0] : 'mixed';
    // A skill whose text describes a variant in a plane its clip does not show spans several planes.
    plane = txt.plane !== 'unknown' && txt.plane !== clipPlane && clipPlane !== 'mixed' ? 'mixed' : clipPlane;
    if (plane !== clipPlane) report.push(`${s.id} ${s.name}: the clip shows the ${clipPlane} plane; the text also describes the ${txt.plane} plane, so mixed`);
    source = 'clip';
  }
  const hands = s.id[0] === 'D' || /\bboth hands\b|\btwo hands\b|\bhand to hand\b|\bswitch(es)? hands\b|\bpass(es)?\b/.test(t) ? 'both' : /\bone hand\b|\bsame hand\b|\bsingle hand\b/.test(t) ? 'one' : 'unknown';
  const grip = /\btail grip\b|\bby the end\b|\bend of the (stick|handle)\b/.test(t) ? 'end' : /\bmiddle of the (stick|handle)\b/.test(t) ? 'middle' : /\bnormal grip\b|\bby the handle\b|\bhandle grip\b/.test(t) ? 'handle' : 'unknown';
  out[s.id] = { plane, direction: txt.direction, hands, grip, source };
}
const counts = k => Object.values(out).reduce((a, m) => (a[m[k]] = (a[m[k]] || 0) + 1, a), {});
console.log('plane', counts('plane'), '\ndirection', counts('direction'), '\nhands', counts('hands'), '\ngrip', counts('grip'), '\nsource', counts('source'));
console.log(report.length ? 'Skills whose variants span planes:\n  ' + report.join('\n  ') : 'Clips and text agree wherever both say something.');
if (process.argv.includes('--write')) {
  for (const s of cat.skills) s.motion = out[s.id];
  fs.writeFileSync(FILE, JSON.stringify(cat, null, 1) + '\n');
  console.log('written to web/catalog/nunchaku.json');
}
module.exports = { planeOf, fromText };
