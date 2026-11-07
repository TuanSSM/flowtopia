'use strict';
// Community: public profiles with a bio and social links. Talks to the endpoints in main.go.

const social = { profile: {} };
const LINK_KINDS = {
  instagram: { name: 'Instagram', hint: 'instagram.com/…', url: h => `https://www.instagram.com/${h}/` },
  tiktok: { name: 'TikTok', hint: 'tiktok.com/@…', url: h => `https://www.tiktok.com/@${h}` },
  youtube: { name: 'YouTube', hint: 'youtube.com/@…', url: h => /^(channel|c|user)\//.test(h) ? `https://www.youtube.com/${h}` : `https://www.youtube.com/@${h.replace(/^@/, '')}` },
};
const linkURL = (k, h) => LINK_KINDS[k].url(encodeURIComponent(h).replace(/%2F/g, '/').replace(/%40/g, '@'));

// Escaped text with @mentions and links made clickable, and line breaks kept. Mentions
// are only looked for outside links, so an @ inside a URL stays part of the URL.
function richText(s) {
  return esc(s).split(/(https?:\/\/[^\s<]+[^\s<.,)])/).map((part, i) => i % 2
    ? `<a href="${part}" target="_blank" rel="noopener noreferrer">${part}</a>`
    : part.replace(/(^|[^\w.-])@([a-z0-9][a-z0-9._-]{2,23})/gi, (m, pre, n) => `${pre}<a href="#/u/${n.toLowerCase()}">@${n}</a>`)).join('').replace(/\n/g, '<br>');
}
async function loadProfile(name) {
  social.profile[name] = { loading: true };
  const r = await api('users/' + encodeURIComponent(name));
  social.profile[name] = r.ok ? r.data : { error: r.error };
  render();
}

/* ---------- public profile ---------- */
function pageUser(name) {
  name = (name || '').toLowerCase();
  const pr = social.profile[name];
  if (!pr) { loadProfile(name); return '<div class="empty small">Loading…</div>'; }
  if (pr.loading) return '<div class="empty small">Loading…</div>';
  if (pr.error) return `<div class="empty">${esc(pr.error)} <a href="#/">Go to the dashboard</a>.</div>`;
  const u = pr.user, mine = u.username === me.username;
  const links = Object.keys(LINK_KINDS).filter(k => (u.links || {})[k]).map(k => `<a class="btn sm social-${k}" href="${esc(linkURL(k, u.links[k]))}" target="_blank" rel="noopener noreferrer">${icon(k)}${esc(LINK_KINDS[k].name)}</a>`).join('');
  return `<div class="card prof-head"><div class="row wrap" style="gap:18px;align-items:flex-start">${avatarHTML(u, 'big')}
      <div class="grow" style="min-width:220px"><h1>${esc(u.displayName || u.username)}</h1><p class="muted">@${esc(u.username)}${u.role === 'admin' ? ' · Admin' : ''} · joined ${esc(new Date(u.created).toLocaleDateString(undefined, { month: 'long', year: 'numeric' }))}</p>
        ${u.bio ? `<p class="prof-bio">${richText(u.bio)}</p>` : mine ? '<p class="tiny" style="margin-top:6px">Add a bio and your social links in your profile settings.</p>' : ''}
        <div class="row wrap" style="gap:6px;margin-top:12px">${(u.props || []).map(id => `<span class="chip">${esc(propById(id)?.name || id)}</span>`).join('')}${links}</div></div>
      ${mine ? `<a class="btn" href="#/profile">${icon('edit')}Edit profile</a>` : ''}</div></div>`;
}
