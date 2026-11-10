'use strict';
// Community: the timeline of posts with likes and comments, notifications, the people
// list, and public profiles with social links. Talks to the endpoints in social.go.

// sending: the forms whose answer is still on its way; keys: the Idempotency-Key each unsent form
// last used, so trying the very same thing again cannot make it twice.
const socialBlank = () => ({ full: {}, fullReplies: {}, replyTo: null, editing: 0, focus: 0, posts: [], users: {}, more: false, loading: false, stale: true, key: '', openCmts: new Set(), draftBody: '', draftImg: '', draftStill: false, draftTags: [], draftCombo: null, draftLinks: [], linkOffer: '', played: new Set(), refreshing: new Set(), pickOpen: false, pickQ: '', pickIdx: 0, notifs: [], nUsers: {}, unread: 0, nOpen: false, people: null, profile: {}, sending: new Set(), keys: {} });
// owner: the account all of this belongs to (0 for a visitor); gen goes up each time it is thrown
// away, so an answer that was asked for by the previous account is not shown to the next.
const social = Object.assign(socialBlank(), { owner: 0, gen: 0 });
// Everything here belongs to one signed-in person. When the session ends or another account signs
// in on this tab it all goes: drafts, the feed, notifications, and a tour waiting to be replayed.
function resetSocial() {
  Object.assign(social, socialBlank(), { owner: me?.id || 0, gen: social.gen + 1 });
  delete social.queue; ui.replay = false;
}
function sameOwner() { if (social.owner !== (me?.id || 0)) resetSocial(); }
const MAX_TAGS = 10;
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
    const g = c.getContext('2d');
    // JPEG has no transparency: without a white sheet under it, a see-through picture turns black.
    g.fillStyle = '#fff'; g.fillRect(0, 0, c.width, c.height);
    g.drawImage(img, 0, 0, c.width, c.height);
    const out = c.toDataURL('image/jpeg', quality);
    if (out.length <= 390000 || side < 320) return out;
    if (quality < 0.5) { quality = q; side = Math.round(side * 0.75); }
  }
}

/* ---------- feed ---------- */
const feedPath = key => key.startsWith('post:') ? `posts/${key.slice(5)}` : key.startsWith('user:') ? `posts?user=${encodeURIComponent(key.slice(5))}` : key === 'bookmarks' ? 'bookmarks' : 'posts';
async function loadFeed(key, append) {
  if (social.loading) return;
  social.loading = true; social.key = key; social.stale = false;
  if (!append) social.posts = [];
  const before = append && social.posts.length ? `${key === 'all' ? '?' : '&'}before=${social.posts[social.posts.length - 1].id}` : '';
  const gen = social.gen, r = await api(feedPath(key) + before);
  if (social.gen !== gen || social.key !== key) return; // another feed was opened meanwhile, or another account
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
  const u = social.users[p.userId] || {}, mine = p.userId === me.id || canDo('content.delete');
  const tags = p.data?.tags || (p.skill ? [{ prop: p.prop, skill: p.skill }] : []);
  const chips = tags.map(t => trickChip(t.prop, t.skill)).join('');
  const combo = p.data?.combo ? comboBlock(p.data.combo, p.userId === me.id) : '';
  let body;
  if (p.kind === 'milestone') {
    const keys = p.data?.achievements || [];
    // With avatars on, the author's avatar in a pose that matches the first badge's group.
    const stk = stickerImg(badgeByKey(keys[0])?.group, u.avatarCfg, p.prop, 'sticker ms-sticker');
    body = `<div class="ms ${stk ? 'has-sticker' : ''}">${stk}<div class="row" style="gap:10px"><span class="badge-ic on">${icon('trophy')}</span><b>Unlocked ${keys.length === 1 ? 'an achievement' : keys.length + ' achievements'}</b></div>
      <div class="chips" style="margin-top:10px">${keys.map(k => { const b = badgeByKey(k); return `<span class="chip ok" data-tip="${esc(b?.desc || '')}">${icon(b?.icon || 'medal')}${esc(b?.name || k)}</span>`; }).join('')}</div></div>`;
  } else {
    body = `${p.body ? `<p class="post-body">${richText(p.body)}</p>` : ''}${(p.links || []).map(c => linkCardHTML(p, c)).join('')}${combo}${p.image ? `<img class="post-img" src="${esc(p.image)}" alt="" loading="lazy">` : ''}${chips ? `<div class="chips post-tags">${chips}</div>` : ''}`;
  }
  const thread = threadHTML(p);
  return `<article class="card post" data-post="${p.id}">
    <header class="row" style="gap:10px"><a href="#/u/${esc(u.username)}" aria-label="${esc(u.displayName || u.username)}">${avatarHTML(u)}</a><span class="grow" style="display:grid"><a class="post-name" href="#/u/${esc(u.username)}">${esc(u.displayName || u.username || 'Someone')}</a><span class="tiny">@${esc(u.username)} · <a class="muted" href="#/post/${p.id}">${ago(p.created)}</a></span></span>${mine ? `<button class="icon-btn" data-act="post-del" data-id="${p.id}" aria-label="Delete post">${icon('trash')}</button>` : ''}${canReport(p.userId) ? `<button class="icon-btn" data-act="report" data-kind="post" data-id="${p.id}" data-what="this post" aria-label="Report post" data-tip="Report">${icon('flag')}</button>` : ''}</header>
    ${p.hidden ? `<p class="notice warn">${icon('lock')} Hidden by a moderator: ${esc(p.hidden)}${p.userId === me.id ? '. Only you and the moderators can see it.' : ''}${canDo('post.hide') && p.userId !== me.id ? ` <button class="btn sm" data-act="mod-do" data-do="restore" data-kind="post" data-id="${p.id}">Restore</button>` : ''}</p>` : ''}
    ${body}
    <footer class="post-acts"><button class="act ${p.liked ? 'on' : ''}" data-act="like" data-id="${p.id}" aria-pressed="${p.liked}" aria-label="Like">${icon('heart')}<span class="num">${p.likes || ''}</span></button><button class="act" data-act="cmt-toggle" data-id="${p.id}" aria-label="Comment">${icon('comment')}<span class="num">${p.commentCount || ''}</span></button>${bmButton('post', String(p.id), true)}</footer>
    ${thread}</article>`;
}
/* ---------- comments and replies ---------- */
const EDIT_MS = 15 * 60e3;
// The comments shown for a post: every loaded one when the post was expanded, else the newest.
const rootsOf = p => social.full[p.id]?.list || p.comments || [];
const repliesOf = c => social.fullReplies[c.id]?.list || c.replies || [];
// Every copy of a comment held in the page (the feed's preview and any expanded list).
function eachComment(id, fn) {
  const visit = list => { for (const c of list || []) { if (c.id === id) fn(c); visit(c.replies); } };
  for (const p of social.posts) visit(p.comments);
  for (const k in social.full) visit(social.full[k].list);
  for (const k in social.fullReplies) visit(social.fullReplies[k].list);
}
// A form whose answer is on its way keeps its button off and ignores another submit. The set is
// state, not a property of the button, so a redraw in between cannot switch the button back on.
const sendOff = k => social.sending.has(k) ? 'disabled' : '';
function setSending(k, on) {
  if (on) social.sending.add(k); else social.sending.delete(k);
  // The page is not redrawn while a text field has the cursor, so the button on show is set too.
  const b = document.querySelector(`form[data-k="${k}"] button[type=submit]`);
  if (b) b.disabled = on;
}
// The Idempotency-Key for what a form is about to send. Trying the very same thing again (after a
// lost connection, say) reuses the key, so the server answers with the post or comment it already
// made; anything edited in between is a new thing and gets a new key.
function onceKey(k, send) {
  const sig = JSON.stringify(send), had = social.keys[k];
  if (had && had.sig === sig) return had.key;
  const key = Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join('');
  social.keys[k] = { sig, key };
  return key;
}
// True while something typed here has not been sent: a post being put together, a comment or
// reply, or a comment being edited. The page asks before it is closed or reloaded with one.
function socialDraft() {
  if (!me) return false;
  if (social.draftBody.trim() || social.draftImg || social.draftTags.length || social.draftCombo || social.draftLinks.length) return true;
  return [...document.querySelectorAll('.cmt-form, .cmt-edit')].some(f => f.body && f.body.value.trim() !== f.body.defaultValue.trim());
}
function commentHTML(p, c, reply) {
  const cu = social.users[c.userId] || {}, mine = c.userId === me.id, canDel = !c.deleted && (mine || p.userId === me.id || canDo('content.delete'));
  const canEdit = mine && !c.deleted && Date.now() - new Date(c.created) < EDIT_MS;
  const editing = social.editing === c.id;
  const body = c.deleted ? `<span class="small cmt-gone">${c.removed ? 'Comment removed by a moderator' : 'Comment deleted'}</span>`
    : editing ? `<form class="cmt-edit" data-k="e:${c.id}" data-id="${c.id}" data-post="${p.id}"><textarea name="body" maxlength="1000" rows="2" aria-label="Edit comment">${esc(c.body)}</textarea><span class="row" style="gap:6px"><button class="btn sm primary" type="submit" ${sendOff(`e:${c.id}`)}>Save</button><button class="btn sm" type="button" data-act="cmt-edit-cancel">Cancel</button></span></form>`
    : `<span class="small cmt-body">${richText(c.body)}</span>`;
  const acts = c.deleted || editing ? '' : `<span class="cmt-acts"><button class="cmt-a ${c.liked ? 'on' : ''}" data-act="cmt-like" data-id="${c.id}" aria-pressed="${c.liked}" aria-label="Like comment">${icon('heart')}${c.likes ? `<span class="num">${c.likes}</span>` : ''}</button><button class="cmt-a" data-act="cmt-reply" data-id="${c.id}" data-post="${p.id}" data-root="${reply ? c.parentId : c.id}">Reply</button>${canEdit ? `<button class="cmt-a" data-act="cmt-edit" data-id="${c.id}">Edit</button>` : ''}${canDel ? `<button class="cmt-a" data-act="cmt-del" data-id="${c.id}" data-post="${p.id}">Delete</button>` : ''}${canReport(c.userId) ? `<button class="cmt-a" data-act="report" data-kind="comment" data-id="${c.id}" data-what="this comment">Report</button>` : ''}${c.hidden && canDo('post.hide') && !mine ? `<button class="cmt-a" data-act="mod-do" data-do="restore" data-kind="comment" data-id="${c.id}">Restore</button>` : ''}</span>${c.hidden ? `<span class="tiny cmt-hidden">${icon('lock')} Hidden by a moderator: ${esc(c.hidden)}</span>` : ''}`;
  return `<div class="cmt ${reply ? 'reply' : ''} ${social.focus === c.id ? 'focus' : ''}" id="c-${c.id}">${avatarHTML(cu, 'sm')}<div class="grow"><span class="row" style="gap:6px"><a class="cmt-name" href="#/u/${esc(cu.username)}">${esc(cu.displayName || cu.username || 'Someone')}</a><span class="tiny">${ago(c.created)}${c.edited ? ' · edited' : ''}</span></span>${body}${acts}</div></div>`;
}
function replyForm(p, root) {
  const r = social.replyTo;
  if (!r || r.root !== root) return '';
  return `<form class="cmt-form reply-form" data-k="c:${p.id}:${r.parent}" data-id="${p.id}" data-parent="${r.parent}">${avatarHTML(me, 'sm')}<input type="text" name="body" maxlength="1000" value="${esc(r.prefill)}" aria-label="Reply to ${esc(r.name)}" autocomplete="off" required><button class="icon-btn" type="submit" aria-label="Send reply" ${sendOff(`c:${p.id}:${r.parent}`)}>${icon('send')}</button><button class="icon-btn" type="button" data-act="reply-cancel" aria-label="Cancel reply">${icon('x')}</button></form>`;
}
function threadHTML(p) {
  const roots = rootsOf(p), full = social.full[p.id];
  // Of what is on the page, the comments the post's own count includes: not deleted, not hidden.
  let shown = 0;
  const items = roots.map(c => {
    const reps = repliesOf(c), hidden = (c.replyCount || 0) - reps.length, loading = social.fullReplies[c.id]?.loading;
    shown += [c, ...reps].filter(x => !x.deleted && !x.hidden).length;
    return `<div class="thr">${commentHTML(p, c, false)}${reps.map(r => commentHTML(p, r, true)).join('')}${hidden > 0 ? `<button class="more-replies" data-act="more-replies" data-id="${c.id}" ${loading ? 'disabled' : ''}>View ${hidden} more ${hidden === 1 ? 'reply' : 'replies'}</button>` : ''}${replyForm(p, c.id)}</div>`;
  }).join('');
  // A page of comments says whether another follows. A preview, or one thread opened from a
  // notification, offers the rest only when the post has comments that are not on the page.
  const more = full && !full.thread ? full.more : (p.commentCount || 0) > shown;
  const open = social.openCmts.has(p.id) || roots.length > 0;
  return `<div class="cmts" ${open ? '' : 'hidden'}>${more ? `<button class="more-replies" data-act="more-comments" data-id="${p.id}" ${full?.loading ? 'disabled' : ''}>${full ? 'View more comments' : `View all ${p.commentCount} comments`}</button>` : ''}${items}<form class="cmt-form" data-k="c:${p.id}:0" data-id="${p.id}">${avatarHTML(me, 'sm')}<input type="text" name="body" maxlength="1000" placeholder="${me.mutedUntil ? 'You are muted' : 'Write a comment. Use @name to mention someone.'}" autocomplete="off" required ${me.mutedUntil ? 'disabled' : ''}><button class="icon-btn" type="submit" aria-label="Send comment" ${sendOff(`c:${p.id}:0`)}>${icon('send')}</button></form></div>`;
}
// Loads the next page of a post's comments; the first call replaces the feed's preview.
async function moreComments(postId) {
  const f = social.full[postId], after = f && !f.thread ? f.list[f.list.length - 1]?.id || 0 : 0;
  social.full[postId] = { ...(f || { list: [] }), loading: true }; render();
  const r = await api(`posts/${postId}/comments?after=${after}`);
  if (!r.ok) { toast(r.error); social.full[postId].loading = false; render(); return; }
  Object.assign(social.users, r.data.users);
  const old = f && !f.thread ? f.list : [];
  social.full[postId] = { list: [...old, ...r.data.comments], more: r.data.more };
  render();
}
async function moreReplies(rootId) {
  const f = social.fullReplies[rootId], after = f?.list?.length && !f.thread ? f.list[f.list.length - 1].id : 0;
  social.fullReplies[rootId] = { ...(f || { list: [] }), loading: true }; render();
  const r = await api(`comments/${rootId}/replies?after=${after}`);
  if (!r.ok) { toast(r.error); social.fullReplies[rootId].loading = false; render(); return; }
  Object.assign(social.users, r.data.users);
  const old = f && !f.thread ? f.list : [];
  social.fullReplies[rootId] = { list: [...old, ...r.data.comments], more: r.data.more };
  eachComment(rootId, c => { c.replyCount = Math.max(c.replyCount || 0, social.fullReplies[rootId].list.length + (r.data.more ? 1 : 0)); });
  render();
}
// Opened from a notification: the thread holding the comment, scrolled into view.
async function loadThread(postId, commentId) {
  social.focus = commentId;
  const r = await api(`comments/${commentId}/thread`);
  if (!r.ok) { toast(r.error); return; }
  const root = r.data.comments[0];
  Object.assign(social.users, r.data.users);
  social.full[postId] = { list: [root], thread: true };
  social.fullReplies[root.id] = { list: root.replies || [], more: false, thread: true };
  social.openCmts.add(postId);
  render();
  setTimeout(() => document.getElementById('c-' + commentId)?.scrollIntoView({ block: 'center' }), 60);
}
/* ---------- tricks and combos on posts ---------- */
const skillOf = (prop, id) => CATALOGS[prop]?.skills.find(s => s.id === id);
// A chip for a tagged trick: its id and name, linking to the skill; the prop in the tooltip.
// In the composer the chip is not a link, so its remove button does not open the skill.
function trickChip(prop, id, extra = '', link = true) {
  const s = skillOf(prop, id), pn = propById(prop)?.name || prop;
  const tag = s && link ? `a class="chip trick" href="#/prop/${prop}/${s.b}/${s.id}"` : 'span class="chip trick"';
  return `<${tag} data-tip="${esc(pn)}${s ? ': ' + esc(s.name) : ''}"><b>${esc(id)}</b>${s ? `<span class="trick-n">${esc(s.name)}</span>` : ''}${extra}</${s && link ? 'a' : 'span'}>`;
}
const ROLE_NAMES = { reliable: 'Reliable', signature: 'Signature', stretch: 'Stretch' };
// A combo attached to a post: name, role and the sequence as chips, in order.
function comboBlock(c, mine) {
  const seq = c.seq || [], reps = c.reps || [];
  return `<div class="post-combo"><div class="row wrap" style="gap:8px"><span class="chip">${icon('link')}Combo</span><b class="grow">${esc(c.name)}</b>${c.role ? `<span class="chip">${esc(ROLE_NAMES[c.role] || c.role)}</span>` : ''}<span class="tiny num">${esc(propById(c.prop)?.name || c.prop)} · ${seq.length} ${seq.length === 1 ? 'skill' : 'skills'}</span>${mine && c.id ? `<a class="small" href="#/combos/${esc(c.id)}">Open</a>` : ''}</div>
    <div class="combo-seq">${seq.map((id, i) => trickChip(c.prop, id, reps[i] > 1 ? `<i class="num">×${reps[i]}</i>` : '') + (i < seq.length - 1 ? icon('right') : '')).join('')}</div></div>`;
}
const tagKey = t => `${t.prop}:${t.skill}`;
// render() leaves the page alone while a text field has focus (so typing survives a save), so
// anything that must redraw the composer steps out of its text box first.
function leaveCompose() { const a = document.activeElement; if (a && a.closest?.('#compose-form') && /^(TEXTAREA|INPUT)$/.test(a.tagName)) a.blur(); }
const hasTag = t => social.draftTags.some(x => tagKey(x) === tagKey(t));
// Tricks worth suggesting: the last session's focus and the skills levelled up this week.
function suggestedTags() {
  const out = [], seen = new Set(social.draftTags.map(tagKey));
  const add = (prop, skill, why) => { const t = { prop, skill, why }; if (skillOf(prop, skill) && !seen.has(tagKey(t))) { seen.add(tagKey(t)); out.push(t); } };
  const last = allSessions().filter(s => (s.focus || []).length).sort((a, b) => b.date.localeCompare(a.date))[0];
  if (last) for (const id of last.focus) add(last.prop, id, last.date === dayKey() ? 'Today' : 'Last session');
  const week = Date.now() - 7 * 864e5;
  for (const e of allEvents()) if (e.to > e.from && new Date(e.t) > week) add(e.prop, e.id, 'Levelled up');
  return out.slice(0, 6);
}
// Everything the picker can offer, filtered by the query: tricks from every prop and the user's combos.
function pickItems(q) {
  q = q.trim().toLowerCase();
  const items = [];
  if (!q) for (const t of suggestedTags()) items.push({ kind: 'skill', group: 'Suggested', prop: t.prop, id: t.skill, why: t.why });
  for (const p of myProps()) {
    const d = docOf(p.id);
    for (const c of d ? d.state.combos : []) {
      if ((c.seq || []).length < 2) continue;
      if (!q || c.name.toLowerCase().includes(q) || c.seq.some(id => id.toLowerCase() === q)) items.push({ kind: 'combo', group: 'Combos', prop: p.id, combo: c });
    }
  }
  for (const p of myProps()) {
    const cat = CATALOGS[p.id];
    if (!cat) continue;
    const hits = cat.skills.filter(s => !q || s.id.toLowerCase().startsWith(q) || s.name.toLowerCase().includes(q));
    for (const s of (q ? hits : hits.slice(0, 6))) items.push({ kind: 'skill', group: p.name, prop: p.id, id: s.id });
  }
  return items.slice(0, 60);
}
function pickAdd(item) {
  const inSearch = document.activeElement?.id === 'pick-q';
  leaveCompose();
  if (item.kind === 'combo') {
    const c = item.combo;
    social.draftCombo = social.draftCombo?.id === c.id ? null : { id: c.id, name: c.name, role: c.role || '', prop: item.prop, seq: [...c.seq], reps: c.seq.map((_, i) => (c.reps || [])[i] || 1) };
  } else {
    const t = { prop: item.prop, skill: item.id };
    if (hasTag(t)) social.draftTags = social.draftTags.filter(x => tagKey(x) !== tagKey(t));
    else if (social.draftTags.length < MAX_TAGS) social.draftTags.push(t);
    else toast(`Up to ${MAX_TAGS} tricks on a post.`);
  }
  render();
  if (inSearch) $('#pick-q')?.focus();
}
function pickListHTML() {
  const items = pickItems(social.pickQ);
  social.pickIdx = Math.min(social.pickIdx, Math.max(0, items.length - 1));
  let last = '';
  const rows = items.map((it, i) => {
    const head = it.group !== last ? `<div class="pick-h">${esc(it.group)}</div>` : '';
    last = it.group;
    const on = it.kind === 'combo' ? social.draftCombo?.id === it.combo.id : hasTag({ prop: it.prop, skill: it.id });
    const label = it.kind === 'combo'
      ? `<span class="tag">${icon('link')}</span><span class="grow"><b>${esc(it.combo.name)}</b><span class="tiny"> ${it.combo.seq.join(' ')}</span></span>`
      : `<span class="tag">${esc(it.id)}</span><span class="grow">${esc(skillOf(it.prop, it.id)?.name || it.id)}${it.why ? `<span class="tiny"> · ${esc(it.why)}</span>` : ''}</span>`;
    return `${head}<button type="button" class="pick-i ${i === social.pickIdx ? 'on' : ''} ${on ? 'picked' : ''}" data-act="pick-add" data-i="${i}" role="option" aria-selected="${on}">${label}${on ? icon('check') : ''}</button>`;
  }).join('');
  return rows || '<div class="empty small">No matches. Tricks come from the skill maps of your props.</div>';
}
function picker() {
  return `<div class="picker" role="dialog" aria-label="Tag tricks and combos"><div class="row" style="gap:6px"><input type="search" id="pick-q" value="${esc(social.pickQ)}" placeholder="Search tricks and combos" autocomplete="off" aria-label="Search tricks and combos"><button type="button" class="btn sm" data-act="pick-close">Done</button></div>
    <div class="pick-list" role="listbox">${pickListHTML()}</div></div>`;
}
function refreshPicker() { const l = $('.pick-list'); if (l) { l.innerHTML = pickListHTML(); $('.pick-i.on')?.scrollIntoView({ block: 'nearest' }); } }
/* ---------- links to Instagram, TikTok and YouTube ---------- */
// Where a link might be in the text. Whether it can be a card is decided by linkCheck (links.js),
// which reads a link the way parseLink in main.go does.
const LINK_RE = /(?:https?:\/\/)?(?:www\.|m\.)?(?:youtube\.com\/(?:watch\?\S*v=|shorts\/|live\/)|youtu\.be\/|instagram\.com\/(?:[\w.]+\/)?(?:p|reels?|tv)\/|(?:vm|vt)\.tiktok\.com\/|tiktok\.com\/(?:@[\w.]+\/video\/|t\/))\S+/gi;
const SITE_NAMES = { youtube: 'YouTube', tiktok: 'TikTok', instagram: 'Instagram' };
const siteOf = u => /tiktok/i.test(u) ? 'tiktok' : /instagram/i.test(u) ? 'instagram' : 'youtube';
// The first link in the text that the server would take as a card and that is not attached yet.
function linkOffer(text) {
  if (!flags.linkCards || social.draftLinks.length >= 4) return '';
  const have = new Set(social.draftLinks.map(u => linkCheck.parseLink(u)?.url));
  for (let u of text.match(LINK_RE) || []) {
    // A link at the end of a sentence drags the full stop or the bracket along: try without it.
    for (; u; u = /[.,;:!?)\]}'"]$/.test(u) ? u.slice(0, -1) : '') {
      const c = linkCheck.parseLink(u);
      if (c && !have.has(c.url)) return u;
      if (c) break;
    }
  }
  return '';
}
function linkCardHTML(p, c) {
  const site = SITE_NAMES[c.site] || c.site, tall = c.ratio === '9:16';
  if (c.status === 'pending' && !social.refreshing.has(p.id)) { social.refreshing.add(p.id); setTimeout(() => { social.refreshing.delete(p.id); refreshPost(p.id); }, 3000); }
  const playing = social.played.has(`${p.id}:${c.url}`);
  const media = playing ? `<div class="lc-media ${tall ? 'tall' : ''}"><iframe src="https://www.youtube-nocookie.com/embed/${esc(c.id)}?autoplay=1" title="${esc(c.title || 'YouTube video')}" referrerpolicy="no-referrer" allow="autoplay; encrypted-media; picture-in-picture" allowfullscreen sandbox="allow-scripts allow-same-origin allow-presentation"></iframe></div>`
    : c.thumb ? `<div class="lc-media ${tall ? 'tall' : ''}"><img src="${esc(c.thumb)}" alt="" loading="lazy">${c.site === 'youtube' && flags.videoEmbeds ? `<button class="lc-play" data-act="yt-play" data-id="${p.id}" data-url="${esc(c.url)}" aria-label="Play here">${icon('play')}</button>` : ''}</div>` : '';
  const meta = c.status === 'pending' ? '<span class="tiny">Getting the preview…</span>' : `<b class="lc-title">${esc(c.title || `${site} ${c.kind === 'reel' ? 'reel' : c.kind === 'short' ? 'short' : c.site === 'instagram' ? 'post' : 'video'}`)}</b>${c.author ? `<span class="tiny">${esc(c.author)}</span>` : ''}`;
  return `<div class="lc ${c.thumb ? '' : 'plain'}">${media}<a class="lc-meta" href="${esc(c.url)}" target="_blank" rel="noopener noreferrer"><span class="lc-site social-${esc(c.site)}">${icon(c.site)}${esc(site)}</span>${meta}<span class="tiny lc-open">Open on ${esc(site)} ${icon('ext')}</span></a></div>`;
}
const mutedNote = () => me.mutedUntil ? `You are muted until ${fmtWhen(me.mutedUntil)}${me.muteReason ? `: ${me.muteReason}` : ''}. You can still read and like.` : '';
function composer() {
  if (me.mutedUntil) return `<div class="card notice warn muted-note">${icon('lock')} ${esc(mutedNote())}</div>`;
  const chips = social.draftTags.map(t => trickChip(t.prop, t.skill, `<button type="button" class="x" data-act="tag-rm" data-prop="${t.prop}" data-skill="${esc(t.skill)}" aria-label="Remove ${esc(t.skill)}">${icon('x')}</button>`, false)).join('')
    + social.draftLinks.map(u => `<span class="chip trick link-chip social-${siteOf(u)}">${icon(siteOf(u))}<span class="trick-n">${esc(u.replace(/^https?:\/\/(www\.)?/, '').slice(0, 34))}</span><button type="button" class="x" data-act="link-rm" data-url="${esc(u)}" aria-label="Remove link">${icon('x')}</button></span>`).join('')
    + (social.draftCombo ? `<span class="chip trick combo-chip">${icon('link')}<span class="trick-n">${esc(social.draftCombo.name)}</span><button type="button" class="x" data-act="combo-rm-draft" aria-label="Remove combo">${icon('x')}</button></span>` : '');
  const sugg = social.pickOpen ? [] : suggestedTags();
  return `<form class="card compose" id="compose-form" data-k="post"><div class="row" style="gap:10px;align-items:flex-start">${avatarHTML(me)}<div class="grow stack" style="gap:8px">
    <textarea name="body" maxlength="2000" rows="2" placeholder="What did you work on today? Use @name to mention someone." aria-label="New post">${esc(social.draftBody)}</textarea>
    ${social.draftImg ? `<div class="compose-img"><img src="${social.draftImg}" alt=""><button type="button" class="icon-btn" data-act="compose-img-rm" aria-label="Remove picture">${icon('x')}</button></div>${social.draftStill ? '<p class="tiny still-note">Animated pictures are kept as a still.</p>' : ''}` : ''}
    <div class="row wrap link-offer" ${social.linkOffer ? '' : 'hidden'}><span class="tiny grow">${social.linkOffer ? `${esc(SITE_NAMES[siteOf(social.linkOffer)])} link found` : ''}</span><button type="button" class="btn sm" data-act="link-attach">${icon('link')}Attach as a card</button></div>
    <div class="chips draft-chips">${chips}<button type="button" class="chip add-trick ${social.pickOpen ? 'on' : ''}" data-act="pick-open" aria-expanded="${social.pickOpen}">${icon('plus')}${chips ? 'Trick' : 'Tag a trick or combo'}</button></div>
    ${social.pickOpen ? picker() : sugg.length ? `<div class="row wrap sugg" style="gap:6px"><span class="tiny">${esc(sugg[0].why)}:</span>${sugg.map(t => `<button type="button" class="chip" data-act="sugg-add" data-prop="${t.prop}" data-skill="${esc(t.skill)}" data-tip="${esc(skillOf(t.prop, t.skill).name)}">${icon('plus')}${esc(t.skill)}</button>`).join('')}</div>` : ''}
    <div class="row wrap" style="gap:8px"><label class="btn sm">${icon('image')}Picture<input type="file" accept="image/*" data-change="compose-img" hidden></label>
      <span class="grow"></span><button class="btn primary sm" type="submit" ${sendOff('post')}>${icon('send')}Post</button></div></div></div></form>`;
}
// The Combos page's Publish button: bring the combo to the composer with a line to edit.
function publishCombo(c) {
  social.draftCombo = { id: c.id, name: c.name, role: c.role || '', prop: ui.prop, seq: [...(c.seq || [])], reps: (c.seq || []).map((_, i) => (c.reps || [])[i] || 1) };
  if (!social.draftBody.trim()) social.draftBody = `New combo: ${c.name}. `;
  social.pickOpen = false; leaveCompose();
  if (location.hash === '#/timeline') render(); else location.hash = '#/timeline';
  setTimeout(() => { const t = $('#compose-form textarea'); if (t) { t.focus(); t.setSelectionRange(t.value.length, t.value.length); } }, 50);
}
function peopleCard() {
  if (!social.people) { loadPeople(); return `<div class="card"><div class="card-h"><h2>People</h2></div><div class="empty small">Loading…</div></div>`; }
  const list = social.people.slice(0, 12);
  return `<div class="card people" data-tour="people"><div class="card-h"><h2 class="grow">People</h2><span class="sub">Most XP first</span></div>
    ${list.map((u, i) => { const s = u.stats || {}; return `<a class="person ${u.id === me.id ? 'me' : ''}" href="#/u/${esc(u.username)}"><span class="rank num">${i + 1}</span>${avatarHTML(u)}<span class="grow" style="display:grid"><b>${esc(u.displayName || u.username)}</b><span class="tiny">Level ${s.level || 1}${s.streak ? ` · ${s.streak}-day streak` : ''}</span></span><span class="tiny num">${(s.xp || 0).toLocaleString()} XP</span></a>`; }).join('')}</div>`;
}
function pageTimeline(single, focus) {
  const key = single ? 'post:' + single : 'all', st = streakInfo();
  if (single && focus && social.focus !== Number(focus)) loadThread(Number(single), Number(focus));
  if (!focus) social.focus = 0;
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
      <div class="grow" style="min-width:220px"><h1>${esc(u.displayName || u.username)}</h1><p class="muted">@${esc(u.username)}${u.role !== 'user' ? ' · ' + RANK_NAMES[u.role] : ''} · joined ${esc(new Date(u.created).toLocaleDateString(undefined, { month: 'long', year: 'numeric' }))}</p>
        ${u.bio ? `<p class="prof-bio">${richText(u.bio)}</p>` : mine ? '<p class="tiny" style="margin-top:6px">Add a bio and your social links in your profile settings.</p>' : ''}
        <div class="row wrap" style="gap:6px;margin-top:12px">${(u.props || []).map(id => `<span class="chip">${esc(propById(id)?.name || id)}</span>`).join('')}${links}</div></div>
      ${mine ? `<a class="btn" href="#/profile">${icon('edit')}Edit profile</a>` : ''}${canReport(u.id) ? `<button class="btn sm" data-act="report" data-kind="user" data-id="${u.id}" data-what="@${esc(u.username)}">${icon('flag')}Report</button>` : ''}</div>
    ${flags.game ? `<div class="grid g4 prof-stats">${[['Level', `${L}<small> ${title}</small>`], ['XP', (s.xp || 0).toLocaleString()], ['Streak', `${s.streak || 0}<small> ${s.streak === 1 ? 'day' : 'days'} · best ${s.best || 0}</small>`], ['Clean skills', s.clean || 0]].map(([n, v]) => `<div class="stat"><div class="lbl">${n}</div><div class="val num">${v}</div></div>`).join('')}</div>` : ''}</div>
  <div class="grid g32 tl" style="margin-top:16px"><div class="stack"><div class="card-h" style="margin:0"><h2 class="grow">Posts</h2><span class="sub num">${pr.posts} ${pr.posts === 1 ? 'post' : 'posts'} · ${pr.likes} ${pr.likes === 1 ? 'like' : 'likes'} received</span></div>${feedHTML('user:' + name, mine ? 'You have not posted yet.' : 'No posts yet.')}</div>
    <div class="stack">${avatarOnProfile(u, pr.clean)}
    ${!flags.game ? '' : `<div class="card"><div class="card-h"><h2 class="grow">Badges</h2><span class="sub num">${keys.length}</span></div>${keys.length ? `<div class="badge-grid one">${keys.map(a => { const b = badgeByKey(a.key); return `<div class="badge on" data-tip="${esc(b?.desc || '')}"><span class="badge-ic on">${icon(b?.icon || 'medal')}</span><span class="grow"><b>${esc(b?.name || a.key)}</b><span class="tiny">${esc(new Date(a.at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }))}</span></span></div>`; }).join('')}</div>` : '<div class="empty small">No badges yet.</div>'}</div>`}</div></div>`;
}

/* ---------- notifications ---------- */
async function loadNotifs() {
  if (!me) return;
  const gen = social.gen, r = await api('notifications');
  if (!r.ok || social.gen !== gen) return;
  social.notifs = r.data.items; social.nUsers = r.data.users; social.unread = r.data.unread;
  const q = r.data.queue || 0;
  if (q !== social.queue) { social.queue = q; mod.count = q; renderSide(parseRoute()); }
  renderBell();
}
function notifText(n) {
  const a = social.nUsers[n.actorId], who = a ? esc(a.displayName || a.username) : 'Someone';
  switch (n.kind) {
    case 'like': return `<b>${who}</b> liked your post`;
    case 'comment': return `<b>${who}</b> commented on your post`;
    case 'mention': return `<b>${who}</b> mentioned you${n.commentId ? ' in a comment' : ''}`;
    case 'reply': return `<b>${who}</b> replied to your comment`;
    case 'comment_like': return `<b>${who}</b> liked your comment`;
    case 'achievement': { const b = badgeByKey(n.text); return `Achievement unlocked: <b>${esc(b?.name || n.text)}</b>`; }
    default: return esc(localTimes(n.text)); // a mute or suspension carries its end time
  }
}
function bellHTML() {
  if (!me || !me.onboarded || !flags.community) return '';
  const st = loaded && flags.game ? streakInfo() : { atRisk: false }, n = social.unread + (st.atRisk ? 1 : 0);
  const items = social.notifs.map(n => `<a class="ntf ${n.read ? '' : 'unread'}" href="${n.postId ? '#/post/' + n.postId + (n.commentId ? '/' + n.commentId : '') : n.kind === 'moderation' ? '#/profile' : '#/achievements'}" data-act="ntf-go" data-id="${n.id}">${n.kind === 'achievement' ? `<span class="badge-ic on sm">${icon(badgeByKey(n.text)?.icon || 'medal')}</span>` : n.kind === 'moderation' ? `<span class="badge-ic sm">${icon('shield')}</span>` : avatarHTML(social.nUsers[n.actorId], 'sm')}<span class="grow"><span class="small">${notifText(n)}</span><span class="tiny">${ago(n.created)}</span></span></a>`).join('');
  const panel = !social.nOpen ? '' : `<div class="ntf-panel" role="dialog" aria-label="Notifications"><div class="row" style="padding:10px 12px 6px"><b class="grow">Notifications</b>${social.unread ? `<button class="btn sm" data-act="ntf-read-all">Mark all read</button>` : ''}</div>
    ${st.atRisk ? `<a class="ntf unread" href="#/practice" data-act="ntf-go"><span class="flame lit sm">${icon('flame')}</span><span class="grow"><span class="small"><b>Your ${st.current}-day streak ends tonight.</b> Log today’s practice to keep it.</span></span></a>` : ''}
    ${items || '<div class="empty small" style="margin:8px">Nothing yet. Likes, comments, replies, mentions and achievements show up here.</div>'}</div>`;
  return `<button class="icon-btn bell ${n ? 'has' : ''}" data-act="bell" aria-label="Notifications${n ? `, ${n} unread` : ''}" aria-expanded="${social.nOpen}">${icon('bell')}${n ? `<span class="bell-n num">${n > 9 ? '9+' : n}</span>` : ''}</button>${panel}`;
}
function renderBell() { const el = $('#bell'); if (el) el.innerHTML = bellHTML(); }
setInterval(() => { if (me && flags.community && document.visibilityState === 'visible' && !social.nOpen) loadNotifs(); }, 60000);

/* ---------- bookmarks: skills, combos and posts to come back to ---------- */
// What the signed-in user bookmarked, as kind and ref keys, loaded once per user (load in app.js) and
// changed on the page before the server answers. The Bookmarks page lists them; its posts are a feed
// (key 'bookmarks'), so likes and comments work there as on the timeline.
const BM = { set: new Set(), loaded: false, user: 0, skills: [], combos: [] };
const bmKey = (kind, ref) => kind + '|' + ref;
const bmHas = (kind, ref) => BM.set.has(bmKey(kind, ref));
function resetBookmarks() { Object.assign(BM, { set: new Set(), loaded: false, user: 0, skills: [], combos: [] }); }
async function loadBookmarks() {
  const r = await api('bookmarks');
  if (!r.ok) { if (r.status !== 404) toast(r.error); return; }
  BM.skills = r.data.skills; BM.combos = r.data.combos; BM.loaded = true;
  BM.set = new Set([...r.data.skills.map(s => bmKey('skill', s)), ...r.data.combos.map(c => bmKey('combo', c)), ...r.data.postIds.map(id => bmKey('post', String(id)))]);
  render();
}
// The toggle: an icon button for a skill or a combo, and with act an action button like the like button under a post.
function bmButton(kind, ref, act = false) {
  if (!me || !flags.bookmarks) return '';
  const on = bmHas(kind, ref);
  return `<button class="${act ? 'act' : 'icon-btn'} bm ${on ? 'on' : ''}" data-act="bookmark" data-kind="${kind}" data-ref="${esc(ref)}" aria-pressed="${on}" aria-label="${on ? 'Remove bookmark' : 'Bookmark this ' + kind}" data-tip="${on ? 'Bookmarked' : 'Bookmark'}">${icon('bookmark')}</button>`;
}
async function toggleBookmark(kind, ref) {
  const k = bmKey(kind, ref), was = BM.set.has(k);
  if (was) BM.set.delete(k); else BM.set.add(k);
  if (kind === 'skill') BM.skills = was ? BM.skills.filter(s => s !== ref) : [ref, ...BM.skills];
  if (kind === 'combo') BM.combos = was ? BM.combos.filter(c => c !== ref) : [ref, ...BM.combos];
  // On the Bookmarks page a post that loses its bookmark leaves the list.
  if (kind === 'post' && was && social.key === 'bookmarks') social.posts = social.posts.filter(p => p.id !== Number(ref));
  render();
  const r = await api('bookmarks', 'POST', { kind, ref });
  if (!r.ok) { toast(r.error); loadBookmarks(); return; }
  if (r.data.on) BM.set.add(k); else BM.set.delete(k);
  render();
}
function pageBookmarks() {
  if (!BM.loaded) { loadBookmarks(); return '<div class="empty small">Loading…</div>'; }
  const skills = BM.skills.map(ref => {
    const [prop, id] = ref.split(':'), s = CATALOGS[prop]?.skills.find(x => x.id === id), st = s && inProp(prop, () => statusLabel(id));
    return `<div class="bm-row"><a class="grow row" href="${s ? `#/prop/${prop}/${s.b}/${id}` : `#/prop/${prop}`}"><span class="tag">${esc(id)}</span><span class="grow"><b>${esc(s?.name || 'A skill that is no longer in the map')}</b><span class="tiny">${esc(propById(prop)?.name || prop)}${st ? ' · ' + esc(st) : ''}</span></span></a>${bmButton('skill', ref)}</div>`;
  }).join('');
  const combos = BM.combos.map(ref => {
    let c = null, prop = null;
    for (const p of myProps()) { const d = p.id === ui.prop ? state : docs[p.id]?.state, hit = d?.combos.find(x => x.id === ref); if (hit) { c = hit; prop = p; break; } }
    const seq = c?.seq || [];
    return `<div class="bm-row">${c ? `<a class="grow row" href="#/combos/${esc(ref)}"><span class="tag">${icon('link')}</span><span class="grow"><b>${esc(c.name)}</b><span class="tiny">${esc(prop.name)} · ${seq.length} ${seq.length === 1 ? 'skill' : 'skills'}</span></span><span class="cb-mini">${seq.slice(0, 6).map(id => `<i>${esc(id)}</i>`).join('')}</span></a>`
      : `<span class="grow row"><span class="tag">${icon('link')}</span><span class="grow"><b>A combo that was deleted</b><span class="tiny">Remove this bookmark</span></span></span>`}${bmButton('combo', ref)}</div>`;
  }).join('');
  return `<div class="page-h"><div class="grow"><h1>Bookmarks</h1><p>Skills, combos and posts you kept to come back to. The bookmark button on each adds or removes it.</p></div></div>
  <div class="grid g32 tl"><div class="stack">${flags.community ? `<div class="card-h" style="margin:0"><h2 class="grow">Posts</h2></div>${feedHTML('bookmarks', 'No bookmarked posts yet. The bookmark button under a post keeps it here.')}` : ''}</div>
    <div class="stack"><div class="card"><div class="card-h"><h2 class="grow">Skills</h2><span class="sub num">${BM.skills.length}</span></div>${skills || '<p class="small muted">No skills yet. Open a skill and use the bookmark button in its details.</p>'}</div>
    <div class="card"><div class="card-h"><h2 class="grow">Combos</h2><span class="sub num">${BM.combos.length}</span></div>${combos || '<p class="small muted">No combos yet. Open a combo and use the bookmark button in its header.</p>'}</div></div></div>`;
}

/* ---------- events ---------- */
document.addEventListener('click', async e => {
  if (social.nOpen && !e.target.closest('#bell')) { social.nOpen = false; renderBell(); }
  const t = e.target.closest('[data-act]');
  if (!t) return;
  const d = t.dataset, id = Number(d.id), post = social.posts.find(p => p.id === id);
  switch (d.act) {
    case 'bell': social.nOpen = !social.nOpen; renderBell(); if (social.nOpen) loadNotifs(); break;
    case 'bookmark': toggleBookmark(d.kind, d.ref); break;
    case 'ntf-go':
      social.nOpen = false;
      if (d.id) { const n = social.notifs.find(x => x.id === id); if (n && !n.read) { n.read = true; social.unread = Math.max(0, social.unread - 1); api('notifications/read', 'PUT', { ids: [id] }); } }
      renderBell(); break;
    case 'ntf-read-all': social.notifs.forEach(n => n.read = true); social.unread = 0; renderBell(); await api('notifications/read', 'PUT', {}); break;
    case 'like': {
      if (!post || post.liking) break;
      const was = { liked: post.liked, likes: post.likes };
      post.liking = true; post.liked = !post.liked; post.likes += post.liked ? 1 : -1; render();
      const r = await api(`posts/${id}/like`, 'POST');
      delete post.liking;
      // A like that did not go through (the post is gone, the connection dropped) is taken back.
      if (r.ok) { post.liked = r.data.liked; post.likes = r.data.likes; } else { Object.assign(post, was); toast(r.error); }
      render(); break;
    }
    case 'cmt-toggle':
      if (social.openCmts.has(id)) social.openCmts.delete(id); else social.openCmts.add(id);
      render(); $(`.post[data-post="${id}"] .cmt-form input`)?.focus(); break;
    case 'post-del':
      if (post && confirm('Delete this post?')) { const r = await api('posts/' + id, 'DELETE'); if (r.ok) { social.posts = social.posts.filter(p => p.id !== id); render(); } else toast(r.error); }
      break;
    case 'cmt-del': {
      if (!confirm('Delete this comment?')) break;
      const r = await api('comments/' + id, 'DELETE');
      if (!r.ok) { toast(r.error); break; }
      // Kept as a placeholder when it has replies; otherwise it leaves every list it is in.
      if (r.data.placeholder) eachComment(id, c => { c.deleted = true; c.body = ''; c.likes = 0; c.liked = false; });
      else {
        const drop = list => list && list.filter(c => c.id !== id).map(c => { const reps = drop(c.replies); const n = reps ? (c.replies.length - reps.length) : 0; return { ...c, replies: reps, replyCount: (c.replyCount || 0) - n }; }).filter(c => !(c.deleted && (c.replyCount || 0) <= 0));
        for (const k in social.full) social.full[k].list = drop(social.full[k].list);
        for (const k in social.fullReplies) social.fullReplies[k].list = drop(social.fullReplies[k].list);
      }
      refreshPost(Number(d.post));
      break;
    }
    case 'cmt-like': {
      let liked = false;
      eachComment(id, c => { c.liked = !c.liked; c.likes = (c.likes || 0) + (c.liked ? 1 : -1); liked = c.liked; });
      render();
      const r = await api(`comments/${id}/like`, 'POST');
      if (r.ok) eachComment(id, c => { c.liked = r.data.liked; c.likes = r.data.likes; });
      else { toast(r.error); eachComment(id, c => { c.liked = !liked; c.likes = (c.likes || 0) + (liked ? -1 : 1); }); }
      render(); break;
    }
    case 'cmt-reply': {
      let author = null;
      eachComment(id, c => { author = social.users[c.userId]; });
      const name = author?.username || '';
      social.replyTo = { post: Number(d.post), root: Number(d.root), parent: id, name, prefill: name && author.id !== me.id ? `@${name} ` : '' };
      social.openCmts.add(Number(d.post)); leaveCompose(); render();
      const inp = $('.reply-form input'); if (inp) { inp.focus(); inp.setSelectionRange(inp.value.length, inp.value.length); }
      break;
    }
    case 'reply-cancel': social.replyTo = null; leaveCompose(); render(); break;
    case 'cmt-edit': social.editing = id; leaveCompose(); render(); $('.cmt-edit textarea')?.focus(); break;
    case 'cmt-edit-cancel': social.editing = 0; leaveCompose(); render(); break;
    case 'more-comments': moreComments(id); break;
    case 'more-replies': moreReplies(id); break;
    case 'more-posts': loadFeed(social.key, true); break;
    case 'compose-img-rm': social.draftImg = ''; social.draftStill = false; leaveCompose(); render(); break;
    case 'link-attach': {
      const u = social.linkOffer;
      if (!u) break;
      social.draftLinks.push(u);
      social.draftBody = social.draftBody.replace(u, '').replace(/[ \t]{2,}/g, ' ').trim();
      social.linkOffer = linkOffer(social.draftBody); leaveCompose(); render(); break;
    }
    case 'link-rm': {
      social.draftLinks = social.draftLinks.filter(x => x !== d.url);
      // Attaching took the link out of the text; taking the card away puts it back, so it is not lost.
      const back = `${social.draftBody.trimEnd()} ${d.url}`.trim();
      if (!social.draftBody.includes(d.url) && back.length <= 2000) social.draftBody = back;
      social.linkOffer = linkOffer(social.draftBody); leaveCompose(); render(); break;
    }
    case 'yt-play': social.played.add(`${d.id}:${d.url}`); render(); break;
    case 'pick-open': social.pickOpen = !social.pickOpen; social.pickQ = ''; social.pickIdx = 0; leaveCompose(); render(); if (social.pickOpen) $('#pick-q')?.focus(); break;
    case 'pick-close': social.pickOpen = false; leaveCompose(); render(); $('#compose-form textarea')?.focus(); break;
    case 'pick-add': { const it = pickItems(social.pickQ)[Number(d.i)]; if (it) pickAdd(it); $('#pick-q')?.focus(); break; }
    case 'sugg-add': pickAdd({ kind: 'skill', prop: d.prop, id: d.skill }); break;
    case 'tag-rm': social.draftTags = social.draftTags.filter(x => tagKey(x) !== `${d.prop}:${d.skill}`); leaveCompose(); render(); break;
    case 'combo-rm-draft': social.draftCombo = null; leaveCompose(); render(); break;
    case 'combo-publish': { const c = state.combos.find(x => x.id === d.id); if (c) publishCombo(c); break; }
  }
});
document.addEventListener('input', e => {
  if (e.target.closest('#compose-form') && e.target.name === 'body') {
    social.draftBody = e.target.value;
    // Offer a pasted or typed link as a card, without redrawing while typing.
    const offer = linkOffer(e.target.value), row = $('.link-offer');
    if (offer !== social.linkOffer && row) { social.linkOffer = offer; row.hidden = !offer; row.querySelector('.tiny').textContent = offer ? `${SITE_NAMES[siteOf(offer)]} link found` : ''; }
  }
  if (e.target.id === 'pick-q') { social.pickQ = e.target.value; social.pickIdx = 0; refreshPicker(); }
});
document.addEventListener('keydown', e => {
  if (e.target.id !== 'pick-q') return;
  const items = pickItems(social.pickQ);
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); if (items.length) { social.pickIdx = (social.pickIdx + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length; refreshPicker(); } }
  else if (e.key === 'Enter') { e.preventDefault(); if (items[social.pickIdx]) pickAdd(items[social.pickIdx]); }
  else if (e.key === 'Escape') { e.preventDefault(); social.pickOpen = false; e.target.blur(); render(); $('#compose-form textarea')?.focus(); }
});
document.addEventListener('change', async e => {
  if (e.target.dataset.change !== 'compose-img') return;
  const f = e.target.files[0]; e.target.value = '';
  if (!f) return;
  const gen = social.gen;
  try {
    const img = await shrinkImage(f);
    if (social.gen !== gen) return;
    social.draftImg = img; social.draftStill = f.type === 'image/gif';
  } catch { toast('That file could not be read as a picture.'); }
  render();
});
document.addEventListener('submit', async e => {
  const f = e.target;
  if (f.id === 'compose-form') {
    e.preventDefault();
    if (social.sending.has('post')) return;
    const body = f.body.value.trim();
    if (!body && !social.draftImg && !social.draftCombo && !social.draftLinks.length) { toast('Write something, add a picture or attach a combo.'); return; }
    const gen = social.gen, send = { body, image: social.draftImg, tags: social.draftTags, combo: social.draftCombo, links: social.draftLinks };
    setSending('post', true);
    const r = await api('posts', 'POST', send, { 'Idempotency-Key': onceKey('post', send) });
    if (social.gen !== gen) return; // signed out meanwhile: the draft and its answer are no longer this tab's
    setSending('post', false);
    if (!r.ok) { toast(r.error); return; }
    delete social.keys.post;
    social.draftBody = ''; social.draftImg = ''; social.draftStill = false; social.draftTags = []; social.draftCombo = null; social.draftLinks = []; social.linkOffer = ''; social.pickOpen = false; leaveCompose();
    // With the cursor resting in a comment box the page is not redrawn, so the sent text is cleared here: left in place it invites a second Post.
    if (f.isConnected) f.body.value = '';
    Object.assign(social.users, r.data.users);
    const made = r.data.posts[0];
    if (social.key !== 'all') social.stale = true; else if (!social.posts.some(p => p.id === made.id)) social.posts.unshift(made);
    render(); grant(['first-post']); toast('Posted.');
  } else if (f.classList.contains('cmt-form')) {
    e.preventDefault();
    const id = Number(f.dataset.id), parentId = Number(f.dataset.parent || 0), body = f.body.value.trim(), k = f.dataset.k;
    if (social.sending.has(k)) return;
    if (!body) { toast('Write something first.'); f.body.focus(); return; }
    const gen = social.gen, send = { body, parentId };
    setSending(k, true);
    const r = await api(`posts/${id}/comments`, 'POST', send, { 'Idempotency-Key': onceKey(k, send) });
    if (social.gen !== gen) return;
    setSending(k, false);
    if (!r.ok) { toast(r.error); return; }
    delete social.keys[k];
    Object.assign(social.users, r.data.users);
    const i = social.posts.findIndex(p => p.id === id), c = r.data.comment;
    if (i >= 0) social.posts[i] = r.data.posts[0];
    // An expanded list keeps what was loaded; the new comment joins it in place, once.
    const add = l => { if (!l.some(x => x.id === c.id)) { l.push(c); return true; } return false; };
    if (social.full[id] && !c.parentId) add(social.full[id].list);
    if (c.parentId) {
      const all = social.fullReplies[c.parentId];
      let fresh = all ? add(all.list) : true;
      if (social.full[id]) for (const root of social.full[id].list) if (root.id === c.parentId) { if (!all) fresh = add(root.replies ||= []); if (fresh) root.replyCount = (root.replyCount || 0) + 1; }
    }
    f.body.value = ''; social.replyTo = null; social.openCmts.add(id);
    leaveCompose(); document.activeElement?.blur?.(); render();
    document.getElementById('c-' + c.id)?.scrollIntoView({ block: 'nearest' });
  } else if (f.classList.contains('cmt-edit')) {
    e.preventDefault();
    const id = Number(f.dataset.id), body = f.body.value.trim(), k = f.dataset.k;
    if (social.sending.has(k)) return;
    if (!body) { toast('Write something first.'); f.body.focus(); return; }
    const gen = social.gen;
    setSending(k, true);
    const r = await api('comments/' + id, 'PUT', { body });
    if (social.gen !== gen) return;
    setSending(k, false);
    if (!r.ok) { toast(r.error); return; }
    eachComment(id, c => { c.body = r.data.comment.body; c.edited = true; });
    social.editing = 0; document.activeElement?.blur?.(); render();
  }
});
