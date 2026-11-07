package main

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/scope"
)

// Reuse #736's external-process fakes without changing its pinned tests.
// Scope, committed authority, admission, preparation, cache inspection and
// the actual command/sandbox orchestration remain production code.
func TestRepositoryGate740ModeledFactory(t *testing.T) {
	for _, mode := range []string{"existing", "new", "failed check", "outside project", "two projects", "cgo", "unratified new project"} {
		t.Run(mode, func(t *testing.T) {
			f := gateFixtureFor736(t, "go", "pytest")
			if mode != "new" && mode != "unratified new project" {
				gateWrite736(t, f.root, "api/.invariant/invariant.json", "{}\n")
				gateWrite736(t, f.root, "worker/.invariant/invariant.json", "{}\n")
				f.base = gateCommit736(t, f.root)
			} else {
				gateWrite736(t, f.root, "api/.invariant/invariant.json", "{}\n")
				if mode == "new" {
					gateWrite736(t, f.root, "api/.invariant/ratified.lock", `{"bounds":{},"statements":[],"ratified":{"by":"writer","issue":736}}`)
				}
			}
			gateWrite736(t, f.root, "api/main.go", "package fixture\nconst Value = 7\n")
			switch mode {
			case "outside project":
				gateWrite736(t, f.root, "README.md", "outside the model\n")
			case "two projects":
				gateWrite736(t, f.root, "worker/main.py", "VALUE = 7\n")
			case "cgo":
				gateWrite736(t, f.root, "api/main.go", "package fixture\nimport \"C\"\n")
			}
			gateCommit736(t, f.root)
			validScope := mode == "existing" || mode == "new" || mode == "failed check"
			classification, err := scope.Check(t.Context(), f.root, f.base, "HEAD", gateIssue736)
			if err != nil || classification.OK() != validScope || classification.Plumbing {
				t.Fatalf("bad modeled fixture: %+v, %v", classification, err)
			}
			r := f.runner()
			if mode == "failed check" {
				r.failure = "lint"
			}
			code, out := f.run(t.Context(), r, f.factoryArgs()...)
			if !validScope {
				if code == 0 || len(r.events) != 0 || !strings.Contains(strings.ToLower(out), "scope") {
					t.Fatalf("invalid modeled scope must fail before preparation: %d %v\n%s", code, r.events, out)
				}
				return
			}
			if mode == "failed check" {
				if code == 0 || !slices.Contains(r.commands, "api/lint") || !strings.Contains(out, "lint detail736") {
					t.Fatalf("modeled checks did not run and retain their failure: %d %v\n%s", code, r.commands, out)
				}
			} else if code != 0 || !slices.Equal(r.commands, []string{"api/format", "api/lint", "api/build", "api/test"}) {
				t.Fatalf("in-bounds modeled factory PR needs ordinary selected checks, without a plan: %d %v\n%s", code, r.commands, out)
			}
			if f.comments.calls != 0 || len(r.accepts) != 0 || slices.Contains(r.events, "install:worker") {
				t.Fatalf("modeled PR acquired plumbing acceptance or unrelated checks: comments=%d events=%v", f.comments.calls, r.events)
			}
		})
	}
	t.Run("plumbing still needs its plan", func(t *testing.T) {
		f := gateFixtureFor736(t, "go")
		f.native([]string{"api"})
		if err := os.Remove(filepath.Join(f.root, plumbing.LockPath(gateIssue736))); err != nil {
			t.Fatal(err)
		}
		gateCommit736(t, f.root)
		r := f.runner()
		if code, out := f.run(t.Context(), r, f.factoryArgs()...); code == 0 || len(r.events) != 0 || strings.TrimSpace(out) == "" {
			t.Fatalf("missing plumbing plan became ordinary CI: %d %v\n%s", code, r.events, out)
		}
	})
}

type gateDependencyRunner740 struct {
	*gateRunner736
	want     map[string]string
	installs []plumbing.DependencyRequest
	opens    []plumbing.DependencyRequest
}

func (r *gateDependencyRunner740) Install(ctx context.Context, q plumbing.DependencyRequest) (plumbing.DependencyArtifact, error) {
	if !maps.Equal(q.Inputs, r.want) {
		return plumbing.DependencyArtifact{}, fmt.Errorf("installation inputs are not the committed candidate: got %v want %v", q.Inputs, r.want)
	}
	part := r.config.Parts[0]
	if q.Image != part.Image || q.Directory != part.Directory {
		return plumbing.DependencyArtifact{}, fmt.Errorf("candidate replaced captured image or working directory: %+v", q)
	}
	wantArgv := slices.Clone(part.Commands["install"].Argv)
	switch part.Language {
	case "python":
		wantArgv = append(wantArgv, "--require-hashes", "--only-binary=:all:", "--ignore-installed", "--isolated", "--no-input", "--disable-pip-version-check", "--no-cache-dir")
	case "typescript":
		wantArgv = append(wantArgv, "--ignore-scripts", "--no-audit", "--no-fund")
	}
	if !slices.Equal(q.Argv, wantArgv) {
		return plumbing.DependencyArtifact{}, fmt.Errorf("candidate changed the admitted base install command: %q want %q", q.Argv, wantArgv)
	}
	a, err := r.gateRunner736.Install(ctx, q)
	if err == nil {
		q.Inputs = maps.Clone(q.Inputs)
		r.installs = append(r.installs, q)
	}
	return a, err
}

func (r *gateDependencyRunner740) OpenChecks(ctx context.Context, q plumbing.DependencyRequest) (plumbing.RepositoryCheckEnvironment, error) {
	a := r.images[q.Image]
	if !maps.Equal(a.Inputs, r.want) || !maps.Equal(q.Inputs, r.want) {
		return nil, fmt.Errorf("offline checks are using another dependency graph: prepared=%v request=%v want=%v", a.Inputs, q.Inputs, r.want)
	}
	for file, digest := range r.want {
		b, err := os.ReadFile(filepath.Join(q.SourceDir, filepath.FromSlash(file)))
		if err != nil || gateHash736(string(b)) != digest {
			return nil, fmt.Errorf("offline candidate bytes disagree with preparation for %s: %v", file, err)
		}
	}
	e, err := r.gateRunner736.OpenChecks(ctx, q)
	if err == nil {
		r.opens = append(r.opens, q)
	}
	return e, err
}

func runGate740(t *testing.T, f *gateFixture736, runner plumbing.DependencyRunner, cache string, args ...string) (int, string) {
	t.Helper()
	sb := plumbing.Sandbox{Timeout: time.Minute, ShardTimeout: 30 * time.Second,
		Repository: &plumbing.RepositoryChecks{Dependencies: plumbing.DependencyPreparer{CacheDir: cache, Platform: "linux/amd64", Runner: runner}}}
	var out bytes.Buffer
	code := runRepositoryCheck(t.Context(), append([]string{"-C", f.root, "-repo", gateRepo736}, args...), sb, f.comments, &out)
	return code, out.String()
}

func gateInputHashes740(t *testing.T, f *gateFixture736) map[string]string {
	t.Helper()
	want := map[string]string{}
	for _, p := range f.config.Parts {
		for _, name := range append(slices.Clone(p.Inputs), p.Lockfiles...) {
			b, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			want[name] = gateHash736(string(b))
		}
	}
	return want
}

func updateGateInputs740(t *testing.T, f *gateFixture736) {
	t.Helper()
	for _, p := range f.config.Parts {
		for _, name := range append(slices.Clone(p.Inputs), p.Lockfiles...) {
			b, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			text := string(b)
			switch p.Language {
			case "go":
				text = strings.ReplaceAll(text, "v1.2.3", "v1.2.4")
			case "python":
				text = strings.ReplaceAll(text, "24.2", "25.0")
			case "typescript":
				text = strings.ReplaceAll(text, "7.0.0", "6.0.0")
			}
			gateWrite736(t, f.root, name, text)
		}
	}
}

func TestRepositoryGate740CandidateDependencies(t *testing.T) {
	for _, framework := range []string{"go", "pytest", "vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			f := gateFixtureFor736(t, framework)
			r := &gateDependencyRunner740{gateRunner736: f.runner(), want: gateInputHashes740(t, f)}
			cache := t.TempDir()
			if code, out := runGate740(t, f, r, cache, "-base", f.base, f.base); code != 0 {
				t.Fatalf("unchanged dependencies must work: %d\n%s", code, out)
			}
			if len(r.installs) != 1 || len(r.opens) != 1 {
				t.Fatalf("base was not prepared and checked exactly once: %v %v", r.installs, r.opens)
			}
			oldKey := r.installs[0].Key
			oldImage := r.opens[0].Image
			updateGateInputs740(t, f)
			r.want = gateInputHashes740(t, f)
			// A committed contract edit must not change commands, toolchain,
			// input declarations or ownership used by this CI invocation.
			gateWrite736(t, f.root, repository.Path, `{"version":999,"parts":[]}`)
			head := gateCommit736(t, f.root)
			for file := range r.want {
				gateWrite736(t, f.root, file, "dirty input is not the candidate commit\n")
			}
			if code, out := runGate740(t, f, r, cache, "-base", f.base, head); code != 0 {
				t.Fatalf("locked candidate update must prepare and check under base policy: %d\n%s", code, out)
			}
			if len(r.installs) != 2 || len(r.opens) != 2 || r.installs[1].Key == oldKey || r.opens[1].Image == oldImage {
				t.Fatalf("changed graph reused base preparation: installs=%v opens=%v", r.installs, r.opens)
			}
			if code, out := runGate740(t, f, r, cache, "-base", f.base, head); code != 0 || len(r.installs) != 2 {
				t.Fatalf("same committed graph did not reuse verified preparation: %d\n%s", code, out)
			}
			gateGit736(t, f.root, "reset", "--hard", head)
			gateWrite736(t, f.root, "README.md", "another commit with identical dependency inputs\n")
			gateCommit736(t, f.root)
			if code, out := runGate740(t, f, r, cache, "-base", f.base); code != 0 || len(r.installs) != 2 {
				t.Fatalf("commit identity changed the dependency cache key: %d\n%s", code, out)
			}
			if len(r.commands) != 16 || r.open != r.closed || f.comments.calls != 0 || len(r.accepts) != 0 {
				t.Fatalf("ordinary checks lost commands, cleanup or authority separation: %+v", r.gateRunner736)
			}
		})
	}
	t.Run("native plumbing keeps captured dependency inputs", func(t *testing.T) {
		f := gateFixtureFor736(t, "go")
		f.native([]string{"api"}, "api/go.mod", "api/go.sum")
		updateGateInputs740(t, f)
		gateCommit736(t, f.root)
		r := f.runner()
		if code, out := f.run(t.Context(), r, f.factoryArgs()...); code == 0 || len(r.events) != 0 {
			t.Fatalf("ordinary update support weakened native plan input validation: %d %v\n%s", code, r.events, out)
		}
	})
}

func TestRepositoryGate740DependencyAdmission(t *testing.T) {
	for _, tc := range []struct{ name, framework, file, body string }{
		{"Go sums mismatch", "go", "api/go.sum", "example.invalid/dep v1.2.4 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"},
		{"Go local replacement", "go", "api/go.mod", "module example.invalid/app\ngo 1.27.1\nrequire example.invalid/dep v1.2.3\nreplace example.invalid/dep => ./local\n"},
		{"unlocked Python", "pytest", "worker/requirements.lock", "packaging>=24.2\n"},
		{"missing Python lock", "pytest", "worker/requirements.lock", ""},
		{"npm mismatch", "vitest", "web/package.json", `{"name":"fixture","version":"1.0.0","dependencies":{"is-number":"9.0.0"}}`},
		{"npm local artifact", "jest", "admin/package-lock.json", "local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The invalid part follows a valid Go part, so admission must finish
			// for the whole selection before any part starts installation.
			frameworks := []string{tc.framework}
			if tc.framework != "go" {
				frameworks = append([]string{"go"}, frameworks...)
			}
			f := gateFixtureFor736(t, frameworks...)
			if tc.body == "" {
				if err := os.Remove(filepath.Join(f.root, tc.file)); err != nil {
					t.Fatal(err)
				}
			} else if tc.body == "local" {
				b, err := os.ReadFile(filepath.Join(f.root, tc.file))
				if err != nil {
					t.Fatal(err)
				}
				gateWrite736(t, f.root, tc.file, strings.ReplaceAll(string(b), "https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz", "file:../local.tgz"))
			} else {
				gateWrite736(t, f.root, tc.file, tc.body)
			}
			gateWrite736(t, f.root, repository.Path, `{"version":1,"parts":[]}`)
			gateCommit736(t, f.root)
			r := f.runner()
			code, out := f.run(t.Context(), r, "-base", f.base)
			if code == 0 || len(r.events) != 0 || strings.TrimSpace(out) == "" {
				t.Fatalf("inadmissible candidate dependencies reached external work: %d %v\n%s", code, r.events, out)
			}
		})
	}
}
