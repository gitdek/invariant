package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// A repository's tested changes are the ratified plans on its branch, read in
// one query of .invariant/plans, each with the acceptance tests it pinned. A
// plan without a ratification, and anything that isn't an issue's plan, isn't
// counted, and a repository without plans has tested nothing (#775).
func TestARepositoryCountsItsTestedChanges775(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	ours, quiet := strings.Repeat("a", 40), strings.Repeat("c", 40)
	mine, _ := queryJournal(t, 1)
	fake := queryFake(t)
	// The fake answers a query of .invariant/plans from plans.json, and the
	// other repository's branch from a file of its own.
	plans := `if [ "$expr" = "$sha:.invariant/plans" ]; then if [ -f "$d/$sha/plans.json" ]; then cat "$d/$sha/plans.json"; else echo '{"data":{"repository":{"object":null}}}'; fi; exit 0; fi` + "\n\t"
	gh := strings.ReplaceAll(queryGH, "@DIR@", fake)
	gh = strings.Replace(gh, "if [ \"$expr\" != \"$sha:decisions/journal\" ]", plans+"if [ \"$expr\" != \"$sha:decisions/journal\" ]", 1)
	gh = strings.Replace(gh, "*) missing ;;", "*/o/quiet/git/ref/heads/main) printf '{\"object\":{\"sha\":\"%s\"}}\\n' \"$(cat \"$d/quiet\")\" ;;\n*) missing ;;", 1)
	for name, text := range map[string]string{"gh": gh, "quiet": quiet} {
		if err := os.WriteFile(filepath.Join(fake, name), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	queryCommit(t, fake, ours, "# r\n", queryFiles(t, mine), nil)
	plan := func(ratified bool, tests int) string {
		var ts []string
		for i := range tests {
			ts = append(ts, fmt.Sprintf(`{"name":"t%d"}`, i))
		}
		r := ""
		if ratified {
			r = `"ratified":{"by":"gitdek"},`
		}
		return `{` + r + `"plan":{"name":"p","tests":[` + strings.Join(ts, ",") + `]}}`
	}
	var entries []map[string]any
	for name, text := range map[string]string{
		"issue-1.json": plan(true, 3), "issue-2.json": plan(true, 2), "issue-3.json": plan(false, 4), "notes.md": plan(true, 9),
	} {
		entries = append(entries, map[string]any{"name": name, "type": "blob", "oid": queryBlob([]byte(text)), "object": map[string]any{"text": text, "isTruncated": false}})
	}
	queryWrite(t, filepath.Join(fake, ours, "plans.json"), map[string]any{"data": map[string]any{"repository": map[string]any{"object": map[string]any{"entries": entries}}}})
	queryHead(t, fake, ours)

	var mu sync.Mutex
	var logged []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	repo := func(name string) *Repo {
		return &Repo{Name: name, GitHub: github.Client{Repo: name, GH: filepath.Join(fake, "gh")}}
	}
	s := &Server{Repos: []*Repo{repo("o/r"), repo("o/quiet")}, Branch: "main", Every: 50 * time.Millisecond, Log: logf}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		time.Sleep(300 * time.Millisecond)
	})
	s.Start(ctx)

	var snap Snapshot
	for deadline := time.Now().Add(time.Minute); ; {
		snap = querySnapshot(t, s)
		if len(snap.Repos) == 2 && snap.Repos[0].Totals.Tested > 0 {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			last := strings.Join(logged[max(len(logged)-4, 0):], "\n")
			mu.Unlock()
			t.Fatalf("the page never counted o/r's tested changes; the server last logged:\n%s", last)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := snap.Repos[0].Totals; got.Tested != 2 || got.Tests != 5 {
		t.Errorf("o/r tested %d changes by %d tests, want its 2 ratified plans' 5", got.Tested, got.Tests)
	}
	if got := snap.Repos[1].Totals; got.Tested != 0 || got.Tests != 0 {
		t.Errorf("o/quiet, which has no plans, tested %d changes by %d tests", got.Tested, got.Tests)
	}
	if snap.Totals.Tested != 2 || snap.Totals.Tests != 5 {
		t.Errorf("everything tested %d changes by %d tests, want 2 by 5", snap.Totals.Tested, snap.Totals.Tests)
	}
}

// Show's choices, a repository's line and the fleet say what's proved,
// model-checked and tested, never "projects", and a repository's view begins
// its numbers with one row of those three levels (#775).
func TestAssuranceWordsBrowser775(t *testing.T) {
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
	s := &Server{Cache: t.TempDir(), Log: t.Logf, Repos: []*Repo{
		{Name: "gitdek/invariant", src: sources{plans: map[string]int{"a": 3, "b": 4}}},
		{Name: "gitdek/ask3d", src: sources{plans: map[string]int{"c": 3}}},
	}}
	fixture, err := json.Marshal(s.assemble(time.Date(2026, 11, 9, 16, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	script := "const fixture = " + string(fixture) + ";\n" + assuranceBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/assurance-check.js" {
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
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/assurance-check.js"></script><script src="/app.js`, 1))
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-background-networking",
		"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1", "--user-data-dir="+t.TempDir(),
		"--virtual-time-budget=20000", "--dump-dom", server.URL+"/?repo=ask3d")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `data-assurance="passed"`) {
		i := strings.Index(string(out), `data-assurance="`)
		t.Fatalf("assurance browser check: %v\n%s", err, string(out)[max(i, 0):min(max(i, 0)+400, len(out))])
	}
}

const assuranceBrowserCheck = `
(() => {
  fixture.slices ||= [];
  const errors = [];
  addEventListener("error", (e) => errors.push(e.message));
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
    throw new Error("timed out: " + label + (errors.length ? "; the page threw: " + errors.join("; ") : ""));
  }
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const text = (sel) => (document.querySelector(sel)?.textContent || "").replace(/\s+/g, " ");
  async function check() {
    await until(() => document.querySelector('#scope a[aria-current="page"]')?.dataset.scope === "gitdek/ask3d", "ask3d's view");
    const chips = text("#scope");
    assert(!/project/i.test(chips), "Show's choices still say project: " + chips);
    assert(chips.includes("nothing proved yet · 1 tested"), "ask3d's choice doesn't say what it has: " + chips);
    assert(chips.includes("nothing proved yet · 2 tested"), "invariant's choice doesn't say what it has: " + chips);
    await until(() => text("#mainline").includes("nothing proved yet · 1 change tested"), "ask3d's line saying what it has, not " + text("#mainline"));
    assert(!/project/i.test(text("#mainline")), "ask3d's line still says project: " + text("#mainline"));
    const rows = document.querySelectorAll("#assurance .arow");
    assert(rows.length === 1 && rows[0].textContent.includes("ask3d"), "ask3d's view doesn't have one row of its own: " + text("#assurance"));
    const lit = [...rows[0].querySelectorAll(".lvl.on")].map((el) => el.querySelector("b").textContent);
    assert(lit.join() === "Tested", "ask3d's lit levels are " + lit.join());
    assert(text("#assurance").includes("1 change, by 3 acceptance tests"), "ask3d's tested level says " + text("#assurance"));
    assert((text("#assurance").match(/Nothing yet/g) || []).length === 2, "ask3d's unlit levels don't say so: " + text("#assurance"));
    assert(!/project/i.test(text("#fleet")), "the fleet still says project: " + text("#fleet").slice(0, 200));
  }
  addEventListener("load", () => check().then(
    () => { document.body.dataset.assurance = "passed"; },
    (e) => { document.body.dataset.assurance = "failed: " + e.message; }));
})();
`
