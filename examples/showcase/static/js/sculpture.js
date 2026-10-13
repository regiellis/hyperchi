import * as THREE from "./three/three.module.js";

const host = document.getElementById("sculpture");
const hero = host.closest(".hero");
const reduce = matchMedia("(prefers-reduced-motion: reduce)");
const control = document.querySelector(".motion-control");
let renderer;
try {
  renderer = new THREE.WebGLRenderer({
    antialias: true,
    alpha: true,
    powerPreference: "low-power",
  });
} catch {
  if (control) control.hidden = true;
}

if (renderer) {
  const scene = new THREE.Scene();
  const camera = new THREE.OrthographicCamera(-12, 12, 6, -6, 0.1, 50);
  camera.position.z = 20;
  renderer.setPixelRatio(Math.min(devicePixelRatio, 1.5));
  renderer.setClearColor(0x000000, 0);
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  renderer.domElement.setAttribute("aria-hidden", "true");
  host.appendChild(renderer.domElement);

  // A sparse lattice follows the original knot. Each connector joins real nodes;
  // packet paths use these same lattice edges rather than unrelated particles.
  const rings = 88;
  const sides = 8;
  const surface = new THREE.TorusKnotGeometry(3.25, 0.88, rings, sides, 2, 3);
  surface.rotateX(0.32);
  surface.rotateY(-0.2);
  surface.rotateZ(-0.35);
  const source = surface.getAttribute("position");
  const nodeCount = rings * sides;
  const positions = new Float32Array(nodeCount * 3);
  const colors = new Float32Array(nodeCount * 3);
  const arrivals = new Float32Array(nodeCount);
  const pointIndex = (u, v) =>
    ((u + rings) % rings) * sides + ((v + sides) % sides);
  for (let u = 0; u < rings; u++) {
    for (let v = 0; v < sides; v++) {
      const node = pointIndex(u, v);
      const original = u * (sides + 1) + v;
      positions.set(
        [source.getX(original), source.getY(original), source.getZ(original)],
        node * 3,
      );
    }
  }
  surface.dispose();

  const edgeIndices = [];
  for (let u = 0; u < rings; u++) {
    for (let v = 0; v < sides; v++) {
      const from = pointIndex(u, v);
      edgeIndices.push(from, pointIndex(u + 1, v), from, pointIndex(u, v + 1));
      // Alternate diagonals leave breathing room in the network.
      if ((u + v) % 2 === 0) edgeIndices.push(from, pointIndex(u + 1, v + 1));
    }
  }
  const network = new THREE.Group();
  scene.add(network);
  const nodeGeometry = new THREE.BufferGeometry();
  nodeGeometry.setAttribute(
    "position",
    new THREE.BufferAttribute(positions, 3),
  );
  nodeGeometry.setAttribute(
    "color",
    new THREE.BufferAttribute(colors, 3).setUsage(THREE.DynamicDrawUsage),
  );
  const edgeGeometry = new THREE.BufferGeometry();
  edgeGeometry.setAttribute(
    "position",
    new THREE.BufferAttribute(positions, 3),
  );
  edgeGeometry.setIndex(edgeIndices);
  const edgeMaterial = new THREE.LineBasicMaterial({
    transparent: true,
    opacity: 0.27,
    depthWrite: false,
  });
  network.add(new THREE.LineSegments(edgeGeometry, edgeMaterial));

  // One tiny shared sprite makes round nodes and softly edged packet heads.
  const spriteCanvas = document.createElement("canvas");
  spriteCanvas.width = spriteCanvas.height = 32;
  const context = spriteCanvas.getContext("2d");
  const gradient = context.createRadialGradient(16, 16, 3, 16, 16, 16);
  gradient.addColorStop(0, "rgba(255,255,255,1)");
  gradient.addColorStop(0.45, "rgba(255,255,255,1)");
  gradient.addColorStop(1, "rgba(255,255,255,0)");
  context.fillStyle = gradient;
  context.fillRect(0, 0, 32, 32);
  const sprite = new THREE.CanvasTexture(spriteCanvas);
  const nodeMaterial = new THREE.PointsMaterial({
    size: 3.8,
    sizeAttenuation: false,
    map: sprite,
    vertexColors: true,
    transparent: true,
    depthWrite: false,
  });
  network.add(new THREE.Points(nodeGeometry, nodeMaterial));

  const packetCount = 22;
  const trailLength = 6;
  const heads = new Float32Array(packetCount * 3);
  const trails = new Float32Array(packetCount * trailLength * 6);
  const trailColors = new Float32Array(trails.length);
  const headGeometry = new THREE.BufferGeometry();
  headGeometry.setAttribute(
    "position",
    new THREE.BufferAttribute(heads, 3).setUsage(THREE.DynamicDrawUsage),
  );
  const headMaterial = new THREE.PointsMaterial({
    size: 6.5,
    sizeAttenuation: false,
    map: sprite,
    transparent: true,
    depthWrite: false,
  });
  const headPoints = new THREE.Points(headGeometry, headMaterial);
  headPoints.frustumCulled = false;
  network.add(headPoints);
  const haloMaterial = new THREE.PointsMaterial({
    size: 20,
    sizeAttenuation: false,
    map: sprite,
    transparent: true,
    opacity: 0.16,
    depthWrite: false,
  });
  const halos = new THREE.Points(headGeometry, haloMaterial);
  halos.frustumCulled = false;
  network.add(halos);
  const trailGeometry = new THREE.BufferGeometry();
  trailGeometry.setAttribute(
    "position",
    new THREE.BufferAttribute(trails, 3).setUsage(THREE.DynamicDrawUsage),
  );
  trailGeometry.setAttribute(
    "color",
    new THREE.BufferAttribute(trailColors, 3),
  );
  const trailMaterial = new THREE.LineBasicMaterial({
    vertexColors: true,
    transparent: true,
    opacity: 0.9,
    depthWrite: false,
  });
  const trailLines = new THREE.LineSegments(trailGeometry, trailMaterial);
  trailLines.frustumCulled = false;
  network.add(trailLines);

  const packets = Array.from({ length: packetCount }, (_, i) => ({
    u: Math.floor((i * rings) / packetCount),
    v: i % sides,
    t: (i * 0.37) % 1,
    speed: 0.5 + (i % 5) * 0.09,
    steps: i,
  }));
  const accent = new THREE.Color();
  const bright = new THREE.Color();
  const color = new THREE.Color();
  function refreshPalette() {
    const css = getComputedStyle(host);
    accent.set(css.getPropertyValue("--accent").trim());
    bright
      .copy(accent)
      .lerp(new THREE.Color(css.getPropertyValue("--ink").trim()), 0.42);
    edgeMaterial.color.copy(accent);
    headMaterial.color.copy(bright);
    haloMaterial.color.copy(accent);
    for (let i = 0; i < packetCount; i++) {
      for (let j = 0; j < trailLength; j++) {
        color.copy(accent).multiplyScalar(0.3 + (j / trailLength) * 0.7);
        color.toArray(trailColors, (i * trailLength + j) * 6);
        color.toArray(trailColors, (i * trailLength + j) * 6 + 3);
      }
    }
    trailGeometry.attributes.color.needsUpdate = true;
  }
  const destination = (packet) => {
    // Cross a ring periodically, otherwise follow the length of the knot.
    return packet.steps % 4 === 0
      ? pointIndex(packet.u, packet.v + 1)
      : pointIndex(packet.u + 1, packet.v);
  };
  function updatePackets(delta) {
    arrivals.forEach((value, i) => {
      arrivals[i] = Math.max(0, value - delta * 1.2);
    });
    packets.forEach((packet, i) => {
      packet.t += delta * packet.speed * (1 + requestEnergy * 1.6);
      if (packet.t >= 1) {
        arrivals[destination(packet)] = 1;
        if (packet.steps % 4 === 0) packet.v = (packet.v + 1) % sides;
        else packet.u = (packet.u + 1) % rings;
        packet.steps++;
        packet.t %= 1;
      }
      const a = pointIndex(packet.u, packet.v) * 3;
      const b = destination(packet) * 3;
      for (let axis = 0; axis < 3; axis++) {
        const start = positions[a + axis];
        const distance = positions[b + axis] - start;
        heads[i * 3 + axis] = start + distance * packet.t;
        for (let j = 0; j < trailLength; j++) {
          const offset = (i * trailLength + j) * 6 + axis;
          trails[offset] =
            start + distance * Math.max(0, packet.t - (trailLength - j) * 0.07);
          trails[offset + 3] =
            start +
            distance * Math.max(0, packet.t - (trailLength - j - 1) * 0.07);
        }
      }
    });
    for (let i = 0; i < nodeCount; i++) {
      const depth = THREE.MathUtils.clamp(
        (positions[i * 3 + 2] + 6) / 12,
        0,
        1,
      );
      color
        .copy(accent)
        .multiplyScalar(0.38 + depth * 0.42)
        .lerp(bright, arrivals[i]);
      color.toArray(colors, i * 3);
    }
    nodeGeometry.attributes.color.needsUpdate = true;
    headGeometry.attributes.position.needsUpdate = true;
    trailGeometry.attributes.position.needsUpdate = true;
  }

  let visible = true;
  let paused = reduce.matches || host.dataset.paused === "true";
  let lastFrame = null;
  let elapsed = 0;
  let requestEnergy = 0;
  const pointer = { x: 0, y: 0 };
  function draw() {
    renderer.render(scene, camera);
  }
  function frame(time) {
    if (lastFrame !== null && time - lastFrame < 32) return;
    const delta =
      lastFrame === null ? 0 : Math.min((time - lastFrame) / 1000, 0.06);
    lastFrame = time;
    elapsed += delta;
    requestEnergy = Math.max(0, requestEnergy - delta * 0.5);
    network.rotation.y = Math.sin(elapsed * 0.09) * 0.12 + pointer.x * 0.04;
    network.rotation.x = Math.cos(elapsed * 0.07) * 0.04 + pointer.y * 0.025;
    updatePackets(delta);
    draw();
  }
  function updateLoop() {
    lastFrame = null;
    renderer.setAnimationLoop(
      visible && !document.hidden && !paused && !reduce.matches ? frame : null,
    );
    draw();
  }
  const resize = new ResizeObserver(() => {
    const { width, height } = host.getBoundingClientRect();
    if (!width || !height) return;
    const aspect = width / height;
    renderer.setSize(width, height, false);
    camera.left = -6 * aspect;
    camera.right = 6 * aspect;
    camera.updateProjectionMatrix();
    // The network bleeds past the sides, instead of fitting inside an illustration column.
    network.scale.x = Math.max(0.95, aspect * 1.12);
    network.position.y = width < 768 ? -1.5 : -0.2;
    draw();
    host.classList.add("ready");
  });
  const intersection = new IntersectionObserver(
    ([entry]) => {
      visible = entry.isIntersecting;
      updateLoop();
    },
    { threshold: 0.01 },
  );
  const theme = new MutationObserver(() => {
    refreshPalette();
    updatePackets(0);
    draw();
  });
  theme.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    refreshPalette();
    updatePackets(0);
    draw();
  });
  hero.addEventListener("pointermove", (event) => {
    const rect = host.getBoundingClientRect();
    pointer.x = (event.clientX - rect.left) / rect.width - 0.5;
    pointer.y = (event.clientY - rect.top) / rect.height - 0.5;
  });
  hero.addEventListener("pointerleave", () => {
    pointer.x = pointer.y = 0;
  });
  document.addEventListener("hyperchi:motion", (event) => {
    paused = event.detail.paused;
    updateLoop();
  });
  document.addEventListener("hyperchi:request-complete", () => {
    if (!paused && !reduce.matches) requestEnergy = 1;
  });
  document.addEventListener("visibilitychange", updateLoop);
  reduce.addEventListener("change", (event) => {
    paused = event.matches || host.dataset.paused === "true";
    updateLoop();
  });
  renderer.domElement.addEventListener("webglcontextlost", (event) => {
    event.preventDefault();
    renderer.setAnimationLoop(null);
    host.classList.remove("ready");
    if (control) control.hidden = true;
  });
  renderer.domElement.addEventListener("webglcontextrestored", () => {
    host.classList.add("ready");
    if (control) control.hidden = false;
    updateLoop();
  });
  window.addEventListener("pagehide", (event) => {
    renderer.setAnimationLoop(null);
    if (!event.persisted) {
      resize.disconnect();
      intersection.disconnect();
      theme.disconnect();
      for (const geometry of [
        nodeGeometry,
        edgeGeometry,
        headGeometry,
        trailGeometry,
      ])
        geometry.dispose();
      for (const material of [
        nodeMaterial,
        edgeMaterial,
        headMaterial,
        haloMaterial,
        trailMaterial,
      ])
        material.dispose();
      sprite.dispose();
      renderer.dispose();
    }
  });
  window.addEventListener("pageshow", (event) => {
    if (event.persisted) updateLoop();
  });
  refreshPalette();
  updatePackets(0);
  resize.observe(host);
  intersection.observe(host);
  updateLoop();
}
