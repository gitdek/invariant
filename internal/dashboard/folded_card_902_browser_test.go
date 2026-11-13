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

// In a browser, a card whose pull request changed after the factory's post
// has waited only since then (#902). One whose gate passed an hour ago,
// under a post three days old, sits with what's new and is counted in what
// needs a person, its retry no longer speaks of a gate still to pass, and
// what the factory said when it stopped is dated. One whose pull request
// hasn't changed in three days, or couldn't be read, stays folded as it was.
func TestACardLeavesTheFoldWhenItsPullRequestChanges902(t *testing.T) {
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
	posted := now.Add(-3 * 24 * time.Hour)
	hourAgo, twoHoursAgo, then := now.Add(-time.Hour), now.Add(-2*time.Hour), posted.Add(10*time.Minute)
	card := func(n int, failure, person string, p *PullNow) Issue {
		retry := &RetryCopy{Label: fmt.Sprintf("%s #%d once CI passes", person, 1000+n), LabelPassed: fmt.Sprintf("%s #%d", person, 1000+n),
			Confirmation: "c", Guidance: "Retrying has the factory watch it again.", Posted: "p"}
		return Issue{Repo: primary, Number: n, Title: fmt.Sprintf("Issue %d", n), Open: true, Stage: StageReview,
			Waiting: &Waiting{Kind: "failed", Failure: failure, PR: 1000 + n, Since: posted, Takes: []string{"retry"}, Retry: retry, Now: p}}
	}
	snap.Issues = []Issue{
		card(1, "ci", "Merge", &PullNow{State: "open", Draft: true, Gate: "passed", Changed: &hourAgo}),
		card(2, "ci", "Merge", &PullNow{State: "open", Gate: "failed", Changed: &then}),
		card(3, "ci", "Merge", nil),
		card(4, "trusted", "Have a person merge", &PullNow{State: "open", Gate: "passed", Changed: &twoHoursAgo}),
	}
	fixture, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	script := "const fixture = " + string(fixture) + ";\n" + foldedCardBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/folded-card-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/folded-card-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	out, err := browserDOM(t, browser, server.URL+"/", 20000)
	if err != nil || !strings.Contains(string(out), `data-folded-card="passed"`) {
		report := string(out)
		if i := strings.Index(report, `data-folded-card="`); i >= 0 {
			report = report[i:min(len(report), i+800)]
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("the cards, in a browser: %v\n%s", err, report)
	}
}

const foldedCardBrowserCheck = `
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
  const text = (el) => (el?.textContent || "").replace(/\s+/g, " ").trim();
  const card = (n) => [...document.querySelectorAll("#inbox .need")].find((el) => el.dataset.issue.endsWith("#" + n));
  const folded = (n) => !!card(n).closest("details.stalled");
  const retry = (n) => text(card(n).querySelector('[data-cmd="/invariant retry"]'));
  const said = (n) => text(card(n).querySelector(".ask"));
  async function check() {
    await until(() => document.querySelectorAll("#inbox .need").length === 4, "four cards");
    await until(() => text(card(1).querySelector(".ask [data-since]")) !== "", "the time the factory stopped");
    // What changed since the post is with what's new. What didn't is folded.
    assert(!folded(1) && !folded(4), "a card whose pull request changed an hour ago is folded: " + folded(1) + ", " + folded(4));
    assert(folded(2) && folded(3), "a card whose pull request hasn't changed in three days isn't folded: " + folded(2) + ", " + folded(3));
    assert(text(document.querySelector("details.stalled summary")) === "2 issues have waited more than two days", "the fold says: " + text(document.querySelector("details.stalled summary")));
    assert(!text(document.querySelector("#inbox")).includes("Nothing new needs you"), "the page says nothing new needs a person");
    // The headline counts them the same way.
    const chip = text(document.querySelector(".chip.people"));
    assert(chip === "2 issues need you · 2 stalled", "the headline counts: " + chip);
    // Past the gate, the retry doesn't speak of a gate still to pass.
    assert(retry(1).startsWith("Merge #1001 · /invariant retry"), "card 1's retry says: " + retry(1));
    assert(retry(4).startsWith("Have a person merge #1004 · /invariant retry"), "card 4's retry says: " + retry(4));
    assert(retry(2).startsWith("Merge #1002 once CI passes · /invariant retry"), "card 2's retry says: " + retry(2));
    assert(retry(3).startsWith("Merge #1003 once CI passes · /invariant retry"), "card 3's retry says: " + retry(3));
    // What the factory said when it stopped is dated, under what is so now.
    assert(/^When the factory stopped, \S.* ago: CI's gate didn't pass on #1001\.$/.test(said(1)), "card 1 says: " + said(1));
    assert(/^When the factory stopped, \S.* ago: CI's gate didn't pass on #1002\.$/.test(said(2)), "card 2 says: " + said(2));
    assert(card(1).querySelector(".now").compareDocumentPosition(card(1).querySelector(".ask")) & Node.DOCUMENT_POSITION_FOLLOWING, "the now line isn't first");
    assert(text(card(1).querySelector(".now")) === "Now: #1001 is a draft, CI's gate passed on its head, and it doesn't conflict with its base.", "card 1's now line says: " + text(card(1).querySelector(".now")));
    // A card that says nothing of now dates nothing.
    assert(said(3) === "CI's gate didn't pass on #1003.", "card 3 says: " + said(3));
  }
  addEventListener("load", () => check().then(
    () => { document.body.dataset.foldedCard = "passed"; },
    (e) => { document.body.dataset.foldedCard = "failed: " + e.message; }));
})();
`
