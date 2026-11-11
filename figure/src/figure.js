// The 3D figure. A rigged human model (any model with a Mixamo skeleton) is posed from the
// same joints the 2D figure uses, and drawn with its props by three.js into an off-screen
// canvas, which web/demo.js copies into the demo's own canvas. If WebGL or the model is
// missing, the 2D figure is used instead.
import * as THREE from 'three';
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js';
import { mergeGeometries } from 'three/examples/jsm/utils/BufferGeometryUtils.js';

const V = a => new THREE.Vector3(a[0], a[1], a[2]);
const F = { ready: false, failed: false, nopose: typeof window !== "undefined" && !!window.__nopose };
let renderer, scene, camera, model, sun;
const bone = {}, rest = {}, fingers = { L: [], R: [] }, handRest = {};
let posedBones = []; // the names of the bones a pose moves, each with its rest in `rest`
const cyl = [], ball = [], mats = new Map(), skins = [];

/* ---------- nothing done twice ---------- */
// A frame is often the one before it: a paused demo, a still picture, a page drawn again. So the figure
// remembers the pose it last skinned and the picture it last rendered, and does neither again when asked
// for the same. What it was asked is kept flat: every number, name and word the caller handed over, in
// order. The next request is written over it and compared on the way, so objects the caller makes anew
// each frame count as the same when they say the same, and anything added to a pose, a prop or a camera
// later is compared without being listed here. One request is remembered, not a history of them.
const memo = () => ({ num: new Float64Array(256), word: [], at: [], n: 0, w: 0, len: -1, words: -1, same: false, held: false });
const posed = memo(), drawn = memo(), count = { poses: 0, skins: 0, renders: 0, reused: 0 };
function number(k, x) {
  if (k.n === k.num.length) { const a = new Float64Array(k.n * 2); a.set(k.num); k.num = a; }
  if (!Object.is(k.num[k.n], x)) { k.num[k.n] = x; k.same = false; }
  k.n++;
}
function note(k, v) {
  if (typeof v === 'number') number(k, v);
  else if (v !== null && typeof v === 'object') {
    // Most of a request is points, [x, y, z]: their numbers are taken here, without a call of `note` for each.
    if (Array.isArray(v)) { number(k, v.length); for (let i = 0; i < v.length; i++) { const x = v[i]; if (typeof x === 'number') number(k, x); else note(k, x); } }
    else for (const p in v) { note(k, p); note(k, v[p]); }
  } else {
    // A word (a colour, a grip, a name) or nothing at all, with its place among the numbers.
    if (k.word[k.w] !== v || k.at[k.w] !== k.n) { k.word[k.w] = v; k.at[k.w] = k.n; k.same = false; }
    k.w++;
  }
}
// Between `begin` and `unchanged` the request is noted. `unchanged` says whether it is the one the memo
// held and the work for it was done; if not, the caller does the work and then sets `held`.
const begin = k => { k.n = k.w = 0; k.same = k.held; };
function unchanged(k) {
  const same = k.same && k.n === k.len && k.w === k.words;
  k.len = k.n; k.words = k.w; k.held = same;
  return same;
}
// Whatever changes the body without being part of a request (another build, another avatar) drops both.
const forget = () => { posed.held = drawn.held = false; };

/* ---------- nothing made for a frame ---------- */
// The working of a frame needs a few hundred vectors and quaternions that nobody keeps. They are lent
// from a pool, one after another, and all taken back when the next staging starts, so a frame makes
// none. A lent vector is zero and a lent quaternion is no rotation, as new ones are. What is kept from
// one frame to the next (the rest pose, the frame of each hand) has a place of its own instead.
const pool = { v: [], q: [], nv: 0, nq: 0 };
const takeBack = () => { pool.nv = pool.nq = 0; };
const tv = () => (pool.v[pool.nv++] || (pool.v[pool.nv - 1] = new THREE.Vector3())).set(0, 0, 0);
const tq = () => (pool.q[pool.nq++] || (pool.q[pool.nq - 1] = new THREE.Quaternion())).identity();
const pt = a => tv().set(a[0], a[1], a[2]), cp = v => tv().copy(v); // a point given as [x, y, z]; a copy of a vector to work on
const basis = new THREE.Matrix4(), cut = new THREE.Plane(), CUT = [cut], NOCUT = [];

function find(root, name) { let hit = null; root.traverse(o => { if (!hit && o.isBone && o.name.replace(/^mixamorig:?/, '') === name) hit = o; }); return hit; }
// Where a bone is and how it is turned, in the world: into `to`, or into a new one where the answer is kept.
const wpos = (o, to = new THREE.Vector3()) => o.getWorldPosition(to), wquat = (o, to = new THREE.Quaternion()) => o.getWorldQuaternion(to);
// A rotation taking the frame (x, n, w) of the rest pose to the same frame now.
function frameQuat(x, n, w, q = new THREE.Quaternion()) { return q.setFromRotationMatrix(basis.makeBasis(x, n, w)); }
// The frame of a hand, into `fr`: each hand has one for the pose being made (handNow); the rest pose gets its own.
const newFrame = () => ({ X: new THREE.Vector3(), N: new THREE.Vector3(), W: new THREE.Vector3(), q: new THREE.Quaternion() });
const handNow = { L: newFrame(), R: newFrame() }, side0 = new THREE.Vector3();
function handFrame(x, w, side, fr = newFrame()) {
  fr.X.copy(x).normalize(); fr.W.copy(w).sub(side0.copy(fr.X).multiplyScalar(w.dot(fr.X))).normalize(); fr.N.crossVectors(fr.X, fr.W).multiplyScalar(side === 'L' ? -1 : 1);
  // A right-handed set for the matrix: (X, N, W) for the right hand, (X, -N, W) mirrored for the left.
  frameQuat(fr.X, side0.crossVectors(fr.W, fr.X), fr.W, fr.q);
  return fr;
}

// url: the model file. bytes: its contents instead, for Node, where the tests pose and measure the
// figure without drawing it; without a document there is no WebGL and render() is not available.
async function load(url, bytes) {
  try {
    if (typeof document !== 'undefined') {
      renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, preserveDrawingBuffer: true });
      renderer.setClearColor(0x000000, 0);
      renderer.localClippingEnabled = true;
      // A lost WebGL context empties the canvas, and a restored one starts empty: the picture is gone.
      for (const e of ['webglcontextlost', 'webglcontextrestored']) renderer.domElement.addEventListener(e, () => { drawn.held = false; });
    }
    scene = new THREE.Scene();
    camera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0.1, 50);
    scene.add(new THREE.HemisphereLight(0xffffff, 0x3a3f4a, 1.5));
    sun = new THREE.DirectionalLight(0xffffff, 2.2); scene.add(sun); scene.add(sun.target);
    const gltf = bytes ? await new GLTFLoader().parseAsync(bytes, '') : await new GLTFLoader().loadAsync(url);
    model = gltf.scene; scene.add(model);
    // A neutral matte finish: light on the body, darker at the joints, so the pose reads on dark and light pages.
    model.traverse(o => { if (o.isMesh && o.material) { const joint = /joint/i.test(o.material.name + o.name); o.material = new THREE.MeshStandardMaterial({ color: joint ? 0x596070 : 0xb8bdc9, roughness: 0.62, metalness: 0.04, side: THREE.DoubleSide }); } });
    const names = ['Hips', 'Spine', 'Spine1', 'Spine2', 'Neck', 'Head'];
    for (const s of ['Left', 'Right']) names.push(s + 'Shoulder', s + 'Arm', s + 'ForeArm', s + 'Hand', s + 'UpLeg', s + 'Leg', s + 'Foot', s + 'ToeBase');
    for (const n of names) { bone[n] = find(model, n); if (!bone[n]) throw new Error('the model has no bone called ' + n); }
    // Scale the model so its shoulders are at the figure's shoulder height.
    model.updateMatrixWorld(true);
    model.scale.multiplyScalar(1.43 / wpos(bone.LeftArm).y);
    model.updateMatrixWorld(true);
    const chain = { LeftShoulder: 'LeftArm', RightShoulder: 'RightArm', LeftArm: 'LeftForeArm', LeftForeArm: 'LeftHand', RightArm: 'RightForeArm', RightForeArm: 'RightHand', LeftUpLeg: 'LeftLeg', LeftLeg: 'LeftFoot', LeftFoot: 'LeftToeBase', RightUpLeg: 'RightLeg', RightLeg: 'RightFoot', RightFoot: 'RightToeBase' };
    posedBones = names;
    for (const n of names) rest[n] = { q: wquat(bone[n]), dir: chain[n] ? wpos(bone[chain[n]]).sub(wpos(bone[n])).normalize() : null, local: bone[n].quaternion.clone(), pos: bone[n].position.clone() };
    rest.hipOff = wpos(bone.Hips).y - wpos(bone.LeftUpLeg).y; // the hips bone sits a little above the hip joints
    rest.scale = model.scale.x;
    // For avatar parts: the head and hands in the rest pose, and the ankle height that ends boots.
    rest.head = { m: bone.Head.matrixWorld.clone(), top: wpos(find(model, 'HeadTop_End') || bone.Head) };
    for (const S of ['Left', 'Right']) rest[S + 'HandM'] = bone[S + 'Hand'].matrixWorld.clone();
    rest.ankle = wpos(bone.LeftFoot).y;
    // How much of the shoulder's distance from the spine the collarbone spans, to widen it with the build.
    { const root = wpos(bone.LeftShoulder), arm = wpos(bone.LeftArm), mid = wpos(bone.Spine2); rest.collar = Math.abs(arm.x - mid.x) / Math.max(arm.distanceTo(root), 1e-6); }
    // The model stands in a T-pose facing its own +z: elbows bend toward the front, knees toward the back.
    rest.fwd = new THREE.Vector3(0, 0, 1).transformDirection(model.matrixWorld).normalize();
    // Hands: the rest frame of each, and how each finger joint curls toward the palm.
    for (const [s, S] of [['L', 'Left'], ['R', 'Right']]) {
      const hand = bone[S + 'Hand'], at = n => wpos(find(model, S + 'Hand' + n));
      const w = at('Middle1').sub(wpos(hand)), x = at('Index1').sub(at('Pinky1')), fr = handFrame(x, w, s);
      handRest[s] = { q: fr.q, bq: wquat(hand), grip: at('Middle1').sub(wpos(hand)).length(), N: fr.N.clone() };
      for (const f of ['Thumb', 'Index', 'Middle', 'Ring', 'Pinky']) for (let j = 1; j <= 3; j++) {
        const b = find(model, S + 'Hand' + f + j), c = find(model, S + 'Hand' + f + (j + 1));
        if (!b) continue;
        const dir = (c ? wpos(c) : wpos(b).add(wpos(b).sub(wpos(b.parent)))).sub(wpos(b)).normalize();
        // Fingers curl toward the palm; the thumb curls across it, toward the little finger.
        const toward = f === 'Thumb' ? fr.X.clone().multiplyScalar(-1).add(fr.N.clone().multiplyScalar(0.6)).normalize() : fr.N;
        const axis = new THREE.Vector3().crossVectors(dir, toward).normalize().applyQuaternion(wquat(b).invert());
        fingers[s].push({ b, axis, local: b.quaternion.clone(), thumb: f === 'Thumb', j });
      }
    }
    // Skinning is done here on the CPU and drawn as an ordinary mesh. Skinning in the shader
    // needs float textures in the vertex stage, which some devices (and software WebGL) lack.
    model.traverse(o => {
      if (!o.isSkinnedMesh) return;
      const g = o.geometry, n = g.attributes.position.count, bind = new Float32Array(n * 3), bnor = new Float32Array(n * 3), v = new THREE.Vector3(), nm = new THREE.Matrix3().getNormalMatrix(o.bindMatrix);
      for (let i = 0; i < n; i++) {
        v.fromBufferAttribute(g.attributes.position, i).applyMatrix4(o.bindMatrix); bind.set([v.x, v.y, v.z], i * 3);
        v.fromBufferAttribute(g.attributes.normal, i).applyMatrix3(nm).normalize(); bnor.set([v.x, v.y, v.z], i * 3);
      }
      const out = new THREE.BufferGeometry();
      out.setIndex(g.index); if (g.attributes.uv) out.setAttribute('uv', g.attributes.uv);
      out.setAttribute('position', new THREE.BufferAttribute(new Float32Array(n * 3), 3)); out.setAttribute('normal', new THREE.BufferAttribute(new Float32Array(n * 3), 3));
      const mesh = new THREE.Mesh(out, o.material); mesh.frustumCulled = false; scene.add(mesh); o.visible = false;
      // The bone that moves each vertex most decides which part of an outfit colours it.
      const region = new Uint8Array(n), dom = new Uint16Array(n), wts = g.attributes.skinWeight.array, ids = g.attributes.skinIndex.array, names = o.skeleton.bones.map(b => b.name.replace(/^mixamorig:?/, ''));
      for (let i = 0; i < n; i++) { let bj = 0; for (let j = 1; j < 4; j++) if (wts[i * 4 + j] > wts[i * 4 + bj]) bj = j; dom[i] = ids[i * 4 + bj]; region[i] = regionOf(names[dom[i]], bind[i * 3 + 1]); }
      out.setAttribute('color', new THREE.BufferAttribute(new Float32Array(n * 3).fill(1), 3));
      skins.push({ sm: o, n, bind, bind0: bind.slice(), bnor, idx: g.attributes.skinIndex.array, wt: g.attributes.skinWeight.array, out, norm: g.attributes.skinWeight.normalized, mesh, neutral: o.material, joint: /joint/i.test(o.material.name + o.name), region, dom, names, isHead: new Uint8Array(n) });
    });
    skin(); // the rest pose, skinned, is where parts are built and the head is measured
    measureHead();
    F.ready = true;
  } catch (e) { F.failed = true; F.reason = String(e.message || e); console.warn('3D figure not available:', F.reason); }
  return F.ready;
}

// Put a bone at a world position with a world rotation.
function place(b, pos, q) {
  b.parent.updateWorldMatrix(true, false);
  if (pos) b.position.copy(b.parent.worldToLocal(cp(pos)));
  b.quaternion.copy(wquat(b.parent, tq()).invert().multiply(q));
}
// Point a bone from one joint to the next, turning it the short way from its rest direction.
// With `flex` and `want` it is then rolled about its own axis so that the direction it bends
// in rest (flex) faces `want`: the elbow toward the forearm, the knee toward the shin, the
// forearm's palm side toward the palm. `keep` leaves the bone where its parent put it.
function aim(n, from, to, flex, want, keep) {
  const d = cp(to).sub(from); if (d.lengthSq() < 1e-10) return;
  d.normalize();
  let q = tq().setFromUnitVectors(rest[n].dir, d);
  if (flex && want) {
    const w = cp(want).sub(cp(d).multiplyScalar(want.dot(d))), f = cp(flex).applyQuaternion(q);
    f.sub(cp(d).multiplyScalar(f.dot(d)));
    if (w.lengthSq() > 1e-8 && f.lengthSq() > 1e-8) {
      w.normalize(); f.normalize();
      q = tq().setFromAxisAngle(d, Math.atan2(tv().crossVectors(f, w).dot(d), f.dot(w))).multiply(q);
    }
  }
  place(bone[n], keep ? null : from, q.multiply(rest[n].q));
}
// The part of `v` at right angles to the unit vector `d`, padded with `fallback` as it gets short,
// so a straight limb still knows which way it would bend.
function across(v, d, fallback) {
  const w = cp(v).sub(cp(d).multiplyScalar(v.dot(d))), L = w.length();
  return L > 0.06 ? w : w.add(cp(fallback).multiplyScalar(0.06 - L));
}

// What each side's joints are called in the rig (shL, elL, hipL, kneeL, fL, tL) and its bones in the model.
const SIDES = [['L', 'Left'], ['R', 'Right']].map(([s, S]) => ({ s, sh: 'sh' + s, el: 'el' + s, hip: 'hip' + s, knee: 'knee' + s, f: 'f' + s, t: 't' + s, UpLeg: S + 'UpLeg', Leg: S + 'Leg', Foot: S + 'Foot', Shoulder: S + 'Shoulder', Arm: S + 'Arm', ForeArm: S + 'ForeArm', Hand: S + 'Hand' }));
// How far each joint of a thumb and of a finger curls, in radians, from the knuckle out, closed and open.
const CURL = { thumb: [0.35, 0.45, 0.6], thumbOpen: [0.05, 0.05, 0.05], finger: [1.35, 1.6, 1.1], fingerOpen: [0.16, 0.16, 0.1] };
const UPY = new THREE.Vector3(0, 1, 0), SOLE = new THREE.Vector3(0, -0.035, 0);
function poseBody(J, hands) {
  count.poses++;
  const left = pt(J.left), up = pt(J.up), fwd = tv().crossVectors(left, up);
  for (const n of posedBones) { bone[n].quaternion.copy(rest[n].local); bone[n].position.copy(rest[n].pos); }
  place(bone.Hips, pt(J.pelvis).add(cp(up).multiplyScalar(rest.hipOff)), frameQuat(left, up, fwd, tq()).multiply(rest.Hips.q));
  // The head stays nearer upright than the trunk, as a person's does when they lean.
  const level = tq().setFromUnitVectors(up, cp(up).multiplyScalar(0.4).add(cp(UPY).multiplyScalar(0.6)).normalize()), half = tq().slerp(level, 0.5);
  place(bone.Neck, null, tq().copy(half).multiply(wquat(bone.Neck, tq())));
  place(bone.Head, null, tq().copy(half).multiply(wquat(bone.Head, tq())));
  for (const d of SIDES) {
    const s = d.s, h = hands[s], fr = handFrame(pt(h.x), pt(h.w), s, handNow[s]), k = handRest[s].grip / 0.085;
    const sh = pt(J[d.sh]), el = pt(J[d.el]), hip = pt(J[d.hip]), knee = pt(J[d.knee]), foot = pt(J[d.f]), toe = pt(J[d.t]);
    // The wrist sits behind the grip and under the stick, as in the drawn hand.
    const wrist = pt(h.o).add(cp(fr.W).multiplyScalar(-0.085 * k)).add(cp(fr.N).multiplyScalar(-(h.r + 0.01)));
    // Legs: the knee bends backward and points where the toes point; the shin's front faces the toes.
    const thigh = cp(knee).sub(hip).normalize(), shin = cp(foot).sub(knee).normalize();
    aim(d.UpLeg, hip, knee, cp(rest.fwd).negate(), across(shin, thigh, cp(toe).negate()));
    aim(d.Leg, knee, foot, rest.fwd, across(toe, shin, fwd));
    aim(d.Foot, foot, cp(foot).add(cp(toe).multiplyScalar(0.14)).add(SOLE));
    // Shoulder: the collarbone lifts as the hand goes up, and the arm starts from its tip.
    const lift = Math.max(0, Math.min(1, (h.o[1] - sh.y) / 0.45)) * 0.1;
    aim(d.Shoulder, wpos(bone[d.Shoulder], tv()), cp(sh).add(cp(up).multiplyScalar(lift)), null, null, true);
    const start = wpos(bone[d.Arm], tv());
    // Upper arm: rolled so the elbow is a hinge that bends toward the forearm; when the arm is
    // straight the crease faces forward and a little in, as at rest.
    const upper = cp(el).sub(start).normalize(), fore = cp(wrist).sub(el).normalize();
    aim(d.Arm, start, el, rest.fwd, across(fore, upper, cp(fwd).add(cp(left).multiplyScalar(s === 'L' ? -0.5 : 0.5))), true);
    // Forearm: turned (pronation or supination) so its palm side follows the hand, within what a forearm can do.
    const q0 = tq().setFromUnitVectors(rest[d.ForeArm].dir, fore), n0 = cp(handRest[s].N).applyQuaternion(q0);
    const want = across(fr.N, fore, n0), have = cp(n0).sub(cp(fore).multiplyScalar(n0.dot(fore))).normalize(); want.normalize();
    const turn = Math.max(-1.75, Math.min(1.75, Math.atan2(tv().crossVectors(have, want).dot(fore), have.dot(want))));
    place(bone[d.ForeArm], el, tq().setFromAxisAngle(fore, turn).multiply(q0).multiply(rest[d.ForeArm].q));
    place(bone[d.Hand], wrist, tq().copy(fr.q).multiply(tq().copy(handRest[s].q).invert()).multiply(handRest[s].bq));
    // Fingers close round what is held, less tightly round something thick.
    const closed = h.grip !== 'open', tight = Math.min(1.15, Math.sqrt(0.012 / Math.max(h.r || 0.012, 0.006)));
    for (const f of fingers[s]) {
      const a = f.thumb ? (closed ? CURL.thumb : CURL.thumbOpen)[f.j - 1] : closed ? CURL.finger[f.j - 1] * tight : CURL.fingerOpen[f.j - 1];
      f.b.quaternion.copy(f.local).multiply(tq().setFromAxisAngle(f.axis, a));
    }
  }
}

const mat = (color = '#888888') => { if (!mats.has(color)) mats.set(color, new THREE.MeshStandardMaterial({ color, roughness: 0.45, metalness: 0.05 })); return mats.get(color); };
function props(items) {
  let nc = 0, nb = 0;
  for (const it of items) {
    if (it.c) {
      const m = ball[nb] || (ball[nb] = scene.add(new THREE.Mesh(new THREE.SphereGeometry(1, 24, 16))) && scene.children[scene.children.length - 1]); nb++;
      m.visible = true; m.material = mat(it.color); m.position.fromArray(it.c); m.scale.setScalar(it.r);
    } else for (let i = 0; i < it.pts.length; i++) {
      // A tube from point to point, with a rounded joint at each point.
      const e = ball[nb] || (ball[nb] = scene.add(new THREE.Mesh(new THREE.SphereGeometry(1, 24, 16))) && scene.children[scene.children.length - 1]); nb++;
      e.visible = true; e.material = mat(it.color); e.position.fromArray(it.pts[i]); e.scale.setScalar(it.w / 2);
      if (i === it.pts.length - 1) break;
      const a = pt(it.pts[i]), b = pt(it.pts[i + 1]), d = cp(b).sub(a), L = d.length(); if (L < 1e-6) continue;
      const m = cyl[nc] || (cyl[nc] = scene.add(new THREE.Mesh(new THREE.CylinderGeometry(1, 1, 1, 20, 1, true))) && scene.children[scene.children.length - 1]); nc++;
      m.visible = true; m.material = mat(it.color); m.position.copy(a.add(b).multiplyScalar(0.5)); m.quaternion.setFromUnitVectors(UPY, d.normalize()); m.scale.set(it.w / 2, L, it.w / 2);
    }
  }
  for (let i = nc; i < cyl.length; i++) cyl[i].visible = false;
  for (let i = nb; i < ball.length; i++) ball[i].visible = false;
}

// Move every vertex with the bones it is weighted to.
function skin() {
  count.skins++;
  model.updateMatrixWorld(true);
  for (const k of skins) {
    k.sm.skeleton.update();
    const bm = k.sm.skeleton.boneMatrices, P = k.out.attributes.position.array, N = k.out.attributes.normal.array, { bind, bnor, idx, wt } = k;
    for (let i = 0; i < k.n; i++) {
      const x = bind[i * 3], y = bind[i * 3 + 1], z = bind[i * 3 + 2], a = bnor[i * 3], b = bnor[i * 3 + 1], c = bnor[i * 3 + 2];
      let px = 0, py = 0, pz = 0, nx = 0, ny = 0, nz = 0, sum = 0;
      for (let j = 0; j < 4; j++) {
        const w = wt[i * 4 + j]; if (!w) continue;
        const m = idx[i * 4 + j] * 16; sum += w;
        px += w * (bm[m] * x + bm[m + 4] * y + bm[m + 8] * z + bm[m + 12]); py += w * (bm[m + 1] * x + bm[m + 5] * y + bm[m + 9] * z + bm[m + 13]); pz += w * (bm[m + 2] * x + bm[m + 6] * y + bm[m + 10] * z + bm[m + 14]);
        nx += w * (bm[m] * a + bm[m + 4] * b + bm[m + 8] * c); ny += w * (bm[m + 1] * a + bm[m + 5] * b + bm[m + 9] * c); nz += w * (bm[m + 2] * a + bm[m + 6] * b + bm[m + 10] * c);
      }
      // The length written out: V8 puts each number handed to Math.hypot on the heap, 3 MB for one skinning.
      const s = sum || 1, l = Math.sqrt(nx * nx + ny * ny + nz * nz) || 1;
      P[i * 3] = px / s; P[i * 3 + 1] = py / s; P[i * 3 + 2] = pz / s; N[i * 3] = nx / l; N[i * 3 + 1] = ny / l; N[i * 3 + 2] = nz / l;
    }
    k.out.attributes.position.needsUpdate = true; k.out.attributes.normal.needsUpdate = true;
  }
  // Avatar parts follow the bone they are fixed to.
  for (const p of parts) { p.g.matrix.multiplyMatrices(bone[p.bone].matrixWorld, p.offset); p.g.matrixWorldNeedsUpdate = true; }
}

// Pose the body, skin it and place the props: what render and probe share. The body is posed and
// skinned only when the joints and the hands are not the ones it was last skinned for.
function stage(J, hands, items) {
  takeBack();
  drawn.held = false; // the scene is being changed, so the picture is of something else until render says otherwise
  begin(posed); note(posed, F.nopose); note(posed, J); note(posed, hands);
  if (!unchanged(posed)) { if (!F.nopose) poseBody(J, hands); skin(); posed.held = true; }
  props(items);
  scene.updateMatrixWorld(true); // the parts' groups, which render would otherwise bring up to date
}
// W: size in pixels. J: joints. hands: { L, R } each { o, x, w, grip, r }. items: props as tubes and balls.
// cam: { right, up, toward (unit vectors), cx, cy (centre, along right and up), half (half the width shown, metres),
// cut (optional: metres behind the centre beyond which nothing is drawn, for a close-up of a hand) }.
// Returns a canvas that holds the picture. When the size, the pose, the props with their colours and the
// camera are all the ones of the last picture (and the body and the avatar have not changed since), nothing
// is posed, skinned or rendered: the picture is still there.
let keep = null, kept = false;
function render(W, J, hands, items, cam) {
  begin(drawn); note(drawn, W); note(drawn, F.nopose); note(drawn, J); note(drawn, hands); note(drawn, items); note(drawn, cam);
  if (unchanged(drawn)) {
    // A browser may read a WebGL canvas back from the graphics card every time it is copied, so the second
    // time the same picture is asked for it is copied once into a plain canvas and handed out from there.
    if (!kept) { keep = keep || document.createElement('canvas'); keep.width = keep.height = W; keep.getContext('2d').drawImage(renderer.domElement, 0, 0); kept = true; }
    count.reused++;
    return keep;
  }
  kept = false;
  if (renderer.domElement.width !== W) renderer.setSize(W, W, false);
  stage(J, hands, items);
  const R = pt(cam.right), U = pt(cam.up), T = pt(cam.toward), c = cp(R).multiplyScalar(cam.cx).add(cp(U).multiplyScalar(cam.cy));
  camera.left = -cam.half; camera.right = cam.half; camera.top = cam.half; camera.bottom = -cam.half; camera.updateProjectionMatrix();
  camera.position.copy(cp(c).add(cp(T).multiplyScalar(20))); camera.up.copy(U); camera.lookAt(c);
  if (cam.cut) { cut.normal.copy(T); cut.constant = cam.cut - c.dot(T); }
  renderer.clippingPlanes = cam.cut ? CUT : NOCUT;
  sun.position.copy(cp(c).add(cp(T).multiplyScalar(4)).add(cp(U).multiplyScalar(3)).add(cp(R).multiplyScalar(-2.5))); sun.target.position.copy(c);
  renderer.render(scene, camera); count.renders++;
  drawn.held = true;
  return renderer.domElement;
}


/* ---------- avatars ---------- */
// Parts are made here in code, not loaded: an outfit colours the body by the bone that moves each
// vertex, and hair, faces, hats, glasses and wristbands are small meshes fixed to one bone each, so
// they cost almost nothing per frame. No textures, no extra files.
const REGION = { skin: 0, top: 1, arm: 2, forearm: 3, bottoms: 4, shin: 5, boot: 6, shoe: 7, ankle: 8 };
let parts = [], head = null, avatar = null;
function regionOf(name, y) {
  if (/^(Head|HeadTop_End|Neck)/.test(name) || /Hand/.test(name)) return REGION.skin;
  if (/^(Spine|LeftShoulder|RightShoulder)/.test(name)) return REGION.top;
  if (/(Left|Right)Arm$/.test(name)) return REGION.arm;
  if (/ForeArm$/.test(name)) return REGION.forearm;
  if (name === 'Hips' || /UpLeg$/.test(name)) return REGION.bottoms;
  if (/(Left|Right)Leg$/.test(name)) return REGION.shin; // the part near the ankle becomes boot, in measureHead
  if (/Foot|Toe/.test(name)) return REGION.shoe;
  return REGION.skin;
}
// Where the head is in the rest pose, measured from the skinned vertices the head bone moves (the
// model is skinned once at rest before this). The head is not a ball: the X Bot's is 12.5 cm front to back and 8.8 cm to each side, so face parts
// are placed on its measured skin (skinPoint) and caps are stretched to its three semi-axes (head.ax).
function measureHead() {
  const up = new THREE.Vector3(0, 1, 0), fwd = rest.fwd.clone(), side = new THREE.Vector3().crossVectors(up, fwd).normalize();
  const c = new THREE.Vector3(), neckY = wpos(bone.Head).y, v = new THREE.Vector3(), pts = [];
  let top = -Infinity;
  for (const k of skins) {
    const P = k.out.attributes.position.array;
    for (let i = 0; i < k.n; i++) {
      v.set(P[i * 3], P[i * 3 + 1], P[i * 3 + 2]);
      // Boots and high-tops: shin vertices near the ankle take the shoe colour (high-tops up to 6 cm, boots 13 cm).
      if (k.region[i] === REGION.shin) { const h = v.y - rest.ankle; if (h < 0.06) k.region[i] = REGION.ankle; else if (h < 0.13) k.region[i] = REGION.boot; }
      if (k.joint || k.region[i] !== REGION.skin || v.y < neckY) continue; // the head is the skin above the neck
      k.isHead[i] = 1; c.add(v); pts.push(v.clone()); top = Math.max(top, v.y);
    }
  }
  const centre = pts.length ? c.multiplyScalar(1 / pts.length) : wpos(bone.Head);
  const r = Math.max(0.06, (top - centre.y) * 0.98);
  for (const q of pts) q.sub(centre);
  const ext = d => pts.reduce((m, q) => Math.max(m, q.dot(d)), 0);
  const front = ext(fwd), back = ext(fwd.clone().negate()), wide = Math.max(ext(side), ext(side.clone().negate()));
  // The ellipsoid that hugs the skull: its semi-axes (to the side, up, front to back) and its centre.
  head = { centre, r, up, fwd, side, pts, ax: new THREE.Vector3(wide, r, (front + back) / 2), mid: centre.clone().add(fwd.clone().multiplyScalar((front - back) / 2)) };
}
// How far the skin is from the head's centre in the direction d: the head vertices within 10 degrees
// of d, weighted toward the middle (within 20 degrees where the mesh is sparse). Measured, not assumed
// round, so a part placed with it sits on the skin, and the nose does not push the eyes forward.
function headExtent(d) {
  for (const cone of [0.985, 0.94]) {
    let sum = 0, w = 0;
    for (const q of head.pts) { const L = q.length(), c = L > 1e-6 ? q.dot(d) / L : 0; if (c > cone) { const k = (c - cone) / (1 - cone); sum += k * L; w += k; } }
    if (w > 0.5) return sum / w;
  }
  return head.r;
}
// A point `out` metres outside the skin in the direction (f, s, u) from the head's centre: forward, to
// the figure's left, up. Returns the point and the direction.
function skinPoint(f, s, u, out = 0) {
  const d = head.fwd.clone().multiplyScalar(f).add(head.side.clone().multiplyScalar(s)).add(head.up.clone().multiplyScalar(u)).normalize();
  return { at: head.centre.clone().add(d.clone().multiplyScalar(headExtent(d) + out)), d };
}
const hexColor = h => new THREE.Color(h || '#888888');
const lambert = color => new THREE.MeshStandardMaterial({ color: hexColor(color), roughness: 0.75, metalness: 0.02 });
// A part built in rest-pose world coordinates and fixed to a bone from then on.
function attach(mesh, boneName, restM) {
  const g = new THREE.Group(); g.add(mesh); g.matrixAutoUpdate = false;
  g.updateMatrix(); scene.add(g);
  const offset = new THREE.Matrix4().copy(restM).invert().multiply(new THREE.Matrix4().compose(new THREE.Vector3(), new THREE.Quaternion(), new THREE.Vector3(1, 1, 1)));
  parts.push({ g, bone: boneName, offset });
  return g;
}
// A mesh at a point, turned by `quat`.
function put(geo, mat, at, quat) {
  const m = new THREE.Mesh(geo, mat);
  m.position.copy(at); if (quat) m.quaternion.copy(quat);
  m.updateMatrix();
  return m;
}
// A thin rod from a to b.
function rod(a, b, radius, mat) {
  const d = b.clone().sub(a), m = new THREE.Mesh(new THREE.CylinderGeometry(radius, radius, d.length(), 6), mat);
  m.position.copy(a).add(b).multiplyScalar(0.5); m.quaternion.setFromUnitVectors(new THREE.Vector3(0, 1, 0), d.normalize()); m.updateMatrix();
  return m;
}
const faceQuat = () => new THREE.Quaternion().setFromRotationMatrix(new THREE.Matrix4().makeBasis(head.side, head.up, head.fwd));
function buildHead(cfg) {
  // Each piece is tagged with its part, so the parts test can measure each one.
  let tag = 'hair';
  const H = new THREE.Group(), r = head.r, add = m => { m.userData.part = tag; H.add(m); }, q = faceQuat(), Z = new THREE.Vector3(0, 0, 1);
  const hairM = lambert(cfg.hairColor), dark = lambert('#202124'), white = lambert('#f2f1ee'), lip = lambert('#7a3a3a');
  // Caps, crowns and bands are round shapes of radius r stretched to the skull's semi-axes, k being the
  // margin over the skin (ky the stretch upward), so they cover a head that is longer than it is wide.
  const ell = (m, k, ky = k) => { m.scale.set(head.ax.x / r * k, ky, head.ax.z / r * k); m.updateMatrix(); return m; };
  const cap = (k, phi = 0.55) => ell(put(new THREE.SphereGeometry(r, 28, 16, 0, Math.PI * 2, 0, Math.PI * phi), hairM, head.mid.clone().add(head.up.clone().multiplyScalar(r * 0.02))), k, 1.02);
  const hat = cfg.hat && cfg.hat !== 'none';
  switch (cfg.hair) {
    case 'buzz': if (!hat) add(cap(1.02, 0.5)); break;
    case 'short': if (!hat) add(cap(1.07, 0.56)); break;
    case 'long': if (!hat) add(cap(1.07, 0.56)); { const back = ell(put(new THREE.CylinderGeometry(r, r * 1.08, r * 1.5, 24, 1, true, Math.PI * 0.5, Math.PI), hairM, head.mid.clone().add(head.up.clone().multiplyScalar(-r * 0.55))), 1.04, 1); back.material = hairM.clone(); back.material.side = THREE.DoubleSide; add(back); } break;
    case 'ponytail': if (!hat) add(cap(1.06, 0.55)); for (let i = 0; i < 4; i++) add(put(new THREE.SphereGeometry(r * (0.24 - i * 0.03), 12, 10), hairM, skinPoint(-1, 0, 0.25 - i * 0.32, r * (0.1 + 0.05 * i)).at)); break;
    case 'bun': if (!hat) add(cap(1.05, 0.55)); add(put(new THREE.SphereGeometry(r * 0.32, 16, 12), hairM, skinPoint(-0.55, 0, 0.95, r * 0.14).at)); break;
    case 'curly': if (!hat) { const ico = new THREE.IcosahedronGeometry(r * 1.02, 2).attributes.position; const seen = new Set(); for (let i = 0; i < ico.count; i++) { const v = new THREE.Vector3().fromBufferAttribute(ico, i); const k = v.toArray().map(x => x.toFixed(3)).join(); if (seen.has(k)) continue; seen.add(k); const up = v.clone().normalize().dot(head.up), fw = v.clone().normalize().dot(head.fwd); if (up < 0.05 || (fw > 0.55 && up < 0.6)) continue; v.x *= head.ax.x / r; v.z *= head.ax.z / r; add(put(new THREE.SphereGeometry(r * 0.17, 8, 6), hairM, head.mid.clone().add(v))); } } break;
  }
  // Eyes, brows and mouth sit on the skin of the face, measured, so they show.
  tag = 'eyes';
  const eyeR = { round: 0.11, wide: 0.14, narrow: 0.11, sleepy: 0.11 }[cfg.eyes] || 0.11;
  for (const sd of [-1, 1]) {
    // The eyeball sits a third of the way into the skin, so most of it shows, with the pupil on its outer side.
    const { at, d } = skinPoint(1, sd * 0.33, 0.1, -eyeR * r * 0.35);
    const eye = put(new THREE.SphereGeometry(r * eyeR, 14, 10), white, at, q);
    if (cfg.eyes === 'narrow') eye.scale.set(1, 0.5, 1); if (cfg.eyes === 'sleepy') eye.scale.set(1, 0.6, 1);
    eye.updateMatrix(); add(eye);
    add(put(new THREE.SphereGeometry(r * eyeR * 0.55, 10, 8), dark, at.clone().add(d.clone().multiplyScalar(r * eyeR * 0.62)), q));
    if (cfg.eyes === 'sleepy') add(put(new THREE.SphereGeometry(r * eyeR * 1.06, 14, 8, 0, Math.PI * 2, 0, Math.PI * 0.45), lambert(cfg.skin), at.clone(), q));
    if (cfg.brows !== 'none') {
      tag = 'brows';
      const h = cfg.brows === 'thick' ? 0.075 : 0.042, brow = put(new THREE.BoxGeometry(r * 0.3, r * h, r * 0.06), lambert(cfg.hairColor), skinPoint(1, sd * 0.33, 0.34, r * 0.035).at, q.clone().multiply(new THREE.Quaternion().setFromAxisAngle(Z, cfg.brows === 'arched' ? sd * -0.28 : cfg.brows === 'angled' ? sd * 0.3 : 0)));
      add(brow); tag = 'eyes';
    }
  }
  tag = 'mouth';
  if (cfg.mouth === 'smile' || cfg.mouth === 'grin') {
    add(put(new THREE.TorusGeometry(r * (cfg.mouth === 'grin' ? 0.24 : 0.17), r * 0.03, 6, 18, Math.PI), lip, skinPoint(1, 0, -0.28, r * 0.05).at, q.clone().multiply(new THREE.Quaternion().setFromAxisAngle(Z, Math.PI))));
    if (cfg.mouth === 'grin') add(put(new THREE.BoxGeometry(r * 0.3, r * 0.07, r * 0.04), white, skinPoint(1, 0, -0.42, r * 0.03).at, q));
  } else if (cfg.mouth === 'open') add(put(new THREE.SphereGeometry(r * 0.12, 12, 10), lip, skinPoint(1, 0, -0.38, -r * 0.04).at, q));
  else add(put(new THREE.BoxGeometry(r * 0.26, r * 0.035, r * 0.04), lip, skinPoint(1, 0, -0.38, r * 0.025).at, q));
  // Hats: a crown over the skull and, for the cap and the bucket hat, a brim from the brow.
  tag = 'hat';
  if (hat) {
    const hm = lambert(cfg.hatColor), top = head.mid.clone().add(head.up.clone().multiplyScalar(r * 0.12));
    if (cfg.hat === 'cap') {
      add(ell(put(new THREE.SphereGeometry(r, 28, 14, 0, Math.PI * 2, 0, Math.PI * 0.5), hm, top), 1.1));
      // The peak is a half disc whose straight edge starts at the brow and reaches 6 cm out.
      add(put(new THREE.CylinderGeometry(r * 0.6, r * 0.6, r * 0.05, 24, 1, false, -Math.PI * 0.5, Math.PI), hm, skinPoint(1, 0, 0.45, r * 0.02).at, new THREE.Quaternion().setFromUnitVectors(Z, head.fwd)));
    }
    if (cfg.hat === 'beanie') {
      add(ell(put(new THREE.SphereGeometry(r, 28, 14, 0, Math.PI * 2, 0, Math.PI * 0.55), hm, top), 1.09, 1.09 * 1.18));
      const band = put(new THREE.TorusGeometry(r, r * 0.08, 8, 28), hm, top.clone().add(head.up.clone().multiplyScalar(r * 0.05)), new THREE.Quaternion().setFromUnitVectors(Z, head.up));
      band.scale.set(head.ax.x / r * 1.05, head.ax.z / r * 1.05, 1); band.updateMatrix(); add(band);
    }
    if (cfg.hat === 'bucket') {
      add(ell(put(new THREE.CylinderGeometry(r * 0.9, r * 1.05, r * 0.7, 24, 1), hm, top.clone().add(head.up.clone().multiplyScalar(r * 0.42))), 1.03, 1));
      const brim = put(new THREE.CylinderGeometry(1, 1, r * 0.05, 28), hm, top.clone().add(head.up.clone().multiplyScalar(r * 0.08)));
      brim.scale.set(1.05 * 1.03 * head.ax.x + 0.048, 1, 1.05 * 1.03 * head.ax.z + 0.048); brim.updateMatrix(); add(brim); // 5 cm of brim beyond the crown
    }
  }
  // Glasses: lenses 1.2 cm in front of the skin, clear of the eyes, a bridge between them, and temples
  // back to the ears, so they read as glasses from the side too.
  tag = 'glasses';
  if (cfg.glasses && cfg.glasses !== 'none') {
    const gm = lambert('#1b1c1f');
    for (const sd of [-1, 1]) {
      const { at } = skinPoint(1, sd * 0.33, 0.1, 0.012);
      if (cfg.glasses === 'round') add(put(new THREE.TorusGeometry(r * 0.17, r * 0.022, 6, 20), gm, at, q));
      else for (const [w, h, dx, dy] of [[0.4, 0.035, 0, 0.14], [0.4, 0.035, 0, -0.14], [0.035, 0.3, 0.19, 0], [0.035, 0.3, -0.19, 0]]) add(put(new THREE.BoxGeometry(r * w, r * h, r * 0.03), gm, at.clone().add(head.side.clone().multiplyScalar(r * dx)).add(head.up.clone().multiplyScalar(r * dy)), q));
      add(rod(at.clone().add(head.side.clone().multiplyScalar(sd * r * 0.2)), skinPoint(0.05, sd, 0.1, 0.008).at, r * 0.02, gm));
    }
    add(rod(skinPoint(1, -0.15, 0.12, 0.012).at, skinPoint(1, 0.15, 0.12, 0.012).at, r * 0.02, gm));
  }
  return merge(H);
}
// The pieces of a group as one mesh with one material, coloured per vertex: one draw call per bone.
// Keeps where each part's vertices are, for the parts test.
function merge(group) {
  const geos = [], ranges = [];
  let at = 0;
  group.updateMatrixWorld(true);
  group.traverse(o => {
    if (!o.isMesh) return;
    const g = o.geometry.toNonIndexed(); g.applyMatrix4(o.matrixWorld); g.deleteAttribute('uv');
    const n = g.attributes.position.count, c = o.material.color, col = new Float32Array(n * 3);
    for (let i = 0; i < n; i++) col.set([c.r, c.g, c.b], i * 3);
    g.setAttribute('color', new THREE.BufferAttribute(col, 3));
    geos.push(g); ranges.push({ part: o.userData.part || 'part', from: at, n }); at += n;
    o.geometry.dispose();
  });
  const mesh = new THREE.Mesh(geos.length ? mergeGeometries(geos) : new THREE.BufferGeometry(), new THREE.MeshStandardMaterial({ vertexColors: true, roughness: 0.75, metalness: 0.02, side: THREE.DoubleSide }));
  mesh.userData.ranges = ranges;
  return mesh;
}
// Colours each vertex of the body for the outfit, and makes the parts. Called with null for the plain figure.
function setAvatar(cfg) {
  if (!F.ready) return false;
  for (const p of parts) { scene.remove(p.g); p.g.traverse(o => { if (o.geometry) o.geometry.dispose(); }); }
  parts = []; avatar = cfg || null; forget();
  if (!cfg) { for (const k of skins) k.mesh.material = k.neutral; return true; }
  const skin = hexColor(cfg.skin), topC = hexColor(cfg.topColor), botC = hexColor(cfg.bottomsColor), shoeC = cfg.shoes === 'barefoot' ? skin : hexColor(cfg.shoesColor);
  const sleeve = { tank: 0, tshirt: 1, longsleeve: 2, hoodie: 2 }[cfg.top] ?? 1, longLegs = cfg.bottoms !== 'shorts';
  const legC = longLegs ? botC : skin, high = cfg.shoes === 'boots' || cfg.shoes === 'hightops';
  const byRegion = [skin, topC, sleeve >= 1 ? topC : skin, sleeve >= 2 ? topC : skin, botC, legC, cfg.shoes === 'boots' ? shoeC : legC, shoeC, high ? shoeC : legC];
  for (const k of skins) {
    const col = k.out.attributes.color.array, shade = k.joint ? 0.72 : 1;
    for (let i = 0; i < k.n; i++) { const c = byRegion[k.region[i]]; col[i * 3] = c.r * shade; col[i * 3 + 1] = c.g * shade; col[i * 3 + 2] = c.b * shade; }
    k.out.attributes.color.needsUpdate = true;
    k.mesh.material = new THREE.MeshStandardMaterial({ vertexColors: true, roughness: 0.7, metalness: 0.02 });
  }
  attach(buildHead(cfg), 'Head', rest.head.m);
  if (cfg.wristbands === 'yes') for (const S of ['Left', 'Right']) {
    const m = new THREE.Mesh(new THREE.TorusGeometry(0.036, 0.011, 8, 20), lambert(cfg.wristColor));
    // Built at the hand's rest position (the bones may be in any pose now), and fixed to the hand.
    const at = new THREE.Vector3().setFromMatrixPosition(rest[S + 'HandM']).add(new THREE.Vector3().setFromMatrixColumn(rest[S + 'HandM'], 1).normalize().multiplyScalar(-0.015));
    m.position.copy(at); m.quaternion.setFromUnitVectors(new THREE.Vector3(0, 0, 1), new THREE.Vector3().setFromMatrixColumn(rest[S + 'HandM'], 1).normalize()); m.updateMatrix();
    m.userData.ranges = [{ part: 'wristbands', from: 0, n: m.geometry.attributes.position.count }];
    attach(m, S + 'Hand', rest[S + 'HandM']);
  }
  return true;
}

// Body presets (web/demo.js setBody): sc scales lengths; bd scales widths; frame is the figure's frame,
// neutral (the model as it is), feminine or masculine. Bones are never scaled unevenly, since posing
// reads each parent's rotation: instead the rest-pose vertices of the trunk spread out from the spine
// and those of the limbs from their bone, and the collarbones grow so the arms start where the rig's
// shoulders are. A frame changes the proportions along the trunk (shoulders, chest, waist, hips, neck),
// the thickness of the arms, and gives the feminine frame a bust; the rig and the body checks are not
// changed by it (docs/avatars.md, "Frames").
const FRAMES = {
  neutral: { shoulder: 1, chest: 1, waist: 1, hip: 1, neck: 1, arm: 1, leg: 1, bust: 0 },
  feminine: { shoulder: 0.93, chest: 0.97, waist: 0.9, hip: 1.06, neck: 0.88, arm: 0.94, leg: 1.02, bust: 0.035 },
  masculine: { shoulder: 1.06, chest: 1.04, waist: 1.02, hip: 0.97, neck: 1.08, arm: 1.05, leg: 1, bust: 0 },
};
let body = { sc: 1, bd: 1, frame: 'neutral' };
function setBody(sc = 1, bd0 = 1, frame = 'neutral') {
  if (!FRAMES[frame]) frame = 'neutral';
  if (!F.ready || (body.sc === sc && body.bd === bd0 && body.frame === frame)) return;
  body = { sc, bd: bd0, frame }; forget();
  const fr = FRAMES[frame];
  model.scale.setScalar(rest.scale * sc);
  // The model scales evenly with height; widths follow the build alone, as in the rig, so they are
  // narrowed back by the height.
  const bd = bd0 / sc;
  for (const S of ['Left', 'Right']) {
    const r = rest[S + 'Arm'];
    if (!r.pos0) r.pos0 = r.pos.clone();
    r.pos.copy(r.pos0).multiplyScalar(1 + (bd * fr.shoulder - 1) * rest.collar);
  }
  for (const k of skins) {
    const B = k.bind, B0 = k.bind0, sk = k.sm.skeleton;
    if (!k.bonePos) {
      // Each bone's place and its child's in the bind pose, and which bones make the trunk, the neck
      // and each kind of limb.
      const names = sk.bones.map(b => b.name.replace(/^mixamorig:?/, ''));
      k.trunk = names.map(n => /^(Hips|Spine|Spine1|Spine2|LeftShoulder|RightShoulder)$/.test(n));
      k.neck = names.map(n => n === 'Neck');
      k.limb = names.map(n => /(Arm|ForeArm)$/.test(n) && !/Shoulder/.test(n) ? 'arm' : /(UpLeg|Leg)$/.test(n) ? 'leg' : '');
      k.bonePos = sk.boneInverses.map(m => new THREE.Vector3().setFromMatrixPosition(m.clone().invert()));
      k.childPos = sk.bones.map((b, i) => { const c = b.children.find(x => x.isBone); return c ? k.bonePos[sk.bones.indexOf(c)] || k.bonePos[i] : k.bonePos[i]; });
      let cx = 0, cz = 0, n = 0;
      for (let i = 0; i < k.n; i++) if (k.region[i] === REGION.top) { cx += B0[i * 3]; cz += B0[i * 3 + 2]; n++; }
      k.axis = n ? [cx / n, cz / n] : [0, 0];
      // Heights of the hips, waist, chest, shoulders and neck in the bind pose, for the frame's profile.
      const at = n => k.bonePos[names.indexOf(n)].y;
      k.marks = { hip: at('LeftUpLeg'), waist: at('Spine1'), chest: at('Spine2'), shoulder: at('LeftArm'), neck: at('Neck') };
    }
    const m = k.marks, knots = [[m.hip - 0.2, fr.hip], [m.hip + 0.03, fr.hip], [m.waist, fr.waist], [m.chest, fr.chest], [m.shoulder, fr.shoulder], [m.neck, fr.neck], [m.neck + 0.2, fr.neck]];
    // The frame's width factor at a height: a line through the knots.
    const profile = y => { for (let i = 1; i < knots.length; i++) if (y <= knots[i][0]) { const [y0, f0] = knots[i - 1], [y1, f1] = knots[i], t = Math.max(0, Math.min(1, (y - y0) / (y1 - y0))); return f0 + (f1 - f0) * (t * t * (3 - 2 * t)); } return knots[knots.length - 1][1]; };
    const bustY = (m.waist + m.chest) / 2 + 0.02, a = new THREE.Vector3(), b = new THREE.Vector3(), p = new THREE.Vector3(), c = new THREE.Vector3();
    for (let i = 0; i < k.n; i++) {
      let wT = 0, wN = 0, wL = 0, bl = -1, bw = 0;
      for (let j = 0; j < 4; j++) {
        const w = k.wt[i * 4 + j], id = k.idx[i * 4 + j]; if (!w) continue;
        if (k.trunk[id]) wT += w;
        if (k.neck[id]) wN += w;
        if (k.limb[id]) { wL += w; if (w > bw) { bw = w; bl = id; } }
      }
      const x = B0[i * 3], y = B0[i * 3 + 1], z = B0[i * 3 + 2];
      if ((bd === 1 && frame === 'neutral') || (wT === 0 && wL === 0 && wN === 0)) { B[i * 3] = x; B[i * 3 + 1] = y; B[i * 3 + 2] = z; continue; }
      // Trunk: out from the spine's vertical line, by the build and the frame's profile at this height.
      // Neck: by the frame alone. Limbs: out from the bone's line.
      const fT = 1 + (bd * profile(y) - 1) * wT + (fr.neck - 1) * wN;
      let nx = k.axis[0] + (x - k.axis[0]) * fT, ny = y, nz = k.axis[1] + (z - k.axis[1]) * fT;
      if (bl >= 0) {
        a.copy(k.bonePos[bl]); b.copy(k.childPos[bl]); p.set(x, y, z);
        const d = b.clone().sub(a), t = d.lengthSq() > 1e-12 ? Math.max(0, Math.min(1, p.clone().sub(a).dot(d) / d.lengthSq())) : 0;
        c.copy(a).add(d.multiplyScalar(t));
        const fL = (bd * fr[k.limb[bl]] - 1) * wL;
        nx += (x - c.x) * fL; ny += (y - c.y) * fL; nz += (z - c.z) * fL;
      }
      // The bust: the front of the chest comes forward, most at two points either side of the middle.
      if (fr.bust && wT > 0 && z > k.axis[1] + 0.02) {
        const dx = Math.abs(x - k.axis[0]) - 0.07, dy = y - bustY, g = Math.exp(-(dx * dx + dy * dy) / (2 * 0.05 * 0.05));
        nz += fr.bust * g * wT;
      }
      B[i * 3] = nx; B[i * 3 + 1] = ny; B[i * 3 + 2] = nz;
    }
  }
}

// One corner of each of a part's triangles where the last render put them, in the rig's world (metres),
// for the parts test (the pieces are not indexed, so every third vertex is one per triangle).
function partPoints() {
  const out = {}, v = new THREE.Vector3();
  for (const p of parts) p.g.traverse(o => {
    if (!o.isMesh || !o.userData.ranges) return;
    o.updateMatrixWorld(true);
    const pos = o.geometry.attributes.position, idx = o.geometry.index;
    for (const r of o.userData.ranges) {
      const list = out[r.part] || (out[r.part] = []);
      for (let i = r.from; i < r.from + r.n; i += 3) { v.fromBufferAttribute(pos, idx ? idx.getX(i) : i).applyMatrix4(o.matrixWorld); list.push([v.x, v.y, v.z]); }
    }
  });
  return out;
}

// The skin as triangles, where the last staging put it: those with a vertex `keep` accepts, as flat
// arrays of corners and face normals, with the bone that moves each most. A large set also gets a grid
// of 5 cm cells, so a query looks only near its point. Gathered once per measurement.
function skinSet(keep) {
  const T = [], N = [], B = [], A = [], a = new THREE.Vector3(), b = new THREE.Vector3(), c = new THREE.Vector3();
  for (const k of skins) {
    const p = k.out.attributes.position.array, idx = k.out.index.array;
    for (let t = 0; t < idx.length; t += 3) {
      const i = idx[t], j = idx[t + 1], l = idx[t + 2];
      if (keep && !(keep(k, i) || keep(k, j) || keep(k, l))) continue;
      a.fromArray(p, i * 3); b.fromArray(p, j * 3); c.fromArray(p, l * 3);
      const n = new THREE.Vector3().crossVectors(b.clone().sub(a), c.clone().sub(a)); if (n.lengthSq() < 1e-14) continue;
      n.normalize();
      const bn = k.names[k.dom[i]];
      T.push(a.x, a.y, a.z, b.x, b.y, b.z, c.x, c.y, c.z); N.push(n.x, n.y, n.z); B.push(bn); A.push(/Arm$/.test(bn) ? (bn[0] === 'L' ? 1 : 2) : 0);
    }
  }
  const set = { T: Float32Array.from(T), N: Float32Array.from(N), B, A: Uint8Array.from(A), n: B.length, cell: 0.05, grid: null };
  if (set.n > 4000) {
    const g = new Map(), key = (x, y, z) => ((x + 512) << 20) | ((y + 512) << 10) | (z + 512), f = v => Math.floor(v / set.cell);
    for (let t = 0; t < set.n; t++) {
      const o = t * 9, xs = [T[o], T[o + 3], T[o + 6]], ys = [T[o + 1], T[o + 4], T[o + 7]], zs = [T[o + 2], T[o + 5], T[o + 8]];
      for (let x = f(Math.min(...xs)); x <= f(Math.max(...xs)); x++) for (let y = f(Math.min(...ys)); y <= f(Math.max(...ys)); y++) for (let z = f(Math.min(...zs)); z <= f(Math.max(...zs)); z++) { const k = key(x, y, z); if (!g.has(k)) g.set(k, []); g.get(k).push(t); }
    }
    set.grid = g; set.key = key;
  }
  return set;
}
// The point of the triangle (a, b, c) nearest p, into q (Ericson, Real-Time Collision Detection, 5.1.5).
function nearestOnTriangle(p, a, b, c, q) {
  const ab = b.clone().sub(a), ac = c.clone().sub(a), ap = p.clone().sub(a), d1 = ab.dot(ap), d2 = ac.dot(ap);
  if (d1 <= 0 && d2 <= 0) return q.copy(a);
  const bp = p.clone().sub(b), d3 = ab.dot(bp), d4 = ac.dot(bp);
  if (d3 >= 0 && d4 <= d3) return q.copy(b);
  const vc = d1 * d4 - d3 * d2;
  if (vc <= 0 && d1 >= 0 && d3 <= 0) return q.copy(a).addScaledVector(ab, d1 / (d1 - d3));
  const cp = p.clone().sub(c), d5 = ab.dot(cp), d6 = ac.dot(cp);
  if (d6 >= 0 && d5 <= d6) return q.copy(c);
  const vb = d5 * d2 - d1 * d6;
  if (vb <= 0 && d2 >= 0 && d6 <= 0) return q.copy(a).addScaledVector(ac, d2 / (d2 - d6));
  const va = d3 * d6 - d5 * d4;
  if (va <= 0 && d4 - d3 >= 0 && d5 - d6 >= 0) return q.copy(b).addScaledVector(c.clone().sub(b), (d4 - d3) / ((d4 - d3) + (d5 - d6)));
  const den = 1 / (va + vb + vc), v = vb * den, w = vc * den;
  return q.copy(a).addScaledVector(ab, v).addScaledVector(ac, w);
}
// Signed distance from p to the skin: to the nearest triangle of the set, positive on the side its
// normal faces. Beyond `reach` the answer is `reach` (and with a grid only triangles within it are looked
// at): far from the skin the sign of one triangle means nothing.
const tA = new THREE.Vector3(), tB = new THREE.Vector3(), tC = new THREE.Vector3(), tQ = new THREE.Vector3();
// `arm` narrows the set: { not: 1 } leaves out the left arm's triangles, { only: 2 } keeps the right arm's alone.
function skinDistance(p, set, reach = 0.08, arm = null) {
  const { T, N, A } = set, not = arm && arm.not, only = arm && arm.only;
  let best = Infinity, bi = -1, sign = 1;
  const tri = t => {
    if (not && A[t] === not || only && A[t] !== only) return;
    const o = t * 9;
    tA.fromArray(T, o); tB.fromArray(T, o + 3); tC.fromArray(T, o + 6);
    nearestOnTriangle(p, tA, tB, tC, tQ);
    const d2 = tQ.distanceToSquared(p);
    if (d2 < best) { best = d2; bi = t; sign = (p.x - tQ.x) * N[t * 3] + (p.y - tQ.y) * N[t * 3 + 1] + (p.z - tQ.z) * N[t * 3 + 2] < 0 ? -1 : 1; }
  };
  if (!set.grid) { for (let t = 0; t < set.n; t++) tri(t); if (best > reach * reach) return { d: reach, bone: '' }; }
  else {
    const f = v => Math.floor(v / set.cell), r = Math.ceil(reach / set.cell), seen = new Set();
    for (let x = f(p.x) - r; x <= f(p.x) + r; x++) for (let y = f(p.y) - r; y <= f(p.y) + r; y++) for (let z = f(p.z) - r; z <= f(p.z) + r; z++) {
      const list = set.grid.get(set.key(x, y, z)); if (!list) continue;
      for (const t of list) if (!seen.has(t)) { seen.add(t); tri(t); }
    }
    if (bi < 0 || best > reach * reach) return { d: reach, bone: '' };
  }
  if (bi < 0) return { d: Infinity, bone: '' };
  return { d: sign * Math.sqrt(best), bone: set.B[bi] };
}
// How far each part stands out of the skin where the last render put it, in metres: the most any of its
// vertices is outside the skin (out) and the most any is inside it (in). Head parts are measured against
// the head's skin, wristbands against the hand and forearm. Parts are fixed to one bone, so this must not
// change with pose.
function partReport() {
  const out = {}, v = new THREE.Vector3();
  const headSet = skinSet((k, i) => k.isHead[i] === 1), wristSet = skinSet((k, i) => /Hand$|ForeArm$/.test(k.names[k.dom[i]]));
  for (const [part, list] of Object.entries(partPoints())) {
    let hi = -Infinity, lo = Infinity;
    for (const p of list) { const { d } = skinDistance(v.fromArray(p), part === 'wristbands' ? wristSet : headSet); hi = Math.max(hi, d); lo = Math.min(lo, d); }
    out[part] = { out: Math.round(hi * 1000) / 1000, in: Math.round(lo * 1000) / 1000 };
  }
  return out;
}
// Every vertex of the skin where the last staging put it, as [x, y, z, bone], for the rig test.
function skinPoints() {
  const out = [];
  for (const k of skins) { const P = k.out.attributes.position.array; for (let i = 0; i < k.n; i++) out.push([P[i * 3], P[i * 3 + 1], P[i * 3 + 2], k.names[k.dom[i]]]); }
  return out;
}
// How far the props go into the body where J and hands put it, for the body test (test/body3d.js), in
// two measures like the body checks' own (sim/check.js): `depth`, the deepest any prop's surface is inside
// the skin of the body and of the other arm, and `grip`, the deepest a prop is inside the arm of the hand
// that holds it, each with the bone of the skin it is under and the item. The hands are left out (the
// fingers wrap round what is held), and so is the forearm within 13 cm of the wrist, where a held stick
// may lie. A cord or tether (role link) is not measured against the arm it leaves from: the hand model
// cannot steer a poi's plane, and the checks let the tether cross that arm (docs/demo-engine.md).
function probe(J, hands, items, opts = {}) {
  stage(J, hands, items);
  const wrist = { L: wpos(bone.LeftHand), R: wpos(bone.RightHand) };
  const keep = () => (k, i) => {
    const n = k.names[k.dom[i]];
    if (/Hand/.test(n)) return false;
    if (/ForeArm$/.test(n)) { const P = k.out.attributes.position.array, w = wrist[n[0]]; return (P[i * 3] - w.x) ** 2 + (P[i * 3 + 1] - w.y) ** 2 + (P[i * 3 + 2] - w.z) ** 2 > 0.13 * 0.13; }
    return true;
  };
  // One set of the skin per frame; a prop a hand holds is measured against the skin without that arm
  // (depth) and against that arm alone (grip). With opts.fingers (a clip turned by finger work) nothing of
  // a prop is measured against its own arm.
  const set = skinSet(keep()), code = side => side === 'L' ? 1 : 2;
  const worst = { depth: 0, bone: '', item: -1, at: null, grip: 0, gripBone: '', gripItem: -1, gripAt: null };
  const p = new THREE.Vector3(), test = (rad, item, arm, key) => {
    const { d, bone: b } = skinDistance(p, set, 0.08, arm), depth = rad - d;
    if (depth > worst[key]) { worst[key] = Math.round(depth * 1000) / 1000; worst[key === 'grip' ? 'gripBone' : 'bone'] = b; worst[key === 'grip' ? 'gripItem' : 'item'] = item; worst[key === 'grip' ? 'gripAt' : 'at'] = [p.x, p.y, p.z].map(x => Math.round(x * 1000) / 1000); }
  };
  items.forEach((it, j) => {
    const own = it.hand ? [{ not: code(it.hand) }, it.role === 'link' || opts.fingers ? null : { only: code(it.hand) }] : [null, null];
    const at = rad => { test(rad, j, own[0], 'depth'); if (own[1]) test(rad, j, own[1], 'grip'); };
    if (it.c) { p.copy(V(it.c)); at(it.r); return; }
    for (let i = 0; i + 1 < it.pts.length; i++) {
      const a = V(it.pts[i]), b = V(it.pts[i + 1]), n = Math.max(1, Math.ceil(a.distanceTo(b) / 0.01));
      for (let t = 0; t <= n; t++) { p.lerpVectors(a, b, t / n); at(it.w / 2); }
    }
  });
  return worst;
}

window.Figure3D = Object.assign(F, { load, render, stage, stats: () => ({ ...count }), setAvatar, setBody, partPoints, partReport, probe, skinPoints, FRAMES: Object.keys(FRAMES), avatar: () => avatar, body: () => body, debug: () => ({ gl2: renderer.capabilities.isWebGL2, maxTex: renderer.capabilities.maxVertexTextures, info: JSON.stringify(renderer.info.render), meshes: (() => { const out = []; model.traverse(o => { if (o.isMesh) out.push([o.name, o.isSkinnedMesh, o.visible, o.material.type, o.material.opacity, o.material.transparent, o.material.color.getHexString(), o.geometry.attributes.position.count, o.parent && o.parent.name, o.scale.x, !!o.skeleton && o.skeleton.bones.length]); }); return out; })(), scale: model.scale.x, hips: wpos(bone.Hips).toArray(), head: wpos(bone.Head).toArray(), lhand: wpos(bone.LeftHand).toArray(), box: new THREE.Box3().setFromObject(model).getSize(new THREE.Vector3()).toArray() }) });
