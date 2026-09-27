"use strict";
// Invariant · Live. Everything drawn here comes from /api/state.json and
// /api/graph/<model>.json, which the server reads from GitHub, CI's
// receipts and TLC. Nothing is typed by hand.
(() => {
  const $ = (s, el = document) => el.querySelector(s);
  const REDUCED = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const nf = new Intl.NumberFormat("en-US");

  // The canvas can't read CSS variables, so the theme's colors are read
  // from them whenever the theme changes.
  const V = {};
  const EDGE_HUES = {
    dark: ["111,168,255", "150,140,255", "230,237,243", "120,200,235", "190,160,255", "160,190,220", "250,210,160"],
    light: ["47,111,235", "111,76,210", "14,17,22", "20,130,170", "140,90,200", "70,90,120", "190,120,40"],
  };
  function readTheme() {
    const cs = getComputedStyle(document.documentElement);
    for (const k of ["ink", "accent", "bug", "glow"]) V[k] = cs.getPropertyValue(`--${k}-rgb`).trim();
    V.light = document.documentElement.dataset.theme === "light";
    V.edges = V.light ? EDGE_HUES.light : EDGE_HUES.dark;
  }
  readTheme();
  const rgba = (k, a) => `rgba(${V[k]},${a})`;

  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : s);
  const langName = { go: "Go", typescript: "TypeScript", python: "Python" };

  // ---------- time ----------
  const T = (ts) => (ts ? new Date(ts).getTime() : 0);
  function ago(ts) {
    const s = Math.max(0, (Date.now() - T(ts)) / 1000);
    if (s < 10) return "just now";
    if (s < 60) return `${Math.floor(s)}s ago`;
    if (s < 3600) return `${Math.floor(s / 60)} min ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
    const d = Math.floor(s / 86400);
    return d === 1 ? "yesterday" : `${d} days ago`;
  }
  function dur(sec) {
    sec = Math.max(0, Math.round(sec));
    if (sec < 60) return `${sec}s`;
    const m = Math.floor(sec / 60), s = sec % 60;
    if (m < 60) return s ? `${m}m ${s}s` : `${m}m`;
    const h = Math.floor(m / 60), mm = m % 60;
    if (h < 48) return mm ? `${h}h ${mm}m` : `${h}h`;
    const d = Math.floor(h / 24), hh = h % 24;
    return hh ? `${d}d ${hh}h` : `${d}d`;
  }
  const stamp = (ts) => new Date(ts).toLocaleString([], { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
  function tick() {
    document.querySelectorAll("[data-ago]").forEach((el) => (el.textContent = ago(el.dataset.ago)));
    document.querySelectorAll("[data-since]").forEach((el) => (el.textContent = dur((Date.now() - T(el.dataset.since)) / 1000)));
    if (state) {
      const u = $("#updated");
      u.textContent = `updated ${ago(state.generatedAt)}` + (state.stale?.length ? " · some sources stale" : "");
      u.classList.toggle("stale", !!state.stale?.length);
    }
  }

  // ---------- the loop ----------
  let state = null;
  const seen = {};
  function changed(key, value) {
    const js = JSON.stringify(value);
    if (seen[key] === js) return false;
    seen[key] = js;
    return true;
  }
  async function load() {
    let wait = 15000;
    try {
      const r = await fetch("/api/state.json", { cache: "no-store" });
      if (r.status === 503) wait = 3000;
      else if (!r.ok) throw new Error(`HTTP ${r.status}`);
      else {
        state = await r.json();
        render(state);
      }
    } catch (e) {
      $("#updated").textContent = "can't reach the server; retrying";
    }
    setTimeout(load, wait);
  }

  function render(s) {
    renderTop(s);
    if (changed("now", [s.now, s.factory, s.main, s.receipts?.id])) renderNow(s);
    if (changed("orbit", [s.issues.map((i) => [i.number, i.stage, i.open, i.title]), s.factory.running, s.totals.merged])) orbit.update(s);
    if (changed("numbers", [s.totals, s.who.ratified])) renderNumbers(s);
    if (changed("models", s.projects.map((p) => [p.model, p.graph, p.states]))) renderTabs(s);
    if (changed("lanes", s.issues)) renderLanes(s.issues);
    if (changed("projects", s.projects)) renderProjects(s);
    if (changed("road", s.slices)) renderRoad(s.slices);
    if (changed("who", [s.who, s.decisions.status])) renderWho(s);
    if (changed("decisions", s.decisions)) renderDecisions(s.decisions);
    if (changed("activity", s.activity)) renderActivity(s.activity);
    tick();
  }

  // ---------- top and now ----------
  function renderTop(s) {
    const f = $("#factory");
    f.classList.toggle("on", !!s.factory.running);
    f.innerHTML = `<i></i>${s.factory.running ? "Factory on" : "Factory off"}`;
    f.title = s.factory.running && s.factory.started ? `Watching ${s.repo} since ${stamp(s.factory.started)}` : "The watcher isn't running. It runs when @gitdek starts it.";
  }

  function renderNow(s) {
    const n = s.now, h = $("#headline");
    const html = esc(n.headline).replace(/#(\d+)/g, '<span class="n">#$1</span>');
    if (h.innerHTML !== html) {
      h.innerHTML = html;
      h.classList.remove("swap");
      void h.offsetWidth;
      h.classList.add("swap");
    }
    const working = n.waitingOn === "factory" || n.waitingOn === "ci";
    h.classList.toggle("working", working && !REDUCED);
    document.title = `${n.headline} · Invariant`;

    const d = $("#detail");
    const lastMerged = s.issues.find((i) => i.stage === "merged");
    if (n.stage === "idle" && lastMerged) {
      const when = lastMerged.closed ? `, <span data-ago="${esc(lastMerged.closed)}"></span>` : "";
      d.innerHTML = `Last merged <b>#${lastMerged.pr}</b> for #${lastMerged.number}, <q>${esc(lastMerged.title)}</q>${when}.`;
    } else {
      d.innerHTML = n.detail ? `<q>${esc(n.detail)}</q>` : "";
    }

    const chips = [];
    if (n.stage === "idle") {
      chips.push(s.factory.running ? `<span class="chip factory pulse"><i></i>Watching for issues</span>` : `<span class="chip"><i></i>The watcher is off</span>`);
      chips.push(`<span class="chip holds"><i></i>${s.totals.badMerges} bad merges in ${s.totals.merged}</span>`);
    } else {
      const w = { people: ["people", "Waiting on a person"], ci: ["ci pulse", "CI's gate is running"], factory: ["factory pulse", "The factory is working"] }[n.waitingOn] || ["", n.stage];
      chips.push(`<span class="chip ${w[0]}"><i></i>${w[1]}</span>`);
      if (n.since) chips.push(`<span class="chip">for&nbsp;<span data-since="${esc(n.since)}"></span></span>`);
    }
    $("#nowmeta").innerHTML = chips.join("");

    const rows = [];
    if (s.main) {
      const m = s.main;
      let gate = `<span class="muted">no gate run yet</span>`;
      if (m.gate) {
        const g = m.gate;
        if (g.status !== "completed") gate = `<span class="run">● gate running</span> for <span data-since="${esc(g.started)}"></span>`;
        else if (g.conclusion === "success") gate = `<span class="ok">✓ gate passed</span> in ${dur((T(g.updated) - T(g.started)) / 1000)}`;
        else gate = `<span class="bad">✕ gate ${esc(g.conclusion)}</span>`;
      }
      rows.push(`<div><span class="k">Main</span><span><code>${esc(m.sha.slice(0, 7))}</code> ${esc(m.title)} · @${esc(m.by)} · <span data-ago="${esc(m.at)}"></span></span></div>`);
      rows.push(`<div><span class="k">CI</span><span>${gate}</span></div>`);
    }
    if (s.receipts) rows.push(`<div><span class="k">Receipts</span><span>from CI's gate on <code>${esc(s.receipts.sha.slice(0, 7))}</code>, <span data-ago="${esc(s.receipts.updated)}"></span></span></div>`);
    $("#mainline").innerHTML = rows.join("");
  }

  // ---------- the pipeline orbit ----------
  const STATIONS = [
    { key: "queued", name: "Issue", sub: "opened", color: "ink2" },
    { key: "asking", name: "Ask", sub: "people decide", color: "people" },
    { key: "ratifying", name: "Ratify", sub: "people sign", color: "people" },
    { key: "building", name: "Build", sub: "factory", color: "ink" },
    { key: "gate", name: "Gate", sub: "CI checks", color: "ci" },
    { key: "merged", name: "Merge", sub: "proved", color: "accent" },
  ];
  const ORBIT_R = 160;
  const stationAngle = (i) => -Math.PI / 2 + (i * Math.PI) / 3;
  const orbit = {
    built: false,
    angles: new Map(),
    build() {
      const el = $("#orbit");
      let st = "";
      STATIONS.forEach((s, i) => {
        const a = stationAngle(i), x = ORBIT_R * Math.cos(a), y = ORBIT_R * Math.sin(a);
        const lx = (ORBIT_R + 34) * Math.cos(a), ly = (ORBIT_R + 34) * Math.sin(a);
        const anchor = Math.abs(lx) < 20 ? "middle" : lx > 0 ? "start" : "end";
        st += `<g class="station s-${s.color}" data-key="${s.key}">
          <circle class="halo" cx="${x}" cy="${y}" r="9" fill="none" stroke-width="2" opacity="0"/>
          <circle class="dot" cx="${x}" cy="${y}" r="7"/>
          <text x="${lx}" y="${ly - 2}" text-anchor="${anchor}">${s.name}</text>
          <text class="sub" x="${lx}" y="${ly + 13}" text-anchor="${anchor}">${s.sub}</text></g>`;
      });
      el.innerHTML = `<svg viewBox="-250 -250 500 500" aria-hidden="true">
        <defs>
          <filter id="glow" x="-100%" y="-100%" width="300%" height="300%"><feGaussianBlur stdDeviation="4" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>
          <radialGradient id="coreGlow"><stop offset="0" style="stop-color:var(--accent);stop-opacity:.3"/><stop offset="1" style="stop-color:var(--accent);stop-opacity:0"/></radialGradient>
        </defs>
        <circle r="130" fill="url(#coreGlow)"/>
        <circle class="ring2" r="${ORBIT_R}"/>
        <circle class="ring" r="${ORBIT_R}"/>
        <g id="moons"></g>
        <g id="ghost"></g>
        ${st}
        <circle class="core-pulse" r="13"/><circle class="core" r="13"/>
        <text class="core-num" id="coreNum" y="56" text-anchor="middle">0</text>
        <text class="core-label" id="coreLabel" y="76" text-anchor="middle">merged</text>
        <g id="dots"></g>
      </svg>`;
      this.built = true;
    },
    update(s) {
      if (!this.built) this.build();
      $("#coreNum").textContent = s.totals.merged;
      $("#coreLabel").textContent = s.totals.merged === 1 ? "merged, proved" : "merged, proved";
      // The ghost: the factory's attention, circling. Slow and dim when it's off.
      const period = s.factory.running ? 14 : 36, op = s.factory.running ? 0.95 : 0.35;
      const path = `M0,${-ORBIT_R} A${ORBIT_R},${ORBIT_R} 0 1 1 -0.01,${-ORBIT_R}`;
      let g = "";
      if (!REDUCED) {
        [0, 0.18, 0.34, 0.48].forEach((lag, i) => {
          g += `<circle r="${[4.5, 3.4, 2.5, 1.8][i]}" class="ghost" opacity="${op * [1, 0.5, 0.3, 0.15][i]}"><animateMotion dur="${period}s" begin="-${lag + 1}s" repeatCount="indefinite" path="${path}"/></circle>`;
        });
      }
      $("#ghost").innerHTML = g;
      // Merged issues orbit the invariant like moons.
      const merged = s.issues.filter((i) => i.stage === "merged").slice().reverse();
      $("#moons").innerHTML = merged.map((is, i) => {
        const r = 40 + i * 13, per = 22 + i * 9, start = (i * 137) % 360;
        const spin = REDUCED ? "" : `<animateTransform attributeName="transform" type="rotate" from="${start}" to="${start + 360}" dur="${per}s" repeatCount="indefinite"/>`;
        return `<g transform="rotate(${start})"><circle class="moonring" r="${r}"/>${spin}<circle class="moon" cx="${r}" cy="0" r="3.2"><title>#${is.number} ${esc(is.title)}: merged #${is.pr}</title></circle></g>`;
      }).join("");
      // Open issues sit at their stage.
      const open = s.issues.filter((i) => i.open);
      const active = new Set(open.map((i) => (i.stage === "review" ? "gate" : i.stage)));
      document.querySelectorAll("#orbit .station").forEach((el) => {
        el.classList.toggle("active", active.has(el.dataset.key));
        el.querySelector(".halo").setAttribute("opacity", active.has(el.dataset.key) ? "1" : "0");
      });
      const dots = $("#dots");
      const counts = {};
      const want = open.map((is) => {
        const k = is.stage === "review" ? "gate" : is.stage;
        const idx = Math.max(0, STATIONS.findIndex((st) => st.key === k));
        const nth = (counts[k] = (counts[k] || 0) + 1) - 1;
        return { is, angle: stationAngle(idx) + nth * 0.14 };
      });
      dots.innerHTML = want.map(({ is }) => {
        const bug = is.stage === "review";
        return `<g data-n="${is.number}"><circle r="7" class="issue-dot${bug ? " bug" : ""}"/><text class="issue-label" x="12" y="4">#${is.number}</text><title>#${is.number} ${esc(is.title)}</title></g>`;
      }).join("");
      want.forEach(({ is, angle }) => {
        const from = this.angles.has(is.number) ? this.angles.get(is.number) : stationAngle(0);
        this.angles.set(is.number, angle);
        const g = dots.querySelector(`[data-n="${is.number}"]`);
        const place = (a) => g.setAttribute("transform", `translate(${ORBIT_R * Math.cos(a)},${ORBIT_R * Math.sin(a)})`);
        if (REDUCED || from === angle) return place(angle);
        let to = angle;
        while (to < from) to += 2 * Math.PI;
        const t0 = performance.now();
        const step = (now) => {
          const t = Math.min(1, (now - t0) / 1400), e = 1 - Math.pow(1 - t, 3);
          place(from + (to - from) * e);
          if (t < 1) requestAnimationFrame(step);
        };
        requestAnimationFrame(step);
      });
    },
  };

  // ---------- numbers ----------
  function countTo(el, to, fmtFn = (v) => nf.format(Math.round(v))) {
    const from = Number(el.dataset.v || 0);
    el.dataset.v = to;
    if (REDUCED || from === to) return (el.textContent = fmtFn(to));
    const t0 = performance.now();
    const step = (now) => {
      const t = Math.min(1, (now - t0) / 1600), e = 1 - Math.pow(1 - t, 4);
      el.textContent = fmtFn(from + (to - from) * e);
      if (t < 1) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  }
  function renderNumbers(s) {
    const t = s.totals;
    const tiles = [
      { k: "bad", cls: "holds", v: t.badMerges, l: "merges without a green gate", d: `CI's gate passed on the exact head of all ${t.mergesChecked} pull requests the factory merged. ${t.badLocks === 0 ? `All ${t.locksChecked} factory locks are exactly what a person ratified.` : `${t.badLocks} locks differ from what was ratified.`}` },
      { k: "states", v: t.states, l: "states TLC explored", d: `across ${t.models} models, exhaustively, within the ratified bounds` },
      { k: "stmts", v: t.statements, l: "statements people ratified", d: "each pinned by hash, so the factory can't change them" },
      { k: "bugs", v: t.bugsCaught, of: t.bugs, l: "planted bugs caught", d: "each one breaks an invariant, and TLC finds the step" },
      { k: "proved", v: t.proved, l: "functions proved", d: "by Gobra and Nagini, against contracts that restate the model" },
      { k: "dec", v: t.decisions, l: "decisions logged", d: `${s.who.ratified} ratified by @gitdek, ${s.who.logged} decided by agents as they built` },
    ];
    const box = $("#numbers");
    if (!box.children.length) {
      box.innerHTML = tiles.map((x) => `<div class="num ${x.cls || ""}" data-k="${x.k}"><div class="v">0</div><div class="l">${esc(x.l)}</div><div class="s"></div></div>`).join("");
    }
    tiles.forEach((x) => {
      const el = box.querySelector(`[data-k="${x.k}"]`);
      el.querySelector(".s").textContent = x.d;
      const v = el.querySelector(".v");
      if (x.of !== undefined) countTo(v, x.v, (n) => `${nf.format(Math.round(n))}/${nf.format(x.of)}`);
      else countTo(v, x.v);
    });
  }

  // ---------- the state graph ----------
  function hash01(i) {
    let x = (i + 1) * 2654435761;
    x ^= x >>> 13; x = Math.imul(x, 1274126177); x ^= x >>> 16;
    return (x >>> 0) / 4294967296;
  }
  // layoutGraph puts each state on a ring by its distance from Init, and
  // orders each ring by where the states that lead to it sit.
  function layoutGraph(g) {
    const n = g.states;
    const out = Array.from({ length: n }, () => []), inn = Array.from({ length: n }, () => []);
    for (let i = 0; i < g.edges.length; i += 3) {
      const a = g.edges[i], b = g.edges[i + 1];
      if (a !== b) { out[a].push(b); inn[b].push(a); }
    }
    const depth = new Int32Array(n).fill(-1), q = [];
    for (const s of g.init) { depth[s] = 0; q.push(s); }
    for (let h = 0; h < q.length; h++) for (const w of out[q[h]]) if (depth[w] < 0) { depth[w] = depth[q[h]] + 1; q.push(w); }
    let D = 1;
    for (let i = 0; i < n; i++) D = Math.max(D, depth[i]);
    for (let i = 0; i < n; i++) if (depth[i] < 0) depth[i] = D;
    const rings = Array.from({ length: D + 1 }, () => []);
    for (let i = 0; i < n; i++) rings[depth[i]].push(i);
    const angle = new Float64Array(n);
    rings[0].forEach((v, i) => (angle[v] = -Math.PI / 2 + (2 * Math.PI * i) / rings[0].length));
    for (let d = 1; d <= D; d++) {
      const ring = rings[d], pref = new Float64Array(n);
      for (const v of ring) {
        let sx = 0, sy = 0;
        for (const p of inn[v]) if (depth[p] === d - 1) { sx += Math.cos(angle[p]); sy += Math.sin(angle[p]); }
        pref[v] = sx || sy ? Math.atan2(sy, sx) : hash01(v) * 2 * Math.PI;
      }
      ring.sort((a, b) => pref[a] - pref[b]);
      const m = ring.length;
      let ox = 0, oy = 0;
      ring.forEach((v, i) => { const df = pref[v] - (2 * Math.PI * i) / m; ox += Math.cos(df); oy += Math.sin(df); });
      const off = Math.atan2(oy, ox);
      ring.forEach((v, i) => (angle[v] = off + (2 * Math.PI * i) / m));
    }
    const jit = new Float32Array(n);
    if (n > 1500) for (let i = 0; i < n; i++) jit[i] = (hash01(i) - 0.5) * 0.38;
    return { n, out, depth, D, angle, jit, rings };
  }
  function place(L, R0, R1, into) {
    const gap = (R1 - R0) / L.D;
    for (let i = 0; i < L.n; i++) {
      const r = R0 + gap * L.depth[i] + gap * L.jit[i];
      into.x[i] = r * Math.cos(L.angle[i]);
      into.y[i] = r * Math.sin(L.angle[i]);
    }
    into.gap = gap;
  }

  const graphs = new Map(); // model key → graph JSON (or a promise)
  function getGraph(key) {
    if (!graphs.has(key)) graphs.set(key, fetch(`/api/graph/${key}.json`).then((r) => (r.ok ? r.json() : Promise.reject(r.status))).then((g) => { g.layout = layoutGraph(g); return g; }));
    return graphs.get(key);
  }

  const cosmos = {
    wrap: $("#cosmos"), cv: $("#sky"), tip: $("#tip"),
    g: null, key: "", meta: null, visible: false, raf: 0, theta: 0, last: 0,
    walkers: [], twinkles: [], bug: null, bugIndex: 0, nextBug: 0, hover: -1,
    x: null, y: null,
    init() {
      this.ctx = this.cv.getContext("2d");
      new ResizeObserver(() => { clearTimeout(this.rt); this.rt = setTimeout(() => this.resize(), 120); }).observe(this.wrap);
      new IntersectionObserver((es) => { this.visible = es[0].isIntersecting; this.kick(); }, { threshold: 0.05 }).observe(this.wrap);
      document.addEventListener("visibilitychange", () => this.kick());
      this.cv.addEventListener("pointermove", (e) => this.pointer(e));
      this.cv.addEventListener("pointerleave", () => { this.hover = -1; this.tip.classList.remove("show"); });
      $("#replay").addEventListener("click", () => this.replay(true));
    },
    async show(key, meta) {
      this.key = key;
      this.meta = meta;
      this.caption();
      let g;
      try { g = await getGraph(key); } catch { return; }
      if (this.key !== key) return;
      this.g = g;
      this.bug = null;
      $("#bugcard").classList.remove("show");
      $("#replay").style.display = g.bugs?.length ? "" : "none";
      this.bugIndex = 0;
      this.nextBug = performance.now() + 6000;
      this.resize();
      this.caption();
    },
    caption() {
      const m = this.meta;
      if (!m) return;
      const inv = m.statements.filter((s) => s.kind === "invariant").map((s) => s.name);
      $("#caption").innerHTML = `<div class="t">${esc(cap(m.name))}</div>
        <div class="m">${nf.format(m.states)} states · ${m.depth} steps deep · ${esc(Object.entries(m.bounds || {}).map(([k, v]) => `${k} = ${v}`).join(", "))}</div>
        <div class="h">In every one of them, <b>${esc(inv.slice(0, 4).join(", "))}</b>${inv.length > 4 ? ` and ${inv.length - 4} more` : ""} hold${inv.length === 1 ? "s" : ""}.</div>`;
    },
    resize() {
      const W = this.wrap.clientWidth, H = this.wrap.clientHeight;
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      this.W = W; this.H = H; this.dpr = dpr;
      this.cv.width = Math.round(W * dpr); this.cv.height = Math.round(H * dpr);
      if (!this.g) return;
      const L = this.g.layout;
      // Leave room for the caption, the legend, and the step a planted bug
      // takes out of the model.
      const wide = W > 900, out = 46;
      const top = wide ? 26 : 112, bottom = wide ? 40 : 30;
      this.cx = wide ? W * 0.58 : W / 2;
      this.cy = top + (H - top - bottom) / 2;
      this.R1 = Math.max(50, Math.min((H - top - bottom) / 2 - out, W - this.cx - out - 12, this.cx - out - 12));
      this.R0 = L.D > 1 ? this.R1 * 0.07 : this.R1 * 0.3;
      this.x = new Float32Array(L.n); this.y = new Float32Array(L.n);
      const pos = { x: this.x, y: this.y };
      place(L, this.R0, this.R1, pos);
      this.gapR = pos.gap;
      this.prerender();
      this.grid();
      this.resetWalkers();
      this.kick();
      if (REDUCED) this.frame(performance.now());
    },
    prerender() {
      const g = this.g, L = g.layout, dpr = this.dpr;
      const size = Math.ceil((this.R1 + 60) * 2);
      const off = (this.off = document.createElement("canvas"));
      off.width = off.height = Math.round(size * dpr);
      this.offSize = size;
      const c = off.getContext("2d");
      c.scale(dpr, dpr);
      c.translate(size / 2, size / 2);
      c.lineWidth = 1;
      for (let d = 0; d <= L.D; d++) {
        c.beginPath();
        c.arc(0, 0, this.R0 + this.gapR * d, 0, 2 * Math.PI);
        c.strokeStyle = rgba("ink", (L.n > 2500 ? 0.06 : 0.035) * (V.light ? 1.4 : 1));
        c.stroke();
      }
      const big = L.n > 2500;
      const E = g.edges;
      c.lineWidth = big ? 0.5 : 0.7;
      for (let i = 0; i < E.length; i += 3) {
        const a = E[i], b = E[i + 1];
        if (a === b) continue;
        const hue = V.edges[E[i + 2] % V.edges.length];
        c.strokeStyle = big ? `rgba(${hue},${V.light ? 0.03 : 0.022})` : `rgba(${hue},${V.light ? 0.22 : 0.16})`;
        c.beginPath();
        c.moveTo(this.x[a], this.y[a]);
        if (big) c.lineTo(this.x[b], this.y[b]);
        else c.quadraticCurveTo((this.x[a] + this.x[b]) * 0.41, (this.y[a] + this.y[b]) * 0.41, this.x[b], this.y[b]);
        c.stroke();
      }
      c.fillStyle = rgba("ink", big ? (V.light ? 0.34 : 0.42) : V.light ? 0.78 : 0.9);
      const r = big ? 0.62 : L.n > 600 ? 1.5 : 2.1;
      for (let i = 0; i < L.n; i++) {
        if (big) c.fillRect(this.x[i] - r, this.y[i] - r, r * 2, r * 2);
        else { c.beginPath(); c.arc(this.x[i], this.y[i], r, 0, 2 * Math.PI); c.fill(); }
      }
      c.strokeStyle = rgba("glow", 0.9);
      c.lineWidth = 1.2;
      for (const s of g.init) { c.beginPath(); c.arc(this.x[s], this.y[s], big ? 3.5 : 4.5, 0, 2 * Math.PI); c.stroke(); }
    },
    grid() {
      const cell = 14, grid = new Map();
      for (let i = 0; i < this.x.length; i++) {
        const k = `${Math.floor(this.x[i] / cell)},${Math.floor(this.y[i] / cell)}`;
        if (!grid.has(k)) grid.set(k, []);
        grid.get(k).push(i);
      }
      this.cell = cell;
      this.cells = grid;
    },
    resetWalkers() {
      const L = this.g.layout, count = L.n > 2500 ? 7 : L.n > 200 ? 4 : 3;
      this.walkers = [];
      for (let i = 0; i < count; i++) this.walkers.push(this.newWalker(i * 380));
      this.twinkles = [];
    },
    newWalker(delay = 0) {
      const g = this.g, start = g.init[Math.floor(Math.random() * g.init.length)];
      return { cur: start, next: this.pick(start), t0: performance.now() + delay, trail: [], steps: 0 };
    },
    pick(v) {
      const o = this.g.layout.out[v];
      return o.length ? o[Math.floor(Math.random() * o.length)] : -1;
    },
    kick() {
      if (REDUCED || !this.g) return;
      const run = this.visible && !document.hidden;
      if (run && !this.raf) {
        this.last = performance.now();
        const loop = (now) => { this.raf = requestAnimationFrame(loop); this.frame(now); };
        this.raf = requestAnimationFrame(loop);
      } else if (!run && this.raf) {
        cancelAnimationFrame(this.raf);
        this.raf = 0;
      }
    },
    replay(user) {
      const bugs = this.g?.bugs;
      if (!bugs?.length) return;
      if (REDUCED) return this.showBugStatic(bugs[this.bugIndex++ % bugs.length]);
      const b = bugs[this.bugIndex++ % bugs.length];
      this.bug = { b, t0: performance.now(), stepMs: 700 };
      $("#bugcard").classList.remove("show");
      this.nextBug = performance.now() + (user ? 20000 : 17000);
    },
    showBugStatic(b) {
      this.bug = { b, t0: -1e9, stepMs: 700, fixed: true };
      this.frame(performance.now());
      this.bugCard(b);
    },
    bugCard(b) {
      const card = $("#bugcard");
      card.innerHTML = `<div class="k">Planted bug · caught</div><div class="t">${esc(b.label)}</div><div class="d">${esc(b.says)}</div>
        <div class="c">After ${b.steps} steps, <code>${esc(b.action)}</code> breaks <b>${esc(b.violated)}</b>. TLC finds that step, so the gate refuses any code that could take it.</div>`;
      card.classList.add("show");
    },
    escapePoint(i) {
      // Where a bug leaves the model: straight out from its last state.
      const x = this.x[i], y = this.y[i];
      const r = Math.hypot(x, y) || 1, a = r > 1 ? Math.atan2(y, x) : -Math.PI / 2;
      const R = this.R1 + 40;
      return [R * Math.cos(a), R * Math.sin(a)];
    },
    frame(now) {
      const c = this.ctx, g = this.g;
      if (!g || !this.off) return;
      const dt = Math.min(64, now - this.last);
      this.last = now;
      if (!REDUCED) this.theta += dt * ((2 * Math.PI) / 260000);
      c.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
      c.clearRect(0, 0, this.W, this.H);
      const cx = this.cx, cy = this.cy;

      const pulse = REDUCED ? 0.5 : (now % 3000) / 3000;
      const glow = c.createRadialGradient(cx, cy, 0, cx, cy, this.R0 * 3.2 + 30);
      glow.addColorStop(0, rgba("accent", V.light ? 0.2 : 0.3));
      glow.addColorStop(1, rgba("accent", 0));
      c.fillStyle = glow;
      c.fillRect(cx - this.R1, cy - this.R1, this.R1 * 2, this.R1 * 2);

      c.save();
      c.translate(cx, cy);
      c.rotate(this.theta);
      c.drawImage(this.off, -this.offSize / 2, -this.offSize / 2, this.offSize, this.offSize);

      // Twinkles: a few states brighten and fade.
      if (!REDUCED) {
        if (this.twinkles.length < Math.min(40, g.states / 6) && Math.random() < 0.5) this.twinkles.push({ i: Math.floor(Math.random() * g.states), t0: now, life: 1400 + Math.random() * 1600 });
        this.twinkles = this.twinkles.filter((t) => now - t.t0 < t.life);
        for (const t of this.twinkles) {
          const p = (now - t.t0) / t.life, a = Math.sin(p * Math.PI);
          c.fillStyle = rgba("glow", 0.75 * a);
          c.beginPath(); c.arc(this.x[t.i], this.y[t.i], 1.2 + 1.6 * a, 0, 2 * Math.PI); c.fill();
        }
      }

      const bugOn = this.bug && !this.bug.done;
      const L = g.layout, stepMs = L.n > 2500 ? 230 : 620;
      if (!REDUCED) {
        for (const w of this.walkers) {
          if (now < w.t0) continue;
          let t = (now - w.t0) / stepMs;
          while (t >= 1) {
            w.trail.push(w.cur);
            if (w.trail.length > (L.n > 2500 ? 20 : 14)) w.trail.shift();
            w.cur = w.next;
            w.steps++;
            w.t0 += stepMs;
            t -= 1;
            if (w.cur < 0 || w.steps > 60) { Object.assign(w, this.newWalker(500)); t = 0; break; }
            w.next = this.pick(w.cur);
            if (w.next < 0) { w.next = w.cur; }
          }
          if (w.cur < 0 || now < w.t0) continue;
          const e = t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2;
          const x = this.x[w.cur] + (this.x[w.next] - this.x[w.cur]) * e;
          const y = this.y[w.cur] + (this.y[w.next] - this.y[w.cur]) * e;
          const dim = bugOn ? 0.25 : 1;
          c.strokeStyle = rgba("glow", 0.35 * dim);
          c.lineWidth = 1;
          c.beginPath(); c.moveTo(this.x[w.cur], this.y[w.cur]); c.lineTo(this.x[w.next], this.y[w.next]); c.stroke();
          w.trail.forEach((v, k) => {
            const a = ((k + 1) / w.trail.length) * 0.45 * dim;
            c.fillStyle = rgba("glow", a);
            c.beginPath(); c.arc(this.x[v], this.y[v], 1.3 + (k / w.trail.length) * 1.6, 0, 2 * Math.PI); c.fill();
          });
          c.shadowColor = V.light ? rgba("glow", 0.45) : "#fff"; c.shadowBlur = (V.light ? 8 : 16) * dim;
          c.fillStyle = rgba("glow", dim);
          c.beginPath(); c.arc(x, y, L.n > 2500 ? 3.6 : 3.1, 0, 2 * Math.PI); c.fill();
          c.shadowBlur = 0;
        }
      }

      if (this.bug) this.drawBug(c, now);
      if (this.hover >= 0) {
        const h = this.hover;
        c.strokeStyle = rgba("glow", 0.55);
        c.lineWidth = 1;
        for (const w of L.out[h]) { c.beginPath(); c.moveTo(this.x[h], this.y[h]); c.lineTo(this.x[w], this.y[w]); c.stroke(); }
        c.fillStyle = rgba("glow", 1);
        c.beginPath(); c.arc(this.x[h], this.y[h], 4, 0, 2 * Math.PI); c.fill();
      }
      c.restore();

      // The invariant, fixed at the center, the only thing in the accent.
      c.fillStyle = rgba("accent", 0.45 * (1 - pulse));
      c.beginPath(); c.arc(cx, cy, 6 + 16 * pulse, 0, 2 * Math.PI); c.fill();
      c.fillStyle = rgba("accent", 1);
      c.shadowColor = rgba("accent", 1); c.shadowBlur = V.light ? 10 : 18;
      c.beginPath(); c.arc(cx, cy, 6, 0, 2 * Math.PI); c.fill();
      c.shadowBlur = 0;

      if (!REDUCED && this.g.bugs?.length && !this.bug && now > this.nextBug) this.replay(false);
    },
    drawBug(c, now) {
      const B = this.bug, b = B.b, path = b.path;
      const el = B.fixed ? 1e9 : now - B.t0;
      const stepsDone = el / B.stepMs;
      const red = (a) => rgba("bug", a);
      const n = Math.min(path.length - 1, Math.floor(stepsDone));
      c.strokeStyle = red(0.9); c.lineWidth = 2;
      c.shadowColor = rgba("bug", 1); c.shadowBlur = V.light ? 4 : 10;
      c.beginPath();
      c.moveTo(this.x[path[0]], this.y[path[0]]);
      for (let k = 1; k <= n; k++) c.lineTo(this.x[path[k]], this.y[path[k]]);
      let hx = this.x[path[n]], hy = this.y[path[n]];
      if (n < path.length - 1) {
        const t = stepsDone - n;
        hx += (this.x[path[n + 1]] - hx) * t;
        hy += (this.y[path[n + 1]] - hy) * t;
        c.lineTo(hx, hy);
      }
      c.stroke();
      for (let k = 0; k <= n; k++) { c.fillStyle = red(0.95); c.beginPath(); c.arc(this.x[path[k]], this.y[path[k]], 3.2, 0, 2 * Math.PI); c.fill(); }
      const walked = (path.length - 1) * B.stepMs;
      const last = path[path.length - 1];
      const [ex, ey] = this.escapePoint(last);
      if (el > walked) {
        const t = Math.min(1, (el - walked) / 900), e = 1 - Math.pow(1 - t, 3);
        const x = this.x[last] + (ex - this.x[last]) * e, y = this.y[last] + (ey - this.y[last]) * e;
        c.setLineDash([5, 5]);
        c.beginPath(); c.moveTo(this.x[last], this.y[last]); c.lineTo(x, y); c.stroke();
        c.setLineDash([]);
        hx = x; hy = y;
        if (t >= 1) {
          const tc = Math.min(1, (el - walked - 900) / 1300);
          c.strokeStyle = red(1 - tc); c.lineWidth = 2;
          c.beginPath(); c.arc(ex, ey, 6 + 34 * tc, 0, 2 * Math.PI); c.stroke();
          c.strokeStyle = red(1); c.lineWidth = 2.4;
          c.beginPath(); c.moveTo(ex - 7, ey - 7); c.lineTo(ex + 7, ey + 7); c.moveTo(ex + 7, ey - 7); c.lineTo(ex - 7, ey + 7); c.stroke();
          if (!B.carded) { B.carded = true; this.bugCard(b); }
        }
      }
      c.fillStyle = red(1);
      c.beginPath(); c.arc(hx, hy, 4.2, 0, 2 * Math.PI); c.fill();
      c.shadowBlur = 0;
      if (!B.fixed && el > walked + 900 + 5200) {
        this.bug = null;
        $("#bugcard").classList.remove("show");
      }
    },
    pointer(e) {
      if (!this.g || !this.x) return;
      const r = this.cv.getBoundingClientRect();
      const mx = e.clientX - r.left - this.cx, my = e.clientY - r.top - this.cy;
      const cs = Math.cos(-this.theta), sn = Math.sin(-this.theta);
      const x = mx * cs - my * sn, y = mx * sn + my * cs;
      const gx = Math.floor(x / this.cell), gy = Math.floor(y / this.cell);
      let best = -1, bd = 10 * 10;
      for (let i = -1; i <= 1; i++) for (let j = -1; j <= 1; j++) {
        for (const v of this.cells.get(`${gx + i},${gy + j}`) || []) {
          const d = (this.x[v] - x) ** 2 + (this.y[v] - y) ** 2;
          if (d < bd) { bd = d; best = v; }
        }
      }
      this.hover = best;
      const tip = this.tip;
      if (best < 0) return tip.classList.remove("show");
      const L = this.g.layout, label = this.g.labels?.[best];
      const onward = L.out[best].length;
      tip.innerHTML = `<b>State ${best + 1}</b> · ${L.depth[best]} step${L.depth[best] === 1 ? "" : "s"} from the start · ${onward ? `${onward} way${onward === 1 ? "" : "s"} onward` : "no way onward"}${label ? "\n" + esc(label.replace(/\/\\ /g, "")) : ""}`;
      const tx = Math.min(e.clientX - r.left + 14, r.width - 350), ty = Math.min(e.clientY - r.top + 14, r.height - 20 - (label ? 120 : 30));
      tip.style.left = `${Math.max(8, tx)}px`;
      tip.style.top = `${Math.max(8, ty)}px`;
      tip.classList.add("show");
      if (REDUCED) this.frame(performance.now());
    },
  };

  // Models: projects that check the same model share one graph.
  let currentModel = "";
  function models(s) {
    const byKey = new Map();
    for (const p of s.projects) {
      if (!byKey.has(p.model)) byKey.set(p.model, { key: p.model, projects: [], meta: p });
      const m = byKey.get(p.model);
      m.projects.push(p);
      if (p.factory && !m.meta.factory) m.meta = p;
    }
    const list = [...byKey.values()];
    const names = {};
    list.forEach((m) => (names[m.meta.name] = (names[m.meta.name] || 0) + 1));
    list.forEach((m) => {
      m.label = cap(m.meta.name);
      if (names[m.meta.name] > 1) {
        const suffix = m.projects.map((p) => p.dir.split("/").pop()).find((d) => /-synthesized$/.test(d));
        m.label += suffix ? " (synthesized)" : "";
      }
    });
    return list.sort((a, b) => b.meta.states - a.meta.states);
  }
  function renderTabs(s) {
    const ms = models(s);
    if (!ms.length) return;
    if (!currentModel || !ms.find((m) => m.key === currentModel)) {
      const pick = ms.find((m) => m.meta.factory && m.meta.graph) || ms.find((m) => m.meta.graph) || ms[0];
      currentModel = pick.key;
    }
    $("#tabs").innerHTML = ms.map((m) => `<button class="tab" type="button" data-key="${m.key}" aria-pressed="${m.key === currentModel}">${esc(m.label)}<span>${nf.format(m.meta.states)}</span></button>`).join("");
    $("#tabs").querySelectorAll(".tab").forEach((b) => b.addEventListener("click", () => selectModel(b.dataset.key)));
    const cur = ms.find((m) => m.key === currentModel);
    if (cur.meta.graph && cosmos.key !== currentModel) cosmos.show(currentModel, cur.meta);
    else if (!cur.meta.graph) $("#caption").innerHTML = `<div class="t">${esc(cap(cur.meta.name))}</div><div class="m">TLC is drawing this state graph…</div>`;
  }
  function selectModel(key) {
    currentModel = key;
    document.querySelectorAll("#tabs .tab").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.key === key)));
    const m = models(state).find((x) => x.key === key);
    if (m?.meta.graph) cosmos.show(key, m.meta);
  }

  // ---------- lanes ----------
  let lanesShown = false;
  function renderLanes(issues) {
    const box = $("#lanes");
    if (!issues.length) { box.innerHTML = `<p class="skeleton">No issues yet.</p>`; return; }
    const FOLD = 11; // a wait on people takes this share of the widest lane's factory time
    const maxFactory = Math.max(...issues.map((i) => i.spans.filter((s) => s.who !== "people").reduce((a, s) => a + (T(s.to) - T(s.from)) / 1000, 0)), 60);
    const unitsOf = (sp) => (sp.who === "people" ? FOLD : Math.max(0.6, ((T(sp.to) - T(sp.from)) / 1000 / maxFactory) * 100));
    const widest = Math.max(...issues.map((i) => i.spans.reduce((a, sp) => a + unitsOf(sp), 0)), 1);
    const stageChip = {
      merged: `<span class="chip holds"><i></i>merged</span>`, gate: `<span class="chip ci pulse"><i></i>CI's gate</span>`,
      building: `<span class="chip factory pulse"><i></i>building</span>`, asking: `<span class="chip people"><i></i>asking</span>`,
      ratifying: `<span class="chip people"><i></i>awaiting ratification</span>`, review: `<span class="chip bug"><i></i>needs a person</span>`,
      queued: `<span class="chip factory"><i></i>queued</span>`, closed: `<span class="chip"><i></i>closed</span>`,
    };
    box.innerHTML = issues.map((is) => {
      let acc = 0, segs = "", marks = "";
      const at = [];
      is.spans.forEach((sp, k) => {
        const w = (unitsOf(sp) / widest) * 100;
        const secs = (T(sp.to) - T(sp.from)) / 1000;
        const cls = `seg ${sp.who}${sp.open ? " open" : ""}`;
        const inner = sp.who === "people" ? `<span class="wait">${sp.open ? "waiting " : ""}${dur(secs)}</span>` : "";
        segs += `<div class="${cls}" style="width:${w.toFixed(3)}%;transition-delay:${k * 90}ms" title="${esc(sp.who === "people" ? "Waiting on a person" : sp.who === "ci" ? "CI's gate" : "The factory working")} · ${dur(secs)}">${inner}</div>`;
        at.push(acc);
        acc += w;
      });
      at.push(acc);
      is.events.forEach((e, k) => {
        const left = at[Math.min(k, at.length - 1)];
        const cls = e.who === "person" ? "person" : e.kind === "merged" ? "merged" : e.kind === "failed" ? "failed" : "factory";
        const who = e.who === "person" ? `@${e.by}` : "Invariant";
        marks += `<span class="mark ${cls}" style="left:${left.toFixed(3)}%" title="${esc(stamp(e.at))} · ${esc(who)} ${esc(e.text)}"></span>`;
      });
      const chips = [
        is.language ? `<span class="chip">${esc(langName[is.language] || is.language)}</span>` : "",
        is.amends ? `<span class="chip">amends ${esc(is.amends)}</span>` : "",
        stageChip[is.stage] || "",
      ].join("");
      return `<article class="lane">
        <div class="lane-head"><span class="no">#${is.number}</span><span class="title">${esc(is.title)}</span><span class="chips">${chips}</span></div>
        <div class="track">${segs}${marks}</div>
        <div class="lane-foot"><span>factory <b>${dur(is.factorySeconds)}</b></span><span class="p">waiting on people <b>${dur(is.peopleSeconds)}</b></span><span><b>${is.peopleComments}</b> comment${is.peopleComments === 1 ? "" : "s"} from people</span>${is.questions ? `<span><b>${is.questions}</b> question${is.questions === 1 ? "" : "s"} asked</span>` : ""}${is.statements ? `<span><b>${is.statements}</b> statements</span>` : ""}${is.pr ? `<span>pull request <b>#${is.pr}</b></span>` : ""}</div>
      </article>`;
    }).join("");
    if (lanesShown || REDUCED) box.classList.add("shown");
    else {
      const io = new IntersectionObserver((es) => {
        if (es[0].isIntersecting) { requestAnimationFrame(() => box.classList.add("shown")); lanesShown = true; io.disconnect(); }
      }, { threshold: 0.15 });
      io.observe(box);
    }
  }

  // ---------- projects ----------
  function renderProjects(s) {
    const r = s.receipts;
    $("#receipts").innerHTML = r ? `Receipts from CI's gate on <code>${esc(r.sha.slice(0, 7))}</code>` : "";
    const order = (p) => (p.factory ? 0 : 1);
    const ps = s.projects.slice().sort((a, b) => order(a) - order(b) || b.states - a.states);
    $("#projects").innerHTML = ps.map((p) => {
      const assure = p.assurance === "proved" ? "proved" : p.assurance === "tested in every state" ? "every" : "tested";
      const caught = p.bugs.filter((b) => b.caught).length;
      let evidence;
      if (p.assurance === "proved") evidence = [`${p.verified}`, "Functions proved"];
      else if ((p.visited || 0) >= p.states) evidence = ["every state", "Code reached"];
      else evidence = [`${nf.format(p.visited || 0)}/${nf.format(p.states)}`, "States the code ran"];
      let prov = "Hand-built, before the factory";
      if (p.ratified) prov = `Ratified by <b>@${esc(p.ratified.by)}</b> on #${p.ratified.issue}${p.ratified.previous ? `, amending ${esc(p.ratified.previous)}` : ""}`;
      else if (p.decision) prov = `Ratified in <b>${esc(p.decision)}</b>, by hand`;
      return `<article class="card" data-model="${p.model}" tabindex="0">
        <canvas class="thumb" data-model="${p.model}" width="150" height="150"></canvas>
        <div class="top"><span class="lang ${esc(p.language)}">${esc(langName[p.language] || p.language)}</span><span class="assure ${assure}">${esc(p.assurance)}</span></div>
        <h3>${esc(cap(p.name))}</h3>
        <p class="dir">${esc(p.dir)}</p>
        <dl class="metrics">
          <div><dt>States</dt><dd>${nf.format(p.states)}</dd></div>
          <div><dt>Statements</dt><dd>${p.statements.length}</dd></div>
          <div><dt>Bugs caught</dt><dd class="${caught === p.bugs.length ? "holds" : ""}">${caught}/${p.bugs.length}</dd></div>
          <div><dt>${esc(evidence[1])}</dt><dd>${evidence[0]}</dd></div>
        </dl>
        <p class="prov">${prov}</p>
        <p class="fp">${esc(p.fingerprint.slice(0, 19))} · ${p.passed ? '<span class="ok">passed</span>' : '<span class="bad">failed</span>'}</p>
      </article>`;
    }).join("");
    document.querySelectorAll("#projects .card").forEach((el) => {
      const go = () => { selectModel(el.dataset.model); $("#states").scrollIntoView({ behavior: REDUCED ? "auto" : "smooth" }); };
      el.addEventListener("click", go);
      el.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
    });
    thumbs(s);
  }
  function thumbs(s) {
    const ready = new Set(s.projects.filter((p) => p.graph).map((p) => p.model));
    document.querySelectorAll("canvas.thumb").forEach((cv) => {
      if (!ready.has(cv.dataset.model)) return;
      getGraph(cv.dataset.model).then((g) => drawThumb(cv, g)).catch(() => {});
    });
  }
  function drawThumb(cv, g) {
    const dpr = Math.min(window.devicePixelRatio || 1, 2), S = 150;
    cv.width = S * dpr; cv.height = S * dpr;
    const c = cv.getContext("2d");
    c.scale(dpr, dpr);
    c.translate(S / 2, S / 2);
    const L = g.layout, pos = { x: new Float32Array(L.n), y: new Float32Array(L.n) };
    place(L, 6, S / 2 - 6, pos);
    const big = L.n > 2500;
    c.lineWidth = 0.5;
    c.strokeStyle = big ? rgba("ink", V.light ? 0.02 : 0.012) : rgba("ink", V.light ? 0.16 : 0.12);
    c.beginPath();
    for (let i = 0; i < g.edges.length; i += 3) {
      const a = g.edges[i], b = g.edges[i + 1];
      c.moveTo(pos.x[a], pos.y[a]); c.lineTo(pos.x[b], pos.y[b]);
    }
    c.stroke();
    c.fillStyle = rgba("ink", big ? 0.16 : 0.75);
    for (let i = 0; i < L.n; i++) c.fillRect(pos.x[i] - 0.6, pos.y[i] - 0.6, 1.2, 1.2);
    c.fillStyle = rgba("accent", 1);
    c.beginPath(); c.arc(0, 0, 3, 0, 2 * Math.PI); c.fill();
  }

  // ---------- road ----------
  let roadShown = false;
  function renderRoad(slices) {
    const words = { done: "done", now: "in progress", proposed: "proposed", planned: "planned", later: "later" };
    const road = $("#road");
    road.innerHTML = `<div class="rail"></div><div class="fill"></div><div class="train"></div>` + slices.map((s) => `
      <div class="stop ${s.status}" title="${esc(s.detail || s.title)}">
        <div class="dot">${s.status === "done" ? "✓" : ""}</div>
        <div class="id">${s.id === "Later" ? "Later" : `Slice ${esc(s.id)}`}</div>
        <div class="tt">${esc(s.title)}</div>
        <div class="st">${words[s.status] || esc(s.status)}</div>
      </div>`).join("");
    const go = () => {
      const now = road.querySelector(".stop.now") || [...road.querySelectorAll(".stop.done")].pop();
      if (!now) return;
      const x = now.offsetLeft + 13;
      road.querySelector(".fill").style.width = `${x}px`;
      road.querySelector(".train").style.left = `${x}px`;
    };
    if (roadShown || REDUCED) return go();
    const io = new IntersectionObserver((es) => { if (es[0].isIntersecting) { roadShown = true; setTimeout(go, 150); io.disconnect(); } }, { threshold: 0.3 });
    io.observe(road);
  }

  // ---------- who builds what ----------
  function renderWho(s) {
    const w = s.who, proposed = s.decisions.status.proposed || 0;
    const people = `<svg class="glyph" viewBox="0 0 56 56" aria-hidden="true" style="color:var(--people)"><circle cx="28" cy="28" r="24" fill="none" stroke="currentColor" stroke-opacity=".35" stroke-width="2"/>
      <path d="M17 29 L25 37 L40 20" fill="none" stroke="currentColor" stroke-width="4" stroke-linecap="round" stroke-linejoin="round" stroke-dasharray="40" stroke-dashoffset="40">
      ${REDUCED ? "" : `<animate attributeName="stroke-dashoffset" values="40;0;0;40" keyTimes="0;.25;.85;1" dur="4s" repeatCount="indefinite"/>`}</path></svg>`;
    const proves = `<svg class="glyph" viewBox="-30 -30 60 60" aria-hidden="true" style="color:var(--ink)"><circle r="21" fill="none" stroke="currentColor" stroke-opacity=".25" stroke-width="2"/>
      <g><circle cx="21" cy="0" r="4.5" fill="currentColor"/>${REDUCED ? "" : `<animateTransform attributeName="transform" type="rotate" from="0" to="360" dur="6s" repeatCount="indefinite"/>`}</g>
      <circle r="8" style="fill:var(--accent)"/></svg>`;
    const agent = `<svg class="glyph" viewBox="0 0 56 56" aria-hidden="true" style="color:var(--agent)"><path d="M20 16 L9 28 L20 40 M36 16 L47 28 L36 40" fill="none" stroke="currentColor" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"/>
      <rect x="26" y="19" width="4" height="18" rx="1" fill="currentColor">${REDUCED ? "" : `<animate attributeName="opacity" values="1;1;0;0" keyTimes="0;.5;.5;1" dur="1.1s" repeatCount="indefinite"/>`}</rect></svg>`;
    $("#who").innerHTML = `
      <div class="col people">${people}<h3>@gitdek decides</h3><p>What must be true, every fork the factory can't settle, and every requirement.</p>
        <ul><li><b>${w.ratified}</b>decisions ratified</li><li><b>${w.answered}</b>questions answered on issues</li><li><b>${w.ratifications}</b>proposals ratified on issues</li></ul></div>
      <div class="col proves">${proves}<h3>Invariant proves</h3><p>Code proved or tested against what was ratified, merged only when CI's gate passes on the exact commit.</p>
        <ul><li><b>${w.merged}</b>pull requests merged</li><li><b>${w.built}</b>ratified statements it built against</li><li><b>${w.botCommits}</b>commits by its own bot</li></ul></div>
      <div class="col agent">${agent}<h3>Agents build</h3><p>The machinery: the gate, the verifiers, the GitHub plumbing, the prompts, the docs, and this page. Any coding agent, working with @gitdek.</p>
        <ul><li><b>${w.logged}</b>calls made and logged as they built</li><li><b>${w.slices}</b>slices built</li><li><b>${proposed}</b>proposal${proposed === 1 ? "" : "s"} waiting on @gitdek</li></ul></div>`;
  }

  // ---------- decisions ----------
  function renderDecisions(d) {
    const st = d.status || {};
    const parts = [["ratified", st.ratified || 0, "ratified by @gitdek"], ["decided", st.decided || 0, "decided by an agent"], ["proposed", st.proposed || 0, "proposed"]];
    const total = Math.max(1, d.total);
    const doors = { "one-way": "◆ one-way", "two-way": "◇ two-way" };
    $("#decisions").innerHTML = `<div class="bar">${parts.map(([k]) => `<span class="${k}" data-w="${((st[k] || 0) / total) * 100}"></span>`).join("")}</div>
      <div class="barkey">${parts.map(([k, n, l]) => `<span><b>${n}</b> ${l}</span>`).join("")}</div>
      <ol class="dlist">${d.latest.map((x) => `<li><span class="id">${esc(x.id)}</span>
        <span class="meta"><span class="st ${esc(x.status)}">${esc(x.status)}</span><span>${esc(doors[x.door] || x.door)}</span><span>${esc(x.date)}</span></span>
        <span class="txt">${esc(x.text)}</span></li>`).join("")}</ol>`;
    requestAnimationFrame(() => document.querySelectorAll("#decisions .bar span").forEach((el) => (el.style.width = `${el.dataset.w}%`)));
    document.querySelectorAll("#decisions .dlist li").forEach((li) => li.addEventListener("click", () => li.classList.toggle("open-text")));
  }

  // ---------- activity ----------
  const known = new Set();
  function renderActivity(items) {
    const first = known.size === 0;
    $("#activity").innerHTML = items.slice(0, 16).map((e, k) => {
      const key = `${e.at}|${e.who}|${e.kind}|${e.text}`;
      const fresh = !known.has(key);
      known.add(key);
      let text;
      if (e.who === "factory") text = `<span class="who">Invariant</span> ${esc(e.text)} on #${e.issue}`;
      else if (e.who === "person") text = e.kind === "opened" ? `<span class="who">@${esc(e.by)}</span> opened #${e.issue}` : `<span class="who">@${esc(e.by)}</span> ${esc(e.text)} on #${e.issue}`;
      else if (e.who === "commit") text = `<span class="who">@${esc(e.by)}</span> committed <code>${esc(e.kind)}</code> ${esc(e.text)}`;
      else text = `<span class="who">CI</span> ${esc(e.text)}`;
      const style = fresh ? `style="animation-delay:${first ? k * 45 : 0}ms"` : `style="animation:none"`;
      return `<li class="${esc(e.who)} ${esc(e.kind)}" ${style}>${text}<span class="when" data-ago="${esc(e.at)}" title="${esc(stamp(e.at))}"></span></li>`;
    }).join("");
  }

  $("#theme").addEventListener("click", () => {
    const next = document.documentElement.dataset.theme === "light" ? "dark" : "light";
    const root = document.documentElement;
    root.classList.add("theming");
    root.dataset.theme = next;
    try { localStorage.setItem("invariant-theme", next); } catch (e) {}
    document.querySelector('meta[name="theme-color"]').setAttribute("content", next === "light" ? "#f4f6f8" : "#090c11");
    readTheme();
    if (cosmos.g) { cosmos.prerender(); if (REDUCED) cosmos.frame(performance.now()); }
    if (state) thumbs(state);
    setTimeout(() => root.classList.remove("theming"), 500);
  });
  document.querySelector('meta[name="theme-color"]').setAttribute("content", V.light ? "#f4f6f8" : "#090c11");

  cosmos.init();
  load();
  setInterval(tick, 1000);
})();
