package main

import (
	"encoding/json"
	"fmt"
	"github.com/gitdek/invariant/internal/formalize"
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

// This TLC stand-in owns its failure branch as well as its complete graph and
// replay protocol; the older command reader fixture always reports success.
func init() {
	if os.Getenv("INVARIANT_PREVIEW639_DOCKER") == "1" {
		os.Exit(preview639Docker(os.Args[1:]))
	}
}
func preview639Tools(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	bin := t.TempDir()
	log := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexport INVARIANT_PREVIEW639_DOCKER=1\nexec '" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_PREVIEW639_LOG", log)
	jar := filepath.Join(t.TempDir(), "tla2tools.jar")
	reader603Write(t, jar, "fixture TLC jar 603")
	return toolchain.Toolchain{JavaImage: toolchain.JavaImage, TLCJar: jar}, log
}
func preview639Docker(args []string) int {
	fail := func(s string) int { fmt.Fprintln(os.Stderr, "preview639 Docker double: "+s); return 91 }
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
	if err := os.WriteFile(filepath.Join(os.Getenv("INVARIANT_PREVIEW639_LOG"), name+".json"), record, 0600); err != nil {
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

func TestPreview639CheckToolReportsOptions(t *testing.T) {
	tc, _ := preview639Tools(t)
	ws := t.TempDir()
	log := filepath.Join(t.TempDir(), "checks.jsonl")
	d := preview639Draft(t, 2, nil)
	preview639Input(d, 0, 0)["model"].(map[string]any)["view"] = `{"version":1,"state":[],"step":[]}`
	preview639Input(d, 1, 1)["model"].(map[string]any)["module_text"] = strings.Replace(reader603Model, `Safe == n \in 0..Limit`, `Safe == FALSE`, 1)
	preview639Write(t, ws, d)
	check := checkTool(ws, tc, 2, log, 0)
	feedback, failed := check.Call(t.Context(), json.RawMessage(`{}`))
	if !failed {
		t.Fatal("invalid options were reported as a successful check")
	}
	for _, want := range []string{"F1", "A", "invalid_view", "F2", "B", "Invariant Safe is violated", "1 of 2"} {
		if !strings.Contains(feedback, want) {
			t.Errorf("repair feedback omits %q: %s", want, feedback)
		}
	}
	if strings.Contains(feedback, "nothing to model-check") {
		t.Fatal("modeled forks bypassed the tool")
	}
	preview639Write(t, ws, preview639Draft(t, 2, nil))
	feedback, failed = check.Call(t.Context(), json.RawMessage(`{}`))
	if failed {
		t.Fatal(feedback)
	}
	for _, want := range []string{"F1", "F2", "A", "B", "Limit", "2 of 2"} {
		if !strings.Contains(feedback, want) {
			t.Errorf("success feedback omits %q: %s", want, feedback)
		}
	}
	if strings.Contains(feedback, "nothing to model-check") {
		t.Fatal("success falsely says previews were not checked")
	}
	file, err := os.Open(log)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var first, second synth.GateRun
	if err = decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err = decoder.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(first.Failed, "\n"), "Invariant Safe is violated") {
		t.Fatal("check log lost the actual TLC invariant violation")
	}
	if first.Passed || len(first.Failed) == 0 || !second.Passed || first.Run != 1 || second.Run != 2 {
		t.Fatalf("option failures not recorded under shared allowance: %+v %+v", first, second)
	}
	feedback, failed = check.Call(t.Context(), json.RawMessage(`{}`))
	if !failed || !strings.Contains(feedback, "No checks left") {
		t.Fatal("forks received an extra uncounted check")
	}
}
