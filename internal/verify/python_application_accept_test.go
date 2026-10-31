package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Issue #310's foundation API and path contract:
//
// project.ResolvePythonApplication(p) (*project.PythonApplication, error)
// returns Root (absolute) and ProjectPath (slash-separated path of p.Dir
// within Root). It applies only to language=python with nonempty existing.
// A root candidate is a strict ancestor of the verification directory at
// which every existing selection names a regular .py file or a directory
// containing Python sources. Exactly one candidate is required; neither a
// package-lock nor requirements nor Python marker/annotation is required.
// Multiple candidates are an error, even if only one has requirements.txt.
// Missing named selections must not silently shrink the set. Selections
// must be clean contained slash paths, not '.', absolute, parent-relative,
// backslash aliases or symlinks. No application-selected path may traverse
// a symlink, including the path from root to the verification directory.
//
// In this newly admitted mode, all component scope/ref/assumption source/
// invocation working_dir/input paths are application-relative. Model and
// manifest conformance paths remain verification-project-relative and
// contained; code remains a project-relative manifest field. Python IDs
// retain python:<dotted source without .py>#<lexical symbol>@<source>, using
// application-relative source paths. Existing manifests keep their bases.
// Scope is still nonrecursive; associated tests/drivers are provenance,
// not production coverage. Python source is parsed but never imported.
//
// StagePythonApplication(p, dst) (*PythonApplicationWorkspace, error)
// requires a missing or empty destination disjoint from the application,
// with no symlink path components under its existing parent. Its result has
// Root (dst), Project (*project.Project with staged Dir), Inventory
// (*project.ComponentInventory), and Files ([]project.ComponentFile).
// Stage the whole application as regular files, preserving bytes and layout,
// except .git, .venv and __pycache__ directories. Explicit selections/inputs
// in excluded directories fail. Reject symlinks and nonregular entries in
// the copied tree; do not follow or silently drop them. A failed operation
// returns no usable workspace. Callers own and remove dst. Never write into
// the original application or reuse/overwrite a nonempty destination.
// Files is sorted by path and hashes the actual staged bytes, and every
// entry also appears in Inventory.Provenance.Files for explicit inventories,
// including unselected siblings and resources. Resolve Inventory from the
// staged bytes, with no additional production scope inferred. Snapshots and
// fingerprints contain no host paths. Caller changes to source files after
// staging cannot change a prepared workspace's provenance.
//
// (*PythonApplicationWorkspace).SelectImage(ctx)
// (string, *toolchain.PythonDependencies, error) selects from the staged Root
// through toolchain.PythonDepsImage. With no root requirements it returns
// toolchain.PythonImage, nil, nil without Docker. Otherwise it returns the
// immutable deps.Image and the existing dependency evidence. Errors yield
// no usable image and never fall back. This API runs no application command;
// the existing installer alone may build dependencies. All original and
// canonical input, artifact, image, isolation and validation rules remain.
// Direct or gate-driven execution of this new mode is still refused, even
// without components; explicit configurations retain ErrComponents.
//
// Docker-free tests reuse #227's existing Docker double (not its assertions).
// Set INVARIANT_PYTHON_APPLICATION_REAL=1 to additionally install the actual
// packaging==24.2 PyPI wheel and run the copied application's unittest in
// the existing isolated Python launcher, offline. This is merger validation
// when Docker/network is unavailable, not an execution adapter implementation.

const pa310Project = "verification/counter"
const pa310Core = "from .helper import next_value\nfrom pathlib import Path\n\ndef advance(n):\n    return next_value(n) + int(Path(__file__).with_name('increment.txt').read_text())\n\ndef outer():\n    def inner():\n        return 1\n    return inner\n\nclass Counter:\n    def step(self, n):\n        return advance(n)\n"
const pa310Contract = "def contracted(n):\r\n    Requires(\r\n        n >= 0  # exact contract bytes\r\n    )\r\n    Ensures(Result() >= n)\r\n    return n\r\n"

func pa310Write(t *testing.T, root, name, body string) {
	t.Helper()
	f := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(f), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func pa310Hash(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func pa310Ref(source, symbol string) project.ComponentRef {
	return project.ComponentRef{Source: source, Symbol: symbol}
}

func pa310ID(source, symbol string) string {
	return "python:" + strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".") + "#" + symbol + "@" + source
}

func pa310Save(t *testing.T, p *project.Project) {
	t.Helper()
	for name, value := range map[string]any{".invariant/invariant.json": p.Manifest, ".invariant/ratified.lock": p.Lock} {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		pa310Write(t, p.Dir, name, string(b))
	}
}

func pa310Fixture(t *testing.T, dependencies bool) (string, *project.Project) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "application")
	p := &project.Project{Dir: filepath.Join(root, filepath.FromSlash(pa310Project)), Manifest: project.Manifest{
		Name: "ordinary Python application", Language: "python", Code: ".",
		Module: ".invariant/specs/Counter.tla", Conformance: "driver.py",
		Existing: []string{"acme/service/core.py"},
		Components: &project.ComponentsConfig{Scope: []string{"acme/service"}, Checks: []project.ComponentCheck{{
			Target:      pa310Ref("acme/service/core.py", "advance"),
			Tests:       []project.ComponentRef{pa310Ref("tests/test_service.py", "ServiceTests.test_advance")},
			Conformance: []project.ComponentRef{pa310Ref(pa310Project+"/driver.py", "drive")},
			Assumptions: []project.ComponentAssumption{{ID: "resource", Statement: "Increment is configured", Reason: "Fixture input", Source: "acme/service/increment.txt"}},
			Invocations: []project.ComponentInvocation{{ID: "unit", Kind: "test", Check: pa310Ref("tests/test_service.py", "ServiceTests.test_advance"), Command: "python", Args: []string{"-m", "unittest", "tests.test_service"}, WorkingDir: ".", Inputs: []string{"acme/service/increment.txt", pa310Project + "/.invariant/specs/Counter.tla"}},
				{ID: "model", Kind: "conformance", Check: pa310Ref(pa310Project+"/driver.py", "drive"), Command: "python", Args: []string{pa310Project + "/driver.py"}, WorkingDir: ".", Inputs: []string{pa310Project + "/.invariant/specs/Counter.tla"}}},
		}}},
	}, Lock: project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}}}}
	for name, body := range map[string]string{
		"acme/__init__.py":                             "",
		"acme/service/__init__.py":                     "",
		"acme/service/core.py":                         pa310Core,
		"acme/service/contracts.py":                    pa310Contract,
		"acme/service/helper.py":                       "def next_value(n):\n    return n + 1\n",
		"acme/service/increment.txt":                   "2\n",
		"acme/shared.py":                               "VALUE = 17\n",
		"resources/message.bin":                        "\x00\xffordinary resource\r\n",
		"tests/__init__.py":                            "",
		"tests/test_service.py":                        "import unittest\nfrom acme.service.core import advance\nclass ServiceTests(unittest.TestCase):\n    def test_advance(self):\n        self.assertEqual(advance(1), 4)\n",
		pa310Project + "/driver.py":                    "from acme.service.core import advance\ndef drive():\n    return advance(1)\n",
		pa310Project + "/.invariant/specs/Counter.tla": "---- MODULE Counter ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n",
	} {
		pa310Write(t, root, name, body)
	}
	if dependencies {
		pa310Write(t, root, "requirements.txt", "-r deps/application.txt\n")
		pa310Write(t, root, "deps/application.txt", "packaging>=24,<25\n-c constraints.txt\n")
		pa310Write(t, root, "deps/constraints.txt", "packaging==24.2\n")
		pa310Write(t, root, "tests/test_wheel.py", "import unittest\nfrom packaging.version import Version\nclass WheelTest(unittest.TestCase):\n    def test_version(self):\n        self.assertLess(Version('1.9'), Version('1.10'))\n")
	}
	pa310Save(t, p)
	return root, p
}

func pa310Load(t *testing.T, p *project.Project) *project.Project {
	t.Helper()
	got, err := project.Load(p.Dir)
	if err != nil {
		t.Fatalf("ordinary Python existing-code manifest must load: %v", err)
	}
	return got
}

func pa310Inventory(t *testing.T, p *project.Project) *project.ComponentInventory {
	t.Helper()
	inv, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func pa310JSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func pa310Fingerprint(t *testing.T, inv *project.ComponentInventory) string {
	t.Helper()
	r := &Report{Project: "ordinary Python application"}
	if err := r.AttributeComponentProofs(inv, nil); err != nil {
		t.Fatalf("staged inventory must work with evidence API: %v", err)
	}
	if r.Components == nil {
		t.Fatal("missing component inventory")
	}
	for _, row := range r.Components.Proofs {
		if row.Status != "unchecked" {
			t.Fatalf("declaration acquired proof: %+v", row)
		}
	}
	return r.Fingerprint
}

func TestPythonApplicationContextAndSelections(t *testing.T) {
	root, p := pa310Fixture(t, false)
	p = pa310Load(t, p)
	app, err := project.ResolvePythonApplication(p)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(app.Root) != filepath.Clean(root) || app.ProjectPath != pa310Project {
		t.Fatalf("wrong context: %+v", app)
	}
	if p.ModulePath() != filepath.Join(p.Dir, ".invariant/specs/Counter.tla") {
		t.Fatal("model path base changed")
	}
	inv := pa310Inventory(t, p)
	byID := map[string]project.Component{}
	for _, c := range inv.Components {
		byID[c.ID] = c
	}
	for _, symbol := range []string{"advance", "outer", "outer.<locals>.inner", "Counter.step"} {
		c, ok := byID[pa310ID("acme/service/core.py", symbol)]
		if !ok || c.Package != "acme.service.core" || c.Source != "acme/service/core.py" || c.SourceSHA256 != pa310Hash([]byte(pa310Core)) || c.ContractSHA256 != "" {
			t.Fatalf("bad ordinary component %s: %+v", symbol, c)
		}
	}
	c := byID[pa310ID("acme/service/contracts.py", "contracted")]
	contract := "Requires(\r\n        n >= 0  # exact contract bytes\r\n    )Ensures(Result() >= n)"
	if c.ContractSHA256 != pa310Hash([]byte(contract)) || c.SourceSHA256 != pa310Hash([]byte(pa310Contract)) {
		t.Fatalf("contract byte policy changed: %+v", c)
	}
	check := inv.Checks[0]
	if check.Target != pa310ID("acme/service/core.py", "advance") || !reflect.DeepEqual(check.Tests, []string{pa310ID("tests/test_service.py", "ServiceTests.test_advance")}) || !reflect.DeepEqual(check.Conformance, []string{pa310ID(pa310Project+"/driver.py", "drive")}) {
		t.Fatalf("wrong selections: %+v", check)
	}
	for _, c := range inv.Components {
		if strings.HasPrefix(c.Source, "tests/") || strings.HasPrefix(c.Source, "verification/") {
			t.Fatalf("input is production coverage: %+v", c)
		}
	}
	if check.Assumptions[0].Source.Path != "acme/service/increment.txt" || check.Assumptions[0].Source.SHA256 != pa310Hash([]byte("2\n")) {
		t.Fatalf("bad resource snapshot: %+v", check.Assumptions)
	}
	for _, v := range check.Invocations {
		if v.WorkingDir != "." || !strings.HasPrefix(v.Check, "python:") || len(v.Inputs) == 0 {
			t.Fatalf("bad invocation: %+v", v)
		}
	}
	// A failing import and side effect are syntax, never discovery commands.
	marker := filepath.Join(root, "import-executed")
	pa310Write(t, root, "acme/service/helper.py", "from pathlib import Path\nPath("+strconvQuote310(marker)+").write_text('bad')\nraise RuntimeError('not an import')\ndef next_value(n): return n+1\n")
	pa310Inventory(t, pa310Load(t, p))
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("discovery executed application code: %v", err)
	}
	// File and directory existing selections both identify this application.
	p.Manifest.Existing = []string{"acme/service"}
	pa310Save(t, p)
	pa310Load(t, p)
}

func strconvQuote310(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestPythonApplicationRejectsInvalidContexts(t *testing.T) {
	_, valid := pa310Fixture(t, false)
	pa310Load(t, valid) // The baseline must not pass by rejecting all Python.
	cases := []struct {
		name string
		edit func(*testing.T, string, *project.Project)
	}{
		{"missing existing", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Existing = append(p.Manifest.Existing, "acme/missing.py")
		}},
		{"missing target", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Components.Checks[0].Target.Symbol = "absent"
		}},
		{"missing test", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Components.Checks[0].Tests[0].Source = "tests/test_absent.py"
		}},
		{"missing resource", func(t *testing.T, r string, p *project.Project) {
			if err := os.Remove(filepath.Join(r, "acme/service/increment.txt")); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing invocation input", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Inputs = append(p.Manifest.Components.Checks[0].Invocations[0].Inputs, "missing.json")
		}},
		{"parent scope", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Components.Scope = []string{"../acme/service"}
		}},
		{"parent source", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Components.Checks[0].Tests[0].Source = "../tests/test_service.py"
		}},
		{"wrong test base", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Components.Checks[0].Conformance[0].Source = "driver.py"
		}},
		{"parent working dir", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].WorkingDir = ".."
		}},
		{"parent input", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Inputs = []string{"../outside.txt"}
		}},
		{"absolute input", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Inputs = []string{filepath.Join(r, "acme/service/increment.txt")}
		}},
		{"parent existing", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Existing = []string{"../application/acme/service/core.py"}
		}},
		{"absolute existing", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Existing = []string{filepath.Join(r, "acme/service/core.py")}
		}},
		{"unclean existing", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Existing = []string{"acme/../acme/service/core.py"}
		}},
		{"backslash existing", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Existing = []string{`acme\service\core.py`}
		}},
		{"dot existing", func(t *testing.T, r string, p *project.Project) { p.Manifest.Existing = []string{"."} }},
		{"ambiguous root", func(t *testing.T, r string, p *project.Project) {
			pa310Write(t, filepath.Dir(r), "acme/service/core.py", pa310Core)
			pa310Write(t, r, "requirements.txt", "packaging==24.2\n")
		}},
		{"linked existing source", func(t *testing.T, r string, p *project.Project) {
			f := filepath.Join(r, "acme/service/core.py")
			if err := os.Rename(f, f+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("core.py.real", f); err != nil {
				t.Fatal(err)
			}
		}},
		{"linked resource", func(t *testing.T, r string, p *project.Project) {
			f := filepath.Join(r, "acme/service/increment.txt")
			if err := os.Remove(f); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("helper.py", f); err != nil {
				t.Fatal(err)
			}
		}},
		{"linked verification", func(t *testing.T, r string, p *project.Project) {
			old := p.Dir + "-real"
			if err := os.Rename(p.Dir, old); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(old, p.Dir); err != nil {
				t.Fatal(err)
			}
		}},
		{"escaping manifest driver", func(t *testing.T, r string, p *project.Project) {
			p.Manifest.Conformance = "../../tests/test_service.py"
		}},
		{"escaping model", func(t *testing.T, r string, p *project.Project) { p.Manifest.Module = "../../outside.tla" }},
		{"missing manifest driver", func(t *testing.T, r string, p *project.Project) { p.Manifest.Conformance = "absent.py" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, p := pa310Fixture(t, false)
			tc.edit(t, r, p)
			pa310Save(t, p)
			if _, err := project.Load(p.Dir); err == nil {
				t.Fatal("invalid application loaded")
			}
			if w, err := StagePythonApplication(p, filepath.Join(t.TempDir(), "copy")); err == nil || w != nil {
				t.Fatalf("invalid application staged: workspace=%+v err=%v", w, err)
			}
		})
	}
}

func pa310Tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(name string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func pa310Stage(t *testing.T, p *project.Project) *PythonApplicationWorkspace {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "staged")
	w, err := StagePythonApplication(p, dst)
	if err != nil || w == nil {
		t.Fatalf("stage application: workspace=%+v err=%v", w, err)
	}
	if filepath.Clean(w.Root) != filepath.Clean(dst) || w.Project == nil || filepath.Clean(w.Project.Dir) != filepath.Join(dst, filepath.FromSlash(pa310Project)) {
		t.Fatalf("wrong staged paths: %+v", w)
	}
	return w
}

func TestPythonApplicationStagingAndProvenance(t *testing.T) {
	root, p := pa310Fixture(t, true)
	p = pa310Load(t, p)
	before := pa310Tree(t, root)
	w := pa310Stage(t, p)
	if got := pa310Tree(t, w.Root); !reflect.DeepEqual(got, before) {
		t.Fatalf("staging changed bytes or omitted files: got=%v want=%v", got, before)
	}
	if got := pa310Tree(t, root); !reflect.DeepEqual(got, before) {
		t.Fatal("staging wrote to original application")
	}
	expected := map[string]string{}
	for name, body := range before {
		expected[name] = pa310Hash([]byte(body))
	}
	hashes := map[string]string{}
	var paths []string
	for _, f := range w.Files {
		hashes[f.Path] = f.SHA256
		paths = append(paths, f.Path)
	}
	if len(w.Files) != len(expected) || !reflect.DeepEqual(hashes, expected) || !sort.StringsAreSorted(paths) {
		t.Fatalf("staged snapshots do not hash exact bytes canonically: %+v", w.Files)
	}
	if w.Inventory == nil || w.Inventory.Provenance == nil {
		t.Fatal("missing staged inventory provenance")
	}
	provenance := map[string]string{}
	for _, f := range w.Inventory.Provenance.Files {
		provenance[f.Path] = f.SHA256
	}
	for name, hash := range hashes {
		if provenance[name] != hash {
			t.Fatalf("staged file %s absent or stale in provenance", name)
		}
	}
	for _, c := range w.Inventory.Components {
		if c.SourceSHA256 != hashes[c.Source] {
			t.Fatalf("component hashes original instead of staged bytes: %+v", c)
		}
	}
	stable := pa310JSON(t, w.Inventory)
	fingerprint := pa310Fingerprint(t, w.Inventory)
	_, relocated := pa310Fixture(t, true)
	again := pa310Stage(t, pa310Load(t, relocated))
	if pa310JSON(t, again.Inventory) != stable || pa310Fingerprint(t, again.Inventory) != fingerprint || !reflect.DeepEqual(w.Files, again.Files) {
		t.Fatal("relocation changed identities, hashes or fingerprint")
	}
	if strings.Contains(stable, root) || strings.Contains(stable, w.Root) {
		t.Fatal("inventory leaked absolute paths")
	}
	// An original edit after staging cannot alter the copied bytes or snapshot.
	pa310Write(t, root, "acme/service/helper.py", "def next_value(n): return n + 99\n")
	if got := pa310Tree(t, w.Root); !reflect.DeepEqual(got, before) {
		t.Fatal("staged tree tracks later original edits")
	}
	if pa310JSON(t, w.Inventory) != stable || pa310Fingerprint(t, w.Inventory) != fingerprint {
		t.Fatal("staged provenance changed after original edit")
	}
	for _, name := range []string{"acme/service/core.py", "acme/service/helper.py", "acme/shared.py", "acme/service/increment.txt", "resources/message.bin", "tests/test_service.py", pa310Project + "/driver.py", "deps/constraints.txt"} {
		t.Run(name, func(t *testing.T) {
			r, q := pa310Fixture(t, true)
			body := before[name]
			if strings.HasSuffix(name, ".py") {
				body += "\n# changed source bytes\n"
			} else {
				body += "changed bytes\n"
			}
			pa310Write(t, r, name, body)
			changed := pa310Stage(t, pa310Load(t, q))
			if pa310Fingerprint(t, changed.Inventory) == fingerprint {
				t.Fatalf("%s change is absent from provenance fingerprint", name)
			}
			// Body/resource changes must not invent new component identities.
			var oldIDs, newIDs []string
			for _, c := range w.Inventory.Components {
				oldIDs = append(oldIDs, c.ID)
			}
			for _, c := range changed.Inventory.Components {
				newIDs = append(newIDs, c.ID)
			}
			if !reflect.DeepEqual(oldIDs, newIDs) {
				t.Fatal("byte edit changed lexical identities")
			}
		})
	}
	// Staging is not allowed to overwrite source or a caller's existing files.
	for _, dst := range []string{root, filepath.Join(root, "scratch"), filepath.Dir(root)} {
		if got, err := StagePythonApplication(p, dst); err == nil || got != nil {
			t.Fatalf("accepted overlapping destination %s: %v", dst, err)
		}
	}
	occupied := t.TempDir()
	pa310Write(t, occupied, "keep.txt", "keep")
	if got, err := StagePythonApplication(p, occupied); err == nil || got != nil {
		t.Fatal("accepted nonempty destination")
	}
	b, err := os.ReadFile(filepath.Join(occupied, "keep.txt"))
	if err != nil || string(b) != "keep" {
		t.Fatal("staging damaged destination")
	}
	for _, name := range []string{"acme/service/link.py", "resources/link.bin"} {
		t.Run("link "+name, func(t *testing.T) {
			r, q := pa310Fixture(t, false)
			if err := os.Symlink(filepath.Join(r, "acme/service/helper.py"), filepath.Join(r, filepath.FromSlash(name))); err != nil {
				t.Fatal(err)
			}
			if got, err := StagePythonApplication(q, filepath.Join(t.TempDir(), "copy")); err == nil || got != nil {
				t.Fatal("staging followed or dropped a link")
			}
		})
	}
	t.Run("linked destination parent", func(t *testing.T) {
		base := t.TempDir()
		real := filepath.Join(base, "real")
		if err := os.Mkdir(real, 0755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "alias")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if got, err := StagePythonApplication(p, filepath.Join(link, "copy")); err == nil || got != nil {
			t.Fatal("accepted linked destination path")
		}
	})
	t.Run("excluded directories", func(t *testing.T) {
		r, q := pa310Fixture(t, false)
		for _, dir := range []string{".git", ".venv", "__pycache__"} {
			pa310Write(t, r, dir+"/ignored", "not an input")
		}
		staged := pa310Stage(t, pa310Load(t, q))
		for _, dir := range []string{".git", ".venv", "__pycache__"} {
			if _, err := os.Stat(filepath.Join(staged.Root, dir)); !os.IsNotExist(err) {
				t.Fatalf("copied excluded %s", dir)
			}
		}
		q.Manifest.Components.Checks[0].Invocations[0].Inputs = append(q.Manifest.Components.Checks[0].Invocations[0].Inputs, ".venv/ignored")
		pa310Save(t, q)
		if got, err := StagePythonApplication(q, filepath.Join(t.TempDir(), "copy")); err == nil || got != nil {
			t.Fatal("silently omitted declared excluded input")
		}
	})
}

func pa310CheckDeps(t *testing.T, w *PythonApplicationWorkspace, image string, deps *toolchain.PythonDependencies) {
	t.Helper()
	if deps == nil || image != deps.Image || !pyDepsDigest.MatchString(image) || deps.Base != toolchain.PythonImage || deps.Platform != "linux/amd64" || !pyDepsDigest.MatchString(deps.Recipe) {
		t.Fatalf("missing immutable application environment: image=%s deps=%+v", image, deps)
	}
	want := map[string]string{}
	for _, name := range []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"} {
		b, err := os.ReadFile(filepath.Join(w.Root, name))
		if err != nil {
			t.Fatal(err)
		}
		want[name] = pa310Hash(b)
	}
	got := map[string]string{}
	var names []string
	for _, in := range deps.Inputs {
		got[in.Path] = in.SHA256
		names = append(names, in.Path)
	}
	if len(deps.Inputs) != 3 || !reflect.DeepEqual(got, want) || !sort.StringsAreSorted(names) {
		t.Fatalf("wrong application dependency inputs: %+v", deps.Inputs)
	}
	if len(deps.CanonicalInputs) != 2 || deps.CanonicalInputs[0].Path != "constraints.txt" || deps.CanonicalInputs[1].Path != "requirements.txt" {
		t.Fatalf("lost canonical dependency inputs: %+v", deps.CanonicalInputs)
	}
	if len(deps.Artifacts) != 1 || deps.Artifacts[0].Name != "packaging" || deps.Artifacts[0].Version != "24.2" || deps.Artifacts[0].Filename != "packaging-24.2-py3-none-any.whl" || !pyDepsDigest.MatchString(deps.Artifacts[0].SHA256) {
		t.Fatalf("lost real wheel evidence: %+v", deps.Artifacts)
	}
}

func TestPythonApplicationDependencyRoot(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_REAL") == "1" {
		t.Run("real wheel and copied tests", pa310RealWheel)
	}
	pyDepsFakeDocker(t)
	root, p := pa310Fixture(t, true)
	p = pa310Load(t, p)
	// A verification-local requirements file is not the application's root.
	pa310Write(t, p.Dir, "requirements.txt", "definitely-not-a-real-project-package==0\n")
	w := pa310Stage(t, p)
	// Selection is bound to the copy, not to mutable original requirements.
	pa310Write(t, root, "deps/constraints.txt", "packaging==24.1\n")
	image, deps, err := w.SelectImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pa310CheckDeps(t, w, image, deps)
	image2, deps2, err := w.SelectImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if image2 != image || !reflect.DeepEqual(deps, deps2) || pyDepsBuildCount(t) != 1 {
		t.Fatal("reuse lost immutable image or artifact evidence")
	}
	parsed := false
	for _, call := range pyDepsCalls(t) {
		if call.Kind == "parser" {
			parsed = true
			found := false
			for _, mount := range call.Mounts {
				if filepath.Clean(mount) == filepath.Clean(w.Root) {
					found = true
				}
			}
			if !found {
				t.Fatalf("parser did not read staged application root: %+v", call)
			}
		}
		if call.Kind == "application" {
			t.Fatal("image selection ran application code")
		}
	}
	if !parsed {
		t.Fatal("dependency selection bypassed authoritative requirements parser")
	}
	_, q := pa310Fixture(t, true)
	relocated := pa310Stage(t, pa310Load(t, q))
	_, deps3, err := relocated.SelectImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if deps3.Recipe != deps.Recipe || !reflect.DeepEqual(deps3.Inputs, deps.Inputs) {
		t.Fatal("dependency recipe depends on host location")
	}
	pa310Write(t, filepath.Dir(filepath.Dir(q.Dir)), "deps/constraints.txt", "# changed recursive input\npackaging==24.2\n")
	changed := pa310Stage(t, pa310Load(t, q))
	_, deps4, err := changed.SelectImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if deps4.Recipe == deps.Recipe {
		t.Fatal("recursive input edit did not change dependency recipe")
	}
}

func TestPythonApplicationDependencyFailuresAndStandardLibrary(t *testing.T) {
	pyDepsFakeDocker(t)
	_, p := pa310Fixture(t, false)
	p = pa310Load(t, p)
	pa310Write(t, p.Dir, "requirements.txt", "verification-local-file-is-not-application-dependencies==0\n")
	w := pa310Stage(t, p)
	image, deps, err := w.SelectImage(context.Background())
	if err != nil || image != toolchain.PythonImage || deps != nil {
		t.Fatalf("no-requirements path: %s %+v %v", image, deps, err)
	}
	if len(pyDepsCalls(t)) != 0 {
		t.Fatal("standard-library path invoked Docker")
	}
	for name, body := range map[string]string{
		"missing include":  "-r absent.txt\n",
		"escaping include": "-r ../outside.txt\n",
		"cycle":            "-r requirements.txt\n",
		"local dependency": "./local-package\n",
		"index override":   "--index-url https://invalid.example/simple\npackaging==24.2\n",
	} {
		t.Run(name, func(t *testing.T) {
			r, q := pa310Fixture(t, true)
			pa310Write(t, r, "requirements.txt", body)
			staged := pa310Stage(t, pa310Load(t, q))
			image, _, err := staged.SelectImage(context.Background())
			if err == nil || image != "" {
				t.Fatalf("invalid dependency returned usable image %q: %v", image, err)
			}
		})
	}
	t.Run("linked recursive input", func(t *testing.T) {
		r, q := pa310Fixture(t, true)
		f := filepath.Join(r, "deps/constraints.txt")
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("application.txt", f); err != nil {
			t.Fatal(err)
		}
		if got, err := StagePythonApplication(q, filepath.Join(t.TempDir(), "copy")); err == nil || got != nil {
			t.Fatal("linked dependency input staged")
		}
	})
	for _, failure := range []string{"missing report", "malformed report", "missing artifact hash"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("INVARIANT_PY_DEPS_FAILURE", failure)
			_, q := pa310Fixture(t, true)
			staged := pa310Stage(t, pa310Load(t, q))
			image, _, err := staged.SelectImage(context.Background())
			if err == nil || image != "" {
				t.Fatalf("missing dependency evidence returned usable image %q: %v", image, err)
			}
		})
	}
}

func pa310RealWheel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	_, p := pa310Fixture(t, true)
	w := pa310Stage(t, pa310Load(t, p))
	image, deps, err := w.SelectImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pa310CheckDeps(t, w, image, deps)
	runtime := t.TempDir()
	if err := writePythonLauncher(runtime); err != nil {
		t.Fatal(err)
	}
	cmd := toolchain.Docker(ctx, "run", "--rm", "--pull=never", "--network", "none", "--platform", "linux/amd64", "--read-only",
		"--memory", "2g", "--pids-limit", "512", "--tmpfs", "/tmp",
		"-e", "PYTHONHASHSEED=0", "-e", "PYTHONDONTWRITEBYTECODE=1", "-e", "DRIVER="+pa310Project+"/driver.py",
		"-v", w.Root+":/src:ro", "-v", runtime+":/runtime:ro", "-w", "/src", image,
		"/usr/local/bin/python", "-S", "-s", "-P", "/runtime/invariant_python.py", "unittest")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Ran 2 tests") || !strings.Contains(string(out), "OK") {
		t.Fatalf("copied package, sibling import, resource and wheel tests failed: %v\n%s", err, out)
	}
}

func TestPythonApplicationPreservesLegacyAndRefusesExecution(t *testing.T) {
	_, p := pa310Fixture(t, false)
	p = pa310Load(t, p)
	// Old Python inventories retain project-relative IDs, even within an app.
	legacy := *p
	legacy.Manifest = p.Manifest
	legacy.Manifest.Existing = nil
	legacy.Manifest.Components = &project.ComponentsConfig{Scope: []string{"core"}}
	pa310Write(t, legacy.Dir, "core/ops.py", "def advance(n): return n+1\n")
	inv := pa310Inventory(t, &legacy)
	if len(inv.Components) != 1 || inv.Components[0].ID != "python:core.ops#advance@core/ops.py" {
		t.Fatalf("legacy Python identities changed: %+v", inv.Components)
	}
	legacy.Manifest.Components = nil
	if got := pa310Inventory(t, &legacy); got.Mode != "legacy" || len(got.Components) != 0 {
		t.Fatal("legacy scope inferred")
	}
	// TypeScript still uses the nearest package-lock and stages named files.
	root := t.TempDir()
	dir := filepath.Join(root, "checks/lease")
	for name, body := range map[string]string{"package.json": "{}", "package-lock.json": "{}", "src/lease.ts": "export const lease = 1;", "src/unrelated.ts": "export const unrelated = 2;", "checks/lease/driver.ts": "import '../../src/lease';"} {
		pa310Write(t, root, name, body)
	}
	ts := project.Manifest{Language: "typescript", Code: ".", Module: "M.tla", Conformance: "driver.ts", Existing: []string{"src/lease.ts"}}
	if err := project.Validate(ts, p.Lock); err != nil {
		t.Fatal(err)
	}
	got, rel, err := PackageRoot(dir)
	if err != nil || got != root || rel != "checks/lease" {
		t.Fatalf("legacy TypeScript root changed: %s %s %v", got, rel, err)
	}
	dst := t.TempDir()
	if err := StagePackage(got, ts.Existing, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "src/lease.ts"))
	if err != nil || string(b) != "export const lease = 1;" {
		t.Fatal("legacy TypeScript staging changed")
	}
	if _, err := os.Stat(filepath.Join(dst, "src/unrelated.ts")); !os.IsNotExist(err) {
		t.Fatal("TypeScript staged unnamed code")
	}
	// Refusal must happen before verification tools or dependency installation.
	log := filepath.Join(t.TempDir(), "tool-called")
	bin := t.TempDir()
	t.Setenv("PA310_TOOL_LOG", log)
	for _, name := range []string{"docker", "go", "java", "node", "pip", "pip3", "nagini"} {
		f := filepath.Join(bin, name)
		if err := os.WriteFile(f, []byte("#!/bin/sh\nprintf called >> \"$PA310_TOOL_LOG\"\nexit 73\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := Check(p.Dir); !errors.Is(err, ErrComponents) {
		t.Fatalf("explicit preflight guard changed: %v", err)
	}
	report, err := Run(ctx, p.Dir, "", toolchain.Toolchain{})
	if report != nil || !errors.Is(err, ErrComponents) {
		t.Fatalf("explicit application execution fell through: %+v %v", report, err)
	}
	p.Manifest.Components = nil
	pa310Save(t, p)
	pa310Load(t, p)
	if err := Check(p.Dir); err == nil {
		t.Fatal("new existing-Python mode without components reached execution")
	}
	report, err = Run(ctx, p.Dir, "", toolchain.Toolchain{})
	if err == nil || report != nil {
		t.Fatal("existing Python acquired evidence through legacy gate")
	}
	build, evidence, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
	if err == nil || build.Passed || len(evidence.Traces) != 0 || evidence.Exploration != nil {
		t.Fatal("direct Python runner executed unsupported application")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("unsupported execution started tools: %v", err)
	}
	p.Manifest.Components = legacyComponents310()
	pa310Save(t, p)
	model, err := RunModel(ctx, p.Dir, toolchain.Toolchain{})
	if err != nil || model == nil || !model.ModelOnly {
		t.Fatalf("application context blocked model-only checks: %+v %v", model, err)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatalf("model-only path never reached tools: %v", err)
	}
}

func legacyComponents310() *project.ComponentsConfig {
	return &project.ComponentsConfig{Scope: []string{"acme/service"}}
}
