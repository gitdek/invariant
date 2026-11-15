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

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonApplicationReplayViews935(t *testing.T) {
	diagnostic := "<dispatch-935> missing call"
	bug := project.Statement{Name: "ReplayCompletedTask", Kind: project.Bug, CodeBehavior: "current"}
	r := &verify.Report{Bugs: []verify.Bug{{Name: bug.Name, Caught: true, CodeBehavior: "current", Replay: &verify.BugReplayEvidence{Required: true, Status: "failed", Message: "one invocation failed"}}}, Application: &verify.PythonApplicationReport{}}
	for _, id := range []string{"first-935", "second-935"} {
		e := verify.BugReplayEvidence{Required: true, Status: "refused", Passed: true, Request: &verify.BugReplayRequest{Bug: bug}}
		if id == "second-935" {
			e.Status, e.Passed, e.Message = "failed", false, diagnostic
		}
		r.Application.BugReplays = append(r.Application.BugReplays, verify.PythonApplicationBugReplayEvidence{Key: verify.PythonApplicationInvocationKey{Target: "app", Check: "driver", Kind: "conformance", ID: id}, Evidence: e})
	}
	found, missed := foundFor(bug, r)
	if !missed || !strings.Contains(found, "Caught") || !strings.Contains(found, "first-935: refused") || !strings.Contains(found, "second-935: failed: "+diagnostic) {
		t.Fatalf("evidence page collapsed replay pairs: %s", found)
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
	if len(snap.Projects) == 0 {
		t.Fatal("browser fixture has no implementation")
	}
	snap.Projects[0].Bugs = projectOf(projectSource{key: "delivery", manifest: project.Manifest{Language: "python", Existing: []string{"app.py"}}}, r).Bugs
	fixture, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	script := "const fixture = " + string(fixture) + ";\n" + pythonReplayBrowser935
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/python-replay-check.js" {
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
			return
		}
		if req.URL.Path == "/" {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/python-replay-check.js"></script><script src="/app.js`, 1))
			return
		}
		handler.ServeHTTP(w, req)
	}))
	defer server.Close()
	out, err := browserDOM(t, browser, server.URL+"/", 20000)
	if err != nil || !strings.Contains(string(out), `data-python-replay="passed"`) {
		t.Fatalf("browser replay evidence: %v\n%s", err, out)
	}
}

const pythonReplayBrowser935 = `
(() => {
  localStorage.clear();
  const json = v => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  window.fetch = url => {
    if (String(url).includes("/api/state.json")) return Promise.resolve(json(structuredClone(fixture)));
    if (String(url).includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const pause = () => new Promise(resolve => setTimeout(resolve, 10));
  async function check() {
    for (let i = 0; i < 1500; i++) {
      const text = document.body.textContent;
      if (text.includes("first-935: refused") && text.includes("second-935: failed: <dispatch-935> missing call")) {
        if (document.querySelector("dispatch-935")) throw new Error("diagnostic rendered as markup");
        if (!text.includes("ReplayCompletedTask: current; failed")) throw new Error("aggregate failure hidden");
        document.body.dataset.pythonReplay = "passed";
        return;
      }
      await pause();
    }
    throw new Error("not every invocation is visible");
  }
  addEventListener("load", () => check().catch(e => { document.body.dataset.pythonReplay = "failed: " + e.message; }));
})();
`
