package dashboard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A choice in the Show bar leaves nothing behind (#886). Before this, a
// choice left a fade's class on up to 76 elements until that part of the
// page was drawn again or the page was read again, and two quick choices
// could leave an element unseen and unclickable. The bar was 125 pixels
// wider than its column on every desktop, so its last choice was cut off,
// and choosing it scrolled the bar sideways, where it stayed. Each choice
// wrote the bar again, so the one under the pointer was a new element, and
// the bar said nothing had been chosen until the page under it was drawn.
// Each dot took the header badge's padding and border, and was an oval. In
// a browser that doesn't hold a reader's place, a choice made down the page
// moved their section by up to 2,016 pixels.
//
// This holds the page to what makes each of those not so: the choices are
// the same elements after any order of choices, the current one is marked
// at once, no fade's class or held fade is left, nothing tagged with a
// repository is unseen or takes no clicks, the bar fits its column at every
// width down to a phone's by leaving out the same parts of every choice,
// each dot is seven pixels round, and the reader's section stays where it
// was. A browser run here draws no frames, so no fade plays: the check
// ends each one itself, as a browser that draws does, and holds each fade
// to naming one end only, which is what lets a card with an entrance of
// its own come in steadily. Measured where frames are drawn, such a card
// faded to both ends sat unseen for half a second and then appeared.
func TestAChoiceInShowLeavesNothingBehind886(t *testing.T) {
	browser := os.Getenv("INVARIANT_DASHBOARD_BROWSER")
	if browser == "" {
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome"} {
			if p, err := exec.LookPath(name); err == nil {
				browser = p
				break
			}
		}
	}
	if browser == "" {
		t.Skip("browser check unavailable: set INVARIANT_DASHBOARD_BROWSER to an installed Chromium executable")
	}
	mine, _ := queryJournal(t, 3)
	theirs, _ := queryJournal(t, 1)
	graph := func(short, says string, files map[string][]byte) *DecisionGraph {
		g, err := BuildDecisionGraph(short, files)
		if err != nil {
			t.Fatal(err)
		}
		for i := range g.Nodes {
			g.Nodes[i].Text, g.Nodes[i].Says = says, says
		}
		return g
	}
	log := "| ID | Date | Door | Status | Decision | Who |\n| :-- | :-- | :-- | :-- | :-- | :-- |\n" +
		"| D-0001 | 2026-11-09 | two-way | decided | Invariant's own decision. | agent |\n"
	const own, other = "gitdek/invariant", "gitdek/ask3d"
	s := &Server{Cache: t.TempDir(), Log: t.Logf, Repos: []*Repo{
		{Name: own, src: sources{log: log, graph: graph("invariant", "Invariant's own decision.", queryFiles(t, mine))}},
		{Name: other, src: sources{graph: graph("ask3d", "ask3d's own decision.", queryFiles(t, theirs))}},
	}}
	now := time.Now()
	snap := s.assemble(now)
	// What the real page's bar has to make room for: three figures for
	// each choice, and a count beside them.
	figures := func(t Totals, proved, models, tested int) Totals {
		t.Proved, t.Models, t.States, t.Tested, t.Tests = proved, models, int64(models)*1000, tested, tested*3
		return t
	}
	snap.Totals = figures(snap.Totals, 232, 20, 12)
	for i := range snap.Repos {
		if snap.Repos[i].Name == own {
			snap.Repos[i].Totals = figures(snap.Repos[i].Totals, 231, 14, 9)
		} else {
			snap.Repos[i].Totals = figures(snap.Repos[i].Totals, 1, 6, 3)
		}
	}
	// And things from each repository down the page: one issue that needs
	// a person, one that's open, the merged ones, and what happened.
	since := now.Add(-time.Hour)
	snap.Issues = []Issue{
		{Repo: own, Number: 1, Title: "Issue 1", Open: true, Stage: StageAsking,
			Spans:   []Span{{From: since, To: now, Who: WhoPeople, Open: true}},
			Waiting: &Waiting{Kind: "closed", Since: since, PR: 1001, Takes: []string{"revise"}}},
		{Repo: other, Number: 2, Title: "Issue 2", Open: true, Stage: StageBuilding},
	}
	for n := 100; n < 112; n++ {
		repo := own
		if n%3 == 0 {
			repo = other
		}
		snap.Issues = append(snap.Issues, Issue{Repo: repo, Number: n, Title: fmt.Sprintf("Merged %d", n), Stage: StageMerged, PR: n + 1000})
	}
	for n := 0; n < 16; n++ {
		repo := own
		if n%2 == 1 {
			repo = other
		}
		snap.Activity = append(snap.Activity, Event{At: now.Add(-time.Duration(n+1) * time.Minute), Repo: repo, Who: "ci", Kind: "success", Text: fmt.Sprintf("gate passed, run %d", n)})
	}
	script := fmt.Sprintf("const fixture = %s;\n", reader631JSON(t, snap)) + showBarBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/show-bar-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/show-bar-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	out, err := browserDOM(t, browser, server.URL+"/", 60000, "--window-size=1440,900")
	if err != nil || !strings.Contains(string(out), `data-show-bar="passed"`) {
		report := string(out)
		if i := strings.Index(report, `data-show-bar="`); i >= 0 {
			report = report[i:]
			if j := strings.Index(report[len(`data-show-bar="`):], `"`); j >= 0 {
				report = report[:len(`data-show-bar="`)+j+1]
			}
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("the Show bar, in a browser: %v\n%s", err, report)
	}
}

const showBarBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.slices ||= [];
  const errors = [];
  addEventListener("error", (e) => errors.push(e.message));
  addEventListener("unhandledrejection", (e) => errors.push(String(e.reason)));
  const json = (v) => new Response(JSON.stringify(v), {status: 200, headers: {"Content-Type": "application/json"}});
  window.fetch = (url) => {
    if (String(url).includes("/api/state.json")) return Promise.resolve(json(structuredClone(fixture)));
    if (String(url).includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected fixture request: " + url));
  };
  const later = window.setTimeout.bind(window);
  const wait = (ms) => new Promise((resolve) => later(resolve, ms));
  const assert = (ok, label) => { if (!ok) throw new Error(label + (errors.length ? "; the page threw: " + errors.join("; ") : "")); };
  async function until(test, label) {
    for (let i = 0; i < 400; i++) { if (test()) return; await wait(10); }
    assert(false, "never: " + label);
  }
  const OWN = "gitdek/invariant", OTHER = "gitdek/ask3d";
  const nav = () => document.querySelector("#scope");
  const links = () => [...nav().querySelectorAll("a")];
  const link = (scope) => nav().querySelector('a[data-scope="' + scope + '"]');
  const chosen = () => links().filter((a) => a.getAttribute("aria-current") === "page").map((a) => a.dataset.scope).join(",");
  const shown = (el) => el.getClientRects().length > 0;
  const tagged = () => [...document.querySelectorAll("[data-repo]")].filter((el) => el.dataset.repo && shown(el));
  const name = (s) => s.id || s.querySelector("h2")?.textContent.trim() || s.className;
  // A fade the page started itself, not one a style sheet's rule gives.
  const scripted = (a) => !(a instanceof CSSAnimation) && !(a instanceof CSSTransition);

  // A browser that draws frames plays each fade to its end. This one draws
  // none, so the check ends every animation that can end, as time would.
  function passTime() {
    for (const a of document.getAnimations()) { try { a.finish(); } catch (e) {} }
  }
  // The page has drawn for the choice: the wait is longer than a fade away.
  async function settled(scope, label) {
    assert(chosen() === scope, label + ": the bar marks " + (chosen() || "nothing") + " at once, not " + scope);
    await wait(900);
    // A draw that a later choice should have called off fades the page in
    // a second time.
    const twice = tagged().filter((el) => el.getAnimations().filter(scripted).length > 1);
    assert(!twice.length, label + ": " + twice.length + " elements fade in more than once");
    // A fade names one end, nothing, and leaves the other to the element's
    // own rules. One that names both holds a card whose own entrance starts
    // at nothing there for the whole fade, and then it appears at once.
    const both = document.getAnimations().filter((a) => scripted(a) && a.effect?.target?.dataset?.repo && a.effect.getKeyframes().length !== 1);
    assert(!both.length, label + ": " + both.length + " fades name both of their ends");
    passTime();
    await wait(50);
    assert(chosen() === scope, label + ": the bar marks " + (chosen() || "nothing") + " once drawn, not " + scope);
  }
  // Nothing is left on the page from a choice.
  function clean(label) {
    const classes = document.querySelectorAll(".arriving, .leaving, .arrive").length;
    assert(!classes, label + ": " + classes + " elements keep a fade's class");
    const held = document.getAnimations().filter((a) => scripted(a) && a.effect?.getComputedTiming().fill !== "none");
    assert(!held.length, label + ": " + held.length + " fades are still held on their elements");
    const all = tagged();
    assert(all.length > 8, label + ": only " + all.length + " elements say which repository they're from");
    const unseen = all.filter((el) => getComputedStyle(el).opacity === "0" || getComputedStyle(el).pointerEvents === "none");
    assert(!unseen.length, label + ": " + unseen.length + " of " + all.length + " elements are unseen or take no clicks, the first a " + (unseen[0]?.tagName || "") + "." + (unseen[0]?.getAttribute("class") || ""));
    const scope = chosen();
    const out = all.filter((el) => scope !== "all" && el.dataset.repo !== scope);
    assert(!out.length, label + ": " + out.length + " elements from another repository are still shown");
  }
  // The bar fits its column, with every choice whole and where it was, and
  // leaves the same parts out of every choice.
  function fits(label) {
    const n = nav(), box = n.getBoundingClientRect();
    assert(n.scrollWidth <= n.clientWidth + 1, label + ": the bar is " + (n.scrollWidth - n.clientWidth) + " pixels wider than its column of " + n.clientWidth);
    assert(n.scrollLeft === 0, label + ": the bar is scrolled " + n.scrollLeft + " pixels sideways");
    assert(links().length === 3, label + ": the bar has " + links().length + " choices");
    let dropped = 0;
    for (const a of links()) {
      const r = a.getBoundingClientRect();
      assert(r.left >= box.left - 1 && r.right <= box.right + 1, label + ": " + a.dataset.scope + " is cut off");
      const est = a.querySelector(".est"), full = [...est.querySelectorAll(".p")], says = full.filter(shown), then = est.querySelector(".then");
      assert(full.length === 3 && est.textContent.split(" · ").filter(Boolean).length === 3, label + ": " + a.dataset.scope + " doesn't hold its three figures: " + est.textContent);
      assert(says.every((part, i) => part === full[i]), label + ": " + a.dataset.scope + " leaves out a part before one it keeps");
      assert(then && shown(then) === says.length > 0, label + ": " + a.dataset.scope + "'s count is set off from nothing, or not set off from what's before it");
      dropped = Math.max(dropped, full.length - says.length);
      assert(full.every((part) => a.title.includes(part.textContent.split(" ").pop())) && a.getAttribute("aria-label").includes(a.title), label + ": " + a.dataset.scope + " doesn't keep its words in full for a tooltip and a screen reader");
    }
    for (const a of links()) {
      const full = [...a.querySelectorAll(".est .p")], says = full.filter(shown);
      assert(says.length === Math.max(0, full.length - dropped), label + ": " + a.dataset.scope + " keeps " + says.length + " of its " + full.length + " parts where another choice drops " + dropped);
    }
    const tight = n.classList.contains("tight"), label0 = n.querySelector(".lbl"), words = [...n.querySelectorAll(".ct .w")];
    assert(!tight || dropped === 3, label + ": the bar drops its label and words while a choice still says what's established");
    assert(!n.classList.contains("tighter") || tight, label + ": the bar drops its dots before its label and words");
    assert(shown(label0) === !tight, label + ": the bar's label is " + (shown(label0) ? "shown" : "hidden") + " in a bar that's " + (tight ? "tight" : "not tight"));
    assert(words.length === 3 && words.every((w) => shown(w) === !tight), label + ": the words beside the counts don't follow the label");
    // The counts stay whatever else goes.
    assert(/^1/.test(link(OWN).querySelector(".needs")?.textContent || ""), label + ": invariant's choice doesn't say one needs a person");
    assert(/^1/.test(link(OTHER).querySelector(".ct .w")?.parentElement.textContent.replace(link(OTHER).querySelector(".est").textContent, "") || ""), label + ": ask3d's choice doesn't count its open issue");
    return { dropped, tight };
  }
  function dots(label) {
    for (const a of links()) {
      const d = a.querySelector("i.lamp");
      assert(d, label + ": " + a.dataset.scope + " has no dot");
      if (!shown(d)) continue;
      const r = d.getBoundingClientRect(), s = getComputedStyle(d);
      assert(Math.abs(r.width - 7) < 0.6 && Math.abs(r.height - 7) < 0.6, label + ": " + a.dataset.scope + "'s dot is " + r.width + " by " + r.height + ", not 7 by 7");
      assert(s.borderTopWidth === "0px" && s.paddingLeft === "0px", label + ": " + a.dataset.scope + "'s dot has the header badge's border or padding");
    }
  }

  async function check() {
    await until(() => links().length === 3 && chosen() === "all" && tagged().length > 8, "the first draw, with three choices");
    passTime();
    const first = links();
    const same = (label) => assert(links().length === 3 && links().every((a, i) => a === first[i]), label + ": the bar's choices were written again");

    // On a fresh page.
    clean("fresh");
    const wide = fits("fresh");
    dots("fresh");
    assert(!wide.tight, "a desktop's bar drops its label");

    // Every order of two choices, each left to settle.
    const order = [OWN, "all", OTHER, OWN, OTHER, "all"];
    for (const scope of order) {
      link(scope).click();
      await settled(scope, "chose " + scope);
      same("chose " + scope);
      clean("chose " + scope);
      const now = fits("chose " + scope);
      assert(now.dropped === wide.dropped && now.tight === wide.tight, "chose " + scope + ": the bar says less or more than before the choice");
      dots("chose " + scope);
    }

    // Choices made before the last one is drawn.
    // The page shows everything now. Going there and back before the draw
    // draws nothing again, so what had started to fade away must come back.
    for (const quick of [[OWN, "all"], [OTHER, "all"], [OWN, OTHER], [OWN, OTHER], [OTHER, "all"], [OWN, "all", OTHER], ["all", OTHER], [OWN, OTHER, OWN, "all"], [OTHER, OWN, "all"]]) {
      const from = chosen();
      for (const scope of quick) link(scope).click();
      const last = quick[quick.length - 1], label = "from " + from + ", chose " + quick.join(", then at once ");
      await settled(last, label);
      same(label);
      clean(label);
      fits(label);
    }
    // And one made while the last is still fading in.
    link(OWN).click();
    await wait(450);
    link("all").click();
    await wait(100);
    link(OTHER).click();
    await settled(OTHER, "chose three, each as the last was drawn");
    clean("chose three, each as the last was drawn");
    link("all").click();
    await settled("all", "back to everything");
    clean("back to everything");

    // A narrower column: the bar still fits, by saying less of every choice.
    const main = document.querySelector("main");
    let last = wide;
    for (const px of [1000, 760, 560, 440]) {
      main.style.maxWidth = px + "px";
      dispatchEvent(new Event("resize"));
      const now = fits("in a column of " + px);
      assert(now.dropped >= last.dropped && (now.tight || !last.tight), "a narrower column of " + px + " says more than a wider one");
      dots("in a column of " + px);
      last = now;
    }
    assert(last.tight, "a phone's column keeps the bar's label and words, with fonts this wide");
    // A choice there works as anywhere, and moves nothing in the bar.
    link(OWN).click();
    await settled(OWN, "chose invariant in a narrow column");
    same("chose invariant in a narrow column");
    fits("chose invariant in a narrow column");
    link("all").click();
    await settled("all", "back to everything in a narrow column");
    main.style.maxWidth = "";
    dispatchEvent(new Event("resize"));
    const back = fits("back in a wide column");
    assert(back.dropped === wide.dropped && !back.tight, "the bar doesn't say again what it said before it was narrow");

    // A choice made down the page leaves the reader's section where it was.
    // The browser's own holding of a reader's place is off, as in one that
    // has none.
    document.documentElement.style.overflowAnchor = "none";
    document.body.style.overflowAnchor = "none";
    document.querySelector("footer").style.marginBottom = "4000px";
    const sections = () => [...document.querySelectorAll("main > section")].filter(shown);
    const under = () => nav().getBoundingClientRect().bottom;
    const reader = () => sections().find((s) => s.getBoundingClientRect().bottom > under() + 1);
    async function from(section, scope, label) {
      // The section starts ten pixels above the bar's lower edge.
      scrollBy(0, section.getBoundingClientRect().top - under() + 10);
      assert(reader() === section, label + ": the check isn't in " + name(section) + " but in " + name(reader()));
      const top = section.getBoundingClientRect().top, y = scrollY;
      link(scope).click();
      await settled(scope, label);
      return { top, y };
    }
    const lastSection = sections().at(-1);
    for (const scope of [OTHER, "all", OWN, OTHER, "all"]) {
      const label = "chose " + scope + " from " + name(lastSection);
      const was = await from(lastSection, scope, label);
      const moved = lastSection.getBoundingClientRect().top - was.top;
      assert(Math.abs(moved) <= 1, label + ": the section moved " + Math.round(moved) + " pixels");
      clean(label);
    }
    // What stands above it does change with the choice, or the check above
    // would hold on a page that kept no place at all.
    const road = document.querySelector("#road").closest("section");
    assert(shown(road), "everything's view doesn't show the road");
    const before = lastSection.getBoundingClientRect().top + scrollY;
    link(OTHER).click();
    await settled(OTHER, "chose ask3d");
    assert(!shown(road), "ask3d's view shows Invariant's road");
    assert(Math.abs(lastSection.getBoundingClientRect().top + scrollY - before) > 100, "the page above the last section is the same height in both views, so the check proves nothing");
    link("all").click();
    await settled("all", "back to everything");

    // A section the choice leaves out gives its place to the one before
    // it, which starts under the bar.
    assert(shown(road), "everything's view doesn't show the road again");
    await from(road, OTHER, "chose ask3d from the road");
    const now = reader();
    assert(now && now !== road && shown(now), "the reader is nowhere once the road is gone");
    assert(Math.abs(now.getBoundingClientRect().top - under()) <= 1, "the section before the road starts " + Math.round(now.getBoundingClientRect().top - under()) + " pixels from the bar's lower edge");
    assert(sections().indexOf(now) === sections().length - 2, "the reader is in " + name(now) + ", not the section before where the road was");
    clean("chose ask3d from the road");
    assert(!errors.length, "the page threw");
  }
  addEventListener("load", () => check().then(
    () => { document.body.dataset.showBar = "passed"; },
    (e) => { document.body.dataset.showBar = "failed: " + e.message; }));
})();
`
