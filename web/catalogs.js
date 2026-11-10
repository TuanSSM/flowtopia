'use strict';
// Skill catalogs, one per prop, loaded from catalog/<prop>.json (format: docs/catalog-format.md).
// The page works on one catalog at a time: useCatalog() points the shared names
// (SKILLS, BRANCHES, PHASES, ...) at it.

// How progress on a skill is recorded: one level, or one per hand or per direction.
const TRACKS = { one: [['O', 'Overall']], LR: [['L', 'Left hand'], ['R', 'Right hand']], FR: [['F', 'Forward'], ['R', 'Reverse']] };
const variants = s => TRACKS[s.track] || TRACKS.one;

// The ideas that carry over from one prop to another, as a sentence reads them (the keys are CONCEPTS
// in catalog-check.js). A catalog names the ones it teaches; a skill lists the ones it is about.
const CONCEPT_NAMES = { plane: 'plane', timing: 'timing and direction', weave: 'weave', isolation: 'isolation', extension: 'extension' };

const CATALOGS = {};
const EMPTY = { branches: [], skills: [], refs: {}, phases: [], weeks: [], about: '', careNote: '' };

function addCatalog(c) {
  for (const s of c.skills) { s.b = s.id[0]; s.star = !!s.star; s.care = !!s.care; s.needs ||= []; s.refs ||= []; }
  c.weeks = Array.isArray(c.weeks) ? c.weeks : [];
  c.aliases = c.aliases && typeof c.aliases === 'object' ? c.aliases : {};
  CATALOGS[c.id] = c;
  const p = PROPS.find(x => x.id === c.id);
  if (p) p.ready = true;
}
// A prop whose file is missing or broken simply stays "not built yet".
async function loadCatalogs() {
  await Promise.all(PROPS.filter(p => !CATALOGS[p.id]).map(async p => {
    try {
      const r = await fetch(`catalog/${p.id}.json`, { cache: 'no-cache' });
      if (r.ok) addCatalog(await r.json());
    } catch { /* not built yet */ }
  }));
}

let CID = '';
function useCatalog(id) {
  if (CID === id) return;
  const c = CATALOGS[id] || EMPTY;
  CID = id;
  // Retired skills leave the maps, plans and totals, but stay findable by id so old progress,
  // posts and history still name them. An alias (an old id) finds the skill it became.
  if (!c.live) c.live = c.skills.filter(s => !s.retired);
  BRANCHES = c.branches; SKILLS = c.live; PHASES = c.phases; WEEKS = c.weeks || []; REFS = c.refs;
  if (!c.sk) {
    c.sk = Object.fromEntries(c.skills.map(s => [s.id, s]));
    for (const [from, to] of Object.entries(c.aliases || {})) if (c.sk[to] && !c.sk[from]) Object.defineProperty(c.sk, from, { value: c.sk[to], enumerable: false });
    c.br = Object.fromEntries(c.branches.map(b => [b.k, b]));
    c.dep = {};
    c.live.forEach(s => s.needs.forEach(n => (c.dep[n.replace('!', '')] ||= []).push(s.id)));
  }
  SK = c.sk; BR = c.br; DEPENDENTS = c.dep;
}
// Moves progress recorded under an old id to the id it became: levels keep the higher of the
// two for each hand or direction, notes are joined, and combos, sessions and history follow.
// Returns whether anything changed, so the caller can save the document.
function applyAliases(st, prop) {
  const al = CATALOGS[prop]?.aliases || {};
  let changed = false;
  const to = id => al[id] || id;
  for (const [from, dest] of Object.entries(al)) {
    const old = st.skills[from];
    if (!old) continue;
    const cur = { ...(st.skills[dest] || {}) };
    for (const [k, v] of Object.entries(old)) {
      if (k === 'notes') cur.notes = [cur.notes, v].filter(Boolean).join('\n');
      else if (k === 'updated') cur.updated = [cur.updated, v].filter(Boolean).sort().pop();
      else if (typeof v === 'number') cur[k] = Math.max(Number(cur[k]) || 0, v);
      else if (cur[k] === undefined) cur[k] = v;
    }
    st.skills[dest] = cur; delete st.skills[from]; changed = true;
  }
  for (const c of st.combos || []) {
    const seq = (c.seq || []).map(to);
    if (seq.join() !== (c.seq || []).join()) { c.seq = seq; changed = true; }
    if (c.tr) for (const k of Object.keys(c.tr)) { const nk = k.split('>').map(to).join('>'); if (nk !== k) { c.tr[nk] = c.tr[k]; delete c.tr[k]; changed = true; } }
  }
  for (const s of st.sessions || []) if ((s.focus || []).some(id => al[id])) { s.focus = s.focus.map(to); changed = true; }
  for (const e of st.events || []) if (al[e.id]) { e.id = al[e.id]; changed = true; }
  return changed;
}
