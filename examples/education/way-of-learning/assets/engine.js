/* The Way of Learning — artifact engine.
 *
 * A chapter provides:
 *   draw(ctx, env) -> function frame(t) { ... }
 *
 * env is live: w/h change on resize, t is seconds since first mount,
 * params are driven by the Modify drawer, pointer is canvas-local px.
 *
 * The engine handles device pixel ratio, resizing, pausing off-screen,
 * reduced motion, pointer input, and re-seeding ("Begin again").
 */

export const PALETTE = {
  paper: "#faf9f5",
  canvas: "#f0eee6",
  ink: "#1f1e1d",
  stone: "#5e5d59",
  muted: "#87867f",
  accent: "#d97757",
  rule: "#e4e2da",
};

export const TAU = Math.PI * 2;
export const clamp = (v, a = 0, b = 1) => (v < a ? a : v > b ? b : v);
export const lerp = (a, b, t) => a + (b - a) * t;
export const map = (v, a, b, c, d) => c + ((v - a) / (b - a)) * (d - c);
export const smoothstep = (t) => t * t * (3 - 2 * t);
export const dist = (x1, y1, x2, y2) => Math.hypot(x2 - x1, y2 - y1);

function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function hash2(x, y) {
  let h = Math.imul(x | 0, 374761393) ^ Math.imul(y | 0, 668265263);
  h = Math.imul(h ^ (h >>> 13), 1274126177);
  return ((h ^ (h >>> 16)) >>> 0) / 4294967296;
}

export function noise(x, y) {
  const xi = Math.floor(x);
  const yi = Math.floor(y);
  const xf = smoothstep(x - xi);
  const yf = smoothstep(y - yi);
  const a = hash2(xi, yi);
  const b = hash2(xi + 1, yi);
  const c = hash2(xi, yi + 1);
  const d = hash2(xi + 1, yi + 1);
  return lerp(lerp(a, b, xf), lerp(c, d, xf), yf);
}

export function fbm(x, y, octaves = 4) {
  let v = 0;
  let amp = 0.5;
  let f = 1;
  for (let i = 0; i < octaves; i++) {
    v += amp * noise(x * f, y * f);
    f *= 2;
    amp *= 0.5;
  }
  return v;
}

const stages = new Set();
let rafId = 0;
let last = 0;

function loop(now) {
  rafId = requestAnimationFrame(loop);
  if (document.hidden) return;
  const dt = Math.min(0.05, last ? (now - last) / 1000 : 0.016);
  last = now;
  for (const s of stages) s.tick(now / 1000, dt);
}

function ensureLoop() {
  if (!rafId) {
    last = 0;
    rafId = requestAnimationFrame(loop);
  }
}

function maybeStop() {
  if (stages.size === 0 && rafId) {
    cancelAnimationFrame(rafId);
    rafId = 0;
  }
}

export function createStage(canvas, chapter, options = {}) {
  const ctx = canvas.getContext("2d", { alpha: false });
  if (!ctx) return null;

  const still = !!options.still;
  const params = { tempo: 1, density: 1, drift: 0.5, ...(options.params || {}) };
  const baseSeed = (chapter.n || 1) * 7919 + 17;

  const env = {
    w: 1,
    h: 1,
    u: 1,
    dpr: 1,
    t: 0,
    dt: 0.016,
    frame: 0,
    seed: baseSeed,
    pointer: { x: 0, y: 0, nx: 0, ny: 0, inside: false, down: false, moved: 0 },
    params,
    palette: PALETTE,
    TAU,
    clamp,
    lerp,
    map,
    smoothstep,
    dist,
    noise,
    fbm,
    rand: mulberry32(baseSeed),
    randn() {
      return (env.rand() + env.rand() + env.rand() - 1.5) * 1.15;
    },
    pick(arr) {
      return arr[Math.floor(env.rand() * arr.length) % arr.length];
    },
    pulse: 0,
  };

  let frameFn = null;
  let started = 0;
  let elapsed = 0;
  let destroyed = false;
  let rafOwner = false;

  function build() {
    if (destroyed) return;
    const rect = canvas.getBoundingClientRect();
    const w = Math.max(1, Math.round(rect.width || canvas.parentElement?.clientWidth || 1));
    const h = Math.max(1, Math.round(rect.height || canvas.parentElement?.clientHeight || 1));
    const dpr = Math.min(2, window.devicePixelRatio || 1);

    env.w = w;
    env.h = h;
    env.u = Math.min(w, h);
    env.dpr = dpr;
    env.seed = baseSeed + env.pulse * 131;
    env.rand = mulberry32(env.seed);

    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.lineCap = "round";
    ctx.lineJoin = "round";

    try {
      frameFn = typeof chapter.draw === "function" ? chapter.draw(ctx, env) : null;
    } catch (err) {
      console.error(`chapter ${chapter.n}:`, err);
      frameFn = null;
    }
  }

  function drawOnce() {
    if (destroyed) return;
    env.dt = 0;
    ctx.save();
    try {
      if (typeof frameFn === "function") frameFn(env.t);
    } catch (err) {
      console.error(`chapter ${chapter.n}:`, err);
    }
    ctx.restore();
  }

  function tick(now, dt) {
    if (destroyed) return;
    if (!started) started = now;
    elapsed = now - started + (options.offset || 0);
    env.t = elapsed;
    env.dt = dt;
    env.frame++;
    if (still) return;
    ctx.save();
    try {
      if (typeof frameFn === "function") frameFn(env.t);
    } catch (err) {
      console.error(`chapter ${chapter.n}:`, err);
      frameFn = null;
    }
    ctx.restore();
  }

  function resize() {
    if (destroyed) return;
    build();
    drawOnce();
  }

  let resizeTimer = 0;
  const ro = new ResizeObserver(() => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(resize, 120);
  });
  ro.observe(canvas.parentElement || canvas);

  function onPointerMove(e) {
    const r = canvas.getBoundingClientRect();
    env.pointer.x = e.clientX - r.left;
    env.pointer.y = e.clientY - r.top;
    env.pointer.nx = (env.pointer.x / r.width) * 2 - 1;
    env.pointer.ny = (env.pointer.y / r.height) * 2 - 1;
    env.pointer.inside = true;
    env.pointer.moved++;
    if (still) drawOnce();
  }

  function onPointerLeave() {
    env.pointer.inside = false;
    env.pointer.down = false;
  }

  function onPointerDown(e) {
    env.pointer.down = true;
    const r = canvas.getBoundingClientRect();
    env.pointer.x = e.clientX - r.left;
    env.pointer.y = e.clientY - r.top;
    env.pointer.nx = (env.pointer.x / r.width) * 2 - 1;
    env.pointer.ny = (env.pointer.y / r.height) * 2 - 1;
    env.pulse++;
    env.seed = baseSeed + env.pulse * 131;
    env.rand = mulberry32(env.seed);
    build();
    drawOnce();
    canvas.classList.add("is-pressed");
  }

  function onPointerUp() {
    env.pointer.down = false;
    canvas.classList.remove("is-pressed");
  }

  canvas.addEventListener("pointermove", onPointerMove, { passive: true });
  canvas.addEventListener("pointerleave", onPointerLeave, { passive: true });
  canvas.addEventListener("pointerdown", onPointerDown, { passive: true });
  window.addEventListener("pointerup", onPointerUp, { passive: true });
  canvas.addEventListener("keydown", (e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      env.pulse++;
      build();
      drawOnce();
    }
  });

  build();
  drawOnce();

  if (!still) {
    stages.add({ tick });
    rafOwner = true;
    ensureLoop();
  }

  return {
    env,
    params,
    stage: { tick },
    start() {
      if (destroyed || still) return;
      if (!rafOwner) {
        stages.add({ tick });
        rafOwner = true;
        ensureLoop();
      }
    },
    stop() {
      if (rafOwner) {
        for (const s of stages) if (s.tick === tick) stages.delete(s);
        rafOwner = false;
        maybeStop();
      }
    },
    reseed() {
      env.pulse++;
      build();
      drawOnce();
    },
    destroy() {
      destroyed = true;
      ro.disconnect();
      clearTimeout(resizeTimer);
      canvas.removeEventListener("pointermove", onPointerMove);
      canvas.removeEventListener("pointerleave", onPointerLeave);
      canvas.removeEventListener("pointerdown", onPointerDown);
      window.removeEventListener("pointerup", onPointerUp);
      this.stop();
      frameFn = null;
    },
  };
}
