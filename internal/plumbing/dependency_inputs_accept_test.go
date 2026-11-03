package plumbing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestRepositoryDependencyCacheIncludesEveryEffectiveInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*repository.Part, map[string]string, *string)
	}{
		{"lock bytes", func(p *repository.Part, files map[string]string, platform *string) {
			files["deps/requirements.lock"] = strings.ReplaceAll(files["deps/requirements.lock"], "24.2", "24.1")
		}},
		{"additional input bytes", func(p *repository.Part, files map[string]string, platform *string) {
			files["config/install.cfg"] = "mode=second\n"
		}},
		{"additional input path", func(p *repository.Part, files map[string]string, platform *string) {
			files["config/renamed.cfg"] = files["config/install.cfg"]
			p.Inputs = []string{"config/renamed.cfg"}
		}},
		{"declared install arguments", func(p *repository.Part, files map[string]string, platform *string) {
			c := p.Commands["install"]
			c.Argv = append(c.Argv, "--quiet")
			p.Commands["install"] = c
		}},
		{"part directory", func(p *repository.Part, files map[string]string, platform *string) {
			p.Directory = "other"
		}},
		{"pinned image", func(p *repository.Part, files map[string]string, platform *string) {
			p.Image = "registry.example/toolchain@sha256:" + strings.Repeat("d", 64)
		}},
		{"platform", func(p *repository.Part, files map[string]string, platform *string) {
			*platform = "linux/arm64"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The same shared lock is reachable from either part directory.
			// This isolates cwd identity from the lock path and command identity.
			part := part468("python", "app")
			part.Lockfiles = []string{"deps/requirements.lock"}
			part.Inputs = []string{"config/install.cfg"}
			part.Commands["install"] = command468("python3", "-m", "pip", "install", "--require-hashes", "--only-binary=:all:", "-r", "../deps/requirements.lock")
			files := map[string]string{
				"deps/requirements.lock": files468("python", ".")["requirements.lock"],
				"config/install.cfg":     "mode=first\n",
				"app/source.py":          "pass\n",
				"other/source.py":        "pass\n",
			}
			root, snapshot := snapshot468(t, []repository.Part{part}, files)
			runner := newRunner468(t)
			preparer := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: runner}
			first := prepare468(t, preparer, snapshot, part.Name).Artifacts()[part.Name]
			tc.change(&part, files, &preparer.Platform)
			changed := commit468(t, root, []repository.Part{part}, files)
			second := prepare468(t, preparer, changed, part.Name).Artifacts()[part.Name]
			if len(runner.installs) != 2 || first.Key == second.Key || first.Image == second.Image {
				t.Fatalf("changed %s reused the previous dependency environment: installs=%d first=%+v second=%+v", tc.name, len(runner.installs), first, second)
			}
			third := prepare468(t, preparer, changed, part.Name).Artifacts()[part.Name]
			if len(runner.installs) != 2 || third.Key != second.Key || third.Image != second.Image {
				t.Fatalf("unchanged new inputs did not reuse: installs=%d second=%+v third=%+v", len(runner.installs), second, third)
			}
		})
	}
}

func TestRepositoryDependenciesRefuseUnlockedAndUnsupportedInputs(t *testing.T) {
	for _, tc := range []struct {
		name, language string
		change         func(*repository.Part, map[string]string)
	}{
		{"missing Go sums", "go", func(p *repository.Part, files map[string]string) {
			p.Lockfiles = []string{}
			delete(files, "go.sum")
		}},
		{"Go sums do not cover required version", "go", func(p *repository.Part, files map[string]string) {
			files["go.sum"] = strings.ReplaceAll(files["go.sum"], "v1.2.3", "v1.2.4")
		}},
		{"floating Go version", "go", func(p *repository.Part, files map[string]string) {
			files["go.mod"] = strings.ReplaceAll(files["go.mod"], "v1.2.3", "latest")
		}},
		{"local Go replacement", "go", func(p *repository.Part, files map[string]string) {
			files["go.mod"] += "\nreplace example.invalid/dep => ./local\n"
			files["local/go.mod"] = "module example.invalid/dep\n\ngo 1.27.1\n"
		}},
		{"floating Python version", "python", func(p *repository.Part, files map[string]string) {
			files["requirements.lock"] = strings.ReplaceAll(files["requirements.lock"], "==24.2", ">=24.2")
		}},
		{"wildcard Python version", "python", func(p *repository.Part, files map[string]string) {
			files["requirements.lock"] = strings.ReplaceAll(files["requirements.lock"], "==24.2", "==24.*")
		}},
		{"Python missing hash", "python", func(p *repository.Part, files map[string]string) {
			files["requirements.lock"] = "packaging==24.2\n"
		}},
		{"Python invalid hash", "python", func(p *repository.Part, files map[string]string) {
			files["requirements.lock"] = "packaging==24.2 --hash=sha256:incomplete\n"
		}},
		{"Python undeclared recursive input", "python", func(p *repository.Part, files map[string]string) {
			files["extra.lock"] = files["requirements.lock"]
			files["requirements.lock"] = "-r extra.lock\n"
		}},
		{"Python VCS requirement", "python", func(p *repository.Part, files map[string]string) {
			files["requirements.lock"] = "packaging @ git+https://example.invalid/repo.git@main\n"
		}},
		{"missing npm lock", "typescript", func(p *repository.Part, files map[string]string) {
			p.Lockfiles = []string{}
			delete(files, "package-lock.json")
		}},
		{"npm manifest lock disagreement", "typescript", func(p *repository.Part, files map[string]string) {
			files["package.json"] = strings.ReplaceAll(files["package.json"], "7.0.0", "8.0.0")
		}},
		{"npm workspace", "typescript", func(p *repository.Part, files map[string]string) {
			files["package.json"] = `{"name":"fixture","version":"1.0.0","workspaces":["packages/*"],"dependencies":{"is-number":"7.0.0"}}`
		}},
		{"npm missing integrity", "typescript", func(p *repository.Part, files map[string]string) {
			files["package-lock.json"] = strings.ReplaceAll(files["package-lock.json"], "sha512-"+strings.Repeat("A", 86)+"==", "")
		}},
		{"npm floating resolved version", "typescript", func(p *repository.Part, files map[string]string) {
			files["package-lock.json"] = strings.ReplaceAll(files["package-lock.json"], `"version":"7.0.0"`, `"version":"latest"`)
		}},
		{"npm local artifact", "typescript", func(p *repository.Part, files map[string]string) {
			files["package-lock.json"] = strings.ReplaceAll(files["package-lock.json"], "https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz", "file:../local.tgz")
		}},
		{"npm install instead of ci", "typescript", func(p *repository.Part, files map[string]string) {
			p.Commands["install"] = command468("npm", "install")
		}},
		{"unsupported Python manager", "python", func(p *repository.Part, files map[string]string) {
			p.Commands["install"] = command468("uv", "sync", "--frozen")
		}},
		{"shell installation wrapper", "go", func(p *repository.Part, files map[string]string) {
			p.Commands["install"] = command468("sh", "-c", "go mod download")
		}},
		{"inapplicable installation with declared dependencies", "go", func(p *repository.Part, files map[string]string) {
			p.Commands["install"] = repository.Command{NotApplicable: "Skip installing the dependencies."}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			part := part468(tc.language, ".")
			files := files468(tc.language, ".")
			tc.change(&part, files)
			_, snapshot := snapshot468(t, []repository.Part{part}, files)
			runner := newRunner468(t)
			preparer := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: runner}
			if _, err := preparer.Prepare(context.Background(), snapshot, []string{part.Name}); err == nil {
				t.Fatal("unlocked, inconsistent or unsupported dependency preparation succeeded")
			}
			if len(runner.installs) != 0 || len(runner.runs) != 0 {
				t.Fatalf("obviously invalid declaration reached a repository command: installs=%d runs=%d", len(runner.installs), len(runner.runs))
			}
		})
	}
}

func TestRepositoryDependenciesRejectLockRewritesBeforePublishing(t *testing.T) {
	for _, language := range []string{"go", "python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			part := part468(language, ".")
			files := files468(language, ".")
			root, snapshot := snapshot468(t, []repository.Part{part}, files)
			runner := newRunner468(t)
			lock := part.Lockfiles[0]
			runner.installHook = func(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
				if err := os.WriteFile(filepath.Join(q.SourceDir, filepath.FromSlash(lock)), []byte(files[lock]+"\nrewritten by installer\n"), 0o644); err != nil {
					return DependencyArtifact{}, fmt.Errorf("lock rewrite prevented: %w", err)
				}
				return runner.finish(q), nil
			}
			preparer := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: runner}
			if _, err := preparer.Prepare(context.Background(), snapshot, []string{part.Name}); err == nil {
				t.Fatal("installer rewrote a lock but published a successful preparation")
			}
			original, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(lock)))
			if err != nil || string(original) != files[lock] {
				t.Fatalf("installation changed the original repository lock: %q, %v", original, err)
			}
			runner.installHook = nil
			prepare468(t, preparer, snapshot, part.Name)
			if len(runner.installs) != 2 {
				t.Fatalf("rewritten preparation became reusable: installs=%d", len(runner.installs))
			}
		})
	}
}

func TestRepositoryDependenciesSupportEmptyDependencyGraphs(t *testing.T) {
	for _, language := range []string{"go", "python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			part := part468(language, ".")
			var files map[string]string
			switch language {
			case "go":
				part.Lockfiles = []string{}
				files = map[string]string{"go.mod": "module example.invalid/stdlib\n\ngo 1.27.1\n"}
			case "python":
				files = map[string]string{"requirements.lock": ""}
			case "typescript":
				files = map[string]string{
					"package.json":      `{"name":"fixture","version":"1.0.0","private":true}`,
					"package-lock.json": `{"name":"fixture","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"fixture","version":"1.0.0"}}}`,
				}
			}
			root, snapshot := snapshot468(t, []repository.Part{part}, files)
			runner := newRunner468(t)
			preparer := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: runner}
			session := prepare468(t, preparer, snapshot, part.Name)
			artifact := session.Artifacts()[part.Name]
			if !artifact.Complete || artifact.Image == "" || !reflect.DeepEqual(artifact.Inputs, hashes468(files)) {
				t.Fatalf("dependency-free preparation lost its identity/inputs: %+v", artifact)
			}
			if _, err := session.Run(context.Background(), part.Name, root, []string{"check-empty-graph"}); err != nil {
				t.Fatal(err)
			}
			if len(runner.runs) != 1 {
				t.Fatalf("offline checks=%d", len(runner.runs))
			}
			secureRequest468(t, runner.runs[0], false)
			before := len(runner.installs)
			again := prepare468(t, preparer, snapshot, part.Name).Artifacts()[part.Name]
			if len(runner.installs) != before || again.Key != artifact.Key {
				t.Fatal("dependency-free preparation was not reusable")
			}
		})
	}
}

// A bare pinned toolchain has an immutable identity, but has no per-install
// recipe evidence. No-op preparation may derive that evidence itself or seal
// an empty environment with an offline, command-free Install request.
type noInstallRunner468 struct{ *runner468 }

func (r noInstallRunner468) Inspect(ctx context.Context, image, platform string) (DependencyArtifact, error) {
	if image != image468 && image != baseID468 {
		return r.runner468.Inspect(ctx, image, platform)
	}
	if err := ctx.Err(); err != nil {
		return DependencyArtifact{}, err
	}
	a := DependencyArtifact{Image: baseID468, Base: image468, BaseImage: baseID468, Platform: platform, Complete: true, Inputs: map[string]string{}}
	r.mu.Lock()
	r.inspections = append(r.inspections, image)
	r.images[baseID468] = a
	r.mu.Unlock()
	return a, nil
}

func TestRepositoryDependenciesHonorExplicitNoInstall(t *testing.T) {
	part := part468("python", ".")
	part.Inputs, part.Lockfiles = []string{}, []string{}
	part.Commands["install"] = repository.Command{NotApplicable: "Only the standard library is used."}
	root, snapshot := snapshot468(t, []repository.Part{part}, map[string]string{"app.py": "pass\n"})
	runner := noInstallRunner468{newRunner468(t)}
	runner.installHook = func(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
		if q.Network || len(q.Argv) != 0 {
			return DependencyArtifact{}, fmt.Errorf("not_applicable ran a network installation: %+v", q)
		}
		return runner.finish(q), nil
	}
	preparer := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: runner}
	session := prepare468(t, preparer, snapshot, part.Name)
	a := session.Artifacts()[part.Name]
	if !a.Complete || a.Image == "" || len(a.Inputs) != 0 || a.Base != image468 || a.BaseImage != baseID468 {
		t.Fatalf("no-install part lacks a pinned, usable environment: %+v", a)
	}
	if _, err := session.Run(context.Background(), part.Name, root, []string{"python3", "-I", "app.py"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("offline checks=%d", len(runner.runs))
	}
	secureRequest468(t, runner.runs[0], false)
	for _, q := range runner.installs {
		if q.Network || len(q.Argv) != 0 {
			t.Fatalf("no-install declaration executed a repository installation: %+v", q)
		}
	}
}

func TestRepositoryDependenciesAcceptNpmRangesCoveredByLock(t *testing.T) {
	part := part468("typescript", ".")
	files := files468("typescript", ".")
	files["package.json"] = strings.ReplaceAll(files["package.json"], `"is-number":"7.0.0"`, `"is-number":"^7.0.0"`)
	files["package-lock.json"] = strings.ReplaceAll(files["package-lock.json"], `"is-number":"7.0.0"`, `"is-number":"^7.0.0"`)
	_, snapshot := snapshot468(t, []repository.Part{part}, files)
	runner := newRunner468(t)
	preparer := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: runner}
	prepare468(t, preparer, snapshot, part.Name)
	if len(runner.installs) != 1 {
		t.Fatalf("complete lock did not admit ordinary npm manifest ranges: installs=%d", len(runner.installs))
	}
}
