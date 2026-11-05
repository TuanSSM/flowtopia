'use strict';
// Skill catalogs, one per prop. Nunchaku is built from data.js and details.js; the
// others are loaded from catalog/<prop>.json (format: docs/catalog-format.md).
// The page works on one catalog at a time: useCatalog() points the shared names
// (SKILLS, BRANCHES, PHASES, ...) at it.

// How progress on a skill is recorded: one level, or one per hand or per direction.
const TRACKS = { one: [['O', 'Overall']], LR: [['L', 'Left hand'], ['R', 'Right hand']], FR: [['F', 'Forward'], ['R', 'Reverse']] };
const variants = s => TRACKS[s.track] || TRACKS.one;

const CATALOGS = {};
CATALOGS.nunchaku = (() => {
  // Double, flow, movement and geometry skills use both hands or the whole body.
  const sided = s => 'FRSCBAN'.includes(s.b) && s.id !== 'F3';
  return {
    id: 'nunchaku', name: 'Nunchaku',
    about: 'Technique names come from the Freestyle Nunchaku Wiki and the sources in the roadmap. Prerequisites and tiers are coaching proposals, not rules of the art.',
    careNote: 'Learn this on foam. It passes close to the head, neck or face, or leaves the hand.',
    branches: BRANCHES, refs: REFS, phases: PHASES, weeks: WEEKS,
    skills: SKILLS.map(s => {
      const d = DETAILS[s.id] || {};
      return { ...s, track: sided(s) ? 'LR' : 'one', care: !!d.f, steps: d.s || [], watch: d.w || [], drill: d.d || '', refs: (d.r || '').split(' ').filter(Boolean) };
    }),
  };
})();
const EMPTY = { branches: [], skills: [], refs: {}, phases: [], weeks: [], about: '', careNote: '' };

function addCatalog(c) {
  for (const s of c.skills) { s.b = s.id[0]; s.star = !!s.star; s.needs ||= []; s.refs ||= []; }
  c.weeks = [];
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
  BRANCHES = c.branches; SKILLS = c.skills; PHASES = c.phases; WEEKS = c.weeks || []; REFS = c.refs;
  if (!c.sk) {
    c.sk = Object.fromEntries(c.skills.map(s => [s.id, s]));
    c.br = Object.fromEntries(c.branches.map(b => [b.k, b]));
    c.dep = {};
    c.skills.forEach(s => s.needs.forEach(n => (c.dep[n.replace('!', '')] ||= []).push(s.id)));
  }
  SK = c.sk; BR = c.br; DEPENDENTS = c.dep;
}
