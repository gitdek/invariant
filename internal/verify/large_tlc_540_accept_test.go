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
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

// Extend the existing Docker-boundary fixture, including its real gate project
// and sampled conformance batches. No Docker daemon, JVM or network is used.
func init() {
	if os.Getenv("INVARIANT_TLC540_DOCKER") == "1" {
		os.Exit(tlc540Docker(os.Args[1:]))
	}
}

func tlc540Bytes(s string) uint64 {
	mult := uint64(1)
	if len(s) > 0 {
		switch strings.ToLower(s[len(s)-1:]) {
		case "k":
			mult, s = 1<<10, s[:len(s)-1]
		case "m":
			mult, s = 1<<20, s[:len(s)-1]
		case "g":
			mult, s = 1<<30, s[:len(s)-1]
		}
	}
	n, _ := strconv.ParseUint(s, 10, 64)
	return n * mult
}

func tlc540Arg(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, flag+"=") {
			return strings.TrimPrefix(arg, flag+"=")
		}
	}
	return ""
}

func tlc540Docker(args []string) int {
	if len(args) == 0 || args[0] != "run" {
		return tlc497Docker(args)
	}
	var module, work, heap string
	for i, arg := range args {
		if strings.HasSuffix(arg, ".tla") {
			module = strings.TrimSuffix(filepath.Base(arg), ".tla")
		}
		if arg == "-v" && i+1 < len(args) && strings.HasSuffix(args[i+1], ":/work") {
			work = strings.TrimSuffix(args[i+1], ":/work")
		}
		if strings.HasPrefix(arg, "-Xmx") {
			heap = strings.TrimPrefix(arg, "-Xmx")
		}
	}
	if module == "" {
		return tlc497Docker(args) // the fixture's ordinary build and driver
	}
	root, name := os.Getenv("TLC497_ROOT"), tlc540Arg(args, "--name")
	memory := tlc540Bytes(tlc540Arg(args, "--memory"))
	if name == "" || work == "" || memory == 0 || memory != tlc540Bytes(tlc540Arg(args, "--memory-swap")) || tlc540Bytes(heap) != memory/(1<<20)*3/4*(1<<20) {
		fmt.Fprintln(os.Stderr, "fake TLC: missing or inconsistent container, swap or heap limits")
		return 90
	}
	write := func(file string, data []byte) {
		if err := os.WriteFile(filepath.Join(root, file), data, 0600); err != nil {
			panic(err)
		}
	}
	raw, _ := json.Marshal(args)
	write(name+".json", raw)
	cfg, err := os.ReadFile(filepath.Join(work, "Invariant.cfg"))
	if err != nil {
		panic(err)
	}
	write(name+".cfg", cfg)
	tlc497Event(root, "start "+name+" "+module)
	defer tlc497Event(root, "end "+name)
	wait := func(file string) {
		for {
			if _, err := os.Stat(filepath.Join(root, file)); err == nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if module == "Hold" {
		wait("release")
	}
	target := strings.Contains(","+os.Getenv("TLC540_TARGET")+",", ","+module+",")
	if target {
		large := memory > 2<<30
		if !large && os.Getenv("TLC540_INITIAL_BARRIER") == "1" {
			wait("initial-release")
		}
		if large && os.Getenv("TLC540_RETRY_BARRIER") == "1" {
			wait("retry-release")
		}
		switch os.Getenv("TLC540_MODE") {
		case "killed":
			return 137
		case "semantic":
			fmt.Print(tlc497Frame(3002, "Parsing or semantic analysis failed."))
			return 150
		}
		if !large || os.Getenv("TLC540_FAIL_LARGE") == "1" {
			// Neither incomplete success nor an unfinished counterexample may
			// survive heap exhaustion as evidence from this attempt.
			fmt.Print(tlc497Frame(2193, "Model checking completed.") +
				tlc497Frame(2110, "Invariant Unfinished is violated.") +
				tlc497Frame(2217, "1: <Initial predicate>\n/\\ n = 999") +
				tlc497Frame(2199, "999 states generated, 999 distinct states found."))
			diagnostic := os.Getenv("TLC540_DIAGNOSTIC")
			if diagnostic == "" {
				diagnostic = "java.lang.OutOfMemoryError: Java heap space\n"
			}
			fmt.Print(diagnostic)
			return 1
		}
	}
	time.Sleep(30 * time.Millisecond)
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

func tlc540Setup(t *testing.T) tlc497Fixture {
	t.Helper()
	f := tlc497Setup(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\\''") + "'"
	tlc497Write(t, filepath.Join(f.root, "docker"), []byte("#!/bin/sh\nGORACE=atexit_sleep_ms=0 INVARIANT_TLC540_DOCKER=1 exec "+quoted+" \"$@\"\n"))
	for _, key := range []string{"TLC540_TARGET", "TLC540_INITIAL_BARRIER", "TLC540_RETRY_BARRIER", "TLC540_FAIL_LARGE", "TLC540_MODE", "TLC540_DIAGNOSTIC"} {
		t.Setenv(key, "")
	}
	return f
}

type tlc540Run struct {
	name, module string
	memory, heap uint64
	config       string
}

func tlc540Runs(t *testing.T, f tlc497Fixture) []tlc540Run {
	t.Helper()
	var runs []tlc540Run
	for _, line := range f.events(t) {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "start" {
			continue
		}
		r := tlc540Run{name: fields[1], module: fields[2]}
		raw, err := os.ReadFile(filepath.Join(f.root, r.name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var args []string
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
		r.memory = tlc540Bytes(tlc540Arg(args, "--memory"))
		for _, arg := range args {
			if strings.HasPrefix(arg, "-Xmx") {
				r.heap = tlc540Bytes(strings.TrimPrefix(arg, "-Xmx"))
			}
		}
		cfg, err := os.ReadFile(filepath.Join(f.root, r.name+".cfg"))
		if err != nil {
			t.Fatal(err)
		}
		r.config = string(cfg)
		runs = append(runs, r)
	}
	return runs
}

func tlc540Wait(t *testing.T, condition func() bool, why string) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(until) {
			t.Fatal(why)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func tlc540Ended(f tlc497Fixture, t *testing.T, count int) bool {
	n := 0
	for _, line := range f.events(t) {
		if strings.HasPrefix(line, "end ") {
			n++
		}
	}
	return n >= count
}

func TestTLC540ExclusiveMemoryRetry(t *testing.T) {
	f := tlc540Setup(t)
	t.Setenv("TLC540_TARGET", "HeavyA,HeavyB")
	t.Setenv("TLC540_INITIAL_BARRIER", "1")
	t.Setenv("TLC540_RETRY_BARRIER", "1")
	runner := f.runner(tlc497Budget(t, 3))
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan error, 4)
	start := func(module string) {
		dir := t.TempDir()
		go func() {
			r, err := runner.Check(ctx, dir, module, tlc.Config{Specification: "Spec", Constants: map[string]string{"Limit": "7"}, Invariants: []string{"Safe"}})
			if err == nil && r.Outcome != tlc.Passed {
				err = fmt.Errorf("%s: %+v", module, r)
			}
			done <- err
		}()
	}
	start("Hold")
	start("HeavyA")
	start("HeavyB")
	for _, module := range []string{"Hold", "HeavyA", "HeavyB"} {
		f.waitStart(t, module)
	}
	tlc497Write(t, filepath.Join(f.root, "initial-release"), nil)
	tlc540Wait(t, func() bool { return tlc540Ended(f, t, 2) }, "normal attempts never completed")
	time.Sleep(100 * time.Millisecond)
	if len(tlc540Runs(t, f)) != 3 {
		t.Fatal("memory retry started while another TLC container was still active")
	}
	tlc497Write(t, filepath.Join(f.root, "release"), nil)
	tlc540Wait(t, func() bool { return len(tlc540Runs(t, f)) >= 4 }, "heap exhaustion never earned a larger retry")
	start("Late")
	time.Sleep(100 * time.Millisecond)
	if len(tlc540Runs(t, f)) != 4 {
		t.Fatal("another check overlapped the exclusive memory retry")
	}
	tlc497Write(t, filepath.Join(f.root, "retry-release"), nil)
	for i := 0; i < 4; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("memory retries deadlocked or starved waiting work")
		}
	}
	runs := tlc540Runs(t, f)
	byName, byModule := map[string]tlc540Run{}, map[string][]tlc540Run{}
	for _, run := range runs {
		byName[run.name] = run
		byModule[run.module] = append(byModule[run.module], run)
	}
	for _, module := range []string{"HeavyA", "HeavyB"} {
		attempts := byModule[module]
		if len(attempts) != 2 || attempts[0].memory != 2<<30 || attempts[1].memory != 12<<30 || attempts[1].heap != 9<<30 || attempts[0].config != attempts[1].config {
			t.Fatalf("%s attempts did not preserve the check and grow to the reserved budget: %+v", module, attempts)
		}
	}
	active := map[string]tlc540Run{}
	for _, line := range f.events(t) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == "end" {
			delete(active, fields[1])
		}
		if fields[0] == "start" {
			active[fields[1]] = byName[fields[1]]
			var total uint64
			for _, run := range active {
				total += run.memory
				if run.memory > 2<<30 && len(active) != 1 {
					t.Fatalf("exclusive attempt overlapped other work: %v", active)
				}
			}
			if total > 12<<30 || len(active) > 3 {
				t.Fatalf("budget exceeded: %v", active)
			}
		}
	}
}

func TestTLC540RetryCancellation(t *testing.T) {
	for _, mode := range []string{"parent cancellation", "original CheckWithin allowance"} {
		t.Run(mode, func(t *testing.T) {
			f := tlc540Setup(t)
			t.Setenv("TLC540_TARGET", "Heavy")
			runner := f.runner(tlc497Budget(t, 2))
			ctx, stop := context.WithTimeout(t.Context(), 10*time.Second)
			defer stop()
			held := make(chan error, 1)
			heldDir := t.TempDir()
			go func() { _, err := runner.Check(ctx, heldDir, "Hold", tlc.Config{Specification: "Spec"}); held <- err }()
			f.waitStart(t, "Hold")
			cause := errors.New("build stopped while waiting for TLC memory")
			checkCtx, cancel := context.WithCancelCause(ctx)
			defer cancel(context.Canceled)
			done := make(chan error, 1)
			dir := t.TempDir()
			go func() {
				_, err := runner.CheckWithin(checkCtx, dir, "Heavy", tlc.Config{Specification: "Spec"}, time.Second)
				done <- err
			}()
			tlc540Wait(t, func() bool { return tlc540Ended(f, t, 1) }, "heap exhaustion did not finish its first attempt")
			want := error(context.DeadlineExceeded)
			if mode == "parent cancellation" {
				want = cause
				cancel(cause)
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("memory retry did not retain cancellation/deadline: %v; want %v", err, want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("waiting memory retry renewed or ignored the original allowance")
			}
			if len(tlc540Runs(t, f)) != 2 {
				t.Fatal("canceled queued retry launched a container")
			}
			tlc497Write(t, filepath.Join(f.root, "release"), nil)
			if err := <-held; err != nil {
				t.Fatal(err)
			}
			r, err := runner.Check(ctx, t.TempDir(), "After", tlc.Config{Specification: "Spec"})
			if err != nil || r.Outcome != tlc.Passed {
				t.Fatalf("canceled retry stranded capacity: %+v, %v", r, err)
			}
		})
	}
}

func TestTLC540RetryBoundedAndClassified(t *testing.T) {
	diagnostics := []string{"java.lang.OutOfMemoryError: Java heap space\n", "GC overhead limit exceeded\n"}
	for _, code := range []int{1001, 1002, 1003} {
		diagnostics = append(diagnostics, tlc497Frame(code, "Java ran out of memory."))
	}
	for i, diagnostic := range diagnostics {
		t.Run(fmt.Sprintf("diagnostic_%d", i), func(t *testing.T) {
			f := tlc540Setup(t)
			t.Setenv("TLC540_TARGET", "Heavy")
			t.Setenv("TLC540_DIAGNOSTIC", diagnostic)
			t.Setenv("TLC540_FAIL_LARGE", "1")
			r, err := f.runner(tlc497Budget(t, 1)).Check(tlc497Context(t), t.TempDir(), "Heavy", tlc.Config{Specification: "Spec"})
			runs := tlc540Runs(t, f)
			if len(runs) != 2 || runs[1].memory != 12<<30 {
				t.Fatalf("one-slot concurrency limit must still permit exactly one larger retry: %+v", runs)
			}
			if err != nil || r.Outcome != tlc.Failed || !strings.Contains(r.Message, "Heavy") || !strings.Contains(strings.ToLower(r.Message), "memory") {
				t.Fatalf("repeated exhaustion was not a named operational failure: %+v, %v", r, err)
			}
			if r.Invariant != "" || r.Property != "" || len(r.Trace) != 0 || r.DistinctStates != 0 || r.StatesGenerated != 0 {
				t.Fatalf("exhausted attempt leaked partial model evidence: %+v", r)
			}
		})
	}
	for _, mode := range []string{"killed", "semantic", "unknown", "no headroom", "standalone"} {
		t.Run(mode, func(t *testing.T) {
			f := tlc540Setup(t)
			t.Setenv("TLC540_TARGET", "Heavy")
			t.Setenv("TLC540_MODE", mode)
			b := tlc497Budget(t, 2)
			switch mode {
			case "unknown":
				b, _ = tlc.NewBudget(0, 0, 0)
			case "no headroom":
				b, _ = tlc.NewBudget(8, 1<<30, 0)
			case "standalone":
				b = nil
			}
			r, err := f.runner(b).Check(tlc497Context(t), t.TempDir(), "Heavy", tlc.Config{Specification: "Spec"})
			if err != nil || r.Outcome != tlc.Failed || len(tlc540Runs(t, f)) != 1 {
				t.Fatalf("ineligible failure earned an expansion: %+v, %v", r, err)
			}
		})
	}
}

// This wire shape is the report API promised by the plan. Check identifies a
// logical check (including individual same-named conformance batches), while
// Attempt is one-based within it. It is operational history, not model evidence.
type tlc540Attempt struct {
	Check       string      `json:"check"`
	Module      string      `json:"module"`
	Attempt     int         `json:"attempt"`
	MemoryBytes uint64      `json:"memory_bytes"`
	HeapBytes   uint64      `json:"heap_bytes"`
	Outcome     tlc.Outcome `json:"outcome"`
	Message     string      `json:"message"`
}

func tlc540Attempts(t *testing.T, r *verify.Report) ([]tlc540Attempt, []byte) {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Attempts []tlc540Attempt `json:"tlc_attempts"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Attempts) == 0 {
		t.Fatal("gate JSON omitted the allocated memory and TLC attempt history")
	}
	return wire.Attempts, raw
}

func TestTLC540GateRecordsMemoryAttempts(t *testing.T) {
	for _, tc := range []struct {
		module string
		model  bool
	}{
		{"Model", true}, {"Witness_W1", true}, {"Invariant_Conformance", false},
	} {
		t.Run(tc.module, func(t *testing.T) {
			f := tlc540Setup(t)
			dir := tlc497Project(t)
			run := func() *verify.Report {
				t.Helper()
				var r *verify.Report
				var err error
				if tc.model {
					r, err = verify.RunModel(tlc497Context(t), dir, f.tc, verify.Options{TLCParallel: 3})
				} else {
					r, err = verify.Run(tlc497Context(t), dir, "", f.tc, verify.Options{TLCParallel: 3})
				}
				if err != nil || r == nil || !r.Passed {
					t.Fatalf("gate failed: %+v, %v", r, err)
				}
				return r
			}
			baseline := run()
			wantChecks := 0
			for _, a := range tlc540Runs(t, f) {
				if a.module == tc.module {
					wantChecks++
				}
			}
			if wantChecks == 0 || !tc.model && wantChecks < 3 {
				t.Fatalf("fixture did not exercise the requested checks: %d", wantChecks)
			}
			t.Setenv("TLC540_TARGET", tc.module)
			r := run()
			attempts, raw := tlc540Attempts(t, r)
			grouped := map[string][]tlc540Attempt{}
			for _, a := range attempts {
				if a.Check == "" || a.Module == "" || a.Attempt < 1 || a.MemoryBytes == 0 || a.HeapBytes == 0 || a.Outcome == "" {
					t.Fatalf("incomplete attempt record: %+v", a)
				}
				if a.Module == tc.module {
					grouped[a.Check] = append(grouped[a.Check], a)
				}
			}
			if len(grouped) != wantChecks {
				t.Fatalf("lost or conflated same-named checks: %v", grouped)
			}
			for _, pair := range grouped {
				if len(pair) != 2 || pair[0].Attempt != 1 || pair[1].Attempt != 2 || pair[0].MemoryBytes != 2<<30 || pair[0].HeapBytes != 1536<<20 || pair[1].MemoryBytes != 12<<30 || pair[1].HeapBytes != 9<<30 || pair[0].Outcome != tlc.Failed || !strings.Contains(strings.ToLower(pair[0].Message), "memory") {
					t.Fatalf("missing first failure or allocated limits: %+v", pair)
				}
				want := tlc.Passed
				if tc.module == "Witness_W1" {
					want = tlc.Violated // the expected witness counterexample
				}
				if pair[1].Outcome != want {
					t.Fatalf("retry lost its actual completed outcome: %+v", pair[1])
				}
			}
			var decoded verify.Report
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			again, _ := tlc540Attempts(t, &decoded)
			if !reflect.DeepEqual(again, attempts) {
				t.Fatal("JSON round trip lost attempt history")
			}
			if r.Fingerprint != baseline.Fingerprint || verify.Fingerprint(decoded) != baseline.Fingerprint || !reflect.DeepEqual(r.Design, baseline.Design) || !reflect.DeepEqual(r.Conformance, baseline.Conformance) {
				t.Fatal("operational retry history changed successful evidence or its fingerprint")
			}
			for label, text := range map[string]string{"receipt": receipt.Markdown(&decoded), "gate feedback": verify.Feedback(&decoded)} {
				// Allocation values are MiB in the human-facing resource history.
				for _, value := range []string{tc.module, "2048", "1536", "12288", "9216", "MiB", "memory"} {
					if !strings.Contains(text, value) {
						t.Errorf("%s omitted %q from its resource history:\n%s", label, value, text)
					}
				}
			}
		})
	}
}
