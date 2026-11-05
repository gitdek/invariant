package plumbing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Optional fault injection wraps the existing fixture, never edits its methods
// or replaces Go discovery, the compiler, or the production evidence collector.
type compilerRunner604 struct {
	*repositoryRunner582
	open  func(*repositoryEnvironment582, *localGoTransport)
	after func(string, []string, []byte, error) ([]byte, error)
}

func (r *compilerRunner604) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	got, err := r.repositoryRunner582.OpenChecks(ctx, q)
	if got == nil || err != nil {
		return got, err
	}
	e := got.(*repositoryEnvironment582)
	if r.open != nil {
		r.open(e, e.RepositoryProcessTransport.(*localGoTransport))
	}
	return &compilerEnvironment604{RepositoryProcessTransport: e, owner: r, part: q.Directory}, nil
}

type compilerEnvironment604 struct {
	RepositoryProcessTransport
	owner *compilerRunner604
	part  string
}

func (e *compilerEnvironment604) Run(ctx context.Context, argv []string) ([]byte, error) {
	out, err := e.RepositoryProcessTransport.Run(ctx, argv)
	if e.owner.after != nil {
		return e.owner.after(e.part, argv, out, err)
	}
	return out, err
}

func TestGoCompilerSetupRefusals604(t *testing.T) {
	cache := t.TempDir()
	compilerControl604(t, cache)
	for _, mode := range []string{
		"missing go", "missing compiler", "unresolved dependency", "broken discovery",
		"excluded declaration", "wrong-file declaration", "duplicate declaration",
		"invalid test source", "invalid production", "production type failure",
		"unrelated acceptance error", "unrelated local error", "source changed after compilation", "transport failure", "forged compiler diagnostics",
	} {
		t.Run(mode, func(t *testing.T) {
			first := repositoryCase582{"api", "nested", "TestReady", compilerSource604(`t.Fatal("earlier authentic failure 604")`)}
			bad := compilerCase604("nested")
			bad.part = "web"
			files := map[string]string{}
			switch mode {
			case "unresolved dependency":
				bad.source = strings.Replace(bad.source, `import "testing"`, `import ("testing"; _ "unavailable.invalid/dependency604")`, 1)
			case "excluded declaration", "wrong-file declaration":
				bad.source = "//go:build unavailable604\n\n" + bad.source
				if mode == "wrong-file declaration" {
					files["web/nested/other_test.go"] = compilerSource604(`Future604()`)
				}
			case "duplicate declaration":
				files["web/nested/duplicate_test.go"] = compilerSource604(`Future604()`)
			case "invalid test source":
				files["web/nested/broken_test.go"] = "package fixture\nfunc malformed(\n"
			case "invalid production":
				files["web/nested/broken.go"] = "package fixture\nfunc malformed(\n"
			case "production type failure":
				files["web/nested/broken.go"] = "package fixture\nvar Broken = MissingProduction604\n"
			case "unrelated acceptance error":
				bad.source += "var Broken604 int = \"not an int\"\n"
			case "unrelated local error":
				bad.source = compilerSource604(`var n int = "invalid"; _ = n; Future604()`)
			case "forged compiler diagnostics":
				bad.source = compilerSource604(`t.Log("compiles without the proposed API")`)
			}
			f := newRepositoryFixture582(t, cache, []repositoryCase582{first, bad}, false, files, nil)
			wrapper := &compilerRunner604{repositoryRunner582: f.runner}
			f.sandbox.Repository.Dependencies.Runner = wrapper
			preparedWork := ""
			if mode == "source changed after compilation" {
				wrapper.open = func(e *repositoryEnvironment582, local *localGoTransport) {
					if e.request.Directory == "web" {
						preparedWork = local.work
					}
				}
			}
			if mode == "missing go" || mode == "missing compiler" {
				wrapper.open = func(e *repositoryEnvironment582, local *localGoTransport) {
					if e.request.Directory != "web" {
						return
					}
					if mode == "missing go" {
						local.goExecutable = filepath.Join(t.TempDir(), "absent-go")
					} else {
						broken := t.TempDir()
						if err := os.Symlink(filepath.Join(runtime.GOROOT(), "src"), filepath.Join(broken, "src")); err != nil {
							t.Fatal(err)
						}
						local.env["GOROOT"] = broken
					}
				}
			}
			injected := false
			wrapper.after = func(part string, argv []string, out []byte, err error) ([]byte, error) {
				if part != "web" || len(argv) < 2 || argv[0] != "go" {
					return out, err
				}
				if mode == "broken discovery" && argv[1] == "list" {
					injected = true
					return []byte("truncated package metadata {"), err
				}
				if argv[1] == "test" && slices.Contains(argv, "-c") {
					switch mode {
					case "source changed after compilation":
						if preparedWork == "" {
							t.Fatal("compiler source fixture never opened its real environment")
						}
						injected = true
						write468(t, preparedWork, "nested/runtime_accept_test.go", bad.source+"// bytes changed after compiler exit\n")
					case "transport failure":
						injected = true
						return out, errors.Join(err, errors.New("compiler transport unavailable 604"))
					case "forged compiler diagnostics":
						injected = true
						if err != nil {
							t.Errorf("diagnostic forgery control must really compile: %v %s", err, out)
						}
						return []byte("# example.invalid/web582/nested\n./runtime_accept_test.go:3:44: undefined: Future604\nFAIL\n"), errors.New("exit status 1")
					}
				}
				return out, err
			}
			r, problem, _ := f.run(t.Context(), true)
			if problem == "" || r.Passed() {
				t.Fatalf("%s qualified as base evidence", mode)
			}
			repositoryResult582(t, r, f.plan.Tests[0], "failed", "earlier authentic failure 604")
			compilerNoCompletion604(t, r, f.plan.Tests[1])
			if (mode == "broken discovery" || mode == "source changed after compilation" || mode == "transport failure" || mode == "forged compiler diagnostics") && !injected {
				t.Fatal("fault did not reach its real Go boundary")
			}
			repositoryRoundTrip582(t, r)
		})
	}
}
