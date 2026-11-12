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

// Needs you is the list a person acts from, so it has to be one they can
// trust at a glance. With everything shown, each repository's cards sit
// together under its name and count, and none is dimmed. What has waited
// more than two days folds under a line of its own, and the headline and
// the Show choices count what's new.
func TestNeedsYouGroupsAndFoldsWhatStalled(t *testing.T) {
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
	other := snap.Repos[0]
	other.Name, other.Short, other.Primary = "gitdek/other", "other", false
	snap.Repos = append(snap.Repos, other)
	waits := func(repo string, number int, hours float64) Issue {
		since := now.Add(-time.Duration(hours * float64(time.Hour)))
		return Issue{Repo: repo, Number: number, Title: fmt.Sprintf("Issue %d", number), Open: true, Stage: StageAsking,
			Spans:   []Span{{From: since, To: now, Who: WhoPeople, Open: true}},
			Waiting: &Waiting{Kind: "closed", Since: since, PR: number + 1000, Takes: []string{"revise"}}}
	}
	// The page lists the primary repository's first, and the longest wait
	// first within each. Issues 4 and 5 have waited more than two days.
	snap.Issues = []Issue{waits(primary, 1, 1), waits("gitdek/other", 2, 3), waits(primary, 3, 30), waits(primary, 4, 100), waits("gitdek/other", 5, 200)}
	script := fmt.Sprintf("const fixture = %s;\n", reader631JSON(t, snap)) + needsYouBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/needs-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/needs-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	// The second read of the state comes 15 seconds after the first, in the
	// page's time.
	out, err := browserDOM(t, browser, server.URL+"/", 40000)
	if err != nil || !strings.Contains(string(out), `data-needs-check="passed"`) {
		report := string(out)
		if i := strings.Index(report, `id="needs-report"`); i >= 0 {
			report = report[i:min(len(report), i+1500)]
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("Needs you, in a browser: %v\n%s", err, report)
	}
}

const needsYouBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  let reads = 0;
  window.fetch = (url) => {
    if (url === "/api/state.json") {
      reads++;
      // The second read has one more issue waiting, so the list is drawn again.
      const state = structuredClone(fixture);
      if (reads > 1) state.issues.push({...structuredClone(fixture.issues[0]), number: 6, title: "Issue 6"});
      return Promise.resolve(json(state));
    }
    if (url.includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const pause = () => new Promise((resolve) => setTimeout(resolve, 10));
  async function until(test, label) {
    for (let i = 0; i < 3000; i++) { if (test()) return; await pause(); }
    throw new Error("never: " + label);
  }
  const text = (el) => el.textContent.replace(/\\s+/g, " ").trim();
  async function run() {
    const box = document.querySelector("#inbox");
    await until(() => box.querySelectorAll(".need").length === 5, "five cards");
    const outline = () => [...box.children].map((el) => el.matches("h3.need-repo") ? "repo " + text(el)
      : el.matches("details.stalled") ? "stalled[" + [...el.children].slice(1).map((c) => c.matches("h3.need-repo") ? "repo " + text(c) : c.dataset.issue.split("#")[1]).join(",") + "]"
      : el.dataset.issue.split("#")[1]).join(" | ");
    const short = fixture.repos[0].short;
    assert(outline() === "repo " + short + "2 | 3 | 1 | repo other1 | 2 | stalled[repo " + short + "1,4,repo other1,5]", "the list reads: " + outline());
    const fold = box.querySelector("details.stalled");
    assert(!fold.open && text(fold.querySelector("summary")) === "2 issues have waited more than two days", "the fold reads: " + text(fold.querySelector("summary")));
    // Each card fades in when it's drawn, which a browser that draws no
    // frames never finishes. With that animation set aside, none is dimmed,
    // whichever repository it's from.
    for (const card of box.querySelectorAll(".need")) {
      card.style.animation = "none";
      assert(getComputedStyle(card).opacity === "1", "a card that needs a person is dimmed: " + card.dataset.issue + " at " + getComputedStyle(card).opacity);
    }
    const elsewhere = document.createElement("div");
    elsewhere.className = "aside";
    document.body.append(elsewhere);
    assert(getComputedStyle(elsewhere).opacity === "0.5", "another repository's items elsewhere are no longer quieter");
    elsewhere.remove();
    assert(box.querySelector('.need.aside[data-repo="gitdek/other"]'), "the other repository's cards aren't marked as another repository's");
    const chip = [...document.querySelectorAll("#nowmeta .chip")].map(text).find((c) => c.includes("need"));
    assert(chip === "3 issues need you · 2 stalled", "the headline's count reads: " + chip);
    const choices = [...document.querySelectorAll("#scope a")].map(text);
    assert(choices[0].endsWith("3 need you") && choices[1].endsWith("2 need you") && choices[2].endsWith("1 needs you"), "the Show choices read: " + choices.join(" / "));
    // Opened, the fold stays open when the list is drawn again.
    fold.querySelector("summary").click();
    await until(() => box.querySelector("details.stalled").open, "the fold opening");
    await until(() => reads >= 2 && box.querySelectorAll(".need").length === 6, "the second read's card");
    const again = box.querySelector("details.stalled");
    assert(again !== fold && again.open, "the fold closed when the list was drawn again");
    assert(outline().startsWith("repo " + short + "3 | 3 | 1 | 6 | repo other1 | 2 | stalled["), "the list then reads: " + outline());
    // One repository's view names no repository, and counts its own.
    document.querySelector('#scope a[data-scope="gitdek/other"]').click();
    await until(() => box.querySelectorAll(".need").length === 2, "the other repository's cards");
    assert(!box.querySelector("h3.need-repo") && outline() === "2 | stalled[5]", "one repository's list reads: " + outline());
    const own = [...document.querySelectorAll("#nowmeta .chip")].map(text).find((c) => c.includes("need"));
    assert(own === "1 issue needs you · 1 stalled", "one repository's count reads: " + own);
    document.body.dataset.needsCheck = "passed";
  }
  addEventListener("DOMContentLoaded", () => run().catch((error) => {
    document.body.dataset.needsCheck = "failed";
    const report = document.createElement("pre");
    report.id = "needs-report";
    report.textContent = error.stack;
    document.body.append(report);
  }));
})();
`
