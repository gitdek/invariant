package plumbing

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The stream assertions always run and fail on the directory-copy adapter,
// even on a host whose uid is already zero. The real half is deliberately
// opt-in, and never substitutes fake installation or skips missing Docker:
//
// INVARIANT_PLUMBING_DEPENDENCIES_REAL=1 go test ./internal/plumbing -run '^TestRepositoryDependencyRootOwnedStagesAndRealPreparation$' -count=1 -timeout 45m
func TestRepositoryDependencyRootOwnedStagesAndRealPreparation(t *testing.T) {
	for _, install := range []bool{true, false} {
		name := "check"
		if install {
			name = "install"
		}
		t.Run(name, func(t *testing.T) { dependency485Stream(t, install) })
	}
	t.Run("refuse_nonregular_sources", dependency485Refusals)
	t.Run("failed_copy", dependency485FailedCopy)
	t.Run("canceled_copy", dependency485CanceledCopy)
	t.Run("real_toolchains", func(t *testing.T) {
		if os.Getenv("INVARIANT_PLUMBING_DEPENDENCIES_REAL") != "1" {
			return
		}
		dependency485Real(t)
	})
}

func dependency485Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(h[:])
}

func dependency485Write(t *testing.T, root, name, body string, mode os.FileMode) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, mode); err != nil {
		t.Fatal(err)
	}
}

func dependency485Fixture(t *testing.T, install bool) DependencyRequest {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"app/go.mod":           "module example.invalid/ownership\n\ngo 1.27.1\n",
		"app/private/data.bin": "private\x00bytes\xff\n",
		"app/check.sh":         "#!/bin/sh\nprintf 'checked\\n'\n",
		"app/plain.sh":         "not executable\n",
		"outside.txt":          "not an installation input\n",
		".git/private":         "never copy Git metadata\n",
		"app/.GiT/private":     "never copy nested Git metadata\n",
	}
	for name, body := range files {
		dependency485Write(t, root, name, body, 0o600)
	}
	if err := os.Chmod(filepath.Join(root, "app/check.sh"), 0o751|os.ModeSetuid|os.ModeSetgid|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "app/plain.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "app/empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	q := DependencyRequest{
		Token: "invariant-deps-485", Image: dependency485Hash("environment"),
		Platform: "linux/amd64", Directory: "app", SourceDir: root,
		Key: dependency485Hash("recipe"), Argv: []string{"./check.sh"},
		Inputs: map[string]string{}, Init: true, MemoryBytes: 4 << 30, PidsLimit: 1024,
	}
	for _, name := range []string{"app/go.mod", "app/private/data.bin"} {
		q.Inputs[name] = dependency485Hash(files[name])
	}
	if install {
		q.Image = "registry.example/toolchain@" + dependency485Hash("base digest")
		q.Argv = []string{"go", "mod", "download"}
		q.Network, q.Pull = true, true
	}
	return q
}

// Replace only the executable, so these assertions observe the production
// adapter's complete stream and launch policy rather than a tar helper's API.
func dependency485Docker(t *testing.T, q DependencyRequest) string {
	t.Helper()
	bin, log := t.TempDir(), t.TempDir()
	inspection, err := json.Marshal([]map[string]any{{
		"Id": dependency485Hash("base image"), "Os": "linux", "Architecture": "amd64",
		"RepoDigests": []string{q.Image}, "Config": map[string]any{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	dependency485Write(t, log, "inspect.json", string(inspection), 0o600)
	dependency485Write(t, bin, "docker", `#!/bin/sh
set -eu
printf '%s\n' "$1" >> "$S485_LOG/calls"
case "$1" in
image) cat "$S485_LOG/inspect.json" ;;
create) printf '%s\000' "$@" > "$S485_LOG/create" ;;
cp)
  if [ "$2" = '-' ]; then
    [ "$#" -eq 3 ] && [ "$3" = 'invariant-deps-485:/' ] || exit 70
    : > "$S485_LOG/copy-started"
    if [ "${S485_COPY_MODE:-}" = cancel ]; then exec sleep 30; fi
    cat > "$S485_LOG/stage.tar"
    if [ "${S485_COPY_MODE:-}" = fail ]; then echo copy-refused >&2; exit 71; fi
  elif [ "${2#invariant-deps-485:/invariant/work/}" != "$2" ]; then
    cp "$S485_SOURCE/${2#invariant-deps-485:/invariant/work/}" "$3"
  else
    echo 'stage must reach docker cp on stdin as a tar stream' >&2
    exit 72
  fi ;;
start) printf 'fixture checked\n' ;;
wait) printf '0\n' ;;
commit) printf '%s\n' "$S485_IMAGE" ;;
rm) : > "$S485_LOG/removed" ;;
*) echo unexpected-docker-command >&2; exit 73 ;;
esac
`, 0o755)
	t.Setenv("S485_LOG", log)
	t.Setenv("S485_SOURCE", q.SourceDir)
	t.Setenv("S485_IMAGE", dependency485Hash("environment"))
	t.Setenv("S485_COPY_MODE", "")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func dependency485Stream(t *testing.T, install bool) {
	t.Helper()
	q := dependency485Fixture(t, install)
	log := dependency485Docker(t, q)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := &DockerDependencyRunner{}
	if install {
		a, err := r.Install(ctx, q)
		if err != nil || !a.Complete || a.Image != dependency485Hash("environment") {
			t.Fatalf("install through a root-owned stream: artifact=%+v error=%v", a, err)
		}
	} else if out, err := r.Run(ctx, q); err != nil || !strings.Contains(string(out), "fixture checked") {
		t.Fatalf("check through a root-owned stream: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(log, "removed")); err != nil {
		t.Fatalf("completed operation did not remove its private container: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(log, "stage.tar"))
	if err != nil {
		t.Fatalf("Docker received no tar stream: %v", err)
	}
	reader := tar.NewReader(bytes.NewReader(b))
	headers, contents := map[string]tar.Header{}, map[string]string{}
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid tar stream: %v", err)
		}
		if h.Uid != 0 || h.Gid != 0 || (h.Uname != "" && h.Uname != "root") || (h.Gname != "" && h.Gname != "root") {
			t.Errorf("%s has non-root ownership: uid=%d gid=%d uname=%q gname=%q", h.Name, h.Uid, h.Gid, h.Uname, h.Gname)
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			t.Fatalf("nonregular tar entry %q has type %d", h.Name, h.Typeflag)
		}
		if h.Linkname != "" || h.Mode&^int64(0o777) != 0 {
			t.Errorf("entry retains a link or special permissions: %+v", *h)
		}
		name := path.Clean(h.Name)
		if path.IsAbs(h.Name) || (name != "." && name != "invariant" && !strings.HasPrefix(name, "invariant/")) {
			t.Fatalf("entry escapes the container stage: %q", h.Name)
		}
		if _, exists := headers[name]; exists {
			t.Fatalf("duplicate archive entry %q", name)
		}
		headers[name] = *h
		if h.Typeflag == tar.TypeReg {
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			contents[name] = string(body)
		}
	}
	want := map[string]string{
		"invariant/npm-user.npmrc":            "",
		"invariant/npm-global.npmrc":          "",
		"invariant/work/app/go.mod":           "module example.invalid/ownership\n\ngo 1.27.1\n",
		"invariant/work/app/private/data.bin": "private\x00bytes\xff\n",
	}
	if !install {
		want["invariant/work/app/check.sh"] = "#!/bin/sh\nprintf 'checked\\n'\n"
		want["invariant/work/app/plain.sh"] = "not executable\n"
		want["invariant/work/outside.txt"] = "not an installation input\n"
	}
	if !reflect.DeepEqual(contents, want) {
		t.Errorf("stream contents = %#v, want %#v", contents, want)
	}
	for _, name := range []string{"invariant", "invariant/work", "invariant/work/app", "invariant/work/app/private"} {
		if h, ok := headers[name]; !ok || h.Typeflag != tar.TypeDir {
			t.Errorf("missing parent directory %q", name)
		}
	}
	if !install {
		if h, ok := headers["invariant/work/app/empty"]; !ok || h.Typeflag != tar.TypeDir {
			t.Error("empty candidate directory was lost")
		}
	}
	for name := range headers {
		for _, component := range strings.Split(name, "/") {
			if strings.EqualFold(component, ".git") {
				t.Errorf("stream includes Git metadata: %s", name)
			}
		}
	}
	for name := range want {
		mode := int64(0o600)
		if strings.HasSuffix(name, "/check.sh") {
			mode = 0o751
		} else if strings.HasSuffix(name, "/plain.sh") {
			mode = 0o644
		}
		if h := headers[name]; h.Mode != mode {
			t.Errorf("%s mode = %#o, want %#o", name, h.Mode, mode)
		}
	}
	launch, err := os.ReadFile(filepath.Join(log, "create"))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(launch), "\x00"), "\x00")
	network := "none"
	if install {
		network = "bridge"
	}
	for _, option := range [][2]string{{"--user", "0:0"}, {"--cap-drop", "ALL"}, {"--security-opt", "no-new-privileges"}, {"--network", network}, {"--pull", "never"}} {
		i := slices.Index(args, option[0])
		if i < 0 || i+1 >= len(args) || args[i+1] != option[1] {
			t.Errorf("container lost %s %s: %q", option[0], option[1], args)
		}
	}
	if slices.Contains(args, "--cap-add") || slices.Contains(args, "--privileged") {
		t.Errorf("ownership was repaired by adding container privileges: %q", args)
	}
}

func dependency485Refusals(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			q := dependency485Fixture(t, false)
			log := dependency485Docker(t, q)
			bad := filepath.Join(q.SourceDir, "app/nonregular")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink("go.mod", bad)
			case "hardlink":
				err = os.Link(filepath.Join(q.SourceDir, "app/go.mod"), bad)
			case "fifo":
				err = syscall.Mkfifo(bad, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := (&DockerDependencyRunner{}).Run(ctx, q); err == nil || ctx.Err() != nil {
				t.Fatalf("%s was not promptly refused: %v", kind, err)
			}
			if _, err := os.Stat(filepath.Join(log, "stage.tar")); !os.IsNotExist(err) {
				t.Errorf("a refused source reached Docker: %v", err)
			}
		})
	}
}

func dependency485FailedCopy(t *testing.T) {
	for _, install := range []bool{true, false} {
		t.Run(fmt.Sprintf("install_%t", install), func(t *testing.T) {
			q := dependency485Fixture(t, install)
			log := dependency485Docker(t, q)
			t.Setenv("S485_COPY_MODE", "fail")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var err error
			if install {
				_, err = (&DockerDependencyRunner{}).Install(ctx, q)
			} else {
				_, err = (&DockerDependencyRunner{}).Run(ctx, q)
			}
			if err == nil || !strings.Contains(err.Error(), "copy-refused") {
				t.Fatalf("copy error was lost: %v", err)
			}
			dependency485Stopped(t, log)
		})
	}
}

func dependency485CanceledCopy(t *testing.T) {
	q := dependency485Fixture(t, false)
	dependency485Write(t, q.SourceDir, "app/large", strings.Repeat("x", 2<<20), 0o600)
	log := dependency485Docker(t, q)
	t.Setenv("S485_COPY_MODE", "cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := (&DockerDependencyRunner{}).Run(ctx, q)
		done <- err
	}()
	for {
		if _, err := os.Stat(filepath.Join(log, "copy-started")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("copy never started: %v", err)
		case <-ctx.Done():
			t.Fatal("copy did not start within its allowance")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("copy did not preserve cancellation: %v", err)
		}
	case <-time.After(SandboxCleanupAllowance + 3*time.Second):
		t.Fatal("canceled copy did not stop within its cleanup allowance")
	}
	dependency485Stopped(t, log)
}

func dependency485Stopped(t *testing.T, log string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(log, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Fields(string(data))
	if slices.Contains(calls, "start") || slices.Contains(calls, "commit") || !slices.Contains(calls, "rm") {
		t.Errorf("failed copy must remove its container without starting or publishing it: %v", calls)
	}
}

// This observer delegates every successful operation to real Docker. Reuse
// turns installation into an explicit failure, so an unchanged image ID alone
// cannot conceal a redundant install.
type dependency485Observer struct {
	DockerDependencyRunner
	installs, runs int
	reuse          bool
	created        []string
}

func (r *dependency485Observer) Install(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
	r.installs++
	if r.reuse {
		return DependencyArtifact{}, errors.New("cache reuse attempted another installation")
	}
	if !q.Network || !q.Pull || !strings.Contains(q.Image, "@sha256:") {
		return DependencyArtifact{}, errors.New("cold preparation did not request its pinned image and network")
	}
	a, err := r.DockerDependencyRunner.Install(ctx, q)
	if a.Image != "" {
		r.created = append(r.created, a.Image)
	}
	return a, err
}

func (r *dependency485Observer) Run(ctx context.Context, q DependencyRequest) ([]byte, error) {
	r.runs++
	if q.Network || q.Pull || !strings.HasPrefix(q.Image, "sha256:") {
		return nil, errors.New("check did not use its offline immutable environment")
	}
	return r.DockerDependencyRunner.Run(ctx, q)
}

func dependency485Real(t *testing.T) {
	platform := "linux/" + runtime.GOARCH
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Fatalf("real dependency test needs an amd64 or arm64 Docker host, got %s", runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput(); err != nil {
		t.Fatalf("real dependency test was enabled but Docker is unavailable: %v\n%s", err, out)
	}
	t.Logf("real preparation from host uid=%d gid=%d, platform=%s", os.Getuid(), os.Getgid(), platform)
	for _, language := range []string{"go", "python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			part, files, marker := dependency485RealFixture(t, ctx, language)
			root, snapshot := snapshot468(t, []repository.Part{part}, files)
			observer := &dependency485Observer{}
			// Only images this cold, private test creates are removed. Pinned
			// base images and anybody else's caches are left alone.
			t.Cleanup(func() {
				for _, id := range observer.created {
					cleanup, finish := context.WithTimeout(context.Background(), 30*time.Second)
					out, err := exec.CommandContext(cleanup, "docker", "image", "rm", id).CombinedOutput()
					finish()
					if err != nil {
						t.Errorf("remove test environment %s: %v\n%s", id, err, out)
					}
				}
			})
			preparer := DependencyPreparer{CacheDir: t.TempDir(), Platform: platform, Runner: observer}
			first, err := preparer.Prepare(ctx, snapshot, []string{part.Name})
			if err != nil {
				t.Fatalf("cold %s preparation: %v", language, err)
			}
			artifact := first.Artifacts()[part.Name]
			if !artifact.Complete || artifact.Base != part.Image || artifact.Platform != platform || observer.installs != 1 {
				t.Fatalf("cold preparation did not install exactly once with the pinned image: %+v installs=%d", artifact, observer.installs)
			}
			check := func(s *DependencySession) {
				t.Helper()
				out, err := s.Run(ctx, part.Name, root, part.Commands["test"].Argv)
				if err != nil || !strings.Contains(string(out), marker) {
					t.Fatalf("offline %s test did not exercise its dependency: %v\n%s", language, err, out)
				}
				if _, err := os.Stat(filepath.Join(root, part.Directory, "checked.txt")); !os.IsNotExist(err) {
					t.Fatalf("container wrote into the original checkout: %v", err)
				}
			}
			check(first)
			observer.reuse = true
			second, err := preparer.Prepare(ctx, snapshot, []string{part.Name})
			if err != nil {
				t.Fatalf("reuse %s without installing: %v", language, err)
			}
			if got := second.Artifacts()[part.Name]; !reflect.DeepEqual(got, artifact) || observer.installs != 1 {
				t.Fatalf("reuse changed its environment or reinstalled: first=%+v second=%+v installs=%d", artifact, got, observer.installs)
			}
			check(second)
			if observer.runs != 2 {
				t.Fatalf("got %d offline tests, want one before and one after reuse", observer.runs)
			}
		})
	}
}

func dependency485RealFixture(t *testing.T, ctx context.Context, language string) (repository.Part, map[string]string, string) {
	t.Helper()
	part := part468(language, "app")
	files := map[string]string{}
	marker := "dependency-real-" + language
	switch language {
	case "go":
		part.Image = toolchain.GoImage
		part.Commands["test"] = repository.Command{Argv: []string{"go", "test", "-v", "-count=1", "./..."}}
		files["app/go.mod"] = "module example.invalid/dependency-real\n\ngo 1.27.1\n\nrequire github.com/google/uuid v1.6.0\n"
		files["app/go.sum"] = "github.com/google/uuid v1.6.0 h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=\ngithub.com/google/uuid v1.6.0/go.mod h1:TIyPZe4MgqvfeYDBFedMoGGpEw/LqOeaOT+nhxU+yHo=\n"
		files["app/dependency_test.go"] = `package fixture
import (
  "os"
  "syscall"
  "testing"
  "github.com/google/uuid"
)
func TestDependency(t *testing.T) {
  const id = "550e8400-e29b-41d4-a716-446655440000"
  if uuid.MustParse(id).String() != id { t.Fatal("uuid dependency did not run") }
  if os.Geteuid() != 0 { t.Fatal("container is not root") }
  for _, name := range []string{"/invariant", "/invariant/work/app", "go.mod"} {
    info, err := os.Stat(name); if err != nil { t.Fatal(err) }
    owner := info.Sys().(*syscall.Stat_t)
    if owner.Uid != 0 || owner.Gid != 0 { t.Fatalf("%s is not root owned", name) }
  }
  interfaces, err := os.ReadDir("/sys/class/net"); if err != nil { t.Fatal(err) }
  if len(interfaces) != 1 || interfaces[0].Name() != "lo" { t.Fatal("check is not offline") }
  if err := os.WriteFile("checked.txt", []byte("private write"), 0600); err != nil { t.Fatal(err) }
  t.Log("dependency-real-go")
}
`
	case "python":
		part.Image = toolchain.PythonImage
		part.Commands["test"] = repository.Command{Argv: []string{"python3", "-m", "unittest", "-v", "check_test"}}
		// This wheel hash is also pinned in the repository's Nagini recipe.
		files["app/requirements.lock"] = "packaging==26.3 --hash=sha256:d7193f7c8e4e93f444fde0262bf90af30e16fa0ad0ad44cb553c87339b23cd1c\n"
		files["app/check_test.py"] = `import os
import unittest
from pathlib import Path
from packaging.version import Version

class DependencyTest(unittest.TestCase):
    def test_dependency(self):
        self.assertLess(Version("1.9"), Version("1.10"))
        self.assertEqual(os.geteuid(), 0)
        for name in ("/invariant", "/invariant/work/app", "requirements.lock"):
            stat = os.stat(name)
            self.assertEqual((stat.st_uid, stat.st_gid), (0, 0))
        self.assertEqual(os.listdir("/sys/class/net"), ["lo"])
        Path("checked.txt").write_text("private write")
        print("dependency-real-python")
`
	case "typescript":
		part.Image = toolchain.NodeImage
		part.Commands["test"] = repository.Command{Argv: []string{"node", "--test", "check.test.ts"}}
		files["app/package.json"] = `{"name":"dependency-real","version":"1.0.0","type":"module","dependencies":{"is-number":"7.0.0"}}`
		// Resolve only this fixed version's integrity metadata, before the
		// snapshot is committed. This performs no installation. Both Prepare
		// calls then read exactly the same captured lock bytes.
		integrity := dependency485NpmIntegrity(t, ctx)
		files["app/package-lock.json"] = `{"name":"dependency-real","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"dependency-real","version":"1.0.0","dependencies":{"is-number":"7.0.0"}},"node_modules/is-number":{"version":"7.0.0","resolved":"https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz","integrity":` + dependency485JSONString(t, integrity) + `}}}`
		files["app/check.test.ts"] = `import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, statSync, writeFileSync } from 'node:fs';
import isNumber from 'is-number';
test('installed dependency works offline', () => {
  const value: string = '42';
  assert.equal(isNumber(value), true);
  assert.equal(isNumber('not a number'), false);
  assert.equal(process.getuid(), 0);
  for (const name of ['/invariant', '/invariant/work/app', 'package.json']) {
    const stat = statSync(name);
    assert.deepEqual([stat.uid, stat.gid], [0, 0]);
  }
  assert.deepEqual(readdirSync('/sys/class/net'), ['lo']);
  writeFileSync('checked.txt', 'private write');
  console.log('dependency-real-typescript');
});
`
	}
	return part, files, marker
}

func dependency485JSONString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func dependency485NpmIntegrity(t *testing.T, ctx context.Context) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://registry.npmjs.org/is-number/7.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("fixed npm fixture metadata: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fixed npm fixture metadata: HTTP %d", resp.StatusCode)
	}
	var pkg struct {
		Name, Version string
		Dist          struct{ Tarball, Integrity string }
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "is-number" || pkg.Version != "7.0.0" || pkg.Dist.Tarball != "https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz" || !strings.HasPrefix(pkg.Dist.Integrity, "sha512-") {
		t.Fatalf("registry did not identify the fixed npm fixture: %+v", pkg)
	}
	return pkg.Dist.Integrity
}
