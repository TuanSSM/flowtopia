// The 3D figure. A rigged human model (any model with a Mixamo skeleton) is posed from the
// same joints the 2D figure uses, and drawn with its props by three.js into an off-screen
// canvas, which web/demo.js copies into the demo's own canvas. If WebGL or the model is
// missing, the 2D figure is used instead.
import * as THREE from 'three';
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js';

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
    model.traverse(o => { if (o.isMesh && o.material) { const joint = /joint/i.test(o.material.name + o.name); o.material = new THREE.MeshStandardMaterial({ color: joint ? 0x596070 : 0xb8bdc9, roughness: 0.62, metalness: 0.04 }); } });
    const names = ['Hips', 'Spine', 'Spine1', 'Spine2', 'Neck', 'Head'];
    for (const s of ['Left', 'Right']) names.push(s + 'Shoulder', s + 'Arm', s + 'ForeArm', s + 'Hand', s + 'UpLeg', s + 'Leg', s + 'Foot', s + 'ToeBase');
    for (const n of names) { bone[n] = find(model, n); if (!bone[n]) throw new Error('the model has no bone called ' + n); }
    // Scale the model so its shoulders are at the figure's shoulder height.
    model.updateMatrixWorld(true);
    model.scale.multiplyScalar(1.43 / wpos(bone.LeftArm).y);
    model.updateMatrixWorld(true);
    const chain = { LeftArm: 'LeftForeArm', LeftForeArm: 'LeftHand', RightArm: 'RightForeArm', RightForeArm: 'RightHand', LeftUpLeg: 'LeftLeg', LeftLeg: 'LeftFoot', LeftFoot: 'LeftToeBase', RightUpLeg: 'RightLeg', RightLeg: 'RightFoot', RightFoot: 'RightToeBase' };
    for (const n of names) rest[n] = { q: wquat(bone[n]), dir: chain[n] ? wpos(bone[chain[n]]).sub(wpos(bone[n])).normalize() : null, local: bone[n].quaternion.clone(), pos: bone[n].position.clone() };
    rest.hipOff = wpos(bone.Hips).y - wpos(bone.LeftUpLeg).y; // the hips bone sits a little above the hip joints
    // Hands: the rest frame of each, and how each finger joint curls toward the palm.
    for (const [s, S] of [['L', 'Left'], ['R', 'Right']]) {
      const hand = bone[S + 'Hand'], at = n => wpos(find(model, S + 'Hand' + n));
      const w = at('Middle1').sub(wpos(hand)), x = at('Index1').sub(at('Pinky1')), fr = handFrame(x, w, s);
      handRest[s] = { q: fr.q, bq: wquat(hand), grip: at('Middle1').sub(wpos(hand)).length() };
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
      skins.push({ sm: o, n, bind, bnor, idx: g.attributes.skinIndex.array, wt: g.attributes.skinWeight.array, out, norm: g.attributes.skinWeight.normalized });
    });
    F.ready = true;
  } catch (e) { F.failed = true; console.warn('3D figure not available:', e.message || e); }
  return F.ready;
}

// Put a bone at a world position with a world rotation.
function place(b, pos, q) {
  b.parent.updateWorldMatrix(true, false);
  if (pos) b.position.copy(b.parent.worldToLocal(pos.clone()));
  b.quaternion.copy(wquat(b.parent).invert().multiply(q));
}
// Point a bone from one joint to the next, turning it the short way from its rest direction.
function aim(n, from, to) {
  const d = to.clone().sub(from); if (d.lengthSq() < 1e-10) return;
  place(bone[n], from, new THREE.Quaternion().setFromUnitVectors(rest[n].dir, d.normalize()).multiply(rest[n].q));
}

function poseBody(J, hands) {
  const left = V(J.left), up = V(J.up), fwd = new THREE.Vector3().crossVectors(left, up);
  for (const n in rest) if (bone[n]) { bone[n].quaternion.copy(rest[n].local); bone[n].position.copy(rest[n].pos); }
  place(bone.Hips, V(J.pelvis).add(up.clone().multiplyScalar(rest.hipOff)), frameQuat(left, up, fwd).multiply(rest.Hips.q));
  for (const [s, S] of [['L', 'Left'], ['R', 'Right']]) {
    const h = hands[s], fr = handFrame(V(h.x), V(h.w), s), k = handRest[s].grip / 0.085;
    // The wrist sits behind the grip and under the stick, as in the drawn hand.
    const wrist = V(h.o).add(fr.W.clone().multiplyScalar(-0.085 * k)).add(fr.N.clone().multiplyScalar(-(h.r + 0.018)));
    aim(S + 'UpLeg', V(J['hip' + s]), V(J['knee' + s])); aim(S + 'Leg', V(J['knee' + s]), V(J['f' + s]));
    aim(S + 'Foot', V(J['f' + s]), V(J['f' + s]).add(V(J['t' + s]).multiplyScalar(0.14)).add(new THREE.Vector3(0, -0.035, 0)));
    aim(S + 'Arm', V(J['sh' + s]), V(J['el' + s])); aim(S + 'ForeArm', V(J['el' + s]), wrist);
    place(bone[S + 'Hand'], wrist, fr.q.clone().multiply(handRest[s].q.clone().invert()).multiply(handRest[s].bq));
    const closed = h.grip !== 'open';
    for (const f of fingers[s]) {
      const a = f.thumb ? (closed ? [0.35, 0.45, 0.6] : [0.05, 0.05, 0.05])[f.j - 1] : (closed ? [1.25, 1.5, 1.0] : [0.16, 0.16, 0.1])[f.j - 1];
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
}

// W: size in pixels. J: joints. hands: { L, R } each { o, x, w, grip, r }. items: props as tubes and balls.
// cam: { right, up, toward (unit vectors), cx, cy (centre, along right and up), half (half the width shown, metres) }.
function render(W, J, hands, items, cam) {
  if (renderer.domElement.width !== W) renderer.setSize(W, W, false);
  if (!F.nopose) poseBody(J, hands);
  skin(); props(items);
  const R = V(cam.right), U = V(cam.up), T = V(cam.toward), c = R.clone().multiplyScalar(cam.cx).add(U.clone().multiplyScalar(cam.cy));
  camera.left = -cam.half; camera.right = cam.half; camera.top = cam.half; camera.bottom = -cam.half; camera.updateProjectionMatrix();
  camera.position.copy(c.clone().add(T.clone().multiplyScalar(20))); camera.up.copy(U); camera.lookAt(c);
  sun.position.copy(c.clone().add(T.clone().multiplyScalar(4)).add(U.clone().multiplyScalar(3)).add(R.clone().multiplyScalar(-2.5))); sun.target.position.copy(c);
  renderer.render(scene, camera);
  return renderer.domElement;
}

window.Figure3D = Object.assign(F, { load, render, debug: () => ({ gl2: renderer.capabilities.isWebGL2, maxTex: renderer.capabilities.maxVertexTextures, info: JSON.stringify(renderer.info.render), meshes: (() => { const out = []; model.traverse(o => { if (o.isMesh) out.push([o.name, o.isSkinnedMesh, o.visible, o.material.type, o.material.opacity, o.material.transparent, o.material.color.getHexString(), o.geometry.attributes.position.count, o.parent && o.parent.name, o.scale.x, !!o.skeleton && o.skeleton.bones.length]); }); return out; })(), scale: model.scale.x, hips: wpos(bone.Hips).toArray(), head: wpos(bone.Head).toArray(), lhand: wpos(bone.LeftHand).toArray(), box: new THREE.Box3().setFromObject(model).getSize(new THREE.Vector3()).toArray() }) });
