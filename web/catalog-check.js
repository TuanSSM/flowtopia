'use strict';
// Checks a prop catalog against docs/catalog-format.md. Shared by the skill map editor in the
// browser and by `node test/catalog.js`; main.go has the same rules in Go (validateCatalog), and
// both must agree on test/catalog-fixtures. Returns errors (the catalog cannot be used) and
// warnings (worth a look), each with the path of the field it is about, for the editor to show.
const MOTION = { plane: ['wall', 'face', 'table', 'mixed', 'unknown'], direction: ['forward', 'reverse', 'either', 'unknown'], hands: ['one', 'both', 'unknown'], grip: ['handle', 'end', 'middle', 'unknown'], source: ['clip', 'description', 'unknown'] };
// The ideas that carry over from one prop to another. A catalog names the ones it teaches, each with a
// sentence on what it means for that prop, and a skill lists the ones it is about. They are chosen by
// a person in the editor and never worked out from a skill's text.
const CONCEPTS = ['plane', 'timing', 'weave', 'isolation', 'extension'];
function checkCatalog(c) {
  const errs = [], warn = [], E = (path, msg) => errs.push({ path, msg }), W = (path, msg) => warn.push({ path, msg });
  if (!c || typeof c !== 'object' || Array.isArray(c)) { E('', 'not a catalog object'); return { errs, warn }; }
  const str = (v, min = 1, max = 400) => typeof v === 'string' && v.trim().length >= min && v.length <= max;
  for (const k of ['id', 'name', 'about', 'careNote']) if (!str(c[k], 1, 600)) E(k, `missing or bad "${k}"`);
  const branches = Array.isArray(c.branches) ? c.branches : [], skills = Array.isArray(c.skills) ? c.skills : [];
  const refs = c.refs && typeof c.refs === 'object' && !Array.isArray(c.refs) ? c.refs : {};
  const concepts = {}, taught = new Set();
  if (c.concepts !== undefined) {
    if (!c.concepts || typeof c.concepts !== 'object' || Array.isArray(c.concepts)) E('concepts', 'concepts must be an object: a concept and a sentence on what it means for this prop');
    else for (const [k, v] of Object.entries(c.concepts)) {
      if (!CONCEPTS.includes(k)) E(`concepts.${k}`, `concepts has no "${k}"; the concepts are ${CONCEPTS.join(', ')}`);
      else if (!str(v, 10, 240)) E(`concepts.${k}`, `concept ${k}: needs a sentence of 10 to 240 characters`);
      else if (/[—–]/.test(v)) E(`concepts.${k}`, `concept ${k}: contains a long dash; use plain punctuation`);
      else concepts[k] = v;
    }
  }
  if (branches.length < 5 || branches.length > 11) E('branches', `needs 5 to 11 branches, has ${branches.length}`);
  const bk = new Set();
  branches.forEach((b, i) => {
    const at = `branches.${i}`;
    if (!/^[A-Z]$/.test((b && b.k) || '')) E(at + '.k', `branch key "${b && b.k}" is not one capital letter`);
    else if (bk.has(b.k)) E(at + '.k', `duplicate branch ${b.k}`);
    if (b && /^[A-Z]$/.test(b.k || '')) bk.add(b.k);
    if (!b || !str(b.name, 1, 40)) E(at + '.name', `branch ${b && b.k}: bad name`);
    if (!b || !str(b.blurb, 1, 120)) E(at + '.blurb', `branch ${b && b.k}: bad blurb`);
  });
  const ids = new Map();
  skills.forEach((s, i) => {
    const at = `skills.${i}`, who = `skill ${s && s.id}`;
    if (!s || !/^[A-Z][1-9][0-9]?$/.test(s.id || '')) { E(at + '.id', `${who}: bad id`); return; }
    if (ids.has(s.id)) E(at + '.id', `${who}: duplicate`);
    ids.set(s.id, s);
    if (!bk.has(s.id[0])) E(at + '.id', `${who}: no branch ${s.id[0]}`);
    if (![1, 2, 3, 4, 5].includes(s.tier)) E(at + '.tier', `${who}: tier must be 1 to 5`);
    if (!str(s.name, 2, 60)) E(at + '.name', `${who}: bad name`);
    if (!str(s.desc, 10, 260)) E(at + '.desc', `${who}: bad desc`);
    if (!['one', 'LR', 'FR'].includes(s.track)) E(at + '.track', `${who}: track must be one, LR or FR`);
    if (!Array.isArray(s.needs)) E(at + '.needs', `${who}: needs must be a list`);
    if (!Array.isArray(s.steps) || s.steps.length < 1 || s.steps.length > 5 || !s.steps.every(x => str(x, 5, 320))) E(at + '.steps', `${who}: needs 1 to 5 steps`);
    if (!Array.isArray(s.watch) || s.watch.length < 1 || s.watch.length > 3 || !s.watch.every(x => str(x, 5, 240))) E(at + '.watch', `${who}: needs 1 to 3 watch items`);
    if (!str(s.drill, 10, 320)) E(at + '.drill', `${who}: needs a drill`);
    if (!Array.isArray(s.refs)) E(at + '.refs', `${who}: refs must be a list`);
    else { for (const r of s.refs) if (!Object.prototype.hasOwnProperty.call(refs, r)) E(at + '.refs', `${who}: unknown ref "${r}"`); if (!s.refs.length && !s.retired) W(at + '.refs', `${who}: no refs`); }
    for (const k of ['star', 'care', 'retired']) if (s[k] !== undefined && typeof s[k] !== 'boolean') E(`${at}.${k}`, `${who}: ${k} must be true or false`);
    if (s.motion !== undefined) {
      // How the move travels, for next-trick suggestions: each value from a fixed list, or unknown.
      if (!s.motion || typeof s.motion !== 'object' || Array.isArray(s.motion)) E(at + '.motion', `${who}: motion must be an object`);
      else for (const [k, v] of Object.entries(s.motion)) {
        if (!MOTION[k]) E(`${at}.motion.${k}`, `${who}: motion has no field "${k}"`);
        else if (!MOTION[k].includes(v)) E(`${at}.motion.${k}`, `${who}: motion ${k} must be one of ${MOTION[k].join(', ')}`);
      }
    }
    if (s.concepts !== undefined) {
      // The shared ideas this skill is about: up to three, each one the catalog itself names.
      if (!Array.isArray(s.concepts) || s.concepts.length > 3 || s.concepts.some(k => typeof k !== 'string') || new Set(s.concepts).size !== s.concepts.length) E(at + '.concepts', `${who}: concepts must be a list of up to 3 different concepts`);
      else for (const k of s.concepts) {
        if (!Object.prototype.hasOwnProperty.call(concepts, k)) E(at + '.concepts', `${who}: "${k}" is not one of this catalog's concepts`);
        else taught.add(k);
      }
    }
    const text = [s.name, s.desc, s.drill, ...(Array.isArray(s.steps) ? s.steps : []), ...(Array.isArray(s.watch) ? s.watch : [])].join(' ');
    if (/[—–]/.test(text)) E(at, `${who}: contains a long dash; use plain punctuation`);
  });
  const live = id => ids.has(id) && !ids.get(id).retired;
  skills.forEach((s, i) => {
    if (!s || !ids.has(s.id) || !Array.isArray(s.needs)) return;
    for (const n of s.needs) {
      const id = String(n).replace(/!$/, ''), at = `skills.${i}.needs`;
      if (!ids.has(id)) E(at, `skill ${s.id}: needs unknown ${n}`);
      else if (id === s.id) E(at, `skill ${s.id}: needs itself`);
      else if (!s.retired && !live(id)) E(at, `skill ${s.id}: needs ${id}, which is retired`);
      else if (ids.get(id).tier > s.tier) W(at, `skill ${s.id} (T${s.tier}) needs ${id} (T${ids.get(id).tier}), a higher tier`);
    }
  });
  const state = {};
  const visit = (id, path) => {
    if (state[id] === 2) return;
    if (state[id] === 1) { E('skills', `cycle: ${[...path, id].join(' > ')}`); return; }
    state[id] = 1;
    for (const n of ids.get(id).needs || []) { const k = String(n).replace(/!$/, ''); if (ids.has(k) && k !== id) visit(k, [...path, id]); }
    state[id] = 2;
  };
  for (const id of ids.keys()) visit(id, []);
  for (const b of bk) if (!skills.some(s => s && s.id && s.id[0] === b && !s.retired)) E('branches', `branch ${b} has no skills`);
  for (const [k, v] of Object.entries(refs)) if (!Array.isArray(v) || !str(v[0], 3, 140) || !/^https:\/\/[^\s]+$/.test(v[1] || '')) E('refs.' + k, `ref ${k}: needs [label, https url]`);
  const used = new Set(skills.flatMap(s => (s && Array.isArray(s.refs)) ? s.refs : []));
  for (const k of Object.keys(refs)) if (!used.has(k)) W('refs.' + k, `ref ${k} is never used`);
  const phases = Array.isArray(c.phases) ? c.phases : [];
  if (phases.length < 3 || phases.length > 6) E('phases', `needs 3 to 6 phases, has ${phases.length}`);
  phases.forEach((p, i) => {
    const at = `phases.${i}`;
    if (!p || p.id !== `p${i + 1}`) E(at + '.id', `phase ${i + 1}: id must be p${i + 1}`);
    if (!p || !str(p.name, 2, 40) || !str(p.time, 2, 40) || !str(p.gate, 10, 260)) E(at, `phase ${p && p.id}: bad name, time or gate`);
    for (const id of String((p && p.skills) || '').split(' ').filter(Boolean)) if (!live(id)) E(at + '.skills', `phase ${p.id}: ${ids.has(id) ? 'retired' : 'unknown'} skill ${id}`);
    if (!p || !Array.isArray(p.req) || !p.req.length) E(at + '.req', `phase ${p && p.id}: needs req`);
    for (const r of (p && Array.isArray(p.req)) ? p.req : []) if (!live(String(r).replace(/!$/, ''))) E(at + '.req', `phase ${p.id}: unknown or retired req ${r}`);
  });
  // Optional: a week-by-week track, and aliases from old skill ids to their current ones.
  if (c.weeks !== undefined) {
    if (!Array.isArray(c.weeks)) E('weeks', 'weeks must be a list');
    else {
      const wk = new Set();
      c.weeks.forEach((w, i) => {
        const at = `weeks.${i}`;
        if (!w || !/^w[0-9]{1,2}$/.test(w.id || '') || wk.has(w.id)) E(at + '.id', `week ${i + 1}: id must be w and a number, unique`);
        if (w) wk.add(w.id);
        if (!w || !Number.isInteger(w.from) || !Number.isInteger(w.to) || w.from < 1 || w.to < w.from || w.to > 52) E(at, `week ${w && w.id}: from and to must be weeks 1 to 52, from first`);
        for (const k of ['label', 'focus', 'result', 'gate']) if (!w || !str(w[k], 2, 160)) E(`${at}.${k}`, `week ${w && w.id}: bad ${k}`);
      });
    }
  }
  if (c.aliases !== undefined) {
    if (!c.aliases || typeof c.aliases !== 'object' || Array.isArray(c.aliases)) E('aliases', 'aliases must be an object of old id to new id');
    else for (const [from, to] of Object.entries(c.aliases)) {
      if (!/^[A-Z][1-9][0-9]?$/.test(from)) E('aliases.' + from, `alias ${from}: bad id`);
      else if (ids.has(from)) E('aliases.' + from, `alias ${from}: that id is a skill, so it cannot also be an alias`);
      if (!live(to)) E('aliases.' + from, `alias ${from}: points at ${to}, which is not a current skill`);
    }
  }
  for (const k of Object.keys(concepts)) if (!taught.has(k)) W(`concepts.${k}`, `concept ${k}: no skill names it`);
  return { errs, warn };
}
if (typeof module !== 'undefined') module.exports = { checkCatalog, MOTION, CONCEPTS };
