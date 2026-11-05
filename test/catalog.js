// Checks a prop catalog against docs/catalog-format.md.
//   node test/catalog.js web/catalog/poi.json [...]
const fs = require('fs');
let failed = 0;
for (const file of process.argv.slice(2)) {
  const errs = [], warn = [];
  let c;
  try { c = JSON.parse(fs.readFileSync(file, 'utf8')); } catch (e) { console.log(`${file}: not valid JSON: ${e.message}`); failed++; continue; }
  const str = (v, min = 1, max = 400) => typeof v === 'string' && v.trim().length >= min && v.length <= max;
  for (const k of ['id', 'name', 'about', 'careNote']) if (!str(c[k], 1, 600)) errs.push(`missing or bad "${k}"`);
  const branches = Array.isArray(c.branches) ? c.branches : [], skills = Array.isArray(c.skills) ? c.skills : [], refs = c.refs && typeof c.refs === 'object' ? c.refs : {};
  if (branches.length < 5 || branches.length > 11) errs.push(`needs 5 to 11 branches, has ${branches.length}`);
  const bk = new Set();
  for (const b of branches) {
    if (!/^[A-Z]$/.test(b.k || '')) errs.push(`branch key "${b.k}" is not one capital letter`);
    if (bk.has(b.k)) errs.push(`duplicate branch ${b.k}`);
    bk.add(b.k);
    if (!str(b.name, 1, 40) || !str(b.blurb, 1, 120)) errs.push(`branch ${b.k}: bad name or blurb`);
  }
  const ids = new Map();
  for (const s of skills) {
    const at = `skill ${s.id}`;
    if (!/^[A-Z][1-9][0-9]?$/.test(s.id || '')) { errs.push(`${at}: bad id`); continue; }
    if (ids.has(s.id)) errs.push(`${at}: duplicate`);
    ids.set(s.id, s);
    if (!bk.has(s.id[0])) errs.push(`${at}: no branch ${s.id[0]}`);
    if (![1, 2, 3, 4, 5].includes(s.tier)) errs.push(`${at}: tier must be 1 to 5`);
    if (!str(s.name, 2, 60)) errs.push(`${at}: bad name`);
    if (!str(s.desc, 10, 260)) errs.push(`${at}: bad desc`);
    if (!['one', 'LR', 'FR'].includes(s.track)) errs.push(`${at}: track must be one, LR or FR`);
    if (!Array.isArray(s.needs)) errs.push(`${at}: needs must be a list`);
    if (!Array.isArray(s.steps) || s.steps.length < 1 || s.steps.length > 5 || !s.steps.every(x => str(x, 5, 320))) errs.push(`${at}: needs 1 to 5 steps`);
    if (!Array.isArray(s.watch) || s.watch.length < 1 || s.watch.length > 3 || !s.watch.every(x => str(x, 5, 240))) errs.push(`${at}: needs 1 to 3 watch items`);
    if (!str(s.drill, 10, 320)) errs.push(`${at}: needs a drill`);
    if (!Array.isArray(s.refs)) errs.push(`${at}: refs must be a list`);
    else { for (const r of s.refs) if (!refs[r]) errs.push(`${at}: unknown ref "${r}"`); if (!s.refs.length) warn.push(`${at}: no refs`); }
    for (const k of ['star', 'care']) if (s[k] !== undefined && typeof s[k] !== 'boolean') errs.push(`${at}: ${k} must be true or false`);
    const text = [s.name, s.desc, s.drill, ...(s.steps || []), ...(s.watch || [])].join(' ');
    if (/[—–]/.test(text)) errs.push(`${at}: contains a long dash; use plain punctuation`);
  }
  for (const s of skills) for (const n of s.needs || []) {
    const id = String(n).replace(/!$/, '');
    if (!ids.has(id)) errs.push(`skill ${s.id}: needs unknown ${n}`);
    else if (id === s.id) errs.push(`skill ${s.id}: needs itself`);
    else if (ids.get(id).tier > s.tier) warn.push(`skill ${s.id} (T${s.tier}) needs ${id} (T${ids.get(id).tier}), a higher tier`);
  }
  const state = {};
  const visit = (id, path) => {
    if (state[id] === 2) return;
    if (state[id] === 1) { errs.push(`cycle: ${[...path, id].join(' > ')}`); return; }
    state[id] = 1;
    for (const n of ids.get(id).needs || []) { const k = String(n).replace(/!$/, ''); if (ids.has(k)) visit(k, [...path, id]); }
    state[id] = 2;
  };
  for (const id of ids.keys()) visit(id, []);
  for (const b of bk) if (!skills.some(s => s.id && s.id[0] === b)) errs.push(`branch ${b} has no skills`);
  for (const [k, v] of Object.entries(refs)) if (!Array.isArray(v) || !str(v[0], 3, 140) || !/^https:\/\/[^\s]+$/.test(v[1] || '')) errs.push(`ref ${k}: needs [label, https url]`);
  const used = new Set(skills.flatMap(s => s.refs || []));
  for (const k of Object.keys(refs)) if (!used.has(k)) warn.push(`ref ${k} is never used`);
  const phases = Array.isArray(c.phases) ? c.phases : [];
  if (phases.length < 3 || phases.length > 6) errs.push(`needs 3 to 6 phases, has ${phases.length}`);
  phases.forEach((p, i) => {
    if (p.id !== `p${i + 1}`) errs.push(`phase ${i + 1}: id must be p${i + 1}`);
    if (!str(p.name, 2, 40) || !str(p.time, 2, 40) || !str(p.gate, 10, 260)) errs.push(`phase ${p.id}: bad name, time or gate`);
    for (const id of String(p.skills || '').split(' ').filter(Boolean)) if (!ids.has(id)) errs.push(`phase ${p.id}: unknown skill ${id}`);
    if (!Array.isArray(p.req) || !p.req.length) errs.push(`phase ${p.id}: needs req`);
    for (const r of p.req || []) if (!ids.has(String(r).replace(/!$/, ''))) errs.push(`phase ${p.id}: unknown req ${r}`);
  });
  const stars = skills.filter(s => s.star).length;
  console.log(`${file}: ${skills.length} skills, ${branches.length} branches, ${Object.keys(refs).length} refs, ${phases.length} phases, ${stars} unconfirmed, ${skills.filter(s => s.care).length} care`);
  for (const e of errs.slice(0, 40)) console.log('  ERROR ' + e);
  if (errs.length > 40) console.log(`  ... and ${errs.length - 40} more errors`);
  for (const w of warn.slice(0, 12)) console.log('  warn  ' + w);
  if (errs.length) failed++;
}
process.exit(failed ? 1 : 0);
