package main

import (
	"encoding/json"
	"fmt"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
	"os"
	"path/filepath"
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

func TestReader603CheckToolRepair(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	log := filepath.Join(t.TempDir(), "checks.jsonl")
	if err := os.Remove(filepath.Join(ws, "view.json")); err != nil {
		t.Fatal(err)
	}
	tool := checkTool(ws, tc, 3, log, 571)
	if !strings.Contains(tool.Description, "view.json") {
		t.Error("check tool does not explain the view input")
	}
	text, bad := tool.Call(t.Context(), json.RawMessage(`{}`))
	if !bad || !strings.Contains(text, "view.json") {
		t.Fatalf("missing-view feedback: error=%v %s", bad, text)
	}
	reader603Write(t, filepath.Join(ws, "view.json"), strings.Replace(reader603View, `"label":"Continue"`, `"label":"Continue","hide":true`, 1))
	text, bad = tool.Call(t.Context(), json.RawMessage(`{}`))
	for _, want := range []string{"unreachable_chart", "advance", "Advance", "Check 2 of 3"} {
		if !strings.Contains(text, want) {
			t.Errorf("check feedback omits %q: %s", want, text)
		}
	}
	if !bad {
		t.Error("dishonest view reported as successful")
	}
	reader603Write(t, filepath.Join(ws, "view.json"), reader603View)
	text, bad = tool.Call(t.Context(), json.RawMessage(`{}`))
	if bad {
		t.Fatalf("repair refused: %s", text)
	}
	for _, want := range []string{"reader", "3", "6", "Limit", "2", "Check 3 of 3"} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Errorf("successful check omits computed reader result %q: %s", want, text)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("check log has %d rows", len(lines))
	}
	for i, line := range lines {
		var run synth.GateRun
		if err := json.Unmarshal([]byte(line), &run); err != nil {
			t.Fatal(err)
		}
		if run.Run != i+1 || run.Passed != (i == 2) {
			t.Errorf("wrong check result: %+v", run)
		}
	}
	text, bad = tool.Call(t.Context(), json.RawMessage(`{}`))
	if !bad || !strings.Contains(text, "No checks left") {
		t.Fatalf("allowance changed: %s", text)
	}
}

func TestReader603CheckToolCapacity(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"500"`, 1))
	log := filepath.Join(t.TempDir(), "checks.jsonl")
	tool := checkTool(ws, tc, 2, log, 571)
	text, bad := tool.Call(t.Context(), json.RawMessage(`{}`))
	if bad {
		t.Fatalf("large draft failed its check: %s", text)
	}
	for _, want := range []string{"too_large", "501", "500", "Limit", "Check 1 of 2"} {
		if !strings.Contains(text, want) {
			t.Errorf("size limitation feedback omits %q: %s", want, text)
		}
	}
	reader603Write(t, filepath.Join(ws, "view.json"), `{"version":1,"state":[],"step":[]}`)
	text, bad = tool.Call(t.Context(), json.RawMessage(`{}`))
	if !bad || !strings.Contains(text, "invalid_view") {
		t.Fatalf("malformed large view accepted: %s", text)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("check log has %d rows", len(lines))
	}
	for i, line := range lines {
		var run synth.GateRun
		if err := json.Unmarshal([]byte(line), &run); err != nil {
			t.Fatal(err)
		}
		if run.Run != i+1 || run.Passed != (i == 0) {
			t.Errorf("wrong capacity check status: %+v", run)
		}
	}
}
