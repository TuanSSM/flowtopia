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
const cyl = [], ball = [], mats = new Map(), skins = [];

function find(root, name) { let hit = null; root.traverse(o => { if (!hit && o.isBone && o.name.replace(/^mixamorig:?/, '') === name) hit = o; }); return hit; }
const wpos = o => o.getWorldPosition(new THREE.Vector3()), wquat = o => o.getWorldQuaternion(new THREE.Quaternion());
// A rotation taking the frame (x, n, w) of the rest pose to the same frame now.
function frameQuat(x, n, w) { return new THREE.Quaternion().setFromRotationMatrix(new THREE.Matrix4().makeBasis(x, n, w)); }
function handFrame(x, w, side) {
  const X = x.clone().normalize(), W = w.clone().sub(X.clone().multiplyScalar(w.dot(X))).normalize(), N = new THREE.Vector3().crossVectors(X, W).multiplyScalar(side === 'L' ? -1 : 1);
  // A right-handed set for the matrix: (X, N, W) for the right hand, (X, -N, W) mirrored for the left.
  return { X, N, W, q: frameQuat(X, new THREE.Vector3().crossVectors(W, X), W) };
}

async function load(url) {
  try {
    renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, preserveDrawingBuffer: true });
    renderer.setClearColor(0x000000, 0);
    scene = new THREE.Scene();
    camera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0.1, 50);
    scene.add(new THREE.HemisphereLight(0xffffff, 0x3a3f4a, 1.5));
    sun = new THREE.DirectionalLight(0xffffff, 2.2); scene.add(sun); scene.add(sun.target);
    const gltf = await new GLTFLoader().loadAsync(url);
    model = gltf.scene; scene.add(model);
    // A neutral matte finish: light on the body, darker at the joints, so the pose reads on dark and light pages.
    model.traverse(o => { if (o.isMesh && o.material) { const joint = /joint/i.test(o.material.name + o.name); o.material = new THREE.MeshStandardMaterial({ color: joint ? 0x596070 : 0xb8bdc9, roughness: 0.62, metalness: 0.04, side: THREE.DoubleSide }); } });
    renderer.localClippingEnabled = true;
    const names = ['Hips', 'Spine', 'Spine1', 'Spine2', 'Neck', 'Head'];
    for (const s of ['Left', 'Right']) names.push(s + 'Shoulder', s + 'Arm', s + 'ForeArm', s + 'Hand', s + 'UpLeg', s + 'Leg', s + 'Foot', s + 'ToeBase');
    for (const n of names) { bone[n] = find(model, n); if (!bone[n]) throw new Error('the model has no bone called ' + n); }
    // Scale the model so its shoulders are at the figure's shoulder height.
    model.updateMatrixWorld(true);
    model.scale.multiplyScalar(1.43 / wpos(bone.LeftArm).y);
    model.updateMatrixWorld(true);
    const chain = { LeftShoulder: 'LeftArm', RightShoulder: 'RightArm', LeftArm: 'LeftForeArm', LeftForeArm: 'LeftHand', RightArm: 'RightForeArm', RightForeArm: 'RightHand', LeftUpLeg: 'LeftLeg', LeftLeg: 'LeftFoot', LeftFoot: 'LeftToeBase', RightUpLeg: 'RightLeg', RightLeg: 'RightFoot', RightFoot: 'RightToeBase' };
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
      const region = new Uint8Array(n), wts = g.attributes.skinWeight.array, ids = g.attributes.skinIndex.array, names = o.skeleton.bones.map(b => b.name.replace(/^mixamorig:?/, ''));
      for (let i = 0; i < n; i++) { let bj = 0; for (let j = 1; j < 4; j++) if (wts[i * 4 + j] > wts[i * 4 + bj]) bj = j; region[i] = regionOf(names[ids[i * 4 + bj]], bind[i * 3 + 1]); }
      out.setAttribute('color', new THREE.BufferAttribute(new Float32Array(n * 3).fill(1), 3));
      skins.push({ sm: o, n, bind, bind0: bind.slice(), bnor, idx: g.attributes.skinIndex.array, wt: g.attributes.skinWeight.array, out, norm: g.attributes.skinWeight.normalized, mesh, neutral: o.material, joint: /joint/i.test(o.material.name + o.name), region });
    });
    measureHead();
    F.ready = true;
  } catch (e) { F.failed = true; F.reason = String(e.message || e); console.warn('3D figure not available:', F.reason); }
  return F.ready;
}

// Put a bone at a world position with a world rotation.
function place(b, pos, q) {
  b.parent.updateWorldMatrix(true, false);
  if (pos) b.position.copy(b.parent.worldToLocal(pos.clone()));
  b.quaternion.copy(wquat(b.parent).invert().multiply(q));
}
// Point a bone from one joint to the next, turning it the short way from its rest direction.
// With `flex` and `want` it is then rolled about its own axis so that the direction it bends
// in rest (flex) faces `want`: the elbow toward the forearm, the knee toward the shin, the
// forearm's palm side toward the palm. `keep` leaves the bone where its parent put it.
function aim(n, from, to, flex, want, keep) {
  const d = to.clone().sub(from); if (d.lengthSq() < 1e-10) return;
  d.normalize();
  let q = new THREE.Quaternion().setFromUnitVectors(rest[n].dir, d);
  if (flex && want) {
    const w = want.clone().sub(d.clone().multiplyScalar(want.dot(d))), f = flex.clone().applyQuaternion(q);
    f.sub(d.clone().multiplyScalar(f.dot(d)));
    if (w.lengthSq() > 1e-8 && f.lengthSq() > 1e-8) {
      w.normalize(); f.normalize();
      q = new THREE.Quaternion().setFromAxisAngle(d, Math.atan2(new THREE.Vector3().crossVectors(f, w).dot(d), f.dot(w))).multiply(q);
    }
  }
  place(bone[n], keep ? null : from, q.multiply(rest[n].q));
}
// The part of `v` at right angles to the unit vector `d`, padded with `fallback` as it gets short,
// so a straight limb still knows which way it would bend.
function across(v, d, fallback) {
  const w = v.clone().sub(d.clone().multiplyScalar(v.dot(d))), L = w.length();
  return L > 0.06 ? w : w.add(fallback.clone().multiplyScalar(0.06 - L));
}

function poseBody(J, hands) {
  const left = V(J.left), up = V(J.up), fwd = new THREE.Vector3().crossVectors(left, up), UP = new THREE.Vector3(0, 1, 0);
  for (const n in rest) if (bone[n]) { bone[n].quaternion.copy(rest[n].local); bone[n].position.copy(rest[n].pos); }
  place(bone.Hips, V(J.pelvis).add(up.clone().multiplyScalar(rest.hipOff)), frameQuat(left, up, fwd).multiply(rest.Hips.q));
  // The head stays nearer upright than the trunk, as a person's does when they lean.
  const level = new THREE.Quaternion().setFromUnitVectors(up, up.clone().multiplyScalar(0.4).add(UP.clone().multiplyScalar(0.6)).normalize()), half = new THREE.Quaternion().slerp(level, 0.5);
  place(bone.Neck, null, half.clone().multiply(wquat(bone.Neck)));
  place(bone.Head, null, half.clone().multiply(wquat(bone.Head)));
  for (const [s, S] of [['L', 'Left'], ['R', 'Right']]) {
    const h = hands[s], fr = handFrame(V(h.x), V(h.w), s), k = handRest[s].grip / 0.085;
    const sh = V(J['sh' + s]), el = V(J['el' + s]), hip = V(J['hip' + s]), knee = V(J['knee' + s]), foot = V(J['f' + s]), toe = V(J['t' + s]);
    // The wrist sits behind the grip and under the stick, as in the drawn hand.
    const wrist = V(h.o).add(fr.W.clone().multiplyScalar(-0.085 * k)).add(fr.N.clone().multiplyScalar(-(h.r + 0.01)));
    // Legs: the knee bends backward and points where the toes point; the shin's front faces the toes.
    const thigh = knee.clone().sub(hip).normalize(), shin = foot.clone().sub(knee).normalize();
    aim(S + 'UpLeg', hip, knee, rest.fwd.clone().negate(), across(shin, thigh, toe.clone().negate()));
    aim(S + 'Leg', knee, foot, rest.fwd, across(toe, shin, fwd));
    aim(S + 'Foot', foot, foot.clone().add(toe.clone().multiplyScalar(0.14)).add(new THREE.Vector3(0, -0.035, 0)));
    // Shoulder: the collarbone lifts as the hand goes up, and the arm starts from its tip.
    const lift = Math.max(0, Math.min(1, (V(h.o).y - sh.y) / 0.45)) * 0.1;
    aim(S + 'Shoulder', wpos(bone[S + 'Shoulder']), sh.clone().add(up.clone().multiplyScalar(lift)), null, null, true);
    const start = wpos(bone[S + 'Arm']);
    // Upper arm: rolled so the elbow is a hinge that bends toward the forearm; when the arm is
    // straight the crease faces forward and a little in, as at rest.
    const upper = el.clone().sub(start).normalize(), fore = wrist.clone().sub(el).normalize();
    aim(S + 'Arm', start, el, rest.fwd, across(fore, upper, fwd.clone().add(left.clone().multiplyScalar(s === 'L' ? -0.5 : 0.5))), true);
    // Forearm: turned (pronation or supination) so its palm side follows the hand, within what a forearm can do.
    const q0 = new THREE.Quaternion().setFromUnitVectors(rest[S + 'ForeArm'].dir, fore), n0 = handRest[s].N.clone().applyQuaternion(q0);
    const want = across(fr.N, fore, n0), have = n0.clone().sub(fore.clone().multiplyScalar(n0.dot(fore))).normalize(); want.normalize();
    const turn = Math.max(-1.75, Math.min(1.75, Math.atan2(new THREE.Vector3().crossVectors(have, want).dot(fore), have.dot(want))));
    place(bone[S + 'ForeArm'], el, new THREE.Quaternion().setFromAxisAngle(fore, turn).multiply(q0).multiply(rest[S + 'ForeArm'].q));
    place(bone[S + 'Hand'], wrist, fr.q.clone().multiply(handRest[s].q.clone().invert()).multiply(handRest[s].bq));
    // Fingers close round what is held, less tightly round something thick.
    const closed = h.grip !== 'open', tight = Math.min(1.15, Math.sqrt(0.012 / Math.max(h.r || 0.012, 0.006)));
    for (const f of fingers[s]) {
      const a = f.thumb ? (closed ? [0.35, 0.45, 0.6] : [0.05, 0.05, 0.05])[f.j - 1] : (closed ? [1.35, 1.6, 1.1].map(x => x * tight) : [0.16, 0.16, 0.1])[f.j - 1];
      f.b.quaternion.copy(f.local).multiply(new THREE.Quaternion().setFromAxisAngle(f.axis, a));
    }
  }
}

const mat = color => { if (!mats.has(color)) mats.set(color, new THREE.MeshStandardMaterial({ color, roughness: 0.45, metalness: 0.05 })); return mats.get(color); };
const UPY = new THREE.Vector3(0, 1, 0);
function props(items) {
  let nc = 0, nb = 0;
  for (const it of items) {
    if (it.c) {
      const m = ball[nb] || (ball[nb] = scene.add(new THREE.Mesh(new THREE.SphereGeometry(1, 24, 16))) && scene.children[scene.children.length - 1]); nb++;
      m.visible = true; m.material = mat(it.color); m.position.copy(V(it.c)); m.scale.setScalar(it.r);
    } else for (let i = 0; i < it.pts.length; i++) {
      // A tube from point to point, with a rounded joint at each point.
      const e = ball[nb] || (ball[nb] = scene.add(new THREE.Mesh(new THREE.SphereGeometry(1, 24, 16))) && scene.children[scene.children.length - 1]); nb++;
      e.visible = true; e.material = mat(it.color); e.position.copy(V(it.pts[i])); e.scale.setScalar(it.w / 2);
      if (i === it.pts.length - 1) break;
      const a = V(it.pts[i]), b = V(it.pts[i + 1]), d = b.clone().sub(a), L = d.length(); if (L < 1e-6) continue;
      const m = cyl[nc] || (cyl[nc] = scene.add(new THREE.Mesh(new THREE.CylinderGeometry(1, 1, 1, 20, 1, true))) && scene.children[scene.children.length - 1]); nc++;
      m.visible = true; m.material = mat(it.color); m.position.copy(a.add(b).multiplyScalar(0.5)); m.quaternion.setFromUnitVectors(UPY, d.normalize()); m.scale.set(it.w / 2, L, it.w / 2);
    }
  }
  for (let i = nc; i < cyl.length; i++) cyl[i].visible = false;
  for (let i = nb; i < ball.length; i++) ball[i].visible = false;
}

// Move every vertex with the bones it is weighted to.
function skin() {
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
      const s = sum || 1, l = Math.hypot(nx, ny, nz) || 1;
      P[i * 3] = px / s; P[i * 3 + 1] = py / s; P[i * 3 + 2] = pz / s; N[i * 3] = nx / l; N[i * 3 + 1] = ny / l; N[i * 3 + 2] = nz / l;
    }
    k.out.attributes.position.needsUpdate = true; k.out.attributes.normal.needsUpdate = true;
  }
  // Avatar parts follow the bone they are fixed to.
  for (const p of parts) { p.g.matrix.multiplyMatrices(bone[p.bone].matrixWorld, p.offset); p.g.matrixWorldNeedsUpdate = true; }
}

// W: size in pixels. J: joints. hands: { L, R } each { o, x, w, grip, r }. items: props as tubes and balls.
// cam: { right, up, toward (unit vectors), cx, cy (centre, along right and up), half (half the width shown, metres),
// cut (optional: metres behind the centre beyond which nothing is drawn, for a close-up of a hand) }.
function render(W, J, hands, items, cam) {
  if (renderer.domElement.width !== W) renderer.setSize(W, W, false);
  if (!F.nopose) poseBody(J, hands);
  skin(); props(items);
  const R = V(cam.right), U = V(cam.up), T = V(cam.toward), c = R.clone().multiplyScalar(cam.cx).add(U.clone().multiplyScalar(cam.cy));
  camera.left = -cam.half; camera.right = cam.half; camera.top = cam.half; camera.bottom = -cam.half; camera.updateProjectionMatrix();
  camera.position.copy(c.clone().add(T.clone().multiplyScalar(20))); camera.up.copy(U); camera.lookAt(c);
  renderer.clippingPlanes = cam.cut ? [new THREE.Plane(T.clone(), cam.cut - c.dot(T))] : [];
  sun.position.copy(c.clone().add(T.clone().multiplyScalar(4)).add(U.clone().multiplyScalar(3)).add(R.clone().multiplyScalar(-2.5))); sun.target.position.copy(c);
  renderer.render(scene, camera);
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
// Where the head is in the rest pose, measured from the vertices the head bone moves. Bind positions
// are taken to the rest-pose world (where parts are built) by the skinning matrix of the rest pose.
function measureHead() {
  const c = new THREE.Vector3(), up = new THREE.Vector3(0, 1, 0), neckY = wpos(bone.Head).y, v = new THREE.Vector3();
  let n = 0, top = -Infinity;
  for (const k of skins) {
    const sk = k.sm.skeleton, M = new THREE.Matrix4().multiplyMatrices(sk.bones[0].matrixWorld, sk.boneInverses[0]);
    for (let i = 0; i < k.n; i++) {
      v.set(k.bind[i * 3], k.bind[i * 3 + 1], k.bind[i * 3 + 2]).applyMatrix4(M);
      // Boots and high-tops: shin vertices near the ankle take the shoe colour (high-tops up to 6 cm, boots 13 cm).
      if (k.region[i] === REGION.shin) { const h = v.y - rest.ankle; if (h < 0.06) k.region[i] = REGION.ankle; else if (h < 0.13) k.region[i] = REGION.boot; }
      if (k.joint || k.region[i] !== REGION.skin || v.y < neckY) continue; // the head is the skin above the neck
      c.add(v); n++; top = Math.max(top, v.y);
    }
  }
  const centre = n ? c.multiplyScalar(1 / n) : wpos(bone.Head);
  const r = Math.max(0.06, (top - centre.y) * 0.98);
  head = { centre, r, up, fwd: rest.fwd.clone(), side: new THREE.Vector3().crossVectors(up, rest.fwd).normalize() };
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
// Place geometry at a point on the head, facing outward along `dir`.
function onHead(geo, mat, at, quat) {
  const m = new THREE.Mesh(geo, mat);
  m.position.copy(at); if (quat) m.quaternion.copy(quat);
  m.updateMatrix();
  return m;
}
function headPoint(fwd, side, up, scale = 1) {
  const h = head;
  return h.centre.clone().add(h.fwd.clone().multiplyScalar(fwd * h.r * scale)).add(h.side.clone().multiplyScalar(side * h.r * scale)).add(h.up.clone().multiplyScalar(up * h.r * scale));
}
const faceQuat = () => new THREE.Quaternion().setFromRotationMatrix(new THREE.Matrix4().makeBasis(head.side, head.up, head.fwd));
function buildHead(cfg) {
  // Each piece is tagged with its part, so the parts test can measure each one.
  let tag = 'hair';
  const H = new THREE.Group(), r = head.r, add = m => { m.userData.part = tag; H.add(m); }, q = faceQuat();
  const hairM = lambert(cfg.hairColor), dark = lambert('#202124'), white = lambert('#f2f1ee'), lip = lambert('#7a3a3a');
  const cap = (scale, phi = 0.55) => onHead(new THREE.SphereGeometry(r * scale, 28, 16, 0, Math.PI * 2, 0, Math.PI * phi), hairM, head.centre.clone().add(head.up.clone().multiplyScalar(r * 0.02)).add(head.fwd.clone().multiplyScalar(-r * 0.06)), new THREE.Quaternion());
  const hat = cfg.hat && cfg.hat !== 'none';
  switch (cfg.hair) {
    case 'buzz': if (!hat) add(cap(1.02, 0.5)); break;
    case 'short': if (!hat) add(cap(1.07, 0.56)); break;
    case 'long': if (!hat) add(cap(1.07, 0.56)); { const back = onHead(new THREE.CylinderGeometry(r * 1.02, r * 1.1, r * 1.5, 24, 1, true, Math.PI * 0.5, Math.PI), hairM, head.centre.clone().add(head.up.clone().multiplyScalar(-r * 0.55)).add(head.fwd.clone().multiplyScalar(-r * 0.12))); back.material = hairM.clone(); back.material.side = THREE.DoubleSide; add(back); } break;
    case 'ponytail': if (!hat) add(cap(1.06, 0.55)); for (let i = 0; i < 4; i++) add(onHead(new THREE.SphereGeometry(r * (0.24 - i * 0.03), 12, 10), hairM, headPoint(-1.05 - i * 0.08, 0, 0.25 - i * 0.32))); break;
    case 'bun': if (!hat) add(cap(1.05, 0.55)); add(onHead(new THREE.SphereGeometry(r * 0.32, 16, 12), hairM, headPoint(-0.55, 0, 0.95))); break;
    case 'curly': if (!hat) { const ico = new THREE.IcosahedronGeometry(r * 1.02, 2).attributes.position; const seen = new Set(); for (let i = 0; i < ico.count; i++) { const v = new THREE.Vector3().fromBufferAttribute(ico, i); const k = v.toArray().map(x => x.toFixed(3)).join(); if (seen.has(k)) continue; seen.add(k); const up = v.clone().normalize().dot(head.up), fw = v.clone().normalize().dot(head.fwd); if (up < 0.05 || (fw > 0.55 && up < 0.6)) continue; add(onHead(new THREE.SphereGeometry(r * 0.17, 8, 6), hairM, head.centre.clone().add(v))); } } break;
  }
  // Eyes, brows and mouth sit on the front of the head.
  tag = 'eyes';
  const eyeR = { round: 0.11, wide: 0.14, narrow: 0.11, sleepy: 0.11 }[cfg.eyes] || 0.11;
  for (const sd of [-1, 1]) {
    const at = headPoint(0.9, sd * 0.33, 0.1);
    const eye = onHead(new THREE.SphereGeometry(r * eyeR, 14, 10), white, at, q);
    if (cfg.eyes === 'narrow') eye.scale.set(1, 0.5, 1); if (cfg.eyes === 'sleepy') eye.scale.set(1, 0.6, 1);
    eye.updateMatrix(); add(eye);
    add(onHead(new THREE.SphereGeometry(r * eyeR * 0.55, 10, 8), dark, at.clone().add(head.fwd.clone().multiplyScalar(r * eyeR * 0.62)), q));
    if (cfg.eyes === 'sleepy') add(onHead(new THREE.SphereGeometry(r * eyeR * 1.06, 14, 8, 0, Math.PI * 2, 0, Math.PI * 0.45), lambert(cfg.skin), at.clone(), q));
    if (cfg.brows !== 'none') {
      tag = 'brows';
      const h = cfg.brows === 'thick' ? 0.075 : 0.042, brow = onHead(new THREE.BoxGeometry(r * 0.3, r * h, r * 0.06), lambert(cfg.hairColor), headPoint(0.92, sd * 0.33, 0.34), q.clone().multiply(new THREE.Quaternion().setFromAxisAngle(new THREE.Vector3(0, 0, 1), cfg.brows === 'arched' ? sd * -0.28 : cfg.brows === 'angled' ? sd * 0.3 : 0)));
      add(brow); tag = 'eyes';
    }
  }
  tag = 'mouth';
  const mouthAt = headPoint(0.93, 0, -0.38);
  if (cfg.mouth === 'smile' || cfg.mouth === 'grin') {
    const arc = onHead(new THREE.TorusGeometry(r * (cfg.mouth === 'grin' ? 0.24 : 0.17), r * 0.03, 6, 18, Math.PI), lip, mouthAt.clone().add(head.up.clone().multiplyScalar(r * 0.1)), q.clone().multiply(new THREE.Quaternion().setFromAxisAngle(new THREE.Vector3(0, 0, 1), Math.PI)));
    add(arc);
    if (cfg.mouth === 'grin') add(onHead(new THREE.BoxGeometry(r * 0.3, r * 0.07, r * 0.04), white, mouthAt.clone().add(head.up.clone().multiplyScalar(-r * 0.04)), q));
  } else if (cfg.mouth === 'open') add(onHead(new THREE.SphereGeometry(r * 0.12, 12, 10), lip, mouthAt, q));
  else add(onHead(new THREE.BoxGeometry(r * 0.26, r * 0.035, r * 0.04), lip, mouthAt, q));
  // Hats and glasses.
  tag = 'hat';
  if (hat) {
    const hm = lambert(cfg.hatColor), top = head.centre.clone().add(head.up.clone().multiplyScalar(r * 0.12));
    if (cfg.hat === 'cap') { add(onHead(new THREE.SphereGeometry(r * 1.1, 28, 14, 0, Math.PI * 2, 0, Math.PI * 0.5), hm, top)); add(onHead(new THREE.CylinderGeometry(r * 0.62, r * 0.62, r * 0.05, 24, 1, false, -Math.PI * 0.5, Math.PI), hm, top.clone().add(head.fwd.clone().multiplyScalar(r * 0.75)).add(head.up.clone().multiplyScalar(r * 0.02)), new THREE.Quaternion().setFromUnitVectors(new THREE.Vector3(0, 0, 1), head.fwd))); }
    if (cfg.hat === 'beanie') { const b = onHead(new THREE.SphereGeometry(r * 1.09, 28, 14, 0, Math.PI * 2, 0, Math.PI * 0.55), hm, top); b.scale.set(1, 1.18, 1); b.updateMatrix(); add(b); add(onHead(new THREE.TorusGeometry(r * 1.05, r * 0.08, 8, 28), hm, top.clone().add(head.up.clone().multiplyScalar(r * 0.05)), new THREE.Quaternion().setFromUnitVectors(new THREE.Vector3(0, 0, 1), head.up))); }
    if (cfg.hat === 'bucket') { add(onHead(new THREE.CylinderGeometry(r * 0.88, r * 1.05, r * 0.7, 24, 1), hm, top.clone().add(head.up.clone().multiplyScalar(r * 0.42)))); add(onHead(new THREE.CylinderGeometry(r * 1.55, r * 1.55, r * 0.05, 28), hm, top.clone().add(head.up.clone().multiplyScalar(r * 0.08)))); }
  }
  tag = 'glasses';
  if (cfg.glasses && cfg.glasses !== 'none') {
    const gm = lambert('#1b1c1f');
    for (const sd of [-1, 1]) {
      const at = headPoint(1.0, sd * 0.33, 0.1);
      if (cfg.glasses === 'round') add(onHead(new THREE.TorusGeometry(r * 0.17, r * 0.022, 6, 20), gm, at, q));
      else for (const [w, h, dx, dy] of [[0.4, 0.035, 0, 0.14], [0.4, 0.035, 0, -0.14], [0.035, 0.3, 0.19, 0], [0.035, 0.3, -0.19, 0]]) add(onHead(new THREE.BoxGeometry(r * w, r * h, r * 0.03), gm, at.clone().add(head.side.clone().multiplyScalar(r * dx)).add(head.up.clone().multiplyScalar(r * dy)), q));
    }
    add(onHead(new THREE.CylinderGeometry(r * 0.02, r * 0.02, r * 0.26, 6), gm, headPoint(1.02, 0, 0.12), q.clone().multiply(new THREE.Quaternion().setFromAxisAngle(new THREE.Vector3(0, 0, 1), Math.PI / 2))));
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
  parts = []; avatar = cfg || null;
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

// Body presets (web/demo.js setBody): sc scales lengths; bd scales widths. Bones are never
// scaled unevenly, since posing reads each parent's rotation: instead the rest-pose vertices of the
// trunk spread out from the spine and those of the limbs from their bone, and the collarbones grow so
// the arms start where the rig's shoulders are.
let body = { sc: 1, bd: 1 };
function setBody(sc = 1, bd0 = 1) {
  if (!F.ready || (body.sc === sc && body.bd === bd0)) return;
  body = { sc, bd: bd0 };
  model.scale.setScalar(rest.scale * sc);
  // The model scales evenly with height; widths follow the build alone, as in the rig, so they are
  // narrowed back by the height.
  const bd = bd0 / sc;
  for (const S of ['Left', 'Right']) {
    const r = rest[S + 'Arm'];
    if (!r.pos0) r.pos0 = r.pos.clone();
    r.pos.copy(r.pos0).multiplyScalar(1 + (bd - 1) * rest.collar);
  }
  for (const k of skins) {
    const B = k.bind, B0 = k.bind0, sk = k.sm.skeleton;
    if (!k.bonePos) {
      // Each bone's place and its child's in the bind pose, and which bones make the trunk.
      const names = sk.bones.map(b => b.name.replace(/^mixamorig:?/, ''));
      k.trunk = names.map(n => /^(Hips|Spine|Spine1|Spine2|LeftShoulder|RightShoulder)$/.test(n));
      k.limb = names.map(n => /(Arm|ForeArm|UpLeg|Leg)$/.test(n) && !/Shoulder/.test(n));
      k.bonePos = sk.boneInverses.map(m => new THREE.Vector3().setFromMatrixPosition(m.clone().invert()));
      k.childPos = sk.bones.map((b, i) => { const c = b.children.find(x => x.isBone); return c ? k.bonePos[sk.bones.indexOf(c)] || k.bonePos[i] : k.bonePos[i]; });
      let cx = 0, cz = 0, n = 0;
      for (let i = 0; i < k.n; i++) if (k.region[i] === REGION.top) { cx += B0[i * 3]; cz += B0[i * 3 + 2]; n++; }
      k.axis = n ? [cx / n, cz / n] : [0, 0];
    }
    const a = new THREE.Vector3(), b = new THREE.Vector3(), p = new THREE.Vector3(), c = new THREE.Vector3();
    for (let i = 0; i < k.n; i++) {
      let wT = 0, wL = 0, bl = -1, bw = 0;
      for (let j = 0; j < 4; j++) {
        const w = k.wt[i * 4 + j], id = k.idx[i * 4 + j]; if (!w) continue;
        if (k.trunk[id]) wT += w;
        if (k.limb[id]) { wL += w; if (w > bw) { bw = w; bl = id; } }
      }
      const x = B0[i * 3], y = B0[i * 3 + 1], z = B0[i * 3 + 2];
      if (bd === 1 || (wT === 0 && wL === 0)) { B[i * 3] = x; B[i * 3 + 1] = y; B[i * 3 + 2] = z; continue; }
      // Trunk: out from the spine's vertical line. Limbs: out from the bone's line.
      const fT = 1 + (bd - 1) * wT;
      let nx = k.axis[0] + (x - k.axis[0]) * fT, ny = y, nz = k.axis[1] + (z - k.axis[1]) * fT;
      if (bl >= 0) {
        a.copy(k.bonePos[bl]); b.copy(k.childPos[bl]); p.set(x, y, z);
        const d = b.clone().sub(a), t = d.lengthSq() > 1e-12 ? Math.max(0, Math.min(1, p.clone().sub(a).dot(d) / d.lengthSq())) : 0;
        c.copy(a).add(d.multiplyScalar(t));
        const fL = (bd - 1) * wL;
        nx += (x - c.x) * fL; ny += (y - c.y) * fL; nz += (z - c.z) * fL;
      }
      B[i * 3] = nx; B[i * 3 + 1] = ny; B[i * 3 + 2] = nz;
    }
  }
}

// Every part's vertices where the last render put them, in the rig's world (metres), for the parts test.
function partPoints() {
  const out = {}, v = new THREE.Vector3();
  for (const p of parts) p.g.traverse(o => {
    if (!o.isMesh || !o.userData.ranges) return;
    o.updateMatrixWorld(true);
    const pos = o.geometry.attributes.position, idx = o.geometry.index;
    for (const r of o.userData.ranges) {
      const list = out[r.part] || (out[r.part] = []);
      for (let i = r.from; i < r.from + r.n; i++) { v.fromBufferAttribute(pos, idx ? idx.getX(i) : i).applyMatrix4(o.matrixWorld); list.push([v.x, v.y, v.z]); }
    }
  });
  return out;
}

// How far each part reaches outside the figure's own head (head parts) or wrist (wristbands) where
// the last render put it, in metres. Parts are fixed to one bone, so this must not change with pose.
function partReport() {
  const out = {}, v = new THREE.Vector3(), s = model.scale.x / rest.scale;
  const hc = head.centre.clone().applyMatrix4(new THREE.Matrix4().multiplyMatrices(bone.Head.matrixWorld, rest.head.m.clone().invert()));
  const wrists = ['Left', 'Right'].map(S => wpos(bone[S + 'Hand']));
  for (const [part, list] of Object.entries(partPoints())) {
    let w = -Infinity;
    for (const p of list) { v.fromArray(p); w = Math.max(w, part === 'wristbands' ? Math.min(...wrists.map(c => v.distanceTo(c))) - 0.045 * s : v.distanceTo(hc) - head.r * s); }
    out[part] = Math.round(w * 1000) / 1000;
  }
  return out;
}

window.Figure3D = Object.assign(F, { load, render, setAvatar, setBody, partPoints, partReport, avatar: () => avatar, debug: () => ({ gl2: renderer.capabilities.isWebGL2, maxTex: renderer.capabilities.maxVertexTextures, info: JSON.stringify(renderer.info.render), meshes: (() => { const out = []; model.traverse(o => { if (o.isMesh) out.push([o.name, o.isSkinnedMesh, o.visible, o.material.type, o.material.opacity, o.material.transparent, o.material.color.getHexString(), o.geometry.attributes.position.count, o.parent && o.parent.name, o.scale.x, !!o.skeleton && o.skeleton.bones.length]); }); return out; })(), scale: model.scale.x, hips: wpos(bone.Hips).toArray(), head: wpos(bone.Head).toArray(), lhand: wpos(bone.LeftHand).toArray(), box: new THREE.Box3().setFromObject(model).getSize(new THREE.Vector3()).toArray() }) });
