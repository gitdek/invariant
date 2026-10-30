package verify

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
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

// The CLI layer checks the actual commands crossing the Docker boundary.
// The real layer first executes the unmodified generated recipe, then makes
// a separate adversarial image by installing a fixture wheel with real pip,
// offline. That wheel installs cat, sh and timeout. Its genuine pip report
// is combined with the recipe's report, retaining both artifact hashes.
// The fixture bypasses requirements admission, never replaces a recipe tag,
// and exercises production evidence reading and checks by immutable image ID.
// Production admission continues to refuse local/direct wheel requirements.
//
// Intended internal seams, also used by the normal production path:
//   - toolchain.ReadPythonDepsReport(ctx, image) ([]byte, error) reads the
//     installation report through a trusted command in the immutable image;
//     PythonDepsImage uses that reader before validating public-PyPI provenance.
//   - checkPythonImage(ctx, image, project) (Build, Evidence, error) runs the
//     production launcher after Python.Check has selected/admitted its image.
//   - gateWithPythonDependencies(ctx, root, outDir, tc, deps) (*Report, error)
//     is the production gate after image selection, with the already selected
//     *toolchain.PythonDependencies shared by checks, larger runs and receipts.
//
// These are ordinary production factoring boundaries, not flags or environment
// overrides that let application inputs bypass the public-PyPI policy.
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
	var recipe string
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
		pyDepsAssertPipParserMatches(t, root, map[string]string{
			"requirements.txt": staged["canonical/requirements.txt"],
			"constraints.txt":  staged["canonical/constraints.txt"],
		})
		recipe = staged["Dockerfile"]
		pyDepsRecipeLibraryTarget(t, recipe)
	})
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
		t.Run("installed commands cannot replace gate commands", func(t *testing.T) {
			pyDepsRealTrustedCommands(t, root, recipe, "commands")
		})
	}
}

func pyDepsTrustedExecutable(command string) bool {
	// These directories belong to the pinned base, outside the application
	// library target. Shell builtins do not consult PATH and need no executable path.
	return path.IsAbs(command) && path.Clean(command) == command && path.Base(command) != "env" &&
		(path.Dir(command) == "/bin" || path.Dir(command) == "/usr/bin" || path.Dir(command) == "/usr/local/bin")
}

func pyDepsRequireTrustedHelpers(t *testing.T, script string) {
	t.Helper()
	// Quoted bootstrap programs stay single arguments. Every executable,
	// including the application's Python and timeout's child, is trusted.
	for _, statement := range pyDepsShellCommands(t, script) {
		if strings.TrimSpace(statement) == "exec 2>&1" {
			continue
		}
		args := pyDepsRecipeArguments(t, statement)
		if len(args) == 0 {
			continue
		}
		if args[0] == "exec" {
			args = args[1:]
			if len(args) == 0 {
				t.Fatal("unexpected empty exec in check script")
			}
		}
		command := args[0]
		switch command {
		case "cd", "echo", "printf", ":", "true", "false", "test", "[":
			continue // builtins of the trusted shell
		}
		if !pyDepsTrustedExecutable(command) {
			t.Errorf("check helper %q can resolve through application PATH:\n%s", command, script)
		}
		if path.Base(command) == "timeout" {
			// Production's timeout uses --option=value, then the duration.
			for i := 1; i < len(args); i++ {
				if strings.HasPrefix(args[i], "-") {
					continue
				}
				if i+1 >= len(args) || !pyDepsTrustedExecutable(args[i+1]) {
					t.Fatalf("timeout child can resolve through application PATH: %s", statement)
				}
				break
			}
		}
	}
}

// Locate the saved pip report in the generated recipe, without imposing a
// new production path just to set up the adversarial image fixture.
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

func pyDepsRealTrustedCommands(t *testing.T, root, recipe, vector string) {
	t.Helper()
	reportPath := pyDepsRecipeReportPath(t, recipe)
	libraryPath := pyDepsRecipeLibraryTarget(t, recipe)
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
	wheel, wheelHash := pyDepsShadowWheel(t, vector)
	selected := pyDepsInstallImportWheel(t, ctx, docker, root, recipe, first, wheel, wheelHash, "invariant-gate-shadow")
	// Negative controls explicitly enable the unsafe lookup/startup path.
	// The production image need not put dependency scripts on PATH.
	pyDepsShadowNegativeControl(t, ctx, docker, selected.Image, libraryPath, reportPath, vector)
	pyDepsAssertImportOnlyImage(t, docker, selected.Image, libraryPath)
	beforeEvidence := pyDepsAssertFixtureEvidence(t, ctx, selected)
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Every application process explicitly imports the fixture library. Its
	// side effect must be absent before that import and present afterwards.
	originalSources := pyDepsFiles(t, root)
	const importProbe = "import builtins\nassert not hasattr(builtins, 'INVARIANT_IMPORTED_LIBRARY')\nimport invariant_gate_shadow\nassert builtins.INVARIANT_IMPORTED_LIBRARY == 'import-only'\n"
	for _, name := range []string{"test_dependency.py", "conformance.py"} {
		pyDepsWrite(t, root, name, importProbe+originalSources[name])
	}
	r, err := gateWithPythonDependencies(ctx, root, "", tc, selected)
	pyDepsRequireGateChecks(t, r, err)
	all, _ := json.Marshal(r)
	if strings.Contains(string(all), "INVARIANT_SHADOW_") {
		t.Fatalf("dependency code ran without an application import: %s", all)
	}
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
	b, _, err := checkPythonImage(ctx, selected.Image, p)
	if err != nil || b.Passed || !strings.Contains(b.Output, "INVARIANT_PLANTED_UNIT_FAILURE") {
		t.Fatalf("installed commands concealed a failing unittest: %#v %v", b, err)
	}
	pyDepsWrite(t, root, "test_dependency.py", original["test_dependency.py"])
	pyDepsWrite(t, root, "conformance.py", "raise RuntimeError('INVARIANT_PLANTED_DRIVER_FAILURE')\n"+original["conformance.py"])
	_, evidence, err := checkPythonImage(ctx, selected.Image, p)
	if err != nil || len(evidence.Traces) != 0 || !strings.Contains(evidence.Message, "INVARIANT_PLANTED_DRIVER_FAILURE") {
		t.Fatalf("installed commands concealed failed conformance: %#v %v", evidence, err)
	}
	larger, err := countLarger(ctx, selected.Image, "python", p, map[string]int{"Capacity": 2})
	if err != nil || larger.OK || !strings.Contains(larger.Message, "INVARIANT_PLANTED_DRIVER_FAILURE") {
		t.Fatalf("installed commands concealed a failing larger driver: %#v %v", larger, err)
	}
	pyDepsWrite(t, root, "conformance.py", original["conformance.py"])
	if after := pyDepsAssertFixtureEvidence(t, ctx, selected); !bytes.Equal(after, beforeEvidence) {
		t.Fatalf("running installed %s changed dependency receipt evidence: %s != %s", vector, after, beforeEvidence)
	}
}

// The production build above installs only public-PyPI requirements. Everything
// here is fixture setup after admission: real pip installs the local wheel into
// a new image and retains its truthful file: URL and is_direct=true report. No
// application requirement mentions the wheel, no production recipe tag moves,
// and PythonDepsImage is never asked to admit the resulting fixture environment.
// The tests then call the same production execution and raw evidence routines
// that the admitted environment normally reaches.
func pyDepsInstallImportWheel(t *testing.T, ctx context.Context, docker func(...string) []byte, root, recipe string, first *toolchain.PythonDependencies, wheel, wheelHash, distribution string) *toolchain.PythonDependencies {
	t.Helper()
	before := pyDepsFiles(t, root)
	if !pyDepsDigest.MatchString(first.Image) || len(first.Artifacts) != 1 {
		t.Fatalf("fixture base must be an immutable image with its genuine packaging artifact: %#v", first)
	}
	reportPath := pyDepsRecipeReportPath(t, recipe)
	target := pyDepsRecipeLibraryTarget(t, recipe)
	container := fmt.Sprintf("invariant-hostile-wheel-fixture-%d", time.Now().UnixNano())
	derived := ""
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		exec.CommandContext(cleanupCtx, "docker", "rm", "--force", container).Run()
		if derived != "" {
			exec.CommandContext(cleanupCtx, "docker", "image", "rm", derived).Run()
		}
	})
	// The setup interpreter comes from the pinned base. Neither it nor pip
	// starts from a dependency directory or processes installed startup hooks.
	const install = `import json, subprocess, sys
report_path, target, wheel = sys.argv[1:]
with open(report_path) as f:
    report = json.load(f)
subprocess.run([sys.executable, "-I", "-m", "pip", "--isolated", "install", "--no-index", "--no-deps", "--only-binary=:all:", "--target", target, "--upgrade", "--report", "/tmp/shadow-report.json", wheel], check=True)
with open("/tmp/shadow-report.json") as f:
    fixture = json.load(f)
assert len(fixture["install"]) == 1
assert fixture["install"][0]["is_direct"] is True
assert fixture["install"][0]["download_info"]["url"].startswith("file:")
report["install"].extend(fixture["install"])
with open(report_path, "w") as f:
    json.dump(report, f)
`
	docker("run", "--name", container, "--network", "none", "--platform", "linux/amd64", "-w", "/tmp",
		"-v", filepath.Dir(wheel)+":/fixture:ro", first.Image, "/usr/local/bin/python", "-I", "-c", install, reportPath, target, "/fixture/"+filepath.Base(wheel))
	derived = string(docker("commit", container))
	if !pyDepsDigest.MatchString(derived) || derived == first.Image {
		t.Fatalf("hostile fixture did not create a distinct immutable image: %q", derived)
	}
	selected := *first
	selected.Image = derived
	// This is fixture provenance, not a fabricated claim that the ordinary
	// public-PyPI recipe produced the additional wheel. Artifact hashes and
	// original/canonical application inputs remain independently checked.
	selected.Recipe = pyDepsHash([]byte(first.Recipe + "\x00" + install + "\x00" + target + "\x00" + reportPath + "\x00" + wheelHash))
	fixture := first.Artifacts[0]
	fixture.Name, fixture.Version, fixture.Filename, fixture.SHA256 = distribution, "1.0", filepath.Base(wheel), wheelHash
	selected.Artifacts = append(append(first.Artifacts[:0:0], first.Artifacts...), fixture)
	sort.Slice(selected.Artifacts, func(i, j int) bool { return selected.Artifacts[i].Name < selected.Artifacts[j].Name })
	// Independent trusted read is the oracle for the production reader. In
	// particular, a wheel-installed cat cannot erase either install entry.
	wantReport := docker("run", "--rm", "--network", "none", "--platform", "linux/amd64", "--read-only", "-w", "/tmp",
		derived, "/bin/cat", reportPath)
	if got := pyDepsAssertFixtureEvidence(t, ctx, &selected); !bytes.Equal(got, wantReport) {
		t.Fatalf("production evidence reader changed the genuine installed-wheel report:\ngot %s\nwant %s", got, wantReport)
	}
	if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("test-only wheel installation rewrote application dependency inputs")
	}
	t.Cleanup(func() {
		checkCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		if got := pyDepsAssertFixtureEvidence(t, checkCtx, &selected); !bytes.Equal(got, wantReport) {
			t.Fatalf("checks changed the immutable fixture's installation evidence:\ngot %s\nwant %s", got, wantReport)
		}
	})
	return &selected
}

// Test-only decoding supplies the already-selected environment to the gate.
// This deliberately does not call or replace public-PyPI report admission.
// The production reader itself must return the genuine report, including its
// local provenance, and the production receipt must retain these exact hashes.
func pyDepsAssertFixtureEvidence(t *testing.T, ctx context.Context, selected *toolchain.PythonDependencies) []byte {
	t.Helper()
	if !pyDepsDigest.MatchString(selected.Image) {
		t.Fatalf("fixture evidence must use an immutable image ID: %q", selected.Image)
	}
	raw, err := toolchain.ReadPythonDepsReport(ctx, selected.Image)
	if err != nil {
		t.Fatalf("reading fixture through production evidence reader: %v", err)
	}
	var report struct {
		Install []struct {
			IsDirect *bool `json:"is_direct"`
			Download struct {
				URL     string `json:"url"`
				Archive struct {
					Hashes map[string]string `json:"hashes"`
				} `json:"archive_info"`
			} `json:"download_info"`
			Metadata struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"metadata"`
		} `json:"install"`
	}
	if err := json.Unmarshal(raw, &report); err != nil || len(report.Install) != len(selected.Artifacts) || len(report.Install) != 2 {
		t.Fatalf("production evidence reader lost genuine wheel entries: error=%v report=%s", err, raw)
	}
	decoded := *selected
	decoded.Artifacts = nil
	local := 0
	for _, item := range report.Install {
		u, err := url.Parse(item.Download.URL)
		if err != nil || u == nil || item.IsDirect == nil {
			t.Fatalf("fixture report lost genuine wheel provenance: %#v error=%v", item, err)
		}
		if u.Scheme == "file" {
			if !*item.IsDirect || !strings.HasPrefix(u.Path, "/fixture/") {
				t.Fatalf("local fixture provenance was disguised as public PyPI: %#v", item)
			}
			local++
		} else if u.Scheme != "https" || u.Host != "files.pythonhosted.org" || *item.IsDirect {
			t.Fatalf("fixture's original PyPI provenance changed: %#v", item)
		}
		artifact := selected.Artifacts[0]
		artifact.Name, artifact.Version = item.Metadata.Name, item.Metadata.Version
		artifact.Filename, artifact.SHA256 = path.Base(u.Path), "sha256:"+item.Download.Archive.Hashes["sha256"]
		if !pyDepsDigest.MatchString(artifact.SHA256) {
			t.Fatalf("fixture report lost an installed wheel's digest: %#v", artifact)
		}
		decoded.Artifacts = append(decoded.Artifacts, artifact)
	}
	sort.Slice(decoded.Artifacts, func(i, j int) bool { return decoded.Artifacts[i].Name < decoded.Artifacts[j].Name })
	if local != 1 || !reflect.DeepEqual(decoded.Artifacts, selected.Artifacts) {
		t.Fatalf("production reader returned different installed artifacts: local=%d got=%#v want=%#v", local, decoded.Artifacts, selected.Artifacts)
	}
	return raw
}

// Construct real wheel bytes, including RECORD hashes and executable script
// modes, so pip's actual wheel installation scheme is exercised offline.
func pyDepsShadowWheel(t *testing.T, vector string) (string, string) {
	t.Helper()
	const dist = "invariant_gate_shadow-1.0.dist-info/"
	const data = "invariant_gate_shadow-1.0.data/scripts/"
	files := map[string]string{
		"invariant_gate_shadow.py": "import builtins\nbuiltins.INVARIANT_IMPORTED_LIBRARY = 'import-only'\n",
		dist + "METADATA":          "Metadata-Version: 2.1\nName: invariant-gate-shadow\nVersion: 1.0\n",
		dist + "WHEEL":             "Wheel-Version: 1.0\nGenerator: invariant-acceptance\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
	}
	// Forged success includes real evidence files, so planted failures detect
	// startup takeover even when the gate requires traces/counts as well as
	// exit status. The marker also makes each negative control observable.
	payload := fmt.Sprintf(`import json, os, sys
print(%q, flush=True)
if "INVARIANT_TRACES" in os.environ:
    with open(os.environ["INVARIANT_TRACES"], "w") as f:
        json.dump({"traces": [[{"n": 0}, {"n": 1}]]}, f)
if "INVARIANT_COUNT" in os.environ:
    with open(os.environ["INVARIANT_COUNT"], "w") as f:
        json.dump({"states": 3, "depth": 3}, f)
print("@@invariant compile=0\n@@invariant test=0\n@@invariant conform=0\n@@invariant count=0", flush=True)
os._exit(0)
`, "INVARIANT_SHADOW_"+vector)
	switch vector {
	case "commands":
		files[data+"cat"] = `#!python
import json, sys
with open(sys.argv[1]) as f:
    report = json.load(f)
report["install"] = []
print(json.dumps(report))
`
		for i, name := range []string{"sh", "timeout"} {
			files[data+name] = fmt.Sprintf("#!python\nimport sys\nprint(%q, file=sys.stderr)\nsys.exit(%d)\n", "INVARIANT_SHADOW_"+name, 86+i)
		}
	case "python":
		files[data+"python"] = "#!python\n" + payload
	case "pth":
		files["invariant_gate_shadow.pth"] = "import invariant_gate_shadow_bootstrap\n"
		files["invariant_gate_shadow_bootstrap.py"] = payload
	case "sitecustomize", "usercustomize":
		files[vector+".py"] = payload
	case "gate_modules":
		for _, name := range []string{"compileall", "unittest", "runpy"} {
			files[name+".py"] = payload
		}
	default:
		t.Fatalf("unknown hostile wheel vector %q", vector)
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
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o644)
		if strings.HasPrefix(name, data) {
			header.SetMode(0o755)
		}
		f, err := w.CreateHeader(header)
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
