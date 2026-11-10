'use strict';
// Contrast of the page's colours, read from web/app.css without a browser (WCAG 2.2, 1.4.3 and 1.4.11):
// text at 4.5:1; large text (24 px, or 18.66 px in bold), icons and the edges that mark a control at 3:1;
// in the dark theme and in the light one. test/browser/contrast.py asks the same of the pages as drawn.
// - The light theme is written twice, for a system that asks for it and for the switch on the page. The
//   two must say the same, or the themes drift apart.
// - A colour used for words (TEXT) holds on every surface words are laid on (ON_ANY, and ON_TINT for the
//   colours used there). That covers each rule that sets such a colour and no background.
// - A rule that sets a colour and a background is one pair, checked as written.
// - A rule that sets any other colour, or one over a tint, is listed below with what it is drawn on
//   (ON). A rule that is in none of these fails: a new colour cannot slip by. The same holds for a new
//   background, a fade, a filter, a ring or border that marks a state, and a colour set in a template.
// - What the standard leaves out is listed with its reason (EXEMPT, DECOR, NOT_UNDER_TEXT). Two kinds are
//   left out without a list: the border around a box that holds words (a card, a button, a chip), which
//   its words mark, and a border that only changes under the pointer.
// Not looked at here: charts, the heatmap and progress bars as shapes (GRAPHIC), and anything drawn on a
// canvas.
//   node test/contrast.js
const fs = require('fs'), path = require('path');
const web = path.join(__dirname, '..', 'web');
const css = fs.readFileSync(path.join(web, 'app.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');

// The colours used for words, and every surface words are laid on. A surface is written from the top
// layer down: a token, a colour, or =selector for the background that rule sets.
const TEXT = ['--ink', '--ink-2', '--ink-3', '--accent-ink', '--good', '--warn', '--bad'];
const PLAIN = ['--bg', '--side', '--card', '--card-2'];
const ON_ANY = [...PLAIN, '--hover',
  '--accent-soft on --bg', '--accent-soft on --side', '--accent-soft on --card',   // the tip card, the chosen menu entry, the row that is playing
  ...PLAIN.map(s => '=.top on ' + s)];                                               // the bar at the top, with the page scrolling under it
// Three tinted boxes hold only the words their templates write in them: a name, and a small line under it.
const ON_TINT = { '=.badge.on': ['--ink', '--ink-3'], '=.ch.done': ['--ink', '--ink-3'], '=.ms': ['--ink'] };
// Rules whose colour is not a TEXT token on a plain background of their own: what they are drawn on.
// kind is 'icon' where the rule colours an icon and its selector does not end in svg.i.
const ON = {
  '.nav-a.on': { on: ['--accent-soft on --side'] },
  '.react-joyride__tooltip [data-action=primary]': { on: ['--accent'] },   // the tour's Next button, which web/account.js has painted in --accent
  '.avatar': { on: [...PLAIN.map(s => '--accent-soft on ' + s), '--accent-soft on --accent-soft on --side', '--accent-soft on --accent-soft on --card'] },   // initials, also in a row that is itself tinted
  '.week-dots i': { on: ['=.week-dots .on i'], kind: 'icon' },                       // the tick shows only on a day that is filled
  '.compose-img .icon-btn': { on: ['=.compose-img .icon-btn on #fff'], kind: 'icon' },   // over a picture: the lightest one there can be
  '.lc-play': { on: ['=.lc-play on #fff', '=.lc-play:hover on #fff'], kind: 'icon' },
  '.social-instagram svg.i': { on: PLAIN }, '.social-tiktok svg.i': { on: PLAIN }, '.social-youtube svg.i': { on: PLAIN },
};
// A background set by a rule that sets no colour, with the rule whose colour ends up on it.
const UNDER = { '.cell.lv1': '.cell', '.btn.primary:hover': '.btn.primary' };
// Left out by the standard, each with its reason.
const EXEMPT = {
  '.flame.lit': 'decoration: the flame beside the count of days, which says the same in words',
  '.flame.lit svg.i': 'decoration: a faint fill inside the flame',
  '.badge-ic.on': 'decoration: the emblem beside the name of the badge',
  'button:disabled': 'a control that is switched off',
  '.cb-step .icon-btn[disabled]': 'a control that is switched off',
  '.cb-reps button[disabled]': 'a control that is switched off',
  '.week-dots .future i': 'an empty dot for a day to come: nothing is written in it',
  '.seg-r input': 'the radio button itself is not drawn; the label beside it is the control and is checked',
};
// Shapes of charts, not text, icons or controls: outside this check.
const GRAPHIC = ['.heat rect', '.heat rect.h1', '.heat rect.h2', '.heat rect.h3', '.heat rect.h4', '.heat rect.today', '.graph path', '.graph path.met', '.graph path.hl',
  '.graph.focus path:not(.hl)', '.ring circle', '.ring circle.on', '.ch.done .ring circle.on', '.dc-bar', '.dc-bar.now', '.dc-col:hover .dc-bar', '.dc-hit', '.dc-base'];
// Edges that mark a control or its state, at 3:1 against what they are drawn on. A field (input, select,
// textarea) is found by its border, its outline or its fill on every plain surface unless listed here.
// A focus ring holds wherever a control can stand (RING_ON).
const RING_ON = [...PLAIN, '--hover', '--accent-soft on --side', '--accent-soft on --card'];
const EDGE = {
  '.pal-box input': ['--side'],
  '.seg button.on': ['--bg'], '.seg a.on': ['--bg'], '.seg-r input:checked + span': ['--side', '--card'],   // the chosen one of a few
  '.node.ready': ['--card'], '.node.sel': ['--card'], '.cell.ready': ['--card-2'], '.key u.k-ready': ['--bg'],
  '.prop-card.on': ['--bg'], '.prop-pick:has(input:checked)': ['--bg', '--card'], '.tpl.on': ['--side'],
  '.week-dots .today i': ['--card'],
  // A field an error is about: its edge turns to the colour of the error, on whatever a form stands on.
  '[aria-invalid=true]:not([type=checkbox]):not([type=radio])': ['--bg', '--side', '--card', '--card-2'],
};
// Borders and rings that mark nothing a person needs to find: the words inside or beside say it.
const DECOR = {
  '.tr.works': 'repeats the colour of the words in it, which are checked', '.tr.bridge': 'repeats the colour of the words in it', '.tr.blocked': 'repeats the colour of the words in it',
  '.cb-step.playing': 'the step that is playing is named under the picture',
  '.badge.on': 'a tint around an earned badge; its line says when it was earned', '.quest.on': 'a tint around the current phase; its number is filled',
  '.risk-card': 'a tint around a card', '.av-first': 'a tint around a card', '.sw.on': 'the gap between a swatch and its ring; the ring is checked',
  '.draft-chips .add-trick.on': 'the list that is open under it shows the state',
};
// Backgrounds nothing is written on.
const NOT_UNDER_TEXT = {
  '.dot': 'a dot', '.sync.saving .dot': 'a dot', '.sync.err .dot': 'a dot', '.ed-skill.has-err::after': 'a dot', '.map-thumb i': 'a dot',
  '.bar': 'a bar', '.bar i': 'a bar', '.cb-prog i': 'a bar', '.cb-prog b': 'a bar', '.cb-link::before': 'a line',
  '.pips i': 'a pip', '.pips i.on1': 'a pip', '.pips i.on2': 'a pip', '.pips i.on3': 'a pip', '.pips i.on4': 'a pip',
  '.palette': 'the dimmed page behind a dialog', '.modal': 'the dimmed page behind a dialog', '.nav-open .scrim': 'the dimmed page behind the menu',
  '.share-prev': 'the checkerboard under a picture', '.tpl canvas': 'under a picture', '.sw': 'a swatch of the colour it offers',
};

// ---------- reading the stylesheet
const top = (s, sep) => { const out = []; let d = 0, a = 0; for (let i = 0; i < s.length; i++) { if (s[i] === '(') d++; else if (s[i] === ')') d--; else if (s[i] === sep && !d) { out.push(s.slice(a, i)); a = i + 1; } } out.push(s.slice(a)); return out.map(x => x.trim()).filter(Boolean); };
const L_SET = ':root[data-theme="light"]', L_AUTO = ':root:where(:not([data-theme="dark"]))';
let bad = 0, n = 0; const count = { own: 0, token: 0, listed: 0, edge: 0, exempt: 0, graphic: 0 };
const fail = (...x) => { bad++; console.log('FAIL', ...x); };
const rules = [];   // { sel, d: { property: value }, light: false, 'set' (the switch) or 'auto' (the system asks) }
(function walk(s, media) {
  for (let i = 0; i < s.length;) {
    const o = s.indexOf('{', i); if (o < 0) break;
    let d = 1, j = o + 1; for (; d && j < s.length; j++) d += s[j] === '{' ? 1 : s[j] === '}' ? -1 : 0;
    const head = s.slice(i, o).trim(), body = s.slice(o + 1, j - 1); i = j;
    if (head.startsWith('@media')) { walk(body, media + ' ' + head); continue; }
    if (head.startsWith('@')) continue;
    const d2 = {}; for (const x of top(body, ';')) { const c = x.indexOf(':'); d2[x.slice(0, c).trim().toLowerCase()] = x.slice(c + 1).replace(/!important\s*$/, '').trim(); }
    for (let sel of top(head, ',')) {
      let light = false;
      if (/prefers-color-scheme:\s*light/.test(media)) { if (!sel.startsWith(L_AUTO)) { fail(`${sel}: inside the light media query, so it must start with ${L_AUTO}`); continue; } light = 'auto'; sel = sel.slice(L_AUTO.length).trim() || ':root'; }
      else if (sel.startsWith(L_SET)) { light = 'set'; sel = sel.slice(L_SET.length).trim() || ':root'; }
      rules.push({ sel, d: d2, light });
    }
  }
})(css, '');
// The light theme written twice says the same both times.
const form = k => rules.filter(r => r.light === k).map(r => r.sel + ' { ' + Object.entries(r.d).map(([p, v]) => p + ': ' + v).sort().join('; ') + ' }').sort();
const [asked, set] = [form('auto'), form('set')]; n++;
if (!set.length) fail('light theme: no rule under [data-theme="light"]');
for (const x of asked) if (!set.includes(x)) fail(`light theme: only where the system asks for it: ${x}`);
for (const x of set) if (!asked.includes(x)) fail(`light theme: only under [data-theme="light"]: ${x}`);

// ---------- colours
const hex = c => '#' + c.slice(0, 3).map(v => Math.round(v).toString(16).padStart(2, '0')).join('');
const over = (f, b) => { const a = f[3] + b[3] * (1 - f[3]); return a ? [0, 1, 2].map(i => (f[i] * f[3] + b[i] * b[3] * (1 - f[3])) / a).concat(a) : [0, 0, 0, 0]; };
const lum = c => { const [r, g, b] = c.slice(0, 3).map(v => { v /= 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
const ratio = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05); };
// A colour as written: hex, rgb(), rgba(), var(--token), color-mix(in srgb, ...), transparent. Anything
// else is not guessed at: null.
const colour = (v, T) => {
  v = v.trim(); let m;
  if (v === 'transparent') return [0, 0, 0, 0];
  if (m = /^#([0-9a-f]{3,8})$/i.exec(v)) { let h = m[1]; if (h.length < 5) h = [...h].map(c => c + c).join(''); const p = i => parseInt(h.slice(i, i + 2), 16); return [p(0), p(2), p(4), h.length > 6 ? p(6) / 255 : 1]; }
  if (m = /^var\((--[\w-]+)\)$/.exec(v)) return T[m[1]] === undefined ? null : colour(T[m[1]], T);
  if (m = /^rgba?\(([^()]*)\)$/i.exec(v)) { const p = m[1].split(/[\s,/]+/).filter(Boolean).map(x => x.endsWith('%') ? parseFloat(x) / 100 : parseFloat(x)); return p.length < 3 || p.some(isNaN) ? null : [p[0], p[1], p[2], p[3] === undefined ? 1 : p[3]]; }
  if (m = /^color-mix\(in srgb,(.*)\)$/i.exec(v)) {
    const part = top(m[1], ',').map(x => { const q = /^(.*?)(?:\s+([\d.]+)%)?$/.exec(x); return [colour(q[1], T), q[2] === undefined ? null : +q[2]]; });
    if (part.length !== 2 || !part[0][0] || !part[1][0]) return null;
    let [[c1, p1], [c2, p2]] = part; if (p1 === null && p2 === null) p1 = p2 = 50; else if (p1 === null) p1 = 100 - p2; else if (p2 === null) p2 = 100 - p1;
    const sum = p1 + p2; if (sum <= 0) return null;
    const w1 = p1 / sum, w2 = p2 / sum, a = c1[3] * w1 + c2[3] * w2;   // mixed with alpha multiplied in, as the browser does
    return a ? [0, 1, 2].map(i => (c1[i] * c1[3] * w1 + c2[i] * c2[3] * w2) / a).concat(a * Math.min(sum, 100) / 100) : [0, 0, 0, 0];
  }
  return null;
};
const skip = v => /^(inherit|currentcolor|none|transparent|0)$/i.test(v);
// The colour at the end of a border, an outline or a ring: "1px solid var(--line)", "0 0 0 1px var(--accent) inset".
const lineColour = v => { const m = /((?:var|rgba?|color-mix)\(.*\)|#[0-9a-f]{3,8}|transparent)(?:\s+inset)?$/i.exec(v); return m ? m[1] : null; };

for (const theme of ['dark', 'light']) {
  // The rules of this theme, later ones over earlier ones for the same selector, and its tokens.
  const R = new Map();
  for (const r of rules) if (!r.light || (theme === 'light' && r.light === 'set')) R.set(r.sel, { ...(R.get(r.sel) || {}), ...r.d });
  const T = Object.fromEntries(Object.entries(R.get(':root')).filter(([p]) => p.startsWith('--')));
  const bgOf = sel => {   // the background a rule sets: none, one colour, or the stops of a gradient
    const d = R.get(sel) || {}, v = d['background-color'] || d.background;
    if (v === undefined || /^(none|transparent)$/.test(v)) return [];
    if (/gradient\(/.test(v)) return (v.match(/#[0-9a-f]{3,8}\b|rgba?\([^()]*\)|var\(--[\w-]+\)/gi) || []).map(x => colour(x, T));
    return [colour(v, T)];
  };
  // A surface as one or more opaque colours: the layers laid over each other from the bottom up.
  const surface = spec => spec.split(' on ').reverse().reduce((under, layer) => {
    const tops = layer.startsWith('=') ? bgOf(layer.slice(1)) : [colour(layer.startsWith('--') ? `var(${layer})` : layer, T)];
    if (!tops.length || tops.includes(null)) return [null];
    return under.flatMap(u => tops.map(t => u && over(t, u)));
  }, [[0, 0, 0, 0]]);
  const pair = (what, fg, spec, need) => {
    for (const s of surface(spec)) {
      n++;
      if (!s || s[3] < 0.999) { fail(`${theme} ${what}: the surface "${spec}" is not a colour this script can read`); continue; }
      const f = over(fg, s), r = ratio(f, s);
      if (r < need) fail(`${theme} ${what}: ${hex(f)} on ${hex(s)} (${spec}) is ${r.toFixed(2)}:1, needs ${need}:1`);
    }
  };
  const large = d => { const px = parseFloat(d['font-size']), w = parseFloat(d['font-weight']) || 400; return /px$/.test(d['font-size'] || '') && (px >= 24 || (px >= 18.66 && w >= 700)); };

  // Each colour used for words, on each surface words are laid on.
  for (const t of TEXT) for (const s of ON_ANY) pair(`text in ${t}`, colour(`var(${t})`, T), s, 4.5);
  for (const [s, ts] of Object.entries(ON_TINT)) for (const t of ts) pair(`text in ${t}`, colour(`var(${t})`, T), s, 4.5);

  const known = new Set([...ON_ANY, ...Object.keys(ON_TINT)].flatMap(s => s.split(' on ')));
  for (const [sel, d] of R) {
    if (sel === ':root') continue;
    // What is written in this rule's colour: words, an icon, or the words of an svg.
    for (const prop of ['color', 'fill', 'stroke']) {
      const v = d[prop]; if (v === undefined || skip(v)) continue;
      if (EXEMPT[sel]) { count.exempt++; continue; }
      if (GRAPHIC.includes(sel)) { count.graphic++; continue; }
      const fg = colour(v, T), kind = (ON[sel] || {}).kind || (/svg\.i$/.test(sel) ? 'icon' : large(d) ? 'large text' : 'text'), need = kind === 'text' ? 4.5 : 3;
      if (!fg) { fail(`${theme} ${sel}: ${prop}: ${v} is not a colour this script can read`); continue; }
      const own = prop === 'color' ? bgOf(sel) : [], token = (/^var\((--[\w-]+)\)$/.exec(v) || [])[1];
      if (ON[sel]) { count.listed++; for (const s of ON[sel].on) pair(`${sel} (${kind})`, fg, s, need); }
      else if (own.length && own.every(c => c && c[3] >= 0.999)) { count.own++; own.forEach((c, i) => pair(`${sel} (${kind})`, fg, hex(c), need)); }
      else if (own.length) fail(`${theme} ${sel}: ${prop}: ${v} over a background that lets through what is under it; list what that is in ON`);
      else if (TEXT.includes(token)) count.token++;
      else fail(`${theme} ${sel}: ${prop}: ${v} is not one of the colours for words and the rule sets no background; list what it is drawn on in ON, or why it is left out in EXEMPT`);
    }
    // A background set without a colour: words written in a colour from another rule end up on it.
    if ((d.background || d['background-color']) && d.color === undefined && !EXEMPT[sel]) {
      const v = d['background-color'] || d.background, t = (/^var\((--[\w-]+)\)$/.exec(v) || [])[1];
      if (UNDER[sel]) { count.listed++; const u = R.get(UNDER[sel]) || {}; if (!u.color) fail(`${theme} ${sel}: UNDER names ${UNDER[sel]}, which sets no colour`); else pair(`${UNDER[sel]} on ${sel} (text)`, colour(u.color, T), '=' + sel, 4.5); }
      else if (!/^(none|transparent)$/.test(v) && !known.has(t) && !known.has('=' + sel) && !NOT_UNDER_TEXT[sel] && !Object.values(ON).some(o => o.on.some(s => s.split(' on ').includes('=' + sel))))
        fail(`${theme} ${sel}: background: ${v} is not a surface words are checked on; add it to ON_ANY, or to UNDER with the rule whose colour lands on it, or to NOT_UNDER_TEXT`);
    }
    // A filter over a button changes its colours: brightness is worked out, anything else is not guessed.
    if (d.filter) {
      const k = (/^brightness\(([\d.]+)\)$/.exec(d.filter) || [])[1], base = R.get(sel.replace(/:(hover|active|focus-visible|focus)/g, '')) || {};
      const f = k && base.color && colour(base.color, T), b = k && bgOf(sel.replace(/:(hover|active|focus-visible|focus)/g, ''))[0];
      if (!f || !b) fail(`${theme} ${sel}: filter: ${d.filter} is not one this script can work out`);
      else { count.own++; const lit = c => c.slice(0, 3).map(x => Math.min(255, x * k)).concat(c[3]); pair(`${sel} (text under the filter)`, lit(f), hex(lit(b)), 4.5); }
    }
    // A fade takes contrast from everything inside it.
    if (d.opacity !== undefined && parseFloat(d.opacity) < 1 && EXEMPT[sel]) count.exempt++;
    else if (d.opacity !== undefined && parseFloat(d.opacity) < 1 && !GRAPHIC.includes(sel)) fail(`${theme} ${sel}: opacity: ${d.opacity} fades the words in it below what their colours were checked at; say in EXEMPT why that is allowed, or do not fade`);
    // Edges. A field is found by its border, outline or fill; a focus ring and a ring or border that marks a state by itself.
    const field = /^(input|select|textarea)(?![\w-])/.test(sel.split(/[\s>+~]+/).pop());
    const lines = ['border', 'border-color', 'border-bottom', 'outline'].map(p => d[p] && lineColour(d[p])).filter(v => v && v !== 'transparent');
    const ring = (/^0 0 0 \d+px /.test(d['box-shadow'] || '') && lineColour(d['box-shadow'])) || null;
    if (field && (lines.length || EDGE[sel])) {
      count.edge++;
      for (const spec of EDGE[sel] || PLAIN) for (const s of surface(spec)) {
        n++; const fill = bgOf(sel)[0] ? over(bgOf(sel)[0], s) : s;
        const best = Math.max(ratio(fill, s), ...lines.map(v => ratio(over(colour(v, T) || fill, fill), s)));
        if (best < 3) fail(`${theme} ${sel}: the edge of the field (${lines.join(', ') || 'none'}; fill ${hex(fill)}) on ${hex(s)} (${spec}) is ${best.toFixed(2)}:1, needs 3:1`);
      }
    } else if (d.outline && !skip(d.outline) && lineColour(d.outline)) {
      count.edge++; for (const s of RING_ON) pair(`${sel} (ring)`, colour(lineColour(d.outline), T), s, 3);
    }
    const mark = [d['border-color'], ring, EDGE[sel] && d.border && lineColour(d.border)].filter(v => v && v !== 'transparent');
    if (!field && EDGE[sel] && !mark.length) fail(`${theme} EDGE lists ${sel}, which sets no border and no ring`);
    if (!field && mark.length && !/:hover/.test(sel)) {
      if (EDGE[sel]) { count.edge++; for (const v of mark) for (const s of EDGE[sel]) pair(`${sel} (edge)`, colour(v, T), s, 3); }
      else if (DECOR[sel]) count.exempt++;
      else fail(`${theme} ${sel}: ${mark.join(', ')} marks a state by a border or ring; list what it is drawn on in EDGE, or why it marks nothing in DECOR`);
    }
  }
  // A table entry for a rule that is gone would hide the next rule of that name.
  for (const [name, t] of Object.entries({ ON, UNDER, EXEMPT, EDGE, DECOR, NOT_UNDER_TEXT })) for (const sel of Object.keys(t)) if (!R.has(sel)) fail(`${theme} ${name} lists ${sel}, which the stylesheet no longer has`);
  for (const s of Object.keys(ON_TINT)) if (!R.has(s.slice(1))) fail(`${theme} ON_TINT lists ${s.slice(1)}, which the stylesheet no longer has`);
  for (const sel of GRAPHIC) if (!R.has(sel)) fail(`${theme} GRAPHIC lists ${sel}, which the stylesheet no longer has`);
}

// A colour set in a template instead of the stylesheet: only a colour for words, or a level of the scale as a swatch.
let inline = 0;
for (const f of fs.readdirSync(web).filter(f => f.endsWith('.js') && !['figure3d.js', 'tour.js'].includes(f))) {
  for (const m of fs.readFileSync(path.join(web, f), 'utf8').matchAll(/style="([^"]*)"/g)) for (const x of m[1].split(';')) {
    const c = x.indexOf(':'), p = x.slice(0, Math.max(c, 0)).trim(), v = x.slice(c + 1).trim();
    if (!/^(color|background|background-color|fill|stroke|border|border-color|outline|opacity|filter)$/.test(p)) continue;
    inline++; n++;
    const t = (/^var\((--[\w-]+)\)$/.exec(v) || [])[1];
    if (!(p === 'color' && TEXT.includes(t)) && !(p === 'background' && /^var\(--l(\d|\$\{i\})\)$/.test(v))) fail(`web/${f}: style="${p}:${v}" sets a colour this script cannot place; use a colour for words, or a class in app.css`);
  }
}
console.log(`contrast: 2 themes, ${TEXT.length} colours for words on ${ON_ANY.length + Object.keys(ON_TINT).length} surfaces, ${count.own / 2} rules on their own background, ${count.token / 2} by their colour for words, ${count.listed / 2} listed, ${count.edge / 2} edges and rings, ${inline} colours in templates; ${count.exempt / 2} exempt, ${count.graphic / 2} chart shapes not looked at; ${n} checks, ${bad} failed`);
process.exit(bad ? 1 : 0);
