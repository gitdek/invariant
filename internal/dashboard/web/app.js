"use strict";
// Invariant · Live. Everything drawn here comes from /api/state.json and
// /api/graph/<model>.json, which the server reads from GitHub, CI's
// receipts and TLC, and /api/live.json, which it reads from the watcher's
// work directory. Nothing is typed by hand.
(() => {
  // The page's own version, from its script's URL. Graph URLs carry it, so
  // a new dashboard never draws a graph the browser kept from an old one.
  const VERSION = new URL(document.currentScript?.src || location.href).searchParams.get("v") || "";
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
  // An issue in Invariant's own repository is #5; one elsewhere is
  // copythis-ad#1.
  const short = (repo) => (repo || "").split("/").pop();
  const ref = (repo, n) => (repo && state && repo !== state.repo ? short(repo) : "") + "#" + n;
  // amendsRef names what an amendment replaces: the issue that ratified it,
  // or a decision, such as D-0027, for a project ratified by hand.
  const amendsRef = (repo, amends) => (/^#\d+$/.test(String(amends)) ? ref(repo, Number(String(amends).slice(1))) : String(amends));
  const issueKey = (is) => `${is.repo || ""}#${is.number}`;

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
  const clockOf = (ts) => new Date(ts).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  function tick() {
    document.querySelectorAll("[data-ago]").forEach((el) => (el.textContent = ago(el.dataset.ago)));
    document.querySelectorAll("[data-since]").forEach((el) => (el.textContent = dur((Date.now() - T(el.dataset.since)) / 1000)));
    if (state) {
      const u = $("#updated");
      u.textContent = `updated ${ago(state.generatedAt)}` + (state.stale?.length ? " · some sources stale" : "");
      u.classList.toggle("stale", !!state.stale?.length);
    }
    liveTick(false);
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
    if (changed("scope", [scope, (s.repos || []).map((r) => [r.name, r.projects, r.factory.running]), s.issues.filter((i) => i.open).map((i) => i.repo)])) renderScope(s);
    if (changed("inbox", [scope, s.issues.filter((i) => i.open).map((i) => [i.repo, i.number, i.title, i.waiting]), [...acts]])) renderInbox(s);
    if (changed("fleet", [scope, s.repos, s.projects.map((p) => [p.repo, p.dir, p.name, p.states, p.passed, p.language, p.model]), s.issues.map((i) => [i.repo, i.number, i.stage, i.open, i.project, i.title])])) renderFleet(s);
    if (changed("now", [scope, s.now, s.nowBy, s.factory, s.main, s.receipts?.id, s.repos])) renderNow(s);
    if (changed("orbit", [scope, s.issues.map((i) => [i.repo, i.number, i.stage, i.open, i.title]), s.factory.running, s.totals.merged])) orbit.update(s);
    if (changed("numbers", [scope, s.totals, (s.repos || []).map((r) => r.totals), s.who.ratified])) renderNumbers(s);
    if (changed("models", [scope, s.projects.map((p) => [p.repo, p.model, p.graph, p.states])])) renderTabs(s);
    if (changed("lanes", [scope, lanesAll, s.issues])) renderLanes(s.issues.filter((i) => inScope(i.repo)));
    if (changed("projects", [scope, pview, s.projects])) renderProjects(s);
    if (changed("road", s.slices)) renderRoad(s.slices);
    if (changed("who", [s.who, s.decisions.status])) renderWho(s);
    if (changed("decisions", s.decisions)) renderDecisions(s.decisions);
    if (changed("activity", [scope, s.activity])) renderActivity(s.activity.filter((e) => inScope(e.repo)));
    if (live) renderLive();
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
    const n = (scope !== "all" && s.nowBy?.[scope]) || s.now, h = $("#headline");
    // Find the issue numbers first, then escape the rest. Escaping first
    // turns an apostrophe into &#39;, whose #39 would look like an issue.
    const html = n.headline.split(/(#\d+)/).map((part) => (/^#\d+$/.test(part) ? `<span class="n">${part}</span>` : esc(part))).join("");
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
    const lastMerged = s.issues.find((i) => i.stage === "merged" && inScope(i.repo));
    if (n.stage === "idle" && lastMerged) {
      const when = lastMerged.closed ? `, <span data-ago="${esc(lastMerged.closed)}"></span>` : "";
      d.innerHTML = `Last merged <b>${esc(ref(lastMerged.repo, lastMerged.pr))}</b> for ${esc(ref(lastMerged.repo, lastMerged.number))}, <q>${esc(lastMerged.title)}</q>${when}.`;
    } else {
      d.innerHTML = n.detail ? `<q>${esc(n.detail)}</q>` : "";
    }

    const chips = [];
    // What waits on a person is counted here and shown in Needs you (D-0098).
    const needs = s.issues.filter((i) => i.open && i.waiting && inScope(i.repo)).length;
    const needsChip = needs ? `<a class="chip people" href="#inbox"><i></i>${needs === 1 ? "1 issue needs you" : `${needs} issues need you`}</a>` : "";
    if (n.stage === "idle") {
      chips.push(s.factory.running ? `<span class="chip factory pulse"><i></i>Watching for issues</span>` : `<span class="chip"><i></i>The watcher is off</span>`);
      if (needsChip) chips.push(needsChip);
      const t = totalsOf(s);
      chips.push(`<span class="chip holds"><i></i>${t.badMerges} bad merges in ${t.merged}</span>`);
    } else {
      const w = { people: ["people", "Waiting on a person"], ci: ["ci pulse", "CI's gate is running"], factory: ["factory pulse", "The factory is working"] }[n.waitingOn] || ["", n.stage];
      chips.push(`<span class="chip ${w[0]}"><i></i>${w[1]}</span>`);
      if (n.since) chips.push(`<span class="chip">for&nbsp;<span data-since="${esc(n.since)}"></span></span>`);
      // The step's checks as they run: TLC checking a draft, or the gate
      // checking the code.
      const owner = (s.repos || []).find((r) => r.name === (n.repo || s.repo))?.factory;
      const fw = owner && owner.issue === n.issue ? owner : {};
      for (const r of fw.runs || []) {
        const what = fw.runsKind === "check" ? "TLC check" : "gate run";
        chips.push(`<span class="chip ${r.passed ? "holds" : "bug"} runchip"><i></i>${what} ${r.run} ${r.passed ? "passed" : "failed"}</span>`);
      }
    }
    if (n.stage !== "idle" && needsChip) chips.push(needsChip);
    $("#nowmeta").innerHTML = chips.join("");

    // A gate run that didn't pass: failed, or never started at all, as when
    // the account's Actions minutes run out. Only a gate that ran can fail.
    const gateNot = (g) => g.notStarted
      ? `<span class="muted" title="GitHub never gave the job a runner">○ gate didn't start</span>`
      : `<span class="bad">✕ gate ${esc(g.conclusion === "failure" ? "failed" : g.conclusion.replaceAll("_", " "))}</span>`;
    // A run that changed only docs skips the gate, so it passed nothing.
    const gateSkipped = `<span class="muted" title="Only docs changed, so the gate had nothing to check">○ gate skipped: only docs changed</span>`;
    const rows = [], mine = inScope(s.repo);
    if (s.main && mine) {
      const m = s.main;
      let gate = `<span class="muted">no gate run yet</span>`;
      if (m.gate) {
        const g = m.gate;
        if (g.status !== "completed") gate = `<span class="run">● gate running</span> for <span data-since="${esc(g.started)}"></span>`;
        else if (g.skipped) gate = gateSkipped;
        else if (g.conclusion === "success") gate = `<span class="ok">✓ gate passed</span> in ${dur((T(g.updated) - T(g.started)) / 1000)}`;
        else gate = gateNot(g);
      }
      rows.push(`<div ${tag(s.repo)}><span class="k">Main</span><span><code>${esc(m.sha.slice(0, 7))}</code> ${esc(m.title)} · @${esc(m.by)} · <span data-ago="${esc(m.at)}"></span></span></div>`);
      rows.push(`<div ${tag(s.repo)}><span class="k">CI</span><span>${gate}</span></div>`);
    }
    if (s.receipts && mine) rows.push(`<div ${tag(s.repo)}><span class="k">Receipts</span><span>from CI's gate on <code>${esc(s.receipts.sha.slice(0, 7))}</code>, <span data-ago="${esc(s.receipts.updated)}"></span></span></div>`);
    // The lease: which one watcher may act, and until when it must renew.
    const lease = (l) => T(l.until) > Date.now()
      ? `<code>${esc(l.holder)}</code> holds it until ${esc(clockOf(l.until))}`
      : `<span class="muted">ran out at ${esc(clockOf(l.until))}, free for the next watcher</span>`;
    const primary = (s.repos || []).find((r) => r.primary);
    if (primary?.lease && mine) rows.push(`<div ${tag(s.repo)}><span class="k">Lease</span><span>${lease(primary.lease)}</span></div>`);
    for (const r of s.repos || []) {
      if (r.primary || !inScope(r.name)) continue;
      let gate = `<span class="muted">no gate run yet</span>`;
      if (r.gate) {
        const g = r.gate;
        if (g.status !== "completed") gate = `<span class="run">● gate running</span>`;
        else if (g.skipped) gate = gateSkipped;
        else if (g.conclusion === "success") gate = `<span class="ok">✓ gate passed</span> on <code>${esc(g.sha.slice(0, 7))}</code>`;
        else gate = gateNot(g);
      }
      const f = r.factory?.running ? "factory on" : "factory off";
      const held = r.lease && T(r.lease.until) > Date.now() ? " · lease held" : "";
      rows.push(`<div class="${quiet(r.name)}" ${tag(r.name)}><span class="k">${esc(r.short)}</span><span>${gate} · ${f}${held} · ${r.projects} project${r.projects === 1 ? "" : "s"}</span></div>`);
    }
    $("#mainline").innerHTML = rows.join("");
  }

  // ---------- the pipeline orbit ----------
  const STATIONS = [
    { key: "queued", name: "Issue", sub: "opened", color: "ink2" },
    { key: "asking", name: "Ask", sub: "people decide", color: "people" },
    { key: "ratifying", name: "Ratify", sub: "people sign", color: "people" },
    { key: "building", name: "Build", sub: "factory", color: "ink" },
    { key: "gate", name: "Gate", sub: "CI checks", color: "ci" },
    { key: "merged", name: "Merge", sub: "gate passed", color: "accent" },
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
      const mine = s.issues.filter((i) => inScope(i.repo));
      $("#coreNum").textContent = totalsOf(s).merged;
      // Some merges are proved and some are tested against the model, and the
      // count claims only what every one of them has: the gate passed (D-0015).
      $("#coreLabel").textContent = "merged through the gate";
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
      const merged = mine.filter((i) => i.stage === "merged").slice().reverse();
      $("#moons").innerHTML = merged.map((is, i) => {
        const r = 40 + i * 13, per = 22 + i * 9, start = (i * 137) % 360;
        const spin = REDUCED ? "" : `<animateTransform attributeName="transform" type="rotate" from="${start}" to="${start + 360}" dur="${per}s" repeatCount="indefinite"/>`;
        return `<g class="${quiet(is.repo)}" ${tag(is.repo)} transform="rotate(${start})"><circle class="moonring" r="${r}"/>${spin}<circle class="moon" cx="${r}" cy="0" r="3.2"><title>${esc(ref(is.repo, is.number))} ${esc(is.title)}: merged ${esc(ref(is.repo, is.pr))}</title></circle></g>`;
      }).join("");
      // Open issues sit at their stage.
      const open = mine.filter((i) => i.open);
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
        return `<g class="${quiet(is.repo)}" ${tag(is.repo)} data-n="${esc(issueKey(is))}"><circle r="7" class="issue-dot${bug ? " bug" : ""}"/><text class="issue-label" x="12" y="4">${esc(ref(is.repo, is.number))}</text><title>${esc(ref(is.repo, is.number))} ${esc(is.title)}</title></g>`;
      }).join("");
      want.forEach(({ is, angle }) => {
        const k = issueKey(is);
        const from = this.angles.has(k) ? this.angles.get(k) : stationAngle(0);
        this.angles.set(k, angle);
        const g = [...dots.children].find((el) => el.dataset.n === k);
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
    const t = totalsOf(s);
    const logged = t.decisions ? `${s.who.ratified} ratified by @gitdek, ${s.who.logged} decided by agents as they built` : "none logged in this repository yet";
    const tiles = [
      { k: "bad", cls: "holds", v: t.badMerges, l: "merges without a green gate", d: `CI's gate passed on the exact head of all ${t.mergesChecked} pull requests the factory merged. ${t.badLocks === 0 ? `All ${t.locksChecked} factory locks are exactly what a person ratified.` : `${t.badLocks} locks differ from what was ratified.`}` },
      { k: "states", v: t.states, l: "states TLC explored", d: `across ${t.models} models, exhaustively, within the ratified bounds` },
      { k: "stmts", v: t.statements, l: "statements people ratified", d: "each pinned by hash, so the factory can't change them" },
      { k: "bugs", v: t.bugsCaught, of: t.bugs, l: "planted bugs caught", d: "each one breaks an invariant, and TLC finds the step" },
      { k: "proved", v: t.proved, l: "functions proved", d: "by Gobra and Nagini, against contracts that restate the model" },
      { k: "dec", v: t.decisions, l: "decisions logged", d: logged },
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
    if (!graphs.has(key)) graphs.set(key, fetch(`/api/graph/${key}.json?v=${VERSION}`).then((r) => (r.ok ? r.json() : Promise.reject(r.status))).then((g) => { g.layout = layoutGraph(g); return g; }));
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
        <div class="m">${nf.format(m.states)} states · ${m.depth} steps deep${this.g?.allEdges ? ` · ${nf.format(this.g.allEdges)} steps, ${nf.format(this.g.edges.length / 3)} drawn` : ""} · ${esc(Object.entries(m.bounds || {}).map(([k, v]) => `${k} = ${v}`).join(", "))}</div>
        <div class="h">In every one of them, <b>${esc(inv.slice(0, 4).join(", "))}</b>${inv.length > 4 ? ` and ${inv.length - 4} more` : ""} hold${inv.length === 1 ? "s" : ""}.</div>${(m.dir || "").startsWith("factory/") ? `<div class="h itself">These are the factory's own rules. The factory drafted them, people ratified them, and the factory built and proved the code it runs on.</div>` : ""}`;
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
      if (!inScope(p.repo)) continue;
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
    if (!ms.length) { $("#tabs").innerHTML = ""; return; }
    if (!currentModel || !ms.find((m) => m.key === currentModel)) {
      const pick = ms.find((m) => m.meta.factory && m.meta.graph) || ms.find((m) => m.meta.graph) || ms[0];
      currentModel = pick.key;
    }
    $("#tabs").innerHTML = ms.map((m) => `<button class="tab${quiet(m.meta.repo)}" type="button" ${tag(m.meta.repo)} data-key="${m.key}" aria-pressed="${m.key === currentModel}">${esc(m.label)}<span>${nf.format(m.meta.states)}</span></button>`).join("");
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
  function renderLanes(all) {
    const box = $("#lanes");
    if (!all.length) { box.innerHTML = `<p class="skeleton">No issues yet.</p>`; return; }
    const first = (i) => (i.repo === state.repo ? 0 : 1);
    const open = all.filter((i) => i.open).sort((a, b) => first(a) - first(b)), closed = all.filter((i) => !i.open);
    const issues = lanesAll ? open.concat(closed) : open.concat(closed.slice(0, Math.max(0, 4 - open.length + 1)));
    const hidden = all.length - issues.length;
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
      return `<article class="lane${quiet(is.repo)}" ${tag(is.repo)}>
        <div class="lane-head"><span class="no">${esc(ref(is.repo, is.number))}</span><span class="title">${esc(is.title)}</span><span class="chips">${chips}</span></div>
        <div class="track">${segs}${marks}</div>
        <div class="lane-foot"><span>factory <b>${dur(is.factorySeconds)}</b></span><span class="p">waiting on people <b>${dur(is.peopleSeconds)}</b></span><span><b>${is.peopleComments}</b> comment${is.peopleComments === 1 ? "" : "s"} from people</span>${is.questions ? `<span><b>${is.questions}</b> question${is.questions === 1 ? "" : "s"} asked</span>` : ""}${is.statements ? `<span><b>${is.statements}</b> statements</span>` : ""}${is.spendUSD ? `<span>agents <b>$${is.spendUSD.toFixed(2)}</b></span>` : ""}${is.gateRuns ? `<span><b>${is.gateRuns}</b> gate run${is.gateRuns === 1 ? "" : "s"}</span>` : ""}${is.pr ? `<span>pull request <b>${esc(ref(is.repo, is.pr))}</b></span>` : ""}</div>
      </article>`;
    }).join("");
    if (hidden > 0 || lanesAll) {
      box.insertAdjacentHTML("beforeend", `<div class="more"><button class="btn" type="button" id="lanesmore">${lanesAll ? "Show fewer" : `Show ${hidden} more`}</button></div>`);
      $("#lanesmore").addEventListener("click", () => { lanesAll = !lanesAll; delete seen.lanes; render(state); });
    }
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
    const ps = s.projects.filter((p) => inScope(p.repo)).sort((a, b) => (a.repo === s.repo ? 0 : 1) - (b.repo === s.repo ? 0 : 1) || order(a) - order(b) || b.states - a.states);
    if (pview === "table") return renderTable(s, ps);
    $("#projects").className = "cards";
    $("#projects").innerHTML = ps.map((p) => {
      const assure = p.assurance === "proved" ? "proved" : p.assurance === "tested in every state" ? "every" : "tested";
      const caught = p.bugs.filter((b) => b.caught).length;
      let evidence;
      if (p.assurance === "proved") evidence = [`${p.verified}`, "Functions proved"];
      else if ((p.visited || 0) >= p.states) evidence = ["every state", "Code reached"];
      else evidence = [`${nf.format(p.visited || 0)}/${nf.format(p.states)}`, "States the code ran"];
      let prov = "Hand-built, before the factory";
      if (p.ratified) prov = `Ratified by <b>@${esc(p.ratified.by)}</b> on ${esc(ref(p.repo, p.ratified.issue))}${p.ratified.previous ? `, amending ${esc(p.ratified.previous)}` : ""}`;
      else if (p.decision) prov = `Ratified in <b>${esc(p.decision)}</b>, by hand`;
      return `<article class="card${quiet(p.repo)}" ${tag(p.repo)} data-model="${p.model}" tabindex="0">
        <canvas class="thumb" data-model="${p.model}" width="150" height="150"></canvas>
        <div class="top"><span class="lang ${esc(p.language)}">${esc(langName[p.language] || p.language)}</span><span class="assure ${assure}">${esc(p.assurance)}</span></div>
        <h3>${esc(cap(p.name))}</h3>
        <p class="dir">${p.repo && p.repo !== s.repo ? esc(short(p.repo)) + " · " : ""}${esc(p.dir)}</p>
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
        <ul><li><b>${w.merged}</b>pull requests merged</li><li><b>${w.built}</b>ratified statements it built against</li><li><b>${w.botCommits}</b>commits by its own bot</li>${w.itself ? `<li class="itself"><b>${w.itself}</b>part${w.itself === 1 ? "" : "s"} of itself it proves: the rules its factory runs on</li>` : ""}</ul></div>
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
      const key = `${e.repo}|${e.at}|${e.who}|${e.kind}|${e.text}`;
      const fresh = !known.has(key);
      known.add(key);
      let text;
      const on = esc(ref(e.repo, e.issue));
      if (e.who === "factory") text = `<span class="who">Invariant</span> ${esc(e.text)} on ${on}`;
      else if (e.who === "person") text = e.kind === "opened" ? `<span class="who">@${esc(e.by)}</span> opened ${on}` : `<span class="who">@${esc(e.by)}</span> ${esc(e.text)} on ${on}`;
      else if (e.who === "commit") text = `<span class="who">@${esc(e.by)}</span> committed <code>${esc(e.kind)}</code> ${esc(e.text)}`;
      else text = `<span class="who">CI</span> ${e.repo && e.repo !== state.repo ? esc(short(e.repo)) + "'s " : ""}${esc(e.text)}`;
      const style = fresh ? `style="animation-delay:${first ? k * 45 : 0}ms"` : `style="animation:none"`;
      return `<li class="${esc(e.who)} ${esc(e.kind)}${quiet(e.repo)}" ${tag(e.repo)} ${style}>${text}<span class="when" data-ago="${esc(e.at)}" title="${esc(stamp(e.at))}"></span></li>`;
    }).join("");
  }


  // ---------- scope: which repositories the page shows ----------
  let scope = "all", pview = "cards", lanesAll = false;
  try { scope = localStorage.getItem("invariant-scope") || "all"; pview = localStorage.getItem("invariant-pview") || "cards"; } catch (e) {}
  const inScope = (repo) => scope === "all" || !repo || repo === scope;
  // Every item from a repository says which, so choosing one repository can
  // fade the others out. With everything shown, another repository's items
  // stay quieter than Invariant's own.
  const tag = (repo) => `data-repo="${esc(repo || "")}"`;
  const quiet = (repo) => (scope === "all" && repo && state && repo !== state.repo ? " aside" : "");
  // The big numbers, for the chosen repository alone.
  const totalsOf = (s) => (scope !== "all" && (s.repos || []).find((r) => r.name === scope)?.totals) || s.totals;
  let fading = 0;
  function setScope(v) {
    if (v === scope) return;
    const was = scope;
    scope = v;
    try { localStorage.setItem("invariant-scope", v); } catch (e) {}
    if (!state) return;
    // What the choice leaves out fades first. Then the page is drawn for it,
    // and what comes back fades in.
    const draw = () => {
      render(state);
      if (REDUCED || was === "all") return;
      document.querySelectorAll("[data-repo]").forEach((el) => {
        if (el.dataset.repo && el.dataset.repo !== was) el.classList.add("arriving");
      });
    };
    const leaving = [...document.querySelectorAll("[data-repo]")].filter((el) => el.dataset.repo && !inScope(el.dataset.repo));
    clearTimeout(fading);
    if (REDUCED || !leaving.length) return draw();
    leaving.forEach((el) => el.classList.add("leaving"));
    fading = setTimeout(draw, 380);
  }
  function renderScope(s) {
    const repos = s.repos || [];
    const nav = $("#scope");
    if (repos.length < 2) { nav.innerHTML = ""; return; }
    if (scope !== "all" && !repos.find((r) => r.name === scope)) scope = "all";
    const openIn = (name) => s.issues.filter((i) => i.open && (!name || i.repo === name)).length;
    const all = repos.reduce((a, r) => a + r.projects, 0);
    const btn = (value, label, projects, open, live) =>
      `<button type="button" data-scope="${esc(value)}" aria-pressed="${scope === value}"><i class="live${live ? " on" : ""}"></i>${esc(label)}<span class="ct">${projects} project${projects === 1 ? "" : "s"}${open ? ` · ${open} open` : ""}</span></button>`;
    nav.innerHTML = `<span class="lbl">Show</span>` + btn("all", "Everything", all, openIn(""), repos.some((r) => r.factory?.running)) +
      repos.map((r) => btn(r.name, r.short, r.projects, openIn(r.name), r.factory?.running)).join("");
    nav.querySelectorAll("button").forEach((b) => b.addEventListener("click", () => setScope(b.dataset.scope)));
  }

  // ---------- needs you ----------
  const people = "var(--people)";
  function needGlyph(kind) {
    if (kind === "forks") return `<svg class="glyph" viewBox="0 0 64 64" aria-hidden="true" style="color:${people}">
      <circle cx="32" cy="32" r="30" fill="none" stroke="currentColor" stroke-opacity=".22"/>
      <path d="M10 32 H28 C34 32, 36 18, 50 18 M28 32 C34 32, 36 46, 50 46" fill="none" stroke="currentColor" stroke-opacity=".45" stroke-width="2.4" stroke-linecap="round"/>
      <circle cx="50" cy="18" r="3" fill="currentColor" fill-opacity=".5"/><circle cx="50" cy="46" r="3" fill="currentColor" fill-opacity=".5"/>
      ${REDUCED ? `<circle cx="28" cy="32" r="4" fill="currentColor"/>` : `<circle r="4" fill="currentColor"><animateMotion dur="3.2s" repeatCount="indefinite" keyPoints="0;0.42;0.42;1" keyTimes="0;0.35;0.65;1" calcMode="linear" path="M10 32 H28 C34 32, 36 18, 50 18"/></circle>
      <circle r="4" fill="currentColor" opacity=".55"><animateMotion dur="3.2s" begin="1.6s" repeatCount="indefinite" keyPoints="0;0.42;0.42;1" keyTimes="0;0.35;0.65;1" calcMode="linear" path="M10 32 H28 C34 32, 36 46, 50 46"/></circle>`}
      <text x="28" y="26" text-anchor="middle" font-size="10" font-weight="700" fill="currentColor" font-family="JetBrains Mono, monospace">?</text></svg>`;
    if (kind === "proposal") return `<svg class="glyph" viewBox="0 0 64 64" aria-hidden="true" style="color:${people}">
      <rect x="15" y="9" width="34" height="44" rx="4" fill="none" stroke="currentColor" stroke-opacity=".5" stroke-width="2"/>
      <path d="M21 19 H43 M21 25 H43 M21 31 H36" stroke="currentColor" stroke-opacity=".35" stroke-width="2" stroke-linecap="round"/>
      <path d="M20 44 c4 -6 6 4 9 -1 s4 -5 6 0 s3 3 8 -2" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-dasharray="40" stroke-dashoffset="${REDUCED ? 0 : 40}">
        ${REDUCED ? "" : `<animate attributeName="stroke-dashoffset" values="40;0;0;40" keyTimes="0;.35;.85;1" dur="3.6s" repeatCount="indefinite"/>`}</path>
      <circle cx="50" cy="50" r="8" fill="var(--bg)" stroke="currentColor" stroke-width="2"/><path d="M46.5 50 l2.5 2.5 l5 -5.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
    return `<svg class="glyph" viewBox="0 0 64 64" aria-hidden="true" style="color:var(--bug)">
      <circle cx="32" cy="32" r="14" fill="currentColor" fill-opacity=".14" stroke="currentColor" stroke-width="2"/>
      ${REDUCED ? "" : `<circle cx="32" cy="32" r="14" fill="none" stroke="currentColor" stroke-width="2"><animate attributeName="r" values="14;29" dur="1.8s" repeatCount="indefinite"/><animate attributeName="opacity" values=".8;0" dur="1.8s" repeatCount="indefinite"/></circle>`}
      <path d="M32 24 V34" stroke="currentColor" stroke-width="3" stroke-linecap="round"/><circle cx="32" cy="39.5" r="2" fill="currentColor"/></svg>`;
  }
  function calmGlyph() {
    return `<svg viewBox="-20 -20 40 40" aria-hidden="true"><circle r="15" fill="none" stroke="currentColor" stroke-opacity=".25" style="color:var(--ink)"/>
      <path d="M-6 0 l4 4 l8 -9" fill="none" stroke="var(--accent)" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/>
      <g>${REDUCED ? "" : `<animateTransform attributeName="transform" type="rotate" from="0" to="360" dur="7s" repeatCount="indefinite"/>`}<circle cx="15" cy="0" r="2.4" style="fill:var(--ink)"/></g></svg>`;
  }
  function renderInbox(s) {
    const items = s.issues.filter((i) => i.open && i.waiting && inScope(i.repo));
    const box = $("#inbox");
    if (!items.length) {
      box.innerHTML = `<div class="calm">${calmGlyph()}<span>Nothing needs you right now. When the factory asks a question, proposes statements or needs a person, it shows up here.</span></div>`;
      return;
    }
    // Invariant's own first, then the longest wait first: the order the
    // factory took them in.
    const first = (i) => (i.repo === s.repo ? 0 : 1);
    items.sort((a, b) => first(a) - first(b) || new Date(a.waiting.since) - new Date(b.waiting.since));
    box.innerHTML = batchBanner(items) + items.map(needCard).join("");
  }
  // proposalText says what a proposal asks a person to ratify. A rebuild
  // that changes no statement says so first, since that's all there is to
  // check.
  function proposalText(w, repo, who) {
    const n = (w.statements || []).length, name = `<strong>${esc(w.name)}</strong>`;
    // A plumbing plan is tested, not proved (D-0105).
    if (w.plan) {
      const files = (w.plan.files || []).length, trusted = (w.plan.trusted || []).length;
      return `<p class="ask">A plan for ${name}: it changes <strong>${files}</strong> file${files === 1 ? "" : "s"}, and its <strong>${n}</strong> acceptance test${n === 1 ? "" : "s"} fail until it's built. It's tested, not proved${trusted ? ", and it changes the trusted base, so a person merges it" : ""}. Waiting for ${who} to ratify it.</p><p class="ask">${esc(w.plan.summary)}</p>`;
    }
    if (w.unchanged) return `<p class="ask"><b class="same">No statement changes.</b> All <strong>${n}</strong> statements for ${name} stay exactly as ${esc(amendsRef(repo, w.amends))} ratified them, and only the code changes.</p>`;
    return `<p class="ask">${w.amends ? `An amendment to what ${esc(amendsRef(repo, w.amends))} ratified: ` : ""}<strong>${n}</strong> statements for ${name}, already checked by TLC, and waiting for ${who} to ratify them.</p>`;
  }
  // why says, in words, why a failed issue needs a person.
  function why(w, repo) {
    const pr = w.pr ? `<strong>${esc(ref(repo, w.pr))}</strong>` : "the pull request";
    switch (w.failure) {
      case "stopped": return "The build stopped before it made a pull request. Try again, or draft again from the comments.";
      case "limit": return "Two builds stopped before they made a pull request, so the factory won't start another on its own. Try again, or draft again.";
      case "gate": return `The code didn't pass the gate in the factory's own run. Its last attempt is in draft ${pr}. If the code needs fixing, fix it, then have the factory try again. If what must be true should change instead, close ${pr}, then have it draft again.`;
      case "unmergeable": return `${pr} passed CI's gate, but it can't merge: it reaches outside its project, or its lock isn't the one ratified. Once it's fixed, have the factory try again.`;
      case "ci": return `CI's gate didn't pass on ${pr}. Once the cause is fixed, have the factory try again.`;
      case "trusted": return `${pr} passed CI's gate and the review, and it changes the trusted base, so a person merges it. Review it and merge it on GitHub.`;
    }
    return `${w.pr ? `Pull request ${pr} didn't pass. ` : ""}A person needs to look. Once the cause is fixed, have the factory try again.`;
  }
  const stoppedBuild = (w) => w.failure === "stopped" || w.failure === "limit";
  // A failure with a pull request can also be drafted again, once the pull
  // request is closed.
  const redraftable = (w) => stoppedBuild(w) || Boolean(w.pr);
  const closedText = (w, repo) => `${w.pr ? `<strong>${esc(ref(repo, w.pr))}</strong>` : "The pull request"} was closed without merging, so the factory stopped. To go on, have it draft again from the comments on the issue.`;
  function needCard(is) {
    if (ACT) return actCard(is);
    const w = is.waiting, repo = is.repo, n = is.number;
    const gh = (body) => `gh issue comment ${n} -R ${repo} --body "${body}"`;
    const url = `https://github.com/${repo}/issues/${n}`;
    const open = `<a class="gh" href="${esc(url)}" target="_blank" rel="noopener">Open on GitHub ↗</a>`;
    const cmd = (c, primary) => `<button class="cmd${primary ? " primary" : ""}" type="button" data-cmd="${esc(c)}" data-gh="${esc(gh(c))}" title="Click to copy. Shift-click copies it as a gh command.">${esc(c)}<span class="cp">copy</span></button>`;
    let body = "", cls = "";
    if (w.kind === "forks") {
      body = (w.forks || []).map((q) => `<p class="ask"><b>${esc(q.id)}</b>${esc(q.question)}</p><div class="opts">${(q.options || []).map((o) => {
        const c = `/invariant choose ${q.id} ${o.id}`;
        return `<button class="opt" type="button" data-cmd="${esc(c)}" data-gh="${esc(gh(c))}" title="Click to copy ${esc(c)}. Shift-click copies it as a gh command."><span class="id">${esc(o.id)}</span><span>${esc(o.says)}</span><span class="cp">copy</span></button>`;
      }).join("")}</div>`).join("") + `<div class="cmds">${cmd("/invariant revise")}${open}</div>`;
    } else if (w.kind === "proposal") {
      body = `${proposalText(w, repo, "a person")}
        <details class="stmts"><summary>Read what they say</summary><ul>${(w.statements || []).map((st) => `<li><span class="k">${esc(st.kind)}</span><span><b>${esc(st.name)}</b>${esc(st.says)}</span></li>`).join("")}</ul></details>
        <div class="cmds">${cmd(`/invariant ratify ${w.hash}`, true)}${cmd("/invariant revise")}${open}</div>`;
    } else if (w.kind === "closed") {
      body = `<p class="ask">${closedText(w, repo)}</p>
        <div class="cmds">${cmd("/invariant revise", true)}${open}</div>`;
    } else {
      cls = "failed";
      body = `<p class="ask">${why(w, repo)}</p>
        <div class="cmds">${cmd("/invariant retry", true)}${redraftable(w) ? cmd("/invariant revise") : ""}${open}</div>`;
    }
    return `<article class="need ${cls}${quiet(repo)}" ${tag(repo)}><div>${needGlyph(w.kind)}</div><div>
      <div class="need-top"><span class="ref">${esc(ref(repo, n))}</span><span class="title">${esc(is.title)}</span><span class="since">waiting <span data-since="${esc(w.since)}"></span></span></div>${body}</div></article>`;
  }
  document.addEventListener("click", async (e) => {
    const b = e.target.closest("[data-cmd]");
    if (!b) return;
    const text = e.shiftKey && b.dataset.gh ? b.dataset.gh : b.dataset.cmd;
    try { await navigator.clipboard.writeText(text); } catch {
      const ta = document.createElement("textarea");
      ta.value = text; document.body.append(ta); ta.select(); document.execCommand("copy"); ta.remove();
    }
    const cp = b.querySelector(".cp");
    b.classList.add("copied");
    if (cp) cp.textContent = e.shiftKey ? "gh copied" : "copied";
    setTimeout(() => { b.classList.remove("copied"); if (cp) cp.textContent = "copy"; }, 1600);
  });

  // On /act, rebuilds that change no statement can be ratified together.
  // Each still posts its own ratify, on its own issue, as the owner, and the
  // factory builds them one at a time.
  const batch = { confirm: false, busy: false, error: "" };
  function batchBanner(items) {
    const same = items.filter((i) => i.waiting.kind === "proposal" && i.waiting.unchanged && !actOf(`${i.repo}#${i.number}`).posted);
    if (!ACT || same.length < 2) return "";
    const refs = same.map((i) => esc(ref(i.repo, i.number))).join(", ");
    const row = batch.confirm
      ? `<div class="confirm"><span>Ratify all ${same.length} as you? Each pins statements that don't change, and the factory builds them one at a time.</span><button class="cmd act primary" type="button" data-batch="post"${batch.busy ? " disabled" : ""}>Yes, ratify all ${same.length}</button><button class="cmd" type="button" data-batch="cancel">Cancel</button></div>`
      : `<div class="cmds"><button class="cmd act primary" type="button" data-batch="ask">Ratify all ${same.length}</button></div>`;
    return `<div class="batch"><p class="ask"><b class="same">${same.length} rebuilds change no statement:</b> ${refs}. Only their code changes.</p>${row}${batch.error ? `<p class="acterr">${esc(batch.error)}</p>` : ""}</div>`;
  }

  // ---------- acting: at /act, the owner's clicks post to GitHub (D-0065) ----------
  // Cloudflare Access signs the owner in, and the server checks it again on
  // every post. Each card offers only what its issue is waiting for, and a
  // ratify or a retry asks first.
  const ACT = document.body.dataset.act || "";
  const acts = new Map(); // "repo#n" → { chosen: {F1: "C"}, confirm, busy, posted, error }
  function actOf(key) {
    if (!acts.has(key)) acts.set(key, { chosen: {} });
    return acts.get(key);
  }
  function actCard(is) {
    const w = is.waiting, repo = is.repo, n = is.number, key = `${repo}#${n}`, a = actOf(key);
    const url = `https://github.com/${repo}/issues/${n}`;
    const open = `<a class="gh" href="${esc(url)}" target="_blank" rel="noopener">Open on GitHub ↗</a>`;
    const post = (label, body, primary) => `<button class="cmd act${primary ? " primary" : ""}" type="button" data-act-key="${esc(key)}" data-act-body="${esc(body)}"${a.busy ? " disabled" : ""}>${esc(label)}</button>`;
    const ask = (label, body, primary) => `<button class="cmd act${primary ? " primary" : ""}" type="button" data-act-key="${esc(key)}" data-act-confirm="${esc(body)}"${a.busy ? " disabled" : ""}>${esc(label)}</button>`;
    let body = "", cls = "", actions = "";
    if (w.kind === "forks") {
      const forks = w.forks || [];
      body = forks.map((q) => `<p class="ask"><b>${esc(q.id)}</b>${esc(q.question)}</p><div class="opts">${(q.options || []).map((o) => {
        const on = a.chosen[q.id] === o.id;
        return `<button class="opt pick${on ? " chosen" : ""}" type="button" aria-pressed="${on}" data-act-key="${esc(key)}" data-act-pick="${esc(q.id)} ${esc(o.id)}"><span class="id">${esc(o.id)}</span><span>${esc(o.says)}</span><span class="cp">${on ? "chosen" : "choose"}</span></button>`;
      }).join("")}</div>`).join("");
      const picked = forks.filter((q) => a.chosen[q.id]);
      const lines = picked.map((q) => `/invariant choose ${q.id} ${a.chosen[q.id]}`).join("\n");
      actions = picked.length === forks.length && forks.length
        ? post(forks.length === 1 ? "Post my answer" : `Post my ${forks.length} answers`, lines, true)
        : `<button class="cmd act primary" type="button" disabled>Choose ${forks.length === 1 ? "an answer" : `all ${forks.length}`} to post</button>`;
      actions += ask("Draft again instead", "/invariant revise");
    } else if (w.kind === "proposal") {
      body = `${proposalText(w, repo, "you")}
        <details class="stmts"><summary>Read what they say</summary><ul>${(w.statements || []).map((st) => `<li><span class="k">${esc(st.kind)}</span><span><b>${esc(st.name)}</b>${esc(st.says)}</span></li>`).join("")}</ul></details>`;
      actions = ask(`Ratify ${w.hash}`, `/invariant ratify ${w.hash}`, true) + ask("Draft again", "/invariant revise");
    } else if (w.kind === "closed") {
      body = `<p class="ask">${closedText(w, repo)}</p>`;
      actions = ask("Draft again", "/invariant revise", true);
    } else {
      cls = "failed";
      body = `<p class="ask">${why(w, repo)}</p>`;
      actions = ask("Retry", "/invariant retry", true) + (redraftable(w) ? ask("Draft again", "/invariant revise") : "");
    }
    let row = `<div class="cmds">${actions}${open}</div>`;
    if (a.confirm) {
      const what = a.confirm.startsWith("/invariant ratify") ? `Ratify <b>${esc(w.hash)}</b> as you? It pins these statements, and the factory starts building.`
        : a.confirm === "/invariant retry" ? "Have the factory try again, as you?"
        : w.kind === "failed" && w.pr && !stoppedBuild(w) ? `Have the factory draft again from the comments, as you? Close <b>${esc(ref(repo, w.pr))}</b> first, or it will ask you to.`
        : "Have the factory draft again from the comments, as you?";
      row = `<div class="confirm"><span>${what}</span>${post("Yes, post it", a.confirm, true)}<button class="cmd" type="button" data-act-key="${esc(key)}" data-act-cancel>Cancel</button></div>`;
    }
    if (a.posted) row = `<p class="posted">✓ Posted <a href="${esc(a.posted)}" target="_blank" rel="noopener">on GitHub</a> as you. The factory picks it up within 30 seconds.</p>`;
    if (a.error) row += `<p class="acterr">${esc(a.error)}</p>`;
    return `<article class="need ${cls}${quiet(repo)}" ${tag(repo)}><div>${needGlyph(w.kind)}</div><div>
      <div class="need-top"><span class="ref">${esc(ref(repo, n))}</span><span class="title">${esc(is.title)}</span><span class="since">waiting <span data-since="${esc(w.since)}"></span></span></div>${body}${row}</div></article>`;
  }
  if (ACT) {
    document.addEventListener("click", async (e) => {
      const b = e.target.closest("[data-batch]");
      if (!b || b.disabled || !state) return;
      batch.error = "";
      if (b.dataset.batch === "ask") batch.confirm = true;
      if (b.dataset.batch === "cancel") batch.confirm = false;
      if (b.dataset.batch === "post") {
        batch.busy = true;
        renderInbox(state);
        const same = state.issues.filter((i) => i.open && i.waiting && inScope(i.repo) && i.waiting.kind === "proposal" && i.waiting.unchanged);
        for (const i of same) {
          const a = actOf(`${i.repo}#${i.number}`);
          if (a.posted) continue;
          try {
            const r = await fetch("/act/api/comment", { method: "POST", headers: { "Content-Type": "application/json", "X-Invariant": "act" },
              body: JSON.stringify({ repo: i.repo, issue: i.number, body: `/invariant ratify ${i.waiting.hash}` }) });
            if (!r.ok) throw new Error((await r.text()).trim() || `HTTP ${r.status}`);
            a.posted = (await r.json()).url || `https://github.com/${i.repo}/issues/${i.number}`;
          } catch (err) {
            batch.error = `Stopped at ${ref(i.repo, i.number)}: ${err.message}`;
            break;
          }
        }
        batch.busy = false;
        batch.confirm = false;
      }
      renderInbox(state);
    });
    document.addEventListener("click", async (e) => {
      const b = e.target.closest("[data-act-key]");
      if (!b || b.disabled) return;
      const key = b.dataset.actKey, a = actOf(key);
      a.error = "";
      if (b.dataset.actPick) {
        const [q, o] = b.dataset.actPick.split(" ");
        a.chosen[q] = a.chosen[q] === o ? undefined : o;
      } else if (b.dataset.actConfirm) {
        a.confirm = b.dataset.actConfirm;
      } else if ("actCancel" in b.dataset) {
        a.confirm = "";
      } else if (b.dataset.actBody) {
        const [repo, n] = key.split("#");
        a.busy = true;
        if (state) renderInbox(state);
        try {
          const r = await fetch("/act/api/comment", { method: "POST", headers: { "Content-Type": "application/json", "X-Invariant": "act" },
            body: JSON.stringify({ repo, issue: Number(n), body: b.dataset.actBody }) });
          if (!r.ok) throw new Error((await r.text()).trim() || `HTTP ${r.status}`);
          a.posted = (await r.json()).url || `https://github.com/${repo}/issues/${n}`;
          a.confirm = "";
        } catch (err) {
          a.error = `Not posted: ${err.message}`;
        }
        a.busy = false;
      }
      if (state) renderInbox(state);
    });
    const lede = $("#needs-lede");
    if (lede) lede.textContent = "What the factory is waiting for from you, in every repository. You're signed in, so your answers post to GitHub as you: choose, then post. A ratify or a retry asks first. Or open a new issue for the factory to solve.";
    composer();
    const who = document.createElement("span");
    who.className = "pill acting";
    who.title = `Signed in as ${ACT} through Cloudflare Access. Your clicks in Needs you post to GitHub as you.`;
    who.innerHTML = "<i></i>Acting as you";
    document.querySelector(".top-right")?.prepend(who);
  }

  // A new issue for the factory to solve, from /act: what must be true,
  // where its project goes, and the code it checks, if any.
  function composer() {
    const box = $("#compose");
    if (!box) return;
    let open = false, busy = false, done = null, error = "", confirm = false;
    const draft = { repo: "", title: "", body: "", project: "", code: "", language: "" };
    const repos = () => (state?.repos || []).map((r) => r.name);
    const projects = () => (state?.projects || []).filter((p) => (p.repo || state.repo) === (draft.repo || repos()[0])).map((p) => p.dir);
    function paint() {
      if (!open) {
        box.innerHTML = `<button class="cmd act primary newissue" type="button" data-compose="open">＋ New issue for the factory</button>` +
          (done ? `<p class="posted">✓ Opened <a href="${esc(done.url)}" target="_blank" rel="noopener">${esc(ref(done.repo, done.number))}</a> as you. The factory starts drafting within 30 seconds.</p>` : "");
        return;
      }
      const repo = draft.repo || repos()[0] || "";
      box.innerHTML = `<form class="composer" autocomplete="off">
        <div class="row"><label>Repository<select name="repo">${repos().map((r) => `<option${r === repo ? " selected" : ""}>${esc(r)}</option>`).join("")}</select></label>
          <label>Language<select name="language"><option value="">the repository's</option>${["go", "typescript", "python"].map((l) => `<option value="${l}"${draft.language === l ? " selected" : ""}>${l === "go" ? "Go" : l === "typescript" ? "TypeScript" : "Python"}</option>`).join("")}</select></label></div>
        <label>Title<input name="title" maxlength="200" placeholder="Prove the lease protocol" value="${esc(draft.title)}"></label>
        <label>What must be true<textarea name="body" rows="7" placeholder="In plain language: what can happen, the rules that must always hold, and what must never happen. The factory asks about anything it can't decide.">${esc(draft.body)}</textarea></label>
        <div class="row"><label>Project <i>optional</i><input name="project" list="compose-projects" placeholder="a new directory, or a project to amend" value="${esc(draft.project)}"><datalist id="compose-projects">${projects().map((d) => `<option value="${esc(d)}">`).join("")}</datalist></label>
          <label>Code to check as it is <i>optional</i><input name="code" placeholder="src/lib, migrations/admin" value="${esc(draft.code)}"></label></div>
        ${confirm ? `<div class="confirm"><span>Open this as you, with <b>/invariant solve</b>? The factory starts drafting within 30 seconds.</span><button class="cmd act primary" type="button" data-compose="post"${busy ? " disabled" : ""}>Yes, open it</button><button class="cmd" type="button" data-compose="back">Not yet</button></div>`
          : `<div class="cmds"><button class="cmd act primary" type="button" data-compose="ask">Open it for the factory</button><button class="cmd" type="button" data-compose="close">Cancel</button></div>`}
        ${error ? `<p class="acterr">${esc(error)}</p>` : ""}
      </form>`;
    }
    box.addEventListener("input", (e) => {
      const f = e.target;
      if (f.name in draft) draft[f.name] = f.value;
      if (f.name === "repo") paint();
    });
    box.addEventListener("click", async (e) => {
      const b = e.target.closest("[data-compose]");
      if (!b || b.disabled) return;
      const what = b.dataset.compose;
      error = "";
      if (what === "open") { open = true; done = null; }
      if (what === "close") open = false;
      if (what === "back") confirm = false;
      if (what === "ask") {
        if (!draft.title.trim() || !draft.body.trim()) error = "Give it a title, and say what must be true.";
        else confirm = true;
      }
      if (what === "post") {
        busy = true; paint();
        const repo = draft.repo || repos()[0];
        try {
          const r = await fetch("/act/api/issue", { method: "POST", headers: { "Content-Type": "application/json", "X-Invariant": "act" },
            body: JSON.stringify({ repo, title: draft.title, body: draft.body, project: draft.project.trim(), language: draft.language,
              code: draft.code.split(/[,\n]/).map((c) => c.trim()).filter(Boolean) }) });
          if (!r.ok) throw new Error((await r.text()).trim() || `HTTP ${r.status}`);
          const j = await r.json();
          done = { repo, number: j.number, url: j.url };
          Object.assign(draft, { title: "", body: "", project: "", code: "", language: "" });
          open = false; confirm = false;
        } catch (err) {
          error = `Not opened: ${err.message}`; confirm = false;
        }
        busy = false;
      }
      paint();
    });
    paint();
  }

  // ---------- the fleet: every repository as a star system ----------
  const STAGE_ANGLE = { queued: -90, asking: -30, ratifying: 30, building: 90, gate: 150, review: 150, merged: 210 };
  const langClass = (l) => (l === "typescript" ? "ts-c" : l === "python" ? "py-c" : "go-c");
  function renderFleet(s) {
    const box = $("#fleet");
    const repos = (s.repos || []).filter((r) => inScope(r.name));
    box.innerHTML = repos.map((r, k) => systemCard(s, r, k)).join("");
    box.querySelectorAll("[data-model]").forEach((el) => el.addEventListener("click", () => {
      selectModel(el.dataset.model);
      $("#states").scrollIntoView({ behavior: REDUCED ? "auto" : "smooth" });
    }));
  }
  function spin(from, dur) {
    return REDUCED ? "" : `<animateTransform attributeName="transform" type="rotate" from="${from}" to="${from + 360}" dur="${dur}s" repeatCount="indefinite"/>`;
  }
  function systemCard(s, r, k) {
    const c = 200, R = 176;
    const projects = s.projects.filter((p) => p.repo === r.name).sort((a, b) => a.states - b.states);
    const issues = s.issues.filter((i) => i.repo === r.name);
    const open = issues.filter((i) => i.open), merged = issues.filter((i) => i.stage === "merged");
    const n = projects.length, inner = 50, outer = R - 40;
    const orbitR = (i) => (n <= 1 ? (inner + outer) / 2 : inner + ((outer - inner) * i) / (n - 1));
    const working = r.factory?.running && r.factory?.doing;
    const ciRunning = r.gate && r.gate.status !== "completed";
    let g = `<defs>
      <radialGradient id="corona${k}"><stop offset="0" style="stop-color:var(--ink);stop-opacity:.55"/><stop offset=".35" style="stop-color:var(--ink);stop-opacity:.12"/><stop offset="1" style="stop-color:var(--ink);stop-opacity:0"/></radialGradient>
      <linearGradient id="tail${k}" x1="0" x2="1"><stop offset="0" style="stop-color:var(--hi);stop-opacity:0"/><stop offset="1" style="stop-color:var(--hi);stop-opacity:.9"/></linearGradient>
    </defs>`;
    g += `<circle class="outer" cx="${c}" cy="${c}" r="${R}"/>`;
    for (const [stage, a] of Object.entries(STAGE_ANGLE)) {
      if (stage === "review") continue;
      const t = (a * Math.PI) / 180;
      g += `<circle class="station2" cx="${c + R * Math.cos(t)}" cy="${c + R * Math.sin(t)}" r="3.2"><title>${stage}</title></circle>`;
    }
    projects.forEach((p, i) => { g += `<circle class="orbitline" cx="${c}" cy="${c}" r="${orbitR(i)}"/>`; });
    // The star: brighter and quicker while the factory works here, with a blue ring while CI runs.
    g += `<circle cx="${c}" cy="${c}" r="${working ? 58 : 46}" fill="url(#corona${k})">${REDUCED ? "" : `<animate attributeName="r" values="${working ? "50;64;50" : "42;50;42"}" dur="${working ? 2.2 : 5}s" repeatCount="indefinite"/>`}</circle>`;
    if (ciRunning) g += `<circle cx="${c}" cy="${c}" r="16" fill="none" style="stroke:var(--ci)" stroke-width="2">${REDUCED ? "" : `<animate attributeName="r" values="14;34" dur="1.6s" repeatCount="indefinite"/><animate attributeName="opacity" values=".9;0" dur="1.6s" repeatCount="indefinite"/>`}</circle>`;
    g += `<circle class="star" cx="${c}" cy="${c}" r="9"/><circle cx="${c}" cy="${c}" r="4" style="fill:var(--accent)"/>`;
    projects.forEach((p, i) => {
      const orr = orbitR(i), pr = 4 + 2.4 * Math.log10(Math.max(10, p.states));
      const start = (i * 137.5 + k * 40) % 360, period = 46 + i * 23;
      const moons = merged.filter((m) => m.project === p.dir);
      const moonSvg = moons.map((m, j) => `<g>${spin(j * 120, 7 + j * 3)}<circle class="moon2" cx="${pr + 7 + j * 3}" cy="0" r="2"><title>${esc(ref(m.repo, m.number))} ${esc(m.title)}</title></circle></g>`).join("");
      const tip = `${cap(p.name)} · ${p.dir}\n${nf.format(p.states)} states · ${p.assurance}\n${p.bugs.filter((b) => b.caught).length}/${p.bugs.length} planted bugs caught`;
      g += `<g transform="translate(${c} ${c})"><g transform="rotate(${start})">${spin(start, period)}<g class="planet" data-model="${esc(p.model)}" transform="translate(${orr} 0)"><title>${esc(tip)}</title>
        <circle class="${p.passed ? "holds" : "fails"}" r="${pr + 4}"/><circle class="body ${langClass(p.language)}" r="${pr}"/>${moonSvg}</g></g></g>`;
    });
    // Comets: open issues on the outer orbit, parked at their stage.
    open.forEach((is, j) => {
      const a = (STAGE_ANGLE[is.stage] ?? -90) + j * 9, t = (a * Math.PI) / 180;
      const x = c + R * Math.cos(t), y = c + R * Math.sin(t);
      const cls = is.stage === "review" ? "bug" : is.stage === "asking" || is.stage === "ratifying" ? "people" : "";
      const tailA = ((a - 28) * Math.PI) / 180;
      g += `<path d="M${c + R * Math.cos(tailA)} ${c + R * Math.sin(tailA)} A${R} ${R} 0 0 1 ${x} ${y}" fill="none" stroke="url(#tail${k})" stroke-width="3" stroke-linecap="round" opacity=".75"/>
        <circle class="comet-head ${cls}" cx="${x}" cy="${y}" r="6">${REDUCED ? "" : `<animate attributeName="r" values="5;7.5;5" dur="1.8s" repeatCount="indefinite"/>`}<title>${esc(ref(is.repo, is.number))} ${esc(is.title)}: ${esc(is.stage)}</title></circle>
        <text class="comet-label" x="${x + (x > c ? 10 : -10)}" y="${y - 10}" text-anchor="${x > c ? "start" : "end"}">${esc(ref(is.repo, is.number))}</text>`;
    });
    // A shooting star crosses the system while the factory works in it.
    if (working && !REDUCED) g += `<line x1="40" y1="60" x2="120" y2="20" stroke="url(#tail${k})" stroke-width="2" stroke-linecap="round" opacity="0">
        <animate attributeName="opacity" values="0;1;0" dur="2.8s" repeatCount="indefinite"/>
        <animateTransform attributeName="transform" type="translate" values="-60 40; 280 -20" dur="2.8s" repeatCount="indefinite"/></line>`;
    const legend = projects.length
      ? projects.slice().reverse().map((p) => `<span data-model="${esc(p.model)}" title="${esc(p.dir)}"><i class="${langClass(p.language)}"></i>${esc(cap(p.name))}</span>`).join("")
      : `<span>No projects yet. The first issue's project will orbit here.</span>`;
    const state = r.factory?.running ? `<span class="chip factory pulse state"><i></i>${working ? "working" : "watching"}</span>` : `<span class="chip state"><i></i>factory off</span>`;
    return `<article class="system${quiet(r.name)}" ${tag(r.name)}>
      <header><h3>${esc(r.short)}</h3><span class="meta">${n} project${n === 1 ? "" : "s"} · ${open.length} open · ${merged.length} merged</span>${state}</header>
      <svg class="map" viewBox="0 0 400 400" role="img" aria-label="${esc(r.short)}: ${n} projects, ${open.length} open issues">${g}</svg>
      <div class="legend2">${legend}</div></article>`;
  }

  // ---------- projects as a table ----------
  function renderTable(s, ps) {
    const box = $("#projects");
    box.className = "tablewrap";
    box.innerHTML = `<table class="ptable"><thead><tr><th>Project</th><th>Evidence</th><th>States</th><th>Statements</th><th>Bugs caught</th><th>Ratified</th></tr></thead><tbody>${ps.map((p) => {
      const caught = p.bugs.filter((b) => b.caught).length;
      const who = p.ratified ? `@${esc(p.ratified.by)} on ${esc(ref(p.repo, p.ratified.issue))}` : esc(p.decision || "by hand");
      return `<tr class="${quiet(p.repo)}" ${tag(p.repo)} data-model="${esc(p.model)}"><td class="name">${esc(cap(p.name))}<div class="repo">${p.repo !== s.repo ? esc(short(p.repo)) + " · " : ""}${esc(p.dir)}</div></td>
        <td>${esc(p.assurance)}${p.passed ? "" : ` · <span class="bad">failed</span>`}</td><td class="num">${nf.format(p.states)}</td><td class="num">${p.statements.length}</td>
        <td class="num ${caught === p.bugs.length ? "ok" : ""}">${caught}/${p.bugs.length}</td><td>${who}</td></tr>`;
    }).join("")}</tbody></table>`;
    box.querySelectorAll("tr[data-model]").forEach((el) => el.addEventListener("click", () => { selectModel(el.dataset.model); $("#states").scrollIntoView({ behavior: REDUCED ? "auto" : "smooth" }); }));
  }
  document.querySelectorAll(".viewtoggle button").forEach((b) => {
    b.setAttribute("aria-pressed", String(b.dataset.view === pview));
    b.addEventListener("click", () => {
      pview = b.dataset.view;
      try { localStorage.setItem("invariant-pview", pview); } catch (e) {}
      document.querySelectorAll(".viewtoggle button").forEach((x) => x.setAttribute("aria-pressed", String(x.dataset.view === pview)));
      if (state) render(state);
    });
  });

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

  // ---------- the running step, live (#97) ----------
  // /api/live.json is read from the watcher's work directory, every few
  // seconds while a step runs. It holds counts, times and file paths only,
  // never the agent's code, words or thinking (D-0051).
  const LIVE_KINDS = [["read", "reading"], ["search", "searching"], ["edit", "editing"], ["gate", "gate runs"], ["check", "draft checks"], ["test", "test runs"], ["other", "other tools"]];
  const LIVE_NOW = { thinking: "Thinking", read: "Reading a file", search: "Searching the code", edit: "Writing code", gate: "Running the gate", check: "Checking the draft", test: "Running the tests", other: "Using a tool" };
  const LIVE_DOING = { building: "Building", formalizing: "Drafting", answering: "Answering" };
  let live = null, liveKey = "", liveMarks = 0, liveRuns = 0, liveLit = "";
  const kilo = (n) => (n >= 1e6 ? `${(n / 1e6).toFixed(1)}M` : n >= 1e4 ? `${Math.round(n / 1e3)}k` : n >= 1e3 ? `${(n / 1e3).toFixed(1)}k` : String(n || 0));
  const bytes = (n) => (n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : n >= 1024 ? `${(n / 1024).toFixed(1)} KB` : `${n} B`);
  const clock = (s) => (s < 60 ? `${Math.floor(s)}s` : s < 3600 ? `${Math.floor(s / 60)}m` : `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`);
  const runName = (l) => ({ check: "Draft check", test: "Test run" }[l.runsKind] || "Gate run");

  async function loadLive() {
    let wait = 12000;
    try {
      const r = await fetch("/api/live.json", { cache: "no-store" });
      if (r.ok) {
        live = await r.json();
        renderLive();
        if ((live.steps || []).some((l) => !l.ended)) wait = 3000;
      }
    } catch (e) {}
    setTimeout(loadLive, wait);
  }

  // The step to show: the one running in the chosen repositories, or the
  // last one that ended.
  function liveStep() {
    const steps = (live?.steps || []).filter((l) => inScope(l.repo));
    return steps.find((l) => !l.ended) || steps[0];
  }

  function renderLive() {
    const sec = $("#live"), l = liveStep();
    sec.hidden = !l;
    if (!l) return;
    const key = `${l.repo}#${l.issue}@${l.since}`;
    if (key !== liveKey) {
      liveKey = key;
      liveMarks = liveRuns = 0;
      liveLit = "";
      sec.classList.remove("arrive");
      void sec.offsetWidth;
      sec.classList.add("arrive");
    }
    sec.classList.toggle("ended", !!l.ended);
    const issue = state?.issues.find((i) => i.repo === l.repo && i.number === l.issue);
    $("#live-title").innerHTML = `${esc(LIVE_DOING[l.doing] || cap(l.doing))} <span class="n">${esc(ref(l.repo, l.issue))}</span>${issue ? ` <q>${esc(issue.title)}</q>` : ""}`;

    // What the agent is doing now, or how its run ended.
    let now;
    if (l.ended) now = `This step ended <span data-ago="${esc(l.ended)}"></span>${l.agent ? `. The agent ${esc(l.agent)}` : ""}.`;
    else if (l.agent) now = `<b>The agent ${esc(l.agent)}.</b> The factory is checking its work.`;
    else {
      const thinking = l.now === "thinking" ? [...(l.marks || [])].reverse().find((m) => m.kind === "think" && !m.until) : null;
      now = `<b>${esc(LIVE_NOW[l.now] || "Working")}</b>${thinking ? ` for <span data-since="${esc(thinking.at)}"></span>` : ""}`;
      if (l.last) now += ` · last activity <span data-ago="${esc(l.last)}"></span>`;
    }
    $("#live-now").innerHTML = now;

    const calls = Object.values(l.tools || {}).reduce((a, b) => a + b, 0);
    const stats = [["Turns", nf.format(l.turns || 0), ""], ["Thinking", `≈${kilo(l.thinking)}`, "tokens"], ["Holds", kilo(l.context), "tokens in context"], ["Tool calls", nf.format(calls), ""]];
    if (l.output) stats.push(["Wrote", kilo(l.output), "output tokens"]);
    $("#live-stats").innerHTML = stats.map(([k, v, u]) => `<div class="stat"><span class="k">${k}</span><b>${v}</b>${u ? `<span class="u">${u}</span>` : ""}</div>`).join("");

    $("#live-legend").innerHTML = LIVE_KINDS.filter(([k]) => l.tools?.[k]).map(([k, word]) => `<span class="lg ${k}"><i></i>${word} ${nf.format(l.tools[k])}</span>`).join("")
      + (l.thinking ? `<span class="lg think"><i></i>thinking ≈${kilo(l.thinking)} tokens</span>` : "");

    $("#live-runs").innerHTML = (l.runs || []).map((r) => `<span class="chip ${r.passed ? "holds" : "bug"}"><i></i>${runName(l)} ${r.run} ${r.passed ? "passed" : "failed"}${!r.passed && r.failed?.length ? `: ${esc(r.failed.join(", "))}` : ""}</span>`).join("");

    const files = l.files || [], shown = files.slice(0, 12);
    $("#live-files").innerHTML = !files.length ? `<div class="muted">No files touched yet.</div>` : `<div class="k">Files it touched</div>` + shown.map((f) => {
      const cut = f.path.lastIndexOf("/") + 1;
      const fresh = f.lit && f.path !== liveLit && !REDUCED;
      return `<div class="file${f.lit ? " lit" : ""}${fresh ? " fresh" : ""}"><code><span class="dir">${esc(f.path.slice(0, cut))}</span>${esc(f.path.slice(cut))}</code>`
        + `<span class="counts">${f.reads ? `<span class="r">read ${f.reads}</span>` : ""}${f.edits ? `<span class="e">edited ${f.edits}</span>` : ""}</span><span class="size">${esc(bytes(f.size))}</span></div>`;
    }).join("") + (files.length > shown.length ? `<div class="more">and ${files.length - shown.length} more</div>` : "");
    liveLit = files.find((f) => f.lit)?.path || "";

    liveTick(true);
    liveMarks = (l.marks || []).length;
    liveRuns = (l.runs || []).length;
  }

  // liveTick moves the dial and the strip's clock. Only a render with new
  // data lands new marks.
  function liveTick(landing) {
    const l = liveStep();
    if (!l || $("#live").hidden) return;
    const end = l.ended ? T(l.ended) : Date.now();
    const elapsed = Math.max(0, (end - T(l.since)) / 1000);
    const frac = l.limit ? Math.min(1, elapsed / l.limit) : 0;
    const C = 2 * Math.PI * 52;
    const tone = frac > 0.95 ? "bug" : frac > 0.8 ? "people" : "accent";
    const arc = l.limit
      ? `<circle cx="64" cy="64" r="52" class="arc ${tone}" stroke-dasharray="${(C * frac).toFixed(1)} ${C.toFixed(1)}" transform="rotate(-90 64 64)"/>`
      : `<circle cx="64" cy="64" r="52" class="arc spin${l.ended ? " still" : ""}" stroke-dasharray="46 ${C.toFixed(1)}"/>`;
    $("#live-dial").innerHTML = `<svg viewBox="0 0 128 128" role="img" aria-label="${esc(`${clock(elapsed)}${l.limit ? ` of ${dur(l.limit)}` : ""}`)}"><circle cx="64" cy="64" r="52" class="track"/>${arc}`
      + `<text x="64" y="62" class="big">${esc(clock(elapsed))}</text><text x="64" y="84" class="small">${l.limit ? `of ${esc(dur(l.limit))}` : l.ended ? "ran" : "no limit set"}</text></svg>`;
    drawStrip(l, end, landing);
  }

  // The heartbeat: the step's whole run in time. A tick per tool call,
  // colored by kind, a band per stretch of thinking, brighter the faster it
  // thought, and a diamond per check, green when it passed.
  function drawStrip(l, end, landing) {
    const box = $("#live-strip");
    const W = Math.max(280, box.clientWidth), H = 100, top = 16, bottom = 70;
    const marks = l.marks || [];
    const t0 = T(l.since) || (marks[0] ? T(marks[0].at) : end);
    const span = Math.max(60000, end - t0);
    const x = (t) => 8 + ((T(t) - t0) / span) * (W - 16);
    const parts = [];
    const steps = [60, 300, 600, 900, 1800, 3600, 7200].map((s) => s * 1000);
    const step = steps.find((s) => span / s <= Math.max(3, Math.floor(W / 90))) || 7200000;
    for (let g = Math.ceil(t0 / step) * step; g < end; g += step) {
      const gx = x(g).toFixed(1);
      parts.push(`<line x1="${gx}" x2="${gx}" y1="${top - 8}" y2="${bottom + 4}" class="grid"/><text x="${gx}" y="${H - 4}" class="tick">${esc(clockOf(g))}</text>`);
    }
    for (const m of marks) {
      if (m.kind !== "think") continue;
      const a = x(m.at), stop = m.until ? T(m.until) : end, b = x(stop);
      const rate = Math.min(1, (m.tokens || 0) / Math.max(0.2, (stop - T(m.at)) / 60000) / 9000);
      parts.push(`<rect x="${a.toFixed(1)}" y="${top + 9}" width="${Math.max(2, b - a).toFixed(1)}" height="${bottom - top - 18}" rx="4" class="think${m.until ? "" : " on"}" fill-opacity="${(0.22 + 0.6 * rate).toFixed(2)}"><title>Thinking, ≈${kilo(m.tokens)} tokens</title></rect>`);
    }
    marks.forEach((m, i) => {
      if (m.kind === "think") return;
      const mx = x(m.at).toFixed(1);
      const land = landing && i >= liveMarks && !REDUCED ? ` new" style="animation-delay:${Math.min(i - liveMarks, 60) * 14}ms` : "";
      parts.push(`<line x1="${mx}" x2="${mx}" y1="${top}" y2="${bottom}" class="tool ${esc(m.kind)}${land}"><title>${esc(LIVE_NOW[m.kind] || m.kind)} · ${esc(clockOf(m.at))}</title></line>`);
    });
    (l.runs || []).forEach((r, i) => {
      if (!r.at) return;
      const label = `${runName(l)} ${r.run} ${r.passed ? "passed" : "failed"}${!r.passed && r.failed?.length ? ": " + r.failed.join(", ") : ""}`;
      const flash = landing && i >= liveRuns && !REDUCED ? " new" : "";
      parts.push(`<g transform="translate(${x(r.at).toFixed(1)} ${bottom + 12})"><path d="M0 -7L7 0L0 7L-7 0Z" class="checkpoint ${r.passed ? "pass" : "fail"}${flash}"><title>${esc(label)}</title></path></g>`);
    });
    if (!l.ended) {
      const nx = x(end).toFixed(1);
      parts.push(`<line x1="${nx}" x2="${nx}" y1="${top - 10}" y2="${bottom + 6}" class="nowline"/><circle cx="${nx}" cy="${top - 10}" r="3.5" class="nowdot"/>`);
    }
    const said = `${marks.filter((m) => m.kind !== "think").length} tool calls and ${marks.filter((m) => m.kind === "think").length} stretches of thinking over ${clock(span / 1000)}`;
    box.innerHTML = `<svg width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" role="img" aria-label="${esc(said)}">${parts.join("")}</svg>`;
  }
  new ResizeObserver(() => liveTick(false)).observe($("#live-strip"));

  cosmos.init();
  load();
  loadLive();
  setInterval(tick, 1000);
})();
