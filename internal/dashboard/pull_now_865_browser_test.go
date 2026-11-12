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

// In a browser, a failed build's card leads with what its pull request is
// now (#865). One that can merge as it stands is offered for review first,
// and the retry that would replace it comes second. One that conflicts or
// failed says so, and its retry still leads. One that has merged or closed
// says so, and a card with nothing read says nothing of now.
func TestAFailedCardLeadsWithItsPullRequestNow865(t *testing.T) {
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
	snap.Issues = []Issue{
		card(1, "gate", rebuild, &PullNow{State: "open", Gate: "passed"}),
		card(2, "ci", watch, &PullNow{State: "open", Conflicts: true, Gate: "failed"}),
		card(3, "ci", watch, &PullNow{State: "merged"}),
		card(4, "ci", watch, nil),
		card(5, "ci", watch, &PullNow{State: "open", Draft: true, Unsettled: true, Gate: "running"}),
		card(6, "ci", watch, &PullNow{State: "closed"}),
	}
	fixture, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	script := "const fixture = " + string(fixture) + ";\n" + pullNowBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pull-now-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/pull-now-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	out, err := browserDOM(t, browser, server.URL+"/", 20000)
	if err != nil || !strings.Contains(string(out), `data-pull-now="passed"`) {
		report := string(out)
		if i := strings.Index(report, `data-pull-now="`); i >= 0 {
			report = report[i:min(len(report), i+800)]
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("the cards, in a browser: %v\n%s", err, report)
	}
}

const pullNowBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  window.fetch = (url) => {
    if (String(url).includes("/api/state.json")) return Promise.resolve(json(structuredClone(fixture)));
    if (String(url).includes("/api/live.json")) return Promise.resolve(json({steps: []}));
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
  const card = (n) => [...document.querySelectorAll("#inbox .need")].find((el) => el.dataset.issue.endsWith("#" + n));
  const now = (n) => (card(n).querySelector(".now")?.textContent || "").replace(/\s+/g, " ").trim();
  const first = (n) => card(n).querySelector(".cmds").firstElementChild;
  async function check() {
    await until(() => document.querySelectorAll("#inbox .need").length === 6, "six cards");
    // Ready, mergeable and past the gate: review it first, and the rebuild that would close it comes second.
    assert(now(1) === "Now: #1001 is ready for review, CI's gate passed on its head, and it can merge.", "card 1 says: " + now(1));
    assert(card(1).querySelector(".now").classList.contains("good"), "a pull request that can merge isn't marked good");
    assert(first(1).matches("a.review") && first(1).href.endsWith("/pull/1001") && first(1).textContent.includes("Review #1001"), "card 1 leads with " + first(1).outerHTML.slice(0, 160));
    assert(card(1).querySelectorAll(".cmd.primary").length === 1, "card 1 has " + card(1).querySelectorAll(".cmd.primary").length + " leading actions");
    assert(card(1).querySelector("[data-cmd]").textContent.includes("Build the plan again"), "card 1 lost its retry");
    // The now line comes before what the factory recorded.
    assert(card(1).querySelector(".now").compareDocumentPosition(card(1).querySelector(".ask")) & Node.DOCUMENT_POSITION_FOLLOWING, "the now line isn't first");
    // Conflicting and failed: it says so, and the retry still leads.
    assert(now(2) === "Now: #1002 is ready for review, CI's gate failed on its head, and it conflicts with its base.", "card 2 says: " + now(2));
    assert(card(2).querySelector(".now").classList.contains("bad") && !card(2).querySelector("a.review"), "card 2 offers a review of a pull request that can't merge");
    assert(first(2).matches("[data-cmd].primary"), "card 2's retry doesn't lead");
    // Merged since: it says so, and no retry leads.
    assert(now(3).startsWith("Now: #1003 has merged."), "card 3 says: " + now(3));
    assert(!card(3).querySelector(".cmd.primary"), "a merged pull request's card still leads with a retry");
    // Nothing read: nothing said, and the card is as it was.
    assert(!card(4).querySelector(".now") && first(4).matches("[data-cmd].primary"), "a card with nothing read changed");
    // A draft, just pushed.
    assert(now(5) === "Now: #1005 is a draft, and CI's gate is running on its head.", "card 5 says: " + now(5));
    assert(first(5).matches("[data-cmd].primary"), "card 5's retry doesn't lead");
    // Closed.
    assert(now(6) === "Now: #1006 is closed, and didn't merge.", "card 6 says: " + now(6));
  }
  addEventListener("load", () => check().then(
    () => { document.body.dataset.pullNow = "passed"; },
    (e) => { document.body.dataset.pullNow = "failed: " + e.message; }));
})();
`
