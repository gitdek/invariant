package dashboard

import (
	"encoding/json"
	"fmt"
	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const reader603Model = `---- MODULE Reader603 ----
EXTENDS Naturals, Helper603
CONSTANT Limit
VARIABLE n
vars == <<n>>
Init == n = Zero
Advance == /\ n < Limit /\ n' = n + 1
Reset == /\ n = Limit /\ n' = Zero
Poll == UNCHANGED n
Next == Advance \/ Reset \/ Poll
Safe == n \in 0..Limit
Spec == Init /\ [][Next]_vars
====
`
const reader603Include = "---- MODULE Helper603 ----\nZero == 0\n====\n"
const reader603Draft = `{"name":"reader lifecycle","slug":"reader-lifecycle","module":"Reader603","package":"reader","bounds":{"Limit":"2"},"statements":[{"name":"Spec","kind":"spec","says":"Advance, reset or poll."},{"name":"Safe","kind":"invariant","says":"The count stays within its limit."}]}`
const reader603View = `{
 "version":1,
 "state":[
  {"id":"ready","when":{"eq":[{"var":"n"},"0"]},"label":"Ready"},
  {"id":"working","when":{"eq":[{"var":"n"},"1"]},"label":"Working"},
  {"id":"done","when":true,"label":"Done"}],
 "step":[
  {"id":"poll","when":{"eq":[{"action":true},"Poll"]},"label":"Internal poll","hide":true},
  {"id":"advance","when":{"eq":[{"action":true},"Advance"]},"label":"Continue"},
  {"id":"reset","when":true,"label":"Start again"}]
}
`

func reader603Write(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func reader603Workspace(t *testing.T, dir string) {
	t.Helper()
	for name, text := range map[string]string{"proposal.json": reader603Draft, "Reader603.tla": reader603Model, "Helper603.tla": reader603Include, "view.json": reader603View} {
		reader603Write(t, filepath.Join(dir, name), text)
	}
}
func reader603JSON(t *testing.T, x any) []byte {
	t.Helper()
	b, err := json.Marshal(x)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func reader603Object(t *testing.T, x any) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(reader603JSON(t, x), &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func reader603Copy(t *testing.T, p *formalize.Proposal, edit func(map[string]any)) *formalize.Proposal {
	t.Helper()
	m := reader603Object(t, p)
	edit(m)
	var q formalize.Proposal
	if err := json.Unmarshal(reader603JSON(t, m), &q); err != nil {
		t.Fatal(err)
	}
	return &q
}

func init() {
	if os.Getenv("INVARIANT_READER603_DOCKER") == "1" {
		os.Exit(reader603Docker(os.Args[1:]))
	}
}
func reader603Tools(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	bin := t.TempDir()
	log := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexport INVARIANT_READER603_DOCKER=1\nexec '" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_READER603_LOG", log)
	jar := filepath.Join(t.TempDir(), "tla2tools.jar")
	reader603Write(t, jar, "fixture TLC jar 603")
	return toolchain.Toolchain{JavaImage: toolchain.JavaImage, TLCJar: jar}, log
}
func reader603Docker(args []string) int {
	fail := func(s string) int { fmt.Fprintln(os.Stderr, "reader603 Docker double: "+s); return 91 }
	if len(args) == 0 {
		return fail("no command")
	}
	switch args[0] {
	case "info":
		fmt.Println(`{"NCPU":2,"MemTotal":4294967296}`)
		return 0
	case "image", "inspect", "pull", "rm":
		return 0
	case "run":
	default:
		return fail("unexpected command " + args[0])
	}
	var work, cfg, dump, name string
	for i, a := range args {
		if i+1 >= len(args) {
			continue
		}
		switch a {
		case "-v", "--volume":
			if strings.HasSuffix(args[i+1], ":/work") {
				work = strings.TrimSuffix(args[i+1], ":/work")
			}
		case "-config":
			cfg = args[i+1]
		case "-dump":
			if i+2 < len(args) && args[i+1] == "dot,actionlabels" {
				dump = args[i+2]
			}
		case "--name":
			name = args[i+1]
		}
	}
	joined := " " + strings.Join(args, " ") + " "
	if work == "" || cfg == "" || !strings.Contains(joined, " tlc2.TLC ") || !strings.Contains(joined, " --network none ") || !strings.Contains(joined, " -workers 1 ") || strings.Contains(joined, " -deadlock ") {
		return fail("lost TLC invocation contract: " + joined)
	}
	local := func(p string) string { return filepath.Join(work, strings.TrimPrefix(p, "/work/")) }
	config, err := os.ReadFile(local(cfg))
	if err != nil {
		return fail(err.Error())
	}
	module := strings.TrimSuffix(args[len(args)-1], ".tla")
	src, err := os.ReadFile(filepath.Join(work, module+".tla"))
	if err != nil {
		return fail(err.Error())
	}
	main, err := os.ReadFile(filepath.Join(work, "Reader603.tla"))
	if err != nil {
		return fail("main model absent: " + err.Error())
	}
	helper, err := os.ReadFile(filepath.Join(work, "Helper603.tla"))
	if err != nil {
		return fail("include absent: " + err.Error())
	}
	match := regexp.MustCompile(`(?m)^CONSTANT Limit = ([0-9]+)$`).FindStringSubmatch(string(config))
	if len(match) != 2 {
		return fail("bounds absent: " + string(config))
	}
	limit, _ := strconv.Atoi(match[1])
	n := limit + 1
	if n < 1 || n > 502 {
		return fail("unexpected fixture size")
	}
	record, _ := json.Marshal(map[string]string{"module": module, "config": string(config), "main": string(main), "include": string(helper), "dump": dump, "args": joined})
	if err := os.WriteFile(filepath.Join(os.Getenv("INVARIANT_READER603_LOG"), name+".json"), record, 0600); err != nil {
		return fail(err.Error())
	}
	initial, steps := 1, 2*n
	if dump != "" {
		var b strings.Builder
		b.WriteString("strict digraph DiskGraph {\nsubgraph cluster_graph {\n")
		replay := module != "Reader603"
		var idVar, rulesVar string
		if replay {
			m := regexp.MustCompile(`(?m)^VARIABLES ([A-Za-z0-9_]+), ([A-Za-z0-9_]+)$`).FindStringSubmatch(string(src))
			if len(m) != 3 {
				return fail("missing rule replay variables")
			}
			idVar, rulesVar = m[1], m[2]
			initial, steps = n, n
			if !strings.Contains(string(src), "Safe |-> Safe") || !strings.Contains(string(src), "UNCHANGED") {
				return fail("invariant replay does not evaluate Safe")
			}
		}
		quote := func(s string) string {
			return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
		}
		for i := 0; i < n; i++ {
			label := fmt.Sprintf("n = %d", i)
			style := ""
			if i == 0 || replay {
				style = ",style=filled"
			}
			if replay {
				label = fmt.Sprintf("/\\ n = %d\n/\\ %s = \"%d\"\n/\\ %s = [Safe |-> TRUE]", i, idVar, i+1, rulesVar)
			}
			fmt.Fprintf(&b, "%d [label=%s%s];\n", i+1, quote(label), style)
		}
		for i := 0; i < n; i++ {
			if replay {
				fmt.Fprintf(&b, "%d -> %d [label=\"Stay\"];\n", i+1, i+1)
				continue
			}
			action := "Advance"
			if i == n-1 {
				action = "Reset"
			}
			fmt.Fprintf(&b, "%d -> %d [label=\"%s\"];\n%d -> %d [label=\"Poll\"];\n", i+1, (i+1)%n+1, action, i+1, i+1)
		}
		b.WriteString("}\n}\n")
		if err := os.WriteFile(local(dump)+".dot", []byte(b.String()), 0600); err != nil {
			return fail(err.Error())
		}
	}
	frame := func(code int, body string) {
		fmt.Printf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
	}
	frame(2262, "TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)")
	frame(2190, fmt.Sprintf("Finished computing initial states: %d distinct states generated at 2026-11-05 00:00:00.", initial))
	if strings.Contains(string(main), "Safe == FALSE") {
		fmt.Println("@!@!@STARTMSG 2110:1 @!@!@\nInvariant Safe is violated.\n@!@!@ENDMSG 2110 @!@!@")
		frame(2186, "Finished in 10ms at (2026-11-05 00:00:00)")
		return 12
	}
	frame(2193, "Model checking completed. No error has been found.")
	frame(2199, fmt.Sprintf("%d states generated, %d distinct states found, 0 states left on queue.", initial+steps, n))
	frame(2194, fmt.Sprintf("The depth of the complete state graph search is %d.", n))
	frame(2186, "Finished in 10ms at (2026-11-05 00:00:00)")
	return 0
}

// Draft wire contract: each alternative modeled behavior Option has preview:{fork,option,choices,
// model}. choices maps ONLY previously authorized fork IDs to their option IDs;
// the alternative being illustrated is identified separately by fork/option.
// model is an ordinary Proposal snapshot: Draft fields plus exact module_text,
// view and includes, with no nested forks, plans, ratification or derived authority.
// Inline sources permit two options to use the same module/include names without
// sharing a filesystem. Checking pins candidates and stages each independently.
// Saved previews add fingerprint, receipt (verify.Report), and problem (on
// failure); model.reader and receipt.reader retain the same computed chart or
// limitation. Chart and receipt.reader add preview:{fork,option,choices}.
// The preview fingerprint binds its containing question/option words, IDs,
// choice context, exact source snapshot, report and reader evidence. Existing
// statement hashes and project verification fingerprints keep their semantics.
// Check checks all independent options and returns their failures together.
// Read alone remains usable for chart-free planning and legacy deserialization.
// Plumbing, PRD and authenticated D-0328 forks with kind:"step_fit" remain
// chart-free; unmarked modeled behavior forks require every preview.
// statechart.Result carries preview as a value with json omitzero, never a
// pointer: SVG validation rejects pointers even when nil. Ordinary charts
// omit preview entirely. Formalizer stages caller-owned effective choice and
// parent context before the agent runs; Check and the final check share it.
type preview639Saved struct {
	Fork        string              `json:"fork"`
	Option      string              `json:"option"`
	Choices     map[string]string   `json:"choices"`
	Model       *formalize.Proposal `json:"model"`
	Receipt     *verify.Report      `json:"receipt"`
	Problem     string              `json:"problem"`
	Fingerprint string              `json:"fingerprint"`
}

func preview639Draft(t *testing.T, forks int, choices map[string]string) map[string]any {
	t.Helper()
	fs := []any{}
	for i := 1; i <= forks; i++ {
		id := fmt.Sprintf("F%d", i)
		opts := []any{}
		for j, oid := range []string{"A", "B"} {
			var model map[string]any
			if err := json.Unmarshal([]byte(reader603Draft), &model); err != nil {
				t.Fatal(err)
			}
			model["bounds"] = map[string]string{"Limit": fmt.Sprint(j + 1)}
			model["module_text"] = reader603Model
			model["includes"] = map[string]string{"Helper603.tla": reader603Include}
			model["view"] = strings.ReplaceAll(reader603View, "Continue", id+oid+" continue")
			model["statements"].([]any)[1].(map[string]any)["says"] = id + oid + " keeps  its own <limit> & result."
			opts = append(opts, map[string]any{"id": oid, "says": id + oid + " completes after " + fmt.Sprint(j+1) + " advances.", "preview": map[string]any{"fork": id, "option": oid, "choices": choices, "model": model}})
		}
		fs = append(fs, map[string]any{"id": id, "question": id + " How many advances?", "options": opts})
	}
	return map[string]any{"forks": fs}
}
func preview639Option(d map[string]any, f, o int) map[string]any {
	return d["forks"].([]any)[f].(map[string]any)["options"].([]any)[o].(map[string]any)
}
func preview639Input(d map[string]any, f, o int) map[string]any {
	return preview639Option(d, f, o)["preview"].(map[string]any)
}
func preview639Write(t *testing.T, ws string, d any) {
	t.Helper()
	reader603Write(t, filepath.Join(ws, "proposal.json"), string(reader603JSON(t, d)))
}
func preview639SavedOptions(t *testing.T, p *formalize.Proposal) []preview639Saved {
	t.Helper()
	if p == nil {
		t.Fatal("missing fork proposal")
	}
	var wire struct {
		Forks []struct {
			Options []struct {
				Preview *preview639Saved `json:"preview"`
			} `json:"options"`
		} `json:"forks"`
	}
	if err := json.Unmarshal(reader603JSON(t, p), &wire); err != nil {
		t.Fatal(err)
	}
	var out []preview639Saved
	for _, f := range wire.Forks {
		for _, o := range f.Options {
			if o.Preview == nil {
				t.Fatal("modeled option has no checked preview")
			}
			out = append(out, *o.Preview)
		}
	}
	if len(out) == 0 {
		t.Fatal("no options")
	}
	return out
}
func preview639Checked(t *testing.T, ws string, tc toolchain.Toolchain) *formalize.Proposal {
	t.Helper()
	p, _, err := formalize.Check(t.Context(), ws, tc)
	if err != nil {
		t.Fatalf("preview check: %v", err)
	}
	for _, v := range preview639SavedOptions(t, p) {
		if v.Problem != "" || v.Model == nil || v.Model.Reader == nil || v.Receipt == nil || !v.Receipt.Passed || !v.Receipt.ModelOnly || v.Receipt.Ratified != nil || v.Receipt.Reader == nil || !strings.HasPrefix(v.Fingerprint, "sha256:") {
			t.Fatalf("incomplete checked preview: %+v", v)
		}
	}
	if p.Ratifiable() || p.Hash != "" {
		t.Fatal("checking alternatives made a ratifiable proposal")
	}
	return p
}
func preview639Validate(t *testing.T, p *formalize.Proposal) error {
	t.Helper()
	v, ok := any(p).(interface{ ValidatePreviews() error })
	if !ok {
		t.Fatal("Proposal must validate bound preview evidence before publication and export")
	}
	return v.ValidatePreviews()
}
func preview639Export(t *testing.T, p *formalize.Proposal, dir string) error {
	t.Helper()
	v, ok := any(p).(interface{ WritePreviews(string) error })
	if !ok {
		t.Fatal("Proposal must export per-option model-only receipts with WritePreviews")
	}
	return v.WritePreviews(dir)
}

// Dashboard wire contract extends each Choice with preview (the complete saved
// option record) and reader (the existing ReaderPresentation shape). The public
// card presents all options and their charts together; its page/SVG links use
// the existing script-free reader endpoint. Cached evidence is validated against
// its repository, issue, containing fork/option words and snapshot, not a project
// commit or another option with the same letter. A damaged option gets its own
// diagnostic and no SVG; siblings remain usable after restart.
type preview639Choice struct {
	ID      string              `json:"id"`
	Says    string              `json:"says"`
	Preview *preview639Saved    `json:"preview"`
	Reader  *ReaderPresentation `json:"reader"`
}

func preview639Server(t *testing.T, p *formalize.Proposal, repo string, issue int) *Server {
	t.Helper()
	const at = "2026-11-05T12:00:00Z"
	is := github.Issue{Number: issue, Title: "Choose modeled behavior", State: "open", CreatedAt: at, Labels: []github.Label{{Name: factory.LabelAsking}}}
	m := factory.Marker{Kind: factory.KindForks, Forks: p.Forks, Proposal: p}
	r := &Repo{Name: repo, FactoryLogin: "factory[bot]", src: sources{issues: []github.Issue{is}, comments: map[int]cachedComments{issue: {comments: []github.Comment{{ID: 639, User: github.User{Login: "factory[bot]"}, CreatedAt: at, Body: marker(t, m)}}}}}}
	return &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
}
func preview639Snapshot(t *testing.T, s *Server) []byte {
	t.Helper()
	snap := s.assemble(time.Date(2026, 11, 5, 12, 1, 0, 0, time.UTC))
	b := reader603JSON(t, snap)
	gz, err := gzipped(b)
	if err != nil {
		t.Fatal(err)
	}
	s.state = gz
	s.saveSnapshot(gz)
	return b
}
func preview639Choices(t *testing.T, b []byte) []preview639Choice {
	t.Helper()
	var wire struct {
		Issues []struct {
			Waiting *struct {
				Forks []struct {
					ID      string             `json:"id"`
					Options []preview639Choice `json:"options"`
				} `json:"forks"`
			} `json:"waiting"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	var out []preview639Choice
	for _, is := range wire.Issues {
		if is.Waiting == nil {
			continue
		}
		for _, f := range is.Waiting.Forks {
			for _, o := range f.Options {
				if o.Preview == nil || o.Reader == nil {
					t.Fatal("modeled decision view dropped option evidence or its presentation")
				}
				out = append(out, o)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("dashboard lost modeled choices")
	}
	return out
}
func preview639Image(t *testing.T, s *Server, o preview639Choice) string {
	t.Helper()
	p := o.Reader
	if p == nil || p.Status != "chart" || p.SVG == "" || p.Page == "" || p.Fingerprint == "" {
		t.Fatalf("incomplete preview presentation: %+v", p)
	}
	w := reader631GET(s, "GET", p.SVG, "")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/svg+xml") || w.Header().Get("ETag") == "" || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("standalone preview SVG: %d %v", w.Code, w.Header())
	}
	want, err := o.Preview.Model.Reader.Chart.SVG()
	if err != nil || w.Body.String() != want {
		t.Fatal("dashboard SVG does not match that option's native receipt renderer")
	}
	if got := reader631GET(s, "GET", p.SVG, w.Header().Get("ETag")); got.Code != 304 || got.Body.Len() != 0 {
		t.Fatal("conditional preview image was not stable")
	}
	page := reader631GET(s, "GET", p.Page, "")
	body := page.Body.String()
	if page.Code != 200 || !strings.Contains(body, "<img") || !strings.Contains(html.UnescapeString(body), p.SVG) {
		t.Fatal("evidence page does not show this preview's image")
	}
	for _, word := range []string{"unratified", "model-only", o.Preview.Fork, o.Preview.Option, "Limit", o.Preview.Model.Statements[1].Says, o.Preview.Model.Reader.Chart.Report.Source.Tool.JarSHA256} {
		if !strings.Contains(strings.ToLower(html.UnescapeString(body)), strings.ToLower(word)) {
			t.Errorf("preview page omits %q", word)
		}
	}
	if strings.Contains(strings.ToLower(body), "<script") || strings.Contains(body, "/invariant ratify") || !strings.Contains(page.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("preview page requires script or offers ratification")
	}
	return w.Body.String()
}

func TestPreview639DashboardComparisonAndRestart(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	preview639Write(t, ws, preview639Draft(t, 2, nil))
	p := preview639Checked(t, ws, tc)
	s := preview639Server(t, p, "owner/project", 639)
	body := preview639Snapshot(t, s)
	options := preview639Choices(t, body)
	if len(options) != 4 {
		t.Fatal("comparison does not retain all options across multiple forks")
	}
	saved := preview639SavedOptions(t, p)
	urls := map[string]bool{}
	for i, o := range options {
		if !reflect.DeepEqual(*o.Preview, saved[i]) {
			t.Fatal("dashboard lost exact inputs, rules, bounds, diagnostics or provenance")
		}
		preview639Image(t, s, o)
		if urls[o.Reader.SVG] {
			t.Fatal("different fork/option identities share an image URL")
		}
		urls[o.Reader.SVG] = true
	}
	cold := &Server{Cache: s.Cache, Repos: []*Repo{{Name: "owner/project"}}}
	cold.loadSnapshot()
	response := reader631GET(cold, "GET", "/api/state.json", "")
	if response.Code != 200 {
		t.Fatal("restart lost the modeled comparison")
	}
	again := preview639Choices(t, response.Body.Bytes())
	if !reflect.DeepEqual(options, again) {
		t.Fatal("restart changed healthy option evidence or identity")
	}
	for _, o := range again {
		preview639Image(t, cold, o)
	}
	for _, where := range []struct {
		repo  string
		issue int
	}{{"owner/other", 639}, {"owner/project", 640}} {
		other := preview639Server(t, p, where.repo, where.issue)
		choices := preview639Choices(t, preview639Snapshot(t, other))
		if choices[0].Reader.SVG == options[0].Reader.SVG {
			t.Fatal("preview identity omitted repository or issue")
		}
	}
	// A partial answer removes only the answered question, retaining the original
	// checked context for the others until a new draft is actually checked.
	m := factory.Marker{Kind: factory.KindForks, Forks: p.Forks[1:], Proposal: p, Answers: []formalize.Answer{{Fork: "F1", Option: "A", By: "owner", Comment: "https://example.test/answer"}}}
	s.Repos[0].src.comments[639] = cachedComments{comments: []github.Comment{{ID: 640, CreatedAt: "2026-11-05T12:00:30Z", User: github.User{Login: "factory[bot]"}, Body: marker(t, m)}}}
	remaining := preview639Choices(t, preview639Snapshot(t, s))
	if len(remaining) != 2 || remaining[0].Preview.Fork != "F2" || !reflect.DeepEqual(remaining[0].Preview, savedPtr639(saved[2])) {
		t.Fatal("partial answers relabeled or dropped remaining preview evidence")
	}
}
func savedPtr639(v preview639Saved) *preview639Saved { return &v }

func TestPreview639DashboardRefusesMixedEvidence(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	preview639Write(t, ws, preview639Draft(t, 2, nil))
	p := preview639Checked(t, ws, tc)
	for _, kind := range []string{"swapped", "changed", "restart"} {
		t.Run(kind, func(t *testing.T) {
			raw := reader603Object(t, p)
			if kind == "swapped" {
				preview639Option(raw, 0, 0)["preview"] = preview639Option(raw, 1, 0)["preview"]
			}
			if kind == "changed" {
				preview639Input(raw, 0, 0)["model"].(map[string]any)["includes"].(map[string]any)["Helper603.tla"] = "changed include"
			}
			var q formalize.Proposal
			if err := json.Unmarshal(reader603JSON(t, raw), &q); err != nil {
				t.Fatal(err)
			}
			s := preview639Server(t, &q, "owner/project", 639)
			body := preview639Snapshot(t, s)
			if kind == "restart" {
				// Corrupt only one persisted option, leaving healthy siblings and the old
				// presentation intact, so a restart must actually revalidate the evidence.
				var snap map[string]any
				if err := json.Unmarshal(body, &snap); err != nil {
					t.Fatal(err)
				}
				option := snap["issues"].([]any)[0].(map[string]any)["waiting"].(map[string]any)["forks"].([]any)[0].(map[string]any)["options"].([]any)[0].(map[string]any)
				option["preview"].(map[string]any)["model"].(map[string]any)["view"] = "{}"
				gz, err := gzipped(reader603JSON(t, snap))
				if err != nil {
					t.Fatal(err)
				}
				s.saveSnapshot(gz)
			}
			cold := &Server{Cache: s.Cache, Repos: []*Repo{{Name: "owner/project"}}}
			cold.loadSnapshot()
			response := reader631GET(cold, "GET", "/api/state.json", "")
			if response.Code != 200 {
				t.Fatal("one damaged preview hid healthy alternatives on restart")
			}
			options := preview639Choices(t, response.Body.Bytes())
			if options[0].Reader.Status == "chart" || options[0].Reader.SVG != "" || options[0].Reader.Message == "" {
				t.Fatal("invalid preview was displayed as checked")
			}
			if options[0].Reader.Page == "" {
				t.Fatal("diagnostic lost its option evidence page")
			}
			page := reader631GET(cold, "GET", options[0].Reader.Page, "")
			if page.Code != 200 || strings.Contains(page.Body.String(), "<img") {
				t.Fatal("invalid preview diagnostic borrows a chart")
			}
			for _, o := range options[1:] {
				preview639Image(t, cold, o)
			}
			// A second restart preserves the diagnostic without a network refresh.
			gz, err := gzipped(response.Body.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			cold.saveSnapshot(gz)
			again := &Server{Cache: cold.Cache, Repos: []*Repo{{Name: "owner/project"}}}
			again.loadSnapshot()
			second := reader631GET(again, "GET", "/api/state.json", "")
			if second.Code != 200 {
				t.Fatal("diagnostic did not survive a second restart")
			}
			if !reflect.DeepEqual(options, preview639Choices(t, second.Body.Bytes())) {
				t.Fatal("repeated restart changed diagnostic or sibling evidence")
			}
		})
	}
}
