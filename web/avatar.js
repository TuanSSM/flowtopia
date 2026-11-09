'use strict';
// Avatars: the editor page (#/avatar), the avatar in demos and combos, on other people's profiles,
// and as stickers. Every option and palette comes from web/avatar-def.json, which the server checks
// a saved avatar against. Drawing is done by the demo player (web/demo.js) and the 3D figure.

// var, not const: the browser tests read the draft (window.AV).
var AV = { def: null, draft: null, undo: [], tab: 'face', first: false, shown: '', other: {} };
const AV_TABS = [
  ['face', 'Face', ['skin', 'eyes', 'brows', 'mouth']],
  ['hair', 'Hair', ['hair', 'hairColor']],
  ['outfit', 'Outfit', ['top', 'topColor', 'bottoms', 'bottomsColor', 'shoes', 'shoesColor']],
  ['extras', 'Accessories', ['hat', 'hatColor', 'glasses', 'wristbands', 'wristColor']],
  ['props', 'Props', []],
  ['body', 'Body', ['frame', 'body', 'build']],
];
const AV_LABEL = { skin: 'Skin tone', eyes: 'Eyes', brows: 'Brows', mouth: 'Mouth', hair: 'Style', hairColor: 'Colour', top: 'Top', topColor: 'Top colour', bottoms: 'Bottoms', bottomsColor: 'Bottoms colour', shoes: 'Shoes', shoesColor: 'Shoes colour', hat: 'Hat', hatColor: 'Hat colour', glasses: 'Glasses', wristbands: 'Wristbands', wristColor: 'Wristband colour', body: 'Height', build: 'Build', frame: 'Frame' };
const AV_NAME = { tshirt: 'T-shirt', longsleeve: 'Long sleeves', hightops: 'High-tops', no: 'None', yes: 'Wear them', none: 'None' };
const avName = v => AV_NAME[v] || v[0].toUpperCase() + v.slice(1);
const avOn = () => !!flags.avatars;
const hasAvatar = u => !!(u && u.avatarCfg && u.avatarCfg.v);

async function avatarDef() {
  if (!AV.def) { try { const r = await fetch('avatar-def.json'); if (r.ok) AV.def = await r.json(); } catch { /* the editor says it cannot load */ } }
  return AV.def;
}
// A complete random avatar, with a colour for each of the user's props.
function avRandom() {
  const d = AV.def, pick = a => a[Math.floor(Math.random() * a.length)], c = { v: d.version, props: {} };
  for (const [k, list] of Object.entries(d.options)) c[k] = pick(list);
  for (const [k, pal] of Object.entries(d.colorKeys)) c[k] = pick(d[pal]);
  for (const p of myProps()) c.props[p.id] = pick(d.propColors);
  return c;
}
const avClone = c => JSON.parse(JSON.stringify(c));
function avChange(fn) { AV.undo.push(JSON.stringify(AV.draft)); fn(AV.draft); render(); }

/* ---------- the editor ---------- */
function pageAvatar(arg) {
  if (!avOn()) return pageOff('Avatars');
  if (!AV.def) { avatarDef().then(d => d && render()); return '<p class="empty">Loading the avatar editor…</p>'; }
  if (arg === 'first' && !AV.first) { AV.first = true; AV.draft = null; }
  if (!AV.draft) { AV.draft = hasAvatar(me) ? avClone({ ...AV.def.defaults, ...me.avatarCfg }) : AV.first ? avRandom() : avClone(AV.def.defaults); AV.undo = []; }
  const d = AV.def, c = AV.draft, tab = AV_TABS.find(t => t[0] === AV.tab) || AV_TABS[0];
  const opt = k => `<div class="av-field"><div class="lbl small">${AV_LABEL[k]}</div><div class="chips">${d.options[k].map(v => `<button class="chip ${c[k] === v ? 'on' : ''}" data-act="av-set" data-k="${k}" data-v="${esc(v)}" aria-pressed="${c[k] === v}">${esc(avName(v))}</button>`).join('')}</div></div>`;
  const sw = (k, list, cur, extra = '') => `<div class="av-sw" role="group" aria-label="${esc(AV_LABEL[k] || k)}">${extra}${list.map((h, i) => `<button class="sw ${cur === h ? 'on' : ''}" style="--c:${h}" data-act="av-set" data-k="${k}" data-v="${h}" aria-pressed="${cur === h}" aria-label="${esc(AV_LABEL[k] || k)} ${i + 1} of ${list.length}" data-tip="${h}"></button>`).join('')}</div>`;
  const col = k => `<div class="av-field"><div class="lbl small">${AV_LABEL[k]}</div>${sw(k, d[d.colorKeys[k]], c[k])}</div>`;
  const fields = tab[0] === 'props'
    ? myProps().map(p => `<div class="av-field"><div class="lbl small">${esc(p.name)}</div><div class="av-sw" role="group" aria-label="${esc(p.name)} colour"><button class="chip ${!c.props?.[p.id] ? 'on' : ''}" data-act="av-prop" data-k="${p.id}" data-v="" aria-pressed="${!c.props?.[p.id]}">Theme colour</button>${d.propColors.map((h, i) => `<button class="sw ${c.props?.[p.id] === h ? 'on' : ''}" style="--c:${h}" data-act="av-prop" data-k="${p.id}" data-v="${h}" aria-pressed="${c.props?.[p.id] === h}" aria-label="${esc(p.name)} colour ${i + 1} of ${d.propColors.length}" data-tip="${h}"></button>`).join('')}</div></div>`).join('')
    : tab[2].map(k => d.options[k] ? opt(k) : col(k)).join('');
  const no3d = !flags.model3d ? '<p class="tiny">The 3D figure is turned off here, so the preview shows the drawn figure without your colours.</p>' : '';
  return `${AV.first ? `<div class="card av-first"><b>Make your avatar</b><p class="small">It plays your demos and combos and shows up on your stickers. Here is a random one to start from: change anything, or skip and do it later from your profile.</p><div class="row" style="gap:8px"><button class="btn primary" data-act="av-save">Keep this avatar</button><button class="btn" data-act="av-skip">Skip for now</button></div></div>` : ''}
  <div class="page-h"><div class="grow"><h1>Your avatar</h1><p>It plays your demos and combos, and appears on your stickers and streak overlays.</p></div></div>
  <div class="av-wrap">
    <div class="av-stage card"><canvas id="av-cv" width="560" height="560" tabindex="0" role="img" aria-label="Your avatar, turning slowly. Drag it, or use the left and right arrow keys, to turn it."></canvas>
      <div class="row av-tools"><button class="btn sm" data-act="av-random">${icon('star')}Surprise me</button><button class="btn sm" data-act="av-undo" ${AV.undo.length ? '' : 'disabled'}>Undo</button><span class="tiny grow" style="text-align:right">Drag to turn</span></div>${no3d}</div>
    <div class="av-panel card">
      <div class="seg av-tabs" role="tablist" aria-label="Category">${AV_TABS.map(([k, n]) => `<button role="tab" class="${AV.tab === k ? 'on' : ''}" aria-selected="${AV.tab === k}" data-act="av-tab" data-v="${k}">${n}</button>`).join('')}</div>
      <div class="av-fields" role="tabpanel">${fields}${tab[0] === 'body' ? '<p class="tiny">Each demo plays on your body when it passes the same reach and overlap checks as on the standard body; any other plays on the standard body and says so.</p>' : ''}</div>
      <label class="check"><input type="checkbox" id="av-headshot" ${AV.headshot ? 'checked' : ''}><span>Use a picture of my avatar as my profile picture</span></label>
      <div class="row" style="gap:8px"><button class="btn primary" data-act="av-save">Save avatar</button>${hasAvatar(me) ? '<button class="btn" data-act="av-remove">Remove avatar</button>' : ''}<a class="btn" href="#/profile">Back to profile</a></div>
    </div>
  </div>`;
}

// After each render: start the preview on the editor or on a profile, and keep the demo player's
// avatar the signed-in user's.
function avatarMount() {
  const key = avOn() && hasAvatar(me) ? JSON.stringify(me.avatarCfg) : '';
  if (key !== AV.shown) { AV.shown = key; Demo.avatar(key ? me.avatarCfg : null); }
  const cv = document.getElementById('av-cv');
  if (cv && AV.draft) { Demo.preview(cv, Demo.firstClip(ui.prop), { who: AV.draft }); avDrag(cv); }
  const ov = document.getElementById('av-other');
  if (ov) { Demo.preview(ov, ov.dataset.key, { who: AV.other[ov.dataset.user] || null }); avDrag(ov); }
}
function avDrag(cv) {
  if (cv.dataset.drag) return;
  cv.dataset.drag = '1';
  let x0 = null;
  cv.addEventListener('pointerdown', e => { x0 = e.clientX; cv.setPointerCapture(e.pointerId); });
  cv.addEventListener('pointermove', e => { if (x0 === null) return; Demo.previewState.yaw += (e.clientX - x0) * 0.012; x0 = e.clientX; });
  cv.addEventListener('pointerup', () => { x0 = null; });
  cv.addEventListener('pointercancel', () => { x0 = null; });
  cv.addEventListener('keydown', e => { if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') { Demo.previewState.yaw += e.key === 'ArrowLeft' ? -0.25 : 0.25; e.preventDefault(); } });
}

async function avSave() {
  const cfg = AV.draft, patch = { avatarCfg: cfg };
  if (document.getElementById('av-headshot')?.checked) { const pic = Demo.headshot(cfg); if (pic) patch.avatar = pic; }
  if (!(await updateMe(patch, 'Avatar saved.'))) return;
  const first = AV.first;
  AV.first = false; AV.undo = [];
  if (first) location.hash = '#/';
  render();
}

document.addEventListener('click', e => {
  const t = e.target.closest('[data-act]');
  if (!t || !t.dataset.act.startsWith('av-')) return;
  const d = t.dataset;
  switch (d.act) {
    case 'av-tab': AV.tab = d.v; render(); break;
    case 'av-set': avChange(c => { c[d.k] = d.v; }); break;
    case 'av-prop': avChange(c => { c.props = { ...(c.props || {}) }; if (d.v) c.props[d.k] = d.v; else delete c.props[d.k]; }); break;
    case 'av-random': avChange(c => Object.assign(c, avRandom())); break;
    case 'av-undo': if (AV.undo.length) { AV.draft = JSON.parse(AV.undo.pop()); render(); } break;
    case 'av-save': avSave(); break;
    case 'av-skip': AV.first = false; AV.draft = null; location.hash = '#/'; break;
    case 'av-remove': updateMe({ avatarCfg: {} }, 'Avatar removed. Demos use the standard figure.').then(ok => { if (ok) { AV.draft = null; render(); } }); break;
    case 'av-play': { const cv = document.getElementById('av-other'); if (cv) { cv.dataset.key = d.key; document.querySelectorAll('[data-act=av-play]').forEach(b => { b.classList.toggle('on', b === t); b.setAttribute('aria-pressed', b === t); }); avatarMount(); } break; }
  }
});
document.addEventListener('change', e => { if (e.target.id === 'av-headshot') AV.headshot = e.target.checked; });

/* ---------- someone's profile: their avatar plays a skill they have clean ---------- */
function avatarOnProfile(u, clean) {
  if (!avOn() || !hasAvatar(u) || !clean) return '';
  const list = [];
  for (const [cid, ids] of Object.entries(clean)) for (const id of ids) if (Demo.has(cid, id)) list.push([cid, id]);
  if (!list.length) return '';
  AV.other[u.username] = u.avatarCfg;
  const first = list[0][0] + ':' + list[0][1], name = (cid, id) => CATALOGS[cid]?.skills.find(s => s.id === id)?.name || id;
  return `<section class="card av-prof"><div class="card-h"><h2>${esc(u.displayName || u.username)}'s avatar</h2></div>
    <canvas id="av-other" width="400" height="400" data-key="${esc(first)}" data-user="${esc(u.username)}" tabindex="0" role="img" aria-label="${esc(u.displayName || u.username)}'s avatar playing a skill they have clean. Drag to turn."></canvas>
    <div class="chips">${list.slice(0, 12).map(([cid, id], i) => `<button class="chip ${i ? '' : 'on'}" data-act="av-play" data-key="${esc(cid + ':' + id)}" aria-pressed="${!i}">${esc(id)} ${esc(name(cid, id))}</button>`).join('')}</div>
    <p class="tiny">Skills they have marked Clean that have a demo.</p></section>`;
}

/* ---------- stickers ---------- */
// Sticker images are rendered once per avatar and pose and kept for the visit.
const stickerCache = new Map();
function stickerURL(pose, who) {
  if (!avOn() || !who || !who.v || !Demo.ready3d()) return '';
  const k = pose + '|' + JSON.stringify(who);
  if (!stickerCache.has(k)) { const cv = Demo.sticker(pose, 256, who); if (!cv) return ''; stickerCache.set(k, cv.toDataURL('image/png')); }
  return stickerCache.get(k);
}
function stickerCanvas(pose, who, W = 512) { return avOn() && who && who.v && Demo.ready3d() ? Demo.sticker(pose, W, who) : null; }
// The pose that goes with a badge group: a signature move of the prop for combos.
function poseFor(group, prop) {
  if (group === 'Combos') { const s = Demo.SIGNATURE.find(([k]) => k.startsWith((prop || ui.prop) + ':')); if (s) { Demo.fetchClip(Demo.firstVariant(s[0]), () => {}); if (Demo.cached(s[0])) return s[0]; } return 'wave'; }
  return { Skills: 'flex', Practice: 'celebrate', Streaks: 'celebrate', Community: 'wave' }[group] || 'celebrate';
}
function stickerImg(group, who, prop, cls = 'sticker') {
  const url = stickerURL(poseFor(group, prop), who);
  return url ? `<img class="${cls}" src="${url}" alt="" data-sticker="${esc(group || '')}">` : '';
}
