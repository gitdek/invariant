package verify

// Issue #315 is one plumbing change. This file is the acceptance contract,
// including requirements exercised by additional builder-owned regression tests.
//
// API (all in internal/verify):
//   PreparePythonApplicationSession(ctx, p, dst, PythonApplicationSessionOptions)
//       (*PythonApplicationSession, error)
//   session.Invocations() []PythonApplicationInvocation
//   session.Provenance() PythonApplicationProvenance
//   session.Run(ctx, PythonApplicationInvocationKey) (*PythonApplicationExecutionResult, error)
//   session.Close() error
// Invocation has Key, Declared (project.ResolvedComponentInvocation), and
// Sampling (map[string]string of prepared effective values). Key has string
// Target, Check, Kind, ID. Every returned view is detached. dst has exactly
// StagePythonApplication's rules; callers own dst, including partial copies.
// Close releases private runtime/output resources, is idempotent, leaves dst
// and the original alone, and prevents further runs. Failed preparation must
// clean up private resources and return no usable session.
//
// Options has optional SelectImage(ctx,*PythonApplicationWorkspace)
// (string,*toolchain.PythonDependencies,error) and Execute(ctx,
// PythonApplicationCommand)(PythonApplicationProcess,error) function fields.
// These are orchestration seams, never a second installer or a test-only
// runtime. Zero options use the production SelectImage and Docker executor.
// A supplied SelectImage replaces the whole image-preparation operation;
// orchestration doubles therefore need no Docker, and real hostile-wheel
// fixtures can supply a separately prepared image with truthful evidence.
// RunPythonApplicationCommand(ctx,command) is that same default executor,
// callable by a recording/fault-injection wrapper. SelectImage runs exactly
// once after ALL declaration validation; no-requirements SelectImage keeps
// returning toolchain.PythonImage,nil without Docker. Any acquisition needed
// for that digest belongs to the default preparation step, separate from
// execution. Dependency IDs must be immutable sha256 IDs. Missing selection,
// bad/incomplete evidence or installation failure never falls back. Check
// base/platform/recipe, actual original and canonical input hashes, and wheel
// names/versions/filenames/hashes. Verification-local requirements never win.
//
// Command has Key, SnapshotSHA256, RunnerSHA256, Image, Interpreter, Launcher,
// Args, WorkingDir, Env, Sampling, Root, RuntimeDir and OutputDir. Args is the
// actual interpreter argv after the executable, including flags and trusted
// launcher, retaining the declared ordered '-m unittest <names>' suffix.
// WorkingDir is an absolute container path; Root/RuntimeDir/OutputDir are host
// paths used solely for mounting, never portable provenance. Process has
// Events []byte, Stdout/Stderr string, ExitCode int. The executor uses
// toolchain.Docker with cancellation cleanup, --pull=never, --network none,
// linux/amd64, memory and process limits, read-only inputs/runtime, separate
// writable output and no host environment/credentials. It must not invoke a
// shell to interpret declaration strings, pull or resolve/install on Run.
// Missing images fail with no retry using a tag or base. No Nagini invocation.
//
// Runner protocol: UTF-8 JSON lines from a dedicated output channel/file,
// separate from arbitrary stdout/stderr. A version=1 begin record contains
// key, snapshot_sha256, runner_sha256. Test records contain type='test', id
// (unittest runtime ID), check (exact configured lexical ID, or empty for
// unselected/loader observations), source_sha256, exercised, outcome and
// diagnostics. End contains type='end', count (number of test records).
// Require all mandatory fields, correct binding, valid order, complete final
// record and consistent counts/outcomes. Reject null, malformed, truncated,
// contradictory, foreign or duplicate records; preserve already valid rows
// as incomplete on later error. Arbitrary stdout cannot become an event.
// An end record/exit zero alone never credits an unobserved required check.
// Real runner identity comes from the executed method's staged source and
// lexical location, not TestCase.id(), a declared selection, or its name alone.
// Overwritten/aliased same-named methods must not credit another declaration.
//
// Result has Key, Declared, Actual, Provenance, Tests, Complete, Success,
// ExitCode, Diagnostics, Fingerprint. Actual records Image (execution-only),
// Interpreter, Launcher, LauncherSHA256, Args, WorkingDir, Env and Sampling.
// Tests has ID, Check, SourceSHA256, Exercised, Outcome, Diagnostics. Preserve
// observation order. Outcomes distinguish passed, failed, error, skipped,
// expected_failure, unexpected_success and not_run. Only actually exercised,
// passing required tests with complete successful execution allow Success.
// Loader/import, fixture, setup/teardown and subtest errors, interruption,
// expected failures and unexpected successes cannot become passing evidence.
// Return partial Result alongside error when observations exist. No assertions,
// target coverage, proof, conformance or project assurance are inferred.
//
// Provenance has Files ([]project.ComponentFile), Dependencies
// (*toolchain.PythonDependencies), RunnerSHA256 and Fingerprint. Files hashes
// every actual copied file: selected and unselected siblings, resources,
// tests, driver, manifest, lock, model/local includes and requirements; do not
// enlarge production scope. Bind result fingerprints to this provenance,
// runner content and actual settings. Portable JSON/fingerprints exclude host
// paths, temp/run/container IDs, local image IDs/tags and incidental timings;
// preserve meaningful argument and observation ordering. Keep operational
// diagnostics separately if their host-specific content is not portable.
// Prevent/detect snapshot changes around preparation and every execution and
// between runs, including content, membership, symlinks and file replacement;
// a run with mutated inputs cannot keep success under old hashes. Generated
// outputs never enter input provenance. Originals stay byte-for-byte unchanged
// on success, failure and cancellation. Mutable caller data cannot retarget a
// prepared session, image, invocation or provenance.
//
// Invocation grammar: command exactly 'python'; test argv exactly '-m',
// 'unittest', then one or more dotted Python module/class/method names, in
// order. No discovery/options/startup flags/shell string/implicit selection.
// Conformance argv starts with a script resolved relative to working_dir;
// it must be contained, regular and identical to BOTH manifest project-relative
// driver and selected conformance source. Remaining args are literal, including
// whitespace and metacharacters. Conformance runs the script and requires
// bound sampled observations under #318's interface. Proof rejected.
// Tests have no declared sampling and effective runs=1; conformance supports
// decimal runs/steps in [1,2147483647] and seed in [0,2147483647], defaults
// 1000/40/1; unknown, malformed, overflow or conflicting controls fail.
// Reserve interpreter/import/startup/loader settings, hash seed, all INVARIANT_
// controls, DRIVER, PATH/HOME, output/launcher locations and all proxy case
// variants. Invalid environment keys and NUL values fail; safe values remain
// exact. No inherited host application env. Preserve #310's path contract.
//
// Trusted startup must retain D-0178: /usr/local/bin/python, no site/user-site
// or implicit source paths, effective PYTHONHASHSEED=0 (not -I ignoring it),
// trusted stdlib/runtime before imports, application root and driver directory
// before libraries, including nested namespace vs dependency regular packages.
// Installed scripts, .pth, sitecustomize/usercustomize cannot take over. Add
// real regression fixtures with hostile dependency regular/namespace packages,
// hooks and scripts, and controls establishing the attacks would otherwise
// work; use the existing wheel fixture techniques without another installer.
// Add regression coverage for resource limits, image disappearance without
// fallback, cancellation container removal, mutation during execution and
// malformed/mismatched output, retaining already observed outcomes. Doubles
// test orchestration only: actual Python execution must test outcomes/startup.
// Retain a builder-owned real-runner regression for a skipped unittest case:
// its outcome is skipped, never passed, Exercised is false, and it cannot
// qualify for Success even when unittest exits zero. Include its skip reason.
// Docker-dependent regression coverage uses the real-suite opt-in; the
// top-level acceptance tests must still run meaningful checks without Docker.
//
// Keep Check, Run, CLI and Python.Check disconnected; preserve ErrComponents,
// ErrPythonApplication, model-only, inert discovery, supplied attribution,
// legacy Python/marked files and TypeScript. No decisions/, pinned statements
// or ratified locks change. New regression tests may cover all these existing
// boundaries. SPEC and README describe this direct API and its limits (remove
// blanket no-adapter statements), sampling, provenance, reuse and ownership.
// Changed SPEC bullets cite recorded foundations D-0013,D-0029,D-0047,D-0054,
// D-0158,D-0168,D-0171,D-0177,D-0178,D-0180,D-0122 as appropriate and link
// https://github.com/gitdek/invariant/issues/313#issuecomment-6536681769
// sha256:0380e0c90425c12cb24fed241112820b07758fecb6d812130b71424a857d2366.
// The merger records the new session/runtime policy and its dependencies and
// runs the real suite on the final head; unavailable Docker/network and
// decision recording belong to Merger work and alone require no repair.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

const pa315Project = "verification/lifecycle"
const pa315TestSource = "tests/test_tasks.py"
const pa315Target = "tasks/lifecycle.py"
const pa315Literal = "  'quoted' \"double\" $HOME $(touch BAD) ; & | * \\ newline\n"
const pa315Image = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func pa315Fixture(t *testing.T, wheel bool) (string, *project.Project) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "application")
	ref := pa310Ref(pa315TestSource, "TaskTests.test_pass")
	p := &project.Project{Dir: filepath.Join(root, pa315Project), Manifest: project.Manifest{
		Name: "task lifecycle", Language: "python", Code: ".", Existing: []string{pa315Target},
		Module: ".invariant/specs/Lifecycle.tla", Conformance: "driver.py",
		Components: &project.ComponentsConfig{Scope: []string{"tasks"}, Checks: []project.ComponentCheck{{
			Target: pa310Ref(pa315Target, "advance"), Tests: []project.ComponentRef{ref},
			Conformance: []project.ComponentRef{pa310Ref(pa315Project+"/driver.py", "drive")},
			Invocations: []project.ComponentInvocation{
				{ID: "unit", Kind: "test", Check: ref, Command: "python", Args: []string{"-m", "unittest", "tests.test_tasks.TaskTests.test_pass"}, WorkingDir: ".", Env: map[string]string{"APP_LITERAL": pa315Literal, "APP_EMPTY": ""}, Inputs: []string{"tasks/next.txt"}},
				{ID: "model", Kind: "conformance", Check: pa310Ref(pa315Project+"/driver.py", "drive"), Command: "python", Args: []string{"driver.py", pa315Literal, "--bound", "2"}, WorkingDir: pa315Project},
			},
		}}},
	}, Lock: project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}}}}
	files := map[string]string{
		"tasks/__init__.py": "",
		pa315Target:         "from .helper import next_state\nfrom pathlib import Path\ndef advance(state):\n    return next_state(state, Path(__file__).with_name('next.txt').read_text().strip())\n",
		"tasks/helper.py":   "def next_state(state, configured):\n    return configured if state == 'pending' else state\n",
		"tasks/next.txt":    "running\n", "siblings/unselected.py": "VALUE = 17\n", "resources/bytes.bin": "\x00\xffresource\r\n",
		"tests/__init__.py":                              "",
		pa315Project + "/driver.py":                      "from tasks.lifecycle import advance\ndef drive():\n    return advance('pending')\n",
		pa315Project + "/.invariant/specs/Lifecycle.tla": "---- MODULE Lifecycle ----\nEXTENDS Naturals, Local\nCONSTANT Limit\nVARIABLE task\nInit == task = 0\nNext == task' = task\nSpec == Init /\\ [][Next]_task\nSafe == task >= 0\n====\n",
		pa315Project + "/.invariant/specs/Local.tla":     "---- MODULE Local ----\nHelper == TRUE\n====\n",
		pa315TestSource: `import os, unittest
from tasks.lifecycle import advance
class TaskTests(unittest.TestCase):
    def test_pass(self):
        self.assertEqual(advance('pending'), 'running')
    def test_fail(self):
        self.assertEqual(advance('pending'), 'done', 'expected failure diagnostic')
    def test_error(self):
        raise RuntimeError('expected error diagnostic')
    @unittest.skip('not exercised')
    def test_skip(self):
        raise AssertionError('must not run')
    @unittest.expectedFailure
    def test_expected(self):
        self.fail('expected')
    @unittest.expectedFailure
    def test_unexpected(self):
        pass
    def test_subtest(self):
        with self.subTest(value=1):
            self.assertEqual(1, 2)
    def test_partial_a(self):
        self.assertTrue(True)
    def test_partial_z(self):
        os._exit(0)
    def test_wait(self):
        import time
        time.sleep(300)
`,
	}
	if wheel {
		files["requirements.txt"] = "-r deps/application.txt\n"
		files["deps/application.txt"] = "packaging>=24,<25\n-c constraints.txt\n"
		files["deps/constraints.txt"] = "packaging==24.2\n"
		files[pa315Project+"/requirements.txt"] = "not-the-application-dependency==0\n"
		files[pa315TestSource] = strings.Replace(files[pa315TestSource], "self.assertEqual(advance('pending'), 'running')", "self.assertEqual(advance('pending'), 'running')\n        from packaging.version import Version\n        self.assertLess(Version('1.9'), Version('1.10'))", 1)
	}
	for name, body := range files {
		pa310Write(t, root, name, body)
	}
	pa310Save(t, p)
	return root, p
}

func pa315Select(t *testing.T, p *project.Project, symbol string, args ...string) {
	t.Helper()
	c := &p.Manifest.Components.Checks[0]
	ref := pa310Ref(pa315TestSource, symbol)
	c.Tests = []project.ComponentRef{ref}
	c.Invocations[0].Check = ref
	c.Invocations[0].Args = append([]string{"-m", "unittest"}, args...)
	pa310Save(t, p)
}
func pa315Prepare(t *testing.T, p *project.Project, o PythonApplicationSessionOptions) *PythonApplicationSession {
	t.Helper()
	s, err := PreparePythonApplicationSession(context.Background(), p, filepath.Join(t.TempDir(), "stage"), o)
	if err != nil || s == nil {
		t.Fatalf("prepare: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func pa315Unit(t *testing.T, s *PythonApplicationSession) PythonApplicationInvocation {
	t.Helper()
	for _, v := range s.Invocations() {
		if v.Key.Kind == "test" {
			return v
		}
	}
	t.Fatal("no prepared unittest invocation")
	return PythonApplicationInvocation{}
}
func pa315Options(n *int) PythonApplicationSessionOptions {
	return PythonApplicationSessionOptions{SelectImage: func(context.Context, *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
		*n++
		return toolchain.PythonImage, nil, nil
	}}
}
func pa315Tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = pa310Hash(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func pa315Files(p PythonApplicationProvenance) map[string]string {
	out := map[string]string{}
	for _, f := range p.Files {
		out[f.Path] = f.SHA256
	}
	return out
}

func pa315TrapTools(t *testing.T) func() {
	t.Helper()
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "tool-called")
	t.Setenv("PA315_TOOL_LOG", log)
	for _, name := range []string{"docker", "pip", "pip3", "nagini", "java", "node", "go"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf called >> \"$PA315_TOOL_LOG\"\nexit 73\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() {
		if b, err := os.ReadFile(log); !os.IsNotExist(err) {
			t.Fatalf("execution/install tool ran before validation: %q %v", b, err)
		}
	}
}

func TestPythonApplicationExecutionValidation(t *testing.T) {
	bad := map[string]func(*project.Project){
		"shell command": func(p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Command = "python -m unittest"
		},
		"alternate interpreter": func(p *project.Project) { p.Manifest.Components.Checks[0].Invocations[0].Command = "python3" },
		"startup flag": func(p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-I", "-m", "unittest", "tests.test_tasks"}
		},
		"discovery": func(p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest", "discover"}
		},
		"implicit suite": func(p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest"}
		},
		"shell selection": func(p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest", "tests.test_tasks; touch BAD"}
		},
		"unittest option": func(p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest", "-v", "tests.test_tasks"}
		},
		"test sampling": func(p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Sampling = map[string]string{"runs": "1"}
		},
		"driver mismatch": func(p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[1].Args[0] = "../../tasks/lifecycle.py"
		},
		"driver outside": func(p *project.Project) { p.Manifest.Components.Checks[0].Invocations[1].Args[0] = "/tmp/driver.py" },
		"proof":          func(p *project.Project) { p.Manifest.Components.Checks[0].Invocations[0].Kind = "proof" },
	}
	for _, key := range []string{"runs", "steps", "seed", "unknown"} {
		for _, value := range []string{"-1", "2147483648", "x", "1.5", " 1", "+1"} {
			k, v := key, value
			bad["sampling "+k+v] = func(p *project.Project) {
				p.Manifest.Components.Checks[0].Invocations[1].Sampling = map[string]string{k: v}
			}
		}
	}
	for _, key := range []string{"runs", "steps"} {
		k := key
		bad["zero "+k] = func(p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[1].Sampling = map[string]string{k: "0"}
		}
	}
	for _, key := range []string{"PYTHONPATH", "PYTHONHOME", "PYTHONHASHSEED", "PYTHONSTARTUP", "PYTHONINSPECT", "PYTHONUSERBASE", "PATH", "HOME", "LD_PRELOAD", "LD_LIBRARY_PATH", "DRIVER", "INVARIANT_RUNS", "INVARIANT_TRACES", "INVARIANT_OUTPUT", "INVARIANT_LAUNCHER", "HTTP_PROXY", "http_proxy", "HtTp_PrOxY", "https_proxy", "ALL_PROXY", "no_proxy", "BAD=KEY", "BAD\x00KEY"} {
		k := key
		bad["env "+k] = func(p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Env = map[string]string{k: ""}
		}
	}
	bad["NUL value"] = func(p *project.Project) {
		p.Manifest.Components.Checks[0].Invocations[0].Env = map[string]string{"SAFE": "a\x00b"}
	}
	for name, change := range bad {
		t.Run(name, func(t *testing.T) {
			assertNoTools := pa315TrapTools(t)
			_, p := pa315Fixture(t, true)
			change(p)
			pa310Save(t, p)
			n, executed := 0, 0
			o := pa315Options(&n)
			o.Execute = func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error) {
				executed++
				return PythonApplicationProcess{}, nil
			}
			s, err := PreparePythonApplicationSession(context.Background(), p, filepath.Join(t.TempDir(), "stage"), o)
			if err == nil || s != nil || n != 0 || executed != 0 {
				t.Fatalf("invalid declaration reached tools: session=%v error=%v selected=%d executed=%d", s, err, n, executed)
			}
			assertNoTools()
		})
	}
	t.Run("literal values and duplicate IDs", func(t *testing.T) {
		_, p := pa315Fixture(t, false)
		c := &p.Manifest.Components.Checks[0]
		second := c.Invocations[0]
		second.Check = pa310Ref(pa315TestSource, "TaskTests.test_fail")
		second.Args = []string{"-m", "unittest", "tests.test_tasks.TaskTests.test_fail"}
		c.Tests = append(c.Tests, second.Check)
		c.Invocations = append(c.Invocations, second)
		c.Invocations[1].Sampling = map[string]string{"runs": "2", "steps": "3", "seed": "0"}
		pa310Save(t, p)
		n := 0
		s := pa315Prepare(t, p, pa315Options(&n))
		seen := map[PythonApplicationInvocationKey]bool{}
		for _, v := range s.Invocations() {
			if seen[v.Key] {
				t.Fatal("aliased invocation")
			}
			seen[v.Key] = true
			if v.Key.Target == "" || v.Key.Check == "" || v.Key.ID == "" {
				t.Fatal("incomplete key")
			}
			if v.Key.Kind == "test" {
				if v.Declared.Env["APP_LITERAL"] != pa315Literal || v.Declared.Env["APP_EMPTY"] != "" || !reflect.DeepEqual(v.Sampling, map[string]string{"runs": "1"}) {
					t.Fatalf("lost literals/sampling: %+v", v)
				}
			} else {
				if !reflect.DeepEqual(v.Declared.Args, []string{"driver.py", pa315Literal, "--bound", "2"}) || !reflect.DeepEqual(v.Sampling, map[string]string{"runs": "2", "steps": "3", "seed": "0"}) {
					t.Fatalf("changed driver request: %+v", v)
				}
			}
		}
		if len(seen) != 3 || n != 1 {
			t.Fatalf("wrong preparation: %d %d", len(seen), n)
		}
	})
}

func TestPythonApplicationExecutionSession(t *testing.T) {
	root, p := pa315Fixture(t, false)
	before := pa315Tree(t, root)
	n := 0
	o := pa315Options(&n)
	executed := 0
	o.Execute = func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error) {
		executed++
		return PythonApplicationProcess{}, errors.New("must not execute")
	}
	s := pa315Prepare(t, p, o)
	prov := s.Provenance()
	if !reflect.DeepEqual(pa315Files(prov), before) || !strings.HasPrefix(prov.Fingerprint, "sha256:") || !strings.HasPrefix(prov.RunnerSHA256, "sha256:") {
		t.Fatalf("incomplete provenance: %+v", prov)
	}
	other := pa315Prepare(t, p, pa315Options(new(int)))
	if !reflect.DeepEqual(prov, other.Provenance()) {
		t.Fatal("relocation changed portable provenance")
	}
	views := s.Invocations()
	saved := s.Invocations()
	views[0].Declared.Args[0] = "changed"
	views[0].Declared.Env = map[string]string{"BAD": "value"}
	prov.Files[0].SHA256 = "changed"
	p.Manifest.Components.Checks[0].Invocations[0].Args[2] = "changed"
	if !reflect.DeepEqual(saved, s.Invocations()) || s.Provenance().Files[0].SHA256 == "changed" {
		t.Fatal("session aliases caller data")
	}
	for _, v := range s.Invocations() {
		if v.Key.Kind == "conformance" {
			if !reflect.DeepEqual(v.Sampling, map[string]string{"runs": "1000", "steps": "40", "seed": "1"}) {
				t.Fatal("bad conformance defaults")
			}
			r, err := s.Run(context.Background(), v.Key)
			if err == nil || r == nil || r.Success || len(r.Observations) != 0 || executed != 1 {
				t.Fatalf("failed conformance execution accepted: %+v %v", r, err)
			}
		}
	}
	if n != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
		t.Fatal("preparation changed original or selected twice")
	}
	t.Run("invalid dependency evidence", func(t *testing.T) {
		for _, which := range []string{"missing image", "mutable tag", "missing evidence", "bad evidence", "dependency failure"} {
			t.Run(which, func(t *testing.T) {
				_, q := pa315Fixture(t, true)
				opts := PythonApplicationSessionOptions{SelectImage: func(context.Context, *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
					switch which {
					case "missing image":
						return "", nil, nil
					case "mutable tag":
						return "python:latest", nil, nil
					case "missing evidence":
						return pa315Image, nil, nil
					case "bad evidence":
						return pa315Image, &toolchain.PythonDependencies{Image: pa315Image, Base: toolchain.PythonImage}, nil
					default:
						return "", nil, errors.New("dependency diagnostic")
					}
				}}
				got, err := PreparePythonApplicationSession(context.Background(), q, filepath.Join(t.TempDir(), "stage"), opts)
				if got != nil || err == nil {
					t.Fatalf("accepted %s", which)
				}
			})
		}
	})
	for _, which := range []string{"content", "added", "deleted", "symlink", "replacement"} {
		t.Run("freshness "+which, func(t *testing.T) {
			_, q := pa315Fixture(t, false)
			count := 0
			opts := pa315Options(&count)
			stage := ""
			selectImage := opts.SelectImage
			opts.SelectImage = func(ctx context.Context, w *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
				stage = w.Root
				return selectImage(ctx, w)
			}
			ran := false
			opts.Execute = func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error) {
				ran = true
				return PythonApplicationProcess{}, nil
			}
			sess := pa315Prepare(t, q, opts)
			file := filepath.Join(stage, "siblings/unselected.py")
			switch which {
			case "content":
				pa310Write(t, stage, "siblings/unselected.py", "VALUE=18\n")
			case "added":
				pa310Write(t, stage, "new.txt", "new")
			case "deleted":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(stage, pa315Target), file); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				b, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				pa310Write(t, stage, "replacement.tmp", string(b))
				if err := os.Rename(filepath.Join(stage, "replacement.tmp"), file); err != nil {
					t.Fatal(err)
				}
			}
			r, err := sess.Run(context.Background(), pa315Unit(t, sess).Key)
			if err == nil || ran || (r != nil && r.Success) {
				t.Fatalf("stale %s accepted: %+v %v ran=%v", which, r, err, ran)
			}
		})
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(context.Background(), pa315Unit(t, other).Key); err == nil {
		t.Fatal("closed session ran")
	}
}

func pa315Stream(t *testing.T, c PythonApplicationCommand, records ...map[string]any) []byte {
	t.Helper()
	begin := map[string]any{"type": "begin", "version": 1, "key": c.Key, "snapshot_sha256": c.SnapshotSHA256, "runner_sha256": c.RunnerSHA256}
	var out []byte
	all := append([]map[string]any{begin}, records...)
	for _, row := range all {
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		out = append(append(out, b...), '\n')
	}
	return out
}
func pa315Event(c PythonApplicationCommand, hash, outcome string) map[string]any {
	return map[string]any{"type": "test", "id": "tests.test_tasks.TaskTests.test_pass", "check": c.Key.Check, "source_sha256": hash, "exercised": outcome != "skipped", "outcome": outcome, "diagnostics": []string{}}
}
func TestPythonApplicationExecutionProtocol(t *testing.T) {
	for _, which := range []string{"passed", "failed", "error", "skipped", "expected_failure", "unexpected_success", "empty", "malformed", "missing end", "wrong snapshot", "wrong key", "wrong runner", "wrong source", "wrong count", "duplicate", "null outcome", "stdout spoof", "exit failure", "missing required"} {
		t.Run(which, func(t *testing.T) {
			root, p := pa315Fixture(t, false)
			before := pa315Tree(t, root)
			selected, executed := 0, 0
			o := pa315Options(&selected)
			o.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				executed++
				if c.Image != toolchain.PythonImage || c.Interpreter != "/usr/local/bin/python" || c.WorkingDir != "/src" || c.Env["APP_LITERAL"] != pa315Literal || c.Env["PYTHONHASHSEED"] != "0" {
					t.Fatalf("wrong actual settings: %+v", c)
				}
				suffix := []string{"-m", "unittest", "tests.test_tasks.TaskTests.test_pass"}
				if len(c.Args) < len(suffix) || !reflect.DeepEqual(c.Args[len(c.Args)-len(suffix):], suffix) {
					t.Fatalf("request changed: %q", c.Args)
				}
				outcome := which
				if !strings.Contains(" passed failed error skipped expected_failure unexpected_success ", " "+outcome+" ") {
					outcome = "passed"
				}
				event := pa315Event(c, before[pa315TestSource], outcome)
				end := map[string]any{"type": "end", "count": 1}
				process := PythonApplicationProcess{ExitCode: 0}
				switch which {
				case "failed", "error", "unexpected_success":
					process.ExitCode = 1
				case "exit failure":
					process.ExitCode = 77
				case "wrong snapshot":
					c.SnapshotSHA256 = "sha256:" + strings.Repeat("b", 64)
				case "wrong key":
					c.Key.ID = "different"
				case "wrong runner":
					c.RunnerSHA256 = "sha256:" + strings.Repeat("c", 64)
				case "wrong source":
					event["source_sha256"] = "sha256:" + strings.Repeat("d", 64)
				case "wrong count":
					end["count"] = 2
				case "null outcome":
					event["outcome"] = nil
				case "missing required":
					event["check"] = ""
				}
				process.Events = pa315Stream(t, c, event, end)
				switch which {
				case "empty":
					process.Events = nil
				case "malformed":
					process.Events = []byte("{bad}\n")
				case "missing end":
					process.Events = pa315Stream(t, c, event)
				case "duplicate":
					end["count"] = 2
					process.Events = pa315Stream(t, c, event, event, end)
				case "stdout spoof":
					process.Stdout = string(process.Events)
					process.Events = nil
				}
				return process, nil
			}
			s := pa315Prepare(t, p, o)
			v := pa315Unit(t, s)
			for i := 0; i < 2; i++ {
				r, err := s.Run(context.Background(), v.Key)
				if which == "passed" {
					if err != nil || r == nil || !r.Complete || !r.Success || len(r.Tests) != 1 || !r.Tests[0].Exercised || r.Tests[0].Check != v.Key.Check || r.Tests[0].Outcome != "passed" {
						t.Fatalf("lost real runner protocol: %+v %v", r, err)
					}
					if !reflect.DeepEqual(r.Declared, v.Declared) || r.Actual.Interpreter != "/usr/local/bin/python" || r.Actual.LauncherSHA256 != s.Provenance().RunnerSHA256 || r.Provenance.Fingerprint != s.Provenance().Fingerprint || r.Fingerprint == "" {
						t.Fatalf("lost execution provenance: %+v", r)
					}
				} else if r != nil && r.Success {
					t.Fatalf("%s granted success: %+v", which, r)
				}
				if which == "missing end" || which == "exit failure" {
					if r == nil || len(r.Tests) != 1 || r.Tests[0].Outcome != "passed" {
						t.Fatalf("lost preceding observation: %+v %v", r, err)
					}
				}
				if which == "missing end" && r.Complete {
					t.Fatal("truncation marked complete")
				}
				if which == "failed" || which == "error" || which == "skipped" || which == "expected_failure" || which == "unexpected_success" {
					if r == nil || len(r.Tests) != 1 || r.Tests[0].Outcome != which {
						t.Fatalf("lost distinct outcome: %+v", r)
					}
				}
			}
			if selected != 1 || executed != 2 {
				t.Fatalf("session reselected image: %d %d", selected, executed)
			}
			if !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("original changed")
			}
		})
	}
	t.Run("image failure no fallback", func(t *testing.T) {
		_, p := pa315Fixture(t, false)
		n, ran := 0, 0
		o := pa315Options(&n)
		o.Execute = func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error) {
			ran++
			return PythonApplicationProcess{ExitCode: 125}, errors.New("selected image disappeared")
		}
		s := pa315Prepare(t, p, o)
		r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
		if err == nil || n != 1 || ran != 1 || (r != nil && r.Success) {
			t.Fatalf("fallback: %d %d %+v %v", n, ran, r, err)
		}
	})
}

func TestPythonApplicationExecutionBoundaries(t *testing.T) {
	assertNoTools := pa315TrapTools(t)
	root, p := pa315Fixture(t, false)
	before := pa315Tree(t, root)
	n := 0
	s := pa315Prepare(t, p, pa315Options(&n))
	if len(s.Invocations()) != 2 {
		t.Fatal("direct adapter unusable")
	}
	if err := Check(p.Dir); err != nil {
		t.Fatalf("public preflight: %v", err)
	}
	p.Manifest.Exhaustive = true
	pa310Save(t, p)
	if r, err := Run(context.Background(), p.Dir, "", toolchain.Toolchain{}); r != nil || err == nil {
		t.Fatalf("public gate: %+v %v", r, err)
	}
	savedComponents := p.Manifest.Components
	p.Manifest.Components = nil
	pa310Save(t, p)
	if err := Check(p.Dir); err == nil {
		t.Fatalf("existing preflight: %v", err)
	}
	if _, _, err := (Python{}).Check(context.Background(), p); err == nil {
		t.Fatalf("direct legacy runner: %v", err)
	}
	// Restore the fixture manifest; comparisons cover all original bytes.
	p.Manifest.Components = savedComponents
	p.Manifest.Exhaustive = false
	pa310Save(t, p)
	after := pa315Tree(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("public guards changed original application")
	}
	assertNoTools()
}
