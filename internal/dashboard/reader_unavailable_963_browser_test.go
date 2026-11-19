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

func TestReader963UnavailableBrowser(t *testing.T) {
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
	f := saved897FixtureFor(t, reader963Questions(t))
	f.s.prepare()
	f.refresh(t)
	s := f.s
	snap := s.assemble(time.Now())
	for i, kind := range []string{"projected_states", "concrete_states", "input_bytes", "replay_bytes", "output_bytes", "steps", "invariant_cells", "historical", "comparison"} {
		p := reader963Proposal(t, reader963Limited(t, "projected_states"))
		if kind == "comparison" {
			p = comparison717Proposal(t, reader963Comparison(t, reader963Limited(t, "historical"), reader631FixtureFor(t, 3, "Candidate")))
		} else {
			p = reader963Proposal(t, reader963Limited(t, kind))
		}
		is := Issue{Repo: f.r.Name, Number: 963 + i, Title: kind, Open: true, Stage: StageAsking,
			Waiting: &Waiting{Kind: "proposal", Proposal: p, Name: p.Name, Hash: strings.TrimPrefix(p.Hash, "sha256:")[:12],
				Since: time.Now(), Takes: []string{"ratify", "revise"}}}
		for _, st := range p.Statements {
			is.Waiting.Statements = append(is.Waiting.Statements, Said{Name: st.Name, Kind: st.Kind, Says: st.Says})
		}
		s.presentComparison(&is)
		snap.Issues = append(snap.Issues, is)
	}
	team := newAccessTeam(t)
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	s.Access = team.access()
	script := fmt.Sprintf("const fixture = %s;\n", reader631JSON(t, snap)) + reader963BrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/unavailable-check.js" {
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
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/unavailable-check.js"></script><script src="/app.js`, 1))
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	for _, page := range []string{"/", "/act"} {
		t.Run(page, func(t *testing.T) {
			out, err := browserDOM(t, browser, server.URL+page, 30000)
			if err != nil || !strings.Contains(string(out), `data-unavailable-check="passed"`) {
				t.Fatalf("unavailable evidence browser check: %v\n%s", err, out)
			}
		})
	}
}

const reader963BrowserCheck = `
(() => {
  localStorage.clear();
  fixture.decisions.latest ||= [];
  fixture.slices ||= [];
  const evidenceFetch = window.fetch.bind(window);
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type": "application/json"}});
  window.fetch = (url) => {
    if (url.startsWith("/reader/")) return evidenceFetch(url);
    if (url === "/api/state.json") return Promise.resolve(json(fixture));
    if (url === "/act/api/untaken.json") return Promise.resolve(json([]));
    if (url.includes("/api/live.json")) return Promise.resolve(json({steps: []}));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok, label) => { if (!ok) throw new Error(label); };
  const pause = () => new Promise((resolve) => setTimeout(resolve, 10));
  async function until(test) {
    for (let i = 0; i < 200; i++) { if (test()) return; await pause(); }
    throw new Error("unavailable evidence did not render");
  }
  // A card can sit inside a closed fold: Needs you folds what has waited
  // more than two days, as the saved question has since 2026-11-12. A person
  // opens each closed ancestor by its own summary before reaching what is
  // inside, so do that, and let its toggle settle before any redraw.
  async function reveal(el) {
    for (let fold; (fold = el.parentElement.closest("details:not([open])")); ) {
      const summary = fold.querySelector(":scope > summary");
      assert(summary, "a closed fold has no summary to open it by");
      const toggled = new Promise((resolve) => fold.addEventListener("toggle", resolve, {once: true}));
      summary.click();
      assert(fold.open, "a closed fold did not open from its summary");
      await toggled;
    }
  }
  async function unavailable(card, reader) {
    const box = card.querySelector(".reader-evidence");
    assert(box && !box.querySelector("img,picture,source"), "unavailable card exposed an image");
    assert(![...box.querySelectorAll("a")].some((a) => a.href.includes(".svg")), "unavailable card exposed an image URL");
    const note = box.querySelector(".reader-note");
    assert(note.textContent === reader.message && reader.message.length <= 420, "concise reason changed");
    const details = note.nextElementSibling;
    assert(details?.tagName === "DETAILS" && !details.open, "closed details must immediately follow the reason");
    await until(() => details.querySelector(".reader-record pre"));
    assert(details.textContent.includes("sha256:") && details.textContent.includes("Limit"), "complete identities and bounds missing");
    assert(!details.querySelector("script,img,svg,iframe,object"), "retained evidence became active markup");
    const summary = details.querySelector("summary");
    await reveal(details);
    summary.focus();
    assert(document.activeElement === summary, "summary is not keyboard focusable");
    summary.click();
    assert(details.open, "native disclosure did not open");
    summary.click();
    assert(!details.open, "native disclosure did not close");
  }
  async function run() {
    await until(() => document.querySelectorAll("article.need[data-issue]").length === fixture.issues.length);
    const privatePage = location.pathname === "/act";
    assert(privatePage === !!document.body.dataset.act, "page mode");
    for (const issue of fixture.issues) {
      const findCard = () => [...document.querySelectorAll("article.need")].find((el) => el.dataset.issue === issue.repo + "#" + issue.number);
      const card = findCard();
      const w = issue.waiting;
      if (w.kind === "proposal") {
        await unavailable(card, w.reader);
        const command = "/invariant ratify " + w.hash;
        const control = privatePage ? card.querySelector('[data-act-confirm="' + command + '"]') : card.querySelector('[data-cmd="' + command + '"]');
        assert(control, "ratification identity or control changed");
        assert(card.querySelector(".reader-record").textContent.includes("Source λ <script>literal</script> & quoted"), "hostile source text lost");
      } else {
        const options = card.querySelectorAll(".alternative");
        assert(options.length === 2, "original alternatives lost");
        await unavailable(options[0], w.forks[0].options[0].reader);
        assert(options[1].querySelector(".reader-poster"), "healthy sibling poster lost");
        for (let i = 0; i < options.length; i++) {
          const option = w.forks[0].options[i];
          const control = findCard().querySelectorAll(".alternative")[i].querySelector("button.opt");
          assert(control && control.textContent.includes(option.says), "literal option wording lost");
          await reveal(control);
          control.focus();
          assert(document.activeElement === control, "choice control cannot receive focus");
          if (privatePage) {
            control.click();
            assert(findCard().querySelectorAll(".alternative")[i].querySelector("button.opt").getAttribute("aria-pressed") === "true", "choice control no longer works");
          } else {
            assert(control.dataset.cmd === "/invariant choose " + w.forks[0].id + " " + option.id, "choice identity changed");
          }
        }
      }
    }
    document.body.dataset.unavailableCheck = "passed";
  }
  addEventListener("DOMContentLoaded", () => run().catch((error) => {
    document.body.dataset.unavailableCheck = "failed";
    const report = document.createElement("pre");
    report.textContent = error.stack;
    document.body.append(report);
  }));
})();
`
