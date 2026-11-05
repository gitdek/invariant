package conformance

// These checks use the real TLC runner and Docker lifecycle. Only Docker's
// executable is replaced; the semantic oracle reads the generated modules.
// Existing s559/accept helpers supply the Counter semantics and trace encoding.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

func init() {
	if os.Getenv("INVARIANT_578_DOCKER") == "1" {
		os.Exit(split578Docker(os.Args[1:]))
	}
}

type split578Run struct {
	Name, Dir, Module, Kind, Config string
	Items                           []string
	States                          []int
	Memory, Swap, Heap              uint64
	Attempt                         int
	Outcome                         string
}

func split578Frame(code int, body string) string {
	return fmt.Sprintf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
}

func split578Arg(args []string, key string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func split578Bytes(s string) uint64 {
	mult := uint64(1)
	if len(s) > 0 {
		switch strings.ToLower(s[len(s)-1:]) {
		case "m":
			mult, s = 1<<20, s[:len(s)-1]
		case "g":
			mult, s = 1<<30, s[:len(s)-1]
		}
	}
	n, _ := strconv.ParseUint(s, 10, 64)
	return n * mult
}

// split578Lock runs fn holding an exclusive flock, which the kernel releases
// when a fake Docker process dies holding it, as a canceled batch's run is
// killed. A lock directory stayed behind then, and every later process
// waited out its ten seconds (#601).
func split578Lock(root string, fn func()) {
	f, err := os.OpenFile(filepath.Join(root, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		panic(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	fn()
}

// split578Write replaces name whole, so a process killed while writing it
// leaves the previous contents.
func split578Write(name string, b []byte) {
	if e := os.WriteFile(name+".tmp", b, 0600); e != nil {
		panic(e)
	}
	if e := os.Rename(name+".tmp", name); e != nil {
		panic(e)
	}
}

func split578Event(root, event string) {
	split578Lock(root, func() {
		f, err := os.OpenFile(filepath.Join(root, "events"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		fmt.Fprintln(f, event)
	})
}

func split578WaitFile(root, name string) {
	until := time.Now().Add(12 * time.Second)
	for {
		if _, e := os.Stat(filepath.Join(root, name)); e == nil {
			return
		}
		if time.Now().After(until) {
			panic("fake Docker barrier timed out: " + name)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func split578Save(root string, r split578Run) {
	b, _ := json.Marshal(r)
	split578Write(filepath.Join(root, r.Name+".json"), b)
}

func split578Docker(args []string) int {
	root := os.Getenv("SPLIT578_ROOT")
	if len(args) == 0 {
		return 90
	}
	if args[0] == "rm" {
		name := args[len(args)-1]
		split578Event(root, "removing "+name)
		if os.Getenv("SPLIT578_HOLD_RM") == "1" {
			split578WaitFile(root, "release-rm")
		}
		split578Event(root, "removed "+name)
		return 0
	}
	if args[0] != "run" {
		return 91
	}
	r := split578Run{Name: split578Arg(args, "--name"), Memory: split578Bytes(split578Arg(args, "--memory")), Swap: split578Bytes(split578Arg(args, "--memory-swap"))}
	for i, a := range args {
		if a == "-v" && i+1 < len(args) && strings.HasSuffix(args[i+1], ":/work") {
			r.Dir = strings.TrimSuffix(args[i+1], ":/work")
		}
		if strings.HasPrefix(a, "-Xmx") {
			r.Heap = split578Bytes(strings.TrimPrefix(a, "-Xmx"))
		}
		if strings.HasSuffix(a, ".tla") {
			r.Module = strings.TrimSuffix(filepath.Base(a), ".tla")
		}
	}
	if r.Name == "" || r.Dir == "" || r.Memory == 0 || r.Memory != r.Swap || r.Heap != r.Memory/(1<<20)*3/4*(1<<20) || split578Arg(args, "-workers") != "1" || split578Arg(args, "--network") != "none" {
		fmt.Fprintln(os.Stderr, "invalid Docker allocation or execution flags")
		return 92
	}
	cfgRaw, err := os.ReadFile(filepath.Join(r.Dir, "Invariant.cfg"))
	if err != nil {
		panic(err)
	}
	r.Config = string(cfgRaw)
	cfg := tlc.Config{}
	for _, line := range strings.Split(r.Config, "\n") {
		if strings.HasPrefix(line, "SPECIFICATION ") {
			cfg.Specification = strings.TrimPrefix(line, "SPECIFICATION ")
		}
		if strings.HasPrefix(line, "INVARIANT ") {
			cfg.Invariants = append(cfg.Invariants, strings.TrimPrefix(line, "INVARIANT "))
		}
	}
	var body string
	if r.Module != "Peer" {
		raw, e := os.ReadFile(filepath.Join(r.Dir, r.Module+".tla"))
		if e != nil {
			panic(e)
		}
		body = string(raw)
		for name, want := range split578Modules() {
			got, e := os.ReadFile(filepath.Join(r.Dir, name+".tla"))
			if e != nil || string(got) != want {
				fmt.Fprintln(os.Stderr, "missing or changed staged model", name)
				return 93
			}
		}
		switch cfg.Specification {
		case "Invariant_ISpec":
			r.Kind = "init"
			for _, n := range acceptValues(body) {
				r.Items = append(r.Items, strconv.Itoa(n))
			}
		case "Invariant_CSpec":
			r.Kind = "steps"
			r.States = acceptValues(acceptTuple(body, "Invariant_States"))
			run := acceptRun{}
			res, _ := acceptVerdict("steps", body, &run)
			if res.Outcome == tlc.Failed {
				fmt.Fprintln(os.Stderr, res.Message)
				return 94
			}
			r.Items = run.items
			used := map[int]bool{}
			for _, pair := range r.Items {
				var from, to int
				fmt.Sscanf(pair, "%d->%d", &from, &to)
				used[from], used[to] = true, true
			}
			if len(used) != len(r.States) {
				fmt.Fprintln(os.Stderr, "batch carried unrelated states")
				return 95
			}
		case "Invariant_ASpec":
			r.Kind = "strict"
			for _, row := range s559Rows.FindAllStringSubmatch(body, -1) {
				r.Items = append(r.Items, strings.Join(row[1:], "/"))
			}
		case "Invariant_TSpec":
			r.Kind = "tried"
			for _, n := range acceptValues(acceptTuple(body, "Invariant_States")) {
				r.Items = append(r.Items, strconv.Itoa(n))
			}
		default:
			return 96
		}
	}
	split578Lock(root, func() {
		key := fmt.Sprintf("node-%x", sha256.Sum256([]byte(r.Dir+"/"+r.Module)))
		raw, _ := os.ReadFile(filepath.Join(root, key))
		r.Attempt, _ = strconv.Atoi(string(raw))
		r.Attempt++
		split578Write(filepath.Join(root, key), []byte(strconv.Itoa(r.Attempt)))
		split578Save(root, r)
	})
	split578Event(root, "start "+r.Name)
	finish := func(outcome string) {
		r.Outcome = outcome
		// Publish completion atomically; barrier polling must never read a
		// partially written record while the fake Docker process is finishing.
		b, _ := json.Marshal(r)
		os.WriteFile(filepath.Join(root, r.Name+".done.tmp"), b, 0600)
		os.Rename(filepath.Join(root, r.Name+".done.tmp"), filepath.Join(root, r.Name+".done"))
		split578Event(root, "end "+r.Name)
	}
	if r.Module == "Peer" {
		split578WaitFile(root, "release-peer")
		finish("passed")
		fmt.Print(split578Frame(2193, "Model checking completed."))
		return 0
	}
	target := r.Kind == os.Getenv("SPLIT578_TARGET")
	threshold, _ := strconv.Atoi(os.Getenv("SPLIT578_THRESHOLD"))
	mode := os.Getenv("SPLIT578_MODE")
	ordinary := r.Memory == split578Bytes(os.Getenv("SPLIT578_ORDINARY"))
	if target {
		switch mode {
		case "killed":
			finish("killed")
			return 137
		case "semantic":
			finish("semantic")
			fmt.Print(split578Frame(3002, "Parsing or semantic analysis failed: OutOfMemoryError is a model name."))
			return 150
		case "quoted":
			finish("deadlock")
			fmt.Print(split578Frame(2114, "Deadlock reached.") + split578Frame(2217, "1: <Initial predicate>\n/\\ n = \"java.lang.OutOfMemoryError: Java heap space\"\n/\\ invariant_source = 1\n/\\ invariant_target = 2"))
			return 11
		}
		if mode == "stall-singleton" && len(r.Items) == 1 && r.Attempt != 2 || mode == "stall" {
			fmt.Println("unfinished attempt before silence")
			time.Sleep(30 * time.Second)
			return 97
		}
		exhausted := len(r.Items) > threshold && ordinary || mode == "persistent" || (mode == "singleton" || mode == "stall-singleton") && ordinary
		if exhausted {
			// Deliberately mix partial success and counterexample with exhaustion.
			fmt.Print(split578Frame(2193, "Model checking completed.") + split578Frame(2199, "999 states generated, 999 distinct states found.") + split578Frame(2110, "Invariant Unfinished is violated.") + split578Frame(2217, "1: <Initial predicate>\n/\\ n = 999"))
			diagnostic := os.Getenv("SPLIT578_DIAGNOSTIC")
			if diagnostic == "" {
				diagnostic = "java.lang.OutOfMemoryError: Java heap space\n"
			}
			fmt.Print(diagnostic)
			if mode == "memory-cleanup" {
				time.Sleep(30 * time.Second)
				return 98
			}
			finish("memory")
			return 1
		}
		if mode == "children-hold" {
			split578WaitFile(root, "release-children")
		}
	}
	res, err := s559Semantic(context.Background(), r.Dir, r.Module, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		finish("oracle error")
		return 99
	}
	if target && res.Outcome != tlc.Passed && os.Getenv("SPLIT578_ORDER") == "1" {
		// The first original batch's failure must wait for the third batch.
		bad := stateText(res.Trace[len(res.Trace)-1], []string{"n"})
		if bad == "n = 8" {
			split578WaitFile(root, "later-failed")
		} else if bad == "n = 4500" {
			os.WriteFile(filepath.Join(root, "later-failed"), nil, 0600)
		}
	}
	finish(string(res.Outcome))
	fmt.Print(split578Frame(2262, "TLC split fixture"))
	for _, p := range res.Printed {
		fmt.Println(p)
	}
	code, exit := 2193, 0
	message := "Model checking completed. No error has been found."
	switch res.Outcome {
	case tlc.Deadlock:
		code, exit, message = 2114, 11, "Deadlock reached."
	case tlc.Violated:
		inv := res.Invariant
		if inv == "" {
			inv = "Init"
		}
		code, exit, message = 2110, 12, "Invariant "+inv+" is violated."
	}
	fmt.Print(split578Frame(code, message))
	for _, st := range res.Trace {
		text := "1: <Initial predicate>"
		for _, v := range st.Vars {
			text += "\n/\\ " + v.Name + " = " + v.Value
		}
		fmt.Print(split578Frame(2217, text))
	}
	return exit
}

type split578Fixture struct {
	root   string
	runner tlc.Runner
}

func split578Modules() map[string]string {
	return map[string]string{
		"Counter": strings.Replace(acceptCounter, "Next == Inc", "Read(k) == n = k /\\ UNCHANGED n\nNext == Inc \\/ (\\E k \\in 0..1 : Read(k))", 1),
		"Limit":   acceptLimit,
	}
}

func split578Dir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range split578Modules() {
		if err := os.WriteFile(filepath.Join(dir, name+".tla"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func split578Setup(t *testing.T, kind string, capacity string, parallel int) split578Fixture {
	t.Helper()
	f := split578Fixture{root: t.TempDir()}
	b, e := tlc.NewBudget(8, 16<<30, parallel)
	if e != nil {
		t.Fatal(e)
	}
	switch capacity {
	case "unknown":
		b, e = tlc.NewBudget(0, 0, 0)
	case "small":
		b, e = tlc.NewBudget(8, 1<<30, 0)
	case "standalone":
		b = nil
	}
	if e != nil {
		t.Fatal(e)
	}
	ordinary := uint64(1 << 30)
	if b != nil {
		ordinary = b.MemoryBytes
	}
	f.runner = tlc.Runner{Image: "fake-java@sha256:578", Jar: filepath.Join(f.root, "tools.jar"), Budget: b, CheckRoot: f.root, SilenceTimeout: 5 * time.Second}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\\''") + "'"
	script := "#!/bin/sh\nGORACE=atexit_sleep_ms=0 INVARIANT_578_DOCKER=1 exec " + quoted + " \"$@\"\n"
	if e := os.WriteFile(filepath.Join(f.root, "docker"), []byte(script), 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", f.root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SPLIT578_ROOT", f.root)
	t.Setenv("SPLIT578_TARGET", kind)
	t.Setenv("SPLIT578_THRESHOLD", "2")
	t.Setenv("SPLIT578_ORDINARY", fmt.Sprint(ordinary))
	for _, k := range []string{"SPLIT578_MODE", "SPLIT578_DIAGNOSTIC", "SPLIT578_HOLD_RM", "SPLIT578_ORDER"} {
		t.Setenv(k, "")
	}
	return f
}

func (f split578Fixture) release(t *testing.T, name string) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(f.root, name), nil, 0600); e != nil {
		t.Fatal(e)
	}
}

func (f split578Fixture) runs(t *testing.T) []split578Run {
	t.Helper()
	raw, _ := os.ReadFile(filepath.Join(f.root, "events"))
	var out []split578Run
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "start ") {
			continue
		}
		name := strings.TrimPrefix(line, "start ")
		b, e := os.ReadFile(filepath.Join(f.root, name+".done"))
		if os.IsNotExist(e) {
			b, e = os.ReadFile(filepath.Join(f.root, name+".json"))
		}
		if e != nil {
			t.Fatal(e)
		}
		var r split578Run
		if e := json.Unmarshal(b, &r); e != nil {
			t.Fatal(e)
		}
		out = append(out, r)
	}
	return out
}

func split578Wait(t *testing.T, predicate func() bool, why string) {
	t.Helper()
	until := time.Now().Add(6 * time.Second)
	for !predicate() {
		if time.Now().After(until) {
			t.Fatal(why)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func split578Recording(t *testing.T, kind string, bad bool) ([]byte, *Model) {
	t.Helper()
	switch kind {
	case "steps":
		runs := [][]int{acceptUpTo(10)}
		if bad {
			runs = append(runs, []int{8, 3})
		}
		return acceptRuns(t, runs...), nil
	case "init":
		var runs [][]int
		for n := 0; n < 9; n++ {
			runs = append(runs, []int{n})
		}
		if bad {
			runs = append(runs, []int{-1})
		}
		return acceptRuns(t, runs...), nil
	case "tried":
		if bad {
			return acceptAttempts(t, 9, 8), s559IncModel()
		}
		return acceptAttempts(t, 9), s559IncModel()
	case "strict", "strict-refused":
		var attempts [][]any
		for i := 0; i < 9; i++ {
			k, outcome := i%2, map[string]any{"outcome": "accepted"}
			if k == 1 {
				outcome = map[string]any{"outcome": "refused", "refusal": map[string]any{"type": "x.Error", "attributes": map[string]any{}}}
			}
			if bad && i == 8 {
				k = 1 // accepted Read(1) in n=0 is disabled
				if kind == "strict-refused" {
					k = 0
					outcome = map[string]any{"outcome": "refused", "refusal": map[string]any{"type": "x.Error", "attributes": map[string]any{}}}
				}
			}
			attempts = append(attempts, []any{0, "Read", []any{k}, 0, outcome})
		}
		b, e := json.Marshal(map[string]any{"states": []any{map[string]any{"n": 0}}, "init": []int{0}, "attempts": attempts})
		if e != nil {
			t.Fatal(e)
		}
		return b, s559ReadModel()
	}
	t.Fatal("unknown fixture kind")
	return nil, nil
}

func split578Check(ctx context.Context, runner Runner, dir string, raw []byte, model *Model) (Result, error) {
	return CheckReader(ctx, runner, dir, "Counter", nil, []string{"n"}, 17, strings.NewReader(string(raw)), model)
}

func split578Target(t *testing.T, f split578Fixture, kind string) []split578Run {
	t.Helper()
	var out []split578Run
	for _, r := range f.runs(t) {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func split578OrdinaryTree(t *testing.T, f split578Fixture, kind string, want []string) {
	t.Helper()
	runs := split578Target(t, f, kind)
	if len(runs) < 3 {
		t.Fatalf("no parent and two children: %+v", runs)
	}
	normal := split578Bytes(os.Getenv("SPLIT578_ORDINARY"))
	var leaves []string
	for _, r := range runs {
		if r.Memory != normal || r.Swap != normal || r.Heap != normal/(1<<20)*3/4*(1<<20) {
			t.Fatalf("divisible conformance escalated: %+v", r)
		}
		if r.Outcome == string(tlc.Passed) {
			if len(r.Items) > 2 || len(r.Items) == 0 {
				t.Fatalf("non-leaf or empty completion: %+v", r)
			}
			leaves = append(leaves, r.Items...)
		}
		if r.Outcome != "memory" {
			continue
		}
		// Identify the two contiguous halves by their contents, not directory names.
		// Repeated strict rows may make halves indistinguishable; count and order
		// are still checked by the complete leaf multiset and history tests.
		n := len(r.Items)
		found := false
		for _, left := range runs {
			for _, right := range runs {
				if left.Name == right.Name || len(left.Items) == 0 || len(right.Items) == 0 || len(left.Items)+len(right.Items) != n {
					continue
				}
				if d := len(left.Items) - len(right.Items); d < -1 || d > 1 {
					continue
				}
				joined := append(append([]string{}, left.Items...), right.Items...)
				if reflect.DeepEqual(joined, r.Items) {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("OOM parent lacks contiguous balanced halves: %+v", r)
		}
	}
	sort.Strings(leaves)
	want = append([]string{}, want...)
	sort.Strings(want)
	if !reflect.DeepEqual(leaves, want) {
		t.Fatalf("leaf obligations lost or duplicated: got %v want %v", leaves, want)
	}
}

func TestConformance578OrdinaryHalves(t *testing.T) {
	acceptCPUs(t)
	for _, kind := range []string{"steps", "init", "strict", "tried"} {
		for _, capacity := range []string{"known", "unknown", "standalone"} {
			t.Run(kind+"/"+capacity, func(t *testing.T) {
				f := split578Setup(t, kind, capacity, 2)
				raw, model := split578Recording(t, kind, false)
				want, e := split578Check(t.Context(), s559Runner(s559Semantic), split578Dir(t), raw, model)
				if e != nil || !want.Passed || want.Tried != nil && !want.Tried.Passed {
					t.Fatalf("reference failed: %+v %v", want, e)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				peer := make(chan error, 1)
				if capacity == "known" {
					dir := t.TempDir()
					go func() { _, e := f.runner.Check(ctx, dir, "Peer", tlc.Config{Specification: "Spec"}); peer <- e }()
					defer func() { cancel(); <-peer }()
					split578Wait(t, func() bool {
						for _, r := range f.runs(t) {
							if r.Module == "Peer" {
								return true
							}
						}
						return false
					}, "peer did not start")
				}
				dir := split578Dir(t)
				type outcome struct {
					r Result
					e error
				}
				done := make(chan outcome, 1)
				go func() { r, e := split578Check(ctx, f.runner, dir, raw, model); done <- outcome{r, e} }()
				var got outcome
				select {
				case got = <-done:
				case <-time.After(4 * time.Second):
					cancel()
					<-done
					t.Fatal("split work waited for the unrelated ordinary allocation or deadlocked")
				}
				if got.e != nil || !reflect.DeepEqual(got.r, want) {
					t.Fatalf("split changed semantics: %+v %v want %+v", got.r, got.e, want)
				}
				runs := split578Target(t, f, kind)
				if len(runs) == 0 {
					t.Fatal("no target check")
				}
				split578OrdinaryTree(t, f, kind, runs[0].Items)
				if capacity == "known" {
					f.release(t, "release-peer")
				}
				entries, e := os.ReadDir(dir)
				if e != nil || len(entries) != len(acceptModules) {
					t.Fatalf("generated modules leaked: %v %v", entries, e)
				}
			})
		}
	}
}

func TestConformance578OriginalDiagnostics(t *testing.T) {
	for _, kind := range []string{"steps", "init", "strict", "strict-refused", "tried"} {
		t.Run(kind, func(t *testing.T) {
			target := kind
			if target == "strict-refused" {
				target = "strict"
			}
			f := split578Setup(t, target, "known", 2)
			raw, model := split578Recording(t, kind, true)
			want, e := split578Check(t.Context(), s559Runner(s559Semantic), split578Dir(t), raw, model)
			if e != nil {
				t.Fatal(e)
			}
			got, e := split578Check(t.Context(), f.runner, split578Dir(t), raw, model)
			if e != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("wrong original obligation: %+v tried=%+v %v; want %+v tried=%+v", got, got.Tried, e, want, want.Tried)
			}
			runs := split578Target(t, f, target)
			if len(runs) < 3 {
				t.Fatalf("failure was not recovered through children: %+v", runs)
			}
			for _, r := range runs {
				if r.Memory != 2<<30 {
					t.Fatalf("bad descendant was checked by escalation: %+v", r)
				}
			}
			if strings.Contains(got.Message, "999") || got.BadStart == "n = 999" {
				t.Fatal("exhausted parent became evidence")
			}
		})
	}
}

func TestConformance578EarliestFailureAndBoundedScheduling(t *testing.T) {
	acceptCPUs(t)
	f := split578Setup(t, "steps", "known", 3)
	t.Setenv("SPLIT578_THRESHOLD", "600")
	t.Setenv("SPLIT578_ORDER", "1")
	raw := acceptRuns(t, acceptUpTo(6001), []int{8, 3}, []int{4500, 4490})
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	got, e := split578Check(ctx, f.runner, split578Dir(t), raw, nil)
	if e != nil || got.BadStep == nil || *got.BadStep != (Step{From: "n = 8", To: "n = 3"}) {
		t.Fatalf("earlier recovering batch was canceled or misindexed: %+v %v", got, e)
	}
	if _, e := os.Stat(filepath.Join(f.root, "later-failed")); e != nil {
		t.Fatal("later failure did not finish first")
	}
	runs := f.runs(t)
	byName := map[string]split578Run{}
	parents := 0
	for _, r := range runs {
		byName[r.Name] = r
		if r.Kind == "steps" && r.Outcome == "memory" {
			parents++
		}
		if r.Memory != 2<<30 {
			t.Fatalf("recovery took exclusive memory: %+v", r)
		}
	}
	if parents < 2 {
		t.Fatalf("not enough independent recovering batches: %d", parents)
	}
	data, _ := os.ReadFile(filepath.Join(f.root, "events"))
	active := map[string]bool{}
	peak := 0
	for _, line := range strings.Split(string(data), "\n") {
		p := strings.Fields(line)
		if len(p) != 2 {
			continue
		}
		switch p[0] {
		case "start":
			active[p[1]] = true
			peak = max(peak, len(active))
		case "end", "removed":
			delete(active, p[1])
		}
		if len(active) > 3 {
			t.Fatalf("unbounded recovery: %d active", len(active))
		}
	}
	if peak < 2 || len(active) != 0 {
		t.Fatalf("lost concurrency or leaked checks: peak=%d active=%v runs=%v", peak, active, byName)
	}
}

func TestConformance578ClassificationAndSingletons(t *testing.T) {
	for _, diagnostic := range []string{"java.lang.OutOfMemoryError: Java heap space\n", "GC overhead limit exceeded\n", split578Frame(1001, "Java ran out of memory."), split578Frame(1002, "Too many initial states."), split578Frame(1003, "Liveness ran out of memory."), split578Frame(1000, "The exception was a java.util.concurrent.ExecutionException\n: java.lang.OutOfMemoryError: GC overhead limit exceeded"), strings.ReplaceAll(split578Frame(1000, "The exception was a java.util.concurrent.ExecutionException: java.lang.OutOfMemoryError: Java heap space"), "\n", "\r\n")} {
		t.Run(fmt.Sprintf("confirmed-%x", sha256.Sum256([]byte(diagnostic))), func(t *testing.T) {
			f := split578Setup(t, "steps", "known", 2)
			t.Setenv("SPLIT578_DIAGNOSTIC", diagnostic)
			raw, model := split578Recording(t, "steps", false)
			r, e := split578Check(t.Context(), f.runner, split578Dir(t), raw, model)
			if e != nil || !r.Passed {
				t.Fatalf("classified exhaustion did not recover: %+v %v", r, e)
			}
			runs := split578Target(t, f, "steps")
			split578OrdinaryTree(t, f, "steps", runs[0].Items)
		})
	}
	for _, mode := range []string{"killed", "semantic", "quoted", "stall"} {
		t.Run("not-memory/"+mode, func(t *testing.T) {
			f := split578Setup(t, "steps", "known", 2)
			t.Setenv("SPLIT578_MODE", mode)
			f.runner.SilenceTimeout = 300 * time.Millisecond
			raw, model := split578Recording(t, "steps", false)
			r, e := split578Check(t.Context(), f.runner, split578Dir(t), raw, model)
			if e != nil || r.Passed {
				t.Fatalf("non-memory failure accepted: %+v %v", r, e)
			}
			want := 1
			if mode == "stall" {
				want = 2
			}
			if got := len(split578Target(t, f, "steps")); got != want {
				t.Fatalf("non-memory outcome split or escalated: %d attempts want %d", got, want)
			}
		})
	}
	// A legacy stand-in returning matching prose has no classified capability.
	t.Run("message is not a capability", func(t *testing.T) {
		calls := 0
		runner := s559Runner(func(_ context.Context, _ string, m string, _ tlc.Config) (tlc.Result, error) {
			if m == "Invariant_ConformanceInit" {
				return tlc.Result{Outcome: tlc.Passed}, nil
			}
			calls++
			return tlc.Result{Outcome: tlc.Failed, Message: "TLC check Invariant_Conformance exhausted Java memory"}, nil
		})
		raw, model := split578Recording(t, "steps", false)
		r, e := split578Check(t.Context(), runner, split578Dir(t), raw, model)
		if e != nil || r.Passed || calls != 1 {
			t.Fatalf("message inferred recovery: %+v %v calls=%d", r, e, calls)
		}
	})
	for _, mode := range []string{"singleton", "persistent"} {
		for _, capacity := range []string{"known", "unknown", "small", "standalone"} {
			t.Run(mode+"/"+capacity, func(t *testing.T) {
				f := split578Setup(t, "steps", capacity, 2)
				t.Setenv("SPLIT578_MODE", mode)
				t.Setenv("SPLIT578_THRESHOLD", "1")
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				peer := make(chan error, 1)
				if capacity == "known" {
					d := t.TempDir()
					go func() { _, e := f.runner.Check(ctx, d, "Peer", tlc.Config{Specification: "Spec"}); peer <- e }()
					defer func() { cancel(); <-peer }()
					split578Wait(t, func() bool {
						for _, r := range f.runs(t) {
							if r.Module == "Peer" {
								return true
							}
						}
						return false
					}, "peer did not start")
				}
				dir := split578Dir(t)
				raw := acceptRuns(t, acceptUpTo(4))
				type outcome struct {
					r Result
					e error
				}
				done := make(chan outcome, 1)
				go func() { r, e := split578Check(ctx, f.runner, dir, raw, nil); done <- outcome{r, e} }()
				if capacity == "known" {
					deadline := time.Now().Add(3 * time.Second)
					for time.Now().Before(deadline) {
						found := false
						for _, r := range split578Target(t, f, "steps") {
							if len(r.Items) == 1 && r.Outcome == "memory" {
								found = true
							}
						}
						if found {
							break
						}
						time.Sleep(5 * time.Millisecond)
					}
					found := false
					for _, r := range split578Target(t, f, "steps") {
						if r.Memory > 2<<30 {
							t.Error("exclusive retry overlapped active work")
						}
						if len(r.Items) == 1 && r.Outcome == "memory" {
							found = true
						}
					}
					f.release(t, "release-peer")
					if !found {
						cancel()
						<-done
						t.Fatal("divisible parent escalated before reaching a singleton")
					}
				}
				got := <-done
				pass := mode == "singleton" && capacity == "known"
				if got.e != nil || got.r.Passed != pass {
					t.Fatalf("singleton outcome: %+v %v want pass=%v", got.r, got.e, pass)
				}
				groups := map[string][]split578Run{}
				for _, r := range split578Target(t, f, "steps") {
					groups[r.Dir] = append(groups[r.Dir], r)
				}
				large := 0
				for _, g := range groups {
					for i, r := range g {
						if r.Memory > 2<<30 {
							large++
							if len(r.Items) != 1 || i != 1 || r.Memory != 12<<30 || r.Heap != 9<<30 || r.Config != g[0].Config || !reflect.DeepEqual(r.Items, g[0].Items) {
								t.Fatalf("invalid singleton retry: %+v", g)
							}
						}
					}
					if len(g) > 2 {
						t.Fatalf("unbounded identical retry: %+v", g)
					}
				}
				if capacity == "known" && large == 0 {
					t.Fatal("singleton never got its one exclusive attempt")
				}
				if capacity != "known" && large != 0 {
					t.Fatal("unknown capacity or absent headroom expanded")
				}
				if !pass && got.r.BadStep != nil {
					t.Fatal("persistent exhaustion supplied a counterexample")
				}
			})
		}
	}
}

func TestConformance578RecoveryCleanupAndCancellation(t *testing.T) {
	for _, cancelDuringCleanup := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup/cancel=%v", cancelDuringCleanup), func(t *testing.T) {
			f := split578Setup(t, "steps", "known", 2)
			t.Setenv("SPLIT578_MODE", "memory-cleanup")
			t.Setenv("SPLIT578_HOLD_RM", "1")
			f.runner.SilenceTimeout = 300 * time.Millisecond
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			type outcome struct {
				r Result
				e error
			}
			done := make(chan outcome, 1)
			joined := false
			t.Cleanup(func() {
				cancel()
				f.release(t, "release-rm")
				if !joined {
					<-done
				}
			})
			dir := split578Dir(t)
			raw, model := split578Recording(t, "steps", false)
			go func() { r, e := split578Check(ctx, f.runner, dir, raw, model); done <- outcome{r, e} }()
			split578Wait(t, func() bool {
				b, _ := os.ReadFile(filepath.Join(f.root, "events"))
				return strings.Contains(string(b), "removing ")
			}, "OOM container was not cleaned up")
			if len(split578Target(t, f, "steps")) != 1 {
				t.Fatal("children started before parent cleanup")
			}
			if cancelDuringCleanup {
				cancel()
			}
			f.release(t, "release-rm")
			got := <-done
			joined = true
			if cancelDuringCleanup {
				if !errors.Is(got.e, context.Canceled) || got.r.Passed || len(split578Target(t, f, "steps")) != 1 {
					t.Fatalf("cleanup cancellation launched recovery: %+v %v", got.r, got.e)
				}
			} else {
				if got.e != nil || !got.r.Passed {
					t.Fatalf("cleanup recovery failed: %+v %v", got.r, got.e)
				}
				runs := split578Target(t, f, "steps")
				split578OrdinaryTree(t, f, "steps", runs[0].Items)
				for _, r := range runs {
					if r.Memory != 2<<30 {
						t.Fatal("cleanup recovery escalated divisible work")
					}
				}
			}
			cancel()
			entries, e := os.ReadDir(dir)
			if e != nil || len(entries) != len(acceptModules) {
				t.Fatalf("recovery staging leaked: %v %v", entries, e)
			}
		})
	}
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("child-cutoff/deadline=%v", deadline), func(t *testing.T) {
			f := split578Setup(t, "steps", "known", 2)
			t.Setenv("SPLIT578_MODE", "children-hold")
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			ctx, cancel := context.WithTimeout(t.Context(), 1200*time.Millisecond)
			defer cancel()
			raw, model := split578Recording(t, "steps", false)
			dir := split578Dir(t)
			type outcome struct {
				r Result
				e error
			}
			done := make(chan outcome, 1)
			go func() { r, e := split578Check(ctx, f.runner, dir, raw, model); done <- outcome{r, e} }()
			if !deadline {
				until := time.Now().Add(time.Second)
				for time.Now().Before(until) && len(split578Target(t, f, "steps")) < 3 {
					time.Sleep(5 * time.Millisecond)
				}
				cancel()
			}
			got := <-done
			want := error(context.Canceled)
			if deadline {
				want = context.DeadlineExceeded
			}
			if !errors.Is(got.e, want) || got.r.Passed {
				t.Fatalf("cutoff renewed allowance or supplied evidence: %+v %v", got.r, got.e)
			}
			runs := split578Target(t, f, "steps")
			if len(runs) < 3 {
				t.Fatalf("never exercised a split tree before cancellation: %+v", runs)
			}
			for _, r := range runs {
				if r.Memory != 2<<30 {
					t.Fatalf("cutoff used exclusive memory: %+v", r)
				}
			}
			before := len(f.runs(t))
			time.Sleep(30 * time.Millisecond)
			if len(f.runs(t)) != before {
				t.Fatal("children started after return")
			}
			entries, e := os.ReadDir(tmp)
			if e != nil || len(entries) != 0 {
				t.Fatalf("disk store leaked: %v %v", entries, e)
			}
			data, _ := os.ReadFile(filepath.Join(f.root, "events"))
			for _, r := range runs {
				if r.Outcome == "" && !strings.Contains(string(data), "removed "+r.Name) {
					t.Fatalf("unjoined child cleanup: %+v", r)
				}
			}
		})
	}
	t.Run("singleton escalation keeps spent stall retry", func(t *testing.T) {
		f := split578Setup(t, "steps", "known", 2)
		t.Setenv("SPLIT578_MODE", "stall-singleton")
		t.Setenv("SPLIT578_THRESHOLD", "1")
		f.runner.SilenceTimeout = 300 * time.Millisecond
		r, e := split578Check(t.Context(), f.runner, split578Dir(t), acceptRuns(t, acceptUpTo(4)), nil)
		if e != nil || r.Passed || !strings.Contains(r.Message, "stopped making progress") {
			t.Fatalf("stall retry renewed: %+v %v", r, e)
		}
		groups := map[string][]split578Run{}
		for _, r := range split578Target(t, f, "steps") {
			groups[r.Dir] = append(groups[r.Dir], r)
		}
		found := false
		for _, g := range groups {
			if len(g[0].Items) > 1 {
				if len(g) != 1 {
					t.Fatalf("divisible work escalated: %+v", g)
				}
				continue
			}
			if len(g) == 3 {
				found = true
				if g[0].Memory != 2<<30 || g[1].Memory != 2<<30 || g[2].Memory != 12<<30 || g[0].Config != g[2].Config {
					t.Fatalf("wrong stall/memory sequence: %+v", g)
				}
			}
			if len(g) > 3 {
				t.Fatal("singleton renewed its stall allowance")
			}
		}
		if !found {
			t.Fatal("no split singleton exhausted after its stall retry")
		}
	})
}
