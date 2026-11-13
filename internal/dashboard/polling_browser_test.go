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

// The page names the snapshot it has when it reads again, and draws
// nothing again when the server says that's still the one. A hidden page
// makes no read at all until it's shown, and then reads at once (#854).
func TestThePageReadsOnlyWhatChangedAndOnlyWhenShown(t *testing.T) {
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
	script := fmt.Sprintf("const fixture = %s;\n", reader631JSON(t, snap)) + pollingBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/polling-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/polling-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	out, err := browserDOM(t, browser, server.URL+"/", 120000)
	if err != nil || !strings.Contains(string(out), `data-polling-check="passed"`) {
		report := string(out)
		if i := strings.Index(report, `id="polling-report"`); i >= 0 {
			report = report[i:min(len(report), i+1500)]
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("the page's reads, in a browser: %v\n%s", err, report)
	}
}

const pollingBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const TAG = '"snapshot-1"';
  const reads = {state: [], live: 0};
  window.fetch = (url, options = {}) => {
    if (url === "/api/state.json") {
      const named = (options.headers || {})["If-None-Match"] || "";
      reads.state.push(named);
      if (named === TAG) return Promise.resolve(new Response(null, {status: 304, headers: {ETag: TAG}}));
      return Promise.resolve(new Response(JSON.stringify(fixture), {headers: {"Content-Type": "application/json", ETag: TAG}}));
    }
    if (url.includes("/api/live.json")) {
      reads.live++;
      return Promise.resolve(new Response(JSON.stringify({steps: []}), {headers: {"Content-Type": "application/json"}}));
    }
    return Promise.reject(new Error("unexpected request: " + url));
  };
  let hidden = false;
  Object.defineProperty(document, "hidden", {get: () => hidden, configurable: true});
  const show = (yes) => { hidden = !yes; document.dispatchEvent(new Event("visibilitychange")); };
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const pause = (ms = 10) => new Promise((resolve) => setTimeout(resolve, ms));
  async function until(test, label) {
    for (let i = 0; i < 4000; i++) { if (test()) return; await pause(); }
    throw new Error("never: " + label);
  }
  async function run() {
    const numbers = document.querySelector("#numbers");
    await until(() => reads.state.length === 1 && numbers.firstElementChild, "the first draw");
    assert(reads.state[0] === "", "the first read named a snapshot it didn't have: " + reads.state[0]);
    // The second read names what the page has, and nothing is drawn again.
    const kept = numbers.firstElementChild;
    kept.dataset.kept = "1";
    let drawn = 0;
    const watcher = new MutationObserver((records) => { drawn += records.filter((r) => r.type === "childList").length; });
    for (const id of ["#numbers", "#projects", "#fleet", "#inbox", "#lanes"]) watcher.observe(document.querySelector(id), {childList: true, subtree: false});
    await until(() => reads.state.length === 2, "the second read");
    await pause(50);
    assert(reads.state[1] === TAG, "the second read didn't name the snapshot the page has: " + reads.state[1]);
    assert(numbers.firstElementChild === kept && drawn === 0, "an unchanged snapshot was drawn again: " + drawn + " sections replaced");
    assert(!document.querySelector("#updated").classList.contains("lost"), "a 304 reads as a lost server");
    // Hidden, the page reads nothing, for as long as it's hidden.
    show(false);
    const before = {state: reads.state.length, live: reads.live};
    await pause(60000);
    assert(reads.state.length === before.state && reads.live === before.live, "a hidden page read " + (reads.state.length - before.state) + " snapshots and " + (reads.live - before.live) + " live states");
    // Shown again, it reads both at once.
    show(true);
    await until(() => reads.state.length === before.state + 1 && reads.live === before.live + 1, "a read when shown again");
    await pause(16000);
    assert(reads.state.length === before.state + 2, "the page didn't go on reading once shown");
    document.body.dataset.pollingCheck = "passed";
  }
  addEventListener("DOMContentLoaded", () => run().catch((error) => {
    document.body.dataset.pollingCheck = "failed";
    const report = document.createElement("pre");
    report.id = "polling-report";
    report.textContent = error.stack;
    document.body.append(report);
  }));
})();
`
