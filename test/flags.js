'use strict';
// An admin can only switch a feature the Features page lists (MAP-02 in docs/audit-plan.md). This
// reads FLAGS in web/app.js and flagDefaults in main.go and fails if one has a key the other lacks,
// or if a switch has no name or no description. The flags only a mode that federates has are two lists of
// their own, FED_FLAGS in web/app.js and fedFlagDefaults in main.go, held to the same rules, and no key may be
// in both kinds.
//   node test/flags.js          check        node test/flags.js --print   print the page's keys
const fs = require('fs'), path = require('path');
const app = fs.readFileSync(path.join(__dirname, '..', 'web', 'app.js'), 'utf8');
const go = fs.readFileSync(path.join(__dirname, '..', 'main.go'), 'utf8');
const fails = [];
// pair reads one list of the page and its counterpart in main.go, and checks them against each other.
function pair(jsName, goName, need) {
  const list = app.match(new RegExp(`\\nconst ${jsName} = \\[\\n([\\s\\S]*?)\\n\\];`));
  if (!list) { fails.push(`web/app.js has no ${jsName} list`); return { page: [], server: [] }; }
  const rows = [...list[1].matchAll(/^\s*\['([A-Za-z0-9]+)', '((?:[^'\\]|\\.)*)', '((?:[^'\\]|\\.)*)'\],$/gm)].map(m => ({ key: m[1], name: m[2], text: m[3] }));
  const page = rows.map(r => r.key);
  const block = go.match(new RegExp(`var ${goName} = map\\[string\\]bool\\{([^}]*)\\}`));
  if (!block) { fails.push(`main.go has no ${goName}`); return { page, server: [] }; }
  const server = [...block[1].matchAll(/"([A-Za-z0-9]+)":\s*(?:true|false)/g)].map(m => m[1]);
  const missing = server.filter(k => !page.includes(k)), extra = page.filter(k => !server.includes(k));
  if (missing.length || extra.length) fails.push(`${jsName} and ${goName} differ. Only in main.go: ${missing.join(', ') || 'none'}. Only in web/app.js: ${extra.join(', ') || 'none'}.`);
  if (rows.length !== list[1].split('\n').filter(l => l.trim()).length) fails.push(`a line in ${jsName} is not a [key, name, description] row this check can read`);
  const twice = page.filter((k, i) => page.indexOf(k) !== i);
  if (twice.length) fails.push(`listed twice: ${twice.join(', ')}`);
  for (const r of rows) {
    if (!r.name.trim()) fails.push(`${r.key} has no name`);
    if (r.text.trim().length < 20) fails.push(`${r.key} has no description an admin could decide from`);
    if (/[–—]/.test(r.name + r.text)) fails.push(`${r.key} has a long dash in its text`);
  }
  if (server.length < need) fails.push(`fewer than ${need} flags read from ${goName}, so the reading is probably broken`);
  return { page, server };
}
const all = pair('FLAGS', 'flagDefaults', 10), fed = pair('FED_FLAGS', 'fedFlagDefaults', 1);
if (process.argv.includes('--print')) { console.log(all.page.join('\n')); process.exit(0); }
const both = fed.server.filter(k => all.server.includes(k) || all.page.includes(k));
if (both.length) fails.push(`a flag of every mode and of a mode that federates at once: ${both.join(', ')}`);
if (fails.length) { for (const f of fails) console.log('FAIL ' + f); process.exit(1); }
console.log(`flags: ${all.page.length} switches, the same in web/app.js and main.go; ${fed.page.length} more only in a mode that federates, the same in both`);
