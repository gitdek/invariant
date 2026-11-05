package synth_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

const reader615Model = `---- MODULE Reader615 ----
EXTENDS Naturals, Helper615
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
const reader615Include = "---- MODULE Helper615 ----\nZero == 0\n====\n"
const reader615View = `{
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

func reader615Write(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func init() {
	if os.Getenv("INVARIANT_READER615_DOCKER") == "1" {
		os.Exit(reader615Docker(os.Args[1:]))
	}
}
func reader615Tools(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	bin := t.TempDir()
	log := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexport INVARIANT_READER615_DOCKER=1\nexec '" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_READER615_LOG", log)
	jar := filepath.Join(t.TempDir(), "tla2tools.jar")
	reader615Write(t, jar, "fixture TLC jar 615")
	return toolchain.Toolchain{JavaImage: toolchain.JavaImage, TLCJar: jar, GoImage: toolchain.GoImage, GobraImage: toolchain.GobraImage}, log
}
func reader615Docker(args []string) int {
	fail := func(s string) int { fmt.Fprintln(os.Stderr, "reader615 Docker double: "+s); return 91 }
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
	joinedArgs := " " + strings.Join(args, " ") + " "
	if strings.Contains(joinedArgs, " "+toolchain.GobraImage+" ") {
		if !strings.Contains(joinedArgs, " --overflow ") || !strings.Contains(joinedArgs, " --network none ") {
			return fail("Gobra sandbox changed")
		}
		fmt.Println("Gobra found 0 errors")
		return 0
	}
	if strings.Contains(joinedArgs, " "+toolchain.GoImage+" ") {
		if !strings.Contains(joinedArgs, " --network none ") || !strings.Contains(joinedArgs, "go vet ./...") || !strings.Contains(joinedArgs, "TestInvariantAgreement") {
			return fail("Go sandbox changed")
		}
		n := os.Getenv("INVARIANT_READER615_CODE_STATES")
		if n == "" {
			n = "3"
		}
		status := "0"
		if os.Getenv("INVARIANT_READER615_BUILD_FAIL") == "1" {
			status = "1"
		}
		fmt.Printf("@@invariant vet=0\n@@invariant test=%s\ninvariant-agreement states=%s depth=%s\n@@invariant agree=0\n", status, n, n)
		return 0
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
	main, err := os.ReadFile(filepath.Join(work, "Reader615.tla"))
	if err != nil {
		return fail("main model absent: " + err.Error())
	}
	helper, err := os.ReadFile(filepath.Join(work, "Helper615.tla"))
	if err != nil {
		return fail("include absent: " + err.Error())
	}
	match := regexp.MustCompile(`(?m)^CONSTANT Limit = ([0-9]+)$`).FindStringSubmatch(string(config))
	if len(match) != 2 {
		return fail("bounds absent: " + string(config))
	}
	limit, _ := strconv.Atoi(match[1])
	start := 0
	if strings.Contains(string(main), "Init == n = 1") {
		start = 1
	}
	n := limit + 1 - start
	if n < 1 || n > 502 {
		return fail("unexpected fixture size")
	}
	record, _ := json.Marshal(map[string]string{"module": module, "config": string(config), "main": string(main), "include": string(helper), "dump": dump, "args": joined})
	if err := os.WriteFile(filepath.Join(os.Getenv("INVARIANT_READER615_LOG"), name+".json"), record, 0600); err != nil {
		return fail(err.Error())
	}
	if module == "Reader615" && dump == "" {
		if original := os.Getenv("INVARIANT_READER615_MUTATE"); original != "" {
			if err := os.WriteFile(filepath.Join(original, ".invariant/view.json"), []byte(strings.ReplaceAll(reader615View, "Ready", "Changed during run")), 0600); err != nil {
				return fail(err.Error())
			}
			if err := os.WriteFile(filepath.Join(original, ".invariant/specs/Helper615.tla"), []byte(reader615Include+"\n\\* edited during run\n"), 0600); err != nil {
				return fail(err.Error())
			}
		}
	}
	initial, steps := 1, 2*n
	if dump != "" {
		if os.Getenv("INVARIANT_READER615_GRAPH_FAIL") == "1" && module == "Reader615" {
			return fail("requested reader graph failure")
		}
		if os.Getenv("INVARIANT_READER615_REPLAY_FAIL") == "1" && module != "Reader615" {
			return fail("requested reader replay failure")
		}
		var b strings.Builder
		b.WriteString("strict digraph DiskGraph {\nsubgraph cluster_graph {\n")
		replay := module != "Reader615"
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
			label := fmt.Sprintf("n = %d", i+start)
			style := ""
			if i == 0 || replay {
				style = ",style=filled"
			}
			if replay {
				label = fmt.Sprintf("/\\ n = %d\n/\\ %s = \"%d\"\n/\\ %s = [Safe |-> TRUE]", i+start, idVar, i+1, rulesVar)
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

// Receipt wire contract. Input digests describe the exact bytes used, not paths
// on a build machine; the separate reader fingerprint also covers bounds,
// statements, tool/configuration provenance and the computed artifact.
type reader615Record struct {
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
	Inputs      struct {
		Module   string            `json:"module"`
		Model    string            `json:"model_sha256"`
		View     string            `json:"view_sha256"`
		Includes map[string]string `json:"includes"`
	} `json:"inputs"`
	Chart      *statechart.Result `json:"chart,omitempty"`
	Limitation *struct {
		Code   string            `json:"code"`
		Actual int               `json:"actual"`
		Limit  int               `json:"limit"`
		Source statechart.Source `json:"source"`
	} `json:"limitation,omitempty"`
	Unavailable *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"unavailable,omitempty"`
}

func reader615JSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func reader615Hash(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }
func reader615RecordOf(t *testing.T, r *verify.Report) reader615Record {
	t.Helper()
	var wire struct {
		Reader *reader615Record `json:"reader"`
	}
	if err := json.Unmarshal(reader615JSON(t, r), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Reader == nil {
		t.Fatal("verification receipt has no reader record")
	}
	got := *wire.Reader
	if got.Version != 1 {
		t.Fatalf("reader version = %d", got.Version)
	}
	alternatives := 0
	if got.Chart != nil {
		alternatives++
	}
	if got.Limitation != nil {
		alternatives++
	}
	if got.Unavailable != nil {
		alternatives++
	}
	if alternatives != 1 {
		t.Fatalf("reader must have exactly one outcome: %+v", got)
	}
	if got.Unavailable == nil && !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(got.Fingerprint) {
		t.Fatalf("missing reader identity: %+v", got)
	}
	return got
}
func reader615Project(t *testing.T, limit int) string {
	t.Helper()
	dir := t.TempDir()
	m := project.Manifest{Name: "receipt reader", Module: ".invariant/specs/Reader615.tla", Code: "machine", Language: "go"}
	lock := project.Lock{Decision: "receipt-fixture", Bounds: map[string]string{"Limit": strconv.Itoa(limit)}, Statements: []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "Advance, reset or poll."},
		{Name: "Safe", Kind: project.Invariant, Says: "The count stays within its limit.\nKeep <literal> | `words` & spaces."},
	}}
	for i := range lock.Statements {
		h, err := tla.PinHash(reader615Model, lock.Statements[i].Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		lock.Statements[i].SHA256 = h
	}
	for name, text := range map[string]string{
		".invariant/invariant.json":      string(reader615JSON(t, m)),
		".invariant/ratified.lock":       string(reader615JSON(t, lock)),
		".invariant/request.md":          "Build a small lifecycle.",
		".invariant/view.json":           reader615View,
		".invariant/specs/Reader615.tla": reader615Model,
		".invariant/specs/Helper615.tla": reader615Include,
		// This derived file is deliberately invalid. Verification must ignore it.
		".invariant/reader.json": `{"chart":"stale proposal must never be reused"}`,
		"go.mod":                 "module example/receipt\n\ngo 1.27.1\n",
		"machine/machine.go":     "package machine\ntype State int\nfunc Init() State { return 0 }\nfunc Successors(s State) []State { return []State{(s+1)%3} }\n",
	} {
		reader615Write(t, filepath.Join(dir, name), text)
	}
	return dir
}
func reader615Inputs(t *testing.T, got reader615Record, model, view, include string) {
	t.Helper()
	if got.Inputs.Module != "Reader615" || got.Inputs.Model != reader615Hash([]byte(model)) || got.Inputs.View != reader615Hash([]byte(view)) || !reflect.DeepEqual(got.Inputs.Includes, map[string]string{"Helper615.tla": reader615Hash([]byte(include))}) {
		t.Fatalf("reader source hashes do not identify exact checked bytes: %+v", got.Inputs)
	}
}
func reader615Chart(t *testing.T, got reader615Record, states int) *statechart.Result {
	t.Helper()
	if got.Chart == nil {
		t.Fatalf("ordinary graph lost its chart: %+v", got)
	}
	c := got.Chart
	if c.Report.Counts["tlc_states"] != int64(states) || c.Report.Counts["tlc_steps"] != int64(2*states) || c.Report.Counts["tlc_generated_states"] != int64(2*states+1) || c.Report.Counts["hidden_steps"] != int64(states) || c.Report.Counts["shown_steps"] != int64(states) {
		t.Fatalf("lost graph accounting: %+v", c.Report.Counts)
	}
	members, hidden := 0, 0
	for _, s := range c.States {
		members += len(s.Members)
	}
	for _, a := range c.Report.Hidden {
		hidden += len(a.Members)
		if !slices.Contains(a.HidingRules, "poll") {
			t.Fatal("lost hiding rule")
		}
	}
	if members != states || hidden != states || len(c.Initial) != 1 {
		t.Fatalf("partial chart: members=%d hidden=%d initial=%v", members, hidden, c.Initial)
	}
	source := c.Report.Source
	if source.Module != "Reader615" || source.Config.Constants["Limit"] == "" || source.Tool.Image != toolchain.JavaImage || source.Tool.JarSHA256 != strings.TrimPrefix(reader615Hash([]byte("fixture TLC jar 615")), "sha256:") || source.TLC == "" {
		t.Fatalf("lost tool/bounds provenance: %+v", source)
	}
	return c
}

func reader615ChangedModel() string {
	s := strings.Replace(reader615Model, "Init == n = Zero", "Init == n = 1", 1)
	return strings.Replace(s, "n' = Zero", "n' = 1", 1)
}

// The backend edits only unpinned model behavior. Deliberately tampering with
// the workspace's view and include must not replace the saved project inputs.
type reader615Backend struct{ t *testing.T }

func (b reader615Backend) Name() string { return "receipt-fixture" }
func (b reader615Backend) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	if job.ReadOnly {
		return synth.Usage{Summary: "Reviewed."}, nil
	}
	b.t.Helper()
	for _, name := range []string{".invariant/view.json", ".invariant/specs/Helper615.tla"} {
		if _, err := os.ReadFile(filepath.Join(job.Workspace, name)); err != nil {
			b.t.Errorf("synthesis workspace lost %s: %v", name, err)
		}
	}
	reader615Write(b.t, filepath.Join(job.Workspace, ".invariant/specs/Reader615.tla"), reader615ChangedModel())
	reader615Write(b.t, filepath.Join(job.Workspace, "machine/machine.go"), "package machine\ntype State int\nfunc Init() State { return 1 }\nfunc Successors(s State) []State { return []State{3-s} }\n")
	reader615Write(b.t, filepath.Join(job.Workspace, ".invariant/view.json"), strings.ReplaceAll(reader615View, "Working", "Forged workspace view"))
	reader615Write(b.t, filepath.Join(job.Workspace, ".invariant/specs/Helper615.tla"), "forged include")
	reader615Write(b.t, filepath.Join(job.Workspace, ".invariant/reader.json"), `{"chart":"forged"}`)
	return synth.Usage{Outcome: "success"}, nil
}

func reader615Artifacts(t *testing.T, dir string, r *verify.Report) {
	t.Helper()
	files := map[string][]byte{}
	for _, name := range []string{"receipt.json", "receipt.md", "reader.json", "reader.svg"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("missing receipt artifact %s: %v", name, err)
		}
		files[name] = b
	}
	var decoded verify.Report
	if err := json.Unmarshal(files["receipt.json"], &decoded); err != nil {
		t.Fatal(err)
	}
	record := reader615RecordOf(t, r)
	if !reflect.DeepEqual(record, reader615RecordOf(t, &decoded)) || string(files["receipt.md"]) != receipt.Markdown(r) {
		t.Fatal("output receipt differs from final gate")
	}
	var standalone reader615Record
	if err := json.Unmarshal(files["reader.json"], &standalone); err != nil || !reflect.DeepEqual(standalone, record) {
		t.Fatalf("standalone JSON lost reader data: %v", err)
	}
	wantSVG, err := record.Chart.SVG()
	if err != nil {
		t.Fatal(err)
	}
	if string(files["reader.svg"]) != wantSVG {
		t.Fatal("receipt SVG bypasses the common native renderer")
	}
	if !strings.Contains(string(files["receipt.md"]), "```mermaid\nstateDiagram-v2") {
		t.Fatal("Markdown receipt lost Mermaid text")
	}
	d := xml.NewDecoder(bytes.NewReader(files["reader.svg"]))
	var metadata string
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if e, ok := tok.(xml.StartElement); ok {
			switch e.Name.Local {
			case "script", "foreignObject", "image":
				t.Fatalf("SVG needs active or external content: %s", e.Name.Local)
			}
			for _, a := range e.Attr {
				if strings.HasPrefix(a.Name.Local, "on") || a.Name.Local == "href" {
					t.Fatal("SVG has resource/event attribute")
				}
				if e.Name.Local == "metadata" && a.Name.Local == "id" && a.Value == "statechart-data" {
					if err := d.DecodeElement(&metadata, &e); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	var embedded statechart.Result
	if err := json.Unmarshal([]byte(metadata), &embedded); err != nil || !reflect.DeepEqual(&embedded, record.Chart) {
		t.Fatalf("SVG metadata differs from JSON: %v", err)
	}
	// CI uploads and gathers the entire out tree. Exercise that archive shape
	// with real artifacts, including the metadata the dashboard will read.
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	for name, b := range files {
		w, err := z.Create("receipt-reader/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data.Bytes()), int64(data.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		f, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(f)
		f.Close()
		if err != nil || !bytes.Equal(b, files[strings.TrimPrefix(file.Name, "receipt-reader/")]) {
			t.Fatal("archive changed receipt artifacts")
		}
	}
}
func TestReader615SynthesisAndFactory(t *testing.T) {
	tc, _ := reader615Tools(t)
	t.Setenv("INVARIANT_READER615_CODE_STATES", "2")
	for _, factoryBuild := range []bool{false, true} {
		t.Run(fmt.Sprint(factoryBuild), func(t *testing.T) {
			dir := reader615Project(t, 2)
			out := filepath.Join(t.TempDir(), "synthesis")
			o := synth.Options{Project: dir, Out: out, KeepModel: true, Backend: reader615Backend{t}, Binary: "fixture-invariant", GateRuns: 1, Toolchain: tc}
			var result *synth.Result
			var err error
			if factoryBuild {
				result, err = (factory.Synthesis{Options: o}).Build(t.Context(), dir, out, false)
			} else {
				result, err = synth.Synthesize(t.Context(), o)
			}
			if err != nil || result == nil || result.Final == nil || !result.Final.Passed {
				t.Fatalf("synthesis/factory gate: %+v %v", result, err)
			}
			record := reader615RecordOf(t, result.Final)
			reader615Chart(t, record, 2)
			reader615Inputs(t, record, reader615ChangedModel(), reader615View, reader615Include)
			for name, want := range map[string]string{".invariant/view.json": reader615View, ".invariant/specs/Helper615.tla": reader615Include} {
				b, err := os.ReadFile(filepath.Join(result.Dir, name))
				if err != nil || string(b) != want {
					t.Fatalf("staging lost original reader input %s: %v", name, err)
				}
			}
			reader615Artifacts(t, filepath.Join(out, "gate"), result.Final)
			var saved synth.Result
			b, err := os.ReadFile(filepath.Join(out, "synthesis.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b, &saved); err != nil || saved.Final == nil || !reflect.DeepEqual(record, reader615RecordOf(t, saved.Final)) {
				t.Fatalf("synthesis record lost final chart: %v", err)
			}
		})
	}
}
