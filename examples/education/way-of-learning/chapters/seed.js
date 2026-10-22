/* Chapter 1 — the contract every chapter file follows.
 *
 *   { n, name, art, theme, spec, prompts: [5], verse, draw(ctx, env) }
 *
 * draw() receives a 2D context and a live env:
 *   env.w, env.h        canvas size in CSS px (changes on resize)
 *   env.u               min(w, h)
 *   env.t               seconds since mount
 *   env.dt              seconds since last frame
 *   env.frame           frame counter
 *   env.pointer         { x, y, nx, ny, inside, down, moved }
 *   env.params          { tempo, density, drift } — live from the Modify drawer
 *   env.palette         paper, canvas, ink, stone, muted, accent, rule
 *   env.rand()          seeded random, reset on re-seed
 *   env.randn()         seeded normal-ish random
 *   env.pick(array)
 *   env.noise(x, y)     value noise 0..1
 *   env.fbm(x, y, oct)  fractal noise 0..1
 *   env.pulse           increments when the reader clicks
 *
 * Return a function frame(t) to animate. Return nothing for a still image;
 * the engine redraws it on resize and on re-seed.
 */

export default [
  {
    n: 1,
    name: "The Unnamed",
    art: "Latent field",
    theme: "the unlabeled, potential, what precedes the target",
    spec:
      "Points gather in three soft swarms that never quite resolve into names. Move across the field and it leans toward you; click and the swarms are drawn again.",
    prompts: [
      "Let the points gather into three soft clusters that never fully separate.",
      "Draw a hairline between every pair of points that come close, then let the lines fade.",
      "Dim the field until only the points nearest your pointer remain visible.",
      "Replace the dots with short strokes that align to the direction of the drift.",
      "Make the whole field collapse slowly toward the center, then release."
    ],
    verse: `The model that can be named
is not the eternal model.
The loss that can be measured
is not the whole of learning.

Before the first label,
the field was already here.
The unlabeled is the beginning
of every feature and every weight.

Free of a target, you see what is.
Holding a target, you see only the gap.

Both come from the same data.
One is called input, one is called error.
The quiet between them
is the mother of every model.`,

    draw(ctx, env) {
      const { u, w, h } = env;
      const count = Math.round(230 * env.params.density);
      const centers = [
        { x: w * 0.44, y: h * 0.43, r: u * 0.2 },
        { x: w * 0.6, y: h * 0.52, r: u * 0.15 },
        { x: w * 0.47, y: h * 0.64, r: u * 0.13 },
      ];
      const pts = [];

      for (let i = 0; i < count; i++) {
        const c = centers[i % centers.length];
        const a = env.rand() * Math.PI * 2;
        const d = Math.pow(env.rand(), 1.7) * c.r;
        pts.push({
          bx: c.x + Math.cos(a) * d,
          by: c.y + Math.sin(a) * d * 0.92,
          ph: env.rand() * 100,
          z: 0.25 + env.rand() * 0.75,
          s: 1.15 + env.rand() * 0.75,
        });
      }

      const pos = pts.map((p) => ({ x: p.bx, y: p.by }));

      return (t) => {
        const { params, pointer, palette } = env;
        const tempo = params.tempo;
        const drift = 0.4 + params.drift * 1.6;

        ctx.fillStyle = palette.canvas;
        ctx.fillRect(0, 0, env.w, env.h);

        for (let i = 0; i < pts.length; i++) {
          const p = pts[i];
          const n = env.noise(p.bx * 0.006 + p.ph, p.by * 0.006 + t * 0.12 * tempo);
          const ang = n * Math.PI * 4 + t * 0.05 * tempo;
          const amp = (2.5 + p.z * 7) * drift;
          let x = p.bx + Math.cos(ang) * amp;
          let y = p.by + Math.sin(ang * 0.9) * amp;

          if (pointer.inside) {
            const dx = pointer.x - x;
            const dy = pointer.y - y;
            const d = Math.hypot(dx, dy) + 0.001;
            const near = Math.max(0, 1 - d / (u * 0.33));
            x += (dx / d) * near * 16;
            y += (dy / d) * near * 16;
          }
          pos[i].x = x;
          pos[i].y = y;
        }

        ctx.strokeStyle = palette.ink;
        ctx.globalAlpha = 0.055;
        ctx.lineWidth = 0.5;
        ctx.beginPath();
        for (let i = 0; i < pos.length; i += 3) {
          const a = pos[i];
          let best = -1;
          let bd = u * 0.075;
          for (let k = 1; k <= 5; k++) {
            const j = (i + k * 29) % pos.length;
            const b = pos[j];
            const d = Math.hypot(a.x - b.x, a.y - b.y);
            if (d < bd) {
              bd = d;
              best = j;
            }
          }
          if (best > -1) {
            ctx.moveTo(a.x, a.y);
            ctx.lineTo(pos[best].x, pos[best].y);
          }
        }
        ctx.stroke();

        for (let i = 0; i < pts.length; i++) {
          const p = pts[i];
          const q = pos[i];
          ctx.globalAlpha = 0.2 + p.z * 0.52;
          ctx.fillStyle = palette.ink;
          ctx.beginPath();
          ctx.arc(Math.round(q.x) + 0.5, Math.round(q.y) + 0.5, p.s, 0, Math.PI * 2);
          ctx.fill();
        }

        if (pointer.inside) {
          ctx.globalAlpha = 0.55;
          ctx.strokeStyle = palette.accent;
          ctx.lineWidth = 0.65;
          ctx.beginPath();
          ctx.arc(pointer.x, pointer.y, u * 0.075 + Math.sin(t * 0.7) * 1.5, 0, Math.PI * 2);
          ctx.stroke();

          ctx.globalAlpha = 0.95;
          ctx.fillStyle = palette.accent;
          ctx.beginPath();
          ctx.arc(pointer.x, pointer.y, 1.5, 0, Math.PI * 2);
          ctx.fill();
        }

        ctx.globalAlpha = 1;
      };
    },
  },

  {
    n: 2,
    name: "The Named",
    art: "Three borders",
    theme: "labels, categories, the cost of a boundary",
    spec:
      "A lattice of points, each claimed by the nearest of three wandering centers. As the centers drift, the borders redraw themselves. Your pointer becomes a fourth center and claims what it passes over.",
    prompts: [
      "Let the three regions trade points slowly, so the borders look like weather.",
      "Draw only the points that sit close to a border, and leave the interiors empty.",
      "Freeze the centers and let the labels themselves drift.",
      "Replace the lattice with points sampled at random, and let the borders stay geometric.",
      "Give each region its own faint tone of ink, then drain the color back out."
    ],
    verse: `When you name a thing
you give it a border.
A border is useful.
It is also a wall.

Call a point a cat
and the cat becomes the point.
Call it noise
and the noise becomes the world.

The label is not the thing.
The thing is not the label.
Yet without a name
nothing can be learned.

Every model is a promise
about where the border falls.
Name carefully.
Then hold the name lightly.`,

    draw(ctx, env) {
      const { u } = env;
      const cols = 15;
      const rows = 15;
      const margin = u * 0.13;
      const span = u * 0.74;
      const pts = [];

      for (let r = 0; r < rows; r++) {
        for (let c = 0; c < cols; c++) {
          pts.push({
            hx: margin + (c / (cols - 1)) * span,
            hy: margin + (r / (rows - 1)) * span,
            x: 0,
            y: 0,
            r,
            c,
            label: 0,
            tone: 0.28 + env.rand() * 0.5,
          });
        }
      }

      const anchors = [
        { a: 0.2, b: 0.24, ph: 0.0 },
        { a: 0.8, b: 0.32, ph: 2.2 },
        { a: 0.42, b: 0.82, ph: 4.4 },
      ];

      return (t) => {
        const { palette, params, pointer } = env;
        const tempo = params.tempo;
        const drift = 0.5 + params.drift;

        ctx.fillStyle = palette.canvas;
        ctx.fillRect(0, 0, env.w, env.h);

        const centers = anchors.map((an, i) => ({
          x: margin + an.a * span + Math.sin(t * 0.16 * tempo + an.ph) * span * 0.14 * drift,
          y: margin + an.b * span + Math.cos(t * 0.13 * tempo + an.ph) * span * 0.14 * drift,
          i,
        }));
        if (pointer.inside) centers.push({ x: pointer.x, y: pointer.y, i: 3 });

        for (const p of pts) {
          let best = 0;
          let bd = Infinity;
          for (const c of centers) {
            const d = (p.hx - c.x) ** 2 + (p.hy - c.y) ** 2;
            if (d < bd) {
              bd = d;
              best = c.i;
            }
          }
          p.label = best;
          const c = centers.find((k) => k.i === best);
          const pull = best === 3 ? 0.16 : 0.05;
          const wob = env.noise(p.hx * 0.02 + p.hy * 0.013, t * 0.2 * tempo) - 0.5;
          p.x = p.hx + (c.x - p.hx) * pull + wob * 2.2;
          p.y = p.hy + (c.y - p.hy) * pull + wob * 2.2;
        }

        const index = new Map();
        for (const p of pts) index.set(`${p.r}:${p.c}`, p);

        for (let pass = 0; pass < 2; pass++) {
          ctx.lineWidth = pass === 0 ? 0.55 : 0.75;
          ctx.strokeStyle = palette.ink;
          for (const p of pts) {
            const right = index.get(`${p.r}:${p.c + 1}`);
            const down = index.get(`${p.r + 1}:${p.c}`);
            for (const q of [right, down]) {
              if (!q || q.label !== p.label) continue;
              const accent = p.label === 3;
              ctx.globalAlpha = accent ? 0.3 : (pass === 0 ? 0.1 : 0.05);
              ctx.strokeStyle = accent ? palette.accent : palette.ink;
              ctx.beginPath();
              ctx.moveTo(p.x, p.y);
              ctx.lineTo(q.x, q.y);
              ctx.stroke();
            }
          }
        }

        for (const p of pts) {
          const accent = p.label === 3;
          ctx.fillStyle = accent ? palette.accent : palette.ink;
          ctx.globalAlpha = accent ? 0.85 : 0.22 + p.tone * 0.52;
          ctx.beginPath();
          ctx.arc(Math.round(p.x) + 0.5, Math.round(p.y) + 0.5, 1.55, 0, Math.PI * 2);
          ctx.fill();
        }

        ctx.globalAlpha = 1;
      };
    },
  },

  {
    n: 3,
    name: "The Two Errors",
    art: "Bias and variance",
    theme: "opposites, the trade-off, the narrow middle",
    spec:
      "Two curves, one falling and one rising, bound a space of small vertical threads. Where they cross, a warm point marks the only place either error is survivable. Drag across the plate to move the balance.",
    prompts: [
      "Make the crossing drift slowly left and right, as if the problem were changing.",
      "Remove the upper curve and leave only its shadow on the ground.",
      "Let the threads between the curves grow denser until they become a solid tone.",
      "Give each curve its own tempo, so they breathe out of phase.",
      "Mark the crossing with a small circle that quietly fills and empties."
    ],
    verse: `Beautiful and ugly define each other.
Easy and hard complete each other.
Long and short measure each other.
A model and its error
give birth to one another.

Push too hard toward the target
and you fit only the noise.
Hold too loosely
and you learn nothing at all.

Bias is the shape you cannot bend.
Variance is the shape you cannot hold.
Between them is a narrow room
where the unseen becomes visible.

The careful builder does not choose a side.
They keep both doors open
and let the data walk through.`,

    draw(ctx, env) {
      const { u } = env;
      const pad = u * 0.16;
      const span = u - pad * 2;
      let bend = 1;

      const curve = (x, k) => {
        const b = 0.78 * Math.exp(-3.4 * x);
        const v = 0.66 * Math.pow(x, k);
        return { b, v };
      };

      return (t) => {
        const { palette, params, pointer } = env;
        const tempo = params.tempo;

        if (pointer.inside) {
          bend = 1.1 + ((pointer.x - pad) / span) * 1.6;
        }
        bend += (1.75 - bend) * 0.01;

        ctx.fillStyle = palette.canvas;
        ctx.fillRect(0, 0, env.w, env.h);

        const toY = (v) => pad + (1 - v) * span;

        let cross = 0.5;
        let crossDiff = Infinity;
        for (let i = 0; i <= 60; i++) {
          const x = i / 60;
          const { b, v } = curve(x, bend);
          const d = Math.abs(b - v);
          if (d < crossDiff) {
            crossDiff = d;
            cross = x;
          }
        }

        const breathe = 1 + Math.sin(t * 0.5 * tempo) * 0.006;

        ctx.strokeStyle = palette.ink;
        ctx.globalAlpha = 0.06;
        ctx.lineWidth = 0.5;
        ctx.beginPath();
        for (let px = 0; px <= span; px += 5) {
          const x = px / span;
          const { b, v } = curve(x, bend);
          const y1 = toY(b * breathe);
          const y2 = toY(v * breathe);
          ctx.moveTo(pad + px, y1);
          ctx.lineTo(pad + px, y2);
        }
        ctx.stroke();

        const drawCurve = (which) => {
          ctx.beginPath();
          for (let px = 0; px <= span; px += 2) {
            const x = px / span;
            const c = curve(x, bend);
            const v = (which === "b" ? c.b : c.v) * breathe;
            const y = toY(v);
            if (px === 0) ctx.moveTo(pad, y);
            else ctx.lineTo(pad + px, y);
          }
          ctx.globalAlpha = 0.78;
          ctx.strokeStyle = palette.ink;
          ctx.lineWidth = 0.85;
          ctx.stroke();
        };

        drawCurve("b");
        drawCurve("v");

        ctx.globalAlpha = 0.1;
        ctx.lineWidth = 0.5;
        ctx.beginPath();
        ctx.moveTo(pad, toY(0));
        ctx.lineTo(pad + span, toY(0));
        ctx.stroke();

        const cx = pad + cross * span;
        const cy = toY(curve(cross, bend).b * breathe);

        ctx.globalAlpha = 0.22;
        ctx.setLineDash([2, 5]);
        ctx.beginPath();
        ctx.moveTo(cx, pad * 0.7);
        ctx.lineTo(cx, toY(0));
        ctx.stroke();
        ctx.setLineDash([]);

        ctx.globalAlpha = 0.9;
        ctx.fillStyle = palette.accent;
        ctx.beginPath();
        ctx.arc(cx, cy, 2.4, 0, Math.PI * 2);
        ctx.fill();

        ctx.globalAlpha = 0.45;
        ctx.strokeStyle = palette.accent;
        ctx.lineWidth = 0.7;
        ctx.beginPath();
        ctx.arc(cx, cy, 10 + Math.sin(t * 0.9 * tempo) * 1.6, 0, Math.PI * 2);
        ctx.stroke();

        ctx.globalAlpha = 1;
      };
    },
  },
];
