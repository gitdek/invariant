package plumbing

// #527 adds GoRepositoryAcceptanceAdapter (a zero-value host-selected adapter)
// implementing RepositoryAcceptanceAdapter, and this optional second boundary:
//
//   Discover(context.Context, repository.Part, RepositoryAcceptanceInvocation,
//       RepositoryCheckEnvironment) (RepositoryAcceptanceDiscovery, error)
//
// RepositoryAcceptanceDiscovery contains Binding RepositoryAcceptanceBinding
// and TestName string. Binding preserves the invocation's identity and digests,
// replacing only the preliminary part-relative Package with the discovered Go
// import path. TestName is the top-level declaration, never a claim about a
// subtest or completion. Every error returns the zero discovery value and a
// *RepositoryAcceptanceDiscoveryError with Kind ("refused" or "setup") and a
// nonempty Reason; cancellation also unwraps to its context error.
//
// The environment optionally implements RepositoryAcceptanceFileEnvironment:
// RepositoryCheckEnvironment plus ReadFile(context.Context, string)
// ([]byte, error). Reads are clean part-relative regular files, with safe
// ancestors, from the same prepared environment as Run. Docker implements it;
// the real-process fixtures below use the same discovery implementation.
//
// Supported command grammar: literal "go", "test"; zero or one clean local
// package scope (implicit ".", ".", "./dir", "./...", "./dir/..."); exactly
// one -run value, separate or =, exactly {test} or ^{test}$. The scope must
// include the acceptance package; recursive scopes may be narrowed, never
// widened. Preserve declared supported flags and their argument boundaries:
// -tags, -timeout, -parallel, -p, -cpu (one positive value), -shuffle (off/on or
// an integer seed), -mod (readonly/vendor), -count (only 1), and boolean -short,
// -v, -race, -trimpath. Value flags allow separate or = spellings, booleans
// allow bare or =true/=false. No flag repeats. An absent count becomes -count=1
// to disable cached execution. Other flags, wrappers, import-path/file scopes,
// multiple scopes, opaque or unsafe forms are refused, never silently dropped.
// Recursive scopes cannot include Go's skipped vendor, testdata, dot-prefixed
// or underscore-prefixed subdirectories merely by replacing the pattern.
// Defaults and legacy fake adapters gain no automatic discovery requirement.

import (
	"context"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func goPart527(argv ...string) repository.Part {
	p := part484("go", "api")
	p.Commands["test_one"] = repository.Command{Argv: slices.Clone(argv)}
	return p
}

func goTest527(file, selector string) Test {
	return Test{Name: "a display label", Part: "api", Framework: "go", File: file, Selector: selector, Says: "One exact test is selected."}
}

func pattern527(selector string) string {
	parts := strings.Split(selector, "/")
	for i := range parts {
		parts[i] = "^" + regexp.QuoteMeta(parts[i]) + "$"
	}
	return strings.Join(parts, "/")
}

func TestGoAcceptanceCapturedSelection(t *testing.T) {
	a := GoRepositoryAcceptanceAdapter{}
	var _ RepositoryAcceptanceAdapter = a
	selector := "TestReady/a.b+[x](y){z}^$|?/$(touch_PWN);`echo_bad`/*glob*/--run=TestOther"
	for _, flag := range []string{"-run", "-run="} {
		for _, placeholder := range []string{"{test}", "^{test}$"} {
			argv := []string{"go", "test", "./..."}
			if flag == "-run" {
				argv = append(argv, flag, placeholder)
			} else {
				argv = append(argv, flag+placeholder)
			}
			argv = append(argv, "-count=1", "-tags", "accept527,extra527", "-timeout=13s", "-parallel", "2", "-p=1", "-cpu", "2", "-shuffle=23", "-short", "-v=false", "-race=false", "-trimpath", "-mod=readonly")
			part := goPart527(argv...)
			test := goTest527("api/nested/ready_accept_test.go", selector)
			got, err := a.Select(context.Background(), part, test)
			if err != nil {
				t.Fatalf("supported captured command refused: %v", err)
			}
			want := slices.Clone(argv)
			want[2] = "./nested"
			for i, arg := range want {
				if arg == placeholder {
					want[i] = pattern527(selector)
				}
				if arg == "-run="+placeholder {
					want[i] = "-run=" + pattern527(selector)
				}
			}
			if got.Package != "./nested" || !reflect.DeepEqual(got.Argv, want) {
				t.Fatalf("lost captured policy or argument boundaries: %+v; want %q", got, want)
			}
			got.Argv[0] = "changed by caller"
			if !reflect.DeepEqual(part.Commands["test_one"].Argv, argv) {
				t.Fatal("selection mutated captured policy")
			}
			pieces, names := strings.Split(pattern527(selector), "/"), strings.Split(selector, "/")
			for i, p := range pieces {
				re := regexp.MustCompile(p)
				if !re.MatchString(names[i]) || re.MatchString("prefix"+names[i]) || re.MatchString(names[i]+"suffix") {
					t.Fatalf("component is not an exact literal: %s", p)
				}
			}
		}
	}
	for _, tc := range []struct{ scope, file, want string }{
		{"", "api/ready_accept_test.go", "."}, {".", "api/ready_accept_test.go", "."},
		{"./...", "api/ready_accept_test.go", "."}, {"./nested", "api/nested/ready_accept_test.go", "./nested"},
		{"./nested/...", "api/nested/deep/ready_accept_test.go", "./nested/deep"},
	} {
		argv := []string{"go", "test", "-run={test}"}
		if tc.scope != "" {
			argv = append(argv, tc.scope)
		}
		got, err := a.Select(context.Background(), goPart527(argv...), goTest527(tc.file, "TestReady"))
		if err != nil || got.Package != tc.want || !slices.Contains(got.Argv, "-count=1") || !slices.Contains(got.Argv, "-run=^TestReady$") {
			t.Fatalf("scope %q: %+v, %v", tc.scope, got, err)
		}
	}
	// Both value spellings and boolean spellings retain their declared values.
	for _, flags := range [][]string{
		{"-tags=accept527", "-timeout", "0", "-parallel=3", "-p", "2", "-cpu=1", "-shuffle", "off", "-mod", "vendor", "-count", "1", "-short=false", "-v", "-race=true", "-trimpath=false"},
		{"-shuffle=on", "-v=true"},
	} {
		argv := append([]string{"go", "test", ".", "-run", "{test}"}, flags...)
		got, err := a.Select(context.Background(), goPart527(argv...), goTest527("api/ready_accept_test.go", "TestReady"))
		if err != nil {
			t.Fatal(err)
		}
		want := slices.Clone(argv)
		want[4] = "^TestReady$"
		if !slices.Contains(flags, "-count") {
			want = append(want, "-count=1")
		}
		if !reflect.DeepEqual(got.Argv, want) {
			t.Fatalf("execution policy changed: %q != %q", got.Argv, want)
		}
	}
}

func badCommands527() [][]string {
	bad := [][]string{
		{"sh", "-c", "go test ./... -run '{test}'"}, {"bash", "-c", "go test -run {test}"},
		{"env", "go", "test", "-run={test}"}, {"wrapper", "{test}"}, {"./go", "test", "-run={test}"},
		{"/usr/local/go/bin/go", "test", "-run={test}"}, {"go", "test -run {test}"},
		{"go", "test", "./other", "-run={test}"}, {"go", "test", ".", "-run={test}"},
		{"go", "test", "./nest/...", "-run={test}"}, {"go", "test", "./nested", "./other", "-run={test}"},
		{"go", "test", "./nested/*", "-run={test}"}, {"go", "test", "../api/nested", "-run={test}"},
		{"go", "test", "example.invalid/app/nested", "-run={test}"}, {"go", "test", "nested/ready_accept_test.go", "-run={test}"},
		{"go", "test", "./...", "-run", "prefix{test}"}, {"go", "test", "./...", "-run=^{test}"},
		{"go", "test", "./...", "-run={test}$"}, {"go", "test", "./...", "-run={test}{test}"},
		{"go", "test", "./...", "{test}"}, {"go", "test", "./...", "-run=TestReady"},
		{"go", "test", "./...", "-run"}, {"go", "test", "./...", "-test.run={test}"},
	}
	for _, suffix := range [][]string{
		{"-run=TestOther"}, {"-test.run=TestOther"}, {"-count=0"}, {"-count=2"}, {"-count=-1"},
		{"-count=1", "-count", "1"}, {"-cpu=1,2"}, {"-cpu=0"}, {"-parallel=0"}, {"-timeout=-1s"},
		{"-tags=a", "-tags=b"}, {"-short=maybe"}, {"-shuffle=bad"}, {"-p=0"},
		{"-list=TestReady"}, {"-bench=."}, {"-benchmem"}, {"-fuzz=FuzzIt"}, {"-fuzztime=1s"},
		{"-c"}, {"-o=out"}, {"-exec=sh"}, {"-toolexec=sh"}, {"-mod=mod"}, {"-modfile=other.mod"},
		{"-overlay=overlay.json"}, {"-args", "-test.run=TestOther"}, {"--", "-test.count=2"},
		{"-json"}, {"-cover"}, {"-unknown=1"}, {"-tags", "bad\x00tag"}, {";", "touch", "PWN"},
	} {
		bad = append(bad, append([]string{"go", "test", "./...", "-run={test}"}, suffix...))
	}
	return bad
}

func TestGoAcceptanceUnsafeCommandsStopBeforePreparation(t *testing.T) {
	a := GoRepositoryAcceptanceAdapter{}
	for _, argv := range badCommands527() {
		got, err := a.Select(context.Background(), goPart527(argv...), goTest527("api/nested/ready_accept_test.go", "TestReady"))
		if err == nil || strings.TrimSpace(err.Error()) == "" || got.Package != "" || len(got.Argv) != 0 {
			t.Errorf("unsafe form retained selection: %q => %+v, %v", argv, got, err)
		}
	}
	for _, file := range []string{"api/testdata/p/ready_accept_test.go", "api/vendor/p/ready_accept_test.go", "api/_hidden/p/ready_accept_test.go", "api/.hidden/p/ready_accept_test.go"} {
		got, err := a.Select(context.Background(), goPart527("go", "test", "./...", "-run={test}"), goTest527(file, "TestReady"))
		if err == nil || len(got.Argv) != 0 {
			t.Fatalf("recursive scope was widened into a skipped directory: %s", file)
		}
	}
	// Each loader-admissible unsafe template is also checked through the actual
	// lifecycle. The loader itself already rejects missing/repeated placeholders.
	for _, argv := range [][]string{
		{"sh", "-c", "go test -run {test}"}, {"wrapper", "{test}"},
		{"go", "test", "./elsewhere", "-run={test}"},
		{"go", "test", "./...", "-run={test}", "-count=2"},
		{"go", "test", "./...", "-run={test}", "-exec=sh"},
		{"go", "test", "./...", "-run={test}", "-run=TestOther"},
	} {
		f := newFixture515(t)
		recapture515(t, f, func(c *repository.Config) { c.Parts[0].Commands["test_one"] = repository.Command{Argv: argv} })
		f.sandbox.Repository.Adapters = map[string]RepositoryAcceptanceAdapter{"go": a}
		checked := base515(t, f, context.Background())
		if checked.Problem == "" || len(f.runner.installs)+len(f.runner.inspections)+len(f.runner.opens)+len(f.runner.invocations) != 0 {
			t.Fatalf("unsupported template reached preparation/execution: %q: %+v", argv, checked)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := a.Select(ctx, goPart527("go", "test", "./...", "-run={test}"), goTest527("api/ready_accept_test.go", "TestReady"))
	if err == nil || len(got.Argv) != 0 {
		t.Fatal("canceled command validation produced an invocation")
	}
}
