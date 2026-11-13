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

// In a browser, /act offers each issue the factory hasn't taken the command
// that fits it (#900): Hand to the factory, which posts /invariant solve,
// for one change, and Plan it for a PRD. An issue that says it waits shows
// why and offers nothing, folded under one line below those that are ready.
func TestActOffersTheCommandThatFitsAnIssue900(t *testing.T) {
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
	team := newAccessTeam(t)
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	s, _, _ := receipt723Load(t, receipt723Fixture(t), nil)
	s.Access = team.access()
	now := time.Now()
	snap := s.assemble(now)
	snap.Issues = []Issue{}
	opened := now.Add(-3 * time.Hour)
	one := func(n int, title, hand, waits string) Untaken {
		return Untaken{Repo: snap.Repo, Number: n, Title: title, Opened: opened, URL: fmt.Sprintf("https://github.com/%s/issues/%d", snap.Repo, n), Hand: hand, Waits: waits}
	}
	untaken := []Untaken{
		one(30, "Check every mesh a person uploads", "plan", ""),
		one(31, "Let a path hold brackets", "solve", ""),
		one(34, "Compare states one size larger", "solve", "for #884, which changes the same check."),
		one(35, "Pin a statement's layout", "plan", "a decision, since it changes what a lock records"),
	}
	fixture, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	list, err := json.Marshal(untaken)
	if err != nil {
		t.Fatal(err)
	}
	script := "const fixture = " + string(fixture) + "; const untakenList = " + string(list) + ";\n" + handOverBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hand-over-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/act":
			r.Header.Set("Cf-Access-Jwt-Assertion", owner)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/hand-over-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	out, err := browserDOM(t, browser, server.URL+"/act", 20000)
	if err != nil || !strings.Contains(string(out), `data-hand-over="passed"`) {
		report := string(out)
		if i := strings.Index(report, `data-hand-over="`); i >= 0 {
			report = report[i:min(len(report), i+800)]
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("the list, in a browser: %v\n%s", err, report)
	}
}

const handOverBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  const posts = [];
  window.fetch = (url, options) => {
    url = String(url);
    if (url.includes("/api/state.json")) return Promise.resolve(json(structuredClone(fixture)));
    if (url.includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    if (url.includes("/act/api/untaken.json")) return Promise.resolve(json(structuredClone(untakenList)));
    if (url.includes("/act/api/comment")) {
      posts.push(JSON.parse(options.body));
      return Promise.resolve(json({url: "https://github.com/o/r/issues/1#issuecomment-" + posts.length}));
    }
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
  const card = (n) => [...document.querySelectorAll("#untaken .need")].find((el) => el.dataset.issue.endsWith("#" + n));
  const offer = (n) => card(n).querySelector("[data-untaken-ask]");
  async function hand(n, label, asks, verb, done) {
    assert(text(offer(n)) === label, "#" + n + " offers: " + text(offer(n)));
    offer(n).click();
    await until(() => card(n).querySelector(".confirm"), "#" + n + "'s question");
    assert(text(card(n).querySelector(".confirm span")) === asks, "#" + n + " asks: " + text(card(n).querySelector(".confirm span")));
    const before = posts.length;
    card(n).querySelector("[data-untaken-post]").click();
    await until(() => card(n).querySelector(".posted"), "#" + n + " posted");
    assert(posts.length === before + 1 && posts.at(-1).issue === n && posts.at(-1).body === "/invariant " + verb, "#" + n + " posted " + JSON.stringify(posts.slice(before)));
    const said = text(card(n).querySelector(".posted"));
    assert(said.includes("/invariant " + verb + " on GitHub") && said.endsWith(done), "#" + n + " says: " + said);
  }
  async function check() {
    await until(() => document.querySelectorAll("#untaken .need").length === 4, "four issues");
    assert(text(document.querySelector("#untaken .lede")).startsWith("Open issues a writer opened that the factory hasn't taken. Hand one over,"), "the list says: " + text(document.querySelector("#untaken .lede")));
    // What's ready is first, and what waits is folded under one line.
    const fold = document.querySelector("#untaken details.waits");
    assert(fold && !fold.open && text(fold.querySelector("summary")) === "2 wait, and say why", "the fold says: " + text(fold?.querySelector("summary")));
    assert(!card(30).closest("details") && !card(31).closest("details"), "an issue that's ready is folded");
    assert(card(34).closest("details.waits") && card(35).closest("details.waits"), "an issue that waits isn't folded");
    assert(fold.compareDocumentPosition(card(31)) & Node.DOCUMENT_POSITION_PRECEDING, "what waits comes before what's ready");
    // One that waits says why, and offers no command.
    assert(text(card(34).querySelector(".ask")) === "Waitsfor #884, which changes the same check.", "#34 says: " + text(card(34).querySelector(".ask")));
    assert(text(card(35).querySelector(".ask")) === "Waitsa decision, since it changes what a lock records", "#35 says: " + text(card(35).querySelector(".ask")));
    assert(!offer(34) && !offer(35), "an issue that waits is offered a command");
    assert(card(34).querySelector("a.gh")?.href.endsWith("/issues/34"), "#34 doesn't link to its issue");
    // The fold stays open once opened, when the list is drawn again.
    fold.open = true;
    fold.dispatchEvent(new Event("toggle"));
    // One change is handed over with solve, and a PRD is planned.
    await hand(31, "Hand to the factory", "Have the factory draft this change for you to ratify, as you?", "solve", "The factory takes it on its next poll, and drafts it when an agent is free.");
    assert(document.querySelector("#untaken details.waits").open, "the fold closed when the list was drawn again");
    await hand(30, "Plan it", "Have the factory read this as a PRD and draft a plan of issues for it, as you?", "plan", "The factory drafts a plan of issues within 30 seconds.");
    assert(posts.length === 2, "posted " + JSON.stringify(posts));
  }
  addEventListener("load", () => check().then(
    () => { document.body.dataset.handOver = "passed"; },
    (e) => { document.body.dataset.handOver = "failed: " + e.message; }));
})();
`
