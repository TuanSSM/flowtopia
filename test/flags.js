'use strict';
// An admin can only switch a feature the Features page lists (MAP-02 in docs/audit-plan.md). This
// reads FLAGS in web/app.js and flagDefaults in main.go and fails if one has a key the other lacks,
// or if a switch has no name or no description.
//   node test/flags.js          check        node test/flags.js --print   print the page's keys
const fs = require('fs'), path = require('path');
const app = fs.readFileSync(path.join(__dirname, '..', 'web', 'app.js'), 'utf8');
const go = fs.readFileSync(path.join(__dirname, '..', 'main.go'), 'utf8');
const list = app.match(/\nconst FLAGS = \[\n([\s\S]*?)\n\];/);
if (!list) { console.log('FAIL web/app.js has no FLAGS list'); process.exit(1); }
const rows = [...list[1].matchAll(/^\s*\['([A-Za-z0-9]+)', '((?:[^'\\]|\\.)*)', '((?:[^'\\]|\\.)*)'\],$/gm)].map(m => ({ key: m[1], name: m[2], text: m[3] }));
const page = rows.map(r => r.key);
if (process.argv.includes('--print')) { console.log(page.join('\n')); process.exit(0); }
const block = go.match(/var flagDefaults = map\[string\]bool\{([^}]*)\}/);
if (!block) { console.log('FAIL main.go has no flagDefaults'); process.exit(1); }
const server = [...block[1].matchAll(/"([A-Za-z0-9]+)":\s*(?:true|false)/g)].map(m => m[1]);
const fails = [];
const missing = server.filter(k => !page.includes(k)), extra = page.filter(k => !server.includes(k));
if (missing.length || extra.length) fails.push(`flag keys differ. Only in main.go: ${missing.join(', ') || 'none'}. Only in web/app.js: ${extra.join(', ') || 'none'}.`);
if (rows.length !== list[1].split('\n').filter(l => l.trim()).length) fails.push('a line in FLAGS is not a [key, name, description] row this check can read');
const twice = page.filter((k, i) => page.indexOf(k) !== i);
if (twice.length) fails.push(`listed twice: ${twice.join(', ')}`);
for (const r of rows) {
  if (!r.name.trim()) fails.push(`${r.key} has no name`);
  if (r.text.trim().length < 20) fails.push(`${r.key} has no description an admin could decide from`);
  if (/[–—]/.test(r.name + r.text)) fails.push(`${r.key} has a long dash in its text`);
}
if (server.length < 10) fails.push('fewer than 10 flags read from main.go, so the reading is probably broken');
if (fails.length) { for (const f of fails) console.log('FAIL ' + f); process.exit(1); }
console.log(`flags: ${page.length} switches, the same in web/app.js and main.go`);
