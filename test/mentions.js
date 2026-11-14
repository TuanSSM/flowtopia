'use strict';
// The page's mention reader (mentionsIn in web/links.js) against test/mentions-fixtures.json, the same cases
// TestMentions puts to the server's parseMentions: @name is a member here, @name@host a handle of another server
// and never a member here (docs/federation.md, finding 9). Run: node test/mentions.js
const fs = require('fs'), path = require('path');
const { mentionsIn } = require('../web/links.js');
const fx = JSON.parse(fs.readFileSync(path.join(__dirname, 'mentions-fixtures.json'), 'utf8'));
let fails = 0, n = 0;
const check = (ok, msg) => { n++; if (!ok) { fails++; console.log('FAIL ' + msg); } };
if (!Array.isArray(fx.cases) || fx.cases.length < 20) { console.log('FAIL the fixtures look cut short'); process.exit(1); }
for (const c of fx.cases) {
  const local = [], remote = [];
  for (const m of mentionsIn(c.text)) {
    const want = '@' + m.name + (m.local ? '' : '@' + m.host);
    check(c.text.slice(m.start, m.end).toLowerCase() === want.toLowerCase(), `${JSON.stringify(c.text)}: the mention at ${m.start} to ${m.end} reads ${JSON.stringify(c.text.slice(m.start, m.end))}, want ${JSON.stringify(want)}`);
    (m.local ? local : remote).push(m.local ? m.name : `${m.name}@${m.host}`);
  }
  check(JSON.stringify(local) === JSON.stringify(c.local) && JSON.stringify(remote) === JSON.stringify(c.remote),
    `mentionsIn(${JSON.stringify(c.text)}) = local ${JSON.stringify(local)}, remote ${JSON.stringify(remote)}; want ${JSON.stringify(c.local)}, ${JSON.stringify(c.remote)}`);
}
console.log(fails ? `${fails} of ${n} mention checks failed` : `PASS ${n} mention checks`);
process.exit(fails ? 1 : 0);
