package verify_test

// Issue #327, one plumbing contract, replacing the four previous issues.
//
// Shared capability preflight is used by Check, Run, Python.Check and session
// preparation before verification tools, image acquisition or installations.
// Only existing Python sampled application execution is newly admitted.
// Explicit tests use literal python -m unittest and explicit dotted selections;
// conformance uses the manifest's contained script and selected lexical driver,
// with literal ordered arguments/cwd/env and declared versus effective sampling.
// Keep reserved env checks, 1000/40/1 sampling defaults and existing maxima.
// Missing executable invocations for selected tests/conformance, proof selections
// (even without invocations), unsupported languages/modes, inconsistent existing
// selections, mismatched drivers, exhaustive and attempted-step requests fail.
// Malformed explicit configurations must never enter a legacy fallback.
// Neighbors with no selected checks are legal, visible and unchecked.
//
// Without components ResolveComponents stays legacy and reports have no invented
// component inventory or checks. Bind execution to selected existing sources,
// actual lexical declarations and surrounding application entries using the same
// trusted collector; account for every selected source, including directories.
// Preserve ordinary unittest discovery in this mode, with actual test outcomes.
// Discovery of execution bindings is not inference of production scope.
//
// Stage application, resources, tests, manifest, lock, driver, model and local
// includes before any execution/TLC. Resolve again from staged bytes, retaining
// freshness/containment checks. Every consumer, including attribution and hashes,
// uses the snapshot. Select the admitted application-root dependency image once
// with immutable identity and original/canonical/resolution/artifact evidence;
// verification-local requirements cannot replace it. Keep offline linux/amd64,
// startup/import policy, hash seed 0, read-only inputs, limits and cleanup.
// No annotations are required; even a +nagini marker cannot enable proof here.
// Preserve D-0183: stateless handles and private read-back/continuity records,
// authenticated complete streams and exact target/driver/entry binding. Access
// to collector internals is gate tampering, not a claim of isolation from all
// hostile in-process Python. Do not weaken this boundary or reopen #323.
//
// Translate authenticated runs to existing conformance.Check, preserving run
// boundaries, ordering, exact scalars, stuttering/refusal semantics and batching.
// TLC checks Init and Next at ratified bounds; no successful process or driver
// trace alone qualifies. Required tests must actually run and pass; skipped,
// not_run, expected_failure, unexpected_success, fixture/import/runtime failure
// and malformed/missing evidence fail, with check-specific diagnostics. Preserve
// independent valid observations when another check fails, not unauthenticated
// partial conformance. Continue safe independent checks; keep cancellation and
// operational errors distinguishable. Existing design/pin/ratification/witness/
// fairness/property/known-bug checks still apply. Reject non-sampled output.
//
// Report JSON adds optional "application" for actual application execution,
// both explicit and legacy. Its "executions" are PythonApplicationExecutionResult
// values, its "conformance" records associate a "key" and TLC "result" with
// the exact executed invocation; include model/includes, bounds, actual tool pins
// and diagnostics there or in its provenance. Retain immutable execution image
// identity for audit, excluding local image IDs from portable fingerprints.
// No row may be manufactured from an intended invocation. Each component gets
// only its own successful conformance via AttributeComponentConformance, runtime
// exercise via AttributeComponentRuntime, assumptions assumed/unverified, and
// no proof or target runtime coverage. No invented assertions. Preserve complete
// inventories including files with no functions. Keep invocation matching exact;
// record actual launcher separately from the selected declared command.
//
// Conformance/runtime component evidence rows add origin "gate" or "supplied".
// The public attribution APIs ALWAYS classify incoming records as supplied,
// retain other kinds and their origins, and revalidate incoming/retained evidence
// transactionally, including JSON round trips. Clearing one kind preserves the
// others; invalid/stale updates are atomic; supplied rows never change Passed.
// Gate origin is descriptive provenance, not an authorization token or verdict.
// Legacy externally supplied rows without origins retain supplied semantics.
//
// JSON/Markdown/fingerprints describe what ran and TLC checked, with complete
// application files/resources, requirements/canonical inputs/wheel resolution,
// driver/runner/observer, model/includes, ratified bounds, effective sampling,
// observations/counts/outcomes/diagnostics. Changed inputs OR observations change
// fingerprints even with equal counts/outcomes. Exclude time, host paths and
// transient container/authentication IDs; retain portable content identity.
// Safely render all untrusted fields in both explicit and legacy application
// receipts using existing literal rules. Old legacy receipts stay unchanged.
// Only sampled tested-against-the-model evidence is granted: no exhaustive-code,
// every-step, proof or project-wide every-size claim. Distinguish exhaustive
// design checking from sampled implementation testing.
//
// Update only obsolete public-existing-Python refusal assertions in the named
// old tests; retain all genuine unsupported, discovery, session, collector,
// attribution, old Python/marked-file Python, TypeScript and model-only tests.
// Add builder-owned regressions for snapshot mutation/cancellation/cleanup,
// selected files/directories/multiple targets, preserved independent outcomes,
// missing images, unsupported bindings, safe literal rendering of every new
// field, dependency/runner/observer fingerprint sensitivity and old receipts.
//
// SPEC and README replace ALL obsolete blanket unavailability claims. README
// supplies a complete usable manual-adoption example (application/verification
// layout, existing and component selections, omitted-components variant,
// unittest/driver invocations, dependency/resource, full model and bounds,
// read-back driver, actual human pin/ratification workflow, verify command,
// receipt/failure expectations). No invented ratifier, ratification or decision,
// no edits to existing pinned statements/locks, no factory drafting/synthesis
// or proof adapters. Exhaustive/attempt and larger-bound integration is the
// next parent issue, not an unplanned follow-up within #327.
// Cite recorded foundations on changed SPEC bullets: D-0013,D-0024,D-0029,
// D-0047,D-0054,D-0158,D-0161,D-0163,D-0168,D-0169,D-0170,D-0171,D-0175,
// D-0177,D-0178,D-0180,D-0181,D-0183 and D-0122 as applicable. This ratified
// plan supplies new gate/receipt policy pending merger recording with decision
// tooling. The builder cannot write decisions/. Decision recording/unavailable
// Docker/network checks belong under Merger work and alone require no repair.
//
// Default tests execute REAL local Python via the production embedded runner;
// only Docker transport/TLC results are doubles. They do not claim real TLC or
// Docker validation. GateFailures and GateFingerprints run locally in the
// pythongate package; their entry points here skip unless the real suite is
// enabled. All other acceptance functions and subtests retain their coverage. With
// INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 the same suite additionally runs
// verify.Run with real Docker, Python, TLC and installed packaging wheel. Missing
// prerequisites FAIL. Merger must post passing final-head output for:
// INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify \
//   ./internal/conformance ./cmd/invariant -run '^TestPythonApplicationGate' \
//   -count=1 -timeout 45m
// Extend real builder-owned regressions for ALL requirements above, including
// actual dependency resolution sensitivity, snapshot/cancellation/no-Nagini,
// independent multiple checks, runtime non-sampled output and original bytes.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
	"github.com/gitdek/invariant/internal/verify/internal/pythongatetest"
)

// Keep the original fixture names for the other gate, contracts, module and
// snapshot tests. The shared implementation also serves the serial pythongate
// package, so both entry points retain the same setup, assertions and cleanup.
const pa327Project = pythongatetest.ProjectPath
const pa327Source = pythongatetest.Source
const pa327DriverPath = pythongatetest.DriverPath
const pa327ModelPath = pythongatetest.ModelPath
const pa327Model = pythongatetest.Model
const pa327Driver = pythongatetest.Driver
const pa327Test = pythongatetest.TestSource

type pa327Fixture struct {
	root string
	p    *project.Project
}

func (f pa327Fixture) shared() pythongatetest.Fixture {
	return pythongatetest.Fixture{Root: f.root, Project: f.p}
}

func pa327Write(t *testing.T, root, name, body string) {
	t.Helper()
	pythongatetest.Write(t, root, name, body)
}
func pa327JSON(t *testing.T, v any) []byte {
	t.Helper()
	return pythongatetest.JSON(t, v)
}
func pa327Save(t *testing.T, f pa327Fixture) {
	t.Helper()
	pythongatetest.Save(t, f.shared())
}
func pa327Ref(source, symbol string) project.ComponentRef {
	return pythongatetest.Ref(source, symbol)
}
func pa327ID(source, symbol string) string {
	return pythongatetest.ID(source, symbol)
}
func pa327New(t *testing.T, explicit, wheel bool) pa327Fixture {
	t.Helper()
	f := pythongatetest.New(t, explicit, wheel)
	return pa327Fixture{root: f.Root, p: f.Project}
}
func pa327Tree(t *testing.T, root string) map[string]string {
	t.Helper()
	return pythongatetest.Tree(t, root)
}
func pa327Admit(t *testing.T, f pa327Fixture) {
	t.Helper()
	pythongatetest.Admit(t, f.shared())
}
func pa327Fake(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	return pythongatetest.Fake(t)
}
func pa327Run(t *testing.T, f pa327Fixture, tc toolchain.Toolchain) *verify.Report {
	t.Helper()
	return pythongatetest.Run(t, f.shared(), tc)
}

type pa327App = pythongatetest.ApplicationResult

func pa327Application(t *testing.T, r *verify.Report) pa327App {
	t.Helper()
	return pythongatetest.Application(t, r)
}
func pa327Assert(t *testing.T, f pa327Fixture, r *verify.Report, pass bool) {
	t.Helper()
	pythongatetest.Assert(t, f.shared(), r, pass)
}

func TestPythonApplicationGateAdmission(t *testing.T) {
	_, log := pa327Fake(t)
	for _, explicit := range []bool{true, false} {
		for _, marked := range []bool{false, true} {
			// Exhaustive applications are admitted since #342.
			for _, exhaustive := range []bool{false, true} {
				f := pa327New(t, explicit, false)
				if marked {
					b, _ := os.ReadFile(filepath.Join(f.root, pa327Source))
					pa327Write(t, f.root, pa327Source, "# +nagini\n"+string(b))
				}
				f.p.Manifest.Exhaustive = exhaustive
				pa327Save(t, f)
				pa327Admit(t, f)
			}
		}
	}
	cases := []struct {
		name   string
		change func(pa327Fixture)
	}{
		{"proof selection without invocation", func(f pa327Fixture) {
			f.p.Manifest.Components.Checks[0].Proof = &project.ComponentProof{Contracts: []project.ComponentRef{pa327Ref(pa327Source, "advance")}}
		}},
		{"missing selected test invocation", func(f pa327Fixture) { c := &f.p.Manifest.Components.Checks[0]; c.Invocations = c.Invocations[1:] }},
		{"missing selected conformance invocation", func(f pa327Fixture) { c := &f.p.Manifest.Components.Checks[0]; c.Invocations = c.Invocations[:1] }},
		{"no usable conformance", func(f pa327Fixture) {
			c := &f.p.Manifest.Components.Checks[0]
			c.Conformance = nil
			c.Invocations = c.Invocations[:1]
		}},
		{"unsupported command", func(f pa327Fixture) { f.p.Manifest.Components.Checks[0].Invocations[0].Command = "sh" }},
		{"implicit unittest", func(f pa327Fixture) {
			f.p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest"}
		}},
		{"explicit discovery", func(f pa327Fixture) {
			f.p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest", "discover"}
		}},
		{"unknown sampling mode", func(f pa327Fixture) { f.p.Manifest.Components.Checks[0].Invocations[1].Sampling["mode"] = "attempted" }},
		{"unsupported sampling", func(f pa327Fixture) { f.p.Manifest.Components.Checks[0].Invocations[1].Sampling["steps"] = "0" }},
		{"reserved environment", func(f pa327Fixture) {
			f.p.Manifest.Components.Checks[0].Invocations[1].Env = map[string]string{"PYTHONPATH": "."}
		}},
		{"different manifest driver", func(f pa327Fixture) {
			pa327Write(t, f.p.Dir, "other.py", pa327Driver)
			f.p.Manifest.Conformance = "other.py"
		}},
		{"target outside existing selection", func(f pa327Fixture) { f.p.Manifest.Existing = []string{"tasks/neighbor.py"} }},
		{"bad explicit scope", func(f pa327Fixture) { f.p.Manifest.Components.Scope = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := pa327New(t, true, false)
			tc.change(f)
			pa327Save(t, f)
			if e := verify.Check(f.p.Dir); e == nil {
				t.Fatal("unsupported configuration admitted")
			}
			if r, e := verify.Run(context.Background(), f.p.Dir, "", toolchain.Toolchain{}); e == nil || r != nil {
				t.Fatalf("invalid preflight returned report: %+v %v", r, e)
			}
			if b, _, e := (verify.Python{}).Check(context.Background(), f.p); e == nil || b.Passed {
				t.Fatal("direct Python bypasses capability preflight")
			}
			n := 0
			s, e := verify.PreparePythonApplicationSession(context.Background(), f.p, filepath.Join(t.TempDir(), "stage"), verify.PythonApplicationSessionOptions{SelectImage: func(context.Context, *verify.PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
				n++
				return toolchain.PythonImage, nil, nil
			}})
			if e == nil || s != nil || n != 0 {
				if s != nil {
					s.Close()
				}
				t.Fatalf("session accepted invalid configuration or selected image: %v calls=%d", e, n)
			}
		})
	}
	if b, _ := os.ReadFile(log); len(b) > 0 {
		t.Fatalf("preflight invoked tools: %s", b)
	}
}

func TestPythonApplicationGateSessions(t *testing.T) {
	pa327Fake(t)
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			f := pa327New(t, explicit, false)
			pa327Admit(t, f)
			n := 0
			stage := filepath.Join(t.TempDir(), "stage")
			s, e := verify.PreparePythonApplicationSession(context.Background(), f.p, stage, verify.PythonApplicationSessionOptions{SelectImage: func(_ context.Context, w *verify.PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
				n++
				if w.Root != stage || w.Project.Dir == f.p.Dir {
					t.Fatal("image selection did not receive staged context")
				}
				if explicit != (w.Inventory.Mode == "explicit") {
					t.Fatal("inventory mode changed")
				}
				return toolchain.PythonImage, nil, nil
			}})
			if e != nil || s == nil {
				t.Fatalf("prepare explicit=%v: %v", explicit, e)
			}
			defer s.Close()
			before := s.Provenance()
			for _, name := range []string{pa327Source, pa327DriverPath, pa327ModelPath, "tasks/next.txt", "tests/test_tasks.py", pa327Project + "/.invariant/specs/Local.tla"} {
				pa327Write(t, f.root, name, "original changed after preparation\n")
			}
			for repeat := 0; repeat < 2; repeat++ {
				keys := s.Invocations()
				if len(keys) < 2 {
					t.Fatal("session omitted tests or driver")
				}
				seen := false
				for _, v := range keys {
					r, e := s.Run(context.Background(), v.Key)
					if e != nil || r == nil || !r.Success {
						t.Fatalf("snapshot execution: %+v %v", r, e)
					}
					seen = seen || len(r.Observations) > 0
					if r.Provenance.Fingerprint != before.Fingerprint {
						t.Fatal("session reread original provenance")
					}
				}
				if !seen {
					t.Fatal("session never collected bound observations")
				}
			}
			if n != 1 {
				t.Fatalf("image selected %d times", n)
			}
			if !reflect.DeepEqual(before, s.Provenance()) {
				t.Fatal("session provenance mutated")
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			if r, e := s.Run(context.Background(), s.Invocations()[0].Key); e == nil || (r != nil && r.Success) {
				t.Fatal("closed session executed")
			}
		})
	}
}

func TestPythonApplicationGateResults(t *testing.T) {
	t.Run("local Python and recording TLC", func(t *testing.T) {
		tc, log := pa327Fake(t)
		pa327Results(t, tc, false)
		b, e := os.ReadFile(log)
		if e != nil {
			t.Fatal(e)
		}
		for _, want := range []string{`"module": "Invariant_ConformanceInit"`, `"module": "Invariant_Conformance"`, `CONSTANT Limit = 2`, `"kind": "python"`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("gate did not perform %s", want)
			}
		}
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real Docker Python TLC and wheel", func(t *testing.T) {
			tc, e := toolchain.Ensure(context.Background())
			if e != nil {
				t.Fatalf("requested real prerequisites: %v", e)
			}
			pa327Results(t, tc, true)
		})
	}
}
func pa327Results(t *testing.T, tc toolchain.Toolchain, wheel bool) {
	t.Helper()
	for _, explicit := range []bool{true, false} {
		for _, marked := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit=%v/marked=%v", explicit, marked), func(t *testing.T) {
				f := pa327New(t, explicit, wheel)
				if marked {
					b, _ := os.ReadFile(filepath.Join(f.root, pa327Source))
					pa327Write(t, f.root, pa327Source, "# +nagini\n"+string(b))
				}
				before := pa327Tree(t, f.root)
				pa327Admit(t, f)
				r := pa327Run(t, f, tc)
				pa327Assert(t, f, r, true)
				a := pa327Application(t, r)
				for _, x := range a.Executions {
					if x.Key.Kind == "conformance" {
						if len(x.Observations) != 4 {
							t.Fatalf("actual observations lost: %+v", x.Observations)
						}
						if x.Observations[0].State["task"] != "pending" || x.Observations[1].After["task"] != "running" || x.Observations[2].After["task"] != "done" {
							t.Fatal("read-back changed")
						}
					}
					if wheel {
						d := x.Provenance.Dependencies
						if d == nil || len(d.Artifacts) != 1 || d.Artifacts[0].Name != "packaging" || d.Artifacts[0].Version != "24.2" || len(d.Inputs) != 3 || len(d.CanonicalInputs) != 2 {
							t.Fatalf("real wheel evidence missing: %+v", d)
						}
					}
				}
				md := receipt.Markdown(r)
				for _, want := range []string{"sampled", "pending", "running", "done", "driver.py"} {
					if !strings.Contains(md, want) {
						t.Errorf("receipt omits %q", want)
					}
				}
				if strings.Contains(md, "**proved**") || strings.Contains(md, "code is **tested in every state") {
					t.Fatal("receipt overclaims sampled evidence")
				}
				var decoded verify.Report
				if e := json.Unmarshal(pa327JSON(t, r), &decoded); e != nil {
					t.Fatal(e)
				}
				if verify.Fingerprint(decoded) != r.Fingerprint {
					t.Fatal("JSON round trip lost evidence fingerprint")
				}
				b, ev, e := (verify.Python{Image: toolchain.PythonImage}).Check(context.Background(), f.p)
				if e != nil || !b.Passed || len(ev.Traces) == 0 {
					t.Fatalf("direct Python application route: %+v %+v %v", b, ev, e)
				}
				if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
					t.Fatal("verification modified original application")
				}
			})
		}
	}
}

func TestPythonApplicationGateFailures(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") != "1" {
		t.Skip("local cases run in internal/verify/pythongate; set INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 for local and real cases")
	}
	pythongatetest.GateFailures(t, true)
}

func TestPythonApplicationGateDesignObligations(t *testing.T) {
	tc, _ := pa327Fake(t)
	f := pa327New(t, true, false)
	pa327Admit(t, f)
	pa327Assert(t, f, pa327Run(t, f, tc), true)
	for _, failure := range []string{"design", "witness", "bug", "fairness", "property", "pin", "ratification", "transport"} {
		t.Run(failure, func(t *testing.T) {
			f := pa327New(t, true, false)
			switch failure {
			case "pin":
				f.p.Lock.Statements[0].SHA256 = "sha256:" + strings.Repeat("0", 64)
			case "ratification":
				f.p.Lock.Ratified = &project.Ratification{By: "test-fixture-only", Proposal: "sha256:" + strings.Repeat("0", 64)}
			default:
				t.Setenv("PA327_FAIL", failure)
			}
			pa327Save(t, f)
			r := pa327Run(t, f, tc)
			pa327Assert(t, f, r, false)
			if len(r.Components.Runtime) == 0 {
				t.Fatal("model failure erased independent runtime evidence")
			}
			if failure != "transport" && (len(r.Components.Conformance) != 1 || !r.Components.Conformance[0].TransitionsChecked) {
				t.Fatal("model obligation failure erased independent checked observations")
			}
		})
	}
	// Model-only remains available even when code capability is unsupported.
	f.p.Manifest.Exhaustive = true
	pa327Save(t, f)
	r, e := verify.RunModel(context.Background(), f.p.Dir, tc)
	if e != nil || r == nil || !r.ModelOnly || r.Components != nil {
		t.Fatalf("model-only regression: %+v %v", r, e)
	}
}

func TestPythonApplicationGateFingerprints(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") != "1" {
		t.Skip("local cases run in internal/verify/pythongate; set INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 for local and real cases")
	}
	pythongatetest.GateFingerprints(t, true)
}

func TestPythonApplicationGateOriginsAndReceipts(t *testing.T) {
	tc, _ := pa327Fake(t)
	f := pa327New(t, true, false)
	pa327Admit(t, f)
	r := pa327Run(t, f, tc)
	pa327Assert(t, f, r, true)
	origins := func(r *verify.Report, kind string) []string {
		t.Helper()
		var root map[string]any
		if e := json.Unmarshal(pa327JSON(t, r), &root); e != nil {
			t.Fatal(e)
		}
		components := root["components"].(map[string]any)
		out := []string{}
		rows, _ := components[kind].([]any)
		for _, row := range rows {
			v, _ := row.(map[string]any)["origin"].(string)
			out = append(out, v)
		}
		return out
	}
	for _, kind := range []string{"conformance", "runtime"} {
		if got := origins(r, kind); len(got) != 1 || got[0] != "gate" {
			t.Fatalf("missing executed origin for %s: %v", kind, got)
		}
	}
	var decoded verify.Report
	if e := json.Unmarshal(pa327JSON(t, r), &decoded); e != nil {
		t.Fatal(e)
	}
	inv := &decoded.Components.Inventory
	supplied := []verify.ComponentRuntimeResult{decoded.Components.Runtime[0].Result}
	oldConformance := string(pa327JSON(t, decoded.Components.Conformance))
	passed := decoded.Passed
	if e := decoded.AttributeComponentRuntime(inv, supplied); e != nil {
		t.Fatal(e)
	}
	if got := origins(&decoded, "runtime"); len(got) != 1 || got[0] != "supplied" {
		t.Fatal("external replacement inherited gate origin")
	}
	if string(pa327JSON(t, decoded.Components.Conformance)) != oldConformance || decoded.Passed != passed {
		t.Fatal("replacement changed independent conformance or verdict")
	}
	md := receipt.Markdown(&decoded)
	if !strings.Contains(strings.ToLower(md), "gate") || !strings.Contains(strings.ToLower(md), "supplied") {
		t.Fatal("mixed origins not explained")
	}
	before := string(pa327JSON(t, &decoded))
	var bad verify.ComponentRuntimeResult
	if e := json.Unmarshal(pa327JSON(t, supplied[0]), &bad); e != nil {
		t.Fatal(e)
	}
	bad.Invocation.Args[0] = "substituted"
	if e := decoded.AttributeComponentRuntime(inv, []verify.ComponentRuntimeResult{bad}); e == nil {
		t.Fatal("mismatched invocation accepted")
	}
	if string(pa327JSON(t, &decoded)) != before {
		t.Fatal("invalid replacement was not atomic")
	}
	if e := decoded.AttributeComponentRuntime(inv, nil); e != nil {
		t.Fatal(e)
	}
	if len(decoded.Components.Runtime) != 0 || string(pa327JSON(t, decoded.Components.Conformance)) != oldConformance {
		t.Fatal("clear changed independent evidence")
	}
	// Even reusing genuine result bytes through the supplied API cannot set a
	// new gate verdict or preserve the original row's executed-by-gate label.
	suppliedConformance := []verify.ComponentConformanceResult{decoded.Components.Conformance[0].Result}
	external := verify.Report{Bounds: decoded.Bounds, Passed: false}
	if e := external.AttributeComponentConformance(inv, suppliedConformance); e != nil {
		t.Fatal(e)
	}
	if external.Passed || origins(&external, "conformance")[0] != "supplied" {
		t.Fatal("supplied row became a gate verdict")
	}
	// Adversarial text is rendered literally in BOTH receipt modes.
	hostile := "| <script>alert(327)</script> [click](https://evil.invalid) `tick`\n<!-- evidence -->"
	for _, explicit := range []bool{true, false} {
		q := pa327New(t, explicit, false)
		literal, _ := json.Marshal(hostile)
		body := strings.Replace(pa327Test, "self.assertEqual(application.tick('pending'), 'running')", "self.fail("+string(literal)+")", 1)
		pa327Write(t, q.root, "tests/test_tasks.py", body)
		if explicit {
			q.p.Manifest.Components.Checks[0].Invocations[1].Args = append(q.p.Manifest.Components.Checks[0].Invocations[1].Args, hostile)
			pa327Save(t, q)
		}
		got := pa327Run(t, q, tc)
		pa327Assert(t, q, got, false)
		md := receipt.Markdown(got)
		for _, raw := range []string{"<script>", "<!-- evidence -->", "[click](https://evil.invalid)"} {
			if strings.Contains(md, raw) {
				t.Fatalf("active hostile evidence rendered: %q", raw)
			}
		}
		if !strings.Contains(md, "alert(327)") {
			t.Fatal("diagnostic hidden instead of safely rendered")
		}
	}
}

func TestPythonApplicationGateBatches(t *testing.T) {
	tc, log := pa327Fake(t)
	f := pa327New(t, true, false)
	pa327Write(t, f.root, pa327Source, "def advance(state):\n    return state+1\n")
	pa327Write(t, f.root, "tasks/application.py", "from .lifecycle import advance\ndef start(): return 0\ndef tick(state): return advance(state)\n")
	pa327Write(t, f.root, "tests/test_tasks.py", "import unittest\nfrom tasks import application\nclass TaskTests(unittest.TestCase):\n    def test_pass(self): self.assertEqual(application.tick(0),1)\n")
	driver := strings.Replace(pa327Driver, "        life.step(application.tick)\n        life.step(application.tick)\n        life.step(application.tick)", "        for _ in range(2001):\n            life.step(application.tick)", 1)
	pa327Write(t, f.root, pa327DriverPath, driver)
	f.p.Manifest.Components.Checks[0].Invocations[1].Sampling["steps"] = "2002"
	model := `---- MODULE Lifecycle ----
EXTENDS Naturals, Local
CONSTANT Limit
VARIABLE task
vars == <<task>>
Init == task = 0
Next == \/ /\ task < 2001 /\ task' = task + 1
        \/ /\ task = 2001 /\ UNCHANGED task
Spec == Init /\ [][Next]_vars
Safe == task \in 0..2001 /\ Limit >= 1 /\ Helper
Seen == task = 2001
Bad == task' = 2002
Fair == WF_vars(Next)
Eventually == <> (task = 2001)
====
`
	pa327Write(t, f.root, pa327ModelPath, model)
	for i := range f.p.Lock.Statements {
		h, err := tla.PinHash(model, f.p.Lock.Statements[i].Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		f.p.Lock.Statements[i].SHA256 = h
	}
	pa327Save(t, f)
	pa327Admit(t, f)
	r := pa327Run(t, f, tc)
	pa327Assert(t, f, r, true)
	a := pa327Application(t, r)
	found := false
	for _, x := range a.Executions {
		if x.Key.Kind == "conformance" {
			found = true
			if len(x.Observations) != 2002 {
				t.Fatalf("batched observations lost: %d", len(x.Observations))
			}
		}
	}
	if !found {
		t.Fatal("missing execution")
	}
	b, e := os.ReadFile(log)
	if e != nil {
		t.Fatal(e)
	}
	batches := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"module": "Invariant_Conformance"`) {
			batches++
		}
	}
	if batches < 2 {
		t.Fatalf("2001 distinct transitions not batched: %d checks", batches)
	}
}
