'use strict';
// The skill map editor (Admin, Skill maps). Edits a draft of one prop's catalog, checks it with the
// same rules as the server (catalog-check.js) as you type, shows what publishing would change and
// for how many people, and lists earlier revisions to export or restore. The server checks again
// before anything is published (main.go: validateCatalog, safeToPublish).

const ed = { prop: '', data: null, draft: null, dirty: false, checks: { errs: [], warn: [] }, sel: 0, q: '', view: 'skills', diff: null, busy: false };
const ED_VIEWS = [['skills', 'Skills'], ['branches', 'Branches'], ['phases', 'Phases'], ['json', 'JSON'], ['review', 'Review and publish'], ['history', 'History']];
const clone = v => JSON.parse(JSON.stringify(v));
const lines = v => String(v || '').split('\n').map(x => x.trim()).filter(Boolean);
const words = v => String(v || '').split(/[\s,]+/).map(x => x.trim()).filter(Boolean);

async function edLoad(prop) {
  ed.prop = prop; ed.diff = null;
  const r = await api('admin/catalog/' + prop);
  if (!r.ok) { toast(r.error); return; }
  if (ed.prop !== prop) return; // another prop was chosen meanwhile
  ed.data = r.data;
  ed.draft = clone(r.data.draft || r.data.published);
  ed.dirty = false; ed.sel = 0; edCheck(); render();
}
function edCheck() { ed.checks = checkCatalog(ed.draft); }
const msgsFor = (path, exact) => [...ed.checks.errs.map(m => ({ ...m, err: true })), ...ed.checks.warn].filter(m => exact ? m.path === path : m.path === path || m.path.startsWith(path + '.'));
const msgHTML = path => `<span class="ed-msg" data-path="${esc(path)}">${msgsFor(path, true).map(m => `<span class="${m.err ? 'bad' : 'warn'}">${esc(m.msg)}</span>`).join('')}</span>`;
// A field with a problem written under it is marked invalid, so the problem is said with the field and
// not only shown in red. The message sits inside the field's label and is read as part of its name.
function edMark() {
  for (const el of document.querySelectorAll('label .ed-msg')) {
    const f = el.closest('label').querySelector('input, select, textarea');
    if (f) el.querySelector('.bad') ? f.setAttribute('aria-invalid', 'true') : f.removeAttribute('aria-invalid');
  }
}
// Updates the messages and buttons in place, so typing never loses its focus.
function edRefreshChecks() {
  edCheck();
  for (const el of document.querySelectorAll('.ed-msg')) el.innerHTML = msgsFor(el.dataset.path, true).map(m => `<span class="${m.err ? 'bad' : 'warn'}">${esc(m.msg)}</span>`).join('');
  edMark();
  for (const el of document.querySelectorAll('.ed-skill')) el.classList.toggle('has-err', ed.checks.errs.some(m => m.path.startsWith(`skills.${el.dataset.i}.`) || m.path === `skills.${el.dataset.i}`));
  // The count of problems is a live region: it is written again only when it changed, so typing that
  // changes nothing says nothing.
  const sum = $('#ed-sum'), now = edSummary(); if (sum && sum.dataset.now !== now) { sum.innerHTML = now; sum.dataset.now = now; }
  const save = $('[data-act=ed-save]'); if (save) save.disabled = !ed.dirty;
}
function edSummary() {
  const e = ed.checks.errs.length, w = ed.checks.warn.length;
  return `${e ? `<span class="chip warn">${e} ${e === 1 ? 'problem' : 'problems'}</span>` : '<span class="chip ok">No problems</span>'}${w ? ` <span class="chip">${w} ${w === 1 ? 'note' : 'notes'}</span>` : ''}${ed.dirty ? ' <span class="tiny">Unsaved changes</span>' : ''}`;
}
function edTouch() { ed.dirty = true; ed.diff = null; edRefreshChecks(); }

/* ---------- views ---------- */
function edSkillsView() {
  const d = ed.draft, skills = d.skills || [], q = ed.q.trim().toLowerCase(), pubIds = new Set((ed.data.published.skills || []).map(s => s.id));
  const list = (d.branches || []).map(b => {
    const items = skills.map((s, i) => [s, i]).filter(([s]) => s.id && s.id[0] === b.k && (!q || s.id.toLowerCase().startsWith(q) || (s.name || '').toLowerCase().includes(q)));
    return `<div class="ed-branch"><div class="row"><b class="grow small">${esc(b.k)} ${esc(b.name)}</b><button class="btn sm" data-act="ed-add" data-k="${esc(b.k)}" aria-label="Add a skill to ${esc(b.name)}">${icon('plus')}Skill</button></div>
      ${items.map(([s, i]) => `<button class="ed-skill ${i === ed.sel ? 'on' : ''} ${s.retired ? 'retired' : ''} ${ed.checks.errs.some(m => m.path.startsWith(`skills.${i}.`) || m.path === `skills.${i}`) ? 'has-err' : ''}" data-act="ed-sel" data-i="${i}" data-i2="${i}"><span class="tag">${esc(s.id)}</span><span class="grow">${esc(s.name || '(no name)')}</span>${s.retired ? '<span class="tiny">retired</span>' : ''}</button>`).join('')}</div>`;
  }).join('');
  const s = skills[ed.sel], i = ed.sel;
  if (!s) return `<div class="ed-split"><div class="ed-list">${list}</div><div class="card"><div class="empty small">Choose a skill.</div></div></div>`;
  const at = `skills.${i}`, isNew = !pubIds.has(s.id);
  const field = (k, label, input) => `<label class="field">${label}${input}${msgHTML(`${at}.${k}`)}</label>`;
  const txt = (k, label, max, v = s[k]) => field(k, label, `<input type="text" data-ed="${k}" maxlength="${max}" value="${esc(v ?? '')}">`);
  const area = (k, label, v, rows = 3) => field(k, label, `<textarea data-ed="${k}" rows="${rows}">${esc(v ?? '')}</textarea>`);
  return `<div class="ed-split"><div class="ed-list"><input type="search" id="ed-q" placeholder="Find a skill by id or name" value="${esc(ed.q)}" aria-label="Find a skill">${list}</div>
    <form class="card ed-form" id="ed-skill" data-i="${i}">
      <div class="row wrap" style="gap:8px"><h2 class="grow">${esc(s.id)} ${esc(s.name || '')}</h2>${isNew ? `<button type="button" class="btn sm danger" data-act="ed-del" data-i="${i}">Delete this new skill</button>` : ''}</div>
      ${msgHTML(at)}
      <div class="grid g3">
        <label class="field">Id<input type="text" data-ed-id maxlength="3" value="${esc(s.id)}" aria-describedby="id-help"><span class="tiny" id="id-help">${isNew ? 'A new skill can take any unused id.' : 'Changing it keeps the old id as an alias, so progress follows.'}</span>${msgHTML(`${at}.id`)}</label>
        ${field('tier', 'Tier', `<select data-ed="tier" data-num="1">${[1, 2, 3, 4, 5].map(t => `<option ${s.tier === t ? 'selected' : ''}>${t}</option>`).join('')}</select>`)}
        ${field('track', 'Progress is tracked', `<select data-ed="track">${[['one', 'As one level'], ['LR', 'Per hand'], ['FR', 'Per direction']].map(([k, n]) => `<option value="${k}" ${s.track === k ? 'selected' : ''}>${n}</option>`).join('')}</select>`)}
      </div>
      ${txt('name', 'Name', 60)}
      ${area('desc', 'Description, one factual sentence', s.desc, 2)}
      ${field('needs', 'Needs (ids, a trailing ! means both hands or directions)', `<input type="text" data-ed="needs" data-words="1" value="${esc((s.needs || []).join(' '))}">`)}
      ${area('steps', 'Steps, one per line (1 to 5)', (s.steps || []).join('\n'), 4)}
      ${area('watch', 'Watch for, one per line (1 to 3)', (s.watch || []).join('\n'), 2)}
      ${area('drill', 'Drill', s.drill, 2)}
      ${field('refs', `References (keys of the catalog's refs: ${esc(Object.keys(ed.draft.refs || {}).slice(0, 8).join(', '))}${Object.keys(ed.draft.refs || {}).length > 8 ? ', …' : ''})`, `<input type="text" data-ed="refs" data-words="1" value="${esc((s.refs || []).join(' '))}">`)}
      <fieldset class="ed-motion"><legend class="small">How it moves (for next-trick suggestions; unknown unless the clip or the text says)</legend>
        <div class="grid g3">${Object.entries(MOTION).map(([k, vals]) => `<label class="field">${k[0].toUpperCase() + k.slice(1)}<select data-edm="${k}">${vals.map(v => `<option ${(s.motion || {})[k] === v ? 'selected' : ''}>${v}</option>`).join('')}</select>${msgHTML(`${at}.motion.${k}`)}</label>`).join('')}</div>${msgHTML(`${at}.motion`)}</fieldset>
      <fieldset class="ed-concepts"><legend class="small">Shared ideas this skill is about (up to 3; chosen by hand, for the "you know this from another prop" hint)</legend>
        ${CONCEPTS.some(k => conceptsOf(ed.draft)[k] !== undefined) ? `<div class="row wrap" style="gap:14px">${CONCEPTS.filter(k => conceptsOf(ed.draft)[k] !== undefined).map(k => `<label class="check" data-tip="${esc(String(conceptsOf(ed.draft)[k]))}"><input type="checkbox" data-edc="${k}" ${Array.isArray(s.concepts) && s.concepts.includes(k) ? 'checked' : ''}><span>${conceptLabel(k)}</span></label>`).join('')}</div>` : '<p class="tiny muted">This map names no shared concept yet. Name them under Branches, each with a sentence on what it means for this prop.</p>'}
        ${msgHTML(`${at}.concepts`)}</fieldset>
      <div class="row wrap" style="gap:14px">${[['star', 'Unconfirmed by a source'], ['care', 'Needs the safety note'], ['retired', 'Retired (hidden; progress kept)']].map(([k, n]) => `<label class="check"><input type="checkbox" data-ed="${k}" data-bool="1" ${s[k] ? 'checked' : ''}><span>${n}</span></label>`).join('')}</div>
    </form></div>`;
}
function edBranchesView() {
  const b = ed.draft.branches || [];
  return `<div class="card"><div class="stack" style="gap:10px">${b.map((x, i) => `<div class="grid ed-row" data-i="${i}"><span class="tag">${esc(x.k)}</span>
      <label class="field">Name<input type="text" data-edb="name" data-i="${i}" maxlength="40" value="${esc(x.name)}">${msgHTML(`branches.${i}.name`)}</label>
      <label class="field">Blurb<input type="text" data-edb="blurb" data-i="${i}" maxlength="120" value="${esc(x.blurb)}">${msgHTML(`branches.${i}.blurb`)}</label></div>`).join('')}</div>
    ${msgHTML('branches')}<button class="btn sm" data-act="ed-add-branch" style="margin-top:10px" ${b.length >= 11 ? 'disabled' : ''} aria-label="Add a branch">${icon('plus')}Branch</button></div>${edConceptsCard()}`;
}
// The shared concepts this map teaches: a sentence names one, an empty box leaves it out. Which
// skills are about a concept is ticked in the skill form, by a person.
const conceptLabel = k => CONCEPT_NAMES[k][0].toUpperCase() + CONCEPT_NAMES[k].slice(1);
const conceptsOf = d => d.concepts && typeof d.concepts === 'object' && !Array.isArray(d.concepts) ? d.concepts : {};
function edConceptsCard() {
  const c = conceptsOf(ed.draft);
  return `<div class="card ed-concept-list" style="margin-top:14px"><div class="card-h"><h2 class="grow">Shared concepts</h2><span class="sub">Ideas that carry over to other props</span></div>
    <p class="tiny muted">Write a sentence on what the idea means for this prop to name it, in the words its own sources use. Leave the box empty and this map does not teach it. Skills are ticked by hand in the skill form.</p>
    <div class="stack" style="gap:10px;margin-top:10px">${CONCEPTS.map(k => `<label class="field">${conceptLabel(k)}<input type="text" data-edk="${k}" maxlength="240" value="${esc(typeof c[k] === 'string' ? c[k] : '')}">${msgHTML(`concepts.${k}`)}</label>`).join('')}</div>${msgHTML('concepts')}</div>`;
}
function edPhasesView() {
  const ph = ed.draft.phases || [];
  return `<div class="stack">${ph.map((p, i) => `<div class="card stack" style="gap:8px"><b>${esc(p.id)}</b>
      <div class="grid g2"><label class="field">Name<input type="text" data-edp="name" data-i="${i}" maxlength="40" value="${esc(p.name)}"></label><label class="field">Time<input type="text" data-edp="time" data-i="${i}" maxlength="40" value="${esc(p.time)}"></label></div>
      <label class="field">Gate (a test you can pass or fail)<input type="text" data-edp="gate" data-i="${i}" maxlength="260" value="${esc(p.gate)}"></label>
      <div class="grid g2"><label class="field">Skills (ids)<input type="text" data-edp="skills" data-i="${i}" value="${esc(p.skills)}">${msgHTML(`phases.${i}.skills`)}</label><label class="field">Gate needs (ids)<input type="text" data-edp="req" data-words="1" data-i="${i}" value="${esc((p.req || []).join(' '))}">${msgHTML(`phases.${i}.req`)}</label></div>
      ${msgHTML(`phases.${i}`)}${msgHTML(`phases.${i}.id`)}</div>`).join('')}${msgHTML('phases')}</div>`;
}
function edJSONView() {
  return `<div class="card stack" style="gap:8px"><p class="small muted">The whole catalog, for what the forms do not cover: references, the week-by-week track and aliases. Apply checks it like any change.</p>
    <textarea id="ed-json" rows="24" spellcheck="false" class="mono" aria-label="Catalog as JSON">${esc(JSON.stringify(ed.draft, null, 1))}</textarea>
    <p class="form-err" id="ed-json-err" role="alert"></p><div><button class="btn" data-act="ed-json">Apply</button></div></div>`;
}
function edReviewView() {
  if (ed.dirty) return `<div class="card"><p class="small">Save the draft first, so the review matches what will be published.</p><button class="btn primary" data-act="ed-save" style="margin-top:10px">Save draft</button></div>`;
  if (!ed.data.draft) return `<div class="card"><div class="empty small">No draft. Make a change in the other tabs to start one.</div></div>`;
  if (!ed.diff) { edLoadDiff(); return '<div class="card"><div class="empty small">Comparing with the published map…</div></div>'; }
  const d = ed.diff, kinds = { added: 'New', changed: 'Changed', retired: 'Retired', renamed: 'Renamed', removed: 'Removed' };
  const rows = d.changes.map(c => `<tr><td><span class="tag">${esc(c.id)}</span></td><td>${kinds[c.kind]}${c.to ? ` to ${esc(c.to)}` : ''}${c.fields ? ` <span class="tiny">${esc(c.fields.join(', '))}</span>` : ''}</td><td class="num">${c.people ? `${c.people} ${c.people === 1 ? 'person' : 'people'}` : '<span class="tiny">nobody</span>'}</td></tr>`).join('');
  const problems = [...d.errors, ...d.blocked], whole = d.map || [];
  return `<div class="card"><div class="card-h"><h2 class="grow">What publishing changes</h2><span class="sub">People with progress on each skill</span></div>
    ${rows ? `<div class="table-wrap"><table class="table"><thead><tr><th>Skill</th><th>Change</th><th>Affects</th></tr></thead><tbody>${rows}</tbody></table></div>` : whole.length ? '' : '<div class="empty small">The draft is the same as the published map.</div>'}
    ${whole.length ? `<p class="small ed-whole" style="margin-top:12px">${rows ? 'Also changed' : 'Changed'} for the whole map: ${esc(whole.join(', '))}.</p>` : ''}
    ${problems.length ? `<div class="notice warn" style="margin-top:12px"><b>Cannot publish yet:</b><ul>${problems.map(p => `<li>${esc(p.msg)}</li>`).join('')}</ul></div>` : ''}
    ${canDo('catalog.publish') ? `<div class="row wrap" style="gap:8px;margin-top:14px"><input type="text" id="ed-note" maxlength="200" placeholder="What changed, for the history (optional)" class="grow" aria-label="Note"><button class="btn primary" data-act="ed-publish" ${problems.length || (!rows && !whole.length) ? 'disabled' : ''}>Publish for everyone</button></div>` : '<p class="tiny" style="margin-top:12px">An admin publishes drafts.</p>'}</div>`;
}
async function edLoadDiff() {
  const r = await api(`admin/catalog/${ed.prop}/diff`);
  if (!r.ok) { toast(r.error); return; }
  ed.diff = r.data; render();
}
function edHistoryView() {
  const rows = ed.data.revisions.filter(x => x.status === 'published').map(x => `<tr><td class="num">${x.rev}</td><td class="small">${esc(new Date(x.created).toLocaleString())}</td><td class="small">${x.source === 'shipped' ? 'Shipped with Flowa' : '@' + esc(x.author || 'deleted')}</td><td class="small">${esc(x.note)}</td>
    <td><span class="row" style="gap:6px;justify-content:flex-end"><a class="btn sm" href="api/admin/catalog/${ed.prop}/revisions/${x.rev}" download>Export</a>${canDo('catalog.publish') && x.rev !== ed.data.rev ? `<button class="btn sm" data-act="ed-restore" data-rev="${x.rev}">Restore</button>` : x.rev === ed.data.rev ? '<span class="chip ok">Live</span>' : ''}</span></td></tr>`).join('');
  return `<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Revision</th><th>When</th><th>By</th><th>Note</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>
    <p class="tiny" style="margin-top:10px">Export gives the catalog file, ready to commit to web/catalog. Restoring publishes an earlier revision again as a new one.</p></div>`;
}
adminMaps = () => {
  const r = parseRoute()[2], prop = PROPS.some(p => p.id === r) ? r : ed.prop || 'nunchaku';
  if (ed.prop !== prop) {
    // Switching props keeps unsaved edits as that prop's draft.
    if (ed.dirty && ed.data) api(`admin/catalog/${ed.prop}/draft`, 'PUT', { body: ed.draft }).then(x => toast(x.ok ? 'Your edits were saved as a draft.' : x.error));
    ed.prop = prop; ed.data = null; ed.dirty = false;
  }
  if (!ed.data) {
    if (!ed.busy) { ed.busy = true; edLoad(prop).finally(() => { ed.busy = false; }); }
    return '<div class="card"><div class="empty small">Loading the skill map…</div></div>';
  }
  queueMicrotask(edMark); // once the page that asked for this view has drawn it
  const D = ed.data, view = ED_VIEWS.find(v => v[0] === ed.view) ? ed.view : 'skills';
  const body = { skills: edSkillsView, branches: edBranchesView, phases: edPhasesView, json: edJSONView, review: edReviewView, history: edHistoryView }[view]();
  return `<div class="row wrap" style="gap:8px;margin-bottom:12px"><div class="seg" role="group" aria-label="Prop">${PROPS.map(p => `<a class="${p.id === prop ? 'on' : ''}" href="#/admin/maps/${p.id}" ${p.id === prop ? 'aria-current="page"' : ''}>${esc(p.name)}</a>`).join('')}</div>
      <span class="grow"></span><span id="ed-sum" class="row" role="status" data-now="${esc(edSummary())}" style="gap:6px">${edSummary()}</span></div>
    ${D.shippedUpdate ? `<p class="notice" style="margin-bottom:12px">Flowa was updated with a newer version of this map than the one published here. <button class="btn sm" data-act="ed-shipped">Load it as the draft</button></p>` : ''}
    <div class="card ed-bar"><span class="small grow">Published revision ${D.rev}${D.draft ? ' · a saved draft is open' : ''}</span>
      <button class="btn sm" data-act="ed-save" ${ed.dirty ? '' : 'disabled'}>Save draft</button>${D.draft || ed.dirty ? '<button class="btn sm" data-act="ed-discard">Discard draft</button>' : ''}</div>
    <div class="seg ed-views" role="group" aria-label="Edit">${ED_VIEWS.map(([k, n]) => `<button class="${k === view ? 'on' : ''}" data-act="ed-view" data-v="${k}" aria-pressed="${k === view}">${n}</button>`).join('')}</div>${body}`;
};

/* ---------- editing ---------- */
const takenIds = () => new Set([...(ed.draft.skills || []).map(s => s.id), ...Object.keys(ed.draft.aliases || {}), ...(ed.data.published.skills || []).map(s => s.id), ...Object.keys(ed.data.published.aliases || {})]);
function nextId(k) {
  const used = [...takenIds()].filter(id => id[0] === k).map(id => Number(id.slice(1)));
  return k + (Math.max(0, ...used) + 1);
}
// Changes a skill's id everywhere the catalog mentions it. A skill that is already published keeps
// its old id as an alias, so everyone's progress follows it.
function renameSkill(i, to) {
  const s = ed.draft.skills[i], from = s.id;
  if (!/^[A-Z][1-9][0-9]?$/.test(to) || to === from) return to === from || 'An id is a capital letter and a number, such as F12.';
  if (takenIds().has(to)) return `${to} is used, or was used before. Ids are never reused.`;
  const swap = n => String(n).replace(/!$/, '') === from ? to + (String(n).endsWith('!') ? '!' : '') : n;
  for (const x of ed.draft.skills) x.needs = (x.needs || []).map(swap);
  for (const p of ed.draft.phases || []) { p.skills = words(p.skills).map(swap).join(' '); p.req = (p.req || []).map(swap); }
  const al = ed.draft.aliases ||= {};
  for (const k of Object.keys(al)) if (al[k] === from) al[k] = to;
  if ((ed.data.published.skills || []).some(x => x.id === from)) al[from] = to;
  if (!Object.keys(al).length) delete ed.draft.aliases;
  s.id = to;
  return true;
}
async function edSave() {
  const r = await api(`admin/catalog/${ed.prop}/draft`, 'PUT', { body: ed.draft });
  if (!r.ok) { toast(r.error); return false; }
  ed.dirty = false; ed.data.draft = clone(ed.draft); ed.diff = null; toast('Draft saved.'); render(); return true;
}
async function reloadCatalog(prop) {
  const r = await fetch(`catalog/${prop}.json`, { cache: 'no-cache' });
  if (r.ok) { const keep = CID; addCatalog(await r.json()); CID = ''; useCatalog(keep); }
}
document.addEventListener('click', async e => {
  const t = e.target.closest('[data-act]');
  if (!t || !t.dataset.act.startsWith('ed-')) return;
  const d = t.dataset;
  switch (d.act) {
    case 'ed-view': ed.view = d.v; document.activeElement?.blur?.(); render(); break;
    case 'ed-sel': ed.sel = Number(d.i); document.activeElement?.blur?.(); render(); break;
    case 'ed-add': {
      const id = nextId(d.k);
      ed.draft.skills.push({ id, tier: 1, name: '', desc: '', needs: [], track: 'one', steps: [], watch: [], drill: '', refs: [] });
      ed.sel = ed.draft.skills.length - 1; edTouch(); render(); $('#ed-skill [data-ed=name]')?.focus(); break;
    }
    case 'ed-del': ed.draft.skills.splice(Number(d.i), 1); ed.sel = 0; edTouch(); render(); break;
    case 'ed-add-branch': {
      const used = new Set(ed.draft.branches.map(b => b.k)), k = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'.split('').find(c => !used.has(c));
      ed.draft.branches.push({ k, name: '', blurb: '' }); edTouch(); render(); break;
    }
    case 'ed-json': {
      try { ed.draft = JSON.parse($('#ed-json').value); } catch (err) { showErr('ed-json-err', 'Not valid JSON: ' + err.message, [$('#ed-json')]); break; }
      edTouch(); render(); toast('Applied. Check the problems, then save the draft.'); break;
    }
    case 'ed-save': await edSave(); break;
    case 'ed-discard':
      if (!confirm('Discard the draft? The published map stays as it is.')) break;
      { const r = await api(`admin/catalog/${ed.prop}/draft`, 'DELETE'); if (!r.ok) { toast(r.error); break; } }
      await edLoad(ed.prop); toast('Draft discarded.'); break;
    case 'ed-publish': {
      const r = await api(`admin/catalog/${ed.prop}/publish`, 'POST', { note: $('#ed-note')?.value || '' });
      if (!r.ok) { toast(r.error); ed.diff = null; render(); break; }
      await reloadCatalog(ed.prop); await edLoad(ed.prop); ed.view = 'history'; toast(`Published as revision ${r.data.rev}. Everyone gets it on their next page load.`); render(); break;
    }
    case 'ed-restore': {
      if (!confirm(`Publish revision ${d.rev} again for everyone?`)) break;
      const r = await api(`admin/catalog/${ed.prop}/revisions/${d.rev}/restore`, 'POST');
      if (!r.ok) { toast(r.error); break; }
      await reloadCatalog(ed.prop); await edLoad(ed.prop); ed.view = 'history'; toast(`Restored as revision ${r.data.rev}.`); render(); break;
    }
    case 'ed-shipped': {
      const r = await api(`admin/catalog/${ed.prop}/shipped`, 'POST');
      if (!r.ok) { toast(r.error); break; }
      await edLoad(ed.prop); ed.view = 'review'; render(); break;
    }
  }
});
document.addEventListener('input', e => {
  const t = e.target, d = t.dataset;
  if (t.id === 'ed-q') { ed.q = t.value; const list = $('.ed-list'); if (list) { const keepFocus = t.selectionStart; render(); const q = $('#ed-q'); if (q) { q.focus(); q.setSelectionRange(keepFocus, keepFocus); } } return; }
  if (d.ed !== undefined && t.closest('#ed-skill')) {
    const s = ed.draft.skills[Number(t.closest('#ed-skill').dataset.i)];
    if (d.bool) s[d.ed] = t.checked || undefined;
    else if (d.num) s[d.ed] = Number(t.value);
    else if (d.words) s[d.ed] = words(t.value);
    else if (['steps', 'watch'].includes(d.ed)) s[d.ed] = lines(t.value);
    else s[d.ed] = t.value;
    if (s[d.ed] === undefined) delete s[d.ed];
    edTouch();
  } else if (d.edc !== undefined && t.closest('#ed-skill')) {
    // A shared concept ticked or unticked: kept in the order of the list, and left out when none is ticked.
    const s = ed.draft.skills[Number(t.closest('#ed-skill').dataset.i)], on = new Set(Array.isArray(s.concepts) ? s.concepts : []);
    if (t.checked) on.add(d.edc); else on.delete(d.edc);
    if (on.size) s.concepts = CONCEPTS.filter(k => on.has(k)); else delete s.concepts;
    edTouch();
  } else if (d.edm !== undefined && t.closest('#ed-skill')) {
    const s = ed.draft.skills[Number(t.closest('#ed-skill').dataset.i)];
    s.motion = { ...(s.motion || {}), [d.edm]: t.value };
    edTouch();
  } else if (d.edk !== undefined) {
    // A concept's sentence: the map names the concept while the box has text. Kept in the order of the list.
    const c = { ...conceptsOf(ed.draft) }, v = t.value.trim();
    if (v) c[d.edk] = v; else delete c[d.edk];
    const next = {};
    for (const k of [...CONCEPTS, ...Object.keys(c)]) if (c[k] !== undefined && !(k in next)) next[k] = c[k];
    if (Object.keys(next).length) ed.draft.concepts = next; else delete ed.draft.concepts;
    edTouch();
  } else if (d.edb !== undefined) { ed.draft.branches[Number(d.i)][d.edb] = t.value; edTouch(); }
  else if (d.edp !== undefined) { const p = ed.draft.phases[Number(d.i)]; p[d.edp] = d.words ? words(t.value) : t.value; edTouch(); }
});
document.addEventListener('change', e => {
  const t = e.target;
  if (t.dataset.edId !== undefined) {
    const i = Number(t.closest('#ed-skill').dataset.i), ok = renameSkill(i, t.value.trim().toUpperCase());
    if (ok !== true) { toast(ok); t.value = ed.draft.skills[i].id; return; }
    edTouch(); render();
  } else if (t.dataset.edm !== undefined) {
    t.dispatchEvent(new Event('input', { bubbles: true }));
  } else if (t.dataset.ed !== undefined && t.closest('#ed-skill') && (t.tagName === 'SELECT' || t.type === 'checkbox')) {
    t.dispatchEvent(new Event('input', { bubbles: true })); render();
  }
});
window.addEventListener('beforeunload', e => { if (ed.dirty) e.preventDefault(); });
