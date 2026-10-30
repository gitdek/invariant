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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The default layer checks the generated recipe and both actual launcher
// boundaries, without pretending the Docker double executes imports. The
// real layer executes the production recipe, installs a real hostile wheel
// into its library target, and tests effective sys.path and imported files in
// unittest, ordinary conformance and larger-bound processes. The wheel's root
// package and driver-local module are independently capable of replacing the
// application. Neither may precede application code; standard-library and
// gate modules must precede both application directories and dependencies.
//
// INVARIANT_PYTHON_DEPS_REAL=1 go test ./internal/verify -run '^TestPythonDependenc' -count=1 -timeout 30m
func TestPythonDependenciesPreferApplicationImports(t *testing.T) {
	root := pyDepsImportOrderFixture(t)
	before := pyDepsFiles(t, root)
	var recipe string
	t.Run("generated recipe and launcher boundaries", func(t *testing.T) {
		pyDepsFakeDocker(t)
		tc := toolchain.Toolchain{PythonImage: toolchain.PythonImage, JavaImage: "accept-java", TLCJar: filepath.Join(t.TempDir(), "tlc.jar")}
		r, err := Run(context.Background(), root, "", tc)
		pyDepsRequireGateChecks(t, r, err)
		selected := pyDepsBuild(t, root)
		staged := pyDepsFiles(t, filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "context"))
		recipe = staged["Dockerfile"]
		target := pyDepsRecipeLibraryTarget(t, recipe)
		pyDepsAssertIsolatedRecipe(t, recipe)
		pyDepsAssertCanonicalStaging(t, root, selected, []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"})
		var ordinary, larger bool
		for _, call := range pyDepsCalls(t) {
			if call.Kind != "application" {
				continue
			}
			ordinary, larger = ordinary || !call.Count, larger || call.Count
			if call.Image != selected.Image || call.Network != "none" || call.Environment["DRIVER"] != "drivers/conformance.py" ||
				call.Mounts["/src"] == "" || call.Mounts["/runtime"] == "" || !call.ReadOnlyMounts["/runtime"] {
				t.Fatalf("nested driver lost its application or trusted runtime boundary: %#v", call)
			}
			command := append([]string(nil), call.Command...)
			if call.Entrypoint != "" {
				command = append([]string{call.Entrypoint}, command...)
			}
			if len(command) != 3 || !pyDepsTrustedExecutable(command[0]) || path.Base(command[0]) != "sh" || command[1] != "-c" {
				t.Fatalf("check launcher is not the trusted shell: %#v", command)
			}
			pyDepsRequireTrustedHelpers(t, command[2])
			pyDepsRequireIsolatedApplicationPython(t, command[2])
			for _, key := range []string{"PATH", "PYTHONPATH", "PYTHONHOME"} {
				for _, dir := range strings.Split(call.Environment[key], ":") {
					if dir == target || strings.HasPrefix(dir, target+"/") {
						t.Fatalf("dependency path enters startup or command lookup through %s: %#v", key, call)
					}
				}
			}
		}
		if !ordinary || !larger {
			t.Fatalf("missing application import boundary: ordinary=%t larger=%t", ordinary, larger)
		}
	})
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
		t.Run("real wheel cannot shadow application or trusted modules", func(t *testing.T) {
			pyDepsRealImportOrder(t, root, recipe)
		})
	}
	if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("import-order checks rewrote the application or its dependency files")
	}
}

func pyDepsImportOrderFixture(t *testing.T) string {
	t.Helper()
	root := pyDepsFixture(t)
	pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("# import-order regression %d\n-r deps/application.txt\n", time.Now().UnixNano()))
	pyDepsWrite(t, root, "application/__init__.py", "ORIGIN = 'application'\ndef value(n):\n    return n\n")
	pyDepsWrite(t, root, "drivers/driver_support.py", "ORIGIN = 'driver-directory'\ndef value(n):\n    return n\n")
	// The probe runs inside the production processes. Module origins and the
	// ordered paths are checked separately, so pre-importing one trusted
	// module cannot conceal a later path-precedence regression.
	pyDepsWrite(t, root, "import_order_probe.py", `import fractions, invariant_explore, nagini_contracts, os, packaging, sys
def assert_import_order(driver=False):
    paths = [os.path.realpath(p) for p in sys.path if p]
    library = os.path.dirname(os.path.dirname(os.path.realpath(packaging.__file__)))
    stdlib = os.path.dirname(os.path.realpath(fractions.__file__))
    assert stdlib.startswith('/usr/local/lib/python'), stdlib
    assert os.path.realpath(invariant_explore.__file__).startswith('/runtime/'), invariant_explore.__file__
    assert os.path.realpath(nagini_contracts.__file__).startswith('/runtime/'), nagini_contracts.__file__
    assert library not in ('/src', '/src/drivers', '/runtime', stdlib), library
    for application in ['/src'] + (['/src/drivers'] if driver else []):
        assert paths.index(stdlib) < paths.index(application) < paths.index(library), paths
        assert paths.index('/runtime') < paths.index(application), paths
`)
	pyDepsWrite(t, root, "test_dependency.py", `import application, unittest
from import_order_probe import assert_import_order
class ApplicationImportTest(unittest.TestCase):
    def test_application_and_library(self):
        assert_import_order()
        self.assertEqual(application.ORIGIN, 'application')
        self.assertTrue(application.__file__.startswith('/src/application/'), application.__file__)
        self.assertEqual(application.value(7), 7)
`)
	pyDepsWrite(t, root, "drivers/conformance.py", `import application, driver_support, json, os
from import_order_probe import assert_import_order
Capacity = 1
assert_import_order(driver=True)
assert application.ORIGIN == 'application', application.__file__
assert driver_support.ORIGIN == 'driver-directory', driver_support.__file__
assert application.__file__.startswith('/src/application/'), application.__file__
assert driver_support.__file__.startswith('/src/drivers/'), driver_support.__file__
assert application.value(7) == driver_support.value(7) == 7
if 'INVARIANT_COUNT' in os.environ:
    with open(os.environ['INVARIANT_COUNT'], 'w') as f:
        json.dump({'states': Capacity + 1, 'depth': Capacity + 1}, f)
else:
    with open(os.environ['INVARIANT_TRACES'], 'w') as f:
        json.dump({'traces': [[{'n': 0}, {'n': 1}]]}, f)
`)
	if err := os.Remove(filepath.Join(root, "conformance.py")); err != nil {
		t.Fatal(err)
	}
	pyDepsWrite(t, root, ".invariant/invariant.json", `{"name":"python dependencies","module":".invariant/specs/Counter.tla","language":"python","code":"core","conformance":"drivers/conformance.py","exhaustive":true}`)
	for _, dir := range []string{"", "drivers/"} {
		for _, module := range []string{"fractions", "invariant_explore", "nagini_contracts"} {
			pyDepsWrite(t, root, dir+module+".py", "raise AssertionError('INVARIANT_APPLICATION_SHADOWED_TRUSTED_"+module+"')\n")
		}
	}
	return root
}

func pyDepsRealImportOrder(t *testing.T, root, recipe string) {
	t.Helper()
	pyDepsRequireRealDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	docker := func(args ...string) []byte {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("Docker import-order fixture %v: %v\n%s", args, err, out)
		}
		return bytes.TrimSpace(out)
	}
	// Build the unmodified production recipe first. The hostile local wheel
	// is only a cached-image fixture; local wheel requirements remain rejected.
	first := pyDepsBuild(t, root)
	pyDepsCleanupRecipeImage(t, first.Image)
	pyDepsAssertEnvironment(t, root, first, "24.2")
	reportPath, target := pyDepsRecipeReportPath(t, recipe), pyDepsRecipeLibraryTarget(t, recipe)
	var tags []string
	if err := json.Unmarshal(docker("image", "inspect", "--format", "{{json .RepoTags}}", first.Image), &tags); err != nil || len(tags) == 0 {
		t.Fatalf("generated image has no cache reference: tags=%v error=%v", tags, err)
	}
	wheel, wheelHash := pyDepsImportOrderWheel(t)
	container := fmt.Sprintf("invariant-import-order-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		exec.CommandContext(cleanupCtx, "docker", "rm", "--force", container).Run()
	})
	const install = `import json, subprocess, sys
report_path, target, wheel = sys.argv[1:]
with open(report_path) as f:
    report = json.load(f)
subprocess.run([sys.executable, '-I', '-m', 'pip', '--isolated', 'install', '--no-index', '--no-deps', '--only-binary=:all:', '--target', target, '--upgrade', '--report', '/tmp/shadow-report.json', wheel], check=True)
with open('/tmp/shadow-report.json') as f:
    fixture = json.load(f)
assert len(fixture['install']) == 1
report['install'].extend(fixture['install'])
with open(report_path, 'w') as f:
    json.dump(report, f)
`
	docker("run", "--name", container, "--network", "none", "--platform", "linux/amd64", "-w", "/tmp",
		"-v", filepath.Dir(wheel)+":/fixture:ro", first.Image, "/usr/local/bin/python", "-I", "-c", install, reportPath, target, "/fixture/"+filepath.Base(wheel))
	derived := string(docker("commit", container))
	if !pyDepsDigest.MatchString(derived) {
		t.Fatalf("fixture did not yield an immutable image: %q", derived)
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
	// Both exported names really replace application modules when dependency
	// paths win. This control uses the same wheel and exact installed target.
	const unsafe = `import sys
sys.path.extend([sys.argv[1], '/src', '/src/drivers'])
import application, driver_support
assert application.ORIGIN == driver_support.ORIGIN == 'dependency'
assert application.__file__.startswith(sys.argv[1] + '/')
assert driver_support.__file__.startswith(sys.argv[1] + '/')
assert application.value(7) == driver_support.value(7) == 7
print('INVARIANT_APPLICATION_SHADOW_CONTROL')
`
	out := docker("run", "--rm", "--network", "none", "--platform", "linux/amd64", "--read-only", "-w", "/tmp",
		"-v", root+":/src:ro", derived, "/usr/local/bin/python", "-I", "-c", unsafe, target)
	if string(out) != "INVARIANT_APPLICATION_SHADOW_CONTROL" {
		t.Fatalf("wheel did not replace both application import locations: %s", out)
	}
	selected := pyDepsBuild(t, root)
	if selected.Image != derived || selected.Recipe != first.Recipe || !reflect.DeepEqual(selected.Inputs, first.Inputs) ||
		!reflect.DeepEqual(selected.CanonicalInputs, first.CanonicalInputs) || len(selected.Artifacts) != 2 {
		t.Fatalf("gate did not select the complete adversarial environment: %#v", selected)
	}
	packaging, shadow := false, false
	for _, artifact := range selected.Artifacts {
		switch artifact.Name {
		case "packaging":
			packaging = reflect.DeepEqual(artifact, first.Artifacts[0])
		case "invariant-application-shadow":
			shadow = artifact.Version == "1.0" && artifact.Filename == filepath.Base(wheel) && artifact.SHA256 == wheelHash
		}
	}
	if !packaging || !shadow {
		t.Fatalf("receipt does not identify the actual installed wheels: %#v", selected.Artifacts)
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
		t.Fatalf("receipt changed under import shadowing: %s != %s", fields["python_dependencies"], want)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	original := pyDepsFiles(t, root)
	for _, file := range []string{"application/__init__.py", "drivers/driver_support.py"} {
		t.Run("application failure in "+file, func(t *testing.T) {
			// The dependency's identically named module remains healthy; a
			// shadowed import would hide this application failure.
			marker := "INVARIANT_APPLICATION_FAILURE_" + strings.ReplaceAll(file, "/", "_")
			pyDepsWrite(t, root, file, original[file]+"\ndef value(n):\n    raise RuntimeError('"+marker+"')\n")
			t.Cleanup(func() { pyDepsWrite(t, root, file, original[file]) })
			build, evidence, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
			if err != nil || len(evidence.Traces) != 0 || !strings.Contains(evidence.Message, marker) {
				t.Fatalf("dependency concealed ordinary application failure: build=%#v evidence=%#v error=%v", build, evidence, err)
			}
			if file == "application/__init__.py" && (build.Passed || !strings.Contains(build.Output, marker)) {
				t.Fatalf("dependency concealed unittest application failure: %#v", build)
			}
			larger, err := countLarger(ctx, selected.Image, "python", p, map[string]int{"Capacity": 2})
			if err != nil || larger.OK || !strings.Contains(larger.Message, marker) {
				t.Fatalf("dependency concealed larger-bound application failure: %#v error=%v", larger, err)
			}
		})
	}
	if again := pyDepsBuild(t, root); !reflect.DeepEqual(again, selected) {
		t.Fatalf("import-order checks changed dependency evidence: %#v != %#v", again, selected)
	}
}

func pyDepsImportOrderWheel(t *testing.T) (string, string) {
	t.Helper()
	const dist = "invariant_application_shadow-1.0.dist-info/"
	files := map[string]string{
		"application/__init__.py": "ORIGIN = 'dependency'\ndef value(n):\n    return n\n",
		"driver_support.py":       "ORIGIN = 'dependency'\ndef value(n):\n    return n\n",
		dist + "METADATA":         "Metadata-Version: 2.1\nName: invariant-application-shadow\nVersion: 1.0\n",
		dist + "WHEEL":            "Wheel-Version: 1.0\nGenerator: invariant-acceptance\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
	}
	for _, module := range []string{"fractions", "invariant_explore", "nagini_contracts"} {
		files[module+".py"] = "raise AssertionError('INVARIANT_DEPENDENCY_SHADOWED_TRUSTED_" + module + "')\n"
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
	filename := filepath.Join(t.TempDir(), "invariant_application_shadow-1.0-py3-none-any.whl")
	if err := os.WriteFile(filename, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return filename, pyDepsHash(buf.Bytes())
}
