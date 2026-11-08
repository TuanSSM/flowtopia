'use strict';
// The 3D figure is fetched only when a demo or an avatar needs it: the bundle (web/figure3d.js, with
// the avatar parts built in code) and the model (web/assets/human.glb). Together they must stay at
// or under 1.5 MB compressed (gzip -9, as a server would send them).
//   node test/bundle-size.js
const fs = require('fs'), path = require('path'), zlib = require('zlib');
const LIMIT = 1.5e6, files = ['web/figure3d.js', 'web/assets/human.glb'];
let total = 0;
for (const f of files) {
  const raw = fs.readFileSync(path.join(__dirname, '..', f)), gz = zlib.gzipSync(raw, { level: 9 }).length;
  total += gz;
  console.log(`${f.padEnd(24)} ${(raw.length / 1e6).toFixed(2)} MB, ${(gz / 1e6).toFixed(3)} MB compressed`);
}
const ok = total <= LIMIT;
console.log(`3D figure: ${(total / 1e6).toFixed(3)} MB compressed, limit ${(LIMIT / 1e6).toFixed(1)} MB: ${ok ? 'ok' : 'TOO BIG'}`);
process.exit(ok ? 0 : 1);
