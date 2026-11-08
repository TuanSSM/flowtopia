'use strict';
// Fits the weights of the transition score to the transitions people have marked, and reports how
// well the score separates Works from Blocked on pairs it was not fitted on.
//   node sim/fit-suggest.js counts.json            report only
//   node sim/fit-suggest.js counts.json --write    write web/suggest-weights.json
// counts.json is what GET /api/suggest/counts returns to an admin (every prop). Deterministic: the
// same counts give the same weights. With fewer than MIN_PAIRS marked pairs nothing is fitted and the
// starting weights from the physics are kept, marked early.
const fs = require('fs'), path = require('path'), crypto = require('crypto');
const S = require('../web/suggest.js');
const ROOT = path.join(__dirname, '..'), MIN_PAIRS = 30, KEYS = Object.keys(S.DEFAULT);
const FEAT = JSON.parse(fs.readFileSync(path.join(ROOT, 'web', 'clips', 'features.json'), 'utf8'));
const cats = {};
const catOf = p => cats[p] ||= JSON.parse(fs.readFileSync(path.join(ROOT, 'web', 'catalog', p + '.json'), 'utf8'));
const skillOf = (p, id) => catOf(p).skills.find(s => s.id === id) || catOf(p).skills.find(s => s.id === (catOf(p).aliases || {})[id]);
const sigmoid = z => 1 / (1 + Math.exp(-z));
// Area under the ROC curve: the chance a random Works pair scores above a random Blocked one.
function auc(items) {
  const pos = items.filter(x => x.y === 1), neg = items.filter(x => x.y === 0);
  if (!pos.length || !neg.length) return null;
  let wins = 0, total = 0;
  for (const p of pos) for (const n of neg) { total += p.w * n.w; wins += p.w * n.w * (p.s > n.s ? 1 : p.s === n.s ? 0.5 : 0); }
  return wins / total;
}
// Pairs from counts: one item per pair and outcome, weighted by how many marked it.
function examples(counts) {
  const out = [];
  for (const c of counts) {
    const a = skillOf(c.prop, c.a), b = skillOf(c.prop, c.b);
    if (!a || !b) continue;
    const r = S.transition(a, FEAT[`${c.prop}:${a.id}`], b, FEAT[`${c.prop}:${b.id}`]);
    const x = KEYS.map(k => r.terms[k] ?? 0), test = parseInt(crypto.createHash('sha1').update(`${c.prop}:${c.a}>${c.b}`).digest('hex').slice(0, 6), 16) % 5 === 0;
    const prereq = (b.needs || []).some(n => n.replace('!', '') === a.id) ? 1 : a.id[0] === b.id[0] ? 0.5 : 0;
    if (c.works) out.push({ x, y: 1, w: c.works, test, prereq });
    if (c.blocked) out.push({ x, y: 0, w: c.blocked, test, prereq });
  }
  return out;
}
// Logistic regression P(works) = sigmoid(b - sum(w_k x_k)) by gradient descent, weights kept >= 0 so
// that more change never makes a transition more likely to work.
function fit(train) {
  let w = KEYS.map(k => S.DEFAULT[k]), b = 2;
  const lr = 0.05, total = train.reduce((s, e) => s + e.w, 0) || 1;
  for (let it = 0; it < 4000; it++) {
    const gw = KEYS.map(() => 0); let gb = 0;
    for (const e of train) {
      const p = sigmoid(b - e.x.reduce((s, v, k) => s + w[k] * v, 0)), d = (p - e.y) * e.w / total;
      gb += d; e.x.forEach((v, k) => { gw[k] -= d * v; });
    }
    b -= lr * gb; w = w.map((v, k) => Math.max(0, v - lr * gw[k] - lr * 1e-3 * v));
  }
  return { w: Object.fromEntries(KEYS.map((k, i) => [k, Math.round(w[i] * 1000) / 1000])), b };
}
function run(counts) {
  const all = examples(counts), pairs = new Set(counts.filter(c => c.works || c.blocked).map(c => `${c.prop}:${c.a}>${c.b}`)).size;
  if (pairs < MIN_PAIRS) return { early: true, pairs, weights: { ...S.DEFAULT }, auc: null, baseline: null, note: `Starting weights from the physics. ${pairs} marked ${pairs === 1 ? 'transition has' : 'transitions have'} been shared; at least ${MIN_PAIRS} are needed to fit and measure.` };
  const train = all.filter(e => !e.test), test = all.filter(e => e.test);
  const { w, b } = fit(train);
  const score = e => sigmoid(b - e.x.reduce((s, v, k) => s + w[KEYS[k]] * v, 0));
  const a = auc(test.map(e => ({ ...e, s: score(e) }))), base = auc(test.map(e => ({ ...e, s: e.prereq })));
  const r = x => x === null ? null : Math.round(x * 1000) / 1000;
  return { early: false, pairs, weights: w, auc: r(a), baseline: r(base), note: `Fitted on ${train.length} outcomes, measured on ${test.length} held-out ones.` };
}
if (require.main === module) {
  const file = process.argv[2];
  if (!file) { console.log('usage: node sim/fit-suggest.js counts.json [--write]'); process.exit(2); }
  const res = run(JSON.parse(fs.readFileSync(file, 'utf8')).counts || []);
  console.log(JSON.stringify(res, null, 1));
  if (process.argv.includes('--write')) fs.writeFileSync(path.join(ROOT, 'web', 'suggest-weights.json'), JSON.stringify(res) + '\n');
}
module.exports = { run, fit, auc, examples, MIN_PAIRS };
