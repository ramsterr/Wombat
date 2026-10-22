import { createStage, PALETTE, TAU, clamp, lerp, noise } from "./engine.js";

const SETS = ["seed", "a", "b", "c", "d"];
const ROMAN = ["", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"];

const $ = (sel, root = document) => root.querySelector(sel);
const pad = (n) => String(n).padStart(2, "0");

const state = {
  chapters: [],
  stages: new Map(),
  active: 1,
  motion: !window.matchMedia("(prefers-reduced-motion: reduce)").matches,
  drawerOpen: false,
  contentsOpen: false,
};

/* ------------------------------------------------------------------ */
/* content                                                             */
/* ------------------------------------------------------------------ */

async function loadChapters() {
  const all = [];
  for (const name of SETS) {
    try {
      const mod = await import(`../chapters/${name}.js`);
      const list = mod.default || mod.chapters || [];
      if (Array.isArray(list)) all.push(...list);
      else if (list && typeof list === "object") all.push(...Object.values(list));
      else console.warn(`chapters/${name}.js did not export an array`);
    } catch (err) {
      if (!/Failed to fetch|Cannot find module|404|Importing a module script failed/i.test(String(err))) {
        console.warn(`chapters/${name}.js`, err);
      }
    }
  }
  const seen = new Set();
  return all
    .filter((c) => c && Number.isFinite(c.n) && c.n >= 1 && c.n <= 81 && typeof c.verse === "string")
    .filter((c) => (seen.has(c.n) ? false : seen.add(c.n)))
    .sort((a, b) => a.n - b.n);
}

/* ------------------------------------------------------------------ */
/* markup                                                              */
/* ------------------------------------------------------------------ */

function coverTemplate() {
  return `
    <section class="cover" id="top">
      <div class="cover-inner">
        <h1>The Way<br>of Learning</h1>
        <p class="subtitle">The Timeless Art of Machine Learning</p>
        <div class="mark" id="coverMark"><canvas role="img" aria-label="A field of faint points slowly finding order around a single warm center"></canvas></div>
        <p class="based"><span>Based on Lao Tzu</span><span>Adapted for the age of models</span></p>
        <p class="scrollcue">Eighty-one chapters</p>
      </div>
    </section>

    <section class="note reveal">
      <p>Lao Tzu wrote of a way that cannot be spoken, and then spoke of it for eighty-one chapters. This book borrows his patience, not his words.</p>
      <p>Learning is older than machines. It is the practice of changing what you believe in proportion to how wrong you were. A model does it with numbers. A person does it with a life. Everything in these pages is that single movement, seen from eighty-one angles.</p>
      <p>The artifacts beside each chapter are alive. Touch them. They are not illustrations of the text. They are the text, thinking.</p>
      <p class="attrib">After Lao Tzu</p>
    </section>
  `;
}

function chapterTemplate(c) {
  const label = c.art || c.name || "Artifact";
  return `
    <article class="chapter" id="ch-${c.n}" data-n="${c.n}">
      <div class="art reveal" data-art="${c.n}">
        <canvas role="img" aria-label="${escapeAttr(c.spec || label)}"></canvas>
      </div>
      <div class="art-meta reveal">
        <span class="name">${escapeHtml(label)}</span>
        <button type="button" class="modify" data-modify="${c.n}">Modify</button>
      </div>
      <div class="chapter-body reveal">
        <p class="chapter-num">${pad(c.n)}</p>
        <p class="verse">${escapeHtml(c.verse.trim())}</p>
        <p class="chapter-theme">themes — <em>${escapeHtml(c.theme || "")}</em></p>
      </div>
    </article>
  `;
}

function colophonTemplate() {
  return `
    <section class="colophon" id="colophon">
      <div class="colophon-inner reveal">
        <h2>An invitation to continue</h2>
        <p>Every artifact in this book is drawn by a few hundred lines of plain JavaScript. Nothing is an image file. Nothing is fixed. Each one is a small model of the idea it sits beside: a field of points, a gradient, a boundary, a drift.</p>
        <p>This is how models are made, too. You begin with a seed, a shape, a suspicion about how something moves. You watch it fail. You adjust one number and watch again. The artifact and the model are the same gesture: a guess, held up to the world, corrected.</p>
        <p>Open the Modify panel. Change the tempo. Break the pattern. Nothing here is sacred, including the text.</p>
        <div class="rule"></div>
        <p class="small">
          <span>Verse and artifacts — original, after Lao Tzu</span>
          <span>Set in Sorts Mill Goudy and Courier Prime</span>
          <span>Canvas 2D, no libraries, no build step</span>
        </p>
        <p class="small" style="margin-top:2.5rem">References consulted</p>
        <p class="small" style="text-transform:none;letter-spacing:0.01em;font-size:0.82rem;line-height:1.8">
          Rumelhart, Hinton &amp; Williams, <em>Learning representations by back-propagating errors</em> (1986) ·
          Cortes &amp; Vapnik, <em>Support-vector networks</em> (1995) ·
          Krizhevsky, Sutskever &amp; Hinton, <em>ImageNet Classification with Deep CNNs</em> (2012) ·
          Sculley et al., <em>Hidden Technical Debt in Machine Learning Systems</em> (2015) ·
          Goodfellow, Bengio &amp; Courville, <em>Deep Learning</em> (2016) ·
          Vaswani et al., <em>Attention Is All You Need</em> (2017) ·
          Gebru et al., <em>Datasheets for Datasets</em> (2018) ·
          Amodei et al., <em>Concrete Problems in AI Safety</em> (2016) ·
          Kaplan et al., <em>Scaling Laws for Neural Language Models</em> (2020) ·
          Mitchell et al., <em>Model Cards for Model Reporting</em> (2019)
        </p>
        <a class="back" href="#top">Return to the beginning</a>
      </div>
    </section>
  `;
}

function escapeHtml(str) {
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}
const escapeAttr = escapeHtml;

/* ------------------------------------------------------------------ */
/* cover artifact                                                      */
/* ------------------------------------------------------------------ */

const coverArt = {
  n: 0,
  draw(ctx, env) {
    const pts = [];
    const count = 520;
    const rnd = env.rand;
    for (let i = 0; i < count; i++) {
      const a = rnd() * TAU;
      const r = Math.pow(rnd(), 0.62);
      pts.push({ a, r, s: 0.35 + rnd() * 0.9, ph: rnd() * TAU, k: 0.15 + rnd() * 0.5 });
    }
    return (t) => {
      const { w, h, u, pointer, params } = env;
      ctx.fillStyle = PALETTE.canvas;
      ctx.fillRect(0, 0, w, h);

      const cx = w / 2;
      const cy = h / 2;
      const maxR = u * 0.46;
      const time = t * 0.12 * params.tempo;

      ctx.fillStyle = PALETTE.ink;
      for (const p of pts) {
        const drift = noise(p.a * 1.6, time + p.ph) - 0.5;
        const rad = maxR * p.r + drift * u * 0.035;
        const ang = p.a + time * p.k * 0.4 + drift * 0.25;
        let x = cx + Math.cos(ang) * rad;
        let y = cy + Math.sin(ang) * rad;

        if (pointer.inside) {
          const dx = cx - pointer.x;
          const dy = cy - pointer.y;
          const pull = clamp(1 - Math.hypot(dx, dy) / (u * 0.6), 0, 1) * 0.06;
          x += dx * pull * p.r;
          y += dy * pull * p.r;
        }

        ctx.globalAlpha = 0.16 + (1 - p.r) * 0.5;
        ctx.fillRect(x, y, p.s, p.s);
      }

      const ring = (r, alpha, lw) => {
        ctx.globalAlpha = alpha;
        ctx.strokeStyle = PALETTE.ink;
        ctx.lineWidth = lw;
        ctx.beginPath();
        ctx.arc(cx, cy, r, 0, TAU);
        ctx.stroke();
      };
      ring(u * 0.3, 0.22, 0.6);
      ring(u * 0.2, 0.3, 0.6);

      ctx.globalAlpha = 1;
      ctx.fillStyle = PALETTE.accent;
      ctx.beginPath();
      ctx.arc(cx, cy, 1.9, 0, TAU);
      ctx.fill();
    };
  },
};

/* ------------------------------------------------------------------ */
/* render                                                              */
/* ------------------------------------------------------------------ */

function render(chapters) {
  const book = $("#book");
  book.insertAdjacentHTML("beforeend", coverTemplate());
  book.insertAdjacentHTML(
    "beforeend",
    chapters.map(chapterTemplate).join("") + colophonTemplate()
  );

  const grid = $("#contentsGrid");
  grid.innerHTML = chapters
    .map(
      (c) => `<li><a href="#ch-${c.n}" data-jump="${c.n}">
        <span class="cn">${pad(c.n)}</span>
        <span class="ct">${escapeHtml(c.name || "")}</span>
      </a></li>`
    )
    .join("");
  $("#contentsFoot").textContent = `${chapters.length} chapters · ${chapters.reduce(
    (n, c) => n + (c.verse ? c.verse.trim().split(/\n\s*\n/).length : 0),
    0
  )} stanzas`;
}

/* ------------------------------------------------------------------ */
/* stages                                                              */
/* ------------------------------------------------------------------ */

function mount(n) {
  if (state.stages.has(n)) return state.stages.get(n);
  const el = document.querySelector(`.art[data-art="${n}"] canvas`);
  const chapter = state.chapters.find((c) => c.n === n);
  if (!el || !chapter) return null;
  const stage = createStage(el, chapter, { still: !state.motion });
  if (stage) state.stages.set(n, stage);
  $(".art[data-art='" + n + "']")?.classList.add("is-live");
  return stage;
}

function unmount(n) {
  const stage = state.stages.get(n);
  if (!stage) return;
  stage.destroy();
  state.stages.delete(n);
  $(".art[data-art='" + n + "']")?.classList.remove("is-live");
}

function observeArtifacts() {
  const io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        const n = Number(e.target.dataset.art);
        if (e.isIntersecting) mount(n);
        else unmount(n);
      }
    },
    { rootMargin: "300px 0px", threshold: 0 }
  );
  document.querySelectorAll(".art").forEach((el) => io.observe(el));

  const reveal = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (e.isIntersecting) {
          e.target.classList.add("in");
          reveal.unobserve(e.target);
        }
      }
    },
    { rootMargin: "0px 0px -8% 0px", threshold: 0.08 }
  );
  document.querySelectorAll(".reveal").forEach((el) => reveal.observe(el));

  const activeIO = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (e.isIntersecting) state.active = Number(e.target.dataset.n);
      }
    },
    { rootMargin: "-45% 0px -45% 0px", threshold: 0 }
  );
  document.querySelectorAll(".chapter").forEach((el) => activeIO.observe(el));
}

function mountCover() {
  const canvas = $("#coverMark canvas");
  if (canvas) createStage(canvas, coverArt, { still: !state.motion });
}

/* ------------------------------------------------------------------ */
/* progress + navigation                                               */
/* ------------------------------------------------------------------ */

function onScroll() {
  const max = document.documentElement.scrollHeight - window.innerHeight;
  const p = max > 0 ? clamp(window.scrollY / max, 0, 1) : 0;
  $("#progressBar").style.width = `${(p * 100).toFixed(2)}%`;
  $("#topbar").classList.toggle("is-scrolled", window.scrollY > 40);
}

function goTo(n) {
  const el = document.getElementById(`ch-${n}`);
  if (el) el.scrollIntoView({ behavior: state.motion ? "smooth" : "auto", block: "start" });
  closeContents();
}

function step(dir) {
  const n = clamp(state.active + dir, 1, state.chapters.length);
  if (n !== state.active) goTo(n);
}

function currentChapter() {
  return state.chapters.find((c) => c.n === state.active) || state.chapters[0];
}

/* ------------------------------------------------------------------ */
/* contents                                                            */
/* ------------------------------------------------------------------ */

function openContents() {
  const el = $("#contents");
  el.hidden = false;
  requestAnimationFrame(() => el.classList.add("open"));
  document.body.classList.add("is-locked");
  $("#contentsBtn").setAttribute("aria-expanded", "true");
  state.contentsOpen = true;
  el.scrollTop = 0;
  closeDrawer();
}

function closeContents() {
  const el = $("#contents");
  if (!state.contentsOpen) return;
  el.classList.remove("open");
  $("#contentsBtn").setAttribute("aria-expanded", "false");
  state.contentsOpen = false;
  document.body.classList.remove("is-locked");
  setTimeout(() => {
    if (!state.contentsOpen) el.hidden = true;
  }, 380);
}

/* ------------------------------------------------------------------ */
/* drawer                                                              */
/* ------------------------------------------------------------------ */

function openDrawer(n) {
  const c = state.chapters.find((x) => x.n === n);
  if (!c) return;
  state.active = n;
  state.drawerChapter = n;
  state.drawerOpen = true;

  $("#drawerEyebrow").textContent = `Modify · ${pad(c.n)}`;
  $("#drawerTitle").textContent = c.art || "Artifact";
  $("#drawerSpec").textContent = c.spec || "";

  const prompts = (c.prompts || []).slice(0, 5);
  $("#drawerPrompts").innerHTML = prompts
    .map(
      (p, i) => `<li><button type="button" data-prompt="${escapeAttr(p)}">
        <span class="n">${pad(i + 1)}</span><span>${escapeHtml(p)}</span>
        <span class="copy">copy</span></button></li>`
    )
    .join("");

  const stage = mount(n);
  syncSliders(stage);

  const drawer = $("#drawer");
  drawer.classList.add("open");
  drawer.setAttribute("aria-hidden", "false");
  $("#scrim").hidden = false;
  requestAnimationFrame(() => $("#scrim").classList.add("open"));
  document.body.classList.add("is-locked");
  $("#drawerClose").focus({ preventScroll: true });
}

function closeDrawer() {
  if (!state.drawerOpen) return;
  state.drawerOpen = false;
  const drawer = $("#drawer");
  drawer.classList.remove("open");
  drawer.setAttribute("aria-hidden", "true");
  const scrim = $("#scrim");
  scrim.classList.remove("open");
  setTimeout(() => {
    if (!state.drawerOpen) scrim.hidden = true;
  }, 340);
  document.body.classList.remove("is-locked");
}

function syncSliders(stage) {
  const params = stage ? stage.params : { tempo: 1, density: 1, drift: 0.5 };
  $("#pTempo").value = params.tempo;
  $("#pDensity").value = params.density;
  $("#pDrift").value = params.drift;
  $("#outTempo").textContent = Number(params.tempo).toFixed(2);
  $("#outDensity").textContent = Number(params.density).toFixed(2);
  $("#outDrift").textContent = Number(params.drift).toFixed(2);
}

function bindControls() {
  const apply = (key, value) => {
    const stage = state.stages.get(state.drawerChapter);
    if (stage) stage.params[key] = value;
    const out = { tempo: "#outTempo", density: "#outDensity", drift: "#outDrift" }[key];
    $(out).textContent = Number(value).toFixed(2);
  };

  $("#pTempo").addEventListener("input", (e) => apply("tempo", Number(e.target.value)));
  $("#pDensity").addEventListener("input", (e) => apply("density", Number(e.target.value)));
  $("#pDrift").addEventListener("input", (e) => apply("drift", Number(e.target.value)));

  $("#reseed").addEventListener("click", () => {
    const stage = state.stages.get(state.drawerChapter);
    if (stage) stage.reseed();
  });

  $("#drawerPrompts").addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-prompt]");
    if (!btn) return;
    const text = btn.dataset.prompt;
    try {
      await navigator.clipboard.writeText(text);
      btn.classList.add("copied");
      btn.querySelector(".copy").textContent = "copied";
      setTimeout(() => {
        btn.classList.remove("copied");
        btn.querySelector(".copy").textContent = "copy";
      }, 1400);
    } catch {
      btn.querySelector(".copy").textContent = "select";
      const range = document.createRange();
      range.selectNodeContents(btn.querySelector("span:nth-child(2)"));
      const sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
    }
  });
}

/* ------------------------------------------------------------------ */
/* events                                                              */
/* ------------------------------------------------------------------ */

function bindGlobal() {
  window.addEventListener("scroll", onScroll, { passive: true });
  window.addEventListener("resize", onScroll);

  $("#contentsBtn").addEventListener("click", () =>
    state.contentsOpen ? closeContents() : openContents()
  );
  $("#contentsClose").addEventListener("click", closeContents);
  $("#drawerClose").addEventListener("click", closeDrawer);
  $("#scrim").addEventListener("click", closeDrawer);

  document.addEventListener("click", (e) => {
    const modify = e.target.closest("[data-modify]");
    if (modify) {
      e.preventDefault();
      openDrawer(Number(modify.dataset.modify));
      return;
    }
    const jump = e.target.closest("[data-jump]");
    if (jump) {
      e.preventDefault();
      goTo(Number(jump.dataset.jump));
      history.replaceState(null, "", `#ch-${jump.dataset.jump}`);
    }
  });

  $("#contents").addEventListener("click", (e) => {
    if (e.target.id === "contents") closeContents();
  });

  document.addEventListener("keydown", (e) => {
    const tag = (e.target.tagName || "").toLowerCase();
    const typing = tag === "input" || tag === "textarea" || tag === "select";

    if (e.key === "Escape") {
      if (state.drawerOpen) closeDrawer();
      else if (state.contentsOpen) closeContents();
      return;
    }
    if (typing) return;

    if (e.key === "ArrowRight") {
      e.preventDefault();
      step(1);
    } else if (e.key === "ArrowLeft") {
      e.preventDefault();
      step(-1);
    } else if (e.key.toLowerCase() === "c") {
      e.preventDefault();
      state.contentsOpen ? closeContents() : openContents();
    } else if (e.key.toLowerCase() === "m") {
      e.preventDefault();
      openDrawer(state.active);
    }
  });

  $("#motionBtn").addEventListener("click", () => {
    state.motion = !state.motion;
    $("#motionBtn").setAttribute("aria-pressed", String(state.motion));
    $("#motionBtn").textContent = state.motion ? "Motion" : "Still";
    for (const n of [...state.stages.keys()]) unmount(n);
    mountCover();
    observeArtifacts();
  });

  window.addEventListener("hashchange", () => {
    const m = location.hash.match(/^#ch-(\d+)/);
    if (m) goTo(Number(m[1]));
  });
}

/* ------------------------------------------------------------------ */
/* boot                                                                */
/* ------------------------------------------------------------------ */

async function boot() {
  const chapters = await loadChapters();
  state.chapters = chapters;

  if (!chapters.length) {
    $("#book").insertAdjacentHTML(
      "beforeend",
      `<p class="noscript">The chapters have not been written yet.</p>`
    );
    return;
  }

  render(chapters);
  bindGlobal();
  bindControls();
  mountCover();
  observeArtifacts();
  onScroll();

  $("#motionBtn").setAttribute("aria-pressed", String(state.motion));
  $("#motionBtn").textContent = state.motion ? "Motion" : "Still";

  const m = location.hash.match(/^#ch-(\d+)/);
  if (m) {
    const n = Number(m[1]);
    state.active = clamp(n, 1, chapters.length);
    requestAnimationFrame(() => {
      document.getElementById(`ch-${n}`)?.scrollIntoView({ block: "start" });
    });
  }

  document.documentElement.dataset.chapters = String(chapters.length);
  document.body.classList.add("ready");
}

boot();
