package verify

// #884 keeps the existing runner, collector and counter-model transport
// fixtures. Only the Docker/TLC boundary is doubled; Python executes the
// staged drivers. These tests stay serial because the fixtures set globals.
import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

const secondDriver884 = "checks/secondary/driver.py"
const secondSource884 = "counter/second.py"
const literal884 = "space value; $(echo untouched)"

func driverPath884(id string) string {
	if id == "first" {
		return pa342DriverPath
	}
	return secondDriver884
}

func fixture884(t *testing.T, wheel, sampled bool) pa342Fixture {
	t.Helper()
	f := pa342New(t, true, wheel)
	f.p.Manifest.Exhaustive = !sampled
	f.p.Manifest.Existing = append(f.p.Manifest.Existing, secondSource884)
	pa342Write(t, f.root, secondSource884, pa342Core)
	pa342Write(t, f.root, "counter/second_service.py", "from .second import increment\ndef start(): return 0\ndef inc(count): return increment(count)\ndef peek(count): return count\n")
	first := &f.p.Manifest.Components.Checks[0].Invocations[1]
	first.ID = "first"
	first.Args = []string{"driver.py", "first", "--literal", literal884}
	first.Env = map[string]string{"CHECK884": "first"}
	second := *first
	second.ID, second.Check = "second", pa342Ref(secondDriver884, "drive")
	second.WorkingDir, second.Args = "checks", []string{"secondary/driver.py", "second", "--literal", literal884}
	second.Env = map[string]string{"CHECK884": "second"}
	f.p.Manifest.Components.Checks = append(f.p.Manifest.Components.Checks, project.ComponentCheck{
		Target: pa342Ref(secondSource884, "increment"), Conformance: []project.ComponentRef{second.Check}, Invocations: []project.ComponentInvocation{second},
	})
	for _, id := range []string{"first", "second"} {
		body := pa342Driver
		if sampled {
			body = pa342SampledDriver
		}
		cwd := "counter"
		if id == "second" {
			body = strings.ReplaceAll(body, "from counter.core", "from counter.second")
			body = strings.ReplaceAll(body, "from counter import service", "from counter import second_service as service")
			cwd = "checks"
		}
		// Same sibling-module name, different bytes: loading the manifest
		// driver's directory for the other check cannot satisfy this import.
		preamble := fmt.Sprintf("import os, sys, support884\nassert support884.NAME == %q\nassert sys.argv[1:] == [%q, '--literal', %q]\nassert os.environ['CHECK884'] == %q\nassert os.path.basename(os.getcwd()) == %q\n", id, id, literal884, id, cwd)
		pa342Write(t, f.root, driverPath884(id), preamble+body)
		pa342Write(t, f.root, path.Join(path.Dir(driverPath884(id)), "support884.py"), fmt.Sprintf("NAME = %q\n", id))
	}
	pa342Save(t, f)
	return f
}

func admit884(t *testing.T, f pa342Fixture) {
	t.Helper()
	if err := Check(f.p.Dir); err != nil {
		t.Fatalf("each selected conformance source must be usable as its own contained driver: %v", err)
	}
}

func read884(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func unchanged884(t *testing.T, root string, before map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(before, pa342Tree(t, root)) {
		t.Fatal("execution changed original or staged application bytes")
	}
}

// Inspect the real executor's command, then run it through the existing
// transport. The callback never supplies observation evidence itself.
func executeCommand884(t *testing.T, ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
	t.Helper()
	before := pa342Tree(t, c.Root)
	if c.Key.Kind == "conformance" {
		name := driverPath884(c.Key.ID)
		if c.Env["CHECK884"] != c.Key.ID || len(c.Args) < 6 || !slices.Equal(c.Args[:6], []string{"-S", "-s", "-P", "-B", "/runtime/runner.py", "/runtime/request.json"}) {
			t.Fatalf("lost declared environment or controlled startup: %+v", c)
		}
		var req pythonApplicationRequest
		if err := json.Unmarshal(read884(t, filepath.Join(c.RuntimeDir, "request.json")), &req); err != nil {
			t.Fatal(err)
		}
		if req.Driver != name || req.Check.Source != name || req.Check.ID != c.Key.Check {
			t.Fatalf("execution is bound to another driver's path or declaration: %+v", req)
		}
		want := read884(t, filepath.Join(c.Root, filepath.FromSlash(name)))
		if len(c.Overlays) > 0 {
			if len(c.Overlays) != 1 || c.Overlays[0].Guest != "/src/"+name {
				t.Fatalf("larger invocation overlaid another driver: %+v", c.Overlays)
			}
			want = bytes.Replace(want, []byte("Limit = 2"), []byte("Limit = 3"), 1)
			o := c.Overlays[0]
			if !bytes.Equal(read884(t, o.Host), want) {
				t.Fatal("private copy changes more than this driver's bound constant")
			}
			info, err := os.Stat(o.Host)
			if err != nil || info.Mode().Perm()&0o222 != 0 {
				t.Fatalf("private driver copy is not read-only: %v", err)
			}
		}
		if req.Check.SourceSHA256 != pythonApplicationHash(want) {
			t.Fatal("selected declaration does not bind the bytes being executed")
		}
	}
	p, err := RunPythonApplicationCommand(ctx, c)
	unchanged884(t, c.Root, before)
	return p, err
}

func report884(t *testing.T, mode string, tc toolchain.Toolchain, wheel bool) *Report {
	t.Helper()
	f := fixture884(t, wheel, mode == "sampled")
	admit884(t, f)
	switch mode {
	case "success", "sampled":
	case "no first bounds", "no second bounds", "no bounds":
		for _, id := range []string{"first", "second"} {
			if mode == "no bounds" || mode == "no "+id+" bounds" {
				pa342Edit(t, f, driverPath884(id), "Limit = 2", "Limit = int('2')")
			}
		}
	case "execution", "both executions":
		pa342Edit(t, f, pa342DriverPath, "    explore(start, STEPS)", "    raise RuntimeError('first base failed 884')")
		if mode == "both executions" {
			pa342Edit(t, f, secondDriver884, "    explore(start, STEPS)", "    raise RuntimeError('second base failed 884')")
		}
	case "states":
		pa342Write(t, f.root, pa342Source, "def increment(count): return count + 1 if count < 1 else count\n")
	case "steps":
		pa342Edit(t, f, pa342DriverPath, "STEPS = [Step('Inc', inc), Step('Done', done)]", "STEPS = [Step('Inc', inc)]")
	case "transition":
		pa342Write(t, f.root, pa342Source, "def increment(count): return count + 1 if count != 1 else 3\n")
	case "first mismatch", "second mismatch":
		name := pa342Source
		if mode == "second mismatch" {
			name = secondSource884
		}
		pa342Write(t, f.root, name, "def increment(count): return count + 1 if count < 2 else count\n")
	default:
		t.Fatalf("unknown #884 fixture mode %q", mode)
	}
	pa342Save(t, f)
	saved := pythonApplicationGateOptions
	defer func() { pythonApplicationGateOptions = saved }()
	var private []string
	pythonApplicationGateOptions.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
		private = append(private, c.OutputDir, c.RuntimeDir)
		for _, o := range c.Overlays {
			private = append(private, o.Host)
		}
		return executeCommand884(t, ctx, c)
	}
	r := pa342Unchanged(t, f, tc)
	for _, name := range private {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("execution leaked private file or directory %s: %v", name, err)
		}
	}
	return r
}

// Exported only from this test file for the external receipt acceptance test.
func PerCheckDriverReport884(t *testing.T, mode string) (*Report, ToolCounts849) {
	t.Helper()
	tc, log := pa342Fake(t)
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(filepath.Dir(docker), "docker.py")
	body := string(read884(t, name))
	if !strings.Contains(body, "mounts={}") {
		t.Fatal("recording transport fixture moved")
	}
	// Record the executor's actual argv as well as the existing fake's
	// parsed invocations, so read-only mounts are checked at the boundary.
	body = strings.Replace(body, "mounts={}", "record({'kind':'command884','args':args})\nmounts={}", 1)
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := report884(t, mode, tc, false)
	for _, line := range bytes.Split(bytes.TrimSpace(read884(t, log)), []byte{'\n'}) {
		var row struct {
			Kind string
			Args []string
		}
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err)
		}
		if row.Kind != "command884" || !slices.Contains(row.Args, "/runtime/runner.py") {
			continue
		}
		for i, arg := range row.Args {
			if arg == "-v" && i+1 < len(row.Args) {
				parts := strings.Split(row.Args[i+1], ":")
				if len(parts) != 3 {
					t.Fatalf("invalid mount: %q", row.Args[i+1])
				}
				if (parts[1] == "/src" || strings.HasPrefix(parts[1], "/src/")) && parts[2] != "ro" {
					t.Fatal("application or rewritten driver mounted writable")
				}
			}
		}
	}
	return r, counts849(t, log)
}

func assertReport884(t *testing.T, r *Report, sampled bool) {
	t.Helper()
	if !r.Passed || r.Application == nil || len(r.Application.Conformance) != 2 || r.Components == nil || len(r.Components.Conformance) != 2 {
		t.Fatalf("independent drivers did not both pass: %+v", r)
	}
	var snapshot, image string
	seen := map[string]bool{}
	for _, x := range r.Application.Executions {
		if snapshot == "" {
			snapshot, image = x.Provenance.Fingerprint, x.Actual.Image
		}
		if x.Provenance.Fingerprint != snapshot || x.Actual.Image != image || !x.Success || !x.Complete {
			t.Fatalf("ordinary invocations did not retain one snapshot and image: %+v", x)
		}
		if x.Key.Kind != "conformance" {
			continue
		}
		seen[x.Key.ID] = true
		if !slices.Equal(x.Actual.Args[6:], x.Declared.Args) || x.Actual.WorkingDir != path.Join("/src", x.Declared.WorkingDir) || !reflect.DeepEqual(x.Actual.Sampling, x.Declared.Sampling) || len(x.Actual.Overlays) != 0 {
			t.Fatal("ordinary invocation lost its own argv, cwd or budgets")
		}
		original := ""
		for _, f := range x.Provenance.Files {
			if f.Path == driverPath884(x.Key.ID) {
				original = f.SHA256
			}
		}
		if original == "" || len(x.Observations) == 0 {
			t.Fatal("missing original driver or authenticated observations")
		}
		for _, o := range x.Observations {
			if o.Key != x.Key || o.Driver != x.Key.Check || o.DriverSHA256 != original || o.Target != x.Key.Target {
				t.Fatal("ordinary observations belong to another driver or target")
			}
		}
		if !sampled && (x.Exploration == nil || x.Exploration.States != 3 || x.Exploration.Depth != 3) {
			t.Fatal("ordinary exploration did not use the ratified bound")
		}
	}
	if !seen["first"] || !seen["second"] {
		t.Fatal("one of the two drivers was never executed")
	}
	for _, c := range r.Application.Conformance {
		if !c.passed() || c.Bounds["Limit"] != "2" || c.Result.States != 3 {
			t.Fatalf("lost ratified-bound conformance: %+v", c)
		}
	}
	if sampled {
		if r.Larger != nil || len(r.Application.Larger) != 0 {
			t.Fatal("sampled drivers acquired larger-bound evidence")
		}
		return
	}
	if len(r.Application.Larger) != 2 || r.Larger == nil || !r.Larger.Required || !r.Larger.Passed {
		t.Fatalf("missing larger comparison for both drivers: %+v", r.Larger)
	}
	for _, l := range r.Application.Larger {
		x := l.Execution
		if x == nil || !x.Complete || !x.Success || x.Exploration == nil || x.Exploration.States != 4 || x.Exploration.Depth != 4 || !l.Result.Required || !l.Result.Passed || l.Bounds["Limit"] != "3" {
			t.Fatalf("larger driver did not use its own rewritten constant: %+v", l)
		}
		if l.Original.Path != driverPath884(l.Key.ID) || l.Driver.Path != l.Original.Path || l.Original.SHA256 == l.Driver.SHA256 || l.Original.SHA256 == "" || l.Driver.SHA256 == "" || x.Key != l.Key {
			t.Fatalf("wrong per-driver larger provenance: %+v", l)
		}
		if x.Provenance.Fingerprint != snapshot || x.Actual.Image != image || !slices.Equal(x.Actual.Args[6:], x.Declared.Args) || x.Actual.WorkingDir != path.Join("/src", x.Declared.WorkingDir) || x.Actual.Env["CHECK884"] != l.Key.ID || x.Actual.Sampling["seed"] != x.Declared.Sampling["seed"] || x.Actual.Sampling["runs"] != "2147483647" || x.Actual.Sampling["steps"] != "2147483647" {
			t.Fatal("larger driver lost its snapshot, image, arguments, environment or budgets")
		}
		if !slices.Contains(x.Provenance.Files, l.Original) || !slices.Equal(x.Actual.Overlays, []PythonApplicationOverlayFile{{Path: l.Driver.Path, SHA256: l.Driver.SHA256}}) {
			t.Fatal("larger overlay or original bytes not bound to this invocation")
		}
		for _, o := range x.Observations {
			if o.Key != l.Key || o.Driver != l.Key.Check || o.DriverSHA256 != l.Driver.SHA256 || o.Target != l.Key.Target {
				t.Fatal("larger observations were credited to another declaration or source")
			}
		}
	}
}

func TestPythonApplicationPerCheckDrivers884(t *testing.T) {
	// The positive preflight is also the baseline failure, before tools run.
	admit884(t, fixture884(t, false, false))
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		for _, mode := range []string{"success", "sampled"} {
			t.Run(mode, func(t *testing.T) { assertReport884(t, report884(t, mode, tc, real), mode == "sampled") })
		}
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("manifest driver explicit=%v", explicit), func(t *testing.T) {
				f := pa342New(t, explicit, real)
				if explicit {
					// Distinct selected declarations can share a driver file.
					pa342Edit(t, f, pa342DriverPath, "if __name__ == '__main__':\n    drive()", "def drive_again():\n    explore(start, STEPS)\n\n\nif __name__ == '__main__':\n    import sys\n    if len(sys.argv) > 1:\n        drive_again()\n    else:\n        drive()")
					v := f.p.Manifest.Components.Checks[0].Invocations[1]
					v.Check = pa342Ref(pa342DriverPath, "drive_again")
					v.ID, v.Args = "again", []string{"driver.py", "extra argument"}
					f.p.Manifest.Components.Checks[0].Conformance = append(f.p.Manifest.Components.Checks[0].Conformance, v.Check)
					f.p.Manifest.Components.Checks[0].Invocations = append(f.p.Manifest.Components.Checks[0].Invocations, v)
					pa342Save(t, f)
				}
				r := pa342Unchanged(t, f, tc)
				want := 1
				if explicit {
					want = 2
				}
				if !r.Passed || len(r.Application.Larger) != want || r.Larger == nil || !r.Larger.Passed || (!explicit && r.Components != nil) {
					t.Fatal("legacy or shared-driver configuration regressed")
				}
				for _, l := range r.Application.Larger {
					if l.Original.Path != pa342DriverPath || l.Driver.Path != pa342DriverPath || l.Execution == nil || !l.Execution.Success {
						t.Fatal("legacy or shared-driver larger binding regressed")
					}
				}
			})
		}
	})
}

func TestPythonApplicationPerCheckLargerEligibility884(t *testing.T) {
	admit884(t, fixture884(t, false, false))
	for _, mode := range []string{"success", "no first bounds", "no second bounds", "no bounds", "execution", "both executions", "states", "steps", "transition", "first mismatch", "second mismatch"} {
		t.Run(mode, func(t *testing.T) {
			r, calls := PerCheckDriverReport884(t, mode)
			if calls.Base["first"] != 1 || calls.Base["second"] != 1 || calls.Base["unit"] != 1 {
				t.Fatalf("ordinary independent work was suppressed: %+v", calls)
			}
			blocked := mode == "execution" || mode == "both executions" || mode == "states" || mode == "steps" || mode == "transition"
			mismatch := strings.HasSuffix(mode, "mismatch")
			if r.Passed != (!blocked && !mismatch) {
				t.Fatalf("wrong overall verdict for %s: %v", mode, r.Passed)
			}
			wantTLC, wantRows := 1, 0
			if mode == "no bounds" || mode == "both executions" {
				wantTLC = 0
			}
			if calls.LargerTLC != wantTLC {
				t.Fatalf("shared larger model ran %d times, want %d", calls.LargerTLC, wantTLC)
			}
			for _, id := range []string{"first", "second"} {
				noBounds := mode == "no bounds" || mode == "no "+id+" bounds"
				skip := blocked && (id == "first" || mode == "both executions")
				wantRun := 1
				if noBounds || skip {
					wantRun = 0
				}
				if calls.Larger[id] != wantRun {
					t.Errorf("%s larger runs=%d, want %d", id, calls.Larger[id], wantRun)
				}
				index := slices.IndexFunc(r.Application.Larger, func(l PythonApplicationLarger) bool { return l.Key.ID == id })
				if noBounds {
					if index >= 0 {
						t.Error("nonapplicable driver acquired a larger record")
					}
					continue
				}
				wantRows++
				if index < 0 {
					t.Fatalf("missing larger record for %s", id)
				}
				l := r.Application.Larger[index]
				if l.Original.Path != driverPath884(id) || l.Original.SHA256 == "" || l.Bounds["Limit"] != "3" {
					t.Fatalf("larger record names another driver's source: %+v", l)
				}
				if skip {
					word := map[string]string{"execution": "execution", "both executions": "execution", "states": "state", "steps": "step", "transition": "transition"}[mode]
					if l.Execution != nil || l.Result.Required || l.Result.Passed || !strings.Contains(strings.Join(l.Skipped, " "), word) {
						t.Fatalf("missing own prerequisite reason or invented execution: %+v", l)
					}
				} else if len(l.Skipped) != 0 || l.Execution == nil || !l.Execution.Success || !l.Result.Required || l.Result.Passed != (mode != id+" mismatch") {
					t.Fatalf("eligible driver's actual verdict was lost: %+v", l)
				}
			}
			if len(r.Application.Larger) != wantRows {
				t.Fatal("extra or missing larger records")
			}
			if wantTLC == 0 {
				if r.Larger != nil && (r.Larger.Passed || r.Larger.Required) {
					t.Fatal("no larger work claimed a verdict")
				}
			} else if r.Larger == nil || !r.Larger.Required || r.Larger.Passed == mismatch {
				t.Fatal("overall larger result is not the weakest actual result")
			}
			for _, c := range r.Application.Conformance {
				if (!blocked || c.Key.ID == "second" && mode != "both executions") && !c.passed() {
					t.Fatal("larger work erased independent passing base conformance")
				}
			}
		})
	}
}

func prepare884(t *testing.T, f pa342Fixture, execute func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error)) (*PythonApplicationSession, string, PythonApplicationInvocationKey) {
	t.Helper()
	stage := filepath.Join(t.TempDir(), "stage")
	s, err := PreparePythonApplicationSession(context.Background(), f.p, stage, PythonApplicationSessionOptions{
		SelectImage: func(context.Context, *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
			return toolchain.PythonImage, nil, nil
		}, Execute: execute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, v := range s.Invocations() {
		if v.Key.ID == "second" {
			return s, stage, v.Key
		}
	}
	t.Fatal("missing second invocation")
	return nil, "", PythonApplicationInvocationKey{}
}

// A trusted test transport can re-sign a deliberately stale observation to
// isolate source binding from authentication, as #793/#849's tests do.
func rebind884(t *testing.T, events, secret []byte, field, value string) []byte {
	t.Helper()
	var out bytes.Buffer
	e := json.NewEncoder(&out)
	changed := 0
	for _, line := range bytes.Split(bytes.TrimSpace(events), []byte{'\n'}) {
		var row map[string]any
		d := json.NewDecoder(bytes.NewReader(line))
		d.UseNumber()
		if err := d.Decode(&row); err != nil {
			t.Fatal(err)
		}
		if row["type"] == "authentication" {
			continue
		}
		if row["type"] == "observation" {
			row[field] = value
			changed++
		}
		if err := e.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if changed == 0 {
		t.Fatal("test transport changed no observations")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(out.Bytes())
	if err := e.Encode(map[string]string{"type": "authentication", "hmac_sha256": hex.EncodeToString(mac.Sum(nil))}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestPythonApplicationPerCheckDriverIsolation884(t *testing.T) {
	admit884(t, fixture884(t, false, false))
	for _, mode := range []string{"mismatched source", "absolute", "escape and return", "symlink file", "symlink and parent", "directory"} {
		t.Run(mode, func(t *testing.T) {
			f := fixture884(t, false, false)
			v := &f.p.Manifest.Components.Checks[1].Invocations[0]
			switch mode {
			case "mismatched source":
				v.Args[0] = "../" + pa342DriverPath
			case "absolute":
				v.Args[0] = filepath.Join(f.root, secondDriver884)
			case "escape and return":
				v.Args[0] = "../../" + filepath.Base(f.root) + "/" + secondDriver884
			case "symlink file":
				if err := os.Symlink("secondary/driver.py", filepath.Join(f.root, "checks/alias.py")); err != nil {
					t.Fatal(err)
				}
				v.Args[0] = "alias.py"
			case "symlink and parent":
				if err := os.Symlink("secondary", filepath.Join(f.root, "checks/link")); err != nil {
					t.Fatal(err)
				}
				v.Args[0] = "link/../secondary/driver.py"
			case "directory":
				v.Args[0] = "secondary"
			}
			pa342Save(t, f)
			selected := 0
			s, err := PreparePythonApplicationSession(context.Background(), f.p, filepath.Join(t.TempDir(), "stage"), pa315Options(&selected))
			if s != nil {
				s.Close()
			}
			if err == nil || s != nil || selected != 0 || Check(f.p.Dir) == nil {
				t.Fatal("invalid driver reached staging/image selection or was admitted")
			}
		})
	}
	t.Run("contained parent and same file", func(t *testing.T) {
		f := fixture884(t, false, false)
		f.p.Manifest.Components.Checks[1].Invocations[0].Args[0] = "../checks/secondary/driver.py"
		pa342Save(t, f)
		admit884(t, f)
		prepare884(t, f, nil)
	})
	for _, larger := range []bool{false, true} {
		t.Run(fmt.Sprintf("stale staged driver larger=%v", larger), func(t *testing.T) {
			f := fixture884(t, false, false)
			before, ran := pa342Tree(t, f.root), 0
			s, stage, key := prepare884(t, f, func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error) {
				ran++
				return PythonApplicationProcess{}, nil
			})
			pa342Write(t, stage, secondDriver884, string(read884(t, filepath.Join(stage, secondDriver884)))+"\n# changed after preparation\n")
			var err error
			if larger {
				_, _, err = s.RunLarger(context.Background(), key, map[string]int{"Limit": 3})
			} else {
				_, err = s.Run(context.Background(), key)
			}
			if err == nil || ran != 0 {
				t.Fatal("stale driver executed")
			}
			unchanged884(t, f.root, before)
		})
	}
	for _, mode := range []string{"old driver hash", "other driver hash", "target hash", "unauthenticated", "changed overlay", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			pa342Fake(t)
			f := fixture884(t, false, false)
			before := pa342Tree(t, f.root)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			var private []string
			ran := 0
			s, stage, key := prepare884(t, f, func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				ran++
				private = append(private, c.OutputDir, c.Overlays[0].Host)
				if mode == "cancel" {
					cancel()
					return PythonApplicationProcess{ExitCode: -1}, ctx.Err()
				}
				secret := read884(t, filepath.Join(c.OutputDir, "authentication.key"))
				p, err := executeCommand884(t, ctx, c)
				if err != nil || p.ExitCode != 0 {
					t.Fatalf("honest driver failed before transport fault: %v %s", err, p.Stderr)
				}
				switch mode {
				case "old driver hash":
					p.Events = rebind884(t, p.Events, secret, "driver_sha256", before[secondDriver884])
				case "other driver hash":
					p.Events = rebind884(t, p.Events, secret, "driver_sha256", before[pa342DriverPath])
				case "target hash":
					p.Events = rebind884(t, p.Events, secret, "source_sha256", "sha256:"+strings.Repeat("0", 64))
				case "unauthenticated":
					wrong := bytes.Repeat([]byte{1}, 32)
					p.Events = rebind884(t, p.Events, wrong, "driver_sha256", pythonApplicationHash(read884(t, c.Overlays[0].Host)))
				case "changed overlay":
					if err := os.Chmod(c.Overlays[0].Host, 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(c.Overlays[0].Host, []byte("# replaced private driver\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				return p, nil
			})
			staged := pa342Tree(t, stage)
			x, _, err := s.RunLarger(ctx, key, map[string]int{"Limit": 3})
			if err == nil || ran != 1 || x == nil || x.Success || x.Complete || len(x.Observations) != 0 || x.Exploration != nil {
				t.Fatalf("invalid larger execution credited: %+v %v", x, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation lost: %v", err)
			}
			for _, name := range private {
				if _, err := os.Stat(name); !os.IsNotExist(err) {
					t.Fatalf("private resource survived failed run: %s", name)
				}
			}
			unchanged884(t, stage, staged)
			unchanged884(t, f.root, before)
		})
	}
	t.Run("all bounds and a valid explored key are required", func(t *testing.T) {
		f := fixture884(t, false, false)
		ran := 0
		s, _, key := prepare884(t, f, func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error) {
			ran++
			return PythonApplicationProcess{}, nil
		})
		if _, _, err := s.RunLarger(context.Background(), key, map[string]int{"Limit": 3, "Other": 4}); err == nil {
			t.Fatal("partial bound declarations accepted")
		}
		unknown := key
		unknown.ID = "absent"
		invocations := s.Invocations()
		unit := slices.IndexFunc(invocations, func(v PythonApplicationInvocation) bool { return v.Key.Kind == "test" })
		if unit < 0 {
			t.Fatal("fixture has no test invocation")
		}
		for _, invalid := range []PythonApplicationInvocationKey{unknown, invocations[unit].Key} {
			if _, _, err := s.RunLarger(context.Background(), invalid, map[string]int{"Limit": 3}); err == nil {
				t.Fatal("unknown or test invocation ran larger")
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := s.RunLarger(ctx, key, map[string]int{"Limit": 3}); !errors.Is(err, context.Canceled) {
			t.Fatal("pre-cancelled larger run lost cancellation")
		}
		if ran != 0 {
			t.Fatal("ineligible call executed")
		}
	})
}
