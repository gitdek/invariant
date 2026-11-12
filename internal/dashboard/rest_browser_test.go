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

// A section that's out of sight rests: its SVG animations are paused and
// its CSS animations stopped, while the one in view goes on (#855). And the
// page opens without a jump: the Show bar keeps its row until the page
// knows what it holds, and the first draw waits for the running steps.
func TestGraphicsOutOfSightRestAndThePageOpensInPlace(t *testing.T) {
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
	snap := s.assemble(time.Now())
	other := snap.Repos[0]
	other.Name, other.Short, other.Primary = "gitdek/other", "other", false
	snap.Repos = append(snap.Repos, other)
	// The dashboard serves two repositories, so its page keeps the Show
	// bar's row. A page of one keeps none (#867).
	s.Repos = append(s.Repos, &Repo{Name: other.Name})
	script := fmt.Sprintf("const fixture = %s;\n", reader631JSON(t, snap)) + restBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/rest-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	out, err := browserDOM(t, browser, server.URL+"/", 30000)
	if err != nil || !strings.Contains(string(out), `data-rest-check="passed"`) {
		report := string(out)
		if i := strings.Index(report, `id="rest-report"`); i >= 0 {
			report = report[i:min(len(report), i+1500)]
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("resting graphics, in a browser: %v\n%s", err, report)
	}
}

const restBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  let liveAnswered = 0;
  window.fetch = (url) => {
    if (url === "/api/state.json") return Promise.resolve(json(fixture));
    // The running steps answer a little later than the snapshot.
    if (url.includes("/api/live.json")) return new Promise((resolve) => setTimeout(() => { liveAnswered = performance.now(); resolve(json({steps: []})); }, 300));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const pause = (ms = 10) => new Promise((resolve) => setTimeout(resolve, ms));
  async function until(test, label) {
    for (let i = 0; i < 2000; i++) { if (test()) return; await pause(); }
    throw new Error("never: " + label);
  }
  async function run() {
    const scope = document.querySelector("#scope"), headline = document.querySelector("#headline");
    // Before anything is read, the Show bar holds its row.
    assert(scope.classList.contains("pending") && scope.offsetHeight >= 52 && !scope.children.length, "the empty Show bar holds " + scope.offsetHeight + " px");
    const heroTop = document.querySelector("section.hero").getBoundingClientRect().top;
    let drawnAt = 0;
    new MutationObserver(() => { if (!drawnAt) drawnAt = performance.now(); }).observe(headline, {childList: true, characterData: true, subtree: true});
    await until(() => scope.children.length > 0 && drawnAt, "the first draw");
    assert(liveAnswered && drawnAt >= liveAnswered, "the first draw came " + Math.round(liveAnswered - drawnAt) + " ms before the running steps answered");
    assert(!scope.classList.contains("pending"), "the filled Show bar still holds a reserved row");
    const moved = document.querySelector("section.hero").getBoundingClientRect().top - heroTop;
    assert(Math.abs(moved) < 1, "the headline moved " + moved + " px when the Show bar filled");
    // The section in view goes on, and one far below rests.
    await pause(200);
    const sections = [...document.querySelectorAll("main > section")];
    const inView = sections.filter((el) => { const r = el.getBoundingClientRect(); return r.bottom > 0 && r.top < innerHeight && r.height > 0; });
    const below = sections.filter((el) => el.getBoundingClientRect().top > innerHeight + 400 && el.getBoundingClientRect().height > 0);
    assert(inView.length > 0 && below.length > 0, "the fixture's page fits the screen: " + inView.length + " in view, " + below.length + " below");
    assert(inView.every((el) => !el.classList.contains("resting")), "a section in view rests");
    assert(below.every((el) => el.classList.contains("resting")), "a section below the screen doesn't rest: " + below.filter((el) => !el.classList.contains("resting")).map((el) => el.id || el.className).join(", "));
    const graphics = below.flatMap((el) => [...el.querySelectorAll("svg")]).filter((svg) => svg.querySelector("animate, animateTransform, animateMotion"));
    assert(graphics.every((svg) => svg.animationsPaused()), "an animated graphic below the screen isn't paused");
    const orbit = document.querySelector("#orbit svg");
    assert(orbit && !orbit.animationsPaused(), "the orbit in view is paused");
    // A resting section's CSS animations stop too. (A browser that draws no
    // frames reports no scrolling, so the rule is checked on a section
    // marked by hand; a real one wakes a section as it scrolls into view.)
    const hero = document.querySelector("section.hero");
    hero.classList.add("resting");
    const style = getComputedStyle(hero.querySelector(".eyebrow"));
    assert(style.animationPlayState.split(", ").every((state) => state === "paused"), "CSS animation in a resting section still runs: " + style.animationPlayState);
    hero.classList.remove("resting");
    document.body.dataset.restCheck = "passed";
  }
  addEventListener("DOMContentLoaded", () => run().catch((error) => {
    document.body.dataset.restCheck = "failed";
    const report = document.createElement("pre");
    report.id = "rest-report";
    report.textContent = error.stack;
    document.body.append(report);
  }));
})();
`
