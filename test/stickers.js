'use strict';
// Sticker poses (web/demo.js STICKERS) pass the same body checks as clips, on the standard body and
// on every body preset: no limb through the body or another limb, arms and legs within their
// length, feet on the floor, and the pelvis over the feet. Signature-move stickers are frames of
// clips, which the physics suite checks; here they must name moves that exist.
//   node test/stickers.js
const fs = require('fs'), path = require('path');
const { Demo } = require('../sim/engine.js'), { bodyErrors } = require('../sim/check.js'), P = require('../sim/presets.js');
let bad = 0, n = 0;
const bodies = [{ id: 'standard', sc: 1, bd: 1 }, ...P.PRESETS];
for (const [key, s] of Object.entries(Demo.STICKERS)) for (const b of bodies) {
  Demo.setBody(b.sc, b.bd);
  const scene = { fr: [Demo.fit(s.pose)], props: [], dur: 1, method: 'hand', loop: false }, errs = bodyErrors(scene, {}).errs;
  Demo.setBody(1, 1);
  n++;
  if (errs.length) { bad++; console.log(`FAIL sticker ${key} on the ${b.id} body: ${errs.join('; ')}`); }
}
const index = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'clips', 'index.json'), 'utf8'));
for (const [key, name] of Demo.SIGNATURE) {
  n++;
  const e = index[key], ok = e && (!name || e.v[0].n === name) && e.v[0].m !== 'still';
  if (!ok) { bad++; console.log(`FAIL signature sticker ${key}${name ? ' ' + name : ''} is not a baked moving clip`); }
}
console.log(`stickers: ${Object.keys(Demo.STICKERS).length} poses on ${bodies.length} bodies and ${Demo.SIGNATURE.length} signature moves, ${n} checks, ${bad} failed`);
process.exit(bad ? 1 : 0);
