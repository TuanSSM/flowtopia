'use strict';
// Moderation and administration: the report dialog, the moderation queue, and the admin area
// (dashboard, accounts, features, instance information, audit log, skill maps). The server decides who may do what
// (permissions in main.go); this file only shows what the signed-in user's permissions allow.

const canDo = p => perms.includes(p);
const adminTabs = () => [
  ['dashboard', 'Overview', canDo('dashboard.read')],
  ['accounts', 'Accounts', canDo('dashboard.read')],
  ['features', 'Features', canDo('flags.edit')],
  ['instance', 'Instance', canDo('flags.edit')],
  ['audit', 'Audit log', canDo('audit.read')],
  ['maps', 'Catalogs', flags.catalogEditor && canDo('catalog.edit')],
].filter(t => t[2]);
const REASONS = [['spam', 'Spam'], ['harassment', 'Harassment'], ['unsafe', 'Unsafe advice'], ['offtopic', 'Not about flow arts'], ['other', 'Other']];
const DURATIONS = [['1h', '1 hour', 1], ['1d', '1 day', 24], ['3d', '3 days', 72], ['7d', '1 week', 168], ['30d', '30 days', 720]];
const mod = { queue: null, history: null, audit: null, auditMore: false, filter: { actor: '', action: '' }, tab: 'queue', count: 0 };

/* ---------- reporting ---------- */
const canReport = ownerId => flags.moderation && me && ownerId !== me.id;
function openReport(kind, id, what) {
  const el = $('#modal');
  el.hidden = false;
  el.innerHTML = `<form class="modal-box narrow" id="report-form" data-kind="${kind}" data-id="${id}" role="dialog" aria-modal="true" aria-labelledby="rep-t">
    <div class="row"><h2 id="rep-t" class="grow">Report ${esc(what)}</h2><button type="button" class="icon-btn" data-act="modal-close" aria-label="Close">${icon('x')}</button></div>
    <p class="small muted" style="margin-top:4px">A moderator will look at it. The person you report is not told who reported them.</p>
    <div class="stack" role="radiogroup" aria-label="What is wrong with it" style="gap:6px;margin-top:12px">${REASONS.map(([k, n]) => `<label class="check"><input type="radio" name="reason" value="${k}" required><span>${n}</span></label>`).join('')}</div>
    <label class="field" style="margin-top:10px">Anything the moderator should know (optional)<textarea name="note" ${countAttrs('rep-count', '', 500)} rows="2"></textarea></label>${countHTML('rep-count', '', 500)}
    <p class="form-err" id="rep-err" role="alert"></p>
    <div class="row" style="margin-top:12px;justify-content:flex-end"><button type="button" class="btn" data-act="modal-close">Cancel</button><button class="btn primary" type="submit" disabled>Send report</button></div></form>`;
  $('#report-form input').focus();
}

/* ---------- the moderation page ---------- */
async function loadQueue(which) {
  const r = await api('mod/queue' + (which === 'history' ? '?status=resolved' : ''));
  if (!r.ok) { toast(r.error); return; }
  mod[which] = r.data.items;
  if (which === 'queue') mod.count = r.data.items.length;
  render();
}
function targetHTML(it) {
  const t = it.target || {};
  if (t.gone) return '<p class="small muted">It has been deleted since it was reported.</p>';
  const hidden = t.hidden ? `<span class="chip warn">${icon('lock')}Hidden: ${esc(t.hiddenReason)}</span>` : '';
  if (it.kind === 'post') return `<div class="mq-target">${t.body ? `<p class="post-body">${richText(t.body)}</p>` : ''}${t.image ? `<img class="post-img" src="${esc(t.image)}" alt="Picture in the reported post">` : ''}<div class="row wrap" style="gap:6px"><a class="small" href="#/post/${it.id}">Open the post</a>${hidden}</div></div>`;
  if (it.kind === 'comment') return `<div class="mq-target"><p class="small">${richText(t.body)}</p><div class="row wrap" style="gap:6px"><a class="small" href="#/post/${t.postId}/${it.id}">Open the comment</a>${hidden}</div></div>`;
  const a = it.author || {};
  return `<div class="mq-target"><p class="small">${a.bio ? richText(a.bio) : '<span class="muted">No bio.</span>'}</p><a class="small" href="#/u/${esc(a.username)}">Open the profile</a></div>`;
}
function authorHTML(it) {
  const a = it.author;
  if (!a) return '';
  const status = [a.mutedUntil && `Muted until ${fmtWhen(a.mutedUntil)}`, a.suspendedUntil && `Suspended until ${fmtWhen(a.suspendedUntil)}`, a.statsHidden && 'Stats hidden'].filter(Boolean);
  return `<div class="mq-author">${avatarHTML(a, 'sm')}<span class="grow"><a href="#/u/${esc(a.username)}"><b>${esc(a.displayName || a.username)}</b></a> <span class="tiny">@${esc(a.username)} · ${esc((RANK_NAMES[a.role] || a.role).toLowerCase())} · ${a.posts} ${a.posts === 1 ? 'post' : 'posts'} · ${a.actions} earlier ${a.actions === 1 ? 'action' : 'actions'}</span>
    ${status.length ? `<span class="row wrap" style="gap:4px;margin-top:4px">${status.map(s => `<span class="chip warn">${esc(s)}</span>`).join('')}</span>` : ''}
    ${it.history?.length ? `<span class="tiny mq-hist">${it.history.map(h => `${esc(fmtWhen(h.at))}: ${esc(h.action.replace('_', ' '))} by @${esc(h.actor)}${h.reason ? ` (${esc(h.reason)})` : ''}`).join('<br>')}</span>` : ''}</span></div>`;
}
function queueItemHTML(it, resolved) {
  const counts = {};
  for (const r of it.reports) counts[r.reason] = (counts[r.reason] || 0) + 1;
  const what = { post: 'Post', comment: 'Comment', user: 'Profile' }[it.kind];
  const reports = it.reports.map(r => `<li><b>${esc(REASONS.find(x => x[0] === r.reason)?.[1] || r.reason)}</b> by @${esc(r.by)}, ${esc(fmtWhen(r.at))}${r.note ? `: “${esc(r.note)}”` : ''}${resolved ? ` <span class="tiny">${esc(r.status)} by @${esc(r.resolver)}${r.resolution ? `: ${esc(r.resolution)}` : ''}</span>` : ''}</li>`).join('');
  const t = it.target || {}, a = it.author || {}, k = it.kind, mine = a.id === me.id;
  const acts = resolved || t.gone || mine ? '' : `<div class="row wrap mq-acts">
    ${k !== 'user' && canDo('post.hide') ? (t.hidden ? `<button class="btn sm" data-act="mod-do" data-do="restore" data-kind="${k}" data-id="${it.id}">Restore</button>` : `<button class="btn sm" data-act="mod-open" data-do="hide" data-kind="${k}" data-id="${it.id}">Hide…</button>`) : ''}
    ${canDo('user.mute') ? `<button class="btn sm" data-act="mod-open" data-do="mute" data-kind="user" data-id="${a.id}" data-name="${esc(a.username)}">Mute…</button>` : ''}
    ${canDo('user.suspend') ? `<button class="btn sm" data-act="mod-open" data-do="suspend" data-kind="user" data-id="${a.id}" data-name="${esc(a.username)}">Suspend…</button>` : ''}
    <span class="grow"></span>
    <button class="btn sm" data-act="mod-resolve" data-outcome="dismissed" data-kind="${k}" data-id="${it.id}">Dismiss</button>
    <button class="btn sm primary" data-act="mod-resolve" data-outcome="actioned" data-kind="${k}" data-id="${it.id}">${icon('check')}Resolve: action taken</button></div>`;
  return `<article class="card mq" data-kind="${k}" data-id="${it.id}"><div class="row wrap" style="gap:8px"><span class="chip">${what}</span><b class="grow">${it.reports.length} ${it.reports.length === 1 ? 'report' : 'reports'}: ${Object.entries(counts).map(([r, n]) => `${esc(REASONS.find(x => x[0] === r)?.[1] || r)}${n > 1 ? ' ×' + n : ''}`).join(', ')}</b><span class="tiny">since ${esc(fmtWhen(it.oldest))}</span></div>
    ${authorHTML(it)}${targetHTML(it)}<ul class="mq-reports small">${reports}</ul>${mine ? '<p class="tiny">This is about you, so another moderator handles it.</p>' : ''}${acts}</article>`;
}
function pageMod(tab) {
  if (!flags.moderation) return pageOff('Moderation');
  if (!canDo('report.resolve')) return notFound();
  mod.tab = tab === 'history' ? 'history' : 'queue';
  const list = mod[mod.tab];
  if (!list) loadQueue(mod.tab);
  const body = !list ? '<div class="empty small">Loading…</div>'
    : list.length ? `<div class="stack">${list.map(it => queueItemHTML(it, mod.tab === 'history')).join('')}</div>`
    : `<div class="empty">${mod.tab === 'queue' ? 'Nothing to review. Reports appear here, grouped by what they are about, oldest first.' : 'No resolved reports yet.'}</div>`;
  return `<div class="page-h"><div class="grow"><h1>Moderation</h1><p>Reports from members. Every action is logged, and the person affected is told.</p></div>
    <div class="seg" role="group" aria-label="Show"><a class="${mod.tab === 'queue' ? 'on' : ''}" href="#/mod" ${mod.tab === 'queue' ? 'aria-current="page"' : ''}>Queue${mod.count ? ` (${mod.count})` : ''}</a><a class="${mod.tab === 'history' ? 'on' : ''}" href="#/mod/history" ${mod.tab === 'history' ? 'aria-current="page"' : ''}>History</a></div></div>${body}`;
}
// The dialog for hiding content, muting or suspending: a reason, and an end time where needed.
function openAction(d) {
  const el = $('#modal'), timed = d.do === 'mute' || d.do === 'suspend';
  const title = { hide: `Hide this ${d.kind}`, mute: `Mute @${d.name}`, suspend: `Suspend @${d.name}` }[d.do];
  const help = { hide: 'Only its author and moderators will see it. The author is told why.', mute: 'They can read and like, but not post or comment, until the end time.', suspend: 'They are signed out at once and cannot sign in until the end time.' }[d.do];
  el.hidden = false;
  el.innerHTML = `<form class="modal-box narrow" id="action-form" data-do="${d.do}" data-kind="${d.kind}" data-id="${d.id}" data-resolve="${esc(d.resolve || '')}" role="dialog" aria-modal="true" aria-labelledby="act-t">
    <div class="row"><h2 id="act-t" class="grow">${esc(title)}</h2><button type="button" class="icon-btn" data-act="modal-close" aria-label="Close">${icon('x')}</button></div>
    <p class="small muted" style="margin-top:4px">${help}</p>
    ${timed ? `<div class="seg" role="radiogroup" aria-label="For how long" style="margin-top:12px;flex-wrap:wrap">${DURATIONS.map(([k, n], i) => `<label class="seg-r"><input type="radio" name="dur" value="${k}" ${i === 1 ? 'checked' : ''}><span>${n}</span></label>`).join('')}</div>` : ''}
    <label class="field" style="margin-top:12px">Reason, shown to them<textarea name="reason" ${countAttrs('act-count', '', 300)} rows="2" required></textarea></label>${countHTML('act-count', '', 300)}
    <p class="form-err" id="act-err" role="alert"></p>
    <div class="row" style="margin-top:12px;justify-content:flex-end"><button type="button" class="btn" data-act="modal-close">Cancel</button><button class="btn primary" type="submit">${esc(title.split(' ')[0])}</button></div></form>`;
  $('#action-form textarea').focus();
}
async function modAction(body) {
  const r = await api('mod/actions', 'POST', body);
  if (!r.ok) { toast(r.error); return false; }
  return true;
}
function refreshAfterMod() { mod.queue = null; mod.history = null; mod.dash = null; social.stale = true; for (const k in social.profile) delete social.profile[k]; render(); if (ui.users) loadUsers(); }

/* ---------- the admin area ---------- */
function pageAdmin(tab) {
  const tabs = adminTabs();
  if (!tabs.length) return notFound();
  const cur = tabs.find(t => t[0] === tab) ? tab : tabs[0][0];
  const nav = `<div class="seg admin-tabs" role="group" aria-label="Admin sections">${tabs.map(([k, n]) => `<a class="${k === cur ? 'on' : ''}" href="#/admin/${k}" ${k === cur ? 'aria-current="page"' : ''}>${n}</a>`).join('')}</div>`;
  const body = { dashboard: () => adminDashboard(), accounts: adminAccounts, features: adminFeatures, instance: adminInstance, audit: adminAudit, maps: () => adminMaps() }[cur]();
  return `<div class="page-h"><div class="grow"><h1>Admin</h1><p>Features, accounts and records for everyone on this Flowa.</p></div></div>${nav}${body}`;
}
function adminFeatures() {
  return `<div class="card"><div class="card-h"><h2 class="grow">Features</h2><span class="sub">Apply to every member, straight away</span></div>
    <div class="stack" style="gap:8px">${FLAGS.map(([k, n, d]) => `<label class="check"><input type="checkbox" data-change="flag" data-k="${k}" ${flags[k] ? 'checked' : ''}><span><b style="font-weight:560">${n}</b><br><span class="tiny">${d}</span></span></label>`).join('')}</div></div>`;
}
// The instance information: a description, a contact and the rules, which people see at sign-up and on the
// about page (docs/federation.md, section 10). The whole document is saved at once, against the version it
// was read at, so a save made over another admin's newer one is refused rather than undoing theirs. Each rule
// has a key of its own on this page (k), so its controls keep naming the same rule when the list is put in
// another order, and a Remove that went with its rule is never taken for the next row's.
// doc is the version as saved, draft what is in the form; saving is true while a save is on its way, and
// theirs holds another admin's version when a save met it (409) and the admin has not yet chosen.
const ied = { doc: null, draft: null, busy: false, n: 0, saving: false, theirs: null, who: 0 };
// Unsaved text is kept when the admin goes to another page of the app and comes back, as the skill map
// editor keeps its draft; with nothing unsaved the tab reads the saved version afresh each time it opens.
// Leaving or reloading the page with unsaved text asks first, as the rest of the app does.
window.addEventListener('hashchange', () => { if (!instDirty() && !ied.saving) ied.doc = ied.draft = ied.theirs = null; });
window.addEventListener('beforeunload', e => { if (instDirty()) e.preventDefault(); });
const instPlain = d => JSON.stringify([d.description, d.contact, d.rules.map(r => [r.text, r.hint])]);
const instDirty = () => !!ied.draft && !!ied.doc && instPlain(ied.draft) !== instPlain(ied.doc);
const INST_CLASH = 'Someone else saved the instance information while you were editing it. Your text is still in the form: load their version, or save yours over it.';
function setInstance(d) {
  ied.doc = d;
  ied.draft = { description: d.description, contact: d.contact, rules: d.rules.map(r => ({ text: r.text, hint: r.hint, k: ++ied.n })) };
}
async function loadInstance() {
  ied.busy = true;
  const r = await api('admin/instance');
  ied.busy = false;
  if (!r.ok) { toast(r.error); return; }
  setInstance(r.data); render();
}
function adminInstance() {
  if (ied.who !== me.id) { ied.who = me.id; ied.doc = ied.draft = ied.theirs = null; } // another admin signed in on this page
  if (!ied.doc) { if (!ied.busy) loadInstance(); return '<div class="card"><div class="empty small">Loading…</div></div>'; }
  const d = ied.draft, L = ied.doc.limits, n = d.rules.length, at = ied.doc.updatedAt;
  const rule = (r, i) => `<li class="inst-rule">
      <div class="stack" style="gap:4px"><label class="field">Rule ${i + 1}<input type="text" data-ed="inst-rule" data-f="text" data-k="${r.k}" value="${esc(r.text)}" ${countAttrs('inst-rt-' + r.k, r.text, L.ruleText)}></label>${countHTML('inst-rt-' + r.k, r.text, L.ruleText)}</div>
      <div class="stack" style="gap:4px"><label class="field">Explanation of rule ${i + 1} (optional)<textarea rows="2" data-ed="inst-rule" data-f="hint" data-k="${r.k}" ${countAttrs('inst-rh-' + r.k, r.hint, L.ruleHint)}>${esc(r.hint)}</textarea></label>${countHTML('inst-rh-' + r.k, r.hint, L.ruleHint)}</div>
      <div class="row wrap" style="gap:6px"><button type="button" class="btn sm" data-act="inst-up" data-k="${r.k}" aria-label="Move rule ${i + 1} up" ${i ? '' : 'disabled'}>Up</button><button type="button" class="btn sm" data-act="inst-down" data-k="${r.k}" aria-label="Move rule ${i + 1} down" ${i < n - 1 ? '' : 'disabled'}>Down</button><button type="button" class="btn sm danger" data-act="inst-rule-rm" data-k="${r.k}" aria-label="Remove rule ${i + 1}">Remove</button></div></li>`;
  return `<form class="card" id="inst-form"><div class="card-h"><h2 class="grow">About this Flowa</h2><span class="sub">Shown at sign-up and on the about page</span></div>
    <div class="stack" style="gap:14px">
      <p class="small muted">Nothing here shows anywhere until it is saved. Everything is plain text, shown as it is typed. A description also opens the about page to people who are not signed in.</p>
      <div class="stack" style="gap:4px"><label class="field">Description<textarea name="description" rows="4" ${countAttrs('inst-dc', d.description, L.description)}>${esc(d.description)}</textarea></label>${countHTML('inst-dc', d.description, L.description)}</div>
      <div class="stack" style="gap:4px"><label class="field">Contact<input type="text" name="contact" value="${esc(d.contact)}" placeholder="An email address, a link that starts with https://, or a short text" autocapitalize="none" spellcheck="false" ${countAttrs('inst-ct', d.contact, L.contact)}></label>${countHTML('inst-ct', d.contact, L.contact)}</div>
      <fieldset class="inst-fs"><legend>Rules</legend><p class="tiny">Shown in this order above the button that creates an account, and on the about page. At most ${L.rules}.</p>
        ${n ? `<ol class="inst-rules">${d.rules.map(rule).join('')}</ol>` : '<p class="small muted">No rules yet.</p>'}
        <div><button type="button" class="btn sm" data-act="inst-add" ${n >= L.rules ? 'disabled' : ''}>${icon('plus')}Add a rule</button></div></fieldset>
      <p class="form-err" id="inst-err" role="alert">${ied.theirs ? INST_CLASH : ''}</p>
      ${ied.theirs ? `<div class="row wrap inst-clash" role="group" aria-label="Someone else saved" style="gap:8px"><button type="button" class="btn sm" data-act="inst-theirs">Load their version</button><button type="button" class="btn sm" data-act="inst-mine">Save mine over it</button><span class="tiny">Theirs was saved ${esc(fmtWhen(ied.theirs.updatedAt))}${ied.theirs.updatedBy ? ` by @${esc(ied.theirs.updatedBy)}` : ''}.</span></div>` : ''}
      <div class="row wrap" style="gap:10px"><button class="btn primary" type="submit" id="inst-save" ${instCanSave() ? '' : 'disabled'}>${ied.saving ? 'Saving…' : 'Save'}</button><span class="tiny">${at ? `Last saved ${esc(fmtWhen(at))}${ied.doc.updatedBy ? ` by @${esc(ied.doc.updatedBy)}` : ''}.` : 'Nothing has been saved yet.'}</span></div>
    </div></form>`;
}
// The control a refusal is about, from the field the server names ("description", "rules.2.text").
function instField(f, field) {
  const m = /^rules\.(\d+)\.(text|hint)$/.exec(field || ''), r = m && ied.draft.rules[Number(m[1])];
  if (r) return f.querySelector(`[data-ed="inst-rule"][data-f="${m[2]}"][data-k="${r.k}"]`);
  if (field === 'rules') return f.querySelector('[data-act="inst-add"]');
  return field ? f.elements[field] : null;
}
// The page is not drawn again under a text box that has the cursor (render), but a change the editor makes
// itself (a rule added, a save, another admin's version) must show: the box lets go first, and then gets the
// focus back, found again by its name or its rule's key, with the cursor where it was. On a touch screen it is
// left alone, as the keyboard code does, since a box with the focus brings the keyboard up again.
function instDraw() {
  const a = document.activeElement, box = a && $('#inst-form')?.contains(a) && /^(INPUT|TEXTAREA)$/.test(a.tagName) ? a : null;
  const sel = box && (box.dataset.k ? `[data-ed="inst-rule"][data-f="${box.dataset.f}"][data-k="${box.dataset.k}"]` : `[name="${box.name}"]`);
  const at = box && [box.selectionStart, box.selectionEnd];
  box?.blur();
  render();
  const again = sel && $('#inst-form ' + sel);
  if (again && !coarse()) { again.focus(); try { again.setSelectionRange(...at); } catch { /* a box that has no cursor */ } }
}
// Save is offered when the form differs from the saved version, no save is on its way, and no clash with
// another admin's save waits for a choice (its own two buttons are the way on then).
const instCanSave = () => !ied.saving && !ied.theirs && instDirty();
const instSaveState = () => { const b = $('#inst-save'); if (b) b.disabled = !instCanSave(); };
// Sends the form against the version it was made on, or, with over, against the version of the admin who
// saved in between, which writes over theirs on purpose (audited as any save). What is typed while the save is
// on its way stays in the form: when the draft changed meanwhile, it is kept over the new version, still
// unsaved, so the next save goes against the right version and loses nothing.
async function instSave(over) {
  const dr = ied.draft, base = over ? ied.theirs : ied.doc;
  if (!dr || !base || ied.saving || (!over && !instCanSave())) return;
  const sent = instPlain(dr);
  ied.saving = true; instSaveState();
  const r = await api('admin/instance', 'PUT', { description: dr.description, contact: dr.contact, rules: dr.rules.map(x => ({ text: x.text, hint: x.hint })), updatedAt: base.updatedAt });
  ied.saving = false;
  if (r.status === 409 && r.data?.limits) {
    // Nothing of theirs is undone unseen, and nothing typed here is thrown away: the admin chooses.
    ied.theirs = r.data; instDraw(); showErr('inst-err', INST_CLASH);
    if (document.activeElement === document.body) $('[data-act=inst-theirs]')?.focus();
    return;
  }
  if (!r.ok) { const f = $('#inst-form'), el = f && instField(f, r.data?.field); showErr('inst-err', r.error, el ? [el] : []); el?.focus(); instSaveState(); return; }
  ied.theirs = null;
  if (instPlain(ied.draft) === sent) setInstance(r.data); else ied.doc = r.data;
  // This admin's own menu and about page follow at once, as everyone else's do on their next visit.
  const d = r.data;
  inst = d.description || d.contact || d.rules.length ? { about: true } : null; about.data = null;
  instDraw(); toast(instDirty() ? 'Saved. What you typed since is not saved yet.' : 'Saved. It shows at sign-up and on the about page.');
}
// Moves a rule one place and keeps the keyboard on the button that moved it, or on the other one when it
// reached an end and this one is off.
function instMove(k, by) {
  const l = ied.draft.rules, i = l.findIndex(r => r.k === k), j = i + by;
  if (i < 0 || j < 0 || j >= l.length) return;
  [l[i], l[j]] = [l[j], l[i]];
  instDraw();
  const b = $(`[data-act="${by < 0 ? 'inst-up' : 'inst-down'}"][data-k="${k}"]`);
  (b && !b.disabled ? b : $(`[data-act="${by < 0 ? 'inst-down' : 'inst-up'}"][data-k="${k}"]`))?.focus();
  say(`Rule ${i + 1} is now rule ${j + 1}.`);
}
// The accounts list is never kept from one visit to the next: it is fetched when the tab opens
// and again after every action on an account, so a row cannot say Active after a suspension.
async function loadUsers() {
  const r = await api('admin/users');
  if (!r.ok) { toast(r.error); return; }
  ui.users = r.data.users;
  // The list also says whether this admin is still the owner, which decides what the rows offer.
  const self = ui.users.find(u => u.id === me.id);
  if (self) me.owner = !!self.owner;
  render();
}
window.addEventListener('hashchange', () => { ui.users = null; ui.rolePick = null; });
// The server calls the plain role "user"; on the page a person with it is a member, as everywhere else.
const RANK_NAMES = { user: 'Member', moderator: 'Moderator', admin: 'Admin' };
const ROLE_ASK = { user: 'a member', moderator: 'a moderator', admin: 'an admin' };
function adminAccounts() {
  if (!ui.users) { loadUsers(); return '<div class="card"><div class="empty small">Loading accounts…</div></div>'; }
  const f = ui.userFilter || { q: '', role: '', status: '' }, q = f.q.trim().toLowerCase();
  const status = u => u.suspendedUntil ? 'suspended' : u.mutedUntil ? 'muted' : 'active';
  const list = ui.users.filter(u => (!q || u.username.includes(q) || (u.displayName || '').toLowerCase().includes(q)) && (!f.role || u.role === f.role) && (!f.status || status(u) === f.status));
  const rows = list.map(u => {
    const self = u.id === me.id, st = status(u), outrank = { user: 0, moderator: 1, admin: 2 }[me.role] > { user: 0, moderator: 1, admin: 2 }[u.role];
    // A role is chosen and then confirmed. The list is offered only where the server would take the
    // change: not for yourself, not for the owner, and for another admin only to the owner.
    const pick = ui.rolePick?.id === u.id ? ui.rolePick.role : null;
    const ownerChip = u.owner ? '<span class="chip" data-tip="The one admin whose role cannot be changed. Only the owner changes another admin\'s role, and can hand ownership to another admin.">Owner</span>' : '';
    const role = canDo('user.role') && !self && !u.owner && (u.role !== 'admin' || me.owner)
      ? `<select data-change="user-role" data-id="${u.id}" aria-label="Role of @${esc(u.username)}">${Object.entries(RANK_NAMES).map(([k, n]) => `<option value="${k}" ${(pick || u.role) === k ? 'selected' : ''}>${n}</option>`).join('')}</select>${pick ? `<span class="role-ask" role="group" aria-label="Confirm the role change"><span class="small">Make @${esc(u.username)} ${ROLE_ASK[pick]}?</span><span class="row" style="gap:6px"><button class="btn sm primary" data-act="role-apply">Apply</button><button class="btn sm" data-act="role-cancel">Cancel</button></span></span>` : ''}`
      : `<span class="row wrap" style="gap:4px"><span class="chip ${u.role === 'user' ? '' : 'ok'}">${RANK_NAMES[u.role]}</span>${ownerChip}</span>`;
    const stChip = st === 'active' ? '<span class="tiny">Active</span>' : `<span class="chip warn" data-tip="${esc(st === 'muted' ? u.muteReason : u.suspendReason)}">${st === 'muted' ? 'Muted' : 'Suspended'} until ${esc(fmtWhen(st === 'muted' ? u.mutedUntil : u.suspendedUntil))}</span>`;
    const modBtns = flags.moderation && !self && outrank ? `${canDo('user.mute') ? (u.mutedUntil ? `<button class="btn sm" data-act="mod-do" data-do="unmute" data-kind="user" data-id="${u.id}">Unmute</button>` : `<button class="btn sm" data-act="mod-open" data-do="mute" data-kind="user" data-id="${u.id}" data-name="${esc(u.username)}">Mute…</button>`) : ''}${canDo('user.suspend') ? (u.suspendedUntil ? `<button class="btn sm" data-act="mod-do" data-do="unsuspend" data-kind="user" data-id="${u.id}">Lift suspension</button>` : `<button class="btn sm" data-act="mod-open" data-do="suspend" data-kind="user" data-id="${u.id}" data-name="${esc(u.username)}">Suspend…</button>`) : ''}${canDo('badge.revoke') ? `<button class="btn sm" data-act="mod-do" data-do="${u.statsHidden ? 'show_stats' : 'hide_stats'}" data-kind="user" data-id="${u.id}">${u.statsHidden ? 'Show stats' : 'Hide stats'}</button>` : ''}` : '';
    const mgmt = canDo('user.manage') ? `<button class="btn sm" data-act="user-pw" data-id="${u.id}" data-name="${esc(u.username)}">Reset password</button>${self || u.owner ? '' : `<button class="btn sm danger" data-act="user-del" data-id="${u.id}" data-name="${esc(u.username)}">Delete</button>`}` : '';
    const hand = me.owner && !self && u.role === 'admin' && !u.suspendedUntil ? `<button class="btn sm" data-act="user-owner" data-id="${u.id}" data-name="${esc(u.username)}">Make owner</button>` : '';
    return `<tr data-user="${esc(u.username)}"><td><span class="row" style="gap:8px">${avatarHTML(u)}<span><a href="#/u/${esc(u.username)}"><b style="font-weight:560">${esc(u.displayName)}</b></a><br><span class="tiny">@${esc(u.username)}</span></span></span></td>
      <td>${role}</td><td>${stChip}</td><td class="small" style="white-space:nowrap">${u.lastActive ? esc(new Date(u.lastActive).toLocaleDateString()) : '<span class="tiny">Never</span>'}</td>
      <td><span class="row wrap" style="gap:6px;justify-content:flex-end">${modBtns}${hand}${mgmt}</span></td></tr>`;
  }).join('');
  return `<div class="card"><div class="row wrap" style="gap:8px;margin-bottom:12px">
      <input type="search" id="user-q" class="grow" placeholder="Search by name" value="${esc(f.q)}" aria-label="Search accounts" style="min-width:160px">
      <select data-change="user-filter" data-k="role" aria-label="Role" style="width:auto"><option value="">Every role</option>${Object.entries(RANK_NAMES).map(([k, n]) => `<option value="${k}" ${f.role === k ? 'selected' : ''}>${n}</option>`).join('')}</select>
      <select data-change="user-filter" data-k="status" aria-label="Status" style="width:auto"><option value="">Any status</option>${['active', 'muted', 'suspended'].map(k => `<option value="${k}" ${f.status === k ? 'selected' : ''}>${k[0].toUpperCase() + k.slice(1)}</option>`).join('')}</select>
      <span class="sub tiny">${list.length} of ${ui.users.length}</span></div>
    <div class="table-wrap"><table class="table"><thead><tr><th>Member</th><th>Role</th><th>Status</th><th>Last active</th><th></th></tr></thead><tbody>${rows || '<tr><td colspan="5" class="empty small">No accounts match.</td></tr>'}</tbody></table></div></div>`;
}
const ACTION_NAMES = { hide: 'Hid', restore: 'Restored', mute: 'Muted', unmute: 'Unmuted', suspend: 'Suspended', unsuspend: 'Lifted suspension of', revoke_badge: 'Revoked a badge of', hide_stats: 'Hid stats of', show_stats: 'Showed stats of', resolve: 'Resolved reports on', role: 'Changed role of', owner: 'Handed ownership to', flags: 'Changed features', delete_user: 'Deleted account', reset_password: 'Reset password of', delete_post: 'Deleted a post by', delete_comment: 'Deleted a comment by', catalog_publish: 'Published a skill map', catalog_restore: 'Restored a skill map', catalog_draft: 'Saved a skill map draft', instance: 'Changed the instance information' };
async function loadAudit(more) {
  const f = mod.filter, before = more && mod.audit?.length ? mod.audit[mod.audit.length - 1].id : 0;
  const r = await api(`admin/audit?actor=${encodeURIComponent(f.actor)}&action=${encodeURIComponent(f.action)}&before=${before}`);
  if (!r.ok) { toast(r.error); return; }
  mod.audit = more ? [...mod.audit, ...r.data.items] : r.data.items; mod.auditMore = r.data.more;
  render();
}
function adminAudit() {
  if (!mod.audit) { loadAudit(); return '<div class="card"><div class="empty small">Loading…</div></div>'; }
  const detail = e => {
    const d = e.detail || {};
    if (e.action === 'role') return `${esc(d.from)} to ${esc(d.to)}`;
    if (e.action === 'owner') return `from @${esc(d.from)}`;
    if (e.action === 'flags') return Object.entries(d).map(([k, v]) => `${esc(k)} ${v ? 'on' : 'off'}`).join(', ');
    if (e.action === 'instance') return `${(d.parts || []).map(esc).join(', ')}; ${d.rules} ${d.rules === 1 ? 'rule' : 'rules'}`;
    if (e.action === 'resolve') return `${esc(d.outcome)}, ${d.reports} ${d.reports === 1 ? 'report' : 'reports'}`;
    if (d.until) return `until ${esc(fmtWhen(d.until))}`;
    if (d.key) return esc(d.key);
    if (d.prop) return `${esc(d.prop)}, revision ${d.rev}`;
    return '';
  };
  const rows = mod.audit.map(e => `<tr><td class="small" style="white-space:nowrap">${esc(fmtWhen(e.at))}</td><td class="small">@${esc(e.actor || 'deleted')}</td>
    <td class="small">${esc(ACTION_NAMES[e.action] || e.action)} ${e.target ? '@' + esc(e.target) : e.kind && e.kind !== 'user' ? esc(e.kind) + ' ' + e.targetId : ''}</td><td class="small muted">${detail(e)}</td><td class="small">${esc(e.reason)}</td></tr>`).join('');
  return `<div class="card"><form class="row wrap" id="audit-form" style="gap:8px;margin-bottom:12px"><input type="search" name="actor" placeholder="Who acted (username)" value="${esc(mod.filter.actor)}" style="width:200px" aria-label="Who acted">
      <select name="action" style="width:auto" aria-label="Action"><option value="">Every action</option>${Object.entries(ACTION_NAMES).map(([k, n]) => `<option value="${k}" ${mod.filter.action === k ? 'selected' : ''}>${n.replace(/ (of|on|by|to)$/, '')}</option>`).join('')}</select><button class="btn sm" type="submit">Filter</button></form>
    <div class="table-wrap"><table class="table"><thead><tr><th>When</th><th>Who</th><th>What</th><th>Detail</th><th>Reason</th></tr></thead><tbody>${rows || '<tr><td colspan="5" class="empty small">Nothing recorded yet.</td></tr>'}</tbody></table></div>
    ${mod.auditMore ? '<button class="btn sm" data-act="audit-more" style="margin-top:10px">Load more</button>' : ''}</div>`;
}
// Filled in by the dashboard and skill map editor further down.
// What each instance mode means, in the words the dashboard shows (FLOWA_MODE, docs/federation.md).
const MODES = { local: 'a community that lives on this server only, which is what FLOWA_MODE means when it is not set' };
let adminDashboard = () => {
  if (!mod.dash) { loadDash(); return '<div class="card"><div class="empty small">Loading…</div></div>'; }
  const t = mod.dash.totals, W = mod.dash.weeks;
  const tile = (label, value, foot) => `<div class="card stat"><div class="lbl">${label}</div><div class="val num">${value.toLocaleString()}</div><div class="foot">${foot}</div></div>`;
  const charts = [['sessions', 'Practice sessions logged'], ['posts', 'Posts'], ['comments', 'Comments and replies'], ['reports', 'Reports']].map(([k, n]) => weekChart(W, k, n)).join('');
  return `<div class="grid g4" style="margin-bottom:16px">${tile('Members', t.users, `${t.moderators} ${t.moderators === 1 ? 'moderator' : 'moderators'}, ${t.admins} ${t.admins === 1 ? 'admin' : 'admins'}`)}${tile('Active in 7 days', t.active7, 'Signed in or saved progress')}${tile('Active in 30 days', t.active30, `${t.users ? Math.round(100 * t.active30 / t.users) : 0}% of members`)}${tile('Open reports', t.openReports, flags.moderation ? '<a href="#/mod">Open the queue</a>' : 'Moderation is off')}</div>
  ${flags.suggest && mod.dash.suggest ? (() => { const u = mod.dash.suggest, pct = (a, b) => b ? Math.round(100 * a / b) + '%' : 'none yet'; const gate = u.opens === 'now' ? `of ${u.gate} needed: the suggestion weights can be fitted now` : u.opens ? `of ${u.gate} needed. ${u.recent} new in the last four weeks; at that pace, the week of ${parseDay(u.opens).toLocaleDateString(undefined, { day: 'numeric', month: 'long', year: 'numeric' })}` : `of ${u.gate} needed. None were first marked in the last four weeks, so there is no date yet`;
    return `<div class="grid g3 dash-suggest" style="margin-bottom:16px">${tile('Suggestions added to a combo', u.added, `of ${u.shown} shown in 12 weeks (${pct(u.added, u.shown)})`)}${tile('Added suggestions later marked Works', u.worked, `of ${u.added} added (${pct(u.worked, u.added)})`)}${tile('Pairs of skills with a shared mark', u.pairs, gate)}</div>`; })() : ''}
  <div class="grid g2 dash-charts">${charts}</div>
  <details class="card dash-table"><summary class="small">Show the weekly numbers as a table</summary><div class="table-wrap"><table class="table"><thead><tr><th>Week of</th><th>Sessions</th><th>Posts</th><th>Comments</th><th>Reports</th></tr></thead><tbody>
    ${W.map(w => `<tr><td>${esc(fmtWeek(w.week))}</td><td class="num">${w.sessions}</td><td class="num">${w.posts}</td><td class="num">${w.comments}</td><td class="num">${w.reports}</td></tr>`).join('')}</tbody></table></div></details>
  <p class="tiny dash-version" style="margin-top:12px">Flowa version <span class="num">${esc(mod.dash.version || 'dev')}</span>: what VERSION in the environment says this build is.</p>
  <p class="tiny dash-mode">Instance mode <span class="num">${esc(mod.dash.mode || 'local')}</span>: ${esc(MODES[mod.dash.mode || 'local'] || 'what FLOWA_MODE in the environment says')}.</p>`;
};
const fmtWeek = k => parseDay(k).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
async function loadDash() {
  const r = await api('admin/dashboard');
  if (!r.ok) { toast(r.error); return; }
  mod.dash = r.data; render();
}
// One measure over 12 weeks as columns: one hue, a hairline baseline, values on the latest and the
// highest week only, every week in a tooltip and in the table below.
function weekChart(W, key, title) {
  const vals = W.map(w => w[key]), max = Math.max(...vals), total = vals.reduce((a, b) => a + b, 0);
  const H = 120, top = 18, base = top + H, slot = 26, bw = 20, left = 4, width = left * 2 + slot * W.length;
  const peak = vals.lastIndexOf(max), lastI = W.length - 1;
  const cols = W.map((w, i) => {
    const v = vals[i], h = max ? Math.max(v ? 3 : 0, Math.round(H * v / max)) : 0, x = left + i * slot + (slot - bw) / 2, y = base - h;
    const r = Math.min(4, h / 2), d = h ? `M${x} ${base}V${y + r}Q${x} ${y} ${x + r} ${y}H${x + bw - r}Q${x + bw} ${y} ${x + bw} ${y + r}V${base}Z` : '';
    const tip = `Week of ${fmtWeek(w.week)}: ${v}`;
    const label = v && (i === lastI || i === peak) ? `<text class="dc-val" x="${x + bw / 2}" y="${y - 5}">${v}</text>` : '';
    return `<g class="dc-col" data-tip="${esc(tip)}"><rect class="dc-hit" x="${left + i * slot}" y="${top - 14}" width="${slot}" height="${H + 14}"></rect>${d ? `<path class="dc-bar ${i === lastI ? 'now' : ''}" d="${d}"></path>` : ''}${label}<title>${esc(tip)}</title></g>`;
  }).join('');
  return `<figure class="card dash-chart"><figcaption><h2>${title}</h2><span class="sub num">${total.toLocaleString()} in 12 weeks</span></figcaption>
    <svg viewBox="0 0 ${width} ${base + 18}" role="img" aria-label="${esc(title)} per week for 12 weeks, ${total} in total. The table below has every value.">${cols}
      ${total ? '' : `<text class="dc-axis dc-empty" x="${width / 2}" y="${top + H / 2}" text-anchor="middle">None in the last 12 weeks</text>`}
      <line class="dc-base" x1="0" x2="${width}" y1="${base + 0.5}" y2="${base + 0.5}"></line>
      <text class="dc-axis" x="${left}" y="${base + 14}" text-anchor="start">${esc(fmtWeek(W[0].week))}</text><text class="dc-axis" x="${width - left}" y="${base + 14}" text-anchor="end">This week</text></svg></figure>`;
}
let adminMaps = () => '<div class="card"><div class="empty small">The skill map editor is not built yet.</div></div>';

/* ---------- events ---------- */
document.addEventListener('click', async e => {
  const t = e.target.closest('[data-act]');
  if (!t) return;
  const d = t.dataset;
  switch (d.act) {
    case 'report': openReport(d.kind, Number(d.id), d.what); break;
    case 'mod-open': openAction({ ...d }); break;
    case 'mod-do': if (await modAction({ action: d.do, kind: d.kind, id: Number(d.id) })) { toast('Done.'); refreshAfterMod(); } break;
    case 'mod-resolve': {
      const note = d.outcome === 'dismissed' ? prompt('Why leave it up? (optional, for the record)') : '';
      if (note === null) break;
      const r = await api('mod/resolve', 'POST', { kind: d.kind, id: Number(d.id), outcome: d.outcome, note });
      if (!r.ok) { toast(r.error); break; }
      toast(d.outcome === 'actioned' ? 'Resolved. The reporters were thanked.' : 'Dismissed. The reporters were told.');
      refreshAfterMod(); break;
    }
    case 'audit-more': loadAudit(true); break;
    case 'inst-add': {
      const l = ied.draft?.rules;
      if (!l || l.length >= ied.doc.limits.rules) break;
      const k = ++ied.n;
      l.push({ text: '', hint: '', k }); instDraw(); instSaveState();
      $(`[data-ed="inst-rule"][data-f="text"][data-k="${k}"]`)?.focus();
      break;
    }
    case 'inst-theirs':
      if (!ied.theirs || ied.saving) break;
      setInstance(ied.theirs); ied.theirs = null; instDraw(); showErr('inst-err', '');
      $('#inst-form [name=description]')?.focus(); say('Their version is in the form.');
      break;
    case 'inst-mine': instSave(true); break;
    case 'inst-up': case 'inst-down': instMove(Number(d.k), d.act === 'inst-up' ? -1 : 1); break;
    case 'inst-rule-rm': {
      // The focus is put back by the keyboard code, which does not send it to the next row's Remove (wipes).
      const l = ied.draft?.rules, i = l ? l.findIndex(r => r.k === Number(d.k)) : -1;
      if (i < 0) break;
      l.splice(i, 1); instDraw(); instSaveState(); say(`Rule ${i + 1} removed.`);
      break;
    }
    case 'user-del':
      if (confirm(`Delete @${d.name} and all their progress? This cannot be undone.`)) {
        const r = await api('admin/users/' + d.id, 'DELETE');
        toast(r.ok ? 'Account deleted.' : r.error); await loadUsers();
      }
      break;
    case 'user-pw': {
      const next = prompt(`New password for @${d.name}: 8 characters or more, not a common password and not their username. They will be signed out everywhere.`);
      if (!next) break;
      const r = await api(`admin/users/${d.id}/password`, 'PUT', { next });
      toast(r.ok ? 'Password changed.' : r.error);
      loadUsers(); break;
    }
    case 'role-cancel': { const id = ui.rolePick?.id; ui.rolePick = null; render(); focusRole(id); break; }
    case 'role-apply': {
      const p = ui.rolePick;
      if (!p) break;
      ui.rolePick = null;
      const r = await api(`admin/users/${p.id}/role`, 'PUT', { role: p.role });
      toast(r.ok ? 'Role changed.' : r.error);
      await loadUsers(); focusRole(p.id); break;
    }
    case 'user-owner':
      if (confirm(`Hand ownership of this Flowa to @${d.name}? You stay an admin, but only @${d.name} can then change an admin's role or hand ownership back.`)) {
        const r = await api(`admin/users/${d.id}/owner`, 'PUT');
        toast(r.ok ? `@${d.name} is the owner now.` : r.error);
        await loadUsers();
      }
      break;
  }
});
document.addEventListener('change', async e => {
  const t = e.target, d = t.dataset;
  if (d.change === 'user-role') {
    // Choosing only asks: arrow keys and typing change a list's value as they go, so nothing is
    // sent until Apply. Choosing the role they have already takes the question away.
    const id = Number(d.id), u = ui.users?.find(x => x.id === id);
    ui.rolePick = u && u.role !== t.value ? { id, role: t.value } : null;
    render(); focusRole(id);
  } else if (t.name === 'reason' && t.form?.id === 'report-form') {
    t.form.querySelector('[type=submit]').disabled = false;
  } else if (d.change === 'user-filter') {
    ui.userFilter = { ...(ui.userFilter || { q: '', role: '', status: '' }), [d.k]: t.value }; render();
  }
});
// After the accounts table is drawn again, the keyboard stays on the role list it was on.
const focusRole = id => id && document.querySelector(`select[data-change="user-role"][data-id="${id}"]`)?.focus();
document.addEventListener('input', e => {
  const t = e.target;
  if (ied.draft && t.form?.id === 'inst-form') {
    if (t.dataset.ed === 'inst-rule') { const r = ied.draft.rules.find(x => x.k === Number(t.dataset.k)); if (r) r[t.dataset.f] = t.value; }
    else if (t.name === 'description' || t.name === 'contact') ied.draft[t.name] = t.value;
    instSaveState(); return;
  }
  if (e.target.id !== 'user-q') return;
  ui.userFilter = { ...(ui.userFilter || { q: '', role: '', status: '' }), q: e.target.value };
  const tb = $('.table tbody'), q = e.target.value.trim().toLowerCase();
  // Filter the rows in place, so typing keeps its focus.
  if (tb) for (const tr of tb.querySelectorAll('tr[data-user]')) tr.hidden = q && !tr.dataset.user.includes(q) && !tr.textContent.toLowerCase().includes(q);
});
document.addEventListener('submit', async e => {
  const f = e.target;
  if (f.id === 'report-form') {
    e.preventDefault();
    const r = await api('reports', 'POST', { kind: f.dataset.kind, id: Number(f.dataset.id), reason: f.reason.value, note: f.note.value });
    if (!r.ok) { showErr('rep-err', r.error, errFields(f, r.error, [[/\bnote\b/i, ['note']]])); return; }
    closeModal(); toast('Thank you. A moderator will look at it.');
  } else if (f.id === 'action-form') {
    e.preventDefault();
    const dd = f.dataset, body = { action: dd.do, kind: dd.kind, id: Number(dd.id), reason: f.reason.value };
    if (f.dur) { const h = DURATIONS.find(x => x[0] === f.dur.value)[2]; body.until = new Date(Date.now() + h * 36e5).toISOString().replace(/\.\d+Z$/, 'Z'); }
    const r = await api('mod/actions', 'POST', body);
    if (!r.ok) { showErr('act-err', r.error, errFields(f, r.error, [[/\breason\b/i, ['reason']]])); return; }
    closeModal(); toast('Done. They have been told.'); refreshAfterMod();
  } else if (f.id === 'inst-form') {
    e.preventDefault(); instSave(false);
  } else if (f.id === 'audit-form') {
    e.preventDefault();
    mod.filter = { actor: f.actor.value.trim(), action: f.action.value }; mod.audit = null; render();
  }
});
