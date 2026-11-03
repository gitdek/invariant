package plumbing

// Contract for #468: dependency preparation is callable independently of the
// factory. It does not enable external plumbing or change the modeled Python
// gate. The following public API is added in internal/plumbing:
//
//   DependencyPreparer{CacheDir string, Platform string, Timeout time.Duration,
//                      Runner DependencyRunner}
//   Prepare(ctx, *repository.Snapshot, []string /* selected part names */)
//       (*DependencySession, error)
//   (*DependencySession).Artifacts() map[string]DependencyArtifact
//   (*DependencySession).Run(ctx, partName, candidateRoot string, argv []string)
//       ([]byte, error)
//
// A nil Runner selects &DockerDependencyRunner{}, the real Docker adapter.
// It accepts validated requests from the preparer/session. The boundary is:
//
//   type DependencyRunner interface {
//     Install(context.Context, DependencyRequest) (DependencyArtifact, error)
//     Inspect(context.Context, image, platform string) (DependencyArtifact, error)
//     Run(context.Context, DependencyRequest) ([]byte, error)
//     Cleanup(context.Context, token string) error
//   }
//   type DependencyRequest struct {
//     Token, Image, Platform, Directory, SourceDir, CacheDir, Key string
//     Argv []string
//     Env map[string]string
//     Inputs map[string]string // repository-relative file -> sha256:<hex>
//     Network, Pull, Init bool
//     MemoryBytes int64
//     PidsLimit int
//   }
//   type DependencyArtifact struct {
//     Image, Base, BaseImage, Platform, Key string
//     Inputs map[string]string
//     Complete bool
//   }
//
// Install requests use the part's digest-pinned base; artifacts carry the
// immutable environment ID and resolved base ID, never just a cache tag.
// Inspect is offline, reads evidence from that immutable image, and must check
// its identity/platform rather than echo caller claims. Install returns only
// after the locked install succeeds and staged inputs remain unchanged.
// Cleanup removes private containers/resources identified by Token, including
// on error/cancellation, without deleting a published dependency artifact.
// The Docker adapter copies staged files into private container storage,
// overrides image entrypoints, enforces resource/network/env policy, records
// complete evidence outside repository control and commits immutable output.
// Candidate runs use disposable container storage/private copies of that
// output, never a writable shared dependency or compilation cache.
//
// Supported forms are Go go.mod/go.sum with go mod download; Python pip -r
// requirements with exact versions and SHA-256 hashes (all transitive deps
// included); and npm ci with package.json/package-lock.json v2/v3. Required
// safe flags/env are enforced, including local Go toolchain/readonly modules,
// pip hash checking and binary-only packages, and npm's disabled lifecycle
// scripts, audit and funding. Refuse unrecognized commands, floating Python,
// missing/inconsistent locks, unsupported managers, workspaces, local/VCS
// dependencies and undeclared recursive inputs explicitly. Dependency-free
// forms work, including install.not_applicable with empty dependency arrays.
// The loader's acceptance of inert declarations is unchanged.
//
// Cache identity covers every declared lock/input's path, role and bytes,
// language, directory, exact declared install argv/reason, pinned image,
// explicit platform, effective install policy and a versioned adapter recipe.
// Identical dependencies in another commit/checkout may reuse it. Completion
// is published atomically only after successful install and independent image
// inspection. Each reuse revalidates immutable identity and all evidence;
// missing/corrupt/stale evidence is never a hit. Concurrent private installs
// may duplicate work but cannot consume one another's unfinished artifacts.
//
// Prepare resolves names/bytes through the captured snapshot, not mutable
// Parts or candidate files. It stages only declared regular input files, never
// credentials, .git or unrelated host paths. Empty selection does no work.
// repository.Snapshot gains ReadFileContext(ctx, path) ([]byte, error), keeping
// ReadFile compatible, so input reads obey preparation's original context.
// Preparation and every session Run share the deadline fixed at Prepare entry
// (at most SandboxTimeout, or a shorter parent/Timeout); cold installs, parts,
// cache waits and later checks never renew it. Parent cancellation propagates.
// Cleanup uses at most SandboxCleanupAllowance, even after cancellation.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

const image468 = "registry.example/toolchain@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const baseID468 = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func hash468(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(h[:])
}

func write468(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git468(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = root
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func command468(argv ...string) repository.Command { return repository.Command{Argv: argv} }

func part468(language, dir string) repository.Part {
	prefix := ""
	if dir != "." {
		prefix = dir + "/"
	}
	p := repository.Part{Name: language, Directory: dir, Language: language, Image: image468, Lockfiles: []string{}, Inputs: []string{}, Commands: map[string]repository.Command{}}
	for _, name := range []string{"install", "format", "lint", "build", "test", "test_one"} {
		p.Commands[name] = repository.Command{NotApplicable: "Not needed by this fixture."}
	}
	switch language {
	case "go":
		p.Inputs, p.Lockfiles = []string{prefix + "go.mod"}, []string{prefix + "go.sum"}
		p.Commands["install"] = command468("go", "mod", "download")
	case "python":
		p.Lockfiles = []string{prefix + "requirements.lock"}
		p.Commands["install"] = command468("python3", "-m", "pip", "install", "--require-hashes", "--only-binary=:all:", "-r", "requirements.lock")
	case "typescript":
		p.Inputs, p.Lockfiles = []string{prefix + "package.json"}, []string{prefix + "package-lock.json"}
		p.Commands["install"] = command468("npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund")
	}
	return p
}

func files468(language, dir string) map[string]string {
	prefix := ""
	if dir != "." {
		prefix = dir + "/"
	}
	var files map[string]string
	switch language {
	case "go":
		files = map[string]string{
			"go.mod": "module example.invalid/app\n\ngo 1.27.1\n\nrequire example.invalid/dep v1.2.3\n",
			"go.sum": "example.invalid/dep v1.2.3 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nexample.invalid/dep v1.2.3/go.mod h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA=\n",
		}
	case "python":
		files = map[string]string{"requirements.lock": "packaging==24.2 --hash=sha256:" + strings.Repeat("c", 64) + "\n"}
	case "typescript":
		files = map[string]string{
			"package.json":      `{"name":"fixture","version":"1.0.0","dependencies":{"is-number":"7.0.0"}}`,
			"package-lock.json": `{"name":"fixture","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"fixture","version":"1.0.0","dependencies":{"is-number":"7.0.0"}},"node_modules/is-number":{"version":"7.0.0","resolved":"https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz","integrity":"sha512-` + strings.Repeat("A", 86) + `=="}}}`,
		}
	}
	out := map[string]string{}
	for n, b := range files {
		out[prefix+n] = b
	}
	return out
}

func snapshot468(t *testing.T, parts []repository.Part, files map[string]string) (string, *repository.Snapshot) {
	t.Helper()
	root := t.TempDir()
	git468(t, root, "init", "-q")
	return root, commit468(t, root, parts, files)
}

func commit468(t *testing.T, root string, parts []repository.Part, files map[string]string) *repository.Snapshot {
	t.Helper()
	c := repository.Config{Version: 1, Parts: parts, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, root, repository.Path, string(b))
	for n, b := range files {
		write468(t, root, n, b)
	}
	write468(t, root, "unrelated-private.txt", "never stage me")
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-q", "--allow-empty", "--no-verify", "-m", "fixture")
	s, err := repository.Load(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func tree468(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		if !e.Type().IsRegular() {
			return fmt.Errorf("staged nonregular file %s", p)
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(r)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func hashes468(files map[string]string) map[string]string {
	out := map[string]string{}
	for n, b := range files {
		out[n] = hash468(b)
	}
	return out
}

func cloneArtifact468(a DependencyArtifact) DependencyArtifact {
	b := a
	b.Inputs = map[string]string{}
	for n, h := range a.Inputs {
		b.Inputs[n] = h
	}
	return b
}

// This fake is the container boundary, not an alternate cache implementation.
// It records the actual requests and models immutable images and private
// candidate storage. Tests vary its outcomes; production owns all admission,
// staging, hashing, publication, reuse validation, deadlines and isolation.
type runner468 struct {
	t           *testing.T
	mu          sync.Mutex
	images      map[string]DependencyArtifact
	installs    []DependencyRequest
	runs        []DependencyRequest
	inspections []string
	cleanups    []string
	staged      []map[string]string
	deadlines   []time.Time
	installHook func(context.Context, DependencyRequest) (DependencyArtifact, error)
	runHook     func(context.Context, DependencyRequest) ([]byte, error)
	inspectHook func(DependencyArtifact) (DependencyArtifact, error)
}

func newRunner468(t *testing.T) *runner468 {
	return &runner468{t: t, images: map[string]DependencyArtifact{}}
}

func (r *runner468) Install(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
	files := tree468(r.t, q.SourceDir)
	d, ok := ctx.Deadline()
	if !ok {
		r.t.Error("install has no deadline")
	}
	r.mu.Lock()
	r.installs = append(r.installs, q)
	r.staged = append(r.staged, files)
	r.deadlines = append(r.deadlines, d)
	hook := r.installHook
	r.mu.Unlock()
	if hook != nil {
		return hook(ctx, q)
	}
	return r.finish(q), nil
}

func (r *runner468) finish(q DependencyRequest) DependencyArtifact {
	a := DependencyArtifact{Image: hash468("environment:" + q.Key), Base: q.Image, BaseImage: baseID468, Platform: q.Platform, Key: q.Key, Inputs: map[string]string{}, Complete: true}
	for n, h := range q.Inputs {
		a.Inputs[n] = h
	}
	r.mu.Lock()
	r.images[a.Image] = cloneArtifact468(a)
	r.mu.Unlock()
	return a
}

func (r *runner468) Inspect(ctx context.Context, image, platform string) (DependencyArtifact, error) {
	if err := ctx.Err(); err != nil {
		return DependencyArtifact{}, err
	}
	r.mu.Lock()
	r.inspections = append(r.inspections, image)
	a, ok := r.images[image]
	hook := r.inspectHook
	r.mu.Unlock()
	if !ok {
		return DependencyArtifact{}, fmt.Errorf("image absent: %s", image)
	}
	a = cloneArtifact468(a)
	if hook != nil {
		return hook(a)
	}
	return a, nil
}

func (r *runner468) Run(ctx context.Context, q DependencyRequest) ([]byte, error) {
	d, ok := ctx.Deadline()
	if !ok {
		r.t.Error("offline run has no deadline")
	}
	r.mu.Lock()
	r.runs = append(r.runs, q)
	r.deadlines = append(r.deadlines, d)
	hook := r.runHook
	_, exists := r.images[q.Image]
	r.mu.Unlock()
	if !exists {
		return nil, fmt.Errorf("not an installed immutable image: %s", q.Image)
	}
	if hook != nil {
		return hook(ctx, q)
	}
	return []byte("checked offline\n"), nil
}

func (r *runner468) Cleanup(ctx context.Context, token string) error {
	if ctx.Err() != nil {
		r.t.Error("cleanup inherited an already-cancelled context")
	}
	d, ok := ctx.Deadline()
	if !ok || time.Until(d) > SandboxCleanupAllowance+time.Second {
		r.t.Error("cleanup is not bounded by the cleanup reserve")
	}
	r.mu.Lock()
	r.cleanups = append(r.cleanups, token)
	r.mu.Unlock()
	return nil
}

func prepare468(t *testing.T, p DependencyPreparer, s *repository.Snapshot, names ...string) *DependencySession {
	t.Helper()
	x, err := p.Prepare(context.Background(), s, names)
	if err != nil || x == nil {
		t.Fatalf("prepare %v: %v", names, err)
	}
	return x
}

func secureRequest468(t *testing.T, q DependencyRequest, network bool) {
	t.Helper()
	if q.Network != network || !q.Init || q.MemoryBytes != 4<<30 || q.PidsLimit != 1024 || q.Token == "" {
		t.Errorf("wrong container isolation: %+v", q)
	}
	if !network && q.Pull {
		t.Error("offline consumer may pull an image")
	}
	for _, name := range []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "SSH_AUTH_SOCK", "DOCKER_HOST", "DOCKER_CONFIG"} {
		if q.Env[name] != "" {
			t.Errorf("host authority forwarded: %s", name)
		}
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if q.Env[name] != "" {
			t.Errorf("proxy forwarded: %s", name)
		}
	}
}

func TestRepositoryDependenciesInstallLockedPartsAndRunOffline(t *testing.T) {
	for _, language := range []string{"go", "python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			part := part468(language, "apps/service")
			files := files468(language, part.Directory)
			root, s := snapshot468(t, []repository.Part{part}, files)
			r := newRunner468(t)
			p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}
			x := prepare468(t, p, s, part.Name)
			a := x.Artifacts()[part.Name]
			if !a.Complete || a.Image == "" || a.Image == part.Image || a.Base != part.Image || a.BaseImage != baseID468 || a.Platform != p.Platform || a.Key == "" || !reflect.DeepEqual(a.Inputs, hashes468(files)) {
				t.Fatalf("incomplete evidence: %+v", a)
			}
			if len(r.installs) != 1 || len(r.inspections) == 0 {
				t.Fatalf("want one install and inspected completion: %d, %v", len(r.installs), r.inspections)
			}
			q := r.installs[0]
			secureRequest468(t, q, true)
			if q.Image != part.Image || q.Platform != p.Platform || q.Directory != part.Directory || !reflect.DeepEqual(r.staged[0], files) {
				t.Fatalf("wrong selected inputs/toolchain/cwd: %+v, %v", q, r.staged[0])
			}
			args := strings.Join(q.Argv, " ")
			switch language {
			case "go":
				if !strings.Contains(args, "go mod download") || q.Env["GOTOOLCHAIN"] != "local" || !strings.Contains(q.Env["GOFLAGS"], "-mod=readonly") {
					t.Errorf("Go installation can resolve/change its toolchain or inputs: %+v", q)
				}
			case "python":
				for _, flag := range []string{"--require-hashes", "--only-binary=:all:", "requirements.lock"} {
					if !strings.Contains(args, flag) {
						t.Errorf("pip missing %s: %v", flag, q.Argv)
					}
				}
			case "typescript":
				for _, flag := range []string{"ci", "--ignore-scripts", "--no-audit", "--no-fund"} {
					if !strings.Contains(args, flag) {
						t.Errorf("npm missing %s: %v", flag, q.Argv)
					}
				}
			}
			out, err := x.Run(context.Background(), part.Name, root, []string{"check-fixture", "literal argument; $(not-a-shell)"})
			if err != nil || string(out) != "checked offline\n" {
				t.Fatalf("offline check: %q, %v", out, err)
			}
			if len(r.runs) != 1 {
				t.Fatalf("checks = %d", len(r.runs))
			}
			check := r.runs[0]
			secureRequest468(t, check, false)
			if check.Image != a.Image || check.Platform != p.Platform || check.Directory != part.Directory || !reflect.DeepEqual(check.Argv, []string{"check-fixture", "literal argument; $(not-a-shell)"}) {
				t.Fatalf("check lost immutable environment/argv: %+v", check)
			}
			if len(r.cleanups) < 2 {
				t.Errorf("install and check resources not cleaned: %v", r.cleanups)
			}
		})
	}
}

func TestRepositoryDependenciesReuseCapturedInputsAcrossCommits(t *testing.T) {
	part := part468("go", ".")
	files := files468("go", ".")
	root, s := snapshot468(t, []repository.Part{part}, files)
	r := newRunner468(t)
	p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}
	x := prepare468(t, p, s, part.Name)
	first := x.Artifacts()[part.Name]
	write468(t, root, "go.mod", "module changed.invalid/candidate\n")
	write468(t, root, "go.sum", "candidate lock\n")
	write468(t, root, repository.Path, `{"candidate":"not authority"}`)
	second := prepare468(t, p, s, part.Name).Artifacts()[part.Name]
	if len(r.installs) != 1 || second.Image != first.Image || second.Key != first.Key {
		t.Fatalf("captured base did not reuse: installs=%d first=%+v second=%+v", len(r.installs), first, second)
	}
	// A different commit and checkout with identical dependency inputs reuse.
	_, other := snapshot468(t, []repository.Part{part}, files)
	third := prepare468(t, p, other, part.Name).Artifacts()[part.Name]
	if len(r.installs) != 1 || third.Key != first.Key || len(r.inspections) < 3 {
		t.Fatalf("reuse was not content-based/verified: installs=%d inspections=%v", len(r.installs), r.inspections)
	}
	// Returned evidence cannot redirect a later run to a mutable image.
	copy := x.Artifacts()
	a := copy[part.Name]
	a.Image = "mutable:latest"
	a.Inputs["go.sum"] = "forged"
	copy[part.Name] = a
	if _, err := x.Run(context.Background(), part.Name, root, []string{"go", "version"}); err != nil {
		t.Fatal(err)
	}
	if r.runs[len(r.runs)-1].Image != first.Image {
		t.Fatal("caller replaced prepared image authority")
	}
}

func TestRepositoryDependenciesSelectOnlyNamedParts(t *testing.T) {
	parts := []repository.Part{part468("go", "api"), part468("python", "worker"), part468("typescript", "web")}
	files := map[string]string{}
	for _, p := range parts {
		for n, b := range files468(p.Language, p.Directory) {
			files[n] = b
		}
	}
	_, s := snapshot468(t, parts, files)
	r := newRunner468(t)
	p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}
	x := prepare468(t, p, s, "typescript", "go", "typescript")
	names := []string{}
	for n := range x.Artifacts() {
		names = append(names, n)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"go", "typescript"}) || len(r.installs) != 2 {
		t.Fatalf("selected %v with %d installs", names, len(r.installs))
	}
	for i, q := range r.installs {
		if q.Directory == "worker" {
			t.Fatal("installed an unselected part")
		}
		for n := range r.staged[i] {
			if strings.HasPrefix(n, "worker/") {
				t.Fatal("staged an unselected input")
			}
		}
	}
	empty := prepare468(t, p, s)
	if len(empty.Artifacts()) != 0 || len(r.installs) != 2 {
		t.Fatal("empty selection did work")
	}
	if _, err := p.Prepare(context.Background(), s, []string{"go", "unknown"}); err == nil {
		t.Fatal("unknown part was silently skipped")
	}
	if _, err := x.Run(context.Background(), "python", t.TempDir(), []string{"true"}); err == nil {
		t.Fatal("ran an unprepared part")
	}
}

func TestRepositoryDependencyInputReadsObeyCancellation(t *testing.T) {
	part := part468("go", ".")
	files := files468("go", ".")
	root, s := snapshot468(t, []repository.Part{part}, files)
	write468(t, root, "go.mod", "module candidate.invalid/changed\n")
	b, err := s.ReadFileContext(context.Background(), "go.mod")
	if err != nil || string(b) != files["go.mod"] {
		t.Fatalf("context read lost captured input: %q %v", b, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ReadFileContext(ctx, "go.mod"); err == nil {
		t.Fatal("cancelled input read succeeded")
	}
}
