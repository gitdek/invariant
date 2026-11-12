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

// With no step running, the page used to show each step that had ended on a
// full card above Needs you, which put what a person must do two to three
// screens down. Now, once every step shown has ended, Needs you comes first
// and each of those steps folds to its heading, with a button that shows its
// run. While a step runs, the live cards lead as before (#822).
func TestFinishedStepsFoldBelowNeedsYou822(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`class="live-word"`, `class="btn fold" hidden aria-expanded="false"`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("a step's card has no %s", want)
		}
	}
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
	script := fmt.Sprintf("const fixture = %s;\n", reader631JSON(t, snap)) + finishedFoldBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fold-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/fold-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	// The second poll comes 12 seconds after the first, in the page's time.
	out, err := browserDOM(t, browser, server.URL+"/", 30000)
	if err != nil || !strings.Contains(string(out), `data-fold-check="passed"`) {
		report := string(out)
		if i := strings.Index(report, `id="fold-report"`); i >= 0 {
			report = report[i:min(len(report), i+1500)]
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("the finished steps' fold, in a browser: %v\n%s", err, report)
	}
}

const finishedFoldBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  const ago = (minutes) => new Date(Date.now() - minutes * 60000).toISOString();
  const step = (issue, ended) => ({repo: fixture.repo, issue, title: ended ? "Build step ended" : "Building", doing: "building", run: "run" + issue,
    since: ago(90), ended: ended ? ago(30) : undefined, agent: ended ? "finished" : "", limit: 9000, turns: 3, marks: [], tools: {}, files: [], runs: []});
  let polls = 0;
  window.fetch = (url) => {
    if (url === "/api/state.json") return Promise.resolve(json(fixture));
    if (url.includes("/api/live.json")) {
      polls++;
      return Promise.resolve(json({steps: polls === 1 ? [step(7, true), step(8, true)] : [step(7, true), step(9, false)]}));
    }
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const pause = () => new Promise((resolve) => setTimeout(resolve, 10));
  async function until(test, label) {
    for (let i = 0; i < 2500; i++) { if (test()) return; await pause(); }
    throw new Error("never: " + label);
  }
  const shown = (el) => getComputedStyle(el).display !== "none";
  async function run() {
    const live = document.querySelector("#live"), inbox = document.querySelector("#inbox-block");
    const cards = () => [...document.querySelectorAll("#live-cards .livestep")];
    await until(() => cards().length === 2, "two finished steps' cards");
    assert(!live.hidden && live.classList.contains("finished") && inbox.nextElementSibling === live, "finished steps come before Needs you");
    for (const card of cards()) {
      const fold = card.querySelector(".fold");
      assert(card.classList.contains("ended") && !card.classList.contains("open"), "a finished step's card isn't folded");
      assert(card.querySelector(".live-word").textContent === "Finished", "a finished step's card says Live");
      assert(!fold.hidden && fold.textContent === "Show the run" && fold.getAttribute("aria-expanded") === "false", "no button to show the run");
      assert(shown(card.querySelector("header")) && card.querySelector(".live-title").textContent.includes("Build step ended"), "a folded card lost its heading");
      for (const part of [".live-top", ".strip", ".legend", ".live-runs", ".live-files", ".fineprint"]) {
        assert(!shown(card.querySelector(part)), "a folded card still shows its " + part);
      }
    }
    const first = cards()[0], fold = first.querySelector(".fold");
    fold.click();
    assert(first.classList.contains("open") && fold.textContent === "Hide the run" && fold.getAttribute("aria-expanded") === "true", "the button didn't open the card");
    assert(shown(first.querySelector(".strip")) && shown(first.querySelector(".live-files")), "an opened card doesn't show its run");
    assert(!cards()[1].classList.contains("open"), "opening one card opened another");
    fold.click();
    assert(!first.classList.contains("open") && fold.textContent === "Show the run", "the button didn't fold the card again");
    // The next poll has a step running: the live cards lead again, and
    // show the running step alone, in full.
    await until(() => polls >= 2 && cards().length === 1 && !cards()[0].classList.contains("ended"), "the running step's card");
    const running = cards()[0];
    assert(!live.classList.contains("finished") && live.nextElementSibling === inbox, "a running step's card comes after Needs you");
    assert(running.querySelector(".fold").hidden && running.querySelector(".live-word").textContent === "Live" && shown(running.querySelector(".strip")), "a running step's card is folded");
    document.body.dataset.foldCheck = "passed";
  }
  addEventListener("DOMContentLoaded", () => run().catch((error) => {
    document.body.dataset.foldCheck = "failed";
    const report = document.createElement("pre");
    report.id = "fold-report";
    report.textContent = error.stack;
    document.body.append(report);
  }));
})();
`
