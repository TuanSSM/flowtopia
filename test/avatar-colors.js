'use strict';
// The avatar palettes (web/avatar-def.json) against the demo canvas, in both themes (web/app.css --side).
// - Prop colours carry the move, so each must reach 3:1 contrast against both backgrounds.
// - The 12 skin tones must span the range of human skin, from very light to very dark, in even steps
//   of lightness (CIE L*).
//   A skin tone cannot reach 3:1 against both a light and a dark page (a pale tone on a light page),
//   so the figure reads by its shading there, and the props by their contrast.
// - The default outfit keeps its parts apart: top, bottoms and skin at least 1.4:1 from each other.
//   node test/avatar-colors.js
const fs = require('fs'), path = require('path');
const def = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'avatar-def.json'), 'utf8'));
const css = fs.readFileSync(path.join(__dirname, '..', 'web', 'app.css'), 'utf8');
const lum = hex => { const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255).map(v => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4); return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]; };
const ratio = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05); };
const sides = [...css.matchAll(/--side:\s*(#[0-9a-f]{6})/gi)].map(m => m[1].toLowerCase());
const bgs = [...new Set(sides)];
let bad = 0, n = 0;
const check = (name, ok, x = '') => { n++; if (!ok) { bad++; console.log(`FAIL ${name}`, x); } };
check('both themes give the demo background', bgs.length === 2, bgs);
for (const c of def.propColors) for (const bg of bgs) check(`prop colour ${c} against ${bg}: ${ratio(c, bg).toFixed(2)}:1`, ratio(c, bg) >= 3);
const L = def.skinTones.map(lum);
check('12 skin tones', def.skinTones.length === 12);
check(`skin tones run from light to dark (${L[0].toFixed(3)} to ${L[11].toFixed(3)} relative luminance)`, L[0] >= 0.6 && L[11] <= 0.02, L);
for (let i = 1; i < L.length; i++) check(`skin tone ${i + 1} is darker than ${i}`, L[i] < L[i - 1]);
// Even steps in lightness as eyes see it (CIE L*): no step more than one and a half times another.
const lstar = y => y > 0.008856 ? 116 * Math.cbrt(y) - 16 : 903.3 * y, steps = L.slice(1).map((l, i) => lstar(L[i]) - lstar(l));
check('skin tones step evenly in lightness', Math.max(...steps) <= 1.5 * Math.min(...steps), steps.map(s => s.toFixed(1)));
const d = def.defaults;
for (const [a, b] of [['topColor', 'bottomsColor'], ['topColor', 'skin'], ['bottomsColor', 'skin']]) check(`default ${a} and ${b} differ by ${ratio(d[a], d[b]).toFixed(2)}:1`, ratio(d[a], d[b]) >= 1.4);
console.log(`avatar colours: ${def.propColors.length} prop colours on ${bgs.length} backgrounds, ${def.skinTones.length} skin tones, ${n} checks, ${bad} failed`);
process.exit(bad ? 1 : 0);
