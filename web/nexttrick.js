'use strict';
// Next-trick suggestions in the combo builder (behind the `suggest` flag). Scores come from
// web/suggest.js; this file loads what it needs, ranks the candidates, shows them with their reasons,
// offers bridges, and, with the user's consent, shares their transition marks anonymously.
const NT = { feats: null, weights: null, counts: {}, loading: null, shownFor: new Set(), usage: { shown: 0, added: 0, worked: 0 }, syncing: false };
const TR_RANK = { blocked: 3, works: 2, bridge: 1 };

function ntLoad(prop) {
  if (NT.loading) return NT.loading;
  NT.loading = Promise.all([
    NT.feats ? null : fetch('clips/features.json').then(r => r.ok ? r.json() : {}).then(x => { NT.feats = x; }),
    NT.weights ? null : fetch('suggest-weights.json').then(r => r.ok ? r.json() : null).then(x => { NT.weights = x || { early: true, weights: Suggest.DEFAULT }; }),
    NT.counts[prop] ? null : api('suggest/counts?prop=' + prop).then(r => { NT.counts[prop] = {}; for (const c of r.ok ? r.data.counts : []) NT.counts[prop][`${c.a}>${c.b}`] = c; }),
  ]).finally(() => { NT.loading = null; render(); });
  return NT.loading;
}
const readiness = id => { const b = best(id); return b >= 2 ? 1 : b === 1 ? 0.9 : unlocked(id) ? 0.8 : 0.35; };
const featsOf = id => (NT.feats || {})[`${CID}:${id}`];
// The user's own marks on a pair, from any combo: Blocked anywhere wins, then Works, then Needs bridge.
function myMarks(st = state) {
  const out = {};
  for (const c of st.combos || []) for (const [k, v] of Object.entries(c.tr || {})) if (TR_RANK[v] && (TR_RANK[v] > (TR_RANK[out[k]] || 0))) out[k] = v;
  for (const k of Object.keys(st.noFit || {})) out[k] = 'blocked';
  return out;
}
function nextTricks(c, limit = 5) {
  const last = (c.seq || [])[c.seq.length - 1];
  if (!last || !SK[last] || !NT.feats || !NT.weights) return { last, list: [], bridges: [] };
  const W = NT.weights.weights, side = state.settings.dominant === 'L' ? 'L' : 'R', mine = myMarks(), shared = NT.counts[CID] || {};
  const no = new Set([...(c.seq || []), ...Object.keys(state.noFit || {}).filter(k => k.startsWith(last + '>')).map(k => k.split('>')[1])]);
  const scored = SKILLS.filter(s => !no.has(s.id)).map(s => {
    const r = Suggest.transition(SK[last], featsOf(last), s, featsOf(s.id), W, side), k = `${last}>${s.id}`, n = shared[k];
    // Shared marks nudge the score: more Works than Blocked raises it, the reverse lowers it.
    const marks = n ? (n.works + 1) / (n.works + n.blocked * 2 + 1) : 1;
    return { s, r, rank: r.score * readiness(s.id) * marks * (mine[k] === 'blocked' ? 0 : 1), shared: n, mine: mine[k] };
  }).sort((a, b) => b.rank - a.rank || a.s.id.localeCompare(b.s.id, undefined, { numeric: true }));
  const list = scored.slice(0, limit);
  // When nothing links well, a bridge through tricks the user can already do.
  const bridges = [];
  if (!list.length || list[0].r.score < 0.25) {
    const known = SKILLS.filter(s => best(s.id) >= 2).map(s => s.id), cache = new Map();
    const cost = (u, v) => { const k = `${u}>${v}`; if (mine[k] === 'blocked') return Infinity; if (!cache.has(k)) cache.set(k, Suggest.transition(SK[u], featsOf(u), SK[v], featsOf(v), W, side).cost); return cache.get(k); };
    for (const x of scored.filter(x => x.s.id !== last && readiness(x.s.id) >= 0.8).slice(0, 3)) {
      const b = Suggest.bridge(last, x.s.id, [...new Set([last, ...known, x.s.id])], cost, 3);
      if (b && b.path.length > 2 && b.cost < x.r.cost - 0.2) bridges.push({ to: x.s, path: b.path });
    }
  }
  return { last, list, bridges };
}
function nextTrickHTML(c) {
  if (!flags.suggest || !c.seq?.length) return '';
  if (!NT.feats || !NT.weights || !NT.counts[CID]) { ntLoad(CID); return '<div class="nt"><div class="tiny">Finding what can follow…</div></div>'; }
  const { last, list, bridges } = nextTricks(c);
  if (list.length && !NT.shownFor.has(c.id + last)) { NT.shownFor.add(c.id + last); NT.usage.shown += list.length; }
  const label = x => [x.r.evidence === 'description' ? 'from the description' : x.r.evidence === 'little' ? 'little is known about it' : '', !x.shared ? 'untested by anyone yet' : `${x.shared.works} works, ${x.shared.blocked} blocked`].filter(Boolean);
  const item = x => `<li class="nt-i"><button class="nt-add" data-act="nt-add" data-id="${esc(c.id)}" data-s="${esc(x.s.id)}" data-tip="Add ${esc(x.s.name)} to the combo"><span class="tag">${esc(x.s.id)}</span><span class="grow"><b>${esc(x.s.name)}</b>${x.r.from || x.r.to ? `<span class="tiny"> ${esc([x.r.from, x.r.to].filter(Boolean).join(' into '))}</span>` : ''}<span class="nt-why">${esc(x.r.why.slice(0, 3).join(', '))}</span><span class="nt-labels">${label(x).map(l => `<i>${esc(l)}</i>`).join('')}</span></span>${icon('plus')}</button>
    <button class="icon-btn nt-no" data-act="nt-no" data-id="${esc(c.id)}" data-s="${esc(x.s.id)}" aria-label="Not a good fit after ${esc(last)}" data-tip="Not a good fit">${icon('x')}</button></li>`;
  return `<div class="nt" data-tour="next-trick"><div class="row"><b class="grow small">After ${esc(last)}, try</b><span class="tiny">${list.length ? 'Ranked by how it flows and whether you can do it' : ''}</span></div>
    ${NT.weights.early ? '<p class="tiny nt-early">Early suggestions: worked out from how the moves travel, not yet checked against real combos.</p>' : ''}
    <ol class="nt-list">${list.map(item).join('') || '<li class="tiny">Nothing left to suggest.</li>'}</ol>
    ${bridges.map(b => `<p class="small nt-bridge">To reach ${esc(b.to.id)} more smoothly: ${b.path.map(id => `<span class="tag">${esc(id)}</span>`).join(icon('right'))} <button class="btn sm" data-act="nt-bridge" data-id="${esc(c.id)}" data-path="${esc(b.path.slice(1).join(' '))}">Add the bridge</button></p>`).join('')}</div>`;
}

/* ---------- sharing marks, with consent ---------- */
// What was last shared is kept in each prop's progress (state.shared), so changes are sent as the
// difference: a new mark, a changed one, a deleted combo, or consent withdrawn (everything back to untested).
async function ntSync(prop, doc) {
  if (!flags.suggest || !me) return;
  const on = !!me.prefs?.shareTransitions, want = on ? myMarks(doc.state) : {}, had = doc.state.shared || {}, deltas = [];
  for (const k of new Set([...Object.keys(want), ...Object.keys(had)])) {
    const from = had[k] || 'untested', to = want[k] || 'untested';
    if (from !== to) { const [a, b] = k.split('>'); deltas.push({ a, b, from, to }); }
  }
  if (!deltas.length) return false;
  if (!on && !Object.keys(had).length) return false;
  const r = await api('suggest/labels', 'POST', { prop, deltas: deltas.slice(0, 500) });
  if (!r.ok && r.status !== 403) return false;
  doc.state.shared = on ? want : {};
  return true;
}
// Called after each save of the prop in use, and for every prop when consent changes.
async function ntSyncAll(all) {
  if (NT.syncing || !flags.suggest || !me) return;
  NT.syncing = true;
  try {
    for (const p of all ? myProps() : [curProp()]) {
      const doc = p.id === ui.prop ? { state } : docs[p.id];
      if (!doc || !(await ntSync(p.id, doc))) continue;
      if (p.id === ui.prop) commit();
      else { const r = await api('progress/' + p.id, 'PUT', { rev: docs[p.id].rev, state: doc.state }); if (r.ok) docs[p.id].rev = r.data.rev; }
    }
  } finally { NT.syncing = false; }
}
function ntFlushUsage() {
  const u = NT.usage;
  if (!flags.suggest || !me || !(u.shown || u.added || u.worked)) return;
  NT.usage = { shown: 0, added: 0, worked: 0 };
  api('suggest/usage', 'POST', u);
}
setInterval(ntFlushUsage, 30000);
document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden') ntFlushUsage(); });

document.addEventListener('click', e => {
  const t = e.target.closest('[data-act]');
  if (!t || !t.dataset.act.startsWith('nt-')) return;
  const d = t.dataset, c = state.combos.find(x => x.id === d.id);
  if (!c) return;
  const last = c.seq[c.seq.length - 1];
  switch (d.act) {
    case 'nt-add': c.seq.push(d.s); (c.reps ||= [])[c.seq.length - 1] = 1; (c.suggested ||= []).push(`${last}>${d.s}`); NT.usage.added++; commit(); break;
    case 'nt-bridge': for (const id of d.path.split(' ')) { (c.suggested ||= []).push(`${c.seq[c.seq.length - 1]}>${id}`); c.seq.push(id); (c.reps ||= [])[c.seq.length - 1] = 1; } NT.usage.added++; commit(); break;
    case 'nt-no': (state.noFit ||= {})[`${last}>${d.s}`] = true; commit(); toast('Hidden. With sharing on, it counts as Blocked.'); break;
  }
});
