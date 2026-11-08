// The transition score and bridges (web/suggest.js) on known cases and on the real clips.
//   node test/suggest.js
const fs = require('fs'), path = require('path'), S = require('../web/suggest.js');
const FEAT = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'clips', 'features.json'), 'utf8'));
const NUN = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'catalog', 'nunchaku.json'), 'utf8'));
let bad = 0, n = 0;
const ok = (name, cond, detail = '') => { n++; if (!cond) { bad++; console.log('FAIL ' + name + (detail !== '' ? ': ' + detail : '')); } else console.log('PASS ' + name); };
const sk = id => NUN.skills.find(s => s.id === id), fe = id => FEAT['nunchaku:' + id];
const variant = (id, name) => fe(id).filter(v => v.name === name);
const tr = (a, an, b, bn) => S.transition(sk(a), an ? variant(a, an) : fe(a), sk(b), bn ? variant(b, bn) : fe(b));
// From the clips: staying in the same plane and direction beats reversing, which beats changing plane.
const same = tr('F5', 'Upspin', 'F5', 'Upspin'), reverse = tr('F5', 'Upspin', 'F5', 'Downspin'), turn = tr('F5', 'Upspin', 'F5', 'Outspin');
ok('same plane and direction scores highest, then a reversal, then a change of plane', same.score > reverse.score && reverse.score > turn.score, [same.score, reverse.score, turn.score].map(x => x.toFixed(3)));
ok('a trick into itself costs nothing', same.cost < 0.05, same.cost.toFixed(3));
ok('the reasons come from the same terms as the score', same.why.includes('same wall plane') && reverse.why.includes('the spin reverses') && turn.why.some(w => w.startsWith('plane turns 90 degrees')), JSON.stringify([same.why, reverse.why, turn.why]));
ok('evidence from two clips is "clip"', same.evidence === 'clip');
// The best variant pairing is chosen and named.
const f5f3 = tr('F5', null, 'F3', null);
ok('between skills with several variants, the best pairing is used and named', f5f3.cost < 0.05 && f5f3.from && f5f3.to, `${f5f3.from} to ${f5f3.to} ${f5f3.cost.toFixed(3)}`);
// From the catalog: coarser, and labelled.
const desc = S.transition({ motion: { plane: 'wall', direction: 'forward' } }, null, { motion: { plane: 'face', direction: 'forward' } }, null);
const descSame = S.transition({ motion: { plane: 'wall', direction: 'forward' } }, null, { motion: { plane: 'wall', direction: 'forward' } }, null);
const unknown = S.transition({ motion: { plane: 'unknown', direction: 'unknown' } }, null, { motion: { plane: 'wall', direction: 'forward' } }, null);
ok('from the description, a change of plane scores below staying in it', descSame.score > desc.score && desc.evidence === 'description', [descSame.score, desc.score].map(x => x.toFixed(3)));
ok('with one side unknown the evidence is "little"', unknown.evidence === 'little');
// Mirroring: the left hand's clips mirror the right's.
const L = S.states(sk('F5'), variant('F5', 'Outspin'), 'L')[0], R = S.states(sk('F5'), variant('F5', 'Outspin'), 'R')[0];
ok('the left hand mirrors the right: face-plane spins turn the other way, the plane stays', L.sense === -R.sense && L.plane === R.plane);
// Bridges: the cheapest chain within three links, never through a blocked link.
const nodes = ['A', 'B', 'C', 'D', 'E'], c = { 'A>B': 1, 'B>C': 1, 'C>D': 1, 'A>E': 0.2, 'E>D': 0.2, 'A>D': 9 };
const cost = (a, b) => c[`${a}>${b}`] ?? Infinity;
ok('a bridge takes the cheapest chain', JSON.stringify(S.bridge('A', 'D', nodes, cost).path) === '["A","E","D"]');
const blocked = new Set(['E>D']), costB = (a, b) => blocked.has(`${a}>${b}`) ? Infinity : cost(a, b);
ok('a bridge never uses a blocked link', JSON.stringify(S.bridge('A', 'D', nodes, costB).path) === '["A","B","C","D"]' && JSON.stringify(S.bridge('A', 'D', nodes, costB, 2).path) === '["A","D"]', JSON.stringify([S.bridge('A', 'D', nodes, costB), S.bridge('A', 'D', nodes, costB, 2)]));
ok('nor more than the allowed number of links', S.bridge('A', 'D', ['A', 'B', 'C', 'D'], (a, b) => ({ 'A>B': 1, 'B>C': 1, 'C>D': 1 })[`${a}>${b}`] ?? Infinity, 2) === null);
// The weights file.
const W = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'suggest-weights.json'), 'utf8'));
ok('the weights file has every term', Object.keys(S.DEFAULT).every(k => typeof W.weights[k] === 'number'));
// The fitting script, on made-up marks where transitions within one plane work and changes of plane
// are blocked: it must find that and separate them.
const FIT = require('../sim/fit-suggest.js');
const f5 = FEAT['nunchaku:F5'].map(v => v.name), planeOfV = name => ({ Upspin: 'w', Downspin: 'w', Outspin: 'f', Inspin: 'f', Nearspin: 't', Farspin: 't', Overhead: 't' })[name];
const ids = NUN.skills.filter(s => FEAT['nunchaku:' + s.id]).map(s => s.id);
const synth = [];
for (const a of ids) for (const b of ids) {
  if (a === b) continue;
  const r = S.transition(sk(a), fe(a), sk(b), fe(b));
  synth.push({ prop: 'nunchaku', a, b, works: r.terms.plane < 0.2 ? 3 : 0, blocked: r.terms.plane >= 0.2 ? 3 : 0, bridge: 0 });
}
const res = FIT.run(synth);
ok('fitting finds what decides the made-up marks and separates them on held-out pairs', !res.early && res.auc >= 0.9 && res.weights.plane > 0, JSON.stringify(res));
ok('fitting is deterministic', JSON.stringify(FIT.run(synth)) === JSON.stringify(res));
ok('with fewer than 30 pairs nothing is fitted', FIT.run(synth.slice(0, 10)).early === true);
console.log(`${n} checks, ${bad} failed`);
process.exit(bad ? 1 : 0);
