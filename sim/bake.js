'use strict';
// Turns the move list (sim/specs.js) into the clips the browser plays.
//   node sim/bake.js                write web/clips/*.json and web/clips/index.json
//   ONLY='poi:W' node sim/bake.js   only the moves whose key matches (the rest are kept)
const fs = require('fs'), path = require('path'), crypto = require('crypto');
const E = require('./engine.js'), { Demo } = E, { scene } = require('./compose.js');
const { SPECS, NOTES } = require('./specs.js');

// Every variant of every move, as [key, variant number, spec, scene].
function* build(only) {
  for (const [prop, moves] of Object.entries(SPECS)) for (const [id, list] of Object.entries(moves)) {
    const key = prop + ':' + id;
    if (only && !key.match(only)) continue;
    for (const [i, sp] of list.entries()) {
      let sc = null, error = null;
      try { sc = scene(sp); } catch (e) { error = e; }
      yield { key, prop, id, i, sp, scene: sc, error };
    }
  }
}

if (require.main === module) {
  const dir = path.join(__dirname, '..', 'web', 'clips'), file = path.join(dir, 'index.json');
  fs.mkdirSync(dir, { recursive: true });
  const only = process.env.ONLY, index = only && fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : {};
  let n = 0, failed = 0; const t0 = Date.now();
  for (const { key, i, sp, scene: sc, error } of build(only)) {
    if (!sc) { failed++; console.log(`FAILED ${key} ${sp.n}: ${error ? error.message : 'the move did not complete'}`); continue; }
    const { meta, method, one, ...rest } = sc, json = JSON.stringify(Demo.pack(rest)), f = crypto.createHash('sha1').update(json).digest('hex').slice(0, 12);
    fs.writeFileSync(path.join(dir, f + '.json'), json);
    if (i === 0) index[key] = { note: NOTES[key], v: [] };
    index[key].v[i] = { n: sp.n, f, m: method, one: one || undefined, note: sp.note, d: Math.round(sc.dur * 100) / 100 };
    n++;
  }
  // Drop clip files that no move uses any more.
  const used = new Set(Object.values(index).flatMap(e => e.v.map(v => v.f + '.json')));
  for (const f of fs.readdirSync(dir)) if (f !== 'index.json' && !used.has(f)) fs.unlinkSync(path.join(dir, f));
  fs.writeFileSync(file, JSON.stringify(index));
  const size = fs.readdirSync(dir).reduce((a, f) => a + fs.statSync(path.join(dir, f)).size, 0);
  console.log(`${n} clips for ${Object.keys(index).length} moves written in ${((Date.now() - t0) / 1000).toFixed(0)} s, ${(size / 1e6).toFixed(1)} MB${failed ? `, ${failed} FAILED` : ''}`);
  process.exit(failed ? 1 : 0);
}
module.exports = { build, scene, SPECS, NOTES };
