package tlc_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Run the test executable as a Docker command double, at the same boundary
// as the existing TLC tests. No daemon, network or Java is used here.
func init() {
	if os.Getenv("INVARIANT_GRAPH575_DOCKER") == "1" {
		os.Exit(graph575Docker(os.Args[1:]))
	}
}

type graph575Invocation struct {
	Name, ConfigPath, DumpPath string
	Args                       []string
	Config                     string
}

func graph575Docker(args []string) int {
	root := os.Getenv("INVARIANT_GRAPH575_ROOT")
	if len(args) == 0 {
		return 90
	}
	if args[0] == "image" || args[0] == "inspect" || args[0] == "pull" {
		return 0
	}
	write := func(file string, b []byte) {
		if err := os.WriteFile(file, b, 0600); err != nil {
			panic(err)
		}
	}
	if args[0] == "rm" {
		name := args[len(args)-1]
		write(filepath.Join(root, name+".removing"), nil)
		if _, err := os.Stat(filepath.Join(root, "hold-rm")); err == nil {
			for {
				if _, err := os.Stat(filepath.Join(root, "release-rm")); err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		write(filepath.Join(root, name+".removed"), nil)
		return 0
	}
	if args[0] != "run" {
		return 91
	}
	var name, cfg, stem, workdir string
	mounts := map[string]string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name", "-config", "-w", "--workdir":
			if i+1 >= len(args) {
				return 92
			}
			switch args[i] {
			case "--name":
				name = args[i+1]
			case "-config":
				cfg = args[i+1]
			default:
				workdir = args[i+1]
			}
			i++
		case "-v", "--volume":
			if i+1 >= len(args) {
				return 92
			}
			parts := strings.Split(args[i+1], ":")
			if len(parts) < 2 {
				return 92
			}
			mounts[parts[1]] = parts[0]
			i++
		case "-dump":
			if i+2 >= len(args) || args[i+1] != "dot,actionlabels" {
				return 93
			}
			stem = args[i+2]
			i += 2
		}
	}
	resolve := func(p string) string {
		if !strings.HasPrefix(p, "/") {
			p = filepath.ToSlash(filepath.Join(workdir, p))
		}
		best, host := "", ""
		for mount, source := range mounts {
			if (p == mount || strings.HasPrefix(p, mount+"/")) && len(mount) > len(best) {
				best, host = mount, source
			}
		}
		if host == "" {
			return ""
		}
		return filepath.Join(host, strings.TrimPrefix(strings.TrimPrefix(p, best), "/"))
	}
	if name == "" || cfg == "" || stem == "" {
		return 94
	}
	if !strings.HasSuffix(stem, ".dot") {
		stem += ".dot"
	}
	configPath, dumpPath := resolve(cfg), resolve(stem)
	if configPath == "" || dumpPath == "" {
		return 95
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		return 96
	}
	// The model must still be available under TLC's selected working directory.
	if _, err := os.ReadFile(resolve(args[len(args)-1])); err != nil {
		return 97
	}
	b, _ := json.Marshal(graph575Invocation{Name: name, ConfigPath: configPath, DumpPath: dumpPath, Args: args, Config: string(config)})
	write(filepath.Join(root, name+".run.json"), b)
	mode, _ := os.ReadFile(filepath.Join(root, "mode"))
	dot, err := os.ReadFile(filepath.Join(root, "dot"))
	if err != nil {
		return 98
	}
	if string(mode) != "missing" {
		if err := os.MkdirAll(filepath.Dir(dumpPath), 0755); err != nil {
			return 99
		}
		if string(mode) == "symlink" {
			if err := os.Symlink(filepath.Join(root, "dot"), dumpPath); err != nil {
				return 99
			}
		} else {
			write(dumpPath, dot)
		}
	}
	out, err := os.ReadFile(filepath.Join(root, "transcript"))
	if err != nil {
		return 98
	}
	fmt.Print(string(out))
	write(filepath.Join(root, name+".ready"), nil)
	if string(mode) == "hold" {
		for {
			if _, err := os.Stat(filepath.Join(root, "release-run")); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if string(mode) == "exit12" {
		return 12
	}
	if string(mode) == "exit137" {
		return 137
	}
	return 0
}

func graph575Frame(code int, body string) string {
	return fmt.Sprintf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
}

func graph575Transcript(duplicateInit bool) string {
	initial := graph575Frame(2190, "Finished computing initial states: 2 distinct states generated at 2026-11-04 00:00:00.")
	generated := 9 // two initial states plus seven dumped steps
	if duplicateInit {
		initial = graph575Frame(2191, "Finished computing initial states: 3 states generated, with 2 of them distinct at 2026-11-04 00:00:00.")
		generated = 10
	}
	return graph575Frame(2262, "TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)") +
		initial +
		graph575Frame(2193, "Model checking completed. No error has been found.") +
		graph575Frame(2199, fmt.Sprintf("%d states generated, 3 distinct states found, 0 states left on queue.", generated)) +
		graph575Frame(2194, "The depth of the complete state graph search is 2.") +
		graph575Frame(2186, "Finished in 10ms at (2026-11-04 00:00:00)")
}

type graph575Fixture struct {
	root, dir string
	runner    tlc.Runner
	config    tlc.Config
}

func graph575Write(t *testing.T, file, contents string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(contents), 0700); err != nil {
		t.Fatal(err)
	}
}

func graph575Setup(t *testing.T) graph575Fixture {
	t.Helper()
	f := graph575Fixture{root: t.TempDir(), dir: t.TempDir()}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\\''") + "'"
	graph575Write(t, filepath.Join(f.root, "docker"), "#!/bin/sh\nGORACE=atexit_sleep_ms=0 INVARIANT_GRAPH575_DOCKER=1 exec "+quoted+" \"$@\"\n")
	graph575Write(t, filepath.Join(f.root, "tla2tools.jar"), "graph575 fake jar bytes\n")
	graph575Write(t, filepath.Join(f.root, "dot"), graph575DOT())
	graph575Write(t, filepath.Join(f.root, "transcript"), graph575Transcript(false))
	graph575Write(t, filepath.Join(f.root, "mode"), "pass")
	graph575Write(t, filepath.Join(f.dir, "Graph575.tla"), "---- MODULE Graph575 ----\n====\n")
	t.Setenv("INVARIANT_GRAPH575_ROOT", f.root)
	t.Setenv("PATH", f.root+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.runner = tlc.Runner{Image: toolchain.JavaImage, Jar: filepath.Join(f.root, "tla2tools.jar")}
	f.config = tlc.Config{Specification: "FairSpec", Constants: map[string]string{"Limit": "2", "Members": "{m1, m2}"}, Invariants: []string{"Safe"}, Properties: []string{"Answered"}}
	return f
}

func graph575Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func (f graph575Fixture) records(t *testing.T) []graph575Invocation {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(f.root, "*.run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var runs []graph575Invocation
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var r graph575Invocation
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
	}
	return runs
}

func graph575Await(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("fake Docker did not reach the required lifecycle boundary")
}

func TestGraph575CompletedRunEvidence(t *testing.T) {
	for _, duplicateInit := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate initial generation=%t", duplicateInit), func(t *testing.T) {
			f := graph575Setup(t)
			transcript := graph575Transcript(duplicateInit)
			graph575Write(t, filepath.Join(f.root, "transcript"), transcript)
			b, err := tlc.NewBudget(1, 1<<30, 1)
			if err != nil {
				t.Fatal(err)
			}
			f.runner.Budget = b
			g, err := f.runner.DumpGraph(graph575Context(t), f.dir, "Graph575", f.config)
			if err != nil || g == nil {
				t.Fatalf("completed dump: %+v, %v", g, err)
			}
			graph575AssertGraph(t, &g.Graph)
			if !reflect.DeepEqual(g.Run, tlc.Parse(transcript, 0)) || g.StateCount != 3 || g.StepCount != 7 || g.Run.StatesGenerated == int64(g.StepCount) {
				t.Errorf("run statistics and graph counts conflated or lost: %+v", g)
			}
			sum := sha256.Sum256([]byte("graph575 fake jar bytes\n"))
			if g.Tool.Image != f.runner.Image || g.Tool.JarSHA256 != hex.EncodeToString(sum[:]) || g.Module != "Graph575" || !reflect.DeepEqual(g.Config, f.config) {
				t.Errorf("incorrect provenance: %+v", g)
			}
			f.config.Constants["Limit"] = "99"
			f.config.Invariants[0] = "Changed"
			f.config.Properties[0] = "Changed"
			if g.Config.Constants["Limit"] != "2" || g.Config.Invariants[0] != "Safe" || g.Config.Properties[0] != "Answered" {
				t.Fatal("caller mutation rewrote returned evidence")
			}
			runs := f.records(t)
			if len(runs) != 1 {
				t.Fatalf("want one graph run, got %v", runs)
			}
			run := runs[0]
			args := "\n" + strings.Join(run.Args, "\n") + "\n"
			for _, part := range []string{"\n--network\nnone\n", "\n--memory\n768m\n", "\n--memory-swap\n768m\n", "\n-Xmx576m\n", "\n-workers\n1\n", "\n-tool\n", "\n-dump\ndot,actionlabels\n", "\n" + f.runner.Image + "\n"} {
				if !strings.Contains(args, part) {
					t.Errorf("graph command lost %q: %q", part, run.Args)
				}
			}
			if strings.Contains(args, "\n-deadlock\n") || strings.Contains(args, "\n-simulate\n") {
				t.Fatal("graph collection changed TLC checking semantics")
			}
			wantConfig := "SPECIFICATION FairSpec\nCONSTANT Limit = 2\nCONSTANT Members = {m1, m2}\nINVARIANT Safe\nPROPERTY Answered\n"
			if run.Config != wantConfig {
				t.Errorf("TLC received a different configuration: %q", run.Config)
			}
		})
	}
}

func TestGraph575RefusesIncompleteRuns(t *testing.T) {
	valid := graph575Transcript(false)
	without := func(code int) string {
		start := strings.Index(valid, fmt.Sprintf("@!@!@STARTMSG %d:", code))
		endMarker := fmt.Sprintf("@!@!@ENDMSG %d @!@!@\n", code)
		end := start + strings.Index(valid[start:], endMarker) + len(endMarker)
		return valid[:start] + valid[end:]
	}
	for _, tc := range []struct{ name, out, dot, mode string }{
		{"missing finish", without(2186), graph575DOT(), "pass"},
		{"missing success", without(2193), graph575DOT(), "pass"},
		{"missing version", without(2262), graph575DOT(), "pass"},
		{"missing stats", without(2199), graph575DOT(), "pass"},
		{"missing depth", without(2194), graph575DOT(), "pass"},
		{"missing initial stats", without(2190), graph575DOT(), "pass"},
		{"unfinished queue", strings.Replace(valid, "0 states left on queue", "1 states left on queue", 1), graph575DOT(), "pass"},
		{"wrong state count", strings.Replace(valid, "3 distinct states found", "4 distinct states found", 1), graph575DOT(), "pass"},
		{"wrong initial count", strings.Replace(valid, "2 distinct states generated", "1 distinct state generated", 1), graph575DOT(), "pass"},
		{"closed dump missing a step", valid, strings.Replace(graph575DOT(), graph575IDs[0]+" -> "+graph575IDs[2]+" [label=\"Alternate\",color=\"black\",fontcolor=\"black\"];\n", "", 1), "pass"},
		{"malformed statistics", strings.Replace(valid, "9 states generated", "oops states generated", 1), graph575DOT(), "pass"},
		{"truncated message", valid + "@!@!@STARTMSG 1000:1 @!@!@\nInterrupted", graph575DOT(), "pass"},
		{"mismatched frame", strings.Replace(valid, "@!@!@ENDMSG 2186", "@!@!@ENDMSG 2185", 1), graph575DOT(), "pass"},
		{"tool failure after success", valid + "@!@!@STARTMSG 1000:1 @!@!@\nInternal error\n@!@!@ENDMSG 1000 @!@!@\n", graph575DOT(), "pass"},
		{"SANY failure after success", valid + graph575Frame(3002, "Parsing or semantic analysis failed."), graph575DOT(), "pass"},
		{"memory failure after success", valid + graph575Frame(1001, "Java ran out of memory."), graph575DOT(), "pass"},
		{"nonzero exit", valid, graph575DOT(), "exit12"},
		{"killed container", valid, graph575DOT(), "exit137"},
		{"malformed DOT", valid, "strict digraph DiskGraph {", "pass"},
		{"no dump", valid, graph575DOT(), "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := graph575Setup(t)
			graph575Write(t, filepath.Join(f.root, "transcript"), tc.out)
			graph575Write(t, filepath.Join(f.root, "dot"), tc.dot)
			graph575Write(t, filepath.Join(f.root, "mode"), tc.mode)
			g, err := f.runner.DumpGraph(graph575Context(t), f.dir, "Graph575", f.config)
			if err == nil || g != nil {
				t.Fatalf("unfinished or failed run published graph evidence: %+v, %v", g, err)
			}
		})
	}
}

func TestGraph575IsolatesOutputAndKeepsDumpCompatible(t *testing.T) {
	f := graph575Setup(t)
	oldDOT := graph575DOT()
	graph575Write(t, filepath.Join(f.dir, "states.dot"), oldDOT)
	graph575Write(t, filepath.Join(f.dir, "Invariant.cfg"), "caller-owned configuration\n")
	ctx := graph575Context(t)
	g, err := f.runner.DumpGraph(ctx, f.dir, "Graph575", f.config)
	if err != nil || g == nil {
		t.Fatalf("first run: %+v, %v", g, err)
	}
	for _, mode := range []string{"missing", "symlink"} {
		graph575Write(t, filepath.Join(f.root, "mode"), mode)
		if g, err := f.runner.DumpGraph(ctx, f.dir, "Graph575", f.config); err == nil || g != nil {
			t.Fatalf("%s reused old output: %+v, %v", mode, g, err)
		}
	}
	// Dump remains the raw-DOT transport used by the dashboard and by existing
	// memory tests; it does not claim the GraphResult completeness guarantee.
	graph575Write(t, filepath.Join(f.root, "mode"), "pass")
	graph575Write(t, filepath.Join(f.root, "dot"), "digraph G {}\n")
	dot, err := f.runner.Dump(ctx, f.dir, "Graph575", f.config)
	if err != nil || dot != "digraph G {}\n" {
		t.Fatalf("legacy raw-DOT contract changed: %q, %v", dot, err)
	}
	graph575Write(t, filepath.Join(f.root, "mode"), "missing")
	if dot, err := f.runner.Dump(ctx, f.dir, "Graph575", f.config); err == nil || dot != "" {
		t.Fatalf("legacy Dump reused an earlier file: %q, %v", dot, err)
	}
	seen := map[string]bool{}
	for _, run := range f.records(t) {
		if seen[run.DumpPath] || run.DumpPath == filepath.Join(f.dir, "states.dot") || run.ConfigPath == filepath.Join(f.dir, "Invariant.cfg") {
			t.Fatalf("run paths were not isolated: %+v", run)
		}
		seen[run.DumpPath] = true
		if _, err := os.Lstat(run.DumpPath); !os.IsNotExist(err) {
			t.Errorf("completed run left dump output at %s: %v", run.DumpPath, err)
		}
	}
	for file, want := range map[string]string{"states.dot": oldDOT, "Invariant.cfg": "caller-owned configuration\n"} {
		got, err := os.ReadFile(filepath.Join(f.dir, file))
		if err != nil || string(got) != want {
			t.Errorf("dump changed caller file %s: %q, %v", file, got, err)
		}
	}
}

func TestGraph575AdmissionAndCancellation(t *testing.T) {
	f := graph575Setup(t)
	b, err := tlc.NewBudget(1, 1<<30, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.runner.Budget = b
	graph575Write(t, filepath.Join(f.root, "mode"), "hold")
	graph575Write(t, filepath.Join(f.root, "hold-rm"), "")
	ctx := graph575Context(t)
	cause := errors.New("reader graph canceled by caller")
	firstCtx, cancelFirst := context.WithCancelCause(ctx)
	t.Cleanup(func() {
		cancelFirst(cause)
		_ = os.WriteFile(filepath.Join(f.root, "release-rm"), nil, 0600)
		_ = os.WriteFile(filepath.Join(f.root, "release-run"), nil, 0600)
	})
	type result struct {
		g   *tlc.GraphResult
		err error
	}
	start := func(ctx context.Context) <-chan result {
		done := make(chan result, 1)
		go func() {
			g, err := f.runner.DumpGraph(ctx, f.dir, "Graph575", f.config)
			done <- result{g, err}
		}()
		return done
	}
	first := start(firstCtx)
	graph575Await(t, func() bool {
		matches, _ := filepath.Glob(filepath.Join(f.root, "*.ready"))
		return len(matches) == 1
	})
	firstRun := f.records(t)[0]

	// A canceled waiter must not launch a container or read the first one's
	// already complete-looking output, which the fake wrote before blocking.
	waitCtx, cancelWait := context.WithCancelCause(ctx)
	waiter := start(waitCtx)
	time.Sleep(100 * time.Millisecond)
	cancelWait(cause)
	select {
	case got := <-waiter:
		if got.g != nil || !errors.Is(got.err, cause) {
			t.Fatalf("queued cancellation: %+v, %v", got.g, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled admission wait did not finish")
	}
	if runs := f.records(t); len(runs) != 1 {
		t.Fatalf("shared budget admitted a second graph: %+v", runs)
	}

	cancelFirst(cause)
	graph575Await(t, func() bool {
		_, err := os.Stat(filepath.Join(f.root, firstRun.Name+".removing"))
		return err == nil
	})
	graph575Write(t, filepath.Join(f.root, "mode"), "pass")
	next := start(ctx)
	select {
	case got := <-first:
		t.Fatalf("graph returned before cleanup: %+v, %v", got.g, got.err)
	case <-time.After(150 * time.Millisecond):
	}
	if runs := f.records(t); len(runs) != 1 {
		t.Fatalf("released admission while canceled container still existed: %+v", runs)
	}
	graph575Write(t, filepath.Join(f.root, "release-rm"), "")
	select {
	case got := <-first:
		if got.g != nil || !errors.Is(got.err, cause) {
			t.Fatalf("running cancellation published evidence or lost cause: %+v, %v", got.g, got.err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("canceled graph did not finish cleanup")
	}
	select {
	case got := <-next:
		if got.err != nil || got.g == nil {
			t.Fatalf("admission was not released after cleanup: %+v, %v", got.g, got.err)
		}
		graph575AssertGraph(t, &got.g.Graph)
	case <-time.After(4 * time.Second):
		t.Fatal("next graph remained blocked after cleanup")
	}
	if _, err := os.Stat(filepath.Join(f.root, firstRun.Name+".removed")); err != nil {
		t.Fatal("canceled container was not removed")
	}
	if _, err := os.Lstat(firstRun.DumpPath); !os.IsNotExist(err) {
		t.Errorf("canceled run retained a dump: %v", err)
	}
	preCanceled, stop := context.WithCancelCause(ctx)
	stop(cause)
	before := len(f.records(t))
	if g, err := f.runner.DumpGraph(preCanceled, f.dir, "Graph575", f.config); g != nil || !errors.Is(err, cause) {
		t.Fatalf("pre-canceled graph: %+v, %v", g, err)
	}
	if len(f.records(t)) != before {
		t.Fatal("a pre-canceled graph launched Docker")
	}
}
