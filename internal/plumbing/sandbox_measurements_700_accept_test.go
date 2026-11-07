package plumbing

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The records are an observable runner protocol, not scheduling-cache data.
// phase_start names prepare (inventory/format/vet), build (compile/discovery),
// test (the shared queue) and accept (the independent pinned reruns).
func TestSandboxMeasuredResultsAndPhases700(t *testing.T) {
	plan := &Plan{Tests: []Test{{Name: "TestPinned", File: "p/pinned_accept_test.go"}}}
	r := parse(`@@invariant vet=0
@@invariant test=0
@@result {"kind":"test","package":"fixture/p","name":"TestZero","phase":"test","status":"passed","shard":3,"elapsed_seconds":0}
@@result {"kind":"test","package":"fixture/p","name":"TestAbsent","phase":"test","status":"passed","shard":2}
@@result {"kind":"test","package":"fixture/p","name":"TestPending","phase":"test","status":"not_run"}
@@result {"kind":"test","package":"fixture/p","name":"TestPinned","phase":"accept","status":"passed","file":"p/pinned_accept_test.go","elapsed_seconds":1.25}
@@result {"kind":"done"}
`, plan)
	for _, row := range r.TestResults {
		elapsed, present := s700Duration(t, row)
		switch row.Name {
		case "TestZero":
			if !present || elapsed != 0 || row.Shard != 3 {
				t.Errorf("measured zero or worker identity lost: %+v (duration %v, present %v)", row, elapsed, present)
			}
		case "TestPinned":
			if !present || elapsed != 1.25 || row.File != "p/pinned_accept_test.go" || row.Phase != "accept" {
				t.Errorf("acceptance duration/identity lost: %+v", row)
			}
		default:
			if present {
				t.Errorf("absent measurement became a duration: %+v", row)
			}
		}
	}
	if len(r.TestResults) != 4 || !r.Accepted["p/pinned_accept_test.go:TestPinned"] || r.Passed() || !strings.Contains(r.Summary(), "TestPending") {
		t.Errorf("measurement changed identities, acceptance or unfinished verdict: %+v", r)
	}

	f := s397New(t)
	f.pkg(t, "work", `package work
import ("fmt"; "testing"; "time")
func TestMeasured(t *testing.T) {
  fmt.Println("S700_TEST_BODY")
  fmt.Println("@@result {\"kind\":\"phase_start\",\"phase\":\"accept\"}")
  time.Sleep(50*time.Millisecond)
  t.Run("child", func(t *testing.T) { time.Sleep(5*time.Millisecond) })
}
func TestFailed(t *testing.T) { time.Sleep(20*time.Millisecond); t.Fatal("measured failure") }
`)
	f.accept(t, "contract", "TestMeasured", fmt.Sprintf(`package contract
import ("fmt"; "os"; "testing"; "time")
func TestMeasured(t *testing.T) {
  p := %q
  if _, err := os.Stat(p); err == nil { fmt.Println("S700_ACCEPT_BODY") } else { fmt.Println("S700_TEST_BODY") }
  if err := os.WriteFile(p, []byte("ran"), 0600); err != nil { t.Fatal(err) }
  time.Sleep(30*time.Millisecond)
}
`, filepath.Join(f.observe, "pinned-ran")))
	f.warm(t)
	s397Write(t, filepath.Join(f.bin, "go"), `#!/usr/bin/env python3
import os, sys
a = sys.argv[1:]
if a and a[0] == 'vet': print('S700_PREPARE_COMMAND', flush=True)
if a and a[0] == 'test' and '-c' in a: print('S700_BUILD_COMMAND', flush=True)
os.execv(os.environ['S397_REAL_GO'], [os.environ['S397_REAL_GO']] + a)
`, 0o755)
	s397SeedTimings(t, f, []s397Timing{
		{"example.com/shards397/work", "TestMeasured", 98765},
		{"example.com/shards397/work", "TestFailed", 87654},
		{"example.com/shards397/contract", "TestMeasured", 76543},
	})
	r = f.run(t, 45*time.Second, 20*time.Second)
	if r.Passed() || r.Tests || !r.Vet || r.Interrupted || r.DeadlineExceeded || !r.Accepted["contract/pinned_accept_test.go:TestMeasured"] {
		t.Fatalf("real fixture verdicts changed: %s\n%s", r.Summary(), r.Output)
	}
	s397Status(t, r, "work", "TestMeasured", "test", "passed")
	s397Status(t, r, "work", "TestMeasured/child", "test", "passed")
	s397Status(t, r, "work", "TestFailed", "test", "failed")
	s397Status(t, r, "contract", "TestMeasured", "test", "passed")
	s397Status(t, r, "contract", "TestMeasured", "accept", "passed")
	if len(r.TestResults) != 5 {
		t.Errorf("want five package/phase-qualified results, got %+v", r.TestResults)
	}
	current := ""
	var phases []string
	seen := map[string]bool{}
	measured := map[string]float64{}
	for _, line := range strings.Split(r.Output, "\n") {
		if data, ok := strings.CutPrefix(line, "@@result "); ok {
			var event struct {
				Kind, Phase, Package, Name, Status string
				Elapsed                            *float64 `json:"elapsed_seconds"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				t.Fatal(err)
			}
			if event.Kind == "phase_start" {
				current = event.Phase
				phases = append(phases, current)
			}
			if event.Kind == "test" && event.Elapsed != nil && (event.Status == "passed" || event.Status == "failed" || event.Status == "skipped") {
				measured[event.Phase+"\x00"+event.Package+"\x00"+event.Name] = *event.Elapsed
			}
		}
		for marker, phase := range map[string]string{"S700_PREPARE_COMMAND": "prepare", "S700_BUILD_COMMAND": "build", "S700_TEST_BODY": "test", "S700_ACCEPT_BODY": "accept"} {
			if line == marker {
				seen[marker] = true
				if current != phase {
					t.Errorf("%s executed during %q, want phase_start %q before work", marker, current, phase)
				}
			}
		}
	}
	if strings.Join(phases, ",") != "prepare,build,test,accept" || len(seen) != 4 {
		t.Errorf("missing, late or forged phase boundaries: phases=%v work=%v\n%s", phases, seen, r.Output)
	}
	for _, row := range r.TestResults {
		value, present := s700Duration(t, row)
		want, emitted := measured[row.Phase+"\x00"+row.Package+"\x00"+row.Name]
		if !emitted || !present || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value != want || value > 60 {
			t.Errorf("result did not retain its measured duration (or used a cache hint): %+v value=%v emitted=%v want=%v", row, value, emitted, want)
		}
		if row.Phase == "test" && (row.Shard < 1 || row.Shard > 8) {
			t.Errorf("worker lost: %+v", row)
		}
	}
	if !strings.Contains(r.Summary(), "work/TestFailed") {
		t.Error("failed test identity disappeared")
	}
	// Duration telemetry must not expand ordinary passing summaries.
	passing := r
	passing.Tests, passing.Problems = true, nil
	passing.TestResults = append([]TestResult(nil), r.TestResults...)
	for i := range passing.TestResults {
		passing.TestResults[i].Status = "passed"
	}
	if !passing.Passed() || len(passing.Summary()) > 1000 || strings.Contains(passing.Summary(), "TestMeasured") {
		t.Errorf("ordinary passing summary lost its compact form: %s", passing.Summary())
	}
	// The fixture's real timing cache still contains measured top-level hints.
	data, err := os.ReadFile(filepath.Join(f.cache, s397TimingFile))
	if err != nil {
		t.Fatal(err)
	}
	var hints []s397Timing
	if err := json.Unmarshal(data, &hints); err != nil {
		t.Fatal(err)
	}
	for _, hint := range hints {
		if hint.Elapsed >= 60 || strings.Contains(hint.Name, "/") {
			t.Errorf("bad persisted ordering hint: %+v", hint)
		}
	}
}

func s700Duration(t *testing.T, row TestResult) (float64, bool) {
	t.Helper()
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	raw, ok := fields["elapsed_seconds"]
	if !ok || string(raw) == "null" {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value, true
}
