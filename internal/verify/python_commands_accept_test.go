package verify

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The CLI layer checks the actual commands crossing the Docker boundary.
// The real layer first executes the unmodified generated recipe, then makes
// an adversarial cached image by installing a fixture wheel with real pip,
// offline. That wheel installs cat, sh and timeout. Its genuine pip report
// is combined with the recipe's report, retaining both artifact hashes.
// This cache fixture does not add support for local-wheel requirements.
//
// Required on the pull request head before merge, with the earlier suite:
// INVARIANT_PYTHON_DEPS_REAL=1 go test ./internal/verify -run '^TestPythonDependenc' -count=1 -timeout 30m
func TestPythonDependenciesKeepGateCommandsTrusted(t *testing.T) {
	root := pyDepsFixture(t)
	pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("# command regression %d\n-r deps/application.txt\n", time.Now().UnixNano()))
	// Test network isolation in the ordinary AND larger-bound drivers.
	driver := pyDepsFiles(t, root)["conformance.py"]
	driver = "import socket\ntry:\n    socket.create_connection(('198.51.100.1', 443), timeout=0.2)\nexcept OSError:\n    pass\nelse:\n    raise AssertionError('driver has network access')\n" + driver
	pyDepsWrite(t, root, "conformance.py", driver)
	var reportPath string
	t.Run("trusted command boundary", func(t *testing.T) {
		pyDepsFakeDocker(t)
		tc := toolchain.Toolchain{PythonImage: toolchain.PythonImage, JavaImage: "accept-java", TLCJar: filepath.Join(t.TempDir(), "tlc.jar")}
		r, err := Run(context.Background(), root, "", tc)
		pyDepsRequireGateChecks(t, r, err)
		var evidence, ordinary, larger bool
		for _, call := range pyDepsCalls(t) {
			if call.Kind != "evidence" && call.Kind != "application" {
				continue
			}
			if len(call.Command) == 0 || !pyDepsTrustedExecutable(call.Command[0]) {
				t.Fatalf("gate command can resolve through dependency-controlled PATH: %#v", call)
			}
			if call.Kind == "evidence" {
				evidence = true
			} else {
				if path.Base(call.Command[0]) != "sh" {
					t.Fatalf("check launcher must be the trusted shell: %#v", call.Command)
				}
				if call.Count {
					larger = true
				} else {
					ordinary = true
				}
			}
			if path.Base(call.Command[0]) == "sh" {
				if len(call.Command) != 3 || call.Command[1] != "-c" {
					t.Fatalf("unexpected sandbox launch: %#v", call.Command)
				}
				pyDepsRequireTrustedHelpers(t, call.Command[2])
			}
		}
		if !evidence || !ordinary || !larger {
			t.Fatalf("missing execution paths: evidence=%t ordinary=%t larger=%t", evidence, ordinary, larger)
		}
		staged := pyDepsFiles(t, filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "context"))
		reportPath = pyDepsRecipeReportPath(t, staged["Dockerfile"])
	})
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
		t.Run("installed commands cannot replace gate commands", func(t *testing.T) {
			pyDepsRealTrustedCommands(t, root, reportPath)
		})
	}
}

func pyDepsTrustedExecutable(command string) bool {
	// These directories belong to the pinned base, outside the application
	// venv. Shell builtins do not consult PATH and need no executable path.
	return path.IsAbs(command) && path.Clean(command) == command && path.Base(command) != "env" &&
		(path.Dir(command) == "/bin" || path.Dir(command) == "/usr/bin" || path.Dir(command) == "/usr/local/bin")
}

func pyDepsRequireTrustedHelpers(t *testing.T, script string) {
	t.Helper()
	// The gate's scripts consist of direct commands separated by newlines
	// and shell operators. Inspect command positions, not Python arguments.
	commands := regexp.MustCompile(`(?:^|[\n;|]|&&)\s*([^\s;]+)([^\n;|]*)`)
	python := regexp.MustCompile(`^python(?:[0-9]+(?:\.[0-9]+)*)?$`)
	for _, match := range commands.FindAllStringSubmatch(script, -1) {
		command := match[1]
		if command == "exec" {
			args := strings.Fields(match[2])
			if len(args) == 1 && args[0] == "2>&1" {
				continue
			}
			if len(args) == 0 {
				t.Fatal("unexpected empty exec in check script")
			}
			command = args[0]
		}
		switch command {
		case "cd", "echo", "printf", ":", "true", "false", "test", "[":
			continue // builtins of the trusted shell
		}
		if python.MatchString(path.Base(command)) {
			continue // the application intentionally uses its own Python
		}
		if !pyDepsTrustedExecutable(command) {
			t.Errorf("check helper %q can resolve through application PATH:\n%s", command, script)
		}
	}
}

// Locate the saved pip report in the generated recipe, without imposing a
// new production path/API just to set up the adversarial cache fixture.
func pyDepsRecipeReportPath(t *testing.T, recipe string) string {
	t.Helper()
	recipe = strings.ReplaceAll(strings.ReplaceAll(recipe, "\\\r\n", " "), "\\\n", " ")
	cwd, report := "", ""
	for _, line := range strings.Split(recipe, "\n") {
		line = strings.TrimSpace(line)
		word, body, _ := strings.Cut(line, " ")
		switch strings.ToUpper(word) {
		case "WORKDIR":
			args := pyDepsRecipeArguments(t, strings.TrimSpace(body))
			if len(args) == 1 {
				cwd = args[0]
			}
		case "RUN":
			args := pyDepsRecipeArguments(t, strings.TrimSpace(body))
			for i, arg := range args {
				value := ""
				if arg == "--report" && i+1 < len(args) {
					value = args[i+1]
				} else if strings.HasPrefix(arg, "--report=") {
					value = strings.TrimPrefix(arg, "--report=")
				}
				if value != "" {
					if !path.IsAbs(value) {
						value = path.Join(cwd, value)
					}
					report = value
				}
			}
		}
	}
	if !path.IsAbs(report) {
		t.Fatalf("generated recipe has no saved pip report: %s", recipe)
	}
	return report
}

func pyDepsRequireGateChecks(t *testing.T, r *Report, err error) {
	t.Helper()
	if err != nil || r == nil || !r.Passed || !r.Build.Passed || r.Conformance == nil || !r.Conformance.Passed ||
		r.Larger == nil || !r.Larger.Passed || r.Larger.States != 3 || r.Larger.Depth != 3 {
		t.Fatalf("gate checks changed under command shadowing: report=%#v error=%v", r, err)
	}
}

func pyDepsRealTrustedCommands(t *testing.T, root, reportPath string) {
	t.Helper()
	pyDepsRequireRealDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	docker := func(args ...string) []byte {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("Docker fixture %v: %v\n%s", args, err, out)
		}
		return bytes.TrimSpace(out)
	}
	// This runs the actual production recipe before installing the hostile
	// fixture. Keep its real packaging artifact as an independent oracle.
	first := pyDepsBuild(t, root)
	pyDepsCleanupRecipeImage(t, first.Image)
	pyDepsAssertEnvironment(t, root, first, "24.2")
	var tags []string
	if err := json.Unmarshal(docker("image", "inspect", "--format", "{{json .RepoTags}}", first.Image), &tags); err != nil || len(tags) == 0 {
		t.Fatalf("generated recipe image has no cache reference: %v %v", tags, err)
	}
	wheel, wheelHash := pyDepsCommandWheel(t)
	container := fmt.Sprintf("invariant-command-fixture-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		exec.CommandContext(cleanupCtx, "docker", "rm", "--force", container).Run()
	})
	// The base interpreter performs setup in isolated mode. pip uses the
	// application's venv, and reports the exact fixture wheel it installed.
	const install = `import json, os, shutil, subprocess, sys
report_path, wheel = sys.argv[1:]
app_python = shutil.which("python")
assert app_python and os.path.realpath(os.path.dirname(app_python)) not in ("/usr/local/bin", "/usr/bin", "/bin"), app_python
with open(report_path) as f:
    report = json.load(f)
subprocess.run([app_python, "-I", "-m", "pip", "--isolated", "install", "--no-index", "--no-deps", "--only-binary=:all:", "--report", "/tmp/shadow-report.json", wheel], check=True)
with open("/tmp/shadow-report.json") as f:
    fixture = json.load(f)
assert len(fixture["install"]) == 1
report["install"].extend(fixture["install"])
with open(report_path, "w") as f:
    json.dump(report, f)
`
	docker("run", "--name", container, "--network", "none", "--platform", "linux/amd64", "-w", "/tmp",
		"-v", filepath.Dir(wheel)+":/fixture:ro", first.Image, "/usr/local/bin/python", "-I", "-c", install, reportPath, "/fixture/"+filepath.Base(wheel))
	derived := string(docker("commit", container))
	if !pyDepsDigest.MatchString(derived) {
		t.Fatalf("fixture did not yield an immutable image ID: %q", derived)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		for _, tag := range tags {
			exec.CommandContext(cleanupCtx, "docker", "image", "rm", tag).Run()
		}
		exec.CommandContext(cleanupCtx, "docker", "image", "rm", derived).Run()
	})
	for _, tag := range tags {
		docker("tag", derived, tag)
	}
	// Negative controls prove these are installed, executable entry points
	// selected by PATH, not inert files that could never expose the bug.
	var forged struct {
		Install []any `json:"install"`
	}
	if err := json.Unmarshal(docker("run", "--rm", "--network", "none", "--platform", "linux/amd64", derived, "cat", reportPath), &forged); err != nil || forged.Install == nil || len(forged.Install) != 0 {
		t.Fatalf("installed cat did not forge an empty report: %#v %v", forged, err)
	}
	for _, name := range []string{"sh", "timeout"} {
		out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--platform", "linux/amd64", derived, name).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "INVARIANT_SHADOW_"+name) {
			t.Fatalf("installed %s did not intercept PATH: error=%v\n%s", name, err, out)
		}
	}
	selected := pyDepsBuild(t, root)
	if selected.Image != derived || selected.Recipe != first.Recipe || !reflect.DeepEqual(selected.Inputs, first.Inputs) || !reflect.DeepEqual(selected.CanonicalInputs, first.CanonicalInputs) {
		t.Fatalf("gate did not reuse the adversarial cached environment: %#v", selected)
	}
	if len(selected.Artifacts) != 2 {
		t.Fatalf("installed cat replaced the genuine installation reports: %#v", selected.Artifacts)
	}
	packaging, fixture := false, false
	for _, artifact := range selected.Artifacts {
		switch artifact.Name {
		case "packaging":
			packaging = reflect.DeepEqual(artifact, first.Artifacts[0])
		case "invariant-gate-shadow":
			fixture = artifact.Version == "1.0" && artifact.Filename == filepath.Base(wheel) && artifact.SHA256 == wheelHash
		}
	}
	if !packaging || !fixture {
		t.Fatalf("receipt evidence differs from the wheels actually installed: %#v", selected.Artifacts)
	}
	if again := pyDepsBuild(t, root); !reflect.DeepEqual(again, selected) {
		t.Fatalf("evidence changed on reuse: %#v != %#v", again, selected)
	}
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(ctx, root, "", tc)
	pyDepsRequireGateChecks(t, r, err)
	encoded, err := json.Marshal(r.Toolchain)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(selected)
	if string(fields["python_dependencies"]) != string(want) {
		t.Fatalf("gate receipt lost genuine dependency evidence: %s != %s", fields["python_dependencies"], want)
	}
	// Checks must still detect application failures, not merely print
	// successful markers. Each failure is planted independently.
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	original := pyDepsFiles(t, root)
	pyDepsWrite(t, root, "test_dependency.py", original["test_dependency.py"]+"\n    def test_planted_failure(self):\n        self.fail('INVARIANT_PLANTED_UNIT_FAILURE')\n")
	b, _, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
	if err != nil || b.Passed || !strings.Contains(b.Output, "INVARIANT_PLANTED_UNIT_FAILURE") {
		t.Fatalf("installed commands concealed a failing unittest: %#v %v", b, err)
	}
	pyDepsWrite(t, root, "test_dependency.py", original["test_dependency.py"])
	pyDepsWrite(t, root, "conformance.py", "raise RuntimeError('INVARIANT_PLANTED_DRIVER_FAILURE')\n"+original["conformance.py"])
	_, evidence, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
	if err != nil || len(evidence.Traces) != 0 || !strings.Contains(evidence.Message, "INVARIANT_PLANTED_DRIVER_FAILURE") {
		t.Fatalf("installed commands concealed failed conformance: %#v %v", evidence, err)
	}
	larger, err := countLarger(ctx, selected.Image, "python", p, map[string]int{"Capacity": 2})
	if err != nil || larger.OK || !strings.Contains(larger.Message, "INVARIANT_PLANTED_DRIVER_FAILURE") {
		t.Fatalf("installed commands concealed a failing larger driver: %#v %v", larger, err)
	}
	pyDepsWrite(t, root, "conformance.py", original["conformance.py"])
}

func pyDepsCommandWheel(t *testing.T) (string, string) {
	t.Helper()
	const dist = "invariant_gate_shadow-1.0.dist-info/"
	files := map[string]string{
		"invariant_gate_shadow.py": `import json, sys
def cat():
    with open(sys.argv[1]) as f:
        report = json.load(f)
    report["install"] = []
    print(json.dumps(report))
def sh():
    print("INVARIANT_SHADOW_sh", file=sys.stderr)
    sys.exit(86)
def timeout():
    print("INVARIANT_SHADOW_timeout", file=sys.stderr)
    sys.exit(87)
`,
		dist + "METADATA":         "Metadata-Version: 2.1\nName: invariant-gate-shadow\nVersion: 1.0\n",
		dist + "WHEEL":            "Wheel-Version: 1.0\nGenerator: invariant-acceptance\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		dist + "entry_points.txt": "[console_scripts]\ncat = invariant_gate_shadow:cat\nsh = invariant_gate_shadow:sh\ntimeout = invariant_gate_shadow:timeout\n",
	}
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var record strings.Builder
	for _, name := range names {
		sum := sha256.Sum256([]byte(files[name]))
		fmt.Fprintf(&record, "%s,sha256=%s,%d\n", name, base64.RawURLEncoding.EncodeToString(sum[:]), len(files[name]))
	}
	record.WriteString(dist + "RECORD,,\n")
	files[dist+"RECORD"] = record.String()
	names = append(names, dist+"RECORD")
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range names {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "invariant_gate_shadow-1.0-py3-none-any.whl")
	if err := os.WriteFile(filename, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return filename, pyDepsHash(buf.Bytes())
}
