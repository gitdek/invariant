package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

// Install only seals an already-local image with no installation command or
// network. Real Docker, language tools, helpers and collectors supply results.
type imageRunner621 struct {
	*DockerDependencyRunner
	t      *testing.T
	opens  []DependencyRequest
	images []string
}

func (r *imageRunner621) Install(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
	if q.Network || len(q.Argv) != 0 {
		return DependencyArtifact{}, errors.New("621 offline fixture cannot install dependencies")
	}
	q.Pull = false
	a, err := r.DockerDependencyRunner.Install(ctx, q)
	if a.Image != "" {
		r.images = append(r.images, a.Image)
	}
	return a, err
}

func (r *imageRunner621) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	if q.Network || q.Pull || !dependencyID.MatchString(q.Image) {
		return nil, errors.New("621 invocation lost its offline immutable preparation")
	}
	r.opens = append(r.opens, cloneDependencyRequest(q))
	return r.DockerDependencyRunner.OpenChecks(ctx, q)
}

func realFixture621(t *testing.T, framework, image, platform, source string) (Sandbox, string, *Plan, *imageRunner621) {
	t.Helper()
	root, candidate := t.TempDir(), t.TempDir()
	part := part617("app", image)
	part.Commands["test_one"] = repository.Command{Argv: []string{"python3", "-m", "pytest", "--import-mode=importlib", "-s", "-q", "{test}"}}
	file, production, body := "app/test_isolation.py", "app/product.py", "VALUE = 0\n"
	selectors := []string{"test_isolation.py::test_first", "test_isolation.py::test_later"}
	if framework == "go" {
		part = docker591Part("app")
		part.Image = image
		file, production, body = "app/isolation_accept_test.go", "app/value.go", "package fixture\nconst Value=0\n"
		selectors = []string{"TestFirst", "TestLater"}
		write468(t, root, "app/go.mod", "module example.invalid/isolation621\n\ngo 1.27.1\n")
	}
	write468(t, root, production, body)
	config := repository.Config{Version: 1, Parts: []repository.Part{part}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, root, repository.Path, string(data))
	git468(t, root, "init", "-q")
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-qm", "offline isolation fixture621")
	snapshot, err := repository.Load(t.Context(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{Name: "offline isolation", Summary: "Return seven independently.", Files: []string{production}, Sources: map[string]string{file: source}}
	for _, selector := range selectors {
		p.Tests = append(p.Tests, Test{Name: "same display label", Part: "app", Framework: framework, File: file, Selector: selector, Says: "Use pristine tools and inputs."})
	}
	p = n503Read(t, p, snapshot)
	if err := CopyCheckout(t.Context(), root, candidate); err != nil {
		t.Fatal(err)
	}
	r := &imageRunner621{DockerDependencyRunner: &DockerDependencyRunner{}, t: t}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
		defer cancel()
		for _, q := range r.opens {
			if _, err := dependencyDocker(ctx, "inspect", q.Token); err == nil {
				t.Error("workload container survived check cleanup: ", q.Token)
				_, _ = dependencyDocker(ctx, "rm", "--force", q.Token)
			}
		}
		for _, id := range r.images {
			if _, err := dependencyDocker(ctx, "image", "rm", id); err != nil {
				t.Error(err)
			}
		}
	})
	s := Sandbox{Original: root, Timeout: 8 * time.Minute, ShardTimeout: 7 * time.Minute, Repository: &RepositoryChecks{Snapshot: snapshot, Dependencies: DependencyPreparer{CacheDir: t.TempDir(), Platform: platform, Runner: r}}}
	return s, candidate, p, r
}

func TestInvocationOfflineImages621(t *testing.T) {
	// This control always runs and fails on the original shared environment.
	// Opt-in absence is not a passing or skipped substitute for an acceptance.
	t.Run("mandatory daemon-free control", TestConfiguredInvocationIsolation621)
	if t.Failed() {
		return
	}
	if os.Getenv("INVARIANT_ACCEPTANCE_ISOLATION_REAL") != "1" {
		t.Log("set INVARIANT_ACCEPTANCE_ISOLATION_REAL=1 with INVARIANT_ACCEPTANCE_ISOLATION_GO_IMAGE, INVARIANT_ACCEPTANCE_ISOLATION_PYTEST_IMAGE (digest-pinned local references), and INVARIANT_ACCEPTANCE_ISOLATION_PLATFORM; no images or packages are fetched")
		return
	}
	platform := os.Getenv("INVARIANT_ACCEPTANCE_ISOLATION_PLATFORM")
	if !dependencyPlatform.MatchString(platform) {
		t.Fatal("enabled isolation opt-in needs an explicit Linux platform")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("enabled isolation opt-in needs Docker: ", err)
	}
	goSource := `package fixture
import("testing";"os";"path/filepath";"runtime";"strings")
func put621(t *testing.T, name string, data []byte, mode os.FileMode) {
    t.Helper()
    if err:=os.MkdirAll(filepath.Dir(name),0700);err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(name,data,mode);err!=nil{t.Fatal(err)}
}
func TestFirst(t *testing.T) {
    cache:=os.Getenv("GOCACHE")
    damaged:=false
    err:=filepath.WalkDir(cache,func(name string,d os.DirEntry,err error) error {
        if err!=nil{return err}
        if !damaged && d.Type().IsRegular() && strings.HasSuffix(name,"-d") {
            damaged=true
            return os.WriteFile(name,[]byte("poisoned compiler cache621"),0600)
        }
        return nil
    })
    if err!=nil || !damaged {t.Fatalf("cache attack did not execute: %v damaged=%v",err,damaged)}
    put621(t,filepath.Join(cache,"poison621"),[]byte("poison"),0600)
    put621(t,filepath.Join(runtime.GOROOT(),".startup621"),[]byte("poison"),0600)
    compiler:=filepath.Join(runtime.GOROOT(),"pkg","tool",runtime.GOOS+"_"+runtime.GOARCH,"compile")
    for _,tool:=range []string{compiler,filepath.Join(runtime.GOROOT(),"bin","go")} {
        if err:=os.Rename(tool,tool+".original621");err!=nil{t.Fatal(err)}
        put621(t,tool,[]byte(REPLACEMENT621),0700)
    }
    t.Log("ATTACK_COMPLETED621")
    if Value!=7{t.Fatal("base needs seven621")}
}
func TestLater(t *testing.T) {
    for _,name:=range []string{filepath.Join(os.Getenv("GOCACHE"),"poison621"),filepath.Join(runtime.GOROOT(),".startup621")} {
        if _,err:=os.Stat(name);!os.IsNotExist(err){t.Fatal("inherited earlier workload",name)}
    }
    t.Log("AUTHENTIC_LATER621")
    if Value!=7{t.Fatal("base needs seven621")}
}
`
	goSource = strings.ReplaceAll(goSource, "REPLACEMENT621", fmt.Sprintf("%q", string(forgedTransport617("go"))))
	pySource := `import os, pathlib, site, sys
import product
def test_first():
    import pytest, _pytest
    # Poison installed framework and its bytecode, and an actual site hook.
    module = pathlib.Path(_pytest.__file__)
    module.write_text("raise RuntimeError('poisoned pytest621')\n")
    caches = list(module.parent.rglob('*.pyc'))
    assert caches, 'cache attack must reach installed bytecode'
    caches[0].write_bytes(b'poisoned bytecode621')
    pathlib.Path(site.getsitepackages()[0], 'sitecustomize.py').write_text("raise RuntimeError('poisoned startup621')\n")
    pathlib.Path('/tmp/poison621').write_text('poison')
    interpreter = pathlib.Path(os.path.realpath(sys.executable))
    interpreter.rename(str(interpreter) + '.original621')
    interpreter.write_text(REPLACEMENT621)
    interpreter.chmod(0o700)
    print('ATTACK_COMPLETED621', flush=True)
    assert product.VALUE == 7, 'base needs seven621'
def test_later():
    assert not pathlib.Path('/tmp/poison621').exists()
    print('AUTHENTIC_LATER621', flush=True)
    assert product.VALUE == 7, 'base needs seven621'
`
	// A JSON string literal is also a Python string literal for this ASCII script.
	replacement, err := json.Marshal(string(forgedTransport617("go")))
	if err != nil {
		t.Fatal(err)
	}
	pySource = strings.ReplaceAll(pySource, "REPLACEMENT621", string(replacement))
	for _, tc := range []struct{ framework, variable, source string }{
		{"go", "INVARIANT_ACCEPTANCE_ISOLATION_GO_IMAGE", goSource},
		{"pytest", "INVARIANT_ACCEPTANCE_ISOLATION_PYTEST_IMAGE", pySource},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			image := os.Getenv(tc.variable)
			if !dependencyBase.MatchString(image) {
				t.Fatal("enabled opt-in needs an already-local digest-pinned image in ", tc.variable)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
			defer cancel()
			if _, err := inspectDependencyImage(ctx, image, platform); err != nil {
				t.Fatal("local image or daemon prerequisite failed; nothing is downloaded: ", err)
			}
			args := []string{"run", "--rm", "--pull", "never", "--network", "none", "--platform", platform, "--cap-drop", "ALL"}
			if tc.framework == "pytest" {
				args = append(args, "--entrypoint", "python3", image, "-I", "-c", "import sys,pytest; assert sys.version_info[:2]==(3,13); assert pytest.__version__=='8.3.5'")
			} else {
				args = append(args, "--entrypoint", "go", image, "version")
			}
			out, err := dependencyDocker(ctx, args...)
			if err != nil || tc.framework == "go" && !strings.Contains(string(out), "go1.27.") {
				t.Fatalf("pinned runtime prerequisite failed: %v %s", err, out)
			}
			s, candidate, p, r := realFixture621(t, tc.framework, image, platform, tc.source)
			base, err := Check(ctx, n503Draft(t, p), s.Original, s)
			if err != nil || base == nil || base.Problem != "" {
				t.Fatalf("real base failed to isolate hostile selector: %v %+v", err, base)
			}
			if !strings.Contains(base.Run.Output+base.Run.Summary(), "ATTACK_COMPLETED621") || !strings.Contains(base.Run.Output+base.Run.Summary(), "AUTHENTIC_LATER621") {
				t.Fatal("attack and later authentic workload must both actually complete")
			}
			for _, test := range p.Tests {
				if got := result515(t, base.Run, test); got.Status != "failed" || got.Kind != "test" {
					t.Fatalf("attack manufactured base evidence: %+v", got)
				}
			}
			fixed := "VALUE = 7\n"
			if tc.framework == "go" {
				fixed = "package fixture\nconst Value=7\n"
			}
			write468(t, candidate, p.Files[0], fixed)
			result, err := s.Run(ctx, candidate, p)
			if err != nil || !result.Passed() || !strings.Contains(result.Output+result.Summary(), "AUTHENTIC_LATER621") {
				t.Fatalf("real correction failed under independent tools: %v\n%s", err, result.Summary())
			}
			if len(r.opens) != 4 {
				t.Fatalf("two selectors on base and candidate require four disposable invocations, got %d", len(r.opens))
			}
			seen := map[string]bool{}
			for _, q := range r.opens {
				if seen[q.Token] || seen[q.SourceDir] {
					t.Fatal("real invocations reused container or staged inputs")
				}
				seen[q.Token], seen[q.SourceDir] = true, true
			}
		})
	}
}
