'use strict';
// What is planned for Flowa itself, in the repository's own documents.
const DOCS_URL = 'https://github.com/TuanSSM/flowtopia/blob/main/docs/';

/* ---------- helpers ---------- */
const $ = (s, r = document) => r.querySelector(s);
const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const ICONS = {
  home: 'M3 11.5 12 4l9 7.5M5.5 10v9.5h13V10',
  map: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z',
  log: 'M12 7v5l3 2M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z',
  link: 'M9.5 14.5l5-5M8 11l-2.5 2.5a4 4 0 0 0 5.6 5.6L13 17M16 13l2.5-2.5a4 4 0 0 0-5.6-5.6L11 7',
  flag: 'M5 21V4h13l-2.5 4.5L18 13H5',
  gear: 'M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7ZM19 12l2-1.2-2-3.5-2.2.8a7 7 0 0 0-1.6-.9L14.8 5h-4l-.4 2.2a7 7 0 0 0-1.6.9l-2.2-.8-2 3.5L6.6 12l-2 1.2 2 3.5 2.2-.8c.5.4 1 .7 1.6.9l.4 2.2h4l.4-2.2c.6-.2 1.1-.5 1.6-.9l2.2.8 2-3.5Z',
  search: 'M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14ZM20 20l-3.5-3.5',
  menu: 'M4 7h16M4 12h16M4 17h16',
  x: 'M6 6l12 12M18 6 6 18',
  lock: 'M7 11V8a5 5 0 0 1 10 0v3M6 11h12v9H6z',
  check: 'M5 12.5 10 17.5 19 7',
  plus: 'M12 5v14M5 12h14',
  star: 'M12 4l2.4 5 5.6.7-4 3.9 1 5.4-5-2.7-5 2.7 1-5.4-4-3.9 5.6-.7Z',
  ext: 'M14 5h5v5M19 5l-8 8M18 14v5H5V6h5',
  trash: 'M5 7h14M10 7V4h4v3M7 7l1 13h8l1-13',
  circle: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z',
  bridge: 'M4 16c4-7 12-7 16 0M4 12v8M20 12v8',
  minus: 'M5 12h14',
  down: 'M6 9l6 6 6-6',
  right: 'M9 6l6 6-6 6',
  user: 'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8ZM4.5 20c1-4 4-5.5 7.5-5.5s6.5 1.5 7.5 5.5',
  shield: 'M12 3l7 3v5.5c0 4.5-3 8-7 9.5-4-1.5-7-5-7-9.5V6z',
  inbox: 'M4 13h4l2 3h4l2-3h4M4 13l2-8h12l2 8v6H4z',
  play: 'M8 5v14l11-7L8 5Z',
  flame: 'M12 21c-3.5 0-6-2.4-6-5.8 0-2.6 1.6-4.3 2.8-6 .3 1.2.9 2.2 1.9 2.9C10.3 9 10.7 5.8 13 3c.4 3 2.5 4.4 3.8 6.4A7 7 0 0 1 18 15.2C18 18.6 15.5 21 12 21Z',
  trophy: 'M8 4h8v5a4 4 0 0 1-8 0V4ZM8 6H5v1a3 3 0 0 0 3 3M16 6h3v1a3 3 0 0 1-3 3M12 13v4M9 20h6M10 17h4',
  bookmark: 'M7 4h10v16l-5-3.5L7 20V4Z',
  medal: 'M12 15a4.5 4.5 0 1 0 0-9 4.5 4.5 0 0 0 0 9ZM9 14l-1.5 6 4.5-2 4.5 2L15 14',
  bolt: 'M13 3 5 14h6l-1 7 8-11h-6l1-7Z',
  heart: 'M12 20s-7-4.5-7-10a4 4 0 0 1 7-2.6A4 4 0 0 1 19 10c0 5.5-7 10-7 10Z',
  bell: 'M6 16v-5a6 6 0 0 1 12 0v5l1.5 2h-15L6 16ZM10 20a2 2 0 0 0 4 0',
  send: 'M20 4 4 11l8 2 2 8 6-17ZM12 13l8-9',
  comment: 'M4 6h16v10H9l-5 4V6Z',
  share: 'M12 4v11M8 8l4-4 4 4M5 14v6h14v-6',
  download: 'M12 4v11M8 11l4 4 4-4M5 20h14',
  image: 'M4 5h16v14H4zM4 16l5-5 4 4 3-3 4 4M15.5 9.5h.01',
  calendar: 'M4 6h16v14H4zM4 10h16M8 3v4M16 3v4',
  hands: 'M7 20v-6l-3-4 2-1 3 3V5h2v7M17 20v-6l3-4-2-1-3 3V5h-2v7',
  note: 'M6 4h9l4 4v12H6zM14 4v5h5M9 13h6M9 16h6',
  users: 'M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7ZM3 20c.8-3.5 3.2-5 6-5s5.2 1.5 6 5M16 11a3 3 0 1 0 0-6M21 20c-.5-2.5-1.8-4-3.5-4.6',
  edit: 'M4 20h4L18 10l-4-4L4 16v4ZM12 8l4 4',
  feed: 'M4 5h16M4 10h16M4 15h10M4 20h7',
  instagram: 'M7 3h10a4 4 0 0 1 4 4v10a4 4 0 0 1-4 4H7a4 4 0 0 1-4-4V7a4 4 0 0 1 4-4ZM12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7ZM17.3 6.7h.01',
  tiktok: 'M14 3v11.5a3.5 3.5 0 1 1-3.5-3.5M14 3c.5 3 2.5 4.5 5 4.5',
  youtube: 'M3 8.5c0-1.5 1-2.5 2.5-2.7C7.5 5.6 10 5.5 12 5.5s4.5.1 6.5.3C20 6 21 7 21 8.5v7c0 1.5-1 2.5-2.5 2.7-2 .2-4.5.3-6.5.3s-4.5-.1-6.5-.3C4 18 3 17 3 15.5v-7ZM10 9v6l5-3-5-3Z',
};
const icon = n => `<svg class="i" viewBox="0 0 24 24" aria-hidden="true"><path d="${ICONS[n]}"/></svg>`;
// Lookups for the catalog in use; useCatalog() in catalogs.js fills them.
let SK = {}, BR = {}, DEPENDENTS = {};
const pad = n => String(n).padStart(2, '0');
const dayKey = (d = new Date()) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const parseDay = k => { const [y, m, d] = k.split('-').map(Number); return new Date(y, m - 1, d); };
const fmtDay = k => parseDay(k).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
const fmtMin = m => m >= 60 ? `${Math.floor(m / 60)} h ${m % 60 ? `${m % 60} min` : ''}`.trim() : `${m} min`;
const uid = () => Date.now().toString(36) + Math.random().toString(36).slice(2, 6);

/* ---------- state and sync ---------- */
const blank = () => ({ version: 1, skills: {}, sessions: [], combos: [], gates: {}, events: [], settings: { dominant: 'R', trackStart: '', event: '', rulesChecked: '' } });
let flags = {};
const FLAGS = [
  ['details', 'Skill guides', 'Steps, what to watch for and a drill on each skill.'],
  ['demos', 'Animated demos', 'Demos with three camera views, speed control and GIF download, on the skills whose motion is fully defined. Some are simulated, some are drawn from the definition.'],
  ['references', 'Reference links', 'Source pages and videos for each skill.'],
  ['model3d', '3D figure', 'Show demos on a shaded 3D human model. When off, or where the browser has no WebGL, the drawn figure is used.'],
  ['tour', 'Guided tour', 'A step-by-step tour for new users, on first sign-in and the first visit to each section. When off, a short note is shown instead.'],
  ['signups', 'Open sign-ups', 'Let anyone who can open this page create an account. Turn it off once everyone has one.'],
  ['community', 'Community', 'The timeline with posts, likes and comments, public profiles, the people list and the notification bell.'],
  ['bookmarks', 'Bookmarks', 'Members keep skills, combos and posts to come back to, on a Bookmarks page.'],
  ['linkCards', 'Link cards', 'Instagram, TikTok and YouTube links in posts become cards with a preview the server fetches, so visitors never contact those sites until they open a link.'],
  ['videoEmbeds', 'Play YouTube in place', 'A play button on YouTube cards that loads the video from youtube-nocookie.com inside Flowa. Adds that one site to the page security policy while on.'],
  ['suggest', 'Next-trick suggestions', 'The combo builder suggests what can follow the last trick, from how the moves travel, with a bridge when nothing links directly. Members can share their transition marks anonymously to improve it.'],
  ['avatars', 'Avatars', 'Members build an avatar that plays their demos and combos (on their body preset where the clip passes its body checks), shows on their profile, and appears on stickers and streak overlays. Needs the 3D figure.'],
  ['moderation', 'Reports and moderation', 'Members can report posts, comments and profiles, and moderators get the Moderation page with the queue, mutes and suspensions. When off, the Report buttons and that page are hidden; suspensions already given still hold.'],
  ['catalogEditor', 'Skill map editor', 'Admins edit the skill catalogs in the browser and publish them for everyone. When off, the editor is hidden and nothing can be changed; what was last published stays in use.'],
  ['game', 'Gamification', 'XP and levels, the practice streak with its shareable overlay, weekly challenges, achievements and XP on the phases of the Plan.'],
];
let state = blank(), rev = 0, dirty = false, saving = false, saveTimer = null, syncState = 'ok', loaded = false;
const ui = { graphView: localStorage.getItem('nf-view') || 'graph', draftFocus: [], palIdx: 0, prop: localStorage.getItem('fl-prop') || 'nunchaku', open: (localStorage.getItem('fl-groups') ?? localStorage.getItem('fl-prop') ?? 'nunchaku').split(',').filter(Boolean), users: null };

function normalize(s) {
  const b = blank();
  const o = { ...b, ...(s && typeof s === 'object' ? s : {}) };
  o.settings = { ...b.settings, ...(o.settings || {}) };
  for (const k of ['sessions', 'combos', 'events']) if (!Array.isArray(o[k])) o[k] = [];
  for (const k of ['skills', 'gates']) if (!o[k] || typeof o[k] !== 'object') o[k] = {};
  return o;
}
function setSync(v) {
  syncState = v;
  drawSync();
}
// The note under the menu. It is a live region that stays on the page, so only its words are changed, and
// only when they differ: a screen reader then says each change once.
function drawSync() {
  const el = $('#sync'), words = { ok: 'All changes saved', saving: 'Saving…', err: 'Not saved, retrying' }[syncState];
  el.hidden = !me; el.className = 'sync ' + (syncState === 'ok' ? '' : syncState);
  if (el.lastElementChild.textContent !== (me ? words : '')) el.lastElementChild.textContent = me ? words : '';
}
function commit() {
  if (!me) { render(); return; }
  dirty = true; setSync('saving');
  clearTimeout(saveTimer); saveTimer = setTimeout(save, 350);
  render();
}
// How many saves in a row met a newer copy from another device; a successful save clears it.
let clashes = 0;
async function save() {
  if (!me) { dirty = false; return; }
  if (saving) { saveTimer = setTimeout(save, 200); return; }
  saving = true; dirty = false;
  const prop = ui.prop, user = me.id, sent = JSON.stringify(state);
  try {
    const r = await fetch(`api/progress/${prop}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: `{"rev":${Number(rev) || 0},"state":${sent}}` });
    if (r.status === 401) {
      me = null; state = blank(); rev = 0; sameOwner();
      toast('You were signed out, so that change was not saved. Sign in again.');
      location.hash = '#/signin'; render();
      return;
    }
    const d = r.status === 409 || r.ok ? await r.json() : null;
    if (!d) throw new Error(r.status);
    if (me?.id !== user) return; // signed out, or someone else signed in, while this was on its way
    // The person may have switched props meanwhile; then the answer belongs to the copy put aside.
    const here = ui.prop === prop;
    if (r.status === 409) {
      // Another device saved first. Both sets of changes since the copy the two started from are
      // kept (merge.js) and saved again. Without that starting copy, or when it keeps happening,
      // the latest copy is loaded and the person is told.
      const theirs = normalize(d.state), mine = here ? state : docs[prop]?.state, base = synced[prop];
      synced[prop] = JSON.stringify(theirs);
      const both = base && mine && clashes < 3 ? normalize(progressMerge.progress(JSON.parse(base), mine, theirs)) : null;
      clashes++;
      const again = !!both && !progressMerge.same(both, theirs);
      if (here) {
        state = both || theirs; rev = d.rev; docs[prop] = { rev, state };
        if (again) { dirty = true; clearTimeout(saveTimer); saveTimer = setTimeout(save, 50); } else dirty = false;
      } else docs[prop] = { rev: d.rev, state: both || theirs, migrated: again }; // saved when the prop is opened again
      toast(both ? 'Changed on another device too. Both sets of changes were kept.' : 'Changed on another device. Loaded the latest copy, so your last edit was not kept.');
      render();
    } else {
      synced[prop] = sent; clashes = 0;
      if (here) { rev = d.rev; docs[prop] = { rev, state }; } else if (docs[prop]) docs[prop].rev = d.rev;
    }
    setSync(dirty ? 'saving' : 'ok');
    if (!dirty) { if (flags.game) syncGame(); ntSyncAll(false); }
  } catch {
    dirty = true; setSync('err');
    clearTimeout(saveTimer); saveTimer = setTimeout(save, 5000);
  } finally { saving = false; }
}
async function setFlag(k, v) {
  try {
    const r = await fetch('api/flags', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ flags: { [k]: v } }) });
    if (!r.ok) throw new Error(r.status);
    flags = (await r.json()).flags;
  } catch { toast('Could not change that feature. Try again.'); }
  render();
}
// Loads the session, the flags and the signed-in user's progress. Returns whether anything changed.
async function load() {
  const mark = () => JSON.stringify([me, flags, rev, ui.prop]);
  const before = mark();
  await loadSession();
  loaded = true;
  if (!me || BM.user !== me.id) resetBookmarks();
  sameOwner();
  if (me && flags.bookmarks && !BM.loaded) { BM.user = me.id; loadBookmarks(); }
  if (flags.game) syncGame();
  ntSyncAll(false); // marks made before consent changed elsewhere, or while offline
  if (flags.community) loadNotifs();
  return mark() !== before;
}
document.addEventListener('visibilitychange', async () => {
  if (document.visibilityState !== 'visible' || dirty || saving || !loaded) return;
  try { if (await load()) render(); } catch { /* the next save reports trouble */ }
});
// Leaving with something unsaved or unsent asks first: progress on its way, a post or comment
// being written, or an edit to the About form.
window.addEventListener('beforeunload', e => { if (dirty || socialDraft() || aboutDirty()) e.preventDefault(); });

/* ---------- skill model ---------- */
function rec(id) { return state.skills[id] || {}; }
// A skill has one level per variant: overall, each hand, or each direction.
function lv(id, key) { return Number(rec(id)[key]) || 0; }
const levels = id => variants(SK[id]).map(([k]) => lv(id, k));
function best(id) { return Math.max(...levels(id)); }
function worst(id) { return Math.min(...levels(id)); }
function score(id) { const l = levels(id); return l.reduce((a, b) => a + b, 0) / (4 * l.length); }
const variantLabel = (id, key) => { const v = SK[id] && variants(SK[id]).find(([k]) => k === key); return v && key !== 'O' ? v[1].split(' ')[0] + ': ' : ''; };
function needMet(need) {
  const both = need.endsWith('!'), id = need.replace('!', '');
  return (both ? worst(id) : best(id)) >= 2;
}
const unlocked = id => SK[id].needs.every(needMet);
// What a member already knows of a skill's shared concepts from their other props. For each concept
// the skill lists and its map names: the other props of theirs whose map names it too and where a
// skill that lists it is Clean or better, with the best such skill. A hint only: it unlocks nothing.
function knownFrom(id) {
  const here = CATALOGS[CID].concepts || {}, s = SK[id], out = [];
  if (!me || !Array.isArray(s.concepts)) return out;
  for (const c of s.concepts) {
    if (typeof here[c] !== 'string' || !CONCEPT_NAMES[c]) continue;
    const from = [];
    for (const p of myProps()) {
      const there = (CATALOGS[p.id] || {}).concepts || {};
      if (p.id === CID || typeof there[c] !== 'string') continue;
      const via = inProp(p.id, () => SKILLS.filter(x => Array.isArray(x.concepts) && x.concepts.includes(c)).map(x => ({ id: x.id, name: x.name, lv: best(x.id) })).filter(x => x.lv >= 2).sort((a, b) => b.lv - a.lv)[0]);
      if (via) from.push({ prop: p, via });
    }
    if (from.length) out.push({ c, text: here[c], from });
  }
  return out;
}
const knownHTML = id => knownFrom(id).map(k => `<p class="notice known-from" style="margin-top:10px" data-concept="${k.c}">You know the ${esc(CONCEPT_NAMES[k.c])} from ${k.from.map(f => `${esc(f.prop.name.toLowerCase())} (<a href="#/prop/${f.prop.id}/${f.via.id[0]}/${f.via.id}">${esc(f.via.name)}</a>, ${LEVELS[f.via.lv].n.toLowerCase()})`).join(' and ')}. Here: ${esc(k.text)}</p>`).join('');
function status(id) {
  if (!me) return 'open'; // guests browse the map without progress
  const b = best(id);
  if (b > 0) return `lv${b}`;
  return unlocked(id) ? 'ready' : 'locked';
}
const branchSkills = k => SKILLS.filter(s => s.b === k);
const pctOf = list => list.length ? Math.round(100 * list.reduce((a, s) => a + score(s.id), 0) / list.length) : 0;
const cleanCount = list => list.filter(s => best(s.id) >= 2).length;

function setLevel(id, side, v) {
  const s = SK[id], key = side;
  if (!variants(s).some(([k]) => k === key)) return;
  const r = { ...rec(id) }, from = Number(r[key]) || 0;
  if (from === v) return;
  r[key] = v; r.updated = new Date().toISOString();
  state.skills[id] = r;
  state.events.unshift({ t: r.updated, id, side: key === 'O' ? '' : key, from, to: v });
  state.events.length = Math.min(state.events.length, 300);
  commit();
}

function minutesByDay() {
  const m = {};
  for (const s of state.sessions) m[s.date] = (m[s.date] || 0) + (Number(s.minutes) || 0);
  return m;
}
function streak(m) {
  let d = new Date(), n = 0;
  if (!m[dayKey(d)]) d.setDate(d.getDate() - 1);
  while (m[dayKey(d)]) { n++; d.setDate(d.getDate() - 1); }
  return n;
}
function weekMinutes(m) {
  const d = new Date(); let t = 0;
  for (let i = 0; i < 7; i++) { t += m[dayKey(d)] || 0; d.setDate(d.getDate() - 1); }
  return t;
}
function currentPhase() { return PHASES.find(p => !state.gates[p.id]) || null; }
function nextUp(limit = 6) {
  const phase = currentPhase(), inPhase = new Set(phase ? phase.skills.split(' ') : []);
  return SKILLS.filter(s => status(s.id) === 'ready')
    .map(s => ({ s, rank: (inPhase.has(s.id) ? 0 : 100) + s.tier * 10 - Math.min((DEPENDENTS[s.id] || []).length, 9) }))
    .sort((a, b) => a.rank - b.rank).slice(0, limit).map(x => x.s);
}
function inProgress(limit = 6) {
  return SKILLS.filter(s => { const b = best(s.id); return b === 1; }).slice(0, limit);
}
function weakSide(limit = 5) {
  const dom = state.settings.dominant, weak = dom === 'L' ? 'R' : 'L';
  return { weak, list: SKILLS.filter(s => s.track === 'LR' && lv(s.id, dom) >= 2 && lv(s.id, weak) < 2).sort((a, b) => a.tier - b.tier).slice(0, limit) };
}

/* ---------- small views ---------- */
function pips(l, label) {
  let h = '<span class="pips">' + (label ? `<span class="pips-lbl">${label}</span>` : '');
  for (let i = 1; i <= 4; i++) h += `<i class="${i <= l ? 'on' + l : ''}"></i>`;
  return h + '</span>';
}
function skillPips(s) {
  return variants(s).map(([k]) => pips(lv(s.id, k), k === 'O' ? '' : k)).join('');
}
const skillHref = id => `#/prop/${CID}/${SK[id].b}/${id}`;
function skillRow(s, extra = '') {
  return `<a class="sk-row" href="${skillHref(s.id)}"><span class="tag">${s.id}</span><span class="grow">${esc(s.name)}</span>${extra}</a>`;
}
function skillOptions() {
  return BRANCHES.map(b => `<optgroup label="${esc(b.name)}">${branchSkills(b.k).map(s => `<option value="${s.id}">${s.id} ${esc(s.name)}</option>`).join('')}</optgroup>`).join('');
}

function heatmap(m, weeks = 39) {
  const size = 12, gap = 3, left = 26, topY = 18;
  const today = new Date();
  const start = new Date(today); start.setDate(start.getDate() - today.getDay() - (weeks - 1) * 7);
  let cells = '', months = '', last = -1;
  const d = new Date(start);
  for (let w = 0; w < weeks; w++) {
    for (let i = 0; i < 7; i++) {
      if (d <= today) {
        const k = dayKey(d), v = m[k] || 0;
        const bin = v === 0 ? 0 : v < 15 ? 1 : v < 30 ? 2 : v < 60 ? 3 : 4;
        const tip = `${fmtDay(k)}: ${v ? fmtMin(v) : 'no practice'}`;
        cells += `<rect x="${left + w * (size + gap)}" y="${topY + i * (size + gap)}" width="${size}" height="${size}" class="h${bin}${k === dayKey() ? ' today' : ''}" data-tip="${esc(tip)}"><title>${esc(tip)}</title></rect>`;
        if (i === 0 && d.getMonth() !== last) {
          if (w < weeks - 1) months += `<text x="${left + w * (size + gap)}" y="10">${d.toLocaleDateString(undefined, { month: 'short' })}</text>`;
          last = d.getMonth();
        }
      }
      d.setDate(d.getDate() + 1);
    }
  }
  const W = left + weeks * (size + gap), H = topY + 7 * (size + gap);
  return `<div class="heat-wrap"><svg class="heat" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" role="img" aria-label="Practice minutes per day, last ${weeks} weeks. The Practice page has the same data as a table.">${months}<text x="0" y="${topY + 1 * (size + gap) + 10}">Mon</text><text x="0" y="${topY + 3 * (size + gap) + 10}">Wed</text><text x="0" y="${topY + 5 * (size + gap) + 10}">Fri</text>${cells}</svg></div>
  <div class="legend" style="margin-top:10px">0<s style="background:var(--l0);margin-left:3px"></s><s style="background:var(--l1)"></s><s style="background:var(--l2)"></s><s style="background:var(--l3)"></s><s style="background:var(--l4);margin-right:3px"></s>60+ min</div>`;
}

/* ---------- pages ---------- */
function pageDashboard() {
  if (!curProp().ready) return pageDashLite();
  const m = minutesByDay(), phase = currentPhase(), total = Object.values(m).reduce((a, b) => a + b, 0);
  const clean = cleanCount(SKILLS), wk = weekMinutes(m), st = streak(m), gates = PHASES.filter(p => state.gates[p.id]).length;
  const up = nextUp(), cont = inProgress(), weak = weakSide();
  const bars = BRANCHES.map(b => {
    const l = branchSkills(b.k), p = pctOf(l);
    return `<a class="br-row" href="#/prop/${CID}/${b.k}"><span class="letter">${b.k}</span><span>${esc(b.name)}</span><span class="bar"><i style="width:${p}%"></i></span><span class="num small muted" style="text-align:right">${p}%</span><span class="num tiny" style="text-align:right">${cleanCount(l)}/${l.length}</span></a>`;
  }).join('');
  const evs = state.events.slice(0, 6).map(e => SK[e.id] ? `<a class="sk-row" href="${skillHref(e.id)}"><span class="tag">${e.id}</span><span class="grow small">${variantLabel(e.id, e.side)}${LEVELS[e.to]?.n || ''}</span><span class="tiny">${esc(new Date(e.t).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }))}</span></a>` : '').join('');
  return `
  <div class="page-h"><div class="grow"><h1>Dashboard</h1><p>${phase ? `Phase ${PHASES.indexOf(phase) + 1} of ${PHASES.length}: ${esc(phase.name)}` : 'All gates passed'}</p></div>
    <a class="btn primary" href="#/practice">${icon('plus')}Log practice</a></div>
  <div class="grid g4" data-tour="stats" style="margin-bottom:16px">
    <div class="card stat"><div class="lbl">Skills clean</div><div class="val">${clean}<small> / ${SKILLS.length}</small></div><div class="foot">Unlocks what depends on them</div></div>
    <div class="card stat"><div class="lbl">Last 7 days</div><div class="val">${wk}<small> min</small></div><div class="foot">${fmtMin(total)} in total</div></div>
    <div class="card stat"><div class="lbl">${esc(curProp().name)} streak</div><div class="val">${st}<small> ${st === 1 ? 'day' : 'days'}</small></div><div class="foot">${state.sessions.length} ${state.sessions.length === 1 ? 'session' : 'sessions'} logged</div></div>
    <div class="card stat"><div class="lbl">Gates passed</div><div class="val">${gates}<small> / ${PHASES.length}</small></div><div class="foot">${phase ? 'Next: ' + esc(phase.name) : 'Plan complete'}</div></div>
  </div>
  ${flags.game ? `<div class="grid g2" style="margin-bottom:16px">${levelCard(true)}${streakCard()}</div>` : ''}
  <div class="grid g32" style="margin-bottom:16px">
    <div class="stack">
      <div class="card"><div class="card-h"><h2 class="grow">Practice, last 9 months</h2><a class="small" href="#/practice">View log</a></div>${heatmap(m)}</div>
      <div class="card"><div class="card-h"><h2 class="grow">Branch progress</h2><span class="sub">Clean skills at right</span></div>${bars}</div>
    </div>
    <div class="stack" data-tour="next">
      ${phase ? `<div class="card"><div class="card-h"><h2 class="grow">Next gate</h2><a class="small" href="#/plan">Plan</a></div><p class="small">${esc(phase.gate)}</p><div class="chips" style="margin-top:10px">${phase.req.map(reqChip).join('')}</div></div>` : ''}
      ${cont.length ? `<div class="card"><div class="card-h"><h2>In progress</h2></div><div class="sk-list">${cont.map(s => skillRow(s, `<span class="row" style="gap:6px">${skillPips(s)}</span>`)).join('')}</div></div>` : ''}
      <div class="card"><div class="card-h"><h2 class="grow">Ready to learn</h2><span class="sub">Current phase first</span></div>
        ${up.length ? `<div class="sk-list">${up.map(s => skillRow(s, `<span class="tiny">T${s.tier}</span>`)).join('')}</div>` : '<div class="empty small">Nothing new is unlocked. Bring a skill to Clean first.</div>'}</div>
      ${weak.list.length ? `<div class="card"><div class="card-h"><h2 class="grow">Weak-side work</h2><span class="sub">${weak.weak === 'L' ? 'Left' : 'Right'} hand behind</span></div><div class="sk-list">${weak.list.map(s => skillRow(s, `<span class="row" style="gap:6px">${skillPips(s)}</span>`)).join('')}</div></div>` : ''}
      ${evs ? `<div class="card"><div class="card-h"><h2>Recent changes</h2></div><div class="sk-list">${evs}</div></div>` : ''}
    </div>
  </div>`;
}
function reqChip(need) {
  const id = need.replace('!', ''), ok = needMet(need);
  return `<a class="chip ${ok ? 'ok' : ''}" href="${skillHref(id)}" data-tip="${esc(SK[id].name)}${need.endsWith('!') ? ', both hands' : ''}">${icon(ok ? 'check' : 'circle')}${id}${need.endsWith('!') ? ' both' : ''}</a>`;
}

function pageMap() {
  let h = `<div class="map" style="grid-template-columns:42px repeat(${BRANCHES.length}, minmax(0, 1fr));min-width:${BRANCHES.length * 84 + 56}px"><div></div>` + BRANCHES.map(b => `<a class="mh" href="#/prop/${CID}/${b.k}" style="color:var(--ink-2)">${esc(b.name)}</a>`).join('');
  for (let t = 1; t <= 5; t++) {
    h += `<div class="ml">T${t}</div>`;
    for (const b of BRANCHES) {
      const l = branchSkills(b.k).filter(s => s.tier === t);
      h += `<div class="mc">${l.map(s => `<a class="cell ${status(s.id)}" href="${skillHref(s.id)}" data-tip="${esc(s.name)}: ${esc(statusLabel(s.id))}">${s.id}</a>`).join('')}</div>`;
    }
  }
  return `<div class="card-h" style="margin:24px 0 10px"><h2 class="grow">${esc(curProp().name)} map</h2><span class="sub">${SKILLS.length} skills by branch and tier${me ? '. Stronger blue means further along, using the better hand.' : ''}</span></div>
  <div class="card map-wrap">${h}</div>${me ? graphKey() : ''}
  <p class="tiny" style="margin-top:12px;max-width:820px">${esc(CATALOGS[CID].about)}${SKILLS.some(s => s.star) ? ` ${SKILLS.filter(s => s.star).length} of ${SKILLS.length} skills are marked unconfirmed, because no source I could open confirms the name or how it is done.` : ''}${undescribed() ? ` ${undescribed()} more are named by the sources but not described, so their guides say to find a demonstration first.` : ''}</p>`;
}
// Skills whose guide opens by saying the sources name the move without describing it.
const undescribed = () => SKILLS.filter(s => !s.star && /sources I could read name this move/.test(s.steps[0] || '')).length;
function statusLabel(id) {
  const st = status(id);
  return st === 'open' ? 'Sign in to track' : st === 'locked' ? 'Locked' : st === 'ready' ? 'Ready to learn' : LEVELS[best(id)].n;
}
function graphKey() {
  return `<div class="key"><span><u class="k-locked"></u>Locked</span><span><u class="k-ready"></u>Ready</span>
  ${[1, 2, 3, 4].map(i => `<span><s style="background:var(--l${i})"></s>${LEVELS[i].n}</span>`).join('')}</div>`;
}

function layout(list) {
  const ids = new Set(list.map(s => s.id));
  const parents = id => SK[id].needs.map(n => n.replace('!', '')).filter(n => ids.has(n));
  const depth = {};
  const dep = id => depth[id] ??= (parents(id).length ? 1 + Math.max(...parents(id).map(dep)) : 0);
  list.forEach(s => dep(s.id));
  const rows = [];
  list.forEach(s => (rows[depth[s.id]] ||= []).push(s.id));
  const pos = {};
  rows.forEach(r => r.forEach((id, i) => pos[id] = i));
  // Barycentre passes keep parents and children close, which reduces crossings.
  for (let pass = 0; pass < 6; pass++) {
    rows.forEach((r, d) => {
      if (!d) return;
      const bc = id => { const p = parents(id); return p.length ? p.reduce((a, x) => a + pos[x] - (rows[depth[x]].length - 1) / 2, 0) / p.length : 0; };
      r.sort((a, b) => bc(a) - bc(b));
      r.forEach((id, i) => pos[id] = i);
    });
  }
  const w = 164, h = 94, gx = 14, gy = 42;
  const width = Math.max(...rows.map(r => r.length)) * (w + gx) - gx;
  const xy = {};
  rows.forEach((r, d) => {
    const rw = r.length * (w + gx) - gx, x0 = (width - rw) / 2;
    r.forEach((id, i) => xy[id] = { x: x0 + i * (w + gx), y: d * (h + gy) });
  });
  return { xy, w, h, width, height: rows.length * (h + gy) - gy, parents };
}
function pageBranch(k, sel) {
  const b = BR[k];
  if (!b) return notFound();
  const list = branchSkills(k), p = pctOf(list);
  const view = ui.graphView;
  let body;
  if (view === 'list') {
    body = `<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>ID</th><th>Skill</th><th>Tier</th><th>Needs</th><th>Status</th><th>Progress</th></tr></thead><tbody>
    ${list.map(s => `<tr><td class="num"><b>${s.id}</b></td><td><a href="${skillHref(s.id)}">${esc(s.name)}</a></td><td>T${s.tier}</td><td><div class="chips">${s.needs.map(reqChip).join('') || '<span class="tiny">None</span>'}</div></td><td class="small">${statusLabel(s.id)}</td><td><span class="row" style="gap:6px">${skillPips(s)}</span></td></tr>`).join('')}</tbody></table></div></div>`;
  } else {
    const L = layout(list);
    const edges = list.flatMap(s => L.parents(s.id).map(pr => {
      const a = L.xy[pr], c = L.xy[s.id];
      const x1 = a.x + L.w / 2, y1 = a.y + L.h, x2 = c.x + L.w / 2, y2 = c.y, my = (y1 + y2) / 2;
      const cls = [best(pr) >= 2 ? 'met' : '', sel && (sel === pr || sel === s.id) ? 'hl' : ''].join(' ').trim();
      return `<path class="${cls}" d="M${x1} ${y1}C${x1} ${my} ${x2} ${my} ${x2} ${y2}"/>`;
    })).join('');
    const nodes = list.map(s => {
      const st = status(s.id), { x, y } = L.xy[s.id];
      const ext = s.needs.map(n => n.replace('!', '')).filter(n => SK[n].b !== k);
      return `<a class="node ${st === 'locked' || st === 'ready' ? st : ''} ${sel === s.id ? 'sel' : ''}" style="left:${x}px;top:${y}px" href="${skillHref(s.id)}" aria-label="${s.id} ${esc(s.name)}, ${statusLabel(s.id)}">
        <span class="nh">${s.id} · T${s.tier}${ext.length ? `<span style="font-weight:500;overflow:hidden;text-overflow:ellipsis">needs ${ext.join(', ')}</span>` : ''}<span class="stateic">${bmHas('skill', CID + ':' + s.id) ? `<span class="bm-mark" data-tip="Bookmarked">${icon('bookmark')}</span>` : ''}${st === 'locked' ? icon('lock') : ''}</span></span>
        <span class="nm">${esc(s.name)}</span><span class="nf">${skillPips(s)}</span></a>`;
    }).join('');
    body = `<div class="graph-wrap"><div class="graph ${sel ? 'focus' : ''}" style="width:${L.width}px;height:${L.height}px"><svg width="${L.width}" height="${L.height}" aria-hidden="true">${edges}</svg>${nodes}</div></div>${graphKey()}`;
  }
  return `<div class="page-h"><div class="grow"><h1>${esc(b.name)}</h1><p>${esc(b.blurb)} · ${me ? `${cleanCount(list)} of ${list.length} clean` : `${list.length} skills`}</p></div>
    <div class="seg" role="group" aria-label="View"><button class="${view === 'graph' ? 'on' : ''}" data-act="view" data-v="graph">Graph</button><button class="${view === 'list' ? 'on' : ''}" data-act="view" data-v="list">List</button></div></div>
  <div class="row" style="margin-bottom:16px"><div class="bar grow"><i style="width:${p}%"></i></div><span class="num small muted">${p}%</span></div>${body.replace(/<div class="key">[\s\S]*?<\/div>$/, m => me ? m : '')}`;
}

function renderPanel(id) {
  const el = $('#panel'), s = SK[id];
  if (!s) { el.hidden = true; el.innerHTML = ''; return; }
  const open = !el.hidden && el.dataset.id === id, top = el.scrollTop;
  el.hidden = false; el.dataset.id = id;
  const lvCtl = (side, title) => {
    const v = lv(id, side);
    return `<div class="side-row"><div class="row small"><b class="grow" style="font-weight:560">${title}</b><span class="tiny">${LEVELS[v].n}</span></div>
    <div class="lv" role="group" aria-label="${title} level">${LEVELS.map((L, i) => `<button class="${i === v ? 'on' : ''}" data-act="level" data-id="${id}" data-side="${side}" data-v="${i}" data-tip="${esc(L.n)}: ${esc(L.d)}" aria-pressed="${i === v}" aria-label="${esc(L.n)}">${i}</button>`).join('')}</div></div>`;
  };
  const deps = (DEPENDENTS[id] || []).map(d => `<a class="chip" href="${skillHref(d)}" data-tip="${esc(SK[d].name)}">${d}</a>`).join('');
  const evs = state.events.filter(e => e.id === id).slice(0, 5);
  const locked = !unlocked(id);
  const refs = s.refs.filter(k => REFS[k]).map(k => REFS[k]);
  refs.push([`Search YouTube for "${s.name}"`, 'https://www.youtube.com/results?search_query=' + encodeURIComponent(`${curProp().name} ${s.name} tutorial`)]);
  el.innerHTML = `<div class="row"><span class="tiny grow">${esc(BR[s.b].name)} · ${s.id} · Tier ${s.tier}</span>${bmButton('skill', CID + ':' + id)}<a class="icon-btn" href="#/prop/${CID}/${s.b}" aria-label="Close details">${icon('x')}</a></div>
  <h2 style="margin-top:4px">${esc(s.name)}</h2><p class="muted small" style="margin-top:6px">${esc(s.desc)}</p>
  ${s.star ? `<p class="notice warn" style="margin-top:10px">Unconfirmed: no source I could open confirms this name or how it is done. Check it against a video or instructor.</p>` : ''}
  ${locked && me ? `<p class="notice" style="margin-top:10px">Prerequisites are not Clean yet. They are a suggested order, so you can still log progress.</p>` : ''}
  ${knownHTML(id)}
  ${s.care && flags.details ? `<p class="notice warn" style="margin-top:10px">${esc(CATALOGS[CID].careNote)}</p>` : ''}
  ${flags.demos && Demo.has(CID, id) ? `<section><div class="sec-t">Demo</div>${Demo.html(CID, id, state.settings.dominant, esc)}</section>` : ''}
  ${flags.details && s.steps.length ? `<section><div class="sec-t">How to do it</div><ol class="steps">${s.steps.map(x => `<li>${esc(x)}</li>`).join('')}</ol></section>
  <section><div class="sec-t">Watch for</div><ul class="steps">${s.watch.map(x => `<li>${esc(x)}</li>`).join('')}</ul></section>
  <section><div class="sec-t">Drill</div><p class="small">${esc(s.drill)}</p><p class="tiny" style="margin-top:6px">Guides are written from each move's definition, not from filmed lessons.</p></section>` : ''}
  ${me ? `<section><div class="sec-t">Progress</div>${variants(s).map(([k, n]) => lvCtl(k, n)).join('')}
    <p class="tiny" style="margin-top:9px">0 not started · 1 learning · 2 clean (20 slow reps in a row) · 3 reliable · 4 performance</p></section>` : `<section><div class="sec-t">Progress</div><p class="small muted"><a href="#/signin">Sign in</a> to track your level on this skill and keep notes.</p></section>`}
  <section><div class="sec-t">Needs</div><div class="chips">${s.needs.map(reqChip).join('') || '<span class="small muted">Nothing. Start any time.</span>'}</div></section>
  <section><div class="sec-t">Unlocks</div><div class="chips">${deps || '<span class="small muted">Nothing directly.</span>'}</div></section>
  ${flags.references ? `<section><div class="sec-t">References</div><ul class="refs">${refs.map(([n, u]) => `<li><a href="${esc(u)}" target="_blank" rel="noopener noreferrer">${esc(n)}</a></li>`).join('')}</ul></section>` : ''}
  ${me ? `<section><div class="sec-t">Notes</div><textarea data-change="note" data-id="${id}" aria-label="Notes" placeholder="Cues, drills, what went wrong">${esc(rec(id).notes || '')}</textarea></section>` : ''}
  ${evs.length ? `<section><div class="sec-t">History</div>${evs.map(e => `<div class="row small" style="padding:2px 0"><span class="grow">${variantLabel(e.id, e.side)}${LEVELS[e.to]?.n || ''}</span><span class="tiny">${esc(new Date(e.t).toLocaleDateString())}</span></div>`).join('')}</section>` : ''}`;
  if (open) el.scrollTop = top;
  Demo.mount();
}

function pagePractice() {
  const sessions = [...state.sessions].sort((a, b) => b.date.localeCompare(a.date) || String(b.id).localeCompare(String(a.id)));
  const m = minutesByDay();
  const focus = ui.draftFocus.map(id => `<button type="button" class="chip" data-act="focus-del" data-id="${id}" data-tip="Remove ${esc(SK[id].name)}" aria-label="Remove ${id}, ${esc(SK[id].name)}, from this session">${id} ${icon('x')}</button>`).join('');
  return `<div class="page-h"><div><h1>Practice</h1><p>${esc(curProp().name)} · ${weekMinutes(m)} min in the last 7 days · ${streak(m)}-day streak</p></div></div>
  <form class="card" id="log-form" style="margin-bottom:16px"><div class="card-h"><h2>Log a session</h2></div>
    <div class="form-grid">
      <label class="field">Date<input type="date" name="date" value="${dayKey()}" max="${dayKey()}" required></label>
      <label class="field">Minutes<input type="number" name="minutes" min="1" max="600" value="25" required></label>
      ${curProp().ready ? `<label class="field span-mobile">Add a skill you worked on<span class="add-pick"><select data-pick="focus-add"><option value="">Choose a skill</option>${skillOptions()}</select><button type="button" class="btn" data-act="add-chosen" disabled>Add</button></span></label>` : ''}
      ${focus ? `<div class="chips span-all">${focus}</div>` : ''}
      <label class="field span-all">Notes<textarea name="notes" placeholder="What worked, what to fix next time"></textarea></label>
    </div>
    <div class="row" style="margin-top:12px"><button class="btn primary" type="submit">${icon('plus')}Save session</button></div></form>
  <div class="card" data-tour="sessions"><div class="card-h"><h2 class="grow">Sessions</h2><span class="sub">${sessions.length} logged</span></div>
  ${sessions.length ? `<div class="table-wrap"><table class="table"><thead><tr><th>Date</th><th>Time</th><th>Skills</th><th>Notes</th><th></th></tr></thead><tbody>
  ${sessions.map(s => `<tr><td style="white-space:nowrap">${esc(fmtDay(s.date))}</td><td class="num" style="white-space:nowrap">${esc(fmtMin(Number(s.minutes) || 0))}</td><td><div class="chips">${(s.focus || []).filter(i => SK[i]).map(i => `<a class="chip" href="${skillHref(i)}" data-tip="${esc(SK[i].name)}">${i}</a>`).join('')}</div></td><td class="small muted">${esc(s.notes)}</td><td><button class="icon-btn" data-act="session-del" data-id="${esc(s.id)}" aria-label="Delete session">${icon('trash')}</button></td></tr>`).join('')}</tbody></table></div>` : '<div class="empty">No sessions yet. Your first entry starts the heatmap on the dashboard.</div>'}</div>`;
}

const TR = { untested: { n: 'Untested', i: 'circle' }, works: { n: 'Works', i: 'check' }, bridge: { n: 'Needs bridge', i: 'bridge' }, blocked: { n: 'Blocked', i: 'x' } };
const TR_ORDER = ['untested', 'works', 'bridge', 'blocked'];
// After a logged run, the member is asked how each transition of that combo went that they have not
// marked anywhere: once per transition, only while they share their marks, and never again once they
// say so (the askTransitions preference). `asking` is the combo and the transitions still to ask,
// and lives only until they are answered; state.asked remembers every one that was put to them.
let asking = null;
const askOn = () => flags.suggest && !!me?.prefs?.shareTransitions && me.prefs.askTransitions !== false;
function askFor(c) {
  if (!askOn()) return null;
  const mine = myMarks(), seq = c.seq || [], keys = [];
  for (let i = 0; i < seq.length - 1; i++) {
    const k = `${seq[i]}>${seq[i + 1]}`;
    if (seq[i] !== seq[i + 1] && !mine[k] && !state.asked?.[k] && !keys.includes(k)) keys.push(k);
  }
  return keys.length ? { id: c.id, keys } : null;
}
function askHTML(c) {
  if (!asking || asking.id !== c.id || !askOn()) return '';
  const k = asking.keys[0], [a, b] = k.split('>'), left = asking.keys.length - 1;
  const btn = (v, n) => `<button class="btn sm" data-act="tr-ask" data-id="${esc(c.id)}" data-k="${esc(k)}" data-v="${v}">${n}</button>`;
  return `<div class="cb-ask" role="group" aria-label="How did ${esc(a)} into ${esc(b)} go?"><b>How did ${esc(a)} into ${esc(b)} go?</b>
    <span class="tiny muted grow">${esc(SK[a]?.name || a)} into ${esc(SK[b]?.name || b)}. Your answer is shared as a count, with no name${left ? `. ${left} more to ask` : ''}.</span>
    ${btn('works', 'Works')}${btn('bridge', 'Needs a bridge')}${btn('blocked', 'Blocked')}${btn('skip', 'Skip')}
    <button class="btn sm cb-ask-off" data-act="tr-ask-off">Do not ask again</button></div>`;
}
function comboReady(c) {
  if ((c.seq || []).length < 2) return false;
  for (let i = 0; i < c.seq.length - 1; i++) if ((c.tr?.[`${c.seq[i]}>${c.seq[i + 1]}`] || 'untested') !== 'works') return false;
  return true;
}
function pageCombos() {
  if (!curProp().ready) return needCatalog('Combos');
  const roles = { reliable: 'Reliable', signature: 'Signature', stretch: 'Stretch' };
  const open = state.combos.find(c => c.id === ui.comboOpen) || state.combos[0];
  const cards = state.combos.map(c => {
    const seq = c.seq || [], reps = c.reps || [], ready = comboReady(c), secs = flags.demos ? Demo.comboSecs(CID, seq, reps) : 0;
    const meta = `<span class="chip">${roles[c.role] || 'Reliable'}</span><span class="chip num">${seq.length} ${seq.length === 1 ? 'skill' : 'skills'}${flags.demos && seq.length ? ' · ' + Demo.clock(secs) : ''}</span>${ready ? `<span class="chip ok">${icon('check')}Ready</span>` : ''}`;
    // Closed combos are one line: open one to play and edit it.
    if (c !== open) return `<button class="cb-row" data-act="combo-open" data-id="${esc(c.id)}" aria-expanded="false"><b>${esc(c.name)}</b><span class="cb-mini">${seq.map(id => `<i>${esc(id)}</i>`).join('') || '<em>empty</em>'}</span><span class="row" style="gap:6px">${meta}</span>${icon('down')}</button>`;
    const runs = Number(c.runs) || 0, cleanRuns = Number(c.clean) || 0;
    const steps = seq.map((id, i) => {
      const inf = flags.demos ? Demo.info(CID, id) : { has: false }, n = reps[i] || 1;
      const trKey = i < seq.length - 1 ? `${id}>${seq[i + 1]}` : '', t = trKey ? (c.tr?.[trKey] || 'untested') : '';
      const tag = !flags.demos ? '' : !inf.has ? '<span class="cb-tag warn">no demo</span>' : inf.still ? '<span class="cb-tag">picture</span>' : `<span class="cb-tag num">${(inf.d * n).toFixed(1)} s</span>`;
      return `<li class="cb-step ${inf.has ? '' : 'nodemo'}" data-step="${i}">
        <button class="cb-main" data-act="combo-demo" data-k="step" data-v="${i}" aria-label="Play from ${esc(id)}"><span class="cb-n num">${i + 1}</span><span class="cb-id">${esc(id)}</span><span class="cb-name">${esc(SK[id]?.name || id)}</span>${tag}</button>
        ${inf.has && !inf.still ? `<span class="cb-reps" role="group" aria-label="Repetitions of ${esc(id)}"><button data-act="combo-rep" data-id="${esc(c.id)}" data-i="${i}" data-d="-1" aria-label="One fewer" ${n <= 1 ? 'disabled' : ''}>${icon('minus')}</button><b class="num">${n}×</b><button data-act="combo-rep" data-id="${esc(c.id)}" data-i="${i}" data-d="1" aria-label="One more" ${n >= 9 ? 'disabled' : ''}>${icon('plus')}</button></span>` : ''}
        <a class="icon-btn" href="${skillHref(id)}" data-tip="Open ${esc(id)}" aria-label="Open ${esc(id)}">${icon('ext')}</a>
        <button class="icon-btn" data-act="combo-move" data-id="${esc(c.id)}" data-i="${i}" aria-label="Move ${esc(id)} earlier" ${i ? '' : 'disabled'}><span style="transform:rotate(180deg);display:grid">${icon('down')}</span></button>
        <button class="icon-btn" data-act="combo-rm" data-id="${esc(c.id)}" data-i="${i}" aria-label="Remove ${esc(id)}">${icon('x')}</button>
      </li>${trKey ? `<li class="cb-link"><button class="tr ${t}" data-act="combo-tr" data-id="${esc(c.id)}" data-k="${esc(trKey)}" data-tip="Transition ${esc(trKey.replace('>', ' to '))}. Click to change.">${icon(TR[t].i)}${TR[t].n}</button></li>` : ''}`;
    }).join('');
    // Logging a run sits under the player, where the run was just watched; without demos it closes the list.
    const log = `<div class="cb-log" role="group" aria-label="Log a run"><b>Log a run</b><span class="small muted num grow">${cleanRuns} clean of ${runs} full runs</span>
      <button class="btn sm" data-act="combo-run" data-id="${esc(c.id)}" data-clean="1">${icon('check')}Clean run</button><button class="btn sm" data-act="combo-run" data-id="${esc(c.id)}" data-clean="0">${icon('x')}Dropped</button></div>${askHTML(c)}`;
    return `<section class="card cb" aria-label="${esc(c.name)}">
      <header class="cb-head"><h2 class="grow">${esc(c.name)}</h2>${meta}${flags.community && seq.length >= 2 ? `<button class="btn sm" data-act="combo-publish" data-id="${esc(c.id)}" data-tip="Share this combo on the timeline">${icon('send')}Publish</button>` : ''}${bmButton('combo', c.id)}<button class="icon-btn" data-act="combo-del" data-id="${esc(c.id)}" aria-label="Delete combo">${icon('trash')}</button></header>
      <div class="cb-body">
        ${flags.demos ? Demo.comboStage(c.id, CID, seq, reps, id => SK[id]?.name || id, state.settings.dominant, esc, log) : ''}
        <div class="cb-side">
          <ol class="cb-steps">${steps || '<li class="small muted" style="padding:8px 2px">Start with two skills you can already do.</li>'}</ol>
          ${nextTrickHTML(c)}
          <span class="add-pick"><select data-pick="combo-add" data-id="${esc(c.id)}" aria-label="Skill to add to ${esc(c.name)}"><option value="">Choose a skill to add</option>${skillOptions()}</select><button type="button" class="btn" data-act="add-chosen" disabled>Add</button></span>
          ${flags.demos ? '' : log}
        </div>
      </div></section>`;
  }).join('');
  return `<div class="page-h"><div><h1>Combos</h1><p>Build a sequence, watch it, and mark each link as you test it. A combo is ready when every link works.</p></div></div>
  <form class="card" id="combo-form" style="margin-bottom:16px"><div class="row wrap" style="align-items:flex-end">
    <label class="field grow" style="min-width:200px">Combo name<input type="text" name="name" maxlength="80" required placeholder="Morning loop"></label>
    <label class="field" style="width:150px">Role<select name="role"><option value="reliable">Reliable</option><option value="signature">Signature</option><option value="stretch">Stretch</option></select></label>
    <button class="btn primary" type="submit" style="height:36px">${icon('plus')}New combo</button></div></form>
  <div class="stack">${cards || '<div class="empty">No combos yet. Start with two skills you can already do.</div>'}</div>`;
}

function pagePlan() {
  if (!curProp().ready) return needCatalog('Plan');
  const cur = currentPhase();
  const start = state.settings.trackStart;
  let week = 0;
  if (start) week = Math.floor((parseDay(dayKey()) - parseDay(start)) / (7 * 864e5)) + 1;
  // Each phase: its skills, the XP they hold, and the gate that ends it.
  const phases = PHASES.map((p, i) => {
    const list = p.skills.split(' ').map(id => SK[id]).filter(Boolean), done = state.gates[p.id];
    const met = p.req.every(needMet), pc = pctOf(list);
    const earned = list.reduce((a, s) => a + variants(s).reduce((b, [k]) => b + XP_SKILL[lv(s.id, k)], 0), 0);
    const possible = list.reduce((a, s) => a + variants(s).length * XP_SKILL[4], 0);
    return `<div class="card phase quest ${done ? 'done' : p === cur ? 'on' : ''}"><span class="phase-n ${done ? 'done' : p === cur ? 'on' : ''}">${done ? icon('check') : i + 1}</span><div>
      <div class="row wrap"><h2 class="grow">${esc(p.name)}</h2>${p === cur && !done ? '<span class="chip on">Current phase</span>' : ''}<span class="tiny">${esc(p.time)} (estimate)</span></div>
      <div class="row" style="margin:9px 0 11px"><div class="bar grow"><i style="width:${pc}%"></i></div><span class="num small muted">${pc}%</span>${flags.game ? `<span class="chip num" data-tip="Skill XP earned in this phase, of what its skills hold at Performance level">${earned.toLocaleString()} / ${possible.toLocaleString()} XP</span>` : ''}</div>
      <div class="quest-skills">${list.map(s => `<a class="qs ${best(s.id) >= 2 ? 'ok' : best(s.id) ? 'part' : ''}" href="${skillHref(s.id)}" data-tip="${esc(s.name)}: ${esc(statusLabel(s.id))}">${best(s.id) >= 2 ? icon('check') : ''}${s.id}</a>`).join('')}<span class="tiny num">${cleanCount(list)} of ${list.length} clean</span></div>
      <label class="check" style="margin-top:10px"><input type="checkbox" data-change="gate" data-id="${p.id}" ${done ? 'checked' : ''}><span class="grow">${esc(p.gate)}</span>${flags.game ? `<span class="chip ${done ? 'ok' : ''} num">${done ? icon('check') : '+'}${XP.gate} XP</span>` : ''}</label>
      <div class="chips" style="margin-top:9px">${p.req.map(reqChip).join('')}${met && !done ? '<span class="chip ok">Skills ready. Test the gate.</span>' : ''}</div></div></div>`;
  }).join('');
  const weeks = WEEKS.map(w => `<div class="wk ${week >= w.from && week <= w.to ? 'now' : ''}"><b class="small">${esc(w.label)}</b><div><div class="small" style="font-weight:540">${esc(w.focus)}</div><div class="tiny">${esc(w.result)}</div></div>
    <label class="check"><input type="checkbox" data-change="gate" data-id="${w.id}" ${state.gates[w.id] ? 'checked' : ''}><span class="grow">${esc(w.gate)}</span>${flags.game ? `<span class="chip ${state.gates[w.id] ? 'ok' : ''} num">${state.gates[w.id] ? icon('check') : '+'}${XP.week}</span>` : ''}</label></div>`).join('');
  const focus = nextUp(1)[0];
  const focusCard = `<div class="card" data-tour="focus"><div class="card-h"><h2 class="grow">Today\u2019s focus</h2><span class="sub">Ready to learn</span></div>
    ${focus ? `<a class="sk-row" style="padding-left:0" href="${skillHref(focus.id)}"><span class="tag">${focus.id}</span><span class="grow"><b style="font-weight:560">${esc(focus.name)}</b><br><span class="tiny">${esc(focus.desc)}</span></span>${icon('right')}</a>
    ${flags.details && focus.drill ? `<p class="small muted" style="margin-top:8px"><b>Drill:</b> ${esc(focus.drill)}</p>` : ''}<p class="tiny" style="margin-top:8px">Clean it for +${XP_SKILL[2]} XP per hand or direction, and it unlocks what depends on it.</p>` : '<div class="empty small">Nothing new is unlocked. Bring a skill to Clean first.</div>'}</div>`;
  return `<div class="page-h"><div><h1>Plan</h1><p>${PHASES.length} long-term phases for ${esc(curProp().name.toLowerCase())}${WEEKS.length ? ', plus a 12-week track for turning existing skills into one routine' : ''}.${flags.game ? ` Each gate passed is ${XP.gate} XP.` : ''} Time estimates are rough.</p></div></div>
  ${flags.game ? `<div class="grid g3" style="margin-bottom:16px">${propXPCard()}${challengesCard()}${focusCard}</div>` : ''}
  <div class="stack" style="margin-bottom:26px">${phases}</div>
  ${!WEEKS.length ? '' : `<div class="card" data-tour="track"><div class="card-h"><h2 class="grow">12-week contest track</h2>${start ? `<span class="chip">${week > 12 ? 'Track finished' : week < 1 ? 'Not started' : 'Week ' + week}</span>` : ''}</div>
    <div class="grid g2" style="margin-bottom:12px">
      <label class="field">Track start date<input type="date" data-change="setting" data-k="trackStart" value="${esc(start)}"></label>
      <label class="field">Target event and edition<input type="text" data-change="setting" data-k="event" maxlength="120" value="${esc(state.settings.event)}" placeholder="Not chosen yet"></label>
    </div>
    <p class="notice warn" style="margin-bottom:10px">Check the current rules for your event before shaping a routine. WNA freestyle (January 2026) is 90 to 180 seconds. The Golden Combo figure of 25 to 35 seconds is unconfirmed.</p>
    ${weeks}<p class="tiny" style="margin-top:10px">Phases are adjustable, not a promise of mastery in twelve weeks. Stay in a phase until you pass its gate.</p></div>`}`;
}
function propXPCard() {
  const x = propXP(), all = xpAll(), L = levelInfo(all.total);
  return `<div class="card" data-tour="xp"><div class="card-h"><h2 class="grow">${esc(curProp().name)} XP</h2><a class="small" href="#/achievements">Level ${L.level} · ${L.title}</a></div>
    <div class="val num" style="font-size:27px;font-weight:640;letter-spacing:-.02em">${x.total.toLocaleString()}<small style="font-size:13px;font-weight:500;color:var(--ink-3)"> of ${all.total.toLocaleString()} XP overall</small></div>
    <div class="xp-split num">${[['Skills', x.skills], ['Practice', x.practice], ['Gates', x.gates], ['Combos', x.combos]].map(([n, v]) => `<span><b>${v.toLocaleString()}</b><span class="tiny">${n}</span></span>`).join('')}</div>
    <div class="bar" style="margin-top:10px"><i style="width:${L.pct}%"></i></div><div class="tiny num" style="margin-top:4px">${L.next.toLocaleString()} XP to level ${L.level + 1}</div></div>`;
}

function pageSettings() {
  const theme = localStorage.getItem('nf-theme') || 'auto';
  return `<div class="page-h"><div><h1>Settings</h1><p>Theme is saved in this browser.${me ? ' Everything else is saved to your account.' : ''}</p></div></div>
  <div class="grid g2">
    <div class="card"><div class="card-h"><h2>Preferences</h2></div>
      <div class="stack"><div class="field">Theme<div class="seg" style="width:max-content">${['auto', 'light', 'dark'].map(t => `<button class="${t === theme ? 'on' : ''}" data-act="theme" data-v="${t}">${t[0].toUpperCase() + t.slice(1)}</button>`).join('')}</div></div>
      ${me ? `<div class="field">Dominant hand<div class="seg" style="width:max-content">${[['L', 'Left'], ['R', 'Right']].map(([k, n]) => `<button class="${state.settings.dominant === k ? 'on' : ''}" data-act="dominant" data-v="${k}">${n}</button>`).join('')}</div></div>` : ''}</div></div>
    ${me ? `<div class="card"><div class="card-h"><h2>Data: ${esc(curProp().name)}</h2></div><p class="small muted" style="margin-bottom:12px">Export, import and reset apply to your ${esc(curProp().name.toLowerCase())} progress only. The server also keeps a daily backup for 14 days.</p>
      <div class="row wrap"><button class="btn" data-act="export">Export JSON</button><label class="btn">Import JSON<input type="file" accept="application/json" data-change="import" hidden></label><button class="btn danger" data-act="reset">Reset</button></div></div>` : ''}
    <div class="card"><div class="card-h"><h2>About the skill maps</h2></div><p class="small muted">Each map takes its technique names from the sources listed on its skills. Prerequisites and tiers are coaching proposals, not rules of the art. Skills marked unconfirmed could not be checked against a source.</p>
      <p class="small muted" style="margin-top:10px">What is planned for Flowa itself is in its repository, the same for every prop.</p>
      <div class="row wrap" style="margin-top:10px"><a href="${DOCS_URL}roadmap.md" target="_blank" rel="noopener noreferrer" class="btn sm">Roadmap ${icon('ext')}</a><a href="${DOCS_URL}epics.md" target="_blank" rel="noopener noreferrer" class="btn sm">Epics ${icon('ext')}</a></div></div>
  </div>`;
}
const notFound = () => `<div class="empty">Page not found. <a href="#/">Go to the dashboard</a>.</div>`;
const pageOff = what => `<div class="empty">${what} is turned off on this Flowa. An admin can turn it on under Admin, Features. <a href="#/">Go to the dashboard</a>.</div>`;

/* ---------- shell ---------- */
function renderSide(route) {
  const a = (href, ic, label, on) => `<a class="nav-a ${on ? 'on' : ''}" href="${href}" ${on ? 'aria-current="page"' : ''}>${icon(ic)}${label}</a>`;
  const curId = route[0] || 'dashboard';
  drawSync();
  // A new member has no props yet, so nothing else in the menu would open: it lists only what does.
  if (me && !me.onboarded) {
    const on = curId === 'profile';
    $('#menu').innerHTML = `<div class="brand"><img src="icon.svg" alt=""><div><b>Flowa</b><span class="tiny">Flow arts tracker</span></div></div>
    <a class="nav-profile ${on ? 'on' : ''}" href="#/profile" ${on ? 'aria-current="page"' : ''}>${avatarHTML(me)}<span class="grow"><b>${esc(me.displayName || me.username)}</b><span class="tiny">@${esc(me.username)}</span></span></a>
    ${a('#/', 'home', 'Choose your props', !on && curId !== 'settings')}
    <div class="side-foot">${a('#/settings', 'gear', 'Settings', curId === 'settings')}</div>`;
    return;
  }
  const props = myProps().map(p => {
    const cat = CATALOGS[p.id];
    if (!cat) {
      const on = curId === 'prop' && route[1] === p.id;
      return `<a class="nav-a ${on ? 'on' : ''}" href="#/prop/${p.id}" ${on ? 'aria-current="page"' : ''}><span class="letter">${esc(p.name[0])}</span>${esc(p.name)}<span class="nav-pct">soon</span></a>`;
    }
    const open = ui.open.includes(p.id), here = p.id === ui.prop;
    const pct = k => me ? `<span class="nav-pct">${inProp(p.id, () => pctOf(k ? branchSkills(k) : SKILLS))}%</span>` : '';
    return `<button class="nav-a nav-group ${here ? 'here' : ''}" data-act="nav-toggle" data-id="${p.id}" aria-expanded="${open}" aria-controls="nav-${p.id}">${icon(open ? 'down' : 'right')}${esc(p.name)}${pct()}</button>
      <div id="nav-${p.id}" class="nav-sub" ${open ? '' : 'hidden'}>${cat.branches.map(b => { const on = here && curId === 'branch' && route[1] === b.k; return `<a class="nav-a ${on ? 'on' : ''}" href="#/prop/${p.id}/${b.k}" ${on ? 'aria-current="page"' : ''}><span class="letter">${b.k}</span>${esc(b.name)}${pct(b.k)}</a>`; }).join('')}</div>`;
  }).join('');
  const staff = staffPages();
  const profile = me
    ? `<a class="nav-profile ${curId === 'profile' ? 'on' : ''}" href="#/profile" ${curId === 'profile' ? 'aria-current="page"' : ''}>${avatarHTML(me)}<span class="grow"><b>${esc(me.displayName || me.username)}</b><span class="tiny">@${esc(me.username)}</span></span></a>`
    : `<a class="nav-profile ${curId === 'signin' || curId === 'signup' ? 'on' : ''}" href="#/signin">${avatarHTML(null)}<span class="grow"><b>Sign in</b><span class="tiny">or create an account</span></span></a>`;
  $('#menu').innerHTML = `<div class="brand"><img src="icon.svg" alt=""><div><b>Flowa</b><span class="tiny">Flow arts tracker</span></div></div>
    ${profile}
    ${a('#/', 'home', me ? 'Dashboard' : 'Welcome', curId === 'dashboard')}
    ${me && flags.community ? a('#/timeline', 'feed', 'Timeline', curId === 'timeline' || curId === 'post' || curId === 'u') : ''}
    ${a('#/maps', 'map', 'Skill maps', curId === 'maps')}
    ${me ? a('#/practice', 'log', 'Practice', curId === 'practice') + a('#/combos', 'link', 'Combos', curId === 'combos') + a('#/plan', 'flag', 'Plan', curId === 'plan') : ''}
    ${me && flags.bookmarks ? a('#/bookmarks', 'bookmark', 'Bookmarks', curId === 'bookmarks') : ''}
    ${me && flags.game ? a('#/achievements', 'trophy', 'Achievements', curId === 'achievements') : ''}
    <div class="nav-h">Props</div>
    ${props}
    <div class="side-foot">${staff.length ? `<div class="side-staff"><div class="nav-h">Staff</div>${staff.map(([n, h, ic]) => h === '#/mod' ? a(h, ic, `${n}${social.queue ? `<span class="nav-pct nav-count">${social.queue}</span>` : ''}`, curId === 'mod') : h === '#/admin' ? a(h, ic, n, curId === 'admin') : `<a class="nav-a" href="${esc(h)}" rel="noopener noreferrer">${icon(ic)}${n}</a>`).join('')}</div>` : ''}${a('#/settings', 'gear', 'Settings', curId === 'settings')}</div>`;
}
// The pages for the people who run this Flowa, for the menu and the search: the moderation queue, the admin
// area, and for admins the start page of the machine itself when the server names one (FLOWA_HOME_URL).
function staffPages() {
  if (!me) return [];
  return [...(flags.moderation && canDo('report.resolve') ? [['Moderation', '#/mod', 'inbox']] : []), ...(adminTabs().length ? [['Admin', '#/admin', 'shield']] : []), ...(homeUrl ? [['Server home', homeUrl, 'ext']] : [])];
}
// Branch pages live at #/prop/<prop>/F/F1; inside the app that is the 'branch' route,
// with the prop as its fourth part. Older #/branch/... and #/map links still work.
function parseRoute() {
  const r = location.hash.replace(/^#\/?/, '').split('/').filter(Boolean);
  if (r[0] === 'branch') return ['branch', r[1], r[2], 'nunchaku'];
  if (r[0] === 'prop' && r[2]) return ['branch', r[2], r[3], r[1]];
  if (r[0] === 'map') return ['maps'];
  return r;
}
const TITLES = { maps: 'Skill maps', prop: 'Skill maps', practice: 'Practice', combos: 'Combos', plan: 'Plan', settings: 'Settings', profile: 'Profile', signin: 'Sign in', signup: 'Create an account', admin: 'Admin', timeline: 'Timeline', post: 'Post', u: 'Profile', achievements: 'Achievements', mod: 'Moderation', avatar: 'Avatar', bookmarks: 'Bookmarks' };
function pageFor(p, a, b, title) {
  if (!p) return me ? pageDashboard() : pageWelcome();
  if (p === 'maps' || p === 'prop') return pageMaps();
  if (p === 'branch') return pageBranch(a, b);
  if (p === 'signin' || p === 'signup') return pageAuth(p);
  if (p === 'settings') return pageSettings();
  if (['practice', 'combos', 'plan', 'profile', 'timeline', 'post', 'u', 'achievements', 'avatar', 'bookmarks'].includes(p) && !me) return pageNeedAccount(title, p);
  if (p === 'avatar') return pageAvatar(a);
  if (p === 'bookmarks') return flags.bookmarks ? pageBookmarks() : pageOff('Bookmarks');
  if (p === 'practice') return pagePractice();
  if (['timeline', 'post', 'u'].includes(p) && !flags.community) return pageOff('Community');
  if (p === 'achievements' && !flags.game) return pageOff('Gamification');
  if (p === 'timeline' || p === 'post') return pageTimeline(p === 'post' ? a : '', p === 'post' ? b : '');
  if (p === 'u') return pageUser(a);
  if (p === 'achievements') return pageAchievements();
  if (p === 'combos') { if (a) ui.comboOpen = a; return pageCombos(); }
  if (p === 'plan') return pagePlan();
  if (p === 'profile') return pageProfile();
  if (p === 'admin' && me) return pageAdmin(a);
  if (p === 'mod' && me) return pageMod(a);
  return notFound();
}
let lastRouteKey = '';
function render() {
  const route = parseRoute(), [p, a, b, rp] = route;
  if (p === 'branch' && rp !== ui.prop && propById(rp)) { switchProp(rp); return; }
  if (p === 'prop' && a !== ui.prop && propById(a)) { switchProp(a); return; }
  // A bookmarked combo can belong to another of the user's props: switch to that prop first.
  if (p === 'combos' && a && me && !state.combos.some(c => c.id === a)) {
    const owner = myProps().find(q => q.id !== ui.prop && docs[q.id]?.state.combos.some(c => c.id === a));
    if (owner) { ui.comboOpen = a; switchProp(owner.id); return; }
  }
  if (me && (p === 'signin' || p === 'signup')) { location.hash = '#/'; return; }
  // A new user picks their props before anything else.
  // Until then only their profile and Settings open, and the menu lists only those.
  const fresh = !!me && !me.onboarded, first = fresh && p !== 'settings' && p !== 'profile';
  renderSide(route);
  const title = first ? 'Welcome' : !p ? (me ? 'Dashboard' : 'Welcome') : p === 'branch' ? (BR[a]?.name || 'Branch') : p === 'u' && a ? '@' + a : TITLES[p] || 'Not found';
  const scoped = me && !first && (!p || ['branch', 'practice', 'combos', 'plan', 'maps', 'prop'].includes(p));
  $('#crumb').innerHTML = `Flowa / ${scoped ? esc(curProp().name) + ' / ' : ''}<b>${esc(title)}</b>`;
  document.title = `${title} · Flowa`;
  const gw = $('.graph-wrap'), sx = gw?.scrollLeft, sy = gw?.scrollTop;
  const key = first ? 'welcome' : `${p}/${a}/${ui.prop}/${me ? me.id : 0}`;
  // Feeds, profiles and expanded threads reload when their page is opened.
  if (key !== lastRouteKey) Object.assign(social, { stale: true, profile: {}, full: {}, fullReplies: {}, replyTo: null, editing: 0, focus: 0 });
  renderBell();
  // Keep form text when a save re-renders the page; only the panel and shell update.
  const active = document.activeElement;
  const typing = active && $('#main').contains(active) && /^(INPUT|TEXTAREA)$/.test(active.tagName) && active.type !== 'checkbox' && key === lastRouteKey;
  if (!typing) {
    const form = $('#log-form');
    const keep = form && key === lastRouteKey ? { date: form.date.value, minutes: form.minutes.value, notes: form.notes.value } : null;
    const tipKey = !p ? 'dashboard' : p === 'prop' ? 'maps' : p;
    $('#main').innerHTML = first ? pageChooser()
      : tipCard(tipKey) + (me && (!p || ['practice', 'combos', 'plan'].includes(p)) ? propTabs() : '') + pageFor(p, a, b, title);
    const nf = $('#log-form');
    if (nf && keep) { nf.date.value = keep.date; nf.minutes.value = keep.minutes; nf.notes.value = keep.notes; }
  }
  Demo.figure((flags.demos || flags.avatars) && flags.model3d); // the 3D model is fetched the first time it is wanted
  avatarMount(); // the signed-in user's avatar for demos, and the editor's or a profile's preview
  Demo.comboMount(); // the combo player, when one is open
  const hw = $('.heat-wrap');
  if (hw) hw.scrollLeft = hw.scrollWidth; // phones show the newest weeks first
  const ngw = $('.graph-wrap');
  if (ngw && key === lastRouteKey) { ngw.scrollLeft = sx || 0; ngw.scrollTop = sy || 0; }
  if (key !== lastRouteKey) window.scrollTo(0, 0);
  lastRouteKey = key;
  const skill = !first && p === 'branch' && SK[b] ? b : null;
  if (!skill || document.activeElement?.dataset?.change !== 'note') renderPanel(skill);
  if (!skill) Demo.mount(); // a demo on the page itself, as on the landing page
  if (!fresh && !skill) maybeTour(!p ? 'dashboard' : p === 'prop' ? 'maps' : p);
}

let toastT;
function toast(msg) {
  const t = $('#toast'); t.textContent = msg;
  clearTimeout(toastT); toastT = setTimeout(() => t.textContent = '', 4500);
}
// Said, not shown: for what a person who sees the page needs no words for, such as the room left in a text
// box or a tag joining a draft. Polite, so it waits for the screen reader to finish its sentence.
let sayT;
function say(msg) {
  const t = $('#say'); t.textContent = msg;
  clearTimeout(sayT); sayT = setTimeout(() => t.textContent = '', 4500);
}

/* ---------- command palette ---------- */
// A visitor is offered the pages that open without an account; a member theirs, and staff the staff pages
// with each admin section they may open.
const pages = () => !me ? [['Welcome', '#/'], ['Skill maps', '#/maps'], ['Sign in', '#/signin'], ...(flags.signups ? [['Create an account', '#/signup']] : []), ['Settings', '#/settings'], ...propPages()]
  : [['Dashboard', '#/'], ...(flags.community ? [['Timeline', '#/timeline']] : []), ['Skill maps', '#/maps'], ['Practice', '#/practice'], ['Combos', '#/combos'], ['Plan', '#/plan'], ...(flags.game ? [['Achievements', '#/achievements']] : []), ['Profile', '#/profile'], ...(flags.avatars ? [['Avatar', '#/avatar']] : []), ...(flags.bookmarks ? [['Bookmarks', '#/bookmarks']] : []), ['Settings', '#/settings'],
    ...staffPages().filter(([, h]) => h[0] === '#'), ...adminTabs().map(([k, n]) => ['Admin: ' + n, '#/admin/' + k]), ...propPages()];
const propPages = () => [...myProps().filter(p => p.id !== ui.prop).map(p => [p.name + ' map', `#/prop/${p.id}`]), ...BRANCHES.map(b => [`${curProp().name}: ${b.name}`, `#/prop/${CID}/${b.k}`])];
function palItems(q) {
  q = q.trim().toLowerCase();
  const found = pages().filter(([n]) => n.toLowerCase().includes(q)).map(([n, h]) => ({ tag: '', n, h }));
  const hit = s => !q || s.id.toLowerCase().startsWith(q) || s.name.toLowerCase().includes(q) || (s.desc || '').toLowerCase().includes(q);
  // Skills come from every prop the member has, the one on screen first. With more than one prop each skill names its own.
  const props = palProps();
  const sk = props.flatMap(p => liveSkills(p.id).filter(hit).map(s => ({ tag: s.id, n: s.name, h: `#/prop/${p.id}/${s.b}/${s.id}`, prop: props.length > 1 ? p.name : '' })));
  return (q ? [...sk, ...found] : [...found, ...sk]).slice(0, 40);
}
// A catalog's skills that are still in use, whether or not that prop is the one on screen.
const liveSkills = id => { const c = CATALOGS[id]; return !c ? [] : c.live || c.skills.filter(s => !s.retired); };
const palProps = () => [curProp(), ...myProps().filter(p => p.id !== ui.prop)].filter(p => CATALOGS[p.id]);
// What a search covers, in words: a member's own props, or every prop for a visitor.
function palScope() {
  const names = palProps().map(p => p.name);
  const list = names.length > 1 ? names.slice(0, -1).join(', ') + ' and ' + names[names.length - 1] : names[0] || '';
  if (!me || !(me.props || []).length) return `Searches the pages and the skills of every prop: ${list}.`;
  return `Searches the pages and the skills of your props: ${list}.${names.length < PROPS.length ? ' Add a prop in your profile to search it too.' : ''}`;
}
let palPrevFocus = null;
function openPalette() {
  palPrevFocus = document.activeElement;
  const el = $('#palette'); el.hidden = false;
  el.innerHTML = `<div class="pal-box" role="dialog" aria-modal="true" aria-label="Search"><input type="search" id="pal-q" placeholder="Search skills and pages" autocomplete="off" aria-label="Search skills and pages"><div class="pal-list" id="pal-list"></div><p class="pal-hint tiny" id="pal-hint">${esc(palScope())}</p></div>`;
  ui.palIdx = 0; drawPal(); $('#pal-q').focus();
}
function closePalette() { $('#palette').hidden = true; $('#palette').innerHTML = ''; palPrevFocus?.focus?.(); }
function drawPal() {
  const items = palItems($('#pal-q').value);
  ui.palIdx = Math.min(ui.palIdx, Math.max(items.length - 1, 0));
  $('#pal-list').innerHTML = items.map((x, i) => `<a class="pal-i ${i === ui.palIdx ? 'on' : ''}" href="${x.h}" data-act="pal-go"><span class="tag">${x.tag}</span><span class="grow">${esc(x.n)}</span>${x.prop ? `<span class="pal-p">${esc(x.prop)}</span>` : ''}</a>`).join('') || '<div class="empty small">No matches</div>';
  $('.pal-i.on')?.scrollIntoView({ block: 'nearest' });
}

/* ---------- events ---------- */
document.addEventListener('click', e => {
  const t = e.target.closest('[data-act]');
  if (e.target.id === 'palette') { closePalette(); return; }
  if (e.target.closest('.side a')) document.body.classList.remove('nav-open');
  if (!t) return;
  const d = t.dataset, combo = d.id ? state.combos.find(c => c.id === d.id) : null;
  switch (d.act) {
    case 'nav-open': document.body.classList.add('nav-open'); break;
    case 'nav-close': document.body.classList.remove('nav-open'); break;
    case 'palette': openPalette(); break;
    case 'pal-go': closePalette(); break;
    case 'view': ui.graphView = d.v; localStorage.setItem('nf-view', d.v); render(); break;
    case 'level': setLevel(d.id, d.side, Number(d.v)); break;
    case 'demo': { Demo.act(d); const sid = parseRoute()[2]; if (SK[sid] && !$('#panel').hidden) renderPanel(sid); else render(); break; }
    case 'combo-open': ui.comboOpen = d.id; render(); break;
    case 'combo-rep': if (combo) { const r = (combo.reps ||= []), i = Number(d.i); r[i] = Math.max(1, Math.min(9, (r[i] || 1) + Number(d.d))); commit(); } break;
    case 'combo-move': if (combo) { const i = Number(d.i), r = (combo.reps ||= []); if (i > 0) { [combo.seq[i - 1], combo.seq[i]] = [combo.seq[i], combo.seq[i - 1]]; [r[i - 1], r[i]] = [r[i] || 1, r[i - 1] || 1]; commit(); } } break;
    case 'combo-demo': Demo.comboAct(d); render(); break;
    case 'focus-del': ui.draftFocus = ui.draftFocus.filter(x => x !== d.id); render(); break;
    case 'add-chosen': addChosen(t.parentElement.querySelector('select[data-pick]')); break;
    case 'session-del':
      if (confirm('Delete this session?')) { state.sessions = state.sessions.filter(s => s.id !== d.id); commit(); }
      break;
    case 'combo-del':
      if (combo && confirm(`Delete "${combo.name}"?`)) { state.combos = state.combos.filter(c => c !== combo); commit(); }
      break;
    case 'combo-rm': if (combo) { combo.seq.splice(Number(d.i), 1); (combo.reps ||= []).splice(Number(d.i), 1); commit(); } break;
    case 'combo-tr':
      if (combo) {
        combo.tr ||= {};
        const n = TR_ORDER[(TR_ORDER.indexOf(combo.tr[d.k] || 'untested') + 1) % TR_ORDER.length];
        combo.tr[d.k] = n;
        if (n === 'works' && (combo.suggested || []).includes(d.k)) NT.usage.worked++; // a suggestion that turned out to work
        commit();
      }
      break;
    case 'combo-run':
      if (combo) { combo.runs = (Number(combo.runs) || 0) + 1; if (d.clean === '1') combo.clean = (Number(combo.clean) || 0) + 1; asking = askFor(combo); commit(); }
      break;
    case 'tr-ask':
      if (combo && asking && asking.id === combo.id && asking.keys.includes(d.k)) {
        (state.asked ||= {})[d.k] = 1; // asked once, whatever the answer
        if (TR[d.v] && d.v !== 'untested') {
          (combo.tr ||= {})[d.k] = d.v;
          if (d.v === 'works' && (combo.suggested || []).includes(d.k)) NT.usage.worked++; // a suggestion that turned out to work
        }
        asking.keys = asking.keys.filter(k => k !== d.k);
        if (!asking.keys.length) asking = null;
        commit();
      }
      break;
    case 'tr-ask-off':
      asking = null; render();
      api('me', 'PUT', { prefs: { askTransitions: false } }).then(r => {
        if (r.ok) { me = r.data.user; toast('You will not be asked again. Your profile has the switch to turn it back on.'); }
        else toast(r.error);
        render();
      });
      break;
    case 'theme':
      if (d.v === 'auto') { localStorage.removeItem('nf-theme'); delete document.documentElement.dataset.theme; }
      else { localStorage.setItem('nf-theme', d.v); document.documentElement.dataset.theme = d.v; }
      render(); break;
    case 'dominant': state.settings.dominant = d.v; commit(); break;
    case 'export': {
      const url = URL.createObjectURL(new Blob([JSON.stringify(state, null, 2)], { type: 'application/json' }));
      const a = document.createElement('a'); a.href = url; a.download = `flowa-${ui.prop}-${dayKey()}.json`; a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      break;
    }
    case 'reset':
      if (confirm(`Erase all your ${curProp().name.toLowerCase()} progress, sessions and combos? The server keeps a daily backup.`)) { state = blank(); commit(); toast('Progress reset.'); }
      break;
  }
});
// Adding from a list of skills takes a deliberate step: the Add button beside the list, or Enter
// in it. Choosing alone adds nothing, because arrow keys and typing a letter change a list's value
// on the way to the skill that is wanted.
function addChosen(sel) {
  const v = sel?.value, d = sel?.dataset;
  if (!v) return;
  if (d.pick === 'focus-add') { if (!ui.draftFocus.includes(v)) ui.draftFocus.push(v); render(); }
  else if (d.pick === 'combo-add') {
    const c = state.combos.find(x => x.id === d.id);
    if (!c) return;
    (c.seq ||= []).push(v); (c.reps ||= [])[c.seq.length - 1] = 1; commit();
  }
  // The page was drawn again: the keyboard goes back to the same list, ready for the next skill.
  [...document.querySelectorAll(`select[data-pick="${d.pick}"]`)].find(s => (s.dataset.id || '') === (d.id || ''))?.focus();
}
document.addEventListener('change', async e => {
  const t = e.target, d = t.dataset;
  if (d.pick) { const b = t.parentElement.querySelector('[data-act="add-chosen"]'); if (b) b.disabled = !t.value; return; }
  switch (d.change) {
    case 'flag': setFlag(d.k, t.checked); break;
    case 'gate': if (t.checked) state.gates[d.id] = dayKey(); else delete state.gates[d.id]; commit(); break;
    case 'setting': state.settings[d.k] = t.value.trim(); t.blur(); commit(); break;
    case 'note': { const r = { ...rec(d.id), notes: t.value }; state.skills[d.id] = r; commit(); break; }
    case 'import': {
      const f = t.files[0]; t.value = '';
      if (!f) break;
      try {
        const o = JSON.parse(await f.text());
        if (!o || typeof o !== 'object' || !o.skills) throw new Error('not a progress file');
        if (confirm(`Replace all your ${curProp().name.toLowerCase()} progress with this file?`)) { state = normalize(o); commit(); toast('Progress imported.'); }
      } catch { toast('That file is not a valid progress export.'); }
      break;
    }
  }
});
document.addEventListener('submit', e => {
  e.preventDefault();
  const f = e.target;
  if (f.id === 'log-form') {
    state.sessions.push({ id: uid(), date: f.date.value, minutes: Math.round(Number(f.minutes.value)), focus: [...ui.draftFocus], notes: f.notes.value.trim() });
    ui.draftFocus = []; f.notes.value = '';
    document.activeElement?.blur?.(); commit(); toast('Session saved.');
  } else if (f.id === 'combo-form') {
    const name = f.name.value.trim();
    if (!name) return;
    const made = { id: uid(), name, role: f.role.value, seq: [], reps: [], tr: {}, runs: 0, clean: 0 };
    state.combos.unshift(made); ui.comboOpen = made.id;
    document.activeElement?.blur?.(); commit(); $('#combo-form')?.reset();
  }
});
document.addEventListener('input', e => { if (e.target.id === 'pal-q') { ui.palIdx = 0; drawPal(); } });
document.addEventListener('keydown', e => {
  const open = !$('#palette').hidden;
  if ((e.key === 'k' && (e.metaKey || e.ctrlKey)) || (e.key === '/' && !open && !/^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName))) { e.preventDefault(); open ? closePalette() : openPalette(); return; }
  if (open) {
    if (e.key === 'Escape') { closePalette(); return; }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault(); const n = palItems($('#pal-q').value).length;
      if (n) { ui.palIdx = (ui.palIdx + (e.key === 'ArrowDown' ? 1 : -1) + n) % n; drawPal(); }
    }
    if (e.key === 'Enter') { const x = $('.pal-i.on'); if (x) { location.hash = x.getAttribute('href'); closePalette(); } }
    return;
  }
  if (e.key === 'Enter' && e.target.matches?.('select[data-pick]')) { e.preventDefault(); addChosen(e.target); return; }
  if (e.key === 'Escape') {
    document.body.classList.remove('nav-open');
    const [p, a, b] = parseRoute();
    if (p === 'branch' && b) location.hash = `#/prop/${CID}/${a}`;
  }
});
const tip = $('#tip');
document.addEventListener('pointerover', e => {
  const t = e.target.closest?.('[data-tip]');
  if (!t || e.pointerType === 'touch') { if (tip.textContent) tip.textContent = ''; return; }
  tip.textContent = t.dataset.tip;
  const r = t.getBoundingClientRect(), w = tip.offsetWidth;
  tip.style.left = Math.max(6, Math.min(innerWidth - w - 6, r.left + r.width / 2 - w / 2)) + 'px';
  tip.style.top = (r.top > 34 ? r.top - 32 : r.bottom + 6) + 'px';
});
window.addEventListener('hashchange', render);
// Less motion can be asked for, and taken back, while Flowa is open: the demos stop where they are and
// show Play, or start again as they do for everyone else. The styles follow by themselves (app.css).
Demo.calm?.addEventListener?.('change', e => { Demo.setStill(e.matches); render(); });

/* ---------- boot ---------- */
$('.menu-btn').innerHTML = icon('menu');
$('.search-btn').innerHTML = `${icon('search')}<span>Search skills</span><kbd>Ctrl K</kbd>`;
(async () => {
  try { await loadCatalogs(); await Demo.init(); await load(); render(); }
  catch { $('#main').innerHTML = '<div class="empty">Could not reach Flowa. Nothing was changed. Reload to try again.</div>'; }
})();
