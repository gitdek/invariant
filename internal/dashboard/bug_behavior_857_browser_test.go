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

// In a browser, the card a person ratifies from says what each bug's
// statement says of the code and what an amendment changes that from, and
// says before its statements are opened which bugs the amendment stops
// calling current (#857). A proposal that amends nothing, and an amendment
// that lowers no bug, say nothing of the kind. A proposal the gate can't
// yet replay says it can't be built, by the bugs' names.
func TestTheRatifyCardShowsEachBugsClassification857(t *testing.T) {
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
	rules := []Said{{Name: "Spec", Kind: "spec", Says: "It follows the model."}, {Name: "Safe", Kind: "invariant", Says: "It's safe."}}
	card := func(n int, amends string, relaxed []string, bugs ...Said) Issue {
		return Issue{Repo: primary, Number: n, Title: fmt.Sprintf("Issue %d", n), Open: true, Stage: StageRatifying,
			Waiting: &Waiting{Kind: "proposal", Name: "queue", Hash: "0123456789ab", Since: now.Add(-time.Hour), Takes: []string{"ratify", "revise"},
				Amends: amends, Relaxed: relaxed, Statements: append(append([]Said(nil), rules...), bugs...)}}
	}
	bug := func(name, behavior, was string) Said {
		return Said{Name: name, Kind: "bug", Says: name + " happens.", Behavior: behavior, Was: was}
	}
	snap.Issues = []Issue{
		card(1, "#12", []string{"Lowered", "<Gone>"}, bug("Kept", "current", ""), bug("Lowered", "hypothetical", "current"), bug("Classified", "current", "not classified"), bug("Legacy", "not classified", "")),
		card(2, "", nil, bug("New", "hypothetical", "")),
		card(3, "#12", []string{"Only"}, bug("Only", "hypothetical", "current")),
		card(4, "#12", nil, bug("Raised", "current", "hypothetical")),
	}
	snap.Issues[0].Waiting.Unreplayed = []string{"Kept", "<Classified>"}
	snap.Issues[3].Waiting.Unreplayed = []string{"Raised"}
	fixture, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	script := "const fixture = " + string(fixture) + ";\n" + bugBehaviorBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bug-behavior-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/bug-behavior-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	out, err := browserDOM(t, browser, server.URL+"/", 20000)
	if err != nil || !strings.Contains(string(out), `data-bug-behavior="passed"`) {
		report := string(out)
		if i := strings.Index(report, `data-bug-behavior="`); i >= 0 {
			report = report[i:min(len(report), i+800)]
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("the ratify cards, in a browser: %v\n%s", err, report)
	}
}

const bugBehaviorBrowserCheck = `
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
  const shown = (el) => !!el && el.getClientRects().length > 0;
  const row = (n, name) => [...card(n).querySelectorAll(".stmts li")].find((li) => text(li.querySelector("b")) === name);
  const says = (n, name) => text(row(n, name).querySelector(".cb"));
  async function check() {
    await until(() => document.querySelectorAll("#inbox .need").length === 4, "four cards");
    // Before the statements are opened, the card names the bugs the
    // amendment stops calling current, with the count in its own words.
    const relaxed = card(1).querySelector(".ask.relaxed");
    assert(shown(relaxed) && !card(1).querySelector(".stmts").open, "card 1 doesn't say which bugs it lowers before its statements are opened");
    assert(text(relaxed) === "2 bugs are no longer called current. What #12 ratified says the code has Lowered, <Gone> today, and the gate has to show that by replaying each against the code. This amendment doesn't say so.", "card 1 says: " + text(relaxed));
    assert(!relaxed.querySelector("gone") && relaxed.querySelectorAll("strong").length === 2, "a bug's name reached the page as markup");
    assert(text(card(3).querySelector(".ask.relaxed")) === "One bug is no longer called current. What #12 ratified says the code has Only today, and the gate has to show that by replaying it against the code. This amendment doesn't say so.", "card 3 says: " + text(card(3).querySelector(".ask.relaxed")));
    // A new proposal, and an amendment that only raises a bug, say none of it.
    assert(!card(2).querySelector(".ask.relaxed") && !card(4).querySelector(".ask.relaxed"), "a card that lowers no bug says it does");
    // A proposal the gate can't yet replay says so, above what it lowers.
    const unbuilt = (n) => text(card(n).querySelector(".ask.unreplayed"));
    assert(unbuilt(1) === "This can't be built yet. It calls Kept, <Classified> current, so the gate has to replay each against the code, and the gate can't run a replay yet. A build would stop there.", "card 1 says: " + unbuilt(1));
    assert(unbuilt(4) === "This can't be built yet. It calls Raised current, so the gate has to replay it against the code, and the gate can't run a replay yet. A build would stop there.", "card 4 says: " + unbuilt(4));
    assert(!card(1).querySelector(".ask.unreplayed classified") && shown(card(1).querySelector(".ask.unreplayed")), "a bug's name reached the page as markup, or the note is hidden");
    assert(card(1).querySelector(".ask.unreplayed").compareDocumentPosition(relaxed) & Node.DOCUMENT_POSITION_FOLLOWING, "what can't be built isn't said first");
    assert(!card(2).querySelector(".ask.unreplayed") && !card(3).querySelector(".ask.unreplayed"), "a card the gate can replay says it can't be built");
    assert(getComputedStyle(card(1).querySelector(".ask.unreplayed b")).color === getComputedStyle(relaxed.querySelector("b")).color && getComputedStyle(relaxed.querySelector("b")).color !== getComputedStyle(card(2).querySelector(".ask strong")).color, "the note's lead isn't in the color of what waits on a person");
    // Opened, each bug says what it says of the code, and what it was.
    card(1).querySelector(".stmts summary").click();
    await until(() => card(1).querySelector(".stmts").open, "card 1's statements open");
    assert(says(1, "Kept") === "Code behavior: current", "Kept says: " + says(1, "Kept"));
    assert(says(1, "Lowered") === "Code behavior: hypothetical (was current)", "Lowered says: " + says(1, "Lowered"));
    assert(says(1, "Classified") === "Code behavior: current (was not classified)", "Classified says: " + says(1, "Classified"));
    assert(says(1, "Legacy") === "Code behavior: not classified", "Legacy says: " + says(1, "Legacy"));
    assert(says(2, "New") === "Code behavior: hypothetical" && says(4, "Raised") === "Code behavior: current (was hypothetical)", "cards 2 and 4 say: " + says(2, "New") + "; " + says(4, "Raised"));
    // It's on a line of its own under the statement's words, and one an
    // amendment changes is told apart from one it keeps.
    const kept = row(1, "Kept").querySelector(".cb"), lowered = row(1, "Lowered").querySelector(".cb");
    assert(shown(kept) && kept.getBoundingClientRect().top >= row(1, "Kept").querySelector("b").getBoundingClientRect().bottom - 1, "the classification isn't under the statement's words");
    assert(lowered.classList.contains("changed") && !kept.classList.contains("changed") && getComputedStyle(lowered).color !== getComputedStyle(kept).color, "a changed classification looks like a kept one");
    assert(kept.title.includes("replay") && lowered.title.includes("Nothing says the code has it"), "a classification doesn't explain itself: " + kept.title + " / " + lowered.title);
    // No other kind of statement says anything of the code.
    assert(!row(1, "Spec").querySelector(".cb") && !row(1, "Safe").querySelector(".cb"), "a rule that isn't a bug has a classification");
  }
  addEventListener("load", () => check().then(
    () => { document.body.dataset.bugBehavior = "passed"; },
    (e) => { document.body.dataset.bugBehavior = "failed: " + e.message; }));
})();
`
