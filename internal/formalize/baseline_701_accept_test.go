package formalize_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
)

// All successful work uses reader603's actual model/graph/replay pipeline.
// Distinct source comments identify baseline work without depending on temp paths.
const baseline701Mark = "\\* captured baseline for 701\n"

type baseline701Work struct{ gate, graph, replay int }

func baseline701Counts(t *testing.T, log, model string) baseline701Work {
	t.Helper()
	var n baseline701Work
	for _, call := range reader603Calls(t, log) {
		if call["main"] != model {
			continue
		}
		if call["include"] != reader603Include || !strings.Contains(call["config"], "CONSTANT Limit = ") {
			t.Fatal("check lost captured includes or explicit bounds")
		}
		switch {
		case call["dump"] == "":
			n.gate++
		case call["module"] == "Reader603":
			n.graph++
		default:
			n.replay++
		}
	}
	return n
}

func baseline701Want(t *testing.T, log, model string, want baseline701Work) {
	t.Helper()
	if got := baseline701Counts(t, log, model); got != want {
		t.Errorf("baseline gate/graph/replay work = %+v, want %+v", got, want)
	}
}

func baseline701Current(t *testing.T, view string, limit string) *formalize.Current {
	t.Helper()
	ws := t.TempDir()
	reader603Workspace(t, ws)
	reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"`+limit+`"`, 1))
	p, err := formalize.Read(ws)
	if err != nil {
		t.Fatal(err)
	}
	return &formalize.Current{Dir: "examples/reader-701", Commit: "captured-701", Manifest: p.Manifest(),
		ModuleText: p.ModuleText + baseline701Mark, Includes: p.Includes, View: view,
		Lock: project.Lock{Decision: "D-0362", Ratified: &project.Ratification{Issue: 571, By: "owner", Proposal: p.Hash}, Bounds: p.Bounds, Statements: p.Statements}}
}

func baseline701Check(t *testing.T, ctx context.Context, ws string, tc toolchain.Toolchain) *formalize.Proposal {
	t.Helper()
	p, r, err := formalize.Check(ctx, ws, tc)
	if err != nil || r == nil || !r.Passed || p == nil || p.Reader == nil || p.Reader.Comparison == nil {
		t.Fatalf("draft check failed: report=%+v error=%v", r, err)
	}
	if err := p.ValidateReader(); err != nil {
		t.Fatal(err)
	}
	return p
}

func baseline701AssertSources(t *testing.T, p *formalize.Proposal, cur *formalize.Current, view string) {
	t.Helper()
	c := p.Reader.Comparison
	if c.Baseline.Model != cur.ModuleText || c.Baseline.View != view || !reflect.DeepEqual(c.Baseline.Includes, cur.Includes) || !reflect.DeepEqual(c.Baseline.Lock, cur.Lock) {
		t.Fatal("comparison substituted captured model, includes, full lock or effective view")
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestComparison701SessionReuseAndFreshFinal(t *testing.T) {
	for _, limit := range []string{"2", "500"} {
		t.Run(limit, func(t *testing.T) {
			tc, log := reader603Tools(t)
			cur := baseline701Current(t, reader603View, limit)
			per := baseline701Work{1, 1, 1}
			if limit == "500" {
				per.replay = 0
			}
			var last *formalize.Proposal
			a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
				reader603Workspace(t, j.Workspace)
				// No chart or proposal hash supplied by the agent may seed the memo.
				reader603Write(t, filepath.Join(j.Workspace, "reader.json"), `{"version":1,"chart":{}}`)
				reader603Write(t, filepath.Join(j.Workspace, "comparison.json"), `{"baseline":{"model":"invented"}}`)
				first := baseline701Check(t, ctx, j.Workspace, tc)
				baseline701AssertSources(t, first, cur, cur.View)
				baseline701Want(t, log, cur.ModuleText, per)
				beforeCandidate := baseline701Counts(t, log, reader603Model)
				if beforeCandidate.gate != 1 || beforeCandidate.graph == 0 || beforeCandidate.replay == 0 {
					t.Fatal("candidate did not run its own model, graph and invariant replay")
				}
				original := string(reader603JSON(t, first.Reader.Comparison.Baseline))
				first.Reader.Comparison.Baseline.Lock.Bounds["Limit"] = "999"
				first.Reader.Comparison.Baseline.Includes["Helper603.tla"] = "invented"
				if limit == "500" {
					if first.Reader.Comparison.Baseline.Chart != nil || first.Reader.Comparison.Baseline.Limitation.Actual != 501 {
						t.Fatal("large baseline shrank or lost its too_large evidence")
					}
					first.Reader.Comparison.Baseline.Limitation.Source.Config.Constants["Limit"] = "999"
				} else {
					first.Reader.Comparison.Baseline.Chart.Report.Counts["tlc_states"] = 999
				}
				// Concurrent warm hits must give independent evidence to every caller.
				const calls = 3
				var wg sync.WaitGroup
				results := make([]*formalize.Proposal, calls)
				errors := make([]error, calls)
				for i := range results {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						var err error
						results[i], _, err = formalize.Check(ctx, j.Workspace, tc)
						errors[i] = err
					}(i)
				}
				wg.Wait()
				for i, p := range results {
					if errors[i] != nil || p == nil || p.Reader == nil || p.Reader.Comparison == nil {
						t.Fatalf("concurrent check %d: %v", i, errors[i])
					}
					if string(reader603JSON(t, p.Reader.Comparison.Baseline)) != original {
						t.Fatal("returned evidence aliases an earlier result or cache entry")
					}
				}
				results[0].Reader.Comparison.Baseline.Lock.Bounds["Limit"] = "777"
				if results[1].Reader.Comparison.Baseline.Lock.Bounds["Limit"] != limit {
					t.Fatal("concurrent results share mutable evidence")
				}
				baseline701Want(t, log, cur.ModuleText, per)
				gotCandidate := baseline701Counts(t, log, reader603Model)
				if gotCandidate.gate != calls+1 || gotCandidate.graph < beforeCandidate.graph+calls || gotCandidate.replay < beforeCandidate.replay+calls {
					t.Fatal("cached baseline skipped candidate checks")
				}
				reader603Write(t, filepath.Join(j.Workspace, "view.json"), strings.ReplaceAll(reader603View, "Working", "Revised working"))
				last = baseline701Check(t, ctx, j.Workspace, tc)
				if last.Hash != first.Hash || last.Reader.Comparison.Fingerprint == results[1].Reader.Comparison.Fingerprint || last.Reader.Comparison.Candidate.View != last.View {
					t.Fatal("same statement hash reused a stale candidate comparison")
				}
				baseline701Want(t, log, cur.ModuleText, per)
				// Tampered conveniences cannot affect final comparison authority.
				for name, value := range map[string]string{"baseline/Reader603.tla": "invented", "baseline/Helper603.tla": "invented", "baseline/ratified.lock": `{}`, "baseline/view.json": `{"version":99}`, "check-context.json": `{}`, "reader.json": `{"version":1,"chart":{}}`} {
					reader603Write(t, filepath.Join(j.Workspace, name), value)
				}
				return synth.Usage{}, nil
			}}
			r, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Current: cur}, t.TempDir())
			if err != nil || r == nil || r.Problem != "" {
				t.Fatalf("fresh final: %+v %v", r, err)
			}
			baseline701AssertSources(t, r.Proposal, cur, cur.View)
			baseline701Want(t, log, cur.ModuleText, baseline701Work{2, 2, 2 * per.replay})
			if r.Proposal.Reader.Comparison.Fingerprint != last.Reader.Comparison.Fingerprint {
				t.Fatal("final comparison differs from independently checked same sources")
			}
			// A new run cannot inherit the previous session's checked evidence.
			fresh := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
				reader603Workspace(t, j.Workspace)
				baseline701Check(t, ctx, j.Workspace, tc)
				baseline701Check(t, ctx, j.Workspace, tc)
				return synth.Usage{}, nil
			}}
			r, err = (formalize.Formalizer{Backend: fresh, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Current: cur}, t.TempDir())
			if err != nil || r == nil || r.Problem != "" {
				t.Fatalf("new session: %+v %v", r, err)
			}
			baseline701Want(t, log, cur.ModuleText, baseline701Work{4, 4, 4 * per.replay})
		})
	}
}

func TestComparison701LegacyAndToolKeys(t *testing.T) {
	tc, log := reader603Tools(t)
	cur := baseline701Current(t, "", "2")
	a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
		reader603Workspace(t, j.Workspace)
		if _, _, err := formalize.Check(ctx, j.Workspace, tc); err == nil || !strings.Contains(err.Error(), "baseline/view.json") || !strings.Contains(err.Error(), "original bounds") {
			t.Fatalf("missing legacy view: %v", err)
		}
		invalid := strings.Replace(reader603View, `"label":"Continue"`, `"label":"Continue","hide":true`, 1)
		reader603Write(t, filepath.Join(j.Workspace, "baseline/view.json"), invalid)
		for i := 0; i < 2; i++ {
			if _, _, err := formalize.Check(ctx, j.Workspace, tc); err == nil || !strings.Contains(err.Error(), "unreachable_chart") || !strings.Contains(err.Error(), "baseline/view.json") || strings.Contains(err.Error(), "new amendment issue") {
				t.Fatalf("invalid supplied view: %v", err)
			}
		}
		baseline701Want(t, log, cur.ModuleText, baseline701Work{2, 2, 2})
		// Same statement hash, different effective view and pinned-tool inputs.
		view := reader603View
		tools := tc
		for n, change := range []string{"valid", "view", "image", "jar bytes at same path"} {
			switch change {
			case "view":
				view = strings.ReplaceAll(view, "Working", "Legacy working")
			case "image":
				tools.JavaImage = "fixture@sha256:" + strings.Repeat("b", 64)
			case "jar bytes at same path":
				reader603Write(t, tools.TLCJar, "different TLC jar bytes at the same path")
			}
			reader603Write(t, filepath.Join(j.Workspace, "baseline/view.json"), view)
			for i := 0; i < 2; i++ {
				p := baseline701Check(t, ctx, j.Workspace, tools)
				baseline701AssertSources(t, p, cur, view)
				if p.Reader.Comparison.Baseline.Chart.Report.Source.Tool.Image != tools.JavaImage {
					t.Fatal("baseline tool provenance did not change with the pinned image")
				}
			}
			want := n + 3
			baseline701Want(t, log, cur.ModuleText, baseline701Work{want, want, want})
		}
		return synth.Usage{}, nil
	}}
	r, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Current: cur}, t.TempDir())
	if err != nil || r == nil || r.Problem != "" {
		t.Fatalf("legacy final: %+v %v", r, err)
	}
	baseline701Want(t, log, cur.ModuleText, baseline701Work{7, 7, 7})
	for _, call := range reader603Calls(t, log) {
		if call["main"] == cur.ModuleText && !strings.Contains(call["config"], "CONSTANT Limit = 2\n") {
			t.Fatal("legacy view was checked at changed bounds")
		}
	}
}

// Inject failure only at the baseline's Docker boundary. Successful executions
// still delegate to the existing strict TLC double, never to a stored chart.
func init() {
	if os.Getenv("INVARIANT_COMPARISON701_DOCKER") != "1" {
		return
	}
	args := os.Args[1:]
	var work string
	dump := false
	for i, a := range args {
		if a == "-dump" {
			dump = true
		}
		if (a == "-v" || a == "--volume") && i+1 < len(args) && strings.HasSuffix(args[i+1], ":/work") {
			work = strings.TrimSuffix(args[i+1], ":/work")
		}
	}
	if work != "" && !dump {
		model, _ := os.ReadFile(filepath.Join(work, "Reader603.tla"))
		mode, _ := os.ReadFile(os.Getenv("INVARIANT_COMPARISON701_FAULT"))
		if strings.Contains(string(model), baseline701Mark) && len(mode) != 0 {
			if string(mode) == "incomplete" {
				fmt.Println("@!@!@STARTMSG 2262:0 @!@!@\nTLC2 Version 2.19\n@!@!@ENDMSG 2262 @!@!@")
				os.Exit(0)
			}
			fmt.Fprintln(os.Stderr, "injected baseline tool failure")
			os.Exit(91)
		}
	}
	os.Exit(reader603Docker(args))
}

func TestComparison701RetriesIncompleteAndCanceledChecks(t *testing.T) {
	tc, log := reader603Tools(t)
	bin, fault := t.TempDir(), filepath.Join(t.TempDir(), "fault")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexport INVARIANT_COMPARISON701_DOCKER=1\nexec '" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_COMPARISON701_FAULT", fault)
	cur := baseline701Current(t, reader603View, "2")
	a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
		reader603Workspace(t, j.Workspace)
		for _, mode := range []string{"fail", "incomplete"} {
			reader603Write(t, fault, mode)
			for i := 0; i < 2; i++ {
				p, _, err := formalize.Check(ctx, j.Workspace, tc)
				if err == nil || !strings.Contains(err.Error(), "baseline") || p != nil && p.Reader != nil && p.Reader.Comparison != nil {
					t.Fatalf("%s baseline was accepted or failed outside comparison: %v", mode, err)
				}
			}
		}
		if err := os.Remove(fault); err != nil {
			t.Fatal(err)
		}
		first := baseline701Check(t, ctx, j.Workspace, tc)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, r, err := formalize.Check(canceled, j.Workspace, tc); err == nil && r != nil && r.Passed {
			t.Fatal("canceled check returned cached success")
		}
		second := baseline701Check(t, ctx, j.Workspace, tc)
		if first.Reader.Comparison.Fingerprint != second.Reader.Comparison.Fingerprint {
			t.Fatal("recovered same sources changed evidence")
		}
		baseline701Want(t, log, cur.ModuleText, baseline701Work{1, 1, 1})
		return synth.Usage{}, nil
	}}
	r, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Current: cur}, t.TempDir())
	if err != nil || r == nil || r.Problem != "" {
		t.Fatalf("recovery final: %+v %v", r, err)
	}
	baseline701Want(t, log, cur.ModuleText, baseline701Work{2, 2, 2})
	// Serialization of a result must not smuggle a session cache into authority.
	var restored formalize.Result
	if err := json.Unmarshal(reader603JSON(t, r), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.CheckContext == nil || restored.CheckContext.Baseline.Chart != nil || restored.CheckContext.Baseline.Limitation != nil {
		t.Fatal("saved check authority contains cached derived evidence")
	}
}
