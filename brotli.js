'use strict';
// Compresses the page's files with brotli before the server is compiled, so the binary can send them
// as they are (main.go, "static files"). Go's standard library has no brotli encoder; Node has one.
//   node brotli.js           write brotli/<path>.<hash>.br for every file of web/ that gains by it
//   node brotli.js --check   write nothing; fail unless brotli/ holds exactly those, each unpacks to its
//                            file, and all were made by the brotli this Node has
// <hash> is the first 16 hex characters of the SHA-256 of the file, which is how the server finds the
// copy of the bytes it has: a copy of other bytes has another name and is never sent. A copy is made
// only when it is at least a tenth smaller than the file, and not of the kinds that are compressed
// already, of index.html (the server rewrites it) or of the catalogs (served from the database). The
// same rules are in newStatic in main.go.
// The binary embeds all of brotli/, so the folder must be what a clean checkout would get. A copy
// that is there and unpacks to its file is kept, so a second run only does the files that changed,
// but only while brotli.stamp (next to this script, not in git and not embedded) says the copies
// were made by the same brotli with the same settings: another Node may carry another brotli, whose
// copies unpack to the same files and are other bytes. When the stamp differs every copy is made
// again. Anything else in the folder, but the .gitignore that git keeps, is removed.
// build-image.sh and deploy.sh --build run this; go run and go test work without it and send gzip.
const fs = require('fs'), path = require('path'), zlib = require('zlib'), crypto = require('crypto');

const WEB = path.join(__dirname, 'web'), OUT = path.join(__dirname, 'brotli'), STAMP = path.join(__dirname, 'brotli.stamp');
const PACKED = new Set(['.png', '.jpg', '.jpeg', '.gif', '.webp', '.avif', '.woff', '.woff2', '.mp4', '.webm']);
const QUALITY = zlib.constants.BROTLI_MAX_QUALITY, WINDOW = zlib.constants.BROTLI_DEFAULT_WINDOW;
// What decides the bytes of a copy, besides the file: the brotli library, and what it is asked for.
const MADE_BY = `brotli ${process.versions.brotli}, quality ${QUALITY}, window ${WINDOW}, size hint\n`;
const check = process.argv[2] === '--check';
if (process.argv.length > 3 || (process.argv[2] && !check)) { console.error('usage: node brotli.js [--check]'); process.exit(2); }

// Every file under dir, as paths relative to it with forward slashes, in a fixed order.
function walk(dir, rel = '') {
  return fs.readdirSync(path.join(dir, rel), { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : 1))
    .flatMap(e => (e.isDirectory() ? walk(dir, rel + e.name + '/') : [rel + e.name]));
}
const pack = raw => zlib.brotliCompressSync(raw, { params: {
  [zlib.constants.BROTLI_PARAM_QUALITY]: QUALITY,
  [zlib.constants.BROTLI_PARAM_LGWIN]: WINDOW,
  [zlib.constants.BROTLI_PARAM_SIZE_HINT]: raw.length } });
const unpacks = (file, raw) => { try { return zlib.brotliDecompressSync(fs.readFileSync(file)).equals(raw); } catch { return false; } };
const worth = (z, n) => z > 0 && z * 10 <= n * 9;
const stray = rel => rel !== '.gitignore';   // a file in brotli/ that is not a copy this run wants

const t0 = Date.now(), want = new Set(), problems = [];
let files = 0, before = 0, after = 0, made = 0, kept = 0, removed = 0;
let stamp = null;
try { stamp = fs.readFileSync(STAMP, 'utf8'); } catch { /* no stamp: nothing here is known to be ours */ }
const ours = stamp === MADE_BY;
if (check && !ours) {
  problems.push(stamp === null ? 'brotli.stamp is missing, so nothing says what made the copies in brotli/'
    : `the copies in brotli/ were made by "${stamp.trim()}"; this Node would make them with "${MADE_BY.trim()}"`);
}
if (!check && !ours) {
  // Made by another brotli, or by nobody we know: start from an empty folder. The stamp goes first,
  // so a run that is stopped half way leaves copies that the next run does not trust either.
  fs.rmSync(STAMP, { force: true });
  for (const rel of walk(OUT)) if (stray(rel)) { fs.unlinkSync(path.join(OUT, rel)); removed++; }
}

for (const rel of walk(WEB)) {
  if (rel === 'index.html' || rel.startsWith('catalog/') || PACKED.has(path.extname(rel))) continue;
  const raw = fs.readFileSync(path.join(WEB, rel));
  const name = rel + '.' + crypto.createHash('sha256').update(raw).digest('hex').slice(0, 16) + '.br';
  const out = path.join(OUT, name);
  files++; before += raw.length;
  if (fs.existsSync(out)) {
    if (unpacks(out, raw)) { want.add(name); kept++; after += fs.statSync(out).size; continue; }
    if (check) { problems.push(`brotli/${name} does not unpack to web/${rel}`); want.add(name); continue; }
  }
  const z = pack(raw);
  if (!worth(z.length, raw.length)) { after += raw.length; continue; }
  if (check) { problems.push(`web/${rel} has no brotli copy`); continue; }
  if (!zlib.brotliDecompressSync(z).equals(raw)) { console.error(`the brotli of web/${rel} does not unpack to it`); process.exit(1); }
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(out, z);
  want.add(name); made++; after += z.length;
}
// What is left from files that changed or are gone, and anything else that got into the folder: the
// binary would carry it, and two builds of one commit would differ.
for (const rel of walk(OUT)) {
  if (!stray(rel) || want.has(rel)) continue;
  if (check) { problems.push(`brotli/${rel} is not the copy of a file in web/ and would be compiled in`); continue; }
  fs.unlinkSync(path.join(OUT, rel)); removed++;
}
const kb = n => Math.round(n / 1000);
if (check) {
  for (const p of problems) console.log(p);
  console.log(`brotli: ${kept} copies unpack to their files, ${problems.length} problems${problems.length ? '; run node brotli.js' : ''}`);
  process.exit(problems.length ? 1 : 0);
}
// Folders that held only what was removed. They are not compiled in; this is tidiness.
const dirs = (dir, rel = '') => fs.readdirSync(path.join(dir, rel), { withFileTypes: true }).filter(e => e.isDirectory())
  .flatMap(e => [...dirs(dir, rel + e.name + '/'), rel + e.name]);
for (const d of dirs(OUT)) if (fs.readdirSync(path.join(OUT, d)).length === 0) fs.rmdirSync(path.join(OUT, d));
fs.writeFileSync(STAMP, MADE_BY);
console.log(`brotli: ${files} files, ${kb(before)} KB as they are, ${kb(after)} KB with brotli where it gains; ${made} written, ${kept} kept, ${removed} removed, in ${((Date.now() - t0) / 1000).toFixed(1)} s (${MADE_BY.trim()})`);
