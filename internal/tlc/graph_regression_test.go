package tlc_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestGraphDOTRecordBoundaries(t *testing.T) {
	// Attributes and statements need not occupy one physical line. Escaped
	// punctuation inside strings must not become part of the DOT grammar.
	const label = "text = \"[a] -> {b}; \\\"quoted\\\" \\\\n\""
	dot := "strict digraph DiskGraph { subgraph cluster_graph { " +
		"-1 [style=filled,label=" + graph575Quote(label) + "] " +
		"-1 -> -1 [color=\"black\",label=\"Keep\",fontcolor=\"black\"] }}"
	g, err := tlc.ParseGraphDOT(dot)
	if err != nil || g == nil {
		t.Fatal(err)
	}
	if g.States[0].Label != label || g.States[0].Values["text"] != strings.TrimPrefix(label, "text = ") ||
		!reflect.DeepEqual(g.Steps, []tlc.GraphStep{{From: "-1", Action: "Keep", To: "-1"}}) {
		t.Fatalf("changed escaped data: %+v", g)
	}
	for name, invalid := range map[string]string{
		"duplicate label":         strings.Replace(dot, "style=filled", "label=\"text = 1\",style=filled", 1),
		"missing attribute end":   strings.Replace(dot, `fontcolor="black"]`, `fontcolor="black"`, 1),
		"unquoted action":         strings.Replace(dot, `label="Keep"`, `label=Keep`, 1),
		"invalid attribute token": strings.Replace(dot, `color="black"`, `color=>`, 1),
		"unsupported escape":      strings.Replace(dot, `label="Keep"`, `label="Kee\qp"`, 1),
		"fingerprint overflow":    strings.ReplaceAll(dot, "-1", "9223372036854775808"),
		"extra close":             dot + "}",
		"duplicate declaration":   strings.Replace(dot, "}}", "-1 [label="+graph575Quote(label)+"] }}", 1),
		"inconsistent variables":  strings.Replace(dot, "}}", "2 [label=\"other = 2\"] }}", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if g, err := tlc.ParseGraphDOT(invalid); err == nil || g != nil {
				t.Fatalf("accepted invalid records: %+v, %v", g, err)
			}
		})
	}
}

func TestGraphCompletionRejectsAmbiguousRecords(t *testing.T) {
	valid := graph575Transcript(false)
	t.Run("distinct exceeds generated", func(t *testing.T) {
		f := graph575Setup(t)
		var lines []string
		for _, line := range strings.Split(graph575DOT(), "\n") {
			if !strings.Contains(line, " -> ") {
				lines = append(lines, line)
			}
		}
		graph575Write(t, filepath.Join(f.root, "dot"), strings.Join(lines, "\n"))
		graph575Write(t, filepath.Join(f.root, "transcript"), strings.Replace(valid, "9 states generated", "2 states generated", 1))
		if g, err := f.runner.DumpGraph(graph575Context(t), f.dir, "Graph575", f.config); err == nil || g != nil {
			t.Fatalf("impossible statistics supplied evidence: %+v, %v", g, err)
		}
	})
	for name, out := range map[string]string{
		"generated mismatch":              strings.Replace(valid, "9 states generated", "10 states generated", 1),
		"numeric overflow":                strings.Replace(valid, "9 states generated", "9223372036854775808 states generated", 1),
		"bad grouping":                    strings.Replace(valid, "9 states generated", "0,9 states generated", 1),
		"negative count":                  strings.Replace(valid, "9 states generated", "-9 states generated", 1),
		"zero depth":                      strings.Replace(valid, "search is 2", "search is 0", 1),
		"impossible depth":                strings.Replace(valid, "search is 2", "search is 4", 1),
		"duplicate run":                   valid + valid,
		"nested frame":                    strings.Replace(valid, "Finished in 10ms", graph575Frame(2193, "Model checking completed. No error has been found.")+"Finished in 10ms", 1),
		"error severity":                  strings.Replace(valid, "STARTMSG 2193:0", "STARTMSG 2193:1", 1),
		"forged success":                  strings.Replace(valid, "Model checking completed. No error has been found.", "not successful", 1),
		"empty finish":                    strings.Replace(valid, "Finished in 10ms at (2026-11-04 00:00:00)", "", 1),
		"duplicate version":               graph575Frame(2262, "TLC2 Version 2.19") + valid,
		"orphan frame end":                valid + "@!@!@ENDMSG 1000 @!@!@\n",
		"inconsistent initial generation": strings.Replace(graph575Transcript(true), "3 states generated, with 2", "1 states generated, with 2", 1),
	} {
		t.Run(name, func(t *testing.T) {
			f := graph575Setup(t)
			graph575Write(t, filepath.Join(f.root, "transcript"), out)
			if g, err := f.runner.DumpGraph(graph575Context(t), f.dir, "Graph575", f.config); err == nil || g != nil {
				t.Fatalf("ambiguous completion supplied evidence: %+v, %v", g, err)
			}
		})
	}
}

func TestGraphConcurrentConfigurationsAndJarSnapshot(t *testing.T) {
	f := graph575Setup(t)
	budget, err := tlc.NewBudget(2, 8<<30, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.runner.Budget = budget
	graph575Write(t, filepath.Join(f.root, "mode"), "hold")
	ctx := graph575Context(t)
	type result struct {
		graph *tlc.GraphResult
		err   error
	}
	done := make(chan result, 2)
	for _, limit := range []string{"2", "3"} {
		cfg := tlc.Config{Specification: "Spec", Constants: map[string]string{"Limit": limit}}
		go func() {
			g, err := f.runner.DumpGraph(ctx, f.dir, "Graph575", cfg)
			done <- result{g, err}
		}()
	}
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(f.root, "release-run"), nil, 0600) })
	graph575Await(t, func() bool {
		matches, _ := filepath.Glob(filepath.Join(f.root, "*.ready"))
		return len(matches) == 2
	})
	runs := f.records(t)
	if len(runs) != 2 || runs[0].ConfigPath == runs[1].ConfigPath || runs[0].DumpPath == runs[1].DumpPath {
		t.Fatalf("concurrent dumps shared paths: %+v", runs)
	}
	configs := map[string]bool{}
	for _, run := range runs {
		configs[run.Config] = true
		jarDir := ""
		for _, arg := range run.Args {
			if host, ok := strings.CutSuffix(arg, ":/opt/tla:ro"); ok {
				jarDir = host
			}
		}
		if jarDir == "" || jarDir == filepath.Dir(f.runner.Jar) {
			t.Fatalf("run did not mount its own jar snapshot: %+v", run)
		}
		data, err := os.ReadFile(filepath.Join(jarDir, "tla2tools.jar"))
		if err != nil || string(data) != "graph575 fake jar bytes\n" {
			t.Fatalf("wrong mounted jar: %q, %v", data, err)
		}
	}
	if !configs["SPECIFICATION Spec\nCONSTANT Limit = 2\n"] || !configs["SPECIFICATION Spec\nCONSTANT Limit = 3\n"] {
		t.Fatalf("concurrent configuration overwritten: %v", configs)
	}
	graph575Write(t, f.runner.Jar, "replacement bytes while TLC runs\n")
	graph575Write(t, filepath.Join(f.root, "release-run"), "")
	sum := sha256.Sum256([]byte("graph575 fake jar bytes\n"))
	limits := map[string]bool{}
	for range 2 {
		got := <-done
		if got.err != nil || got.graph == nil || got.graph.Tool.JarSHA256 != fmt.Sprintf("%x", sum) {
			t.Fatalf("jar provenance changed while running: %+v, %v", got.graph, got.err)
		}
		limits[got.graph.Config.Constants["Limit"]] = true
	}
	if len(limits) != 2 {
		t.Fatalf("returned configuration overwritten: %v", limits)
	}
	for _, run := range runs {
		if _, err := os.Stat(filepath.Dir(run.ConfigPath)); !os.IsNotExist(err) {
			t.Fatalf("run directory not cleaned up: %v", err)
		}
	}
}

func TestGraphMissingJarReleasesAdmission(t *testing.T) {
	f := graph575Setup(t)
	budget, err := tlc.NewBudget(1, 1<<30, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.runner.Budget = budget
	if err := os.Remove(f.runner.Jar); err != nil {
		t.Fatal(err)
	}
	ctx := graph575Context(t)
	if g, err := f.runner.DumpGraph(ctx, f.dir, "Graph575", f.config); err == nil || g != nil {
		t.Fatalf("missing jar supplied evidence: %+v, %v", g, err)
	}
	if len(f.records(t)) != 0 {
		t.Fatal("missing jar launched Docker")
	}
	graph575Write(t, f.runner.Jar, "graph575 fake jar bytes\n")
	if g, err := f.runner.DumpGraph(ctx, f.dir, "Graph575", f.config); err != nil || g == nil {
		t.Fatalf("setup failure leaked admission: %+v, %v", g, err)
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "Graph575.tla" {
		t.Fatalf("setup failure leaked temporary files: %v, %v", entries, err)
	}
}
