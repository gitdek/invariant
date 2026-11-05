package plumbing

// #503's public contract adds optional Part, Framework and Selector strings
// (JSON part/framework/selector) to Test. The framework spellings are go,
// pytest, vitest and jest. Native Name is a display label, not an execution key.
// Go selectors name one declared Test function (and an exact case when needed).
// Pytest node IDs start with the source path relative to the part directory;
// JS selectors are complete
// nested titles, including the concrete parameterized case's title.
//
// ReadDraft remains usable for structural inspection. ReadRepositoryDraft(ctx,
// workspace, snapshot) (*Plan, error) additionally validates against trusted
// captured policy and binds execution. Plan.ValidateRepository(ctx, snapshot)
// error verifies that binding without replacing it. Plan JSON stores execution
// as {"base": <captured commit>, "digest": <content identity>}; the base is
// provenance, not a requirement that an identical policy have the same commit.
// Agent-written plan.json must not supply execution. Legacy plans acquire none.
// Hashing includes execution.digest and every native selector field, preserves
// legacy canonical bytes, and excludes execution.base from execution identity.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func n503Part(language, dir string) repository.Part {
	p := part484(language, dir)
	p.Commands["test_one"] = repository.Command{Argv: []string{"native-runner", "{test}"}}
	return p
}

func n503Plan(t *testing.T, framework, part, file, selector, source string) *Plan {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"name": "native acceptance", "summary": "Pin one exact native test.",
		"files":   []string{part + "/main.go"},
		"tests":   []map[string]string{{"name": "same display label", "part": part, "framework": framework, "file": file, "selector": selector, "says": "The selected behavior holds."}},
		"sources": map[string]string{file: source},
	})
	if err != nil {
		t.Fatal(err)
	}
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func n503Draft(t *testing.T, p *Plan) string {
	t.Helper()
	ws := t.TempDir()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var draft map[string]any
	if err := json.Unmarshal(b, &draft); err != nil {
		t.Fatal(err)
	}
	delete(draft, "sources")
	delete(draft, "trusted")
	delete(draft, "execution")
	b, err = json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, ws, "plan.json", string(b))
	for f, src := range p.Sources {
		write468(t, ws, "tests/"+f, src)
	}
	return ws
}

func n503Read(t *testing.T, p *Plan, snapshot *repository.Snapshot) *Plan {
	t.Helper()
	got, err := ReadRepositoryDraft(context.Background(), n503Draft(t, p), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func n503Clone(t *testing.T, p *Plan) *Plan {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var out Plan
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func n503Execution(t *testing.T, p *Plan) map[string]string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Execution map[string]string `json:"execution"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Execution["base"] == "" || !strings.HasPrefix(out.Execution["digest"], "sha256:") {
		t.Fatalf("missing captured execution identity: %s", b)
	}
	return out.Execution
}

func n503BindFixture(t *testing.T) (string, *repository.Snapshot, *Plan) {
	t.Helper()
	part := n503Part("python", "worker")
	part.Inputs = append(part.Inputs, "shared/input.txt")
	root, _, snapshot := fixture484(t, []repository.Part{part}, nil)
	p := n503Plan(t, "pytest", "worker", "worker/test_accept.py", "test_accept.py::TestGroup::test_ready[case one]", "# exact bytes\r\nclass TestGroup:\r\n    def test_ready(self):\r\n        assert False\r\n")
	return root, snapshot, n503Read(t, p, snapshot)
}

func TestNativePlanReadsFourExactIdentities(t *testing.T) {
	parts := []repository.Part{n503Part("go", "api"), n503Part("python", "worker"), n503Part("typescript", "web"), n503Part("typescript", "admin")}
	_, _, snapshot := fixture484(t, parts, nil)
	cases := []struct{ framework, part, file, selector, source string }{
		{"go", "api", "api/ready_accept_test.go", "TestReady", "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) { t.Fatal(\"pending\") }\n"},
		{"pytest", "worker", "worker/test_ready.py", "test_ready.py::TestGroup::test_ready[a b;$(literal)]", "# UTF-8: café\r\nclass TestGroup:\r\n    def test_ready(self):\r\n        assert False"},
		{"vitest", "web", "web/ready.test.ts", "checkout handles a b; $(literal) [case 1]", "describe('checkout', () => { it('handles a b; $(literal) [case 1]', () => {}); });\n"},
		{"jest", "admin", "admin/ready.spec.ts", "checkout handles a b; $(literal) [case 1]", "describe('checkout', () => { it('handles a b; $(literal) [case 1]', () => {}); });\n"},
		{"vitest", "web", "web/other.test.ts", "checkout handles a b; $(literal) [case 1]", "// another file with the same complete title\n"},
	}
	p := &Plan{Name: "all identities", Summary: "Pin each test independently.", Files: []string{"api/main.go", "web/main.ts"}, Sources: map[string]string{}}
	for _, tc := range cases {
		one := n503Plan(t, tc.framework, tc.part, tc.file, tc.selector, tc.source)
		p.Tests = append(p.Tests, one.Tests...)
		p.Sources[tc.file] = tc.source
	}
	got := n503Read(t, p, snapshot)
	if !reflect.DeepEqual(got.Sources, p.Sources) {
		t.Fatal("source bytes changed")
	}
	if len(got.Tests) != len(cases) {
		t.Fatal("same display labels collapsed identities")
	}
	for i, test := range got.Tests {
		if test.Part != cases[i].part || test.Framework != cases[i].framework || test.Selector != cases[i].selector {
			t.Fatalf("identity changed: %+v", test)
		}
	}
	if err := got.ValidateRepository(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if n503Execution(t, got)["base"] != snapshot.Commit() {
		t.Fatal("lost base provenance")
	}
	reversed := n503Clone(t, got)
	slices.Reverse(reversed.Tests)
	slices.Reverse(reversed.Files)
	if reversed.Hash() != got.Hash() {
		t.Fatal("declaration order changed the hash")
	}
	// Declaration discovery is the later adapter's job, not a Go parser's.
	unknown := n503Plan(t, "pytest", "worker", "worker/test_dynamic.py", "test_dynamic.py::test_generated[one]", "# Python declaration discovered at runtime\n")
	_ = n503Read(t, unknown, snapshot)
}

func TestNativePlanRefusesIncompleteAndAmbiguousIdentities(t *testing.T) {
	_, snapshot, valid := n503BindFixture(t)
	for _, tc := range []struct {
		name string
		edit func(*Plan)
	}{
		{"missing part", func(p *Plan) { p.Tests[0].Part = "" }},
		{"missing framework", func(p *Plan) { p.Tests[0].Framework = "" }},
		{"missing selector", func(p *Plan) { p.Tests[0].Selector = "" }},
		{"blank selector", func(p *Plan) { p.Tests[0].Selector = " \t" }},
		{"unknown framework", func(p *Plan) { p.Tests[0].Framework = "unittest" }},
		{"wrong language", func(p *Plan) { p.Tests[0].Framework = "vitest" }},
		{"unknown owner", func(p *Plan) { p.Tests[0].Part = "elsewhere" }},
		{"file mismatch", func(p *Plan) { p.Tests[0].Selector = "other_test.py::test_ready" }},
		{"file only", func(p *Plan) { p.Tests[0].Selector = "test_accept.py" }},
		{"class only", func(p *Plan) { p.Tests[0].Selector = "test_accept.py::TestGroup" }},
		{"path traversal", func(p *Plan) { p.Tests[0].File = "worker/../test_accept.py" }},
		{"not clean", func(p *Plan) { p.Tests[0].File = "worker//test_accept.py" }},
		{"absolute", func(p *Plan) { p.Tests[0].File = "/worker/test_accept.py" }},
		{"backslash", func(p *Plan) { p.Tests[0].File = `worker\test_accept.py` }},
		{"source convention", func(p *Plan) { p.Tests[0].File = "worker/accept.txt" }},
		{"duplicate", func(p *Plan) { x := p.Tests[0]; x.Name = "another label"; p.Tests = append(p.Tests, x) }},
		{"writable source", func(p *Plan) { p.Files = append(p.Files, p.Tests[0].File) }},
		{"extra source", func(p *Plan) { p.Sources["worker/test_unlisted.py"] = "assert False\n" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := n503Clone(t, valid)
			tc.edit(p)
			if p.Validate() == nil && p.ValidateRepository(context.Background(), snapshot) == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
	// A shared-path owner still cannot execute a file outside its directory.
	_, _, owners := fixture484(t, []repository.Part{n503Part("python", "worker"), n503Part("python", "other")}, func(c *repository.Config) {
		c.SharedPaths = []repository.SharedPath{{Path: "worker/test_accept.py", Parts: []string{"other"}}}
	})
	p := n503Clone(t, valid)
	p.Tests[0].Part = "other"
	if _, err := ReadRepositoryDraft(context.Background(), n503Draft(t, p), owners); err == nil {
		t.Fatal("shared ownership escaped execution directory")
	}
	child := n503Part("python", "worker/nested")
	child.Name = "nested"
	_, _, nested := fixture484(t, []repository.Part{n503Part("python", "worker"), child}, nil)
	wrongOwner := n503Plan(t, "pytest", "worker", "worker/nested/test_accept.py", "nested/test_accept.py::test_ready", "def test_ready():\n    assert False\n")
	if _, err := ReadRepositoryDraft(context.Background(), n503Draft(t, wrongOwner), nested); err == nil {
		t.Fatal("ancestor part replaced the file's deepest owner")
	}
	part := n503Part("python", "worker")
	part.Commands["test_one"] = repository.Command{NotApplicable: "No individual test command."}
	_, _, noOne := fixture484(t, []repository.Part{part}, nil)
	if _, err := ReadRepositoryDraft(context.Background(), n503Draft(t, valid), noOne); err == nil {
		t.Fatal("inapplicable test_one accepted")
	}
	// Go selector checks apply to Selector; a display label is not a function.
	_, _, goSnapshot := fixture484(t, []repository.Part{n503Part("go", "api")}, nil)
	goPlan := n503Plan(t, "go", "api", "api/a_accept_test.go", "TestReady", "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n")
	_ = n503Read(t, goPlan, goSnapshot)
	for _, selector := range []string{"TestMissing", "Test.*", "TestReady|TestOther", "ready"} {
		p := n503Clone(t, goPlan)
		p.Tests[0].Selector = selector
		if _, err := ReadRepositoryDraft(context.Background(), n503Draft(t, p), goSnapshot); err == nil {
			t.Fatalf("non-single Go selector accepted: %q", selector)
		}
	}
	x := goPlan.Tests[0]
	x.File = "api/b_accept_test.go"
	goPlan.Tests = append(goPlan.Tests, x)
	goPlan.Sources[x.File] = goPlan.Sources[goPlan.Tests[0].File]
	if _, err := ReadRepositoryDraft(context.Background(), n503Draft(t, goPlan), goSnapshot); err == nil {
		t.Fatal("ambiguous Go function in one package accepted")
	}
}

func TestNativePlanLockPinsEveryIdentityAndExactSource(t *testing.T) {
	_, snapshot, p := n503BindFixture(t)
	lock := Lock{Plan: *p, Ratified: Ratification{Issue: 503, By: "owner", Proposal: p.Hash()}}
	root := t.TempDir()
	if err := lock.Write(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, LockPath(503)))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadLock(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan.Hash() != p.Hash() || !reflect.DeepEqual(got.Plan.Sources, p.Sources) || !reflect.DeepEqual(got.Plan.Tests, p.Tests) {
		t.Fatal("lock round trip changed contract")
	}
	if err := got.Plan.ValidateRepository(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	for f, want := range p.Sources {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil || string(data) != want {
			t.Fatalf("lock write changed source: %v %q", err, data)
		}
	}
	for _, field := range []string{"source", "selector", "part", "framework", "file", "authority"} {
		t.Run(field, func(t *testing.T) {
			changed := n503Clone(t, p)
			switch field {
			case "source":
				changed.Sources[changed.Tests[0].File] += "\n"
			case "selector":
				changed.Tests[0].Selector += "-changed"
			case "part":
				changed.Tests[0].Part = "other"
			case "framework":
				changed.Tests[0].Framework = "jest"
			case "file":
				changed.Tests[0].File = "worker/test_other.py"
			case "authority":
				encoded, _ := json.Marshal(changed)
				var object map[string]any
				if err := json.Unmarshal(encoded, &object); err != nil {
					t.Fatal(err)
				}
				object["execution"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("0", 64)
				encoded, _ = json.Marshal(object)
				if err := json.Unmarshal(encoded, changed); err != nil {
					t.Fatal(err)
				}
			}
			if changed.Hash() == p.Hash() {
				t.Fatal("tampering preserved ratified hash")
			}
			bad := lock
			bad.Plan = *changed
			encoded, _ := json.Marshal(bad)
			if _, err := ReadLock(encoded); err == nil {
				t.Fatal("tampered lock accepted")
			}
		})
	}
}

func TestNativePlanBindsContentAuthorityRatherThanMovingRevisions(t *testing.T) {
	root, snapshot, p := n503BindFixture(t)
	ctx := context.Background()
	if p.ValidateRepository(ctx, nil) == nil {
		t.Fatal("missing execution authority accepted")
	}
	originalHash := p.Hash()
	write468(t, root, "unrelated.txt", "unrelated change\n")
	pretty, err := json.MarshalIndent(snapshot.Config(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write468(t, root, repository.Path, string(pretty)+"\n")
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-qm", "unrelated")
	same, err := repository.Load(ctx, root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if same.Commit() == snapshot.Commit() {
		t.Fatal("fixture did not move base")
	}
	if err := p.ValidateRepository(ctx, same); err != nil {
		t.Fatalf("identical execution configuration refused: %v", err)
	}
	if p.Hash() != originalHash || n503Execution(t, p)["base"] != snapshot.Commit() {
		t.Fatal("verification rewrote ratified provenance")
	}
	fresh := n503Read(t, p, same)
	if n503Execution(t, fresh)["digest"] != n503Execution(t, p)["digest"] {
		t.Fatal("unrelated commit changed content identity")
	}
	if fresh.Hash() != p.Hash() {
		t.Fatal("base provenance changed the ratified content hash")
	}
	for _, change := range []string{"image", "test_one", "directory", "language", "input", "ownership"} {
		t.Run(change, func(t *testing.T) {
			git468(t, root, "reset", "--hard", same.Commit())
			config := same.Config()
			switch change {
			case "image":
				config.Parts[0].Image = "registry.example/new@sha256:" + strings.Repeat("b", 64)
			case "test_one":
				config.Parts[0].Commands["test_one"] = repository.Command{Argv: []string{"different-runner", "{test}"}}
			case "directory":
				config.Parts[0].Directory = "."
			case "language":
				config.Parts[0].Language = "typescript"
			case "input":
				write468(t, root, "shared/input.txt", "changed execution input\n")
			case "ownership":
				config.SharedPaths = []repository.SharedPath{{Path: "unrelated.txt", Parts: []string{"worker"}}}
			}
			data, _ := json.Marshal(config)
			write468(t, root, repository.Path, string(data))
			git468(t, root, "add", "--all")
			git468(t, root, "commit", "-qm", change)
			next, err := repository.Load(ctx, root, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if p.ValidateRepository(ctx, next) == nil {
				t.Fatal("different captured execution identity accepted")
			}
			if err := p.ValidateRepository(ctx, snapshot); err != nil {
				t.Fatalf("moving HEAD reinterpreted old snapshot: %v", err)
			}
		})
	}
	// A serialized digest is a claim to verify, not permission to execute.
	ws := n503Draft(t, p)
	data, _ := os.ReadFile(filepath.Join(ws, "plan.json"))
	var draft map[string]any
	if err := json.Unmarshal(data, &draft); err != nil {
		t.Fatal(err)
	}
	draft["execution"] = n503Execution(t, p)
	data, _ = json.Marshal(draft)
	write468(t, ws, "plan.json", string(data))
	if _, err := ReadRepositoryDraft(ctx, ws, snapshot); err == nil {
		t.Fatal("agent supplied its execution authority")
	}
}

func TestNativeMetadataCannotDowngradeToLegacy(t *testing.T) {
	_, snapshot, p := n503BindFixture(t)
	_ = n503Read(t, p, snapshot)
	for _, metadata := range []map[string]any{
		{"part": ""}, {"framework": ""}, {"selector": ""},
		{"part": nil}, {"framework": nil}, {"selector": nil},
		{"part": "", "framework": "", "selector": ""},
		{"part": "app", "framework": "go"},
	} {
		ws := t.TempDir()
		test := map[string]any{"name": "TestReady", "file": "app/ready_accept_test.go", "says": "It is ready."}
		for k, v := range metadata {
			test[k] = v
		}
		draft := map[string]any{"name": "legacy-looking partial native record", "summary": "Never drop native metadata.", "files": []string{"app/main.go"}, "tests": []any{test}}
		data, _ := json.Marshal(draft)
		write468(t, ws, "plan.json", string(data))
		write468(t, ws, "tests/app/ready_accept_test.go", "package app\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n")
		if _, err := ReadDraft(ws); err == nil {
			t.Fatalf("partial native record became legacy: %s", data)
		}
	}
}

func TestNativeSourcesRefuseLinksExistingFilesAndExtraEntries(t *testing.T) {
	root, snapshot, p := n503BindFixture(t)
	file := p.Tests[0].File
	for _, kind := range []string{"symbolic leaf", "hard link", "symbolic ancestor", "extra entry", "existing base"} {
		t.Run(kind, func(t *testing.T) {
			ws := n503Draft(t, p)
			leaf := filepath.Join(ws, "tests", file)
			snap := snapshot
			switch kind {
			case "symbolic leaf", "hard link":
				outside := filepath.Join(t.TempDir(), "source.py")
				write468(t, filepath.Dir(outside), "source.py", p.Sources[file])
				if err := os.Remove(leaf); err != nil {
					t.Fatal(err)
				}
				link := os.Symlink
				if kind == "hard link" {
					link = os.Link
				}
				if err := link(outside, leaf); err != nil {
					t.Fatal(err)
				}
			case "symbolic ancestor":
				dir := filepath.Dir(leaf)
				other := filepath.Join(t.TempDir(), "sources")
				if err := os.Rename(dir, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, dir); err != nil {
					t.Fatal(err)
				}
			case "extra entry":
				write468(t, ws, "tests/worker/test_extra.py", "assert False\n")
			case "existing base":
				write468(t, root, file, p.Sources[file])
				git468(t, root, "add", "--all")
				git468(t, root, "commit", "-qm", "existing acceptance source")
				var err error
				snap, err = repository.Load(context.Background(), root, "HEAD")
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadRepositoryDraft(context.Background(), ws, snap); err == nil {
				t.Fatal("unsafe or non-new source accepted")
			}
		})
	}
}

func TestNativeStagingProtectsSourcesSelectorsAndOriginalRegressionScope(t *testing.T) {
	root, _, p := n503BindFixture(t)
	lock := Lock{Plan: *p, Ratified: Ratification{Issue: 503, Proposal: p.Hash()}}
	if err := lock.Write(root); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := CopyCheckout(context.Background(), root, ws); err != nil {
		t.Fatal(err)
	}
	file := p.Tests[0].File
	write468(t, ws, file, "assert True\n")
	write468(t, ws, LockPath(503), "tampered selector and lock\n")
	write468(t, ws, "worker/main.go", "package fixture // built\n")
	write468(t, ws, "worker/review_test.go", "package fixture // new regression\n")
	write468(t, ws, "worker/test_unplanned.py", "assert True\n")
	write468(t, ws, "unrelated/new_test.go", "package unrelated\n")
	for round := 0; round < 2; round++ {
		dst := t.TempDir()
		if err := Stage(context.Background(), root, ws, p, dst); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dst, file))
		if err != nil || string(data) != p.Sources[file] {
			t.Fatal("staging accepted rewritten native source")
		}
		data, err = os.ReadFile(filepath.Join(dst, LockPath(503)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := ReadLock(data)
		if err != nil || got.Plan.Hash() != p.Hash() {
			t.Fatal("staging accepted a rewritten identity")
		}
		if _, err := os.Stat(filepath.Join(dst, "worker/review_test.go")); (err == nil) != (round == 0) {
			t.Fatal("new regression cannot be added then removed against original baseline")
		}
		for _, f := range []string{"worker/test_unplanned.py", "unrelated/new_test.go"} {
			if _, err := os.Stat(filepath.Join(dst, f)); !os.IsNotExist(err) {
				t.Fatalf("regression allowance widened: %s", f)
			}
		}
		if round == 0 {
			if err := os.Remove(filepath.Join(ws, "worker/review_test.go")); err != nil {
				t.Fatal(err)
			}
		}
	}
}
