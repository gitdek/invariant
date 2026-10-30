package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The default layer inspects every production Python entry point, including
// compileall. It checks configured seed and retained startup isolation, but
// does not pretend a Docker double can establish Python's hashing behavior.
// The real layer builds the generated recipe, then observes unchanged checks:
// unit tests, ordinary drivers and larger drivers must all match an independent
// seed-0 reference and repeat the same set-selected operation trace. A literal
// -I plus PYTHONHASHSEED=0 passes the configuration check and fails that runtime
// oracle. Both the dependency and standard-library paths are covered.
//
// INVARIANT_PYTHON_DEPS_REAL=1 go test ./internal/verify -run '^TestPythonDependenc' -count=1 -timeout 30m
func TestPythonDependenciesKeepHashSeedDeterministic(t *testing.T) {
	for _, dependencies := range []bool{true, false} {
		name := "without requirements"
		if dependencies {
			name = "with requirements"
		}
		t.Run(name, func(t *testing.T) {
			t.Run("launcher contract", func(t *testing.T) {
				pyDepsFakeDocker(t)
				root := pyDepsHashSeedFixture(t, dependencies)
				p, err := project.Load(root)
				if err != nil {
					t.Fatal(err)
				}
				image := toolchain.PythonImage
				if dependencies {
					image = pyDepsBuild(t, root).Image
				}
				b, evidence, err := (Python{Image: toolchain.PythonImage}).Check(context.Background(), p)
				if err != nil || !b.Passed || len(evidence.Traces) == 0 {
					t.Fatalf("ordinary checks unavailable: build=%#v evidence=%#v error=%v", b, evidence, err)
				}
				larger, err := countLarger(context.Background(), image, "python", p, map[string]int{"Capacity": 2})
				if err != nil || !larger.OK {
					t.Fatalf("larger check unavailable: %#v %v", larger, err)
				}
				seen := map[bool]bool{}
				for _, call := range pyDepsCalls(t) {
					if call.Kind != "application" {
						continue
					}
					seen[call.Count] = true
					if call.Environment["PYTHONHASHSEED"] != "0" {
						t.Errorf("check did not configure the fixed hash seed: %#v", call)
					}
					command := append([]string(nil), call.Command...)
					if call.Entrypoint != "" {
						command = append([]string{call.Entrypoint}, command...)
					}
					if len(command) != 3 || !pyDepsTrustedExecutable(command[0]) || path.Base(command[0]) != "sh" || command[1] != "-c" {
						t.Fatalf("unexpected trusted check launcher: %#v", call)
					}
					pyDepsRequireTrustedHelpers(t, command[2])
					pyDepsRequireIsolatedApplicationPython(t, command[2])
					// These markers require ordinary compilation, tests and the
					// driver to remain separate observable gate steps.
					markers := []string{"@@invariant compile=", "@@invariant test=", "@@invariant conform="}
					if call.Count {
						markers = []string{"@@invariant count="}
					}
					for _, marker := range markers {
						if !strings.Contains(command[2], marker) {
							t.Errorf("Python launcher lost %s: %s", marker, command[2])
						}
					}
				}
				if !seen[false] || !seen[true] {
					t.Fatal("hash-seed contract did not exercise ordinary and larger checks")
				}
			})
			if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
				t.Run("real repeated interpreter traces", func(t *testing.T) {
					pyDepsRealHashSeed(t, dependencies)
				})
			}
		})
	}
}

// String hashes expose the actual interpreter seed, while a seeded PRNG that
// chooses from a set reproduces the user-visible trace instability. Neither
// sorting the final output nor setting INVARIANT_SEED repairs randomized hashes.
const pyDepsHashSeedProgram = `import json, random
def fingerprint():
    words = ["operation-%02d" % n for n in range(64)]
    operations = list(set(words))
    rng = random.Random(271828)
    return {"hashes": [hash(word) for word in words],
            "order": operations,
            "traces": [[{"operation": rng.choice(operations)} for _ in range(40)] for _ in range(4)]}
`

func pyDepsHashSeedFixture(t *testing.T, dependencies bool) string {
	t.Helper()
	root := pyDepsFixture(t)
	packageCheck := ""
	if dependencies {
		pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("# hash-seed regression %d\n-r deps/application.txt\n", time.Now().UnixNano()))
		packageCheck = "from packaging.version import Version\nassert Version('1.9') < Version('1.10')\n"
	} else if err := os.Remove(filepath.Join(root, "requirements.txt")); err != nil {
		t.Fatal(err)
	}
	guard := `import builtins, os, sys
assert not hasattr(builtins, "INVARIANT_HASH_STARTUP"), "application startup hook ran"
assert sys.flags.no_user_site and sys.flags.safe_path, "Python startup isolation was lost"
assert "INVARIANT_HASH_HOST_SECRET" not in os.environ
assert not os.environ.get("PYTHONHOME")
assert not os.environ.get("PYTHONUSERBASE")
assert not os.environ.get("PYTHONSTARTUP")
`
	pyDepsWrite(t, root, "hash_probe.py", guard+packageCheck+pyDepsHashSeedProgram)
	pyDepsWrite(t, root, "test_dependency.py", `import json, os, unittest
from hash_probe import fingerprint
class HashSeedTest(unittest.TestCase):
    def test_observe_interpreter(self):
        output = os.path.join(os.path.dirname(os.environ["INVARIANT_TRACES"]), "hash-unit.json")
        with open(output, "w") as f:
            json.dump(fingerprint(), f, sort_keys=True)
`)
	pyDepsWrite(t, root, "conformance.py", `import json, os
from hash_probe import fingerprint
Capacity = 1
observed = fingerprint()
output = os.environ.get("INVARIANT_COUNT", os.environ.get("INVARIANT_TRACES"))
with open(os.path.join(os.path.dirname(output), "hash-driver.json"), "w") as f:
    json.dump(observed, f, sort_keys=True)
with open(output, "w") as f:
    if "INVARIANT_COUNT" in os.environ:
        json.dump({"states": Capacity + 1, "depth": Capacity + 1}, f)
    else:
        json.dump({"traces": observed["traces"]}, f, sort_keys=True)
`)
	// These must become importable application files only after startup. The
	// hostile installed .pth/sitecustomize wheels remain covered separately.
	for _, name := range []string{"sitecustomize.py", "usercustomize.py"} {
		pyDepsWrite(t, root, name, "import builtins\nbuiltins.INVARIANT_HASH_STARTUP = True\n")
	}
	return root
}

func pyDepsRealHashSeed(t *testing.T, dependencies bool) {
	t.Helper()
	pyDepsRequireRealDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	root := pyDepsHashSeedFixture(t, dependencies)
	before := pyDepsFiles(t, root)
	image := toolchain.PythonImage
	var environment *toolchain.PythonDependencies
	if dependencies {
		// Production must acquire the base before the independent reference.
		// The unique input forces execution of the generated recipe with real
		// pip, rather than using a hand-built image or a Docker CLI double.
		environment = pyDepsBuild(t, root)
		image = environment.Image
		pyDepsCleanupRecipeImage(t, image)
		pyDepsAssertEnvironment(t, root, environment, "24.2")
	} else {
		pyDepsPreparePinnedPipOracle(t)
	}
	pyDepsRequirePinnedPipDocker(t)
	// Use the pinned base, independently of the gate's startup bootstrap, to
	// establish seed 0. Changing only the seed must change both hashes and trace.
	reference := func(seed string) []byte {
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--pull=never", "--network", "none", "--platform", "linux/amd64", "-w", "/tmp",
			"-e", "PYTHONHASHSEED="+seed, "-e", "PYTHONPATH=", "-e", "PYTHONHOME=", toolchain.PythonImage,
			"/usr/local/bin/python", "-S", "-s", "-P", "-c", pyDepsHashSeedProgram+"print(json.dumps(fingerprint(), sort_keys=True))")
		out, err := cmd.CombinedOutput()
		if err != nil || !json.Valid(bytes.TrimSpace(out)) {
			t.Fatalf("independent pinned-Python hash reference: %v\n%s", err, out)
		}
		return bytes.TrimSpace(out)
	}
	want, other := reference("0"), reference("1")
	var zero, one struct {
		Hashes []int64         `json:"hashes"`
		Traces json.RawMessage `json:"traces"`
	}
	if json.Unmarshal(want, &zero) != nil || json.Unmarshal(other, &one) != nil ||
		reflect.DeepEqual(zero.Hashes, one.Hashes) || bytes.Equal(zero.Traces, one.Traces) {
		t.Fatal("hash-seed fixture does not distinguish seed 0 from seed 1")
	}
	log := pyDepsHashSeedObserver(t)
	// Ignoring all PYTHON* variables would also ignore the required seed.
	// Only the gate's deliberate values may survive into the new interpreter.
	poison := t.TempDir()
	pyDepsWrite(t, poison, "sitecustomize.py", "raise RuntimeError('host startup path leaked')\n")
	for key, value := range map[string]string{
		"PYTHONHASHSEED": "17", "PYTHONPATH": poison, "PYTHONHOME": filepath.Join(poison, "invalid-home"),
		"PYTHONUSERBASE": poison, "PYTHONSTARTUP": filepath.Join(poison, "sitecustomize.py"),
		"INVARIANT_HASH_HOST_SECRET": "must-not-reach-checks",
	} {
		t.Setenv(key, value)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var firstTrace []byte
	for attempt := 0; attempt < 3; attempt++ {
		b, evidence, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
		if err != nil || !b.Passed || len(evidence.Traces) == 0 {
			t.Fatalf("ordinary interpreter lost startup isolation: build=%#v evidence=%#v error=%v", b, evidence, err)
		}
		if attempt == 0 {
			firstTrace = append([]byte(nil), evidence.Traces...)
		} else if !bytes.Equal(firstTrace, evidence.Traces) {
			t.Fatalf("identical driver seed produced different traces on repetition %d:\n%s\n%s", attempt, firstTrace, evidence.Traces)
		}
		larger, err := countLarger(ctx, image, "python", p, map[string]int{"Capacity": 2})
		if err != nil || !larger.OK || larger.States != 3 || larger.Depth != 3 {
			t.Fatalf("larger interpreter lost startup isolation: %#v %v", larger, err)
		}
	}
	observed, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(observed), []byte{'\n'})
	if len(lines) != 6 {
		t.Fatalf("expected three ordinary and three larger observations, got %d: %s", len(lines), observed)
	}
	counts := map[bool]int{}
	for _, line := range lines {
		var call pyDepsHashSeedCall
		if err := json.Unmarshal(line, &call); err != nil {
			t.Fatal(err)
		}
		counts[call.Count]++
		pyDepsRequireHashFingerprint(t, "driver", call.Driver, want)
		if !call.Count {
			pyDepsRequireHashFingerprint(t, "unittest", call.Unit, want)
		}
	}
	if counts[false] != 3 || counts[true] != 3 {
		t.Fatalf("missed interpreter paths: %#v", counts)
	}
	if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("checks rewrote the application's inputs")
	}
	if dependencies && !reflect.DeepEqual(environment, pyDepsBuild(t, root)) {
		t.Fatal("repeated checks changed the resolved dependency evidence")
	}
}

func pyDepsRequireHashFingerprint(t *testing.T, phase string, actual, expected []byte) {
	t.Helper()
	var got, want any
	decode := func(raw []byte, value *any) error {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber() // retain every bit of the interpreter's 64-bit hashes
		return decoder.Decode(value)
	}
	if !json.Valid(actual) || !json.Valid(expected) || decode(actual, &got) != nil || decode(expected, &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s interpreter did not retain hash seed 0 and identical set-selected traces:\ngot %s\nwant %s", phase, actual, expected)
	}
}

type pyDepsHashSeedCall struct {
	Count  bool            `json:"count"`
	Driver json.RawMessage `json:"driver"`
	Unit   json.RawMessage `json:"unit,omitempty"`
}

// countLarger consumes only counts and removes its /out directory. Observe
// the driver's complete trace before that cleanup, without replacing its
// command, script, environment, stdout, result or evidence.
func pyDepsHashSeedObserver(t *testing.T) string {
	t.Helper()
	target, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "hash-seed.jsonl")
	t.Setenv("INVARIANT_PY_DEPS_HASH_BINARY", binary)
	t.Setenv("INVARIANT_PY_DEPS_HASH_TARGET", target)
	t.Setenv("INVARIANT_PY_DEPS_HASH_LOG", log)
	script := "#!/bin/sh\nINVARIANT_PY_DEPS_HASH_HELPER=1 exec \"$INVARIANT_PY_DEPS_HASH_BINARY\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func init() {
	if os.Getenv("INVARIANT_PY_DEPS_HASH_HELPER") == "1" {
		os.Exit(pyDepsHashSeedDocker(os.Args[1:]))
	}
}

func pyDepsHashSeedDocker(args []string) int {
	fail := func(err error) int { fmt.Fprintln(os.Stderr, err); return 73 }
	cmd := exec.Command(os.Getenv("INVARIANT_PY_DEPS_HASH_TARGET"), args...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "INVARIANT_PY_DEPS_HASH_HELPER=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		return fail(err)
	}
	_, command, err := pyDepsProxyClientArguments(args)
	if err != nil {
		return fail(err)
	}
	if len(command) == 0 || command[0] != "run" {
		return 0
	}
	options, _, environment, err := pyDepsProxyRunArguments(command[1:])
	if err != nil {
		return fail(err)
	}
	if environment["DRIVER"] == "" {
		return 0
	}
	out := ""
	for i := 0; i < len(options); i++ {
		key, value, joined := strings.Cut(options[i], "=")
		if key != "-v" && key != "--volume" && key != "--mount" {
			continue
		}
		if !joined {
			i++
			if i >= len(options) {
				return fail(fmt.Errorf("missing mount value"))
			}
			value = options[i]
		}
		if key == "--mount" {
			source, destination := "", ""
			for _, field := range strings.Split(value, ",") {
				name, content, _ := strings.Cut(field, "=")
				switch name {
				case "src", "source":
					source = content
				case "dst", "destination", "target":
					destination = content
				}
			}
			if destination == "/out" {
				out = source
			}
		} else if fields := strings.Split(value, ":"); len(fields) >= 2 && fields[1] == "/out" {
			out = fields[0]
		}
	}
	if out == "" {
		return fail(fmt.Errorf("hash-seed observer found no check output mount"))
	}
	call := pyDepsHashSeedCall{Count: environment["INVARIANT_COUNT"] != ""}
	call.Driver, err = os.ReadFile(filepath.Join(out, "hash-driver.json"))
	if err != nil || !json.Valid(call.Driver) {
		return fail(fmt.Errorf("missing driver hash/trace observation: %v", err))
	}
	if !call.Count {
		call.Unit, err = os.ReadFile(filepath.Join(out, "hash-unit.json"))
		if err != nil || !json.Valid(call.Unit) {
			return fail(fmt.Errorf("missing unit-test hash/trace observation: %v", err))
		}
	}
	f, err := os.OpenFile(os.Getenv("INVARIANT_PY_DEPS_HASH_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fail(err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(call); err != nil {
		return fail(err)
	}
	return 0
}
