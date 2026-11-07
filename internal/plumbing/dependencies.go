package plumbing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/repository"
)

// DependencyPreparer prepares only named parts of a captured repository
// contract. It is independent of the factory's built-in Go sandbox.
type DependencyPreparer struct {
	CacheDir string
	Platform string
	Timeout  time.Duration
	Runner   DependencyRunner
}

// DependencyRunner is the container boundary. Install must check its private
// copy of the inputs before sealing it; Inspect independently reads immutable
// image evidence. Cleanup removes only resources private to Token.
type DependencyRunner interface {
	Install(context.Context, DependencyRequest) (DependencyArtifact, error)
	Inspect(context.Context, string, string) (DependencyArtifact, error)
	Run(context.Context, DependencyRequest) ([]byte, error)
	Cleanup(context.Context, string) error
}

type DependencyRequest struct {
	Token       string
	Image       string
	Platform    string
	Directory   string
	SourceDir   string
	CacheDir    string
	Key         string
	Argv        []string
	Env         map[string]string
	Inputs      map[string]string
	Network     bool
	Pull        bool
	Init        bool
	MemoryBytes int64
	PidsLimit   int
}

// DependencyArtifact is evidence, not execution authority. Sessions return
// copies of it and retain their own verified immutable image IDs.
type DependencyArtifact struct {
	Image     string
	Base      string
	BaseImage string
	Platform  string
	Key       string
	Inputs    map[string]string
	Complete  bool
}

type preparedDependency struct {
	part     repository.Part
	request  DependencyRequest
	artifact DependencyArtifact
}

type DependencySession struct {
	parent   context.Context
	deadline time.Time
	runner   DependencyRunner
	parts    map[string]preparedDependency
}

const dependencyRecipe = "repository-dependencies-v3"

var dependencyID = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var dependencyPlatform = regexp.MustCompile(`^linux/(amd64|arm64)(/v[0-9]+)?$`)

// Prepare fixes the allowance at entry. Admission and input reads for every
// selection finish before any installation starts. Concurrent callers install
// privately and atomically publish only complete, independently checked records.
func (p DependencyPreparer) Prepare(parent context.Context, snapshot *repository.Snapshot, names []string) (*DependencySession, error) {
	return p.prepareInputs(parent, snapshot, names, snapshot.ReadFileContext)
}

// Ordinary CI supplies committed candidate bytes from its private staging
// tree. The captured snapshot still owns every declaration and command.
func (p DependencyPreparer) prepareInputs(parent context.Context, snapshot *repository.Snapshot, names []string, read func(context.Context, string) ([]byte, error)) (*DependencySession, error) {
	ctx, cancel := context.WithTimeout(parent, sandboxLimit(p.Timeout, SandboxTimeout))
	defer cancel()
	deadline, _ := ctx.Deadline()
	runner := p.Runner
	if runner == nil {
		runner = &DockerDependencyRunner{}
	}
	s := &DependencySession{parent: parent, deadline: deadline, runner: runner, parts: map[string]preparedDependency{}}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return s, nil
	}
	if snapshot == nil || !dependencyPlatform.MatchString(p.Platform) || p.CacheDir == "" {
		return nil, errors.New("dependency preparation needs a snapshot, cache directory and explicit Linux platform")
	}
	available := map[string]repository.Part{}
	for _, part := range snapshot.Config().Parts {
		available[part.Name] = part
	}
	selected := []repository.Part{}
	seen := map[string]bool{}
	for _, name := range names {
		part, ok := available[name]
		if !ok {
			return nil, fmt.Errorf("unknown repository part %q", name)
		}
		if !seen[name] {
			selected = append(selected, part)
			seen[name] = true
		}
	}
	requests := make([]DependencyRequest, len(selected))
	contents := make([]map[string][]byte, len(selected))
	for i, part := range selected {
		files := map[string][]byte{}
		for _, name := range append(slices.Clone(part.Inputs), part.Lockfiles...) {
			data, err := read(ctx, name)
			if err != nil {
				return nil, fmt.Errorf("dependency input %s: %w", name, err)
			}
			files[name] = data
		}
		argv, err := dependencyInstall(part, files)
		if err != nil {
			return nil, fmt.Errorf("part %q: %w", part.Name, err)
		}
		q := DependencyRequest{
			Image:       part.Image,
			Platform:    p.Platform,
			Directory:   part.Directory,
			Argv:        argv,
			Env:         dependencyEnv(part.Language, true),
			Inputs:      map[string]string{},
			Network:     len(argv) != 0,
			Pull:        true,
			Init:        true,
			MemoryBytes: 4 << 30,
			PidsLimit:   1024,
		}
		for name, data := range files {
			q.Inputs[name] = dependencyHash(data)
		}
		// Roles and original arguments remain in the identity even when the
		// effective command adds mandatory policy flags. Names/commits do not.
		locks, inputs := slices.Clone(part.Lockfiles), slices.Clone(part.Inputs)
		slices.Sort(locks)
		slices.Sort(inputs)
		recipe := struct {
			Version, Language string
			Locks, Inputs     []string
			Install           repository.Command
			Request           DependencyRequest
		}{dependencyRecipe, part.Language, locks, inputs, part.Commands["install"], q}
		data, err := json.Marshal(recipe)
		if err != nil {
			return nil, err
		}
		q.Key = dependencyHash(data)
		requests[i], contents[i] = q, files
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.CacheDir, 0o700); err != nil {
		return nil, err
	}
	for i, part := range selected {
		q := requests[i]
		a, err := p.prepare(ctx, runner, q, contents[i])
		if err != nil {
			return nil, fmt.Errorf("prepare part %q: %w", part.Name, err)
		}
		s.parts[part.Name] = preparedDependency{part: part, request: q, artifact: cloneDependencyArtifact(a)}
	}
	return s, ctx.Err()
}

func (p DependencyPreparer) prepare(ctx context.Context, runner DependencyRunner, q DependencyRequest, files map[string][]byte) (artifact DependencyArtifact, resultErr error) {
	if err := ctx.Err(); err != nil {
		return artifact, err
	}
	name := strings.TrimPrefix(q.Key, "sha256:") + ".json"
	data, err := regular.ReadFile(p.CacheDir, name)
	if err == nil {
		if err := json.Unmarshal(data, &artifact); err != nil {
			return artifact, fmt.Errorf("invalid dependency cache record: %w", err)
		}
		return inspectDependency(ctx, runner, q, artifact)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return artifact, err
	}
	stage, err := os.MkdirTemp("", "invariant-dependency-inputs-")
	if err != nil {
		return artifact, err
	}
	defer func() { resultErr = errors.Join(resultErr, removeRepositoryStage(context.Background(), stage)) }()
	q.SourceDir = stage
	q.Token, err = dependencyToken()
	if err != nil {
		return artifact, err
	}
	defer func() { resultErr = errors.Join(resultErr, cleanupDependency(runner, q.Token)) }()
	for name, data := range files {
		if err := ctx.Err(); err != nil {
			return artifact, err
		}
		to := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return artifact, err
		}
		if err := os.WriteFile(to, data, 0o600); err != nil {
			return artifact, err
		}
	}
	if err := ctx.Err(); err != nil {
		return artifact, err
	}
	artifact, err = runner.Install(ctx, cloneDependencyRequest(q))
	if err != nil {
		return artifact, err
	}
	if err := verifyDependencyInputs(ctx, stage, q.Inputs); err != nil {
		return artifact, err
	}
	artifact, err = inspectDependency(ctx, runner, q, artifact)
	if err != nil {
		return artifact, err
	}
	data, err = json.Marshal(artifact)
	if err != nil {
		return artifact, err
	}
	// Rename never exposes a partial record. Two successful private creators
	// may replace the record with either independently verified artifact.
	f, err := os.CreateTemp(p.CacheDir, ".dependency-")
	if err != nil {
		return artifact, err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr, ctx.Err()); err != nil {
		return artifact, err
	}
	if err := os.Rename(f.Name(), filepath.Join(p.CacheDir, name)); err != nil {
		return artifact, err
	}
	return artifact, nil
}

func inspectDependency(ctx context.Context, runner DependencyRunner, q DependencyRequest, a DependencyArtifact) (DependencyArtifact, error) {
	if err := ctx.Err(); err != nil {
		return DependencyArtifact{}, err
	}
	if !a.Complete || !dependencyID.MatchString(a.Image) || !dependencyID.MatchString(a.BaseImage) || a.Base != q.Image || a.Platform != q.Platform || a.Key != q.Key || !maps.Equal(a.Inputs, q.Inputs) {
		return DependencyArtifact{}, errors.New("incomplete or mismatched dependency artifact")
	}
	inspected, err := runner.Inspect(ctx, a.Image, q.Platform)
	if err != nil {
		return DependencyArtifact{}, err
	}
	if !reflect.DeepEqual(inspected, a) {
		return DependencyArtifact{}, errors.New("dependency image inspection disagrees with installation evidence")
	}
	return cloneDependencyArtifact(inspected), ctx.Err()
}

func verifyDependencyInputs(ctx context.Context, dir string, inputs map[string]string) error {
	for name, hash := range inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := regular.ReadFile(dir, name)
		if err != nil {
			return fmt.Errorf("installed input %s: %w", name, err)
		}
		if dependencyHash(data) != hash {
			return fmt.Errorf("installation changed dependency input %s", name)
		}
	}
	return ctx.Err()
}

func (s *DependencySession) Artifacts() map[string]DependencyArtifact {
	out := map[string]DependencyArtifact{}
	for name, p := range s.parts {
		out[name] = cloneDependencyArtifact(p.artifact)
	}
	return out
}

// Run checks a candidate offline with private writable storage. Both the
// preparation parent and this call can cancel it; neither can renew its time.
func (s *DependencySession) Run(caller context.Context, partName, candidateRoot string, argv []string) (output []byte, resultErr error) {
	p, ok := s.parts[partName]
	if !ok {
		return nil, fmt.Errorf("repository part %q was not prepared", partName)
	}
	ctx, cancel := context.WithDeadline(s.parent, s.deadline)
	defer cancel()
	if end, ok := caller.Deadline(); ok && end.Before(s.deadline) {
		var finish context.CancelFunc
		ctx, finish = context.WithDeadline(ctx, end)
		defer finish()
	}
	stop := context.AfterFunc(caller, cancel)
	defer stop()
	if err := errors.Join(ctx.Err(), caller.Err()); err != nil {
		return nil, err
	}
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("dependency check needs an executable")
	}
	q := cloneDependencyRequest(p.request)
	q.Image, q.SourceDir = p.artifact.Image, candidateRoot
	q.Network, q.Pull = false, false
	q.Argv, q.Env = slices.Clone(argv), dependencyEnv(p.part.Language, false)
	var err error
	q.Token, err = dependencyToken()
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, cleanupDependency(s.runner, q.Token)) }()
	output, err = s.runner.Run(ctx, q)
	return output, errors.Join(err, ctx.Err())
}

func cleanupDependency(runner DependencyRunner, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
	defer cancel()
	return runner.Cleanup(ctx, token)
}

func dependencyToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "invariant-deps-" + hex.EncodeToString(b[:]), nil
}

func dependencyHash(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

func cloneDependencyArtifact(a DependencyArtifact) DependencyArtifact {
	a.Inputs = maps.Clone(a.Inputs)
	return a
}

func cloneDependencyRequest(q DependencyRequest) DependencyRequest {
	q.Argv, q.Env, q.Inputs = slices.Clone(q.Argv), maps.Clone(q.Env), maps.Clone(q.Inputs)
	return q
}

func dependencyEnv(language string, network bool) map[string]string {
	env := map[string]string{"HOME": "/tmp", "PATH": "/invariant/venv/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"}
	switch language {
	case "go":
		env["GOTOOLCHAIN"], env["GOFLAGS"], env["GOENV"], env["GOWORK"] = "local", "-mod=readonly -p=1", "off", "off"
		env["GOMODCACHE"], env["GOCACHE"], env["GOPATH"] = "/invariant/gomod", "/tmp/gocache", "/tmp/gopath"
		env["GOMAXPROCS"], env["CGO_ENABLED"] = "2", "0"
		env["GOPROXY"], env["GOSUMDB"], env["GOVCS"] = "off", "off", "*:off"
		if network {
			env["GOPROXY"], env["GOSUMDB"] = "https://proxy.golang.org", "sum.golang.org"
		}
	case "python":
		env["PIP_CONFIG_FILE"], env["PYTHONNOUSERSITE"] = "/dev/null", "1"
		env["PIP_DISABLE_PIP_VERSION_CHECK"], env["PIP_NO_INPUT"] = "1", "1"
		if !network {
			env["PIP_NO_INDEX"] = "1"
		}
	case "typescript":
		env["NPM_CONFIG_USERCONFIG"], env["NPM_CONFIG_GLOBALCONFIG"] = "/invariant/npm-user.npmrc", "/invariant/npm-global.npmrc"
		env["NPM_CONFIG_CACHE"] = "/tmp/npm-cache"
		env["NPM_CONFIG_IGNORE_SCRIPTS"], env["NPM_CONFIG_AUDIT"], env["NPM_CONFIG_FUND"] = "true", "false", "false"
		if !network {
			env["NPM_CONFIG_OFFLINE"] = "true"
		}
	}
	return env
}
