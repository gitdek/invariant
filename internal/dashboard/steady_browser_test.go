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

// The page is watched for hours, so nothing on it starts over or drifts
// while nothing has changed (#845). A read that differs only in a watcher's
// heartbeat and lease leaves the fleet's elements where they are. The
// orbit draws only the merged issues that fit inside it. A card already in
// Needs you is drawn again without its entrance, and a new one fades in.
// On a phone the "updated" label is left out unless it has something to
// say.
func TestNothingStartsOverWhileThePageIsWatched(t *testing.T) {
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
	s, _, _ := receipt723Load(t, receipt723Fixture(t), nil)
	now := time.Now()
	snap := s.assemble(now)
	primary := snap.Repo
	since := now.Add(-time.Hour)
	snap.Issues = []Issue{{Repo: primary, Number: 1, Title: "Issue 1", Open: true, Stage: StageAsking,
		Spans:   []Span{{From: since, To: now, Who: WhoPeople, Open: true}},
		Waiting: &Waiting{Kind: "closed", Since: since, PR: 1001, Takes: []string{"revise"}}}}
	// Forty merged issues: far more than fit inside the orbit.
	for n := 100; n < 140; n++ {
		snap.Issues = append(snap.Issues, Issue{Repo: primary, Number: n, Title: fmt.Sprintf("Merged %d", n), Stage: StageMerged, PR: n + 1000})
	}
	script := fmt.Sprintf("const fixture = %s;\n", reader631JSON(t, snap)) + steadyBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/steady-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/steady-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	// The page reads the state again every 15 seconds of its own time, and
	// the check needs three reads.
	out, err := browserDOM(t, browser, server.URL+"/", 50000)
	if err != nil || !strings.Contains(string(out), `data-steady-check="passed"`) {
		report := string(out)
		if i := strings.Index(report, `id="steady-report"`); i >= 0 {
			report = report[i:min(len(report), i+1500)]
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("the steady page, in a browser: %v\n%s", err, report)
	}
}

const steadyBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  let reads = 0;
  window.fetch = (url) => {
    if (url === "/api/state.json") {
      reads++;
      const state = structuredClone(fixture);
      // Every read after the first has a later heartbeat and lease, as a
      // watcher writes each minute. The third also has one more issue waiting.
      if (reads > 1) {
        const later = new Date(Date.now() + reads * 60000).toISOString();
        for (const r of state.repos) {
          r.factory = {...(r.factory || {}), heartbeat: later};
          r.lease = {holder: "watcher-1", until: later};
        }
        state.factory = {...(state.factory || {}), heartbeat: later};
      }
      if (reads > 2) state.issues.push({...structuredClone(fixture.issues[0]), number: 2, title: "Issue 2"});
      return Promise.resolve(json(state));
    }
    if (url.includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const pause = () => new Promise((resolve) => setTimeout(resolve, 10));
  async function until(test, label) {
    for (let i = 0; i < 4000; i++) { if (test()) return; await pause(); }
    throw new Error("never: " + label);
  }
  async function run() {
    const box = document.querySelector("#inbox"), fleet = document.querySelector("#fleet");
    await until(() => box.querySelectorAll(".need").length === 1 && fleet.firstElementChild, "the first draw");
    // The orbit holds only the moons that fit, each inside its ring.
    const moons = [...document.querySelectorAll("#moons .moon")];
    assert(moons.length === 9, "the orbit draws " + moons.length + " of 40 merged issues' moons, not the 9 that fit");
    assert(moons.every((m) => Number(m.getAttribute("cx")) < 160), "a moon lies outside the orbit: " + moons.map((m) => m.getAttribute("cx")).join(" "));
    assert(document.querySelector("#coreNum").textContent.trim() !== "9", "the count is the moons drawn, not the issues merged");
    // A read with only a later heartbeat and lease leaves the fleet as it is.
    const kept = fleet.firstElementChild;
    kept.dataset.kept = "1";
    const card = box.querySelector(".need");
    await until(() => reads >= 2, "the second read");
    await pause();
    assert(fleet.firstElementChild === kept && fleet.firstElementChild.dataset.kept === "1", "the fleet was rebuilt for a heartbeat");
    assert(box.querySelector(".need") === card, "Needs you was drawn again for a heartbeat");
    // A new card fades in. The one already there is drawn again without it.
    await until(() => reads >= 3 && box.querySelectorAll(".need").length === 2, "the third read's card");
    const cards = Object.fromEntries([...box.querySelectorAll(".need")].map((el) => [el.dataset.issue.split("#")[1], el]));
    assert(cards["1"].classList.contains("shown") && getComputedStyle(cards["1"]).animationName === "none", "a card already shown animates in again");
    assert(!cards["2"].classList.contains("shown") && getComputedStyle(cards["2"]).animationName === "needin", "a new card doesn't animate in: " + getComputedStyle(cards["2"]).animationName);
    // When the fleet is drawn again, each body carries on from where it was.
    const spins = [...fleet.querySelectorAll("animateTransform")];
    assert(spins.length > 0 && spins.some((a) => Number(a.getAttribute("from")) % 120 !== 0), "the fleet's bodies start again from their first angle: " + spins.slice(0, 4).map((a) => a.getAttribute("from")).join(" "));
    // On a phone the updated label is left out, unless something is wrong.
    let hidden = false, shown = false, brief = false;
    for (const sheet of document.styleSheets) {
      let rules = [];
      // A sheet from another origin keeps its rules to itself.
      try { rules = sheet.cssRules; } catch (e) { continue; }
      for (const rule of rules) {
        if (!(rule instanceof CSSMediaRule) || !/max-width:\s*560px/.test(rule.conditionText)) continue;
        for (const inner of rule.cssRules) {
          if (inner.selectorText === "#updated" && inner.style.display === "none") hidden = true;
          if (/#updated\.lost/.test(inner.selectorText || "") && inner.style.display === "inline-block") shown = true;
          if (inner.selectorText === ".scope a .ct .est" && inner.style.display === "none") brief = true;
        }
      }
    }
    assert(hidden && shown, "at a phone's width the updated label isn't left out, or can't say the server is lost");
    // And Show's choices leave out what's established there, so each fits.
    assert(brief, "at a phone's width Show's choices keep everything they say on a wide screen");
    document.body.dataset.steadyCheck = "passed";
  }
  addEventListener("DOMContentLoaded", () => run().catch((error) => {
    document.body.dataset.steadyCheck = "failed";
    const report = document.createElement("pre");
    report.id = "steady-report";
    report.textContent = error.stack;
    document.body.append(report);
  }));
})();
`
