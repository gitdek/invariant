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

	"github.com/gitdek/invariant/internal/plumbing"
)

// A Needs you card reads whole on a phone, and says "it can merge" only of
// a pull request that can (#889). Under 560 pixels a card is one column,
// and written 1fr that column was at least as wide as its longest word. A
// plan's summary names files, so the two plans waiting to be ratified when
// this was found each had a column wider than its card, and every line of
// them ended a few letters short at the card's edge. And a failed build's
// card ended "and it can merge" whenever GitHub found no conflict, of a
// draft and of a pull request CI's gate had failed.
//
// This holds a plan's card, with its statements open, to being no wider
// than itself and to keeping every line inside it, at a phone's width and
// a laptop's, and each card's line about its pull request to its words. A
// plan's summary, which comes as one paragraph a dozen sentences long,
// reads a sentence to a line with its words unchanged.
func TestNeedsYouCardsReadWholeAndSayWhatCanMerge889(t *testing.T) {
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
	rebuild := &RetryCopy{Label: "Build the plan again, replacing it", Confirmation: "c", Guidance: "Retrying has the factory close the draft and build the plan again.", Posted: "p"}
	watch := &RetryCopy{Label: "Merge it once CI passes", Confirmation: "c", Guidance: "Retrying has the factory watch it again.", Posted: "p"}
	card := func(n int, failure string, retry *RetryCopy, p *PullNow) Issue {
		return Issue{Repo: primary, Number: n, Title: fmt.Sprintf("Issue %d", n), Open: true, Stage: StageReview,
			Waiting: &Waiting{Kind: "failed", Failure: failure, PR: 1000 + n, Since: since, Takes: []string{"retry"}, Retry: retry, Now: p}}
	}
	// A plan as the factory writes one: its summary and its tests name
	// files and functions, each one long word.
	const long = "internal/verify/python_application_driver_receipts_884_accept_test.go"
	const test = "TestPythonApplicationPerCheckLargerEligibilityAcrossIndependentDrivers884"
	plan := Issue{Repo: primary, Number: 5, Title: "Run each Python check's own driver at larger bounds", Open: true, Stage: StageRatifying,
		Waiting: &Waiting{Kind: "proposal", Name: "Run each Python check with its own driver", Hash: "b8e4bb76bb15", Together: true, Since: since, Takes: []string{"ratify", "revise"},
			Statements: []Said{{Name: test, Kind: "test", Says: "Missing bounds suppress only the affected invocation, as " + long + " shows."}},
			Plan: &PlanView{
				Summary: "This intentionally replaces the different-manifest-driver refusal in python_application_gate_accept_test.go and the corresponding contract comment in python_application_execution_accept_test.go. It adds " + long + ", as SPEC.md will say. Update docs/python.md too.",
				Files:   []string{"internal/verify/python_application_execution.go", long},
				Tests:   []plumbing.Test{{Name: test, File: long, Says: "Missing bounds suppress only the affected invocation."}},
			}}}
	snap.Issues = []Issue{
		card(1, "gate", rebuild, &PullNow{State: "open", Draft: true, Gate: "passed"}),
		card(2, "ci", watch, &PullNow{State: "open", Draft: true, Gate: "failed"}),
		card(3, "ci", watch, &PullNow{State: "open", Gate: "failed"}),
		card(4, "gate", rebuild, &PullNow{State: "open", Gate: "passed"}),
		plan,
		card(6, "ci", watch, &PullNow{State: "open", Gate: "running"}),
	}
	fixture, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	script := "const fixture = " + string(fixture) + ";\n" + needsYouCardsBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/needs-you-cards-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/needs-you-cards-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	// A browser run's window is 500 pixels wide at its narrowest, which is
	// under the 560 where a card becomes one column.
	for _, width := range []int{500, 1440} {
		t.Run(fmt.Sprintf("%d wide", width), func(t *testing.T) {
			out, err := browserDOM(t, browser, server.URL+"/", 20000, fmt.Sprintf("--window-size=%d,900", width))
			if err != nil || !strings.Contains(string(out), `data-needs-you-cards="passed"`) {
				report := string(out)
				if i := strings.Index(report, `data-needs-you-cards="`); i >= 0 {
					report = report[i:]
					if j := strings.Index(report[len(`data-needs-you-cards="`):], `"`); j >= 0 {
						report = report[:len(`data-needs-you-cards="`)+j+1]
					}
				} else if len(report) > 1500 {
					report = report[len(report)-1500:]
				}
				t.Fatalf("Needs you's cards, in a browser: %v\n%s", err, report)
			}
		})
	}
}

const needsYouCardsBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const errors = [];
  addEventListener("error", (e) => errors.push(e.message));
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  window.fetch = (url) => {
    if (String(url).includes("/api/state.json")) return Promise.resolve(json(structuredClone(fixture)));
    if (String(url).includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok, label) => { if (!ok) throw new Error(label + (errors.length ? "; the page threw: " + errors.join("; ") : "")); };
  const pause = () => new Promise((resolve) => setTimeout(resolve, 10));
  async function until(test, label) {
    for (let i = 0; i < 1500; i++) { if (test()) return; await pause(); }
    assert(false, "never: " + label);
  }
  const card = (n) => [...document.querySelectorAll("#inbox .need")].find((el) => el.dataset.issue.endsWith("#" + n));
  const now = (n) => (card(n).querySelector(".now")?.textContent || "").replace(/\s+/g, " ").trim();
  async function check() {
    await until(() => document.querySelectorAll("#inbox .need").length === 6, "six cards");
    // What a pull request is now, in words that are so.
    assert(now(1) === "Now: #1001 is a draft, CI's gate passed on its head, and it doesn't conflict with its base.", "a draft past the gate says: " + now(1));
    assert(now(2) === "Now: #1002 is a draft, CI's gate failed on its head, and it doesn't conflict with its base.", "a draft that failed says: " + now(2));
    assert(now(3) === "Now: #1003 is ready for review, CI's gate failed on its head, and it doesn't conflict with its base.", "a ready pull request that failed says: " + now(3));
    assert(now(4) === "Now: #1004 is ready for review, CI's gate passed on its head, and it can merge.", "one a person can merge says: " + now(4));
    assert(now(6) === "Now: #1006 is ready for review, CI's gate is running on its head, and it doesn't conflict with its base.", "one whose gate is running says: " + now(6));
    for (const n of [1, 2, 3, 6]) assert(!/can merge/.test(now(n)) && !card(n).querySelector(".now").classList.contains("good"), "card " + n + " is said, or marked, to be able to merge");
    assert(card(4).querySelector(".now").classList.contains("good"), "the one that can merge isn't marked good");

    // The plan's card, with everything in it open, is no wider than itself
    // and keeps every line inside its edge.
    const plan = card(5);
    plan.querySelectorAll("details").forEach((d) => { d.open = true; });
    await pause();
    const words = plan.textContent;
    assert(words.includes("python_application_driver_receipts_884_accept_test.go") && words.includes("TestPythonApplicationPerCheckLargerEligibilityAcrossIndependentDrivers884"), "the plan's card doesn't hold its long words, so the check proves nothing");
    const box = plan.getBoundingClientRect(), edge = box.right - parseFloat(getComputedStyle(plan).borderRightWidth);
    assert(plan.scrollWidth <= plan.clientWidth + 1, "at " + innerWidth + " wide, the plan's card is " + (plan.scrollWidth - plan.clientWidth) + " pixels wider than itself");
    const past = [...plan.querySelectorAll("p, li, summary, span, strong, code, b, a, button, .title, .since")].filter((el) => el.getClientRects().length && [...el.getClientRects()].some((r) => r.right > edge + 1));
    assert(!past.length, "at " + innerWidth + " wide, " + past.length + " of the plan's lines reach past the card, the first by " + Math.round(Math.max(...[...past[0]?.getClientRects() || []].map((r) => r.right)) - edge) + " pixels: " + (past[0]?.textContent || "").slice(0, 60));
    // Its summary reads a sentence to a line, with every word it came with.
    const says = plan.querySelector(".ask.says"), lines = [...says.querySelectorAll("span")];
    const summary = fixture.issues.find((i) => i.number === 5).waiting.plan.summary;
    assert(lines.length === 3 && lines.every((line) => getComputedStyle(line).display === "block"), "the plan's summary is " + lines.length + " lines, not its three sentences each on its own");
    assert(lines[1].textContent.startsWith("It adds ") && lines[2].textContent.trim() === "Update docs/python.md too.", "the summary's sentences are split in the wrong places: " + lines.map((line) => line.textContent.slice(0, 24)).join(" | "));
    assert(says.textContent.trim() === summary, "the summary's words changed");
    // Its words are all there to read: nothing is clipped away either.
    assert(plan.querySelector(".ask").scrollWidth <= plan.querySelector(".ask").clientWidth + 1, "the plan's summary is wider than its paragraph");
    const page = document.documentElement;
    assert(page.scrollWidth <= page.clientWidth, "the page is " + (page.scrollWidth - page.clientWidth) + " pixels wider than its window");
    assert(!errors.length, "the page threw");
  }
  addEventListener("load", () => check().then(
    () => { document.body.dataset.needsYouCards = "passed"; },
    (e) => { document.body.dataset.needsYouCards = "failed: " + e.message; }));
})();
`
