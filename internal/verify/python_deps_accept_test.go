package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
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

// Intended API: PythonDepsRecipe(root) returns the recipe digest without
// running Docker; PythonDepsImage(ctx, root) returns *PythonDependencies.
// Missing requirements.txt returns an empty recipe / nil environment.
// PythonDependencies has Image (an immutable Docker ID, execution only,
// json:"-"), Base, Recipe,
// Platform, Inputs {Path,SHA256}, CanonicalInputs {Path,SHA256}, and
// Artifacts {Name,Version,Filename,SHA256}.
// JSON uses lower_snake_case, including toolchain.python_dependencies.
// Lists are canonical (inputs by path; artifacts by name/version/filename).
// Hashes are sha256:<64 lowercase hex>. Base is toolchain.PythonImage and
// platform is linux/amd64; both and the full installation recipe enter the
// recipe digest, together with length-delimited relative paths and bytes of
// every reachable input and the generated canonical files. CanonicalInputs
// names constraints.txt and requirements.txt relative to the canonical/
// build-context directory, in path order. Local tags and timestamps are not
// evidence.
//
// Supported requirements: UTF-8 pip requirement files with an optional leading
// UTF-8 BOM, LF or CRLF line endings, comments, blank lines,
// single trailing backslash continuations joined without inserted characters,
// exactly as pip joins them, a strict safe subset of PEP 508
// names/extras/versions/markers,
// --hash=sha256:..., and local -r/--requirement/-c/--constraint references.
// Only ASCII spaces separate a requirement from its hash options; other
// whitespace at that boundary is rejected rather than normalized into spaces.
// References resolve from their containing file, stay inside root and may
// share inputs; cycles and symlinks are errors. A strict parser validates
// every requirement and marker; unsupported syntax is rejected rather than
// passed through to pip. Runs of backslashes and every other backslash
// position are rejected, including in comments or markers. Pip checks
// resolution only after this validation; marker literals containing '#' or
// option-like tokens (including --hash) are outside the supported subset.
// The installer also applies
// its full hash-checking rules. Wheel-only installation from
// public PyPI rejects sdist-only packages rather than running build backends.
// No environment interpolation, URLs/VCS/local/editable packages, index or
// other pip option overrides are allowed. No host pip configuration is used.
// Reject invalid UTF-8, bare CR, encoding declarations in comments anywhere
// in a file (never a package name such as geocoding==1.0), and VT, FF, FS,
// GS, RS, NEL, LS and PS before interpreting comments,
// continuations or references. Installation never reads application files:
// serialize accepted requirements to canonical/requirements.txt and accepted
// constraints to canonical/constraints.txt, one requirement with its hashes
// per LF-terminated line, with no comments, includes, continuations or other
// options. Copy only that directory literally into the build. Validate every
// directory traversed by an input reference on the host, including those
// canceled by '..', but do not stage the original inputs or directories.
// All Python invoked during the build uses -I from a clean cwd outside the
// canonical files. These restrictions do not change application imports
// during the offline tests and conformance runs.
//
// Persist pip's installation report in the image and read it back (offline)
// on both build and reuse; never manufacture artifact evidence from inputs.
// Missing or invalid reports are errors, or may trigger a rebuild that must
// yield valid evidence before use. All installed application distributions,
// including transitives, need wheel filename, version and artifact SHA-256.
// A valid empty resolution (e.g. all markers false) differs from no report.
// Resolve an immutable image ID once, then use it for report reads and every
// application run, including larger bounds. A recipe tag is only a cache key.
//
// Default tests use a Docker CLI double. With INVARIANT_PYTHON_DEPS_REAL=1,
// The image reuse and recipe regression tests also execute the actual build
// with real Docker and PyPI, without skipping failures. The parser comparison
// reads every accepted fixture with the pinned image's own pip, offline, and
// compares its requirements, constraints and hashes with the canonical files.
// It also requires pip and the validator to refuse the shared rejected
// option-boundary whitespace corpus, including tabs before --hash.
// The merger runs this
// on the final pull request head and posts its passing output before merge:
// INVARIANT_PYTHON_DEPS_REAL=1 go test ./internal/verify -run '^TestPythonDependenc' -count=1 -timeout 30m

var pyDepsDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func pyDepsHash(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func pyDepsWrite(t *testing.T, root, path, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pyDepsFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The same referenced-requirement/constraint fixture is used with the CLI
// double and in the real integration run. packaging is a small real wheel.
func pyDepsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{
		"requirements.txt":     "# application dependencies\n-r deps/application.txt\n",
		"deps/application.txt": "packaging>=24,<25\n-c constraints.txt\n",
		"deps/constraints.txt": "packaging==24.2\n",
		"test_dependency.py": `import os, socket, unittest
from packaging.version import Version
class DependencyTest(unittest.TestCase):
    def test_package_and_isolation(self):
        self.assertLess(Version("1.9"), Version("1.10"))
        self.assertNotIn("INVARIANT_ACCEPT_CREDENTIAL", os.environ)
        self.assertNotIn("PIP_INDEX_URL", os.environ)
        self.assertFalse(os.path.exists("/root/.config/pip/pip.conf"))
        with self.assertRaises(OSError):
            socket.create_connection(("198.51.100.1", 443), timeout=0.2)
`,
		"conformance.py": `import json, os
from packaging.version import Version
Capacity = 1
assert Version("1.9") < Version("1.10")
assert "INVARIANT_ACCEPT_CREDENTIAL" not in os.environ
if "INVARIANT_COUNT" in os.environ:
    with open(os.environ["INVARIANT_COUNT"], "w") as f:
        json.dump({"states": Capacity + 1, "depth": Capacity + 1}, f)
else:
    with open(os.environ["INVARIANT_TRACES"], "w") as f:
        json.dump({"traces": [[{"n": 0}, {"n": 1}]]}, f)
`,
		"core/core.py":                 "def identity(n: int) -> int:\n    return n\n",
		".invariant/invariant.json":    `{"name":"python dependencies","module":".invariant/specs/Counter.tla","language":"python","code":"core","conformance":"conformance.py","exhaustive":true}`,
		".invariant/ratified.lock":     `{"decision":"acceptance fixture","bounds":{"Capacity":"1"},"statements":[{"name":"Spec","kind":"spec","says":"Count within the bound."},{"name":"Safe","kind":"invariant","says":"The count stays within the bound."}]}`,
		".invariant/specs/Counter.tla": "---- MODULE Counter ----\nEXTENDS Naturals\nCONSTANT Capacity\nVARIABLE n\nvars == <<n>>\nInit == n = 0\nNext == IF n < Capacity THEN n' = n + 1 ELSE UNCHANGED n\nSpec == Init /\\ [][Next]_vars\nSafe == n \\in 0..Capacity\n====\n",
	} {
		pyDepsWrite(t, root, path, body)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPythonDependencyRecipeCoversReferencedInputs(t *testing.T) {
	root := pyDepsFixture(t)
	before := pyDepsFiles(t, root)
	recipe, err := toolchain.PythonDepsRecipe(root)
	if err != nil || !pyDepsDigest.MatchString(recipe) {
		t.Fatalf("recipe=%q error=%v", recipe, err)
	}
	pyDepsAssertAcceptedWithPip(t, root)
	copy := t.TempDir()
	for name, body := range before {
		pyDepsWrite(t, copy, name, body)
	}
	pyDepsWrite(t, copy, "test_dependency.py", "# unrelated source edit\n")
	pyDepsWrite(t, copy, "unused-requirements.txt", "idna==3.10\n")
	if got, err := toolchain.PythonDepsRecipe(copy); err != nil || got != recipe {
		t.Fatalf("relocation/unreferenced files changed recipe: %q %v", got, err)
	}
	pyDepsAssertAcceptedWithPip(t, copy)
	for _, path := range []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"} {
		pyDepsWrite(t, copy, path, before[path]+"# effective input changed\n")
		if got, err := toolchain.PythonDepsRecipe(copy); err != nil || got == recipe {
			t.Errorf("changed reachable input %s did not change recipe: %q %v", path, got, err)
		}
		pyDepsAssertAcceptedWithPip(t, copy)
		pyDepsWrite(t, copy, path, before[path])
	}
	// Include spelling, nesting, whitespace, markers, extras and pip hashes
	// are accepted inputs, not content a Go parser silently drops.
	pyDepsWrite(t, copy, "requirements.txt", "--requirement=deps/application.txt\n--constraint deps/constraints.txt\n")
	pyDepsWrite(t, copy, "deps/application.txt", "# nested input\n-rshared.txt\n-cconstraints.txt\n")
	pyDepsWrite(t, copy, "deps/shared.txt", "requests[socks]==2.32.3 ; python_version >= '3.13' \\\n    --hash=sha256:"+strings.Repeat("a", 64)+"\n")
	if _, err := toolchain.PythonDepsRecipe(copy); err != nil {
		t.Fatalf("documented requirement forms rejected: %v", err)
	}
	pyDepsAssertAcceptedWithPip(t, copy)
	if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("reading dependency inputs rewrote the application")
	}
	if got, err := toolchain.PythonDepsRecipe(t.TempDir()); err != nil || got != "" {
		t.Fatalf("no requirements should need no dependency recipe: %q %v", got, err)
	}
}

func TestPythonDependenciesRejectUnaccountedInputs(t *testing.T) {
	pyDepsFakeDocker(t)
	for name, body := range map[string]string{
		"outside reference":  "-r ../outside.txt\n",
		"absolute reference": "-c /etc/passwd\n",
		"missing reference":  "-r missing.txt\n",
		"cycle":              "-r requirements.txt\n",
		"url":                "packaging @ https://example.test/pkg.whl\n",
		"remote include":     "--requirement=https://example.test/requirements.txt\n",
		"vcs":                "git+https://example.test/pkg.git\n",
		"editable":           "-e ./package\n",
		"local archive":      "./package.whl\n",
		"local directory":    "../package\n",
		"environment":        "packaging==${PACKAGE_VERSION}\n",
		"index override":     "--index-url https://example.test/simple\npackaging\n",
		"extra index":        "--extra-index-url https://example.test/simple\n",
		"find links":         "--find-links wheels\n",
		"source override":    "--no-binary :all:\npackaging\n",
		"option":             "--trusted-host example.test\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			pyDepsWrite(t, root, "requirements.txt", body)
			if _, err := toolchain.PythonDepsRecipe(root); err == nil {
				t.Fatal("accepted input outside the declared requirements policy")
			}
			if env, err := toolchain.PythonDepsImage(context.Background(), root); err == nil || env != nil {
				t.Fatalf("invalid inputs reached image selection: %#v %v", env, err)
			}
		})
	}
	root := t.TempDir()
	pyDepsWrite(t, root, "requirements.txt", "-r linked.txt\n")
	pyDepsWrite(t, root, "actual.txt", "packaging==24.2\n")
	if err := os.Symlink("actual.txt", filepath.Join(root, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := toolchain.PythonDepsRecipe(root); err == nil {
		t.Fatal("symlinked dependency input accepted")
	}
	if calls := pyDepsCalls(t); len(calls) != 0 {
		t.Fatalf("invalid inputs invoked Docker: %v", calls)
	}
}

// Python str.splitlines recognizes these in addition to CR and LF.
func TestPythonDependenciesRejectHiddenPipOptions(t *testing.T) {
	pyDepsFakeDocker(t)
	for _, separator := range []rune{'\v', '\f', '\u001c', '\u001d', '\u001e', '\u0085', '\u2028', '\u2029'} {
		for _, option := range []string{"--index-url https://example.invalid/simple", "--no-binary :all:"} {
			for _, path := range []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"} {
				t.Run(fmt.Sprintf("U+%04X/%s/%s", separator, strings.Fields(option)[0], path), func(t *testing.T) {
					root := pyDepsFixture(t)
					pyDepsWrite(t, root, path, "# seemingly harmless comment"+string(separator)+option+"\npackaging==24.2\n")
					if recipe, err := toolchain.PythonDepsRecipe(root); err == nil {
						t.Fatalf("hidden pip option accepted in %s: recipe=%s", path, recipe)
					}
					if env, err := toolchain.PythonDepsImage(context.Background(), root); err == nil || env != nil {
						t.Fatalf("hidden pip option reached image selection: env=%#v error=%v", env, err)
					}
				})
			}
		}
	}
	if calls := pyDepsCalls(t); len(calls) != 0 {
		t.Fatalf("hidden options must be rejected before Docker: %#v", calls)
	}
	// Supported LF/CRLF line endings and continuations remain supported.
	for _, newline := range []string{"\n", "\r\n"} {
		root := t.TempDir()
		body := strings.Join([]string{"# ordinary comment", "packaging==24.2 \\", " --hash=sha256:" + strings.Repeat("a", 64), ""}, newline)
		pyDepsWrite(t, root, "requirements.txt", body)
		if _, err := toolchain.PythonDepsRecipe(root); err != nil {
			t.Errorf("ordinary pip line ending %q rejected: %v", newline, err)
		}
		pyDepsAssertAcceptedWithPip(t, root)
	}
}

// Replace the mutable tag immediately after selection, retaining both images.
// Fresh builds and cache hits are covered without timing-dependent sleeps.
func TestPythonDependenciesPinImageAcrossTagReplacement(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%t", cached), func(t *testing.T) {
			pyDepsFakeDocker(t)
			root := pyDepsFixture(t)
			pyDepsWrite(t, root, "deps/constraints.txt", "# unconstrained within the requested range\n")
			if cached {
				pyDepsBuild(t, root)
			}
			if err := os.WriteFile(filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "calls.jsonl"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("INVARIANT_PY_DEPS_REPLACE_TAG", "1")
			tc := toolchain.Toolchain{PythonImage: toolchain.PythonImage, JavaImage: "accept-java", TLCJar: filepath.Join(t.TempDir(), "tlc.jar")}
			r, err := Run(context.Background(), root, "", tc)
			if err != nil || r == nil || !r.Build.Passed || r.Conformance == nil || !r.Conformance.Passed || r.Larger == nil || !r.Larger.Passed {
				t.Fatalf("gate failed across tag replacement: report=%#v error=%v", r, err)
			}
			calls := pyDepsCalls(t)
			selected := ""
			for _, c := range calls {
				if c.Kind == "selection" {
					if selected != "" {
						t.Fatal("race fixture selected more than one original image")
					}
					selected = c.Image
				}
			}
			if !pyDepsDigest.MatchString(selected) {
				t.Fatalf("race never observed a selected immutable image: %#v", calls)
			}
			var evidenceRead, ordinary, larger bool
			for _, c := range calls {
				if c.Kind != "evidence" && c.Kind != "application" {
					continue
				}
				if c.Image != selected || c.Network != "none" {
					t.Errorf("read/run used a mutable or different environment: %+v; selected %s", c, selected)
				}
				if c.Kind == "evidence" {
					evidenceRead = true
				} else if c.Count {
					larger = true
				} else {
					ordinary = true
				}
			}
			if !evidenceRead || !ordinary || !larger {
				t.Fatalf("missing paths across replacement: evidence=%t ordinary=%t larger=%t", evidenceRead, ordinary, larger)
			}
			encoded, err := json.Marshal(r.Toolchain)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Dependencies *toolchain.PythonDependencies `json:"python_dependencies"`
			}
			if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Dependencies == nil {
				t.Fatalf("receipt missing selected dependency evidence: %s; %v", encoded, err)
			}
			pyDepsAssertEnvironment(t, root, decoded.Dependencies, "24.2")
			pyDepsAssertCanonicalStaging(t, root, decoded.Dependencies, []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"})
			if decoded.Dependencies.Artifacts[0].SHA256 != "sha256:"+strings.Repeat("a", 64) {
				t.Fatalf("receipt describes the replacement wheel: %s", encoded)
			}
			refs, err := filepath.Glob(filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "*.ref"))
			if err != nil || len(refs) != 1 {
				t.Fatalf("expected one mutable recipe tag: %v %v", refs, err)
			}
			current, err := os.ReadFile(refs[0])
			if err != nil || string(current) == selected {
				t.Fatalf("concurrent builder did not replace tag: %q %v", current, err)
			}
		})
	}
}

func TestPythonDependencyImagesBuildReuseAndRunOffline(t *testing.T) {
	// This subtest restores PATH before the optional real integration run.
	t.Run("CLI contract", func(t *testing.T) {
		pyDepsFakeDocker(t)
		root := pyDepsFixture(t)
		before := pyDepsFiles(t, root)
		first := pyDepsBuild(t, root)
		second := pyDepsBuild(t, root)
		if first.Image != second.Image || !reflect.DeepEqual(first, second) || pyDepsBuildCount(t) != 1 {
			t.Fatalf("unchanged requirements did not reuse the recorded environment: %#v %#v", first, second)
		}
		pyDepsAssertEnvironment(t, root, first, "24.2")
		pyDepsAssertCanonicalStaging(t, root, first, []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"})
		pyDepsRunChecks(t, root)
		if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
			t.Fatal("image construction or execution changed application inputs")
		}
		pyDepsWrite(t, root, "deps/constraints.txt", "packaging==24.1\n")
		changed := pyDepsBuild(t, root)
		if changed.Recipe == first.Recipe || changed.Image == first.Image || pyDepsBuildCount(t) != 2 {
			t.Fatal("changing a referenced constraint reused the old image")
		}
		pyDepsAssertEnvironment(t, root, changed, "24.1")
		pyDepsAssertCanonicalStaging(t, root, changed, []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"})
		// The recipe is an input identity, not a promise that floating
		// versions always resolve alike. A cached image keeps its original
		// report; rebuilding after eviction may produce another wheel.
		pyDepsWrite(t, root, "deps/constraints.txt", "# unconstrained within the requested range\n")
		floating := pyDepsBuild(t, root)
		pyDepsAssertCanonicalStaging(t, root, floating, []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"})
		t.Setenv("INVARIANT_PY_DEPS_RESOLUTION", "24.1")
		cached := pyDepsBuild(t, root)
		if !reflect.DeepEqual(floating, cached) {
			t.Fatal("cache evidence was replaced by a fresh resolution")
		}
		cacheFile := filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), strings.TrimPrefix(pyDepsHash([]byte(floating.Image)), "sha256:")+".json")
		if err := os.Remove(cacheFile); err != nil {
			t.Fatal(err)
		}
		rebuilt := pyDepsBuild(t, root)
		if rebuilt.Recipe != floating.Recipe || rebuilt.Artifacts[0].SHA256 == floating.Artifacts[0].SHA256 {
			t.Fatal("fresh resolution of unchanged unpinned requirements did not preserve its distinct artifact evidence")
		}
	})
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
		pyDepsRealIntegration(t)
	}
}

func pyDepsBuild(t *testing.T, root string) *toolchain.PythonDependencies {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env, err := toolchain.PythonDepsImage(ctx, root)
	if err != nil || env == nil || !pyDepsDigest.MatchString(env.Image) {
		t.Fatalf("building Python dependencies: env=%#v error=%v", env, err)
	}
	return env
}

func pyDepsAssertEnvironment(t *testing.T, root string, env *toolchain.PythonDependencies, version string) {
	t.Helper()
	recipe, err := toolchain.PythonDepsRecipe(root)
	if err != nil || env.Recipe != recipe || env.Base != toolchain.PythonImage || env.Platform != "linux/amd64" {
		t.Fatalf("wrong recipe provenance: %#v; recipe error=%v", env, err)
	}
	wantInputs := map[string]string{}
	for _, path := range []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"} {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		wantInputs[path] = pyDepsHash(b)
	}
	gotInputs := map[string]string{}
	var ordered []string
	for _, input := range env.Inputs {
		gotInputs[input.Path] = input.SHA256
		ordered = append(ordered, input.Path)
	}
	if !reflect.DeepEqual(wantInputs, gotInputs) || len(env.Inputs) != 3 || !sort.StringsAreSorted(ordered) {
		t.Fatalf("receipt inputs=%#v want=%#v in canonical order", env.Inputs, wantInputs)
	}
	if len(env.CanonicalInputs) != 2 || env.CanonicalInputs[0].Path != "constraints.txt" || env.CanonicalInputs[1].Path != "requirements.txt" {
		t.Fatalf("receipt lacks the two canonical installation inputs: %#v", env.CanonicalInputs)
	}
	for _, input := range env.CanonicalInputs {
		if !pyDepsDigest.MatchString(input.SHA256) {
			t.Fatalf("canonical input lacks its content hash: %#v", input)
		}
	}
	if len(env.Artifacts) != 1 {
		t.Fatalf("expected packaging's one resolved wheel: %#v", env.Artifacts)
	}
	a := env.Artifacts[0]
	if a.Name != "packaging" || a.Version != version || a.Filename != "packaging-"+version+"-py3-none-any.whl" || !pyDepsDigest.MatchString(a.SHA256) {
		t.Fatalf("incomplete resolved wheel evidence: %#v", a)
	}
}

func pyDepsRunChecks(t *testing.T, root string) {
	t.Helper()
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b, evidence, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
	if err != nil || !b.Passed || len(evidence.Traces) == 0 {
		t.Fatalf("tests/driver could not use installed package offline: build=%#v evidence=%#v err=%v", b, evidence, err)
	}
}

func TestPythonDependencyFailuresDoNotFallBack(t *testing.T) {
	pyDepsFakeDocker(t)
	root := pyDepsFixture(t)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"install", "missing report", "malformed report", "missing artifact hash"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("INVARIANT_PY_DEPS_FAILURE", mode)
			// Each case has a fresh cache key so installation must be attempted.
			pyDepsWrite(t, root, "requirements.txt", "# "+mode+"\n-r deps/application.txt\n")
			pyDepsAssertAcceptedWithPip(t, root)
			b, evidence, err := (Python{Image: toolchain.PythonImage}).Check(context.Background(), p)
			if err == nil || b.Passed || len(evidence.Traces) > 0 {
				t.Fatalf("dependency failure silently ran Python: %#v %#v %v", b, evidence, err)
			}
			if mode == "install" && !strings.Contains(err.Error(), "No matching distribution found for impossible-invariant-dependency") {
				t.Fatalf("installation error lost pip's diagnostic: %v", err)
			}
		})
	}
	// Repeat at the same recipe after a successful build to exercise corrupt
	// cache evidence too. The fake keeps returning corruption even if the
	// implementation tries to rebuild, so it must ultimately fail closed.
	pyDepsWrite(t, root, "requirements.txt", "# cached image\n-r deps/application.txt\n")
	pyDepsBuild(t, root)
	for _, mode := range []string{"missing report", "malformed report", "missing artifact hash"} {
		t.Run("cached "+mode, func(t *testing.T) {
			t.Setenv("INVARIANT_PY_DEPS_FAILURE", mode)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			b, evidence, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
			if err == nil || b.Passed || len(evidence.Traces) != 0 {
				t.Fatalf("unverifiable cached image was used: %#v %#v %v", b, evidence, err)
			}
		})
	}
	for _, c := range pyDepsCalls(t) {
		if c.Kind == "application" {
			t.Fatalf("application ran despite dependency failure: %+v", c)
		}
	}
}

func TestPythonGateRecordsAndReusesApplicationEnvironment(t *testing.T) {
	pyDepsFakeDocker(t)
	t.Setenv("INVARIANT_ACCEPT_CREDENTIAL", "host-only-secret")
	t.Setenv("PIP_INDEX_URL", "https://host-only.invalid/private")
	root := pyDepsFixture(t)
	// Application packages must never be installed into the proof image.
	pyDepsWrite(t, root, "core/core.py", "# +nagini\ndef identity(n: int) -> int:\n    return n\n")
	tc := toolchain.Toolchain{PythonImage: toolchain.PythonImage, JavaImage: "accept-java", TLCJar: filepath.Join(t.TempDir(), "tlc.jar")}
	r, err := Run(context.Background(), root, "", tc)
	if err != nil || r == nil || !r.Build.Passed || r.Conformance == nil || !r.Conformance.Passed || r.Larger == nil || !r.Larger.Passed {
		t.Fatalf("Python gate did not use application dependencies throughout: report=%#v error=%v", r, err)
	}
	b, err := json.Marshal(r.Toolchain)
	if err != nil {
		t.Fatal(err)
	}
	var tools map[string]json.RawMessage
	if err := json.Unmarshal(b, &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools["python_dependencies"]) == 0 || string(tools["python_dependencies"]) == "null" {
		t.Fatalf("gate omitted application environment from receipt: %s", b)
	}
	selected := pyDepsBuild(t, root)
	pyDepsAssertCanonicalStaging(t, root, selected, []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"})
	want, _ := json.Marshal(selected)
	if string(tools["python_dependencies"]) != string(want) {
		t.Fatalf("receipt does not describe the selected environment: %s != %s", tools["python_dependencies"], want)
	}
	if r.Toolchain.NaginiRecipe != toolchain.NaginiRecipe() || pyDepsBuildCount(t) != 1 {
		t.Fatalf("the application changed Nagini or was rebuilt during gate: %#v", r.Toolchain)
	}
	var application, larger, prover bool
	for _, c := range pyDepsCalls(t) {
		switch c.Kind {
		case "application":
			if c.Image != selected.Image || c.Network != "none" {
				t.Errorf("wrong execution environment: %+v", c)
			}
			if c.Count {
				larger = true
			} else {
				application = true
			}
		case "nagini":
			prover = true
			if c.Image == selected.Image || c.Network != "none" {
				t.Errorf("application environment replaced Nagini: %+v", c)
			}
		}
	}
	if !application || !larger || !prover {
		t.Fatalf("missing execution paths: tests/conformance=%t larger=%t Nagini=%t", application, larger, prover)
	}
	// A project without requirements must still run in the pinned plain image.
	if err := os.Remove(filepath.Join(root, "requirements.txt")); err != nil {
		t.Fatal(err)
	}
	env, err := toolchain.PythonDepsImage(context.Background(), root)
	if err != nil || env != nil {
		t.Fatalf("no requirements should select no dependency environment: %#v %v", env, err)
	}
	pyDepsWrite(t, root, "test_dependency.py", "import unittest\nclass StandardLibraryTest(unittest.TestCase):\n    def test_add(self):\n        self.assertEqual(1 + 1, 2)\n")
	pyDepsWrite(t, root, "conformance.py", "import json, os\nwith open(os.environ['INVARIANT_TRACES'], 'w') as f:\n    json.dump({'traces': [[{'n': 0}, {'n': 1}]]}, f)\n")
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := (Python{Image: toolchain.PythonImage}).Check(context.Background(), p)
	if err != nil || !plain.Passed {
		t.Fatalf("stdlib path failed: %#v %v", plain, err)
	}
	calls := pyDepsCalls(t)
	if calls[len(calls)-1].Image != toolchain.PythonImage || pyDepsBuildCount(t) != 1 {
		t.Fatal("stdlib path built dependencies or changed its image")
	}
}

func pyDepsRealIntegration(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput(); err != nil {
		t.Fatalf("real dependency integration requires Docker: %v\n%s", err, out)
	}
	t.Setenv("INVARIANT_ACCEPT_CREDENTIAL", "host-only-secret")
	t.Setenv("PIP_INDEX_URL", "https://host-only.invalid/private")
	root := pyDepsFixture(t)
	// A unique comment gives this integration run its own recipe and forces
	// an initial build; the second call must keep the exact image, not rebuild.
	pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("# integration %d\n-r deps/application.txt\n", time.Now().UnixNano()))
	before := pyDepsFiles(t, root)
	first := pyDepsBuild(t, root)
	var images []string
	pyDepsAssertAcceptedWithPip(t, root)
	images = append(images, first.Image)
	t.Cleanup(func() {
		for _, image := range images {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
			exec.CommandContext(cleanupCtx, "docker", "image", "rm", image).Run()
			stop()
		}
	})
	pyDepsAssertEnvironment(t, root, first, "24.2")
	identity := func(image string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}} {{.Created}}", image).Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	id := identity(first.Image)
	second := pyDepsBuild(t, root)
	if !reflect.DeepEqual(first, second) || identity(second.Image) != id {
		t.Fatal("real dependency image was not reused with identical provenance")
	}
	pyDepsRunChecks(t, root)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	larger, err := countLarger(ctx, first.Image, "python", p, map[string]int{"Capacity": 2})
	if err != nil || !larger.OK || larger.States != 3 || larger.Depth != 3 {
		t.Fatalf("larger run could not import installed package: %#v %v", larger, err)
	}
	if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("real image build or execution rewrote application inputs")
	}
	pyDepsWrite(t, root, "deps/constraints.txt", "packaging==24.1\n")
	changed := pyDepsBuild(t, root)
	images = append(images, changed.Image)
	pyDepsAssertEnvironment(t, root, changed, "24.1")
	if changed.Recipe == first.Recipe || changed.Image == first.Image || changed.Artifacts[0].SHA256 == first.Artifacts[0].SHA256 {
		t.Fatal("real referenced-file change did not select a different dependency artifact")
	}
	pyDepsRunChecks(t, root)
	pyDepsWrite(t, root, "deps/constraints.txt", "packaging==0.0.0+invariant-does-not-exist\n")
	pyDepsAssertAcceptedWithPip(t, root)
	b, evidence, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
	if err == nil || b.Passed || len(evidence.Traces) != 0 {
		t.Fatalf("impossible real requirement fell back to a Python execution: %#v %#v %v", b, evidence, err)
	}
	// This executes the full gate, including TLC, and checks provenance from
	// real installation in a receipt. Nagini is not needed for this fixture.
	pyDepsWrite(t, root, "deps/constraints.txt", "packaging==24.2\n")
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(ctx, root, "", tc)
	if err != nil || r == nil || !r.Passed || r.Larger == nil || !r.Larger.Passed {
		t.Fatalf("real Python gate failed: %#v %v", r, err)
	}
	bJSON, _ := json.Marshal(r.Toolchain)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(bJSON, &fields); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(first)
	if string(fields["python_dependencies"]) != string(want) {
		t.Fatalf("real receipt lost the image recipe or resolved wheels: %s != %s", fields["python_dependencies"], want)
	}
}

// The fake implements the Docker boundary, not Python or pip. It persists a
// pip report per image, checks offline application invocation, and records
// which environment was used. Only the opt-in integration runs Python.
type pyDepsCall struct {
	Kind    string
	Image   string
	Network string
	Count   bool
	Command []string
}

func pyDepsFakeDocker(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("INVARIANT_PY_DEPS_TEST_BINARY", binary)
	t.Setenv("INVARIANT_PY_DEPS_FAKE", dir)
	script := "#!/bin/sh\nINVARIANT_PY_DEPS_HELPER=1 exec \"$INVARIANT_PY_DEPS_TEST_BINARY\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func pyDepsCalls(t *testing.T) []pyDepsCall {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "calls.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []pyDepsCall
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var call pyDepsCall
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return calls
}

func pyDepsBuildCount(t *testing.T) int {
	t.Helper()
	n := 0
	for _, call := range pyDepsCalls(t) {
		if call.Kind == "build" {
			n++
		}
	}
	return n
}

func init() {
	if os.Getenv("INVARIANT_PY_DEPS_HELPER") == "1" {
		os.Exit(pyDepsDocker(os.Args[1:]))
	}
}

func pyDepsDocker(args []string) int {
	state := os.Getenv("INVARIANT_PY_DEPS_FAKE")
	fail := func(s string) int { fmt.Fprintln(os.Stderr, s); return 71 }
	log := func(c pyDepsCall) {
		f, err := os.OpenFile(filepath.Join(state, "calls.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			panic(err)
		}
		b, _ := json.Marshal(c)
		f.Write(append(b, '\n'))
		f.Close()
	}
	reportPath := func(image string) string {
		return filepath.Join(state, strings.TrimPrefix(pyDepsHash([]byte(image)), "sha256:")+".json")
	}
	refPath := func(tag string) string {
		return filepath.Join(state, strings.TrimPrefix(pyDepsHash([]byte(tag)), "sha256:")+".ref")
	}
	resolve := func(image string) string {
		if id, err := os.ReadFile(refPath(image)); err == nil {
			return string(id)
		}
		return image
	}
	cache := func(image string) string { return reportPath(resolve(image)) }
	// Immediately before Docker reads the first report, a concurrent builder
	// publishes another resolution under the same tag. An immutable ID still
	// addresses the original report. Existence-only probes are not selections.
	replaceTag := func(id string) error {
		if os.Getenv("INVARIANT_PY_DEPS_REPLACE_TAG") != "1" {
			return nil
		}
		marker := filepath.Join(state, "selected")
		if _, err := os.Stat(marker); err == nil {
			return nil
		}
		report, err := os.ReadFile(reportPath(id))
		if err != nil {
			return err
		}
		if err := os.WriteFile(marker, []byte(id), 0o644); err != nil {
			return err
		}
		log(pyDepsCall{Kind: "selection", Image: id})
		replacement := strings.ReplaceAll(string(report), "24.2", "24.1")
		replacement = strings.ReplaceAll(replacement, strings.Repeat("a", 64), strings.Repeat("b", 64))
		other := pyDepsHash([]byte("concurrent build\n" + replacement))
		if err := os.WriteFile(reportPath(other), []byte(replacement), 0o644); err != nil {
			return err
		}
		refs, err := filepath.Glob(filepath.Join(state, "*.ref"))
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if err := os.WriteFile(ref, []byte(other), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	if len(args) == 0 {
		return fail("missing docker command")
	}
	log(pyDepsCall{Kind: "command", Image: strings.Join(args, " ")})
	switch args[0] {
	case "info", "rm":
		return 0
	case "image":
		if len(args) < 3 || args[1] != "inspect" {
			return fail("unexpected image command")
		}
		img := args[len(args)-1]
		id := pyDepsHash([]byte(img))
		if img != toolchain.PythonImage && img != "accept-java" && !strings.HasPrefix(img, "invariant-nagini:") {
			if _, err := os.Stat(cache(img)); err != nil {
				return 1
			}
			id = resolve(img)
		}
		format := ""
		for i, a := range args {
			if (a == "--format" || a == "-f") && i+1 < len(args) {
				format = args[i+1]
			} else if strings.HasPrefix(a, "--format=") {
				format = strings.TrimPrefix(a, "--format=")
			}
		}
		switch {
		case strings.Contains(format, ".Id"):
			fmt.Println(id)
		case strings.Contains(format, "json ."):
			fmt.Printf("{\"Id\":%q,\"Os\":\"linux\",\"Architecture\":\"amd64\"}\n", id)
		default:
			fmt.Printf("[{\"Id\":%q,\"Os\":\"linux\",\"Architecture\":\"amd64\"}]\n", id)
		}
		return 0
	case "build":
		tag, iidfile := "", ""
		for i, arg := range args {
			if (arg == "-t" || arg == "--tag") && i+1 < len(args) {
				tag = args[i+1]
			}
			if arg == "--iidfile" && i+1 < len(args) {
				iidfile = args[i+1]
			}
		}
		if tag == "" {
			return fail("build has no tag")
		}
		log(pyDepsCall{Kind: "build", Image: tag})
		if os.Getenv("INVARIANT_PY_DEPS_FAILURE") == "install" {
			return fail("ERROR: No matching distribution found for impossible-invariant-dependency")
		}
		contextDir := args[len(args)-1]
		dest := filepath.Join(state, "context")
		os.RemoveAll(dest)
		err := filepath.WalkDir(contextDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(contextDir, path)
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dest, rel), 0o755)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dest, rel), b, 0o644)
		})
		if err != nil {
			return fail("copying dependency build context: " + err.Error())
		}
		constraints, err := os.ReadFile(filepath.Join(dest, "canonical", "constraints.txt"))
		if err != nil {
			return fail("canonical constraints were not staged")
		}
		if _, err := os.ReadFile(filepath.Join(dest, "canonical", "requirements.txt")); err != nil {
			return fail("canonical requirements were not staged")
		}
		version, hash := "24.2", strings.Repeat("a", 64)
		if strings.Contains(string(constraints), "24.1") {
			version, hash = "24.1", strings.Repeat("b", 64)
		}
		if os.Getenv("INVARIANT_PY_DEPS_RESOLUTION") == "24.1" {
			version, hash = "24.1", strings.Repeat("b", 64)
		}
		report := fmt.Sprintf(`{"version":"1","pip_version":"25.0","install":[{"download_info":{"url":"https://files.pythonhosted.org/packages/packaging-%s-py3-none-any.whl","archive_info":{"hashes":{"sha256":"%s"}}},"is_direct":false,"is_yanked":false,"requested":true,"metadata":{"name":"packaging","version":"%s"}}],"environment":{"implementation_name":"cpython","implementation_version":"3.13.15","os_name":"posix","platform_machine":"x86_64","platform_python_implementation":"CPython","platform_system":"Linux","python_full_version":"3.13.15","python_version":"3.13","sys_platform":"linux"}}`, version, hash, version)
		id := pyDepsHash([]byte(tag + "\n" + report))
		if err := os.WriteFile(reportPath(id), []byte(report), 0o644); err != nil {
			return fail(err.Error())
		}
		if err := os.WriteFile(refPath(tag), []byte(id), 0o644); err != nil {
			return fail(err.Error())
		}
		if iidfile != "" {
			if err := os.WriteFile(iidfile, []byte(id), 0o644); err != nil {
				return fail(err.Error())
			}
		}
		fmt.Println(id)
		return 0
	case "run":
		env, volumes := map[string]string{}, map[string]string{}
		image, network := "", ""
		var command []string
		for i := 1; i < len(args); i++ {
			a := args[i]
			if a == "--rm" || a == "--read-only" {
				continue
			}
			if strings.HasPrefix(a, "--network=") {
				network = strings.TrimPrefix(a, "--network=")
				continue
			}
			switch a {
			case "--name", "--network", "--platform", "--memory", "--pids-limit", "-w", "--workdir", "--user", "--entrypoint", "--cap-drop", "--security-opt", "--tmpfs", "-e", "--env", "-v", "--volume":
				if i+1 >= len(args) {
					return fail("missing Docker option value")
				}
				i++
				v := args[i]
				switch a {
				case "--network":
					network = v
				case "-e", "--env":
					k, value, _ := strings.Cut(v, "=")
					env[k] = value
				case "-v", "--volume":
					parts := strings.Split(v, ":")
					if len(parts) >= 2 {
						volumes[parts[1]] = parts[0]
					}
				}
				continue
			}
			if strings.HasPrefix(a, "-") {
				return fail("unrecognized Docker option " + a)
			}
			image, command = a, args[i+1:]
			break
		}
		if network != "none" {
			return fail("execution or metadata inspection has network access")
		}
		if image == "accept-java" {
			count := 2
			cfg, _ := os.ReadFile(filepath.Join(volumes["/work"], "Invariant.cfg"))
			if strings.Contains(string(cfg), "CONSTANT Capacity = 2") {
				count = 3
			}
			message := func(code int, body string) {
				fmt.Printf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
			}
			message(2262, "TLC2 Version 2.19")
			message(2193, "Model checking completed. No error has been found.")
			message(2199, fmt.Sprintf("%d states generated, %d distinct states found, 0 states left on queue.", count, count))
			message(2194, fmt.Sprintf("The depth of the complete state graph search is %d.", count))
			return 0
		}
		if strings.HasPrefix(image, "invariant-nagini:") {
			log(pyDepsCall{Kind: "nagini", Image: image, Network: network})
			fmt.Println("Verification successful")
			return 0
		}
		if env["DRIVER"] != "" {
			count := env["INVARIANT_COUNT"] != ""
			log(pyDepsCall{Kind: "application", Image: image, Network: network, Count: count, Command: command})
			for _, key := range []string{"INVARIANT_ACCEPT_CREDENTIAL", "PIP_INDEX_URL"} {
				if _, present := env[key]; present {
					return fail("host variable passed to application: " + key)
				}
			}
			_, requirementsErr := os.Stat(filepath.Join(volumes["/src"], "requirements.txt"))
			if requirementsErr == nil && image == toolchain.PythonImage {
				fmt.Print("@@invariant compile=0\nModuleNotFoundError: No module named 'packaging'\n@@invariant test=1\n@@invariant conform=1\n")
				return 0
			}
			if image != toolchain.PythonImage {
				if _, err := os.Stat(cache(image)); err != nil {
					return fail("application image was never built")
				}
			}
			if count {
				os.WriteFile(filepath.Join(volumes["/out"], "count.json"), []byte(`{"states":3,"depth":3}`), 0o644)
				fmt.Println("@@invariant count=0")
			} else {
				os.WriteFile(filepath.Join(volumes["/out"], "traces.json"), []byte(`{"traces":[[{"n":0},{"n":1}]]}`), 0o644)
				fmt.Print("@@invariant compile=0\nRan 1 test\n@@invariant test=0\n@@invariant conform=0\n")
			}
			return 0
		}
		// Metadata reads may use cat or a Python wrapper; return the image's
		// pip report, never the currently available index resolution.
		if len(command) == 0 {
			return fail("missing image metadata command")
		}
		id := resolve(image)
		if err := replaceTag(id); err != nil {
			return fail(err.Error())
		}
		report, err := os.ReadFile(cache(image))
		if err != nil {
			return fail("no pip installation report")
		}
		log(pyDepsCall{Kind: "evidence", Image: image, Network: network, Command: command})
		switch os.Getenv("INVARIANT_PY_DEPS_FAILURE") {
		case "missing report":
			return fail("no pip installation report")
		case "malformed report":
			report = []byte("not a pip report")
		case "missing artifact hash":
			report = []byte(strings.ReplaceAll(string(report), `"sha256":`, `"untrusted":`))
		}
		fmt.Println(string(report))
		return 0
	default:
		return fail("unexpected docker command: " + strings.Join(args, " "))
	}
}
