package factory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
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
)

// The wire contract adds Proposal.view (the exact view.json text),
// Proposal.includes (sibling .tla filename -> exact source), and optional
// Proposal.reader has version:1, fingerprint:"sha256:..." and exactly one of
// chart:<statechart.Result> or limitation:{code:"too_large",actual:501,limit:500,
// source:<statechart.Source>}. The latter records a passing check, not a chart.
// The fingerprint binds the full inputs, pinned tools and computed evidence;
// it is independent of Proposal.hash. No agent-written reader is authority.
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

type reader603Limitation struct {
	Code   string            `json:"code"`
	Actual int               `json:"actual"`
	Limit  int               `json:"limit"`
	Source statechart.Source `json:"source"`
}

type reader603Snapshot struct {
	Version     int                  `json:"version"`
	Fingerprint string               `json:"fingerprint"`
	Chart       *statechart.Result   `json:"chart"`
	Limitation  *reader603Limitation `json:"limitation"`
}

func reader603Evidence(t *testing.T, p *formalize.Proposal) reader603Snapshot {
	t.Helper()
	var saved struct {
		View     string             `json:"view"`
		Includes map[string]string  `json:"includes"`
		Reader   *reader603Snapshot `json:"reader"`
	}
	if err := json.Unmarshal(reader603JSON(t, p), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.View == "" || saved.Includes["Helper603.tla"] == "" || saved.Reader == nil || (saved.Reader.Chart == nil) == (saved.Reader.Limitation == nil) || saved.Reader.Version != 1 || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(saved.Reader.Fingerprint) {
		t.Fatalf("modeled proposal lost its view, includes or bound computed snapshot: %s", reader603JSON(t, p))
	}
	return *saved.Reader
}
func reader603Saved(t *testing.T, p *formalize.Proposal) reader603Snapshot {
	t.Helper()
	s := reader603Evidence(t, p)
	if s.Chart == nil {
		t.Fatal("supported graph lost its chart")
	}
	return s
}
func reader603Limited(t *testing.T, p *formalize.Proposal, actual int) reader603Snapshot {
	t.Helper()
	s := reader603Evidence(t, p)
	if s.Chart != nil || s.Limitation == nil {
		t.Fatal("large graph needs a limitation and no chart")
	}
	l := s.Limitation
	if l.Code != "too_large" || l.Actual != actual || l.Limit != 500 {
		t.Fatalf("wrong size limitation: %+v", l)
	}
	source := l.Source
	if source.Module != p.Module || source.Config.Specification != "Spec" || source.Config.Constants["Limit"] != p.Bounds["Limit"] || source.Tool.Image == "" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(source.Tool.JarSHA256) || source.TLC == "" {
		t.Fatalf("size limitation lost checked provenance: %+v", source)
	}
	return s
}
func reader603Checked(t *testing.T, ws string, tc toolchain.Toolchain) (*formalize.Proposal, *verify.Report) {
	t.Helper()
	p, r, err := formalize.Check(t.Context(), ws, tc)
	if err != nil || p == nil || r == nil || !r.Passed {
		t.Fatalf("check: proposal=%+v report=%+v error=%v", p, r, err)
	}
	return p, r
}

// Only Docker is replaced. Real Read, pins, RunModel, DumpGraph, rule replay
// and Compute must run. The double checks flags and staged bytes and returns
// complete TLC protocol output; it does not provide a precomputed chart.
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
	frame(2193, "Model checking completed. No error has been found.")
	frame(2199, fmt.Sprintf("%d states generated, %d distinct states found, 0 states left on queue.", initial+steps, n))
	frame(2194, fmt.Sprintf("The depth of the complete state graph search is %d.", n))
	frame(2186, "Finished in 10ms at (2026-11-05 00:00:00)")
	return 0
}
func reader603Calls(t *testing.T, dir string) []map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]string
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

type reader603Formalizer struct {
	run func(context.Context, formalize.Request, string) (*formalize.Result, error)
}

func (f reader603Formalizer) Formalize(ctx context.Context, r formalize.Request, out string) (*formalize.Result, error) {
	return f.run(ctx, r, out)
}

func reader603Visible(body string) string { return strings.Split(body, "<!-- invariant:")[0] }
func reader603AssertPost(t *testing.T, body string, p *formalize.Proposal) {
	t.Helper()
	visible := reader603Visible(body)
	lower := strings.ToLower(visible)
	for _, want := range []string{"```mermaid\nstateDiagram-v2", "Ready", "Working", "Done", "Continue", "Start again", "Internal poll", "Poll", "Limit = 2", "Already checked", "/invariant ratify " + strings.TrimPrefix(p.Hash, "sha256:")[:12]} {
		if !strings.Contains(visible, want) {
			t.Errorf("proposal is missing %q:\n%s", want, visible)
		}
	}
	for _, want := range []string{"unavailable", "hidden", "tlc", "states", "steps", "arrows", "bounds"} {
		if !strings.Contains(lower, want) {
			t.Errorf("reader report is missing %q", want)
		}
	}
	// Counts are named separately in the reader report, so a reader cannot
	// mistake TLC generation counts for concrete steps or displayed arrows.
	for _, count := range []struct{ name, value string }{{"TLC states", "3"}, {"TLC steps", "6"}, {"Generated states", "7"}, {"Chart states", "3"}, {"Visible arrows", "3"}, {"Hidden steps", "3"}} {
		if !regexp.MustCompile(`(?im)^\|\s*` + count.name + `\s*\|\s*` + count.value + `\s*\|`).MatchString(visible) {
			t.Errorf("reader count %s = %s is not shown", count.name, count.value)
		}
	}
	for _, state := range []struct{ label, action string }{{"Ready", "Reset"}, {"Working", "Reset"}, {"Done", "Advance"}} {
		if !regexp.MustCompile(`(?m)^.*` + state.label + `[^\n]*` + state.action + `.*$`).MatchString(visible) {
			t.Errorf("per-state unavailable action is missing: %+v", state)
		}
	}
	chart := reader603Saved(t, p).Chart
	for _, rule := range chart.Report.Rules {
		if !strings.Contains(html.UnescapeString(visible), rule.Says) {
			t.Errorf("rule wording changed: %q", rule.Says)
		}
	}
	m := regexp.MustCompile("(?s)```mermaid\\n(.*?)\\n```").FindStringSubmatch(visible)
	if len(m) != 2 {
		t.Fatalf("no complete Mermaid fence: %s", visible)
	}
	edges := regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9_]*|\[\*\])\s*-->\s*([A-Za-z][A-Za-z0-9_]*)`).FindAllStringSubmatch(m[1], -1)
	initials, arrows := 0, 0
	for _, edge := range edges {
		if edge[1] == "[*]" {
			initials++
		} else {
			arrows++
		}
	}
	if initials != len(chart.Initial) || arrows != len(chart.Arrows) {
		t.Errorf("Mermaid invented or omitted topology: %d initials %d arrows; want %d %d", initials, arrows, len(chart.Initial), len(chart.Arrows))
	}
	aliases := map[string]string{}
	for _, state := range regexp.MustCompile(`(?m)^\s*state "([^"\n]*)" as ([A-Za-z][A-Za-z0-9_]*)\s*$`).FindAllStringSubmatch(m[1], -1) {
		aliases[state[2]] = state[1]
	}
	labels := map[string]string{}
	for _, state := range chart.States {
		labels[state.ID] = state.Label
	}
	wantEdges, gotEdges := map[string]int{}, map[string]int{}
	for _, arrow := range chart.Arrows {
		wantEdges[labels[arrow.From]+"\x00"+arrow.Label+"\x00"+labels[arrow.To]]++
	}
	for _, arrow := range regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9_]*)\s*-->\s*([A-Za-z][A-Za-z0-9_]*)\s*:\s*([^\n]*)`).FindAllStringSubmatch(m[1], -1) {
		gotEdges[aliases[arrow[1]]+"\x00"+strings.TrimSpace(arrow[3])+"\x00"+aliases[arrow[2]]]++
	}
	if !reflect.DeepEqual(wantEdges, gotEdges) {
		t.Errorf("drawn transitions differ from computed transitions: %v; want %v", gotEdges, wantEdges)
	}
	for _, label := range []string{"Ready", "Working", "Done"} {
		if !regexp.MustCompile(`(?m)^.*` + label + `[^\n]*Poll.*$`).MatchString(visible) {
			t.Errorf("hidden Poll occurrence at %s is absent", label)
		}
	}
	if strings.Contains(m[1], "Internal poll") {
		t.Error("hidden arrows were drawn")
	}
	marker, ok := DecodeMarker(body)
	if !ok || marker.Proposal == nil || !reflect.DeepEqual(reader603Saved(t, marker.Proposal), reader603Saved(t, p)) {
		t.Fatal("post marker lost the computed snapshot")
	}
}

func TestReader603PostsRevisionAndRatification(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	p, report := reader603Checked(t, ws, tc)
	r := newRig(t)
	runs := 0
	r.f.Formalizer = reader603Formalizer{run: func(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
		if runs > 0 {
			if req.Previous == nil || !reflect.DeepEqual(reader603Saved(t, req.Previous), reader603Saved(t, p)) {
				t.Fatal("revision lost previous chart")
			}
			reader603Comparison(t, req, p)
		}
		runs++
		return &formalize.Result{Proposal: p, Report: report}, nil
	}}
	r.gh.open(603, "gitdek", "Reader chart", "A lifecycle.\n\n/invariant solve")
	r.poll()
	first := r.expect(603, KindProposal, LabelProposal)
	reader603AssertPost(t, first.Comment.Body, p)
	r.gh.say(603, "gitdek", "Keep these rules.\n/invariant revise")
	r.poll()
	revised := r.expect(603, KindProposal, LabelProposal)
	reader603AssertPost(t, revised.Comment.Body, p)
	if runs != 2 {
		t.Fatalf("revision runs=%d", runs)
	}
	r.gh.say(603, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Hash, "sha256:"))
	r.poll()
	post := r.expect(603, KindPR, LabelPR)
	ref := post.Marker.Branch + ":" + post.Marker.Project + "/"
	if err := r.f.Repo.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	// git's test helper trims trailing whitespace; compare raw blob bytes via Show.
	for name, want := range map[string]string{".invariant/view.json": reader603View, ".invariant/specs/Helper603.tla": reader603Include} {
		b, err := r.f.Repo.Show(t.Context(), "origin/"+post.Marker.Branch, post.Marker.Project+"/"+name)
		if err != nil || string(b) != want {
			t.Errorf("ratified %s=%q %v", name, b, err)
		}
	}
	var saved reader603Snapshot
	if err := json.Unmarshal([]byte(git(t, r.origin, "show", ref+".invariant/reader.json")), &saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, reader603Saved(t, p)) {
		t.Fatal("ratification replaced computed snapshot")
	}
	var lock project.Lock
	if err := json.Unmarshal([]byte(git(t, r.origin, "show", ref+".invariant/ratified.lock")), &lock); err != nil {
		t.Fatal(err)
	}
	if project.ProposalHash(lock.Bounds, lock.Statements) != p.Hash || lock.Ratified.Proposal != p.Hash {
		t.Fatal("chart changed ratification identity")
	}
}

func TestReader603AmendmentAndCurrent(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	reader603Saved(t, old)
	r := newRig(t)
	dir := "examples/reader-lifecycle"
	work := t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{By: "gitdek", Issue: 600, Proposal: old.Hash}); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed reader")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	current, err := r.f.current(t.Context(), dir)
	if err != nil || current == nil {
		t.Fatalf("current: %+v %v", current, err)
	}
	wire := reader603Object(t, current)
	// Current's JSON uses either exported Go names or explicit lowercase tags.
	lookup := func(key string) any {
		for k, v := range wire {
			if strings.EqualFold(k, key) {
				return v
			}
		}
		return nil
	}
	if lookup("view") != reader603View {
		t.Fatal("current project lost saved view")
	}
	includes, ok := lookup("includes").(map[string]any)
	if !ok || includes["Helper603.tla"] != reader603Include {
		t.Fatal("current project lost include bytes")
	}
	reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, "The count stays within its limit.", "The count remains within its limit.", 1))
	p, report := reader603Checked(t, ws, tc)
	if err := p.Amend(current); err != nil {
		t.Fatal(err)
	}
	changes := formalize.Diff(current, p)
	r.f.Formalizer = reader603Formalizer{run: func(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
		if req.Current == nil {
			t.Fatal("amendment not staged as current project")
		}
		reader603Comparison(t, req, p)
		changes = formalize.Diff(current, p)
		return &formalize.Result{Proposal: p, Report: report, Changes: changes}, nil
	}}
	r.gh.open(603, "gitdek", "Clarify the count", "Project: "+dir+"\n\n/invariant solve")
	r.poll()
	post := r.expect(603, KindProposal, LabelProposal)
	reader603AssertPost(t, post.Comment.Body, p)
	for _, want := range []string{"amendment for ratification", "It amends the statements ratified in #600", "**This changes the invariant `Safe`.**", "| Statement | Change | Says |"} {
		if !strings.Contains(post.Comment.Body, want) {
			t.Errorf("amendment lost %q", want)
		}
	}
	if p.Target.Amends != old.Hash || p.Hash != project.ProposalHash(p.Bounds, p.Statements) {
		t.Fatal("amendment identity changed")
	}
	out := t.TempDir()
	if err := p.Write(out, "amended", &project.Ratification{By: "gitdek", Issue: 603, Proposal: p.Hash, Amends: p.Target.Amends}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, ".invariant/view.json"))
	if err != nil || string(b) != reader603View {
		t.Fatal("amendment write lost view")
	}
}

func TestReader603RecoveryAndRefusal(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	p, report := reader603Checked(t, ws, tc)
	reader603Saved(t, p)
	for _, recovered := range []bool{false, true} {
		for _, kind := range []string{"valid", "missing", "changed view", "changed model", "changed include", "changed bounds", "changed chart"} {
			t.Run(fmt.Sprintf("%s recovered=%v", kind, recovered), func(t *testing.T) {
				q := reader603Copy(t, p, func(m map[string]any) {
					switch kind {
					case "missing":
						delete(m, "reader")
						delete(m, "view")
					case "changed view":
						m["view"] = reader603View + " "
					case "changed model":
						m["module_text"] = reader603Model + "\\* changed\n"
					case "changed include":
						m["includes"].(map[string]any)["Helper603.tla"] = reader603Include + "\\* changed\n"
					case "changed bounds":
						m["bounds"].(map[string]any)["Limit"] = "4"
					case "changed chart":
						m["reader"].(map[string]any)["chart"].(map[string]any)["arrows"] = []any{}
					}
				})
				r := newRig(t)
				calls := 0
				res := &formalize.Result{Proposal: q, Report: report}
				form := reader603Formalizer{run: func(context.Context, formalize.Request, string) (*formalize.Result, error) { calls++; return res, nil }}
				r.f.Formalizer = form
				r.gh.open(603, "gitdek", "Reader recovery", "A lifecycle.\n\n/invariant solve")
				if recovered {
					commit, err := r.f.saveResult(t.Context(), res, "saved reader draft", "")
					if err != nil {
						t.Fatal(err)
					}
					if err := r.f.Repo.Record(t.Context(), 603, "draft-issue", "draft before restart"); err != nil {
						t.Fatal(err)
					}
					if err := r.f.Repo.Finish(t.Context(), 603, "draft-issue", commit); err != nil {
						t.Fatal(err)
					}
					r.f = &Factory{Repository: "o/r", GitHub: r.gh, Repo: r.f.Repo, Formalizer: form, Builder: r.build, Base: "main", Projects: "examples", Work: t.TempDir(), Check: "invariant/gate", Log: t.Logf}
				}
				r.poll()
				if recovered && calls != 0 {
					t.Fatal("recovery ran another agent")
				}
				if !recovered && calls != 1 {
					t.Fatalf("fresh calls=%d", calls)
				}
				if kind == "valid" {
					post := r.expect(603, KindProposal, LabelProposal)
					reader603AssertPost(t, post.Comment.Body, q)
					return
				}
				post := r.expect(603, KindStuck, LabelHumanReview)
				body := reader603Visible(post.Comment.Body)
				if strings.Contains(body, "/invariant ratify ") || strings.Contains(body, "```mermaid") {
					t.Fatal("unusable evidence offered for ratification")
				}
				lower := strings.ToLower(body)
				if !strings.Contains(lower, "reader") && !strings.Contains(lower, "view") && !strings.Contains(lower, "statechart") {
					t.Fatalf("refusal not actionable: %s", body)
				}
			})
		}
	}
}

func TestReader603LiteralRenderingAndKinds(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	const label = "Ready \"\nEVIL --> X: forged\n```\n<script>alert(1)</script> | & #quot;"
	const says = "Keep | `ticks` & <script>alert(1)</script> literal.\n```\nDo not execute."
	var v map[string]any
	if err := json.Unmarshal([]byte(reader603View), &v); err != nil {
		t.Fatal(err)
	}
	v["state"].([]any)[0].(map[string]any)["label"] = label
	v["step"].([]any)[1].(map[string]any)["label"] = "Continue \" : [*] --> EVIL\n```mermaid"
	reader603Write(t, filepath.Join(ws, "view.json"), string(reader603JSON(t, v)))
	var draft map[string]any
	if err := json.Unmarshal([]byte(reader603Draft), &draft); err != nil {
		t.Fatal(err)
	}
	draft["statements"].([]any)[1].(map[string]any)["says"] = says
	reader603Write(t, filepath.Join(ws, "proposal.json"), string(reader603JSON(t, draft)))
	p, report := reader603Checked(t, ws, tc)
	saved := reader603Saved(t, p)
	if saved.Chart.Report.Rules[1].Says != says {
		t.Fatal("lock wording normalized")
	}
	body := proposalComment(p, report, Marker{Kind: KindProposal, Proposal: p})
	visible := reader603Visible(body)
	if strings.Count(visible, "```mermaid") != 1 || strings.Contains(visible, "<script>") || strings.Contains(visible, "\nEVIL -->") || strings.Contains(visible, "Keep | `ticks`") {
		t.Fatalf("literal content became markup or topology: %s", visible)
	}
	if !strings.Contains(html.UnescapeString(visible), says) {
		t.Fatal("literal escaping changed the rule's words")
	}
	mermaid := regexp.MustCompile("(?s)```mermaid\\n(.*?)\\n```").FindStringSubmatch(visible)
	if len(mermaid) != 2 || !strings.HasPrefix(mermaid[1], "stateDiagram-v2") {
		t.Fatal("literal content broke the chart fence")
	}
	// Mermaid's entity syntax is decoded once, just as literal labels are.
	// In particular an input #quot; must remain those six literal characters.
	decode := func(s string) string {
		return regexp.MustCompile(`#([0-9]+|quot|amp|lt|gt|apos);`).ReplaceAllStringFunc(s, func(entity string) string {
			name := strings.TrimSuffix(strings.TrimPrefix(entity, "#"), ";")
			if n, err := strconv.Atoi(name); err == nil {
				return string(rune(n))
			}
			return map[string]string{"quot": `"`, "amp": "&", "lt": "<", "gt": ">", "apos": "'"}[name]
		})
	}
	found := false
	for _, state := range regexp.MustCompile(`(?m)^\s*state "([^"\n]*)" as [A-Za-z][A-Za-z0-9_]*\s*$`).FindAllStringSubmatch(mermaid[1], -1) {
		found = found || decode(state[1]) == label
	}
	if !found {
		t.Fatal("the reader's literal label was lost or interpreted as Mermaid syntax")
	}
	if got := len(regexp.MustCompile(`(?m)^\s*(?:[A-Za-z][A-Za-z0-9_]*|\[\*\])\s*-->`).FindAllString(mermaid[1], -1)); got != 4 {
		t.Fatalf("injected/missing arrows: %d", got)
	}
	decoded, ok := DecodeMarker(body)
	if !ok || reader603Object(t, decoded.Proposal)["view"] != reader603Object(t, p)["view"] {
		t.Fatal("escaping changed saved view")
	}
	legacy := reader603Copy(t, p, func(m map[string]any) { delete(m, "reader"); delete(m, "view"); delete(m, "includes") })
	old, ok := DecodeMarker(post("legacy proposal", "old", Marker{Kind: KindProposal, Proposal: legacy}))
	if !ok || old.Proposal.Hash != p.Hash {
		t.Fatal("old proposal no longer readable")
	}
	plan := &plumbing.Plan{Name: "Plumbing", Summary: "Test it.", Files: []string{"x.go"}, Tests: []plumbing.Test{{Name: "TestX", File: "x_accept_test.go", Says: "It works."}}, Sources: map[string]string{"x_accept_test.go": "package x\n"}}
	pp := &formalize.Proposal{Plan: plan, Hash: plan.Hash()}
	pp.Name = plan.Name
	ip := &formalize.IssuePlan{Name: "Steps", Summary: "Two steps.", Issues: []formalize.PlannedIssue{{Title: "One", Body: "Kind: plumbing\nDo one."}, {Title: "Two", Body: "Kind: plumbing\nDo two."}}}
	pi := &formalize.Proposal{IssuePlan: ip, Hash: ip.Hash()}
	pi.Name = ip.Name
	for _, text := range []string{planComment(pp, Marker{Kind: KindProposal, Proposal: pp}), issuePlanComment(pi, Marker{Kind: KindProposal, Proposal: pi})} {
		if strings.Contains(text, "stateDiagram-v2") || strings.Contains(text, "```mermaid") {
			t.Fatal("non-modeled proposal acquired a chart")
		}
	}
}

func TestReader603CommentCapacity(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	// Incompressible text also makes the saved marker too large. The refusal
	// must not carry that oversized payload into its own comment.
	var large strings.Builder
	for i := range 1600 {
		sum := sha256.Sum256([]byte(fmt.Sprint(i)))
		fmt.Fprintf(&large, "%x", sum)
	}
	reader603Write(t, filepath.Join(ws, "view.json"), strings.Replace(reader603View, "Working", large.String(), 1))
	p, report := reader603Checked(t, ws, tc)
	reader603Saved(t, p)
	r := newRig(t)
	r.f.Formalizer = reader603Formalizer{run: func(context.Context, formalize.Request, string) (*formalize.Result, error) {
		return &formalize.Result{Proposal: p, Report: report}, nil
	}}
	r.gh.open(603, "gitdek", "Reader size", "A lifecycle.\n\n/invariant solve")
	r.poll()
	post := r.expect(603, KindStuck, LabelHumanReview)
	body := strings.ToLower(reader603Visible(post.Comment.Body))
	if !strings.Contains(body, "comment") || (!strings.Contains(body, "limit") && !strings.Contains(body, "large") && !strings.Contains(body, "size")) {
		t.Fatalf("missing explicit publication capacity: %s", body)
	}
	if len(post.Comment.Body) > maxComment || strings.Contains(body, "/invariant ratify ") {
		t.Fatal("truncated or oversized proposal remained usable")
	}
}

func reader603AssertLimitedPost(t *testing.T, body string, p *formalize.Proposal) {
	t.Helper()
	visible := reader603Visible(body)
	lower := strings.ToLower(visible)
	for _, want := range []string{"501", "500", "tlc", "states", "bounds", "chart", "already checked", "limit = 500", "/invariant ratify " + strings.TrimPrefix(p.Hash, "sha256:")[:12]} {
		if !strings.Contains(lower, want) {
			t.Errorf("size-limited proposal omits %q: %s", want, visible)
		}
	}
	if !strings.Contains(lower, "not drawn") && !strings.Contains(lower, "isn't drawn") {
		t.Errorf("no explicit chart omission note: %s", visible)
	}
	if !strings.Contains(lower, "more than") && !strings.Contains(lower, "exceed") {
		t.Errorf("note does not explain the capacity: %s", visible)
	}
	if strings.Contains(visible, "```mermaid") || strings.Contains(visible, "stateDiagram-v2") {
		t.Fatal("large proposal fabricated a chart")
	}
	if !strings.Contains(visible, "| Statement |") {
		t.Fatal("size note replaced the statement table")
	}
	if !strings.Contains(html.UnescapeString(visible), p.Statements[1].Says) {
		t.Fatal("size note lost rule wording")
	}
	marker, ok := DecodeMarker(body)
	if !ok || marker.Proposal == nil || !reflect.DeepEqual(reader603Limited(t, marker.Proposal, 501), reader603Limited(t, p, 501)) {
		t.Fatal("marker lost size limitation")
	}
}

func TestReader603LargePublicationAndRecovery(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	largeDraft := strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"500"`, 1)
	reader603Write(t, filepath.Join(ws, "proposal.json"), largeDraft)
	p, report := reader603Checked(t, ws, tc)
	reader603Limited(t, p, 501)
	for _, mode := range []string{"proposal", "revision", "amendment", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			r := newRig(t)
			q := reader603Copy(t, p, func(map[string]any) {})
			result := &formalize.Result{Proposal: q, Report: report}
			body := "A large lifecycle.\n\n/invariant solve"
			if mode == "amendment" {
				dir := "examples/reader-lifecycle"
				work := t.TempDir()
				git(t, work, "clone", "--quiet", r.origin, ".")
				if err := p.Write(filepath.Join(work, dir), "original", &project.Ratification{By: "gitdek", Issue: 600, Proposal: p.Hash}); err != nil {
					t.Fatal(err)
				}
				git(t, work, "add", "-A")
				git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed large reader")
				git(t, work, "push", "--quiet", "origin", "HEAD:main")
				current, err := r.f.current(t.Context(), dir)
				if err != nil || current == nil {
					t.Fatalf("current: %+v %v", current, err)
				}
				var restored struct {
					View     string             `json:"view"`
					Includes map[string]string  `json:"includes"`
					Reader   *reader603Snapshot `json:"reader"`
				}
				if err := json.Unmarshal(reader603JSON(t, current), &restored); err != nil {
					t.Fatal(err)
				}
				if restored.View != reader603View || restored.Includes["Helper603.tla"] != reader603Include || restored.Reader == nil || !reflect.DeepEqual(*restored.Reader, reader603Limited(t, p, 501)) {
					t.Fatal("current project lost size limitation or inputs")
				}
				fresh := t.TempDir()
				reader603Workspace(t, fresh)
				reader603Write(t, filepath.Join(fresh, "proposal.json"), strings.Replace(largeDraft, "The count stays within its limit.", "The count remains within its limit.", 1))
				q, result.Report = reader603Checked(t, fresh, tc)
				if err := q.Amend(current); err != nil {
					t.Fatal(err)
				}
				result.Proposal, result.Changes = q, formalize.Diff(current, q)
				body = "Project: " + dir + "\n\n/invariant solve"
			}
			calls := 0
			form := reader603Formalizer{run: func(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
				if mode == "revision" && calls > 0 {
					if req.Previous == nil || !reflect.DeepEqual(reader603Limited(t, req.Previous, 501), reader603Limited(t, q, 501)) {
						t.Fatal("revision lost previous limitation")
					}
				}
				if mode == "amendment" && req.Current == nil {
					t.Fatal("amendment did not load current project")
				}
				reader603Comparison(t, req, q)
				if req.Current != nil {
					result.Changes = formalize.Diff(req.Current, q)
				}
				calls++
				return result, nil
			}}
			r.f.Formalizer = form
			r.gh.open(603, "gitdek", "Large reader", body)
			if mode == "recovery" {
				commit, err := r.f.saveResult(t.Context(), result, "saved size limitation", "")
				if err != nil {
					t.Fatal(err)
				}
				if err := r.f.Repo.Record(t.Context(), 603, "draft-issue", "draft before restart"); err != nil {
					t.Fatal(err)
				}
				if err := r.f.Repo.Finish(t.Context(), 603, "draft-issue", commit); err != nil {
					t.Fatal(err)
				}
				r.f = &Factory{Repository: "o/r", GitHub: r.gh, Repo: r.f.Repo, Formalizer: form, Builder: r.build, Base: "main", Projects: "examples", Work: t.TempDir(), Check: "invariant/gate", Log: t.Logf}
			}
			r.poll()
			post := r.expect(603, KindProposal, LabelProposal)
			reader603AssertLimitedPost(t, post.Comment.Body, q)
			if mode == "revision" {
				r.gh.say(603, "gitdek", "Keep these bounds.\n/invariant revise")
				r.poll()
				post = r.expect(603, KindProposal, LabelProposal)
				reader603AssertLimitedPost(t, post.Comment.Body, q)
			}
			wantCalls := 1
			if mode == "recovery" {
				wantCalls = 0
			}
			if mode == "revision" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("agent runs=%d, want %d", calls, wantCalls)
			}
			if mode == "amendment" {
				for _, want := range []string{"amendment for ratification", "It amends the statements ratified in #600", "**This changes the invariant `Safe`.**", "| Statement | Change | Says |"} {
					if !strings.Contains(post.Comment.Body, want) {
						t.Errorf("large amendment lost %q", want)
					}
				}
				if q.Target.Amends != p.Hash {
					t.Fatal("size limitation changed amendment identity")
				}
				out := t.TempDir()
				if err := q.Write(out, "amended", &project.Ratification{By: "gitdek", Issue: 603, Proposal: q.Hash, Amends: q.Target.Amends}); err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(filepath.Join(out, ".invariant/reader.json"))
				if err != nil {
					t.Fatal(err)
				}
				var saved reader603Snapshot
				if err := json.Unmarshal(b, &saved); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(saved, reader603Limited(t, q, 501)) {
					t.Fatal("amendment write lost limitation")
				}
				return
			}
			r.gh.say(603, "gitdek", "/invariant ratify "+strings.TrimPrefix(q.Hash, "sha256:"))
			r.poll()
			built := r.expect(603, KindPR, LabelPR)
			if err := r.f.Repo.Fetch(t.Context()); err != nil {
				t.Fatal(err)
			}
			read := func(path string) []byte {
				b, err := r.f.Repo.Show(t.Context(), "origin/"+built.Marker.Branch, built.Marker.Project+"/"+path)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			var saved reader603Snapshot
			if err := json.Unmarshal(read(".invariant/reader.json"), &saved); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(saved, reader603Limited(t, q, 501)) {
				t.Fatal("ratification lost size limitation")
			}
			if string(read(".invariant/view.json")) != reader603View || string(read(".invariant/specs/Helper603.tla")) != reader603Include {
				t.Fatal("ratification lost view or includes")
			}
			var lock project.Lock
			if err := json.Unmarshal(read(".invariant/ratified.lock"), &lock); err != nil {
				t.Fatal(err)
			}
			if project.ProposalHash(lock.Bounds, lock.Statements) != q.Hash || lock.Ratified.Proposal != q.Hash {
				t.Fatal("limitation changed ratification identity")
			}
		})
	}
	for _, recovered := range []bool{false, true} {
		for _, kind := range []string{"missing reader", "missing view", "view", "model", "include", "bounds", "count", "tool"} {
			t.Run(fmt.Sprintf("refuse %s recovered=%v", kind, recovered), func(t *testing.T) {
				q := reader603Copy(t, p, func(m map[string]any) {
					switch kind {
					case "missing reader":
						delete(m, "reader")
					case "missing view":
						delete(m, "view")
					case "view":
						m["view"] = reader603View + " "
					case "model":
						m["module_text"] = reader603Model + "\\* changed\n"
					case "include":
						m["includes"].(map[string]any)["Helper603.tla"] = reader603Include + "\\* changed\n"
					case "bounds":
						m["bounds"].(map[string]any)["Limit"] = "501"
					case "count":
						m["reader"].(map[string]any)["limitation"].(map[string]any)["actual"] = 900
					case "tool":
						m["reader"].(map[string]any)["limitation"].(map[string]any)["source"].(map[string]any)["tool"].(map[string]any)["Image"] = "changed"
					}
				})
				r := newRig(t)
				calls := 0
				result := &formalize.Result{Proposal: q, Report: report}
				form := reader603Formalizer{run: func(context.Context, formalize.Request, string) (*formalize.Result, error) {
					calls++
					return result, nil
				}}
				r.f.Formalizer = form
				r.gh.open(603, "gitdek", "Stale large reader", "A large lifecycle.\n\n/invariant solve")
				if recovered {
					commit, err := r.f.saveResult(t.Context(), result, "saved stale limitation", "")
					if err != nil {
						t.Fatal(err)
					}
					if err := r.f.Repo.Record(t.Context(), 603, "draft-issue", "draft before restart"); err != nil {
						t.Fatal(err)
					}
					if err := r.f.Repo.Finish(t.Context(), 603, "draft-issue", commit); err != nil {
						t.Fatal(err)
					}
					r.f = &Factory{Repository: "o/r", GitHub: r.gh, Repo: r.f.Repo, Formalizer: form, Builder: r.build, Base: "main", Projects: "examples", Work: t.TempDir(), Check: "invariant/gate", Log: t.Logf}
				}
				r.poll()
				if (recovered && calls != 0) || (!recovered && calls != 1) {
					t.Fatalf("unexpected agent runs: %d", calls)
				}
				post := r.expect(603, KindStuck, LabelHumanReview)
				visible := reader603Visible(post.Comment.Body)
				if strings.Contains(visible, "/invariant ratify ") || strings.Contains(visible, "```mermaid") {
					t.Fatal("stale limitation offered for ratification")
				}
				lower := strings.ToLower(visible)
				if !strings.Contains(lower, "reader") && !strings.Contains(lower, "view") && !strings.Contains(lower, "statechart") {
					t.Fatalf("unhelpful refusal: %s", visible)
				}
			})
		}
	}
}

// These publication doubles now supply the same captured comparison evidence
// as a real revision or amendment, while retaining their original chart checks.
func reader603Comparison(t *testing.T, req formalize.Request, p *formalize.Proposal) {
	t.Helper()
	s := req.CheckContext().Baseline
	if s == nil {
		return
	}
	if s.Chart == nil && s.Limitation == nil {
		if req.Current == nil || req.Current.Reader == nil {
			t.Fatal("fixture baseline has no checked reader")
		}
		s.Chart, s.Limitation = req.Current.Reader.Chart, req.Current.Reader.Limitation
	}
	attachFactoryComparison(t, p, *s)
}
