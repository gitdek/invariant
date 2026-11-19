package dashboard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
)

// The page says what each run and issue is (#948, #896): a run that waits
// for Codex's turn says so, and what holds it, with no clock, and the header
// counts it apart; each repository's receipts name its own commit; an issue
// a person finished, one just handed over and a plan's issue say what they
// are; and an issue the factory stopped on offers no command. Like the other
// browser checks, it uses an installed browser.
func TestThePageSaysWhatEachRunAndIssueIs948(t *testing.T) {
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
	s, now := page948Server(t)
	snap := s.assemble(now)
	live, err := s.LiveJSON(now)
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("const fixture = %s;\nconst live = %s;\n", reader631JSON(t, snap), live) + page948BrowserCheck
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page-948-check.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
		case "/":
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/page-948-check.js"></script><script src="/app.js`, 1))
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	out, err := browserDOM(t, browser, server.URL+"/", 20000)
	if err != nil || !strings.Contains(string(out), `data-page948="passed"`) {
		i := strings.Index(string(out), `data-page948="`)
		t.Fatalf("page browser check: %v\n%s", err, string(out)[max(i, 0):min(max(i, 0)+600, len(out))])
	}
}

// page948Server is Invariant's and On Air's watchers, issues and receipts
// as they stood on 19 November 2026, recorded: #944's build holds Codex's
// turn while #946's draft and On Air's #16's wait for it; #946 was handed
// over by a comment; #571's plan waits on #669, which needs a person; the
// factory stopped on #933, whose newest comment was cut; and On Air's #8 was
// finished by hand in #21.
func page948Server(t *testing.T) (*Server, time.Time) {
	t.Helper()
	work := t.TempDir()
	at := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
	stamp := func(d time.Duration) string { return at.Add(d).Format(time.RFC3339Nano) }
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	build := Step{Issue: 944, Doing: "building", Since: at, Agent: "codex", Run: "build-944"}
	draft := Step{Issue: 946, Doing: "formalizing", Since: at.Add(time.Minute), Agent: "codex", Run: "formalize-946"}
	onair := Step{Issue: 16, Doing: "formalizing", Since: at.Add(2 * time.Minute), Agent: "codex", Run: "formalize-16"}
	for repo, steps := range map[string][]Step{"gitdek/invariant": {build, draft}, "gitdek/onair": {onair}} {
		st := Status{PID: os.Getpid(), Repo: repo, Login: bot948.Login, Started: at, Heartbeat: at, Every: 30, Limit: 9000}
		st.Update(steps, at)
		if err := WriteStatus(StatusPath(work, repo), st); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(stepDir(work, "gitdek/invariant", build), "transcript.jsonl"),
		`{"timestamp":"`+stamp(10*time.Second)+`","type":"invariant.workspace","cwd":"/nowhere"}`+"\n"+`{"timestamp":"`+stamp(11*time.Second)+`","type":"turn.started"}`+"\n")
	write(filepath.Join(stepDir(work, "gitdek/invariant", draft), "transcript.jsonl"), "")
	write(filepath.Join(stepDir(work, "gitdek/onair", onair), "transcript.jsonl"), "")

	steps := make([]formalize.PlannedIssue, 10)
	for i := range steps {
		steps[i] = formalize.PlannedIssue{Title: fmt.Sprintf("Step %d", i+1)}
	}
	proposal := &formalize.Proposal{Draft: formalize.Draft{Name: "Pieces", Statements: []project.Statement{{Name: "Ordered", Kind: project.Invariant, Says: "Pieces stay in order."}}}, Hash: "sha256:" + strings.Repeat("ab", 32)}
	cut := say948(t, 41, stamp(-time.Hour), factory.Marker{Kind: factory.KindProposal, Proposal: proposal})
	cut.Body = cut.Body[:strings.Index(cut.Body, "<!-- invariant:")]
	cut.URL = "https://github.com/gitdek/invariant/issues/933#issuecomment-41"
	open := func(n int, title, body string, labels ...string) github.Issue {
		is := github.Issue{Number: n, Title: title, State: "open", Body: body, User: person948, AuthorAssociation: "OWNER", CreatedAt: stamp(-48 * time.Hour)}
		for _, l := range labels {
			is.Labels = append(is.Labels, github.Label{Name: l})
		}
		return is
	}
	receipts := func(sha string) *receiptSet {
		return &receiptSet{run: github.Run{ID: int64(len(sha)), Name: "gate", HeadSHA: sha, Status: "completed", Conclusion: "success", StartedAt: stamp(-time.Hour), UpdatedAt: stamp(-50 * time.Minute)}}
	}
	invariant := &Repo{Name: "gitdek/invariant", FactoryLogin: bot948.Login, src: sources{
		issues: []github.Issue{
			open(944, "Apply the reader chart limit after projection", "Kind: plumbing", factory.LabelTrigger, factory.LabelBuilding),
			open(946, "Say which runs wait for their turn", "Kind: plumbing"),
			open(571, "Show a reader's statechart in every modeled proposal", "/invariant plan"),
			open(669, "Draw each known bug as a numbered TLC path", "/invariant solve", factory.LabelTrigger, factory.LabelHumanReview),
			open(933, "Read the pieces in order", "/invariant solve", factory.LabelTrigger, factory.LabelProposal),
		},
		comments: map[int]cachedComments{
			946: {comments: []github.Comment{command948(1, stamp(0), "/invariant solve")}},
			571: {comments: []github.Comment{
				say948(t, 2, stamp(-40*time.Hour), factory.Marker{Kind: factory.KindRatified, Proposal: &formalize.Proposal{IssuePlan: &formalize.IssuePlan{Name: "Reader statecharts", Issues: steps}}}),
				say948(t, 3, stamp(-30*time.Hour), factory.Marker{Kind: factory.KindPlanOpened, Step: 9, Opened: 669}),
			}},
			669: {comments: []github.Comment{
				say948(t, 4, stamp(-51*time.Hour), factory.Marker{Kind: factory.KindRatified, Project: "plumbing/669"}),
				say948(t, 5, stamp(-50*time.Hour), factory.Marker{Kind: factory.KindFailed, Project: "plumbing/669", Failure: factory.FailGate, PR: 700}),
			}},
			933: {comments: []github.Comment{say948(t, 40, stamp(-2*time.Hour), factory.Marker{Kind: factory.KindProposal, Proposal: proposal}), cut}},
		},
		receipts: receipts("950fefa6" + strings.Repeat("0", 32)),
	}}
	finished := github.Issue{Number: 8, Title: "Recording sessions", State: "closed", User: person948, AuthorAssociation: "OWNER", CreatedAt: stamp(-120 * time.Hour), ClosedAt: stamp(-96 * time.Hour)}
	onairRepo := &Repo{Name: "gitdek/onair", FactoryLogin: bot948.Login, src: sources{
		issues: []github.Issue{finished, open(16, "Live context", "Kind: plumbing")},
		comments: map[int]cachedComments{
			8: {comments: []github.Comment{
				command948(6, stamp(-119*time.Hour), "/invariant solve"),
				say948(t, 7, stamp(-110*time.Hour), factory.Marker{Kind: factory.KindRatified, Project: "core/session"}),
				say948(t, 8, stamp(-100*time.Hour), factory.Marker{Kind: factory.KindClosed, Project: "core/session", PR: 21}),
			}},
			16: {comments: []github.Comment{command948(9, stamp(0), "/invariant solve")}},
		},
		hand:     map[int]int{8: 21},
		merges:   map[int]mergeCheck{21: {sha: strings.Repeat("2", 40), green: true, base: "main"}},
		receipts: receipts("31952c1" + strings.Repeat("0", 33)),
	}}
	s := &Server{Work: work, Works: []string{work}, Branch: "main", Cache: t.TempDir(), Log: t.Logf, Repos: []*Repo{invariant, onairRepo}}
	return s, at.Add(90 * time.Minute)
}

const page948BrowserCheck = `
(() => {
  fixture.slices ||= [];
  const errors = [];
  addEventListener("error", (e) => errors.push(e.message));
  addEventListener("unhandledrejection", (e) => errors.push(String(e.reason)));
  const json = (v) => new Response(JSON.stringify(v), {status: 200, headers: {"Content-Type": "application/json"}});
  window.fetch = (url) => {
    if (url === "/api/state.json") return Promise.resolve(json(fixture));
    if (String(url).includes("/api/live.json")) return Promise.resolve(json(live));
    return Promise.reject(new Error("unexpected fixture request: " + url));
  };
  const later = window.setTimeout.bind(window);
  const pause = () => new Promise((resolve) => later(resolve, 10));
  async function until(test, label) {
    for (let i = 0; i < 400; i++) { if (test()) return; await pause(); }
    throw new Error("timed out: " + label() + (errors.length ? "; the page threw: " + errors.join("; ") : ""));
  }
  const assert = (ok, label) => { if (!ok) throw new Error(label()); };
  const text = (el) => (typeof el === "string" ? document.querySelector(el) : el)?.textContent.replace(/\s+/g, " ").trim() || "";
  const card = (n) => [...document.querySelectorAll("#live-cards .livestep")].find((el) => text(el.querySelector(".live-title")).includes(n + " "));
  const lane = (n) => [...document.querySelectorAll("#lanes .lane")].find((el) => text(el.querySelector(".no")) === n);
  async function check() {
    await until(() => text("#receipts").includes("onair"), () => "the receipts label in Everything names each repository's commit, not: " + text("#receipts"));
    assert(text("#receipts") === "Receipts from CI's gate on invariant's 950fefa and onair's 31952c1", () => "Everything's receipts label: " + text("#receipts"));

    await until(() => card("#946") && card("onair#16") && card("#944"), () => "the live cards");
    const waiting = card("#946");
    assert(waiting.classList.contains("waiting") && text(waiting.querySelector(".live-title")).startsWith("Waiting to draft #946"), () => "#946's card: " + text(waiting.querySelector(".live-title")));
    assert(text(waiting.querySelector(".live-now")).startsWith("Waiting for Codex's turn, which #944's build holds."), () => "#946's card says: " + text(waiting.querySelector(".live-now")));
    assert(!waiting.querySelector(".dial").getClientRects().length, () => "a run that waits for its turn shows a clock");
    assert(text(card("onair#16").querySelector(".live-now")).includes("which #944's build holds"), () => "On Air's waiting card says: " + text(card("onair#16").querySelector(".live-now")));
    const running = card("#944");
    assert(!running.classList.contains("waiting") && /\d/.test(text(running.querySelector(".dial .big"))), () => "#944's running card has no clock");
    await until(() => text("#nowmeta").includes("1 running, 2 waiting their turn"), () => "the header counts the waits apart, not: " + text("#nowmeta"));
    assert(!/steps running/.test(text("#nowmeta")), () => "the header counts runs that wait as running: " + text("#nowmeta"));
    assert(text("#nowmeta").includes("1 issue needs you · 1 more for over two days") && !/stalled/.test(text("#nowmeta")), () => "the header's Needs you chip says: " + text("#nowmeta"));

    document.querySelector("#lanesmore")?.click();
    await until(() => lane("#571") && lane("onair#8") && lane("#946"), () => "the lanes, not: " + [...document.querySelectorAll("#lanes .lane .no")].map(text).join(", "));
    assert(text(lane("#571").querySelector(".chips")).includes("step 9 of 10, #669, needs a person"), () => "#571's lane says: " + text(lane("#571").querySelector(".chips")));
    assert(!text(lane("#571").querySelector(".chips")).includes("queued"), () => "#571's lane says it's queued");
    assert(text(lane("onair#8").querySelector(".chips")).includes("merged, finished by hand"), () => "On Air's #8 lane says: " + text(lane("onair#8").querySelector(".chips")));
    assert(text(lane("#946").querySelector(".chips")).includes("waiting for Codex's turn"), () => "#946's lane says: " + text(lane("#946").querySelector(".chips")));

    const stopped = document.querySelector('#inbox [data-issue="gitdek/invariant#933"]');
    assert(stopped && text(stopped).includes("The factory has stopped on this issue") && stopped.querySelector('a[href$="#issuecomment-41"]'), () => "#933's card: " + text(stopped));
    assert(!stopped.querySelector("button[data-cmd]"), () => "#933's card offers a command");

    const system = [...document.querySelectorAll("#fleet .system")].find((el) => text(el.querySelector("h3")) === "onair");
    assert(system && text(system.querySelector(".state")) === "waiting its turn", () => "On Air's watcher in the fleet: " + text(system?.querySelector(".state")));
    assert(text(system.querySelector(".meta")).includes("1 merged"), () => "On Air's merged count: " + text(system.querySelector(".meta")));

    document.querySelector('#scope a[data-scope="gitdek/onair"]').click();
    await until(() => text("#receipts") === "Receipts from CI's gate on 31952c1", () => "On Air's view names its own receipts' commit, not: " + text("#receipts"));
  }
  addEventListener("load", () => check().then(() => { document.body.dataset.page948 = "passed"; }, (e) => { document.body.dataset.page948 = "failed: " + e.message; }));
})();
`
