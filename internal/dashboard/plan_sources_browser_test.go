package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This uses an installed browser, never a downloaded runtime or a substitute
// DOM. An explicit executable makes missing browser evidence a test failure.
func TestPlanSourcesBrowserControlledResponses(t *testing.T) {
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
	a := source715Proposal(true, "first exact source")
	b := source715Proposal(true, "retry exact source")
	c := source715Proposal(false, "replacement exact source")
	d := source715Proposal(true, "obsolete pending source")
	s := source715Server(t, a, a, b, c, d)
	s.Access = team.access()
	snap := s.assemble(source715Now)
	// The check advances ordinary state polls explicitly. All source responses
	// remain pending until the check resolves them, including out of order.
	script := fmt.Sprintf("const fixture = %s; const responses = [%s,%s,%s,%s];\n", source715JSON(t, snap), checkedPlanSources(a), checkedPlanSources(b), checkedPlanSources(c), checkedPlanSources(d)) + planSourcesBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plan-source-check.js" {
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
			page := strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/plan-source-check.js"></script><script src="/app.js`, 1)
			fmt.Fprint(w, page)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	for _, page := range []string{"/", "/act"} {
		t.Run(page, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-background-networking",
				"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1", "--user-data-dir="+t.TempDir(),
				"--virtual-time-budget=20000", "--dump-dom", server.URL+page)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), `data-plan-check="passed"`) {
				t.Fatalf("controlled-response browser check: %v\n%s", err, out)
			}
			t.Log("closed polls, concurrent/repeated opens, refresh retention, literal source, failure/retry, missing/mismatched responses and replacement passed")
		})
	}
}

const planSourcesBrowserCheck = `
(() => {
  const clone = (v) => JSON.parse(JSON.stringify(v));
  const variants = [fixture.issues[0].waiting, fixture.issues[2].waiting, fixture.issues[3].waiting, fixture.issues[4].waiting];
  const snapshot = clone(fixture);
  snapshot.issues = snapshot.issues.slice(0, 2);
  snapshot.decisions.latest ||= [];
  snapshot.slices ||= [];
  const requests = [];
  let poll, stateReads = 0;
  const later = window.setTimeout.bind(window);
  window.setTimeout = (fn, ms, ...args) => {
    if (fn.name === "load") { poll = fn; return 0; }
    return later(fn, ms, ...args);
  };
  const json = (v, status = 200) => new Response(JSON.stringify(v), {status, headers: {"Content-Type": "application/json"}});
  window.fetch = (url) => {
    if (url === "/api/state.json") { stateReads++; return Promise.resolve(json(snapshot)); }
    if (url.startsWith("/api/plan-sources/")) return new Promise((resolve) => requests.push({url, resolve}));
    if (url === "/act/api/untaken.json") return Promise.resolve(json([]));
    if (url.includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected fixture request: " + url));
  };
  const pause = () => new Promise((resolve) => later(resolve, 10));
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  async function until(test, label) {
    for (let i = 0; i < 100; i++) { if (test()) return; await pause(); }
    throw new Error("timed out: " + label);
  }
  const disclosures = () => [...document.querySelectorAll("[data-plan-sources]")];
  const codes = (el) => [...el.querySelectorAll("pre code")];
  const toggle = (el) => el.querySelector("summary").click();
  const retry = (el) => el.querySelector("[data-source-retry]").click();
  const reply = (index, value, status) => requests[index].resolve(json(value, status));
  async function refresh() {
    const before = stateReads;
    await until(() => poll, "scheduled poll");
    snapshot.issues[0].title += ".";
    poll();
    await until(() => stateReads > before, "state read");
    await pause();
  }
  function exact(el, response) {
    const files = Object.keys(response.sources).sort();
    assert(codes(el).length === files.length, "complete file set");
    codes(el).forEach((code, i) => {
      assert(code.textContent === response.sources[files[i]], "literal bytes: " + files[i]);
      assert(code.className === "language-" + response.languages[files[i]], "language: " + files[i]);
      assert(!code.querySelector("script,img,b"), "source became markup");
    });
    assert(el.querySelector("pre code span"), "language highlighting");
  }
  async function replace(index) {
    for (const issue of snapshot.issues) issue.waiting = clone(variants[index]);
    await refresh();
    assert(disclosures().every((el) => !el.open && !codes(el).length), "replacement must start closed");
  }
  async function run() {
    await until(() => disclosures().length === 2, "both proposal cards");
    assert((location.pathname === "/act") === !!document.body.dataset.act, "page mode");
    const issueKeys = disclosures().map((el) => el.closest("[data-issue]")?.dataset.issue);
    assert(issueKeys.every(Boolean) && new Set(issueKeys).size === 2, "distinct issue identities for shared hash");
    for (let i = 0; i < 3; i++) await refresh();
    assert(requests.length === 0 && disclosures().every((el) => !el.open), "closed polls fetched sources");
    let [first, second] = disclosures();
    toggle(first); toggle(second);
    await until(() => requests.length === 1, "shared pending request");
    assert(first.textContent.includes("Loading") && second.textContent.includes("Loading"), "loading feedback");
    toggle(first); await pause(); toggle(first); await pause();
    await refresh();
    assert(disclosures()[0] === first && disclosures()[1] === second && first.open && second.open && requests.length === 1, "pending open retention and deduplication");
    reply(0, responses[0]);
    await until(() => codes(second).length > 0, "concurrent success");
    exact(first, responses[0]); exact(second, responses[0]);
    first.querySelector("[data-source-content] details").open = true;
    toggle(second); await pause();
    toggle(first); await pause(); toggle(first); await pause();
    await refresh();
    assert(disclosures()[0] === first && disclosures()[1] === second && first.open && !second.open &&
      first.querySelector("[data-source-content] details").open && !second.querySelector("[data-source-content] details").open &&
      requests.length === 1, "independent shared-hash open retention");
    await replace(1);
    first = disclosures()[0]; toggle(first);
    await until(() => requests.length === 2, "new proposal request");
    reply(1, {}, 503);
    await until(() => first.querySelector("[data-source-retry]"), "HTTP failure and retry");
    await refresh();
    assert(disclosures()[0] === first && requests.length === 2, "poll retried a failed request");
    const bad = [null, {}, {...responses[1], hash: responses[0].hash}, {...responses[1], sources: {}}, {...responses[1], languages: {}}];
    for (const response of bad) {
      const n = requests.length;
      retry(first);
      await until(() => requests.length === n + 1, "explicit retry fetch");
      reply(n, response);
      await until(() => first.querySelector("[data-source-retry]"), "reject missing or mismatched response");
      assert(!codes(first).length, "invalid response displayed sources");
    }
    let n = requests.length;
    retry(first);
    await until(() => requests.length === n + 1, "recovery request");
    reply(n, responses[1]);
    await until(() => codes(first).length > 0, "retry success");
    exact(first, responses[1]);
    await replace(3);
    const obsolete = disclosures()[0]; toggle(obsolete);
    n = requests.length;
    await until(() => requests.length === n + 1, "obsolete pending request");
    const oldRequest = n;
    await replace(2);
    first = disclosures()[0]; toggle(first);
    n = requests.length;
    await until(() => requests.length === n + 1, "replacement request");
    reply(n, responses[2]);
    await until(() => codes(first).length > 0, "replacement success");
    reply(oldRequest, responses[3]);
    await pause();
    exact(first, responses[2]);
    assert(!obsolete.isConnected && !codes(obsolete).length, "old response populated replaced proposal");
    n = requests.length;
    await replace(0);
    first = disclosures()[0]; toggle(first);
    await until(() => codes(first).length > 0, "reuse successful full hash");
    assert(requests.length === n, "successful response was fetched again");
    exact(first, responses[0]);
    document.body.dataset.planCheck = "passed";
  }
  addEventListener("DOMContentLoaded", () => run().catch((error) => {
    document.body.dataset.planCheck = "failed";
    const report = document.createElement("pre");
    report.textContent = error.stack;
    document.body.append(report);
  }));
})();
`
