package dashboard

import (
	"context"
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

// The page opens on the repository its address names, by short name or
// owner/name, and keeps the address on the view it shows. Another
// repository's view shows that repository's decisions, or says it has none,
// leaves out Invariant's road and who builds what, and names the repository
// in the title, and back and forward move between views (#773). Like the
// other browser checks, it uses an installed browser.
func TestRepositoryViewsBrowser773(t *testing.T) {
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
	theirs, _ := queryJournal(t, 1)
	graph := func(short, says string, files map[string][]byte) *DecisionGraph {
		g, err := BuildDecisionGraph(short, files)
		if err != nil {
			t.Fatal(err)
		}
		for i := range g.Nodes {
			g.Nodes[i].Text, g.Nodes[i].Says = says, says
		}
		return g
	}
	log := "| ID | Date | Door | Status | Decision | Who |\n| :-- | :-- | :-- | :-- | :-- | :-- |\n" +
		"| D-0001 | 2026-11-09 | two-way | decided | Invariant's own decision. | agent |\n"
	s := &Server{Cache: t.TempDir(), Log: t.Logf, Repos: []*Repo{
		{Name: "gitdek/invariant", src: sources{log: log, graph: graph("invariant", "Invariant's own decision.", queryFiles(t, mine))}},
		{Name: "gitdek/ask3d", src: sources{graph: graph("ask3d", "ask3d's own decision.", queryFiles(t, theirs))}},
		{Name: "gitdek/quiet"},
	}}
	fixture, err := json.Marshal(s.assemble(time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	script := "const fixture = " + string(fixture) + ";\n" + repoViewsBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repo-views-check.js" {
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
			return
		}
		if r.URL.Path == "/" {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/repo-views-check.js"></script><script src="/app.js`, 1))
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	for _, page := range []string{"/?repo=ask3d", "/?repo=gitdek%2Fask3d", "/?repo=nope"} {
		t.Run(page, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-background-networking",
				"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1", "--user-data-dir="+t.TempDir(),
				"--virtual-time-budget=20000", "--dump-dom", server.URL+page)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), `data-repo-views="passed"`) {
				i := strings.Index(string(out), `data-repo-views="`)
				t.Fatalf("repository views browser check: %v\n%s", err, string(out)[max(i, 0):min(max(i, 0)+400, len(out))])
			}
		})
	}
}

const repoViewsBrowserCheck = `
(() => {
  // A snapshot without a road, as the fixture's README gives, draws none.
  fixture.slices ||= [];
  const errors = [];
  addEventListener("error", (e) => errors.push(e.message));
  addEventListener("unhandledrejection", (e) => errors.push(String(e.reason)));
  const json = (v) => new Response(JSON.stringify(v), {status: 200, headers: {"Content-Type": "application/json"}});
  window.fetch = (url) => {
    if (url === "/api/state.json") return Promise.resolve(json(fixture));
    if (url.includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected fixture request: " + url));
  };
  const later = window.setTimeout.bind(window);
  const pause = () => new Promise((resolve) => later(resolve, 10));
  async function until(test, label) {
    for (let i = 0; i < 300; i++) { if (test()) return; await pause(); }
    throw new Error("timed out: " + label + " (at " + location.search + ", chosen " + chosen() + ", stored " + localStorage.getItem("invariant-scope") + ")" + (errors.length ? "; the page threw: " + errors.join("; ") : ""));
  }
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const chosen = () => document.querySelector('#scope a[aria-current="page"]')?.dataset.scope;
  const hidden = (id) => document.getElementById(id).closest("section").hidden;
  const decisions = () => document.getElementById("decisions").textContent;
  const link = (scope) => document.querySelector('#scope a[data-scope="' + scope + '"]');
  async function check() {
    const asked = new URLSearchParams(location.search).get("repo");
    if (asked === "nope") {
      await until(() => chosen() === "all", "Everything chosen for a repository the page doesn't show");
      assert(location.search === "", "the address still names a repository the page doesn't show: " + location.search);
      return;
    }
    await until(() => chosen() === "gitdek/ask3d", "ask3d chosen from the address");
    assert(location.search === "?repo=ask3d", "the address isn't ask3d's short name: " + location.search);
    await until(() => document.title.endsWith(" · ask3d · Invariant"), "a title naming ask3d, not " + document.title);
    assert(hidden("road") && hidden("who"), "ask3d's view shows Invariant's road or who builds what");
    await until(() => decisions().includes("ask3d's own decision"), "ask3d's own decisions, not " + decisions());
    assert(!decisions().includes("Invariant's own decision"), "ask3d's view shows Invariant's decisions");
    assert(!new URL(link("all").href).searchParams.has("repo"), "Everything's link names a repository: " + link("all").href);
    assert(new URL(link("gitdek/quiet").href).search === "?repo=quiet", "quiet's link isn't its address: " + link("gitdek/quiet").href);
    if (asked !== "ask3d") return;
    link("all").click();
    await until(() => chosen() === "all", "Everything chosen");
    assert(location.search === "", "Everything's address names a repository: " + location.search);
    await until(() => !hidden("road") && !hidden("who"), "Everything's view shows the road and who builds what");
    await until(() => decisions().includes("Invariant's own decision"), "Everything's view shows Invariant's decisions, not " + decisions());
    history.back();
    await until(() => chosen() === "gitdek/ask3d", "back to ask3d's view");
    assert(location.search === "?repo=ask3d", "back didn't return to ask3d's address: " + location.search);
    link("gitdek/quiet").click();
    await until(() => chosen() === "gitdek/quiet", "quiet chosen");
    assert(location.search === "?repo=quiet", "quiet's address is " + location.search);
    await until(() => decisions().includes("No decisions are recorded in quiet yet"), "quiet's view says it has no decisions, not " + decisions());
    history.back();
    await until(() => chosen() === "gitdek/ask3d" && location.search === "?repo=ask3d", "back from quiet to ask3d");
    history.forward();
    await until(() => chosen() === "gitdek/quiet" && location.search === "?repo=quiet", "forward to quiet again");
  }
  addEventListener("load", () => check().then(
    () => { document.body.dataset.repoViews = "passed"; },
    (e) => { document.body.dataset.repoViews = "failed: " + e.message; }));
})();
`
