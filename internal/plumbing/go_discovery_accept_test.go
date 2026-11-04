package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/repository"
)

type goCall527 struct {
	argv     []string
	dir      string
	env      []string
	deadline time.Time
}

// This is a transport for actual local Go processes, not a discovery fake.
// The production adapter must parse the actual toolchain's answer and compile
// the actual module. No fixture returns canned go list or compiler output.
type goEnvironment527 struct {
	t               *testing.T
	dir, executable string
	env             []string
	calls           []goCall527
	before          func(context.Context, []string) error
	afterStart      func()
	close           func(context.Context) error
}

func goCache527(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func goEnvironmentFor527(t *testing.T, root string, part repository.Part, cache string) *goEnvironment527 {
	t.Helper()
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("default Go acceptance tests need the Go toolchain: ", err)
	}
	e := &goEnvironment527{t: t, executable: goExe, dir: filepath.Join(root, filepath.FromSlash(part.Directory))}
	values := dependencyEnv("go", false)
	values["PATH"] = filepath.Dir(goExe) + string(os.PathListSeparator) + "/usr/bin:/bin"
	values["HOME"] = filepath.Join(cache, "home")
	values["GOCACHE"] = filepath.Join(cache, "build")
	values["GOMODCACHE"] = filepath.Join(cache, "mod")
	values["GOPATH"] = filepath.Join(cache, "path")
	values["TMPDIR"] = filepath.Join(cache, "tmp")
	values["CGO_ENABLED"] = "0"
	for _, name := range []string{"HOME", "GOCACHE", "GOMODCACHE", "GOPATH", "TMPDIR"} {
		if err := os.MkdirAll(values[name], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range values {
		e.env = append(e.env, k+"="+v)
	}
	return e
}

func (e *goEnvironment527) Run(ctx context.Context, argv []string) ([]byte, error) {
	end, bounded := ctx.Deadline()
	if !bounded {
		e.t.Error("Go discovery did not inherit a bounded context")
	}
	e.calls = append(e.calls, goCall527{slices.Clone(argv), e.dir, slices.Clone(e.env), end})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.before != nil {
		if err := e.before(ctx, argv); err != nil {
			return nil, err
		}
	}
	if len(argv) < 2 || argv[0] != "go" {
		return nil, fmt.Errorf("discovery requested non-Go command %q", argv)
	}
	switch argv[1] {
	case "list", "env", "version":
	case "test":
		if !slices.Contains(argv, "-c") {
			return nil, fmt.Errorf("discovery tried to execute candidate tests: %q", argv)
		}
	default:
		return nil, fmt.Errorf("discovery tried installation or another undeclared operation: %q", argv)
	}
	cmd := exec.CommandContext(ctx, e.executable, argv[1:]...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = e.dir, slices.Clone(e.env), time.Second
	if e.afterStart == nil {
		out, err := cmd.CombinedOutput()
		return out, errors.Join(err, ctx.Err())
	}
	// Cancel a real child after launch; no fabricated discovery response.
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	e.afterStart()
	return nil, errors.Join(cmd.Wait(), ctx.Err())
}

func (e *goEnvironment527) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if filepath.IsAbs(name) || strings.Contains(name, "\\") || filepath.ToSlash(filepath.Clean(name)) != name || name == ".." || strings.HasPrefix(name, "../") {
		return nil, fmt.Errorf("source read escaped part: %q", name)
	}
	data, err := regular.ReadFile(e.dir, name)
	return data, errors.Join(err, ctx.Err())
}

func (e *goEnvironment527) Close(ctx context.Context) error {
	if e.close != nil {
		return e.close(ctx)
	}
	return ctx.Err()
}

func goInvocation527(t *testing.T, part repository.Part, test Test, source string) RepositoryAcceptanceInvocation {
	t.Helper()
	selection, err := (GoRepositoryAcceptanceAdapter{}).Select(context.Background(), part, test)
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{Sources: map[string]string{test.File: source}, Execution: &Execution{Digest: hash468("captured policy " + part.Name)}}
	return repositoryInvocation(p, test, selection)
}

func goModule527(t *testing.T, cache, directory, module, pkg, filename, source string, argv []string) (repository.Part, Test, RepositoryAcceptanceInvocation, *goEnvironment527) {
	t.Helper()
	root := t.TempDir()
	part := goPart527(argv...)
	part.Directory = directory
	write468(t, root, filepath.ToSlash(filepath.Join(directory, "go.mod")), "module "+module+"\n\ngo 1.23\n")
	write468(t, root, filepath.ToSlash(filepath.Join(directory, pkg, "value.go")), "package fixture\nconst Value = 7\n")
	file := filepath.ToSlash(filepath.Join(directory, pkg, filename))
	write468(t, root, file, source)
	test := goTest527(file, "TestReady")
	in := goInvocation527(t, part, test, source)
	return part, test, in, goEnvironmentFor527(t, root, part, cache)
}

func discover527(t *testing.T, part repository.Part, in RepositoryAcceptanceInvocation, env RepositoryCheckEnvironment) (RepositoryAcceptanceDiscovery, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	return (GoRepositoryAcceptanceAdapter{}).Discover(ctx, part, in, env)
}

func assertDiscovery527(t *testing.T, got RepositoryAcceptanceDiscovery, in RepositoryAcceptanceInvocation, importPath string) {
	t.Helper()
	want := in.Binding
	want.Package = importPath
	if got.Binding != want || got.TestName != strings.Split(in.Binding.Selector, "/")[0] {
		t.Fatalf("discovery lost or substituted identity: %+v; want %+v", got, want)
	}
}

func assertDiscoveryError527(t *testing.T, got RepositoryAcceptanceDiscovery, err error, kind string) {
	t.Helper()
	var detail *RepositoryAcceptanceDiscoveryError
	if err == nil || !errors.As(err, &detail) || detail.Reason == "" || (kind != "" && detail.Kind != kind) || (detail.Kind != "setup" && detail.Kind != "refused") {
		t.Fatalf("missing structured %s refusal/setup information: %+v, %v", kind, got, err)
	}
	if !reflect.DeepEqual(got, RepositoryAcceptanceDiscovery{}) {
		t.Fatalf("failed discovery left selection authority: %+v", got)
	}
}

func TestGoAcceptanceRealToolchainDiscovery(t *testing.T) {
	cache := goCache527(t)
	for _, tc := range []struct {
		directory, module, pkg, preamble string
		external                         bool
	}{
		{".", "example.invalid/root527", ".", "", false},
		{"api", "example.invalid/api527", "nested", "", false},
		{"api", "example.invalid/api527", "nested", "//go:build accept527\n\n", false},
		{"services/api", "example.invalid/other527", "nested/deep", "//go:build accept527\n\n", true},
	} {
		name := tc.directory + "/" + tc.pkg + tc.preamble
		t.Run(name, func(t *testing.T) {
			importPath := tc.module
			if tc.pkg != "." {
				importPath += "/" + tc.pkg
			}
			source := tc.preamble + "package fixture\nimport \"testing\"\nfunc init() { panic(\"discovery must not run candidate initialization\") }\nfunc TestReady(t *testing.T) { if Value != 7 { t.Fatal(Value) } }\nfunc TestReadyPrefix(t *testing.T) { t.Fatal(\"neighbor\") }\n"
			if tc.external {
				source = tc.preamble + "package fixture_test\nimport (\"testing\"; f \"" + importPath + "\")\nfunc TestMain(m *testing.M) { panic(\"discovery must not run TestMain\") }\nfunc TestReady(t *testing.T) { if f.Value != 7 { t.Fatal(f.Value) } }\n"
			}
			argv := []string{"go", "test", "./...", "-tags=accept527", "-run", "^{test}$", "-count=1", "-short", "-timeout=11s"}
			part, test, in, env := goModule527(t, cache, tc.directory, tc.module, tc.pkg, "ready_accept_test.go", source, argv)
			// A same-name declaration and even a broken sibling must be irrelevant
			// after narrowing. Another module cannot supply the requested function.
			write468(t, env.dir, "elsewhere/value.go", "package elsewhere\nvar Bad = MissingSiblingAPI\n")
			write468(t, env.dir, "elsewhere/ready_test.go", "package elsewhere\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n")
			write468(t, env.dir, "othermodule/go.mod", "module example.invalid/foreign527\n\ngo 1.23\n")
			write468(t, env.dir, "othermodule/ready_test.go", "package foreign\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n")
			got, err := discover527(t, part, in, env)
			if err != nil {
				t.Fatal(err)
			}
			assertDiscovery527(t, got, in, importPath)
			listed, compiled := false, false
			for _, call := range env.calls {
				if call.dir != env.dir || !slices.Contains(call.env, "GOPROXY=off") || !slices.Contains(call.env, "GOSUMDB=off") || !slices.Contains(call.env, "GOTOOLCHAIN=local") || !slices.Contains(call.env, "GOWORK=off") {
					t.Fatalf("wrong workdir or online/inherited discovery environment: %+v", call)
				}
				if call.argv[1] == "list" {
					listed = true
				}
				if call.argv[1] == "test" {
					compiled = true
				}
				if call.argv[1] == "list" || call.argv[1] == "test" {
					if flag484(call.argv, "-tags") != "accept527" || !slices.Contains(call.argv, in.Binding.Package) || slices.Contains(call.argv, "./...") {
						t.Fatalf("toolchain lost build constraints or narrowed package: %q", call.argv)
					}
				}
			}
			if !listed || !compiled {
				t.Fatal("discovery did not use both the real package loader and compiler")
			}
			// There is intentionally no such subtest. Discovery only confirms its
			// top-level declaration; a later runtime collector must confirm the case.
			test.Selector = "TestReady/nonexistent.[x]/--literal"
			sub := goInvocation527(t, part, test, source)
			got, err = discover527(t, part, sub, env)
			if err != nil {
				t.Fatal(err)
			}
			assertDiscovery527(t, got, sub, importPath)
		})
	}
}

func TestGoAcceptanceWrongFileAndAmbiguousDiscovery(t *testing.T) {
	cache := goCache527(t)
	ordinary := "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n"
	otherOS := "windows"
	if runtime.GOOS == otherOS {
		otherOS = "linux"
	}
	for _, tc := range []struct{ name, file, source, other, kind string }{
		{"wrong file", "ready_accept_test.go", "package fixture\n", ordinary, "refused"},
		{"excluded tag", "ready_accept_test.go", "//go:build excluded527\n\n" + ordinary, ordinary, "refused"},
		{"excluded platform", "ready_" + otherOS + "_test.go", ordinary, "", "refused"},
		{"ignored filename", "_ready_test.go", ordinary, ordinary, "refused"},
		{"dot filename", ".ready_test.go", ordinary, ordinary, "refused"},
		{"duplicate in file", "ready_accept_test.go", ordinary + "func TestReady(t *testing.T) {}\n", "", "refused"},
		{"duplicate across files", "ready_accept_test.go", ordinary, ordinary, "refused"},
		{"ordinary and external matches", "ready_accept_test.go", ordinary, "package fixture_test\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n", "refused"},
		{"method", "ready_accept_test.go", "package fixture\nimport \"testing\"\ntype object struct{}\nfunc (object) TestReady(t *testing.T) {}\n", "", "refused"},
		{"wrong signature", "ready_accept_test.go", "package fixture\nfunc TestReady() {}\n", "", "refused"},
		{"wrong testing type", "ready_accept_test.go", "package fixture\nimport testing \"bytes\"\nfunc TestReady(t *testing.Buffer) {}\n", "", "refused"},
		{"generic declaration", "ready_accept_test.go", "package fixture\nimport \"testing\"\nfunc TestReady[T any](t *testing.T) {}\n", "", "refused"},
		{"result declaration", "ready_accept_test.go", "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) int { return 1 }\n", "", "refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			part, _, in, env := goModule527(t, cache, "api", "example.invalid/refuse527", ".", tc.file, tc.source, []string{"go", "test", "./...", "-run={test}", "-count=1"})
			if tc.other != "" {
				write468(t, env.dir, "other_test.go", tc.other)
			}
			got, err := discover527(t, part, in, env)
			assertDiscoveryError527(t, got, err, tc.kind)
		})
	}
	// Source and execution binding remain independent of identical labels and
	// names. Discovery cannot bless a changed staged copy, digest or argv.
	for _, change := range []string{"source bytes", "source digest", "package hint", "arguments", "owner", "framework"} {
		t.Run(change, func(t *testing.T) {
			part, _, in, env := goModule527(t, cache, "api", "example.invalid/bound527", ".", "ready_accept_test.go", ordinary, []string{"go", "test", "./...", "-run={test}"})
			switch change {
			case "source bytes":
				write468(t, env.dir, "ready_accept_test.go", ordinary+"// changed\n")
			case "source digest":
				in.Binding.SourceDigest = hash468("different")
			case "package hint":
				in.Binding.Package = "./elsewhere"
			case "arguments":
				in.Argv = []string{"go", "test", "./...", "-run=.*"}
			case "owner":
				in.Binding.Part = "another part"
			case "framework":
				in.Binding.Framework = "pytest"
			}
			got, err := discover527(t, part, in, env)
			assertDiscoveryError527(t, got, err, "refused")
		})
	}
}

func TestGoAcceptanceDiscoverySetupAndInterruption(t *testing.T) {
	cache := goCache527(t)
	for _, mode := range []string{"missing go", "missing compiler", "unresolved dependency", "invalid module", "nested module", "already canceled", "cancel child"} {
		t.Run(mode, func(t *testing.T) {
			source := "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n"
			if mode == "unresolved dependency" {
				source = "package fixture\nimport (\"testing\"; _ \"missing.invalid/unavailable\")\nfunc TestReady(t *testing.T) {}\n"
			}
			part, _, in, env := goModule527(t, cache, "api", "example.invalid/setup527", "nested", "ready_accept_test.go", source, []string{"go", "test", "./...", "-run=^{test}$", "-count=1"})
			switch mode {
			case "missing go":
				env.executable = filepath.Join(t.TempDir(), "missing-go")
			case "missing compiler":
				// Real Go sees a GOROOT with sources but no compiler tools.
				broken := t.TempDir()
				if err := os.Symlink(filepath.Join(runtime.GOROOT(), "src"), filepath.Join(broken, "src")); err != nil {
					t.Fatal(err)
				}
				env.env = append(env.env, "GOROOT="+broken)
			case "invalid module":
				write468(t, env.dir, "go.mod", "not a module declaration\n")
			case "nested module":
				write468(t, env.dir, "nested/go.mod", "module example.invalid/separate527\n\ngo 1.23\n")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if mode == "already canceled" {
				cancel()
			}
			if mode == "cancel child" {
				env.afterStart = cancel
			}
			got, err := (GoRepositoryAcceptanceAdapter{}).Discover(ctx, part, in, env)
			assertDiscoveryError527(t, got, err, "setup")
			if strings.Contains(mode, "cancel") && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was lost: %v", err)
			}
			if mode == "already canceled" && len(env.calls) != 0 {
				t.Fatal("already canceled discovery launched work")
			}
			if mode == "cancel child" && len(env.calls) == 0 {
				t.Fatal("cancellation fixture never launched a real Go child")
			}
		})
	}
}
