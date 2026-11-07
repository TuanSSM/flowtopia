'use strict';
// Community: the timeline of posts with likes and comments, notifications, the people
// list, and public profiles with social links. Talks to the endpoints in social.go.

const social = { posts: [], users: {}, more: false, loading: false, stale: true, key: '', openCmts: new Set(), draftBody: '', draftImg: '', notifs: [], nUsers: {}, unread: 0, nOpen: false, people: null, profile: {} };
const LINK_KINDS = {
  instagram: { name: 'Instagram', hint: 'instagram.com/…', url: h => `https://www.instagram.com/${h}/` },
  tiktok: { name: 'TikTok', hint: 'tiktok.com/@…', url: h => `https://www.tiktok.com/@${h}` },
  youtube: { name: 'YouTube', hint: 'youtube.com/@…', url: h => /^(channel|c|user)\//.test(h) ? `https://www.youtube.com/${h}` : `https://www.youtube.com/@${h.replace(/^@/, '')}` },
};
const linkURL = (k, h) => LINK_KINDS[k].url(encodeURIComponent(h).replace(/%2F/g, '/').replace(/%40/g, '@'));

function ago(t) {
  const s = Math.max(0, (Date.now() - new Date(t)) / 1000);
  if (s < 45) return 'just now';
  if (s < 3600) return `${Math.round(s / 60)} min`;
  if (s < 86400) return `${Math.round(s / 3600)} h`;
  if (s < 7 * 86400) return `${Math.round(s / 86400)} d`;
  return new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}
// Escaped text with @mentions and links made clickable, and line breaks kept. Mentions
// are only looked for outside links, so an @ inside a URL stays part of the URL.
function richText(s) {
  return esc(s).split(/(https?:\/\/[^\s<]+[^\s<.,)])/).map((part, i) => i % 2
    ? `<a href="${part}" target="_blank" rel="noopener noreferrer">${part}</a>`
    : part.replace(/(^|[^\w.-])@([a-z0-9][a-z0-9._-]{2,23})/gi, (m, pre, n) => `${pre}<a href="#/u/${n.toLowerCase()}">@${n}</a>`)).join('').replace(/\n/g, '<br>');
}
// A photo for a post: shrunk to fit the server's limit.
async function shrinkImage(file, max = 1280, q = 0.82) {
  const url = URL.createObjectURL(file), img = new Image();
  img.src = url;
  try { await img.decode(); } finally { URL.revokeObjectURL(url); }
  const c = document.createElement('canvas');
  // Smaller and rougher until it fits the server's limit (400,000 characters of data URL).
  for (let side = max, quality = q; ; quality -= 0.12) {
    const k = Math.min(1, side / Math.max(img.naturalWidth, img.naturalHeight));
    c.width = Math.round(img.naturalWidth * k); c.height = Math.round(img.naturalHeight * k);
    c.getContext('2d').drawImage(img, 0, 0, c.width, c.height);
    const out = c.toDataURL('image/jpeg', quality);
    if (out.length <= 390000 || side < 320) return out;
    if (quality < 0.5) { quality = q; side = Math.round(side * 0.75); }
  }
}

/* ---------- feed ---------- */
const feedPath = key => key.startsWith('post:') ? `posts/${key.slice(5)}` : key.startsWith('user:') ? `posts?user=${encodeURIComponent(key.slice(5))}` : 'posts';
async function loadFeed(key, append) {
  if (social.loading) return;
  social.loading = true; social.key = key; social.stale = false;
  if (!append) social.posts = [];
  const before = append && social.posts.length ? `${key === 'all' ? '?' : '&'}before=${social.posts[social.posts.length - 1].id}` : '';
  const r = await api(feedPath(key) + before);
  if (social.key !== key) return; // another feed was opened meanwhile
  social.loading = false;
  if (!r.ok) { if (r.status !== 404) toast(r.error); social.posts = []; social.more = false; render(); return; }
  social.posts = append ? [...social.posts, ...r.data.posts] : r.data.posts;
  Object.assign(social.users, r.data.users); social.more = r.data.more;
  render();
}
async function refreshPost(id) {
  const r = await api('posts/' + id);
  const i = social.posts.findIndex(p => p.id === id);
  if (!r.ok) { if (i >= 0) social.posts.splice(i, 1); }
  else { Object.assign(social.users, r.data.users); if (i >= 0) social.posts[i] = r.data.posts[0]; }
  render();
}
async function loadPeople() {
  const r = await api('users');
  if (r.ok) { social.people = r.data.users; render(); }
}
async function loadProfile(name) {
  social.profile[name] = { loading: true };
  const r = await api('users/' + encodeURIComponent(name));
  social.profile[name] = r.ok ? r.data : { error: r.error };
  render();
}

function feedHTML(key, empty) {
  if (social.key !== key || social.stale) { if (!social.loading || social.key !== key) { social.loading = false; loadFeed(key); } }
  if (social.loading && social.key === key && !social.posts.length) return '<div class="empty small">Loading…</div>';
  if (!social.posts.length) return `<div class="empty">${empty}</div>`;
  return social.posts.map(postCard).join('') + (social.more ? `<button class="btn" data-act="more-posts" style="justify-self:center">Load more</button>` : '');
}
function postCard(p) {
  const u = social.users[p.userId] || {}, mine = p.userId === me.id || me.role === 'admin';
  const cat = CATALOGS[p.prop], skill = p.skill && cat ? cat.skills.find(s => s.id === p.skill) : null;
  const chips = (p.prop ? `<span class="chip">${esc(propById(p.prop)?.name || p.prop)}</span>` : '') + (skill ? `<a class="chip" href="#/prop/${p.prop}/${skill.b}/${skill.id}" data-tip="${esc(skill.name)}">${skill.id}</a>` : '');
  let body;
  if (p.kind === 'milestone') {
    const keys = p.data?.achievements || [];
    body = `<div class="ms"><div class="row" style="gap:10px"><span class="badge-ic on">${icon('trophy')}</span><b>Unlocked ${keys.length === 1 ? 'an achievement' : keys.length + ' achievements'}</b></div>
      <div class="chips" style="margin-top:10px">${keys.map(k => { const b = badgeByKey(k); return `<span class="chip ok" data-tip="${esc(b?.desc || '')}">${icon(b?.icon || 'medal')}${esc(b?.name || k)}</span>`; }).join('')}</div></div>`;
  } else {
    body = `${p.body ? `<p class="post-body">${richText(p.body)}</p>` : ''}${p.image ? `<img class="post-img" src="${esc(p.image)}" alt="" loading="lazy">` : ''}`;
  }
  const comments = p.comments.map(c => {
    const cu = social.users[c.userId] || {}, canDel = c.userId === me.id || p.userId === me.id || me.role === 'admin';
    return `<div class="cmt">${avatarHTML(cu, 'sm')}<div class="grow"><span class="row" style="gap:6px"><a class="cmt-name" href="#/u/${esc(cu.username)}">${esc(cu.displayName || cu.username || 'Someone')}</a><span class="tiny">${ago(c.created)}</span><span class="grow"></span>${canDel ? `<button class="icon-btn xs" data-act="cmt-del" data-id="${c.id}" data-post="${p.id}" aria-label="Delete comment">${icon('x')}</button>` : ''}</span><span class="small cmt-body">${richText(c.body)}</span></div></div>`;
  }).join('');
  const showCmts = social.openCmts.has(p.id) || p.comments.length > 0;
  return `<article class="card post" data-post="${p.id}">
    <header class="row" style="gap:10px"><a href="#/u/${esc(u.username)}" aria-label="${esc(u.displayName || u.username)}">${avatarHTML(u)}</a><span class="grow" style="display:grid"><a class="post-name" href="#/u/${esc(u.username)}">${esc(u.displayName || u.username || 'Someone')}</a><span class="tiny">@${esc(u.username)} · <a class="muted" href="#/post/${p.id}">${ago(p.created)}</a></span></span>${chips ? `<span class="row" style="gap:4px">${chips}</span>` : ''}${mine ? `<button class="icon-btn" data-act="post-del" data-id="${p.id}" aria-label="Delete post">${icon('trash')}</button>` : ''}</header>
    ${body}
    <footer class="post-acts"><button class="act ${p.liked ? 'on' : ''}" data-act="like" data-id="${p.id}" aria-pressed="${p.liked}" aria-label="Like">${icon('heart')}<span class="num">${p.likes || ''}</span></button><button class="act" data-act="cmt-toggle" data-id="${p.id}" aria-label="Comment">${icon('comment')}<span class="num">${p.comments.length || ''}</span></button></footer>
    <div class="cmts" ${showCmts ? '' : 'hidden'}>${comments}<form class="cmt-form" data-id="${p.id}">${avatarHTML(me, 'sm')}<input type="text" name="body" maxlength="1000" placeholder="Write a comment. Use @name to mention someone." autocomplete="off" required><button class="icon-btn" type="submit" aria-label="Send comment">${icon('send')}</button></form></div></article>`;
}
function composer() {
  const cat = CATALOGS[ui.prop];
  return `<form class="card compose" id="compose-form"><div class="row" style="gap:10px;align-items:flex-start">${avatarHTML(me)}<div class="grow stack" style="gap:8px">
    <textarea name="body" maxlength="2000" rows="2" placeholder="What did you work on today? Use @name to mention someone." aria-label="New post">${esc(social.draftBody)}</textarea>
    ${social.draftImg ? `<div class="compose-img"><img src="${social.draftImg}" alt=""><button type="button" class="icon-btn" data-act="compose-img-rm" aria-label="Remove picture">${icon('x')}</button></div>` : ''}
    <div class="row wrap" style="gap:8px"><label class="btn sm">${icon('image')}Picture<input type="file" accept="image/*" data-change="compose-img" hidden></label>
      ${cat ? `<select name="skill" class="sm" aria-label="Tag a skill"><option value="">Tag a ${esc(curProp().name.toLowerCase())} skill</option>${skillOptions()}</select>` : ''}
      <span class="grow"></span><button class="btn primary sm" type="submit">${icon('send')}Post</button></div></div></div></form>`;
}
function peopleCard() {
  if (!social.people) { loadPeople(); return `<div class="card"><div class="card-h"><h2>People</h2></div><div class="empty small">Loading…</div></div>`; }
  const list = social.people.slice(0, 12);
  return `<div class="card people" data-tour="people"><div class="card-h"><h2 class="grow">People</h2><span class="sub">Most XP first</span></div>
    ${list.map((u, i) => { const s = u.stats || {}; return `<a class="person ${u.id === me.id ? 'me' : ''}" href="#/u/${esc(u.username)}"><span class="rank num">${i + 1}</span>${avatarHTML(u)}<span class="grow" style="display:grid"><b>${esc(u.displayName || u.username)}</b><span class="tiny">Level ${s.level || 1}${s.streak ? ` · ${s.streak}-day streak` : ''}</span></span><span class="tiny num">${(s.xp || 0).toLocaleString()} XP</span></a>`; }).join('')}</div>`;
}
function pageTimeline(single) {
  const key = single ? 'post:' + single : 'all', st = streakInfo();
  return `<div class="page-h"><div class="grow"><h1>${single ? 'Post' : 'Timeline'}</h1><p>${single ? '<a href="#/timeline">Back to the timeline</a>' : 'What everyone here is working on. Your milestones post themselves; turn that off in your profile.'}</p></div></div>
  <div class="grid g32 tl"><div class="stack" data-tour="feed">${single ? '' : composer()}${feedHTML(key, single ? 'That post is gone.' : 'Nothing here yet. Share what you worked on today.')}</div>
    <div class="stack tl-side">${!flags.game ? '' : st.atRisk ? `<div class="card risk-card"><span class="flame lit sm">${icon('flame')}</span><span class="grow small"><b>${st.current}-day streak at risk.</b> Log today’s practice to keep it.</span><a class="btn sm" href="#/practice">Log</a></div>` : levelCard(true)}${peopleCard()}</div></div>`;
}

/* ---------- public profile ---------- */
function pageUser(name) {
  name = (name || '').toLowerCase();
  const pr = social.profile[name];
  if (!pr) { loadProfile(name); return '<div class="empty small">Loading…</div>'; }
  if (pr.loading) return '<div class="empty small">Loading…</div>';
  if (pr.error) return `<div class="empty">${esc(pr.error)} <a href="#/timeline">Back to the timeline</a>.</div>`;
  const u = pr.user, s = u.stats || {}, L = Math.max(1, Math.floor(Number(s.level) || 1)), title = levelTitle(L), mine = u.username === me.username;
  const links = Object.keys(LINK_KINDS).filter(k => (u.links || {})[k]).map(k => `<a class="btn sm social-${k}" href="${esc(linkURL(k, u.links[k]))}" target="_blank" rel="noopener noreferrer">${icon(k)}${esc(LINK_KINDS[k].name)}</a>`).join('');
  const keys = (pr.achievements || []).slice().reverse();
  return `<div class="card prof-head"><div class="row wrap" style="gap:18px;align-items:flex-start">${avatarHTML(u, 'big')}
      <div class="grow" style="min-width:220px"><h1>${esc(u.displayName || u.username)}</h1><p class="muted">@${esc(u.username)}${u.role === 'admin' ? ' · Admin' : ''} · joined ${esc(new Date(u.created).toLocaleDateString(undefined, { month: 'long', year: 'numeric' }))}</p>
        ${u.bio ? `<p class="prof-bio">${richText(u.bio)}</p>` : mine ? '<p class="tiny" style="margin-top:6px">Add a bio and your social links in your profile settings.</p>' : ''}
        <div class="row wrap" style="gap:6px;margin-top:12px">${(u.props || []).map(id => `<span class="chip">${esc(propById(id)?.name || id)}</span>`).join('')}${links}</div></div>
      ${mine ? `<a class="btn" href="#/profile">${icon('edit')}Edit profile</a>` : ''}</div>
    ${flags.game ? `<div class="grid g4 prof-stats">${[['Level', `${L}<small> ${title}</small>`], ['XP', (s.xp || 0).toLocaleString()], ['Streak', `${s.streak || 0}<small> ${s.streak === 1 ? 'day' : 'days'} · best ${s.best || 0}</small>`], ['Clean skills', s.clean || 0]].map(([n, v]) => `<div class="stat"><div class="lbl">${n}</div><div class="val num">${v}</div></div>`).join('')}</div>` : ''}</div>
  <div class="grid g32 tl" style="margin-top:16px"><div class="stack"><div class="card-h" style="margin:0"><h2 class="grow">Posts</h2><span class="sub num">${pr.posts} ${pr.posts === 1 ? 'post' : 'posts'} · ${pr.likes} ${pr.likes === 1 ? 'like' : 'likes'} received</span></div>${feedHTML('user:' + name, mine ? 'You have not posted yet.' : 'No posts yet.')}</div>
    ${!flags.game ? '' : `<div class="card"><div class="card-h"><h2 class="grow">Badges</h2><span class="sub num">${keys.length}</span></div>${keys.length ? `<div class="badge-grid one">${keys.map(a => { const b = badgeByKey(a.key); return `<div class="badge on" data-tip="${esc(b?.desc || '')}"><span class="badge-ic on">${icon(b?.icon || 'medal')}</span><span class="grow"><b>${esc(b?.name || a.key)}</b><span class="tiny">${esc(new Date(a.at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }))}</span></span></div>`; }).join('')}</div>` : '<div class="empty small">No badges yet.</div>'}</div>`}</div>`;
}

/* ---------- notifications ---------- */
async function loadNotifs() {
  if (!me) return;
  const r = await api('notifications');
  if (!r.ok) return;
  social.notifs = r.data.items; social.nUsers = r.data.users; social.unread = r.data.unread;
  renderBell();
}
function notifText(n) {
  const a = social.nUsers[n.actorId], who = a ? esc(a.displayName || a.username) : 'Someone';
  switch (n.kind) {
    case 'like': return `<b>${who}</b> liked your post`;
    case 'comment': return `<b>${who}</b> commented on your post`;
    case 'mention': return `<b>${who}</b> mentioned you`;
    case 'achievement': { const b = badgeByKey(n.text); return `Achievement unlocked: <b>${esc(b?.name || n.text)}</b>`; }
    default: return esc(n.text);
  }
}
function bellHTML() {
  if (!me || !me.onboarded || !flags.community) return '';
  const st = loaded && flags.game ? streakInfo() : { atRisk: false }, n = social.unread + (st.atRisk ? 1 : 0);
  const items = social.notifs.map(n => `<a class="ntf ${n.read ? '' : 'unread'}" href="${n.postId ? '#/post/' + n.postId : '#/achievements'}" data-act="ntf-go" data-id="${n.id}">${n.kind === 'achievement' ? `<span class="badge-ic on sm">${icon(badgeByKey(n.text)?.icon || 'medal')}</span>` : avatarHTML(social.nUsers[n.actorId], 'sm')}<span class="grow"><span class="small">${notifText(n)}</span><span class="tiny">${ago(n.created)}</span></span></a>`).join('');
  const panel = !social.nOpen ? '' : `<div class="ntf-panel" role="dialog" aria-label="Notifications"><div class="row" style="padding:10px 12px 6px"><b class="grow">Notifications</b>${social.unread ? `<button class="btn sm" data-act="ntf-read-all">Mark all read</button>` : ''}</div>
    ${st.atRisk ? `<a class="ntf unread" href="#/practice" data-act="ntf-go"><span class="flame lit sm">${icon('flame')}</span><span class="grow"><span class="small"><b>Your ${st.current}-day streak ends tonight.</b> Log today’s practice to keep it.</span></span></a>` : ''}
    ${items || '<div class="empty small" style="margin:8px">Nothing yet. Likes, comments, mentions and achievements show up here.</div>'}</div>`;
  return `<button class="icon-btn bell ${n ? 'has' : ''}" data-act="bell" aria-label="Notifications${n ? `, ${n} unread` : ''}" aria-expanded="${social.nOpen}">${icon('bell')}${n ? `<span class="bell-n num">${n > 9 ? '9+' : n}</span>` : ''}</button>${panel}`;
}
function renderBell() { const el = $('#bell'); if (el) el.innerHTML = bellHTML(); }
setInterval(() => { if (me && flags.community && document.visibilityState === 'visible' && !social.nOpen) loadNotifs(); }, 60000);

/* ---------- events ---------- */
document.addEventListener('click', async e => {
  if (social.nOpen && !e.target.closest('#bell')) { social.nOpen = false; renderBell(); }
  const t = e.target.closest('[data-act]');
  if (!t) return;
  const d = t.dataset, id = Number(d.id), post = social.posts.find(p => p.id === id);
  switch (d.act) {
    case 'bell': social.nOpen = !social.nOpen; renderBell(); if (social.nOpen) loadNotifs(); break;
    case 'ntf-go':
      social.nOpen = false;
      if (d.id) { const n = social.notifs.find(x => x.id === id); if (n && !n.read) { n.read = true; social.unread = Math.max(0, social.unread - 1); api('notifications/read', 'PUT', { ids: [id] }); } }
      renderBell(); break;
    case 'ntf-read-all': social.notifs.forEach(n => n.read = true); social.unread = 0; renderBell(); await api('notifications/read', 'PUT', {}); break;
    case 'like': {
      if (!post) break;
      post.liked = !post.liked; post.likes += post.liked ? 1 : -1; render();
      const r = await api(`posts/${id}/like`, 'POST');
      if (r.ok) { post.liked = r.data.liked; post.likes = r.data.likes; } else toast(r.error);
      render(); break;
    }
    case 'cmt-toggle':
      if (social.openCmts.has(id)) social.openCmts.delete(id); else social.openCmts.add(id);
      render(); $(`.post[data-post="${id}"] .cmt-form input`)?.focus(); break;
    case 'post-del':
      if (post && confirm('Delete this post?')) { const r = await api('posts/' + id, 'DELETE'); if (r.ok) { social.posts = social.posts.filter(p => p.id !== id); render(); } else toast(r.error); }
      break;
    case 'cmt-del':
      if (confirm('Delete this comment?')) { const r = await api('comments/' + id, 'DELETE'); if (r.ok) refreshPost(Number(d.post)); else toast(r.error); }
      break;
    case 'more-posts': loadFeed(social.key, true); break;
    case 'compose-img-rm': social.draftImg = ''; render(); break;
  }
});
document.addEventListener('input', e => { if (e.target.closest('#compose-form') && e.target.name === 'body') social.draftBody = e.target.value; });
document.addEventListener('change', async e => {
  if (e.target.dataset.change !== 'compose-img') return;
  const f = e.target.files[0]; e.target.value = '';
  if (!f) return;
  try { social.draftImg = await shrinkImage(f); } catch { toast('That file could not be read as a picture.'); }
  render();
});
document.addEventListener('submit', async e => {
  const f = e.target;
  if (f.id === 'compose-form') {
    e.preventDefault();
    const body = f.body.value.trim();
    if (!body && !social.draftImg) { toast('Write something or add a picture.'); return; }
    const btn = f.querySelector('button[type=submit]'); btn.disabled = true;
    const r = await api('posts', 'POST', { body, prop: f.skill?.value ? ui.prop : '', skill: f.skill?.value || '', image: social.draftImg });
    btn.disabled = false;
    if (!r.ok) { toast(r.error); return; }
    social.draftBody = ''; social.draftImg = '';
    Object.assign(social.users, r.data.users);
    if (social.key === 'all') social.posts.unshift(r.data.posts[0]); else social.stale = true;
    render(); grant(['first-post']); toast('Posted.');
  } else if (f.classList.contains('cmt-form')) {
    e.preventDefault();
    const id = Number(f.dataset.id), body = f.body.value.trim();
    if (!body) return;
    const r = await api(`posts/${id}/comments`, 'POST', { body });
    if (!r.ok) { toast(r.error); return; }
    Object.assign(social.users, r.data.users);
    const i = social.posts.findIndex(p => p.id === id);
    if (i >= 0) social.posts[i] = r.data.posts[0];
    social.openCmts.add(id); render();
  }
});
