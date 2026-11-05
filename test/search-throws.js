// Finds nunchaku throw settings whose flight ends in a clean catch. The simulation is
// deterministic, so the winners are written into web/demo.js by hand.   node test/search-throws.js
const fs = require('fs');
const Demo = (0, eval)(fs.readFileSync(__dirname + '/../web/demo.js', 'utf8').replace('const Demo =', 'globalThis.Demo =') + ';Demo');
const TAU = Math.PI * 2;
for (const [label, fixed, acrosses] of [['same hand, 1 turn', { turns: 1, other: false }, [0]], ['other hand, 1 turn', { turns: 1, other: true }, [0.6, 0.8, 1.0, 1.2]]]) {
  const found = [];
  for (const dir of [1, -1]) for (let k = 0; k < 8; k++) for (const lift of [1.8, 2.1, 2.4, 2.7, 3.0, 3.3]) for (const across of acrosses) {
    const o = { dir, phase: k * TAU / 8, lift, across, ...fixed };
    let c; try { c = Demo.aerialClip(o); } catch (e) { continue; }
    if (!c) continue;
    found.push({ o, a: c.air, score: c.air.home + 0.12 * (c.air.turns - o.turns) * TAU + 0.2 * c.air.twist });
  }
  found.sort((x, y) => x.score - y.score);
  console.log(`\n${label}: ${found.length} clean catches`);
  for (const f of found.slice(0, 6)) console.log(`  dir ${f.o.dir} phase ${(f.o.phase / TAU).toFixed(3)} lift ${f.o.lift} across ${f.o.across} | turns ${f.a.turns.toFixed(2)} air ${f.a.time.toFixed(2)} s twist ${f.a.twist.toFixed(2)} reach ${f.a.reach.toFixed(2)} from home ${f.a.home.toFixed(2)}`);
  const low = found.filter(f => f.a.time < 0.5).slice(0, 3);
  for (const f of low) console.log(`  (short flight) dir ${f.o.dir} phase ${(f.o.phase / TAU).toFixed(3)} lift ${f.o.lift} across ${f.o.across} | turns ${f.a.turns.toFixed(2)} air ${f.a.time.toFixed(2)} s from home ${f.a.home.toFixed(2)}`);
}
