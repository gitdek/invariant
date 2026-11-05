package verify_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

// Keep the command-boundary fixture's model, witness and build semantics.
// Only size-dependent conformance exhaustion is added to its Docker transport.
func init() {
	if os.Getenv("INVARIANT_GATE578_DOCKER") == "1" {
		os.Exit(gate578Docker(os.Args[1:]))
	}
}

var gate578Pairs = regexp.MustCompile(`<<[0-9]+,\s*[0-9]+>>`)

func gate578Docker(args []string) int {
	if len(args) == 0 || args[0] != "run" || os.Getenv("GATE578_SPLIT") != "1" {
		return tlc497Docker(args)
	}
	module, work := "", ""
	for i, a := range args {
		if strings.HasSuffix(a, ".tla") {
			module = strings.TrimSuffix(filepath.Base(a), ".tla")
		}
		if a == "-v" && i+1 < len(args) && strings.HasSuffix(args[i+1], ":/work") {
			work = strings.TrimSuffix(args[i+1], ":/work")
		}
	}
	if module != "Invariant_Conformance" {
		return tlc497Docker(args)
	}
	body, e := os.ReadFile(filepath.Join(work, module+".tla"))
	if e != nil {
		panic(e)
	}
	_, pairs, _ := strings.Cut(string(body), "Invariant_Steps == {")
	pairs, _, _ = strings.Cut(pairs, "}")
	if len(gate578Pairs.FindAllString(pairs, -1)) <= 600 || tlc540Bytes(tlc540Arg(args, "--memory")) > 2<<30 {
		return tlc497Docker(args)
	}
	root, name := os.Getenv("TLC497_ROOT"), tlc540Arg(args, "--name")
	data, _ := json.Marshal(args)
	os.WriteFile(filepath.Join(root, name+".json"), data, 0600)
	cfg, e := os.ReadFile(filepath.Join(work, "Invariant.cfg"))
	if e != nil {
		panic(e)
	}
	os.WriteFile(filepath.Join(root, name+".cfg"), cfg, 0600)
	tlc497Event(root, "start "+name+" "+module)
	fmt.Print(tlc497Frame(2193, "Model checking completed.") + tlc497Frame(2199, "999 states generated, 999 distinct states found.") + tlc497Frame(1000, "The exception was a java.util.concurrent.ExecutionException\n: java.lang.OutOfMemoryError: GC overhead limit exceeded"))
	tlc497Event(root, "end "+name)
	return 1
}

type gate578Range struct {
	Kind  string `json:"kind"`
	Start int    `json:"start"`
	Count int    `json:"count"`
}

type gate578Attempt struct {
	tlc540Attempt
	Parent        string        `json:"parent_check"`
	ParentAttempt int           `json:"parent_attempt"`
	Range         *gate578Range `json:"range"`
	Split         bool          `json:"split"`
}

func gate578History(t *testing.T, r *verify.Report, minimumSplits int) []gate578Attempt {
	t.Helper()
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var wire struct {
		Attempts []gate578Attempt `json:"tlc_attempts"`
	}
	if e = json.Unmarshal(raw, &wire); e != nil {
		t.Fatal(e)
	}
	byCheck := map[string][]gate578Attempt{}
	children := map[string]map[string]gate578Attempt{}
	splits := 0
	for _, a := range wire.Attempts {
		if a.Check == "" || a.Module == "" || a.Attempt < 1 || a.MemoryBytes == 0 || a.HeapBytes == 0 || a.Outcome == "" {
			t.Fatalf("incomplete resource record: %+v", a)
		}
		if filepath.IsAbs(a.Check) || strings.Contains(a.Check, "\\") || strings.Contains(a.Check, "../") {
			t.Fatalf("nonportable check identity: %q", a.Check)
		}
		byCheck[a.Check] = append(byCheck[a.Check], a)
		if a.Parent != "" {
			if children[a.Parent] == nil {
				children[a.Parent] = map[string]gate578Attempt{}
			}
			children[a.Parent][a.Check] = a
		}
		if a.Split {
			splits++
		}
	}
	if splits < minimumSplits {
		t.Fatalf("gate omitted split history: got %d split parents, want at least %d: %+v", splits, minimumSplits, wire.Attempts)
	}
	for check, attempts := range byCheck {
		for i, a := range attempts {
			if a.Attempt != i+1 {
				t.Fatalf("attempts are not ordered within %s: %+v", check, attempts)
			}
			if a.Parent != "" {
				parent := byCheck[a.Parent]
				if len(parent) == 0 || a.ParentAttempt < 1 || a.ParentAttempt > len(parent) || !parent[a.ParentAttempt-1].Split {
					t.Fatalf("orphan child or wrong exhausted parent attempt: %+v", a)
				}
			}
			if !a.Split {
				continue
			}
			if a.Outcome != tlc.Failed || !strings.Contains(strings.ToLower(a.Message), "memory") || a.Range == nil || a.Range.Count <= 1 {
				t.Fatalf("split parent lost exhaustion or range: %+v", a)
			}
			if a.MemoryBytes != 2<<30 || a.HeapBytes != 1536<<20 {
				t.Fatalf("divisible parent expanded: %+v", a)
			}
			var halves []gate578Attempt
			for _, c := range children[check] {
				halves = append(halves, c)
			}
			if len(halves) != 2 {
				t.Fatalf("split does not have exactly two child identities: %+v children=%+v", a, halves)
			}
			for _, c := range halves {
				if c.Range == nil || c.Range.Kind != a.Range.Kind || c.Range.Count < 1 || c.MemoryBytes != 2<<30 || c.HeapBytes != 1536<<20 {
					t.Fatalf("invalid ordinary child: %+v", c)
				}
			}
			sort.Slice(halves, func(i, j int) bool { return halves[i].Range.Start < halves[j].Range.Start })
			left, right := halves[0].Range, halves[1].Range
			if left.Start != a.Range.Start || right.Start != left.Start+left.Count || right.Start+right.Count != a.Range.Start+a.Range.Count || left.Count-right.Count < -1 || left.Count-right.Count > 1 {
				t.Fatalf("halves dropped, duplicated or reordered units: parent=%+v children=%+v", a.Range, halves)
			}
		}
	}
	var decoded verify.Report
	if e = json.Unmarshal(raw, &decoded); e != nil {
		t.Fatal(e)
	}
	again, _ := json.Marshal(decoded.TLCAttempts)
	original, _ := json.Marshal(r.TLCAttempts)
	if string(again) != string(original) {
		t.Fatal("JSON round trip lost resource tree")
	}
	if verify.Fingerprint(decoded) != r.Fingerprint {
		t.Fatal("round trip changed evidence fingerprint")
	}
	for label, text := range map[string]string{"feedback": verify.Feedback(&decoded), "receipt": receipt.Markdown(&decoded)} {
		for _, want := range []string{"split", "parent", "range", "memory", "2048", "1536", "MiB"} {
			if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
				t.Errorf("%s omitted %q", label, want)
			}
		}
		for _, a := range wire.Attempts {
			if a.Split && !strings.Contains(text, a.Check) {
				t.Errorf("%s omitted split parent identity %q", label, a.Check)
			}
		}
	}
	// Optional fields must not prevent old reports from being read or rendered.
	legacy := []byte(`{"passed":false,"tlc_attempts":[{"check":"Model/1","module":"Model","attempt":1,"memory_bytes":2147483648,"heap_bytes":1610612736,"outcome":"error","message":"old diagnostic"}]}`)
	var old verify.Report
	if e = json.Unmarshal(legacy, &old); e != nil {
		t.Fatal(e)
	}
	for _, text := range []string{verify.Feedback(&old), receipt.Markdown(&old)} {
		if !strings.Contains(text, "old diagnostic") {
			t.Fatal("old report lost its diagnostic")
		}
	}
	return wire.Attempts
}

func TestGate578SplitHistoryAndFingerprint(t *testing.T) {
	f := tlc497Setup(t)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\\''") + "'"
	tlc497Write(t, filepath.Join(f.root, "docker"), []byte("#!/bin/sh\nGORACE=atexit_sleep_ms=0 INVARIANT_GATE578_DOCKER=1 exec "+quoted+" \"$@\"\n"))
	dir := tlc497Project(t)
	t.Setenv("GATE578_SPLIT", "")
	baseline, e := verify.Run(tlc497Context(t), dir, "", f.tc, verify.Options{TLCParallel: 3})
	if e != nil || baseline == nil || !baseline.Passed {
		t.Fatalf("reference gate failed: %+v %v", baseline, e)
	}
	t.Setenv("GATE578_SPLIT", "1")
	for _, parallel := range []int{1, 3} {
		r, e := verify.Run(tlc497Context(t), dir, "", f.tc, verify.Options{TLCParallel: parallel})
		if e != nil || r == nil || !r.Passed {
			t.Fatalf("split gate failed: %+v %v", r, e)
		}
		if r.Fingerprint != baseline.Fingerprint || !reflect.DeepEqual(r.Conformance, baseline.Conformance) || !reflect.DeepEqual(r.Design, baseline.Design) {
			t.Fatal("operational split changed successful evidence")
		}
		history := gate578History(t, r, 9)
		roots := map[int]int{}
		leaves := map[int]int{}
		for _, a := range history {
			if a.Range == nil || a.Range.Kind != "steps" {
				continue
			}
			if a.Parent == "" && a.Attempt == 1 {
				roots[a.Range.Start] = a.Range.Count
			}
			if !a.Split && a.Outcome == tlc.Passed {
				for i := a.Range.Start; i < a.Range.Start+a.Range.Count; i++ {
					leaves[i]++
				}
			}
		}
		if !reflect.DeepEqual(roots, map[int]int{0: 2000, 2000: 2000, 4000: 2000, 6000: 1}) {
			t.Fatalf("same-module batches lost original ranges: %v", roots)
		}
		if len(leaves) != 6001 {
			t.Fatalf("incomplete leaf coverage: %d", len(leaves))
		}
		for i := 0; i < 6001; i++ {
			if leaves[i] != 1 {
				t.Fatalf("logical step %d has %d completed leaves", i, leaves[i])
			}
		}
		// An ordinary report must still bind its actual semantic result.
		copy := *r
		c := *r.Conformance
		c.Passed = false
		copy.Conformance = &c
		if verify.Fingerprint(copy) == r.Fingerprint {
			t.Fatal("logical verdict no longer affects fingerprint")
		}
	}
	// Exercise Budget.Attempts separately to ensure optional range pointers are
	// deeply detached, rather than only copying the outer slice.
	b := tlc497Budget(t, 2)
	runner := f.runner(b)
	stage := t.TempDir()
	tlc497Write(t, filepath.Join(stage, "Model.tla"), []byte("---- MODULE Model ----\n====\n"))
	raw, _ := json.Marshal(map[string]any{"traces": [][]map[string]any{{{"n": 0}, {"n": 1}, {"n": 2}}}})
	_, e = conformance.Check(tlc497Context(t), runner, stage, "Model", nil, []string{"n"}, 3, raw, nil)
	if e != nil {
		t.Fatal(e)
	}
	snapshot := b.Attempts()
	before, _ := json.Marshal(b.Attempts())
	for i := range snapshot {
		snapshot[i].Message = "mutated reader"
		v := reflect.ValueOf(&snapshot[i]).Elem().FieldByName("Range")
		if v.IsValid() && v.Kind() == reflect.Pointer && !v.IsNil() {
			v = v.Elem()
		}
		if v.IsValid() && v.Kind() == reflect.Struct {
			field := v.FieldByName("Count")
			if field.IsValid() && field.CanSet() {
				field.SetInt(999)
			}
		}
	}
	after, _ := json.Marshal(b.Attempts())
	if string(before) != string(after) {
		t.Fatal("history snapshot aliases stored range or attempt data")
	}
}

func TestPythonGate578SplitEvidence(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			tc, _ := pa327Fake(t)
			bin, e := exec.LookPath("docker")
			if e != nil {
				t.Fatal(e)
			}
			file := filepath.Join(filepath.Dir(bin), "docker.py")
			raw, e := os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			script := strings.Replace(string(raw), "print('pa327 deliberately unavailable Docker'); sys.exit(73)", "print(json.dumps({'NCPU':8,'MemTotal':17179869184})); sys.exit(0)", 1)
			needle := "    msg(2262,'TLC2 Version"
			insert := `    if os.environ.get('PA578_SPLIT')=='1':
        import re
        memory=args[args.index('--memory')+1]
        count=0
        if module=='Invariant_Conformance':
            pairs=body.split('Invariant_Steps == {',1)[1].split('}',1)[0]
            count=len(re.findall(r'<<[0-9]+,\s*[0-9]+>>',pairs))
        record({'kind':'allocation578','module':module,'memory':memory,'count':count,'body':body})
        if memory=='2048m' and (module=='Lifecycle' or count>1):
            msg(2193,'Model checking completed.')
            msg(2199,'999 states generated, 999 distinct states found.')
            msg(1000,'The exception was a java.util.concurrent.ExecutionException\n: java.lang.OutOfMemoryError: Java heap space')
            sys.exit(1)
    msg(2262,'TLC2 Version`
			if !strings.Contains(script, needle) {
				t.Fatal("Python command fixture changed; cannot insert size-dependent exhaustion")
			}
			script = strings.Replace(script, needle, insert, 1)
			if e = os.WriteFile(file, []byte(script), 0600); e != nil {
				t.Fatal(e)
			}
			f := pa327New(t, explicit, false)
			t.Setenv("PA578_SPLIT", "")
			baseline := pa327Run(t, f, tc)
			pa327Assert(t, f, baseline, true)
			t.Setenv("PA578_SPLIT", "1")
			for _, parallel := range []int{1, 3} {
				r, e := verify.Run(tlc497Context(t), f.shared().Project.Dir, "", tc, verify.Options{TLCParallel: parallel})
				if e != nil || r == nil {
					t.Fatalf("Python recovery deadlocked or failed: %+v %v", r, e)
				}
				pa327Assert(t, f, r, true)
				history := gate578History(t, r, 1)
				if r.Fingerprint != baseline.Fingerprint || !reflect.DeepEqual(r.Design, baseline.Design) || !reflect.DeepEqual(r.Components, baseline.Components) {
					t.Fatal("split changed application evidence, attribution or fingerprint")
				}
				modelRetry := false
				for _, a := range history {
					if a.Module == "Lifecycle" && a.Attempt == 2 && a.MemoryBytes == 12<<30 {
						modelRetry = true
					}
					if a.Range != nil && a.Range.Kind == "steps" && a.MemoryBytes != 2<<30 {
						t.Fatalf("Python conformance escalated divisible work: %+v", a)
					}
				}
				if !modelRetry {
					t.Fatal("generic model escalation was lost")
				}
				if len(r.Application.Conformance) != len(baseline.Application.Conformance) {
					t.Fatal("lost invocation conformance")
				}
				for i, c := range r.Application.Conformance {
					before := baseline.Application.Conformance[i]
					if c.OperationalError || !reflect.DeepEqual(c.Result, before.Result) || !reflect.DeepEqual(c.Key, before.Key) || !reflect.DeepEqual(c.Invocation, before.Invocation) || !reflect.DeepEqual(c.Model, before.Model) {
						t.Fatalf("recovered parent poisoned logical evidence: %+v", c)
					}
					steps := 0
					for _, check := range c.Checks {
						if check.Error != "" || check.Result.Outcome != tlc.Passed || check.Result.DistinctStates != 3 || check.Result.StatesGenerated != 4 || check.Result.Depth != 3 || check.InputSHA256 == "" {
							t.Fatalf("unfinished parent used as successful evidence: %+v", check)
						}
						if check.Module == "Invariant_Conformance" {
							steps++
						}
					}
					if steps != 2 {
						t.Fatalf("actual completed child evidence conflated with parent: %d step checks: %+v", steps, c.Checks)
					}
					// Aggregate obligations have their own wire records. They certify
					// logical inputs and verdicts, not invented physical TLC statistics.
					wire, _ := json.Marshal(c)
					var fields map[string]json.RawMessage
					json.Unmarshal(wire, &fields)
					var logical []map[string]json.RawMessage
					if e = json.Unmarshal(fields["logical_checks"], &logical); e != nil || len(logical) != len(before.Checks) {
						t.Fatalf("missing aggregate logical obligations: %s", wire)
					}
					for _, l := range logical {
						for _, key := range []string{"module", "batch", "input_sha256", "config", "outcome"} {
							if len(l[key]) == 0 {
								t.Fatalf("logical obligation omitted %s: %v", key, l)
							}
						}
						if len(l["result"]) != 0 || len(l["distinct_states"]) != 0 || len(l["states_generated"]) != 0 {
							t.Fatal("logical aggregate claims physical model-check counts")
						}
						var module, batch, input, outcome string
						json.Unmarshal(l["module"], &module)
						json.Unmarshal(l["batch"], &batch)
						json.Unmarshal(l["input_sha256"], &input)
						json.Unmarshal(l["outcome"], &outcome)
						matched := false
						for _, original := range before.Checks {
							if original.Module == module && original.Batch == batch {
								matched = input == original.InputSHA256 && outcome == string(tlc.Passed)
								var config tlc.Config
								if err := json.Unmarshal(l["config"], &config); err != nil || !reflect.DeepEqual(config, original.Config) {
									t.Fatal("logical obligation changed configuration")
								}
							}
						}
						if !matched {
							t.Fatal("aggregate does not bind the original logical input and verdict")
						}
					}
				}
				// Execution provenance, including ordered observations and declaration
				// bindings, is unchanged by recovery and still binds the fingerprint.
				if len(r.Application.Executions) != len(baseline.Application.Executions) {
					t.Fatal("lost authenticated execution")
				}
				for i, x := range r.Application.Executions {
					b := baseline.Application.Executions[i]
					if !reflect.DeepEqual(x.Observations, b.Observations) || !reflect.DeepEqual(x.Key, b.Key) || !reflect.DeepEqual(x.Declared, b.Declared) || !reflect.DeepEqual(x.Provenance, b.Provenance) {
						t.Fatal("recovery changed authenticated provenance")
					}
				}
				wire, _ := json.Marshal(r)
				var changed verify.Report
				json.Unmarshal(wire, &changed)
				changed.Application.Conformance[0].Result.Passed = false
				if verify.Fingerprint(changed) == r.Fingerprint {
					t.Fatal("logical conformance verdict omitted from fingerprint")
				}
				var document map[string]any
				json.Unmarshal(wire, &document)
				app := document["application"].(map[string]any)
				first := app["conformance"].([]any)[0].(map[string]any)
				first["logical_checks"].([]any)[0].(map[string]any)["input_sha256"] = "sha256:" + strings.Repeat("f", 64)
				mutated, _ := json.Marshal(document)
				if err := json.Unmarshal(mutated, &changed); err != nil {
					t.Fatal(err)
				}
				if verify.Fingerprint(changed) == r.Fingerprint {
					t.Fatal("logical module input omitted from fingerprint")
				}
			}
			// Splitting may not turn recovered OOM into OperationalError or make
			// a semantic failure's actual invocation prefix depend on scheduling.
			t.Setenv("PA327_FAIL", "step")
			t.Setenv("PA578_SPLIT", "")
			failedReference := pa327Run(t, f, tc)
			pa327Assert(t, f, failedReference, false)
			t.Setenv("PA578_SPLIT", "1")
			failureFingerprint := ""
			for _, parallel := range []int{1, 3} {
				r, e := verify.Run(tlc497Context(t), f.shared().Project.Dir, "", tc, verify.Options{TLCParallel: parallel})
				if e != nil || r == nil || r.Passed {
					t.Fatalf("failed split gate: %+v %v", r, e)
				}
				for i, c := range r.Application.Conformance {
					if c.OperationalError || !reflect.DeepEqual(c.Result, failedReference.Application.Conformance[i].Result) || len(c.Checks) != 2 {
						t.Fatalf("nondeterministic failure prefix or original diagnostic: %+v", c)
					}
					if c.Checks[0].Module != "Invariant_ConformanceInit" || c.Checks[0].Result.Outcome != tlc.Passed || c.Checks[1].Module != "Invariant_Conformance" || c.Checks[1].Result.Outcome != tlc.Deadlock {
						t.Fatalf("failure prefix contains an unfinished parent or later child: %+v", c.Checks)
					}
				}
				if failureFingerprint != "" && failureFingerprint != r.Fingerprint {
					t.Fatal("split failure evidence depends on scheduling")
				}
				failureFingerprint = r.Fingerprint
			}
		})
	}
}
