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

// The page is never wider than its window (#888). Between a phone's width
// and 1,000 pixels a long chip in a model's row reached past the page's
// edge, by 53 pixels in a window of 900, 78 in one of 874 and 179 in one
// of 768, measured on the live page's data. The page hides what's past its
// edge, so it shows no sideways scrollbar, but a focus or a scroll into
// view could still move it there, and nothing a reader did moved it back.
//
// The fixture's chips are short, so the check makes each one longer than
// any row, and then holds the page to its window's width, each chip to its
// row, and a request to scroll sideways to moving nothing.
func TestThePageIsNeverWiderThanItsWindow888(t *testing.T) {
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
	script := fmt.Sprintf("const fixture = %s;\n", reader631JSON(t, s.assemble(time.Now()))) + pageWidthBrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page-width-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/page-width-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	// A laptop's window, where a row has room, the widths where it hadn't,
	// and the narrowest a browser run's window gets, where the page lays
	// out as on a phone.
	for _, width := range []int{1440, 1000, 874, 768, 500} {
		t.Run(fmt.Sprintf("%d wide", width), func(t *testing.T) {
			out, err := browserDOM(t, browser, server.URL+"/", 20000, fmt.Sprintf("--window-size=%d,900", width))
			if err != nil || !strings.Contains(string(out), `data-page-width="passed"`) {
				report := string(out)
				if i := strings.Index(report, `data-page-width="`); i >= 0 {
					report = report[i:]
					if j := strings.Index(report[len(`data-page-width="`):], `"`); j >= 0 {
						report = report[:len(`data-page-width="`)+j+1]
					}
				} else if len(report) > 1500 {
					report = report[len(report)-1500:]
				}
				t.Fatalf("the page's width, in a browser: %v\n%s", err, report)
			}
		})
	}
}

const pageWidthBrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  window.fetch = (url) => {
    if (String(url).includes("/api/state.json")) return Promise.resolve(json(structuredClone(fixture)));
    if (String(url).includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const pause = () => new Promise((resolve) => setTimeout(resolve, 10));
  async function until(test, label) {
    for (let i = 0; i < 1500; i++) { if (test()) return; await pause(); }
    throw new Error("never: " + label);
  }
  async function check() {
    const page = document.documentElement;
    const over = () => page.scrollWidth - page.clientWidth;
    const chips = () => [...document.querySelectorAll("#projects .impls .impl")];
    await until(() => chips().length > 0, "a model's row with a chip");
    assert(over() <= 0, "the page is " + over() + " pixels wider than its window of " + innerWidth + " as it's drawn");
    // Every chip says a figure six times more, each on a line of its own
    // that can't break, so it's longer than any row.
    let longest = 0;
    for (const chip of chips()) {
      for (let i = 0; i < 6; i++) {
        const more = document.createElement("span");
        more.className = "ev";
        more.textContent = "1,234,567/9,876,543 states run";
        chip.append(more);
      }
      const words = [...chip.children].reduce((sum, part) => sum + part.getBoundingClientRect().width, 0);
      longest = Math.max(longest, words - chip.closest(".impls").getBoundingClientRect().width);
    }
    assert(longest > 0, "the check's chips aren't longer than their rows, so it proves nothing");
    assert(over() <= 0, "a long chip makes the page " + over() + " pixels wider than its window of " + innerWidth);
    for (const chip of chips()) {
      const box = chip.getBoundingClientRect(), row = chip.closest(".impls").getBoundingClientRect();
      assert(box.right <= row.right + 1 && box.left >= row.left - 1, "a chip reaches " + Math.round(box.right - row.right) + " pixels past its row");
      assert(box.right <= page.clientWidth + 1, "a chip reaches " + Math.round(box.right - page.clientWidth) + " pixels past the page's edge");
    }
    // Nothing can move the page sideways: not a request to scroll, and not
    // bringing its last chip into view.
    scrollTo(400, scrollY);
    assert(scrollX === 0, "asked to scroll sideways, the page moved " + scrollX + " pixels");
    chips().at(-1).scrollIntoView({ block: "center", inline: "end" });
    assert(scrollX === 0, "bringing a chip into view moved the page " + scrollX + " pixels sideways");
    chips().at(-1).focus?.();
    assert(scrollX === 0, "a focus moved the page " + scrollX + " pixels sideways");
  }
  addEventListener("load", () => check().then(
    () => { document.body.dataset.pageWidth = "passed"; },
    (e) => { document.body.dataset.pageWidth = "failed: " + e.message; }));
})();
`
