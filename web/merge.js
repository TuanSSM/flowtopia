'use strict';
// Putting two copies of one progress document back together. When a save meets a newer copy from
// another device, each side's changes since the copy they both started from are worked out and
// both sets are kept. Where both changed the very same thing, this device's edit wins: it is the
// one the person is looking at. Used by save() in app.js; checked by test/merge.js.
const progressMerge = (() => {
  const obj = v => !!v && typeof v === 'object' && !Array.isArray(v);
  // One spelling for a value whatever order its keys came in (the database reorders them).
  const canon = v => JSON.stringify(v, (k, x) => obj(x) ? Object.fromEntries(Object.keys(x).sort().map(n => [n, x[n]])) : x);
  const same = (a, b) => canon(a) === canon(b);

  // How the items of a list are told apart: by id when every item has its own, by content when
  // they are records without one (the history of level changes), and not at all otherwise.
  function keyer(...lists) {
    const all = lists.flat();
    if (!all.every(obj)) return null;
    const ids = l => l.map(x => x.id), own = l => new Set(ids(l)).size === l.length;
    return all.every(x => typeof x.id === 'string' || typeof x.id === 'number') && lists.every(own) ? x => 'i' + typeof x.id + x.id : canon;
  }
  function list(base, mine, theirs) {
    const key = keyer(base, mine, theirs);
    if (!key) return mine; // plain values, such as the tricks of one combo: an edit replaces the list
    const by = l => new Map(l.map(x => [key(x), x]));
    const B = by(base), M = by(mine), T = by(theirs), out = [];
    // The other device's order, without what was deleted here.
    for (const t of theirs) {
      const k = key(t);
      if (M.has(k)) out.push(merge3(B.get(k), M.get(k), t));
      else if (!B.has(k)) out.push(t); // new there
      else if (!same(t, B.get(k))) out.push(t); // deleted here, changed there: the change is kept
    }
    // What was added here, or changed here and deleted there, goes back where it sat: before the
    // shared items when it was put in front of them, after them otherwise.
    const front = [], back = [];
    let seen = false;
    for (const m of mine) {
      const k = key(m);
      if (T.has(k)) { seen = true; continue; }
      if (B.has(k) && same(m, B.get(k))) continue; // deleted there, untouched here
      (seen ? back : front).push(m);
    }
    return [...front, ...out, ...back];
  }
  function merge3(base, mine, theirs) {
    if (same(mine, theirs) || same(theirs, base)) return mine;
    if (same(mine, base)) return theirs;
    if (obj(mine) && obj(theirs)) {
      const b = obj(base) ? base : {}, out = {};
      // Set as plain data whatever the key is called, so a key named __proto__ stays a key.
      const put = (k, v) => Object.defineProperty(out, k, { value: v, enumerable: true, writable: true, configurable: true });
      for (const k of new Set([...Object.keys(theirs), ...Object.keys(mine)])) {
        const inM = Object.hasOwn(mine, k), inT = Object.hasOwn(theirs, k), inB = Object.hasOwn(b, k);
        if (inM && inT) put(k, merge3(inB ? b[k] : undefined, mine[k], theirs[k]));
        else if (inM) { if (!inB || !same(mine[k], b[k])) put(k, mine[k]); } // gone there: kept only if new or changed here
        else if (!inB || !same(theirs[k], b[k])) put(k, theirs[k]); // gone here: kept only if new or changed there
      }
      return out;
    }
    if (Array.isArray(mine) && Array.isArray(theirs)) return list(Array.isArray(base) ? base : [], mine, theirs);
    return mine;
  }
  // A whole progress document. The history of level changes stays newest first and within its cap.
  function progress(base, mine, theirs) {
    const m = merge3(base, mine, theirs);
    if (Array.isArray(m.events)) {
      if (m.events.every(e => obj(e) && typeof e.t === 'string')) m.events.sort((a, b) => (a.t < b.t) - (a.t > b.t));
      m.events.length = Math.min(m.events.length, 300);
    }
    return m;
  }
  return { merge3, progress, same };
})();
if (typeof module !== 'undefined') module.exports = progressMerge;
