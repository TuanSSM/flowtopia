'use strict';
// The server accepts only badges the page knows (S7 in docs/hardening-plan.md). This reads the
// badge registry in web/game.js and the list in main.go (badgeKeys) and fails if they differ.
//   node test/badges.js          check        node test/badges.js --print   print the page's keys
const fs = require('fs'), path = require('path');
const game = fs.readFileSync(path.join(__dirname, '..', 'web', 'game.js'), 'utf8');
const go = fs.readFileSync(path.join(__dirname, '..', 'main.go'), 'utf8');
const page = [];
// Tiers: ...achTiers('base', desc, icon, group, [[of, 'Name'], ...], n)
for (const m of game.matchAll(/achTiers\('([a-z0-9-]+)',[\s\S]*?\[(\[\d+, '[^']*'\](?:, \[\d+, '[^']*'\])*)\]/g)) for (const t of m[2].matchAll(/\[(\d+),/g)) page.push(`${m[1]}-${t[1]}`);
for (const m of game.matchAll(/\{ key: '([a-z0-9:_-]+)'/g)) page.push(m[1]);
if (process.argv.includes('--print')) { console.log(page.join('\n')); process.exit(0); }
const block = go.match(/var badgeKeys = map\[string\]bool\{([\s\S]*?)\n\}/);
if (!block) { console.log('FAIL main.go has no badgeKeys'); process.exit(1); }
const server = [...block[1].matchAll(/"([a-z0-9:_-]+)":\s*true/g)].map(m => m[1]);
const missing = page.filter(k => !server.includes(k)), extra = server.filter(k => !page.includes(k));
if (missing.length || extra.length || page.length < 20) {
  console.log(`FAIL badge keys differ. Only in web/game.js: ${missing.join(', ') || 'none'}. Only in main.go: ${extra.join(', ') || 'none'}.`);
  process.exit(1);
}
console.log(`badges: ${page.length} keys, the same in web/game.js and main.go`);
