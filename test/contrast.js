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
//   (ON). A rule that is in none of these fails. The same holds for a new background, a tint without
//   what is under it, a gradient or picture under words, a fade, a filter, a ring or border that marks a
//   state, the edge of a field and a colour set in a template.
// - A rule inside @media, @supports or @layer is read where it stands. Where such blocks change something
//   looked at, the rule is checked outside them and inside every set of them that can hold together.
// - What the standard leaves out is listed with its reason (EXEMPT, DECOR, NOT_UNDER_TEXT). Two kinds are
//   left out without a list: the border around a box that holds words (a card, a button, a chip), which
//   its words mark, and a border that only changes under the pointer.
// This is a reader of the CSS this stylesheet is written in, not of all CSS. Where it cannot read, it
// fails and says what to write instead; where it refuses something sound, a table allows it:
// - a colour it cannot turn into numbers fails wherever it stands (it reads hex, rgb(), hsl(), hwb(),
//   color-mix(in srgb), a token, black, white and transparent);
// - a property that paints and is not one it places fails, unless OTHER_COLOURS says why it may;
// - a nested rule, an @import, a colour in @keyframes or another block it does not enter, and a token
//   set anywhere but the three theme blocks fail (LOCAL_TOKENS allows a token that is no theme colour);
// - a border on a rule that adds a class to another is taken for the mark of a state; NOT_A_STATE and
//   DECOR say when it is not. A class the templates put only on fields is taken for a field;
//   NOT_A_FIELD says when it is not. Two blocks are taken to hold together unless their widths rule it
//   out; APART says when they never do.
// What it cannot see at all, and test/browser/contrast.py is there for. This script is a first net; the
// walk over the computed styles is the judge.
// - Which rules meet on one element. A colour is placed by its own rule, its token or a table, never by
//   matching selectors against a page: words that inherit a colour which is not a token, onto a background
//   from another rule, are seen only by the walk.
// - A state shown by a tint alone (the chosen entry of the menu, of the search and of a list): the tint
//   is checked as a surface for words, not as a mark that must stand out by itself.
// - A border written whole (border: 1px solid ...) on a rule of one class is taken for the frame of a
//   box that holds words, and a shadow that is blurred for a soft shadow under a box, not a mark.
// - A colour or a class put on an element by a script (classList, style) and not written in a template.
// - Charts, the heatmap and progress bars as shapes (GRAPHIC), and anything drawn on a canvas.
//   node test/contrast.js
const fs = require('fs'), path = require('path');
const web = path.join(__dirname, '..', 'web');
const css = fs.readFileSync(path.join(web, 'app.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');

// The colours used for words, and every surface words are laid on. A surface is written from the top
// layer down: a token, a colour, or =selector for the background that rule sets.
const TEXT = ['--ink', '--ink-2', '--ink-3', '--accent-ink', '--good', '--warn', '--bad'];
const PLAIN = ['--bg', '--side', '--card', '--card-2'];
const SOFT = ['--accent-soft on --bg', '--accent-soft on --side', '--accent-soft on --card'];   // the tip card, the chosen menu entry, the row that is playing
const ON_ANY = [...PLAIN, '--hover', ...SOFT, ...PLAIN.map(s => '=.top on ' + s)];               // and the bar at the top, with the page scrolling under it
// A background that lets through what is under it, with what that is. Each must be a surface of ON_ANY.
// No tint lies on --card-2: the boxes of that colour (a chip, a challenge, a link card, a suggestion)
// hold no row that is tinted, and a prop that is ticked swaps its --card-2 for the tint.
const TINTED = {
  '.top': PLAIN,
  '.nav-profile.on': ['--side'], '.pal-i.on': ['--side'], '.ntf.unread': ['--side'], '.pick-i:hover': ['--side'], '.pick-i.on': ['--side'],
  '.tipcard': ['--bg'], '.ed-skill.on': ['--bg'], '.prop-pick:has(input:checked)': ['--bg', '--card'],
  '.wk.now': ['--card'], '.cb-step.playing': ['--card'], '.cmt.focus': ['--card'], '.person.me': ['--card'], '.link-offer': ['--card'],
};
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
// Shapes of charts, not text, icons or controls: outside this check, whether the rule sets a colour or a fade.
const GRAPHIC = ['.heat rect', '.heat rect.h1', '.heat rect.h2', '.heat rect.h3', '.heat rect.h4', '.heat rect.today', '.graph path', '.graph path.met', '.graph path.hl',
  '.graph.focus path:not(.hl)', '.ring circle', '.ring circle.on', '.ch.done .ring circle.on', '.dc-bar', '.dc-bar.now', '.dc-col:hover .dc-bar', '.dc-hit', '.dc-base'];
// Edges that mark a control or its state, at 3:1 against what they are drawn on. A focus ring holds
// wherever a control can stand (RING_ON). A field is found by its border, its outline or its fill
// wherever a field can stand (FIELD_ON) unless listed here: on the plain surfaces, in a row under the
// pointer, and in a comment that is edited while it is highlighted.
const RING_ON = [...PLAIN, '--hover', ...SOFT];
const FIELD_ON = [...PLAIN, '--hover', '--accent-soft on --card'];
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
  '.badge-ic.on': 'a sheen inside the emblem',
};
// Another kind of the same thing, not a state it can be in: a selector as written, or a pattern. Its
// border is the frame of a box that holds words, as on a rule of one class.
const NOT_A_STATE = ['.notice.warn'];
// A class the templates put only on fields, which is a utility all the same (a font, a width).
const NOT_A_FIELD = ['mono'];
// Blocks that never hold together, as pairs of what their conditions say: ['(pointer: coarse)', '(hover: hover)'].
// Widths that rule each other out (max-width: 600px, min-width: 601px) are worked out and need no entry.
const APART = [];
// A token set outside the three theme blocks (on a rule, or in a style attribute), with why that changes
// no colour this script checks.
const LOCAL_TOKENS = { '--c': 'the colour a swatch offers, set on the swatch itself; nothing is written on it' };
// Properties that paint and that this script does not place, with why they may.
const OTHER_COLOURS = {
  'scrollbar-color': 'the scroll bar, which the browser draws', 'accent-color': 'the tick of a checkbox, which the browser draws',
  '-webkit-tap-highlight-color': 'set to nothing: the flash under a finger is turned off',
};
// Backgrounds nothing is written on.
const NOT_UNDER_TEXT = {
  '.dot': 'a dot', '.sync.saving .dot': 'a dot', '.sync.err .dot': 'a dot', '.ed-skill.has-err::after': 'a dot', '.map-thumb i': 'a dot',
  '.bar': 'a bar', '.bar i': 'a bar', '.cb-prog i': 'a bar', '.cb-prog b': 'a bar', '.cb-link::before': 'a line',
  '.pips i': 'a pip', '.pips i.on1': 'a pip', '.pips i.on2': 'a pip', '.pips i.on3': 'a pip', '.pips i.on4': 'a pip',
  '.palette': 'the dimmed page behind a dialog', '.modal': 'the dimmed page behind a dialog', '.nav-open .scrim': 'the dimmed page behind the menu',
  '.share-prev': 'the checkerboard under a picture', '.tpl canvas': 'under a picture', '.sw': 'a swatch of the colour it offers',
};

// ---------- reading the stylesheet and the templates
const top = (s, sep) => { const out = []; let d = 0, a = 0; for (let i = 0; i < s.length; i++) { if (s[i] === '(') d++; else if (s[i] === ')') d--; else if (s[i] === sep && !d) { out.push(s.slice(a, i)); a = i + 1; } } out.push(s.slice(a)); return out.map(x => x.trim()).filter(Boolean); };
// The words of a value, a function with its brackets counting as one: "1px solid rgb(1, 2, 3)" is three.
const words = s => { const out = []; let d = 0, a = -1; for (let i = 0; i <= s.length; i++) { const c = s[i]; if (c === '(') d++; else if (c === ')') d--; const gap = i === s.length || (!d && /\s/.test(c)); if (!gap && a < 0) a = i; if (gap && a >= 0) { out.push(s.slice(a, i)); a = -1; } } return out; };
// A selector as its compounds (".seg button.on" is ".seg" and "button.on"), and the simple selectors of
// one, without what only says where it stands among its siblings (:first-child, :not(:last-child), :nth-child(2)).
const compounds = sel => { const out = []; let d = 0, a = 0; for (let i = 0; i <= sel.length; i++) { const c = sel[i]; if (c === '(') d++; else if (c === ')') d--; if (i === sel.length || (!d && /[\s>+~]/.test(c))) { if (i > a) out.push(sel.slice(a, i)); a = i + 1; } } return out; };
const PLACE = '(?:first-child|last-child|first-of-type|last-of-type|only-child|only-of-type|empty|root|nth-(?:last-)?(?:child|of-type)\\([^()]*\\))';
const simples = c => c.replace(new RegExp(`:not\\(\\s*:${PLACE}\\s*\\)|:${PLACE}|::[\\w-]+`, 'g'), '').match(/^[a-z][\w-]*|\*|\.[\w-]+|#[\w-]+|\[[^\]]*\]|:[\w-]+(?:\((?:[^()]|\([^()]*\))*\))?/gi) || [];
// A rule that adds a class, an attribute or a pseudo-class to something: the way a state or a variant is written.
const varied = sel => compounds(sel).some(c => { const s = simples(c); return s.length > 1 || s.some(x => /^[:[]/.test(x)); });
const listed = (list, sel) => list.some(x => x instanceof RegExp ? x.test(sel) : x === sel);
const L_SET = ':root[data-theme="light"]', L_AUTO = ':root:where(:not([data-theme="dark"]))';
// The properties that carry a colour. A border is any of its spellings: a side, a logical side, the colour alone.
const BORDER = /^(border|column-rule)(-(top|right|bottom|left|inline|block|inline-start|inline-end|block-start|block-end))?(-color)?$/;
const PLACED = p => /^(color|background|background-color|background-image|fill|stroke|box-shadow|outline|outline-color)$/.test(p) || BORDER.test(p);
const CARRIES = p => PLACED(p) || p === 'text-shadow' || /color$/.test(p);
// What this script looks at in a rule. A block that changes none of it is not a variant worth a second check.
const LOOKED = p => PLACED(p) || /^(opacity|filter|font-size|font-weight)$/.test(p);
const LENGTH = /^[+-]?(\d*\.)?\d+([a-z]+|%)?$/i, WORD = /^(none|hidden|solid|dashed|dotted|double|groove|ridge|inset|outset|thin|medium|thick|auto|initial|inherit|unset|revert|currentcolor|\/)$/i;
let bad = 0, n = 0; const count = { own: 0, token: 0, listed: 0, edge: 0, exempt: 0, variant: 0 };
const fail = (...x) => { bad++; console.log('FAIL', ...x); };
// Whether a block this script does not enter would change a colour: a property that carries one, with a
// value that is more than sizes and keywords, or a filter other than a blur.
const paintsIn = body => [...body.matchAll(/([\w-]+)\s*:\s*([^;{}]+)/g)].some(([, p, v]) => (CARRIES(p) && words(v.replace(/!important\s*$/, '')).some(w => !LENGTH.test(w) && !WORD.test(w))) || (p === 'filter' && !/^(none|blur\([^()]*\))\s*$/.test(v.trim())));
const rules = [];   // { sel, d: { property: value }, light: false, 'set' (the switch) or 'auto' (the system asks), at: the blocks it stands in }
(function walk(s, at) {
  for (let i = 0; i < s.length;) {
    const o = s.indexOf('{', i); if (o < 0) break;
    let d = 1, j = o + 1; for (; d && j < s.length; j++) d += s[j] === '{' ? 1 : s[j] === '}' ? -1 : 0;
    let head = s.slice(i, o).trim(); const body = s.slice(o + 1, j - 1); i = j;
    // A line such as "@layer a, b;" or "@import ...;" before the rule is not part of its selector.
    for (let m; m = /^@[^{;]*;\s*/.exec(head);) { if (/^@import/.test(m[0])) fail(`${m[0].trim()} brings in rules this script does not read; put them in app.css`); head = head.slice(m[0].length); }
    if (/^@(media|supports|layer)\b/.test(head)) { walk(body, at.concat(head.replace(/\s+/g, ' '))); continue; }
    // An animation may fade or move something; no block of another kind may set a colour, because it is not read.
    if (head.startsWith('@')) { if (paintsIn(body)) fail(`${head}: sets a colour, and this script does not read inside such a block; set the colour in a rule, or animate something that is not a colour`); continue; }
    if (body.includes('{')) { fail(`${head}: holds a rule or a block inside it. This script does not read nested CSS; write each rule out with its whole selector`); continue; }
    const d2 = {}; for (const x of top(body, ';')) { const c = x.indexOf(':'), p = x.slice(0, c).trim().toLowerCase(); delete d2[p]; d2[p] = x.slice(c + 1).replace(/!important\s*$/, '').trim(); }   // a property written twice counts where it is written last
    const asked = at.some(a => /prefers-color-scheme:\s*light/.test(a)), rest = at.filter(a => !/prefers-color-scheme:\s*light/.test(a)).join(' ');
    for (let sel of top(head, ',')) {
      let light = false;
      if (asked) { if (!sel.startsWith(L_AUTO)) { fail(`${sel}: inside the light media query, so it must start with ${L_AUTO}`); continue; } light = 'auto'; sel = sel.slice(L_AUTO.length).trim() || ':root'; }
      else if (sel.startsWith(L_SET)) { light = 'set'; sel = sel.slice(L_SET.length).trim() || ':root'; }
      // A token is set in the three theme blocks and nowhere else: this script reads two themes, and a token that changes on one element or in one block is a colour it would hold for another.
      for (const t of Object.keys(d2)) if (t.startsWith('--') && (sel !== ':root' || rest) && !LOCAL_TOKENS[t]) fail(`${sel}${rest ? ' in ' + rest : ''} sets ${t}: a token set outside the three theme blocks is a colour this script would take for another. Set a property there instead, or list the token in LOCAL_TOKENS if it is no theme colour`);
      rules.push({ sel, d: d2, light, at: rest });
    }
  }
})(css, []);
// The light theme written twice says the same both times.
const form = k => rules.filter(r => r.light === k).map(r => (r.at ? r.at + ' ' : '') + r.sel + ' { ' + Object.entries(r.d).map(([p, v]) => p + ': ' + v).sort().join('; ') + ' }').sort();
const [asked, set] = [form('auto'), form('set')]; n++;
if (!set.length) fail('light theme: no rule under [data-theme="light"]');
for (const x of asked) if (!set.includes(x)) fail(`light theme: only where the system asks for it: ${x}`);
for (const x of set) if (!asked.includes(x)) fail(`light theme: only under [data-theme="light"]: ${x}`);
// The page's own scripts and index.html: the classes they put on fields only, and the colours they set in a style attribute.
const pages = fs.readdirSync(web).filter(f => /\.(js|html)$/.test(f) && !['figure3d.js', 'tour.js'].includes(f)).map(f => [f, fs.readFileSync(path.join(web, f), 'utf8')]);
const isField = (tag, attrs) => /^(select|textarea)$/.test(tag) || (tag === 'input' && !/\btype="?(checkbox|radio|file|hidden|range|color|submit|button|reset|image)/.test(attrs)) || /\bcontenteditable|role="(textbox|searchbox|combobox)"/.test(attrs);
const classOn = new Map();   // class → [times on a field, times on anything else]
for (const [, s] of pages) for (const [, tag, attrs] of s.matchAll(/<([a-z][a-z0-9-]*)\b([^<>]*)>/g)) for (const c of ((/\bclass="([^"]*)"/.exec(attrs) || [])[1] || '').split(/\s+/)) if (/^[\w-]+$/.test(c)) { const k = classOn.get(c) || classOn.set(c, [0, 0]).get(c); k[isField(tag, attrs) ? 0 : 1]++; }
const FIELD_CLASS = [...classOn].filter(([c, [on, off]]) => on && !off && !NOT_A_FIELD.includes(c)).map(([c]) => c);

// ---------- colours
const hex = c => '#' + c.slice(0, 3).map(v => Math.round(v).toString(16).padStart(2, '0')).join('');
const over = (f, b) => { const a = f[3] + b[3] * (1 - f[3]); return a ? [0, 1, 2].map(i => (f[i] * f[3] + b[i] * b[3] * (1 - f[3])) / a).concat(a) : [0, 0, 0, 0]; };
const lum = c => { const [r, g, b] = c.slice(0, 3).map(v => { v /= 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
const ratio = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05); };
const CAN_READ = 'write it as a hex colour, rgb(), hsl(), hwb(), color-mix(in srgb, ...) or a token';
// A colour as written: hex, rgb(), hsl(), hwb() (with or without alpha, channels as numbers or percentages),
// var(--token), color-mix(in srgb, ...), black, white, transparent. Anything else is not guessed at: null.
const colour = (v, T) => {
  v = v.trim(); let m;
  if (/^transparent$/i.test(v)) return [0, 0, 0, 0];
  if (/^(black|white)$/i.test(v)) return /^b/i.test(v) ? [0, 0, 0, 1] : [255, 255, 255, 1];
  if (m = /^#([0-9a-f]{3,8})$/i.exec(v)) { let h = m[1]; if (h.length < 5) h = [...h].map(c => c + c).join(''); const p = i => parseInt(h.slice(i, i + 2), 16); return h.length % 2 ? null : [p(0), p(2), p(4), h.length > 6 ? p(6) / 255 : 1]; }
  if (m = /^var\((--[\w-]+)\)$/.exec(v)) return T[m[1]] === undefined ? null : colour(T[m[1]], T);
  if (m = /^(rgb|hsl|hwb)a?\(([^()]*)\)$/i.exec(v)) {
    const p = m[2].split(/[\s,/]+/).filter(Boolean), pc = (x, one) => parseFloat(x) * (x.endsWith('%') ? one / 100 : 1), a = p[3] === undefined ? 1 : pc(p[3], 1);
    if (p.length < 3 || p.length > 4 || [p[0], p[1], p[2], String(a)].some(x => isNaN(parseFloat(x)))) return null;
    if (/^rgb/i.test(m[1])) return [pc(p[0], 255), pc(p[1], 255), pc(p[2], 255), a];
    const turn = /turn$/.test(p[0]) ? 360 : /grad$/.test(p[0]) ? 0.9 : /rad$/.test(p[0]) ? 180 / Math.PI : 1, h = ((parseFloat(p[0]) * turn) % 360 + 360) % 360, x = parseFloat(p[1]) / 100, y = parseFloat(p[2]) / 100;
    const pure = (s, l) => [0, 8, 4].map(k => { const t = (k + h / 30) % 12; return l - s * Math.min(l, 1 - l) * Math.max(-1, Math.min(t - 3, 9 - t, 1)); });
    if (/^hsl/i.test(m[1])) return pure(x, y).map(c => c * 255).concat(a);
    return (x + y >= 1 ? [0, 0, 0].map(() => x / (x + y)) : pure(1, 0.5).map(c => c * (1 - x - y) + x)).map(c => c * 255).concat(a);   // hwb: whiteness x, blackness y
  }
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

for (const theme of ['dark', 'light']) {
  const mine = rules.filter(r => !r.light || (theme === 'light' && r.light === 'set'));
  // Later over earlier, and a property set again counts where it was set last: background and
  // background-color, written in either order, are read in the order they are written.
  const merge = (a, b) => { const o = { ...a }; for (const [p, v] of Object.entries(b)) { delete o[p]; o[p] = v; } return o; };
  // Each selector of this theme: what it sets outside any block, and inside each block it stands in.
  const sels = new Map(); let whole = new Map();
  for (const r of mine) {
    const m = sels.get(r.sel) || sels.set(r.sel, new Map()).get(r.sel); m.set(r.at, merge(m.get(r.at) || {}, r.d));
    whole.set(r.sel, merge(whole.get(r.sel) || {}, r.d));   // everything together, for looking a rule up by its name
  }
  const T = Object.fromEntries(Object.entries(whole.get(':root')).filter(([p]) => p.startsWith('--')));
  // A value with its tokens written out, so "var(--shadow)" is read as the shadow it stands for.
  const spell = v => { for (let i = 0; i < 4 && /var\(--[\w-]+\)/.test(v); i++) v = v.replace(/var\((--[\w-]+)\)/g, (m, t) => T[t] === undefined ? m : T[t]); return v; };
  const local = w => LOCAL_TOKENS[(/^var\((--[\w-]+)\)$/.exec(w) || [])[1]];
  // What a value paints with: its colours, and the words that should be colours and cannot be read.
  const paints = v => { const out = { colours: [], unread: [], lengths: [], inset: false, local: false };
    for (const layer of top(spell(v), ',')) for (const w of words(layer)) { const c = colour(w, T); if (/^inset$/i.test(w)) out.inset = true; if (c) out.colours.push(c); else if (LENGTH.test(w)) out.lengths.push(parseFloat(w)); else if (local(w)) out.local = true; else if (!WORD.test(w)) out.unread.push(w); }
    return out; };
  // The background a rule sets, read in the order it is written: a colour, the stops of a gradient over it, or a picture.
  const bgOf = d => { let c = null, stops = null, picture = false, unread = [];
    const image = v => { stops = null; picture = false; for (const layer of top(v, ',')) for (const w of words(layer)) {
      const g = /^(?:repeating-)?(?:linear|radial|conic)-gradient\((.*)\)$/i.exec(w);
      if (g) { for (const part of top(g[1], ',')) for (const x of words(spell(part))) { const k = colour(x, T); if (k) stops = (stops || []).concat([k]); else if (!LENGTH.test(x) && !/^(to|at|from|in|top|right|bottom|left|center|circle|ellipse|closest-side|closest-corner|farthest-side|farthest-corner|srgb|oklab|oklch|hsl|shorter|longer|hue)$/i.test(x)) unread.push(x); } }
      else if (/^(url|image-set|image|element|cross-fade)\(/i.test(w)) picture = true;
      else if (!/^none$/i.test(w)) return w; } };
    for (const [p, raw] of Object.entries(d)) {
      if (p === 'background-color') { const k = colour(spell(raw), T); if (k) c = k; else if (local(raw)) c = 'local'; else if (!/^(inherit|initial|unset|currentcolor)$/i.test(raw)) unread.push(raw); }
      else if (p === 'background-image') { const w = image(raw); if (w) unread.push(w); }
      else if (p === 'background') { c = null; stops = null; picture = false;
        for (const layer of top(raw, ',')) for (const w of words(layer)) { const k = colour(spell(w), T);
          if (k) c = k; else if (local(w)) c = 'local'; else if (/gradient\(|^(url|image-set|image|element|cross-fade)\(/i.test(w)) { const s0 = stops, x = image(w); stops = s0 && stops ? s0.concat(stops) : stops || s0; if (x) unread.push(x); }
          else if (!LENGTH.test(w) && !WORD.test(w) && !/^(center|top|right|bottom|left|no-repeat|repeat|repeat-x|repeat-y|space|round|cover|contain|fixed|scroll|local|border-box|padding-box|content-box|text)$/i.test(w)) unread.push(w); } }
    }
    if (c && c !== 'local' && c[3] === 0) c = null;
    return { c, stops, picture, unread, none: !c && !stops && !picture }; };
  // The edges a rule draws: the colour of a border in any spelling, of an outline, and of a shadow that is not blurred.
  const edgesOf = d => { const out = { only: [], whole: [], ring: [], outline: [], unread: [] };
    const take = (to, p, v) => { const x = paints(v); for (const w of x.unread) out.unread.push([p, v, w]); for (const c of x.colours) if (c[3] > 0) to.push([c, `${p}: ${v}`]); return x; };
    for (const [p, v] of Object.entries(d)) {
      if (BORDER.test(p)) take(/-color$/.test(p) ? out.only : out.whole, p, v);
      else if (/^outline(-color)?$/.test(p)) take(out.outline, p, v);
      else if (p === 'box-shadow') for (const layer of top(spell(v), ',')) { const soft = [], x = take(soft, p, layer); if (soft.length && !(x.lengths[2] > 0)) out.ring.push(...soft); }   // offsets, then the blur: none or 0 is a hard edge
    }
    return out; };
  const withinPx = ats => { const all = ats.join(' '); if (/,|\bor\b|\bnot\b/.test(all)) return true;
    const lo = [...all.matchAll(/min-width:\s*([\d.]+)px/g)].map(m => +m[1]), hi = [...all.matchAll(/max-width:\s*([\d.]+)px/g)].map(m => +m[1]);
    return !(lo.length && hi.length && Math.max(...lo) > Math.min(...hi)); };
  const together = ats => withinPx(ats) && !APART.some(([a, b]) => ats.some(x => x.includes(a)) && ats.some(x => x.includes(b)));
  // The ways a rule can apply: outside any block, and inside every set of its blocks that can hold together.
  const V = [];
  for (const [sel, m] of sels) {
    if (sel === ':root') continue;
    const base = m.get('') || {}, blocks = [...m].filter(([at, d]) => at && Object.keys(d).some(LOOKED));
    if (m.has('')) V.push([sel, base, '']);
    if (blocks.length > 6) { fail(`${theme} ${sel}: changed in ${blocks.length} blocks, more than this script tries together; fewer blocks, or one rule per block with its own selector`); continue; }
    for (let k = 1; k < 1 << blocks.length; k++) {
      const some = blocks.filter((b, i) => k >> i & 1);
      if (!together(some.map(b => b[0]))) continue;
      V.push([sel, some.reduce((d, b) => merge(d, b[1]), base), ` in ${some.map(b => b[0]).join(' and ')}`]); count.variant++;
    }
  }
  // Read everything first: a colour that cannot be turned into numbers fails wherever it stands, and so
  // does a property that paints and is not one this script places.
  for (const r of mine) for (const [p, v] of Object.entries(r.d)) {
    const at = `${theme} ${r.sel}${r.at ? ' in ' + r.at : ''}: ${p}: ${v}`;
    if (p.startsWith('--') || !CARRIES(p)) continue;
    if (!PLACED(p) && !OTHER_COLOURS[p]) fail(`${at} paints with a property this script does not place; use color, background, border, outline or box-shadow, or say in OTHER_COLOURS why it may`);
    const unread = /^background/.test(p) ? bgOf({ [p]: v }).unread : paints(v).unread;
    if (!/^background/.test(p) && paints(v).local) fail(`${at} takes its colour from a token set on the element, which this script cannot know; only a background that nothing is written on may (NOT_UNDER_TEXT)`);
    for (const w of unread) fail(`${at}: "${w}" is not a colour this script can turn into numbers; ${CAN_READ}`);
  }
  // A surface as one or more opaque colours: the layers laid over each other from the bottom up.
  const surface = spec => spec.split(' on ').reverse().reduce((under, layer) => {
    const b = layer.startsWith('=') ? bgOf(whole.get(layer.slice(1)) || {}) : null, tops = b ? b.stops || (b.c && b.c !== 'local' ? [b.c] : []) : [colour(layer.startsWith('--') ? `var(${layer})` : layer, T)];
    if (!tops.length || tops.includes(null) || (b && b.picture)) return [null];
    return under.flatMap(u => tops.map(t => u && over(t, u)));
  }, [[0, 0, 0, 0]]);
  const pair = (what, fg, spec, need) => {
    const on = surface(spec);
    for (const s of on) {
      n++;
      if (!s || s[3] < 0.999) { fail(`${theme} ${what}: the surface "${spec}" is not a colour this script can read`); continue; }
      const f = over(fg, s), r = ratio(f, s);
      if (r < need) fail(`${theme} ${what}: ${hex(f)} on ${hex(s)} (${spec}) is ${r.toFixed(2)}:1, needs ${need}:1`);
    }
    // On a gradient the ends are checked; a colour between its ends meets it somewhere along the way.
    const ends = on.filter(Boolean).map(lum), l = lum(over(fg, on.find(Boolean) || [0, 0, 0, 1]));
    if (ends.length > 1 && l > Math.min(...ends) && l < Math.max(...ends)) fail(`${theme} ${what}: ${hex(fg)} lies between the ends of the gradient "${spec}", so somewhere on it the two are alike`);
  };
  const large = d => { const px = parseFloat(d['font-size']), w = parseFloat(d['font-weight']) || 400; return /px$/.test(d['font-size'] || '') && (px >= 24 || (px >= 18.66 && w >= 700)); };

  // Each colour used for words, on each surface words are laid on.
  for (const t of TEXT) for (const s of ON_ANY) pair(`text in ${t}`, colour(`var(${t})`, T), s, 4.5);
  for (const [s, ts] of Object.entries(ON_TINT)) for (const t of ts) pair(`text in ${t}`, colour(`var(${t})`, T), s, 4.5);

  for (const [sel, d, where] of V) {
    const name = sel + where, own = bgOf(d);
    // What is written in this rule's colour: words, an icon, or the words of an svg.
    for (const prop of ['color', 'fill', 'stroke']) {
      const v = d[prop]; if (v === undefined || skip(v)) continue;
      if (EXEMPT[sel]) { count.exempt++; continue; }
      if (GRAPHIC.includes(sel)) continue;
      const fg = colour(v, T), kind = (ON[sel] || {}).kind || (/svg\.i$/.test(sel) ? 'icon' : large(d) ? 'large text' : 'text'), need = kind === 'text' ? 4.5 : 3;
      if (!fg) continue;   // said above, where everything is read: it cannot be turned into numbers, or is set on the element
      const mine = prop === 'color' ? own : { none: true }, token = (/^var\((--[\w-]+)\)$/.exec(v) || [])[1];
      if (ON[sel]) { count.listed++; for (const s of ON[sel].on) pair(`${name} (${kind})`, fg, s, need); }
      else if (mine.stops || mine.picture || mine.c === 'local') fail(`${theme} ${name}: ${prop}: ${v} over a gradient, a picture or a colour set on the element; list in ON what it is drawn on (a gradient as =${sel}, which holds it to each of its ends), or in EXEMPT why it is left out`);
      else if (mine.c && mine.c[3] >= 0.999) { count.own++; pair(`${name} (${kind})`, fg, hex(mine.c), need); }
      else if (mine.c) fail(`${theme} ${name}: ${prop}: ${v} over a background that lets through what is under it; list what that is in ON`);
      else if (TEXT.includes(token)) count.token++;
      else fail(`${theme} ${name}: ${prop}: ${v} is not one of the colours for words and the rule sets no background; list what it is drawn on in ON, or why it is left out in EXEMPT`);
    }
    // A background set without a colour of the rule's own (none, or one it inherits): words written in a
    // colour from another rule end up on it.
    if (!own.none && (d.color === undefined || /^(inherit|currentcolor)$/i.test(d.color)) && !EXEMPT[sel]) {
      const bg = ['background', 'background-color', 'background-image'].filter(p => d[p]).map(p => `${p}: ${d[p]}`).join('; '), as = (/^var\((--[\w-]+)\)$/.exec(d['background-color'] || d.background || '') || [])[1] || '=' + sel;
      if (UNDER[sel]) { count.listed++; const u = whole.get(UNDER[sel]) || {}; if (!u.color || !colour(u.color, T)) fail(`${theme} ${name}: UNDER names ${UNDER[sel]}, which sets no colour this script can read`); else if (own.stops || own.picture || own.c === 'local') fail(`${theme} ${name}: ${bg} is a gradient or a picture, which UNDER cannot hold a colour to; list the rule whose colour lands on it in ON, drawn on =${sel}`); else pair(`${UNDER[sel]} on ${name} (text)`, colour(u.color, T), hex(own.c), 4.5); }
      else if (NOT_UNDER_TEXT[sel]) count.exempt++;
      else if (Object.values(ON).some(o => o.on.some(s => s.split(' on ').includes('=' + sel)))) count.listed++;
      else if (own.stops || own.picture || own.c === 'local') fail(`${theme} ${name}: ${bg} puts a gradient, a picture or a colour set on the element where words may be written, and such a surface is not one colour. List in ON the rule whose colour lands on it, drawn on =${sel}, or say in NOT_UNDER_TEXT that nothing is written on it`);
      else if (own.c[3] >= 0.999) { if (!ON_ANY.includes(as) && !ON_TINT[as]) fail(`${theme} ${name}: ${bg} is not a surface words are checked on; add it to ON_ANY, or to UNDER with the rule whose colour lands on it, or to NOT_UNDER_TEXT`); }
      else if (!TINTED[sel]) fail(`${theme} ${name}: ${bg} lets through what is under it; list what that is in TINTED`);
      else for (const u of TINTED[sel]) if (!ON_ANY.includes(`${as} on ${u}`)) fail(`${theme} ${name}: words are not checked on "${as} on ${u}"; add that surface to ON_ANY, and see that every colour for words holds on it`);
    }
    // A filter over a button changes its colours: brightness is worked out, anything else is not guessed.
    if (d.filter && !/^(none|blur\([^()]*\))$/.test(d.filter)) {
      const k = (/^brightness\(([\d.]+)\)$/.exec(d.filter) || [])[1], base = whole.get(sel.replace(/:(hover|active|focus-visible|focus)/g, '')) || {};
      const f = k && base.color && colour(base.color, T), b = k && bgOf(base).c;
      if (!f || !b || b === 'local') fail(`${theme} ${name}: filter: ${d.filter} is not one this script can work out; change the colours themselves, with tokens`);
      else { count.own++; const lit = c => c.slice(0, 3).map(x => Math.min(255, x * k)).concat(c[3]); pair(`${name} (text under the filter)`, lit(f), hex(lit(b)), 4.5); }
    }
    // A fade takes contrast from everything inside it.
    if (d.opacity !== undefined && parseFloat(d.opacity) < 1 && EXEMPT[sel]) count.exempt++;
    else if (d.opacity !== undefined && parseFloat(d.opacity) < 1 && !GRAPHIC.includes(sel)) fail(`${theme} ${name}: opacity: ${d.opacity} fades the words in it below what their colours were checked at; say in EXEMPT why that is allowed, or do not fade`);
    // Edges. A field is found by its border, outline, ring or fill.
    const subject = compounds(sel).pop(), cls = FIELD_CLASS.find(c => new RegExp(`\\.${c}(?![\\w-])`).test(subject)), e = edgesOf(d);
    if (/^(input|select|textarea)(?![\w-])/.test(subject) || cls) {
      const lines = [...e.whole, ...e.only, ...e.outline, ...e.ring];
      if (lines.length || EDGE[sel]) {
        count.edge++;
        for (const spec of EDGE[sel] || FIELD_ON) for (const s of surface(spec)) {
          n++; const fill = own.c && own.c !== 'local' ? over(own.c, s) : s;
          const best = Math.max(ratio(fill, s), ...lines.map(([c]) => ratio(over(c, fill), s)));
          if (best < 3) fail(`${theme} ${name}: the edge of the field${cls ? ` (the templates put the class ${cls} on fields only; NOT_A_FIELD says when such a class is no field)` : ''} (${lines.map(l => l[1]).join('; ') || 'none'}; fill ${hex(fill)}) on ${hex(s)} (${spec}) is ${best.toFixed(2)}:1, needs 3:1`);
        }
      }
      continue;
    }
    // A focus ring, wherever a control can stand.
    for (const [c, as] of e.outline) { count.edge++; for (const s of RING_ON) pair(`${name} (ring, ${as})`, c, s, 3); }
    // A mark of a state: a rule that changes only the colour of an edge or draws a hard-edged shadow, or
    // one that adds a class, an attribute or a pseudo-class to something and draws a border.
    const mark = [...e.only, ...e.ring, ...(varied(sel) || EDGE[sel] ? e.whole : [])];
    if (EDGE[sel] && !mark.length) fail(`${theme} EDGE lists ${sel}, which sets no border and no ring`);
    if (mark.length && !/:hover/.test(sel)) {
      if (EDGE[sel]) { count.edge++; for (const [c, as] of mark) for (const s of EDGE[sel]) pair(`${name} (edge, ${as})`, c, s, 3); }
      else if (DECOR[sel] || listed(NOT_A_STATE, sel)) count.exempt++;
      else fail(`${theme} ${name}: ${mark.map(m => m[1]).join('; ')} looks like the mark of a state. If it is one, list what it is drawn on in EDGE and it is held to 3:1. If the words beside it say the same, give the reason in DECOR. If the rule is another kind of the same thing and no state, add its selector to NOT_A_STATE`);
    }
  }
  // A table entry for a rule that is gone would hide the next rule of that name.
  for (const [name, t] of Object.entries({ ON, UNDER, EXEMPT, EDGE, DECOR, NOT_UNDER_TEXT, TINTED })) for (const sel of Object.keys(t)) if (!sels.has(sel)) fail(`${theme} ${name} lists ${sel}, which the stylesheet no longer has`);
  for (const s of Object.keys(ON_TINT)) if (!sels.has(s.slice(1))) fail(`${theme} ON_TINT lists ${s.slice(1)}, which the stylesheet no longer has`);
  for (const sel of [...GRAPHIC, ...NOT_A_STATE.filter(x => typeof x === 'string')]) if (!sels.has(sel)) fail(`${theme} GRAPHIC or NOT_A_STATE lists ${sel}, which the stylesheet no longer has`);
}

// A style attribute in a template: a colour there is only a colour for words, or a level of the scale as
// a swatch; a token there is only one of LOCAL_TOKENS. What carries no colour (border-radius, border: 0) is not looked at.
let inline = 0;
for (const [f, s] of pages) for (const m of s.matchAll(/style="([^"]*)"/g)) for (const x of m[1].split(';')) {
  const c = x.indexOf(':'), p = x.slice(0, Math.max(c, 0)).trim(), v = x.slice(c + 1).trim();
  if (p.startsWith('--')) { inline++; n++; if (!LOCAL_TOKENS[p]) fail(`web/${f}: style="${p}:${v}" sets a token on one element, a colour this script would take for another; use a class in app.css, or list the token in LOCAL_TOKENS if it is no theme colour`); continue; }
  if (!(CARRIES(p) || p === 'opacity' || p === 'filter') || (CARRIES(p) && !words(v).some(w => !LENGTH.test(w) && !WORD.test(w)))) continue;
  inline++; n++;
  const t = (/^var\((--[\w-]+)\)$/.exec(v) || [])[1];
  if (!(p === 'color' && TEXT.includes(t)) && !(p === 'background' && /^var\(--l(\d|\$\{i\})\)$/.test(v))) fail(`web/${f}: style="${p}:${v}" sets a colour this script cannot place; use a colour for words (color:var(--ink-2)), or a class in app.css`);
}
console.log(`contrast: 2 themes, ${TEXT.length} colours for words on ${ON_ANY.length + Object.keys(ON_TINT).length} surfaces, ${count.own / 2} rules on their own background, ${count.token / 2} by their colour for words, ${count.listed / 2} listed, ${count.edge / 2} edges and rings, ${count.variant / 2} rules again inside a block, ${inline} colours in templates; ${count.exempt / 2} exempt, ${GRAPHIC.length} rules for chart shapes not looked at; ${n} checks, ${bad} failed`);
process.exit(bad ? 1 : 0);
