'use strict';
// Accounts, props and onboarding. Loaded before app.js, which calls into it.
let me = null, perms = []; // perms: what the signed-in user may do, from the server
const docs = {}; // progress per prop for the signed-in user: { rev, state }

async function api(path, method = 'GET', body) {
  try {
    const opt = { method, cache: 'no-store' };
    if (method !== 'GET') { opt.headers = { 'Content-Type': 'application/json' }; opt.body = JSON.stringify(body ?? {}); }
    const r = await fetch('api/' + path, opt);
    let data = null;
    try { data = await r.json(); } catch { /* no body */ }
    return { ok: r.ok, status: r.status, data, error: data?.error || 'Something went wrong.' };
  } catch { return { ok: false, status: 0, data: null, error: 'Could not reach the Pi.' }; }
}

const propById = id => PROPS.find(p => p.id === id);
// A signed-in user sees the props they chose; everyone else sees all of them.
function myProps() {
  const mine = me ? PROPS.filter(p => (me.props || []).includes(p.id)) : [];
  return mine.length ? mine : PROPS;
}
const curProp = () => propById(ui.prop) || PROPS[0];
// Run fn against another prop's progress, for totals shown outside that prop.
function inProp(id, fn) {
  const d = id === ui.prop ? { state } : docs[id];
  if (!d) return null;
  const keep = state, cat = CID;
  state = d.state; useCatalog(id);
  try { return fn(); } finally { state = keep; useCatalog(cat); }
}

async function loadSession() {
  const r = await api('me');
  if (!r.ok) throw new Error(r.status);
  me = r.data.user; flags = r.data.flags || {}; perms = r.data.perms || [];
  setAchievements(r.data.achievements);
  for (const k in docs) delete docs[k];
  if (!myProps().some(p => p.id === ui.prop)) ui.prop = myProps()[0].id;
  useCatalog(ui.prop);
  if (!me) { state = blank(); rev = 0; return; }
  await Promise.all(myProps().map(async p => {
    const d = await api('progress/' + p.id);
    if (!d.ok) throw new Error(d.status);
    docs[p.id] = { rev: d.data.rev, state: normalize(d.data.state) };
    docs[p.id].migrated = applyAliases(docs[p.id].state, p.id);
  }));
  state = docs[ui.prop].state; rev = docs[ui.prop].rev;
  // Progress moved from renamed skill ids is saved for the prop in use now, and for the others when
  // they are switched to (switchProp).
  if (docs[ui.prop].migrated) { docs[ui.prop].migrated = false; setTimeout(commit, 0); }
}
async function switchProp(id) {
  if (id === ui.prop || !propById(id)) return;
  if (dirty) { clearTimeout(saveTimer); await save(); }
  if (me) {
    docs[ui.prop] = { rev, state };
    if (!docs[id]) {
      const d = await api('progress/' + id);
      if (!d.ok) { toast(d.error); return; }
      docs[id] = { rev: d.data.rev, state: normalize(d.data.state) };
      docs[id].migrated = applyAliases(docs[id].state, id);
    }
    state = docs[id].state; rev = docs[id].rev;
    // Progress moved from renamed skill ids at load is saved once this prop is the one in use.
    if (docs[id].migrated) { docs[id].migrated = false; setTimeout(commit, 0); }
  }
  ui.prop = id; ui.draftFocus = [];
  useCatalog(id);
  if (!ui.open.includes(id)) ui.open.push(id);
  localStorage.setItem('fl-prop', id); localStorage.setItem('fl-groups', ui.open.join(','));
  render();
}
async function updateMe(patch, done) {
  const r = await api('me', 'PUT', patch);
  if (r.ok) { me = r.data.user; if (done) toast(done); } else toast(r.error);
  return r.ok;
}

/* ---------- small views ---------- */
function avatarHTML(u, cls = '') {
  if (u?.avatar) return `<img class="avatar ${cls}" src="${esc(u.avatar)}" alt="">`;
  const name = u ? (u.displayName || u.username) : '';
  const initials = name.split(/\s+/).map(w => w[0]).slice(0, 2).join('').toUpperCase();
  return `<span class="avatar ${cls}" aria-hidden="true">${u ? esc(initials) : icon('user')}</span>`;
}
function propTabs() {
  const list = myProps();
  if (list.length < 2) return '';
  return `<div class="seg prop-tabs" role="group" aria-label="Prop">${list.map(p => `<button class="${p.id === ui.prop ? 'on' : ''}" data-act="prop" data-v="${p.id}" aria-pressed="${p.id === ui.prop}">${esc(p.name)}</button>`).join('')}</div>`;
}
const propChecks = picked => `<div class="prop-grid">${PROPS.map(p => `<label class="prop-pick"><input type="checkbox" name="prop" value="${p.id}" ${picked.includes(p.id) ? 'checked' : ''}>
  <span><b>${esc(p.name)}</b><span class="tiny">${esc(p.kind)}</span><span class="chip ${p.ready ? 'ok' : ''}">${p.ready ? 'Skill map ready' : 'Practice log only, for now'}</span></span></label>`).join('')}</div>`;

// First-visit notes, shown once per section and remembered on the account.
const TIPS = {
  dashboard: ['Your dashboard', ['It shows what is clean, your practice streak, and the next gate.', '"Ready to learn" lists skills whose prerequisites you have met.', 'Switch props with the tabs at the top if you use more than one.']],
  maps: ['Skill maps', ['Each prop has its own map. Pick a prop card to see it.', 'Stronger blue means further along. Dashed cells are locked behind prerequisites.', 'Skills marked unconfirmed could not be checked against a source, so treat them with care.']],
  branch: ['Branches and skills', ['Arrows run from a prerequisite to the skill it unlocks.', 'Click a skill for its guide, demo and references, and to set your level.', 'Level 2, "Clean", is what unlocks the next skills.']],
  practice: ['Practice log', ['Log each session with its minutes and the skills you worked on.', 'Sessions feed the heatmap and the streak on your dashboard.']],
  combos: ['Combos', ['Build a sequence of skills, then mark each transition as you test it.', 'A combo is ready only when every transition works.']],
  plan: ['Plan', ['Tick a gate when you pass its test. Gates decide your current phase.', 'The 12-week track is for turning skills you already have into one routine.']],
  profile: ['Your profile', ['Add a picture, a bio and your social links, and choose the props you use.', 'There is no email on file, so a forgotten password has to be reset by the admin.']],
  timeline: ['Timeline', ['Post what you worked on, with a picture or a tagged skill. Like and comment on others\u2019 posts.', 'Your milestones post themselves; turn that off in your profile.', 'The bell at the top shows likes, comments, mentions and new badges.']],
  achievements: ['Achievements', ['XP comes from skill levels, sessions, gates, combos and weekly challenges. Levels count every prop.', 'Your streak counts a practice session on any prop. Share it as a PNG overlay for your own photo.']],
};
function tipCard(k) {
  if (flags.tour && !tourBroken) return ''; // the guided tour covers it
  if (!me || !TIPS[k] || (me.seen || []).includes(k)) return '';
  const [title, lines] = TIPS[k];
  return `<div class="tipcard" role="note"><div class="grow"><b>${title}</b><ul>${lines.map(l => `<li>${l}</li>`).join('')}</ul></div><button class="btn sm" data-act="tip-ok" data-k="${k}">Got it</button></div>`;
}

/* ---------- guided tours (React Joyride, in tour.js) ---------- */
// Each step: t target selector, n title, c text. nav marks steps inside the sidebar,
// which is a drawer on phones and has to be opened first.
const TOURS = {
  dashboard: [
    { t: 'body', n: 'Welcome to Flowa', c: 'This is a one-minute tour of where things are. You can skip it, and replay it later from your profile.' },
    { t: '.nav-profile', nav: 1, n: 'Your profile', c: 'Your picture, name, props and password are here. So is Sign out.' },
    { t: '#side a[href="#/maps"]', nav: 1, n: 'Skill maps', c: 'One map per prop, showing every skill and what it unlocks.' },
    { t: '#side a[href="#/practice"]', nav: 1, n: 'Practice, Combos and Plan', c: 'Log sessions in Practice, test sequences in Combos, and follow the gates in Plan.' },
    { t: '.nav-group', nav: 1, n: 'Your props', c: 'The props you chose are listed here. Each one opens to show its branches.' },
    { t: '.search-btn', n: 'Search', c: 'Press Ctrl K or / to jump to any skill or page.' },
    { t: '.prop-tabs', n: 'Switch props', c: 'Each prop has its own dashboard, practice log, combos and plan.' },
    { t: '[data-tour=stats]', n: 'At a glance', c: 'Clean skills, practice this week, your streak, and gates passed.' },
    { t: '[data-tour=next]', n: 'What to do next', c: 'Your next gate, and the skills whose prerequisites you have met.' },
  ],
  maps: [
    { t: '.prop-cards', n: 'Pick a prop', c: 'Each card is one of your props. Select one to see its map.' },
    { t: '.map-wrap', n: 'The map', c: 'Columns are branches and rows are tiers. Stronger blue means further along. Click any cell to open that skill.' },
  ],
  branch: [
    { t: '.seg[aria-label=View]', n: 'Graph or list', c: 'Switch between the dependency graph and a plain table.' },
    { t: '.graph-wrap', n: 'Prerequisites', c: 'Arrows run from a skill to what it unlocks. Dashed boxes are locked, and blue outlines are ready to learn.' },
    { t: '.graph .node', n: 'Open a skill', c: 'Click a skill for its guide, demo and references, and to set your level. Level 2, Clean, unlocks the next skills.' },
  ],
  practice: [
    { t: '#log-form', n: 'Log a session', c: 'Enter the minutes and the skills you worked on. Notes are for what to fix next time.' },
    { t: '[data-tour=sessions]', n: 'Your sessions', c: 'Every session is listed here. They feed the heatmap and the streak on your dashboard.' },
  ],
  combos: [
    { t: '#combo-form', n: 'Build a combo', c: 'Name it, then add skills in order. Mark each transition as you test it: a combo is ready only when every transition works.' },
  ],
  plan: [
    { t: '.phase', n: 'Phases and gates', c: 'Tick a gate when you pass its test. Gates decide which phase you are in.' },
    { t: '[data-tour=track]', n: 'Contest track', c: 'An optional 12-week track for turning skills you already have into one routine.' },
  ],
  timeline: [
    { t: '#compose-form', n: 'Post', c: 'Say what you worked on, add a picture or tag a skill. Use @name to mention someone.' },
    { t: '[data-tour=people]', n: 'People', c: 'Everyone here, by XP. Open a profile to see their bio, links and badges.' },
    { t: '#bell', n: 'Notifications', c: 'Likes, comments, mentions and new badges arrive here.' },
  ],
  achievements: [
    { t: '[data-tour=level]', n: 'Level and XP', c: 'XP from every prop adds up to one level.' },
    { t: '[data-tour=streak]', n: 'Streak', c: 'A session on any prop keeps it going. Share it as an overlay for your own photo or video.' },
    { t: '[data-tour=challenges]', n: 'Weekly challenges', c: 'Four challenges a week, Monday to Sunday, each worth XP.' },
  ],
  profile: [
    { t: '.avatar.big', n: 'Your picture', c: 'Upload a picture. It is cropped to a square and shown in the menu.' },
    { t: '#about-form', n: 'About you', c: 'A bio and your Instagram, TikTok and YouTube, shown on your public profile.' },
    { t: '#props-form', n: 'Your props', c: 'Choose which props Flowa shows you. Your progress on the others is kept.' },
    { t: '#password-form', n: 'Password', c: 'There is no email on file, so only the admin can reset a forgotten password.' },
  ],
};
let tourBusy = false, tourBroken = false;
function loadTour() {
  if (window.FlowaTour) return Promise.resolve();
  return new Promise((ok, bad) => {
    const s = document.createElement('script');
    s.src = 'tour.js'; s.onload = ok; s.onerror = bad;
    document.head.appendChild(s);
  });
}
const tourKeyNow = () => { const [p, , b] = parseRoute(); return p === 'branch' && b ? '' : !p ? 'dashboard' : p === 'prop' ? 'maps' : p; };
// Starts the tour for a section the first time the user opens it.
async function maybeTour(key) {
  const replay = ui.replay && key === 'dashboard';
  if (!me || !flags.tour || tourBusy || tourBroken || !TOURS[key]) return;
  if (!replay && (me.seen || []).includes(key)) return;
  tourBusy = true; ui.replay = false;
  try { await loadTour(); } catch {
    // Without the bundle, fall back to the short notes.
    tourBroken = true; tourBusy = false; render(); return;
  }
  await new Promise(r => setTimeout(r, 350)); // let the page settle
  if (tourKeyNow() !== key || !me) { tourBusy = false; return; }
  const narrow = matchMedia('(max-width: 820px)').matches;
  const drawer = want => {
    if (document.body.classList.contains('nav-open') === want) return Promise.resolve();
    document.body.classList.toggle('nav-open', want);
    return new Promise(r => setTimeout(r, 280));
  };
  const steps = TOURS[key].filter(s => s.t === 'body' || $(s.t)).map(s => ({
    target: s.t, title: s.n, content: s.c, placement: s.t === 'body' ? 'center' : 'auto',
    before: () => drawer(narrow && !!s.nav),
  }));
  if (!steps.length) { tourBusy = false; return; }
  const cs = getComputedStyle(document.documentElement), v = n => cs.getPropertyValue(n).trim();
  window.FlowaTour.start(steps, { accent: v('--accent'), card: v('--card-2'), text: v('--ink'), muted: v('--ink-2'), line: v('--line-2') }, async () => {
    document.body.classList.remove('nav-open');
    if (me) await updateMe({ seen: [...new Set([...(me.seen || []), key])] });
    tourBusy = false;
    render();
  });
}

/* ---------- pages ---------- */
function pageWelcome() {
  return `<div class="hero"><img src="icon.svg" alt="" width="56" height="56"><h1>Flowa</h1>
    <p>Skill maps, guides and simulated demos for flow arts props, with your own progress saved to your account.</p>
    <div class="row wrap" style="justify-content:center"><a class="btn primary" href="#/signin">Sign in</a>${flags.signups ? '<a class="btn" href="#/signup">Create an account</a>' : ''}<a class="btn" href="#/maps">Browse skill maps</a></div></div>
  <div class="grid g2"><div class="card"><div class="card-h"><h2>Without an account</h2></div><ul class="steps">
      <li>Browse the skill maps and every branch graph.</li><li>Read each skill's guide and references, and watch its demo.</li><li>Search skills with Ctrl K.</li></ul></div>
    <div class="card"><div class="card-h"><h2>With an account</h2></div><ul class="steps">
      <li>Track your level on every skill, for each hand.</li><li>Log practice, and see your streak and heatmap.</li><li>Build combos and test their transitions.</li><li>Follow the plan and tick off gates.</li><li>Choose your props and keep notes.</li></ul></div></div>`;
}
function pageAuth(mode) {
  const up = mode === 'signup';
  if (up && !flags.signups) return `<div class="auth card"><h1>Sign-ups are off</h1><p class="muted" style="margin-top:8px">Ask the admin for an account, then <a href="#/signin">sign in</a>.</p></div>`;
  return `<form class="auth card" id="auth-form" data-mode="${mode}" novalidate>
    <h1>${up ? 'Create an account' : 'Sign in'}</h1>
    <label class="field">Username<input type="text" name="username" autocomplete="username" autocapitalize="none" spellcheck="false" maxlength="24" required></label>
    ${up ? '<label class="field">Display name (optional)<input type="text" name="displayName" autocomplete="name" maxlength="40"></label>' : ''}
    <label class="field">Password<input type="password" name="password" autocomplete="${up ? 'new-password' : 'current-password'}" minlength="8" maxlength="200" required></label>
    ${up ? '<label class="field">Repeat password<input type="password" name="again" autocomplete="new-password" maxlength="200" required></label><p class="tiny">At least 8 characters. No email is needed, so nothing can be sent to you if you forget it.</p>' : ''}
    <p class="form-err" id="auth-err" role="alert" hidden></p>
    <button class="btn primary" type="submit">${up ? 'Create account' : 'Sign in'}</button>
    <p class="small muted">${up ? 'Already have an account? <a href="#/signin">Sign in</a>' : flags.signups ? 'New here? <a href="#/signup">Create an account</a>' : 'New sign-ups are turned off.'}</p></form>`;
}
function pageNeedAccount(title) {
  return `<div class="auth card"><h1>${esc(title)}</h1><p class="muted" style="margin:8px 0 14px">This page saves to your account. Sign in to use it.</p>
    <div class="row wrap"><a class="btn primary" href="#/signin">Sign in</a>${flags.signups ? '<a class="btn" href="#/signup">Create an account</a>' : ''}</div></div>`;
}
// Shown until a new user has picked their props.
function pageChooser() {
  const used = PROPS.filter(p => docs[p.id]?.rev > 0).map(p => p.id);
  return `<form class="chooser" id="props-form" data-first="1"><div class="page-h"><div><h1>Welcome, ${esc(me.displayName || me.username)}</h1>
    <p>Which props do you use? Flowa shows only these. You can change them later in your profile.</p></div></div>
    ${propChecks(used)}<p class="form-err" id="props-err" role="alert" hidden></p>
    <div class="row" style="margin-top:16px"><button class="btn primary" type="submit">Continue</button></div></form>`;
}
function pageProfile() {
  const status = [me.mutedUntil && `You are muted until ${new Date(me.mutedUntil).toLocaleString()}${me.muteReason ? `. Reason: ${me.muteReason}` : ''}. You can read and like, but not post or comment.`, me.statsHidden && 'A moderator hid your level and XP from other people.'].filter(Boolean);
  return `${status.map(s => `<p class="notice warn" style="margin-bottom:12px">${esc(s)}</p>`).join('')}<div class="page-h"><div class="grow"><h1>Profile</h1><p>@${esc(me.username)} · ${me.role !== 'user' ? RANK_NAMES[me.role] + ' · ' : ''}joined ${esc(new Date(me.created).toLocaleDateString(undefined, { month: 'long', year: 'numeric' }))}</p></div>
    ${flags.tour ? '<button class="btn" data-act="tour-replay">Replay the tour</button>' : ''}<button class="btn" data-act="signout">Sign out</button></div>
  <div class="grid g2">
    <div class="card"><div class="card-h"><h2>Picture and name</h2></div>
      <div class="row" style="gap:16px;margin-bottom:14px">${avatarHTML(me, 'big')}<div class="row wrap"><label class="btn sm">Upload picture<input type="file" accept="image/*" data-change="avatar" hidden></label>${flags.avatars ? `<a class="btn sm" href="#/avatar">${hasAvatar(me) ? 'Edit avatar' : 'Make an avatar'}</a>` : ''}${me.avatar ? '<button class="btn sm" data-act="avatar-remove">Remove</button>' : ''}</div></div>
      <form id="profile-form" class="row wrap" style="align-items:flex-end"><label class="field grow">Display name<input type="text" name="displayName" maxlength="40" required value="${esc(me.displayName)}"></label><button class="btn" type="submit" style="height:36px">Save</button></form></div>
    <form class="card" id="password-form"><div class="card-h"><h2>Password</h2></div><div class="stack" style="gap:10px">
      <label class="field">Current password<input type="password" name="current" autocomplete="current-password" required></label>
      <label class="field">New password<input type="password" name="next" autocomplete="new-password" minlength="8" maxlength="200" required></label>
      <p class="form-err" id="pw-err" role="alert" hidden></p>
      <div><button class="btn" type="submit">Change password</button></div><p class="tiny">Changing it signs out your other devices.</p></div></form>
    <form class="card span-2" id="about-form"><div class="card-h"><h2 class="grow">About you</h2>${flags.community ? `<a class="small" href="#/u/${esc(me.username)}">View your public profile</a>` : ''}</div>
      <div class="stack" style="gap:12px">
        <label class="field">Bio<textarea name="bio" maxlength="300" placeholder="What you spin, where you practise, what you are working toward">${esc(me.bio || '')}</textarea></label>
        <div class="grid links-grid">${Object.entries(LINK_KINDS).map(([k, L]) => `<label class="field social-${k}">${icon(k)} ${L.name}<input type="text" name="${k}" maxlength="120" value="${esc((me.links || {})[k] || '')}" placeholder="${L.hint} or just the handle" autocapitalize="none" spellcheck="false"></label>`).join('')}</div>
        <label class="check" ${flags.community && flags.game ? '' : 'hidden'}><input type="checkbox" name="autoPost" ${(me.prefs || {}).autoPost === false ? '' : 'checked'}><span>Post my milestones to the timeline automatically (new badges)</span></label>
        <label class="check" ${flags.suggest ? '' : 'hidden'}><input type="checkbox" name="shareTransitions" ${(me.prefs || {}).shareTransitions ? 'checked' : ''}><span>Help improve next-trick suggestions: share which transitions work, are blocked or need a bridge in my combos. Only counts per pair of skills are kept, with no name. Turning it off takes my marks back out.</span></label>
        <p class="form-err" id="about-err" role="alert" hidden></p>
        <div><button class="btn" type="submit">Save</button></div></div></form>
    <form class="card span-2" id="props-form"><div class="card-h"><h2>Your props</h2></div>${propChecks(me.props || [])}
      <p class="form-err" id="props-err" role="alert" hidden></p><div class="row" style="margin-top:14px"><button class="btn" type="submit">Save props</button></div></form>
  </div>`;
}
function propPlaceholder(p) {
  return `<div class="card"><div class="card-h"><h2>${esc(p.name)}: skill map not built yet</h2></div>
    <p class="small muted">${esc(p.kind)}. Its skill map could not be loaded.${me ? ' You can already <a href="#/practice">log practice</a> for it.' : ''}</p>
    <div class="sec-t" style="margin-top:14px">Planned branches</div><div class="chips">${p.families.map(f => `<span class="chip">${esc(f)}</span>`).join('')}</div>
    <p class="tiny" style="margin-top:10px">This is a draft outline from general knowledge of the prop. It has not been checked against sources.</p></div>`;
}
function pageMaps() {
  const cards = myProps().map(p => {
    const mins = me ? inProp(p.id, () => state.sessions.reduce((a, s) => a + (Number(s.minutes) || 0), 0)) : null;
    const pct = p.ready && me ? inProp(p.id, () => pctOf(SKILLS)) : null, cat = CATALOGS[p.id];
    return `<a class="prop-card ${p.id === ui.prop ? 'on' : ''}" href="#/prop/${p.id}" ${p.id === ui.prop ? 'aria-current="true"' : ''}><b>${esc(p.name)}</b><span class="tiny">${esc(p.kind)}</span>
      ${p.ready ? `<span class="small">${cat.skills.length} skills in ${cat.branches.length} branches</span>${pct === null ? '' : `<span class="row" style="gap:8px"><span class="bar grow"><i style="width:${pct}%"></i></span><span class="tiny num">${pct}%</span></span>`}` : '<span class="chip">Map not built yet</span>'}
      ${mins ? `<span class="tiny">${fmtMin(mins)} practised</span>` : ''}</a>`;
  }).join('');
  const p = curProp();
  return `<div class="page-h"><div><h1>Skill maps</h1><p>One map per prop. ${me ? 'These are the props on your profile.' : 'Sign in to track your own progress on them.'}</p></div></div>
    <div class="prop-cards">${cards}</div>${p.ready ? pageMap() : propPlaceholder(p)}`;
}
function pageDashLite() {
  const p = curProp(), m = minutesByDay(), total = Object.values(m).reduce((a, b) => a + b, 0), st = streak(m);
  return `<div class="page-h"><div class="grow"><h1>Dashboard</h1><p>${esc(p.name)}</p></div><a class="btn primary" href="#/practice">${icon('plus')}Log practice</a></div>
  <div class="grid g4" data-tour="stats" style="margin-bottom:16px">
    <div class="card stat"><div class="lbl">Last 7 days</div><div class="val">${weekMinutes(m)}<small> min</small></div><div class="foot">${fmtMin(total)} in total</div></div>
    <div class="card stat"><div class="lbl">Practice streak</div><div class="val">${st}<small> ${st === 1 ? 'day' : 'days'}</small></div><div class="foot">${state.sessions.length} ${state.sessions.length === 1 ? 'session' : 'sessions'} logged</div></div></div>
  ${flags.game ? `<div class="grid g2" style="margin-bottom:16px">${levelCard(true)}${streakCard()}</div>` : ''}
  <div class="stack"><div class="card"><div class="card-h"><h2 class="grow">Practice, last 9 months</h2><a class="small" href="#/practice">View log</a></div>${heatmap(m)}</div>${propPlaceholder(p)}</div>`;
}
const needCatalog = title => `<div class="page-h"><div><h1>${title}</h1><p>${esc(curProp().name)}</p></div></div>${propPlaceholder(curProp())}`;

/* ---------- events ---------- */
const showErr = (id, msg) => { const el = $('#' + id); if (el) { el.textContent = msg; el.hidden = !msg; } };
document.addEventListener('click', async e => {
  const t = e.target.closest('[data-act]');
  if (!t) return;
  const d = t.dataset;
  switch (d.act) {
    case 'prop': switchProp(d.v); break;
    case 'nav-toggle':
      ui.open = ui.open.includes(d.id) ? ui.open.filter(x => x !== d.id) : [...ui.open, d.id];
      localStorage.setItem('fl-groups', ui.open.join(',')); render(); break;
    case 'tip-ok': await updateMe({ seen: [...new Set([...(me.seen || []), d.k])] }); render(); break;
    case 'tour-replay': ui.replay = true; if (location.hash === '#/') render(); else location.hash = '#/'; break;
    case 'avatar-remove': await updateMe({ avatar: '' }, 'Picture removed.'); render(); break;
    case 'signout':
      if (dirty) { clearTimeout(saveTimer); await save(); }
      await api('signout', 'POST');
      ui.users = null; location.hash = '#/';
      try { await load(); } catch { /* render shows the guest view */ }
      render(); toast('Signed out.');
      break;
  }
});
document.addEventListener('change', async e => {
  if (e.target.dataset.change !== 'avatar') return;
  const f = e.target.files[0]; e.target.value = '';
  if (!f) return;
  try {
    const url = URL.createObjectURL(f), img = new Image();
    img.src = url; await img.decode();
    const c = document.createElement('canvas'), s = Math.min(img.naturalWidth, img.naturalHeight);
    c.width = c.height = 160;
    c.getContext('2d').drawImage(img, (img.naturalWidth - s) / 2, (img.naturalHeight - s) / 2, s, s, 0, 0, 160, 160);
    URL.revokeObjectURL(url);
    await updateMe({ avatar: c.toDataURL('image/jpeg', 0.85) }, 'Picture updated.');
  } catch { toast('That file could not be read as a picture.'); }
  render();
});
document.addEventListener('submit', async e => {
  const f = e.target;
  if (f.id === 'auth-form') {
    const up = f.dataset.mode === 'signup', body = { username: f.username.value, password: f.password.value };
    if (up) {
      if (f.password.value !== f.again.value) { showErr('auth-err', 'The two passwords do not match.'); return; }
      body.displayName = f.displayName.value;
    }
    const btn = f.querySelector('button[type=submit]');
    btn.disabled = true; showErr('auth-err', '');
    const r = await api(up ? 'signup' : 'signin', 'POST', body);
    btn.disabled = false;
    if (!r.ok) { showErr('auth-err', r.error); return; }
    document.activeElement?.blur?.();
    location.hash = '#/';
    try { await load(); } catch { toast('Signed in, but your progress did not load. Reload the page.'); }
    render();
  } else if (f.id === 'props-form') {
    const props = [...f.querySelectorAll('input[name=prop]:checked')].map(i => i.value);
    if (!props.length) { showErr('props-err', 'Pick at least one prop.'); return; }
    if (await updateMe({ props }, f.dataset.first ? '' : 'Props saved.')) {
      try { await load(); } catch { /* keeps the old view */ }
      // New users are offered the avatar editor next, with a random avatar, and can skip it.
      if (f.dataset.first) location.hash = flags.avatars && !hasAvatar(me) ? '#/avatar/first' : '#/';
    }
    render();
  } else if (f.id === 'about-form') {
    const links = {};
    for (const k in LINK_KINDS) links[k] = f[k].value;
    const before = !!(me.prefs || {}).shareTransitions;
    const r = await api('me', 'PUT', { bio: f.bio.value, links, prefs: { autoPost: f.autoPost.checked, shareTransitions: f.shareTransitions.checked } });
    if (!r.ok) { showErr('about-err', r.error); return; }
    me = r.data.user; showErr('about-err', ''); document.activeElement?.blur?.(); delete social.profile[me.username];
    if (before !== !!me.prefs?.shareTransitions) await ntSyncAll(true); // share every mark, or take them all back
    toast('Profile saved.'); render();
  } else if (f.id === 'profile-form') {
    document.activeElement?.blur?.();
    await updateMe({ displayName: f.displayName.value }, 'Name saved.'); render();
  } else if (f.id === 'password-form') {
    const r = await api('me/password', 'PUT', { current: f.current.value, next: f.next.value });
    if (!r.ok) { showErr('pw-err', r.error); return; }
    document.activeElement?.blur?.(); f.reset(); showErr('pw-err', ''); toast('Password changed.');
  }
});
