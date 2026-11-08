// The JavaScript validator against the fixtures: good-*.json must pass and bad-*.json must fail.
// TestCatalogFixtures in main_test.go runs the Go validator against the same files.
const fs = require('fs'), path = require('path');
const { checkCatalog } = require(path.join(__dirname, '..', '..', 'web', 'catalog-check.js'));
let bad = 0, n = 0;
for (const f of fs.readdirSync(__dirname).filter(f => f.endsWith('.json')).sort()) {
  const { errs } = checkCatalog(JSON.parse(fs.readFileSync(path.join(__dirname, f), 'utf8'))), want = f.startsWith('good-');
  n++;
  if ((errs.length === 0) !== want) { bad++; console.log(`FAIL ${f}: ${want ? 'should pass' : 'should fail'}${errs.length ? ': ' + errs.map(e => e.msg).join('; ') : ''}`); }
}
console.log(`${n} fixtures, ${bad} disagreements`);
process.exit(bad ? 1 : 0);
