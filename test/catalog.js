// Checks prop catalogs against docs/catalog-format.md, with the same rules the editor uses.
//   node test/catalog.js web/catalog/poi.json [...]
const fs = require('fs'), path = require('path');
const { checkCatalog } = require(path.join(__dirname, '..', 'web', 'catalog-check.js'));
let failed = 0;
for (const file of process.argv.slice(2)) {
  let c;
  try { c = JSON.parse(fs.readFileSync(file, 'utf8')); } catch (e) { console.log(`${file}: not valid JSON: ${e.message}`); failed++; continue; }
  const { errs, warn } = checkCatalog(c), skills = Array.isArray(c.skills) ? c.skills : [];
  console.log(`${file}: ${skills.length} skills, ${(c.branches || []).length} branches, ${Object.keys(c.refs || {}).length} refs, ${(c.phases || []).length} phases, ${skills.filter(s => s.star).length} unconfirmed, ${skills.filter(s => s.care).length} care`);
  for (const e of errs.slice(0, 40)) console.log('  ERROR ' + e.msg);
  if (errs.length > 40) console.log(`  ... and ${errs.length - 40} more errors`);
  for (const w of warn.slice(0, 12)) console.log('  warn  ' + w.msg);
  if (errs.length) failed++;
}
process.exit(failed ? 1 : 0);
