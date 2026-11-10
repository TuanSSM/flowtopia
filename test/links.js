'use strict';
// The page's link reader (web/links.js) against test/links-fixtures.json, the same cases
// TestParseLink puts to the server's. Run: node test/links.js
const fs = require('fs'), path = require('path');
const { parseLink } = require('../web/links.js');
const fx = JSON.parse(fs.readFileSync(path.join(__dirname, 'links-fixtures.json'), 'utf8'));
let fails = 0, n = 0;
const check = (ok, msg) => { n++; if (!ok) { fails++; console.log('FAIL ' + msg); } };
if (Object.keys(fx.good).length < 30 || fx.bad.length < 30 || Object.keys(fx.kinds).length < 4) {
  console.log('FAIL the fixtures look cut short'); process.exit(1);
}
for (const [raw, want] of Object.entries(fx.good)) {
  const l = parseLink(raw);
  check(l && l.url === want, `parseLink(${JSON.stringify(raw)}) = ${l && l.url}; want ${want}`);
  const again = parseLink(want);
  check(again && again.url === want, `a canonical link must not change: ${want} became ${again && again.url}`);
}
for (const raw of fx.bad) {
  const l = parseLink(raw);
  check(l === null, `parseLink(${JSON.stringify(raw)}) should be refused, got ${l && l.url}`);
}
for (const [raw, [site, kind, id]] of Object.entries(fx.kinds)) {
  const l = parseLink(raw) || {};
  check(l.site === site && l.kind === kind && l.id === id, `parseLink(${JSON.stringify(raw)}) = ${JSON.stringify(l)}; want ${site}, ${kind}, ${id}`);
}
console.log(fails ? `${fails} of ${n} link checks failed` : `PASS ${n} link checks`);
process.exit(fails ? 1 : 0);
