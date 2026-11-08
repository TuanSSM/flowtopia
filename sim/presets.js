'use strict';
// Body presets for avatars, and which clips pass the body checks on each.
//
// A preset is a height (short, average, tall) and a build (lean, average, broad), defined in
// web/demo.js (HEIGHTS, BUILDS) next to the standard lengths and documented with their sources in
// docs/avatars.md. Clips are not simulated again per preset: a clip is moved as a whole to fit the
// body (Demo.fit), which leaves the physics as it was, and then the same floor and body checks as
// test/physics.js run on the preset's body, with no tolerance changed. A clip that fails on a preset
// is not offered on it; web/clips/presets.json lists those, with the first reason, for the player.
const fs = require('fs'), path = require('path');
const { Demo } = require('./engine.js'), { floorErrors, bodyErrors } = require('./check.js');
const FILE = path.join(__dirname, '..', 'web', 'clips', 'presets.json');

const PRESETS = [];
for (const [h, sc] of Object.entries(Demo.HEIGHTS)) for (const [b, bd] of Object.entries(Demo.BUILDS)) if (sc !== 1 || bd !== 1) PRESETS.push({ id: `${h}-${b}`, sc, bd });

// The presets a clip fails on, each with the first thing that went wrong: { 'short-lean': '...' }.
// Close-ups show only the hands, so they fit every body.
function check(c, sp) {
  const out = {};
  if (c.hide) return out;
  try {
    for (const p of PRESETS) {
      Demo.setBody(p.sc, p.bd);
      const moved = { ...c, fr: c.fr.map(Demo.fit) }, errs = [...floorErrors(moved), ...bodyErrors(moved, sp).errs];
      if (errs.length) out[p.id] = errs[0];
    }
  } finally { Demo.setBody(1, 1); }
  return out;
}
const load = () => fs.existsSync(FILE) ? JSON.parse(fs.readFileSync(FILE, 'utf8')) : { presets: {}, fail: {} };
// fail: clip file -> presets it fails on. Clips no move uses any more are dropped.
function save(fail, used) {
  const kept = Object.fromEntries(Object.keys(fail).filter(f => used.has(f) && Object.keys(fail[f]).length).sort().map(f => [f, fail[f]]));
  const presets = Object.fromEntries(PRESETS.map(p => [p.id, { height: p.sc, build: p.bd }]));
  fs.writeFileSync(FILE, JSON.stringify({ presets, fail: kept }));
  return kept;
}

module.exports = { PRESETS, check, load, save, FILE };
