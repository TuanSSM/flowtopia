'use strict';
// Turns the move list (sim/specs.js) into the clips the browser plays.
//   node sim/bake.js                write web/clips/*.json and web/clips/index.json
//   ONLY='poi:W' node sim/bake.js   only the moves whose key matches (the rest are kept)
//   node sim/bake.js --diff         bake nothing; say how the clips here differ from the last commit
// The same sources give the same bytes on any machine: every stored number is rounded (Demo.pack),
// and CI bakes and fails when the result is not what is committed (docs/demo-engine.md).
const fs = require('fs'), path = require('path'), crypto = require('crypto');
const E = require('./engine.js'), { Demo } = E, { scene } = require('./compose.js');
const { SPECS, NOTES } = require('./specs.js');

// The clip file a scene is baked to: its JSON and the name it is stored under (a hash of the JSON).
function fileOf(sc) {
  const { meta, method, one, ...rest } = sc, json = JSON.stringify(Demo.pack(rest));
  return { json, f: crypto.createHash('sha1').update(json).digest('hex').slice(0, 12) };
}
const CLIPS = path.join(__dirname, '..', 'web', 'clips');
// Whether a clip file is the one baked for its move now (so its preset results can be compared).
let baked_ = null;
const baked = f => { if (!baked_) { try { baked_ = new Set(Object.values(JSON.parse(fs.readFileSync(path.join(CLIPS, 'index.json'), 'utf8'))).flatMap(e => e.v.map(v => v.f))); } catch { baked_ = new Set(); } } return baked_.has(f); };

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

// What a bake changed: the files as they are now against the last commit, move by move, since a clip
// that changed has another name. Prints, for each clip that differs, the first field or stored number
// that does, with both values in full, and the same for the lists beside the clips. Returns how many
// things differ. CI runs it when a bake there does not give the committed clips.
function diff() {
  const root = path.join(__dirname, '..'), rel = f => 'web/clips/' + f;
  const was = p => { try { return JSON.parse(require('child_process').execFileSync('git', ['show', 'HEAD:' + p], { cwd: root, encoding: 'utf8', maxBuffer: 1 << 28, stdio: ['ignore', 'pipe', 'ignore'] })); } catch { return null; } };
  const now = p => { try { return JSON.parse(fs.readFileSync(path.join(root, p), 'utf8')); } catch { return null; } };
  // JSON writes a number with as many digits as it takes to tell it from its neighbours, so two that differ print differently.
  const both = (a, b) => `${JSON.stringify(a)} committed, ${JSON.stringify(b)} baked now`;
  // The first place two values differ, as [path, committed, baked now].
  const first = (a, b, at = '') => {
    if (a === b) return null;
    if (!a || !b || typeof a !== 'object' || typeof b !== 'object') return [at, a, b];
    for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) { const d = first(a[k], b[k], at ? at + '.' + k : k); if (d) return d; }
    return null;
  };
  const A = was(rel('index.json')) || {}, B = now(rel('index.json')) || {};
  let n = 0; const say = line => { n++; console.log(line); };
  for (const key of new Set([...Object.keys(A), ...Object.keys(B)])) {
    const va = A[key] ? A[key].v : [], vb = B[key] ? B[key].v : [];
    for (let i = 0; i < Math.max(va.length, vb.length); i++) {
      const a = va[i], b = vb[i], name = `${key} "${(b || a).n}"`;
      if (!a || !b) { say(`${name}: only ${a ? 'in the commit' : 'in the bake now'}`); continue; }
      if (a.f === b.f) continue;
      const ca = was(rel(a.f + '.json')), cb = now(rel(b.f + '.json')), d = ca && cb ? first({ ...ca, data: 0 }, { ...cb, data: 0 }) : ['file', ca ? a.f : 'missing', cb ? b.f : 'missing'];
      let line = `${name}: ${a.f}.json committed, ${b.f}.json baked now\n`;
      if (d) line += `  first difference, ${d[0]}: ${both(d[1], d[2])}`;
      else {
        // Only the frames differ: say which stored number is the first, as a frame, a part and an axis.
        const k = ca.keys.length * 3, off = []; for (let j = 0; j < Math.max(ca.data.length, cb.data.length); j++) if (ca.data[j] !== cb.data[j]) off.push(j);
        const at = off[0], m = x => typeof x === 'number' ? x / 1e4 : x;
        line += `  first difference, frame ${Math.floor(at / k)} of ${ca.n}, part ${ca.keys[Math.floor(at % k / 3)]}, ${'xyz'[at % 3]}: ${both(m(ca.data[at]), m(cb.data[at]))} (m); ${off.length} of ${ca.data.length} stored numbers differ`;
      }
      say(line);
    }
  }
  // The lists beside the clips. In them a clip goes by its place in its move, not by its file name.
  const plain = ix => Object.fromEntries(Object.entries(ix).map(([k, e]) => [k, { ...e, v: e.v.map(({ f, ...v }) => v) }]));
  const fails = (p, ix) => p && { presets: p.presets, fail: Object.fromEntries(Object.entries(ix).flatMap(([k, e]) => e.v.map((v, i) => [`${k} #${i}`, p.fail[v.f] || {}]))) };
  for (const [what, a, b] of [['web/clips/index.json', plain(A), plain(B)], ['web/clips/presets.json', fails(was(rel('presets.json')), A), fails(now(rel('presets.json')), B)],
    ['web/clips/features.json', was(rel('features.json')), now(rel('features.json'))], ['sim/fits.json', was('sim/fits.json'), now('sim/fits.json')]]) {
    const d = first(a, b);
    if (d) say(`${what}: first difference, ${d[0]}: ${both(d[1], d[2])}`);
  }
  console.log(n ? `${n} differences between the commit and the bake now` : 'the clips and their lists are the ones in the commit');
  return n;
}

if (require.main === module) {
  if (process.argv.includes('--diff')) process.exit(diff() ? 1 : 0);
  const dir = path.join(__dirname, '..', 'web', 'clips'), file = path.join(dir, 'index.json');
  fs.mkdirSync(dir, { recursive: true });
  const only = process.env.ONLY, index = only && fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : {};
  let n = 0, failed = 0; const t0 = Date.now();
  const P = require('./presets.js'), fail = only ? P.load().fail : {};
  for (const { key, i, sp, scene: sc, error } of build(only)) {
    if (!sc) { failed++; console.log(`FAILED ${key} ${sp.n}: ${error ? error.message : 'the move did not complete'}`); continue; }
    const { method, one } = sc, { json, f } = fileOf(sc);
    fs.writeFileSync(path.join(dir, f + '.json'), json);
    fail[f] = P.check(sc, sp);
    if (i === 0) index[key] = { note: NOTES[key], v: [] };
    index[key].v[i] = { n: sp.n, f, m: method, one: one || undefined, note: sp.note, d: Math.round(sc.dur * 100) / 100 };
    n++;
  }
  // Drop clip files that no move uses any more.
  const used = new Set(Object.values(index).flatMap(e => e.v.map(v => v.f + '.json')));
  for (const f of fs.readdirSync(dir)) if (!/^(index|features|presets)\.json$/.test(f) && !used.has(f)) fs.unlinkSync(path.join(dir, f));
  // Which clips each body preset may play (sim/presets.js).
  const kept = P.save(fail, new Set([...used].map(f => f.replace('.json', ''))));
  console.log(`body presets: ${Object.keys(kept).length} clips are not offered on at least one of ${P.PRESETS.length} presets`);
  fs.writeFileSync(file, JSON.stringify(index));
  const size = fs.readdirSync(dir).reduce((a, f) => a + fs.statSync(path.join(dir, f)).size, 0);
  console.log(`${n} clips for ${Object.keys(index).length} moves written in ${((Date.now() - t0) / 1000).toFixed(0)} s, ${(size / 1e6).toFixed(1)} MB${failed ? `, ${failed} FAILED` : ''}`);
  // The measurements next-trick suggestions use, from the clips just written (sim/features.js).
  require('child_process').execFileSync(process.execPath, [path.join(__dirname, 'features.js')], { stdio: 'inherit' });
  process.exit(failed ? 1 : 0);
}
module.exports = { build, scene, fileOf, baked, SPECS, NOTES };
