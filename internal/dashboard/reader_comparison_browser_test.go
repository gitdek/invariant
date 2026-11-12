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

// Exercise the actual public and /act project renderers, including a damaged
// comparison on a grouped implementation that does not supply the row's tile.
func TestReaderComparisonDiagnosticBrowser(t *testing.T) {
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
	f := receipt723Fixture(t)
	f.Report.Reader.Comparison = nil
	f.bind(t)
	s, _, _ := receipt723Load(t, f, nil)
	snap := s.assemble(time.Now())
	affected := snap.Projects[0]
	healthy := affected
	r := *healthy.Reader
	r.Message = "Checked final evidence."
	healthy.Reader = &r
	healthy.Name, healthy.Dir = "Healthy implementation", "examples/a"
	affected.Name, affected.Dir = "Affected <implementation>", "examples/b"
	affected.Reader.Message += ` <script>window.comparisonExecuted = true</script> & λ`
	snap.Projects = append(snap.Projects[:0], healthy, affected)
	team := newAccessTeam(t)
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	s.Access = team.access()
	script := fmt.Sprintf("const fixture = %s;\n", reader631JSON(t, snap)) + readerComparisonBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/comparison-check.js" {
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/act" {
			r.Header.Set("Cf-Access-Jwt-Assertion", owner)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/comparison-check.js"></script><script src="/app.js`, 1))
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	for _, page := range []string{"/", "/act"} {
		t.Run(page, func(t *testing.T) {
			out, err := browserDOM(t, browser, server.URL+page, 20000)
			if err != nil || !strings.Contains(string(out), `data-comparison-check="passed"`) {
				t.Fatalf("comparison diagnostic browser check: %v\n%s", err, out)
			}
		})
	}
}

const readerComparisonBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  window.fetch = (url) => {
    if (url === "/api/state.json") return Promise.resolve(json(fixture));
    if (url === "/act/api/untaken.json") return Promise.resolve(json([]));
    if (url.includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const pause = () => new Promise((resolve) => setTimeout(resolve, 10));
  async function until(test) {
    for (let i = 0; i < 100; i++) { if (test()) return; await pause(); }
    throw new Error("projects did not render");
  }
  function diagnostic(container) {
    const note = container.querySelector(".reader-note");
    assert(note && note.textContent === fixture.projects[1].name + " (" + fixture.projects[1].dir + "): " + fixture.projects[1].reader.message, "complete literal project diagnostic");
    assert(!note.querySelector("script,implementation") && !window.comparisonExecuted, "diagnostic became markup");
  }
  async function run() {
    await until(() => document.querySelectorAll("#projects .impls li").length === 2);
    assert((location.pathname === "/act") === !!document.body.dataset.act, "page mode");
    assert(document.querySelectorAll("#projects article.model").length === 1, "grouped model row");
    const implementations = document.querySelectorAll("#projects .impls li");
    assert(!implementations[0].querySelector(".reader-note"), "diagnostic assigned to healthy implementation");
    diagnostic(implementations[1]);
    assert(document.querySelector("#projects .tile.chart img"), "final poster missing");
    document.querySelector('.viewtoggle button[data-view="table"]').click();
    await until(() => document.querySelectorAll("#projects tbody tr").length === 2);
    const rows = document.querySelectorAll("#projects tbody tr");
    assert(!rows[0].querySelector(".reader-note"), "table diagnostic assigned to healthy implementation");
    diagnostic(rows[1]);
    document.body.dataset.comparisonCheck = "passed";
  }
  addEventListener("DOMContentLoaded", () => run().catch((error) => {
    document.body.dataset.comparisonCheck = "failed";
    const report = document.createElement("pre");
    report.textContent = error.stack;
    document.body.append(report);
  }));
})();
`
