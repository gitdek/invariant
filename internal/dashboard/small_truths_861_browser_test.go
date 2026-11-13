package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Small things the page got wrong (#861), in a browser: the idle line names
// the issue merged last, the fleet counts merged pull requests as the
// totals do, a decision's status has one color in the bar and the graph,
// the graph is one tab stop, the hero isn't a live region, a step's runs
// are named by their try, an agent that reports no context shows none, an
// issue that needs a person is drawn where it stopped, a wait's label
// stays on its lane, one repository's view drops the repository from the
// orbit's labels, and the header's mark is still for a reader who asked
// for less motion.
func TestSmallTruthsInABrowser861(t *testing.T) {
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
	graph, err := BuildDecisionGraph("invariant", queryFiles(t, mine))
	if err != nil {
		t.Fatal(err)
	}
	log := "| ID | Date | Door | Status | Decision | Who |\n| :-- | :-- | :-- | :-- | :-- | :-- |\n" +
		"| D-0001 | 2026-11-09 | one-way | ratified | The first. | @gitdek |\n" +
		"| D-0002 | 2026-11-10 | two-way | decided | The second. | agent |\n" +
		"| D-0003 | 2026-11-11 | two-way | proposed | The third. | agent |\n"
	const primary, other = "gitdek/invariant", "gitdek/ask3d"
	s := &Server{Cache: t.TempDir(), Log: t.Logf, Repos: []*Repo{
		{Name: primary, src: sources{log: log, graph: graph}},
		{Name: other},
	}}
	now := time.Now().UTC()
	snap := s.assemble(now)
	ago := func(minutes int) time.Time { return now.Add(-time.Duration(minutes) * time.Minute) }
	closed := func(minutes int) *time.Time { at := ago(minutes); return &at }
	snap.Issues = []Issue{
		// Listed first, and merged before the next one.
		{Repo: primary, Number: 10, Title: "Merged earlier", Stage: StageMerged, PR: 1010, Closed: closed(120)},
		{Repo: primary, Number: 11, Title: "Merged last", Stage: StageMerged, PR: 1011, Closed: closed(60)},
		{Repo: primary, Number: 12, Title: "Merged by hand", Stage: StageMerged, Closed: closed(180)},
		// Two waits a minute of the factory's work apart, at the left end of
		// a lane, then the factory's hour.
		{Repo: primary, Number: 20, Title: "Failed in the factory's own run", Open: true, Stage: StageReview,
			Spans: []Span{{From: ago(230), To: ago(130), Who: WhoPeople}, {From: ago(130), To: ago(129), Who: WhoFactory},
				{From: ago(129), To: ago(100), Who: WhoPeople}, {From: ago(100), To: ago(40), Who: WhoFactory}},
			Waiting: &Waiting{Kind: "failed", Failure: "gate", PR: 1020, Since: ago(40), Takes: []string{"retry"}}},
		{Repo: primary, Number: 21, Title: "Failed CI's gate", Open: true, Stage: StageReview,
			Waiting: &Waiting{Kind: "failed", Failure: "ci", PR: 1021, Since: ago(30), Takes: []string{"retry"}}},
		{Repo: primary, Number: 22, Title: "No draft checked out", Open: true, Stage: StageReview,
			Waiting: &Waiting{Kind: "stuck", Since: ago(20), Takes: []string{"revise"}}},
		{Repo: other, Number: 30, Title: "Another repository's", Open: true, Stage: StageReview,
			Waiting: &Waiting{Kind: "failed", Failure: "ci", PR: 31, Since: ago(10), Takes: []string{"retry"}}},
	}
	idle := Now{Stage: "idle", Headline: "Idle, and watching for issues"}
	snap.Now, snap.NowBy = idle, map[string]Now{primary: idle, other: idle}
	snap.Totals.Merged, snap.Who.Merged = 2, 2
	for i := range snap.Repos {
		if snap.Repos[i].Name == primary {
			snap.Repos[i].Totals.Merged = 2
		}
	}
	fixture, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	live := fmt.Sprintf(`{"at": %q, "steps": [{"repo": %q, "issue": 20, "doing": "building", "title": "Writing the code", "run": "build-861",
		"since": %q, "limit": 9000, "turns": 3, "thinking": 40, "thinkingExact": true, "thinkingPending": false, "context": 0, "output": 12,
		"tools": {"read": 2, "test": 3}, "last": %q, "marks": [], "files": [], "runsKind": "test", "runs": [
			{"run": 1, "passed": true, "at": %q}, {"run": 1, "passed": false, "failed": ["TestOne"], "at": %q}, {"run": 2, "passed": true, "at": %q}]}]}`,
		now.Format(time.RFC3339), primary, ago(30).Format(time.RFC3339), ago(1).Format(time.RFC3339),
		ago(20).Format(time.RFC3339), ago(10).Format(time.RFC3339), ago(5).Format(time.RFC3339))
	script := "const fixture = " + string(fixture) + ";\nconst liveFixture = " + live + ";\n" + smallTruthsBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/small-truths-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/small-truths-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	for _, view := range []struct {
		name, query string
		flags       []string
	}{
		{"everything", "", nil},
		{"one repository", "?repo=ask3d", nil},
		{"less motion", "?motion=less", []string{"--force-prefers-reduced-motion"}},
	} {
		t.Run(view.name, func(t *testing.T) {
			out, err := browserDOM(t, browser, server.URL+"/"+view.query, 20000, view.flags...)
			if err != nil || !strings.Contains(string(out), `data-small-truths="passed"`) {
				report := string(out)
				if i := strings.Index(report, `data-small-truths="`); i >= 0 {
					report = report[i:min(len(report), i+1500)]
				} else if len(report) > 1500 {
					report = report[len(report)-1500:]
				}
				t.Fatalf("the page, in a browser: %v\n%s", err, report)
			}
		})
	}
}

const smallTruthsBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.slices ||= [];
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  window.fetch = (url) => {
    if (String(url).includes("/api/state.json")) return Promise.resolve(json(structuredClone(fixture)));
    if (String(url).includes("/api/live.json")) return Promise.resolve(json(structuredClone(liveFixture)));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const errors = [];
  addEventListener("error", (e) => errors.push(e.message));
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const pause = () => new Promise((resolve) => setTimeout(resolve, 10));
  async function until(test, label) {
    for (let i = 0; i < 1500; i++) { if (test()) return; await pause(); }
    throw new Error("never: " + label + (errors.length ? "; the page threw: " + errors.join("; ") : ""));
  }
  const $ = (q) => document.querySelector(q), $$ = (q) => [...document.querySelectorAll(q)];
  const text = (q) => ($(q)?.textContent || "").replace(/\s+/g, " ").trim();
  const labels = () => $$("#dots .issue-label").map((el) => el.textContent).sort().join(" ");
  async function everything() {
    // The idle line names the issue that closed last, not the first listed.
    await until(() => text("#detail").startsWith("Last merged"), "the idle line");
    assert(text("#detail").includes("#1011 for #11, Merged last"), "the idle line names " + text("#detail"));
    // The hero isn't a live region: timers in it change every second.
    assert(!$(".hero").hasAttribute("aria-live") && $("#headline").getAttribute("aria-live") === "polite", "the live region is the hero, not its headline");
    // The fleet counts merged pull requests, as the totals do: two of three merged issues.
    await until(() => $("#fleet header"), "the fleet");
    const fleet = $$("#fleet header").map((h) => h.textContent.replace(/\s+/g, " ")).find((h) => h.includes("invariant"));
    assert((fleet || "").includes("· 2 merged"), "the fleet counts " + fleet);
    // A decision's status has one color, in the bar, its key, and the graph's key.
    await until(() => $("#decisions .bar .ratified") && $(".dkey i.ratified"), "the decisions bar and the graph's key");
    const color = (q) => getComputedStyle($(q)).backgroundColor;
    for (const status of ["ratified", "proposed"]) {
      assert(color("#decisions .bar ." + status) === color(".dkey i." + status), status + " is " + color("#decisions .bar ." + status) + " in the bar and " + color(".dkey i." + status) + " in the graph");
      assert(color("#decisions .barkey i." + status) === color(".dkey i." + status), "the bar's key draws " + status + " in another color");
    }
    assert($$("#decisions .barkey i").length === 3, "the bar's key has " + $$("#decisions .barkey i").length + " swatches");
    // The graph is one tab stop, the newest decision, and the arrows walk it.
    await until(() => $$(".dmap .dn").length === 3, "the graph's three decisions");
    const stops = () => $$('.dmap .dn[tabindex="0"]');
    assert(stops().length === 1 && stops()[0] === $$(".dmap .dn")[2], "the graph has " + stops().length + " tab stops");
    stops()[0].focus();
    stops()[0].dispatchEvent(new KeyboardEvent("keydown", {key: "ArrowLeft", bubbles: true}));
    assert(stops().length === 1 && stops()[0] === $$(".dmap .dn")[1] && document.activeElement === stops()[0], "the left arrow didn't move to the decision before");
    stops()[0].dispatchEvent(new KeyboardEvent("keydown", {key: "Home", bubbles: true}));
    assert(stops()[0] === $$(".dmap .dn")[0], "Home didn't move to the first decision");
    stops()[0].dispatchEvent(new KeyboardEvent("keydown", {key: "Enter", bubbles: true}));
    assert($$(".dmap .dn")[0].classList.contains("chosen") && stops()[0] === $$(".dmap .dn")[0], "Enter didn't choose the decision in focus");
    // A step's runs are named by their try, and an agent that reports no context shows none.
    await until(() => $$(".live-runs .chip").length === 3, "the step's three runs");
    const chips = $$(".live-runs .chip").map((c) => c.textContent.trim()).join(" | ");
    assert(chips === "Try 1 · test run 1 passed | Try 2 · test run 1 failed: TestOne | Try 2 · test run 2 passed", "the runs read " + chips);
    assert(!text(".live-stats").includes("Holds") && text(".live-stats").includes("Turns"), "the step's figures read " + text(".live-stats"));
    // An issue that needs a person is drawn where it stopped.
    await until(() => $$("#dots .issue-label").length === 4, "the orbit's four open issues");
    const active = $$("#orbit .station.active").map((el) => el.dataset.key).sort().join(" ");
    assert(active === "building gate queued", "issues that need a person are drawn at " + active);
    assert(labels() === "#20 #21 #22 ask3d#30", "Everything's orbit labels " + labels());
    // A wait's label stays on its lane, however narrow.
    await until(() => $("#lanes .seg.people .wait"), "a wait on a lane");
    $("#lanes").style.width = "300px";
    dispatchEvent(new Event("resize"));
    const off = $$("#lanes .seg.people .wait").filter((label) => {
      const seg = label.parentElement, track = seg.parentElement, shift = Number(/([-\d.]+)px/.exec(label.style.transform)?.[1] || 0);
      const mid = seg.offsetLeft + seg.offsetWidth / 2 + shift, half = label.offsetWidth / 2;
      return mid - half < -1 || mid + half > track.clientWidth + 1;
    });
    assert(!off.length, off.length + " wait labels lie off their lane: " + off.map((l) => l.textContent + " " + l.style.transform).join(", "));
    assert($$("#lanes .seg.people .wait").some((l) => l.style.transform), "no label needed moving, so the check shows nothing");
    // Two waits a minute apart can't both be labeled: the longer keeps its label.
    const shown = $$("#lanes .seg.people .wait").filter((l) => l.style.visibility !== "hidden").map((l) => l.textContent.trim());
    assert(shown.join(" | ") === "1h 40m", "the lane labels its waits " + shown.join(" | "));
    // The updated label has room for a snapshot that's minutes old.
    const updated = $("#updated");
    updated.textContent = "updated 59 min ago";
    assert(updated.scrollWidth <= updated.clientWidth, "the updated label cuts off a minute's age: " + updated.scrollWidth + " wide in " + updated.clientWidth);
    // The header's mark moves for a reader who didn't ask for less.
    assert($$(".brand svg animate, .brand svg animateTransform").length > 0, "the header's mark has no motion to still");
  }
  async function oneRepository() {
    await until(() => document.querySelector('#scope a[aria-current="page"]')?.dataset.scope === "gitdek/ask3d", "ask3d's view");
    await until(() => $$("#dots .issue-label").length === 1, "ask3d's one open issue on the orbit");
    assert(labels() === "#30", "ask3d's own view labels its issue " + labels());
  }
  async function lessMotion() {
    assert(matchMedia("(prefers-reduced-motion: reduce)").matches, "the browser wasn't asked for less motion");
    await until(() => text("#detail").startsWith("Last merged"), "the first draw");
    assert($$(".brand svg animate, .brand svg animateTransform").length === 0, "the header's mark still moves");
  }
  const asked = new URLSearchParams(location.search);
  const check = asked.get("repo") ? oneRepository : asked.get("motion") ? lessMotion : everything;
  addEventListener("load", () => check().then(
    () => { document.body.dataset.smallTruths = "passed"; },
    (e) => { document.body.dataset.smallTruths = "failed: " + e.message; }));
})();
`
