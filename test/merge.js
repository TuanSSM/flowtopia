'use strict';
// Checks web/merge.js: two copies of a progress document, changed on two devices from one starting
// point, come back together with both sets of changes. Run: node test/merge.js
const { merge3, progress, same } = require('../web/merge.js');

let n = 0, bad = 0;
function eq(name, got, want) {
  n++;
  if (same(got, want)) return;
  bad++;
  console.log(`FAIL ${name}\n  got  ${JSON.stringify(got)}\n  want ${JSON.stringify(want)}`);
}
const doc = o => ({ version: 1, skills: {}, sessions: [], combos: [], gates: {}, events: [], settings: { dominant: 'R' }, ...o });
const clone = v => JSON.parse(JSON.stringify(v));

// Two different skills, one on each device.
{
  const base = doc({ skills: { A1: { O: 1 } } });
  const mine = doc({ skills: { A1: { O: 1 }, B2: { O: 2, updated: 't2' } }, events: [{ t: '2026-01-02', id: 'B2', side: '', from: 0, to: 2 }] });
  const theirs = doc({ skills: { A1: { O: 3, updated: 't1' } }, events: [{ t: '2026-01-01', id: 'A1', side: '', from: 1, to: 3 }] });
  eq('two skills', progress(base, mine, theirs), doc({
    skills: { A1: { O: 3, updated: 't1' }, B2: { O: 2, updated: 't2' } },
    events: [{ t: '2026-01-02', id: 'B2', side: '', from: 0, to: 2 }, { t: '2026-01-01', id: 'A1', side: '', from: 1, to: 3 }],
  }));
}
// The same skill on both: one hand each is kept, and the same hand goes to this device.
eq('one skill, two hands', merge3({ A1: { L: 1, R: 1 } }, { A1: { L: 2, R: 1 } }, { A1: { L: 1, R: 3 } }), { A1: { L: 2, R: 3 } });
eq('one skill, same hand', merge3({ A1: { O: 1 } }, { A1: { O: 2 } }, { A1: { O: 3 } }), { A1: { O: 2 } });
// Key order does not count as a change (the database returns keys in its own order).
eq('key order', merge3({ a: 1, b: { x: 1, y: 2 } }, { b: { y: 2, x: 1 }, a: 1 }, { a: 2, b: { x: 1, y: 2 } }), { a: 2, b: { x: 1, y: 2 } });
// Nothing changed here, or nothing changed there.
eq('only theirs', merge3({ a: 1 }, { a: 1 }, { a: 2, b: 3 }), { a: 2, b: 3 });
eq('only mine', merge3({ a: 1 }, { a: 5 }, { a: 1 }), { a: 5 });
eq('both the same', merge3({ a: 1 }, { a: 2 }, { a: 2 }), { a: 2 });
// Deletions in a map: respected unless the other side changed the thing.
eq('deleted here', merge3({ a: 1, b: 2 }, { a: 1 }, { a: 1, b: 2, c: 3 }), { a: 1, c: 3 });
eq('deleted there', merge3({ a: 1, b: 2 }, { a: 1, b: 2, c: 3 }, { a: 1 }), { a: 1, c: 3 });
eq('deleted here, changed there', merge3({ b: { n: 1 } }, {}, { b: { n: 2 } }), { b: { n: 2 } });
eq('changed here, deleted there', merge3({ b: { n: 1 } }, { b: { n: 2 } }, {}), { b: { n: 2 } });
eq('added on both', merge3({}, { a: 1 }, { b: 2 }), { a: 1, b: 2 });
eq('a key called __proto__ is only data', Object.keys(merge3({}, JSON.parse('{"__proto__":{"x":1}}'), { b: 2 })).sort(), ['__proto__', 'b']);
// Lists with ids: sessions are added at the end, combos at the front.
{
  const s = (id, minutes = 10) => ({ id, date: '2026-01-01', minutes, focus: [], notes: '' });
  eq('sessions added on both', merge3([s('a')], [s('a'), s('m')], [s('a'), s('t')]), [s('a'), s('t'), s('m')]);
  eq('combo added in front on both', merge3([s('a')], [s('m'), s('a')], [s('t'), s('a')]), [s('m'), s('t'), s('a')]);
  eq('session deleted here', merge3([s('a'), s('b')], [s('b')], [s('a'), s('b'), s('t')]), [s('b'), s('t')]);
  eq('session deleted there', merge3([s('a'), s('b')], [s('a'), s('b'), s('m')], [s('b')]), [s('b'), s('m')]);
  eq('session edited here, deleted there', merge3([s('a'), s('b')], [s('a', 25), s('b')], [s('b')]), [s('a', 25), s('b')]);
  eq('session deleted here, edited there', merge3([s('a'), s('b')], [s('b')], [s('a', 25), s('b')]), [s('a', 25), s('b')]);
  eq('session edited on both', merge3([s('a')], [{ ...s('a'), notes: 'mine' }], [s('a', 30)]), [{ ...s('a', 30), notes: 'mine' }]);
  eq('everything deleted on both', merge3([s('a')], [], []), []);
  eq('list emptied here, grown there', merge3([s('a')], [], [s('a'), s('t')]), [s('t')]);
}
// A combo's tricks are plain values: an edit replaces the list, and the other device's other
// changes to the combo are kept.
{
  const c = (seq, name = 'One') => ({ id: 'c1', name, seq, reps: seq.map(() => 1) });
  eq('combo tricks edited here', merge3([c(['A1'])], [c(['A1', 'B2'])], [c(['A1'], 'Renamed')]), [c(['A1', 'B2'], 'Renamed')]);
  eq('combo tricks edited on both', merge3([c(['A1'])], [c(['A1', 'B2'])], [c(['A1', 'C3'])]), [c(['A1', 'B2'])]);
}
// The history has no ids of its own: the same entry is kept once, newest first, and capped.
{
  const ev = (t, id = 'A1') => ({ t, id, side: '', from: 0, to: 1 });
  const base = doc({ events: [ev('2026-01-03'), ev('2026-01-01')] });
  const mine = doc({ events: [ev('2026-01-05', 'B2'), ev('2026-01-03'), ev('2026-01-01')] });
  const theirs = doc({ events: [ev('2026-01-04', 'C3'), ev('2026-01-03'), ev('2026-01-01')] });
  eq('history', progress(base, mine, theirs).events.map(e => e.t), ['2026-01-05', '2026-01-04', '2026-01-03', '2026-01-01']);
  const many = i => ev(new Date(Date.UTC(2026, 0, 1, 0, i)).toISOString(), 'S' + i);
  const b2 = doc({ events: Array.from({ length: 290 }, (_, i) => many(i)).reverse() });
  const m2 = clone(b2), t2 = clone(b2);
  for (let i = 300; i < 320; i++) m2.events.unshift(many(i));
  for (let i = 400; i < 420; i++) t2.events.unshift(many(i));
  const got = progress(b2, m2, t2).events;
  eq('history cap', [got.length, got[0].id, got[20].id, got[299].id], [300, 'S419', 'S319', 'S30']);
}
// Settings and gates, and a whole document where only the other side moved.
eq('settings', merge3({ dominant: 'R', event: '' }, { dominant: 'L', event: '' }, { dominant: 'R', event: 'Worlds' }), { dominant: 'L', event: 'Worlds' });
{
  const base = doc({ gates: { g1: true } }), theirs = doc({ gates: { g1: true, g2: true }, skills: { A1: { O: 1 } } });
  eq('untouched here', progress(base, clone(base), theirs), theirs);
}
// The inputs are left as they were.
{
  const base = doc({ skills: { A1: { O: 1 } } }), mine = doc({ skills: { A1: { O: 2 } } }), theirs = doc({ skills: { A1: { O: 1 }, B2: { O: 1 } } });
  const keep = JSON.stringify([base, mine, theirs]);
  progress(base, mine, theirs);
  eq('inputs untouched', JSON.stringify([base, mine, theirs]), keep);
}

if (bad) { console.log(`FAILURES: ${bad} of ${n} merge checks`); process.exit(1); }
console.log(`PASS ${n} merge checks`);
