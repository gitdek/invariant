package formalize_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
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

func TestReader603CheckFeedbackAndRepair(t *testing.T) {
	tc, log := reader603Tools(t)
	cases := []struct {
		name, view string
		wants      []string
	}{
		{"missing", "", []string{"view.json"}},
		{"invalid", `{"version":1,"state":[],"step":[]}`, []string{"invalid_view"}},
		{"unmapped", strings.ReplaceAll(reader603View, `"when":true`, `"when":false`), []string{"unmapped_state", "3"}},
		{"disconnect", strings.Replace(reader603View, `"label":"Continue"`, `"label":"Continue","hide":true`, 1), []string{"unreachable_chart", "advance", "Advance"}},
		{"no outgoing", strings.Replace(reader603View, `"label":"Start again"`, `"label":"Start again","hide":true`, 1), []string{"empty_outgoing", "reset", "Reset"}},
		{"partial", `{"version":1,"state":[{"id":"all","when":true,"label":"All"}],"step":[{"id":"poll","when":{"eq":[{"action":true},"Poll"]},"label":"Step","hide":true},{"id":"other","when":true,"label":"Step"}]}`, []string{"partial_arrow", "poll", "Poll"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ws := t.TempDir()
			reader603Workspace(t, ws)
			if c.view == "" {
				if err := os.Remove(filepath.Join(ws, "view.json")); err != nil {
					t.Fatal(err)
				}
			} else {
				reader603Write(t, filepath.Join(ws, "view.json"), c.view)
			}
			p, _, err := formalize.Check(t.Context(), ws, tc)
			if err == nil {
				t.Fatalf("bad view accepted: %+v", p)
			}
			for _, want := range c.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("repair feedback lacks %q: %v", want, err)
				}
			}
			if p != nil && reader603Object(t, p)["reader"] != nil {
				t.Fatal("refusal retained a usable chart")
			}
			reader603Write(t, filepath.Join(ws, "view.json"), reader603View)
			p, r := reader603Checked(t, ws, tc)
			s := reader603Saved(t, p)
			chart := s.Chart
			if !r.ModelOnly || r.Design.DistinctStates != 3 {
				t.Fatalf("ordinary model gate changed: %+v", r)
			}
			for k, want := range map[string]int64{"tlc_states": 3, "tlc_steps": 6, "tlc_generated_states": 7, "chart_states": 3, "chart_arrows": 3, "shown_steps": 3, "hidden_steps": 3} {
				if chart.Report.Counts[k] != want {
					t.Errorf("%s=%d, want %d", k, chart.Report.Counts[k], want)
				}
			}
			if len(chart.Initial) != 1 || len(chart.Report.Hidden) != 3 || !reflect.DeepEqual(chart.Report.Rules, p.Statements) {
				t.Fatalf("chart lost initial, hidden or exact rule evidence: %+v", chart)
			}
			for _, state := range chart.States {
				want := "Reset"
				if state.Label == "Done" {
					want = "Advance"
				}
				if !reflect.DeepEqual(state.UnavailableActions, []string{want}) || len(state.ViolatedRules) != 0 {
					t.Errorf("availability or rules for %s: %+v", state.Label, state)
				}
			}
			sum := sha256.Sum256([]byte("fixture TLC jar 603"))
			if chart.Report.Source.Tool.Image != tc.JavaImage || chart.Report.Source.Tool.JarSHA256 != fmt.Sprintf("%x", sum) || chart.Report.Source.Config.Constants["Limit"] != "2" {
				t.Fatalf("wrong provenance: %+v", chart.Report.Source)
			}
			if p.Hash != project.ProposalHash(p.Bounds, p.Statements) {
				t.Fatal("reader explanation changed the ratification hash")
			}
		})
	}
	var mainDump, replay bool
	for _, call := range reader603Calls(t, log) {
		if call["main"] != reader603Model || call["include"] != reader603Include {
			t.Fatalf("check used different source bytes: %+v", call)
		}
		if call["dump"] != "" {
			if call["module"] == "Reader603" {
				mainDump = true
			} else {
				replay = true
			}
		}
	}
	if !mainDump || !replay {
		t.Fatal("chart was not independently derived through graph dumping and TLC invariant replay")
	}
}

func TestReader603Capacity(t *testing.T) {
	tc, _ := reader603Tools(t)
	for _, n := range []int{500, 501} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			ws := t.TempDir()
			reader603Workspace(t, ws)
			reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, fmt.Sprintf(`"Limit":"%d"`, n-1), 1))
			// No hiding; all concrete states remain accounted for even when labels merge.
			reader603Write(t, filepath.Join(ws, "view.json"), strings.Replace(reader603View, `"hide":true`, `"hide":false`, 1))
			p, r, err := formalize.Check(t.Context(), ws, tc)
			if err != nil || p == nil || r == nil || !r.Passed || !r.ModelOnly || r.Design.DistinctStates != int64(n) {
				t.Fatalf("%d states should pass: %+v %+v %v", n, p, r, err)
			}
			if n == 501 {
				s := reader603Limited(t, p, n)
				sum := sha256.Sum256([]byte("fixture TLC jar 603"))
				if s.Limitation.Source.Tool.Image != tc.JavaImage || s.Limitation.Source.Tool.JarSHA256 != fmt.Sprintf("%x", sum) || !p.Ratifiable() || p.Hash != project.ProposalHash(p.Bounds, p.Statements) {
					t.Fatalf("size limitation changed tool provenance or ratification: %+v", s)
				}
				return
			}
			s := reader603Saved(t, p)
			if s.Chart.Report.Counts["tlc_states"] != 500 || s.Chart.Report.Counts["tlc_steps"] != 1000 {
				t.Fatalf("500-state graph sampled: %+v", s.Chart.Report)
			}
		})
	}
	for _, c := range []struct {
		name, view, refusal string
	}{
		{"missing", "", "view.json"},
		{"malformed", `{`, "invalid_view"},
		{"empty mappings", `{"version":1,"state":[],"step":[]}`, "invalid_view"},
		{"duplicate key", strings.Replace(reader603View, `"version":1`, `"version":1,"version":1`, 1), "invalid_view"},
		{"static type", strings.Replace(reader603View, `"0"`, `true`, 1), "invalid_view"},
		{"foreign graph", strings.Replace(reader603View, `"version":1`, `"version":1,"graph":{}`, 1), "invalid_view"},
		// These views are well-formed but would fail if evaluated on the graph.
		{"lookup not evaluated", strings.ReplaceAll(reader603View, `"var":"n"`, `"var":"absent"`), ""},
		{"totality not evaluated", strings.ReplaceAll(reader603View, `"when":true`, `"when":false`), ""},
		{"hiding not evaluated", strings.Replace(reader603View, `"label":"Continue"`, `"label":"Continue","hide":true`, 1), ""},
	} {
		t.Run("large view/"+c.name, func(t *testing.T) {
			ws := t.TempDir()
			reader603Workspace(t, ws)
			reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"500"`, 1))
			if c.view == "" {
				if err := os.Remove(filepath.Join(ws, "view.json")); err != nil {
					t.Fatal(err)
				}
			} else {
				reader603Write(t, filepath.Join(ws, "view.json"), c.view)
			}
			p, r, err := formalize.Check(t.Context(), ws, tc)
			if c.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), c.refusal) {
					t.Fatalf("invalid large view accepted or unhelpful feedback: %+v %v", r, err)
				}
				if p != nil && reader603Object(t, p)["reader"] != nil {
					t.Fatal("invalid view acquired reader evidence")
				}
				return
			}
			if err != nil || r == nil || !r.Passed {
				t.Fatalf("well-formed large view was evaluated or refused: %+v %v", r, err)
			}
			reader603Limited(t, p, 501)
		})
	}
	t.Run("ordinary model checks still fail", func(t *testing.T) {
		ws := t.TempDir()
		reader603Workspace(t, ws)
		reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"500"`, 1))
		reader603Write(t, filepath.Join(ws, "Reader603.tla"), strings.Replace(reader603Model, `Safe == n \in 0..Limit`, `Safe == FALSE`, 1))
		p, r, err := formalize.Check(t.Context(), ws, tc)
		if err != nil || r == nil || r.Passed || !strings.Contains(verify.Feedback(r), "Invariant Safe is violated") {
			t.Fatalf("size limitation hid a failing model gate: %+v %v", r, err)
		}
		if p != nil && reader603Object(t, p)["reader"] != nil {
			t.Fatal("failed model acquired successful reader evidence")
		}
	})
}

type reader603Agent struct {
	run func(context.Context, synth.Job) (synth.Usage, error)
}

func (reader603Agent) Name() string { return "reader-fixture" }
func (a reader603Agent) Run(ctx context.Context, j synth.Job) (synth.Usage, error) {
	return a.run(ctx, j)
}

func TestReader603IndependentFinalCheck(t *testing.T) {
	tc, log := reader603Tools(t)
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			var first reader603Snapshot
			finalView := strings.Replace(reader603View, "Working", "Final working", 1)
			finalModel := reader603Model + "\\* final model bytes\n"
			finalInclude := reader603Include + "\\* final include bytes\n"
			a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
				reader603Workspace(t, j.Workspace)
				p, _ := reader603Checked(t, j.Workspace, tc)
				first = reader603Saved(t, p)
				// Plant stale derived evidence in both plausible locations. The final
				// check reads draft inputs only, never this agent-supplied authority.
				wire := reader603Object(t, p)
				wire["bounds"].(map[string]any)["Limit"] = "3"
				reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), string(reader603JSON(t, wire)))
				reader603Write(t, filepath.Join(j.Workspace, "formalization.json"), string(reader603JSON(t, formalize.Result{Proposal: p})))
				reader603Write(t, filepath.Join(j.Workspace, "reader.json"), string(reader603JSON(t, first)))
				reader603Write(t, filepath.Join(j.Workspace, "Reader603.tla"), finalModel)
				reader603Write(t, filepath.Join(j.Workspace, "Helper603.tla"), finalInclude)
				view := finalView
				if bad {
					view = strings.Replace(finalView, `"label":"Continue"`, `"label":"Continue","hide":true`, 1)
				}
				reader603Write(t, filepath.Join(j.Workspace, "view.json"), view)
				return synth.Usage{}, nil
			}}
			out := t.TempDir()
			res, err := (formalize.Formalizer{Backend: a, Toolchain: tc, CheckRuns: 3}).Formalize(t.Context(), formalize.Request{Repo: "o/r", Issue: 603, Title: "Reader chart"}, out)
			if err != nil || res == nil {
				t.Fatalf("formalizer: %+v %v", res, err)
			}
			if bad {
				if !strings.Contains(res.Problem, "unreachable_chart") {
					t.Fatalf("stale successful check escaped final validation: %+v", res)
				}
				if res.Proposal != nil && reader603Object(t, res.Proposal)["reader"] != nil {
					t.Fatal("invalid final view retained old chart")
				}
				return
			}
			if res.Problem != "" || res.Report == nil || !res.Report.Passed {
				t.Fatalf("valid final draft refused: %+v", res)
			}
			saved := reader603Saved(t, res.Proposal)
			if saved.Fingerprint == first.Fingerprint || saved.Chart.Report.Counts["tlc_states"] != 4 || saved.Chart.Report.Source.Config.Constants["Limit"] != "3" {
				t.Fatalf("final check reused old evidence: %+v", saved)
			}
			wire := reader603Object(t, res.Proposal)
			if res.Proposal.ModuleText != finalModel || wire["view"] != finalView || wire["includes"].(map[string]any)["Helper603.tla"] != finalInclude {
				t.Fatal("final snapshot not made from final files")
			}
			var restored formalize.Result
			b, err := os.ReadFile(filepath.Join(out, "formalization.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b, &restored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(reader603Saved(t, restored.Proposal), saved) {
				t.Fatal("formalization.json lost checked evidence")
			}
			for name, want := range map[string]string{"view.json": finalView, "Reader603.tla": finalModel, "Helper603.tla": finalInclude} {
				b, err := os.ReadFile(filepath.Join(out, name))
				if err != nil || string(b) != want {
					t.Errorf("saved %s = %q, %v", name, b, err)
				}
			}
		})
	}
	var finalDump bool
	for _, call := range reader603Calls(t, log) {
		if call["module"] == "Reader603" && call["dump"] != "" && strings.Contains(call["config"], "Limit = 3") && strings.Contains(call["include"], "final include bytes") {
			finalDump = true
		}
	}
	if !finalDump {
		t.Fatal("independent final graph run was missing")
	}
}

func TestReader603PersistenceAndStaging(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	p, _ := reader603Checked(t, ws, tc)
	reader603Saved(t, p)
	reader603PersistenceAndStaging(t, tc, p)
}

func reader603PersistenceAndStaging(t *testing.T, tc toolchain.Toolchain, p *formalize.Proposal) {
	t.Helper()
	saved := reader603Evidence(t, p)
	var restored formalize.Result
	if err := json.Unmarshal(reader603JSON(t, formalize.Result{Proposal: p}), &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reader603Object(t, p), reader603Object(t, restored.Proposal)) {
		t.Fatal("saved proposal lost inputs")
	}
	dir := t.TempDir()
	rat := &project.Ratification{By: "owner", Issue: 603, Proposal: p.Hash}
	if err := restored.Proposal.Write(dir, "request", rat); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{".invariant/view.json": reader603View, ".invariant/specs/Helper603.tla": reader603Include, ".invariant/specs/Reader603.tla": reader603Model} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != want {
			t.Errorf("written %s=%q, %v", name, b, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, ".invariant/reader.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk reader603Snapshot
	if err := json.Unmarshal(b, &onDisk); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(onDisk, saved) {
		t.Fatal("project snapshot changed")
	}
	loaded, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if project.ProposalHash(loaded.Lock.Bounds, loaded.Lock.Statements) != p.Hash || !reflect.DeepEqual(loaded.Lock.Statements, p.Statements) {
		t.Fatal("explanation altered lock semantics")
	}
	cur := &formalize.Current{Dir: "examples/reader-lifecycle", Manifest: p.Manifest(), Lock: loaded.Lock, ModuleText: p.ModuleText}
	raw := reader603Object(t, cur)
	raw["view"] = reader603View
	raw["includes"] = map[string]string{"Helper603.tla": reader603Include}
	raw["reader"] = saved
	if err := json.Unmarshal(reader603JSON(t, raw), cur); err != nil {
		t.Fatal(err)
	}
	for _, amend := range []bool{false, true} {
		inspected := false
		a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
			root := filepath.Join(j.Workspace, "previous")
			if amend {
				root = j.Workspace
			}
			for name, want := range map[string]string{"view.json": reader603View, "Helper603.tla": reader603Include, "Reader603.tla": reader603Model} {
				b, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(b) != want {
					t.Fatalf("staged %s=%q, %v", name, b, err)
				}
			}
			if !amend {
				b, err := os.ReadFile(filepath.Join(root, "reader.json"))
				if err != nil {
					t.Fatal(err)
				}
				var s reader603Snapshot
				if err := json.Unmarshal(b, &s); err != nil || !reflect.DeepEqual(s, saved) {
					t.Fatalf("previous computed snapshot lost: %v", err)
				}
			}
			inspected = true
			reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), `{"unsupported":"Fixture stops after inspecting staged input."}`)
			return synth.Usage{}, nil
		}}
		req := formalize.Request{Repo: "o/r", Issue: 603, Previous: p}
		if amend {
			req.Previous = nil
			req.Current = cur
		}
		if _, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), req, t.TempDir()); err != nil || !inspected {
			t.Fatalf("staging: %v", err)
		}
	}
	// Legacy JSON remains readable and writable without inventing evidence.
	legacy := reader603Copy(t, p, func(m map[string]any) { delete(m, "reader"); delete(m, "view"); delete(m, "includes") })
	if legacy.Hash != p.Hash || !legacy.Ratifiable() {
		t.Fatal("legacy proposal compatibility changed")
	}
	legacyDir := t.TempDir()
	if err := legacy.Write(legacyDir, "legacy", rat); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacyDir, ".invariant/reader.json")); !os.IsNotExist(err) {
		t.Fatal("legacy proposal acquired fabricated chart")
	}
}

func TestReader603ChangedInputsInvalidate(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	p, _ := reader603Checked(t, ws, tc)
	before := reader603Saved(t, p)
	edits := map[string]func(map[string]any){
		"model": func(m map[string]any) { m["module_text"] = m["module_text"].(string) + "\\* changed\n" },
		"view":  func(m map[string]any) { m["view"] = m["view"].(string) + " " },
		"include bytes": func(m map[string]any) {
			m["includes"].(map[string]any)["Helper603.tla"] = reader603Include + "\\* changed\n"
		},
		"include set": func(m map[string]any) { delete(m["includes"].(map[string]any), "Helper603.tla") },
		"bounds":      func(m map[string]any) { m["bounds"].(map[string]any)["Limit"] = "3" },
		"wording":     func(m map[string]any) { m["statements"].([]any)[1].(map[string]any)["says"] = "Different words." },
		"chart": func(m map[string]any) {
			m["reader"].(map[string]any)["chart"].(map[string]any)["states"].([]any)[0].(map[string]any)["label"] = "FORGED"
		},
		"tool": func(m map[string]any) {
			m["reader"].(map[string]any)["chart"].(map[string]any)["report"].(map[string]any)["source"].(map[string]any)["tool"].(map[string]any)["Image"] = "different@sha256:" + strings.Repeat("b", 64)
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			q := reader603Copy(t, p, edit)
			if err := q.Write(t.TempDir(), "", nil); err == nil {
				t.Fatal("stale or edited chart was written as current evidence")
			}
		})
	}
	for _, kind := range []string{"model", "view", "include", "bounds", "image", "jar"} {
		t.Run("fresh "+kind, func(t *testing.T) {
			fresh := t.TempDir()
			reader603Workspace(t, fresh)
			tools := tc
			switch kind {
			case "model":
				reader603Write(t, filepath.Join(fresh, "Reader603.tla"), reader603Model+"\\* changed\n")
			case "view":
				reader603Write(t, filepath.Join(fresh, "view.json"), reader603View+" ")
			case "include":
				reader603Write(t, filepath.Join(fresh, "Helper603.tla"), reader603Include+"\\* changed\n")
			case "bounds":
				reader603Write(t, filepath.Join(fresh, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"3"`, 1))
			case "image":
				tools.JavaImage = "fixture@sha256:" + strings.Repeat("b", 64)
			case "jar":
				tools.TLCJar = filepath.Join(t.TempDir(), "changed.jar")
				reader603Write(t, tools.TLCJar, "changed jar")
			}
			q, _ := reader603Checked(t, fresh, tools)
			after := reader603Saved(t, q)
			if before.Fingerprint == after.Fingerprint {
				t.Fatal("changed input reused the artifact fingerprint")
			}
			if kind != "bounds" && q.Hash != p.Hash {
				t.Fatal("explanatory input changed statement ratification hash")
			}
		})
	}
	// Identical bytes in another workspace produce identical artifacts.
	another := t.TempDir()
	reader603Workspace(t, another)
	q, _ := reader603Checked(t, another, tc)
	if !reflect.DeepEqual(before, reader603Saved(t, q)) {
		t.Fatal("temporary paths or timing leaked into deterministic snapshot")
	}
}

func TestReader603RegularInputsAndPrompt(t *testing.T) {
	for _, req := range []formalize.Request{{}, {Previous: &formalize.Proposal{}}, {Current: &formalize.Current{Manifest: project.Manifest{Module: ".invariant/specs/Reader603.tla", Code: "reader"}}}} {
		prompt := formalize.Prompt(req, 4)
		for _, want := range []string{"view.json", "500", "state", "step", "hide", "Mermaid"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("modeled prompt omits %q", want)
			}
		}
		if !strings.Contains(prompt, "thousands of states, not millions") {
			t.Error("prompt lost the existing bounds guidance")
		}
		if !strings.Contains(strings.ToLower(prompt), "charts are drawn for models of up to 500 states") {
			t.Error("prompt must explain the chart capacity without requiring models to shrink")
		}
	}
	for _, name := range []string{"view.json", "Helper603.tla"} {
		for _, hard := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s hard=%v", name, hard), func(t *testing.T) {
				ws := t.TempDir()
				reader603Workspace(t, ws)
				outside := filepath.Join(t.TempDir(), "secret")
				const secret = "PRIVATE_FIXTURE_CONTENT_603"
				reader603Write(t, outside, secret)
				if err := os.Remove(filepath.Join(ws, name)); err != nil {
					t.Fatal(err)
				}
				link := os.Symlink
				if hard {
					link = os.Link
				}
				if err := link(outside, filepath.Join(ws, name)); err != nil {
					t.Fatal(err)
				}
				p, err := formalize.Read(ws)
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("unsafe reader input accepted: %+v %v", p, err)
				}
				if strings.Contains(err.Error(), secret) || strings.Contains(string(reader603JSON(t, p)), secret) {
					t.Fatal("refusal disclosed linked bytes")
				}
			})
		}
	}
}

func TestReader603LargePersistenceAndFinalCheck(t *testing.T) {
	tc, _ := reader603Tools(t)
	for _, final := range []string{"valid", "missing", "invalid"} {
		t.Run(final, func(t *testing.T) {
			var first reader603Snapshot
			a := reader603Agent{run: func(ctx context.Context, job synth.Job) (synth.Usage, error) {
				reader603Workspace(t, job.Workspace)
				reader603Write(t, filepath.Join(job.Workspace, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"499"`, 1))
				p, _ := reader603Checked(t, job.Workspace, tc)
				first = reader603Saved(t, p)
				wire := reader603Object(t, p)
				wire["bounds"].(map[string]any)["Limit"] = "500"
				reader603Write(t, filepath.Join(job.Workspace, "proposal.json"), string(reader603JSON(t, wire)))
				reader603Write(t, filepath.Join(job.Workspace, "reader.json"), string(reader603JSON(t, first)))
				reader603Write(t, filepath.Join(job.Workspace, "formalization.json"), string(reader603JSON(t, formalize.Result{Proposal: p})))
				switch final {
				case "missing":
					if err := os.Remove(filepath.Join(job.Workspace, "view.json")); err != nil {
						t.Fatal(err)
					}
				case "invalid":
					reader603Write(t, filepath.Join(job.Workspace, "view.json"), `{"version":1,"state":[],"step":[]}`)
				}
				return synth.Usage{}, nil
			}}
			out := t.TempDir()
			res, err := (formalize.Formalizer{Backend: a, Toolchain: tc, CheckRuns: 3}).Formalize(t.Context(), formalize.Request{Repo: "o/r", Issue: 603}, out)
			if err != nil || res == nil {
				t.Fatalf("formalizer: %+v %v", res, err)
			}
			if final != "valid" {
				want := "invalid_view"
				if final == "missing" {
					want = "view.json"
				}
				if !strings.Contains(res.Problem, want) {
					t.Fatalf("final large draft bypassed view validation: %+v", res)
				}
				if res.Proposal != nil && reader603Object(t, res.Proposal)["reader"] != nil {
					t.Fatal("invalid final view retained evidence")
				}
				return
			}
			if res.Problem != "" || res.Report == nil || !res.Report.Passed || res.Report.Design.DistinctStates != 501 {
				t.Fatalf("large final draft failed: %+v", res)
			}
			saved := reader603Limited(t, res.Proposal, 501)
			if saved.Fingerprint == first.Fingerprint {
				t.Fatal("final size limitation reused stale chart fingerprint")
			}
			var restored formalize.Result
			b, err := os.ReadFile(filepath.Join(out, "formalization.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b, &restored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(saved, reader603Limited(t, restored.Proposal, 501)) {
				t.Fatal("formalization lost size limitation")
			}
			for name, want := range map[string]string{"view.json": reader603View, "Reader603.tla": reader603Model, "Helper603.tla": reader603Include} {
				b, err := os.ReadFile(filepath.Join(out, name))
				if err != nil || string(b) != want {
					t.Errorf("saved %s=%q, %v", name, b, err)
				}
			}
			reader603PersistenceAndStaging(t, tc, restored.Proposal)
		})
	}
}

func TestReader603LargeChangedInputsInvalidate(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	const largeDraft = `"Limit":"500"`
	reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, largeDraft, 1))
	p, _ := reader603Checked(t, ws, tc)
	before := reader603Limited(t, p, 501)
	edits := map[string]func(map[string]any){
		"model": func(m map[string]any) { m["module_text"] = reader603Model + "\\* changed\n" },
		"view":  func(m map[string]any) { m["view"] = reader603View + " " },
		"include bytes": func(m map[string]any) {
			m["includes"].(map[string]any)["Helper603.tla"] = reader603Include + "\\* changed\n"
		},
		"include set":        func(m map[string]any) { delete(m["includes"].(map[string]any), "Helper603.tla") },
		"bounds":             func(m map[string]any) { m["bounds"].(map[string]any)["Limit"] = "501" },
		"wording":            func(m map[string]any) { m["statements"].([]any)[1].(map[string]any)["says"] = "Changed words." },
		"missing limitation": func(m map[string]any) { delete(m["reader"].(map[string]any), "limitation") },
		"code":               func(m map[string]any) { m["reader"].(map[string]any)["limitation"].(map[string]any)["code"] = "other" },
		"count":              func(m map[string]any) { m["reader"].(map[string]any)["limitation"].(map[string]any)["actual"] = 900 },
		"capacity":           func(m map[string]any) { m["reader"].(map[string]any)["limitation"].(map[string]any)["limit"] = 900 },
		"tool": func(m map[string]any) {
			m["reader"].(map[string]any)["limitation"].(map[string]any)["source"].(map[string]any)["tool"].(map[string]any)["Image"] = "changed@sha256:" + strings.Repeat("b", 64)
		},
		"jar": func(m map[string]any) {
			m["reader"].(map[string]any)["limitation"].(map[string]any)["source"].(map[string]any)["tool"].(map[string]any)["JarSHA256"] = strings.Repeat("b", 64)
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			q := reader603Copy(t, p, edit)
			if err := q.Write(t.TempDir(), "", nil); err == nil {
				t.Fatal("stale or edited limitation was written as current evidence")
			}
		})
	}
	for _, kind := range []string{"unchanged", "model", "view", "include", "include set", "bounds", "wording", "image", "jar"} {
		t.Run("fresh "+kind, func(t *testing.T) {
			fresh := t.TempDir()
			reader603Workspace(t, fresh)
			draft := strings.Replace(reader603Draft, `"Limit":"2"`, largeDraft, 1)
			tools := tc
			actual := 501
			switch kind {
			case "model":
				reader603Write(t, filepath.Join(fresh, "Reader603.tla"), reader603Model+"\\* changed\n")
			case "view":
				reader603Write(t, filepath.Join(fresh, "view.json"), reader603View+" ")
			case "include":
				reader603Write(t, filepath.Join(fresh, "Helper603.tla"), reader603Include+"\\* changed\n")
			case "include set":
				reader603Write(t, filepath.Join(fresh, "Extra603.tla"), "---- MODULE Extra603 ----\nExtra == 0\n====\n")
			case "bounds":
				draft = strings.Replace(draft, largeDraft, `"Limit":"501"`, 1)
				actual = 502
			case "wording":
				draft = strings.Replace(draft, "The count stays within its limit.", "The count remains within its limit.", 1)
			case "image":
				tools.JavaImage = "fixture@sha256:" + strings.Repeat("b", 64)
			case "jar":
				tools.TLCJar = filepath.Join(t.TempDir(), "changed.jar")
				reader603Write(t, tools.TLCJar, "changed jar")
			}
			reader603Write(t, filepath.Join(fresh, "proposal.json"), draft)
			q, _ := reader603Checked(t, fresh, tools)
			after := reader603Limited(t, q, actual)
			if kind == "unchanged" {
				if !reflect.DeepEqual(before, after) {
					t.Fatal("paths or timing leaked into limitation")
				}
				return
			}
			if before.Fingerprint == after.Fingerprint {
				t.Fatal("changed input reused limitation fingerprint")
			}
			if kind != "bounds" && kind != "wording" && q.Hash != p.Hash {
				t.Fatal("explanatory input changed statement hash")
			}
		})
	}
}
