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

// The page opens without a layout shift (#867): until its first snapshot is
// drawn it shows nothing the snapshot will move. Measured on the page before
// this, the shift at load was 0.18 to 0.38 on a laptop and 0.51 on a phone;
// with it, under 0.003 on both. A browser run here can't measure a shift,
// since it draws no frames, so this holds the page to what makes it so: the
// sections under the hero and the footer aren't shown, the hero's words
// wait unseen, and its orbit isn't drawn yet. A page of one repository
// keeps no row for a Show bar it won't have. The first draw shows them
// all, and the hero stays where it was.
func TestThePageShowsNothingMovableBeforeItsFirstDraw867(t *testing.T) {
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
	script := fmt.Sprintf("const fixture = %s;\n", reader631JSON(t, s.assemble(time.Now()))) + noJumpBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/no-jump-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/no-jump-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	out, err := browserDOM(t, browser, server.URL+"/", 20000)
	if err != nil || !strings.Contains(string(out), `data-no-jump="passed"`) {
		report := string(out)
		if i := strings.Index(report, `data-no-jump="`); i >= 0 {
			report = report[i:min(len(report), i+600)]
		} else if len(report) > 1500 {
			report = report[len(report)-1500:]
		}
		t.Fatalf("the opening page, in a browser: %v\n%s", err, report)
	}
}

const noJumpBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  // The snapshot is held back until the check has looked at the page without it.
  let release;
  const held = new Promise((resolve) => { release = resolve; });
  window.fetch = (url) => {
    if (String(url).includes("/api/state.json")) return held.then(() => json(structuredClone(fixture)));
    if (String(url).includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const pause = () => new Promise((resolve) => setTimeout(resolve, 10));
  async function until(test, label) {
    for (let i = 0; i < 1500; i++) { if (test()) return; await pause(); }
    throw new Error("never: " + label);
  }
  const shown = (el) => getComputedStyle(el).display !== "none";
  const below = () => [...document.querySelectorAll("main > section:not(.hero), footer")];
  const words = () => document.querySelector(".hero > div");
  async function check() {
    const main = document.querySelector("main");
    await pause();
    // Before the snapshot: nothing it will move is on the page.
    assert(main.classList.contains("opening"), "the page doesn't start as opening");
    const early = below().filter(shown);
    assert(!early.length, early.length + " sections or the footer show before the first draw: " + early.map((el) => el.id || el.tagName).join(", "));
    assert(getComputedStyle(words()).visibility === "hidden", "the hero's words show before the first draw");
    // This page shows one repository, so it keeps no row for a Show bar.
    assert(document.querySelector("#scope").getBoundingClientRect().height < 1, "a page of one repository keeps " + document.querySelector("#scope").getBoundingClientRect().height + " pixels for a Show bar it won't have");
    // The orbit is drawn by the first snapshot, so there's nothing of it to move.
    assert(document.querySelector("#orbit").childElementCount === 0, "the orbit is drawn before the first snapshot");
    const heroTop = document.querySelector(".hero").getBoundingClientRect().top;
    release();
    await until(() => !main.classList.contains("opening"), "the first draw");
    // After it: everything that waited is shown, and what stayed didn't move.
    assert(getComputedStyle(words()).visibility === "visible", "the hero's words stay unseen after the first draw");
    assert(shown(document.querySelector("footer")), "the footer stays hidden after the first draw");
    const late = below().filter((el) => !el.hidden && !shown(el));
    assert(!late.length, late.length + " sections stay hidden after the first draw");
    assert(Math.abs(document.querySelector(".hero").getBoundingClientRect().top - heroTop) < 1, "the hero moved at the first draw");
    assert(document.querySelector("#orbit").childElementCount > 0, "the first draw didn't draw the orbit");
  }
  addEventListener("load", () => check().then(
    () => { document.body.dataset.noJump = "passed"; },
    (e) => { document.body.dataset.noJump = "failed: " + e.message; }));
})();
`
