'use strict';
// Gamification: XP and levels, the practice streak across props, weekly challenges,
// achievements, and the shareable streak overlay. Everything is worked out from the
// progress documents in the browser; only the unlocked achievements and a few totals for
// the profile are reported to the server (social.go). Loaded before app.js, which defines
// the helpers used here (lv, best, variants, comboReady, inProp, ...).

const XP_SKILL = [0, 10, 40, 100, 200]; // total XP for one skill variant at level 0 to 4
const XP = { session: 10, minuteCap: 120, gate: 150, week: 40, comboReady: 50, cleanRun: 15, cleanRunCap: 20, challenge: 40 };
const LEVEL_TITLES = [[1, 'Beginner'], [3, 'Novice'], [5, 'Apprentice'], [8, 'Spinner'], [12, 'Flow artist'], [16, 'Performer'], [20, 'Virtuoso'], [25, 'Master'], [30, 'Legend']];
// Total XP needed to reach level n: 100, 300, 600, 1000, ... (level 1 is the start).
const levelNeed = n => 50 * (n - 1) * n;
const levelTitle = n => LEVEL_TITLES.filter(([l]) => l <= Math.max(1, n)).pop()[1];
function levelInfo(xp) {
  let n = 1;
  while (levelNeed(n + 1) <= xp) n++;
  const base = levelNeed(n), span = levelNeed(n + 1) - base;
  return { level: n, title: levelTitle(n), into: xp - base, span, pct: Math.min(100, Math.round(100 * (xp - base) / span)), next: levelNeed(n + 1) - xp };
}

/* ---------- XP ---------- */
// XP for the prop in use: skill levels, sessions, gates and combos.
function propXP() {
  let skills = 0, practice = 0, gates = 0, combos = 0;
  for (const s of SKILLS) for (const [k] of variants(s)) skills += XP_SKILL[lv(s.id, k)] || 0;
  for (const s of state.sessions) practice += XP.session + Math.min(Number(s.minutes) || 0, XP.minuteCap);
  const ph = new Set(PHASES.map(p => p.id));
  for (const k in state.gates) gates += ph.has(k) ? XP.gate : XP.week;
  for (const c of state.combos) combos += (comboReady(c) ? XP.comboReady : 0) + Math.min(Number(c.clean) || 0, XP.cleanRunCap) * XP.cleanRun;
  return { skills, practice, gates, combos, total: skills + practice + gates + combos };
}
// XP over every prop, plus the weekly challenges.
function xpAll() {
  const sum = { skills: 0, practice: 0, gates: 0, combos: 0, challenges: 0, total: 0, byProp: {} };
  for (const p of myProps()) {
    const x = inProp(p.id, propXP);
    if (!x) continue;
    sum.byProp[p.id] = x.total;
    for (const k of ['skills', 'practice', 'gates', 'combos']) sum[k] += x[k];
  }
  sum.challenges = challengeXP().xp;
  sum.total = sum.skills + sum.practice + sum.gates + sum.combos + sum.challenges;
  return sum;
}

/* ---------- sessions and events across props ---------- */
const docOf = id => id === ui.prop ? { state } : docs[id];
function allSessions() {
  const out = [];
  for (const p of myProps()) { const d = docOf(p.id); if (d) for (const s of d.state.sessions) out.push({ ...s, prop: p.id }); }
  return out;
}
function allEvents() {
  const out = [];
  for (const p of myProps()) {
    const d = docOf(p.id);
    if (!d) continue;
    const weak = d.state.settings.dominant === 'L' ? 'R' : 'L';
    for (const e of d.state.events) out.push({ ...e, prop: p.id, weak });
  }
  return out;
}

/* ---------- streak ---------- */
const weekStart = d => { const x = new Date(d.getFullYear(), d.getMonth(), d.getDate()); x.setDate(x.getDate() - (x.getDay() + 6) % 7); return x; };
const daysBetween = (a, b) => Math.round((b - a) / 864e5);
function streakInfo() {
  const mins = {};
  for (const s of allSessions()) mins[s.date] = (mins[s.date] || 0) + (Number(s.minutes) || 0);
  const days = Object.keys(mins).filter(k => mins[k] > 0).sort();
  const current = streak(mins), today = !!mins[dayKey()];
  let best = 0, run = 0, prev = null;
  for (const k of days) {
    const d = parseDay(k);
    run = prev && daysBetween(prev, d) === 1 ? run + 1 : 1;
    best = Math.max(best, run); prev = d;
  }
  const now = new Date(), monday = weekStart(now);
  const week = [...Array(7)].map((_, i) => { const d = new Date(monday); d.setDate(d.getDate() + i); const k = dayKey(d); return { key: k, on: !!mins[k], future: d > now && k !== dayKey() }; });
  return { current, best: Math.max(best, current), today, atRisk: current > 0 && !today, week, days: days.length, minutes: Object.values(mins).reduce((a, b) => a + b, 0), mins };
}

/* ---------- weekly challenges ---------- */
const CHALLENGES = [
  { id: 'days', name: 'Practise on 4 days', of: 4, unit: 'days', n: x => x.days.size },
  { id: 'minutes', name: 'Log 120 minutes', of: 120, unit: 'min', n: x => x.min },
  { id: 'levels', name: 'Raise 3 skill levels', of: 3, unit: 'level-ups', n: x => x.ups },
  { id: 'weak', name: 'Level up a weak-hand skill', of: 1, unit: '', n: x => x.weak },
];
const emptyWeek = () => ({ days: new Set(), min: 0, ups: 0, weak: 0 });
// Per week (keyed by its Monday): practice days and minutes, level-ups, weak-hand level-ups.
function weekStats() {
  const w = {}, get = k => w[k] ||= emptyWeek();
  for (const s of allSessions()) {
    const m = Number(s.minutes) || 0;
    if (m > 0 && /^\d{4}-\d\d-\d\d$/.test(s.date)) { const x = get(dayKey(weekStart(parseDay(s.date)))); x.days.add(s.date); x.min += m; }
  }
  for (const e of allEvents()) {
    if (!(e.to > e.from)) continue;
    const x = get(dayKey(weekStart(new Date(e.t))));
    x.ups++;
    if (e.side && e.side === e.weak) x.weak++;
  }
  return w;
}
const thisWeekKey = () => dayKey(weekStart(new Date()));
function challenges(weekKey = thisWeekKey(), ws = weekStats()) {
  const x = ws[weekKey] || emptyWeek();
  return CHALLENGES.map(c => { const n = Math.min(c.n(x), c.of); return { ...c, n, done: n >= c.of }; });
}
function challengeXP(ws = weekStats()) {
  let xp = 0, weeks = 0;
  for (const k in ws) { const d = challenges(k, ws).filter(c => c.done).length; xp += d * XP.challenge; if (d === CHALLENGES.length) weeks++; }
  return { xp, weeks };
}

/* ---------- achievements ---------- */
// Each badge has a key, a group, an icon, a target `of` and a reader `n` of the context
// below. Manual badges are granted by an action (sharing, posting) instead.
const achTiers = (base, desc, icon, group, steps, n) => steps.map(([of, name], i) => ({ key: `${base}-${of}`, name, desc: desc(of), icon, group, tier: i + 1, of, n }));
const ACH = [
  ...achTiers('sessions', n => `Log ${n} practice ${n === 1 ? 'session' : 'sessions'}.`, 'log', 'Practice', [[1, 'First steps'], [10, 'Regular'], [50, 'Dedicated'], [200, 'Devoted']], c => c.sessions),
  ...achTiers('hours', n => `Practise for ${n} hours in total.`, 'log', 'Practice', [[10, 'Ten hours in'], [50, 'Fifty hours'], [100, 'Hundred hours']], c => c.minutes / 60),
  { key: 'props-2', name: 'Multi-prop', desc: 'Log practice on two different props.', icon: 'map', group: 'Practice', of: 2, n: c => c.props },
  ...achTiers('streak', n => `Practise ${n} days in a row.`, 'flame', 'Streaks', [[3, 'Warming up'], [7, 'One week on fire'], [30, 'Thirty days'], [100, 'Hundred days']], c => c.best),
  { key: 'week-5', name: 'Five-day week', desc: 'Practise on five days of one week.', icon: 'calendar', group: 'Streaks', of: 5, n: c => c.weekBest },
  ...achTiers('challenges', n => `Complete all four weekly challenges in ${n === 1 ? 'a week' : n + ' weeks'}.`, 'bolt', 'Streaks', [[1, 'Challenger'], [4, 'Four full weeks']], c => c.challengeWeeks),
  ...achTiers('clean', n => `Bring ${n} ${n === 1 ? 'skill' : 'skills'} to Clean.`, 'check', 'Skills', [[1, 'First clean skill'], [10, 'Ten clean'], [50, 'Fifty clean'], [100, 'Hundred clean']], c => c.clean),
  { key: 'perf-1', name: 'Showtime', desc: 'Take a skill to Performance level.', icon: 'star', group: 'Skills', of: 1, n: c => c.lv4 },
  ...achTiers('both', n => `Have ${n} two-handed skills clean on both hands.`, 'hands', 'Skills', [[5, 'Ambidextrous'], [20, 'Even-handed']], c => c.both),
  { key: 'notes-10', name: 'Note taker', desc: 'Keep notes on ten skills.', icon: 'note', group: 'Skills', of: 10, n: c => c.notes },
  ...achTiers('gates', n => `Pass ${n === 1 ? 'your first gate' : n + ' gates'}.`, 'flag', 'Plan', [[1, 'Through the gate'], [5, 'Gatekeeper']], c => c.gates),
  { key: 'combo-1', name: 'Combo builder', desc: 'Build a combo of two or more skills.', icon: 'link', group: 'Combos', of: 1, n: c => c.combos },
  { key: 'combo-ready', name: 'Linked up', desc: 'Get a combo ready: every transition works.', icon: 'link', group: 'Combos', of: 1, n: c => c.ready },
  ...achTiers('runs', n => `Log ${n} clean combo runs.`, 'check', 'Combos', [[10, 'Ten clean runs'], [50, 'Fifty clean runs']], c => c.cleanRuns),
  { key: 'first-post', name: 'Said hello', desc: 'Write your first post on the timeline.', icon: 'comment', group: 'Community', manual: true, of: 1, n: () => 0 },
  { key: 'share-streak', name: 'Shared the flame', desc: 'Download or share your streak overlay.', icon: 'share', group: 'Community', manual: true, of: 1, n: () => 0 },
];
// Badges for each prop on the profile: its roadmap and each of its branches.
function propBadges(props = myProps()) {
  const out = [];
  for (const p of props) {
    const cat = CATALOGS[p.id];
    if (!cat) continue;
    if (cat.phases.length) out.push({ key: `gates:${p.id}`, name: `${p.name} roadmap complete`, desc: `Pass every gate of the ${p.name.toLowerCase()} plan.`, icon: 'trophy', group: p.name, of: cat.phases.length, n: c => c.gatesBy[p.id] || 0 });
    for (const b of cat.branches) {
      const size = cat.skills.filter(s => s.b === b.k).length;
      if (size) out.push({ key: `branch:${p.id}:${b.k}`, name: `${b.name} complete`, desc: `Every ${p.name.toLowerCase()} ${b.name.toLowerCase()} skill clean.`, icon: 'medal', group: p.name, of: size, n: c => c.branchClean[`${p.id}:${b.k}`] || 0 });
    }
  }
  return out;
}
const allBadges = (props = myProps()) => [...ACH, ...propBadges(props)];
const badgeByKey = key => allBadges(PROPS).find(b => b.key === key);

// What the badges are measured against, over every prop.
function gameContext() {
  const sess = allSessions(), st = streakInfo(), ws = weekStats();
  const ctx = {
    sessions: sess.length, minutes: sess.reduce((a, s) => a + (Number(s.minutes) || 0), 0), streak: st.current, best: st.best,
    props: new Set(sess.filter(s => Number(s.minutes) > 0).map(s => s.prop)).size, weekBest: Math.max(0, ...Object.values(ws).map(x => x.days.size)),
    challengeWeeks: challengeXP(ws).weeks, clean: 0, lv4: 0, both: 0, notes: 0, gates: 0, gatesBy: {}, branchClean: {}, combos: 0, ready: 0, cleanRuns: 0,
  };
  for (const p of myProps()) inProp(p.id, () => {
    for (const s of SKILLS) {
      const b = best(s.id);
      if (b >= 2) { ctx.clean++; ctx.branchClean[`${p.id}:${s.b}`] = (ctx.branchClean[`${p.id}:${s.b}`] || 0) + 1; }
      if (b >= 4) ctx.lv4++;
      if (s.track === 'LR' && worst(s.id) >= 2) ctx.both++;
      if ((rec(s.id).notes || '').trim()) ctx.notes++;
    }
    const g = PHASES.filter(x => state.gates[x.id]).length;
    ctx.gates += g; ctx.gatesBy[p.id] = g;
    for (const c of state.combos) { if ((c.seq || []).length >= 2) ctx.combos++; if (comboReady(c)) ctx.ready++; ctx.cleanRuns += Number(c.clean) || 0; }
  });
  return ctx;
}
let achSet = new Set(), achAt = {}, lastStats = '';
function setAchievements(list) { achSet = new Set(); achAt = {}; for (const a of list || []) { achSet.add(a.key); achAt[a.key] = a.at; } }
// Badges with their state: unlocked (and when), or progress toward the target.
function badgeStates(ctx = gameContext()) {
  return allBadges().map(b => {
    const n = Math.min(b.n(ctx), b.of), on = achSet.has(b.key) || (!b.manual && n >= b.of);
    return { ...b, n: on ? b.of : n, on, at: achAt[b.key] };
  });
}

// After loading and after every save: record new achievements, report the totals.
async function syncGame() {
  if (!me || !loaded) return;
  const ctx = gameContext();
  const fresh = allBadges().filter(b => !b.manual && !achSet.has(b.key) && b.n(ctx) >= b.of).map(b => b.key);
  if (fresh.length) await grant(fresh, true);
  const xp = xpAll(), L = levelInfo(xp.total), st = streakInfo();
  const stats = { xp: xp.total, level: L.level, streak: st.current, best: st.best, clean: ctx.clean, minutes: ctx.minutes, sessions: ctx.sessions, badges: achSet.size };
  const key = JSON.stringify(stats);
  if (key !== lastStats) { lastStats = key; await updateMe({ stats }); }
}
// Records unlocked badges. Badges earned by an action (posting, sharing) make no milestone post.
async function grant(keys, post = false) {
  keys = keys.filter(k => !achSet.has(k));
  if (!keys.length) return;
  const r = await api('achievements', 'POST', { keys, prop: ui.prop, post });
  if (!r.ok) return;
  for (const a of r.data.all) { achSet.add(a.key); achAt[a.key] = a.at; }
  if (r.data.new.length) { showUnlock(r.data.new); render(); }
}
let unlockT;
function showUnlock(keys) {
  const el = $('#unlock'), b = badgeByKey(keys[0]);
  el.innerHTML = `<a href="#/achievements" class="unlock-card"><span class="badge-ic on">${icon(b?.icon || 'medal')}</span><span><span class="tiny">Achievement${keys.length > 1 ? 's' : ''} unlocked</span><b>${esc(b?.name || keys[0])}${keys.length > 1 ? ` and ${keys.length - 1} more` : ''}</b></span></a>`;
  el.hidden = false;
  clearTimeout(unlockT); unlockT = setTimeout(() => el.hidden = true, 6000);
}

/* ---------- views ---------- */
function ring(pct, size = 44, stroke = 5, cls = '') {
  const r = (size - stroke) / 2, c = 2 * Math.PI * r;
  return `<svg class="ring ${cls}" width="${size}" height="${size}" viewBox="0 0 ${size} ${size}" aria-hidden="true"><circle cx="${size / 2}" cy="${size / 2}" r="${r}" stroke-width="${stroke}"/><circle class="on" cx="${size / 2}" cy="${size / 2}" r="${r}" stroke-width="${stroke}" stroke-dasharray="${c}" stroke-dashoffset="${c * (1 - Math.min(1, pct / 100))}"/></svg>`;
}
function levelCard(compact) {
  const xp = xpAll(), L = levelInfo(xp.total);
  return `<div class="card level-card" data-tour="level"><div class="row" style="gap:14px"><div class="level-ring">${ring(L.pct, 64, 6)}<b class="num">${L.level}</b></div>
    <div class="grow"><div class="lbl small muted">Level ${L.level}</div><div class="level-title">${L.title}</div>
      <div class="bar" style="margin-top:8px"><i style="width:${L.pct}%"></i></div>
      <div class="row small muted num" style="margin-top:5px;justify-content:space-between"><span>${xp.total.toLocaleString()} XP</span><span>${L.next.toLocaleString()} XP to level ${L.level + 1}</span></div></div></div>
    ${compact ? '' : `<div class="xp-split num">${[['Skills', xp.skills], ['Practice', xp.practice], ['Gates', xp.gates], ['Combos', xp.combos], ['Challenges', xp.challenges]].map(([n, v]) => `<span><b>${v.toLocaleString()}</b><span class="tiny">${n}</span></span>`).join('')}</div>`}</div>`;
}
function weekDots(st, cls = '') {
  const names = ['M', 'T', 'W', 'T', 'F', 'S', 'S'];
  return `<div class="week-dots ${cls}" aria-label="This week">${st.week.map((d, i) => `<span class="${d.on ? 'on' : d.future ? 'future' : ''} ${d.key === dayKey() ? 'today' : ''}" title="${esc(fmtDay(d.key))}"><i>${d.on ? icon('check') : ''}</i><u>${names[i]}</u></span>`).join('')}</div>`;
}
function streakCard(st = streakInfo()) {
  const n = st.current;
  return `<div class="card streak-card ${st.today ? 'lit' : ''}" data-tour="streak">
    <div class="row" style="gap:14px;align-items:flex-start"><span class="flame ${n ? 'lit' : ''}">${icon('flame')}</span>
      <div class="grow"><div class="val num">${n}<small> ${n === 1 ? 'day' : 'days'}</small></div><div class="small muted">Practice streak · best ${st.best}</div></div>
      <button class="btn sm" data-act="share-streak">${icon('share')}Share</button></div>
    ${weekDots(st)}
    <p class="tiny" style="margin-top:8px">${st.today ? 'Today is in. See you tomorrow.' : n ? 'Log today’s practice to keep the streak.' : 'Log a session to start a streak. Any prop counts.'}</p></div>`;
}
function challengesCard() {
  const list = challenges(), done = list.filter(c => c.done).length;
  return `<div class="card" data-tour="challenges"><div class="card-h"><h2 class="grow">Weekly challenges</h2><span class="sub num">${done} of ${list.length} · ${XP.challenge} XP each</span></div>
    <div class="ch-list">${list.map(c => `<div class="ch ${c.done ? 'done' : ''}">${ring(100 * c.n / c.of, 34, 4)}<span class="grow"><b>${c.name}</b><span class="tiny num">${c.done ? 'Done' : `${c.id === 'minutes' ? c.n : Math.floor(c.n)} of ${c.of}${c.unit ? ' ' + c.unit : ''}`}</span></span>${c.done ? `<span class="chip ok">${icon('check')}+${XP.challenge}</span>` : ''}</div>`).join('')}</div>
    <p class="tiny" style="margin-top:10px">Weeks run Monday to Sunday and count every prop.</p></div>`;
}
function badgeHTML(b) {
  return `<div class="badge ${b.on ? 'on' : ''}" data-tip="${esc(b.desc)}"><span class="badge-ic ${b.on ? 'on' : ''}">${icon(b.icon)}</span>
    <span class="grow"><b>${esc(b.name)}</b><span class="tiny">${b.on ? (b.at ? 'Unlocked ' + esc(new Date(b.at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })) : 'Unlocked') : esc(b.desc)}</span>
    ${b.on ? '' : `<span class="bar" style="margin-top:6px"><i style="width:${Math.round(100 * b.n / b.of)}%"></i></span>`}</span>${b.on ? '' : `<span class="tiny num">${Math.floor(b.n)}/${b.of}</span>`}</div>`;
}
function badgeGrid(list) {
  const groups = [...new Set(list.map(b => b.group))];
  return groups.map(g => { const l = list.filter(b => b.group === g); return `<section class="badge-group"><div class="card-h"><h2 class="grow">${esc(g)}</h2><span class="sub num">${l.filter(b => b.on).length} of ${l.length}</span></div><div class="badge-grid">${l.map(badgeHTML).join('')}</div></section>`; }).join('');
}
function pageAchievements() {
  const states = badgeStates(), on = states.filter(b => b.on).length;
  const recent = states.filter(b => b.on && b.at).sort((a, b) => b.at.localeCompare(a.at)).slice(0, 4);
  return `<div class="page-h"><div class="grow"><h1>Achievements</h1><p>${on} of ${states.length} badges, across all your props. Level and streak count every prop too.</p></div><a class="btn" href="#/u/${esc(me.username)}">${icon('user')}Public profile</a></div>
  <div class="grid g2" style="margin-bottom:16px">${levelCard()}${streakCard()}</div>
  <div class="grid g2" style="margin-bottom:22px">${challengesCard()}
    <div class="card"><div class="card-h"><h2 class="grow">How XP works</h2></div><ul class="steps small">
      <li>A skill variant earns ${XP_SKILL.slice(1).join(', ')} XP at levels 1 to 4 (per hand or direction where tracked).</li>
      <li>Each session earns ${XP.session} XP plus one per minute, up to ${XP.minuteCap} minutes.</li>
      <li>A phase gate is ${XP.gate} XP; a track week is ${XP.week} XP.</li>
      <li>A ready combo is ${XP.comboReady} XP, and each clean run ${XP.cleanRun} XP, up to ${XP.cleanRunCap} runs.</li>
      <li>Each weekly challenge done is ${XP.challenge} XP.</li></ul>
      ${recent.length ? `<div class="sec-t" style="margin-top:14px">Latest badges</div><div class="chips">${recent.map(b => `<span class="chip ok">${icon(b.icon)}${esc(b.name)}</span>`).join('')}</div>` : ''}</div></div>
  ${badgeGrid(states)}`;
}

/* ---------- share the streak: a PNG overlay for stories ---------- */
// Draws the streak on a transparent canvas (or over a gradient), sized for a story or a
// square post, so people can lay it over their own photo in Instagram or TikTok.
function drawFlame(c, x, y, s) {
  const p = new Path2D('M12 21c-3.5 0-6-2.4-6-5.8 0-2.6 1.6-4.3 2.8-6 .3 1.2.9 2.2 1.9 2.9C10.3 9 10.7 5.8 13 3c.4 3 2.5 4.4 3.8 6.4A7 7 0 0 1 18 15.2C18 18.6 15.5 21 12 21Z');
  c.save(); c.translate(x - s / 2, y - s / 2); c.scale(s / 24, s / 24);
  const g = c.createLinearGradient(0, 3, 0, 21); g.addColorStop(0, '#ffd166'); g.addColorStop(.55, '#ff7a3d'); g.addColorStop(1, '#e8402f');
  c.fillStyle = g; c.fill(p);
  const inner = new Path2D('M12 20c-1.9 0-3.3-1.3-3.3-3.2 0-1.5 1-2.6 1.7-3.6.3.8.8 1.3 1.3 1.6.1-1.6.6-2.9 1.6-4 .3 1.6 1.4 2.5 2 3.6.4.7.6 1.5.6 2.4 0 1.9-1.6 3.2-3.9 3.2Z');
  c.fillStyle = '#fff3c4'; c.fill(inner); c.restore();
}
function streakCanvas(opts = {}) {
  const st = streakInfo(), story = opts.format !== 'square', W = 1080, H = story ? 1920 : 1080;
  const c = document.createElement('canvas'); c.width = W; c.height = H;
  const x = c.getContext('2d');
  if (opts.bg) {
    const g = x.createLinearGradient(0, 0, W, H); g.addColorStop(0, '#15203a'); g.addColorStop(1, '#2a1f4a');
    x.fillStyle = g; x.fillRect(0, 0, W, H);
    x.fillStyle = 'rgba(255,255,255,.04)';
    for (let i = 0; i < 40; i++) { x.beginPath(); x.arc((i * 263) % W, (i * 571) % H, 40 + (i * 37) % 90, 0, 7); x.fill(); }
  }
  // The block sits in the lower part of a story, leaving room for a face above it.
  const cy = story ? 1250 : 540, font = 'Inter, ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif';
  const text = (t, y, size, weight = 600, color = '#fff', spacing = 0) => {
    x.font = `${weight} ${size}px ${font}`; x.textAlign = 'center'; x.textBaseline = 'middle';
    if ('letterSpacing' in x) x.letterSpacing = spacing + 'px';
    x.shadowColor = 'rgba(0,0,0,.6)'; x.shadowBlur = size / 5; x.shadowOffsetY = size / 30;
    x.lineWidth = Math.max(2, size / 22); x.strokeStyle = 'rgba(0,0,0,.35)'; x.strokeText(t, W / 2, y);
    x.fillStyle = color; x.fillText(t, W / 2, y);
    x.shadowBlur = 0; x.shadowOffsetY = 0;
  };
  x.shadowColor = 'rgba(0,0,0,.45)'; x.shadowBlur = 30;
  drawFlame(x, W / 2, cy - 330, 300);
  x.shadowBlur = 0;
  text(String(st.current), cy - 40, 320, 800, '#fff', -10);
  text(st.current === 1 ? 'DAY STREAK' : 'DAY STREAK', cy + 160, 54, 700, '#ffd9a8', 10);
  // The week, Monday to Sunday.
  const names = ['M', 'T', 'W', 'T', 'F', 'S', 'S'], gap = 92, x0 = W / 2 - 3 * gap, wy = cy + 290;
  st.week.forEach((d, i) => {
    const px = x0 + i * gap;
    x.beginPath(); x.arc(px, wy, 30, 0, 7);
    x.fillStyle = d.on ? '#ff8a3d' : 'rgba(255,255,255,.22)'; x.fill();
    x.lineWidth = 4; x.strokeStyle = d.key === dayKey() ? '#fff' : 'rgba(255,255,255,.55)'; x.stroke();
    if (d.on) { x.beginPath(); x.moveTo(px - 12, wy); x.lineTo(px - 3, wy + 10); x.lineTo(px + 13, wy - 10); x.lineWidth = 6; x.strokeStyle = '#fff'; x.lineCap = x.lineJoin = 'round'; x.stroke(); }
    text(names[i], wy + 60, 26, 600, 'rgba(255,255,255,.85)');
  });
  const props = myProps().map(p => p.name).join(' · ');
  text(`@${me.username}${props ? ' · ' + props : ''}`, cy + 450, 40, 600, '#fff');
  const L = levelInfo(xpAll().total);
  text(`Level ${L.level} ${L.title} · ${st.best} day best · flowa`, cy + 515, 30, 500, 'rgba(255,255,255,.8)');
  return c;
}
const canvasBlob = (c, type = 'image/png', q) => new Promise(ok => c.toBlob(ok, type, q));
let shareFmt = 'story';
function openShare() {
  const st = streakInfo(), el = $('#modal');
  el.hidden = false;
  el.innerHTML = `<div class="modal-box" role="dialog" aria-modal="true" aria-labelledby="share-t">
    <div class="row"><h2 id="share-t" class="grow">Share your streak</h2><button class="icon-btn" data-act="modal-close" aria-label="Close">${icon('x')}</button></div>
    <p class="small muted" style="margin-top:4px">A transparent PNG to lay over your own photo or video, for Instagram, TikTok or anywhere else.</p>
    <div class="share-body"><div class="share-prev ${shareFmt}"><canvas id="share-cv" aria-label="Streak overlay preview"></canvas></div>
      <div class="stack" style="gap:10px">
        <div class="seg" role="group" aria-label="Format" style="width:max-content"><button class="${shareFmt === 'story' ? 'on' : ''}" data-act="share-fmt" data-v="story">Story 9:16</button><button class="${shareFmt === 'square' ? 'on' : ''}" data-act="share-fmt" data-v="square">Square</button></div>
        <button class="btn primary" data-act="share-do" data-v="download">${icon('download')}Download PNG overlay</button>
        ${navigator.canShare ? `<button class="btn" data-act="share-do" data-v="share">${icon('share')}Share to an app…</button>` : ''}
        <button class="btn" data-act="share-do" data-v="post">${icon('send')}Post to the Flowa timeline</button>
        <div class="row wrap" style="gap:6px"><a class="btn sm" href="https://www.instagram.com/" target="_blank" rel="noopener noreferrer">${icon('instagram')}Open Instagram</a><a class="btn sm" href="https://www.tiktok.com/" target="_blank" rel="noopener noreferrer">${icon('tiktok')}Open TikTok</a><button class="btn sm" data-act="share-do" data-v="copy">Copy caption</button></div>
        <p class="tiny">${st.current}-day streak, best ${st.best}. The PNG has a transparent background: add it over your own photo or video as a sticker or image layer in the app you post from. Nothing leaves this page until you share or post it.</p>
      </div></div></div>`;
  drawSharePreview();
}
function drawSharePreview() {
  const cv = $('#share-cv');
  if (!cv) return;
  const src = streakCanvas({ format: shareFmt });
  cv.width = src.width / 3; cv.height = src.height / 3;
  cv.getContext('2d').drawImage(src, 0, 0, cv.width, cv.height);
}
const shareCaption = () => { const st = streakInfo(); return `${st.current}-day practice streak on Flowa. ${myProps().map(p => p.name).join(', ')}. #flowarts #practice`; };
async function shareStreak(how) {
  const st = streakInfo(), name = `flowa-streak-${st.current}.png`;
  if (how === 'copy') { try { await navigator.clipboard.writeText(shareCaption()); toast('Caption copied.'); } catch { toast('Could not copy. The caption is in the note below.'); } return; }
  if (how === 'post') {
    const c = streakCanvas({ format: 'square', bg: true }), img = c.toDataURL('image/jpeg', 0.82);
    const r = await api('posts', 'POST', { body: shareCaption().replace(/ #\S+/g, ''), image: img });
    if (!r.ok) { toast(r.error); return; }
    closeModal(); grant(['share-streak', 'first-post']); location.hash = '#/timeline'; social.stale = true; render(); toast('Posted to the timeline.');
    return;
  }
  const blob = await canvasBlob(streakCanvas({ format: shareFmt }));
  if (how === 'share') {
    const file = new File([blob], name, { type: 'image/png' });
    if (navigator.canShare?.({ files: [file] })) {
      try { await navigator.share({ files: [file], title: 'My practice streak', text: shareCaption() }); grant(['share-streak']); } catch { /* cancelled */ }
      return;
    }
    toast('Sharing files is not available here. Download the PNG instead.');
    how = 'download';
  }
  const url = URL.createObjectURL(blob), a = document.createElement('a');
  a.href = url; a.download = name; a.click();
  setTimeout(() => URL.revokeObjectURL(url), 2000);
  grant(['share-streak']);
  toast('Overlay downloaded. Add it over your photo as a sticker.');
}
function closeModal() { const el = $('#modal'); el.hidden = true; el.innerHTML = ''; }

document.addEventListener('click', e => {
  const t = e.target.closest('[data-act]');
  if (e.target.id === 'modal') { closeModal(); return; }
  if (!t) return;
  const d = t.dataset;
  switch (d.act) {
    case 'share-streak': openShare(); break;
    case 'share-fmt': shareFmt = d.v; openShare(); break;
    case 'share-do': shareStreak(d.v); break;
    case 'modal-close': closeModal(); break;
  }
});
document.addEventListener('keydown', e => { if (e.key === 'Escape' && !$('#modal').hidden) closeModal(); });
