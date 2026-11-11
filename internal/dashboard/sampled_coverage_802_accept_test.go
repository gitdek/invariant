package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

type dashboardCoverage802 struct {
	Source      string `json:"source"`
	Invocation  string `json:"invocation"`
	States      int64  `json:"states"`
	ModelStates int64  `json:"model_states"`
	Status      string `json:"status"`
	Fraction    string `json:"fraction"`
	Percent     string `json:"percent"`
	Checked     bool   `json:"checked"`
	Sampled     bool   `json:"sampled"`
}

func dashboardCoverageJSON802(t *testing.T, p Project) []dashboardCoverage802 {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Coverage []dashboardCoverage802 `json:"coverage"`
	}
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out.Coverage
}

var dashboardTags802 = regexp.MustCompile(`<[^>]*>`)
var dashboardSampledPass802 = regexp.MustCompile(`(?i)(pass(?:ed)?|tested)[\s·:()—.,-]*sampled|sampled[\s·:()—.,-]*(pass(?:ed)?|tested)`)

func dashboardText802(b []byte) string {
	return strings.Join(strings.Fields(html.UnescapeString(dashboardTags802.ReplaceAllString(string(b), " "))), " ")
}

// Freeze the pre-extension evidence encoding. A presentation redesign may
// change the page's ?h= hash, but must not move the evidence or poster identity.
func dashboardOriginalIdentity802(e readerEvidence) string {
	type originalReport verify.Report
	var report *originalReport
	if e.Report != nil {
		r := originalReport(*e.Report)
		report = &r
	}
	old := struct {
		Format, Repo, Dir, Commit string
		Manifest                  project.Manifest
		Lock                      project.Lock
		ManifestBytes, LockBytes  []byte
		Files                     map[string][]byte
		View                      []byte
		Comparison                []byte            `json:",omitzero"`
		Code                      map[string]string `json:",omitzero"`
		Report                    *originalReport
		Artifacts                 readerArtifacts
		Traces                    map[string]tlc.TraceFile
		Problem                   string
	}{e.Format, e.Repo, e.Dir, e.Commit, e.Manifest, e.Lock, e.ManifestBytes, e.LockBytes, e.Files, e.View, e.Comparison, e.Code, report, e.Artifacts, e.Traces, e.Problem}
	status, message, svg := e.checked()
	poster, still := e.posters(status)
	if poster != "" {
		message = strings.Replace(message, "the full report is included in the image", "the poster's light replays one real behavior from TLC's graph, and the evidence page holds the full report", 1)
	}
	b, _ := json.Marshal(struct {
		Evidence                       any
		Status, Message, SVG, Template string
		Poster, Still                  string `json:",omitempty"`
	}{old, status, message, svg, readerIdentityTemplate, poster, still})
	return readerHash(b)
}

func TestSampledCoverageDashboardAndEvidencePages802(t *testing.T) {
	s := &Server{Cache: t.TempDir(), Log: t.Logf, Repos: []*Repo{{Name: "owner/coverage"}}}
	snap := s.assemble(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC))
	for _, tc := range []struct {
		name                      string
		states                    int
		total                     int64
		passed, proof, exhaustive bool
		want, percent             string
	}{
		{"partial", 2, 5, true, false, false, "tested (sampled)", "40%"},
		{"near full", 999999, 1000000, true, false, false, "tested (sampled)", "99.99%"},
		{"full sample", 5, 5, true, false, false, "tested", "100%"},
		{"unknown", 2, 0, true, false, false, "tested (sampled)", ""},
		{"proved", 2, 5, true, true, false, "proved", "40%"},
		{"failed", 2, 5, false, false, false, "unchecked", ""},
		{"exhaustive", 5, 5, true, false, true, "tested in every state", "100%"},
	} {
		r := verify.Report{Project: tc.name, Passed: tc.passed, Assurance: "proved", Bounds: map[string]string{"Limit": "4"},
			Design: verify.Design{Passed: true, Outcome: "passed", DistinctStates: tc.total}, Build: verify.Build{Passed: true},
			Conformance: &conformance.Result{Passed: tc.passed, Exhaustive: tc.exhaustive, Runs: 100, Steps: 20000, States: tc.states, ModelStates: tc.total}}
		if tc.proof {
			r.Code = &verify.Code{Passed: true, Verifier: "Nagini", Functions: []string{"step"}}
		}
		r.Fingerprint = verify.Fingerprint(r)
		before, _ := json.Marshal(r)
		ps := projectSource{dir: "examples/" + strings.ReplaceAll(tc.name, " ", "-"), key: "model-" + tc.name, manifest: project.Manifest{Name: tc.name, Language: "typescript"}}
		p := projectOf(ps, &r)
		p.Repo = "owner/coverage"
		if p.Assurance != tc.want {
			t.Errorf("%s assurance=%q, want %q", tc.name, p.Assurance, tc.want)
		}
		if p.Passed != r.Passed || p.Fingerprint != r.Fingerprint {
			t.Errorf("%s changed gate outcome or evidence identity", tc.name)
		}
		coverage := dashboardCoverageJSON802(t, p)
		if len(coverage) != 1 {
			t.Errorf("%s: dashboard JSON has %d coverage records, want one", tc.name, len(coverage))
		} else {
			c := coverage[0]
			if c.Source != "conformance" || c.States != int64(tc.states) || c.ModelStates != tc.total || c.Percent != tc.percent || c.Checked != tc.passed {
				t.Errorf("%s has wrong JSON evidence: %+v", tc.name, c)
			}
		}
		e := readerEvidence{Repo: "owner/coverage", Dir: ps.dir, Commit: strings.Repeat("a", 40), Manifest: ps.manifest, Report: &r}
		page := makeReader(e)
		visible := dashboardText802(page.html)
		if page.presentation.Fingerprint != dashboardOriginalIdentity802(e) {
			t.Errorf("%s: adding display metadata moved archived reader identity", tc.name)
		}
		if tc.percent != "" && (!strings.Contains(visible, fmt.Sprintf("%d/%d", tc.states, tc.total)) || !strings.Contains(visible, tc.percent)) {
			t.Errorf("%s: rendered evidence page omits exact coverage: %s", tc.name, visible)
		}
		if tc.passed && (tc.states < int(tc.total) || tc.total == 0) && !dashboardSampledPass802.MatchString(visible) {
			t.Errorf("%s: rendered page has an unqualified passing result", tc.name)
		}
		if tc.total == 0 && !strings.Contains(strings.ToLower(visible), "coverage unknown") {
			t.Error("missing totals are not visibly unknown on the evidence page")
		}
		if tc.proof && !strings.Contains(visible, "Code proved") {
			t.Error("sampled check hid independent proof on evidence page")
		}
		if tc.name == "full sample" && strings.Contains(visible, "Code tested in every state") {
			t.Error("all sampled states became an exhaustive code claim")
		}
		after, _ := json.Marshal(r)
		if !bytes.Equal(before, after) || verify.Fingerprint(r) != r.Fingerprint {
			t.Errorf("%s: project/evidence rendering mutated report", tc.name)
		}
		// Exercise the actual page handler, including its script-free policy.
		url := s.keepReader(e).Page
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		if w.Code != http.StatusOK || w.Body.String() != string(page.html) || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s: evidence page HTTP contract failed: %d", tc.name, w.Code)
		}
		snap.Projects = append(snap.Projects, p)
	}
	// Independent Python invocations share neither a numerator nor a total.
	app := &verify.Report{Project: "application", Passed: true, Bounds: map[string]string{"Limit": "4"}, Application: &verify.PythonApplicationReport{}}
	for _, id := range []string{"first", "second"} {
		app.Application.Conformance = append(app.Application.Conformance, verify.PythonApplicationConformance{
			Key: verify.PythonApplicationInvocationKey{Target: "application", Check: "drive", Kind: "conformance", ID: id}, Bounds: app.Bounds,
			Result: conformance.Result{Passed: true, States: 2, ModelStates: 5}})
	}
	p := projectOf(projectSource{}, app)
	if cs := dashboardCoverageJSON802(t, p); len(cs) != 2 {
		t.Errorf("application dashboard has %d coverage rows, want two", len(cs))
	} else {
		for _, c := range cs {
			if c.Source != "python_application" || c.Fraction != "2/5" || c.Percent != "40%" || c.Invocation == "" {
				t.Errorf("application coverage was combined or lost: %+v", c)
			}
		}
	}
	e := readerEvidence{Repo: "owner/coverage", Manifest: project.Manifest{Name: "application", Language: "python"}, Report: app}
	page := makeReader(e)
	visible := dashboardText802(page.html)
	if !strings.Contains(visible, "first") || !strings.Contains(visible, "second") || strings.Count(visible, "2/5") < 2 || strings.Contains(visible, "4/10") {
		t.Error("application evidence page does not preserve per-invocation coverage")
	}
	// Browser checks use the same installed-runtime convention as the existing
	// dashboard suite; native rendered-page and JSON assertions above always run.
	dashboardBrowserCoverage802(t, s, snap)
}

func dashboardBrowserCoverage802(t *testing.T, s *Server, snap Snapshot) {
	t.Helper()
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
		t.Log("Browser portion unavailable locally; run with INVARIANT_DASHBOARD_BROWSER for the required browser validation.")
		return
	}
	// A second implementation of the first model must retain its own share.
	snap.Projects[2].Model = snap.Projects[0].Model
	fixture, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	script := "const fixture = " + string(fixture) + ";\n" + dashboardBrowserCheck802
	team := newAccessTeam(t)
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	s.Access = team.access()
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/coverage-check.js" {
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
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/coverage-check.js"></script><script src="/app.js`, 1))
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	for _, path := range []string{"/", "/act"} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-background-networking",
			"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1", "--user-data-dir="+t.TempDir(), "--virtual-time-budget=20000", "--dump-dom", server.URL+path)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil || !strings.Contains(string(out), `data-coverage-check="passed"`) {
			t.Errorf("%s rendered dashboard coverage: %v\n%s", path, err, out)
		}
	}
}

const dashboardBrowserCheck802 = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const json = v => new Response(JSON.stringify(v), {headers:{"Content-Type":"application/json"}});
  window.fetch = url => {
    if (url === "/api/state.json") return Promise.resolve(json(fixture));
    if (url === "/act/api/untaken.json") return Promise.resolve(json([]));
    if (url.includes("/api/live.json")) return Promise.resolve(json({steps:[]}));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok,why) => { if (!ok) throw new Error(why); };
  const text = el => (el?.textContent || "").replace(/\s+/g," ");
  const wait = () => new Promise(resolve => setTimeout(resolve,10));
  async function until(f) { for(let i=0;i<400;i++){if(f())return;await wait();}throw new Error("projects never rendered"); }
  const sampledPass = /(?:pass(?:ed)?|tested)[\s·:()—.,-]*sampled|sampled[\s·:()—.,-]*(?:pass(?:ed)?|tested)/i;
  function check(el,p) {
    const s = text(el), c = p.coverage[0];
    assert(c,"missing coverage in fixture");
    if(c.checked && c.sampled) assert(sampledPass.test(s),p.name+" has an unqualified pass: "+s);
    if(c.percent) assert(s.includes(c.fraction) && s.includes(c.percent),p.name+" omits its fraction: "+s);
    if(c.checked && c.status === "unknown") assert(/coverage unknown/i.test(s) && !s.includes("%"),"unknown invented a percentage: "+s);
    if(p.name === "near full") assert(!/(?:^|[^0-9])100(?:\.0+)?%/.test(s),"partial rounded to 100%: "+s);
    if(p.name === "full sample") assert(!/tested in every state|every step tried|proved/.test(s),"full sample gained a stronger claim: "+s);
    if(p.name === "proved") assert(/proved/.test(s),"independent proof disappeared");
    if(p.name === "failed") assert(/failed/.test(s),"failed conformance became a pass");
  }
  async function run() {
    await until(()=>document.querySelectorAll("#projects .impl").length===fixture.projects.length);
    assert([...document.querySelectorAll("#projects article.model")].some(el=>el.querySelectorAll(".impl").length===2),"implementations were not grouped");
    for(const p of fixture.projects) {
      const el=[...document.querySelectorAll("#projects .impl")].find(el=>(el.title||"").startsWith(p.dir));
      assert(el,"missing variant "+p.dir);check(el,p);
    }
    document.querySelector('.viewtoggle button[data-view="table"]').click();
    await until(()=>document.querySelectorAll("#projects tbody tr").length===fixture.projects.length);
    for(const p of fixture.projects) {
      const el=[...document.querySelectorAll("#projects tbody tr")].find(el=>text(el.querySelector(".name")).includes(p.dir));
      assert(el,"missing table row "+p.dir);check(el,p);
    }
    document.body.dataset.coverageCheck="passed";
  }
  addEventListener("DOMContentLoaded",()=>run().catch(e=>{
    document.body.dataset.coverageCheck="failed";
    const pre=document.createElement("pre");pre.textContent=e.stack;document.body.append(pre);
  }));
})();
`
