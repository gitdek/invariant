package verify_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// The executable stands in for Docker at its command boundary. No daemon,
// JVM, Python interpreter or network is needed, and every launched process is
// owned by the runner. Cross-process event writes use a directory lock.
func init() {
	if os.Getenv("INVARIANT_TLC497_DOCKER") == "1" {
		os.Exit(tlc497Docker(os.Args[1:]))
	}
}

func tlc497Locked(root string, fn func()) {
	lock := filepath.Join(root, "lock")
	until := time.Now().Add(15 * time.Second)
	for {
		if err := os.Mkdir(lock, 0700); err == nil {
			break
		}
		if time.Now().After(until) {
			panic("fake Docker event lock timed out")
		}
		time.Sleep(time.Millisecond)
	}
	defer os.Remove(lock)
	fn()
}

func tlc497Event(root, line string) {
	tlc497Locked(root, func() {
		f, err := os.OpenFile(filepath.Join(root, "events"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		if _, err := fmt.Fprintln(f, line); err != nil {
			panic(err)
		}
	})
}

func tlc497Frame(code int, body string) string {
	return fmt.Sprintf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
}

func tlc497Docker(args []string) int {
	root := os.Getenv("TLC497_ROOT")
	if len(args) == 0 {
		return 90
	}
	switch args[0] {
	case "info":
		tlc497Event(root, "info")
		fmt.Println(os.Getenv("TLC497_INFO"))
		return 0
	case "image", "inspect", "pull":
		return 0
	case "rm":
		name := args[len(args)-1]
		tlc497Event(root, "removing "+name)
		if d, err := time.ParseDuration(os.Getenv("TLC497_REMOVE_DELAY")); err == nil {
			time.Sleep(d)
		}
		tlc497Event(root, "end "+name)
		return 0
	case "run":
	default:
		return 91
	}
	var name, work, output, module string
	for i, arg := range args {
		if arg == "--name" && i+1 < len(args) {
			name = args[i+1]
		}
		if arg == "-v" && i+1 < len(args) {
			mount := args[i+1]
			if strings.HasSuffix(mount, ":/work") {
				work = strings.TrimSuffix(mount, ":/work")
			}
			if strings.HasSuffix(mount, ":/out") {
				output = strings.TrimSuffix(mount, ":/out")
			}
		}
		if strings.HasSuffix(arg, ".tla") {
			module = strings.TrimSuffix(filepath.Base(arg), ".tla")
		}
	}
	if module == "" {
		// The ordinary TypeScript build/driver writes sampled traces.
		if output == "" {
			return 92
		}
		data, err := os.ReadFile(filepath.Join(root, "traces.json"))
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(output, "traces.json"), data, 0600); err != nil {
			panic(err)
		}
		fmt.Print("@@invariant test=0\n@@invariant conform=0\n")
		return 0
	}
	if name == "" || work == "" {
		return 93
	}
	raw, _ := json.Marshal(args)
	if err := os.WriteFile(filepath.Join(root, name+".json"), raw, 0600); err != nil {
		panic(err)
	}
	config, err := os.ReadFile(filepath.Join(work, "Invariant.cfg"))
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(root, name+".cfg"), config, 0600); err != nil {
		panic(err)
	}
	tlc497Event(root, "start "+name+" "+module)
	attempt := 0
	tlc497Locked(root, func() {
		file := filepath.Join(root, "attempt-"+module)
		b, _ := os.ReadFile(file)
		attempt, _ = strconv.Atoi(string(b))
		attempt++
		if err := os.WriteFile(file, []byte(strconv.Itoa(attempt)), 0600); err != nil {
			panic(err)
		}
	})
	if module == "Hold" {
		for {
			if _, err := os.Stat(filepath.Join(root, "release")); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if module == "Stall" && attempt == 1 {
		fmt.Println("first attempt reached its initial state")
		time.Sleep(30 * time.Second)
	}
	delay := 40 * time.Millisecond
	if d, err := time.ParseDuration(os.Getenv("TLC497_DELAY")); err == nil {
		delay = d
	}
	time.Sleep(delay)
	tlc497Event(root, "end "+name)
	if module == os.Getenv("TLC497_FAIL") {
		if os.Getenv("TLC497_KILLED") == "1" {
			return 137
		}
		fmt.Print("java.lang.OutOfMemoryError: GC overhead limit exceeded\n")
		return 1
	}
	if module == "Transcript" {
		data, err := os.ReadFile(filepath.Join(root, "transcript"))
		if err != nil {
			panic(err)
		}
		fmt.Print(string(data))
		n, _ := strconv.Atoi(os.Getenv("TLC497_EXIT"))
		return n
	}
	if module == "Dump" {
		if err := os.WriteFile(filepath.Join(work, "states.dot"), []byte("digraph G {}\n"), 0600); err != nil {
			panic(err)
		}
	}
	fmt.Print(tlc497Frame(2262, "TLC test version"))
	violated := ""
	switch {
	case strings.HasPrefix(module, "Witness_"):
		violated = "Invariant_Never_" + strings.TrimPrefix(module, "Witness_")
	case module == "Bug_B1":
		violated = "Safe"
	case module == "Bug_B2":
		fmt.Print(tlc497Frame(2116, "Temporal properties were violated.") +
			tlc497Frame(2217, "1: <Initial predicate>\n/\\ n = 0") + tlc497Frame(2218, "2: Stuttering"))
		return 13
	}
	if violated != "" {
		fmt.Print(tlc497Frame(2110, "Invariant "+violated+" is violated.") + tlc497Frame(2217, "1: <Initial predicate>\n/\\ n = 0"))
		return 12
	}
	fmt.Print(tlc497Frame(2193, "Model checking completed. No error has been found.") +
		tlc497Frame(2199, "12004 states generated, 6002 distinct states found, 0 states left on queue.") +
		tlc497Frame(2194, "The depth of the complete state graph search is 6002."))
	return 0
}

type tlc497Fixture struct {
	root string
	tc   toolchain.Toolchain
}

func tlc497Write(t *testing.T, file string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0755); err != nil {
		t.Fatal(err)
	}
}

func tlc497Setup(t *testing.T) tlc497Fixture {
	t.Helper()
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\\''") + "'"
	tlc497Write(t, filepath.Join(root, "docker"), []byte("#!/bin/sh\nGORACE=atexit_sleep_ms=0 INVARIANT_TLC497_DOCKER=1 exec "+quoted+" \"$@\"\n"))
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TLC497_ROOT", root)
	t.Setenv("TLC497_INFO", `{"NCPU":8,"MemTotal":17179869184}`)
	for _, key := range []string{"TLC497_FAIL", "TLC497_KILLED", "TLC497_EXIT", "TLC497_REMOVE_DELAY", "TLC497_DELAY"} {
		t.Setenv(key, "")
	}
	var states []map[string]any
	for n := 0; n <= 6001; n++ {
		states = append(states, map[string]any{"n": n})
	}
	traces, err := json.Marshal(map[string]any{"traces": []any{states}})
	if err != nil {
		t.Fatal(err)
	}
	tlc497Write(t, filepath.Join(root, "traces.json"), traces)
	return tlc497Fixture{root, toolchain.Toolchain{JavaImage: "test-java", TLCJar: filepath.Join(root, "tlc.jar"), NodeImage: "test-node"}}
}

func (f tlc497Fixture) runner(budget *tlc.Budget) tlc.Runner {
	return tlc.Runner{Image: f.tc.JavaImage, Jar: f.tc.TLCJar, Budget: budget}
}

func tlc497Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func tlc497Budget(t *testing.T, slots int) *tlc.Budget {
	t.Helper()
	b, err := tlc.NewBudget(8, 16<<30, slots)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f tlc497Fixture) events(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, "events"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (f tlc497Fixture) waitStart(t *testing.T, module string) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		for _, line := range f.events(t) {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "start" && fields[2] == module {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never started: %v", module, f.events(t))
}

func (f tlc497Fixture) runs(t *testing.T) (peak int, modules map[string]int, names []string) {
	t.Helper()
	active := map[string]bool{}
	modules = map[string]int{}
	for _, line := range f.events(t) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "start":
			if active[fields[1]] {
				t.Fatalf("container name reused: %s", line)
			}
			active[fields[1]] = true
			modules[fields[2]]++
			names = append(names, fields[1])
			if len(active) > peak {
				peak = len(active)
			}
		case "end":
			delete(active, fields[1])
		}
	}
	if len(active) != 0 {
		t.Fatalf("containers still active: %v", active)
	}
	return
}

func tlc497Size(t *testing.T, s string) uint64 {
	t.Helper()
	mult := uint64(1)
	if len(s) > 0 {
		switch strings.ToLower(s[len(s)-1:]) {
		case "k":
			mult = 1 << 10
			s = s[:len(s)-1]
		case "m":
			mult = 1 << 20
			s = s[:len(s)-1]
		case "g":
			mult = 1 << 30
			s = s[:len(s)-1]
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatalf("invalid memory size %q: %v", s, err)
	}
	return n * mult
}

func (f tlc497Fixture) limits(t *testing.T, names []string, memory, heap uint64) {
	t.Helper()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(f.root, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var args []string
		if err := json.Unmarshal(data, &args); err != nil {
			t.Fatal(err)
		}
		values := map[string]string{}
		for i, a := range args {
			if strings.HasPrefix(a, "-Xmx") {
				values["heap"] = strings.TrimPrefix(a, "-Xmx")
			}
			for _, flag := range []string{"--memory", "--memory-swap"} {
				if a == flag && i+1 < len(args) {
					values[flag] = args[i+1]
				}
				if strings.HasPrefix(a, flag+"=") {
					values[flag] = strings.TrimPrefix(a, flag+"=")
				}
			}
		}
		for key, want := range map[string]uint64{"--memory": memory, "--memory-swap": memory, "heap": heap} {
			got, ok := values[key]
			if !ok {
				t.Errorf("TLC container lacks %s: %v", key, args)
				continue
			}
			if n := tlc497Size(t, got); n != want {
				t.Errorf("%s = %d, want %d", key, n, want)
			}
		}
	}
}

func tlc497Project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := "---- MODULE Model ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nvars == <<n>>\nInit == n = 0\nNext == n' = (n + 1) % (Limit + 1)\nSpec == Init /\\ [][Next]_vars\nSafe == n \\in 0..Limit\n"
	statements := []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}}
	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("W%d", i)
		source += name + " == n = 1\n"
		statements = append(statements, project.Statement{Name: name, Kind: project.Witness})
	}
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("P%d", i)
		source += name + " == []<>(n = 1)\n"
		statements = append(statements, project.Statement{Name: name, Kind: project.Property})
	}
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("F%d", i)
		source += name + " == WF_vars(Next)\n"
		statements = append(statements, project.Statement{Name: name, Kind: project.Fairness})
	}
	source += "B1 == n' = Limit + 1\nB2 == n' = 2\n====\n"
	statements = append(statements, project.Statement{Name: "B1", Kind: project.Bug, Expect: "Safe"}, project.Statement{Name: "B2", Kind: project.Bug, Expect: "P1"})
	for i := range statements {
		h, err := tla.PinHash(source, statements[i].Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		statements[i].SHA256, statements[i].Says = h, statements[i].Name
	}
	manifest := project.Manifest{Name: "TLC budget fixture", Module: ".invariant/specs/Model.tla", Code: "src", Language: "typescript", Conformance: "conformance.ts"}
	lock := project.Lock{Decision: "test fixture", Bounds: map[string]string{"Limit": "6001"}, Statements: statements}
	m, _ := json.Marshal(manifest)
	l, _ := json.Marshal(lock)
	for name, data := range map[string][]byte{".invariant/invariant.json": m, ".invariant/ratified.lock": l, ".invariant/specs/Model.tla": []byte(source), "src/machine.ts": []byte("export const next = (n: number) => (n+1)%3;\n"), "conformance.ts": []byte("// The fake runtime supplies recorded observations.\n")} {
		tlc497Write(t, filepath.Join(dir, name), data)
	}
	return dir
}

func tlc497AllModelResults(t *testing.T, r *verify.Report, modules map[string]int) {
	t.Helper()
	if len(r.Witnesses) != 6 || len(r.Properties) != 3 || len(r.Fairness) != 2 || len(r.Bugs) != 2 || r.Design.Spec != "Spec" {
		t.Fatalf("missing model results: %+v", r)
	}
	want := []string{"Model", "Bug_B1", "Bug_B2"}
	for i := 1; i <= 6; i++ {
		want = append(want, fmt.Sprintf("Witness_W%d", i))
	}
	for i := 1; i <= 3; i++ {
		want = append(want, fmt.Sprintf("Property_P%d", i))
	}
	for i := 1; i <= 2; i++ {
		want = append(want, fmt.Sprintf("Fairness_F%d", i))
	}
	for _, name := range want {
		if modules[name] != 1 {
			t.Errorf("%s ran %d times, want once", name, modules[name])
		}
	}
	for i, w := range r.Witnesses {
		if w.Name != fmt.Sprintf("W%d", i+1) {
			t.Errorf("witness order changed: %v", r.Witnesses)
		}
	}
	for i, p := range r.Properties {
		if p.Name != fmt.Sprintf("P%d", i+1) {
			t.Errorf("property order changed: %v", r.Properties)
		}
	}
	for i, f := range r.Fairness {
		if f.Name != fmt.Sprintf("F%d", i+1) {
			t.Errorf("fairness order changed: %v", r.Fairness)
		}
	}
}

func TestTLC497ResourceBudget(t *testing.T) {
	for _, c := range []struct {
		name           string
		cpus           int
		memory         uint64
		request, slots int
		cap, heap      uint64
	}{
		{"memory", 8, 16 << 30, 0, 6, 2 << 30, 1536 << 20},
		{"cpu", 2, 64 << 30, 0, 2, 2 << 30, 1536 << 20},
		{"lower", 8, 16 << 30, 2, 2, 2 << 30, 1536 << 20},
		{"cannot raise", 8, 4 << 30, 99, 1, 2 << 30, 1536 << 20},
		{"small machine", 8, 1 << 30, 0, 1, 768 << 20, 576 << 20},
		{"unknown memory", 8, 0, 8, 1, 1 << 30, 768 << 20},
		{"unknown cpu", 0, 16 << 30, 8, 1, 1 << 30, 768 << 20},
		{"unknown both", 0, 0, 0, 1, 1 << 30, 768 << 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := tlc.NewBudget(c.cpus, c.memory, c.request)
			if err != nil {
				t.Fatal(err)
			}
			if b.Parallel != c.slots || b.MemoryBytes != c.cap || b.HeapBytes != c.heap {
				t.Fatalf("budget = %+v, want slots=%d memory=%d heap=%d", b, c.slots, c.cap, c.heap)
			}
		})
	}
	if _, err := tlc.NewBudget(8, 16<<30, -1); err == nil {
		t.Error("negative limit accepted")
	}
	if _, err := tlc.NewBudget(8, 128<<20, 0); err == nil {
		t.Error("insufficient known memory accepted")
	}
}

func TestTLC497GateBoundsEveryCheck(t *testing.T) {
	for _, modelOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(modelOnly), func(t *testing.T) {
			f := tlc497Setup(t)
			dir := tlc497Project(t)
			var r *verify.Report
			var err error
			if modelOnly {
				r, err = verify.RunModel(tlc497Context(t), dir, f.tc, verify.Options{TLCParallel: 2})
			} else {
				r, err = verify.Run(tlc497Context(t), dir, "", f.tc, verify.Options{TLCParallel: 2})
			}
			if err != nil {
				t.Fatal(err)
			}
			if !r.Passed {
				t.Fatalf("complete fixture failed: %s", verify.Feedback(r))
			}
			peak, modules, names := f.runs(t)
			if peak != 2 {
				t.Errorf("peak TLC containers = %d, want 2", peak)
			}
			tlc497AllModelResults(t, r, modules)
			if !modelOnly && (r.Conformance == nil || !r.Conformance.Passed || modules["Invariant_Conformance"] < 3) {
				t.Fatalf("conformance batches were not checked: %+v, runs=%v", r.Conformance, modules)
			}
			f.limits(t, names, 2<<30, 1536<<20)
		})
	}
	t.Run("graph dumps also have memory limits", func(t *testing.T) {
		f := tlc497Setup(t)
		r := f.runner(tlc497Budget(t, 2))
		if _, err := r.Dump(tlc497Context(t), t.TempDir(), "Dump", tlc.Config{Specification: "Spec"}); err != nil {
			t.Fatal(err)
		}
		_, _, names := f.runs(t)
		f.limits(t, names, 2<<30, 1536<<20)
	})
	t.Run("standalone runner has safe memory defaults", func(t *testing.T) {
		f := tlc497Setup(t)
		r := f.runner(nil)
		result, err := r.Check(tlc497Context(t), t.TempDir(), "Standalone", tlc.Config{Specification: "Spec"})
		if err != nil || result.Outcome != tlc.Passed {
			t.Fatalf("standalone check = %+v, %v", result, err)
		}
		_, _, names := f.runs(t)
		f.limits(t, names, 1<<30, 768<<20)
	})
}

func TestTLC497GateAutomaticBudget(t *testing.T) {
	for _, c := range []struct {
		name, info       string
		request, ceiling int
		memory, heap     uint64
	}{
		{"memory limited", `{"NCPU":8,"MemTotal":4294967296}`, 0, 1, 2 << 30, 1536 << 20},
		{"cpu limited", `{"NCPU":2,"MemTotal":68719476736}`, 0, 2, 2 << 30, 1536 << 20},
		{"large request", `{"NCPU":8,"MemTotal":4294967296}`, 50, 1, 2 << 30, 1536 << 20},
		{"malformed discovery", `not Docker JSON`, 0, 1, 1 << 30, 768 << 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := tlc497Setup(t)
			t.Setenv("TLC497_INFO", c.info)
			var options []verify.Options
			if c.request != 0 {
				options = append(options, verify.Options{TLCParallel: c.request})
			}
			r, err := verify.RunModel(tlc497Context(t), tlc497Project(t), f.tc, options...)
			if err != nil || !r.Passed {
				t.Fatalf("gate failed: %+v, %v", r, err)
			}
			peak, modules, names := f.runs(t)
			if peak != c.ceiling {
				t.Errorf("peak = %d, want %d from selected daemon", peak, c.ceiling)
			}
			if !strings.Contains(strings.Join(f.events(t), "\n"), "info") {
				t.Error("gate never discovered Docker capacity")
			}
			tlc497AllModelResults(t, r, modules)
			f.limits(t, names, c.memory, c.heap)
		})
	}
}

func TestTLC497MemoryFailureKeepsOtherResults(t *testing.T) {
	for _, module := range []string{"Model", "Witness_W3", "Property_P1", "Fairness_F1", "Bug_B1", "Bug_B2", "Invariant_Conformance"} {
		t.Run(module, func(t *testing.T) {
			f := tlc497Setup(t)
			t.Setenv("TLC497_FAIL", module)
			r, err := verify.Run(tlc497Context(t), tlc497Project(t), "", f.tc, verify.Options{TLCParallel: 2})
			if err != nil {
				t.Fatalf("lost the report on memory exhaustion: %v", err)
			}
			if r.Passed {
				t.Fatal("memory exhaustion passed the gate")
			}
			peak, modules, _ := f.runs(t)
			if peak > 2 {
				t.Errorf("peak = %d, want at most 2", peak)
			}
			tlc497AllModelResults(t, r, modules)
			data, _ := json.Marshal(r)
			text := strings.ToLower(string(data))
			if !strings.Contains(text, "memory") || !strings.Contains(text, strings.ToLower(module)) {
				t.Errorf("missing named memory failure: %s", data)
			}
			if !r.Build.Passed {
				t.Error("independent build did not finish")
			}
			if module != "Model" && !r.Design.Passed {
				t.Error("independent design check did not finish")
			}
			for _, w := range r.Witnesses {
				if module != "Witness_"+w.Name && !w.Reached {
					t.Errorf("independent witness lost: %+v", w)
				}
			}
			for _, p := range r.Properties {
				if module != "Property_"+p.Name && !p.Holds {
					t.Errorf("independent property lost: %+v", p)
				}
			}
			for _, fair := range r.Fairness {
				if module != "Fairness_"+fair.Name && !fair.InNext {
					t.Errorf("independent fairness lost: %+v", fair)
				}
			}
			for _, bug := range r.Bugs {
				if module != "Bug_"+bug.Name && !bug.Caught {
					t.Errorf("independent known bug lost: %+v", bug)
				}
			}
			if module != "Invariant_Conformance" && (r.Conformance == nil || !r.Conformance.Passed) {
				t.Errorf("independent conformance lost: %+v", r.Conformance)
			}
			if !strings.Contains(strings.ToLower(receipt.Markdown(r)), "memory") {
				t.Error("receipt lost memory diagnostic")
			}
		})
	}
	t.Run("killed without a Java diagnostic", func(t *testing.T) {
		f := tlc497Setup(t)
		t.Setenv("TLC497_FAIL", "Witness_W3")
		t.Setenv("TLC497_KILLED", "1")
		r, err := verify.RunModel(tlc497Context(t), tlc497Project(t), f.tc, verify.Options{TLCParallel: 2})
		if err != nil || r.Passed {
			t.Fatalf("killed check: %+v, %v", r, err)
		}
		msg := strings.ToLower(r.Witnesses[2].Message)
		if !strings.Contains(msg, "memory") || !strings.Contains(msg, "137") || !strings.Contains(msg, "witness_w3") || (!strings.Contains(msg, "possible") && !strings.Contains(msg, "may")) {
			t.Fatalf("ambiguous SIGKILL lacks an honest named diagnostic: %s", msg)
		}
		_, modules, _ := f.runs(t)
		tlc497AllModelResults(t, r, modules)
	})
}

func TestTLC497QueuedCancellationAndTimeout(t *testing.T) {
	f := tlc497Setup(t)
	b := tlc497Budget(t, 1)
	holder := f.runner(b)
	holder.SilenceTimeout = 5 * time.Second
	ctx := tlc497Context(t)
	cfg := tlc.Config{Specification: "Spec"}
	heldDir, queuedDir, timedDir := t.TempDir(), t.TempDir(), t.TempDir()
	held := make(chan error, 1)
	go func() {
		r, err := holder.Check(ctx, heldDir, "Hold", cfg)
		if err == nil && r.Outcome != tlc.Passed {
			err = fmt.Errorf("hold: %+v", r)
		}
		held <- err
	}()
	f.waitStart(t, "Hold")
	cause := errors.New("cancelled queued TLC check")
	qctx, cancel := context.WithCancelCause(ctx)
	queued := make(chan error, 1)
	go func() { _, err := holder.Check(qctx, queuedDir, "NeverStart", cfg); queued <- err }()
	cancel(cause)
	select {
	case err := <-queued:
		if !errors.Is(err, cause) {
			t.Fatalf("queued cancellation = %v, want cause", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued cancellation waited for the occupied slot")
	}
	timed := f.runner(b)
	timed.SilenceTimeout = 300 * time.Millisecond
	done := make(chan error, 1)
	entered := make(chan struct{})
	go func() {
		close(entered)
		r, err := timed.CheckWithin(ctx, timedDir, "Timed", cfg, 300*time.Millisecond)
		if err == nil && r.Outcome != tlc.Passed {
			err = fmt.Errorf("timed: %+v", r)
		}
		done <- err
	}()
	<-entered
	// This exceeds both the admitted run's timeout and silence window.
	time.Sleep(700 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("queued work consumed an execution timeout: %v", err)
	default:
	}
	tlc497Write(t, filepath.Join(f.root, "release"), nil)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("admitted work lacked its own execution budget: %v", err)
	}
	peak, modules, _ := f.runs(t)
	if peak != 1 || modules["NeverStart"] != 0 || modules["Timed"] != 1 {
		t.Fatalf("queue launched wrong containers: peak=%d, %v", peak, modules)
	}
}

func TestTLC497RetryHoldsCapacityThroughCleanup(t *testing.T) {
	f := tlc497Setup(t)
	t.Setenv("TLC497_REMOVE_DELAY", "300ms")
	r := f.runner(tlc497Budget(t, 1))
	r.SilenceTimeout = 500 * time.Millisecond
	ctx := tlc497Context(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	run := func(module, dir string) {
		defer wg.Done()
		res, err := r.Check(ctx, dir, module, tlc.Config{Specification: "Spec", Invariants: []string{"Safe"}})
		if err == nil && res.Outcome != tlc.Passed {
			err = fmt.Errorf("%s: %+v", module, res)
		}
		errs <- err
	}
	stallDir, otherDir := t.TempDir(), t.TempDir()
	wg.Add(1)
	go run("Stall", stallDir)
	f.waitStart(t, "Stall")
	wg.Add(1)
	go run("Other", otherDir)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	peak, modules, names := f.runs(t)
	if peak != 1 || modules["Stall"] != 2 || modules["Other"] != 1 {
		t.Fatalf("retry lifecycle: peak=%d, runs=%v", peak, modules)
	}
	var configs [][]byte
	for _, name := range names {
		args, err := os.ReadFile(filepath.Join(f.root, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(args), "Stall.tla") {
			cfg, err := os.ReadFile(filepath.Join(f.root, name+".cfg"))
			if err != nil {
				t.Fatal(err)
			}
			configs = append(configs, cfg)
		}
	}
	if len(configs) != 2 || string(configs[0]) != string(configs[1]) {
		t.Fatal("retry changed the check configuration")
	}
	f.limits(t, names, 2<<30, 1536<<20)
}

func TestTLC497OutOfMemoryIsNotCounterexample(t *testing.T) {
	for _, c := range []struct {
		name, prefix, diagnostic string
		exit                     int
	}{
		{"heap", "", "java.lang.OutOfMemoryError: Java heap space\n", 1},
		{"GC overhead", "", "GC overhead limit exceeded\n", 1},
		{"partial success", tlc497Frame(2193, "Model checking completed."), "java.lang.OutOfMemoryError: Java heap space\n", 1},
		{"partial violation", tlc497Frame(2110, "Invariant Safe is violated.") + tlc497Frame(2217, "1: <Initial predicate>\n/\\ n = 0"), "java.lang.OutOfMemoryError: Java heap space\n", 1},
		{"partial property", tlc497Frame(2116, "Temporal properties were violated."), "java.lang.OutOfMemoryError: GC overhead limit exceeded\n", 1},
		{"killed", "", "", 137},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := tlc497Setup(t)
			tlc497Write(t, filepath.Join(f.root, "transcript"), []byte(c.prefix+c.diagnostic))
			t.Setenv("TLC497_EXIT", strconv.Itoa(c.exit))
			res, err := f.runner(tlc497Budget(t, 2)).Check(tlc497Context(t), t.TempDir(), "Transcript", tlc.Config{Specification: "Spec"})
			if err != nil {
				t.Fatal(err)
			}
			msg := strings.ToLower(res.Message)
			if res.Outcome != tlc.Failed || !strings.Contains(msg, "memory") || !strings.Contains(msg, "transcript") {
				t.Fatalf("unidentified memory failure: %+v", res)
			}
			if res.Invariant != "" || res.Property != "" || len(res.Trace) != 0 {
				t.Fatalf("memory failure retained counterexample evidence: %+v", res)
			}
			_, modules, _ := f.runs(t)
			if modules["Transcript"] != 1 {
				t.Errorf("memory failure retried: %v", modules)
			}
		})
	}
}

func TestTLC497LimitsPreserveEvidenceAndReceipts(t *testing.T) {
	t.Run("recorded examples and unrelated failure", func(t *testing.T) {
		f := tlc497Setup(t)
		cases := []struct {
			file string
			exit int
		}{{"passed.txt", 0}, {"violated.txt", 12}, {"property-passed.txt", 0}, {"property-loop.txt", 13}, {"property-stutter.txt", 13}, {"action-property.txt", 13}}
		for _, c := range cases {
			data, err := os.ReadFile(filepath.Join("../tlc/testdata", c.file))
			if err != nil {
				t.Fatal(err)
			}
			tlc497Write(t, filepath.Join(f.root, "transcript"), data)
			t.Setenv("TLC497_EXIT", strconv.Itoa(c.exit))
			res, err := f.runner(tlc497Budget(t, 2)).Check(tlc497Context(t), t.TempDir(), "Transcript", tlc.Config{Specification: "Spec"})
			if err != nil || !reflect.DeepEqual(res, tlc.Parse(string(data), c.exit)) {
				t.Fatalf("%s changed evidence: %+v, %v", c.file, res, err)
			}
		}
		data := tlc497Frame(3002, "Parsing or semantic analysis failed.")
		tlc497Write(t, filepath.Join(f.root, "transcript"), []byte(data))
		t.Setenv("TLC497_EXIT", "150")
		res, err := f.runner(tlc497Budget(t, 1)).Check(tlc497Context(t), t.TempDir(), "Transcript", tlc.Config{Specification: "Spec"})
		if err != nil || !reflect.DeepEqual(res, tlc.Parse(data, 150)) {
			t.Fatalf("ordinary failure changed: %+v, %v", res, err)
		}
		_, _, names := f.runs(t)
		f.limits(t, names, 2<<30, 1536<<20)
	})
	t.Run("receipt independent of scheduling", func(t *testing.T) {
		f := tlc497Setup(t)
		dir := tlc497Project(t)
		var wantJSON, wantMD string
		for _, n := range []int{0, 1, 2} {
			r, err := verify.Run(tlc497Context(t), dir, "", f.tc, verify.Options{TLCParallel: n})
			if err != nil || !r.Passed {
				t.Fatalf("schedule %d failed: %+v, %v", n, r, err)
			}
			r.GeneratedAt = "fixed timestamp"
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			md := receipt.Markdown(r)
			if n == 0 {
				wantJSON, wantMD = string(data), md
			} else if string(data) != wantJSON || md != wantMD {
				t.Fatalf("schedule %d changed receipt evidence or fingerprint\n%s\nwant\n%s", n, data, wantJSON)
			}
		}
	})
}
