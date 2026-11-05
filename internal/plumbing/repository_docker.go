package plumbing

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
)

// OpenChecks gives a part one writable container for all its commands. The
// host owns sequencing and exit status. Process helpers are staged lazily,
// outside the candidate tree. The caller owns Close once opening succeeds.
func (r *DockerDependencyRunner) OpenChecks(ctx context.Context, q DependencyRequest) (_ RepositoryCheckEnvironment, openErr error) {
	if err := validateDependencyContainer(q, false); err != nil {
		return nil, err
	}
	if len(q.Argv) != 0 {
		return nil, errors.New("open repository checks without a command")
	}
	stage, err := stageDependencySource(ctx, q, false)
	if err != nil {
		return nil, err
	}
	e := &dockerRepositoryChecks{runner: r, request: cloneDependencyRequest(q), stage: stage}
	defer func() {
		if openErr != nil {
			cleanup, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
			defer done()
			openErr = errors.Join(openErr, e.Close(cleanup))
		}
	}()
	// A fixed idle process keeps the container alive across docker exec calls.
	// Repository arguments never enter this shell script.
	if err := createDependencyContainer(ctx, q, "exec sleep 86400\n"); err != nil {
		return nil, err
	}
	// Root-owned, as installation's copy is: the container's root, with every
	// capability dropped, can't write in what docker cp gives the host's uid
	// (#485).
	if err := copyDependencyStage(ctx, stage, q.Token); err != nil {
		return nil, err
	}
	if _, err := dependencyDocker(ctx, "start", q.Token); err != nil {
		return nil, err
	}
	if q.Env["GOTOOLCHAIN"] != "local" {
		return dockerRepositoryCommands{e}, nil
	}
	return e, nil
}

// Non-Go environments retain only their existing command and file capability.
type dockerRepositoryCommands struct {
	RepositoryAcceptanceFileEnvironment
}

type dockerRepositoryChecks struct {
	mu          sync.Mutex
	prepareMu   sync.Mutex
	runner      *DockerDependencyRunner
	request     DependencyRequest
	stage       string
	closed      bool
	err         error
	helpers     string
	helper      string
	staged      map[string]bool
	used        map[string]bool
	prepared    []*dockerPreparedProcess
	clients     []*dockerCheckClient
	cleanupOnce sync.Once
	cleanup     context.Context
	cleanupDone context.CancelFunc
	removeOnce  sync.Once
	removeErr   error
	closeOnce   sync.Once
}

func (e *dockerRepositoryChecks) Run(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" || strings.HasPrefix(argv[0], "-") {
		return nil, errors.New("repository check needs an executable")
	}
	for _, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("repository check argument contains NUL")
		}
	}
	args := e.execArgs("", nil)
	// The fixed wrapper also keeps an executable containing '=' from being
	// interpreted as an env assignment. Only positional arguments reach exec.
	args = append(args, "/bin/sh", "-c", "exec \"$@\"", "invariant-check")
	args = append(args, argv...)
	var output dockerCheckBuffer
	client, err := e.startClient(ctx, args, &output, &output, false)
	if err != nil {
		return nil, err
	}
	err = client.wait(ctx)
	out := output.bytes()
	if err != nil {
		err = fmt.Errorf("docker exec: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, err
}

func (e *dockerRepositoryChecks) execArgs(directory string, extra map[string]string) []string {
	q := e.request
	args := []string{"exec", "--workdir", path.Join(dependencyWork, q.Directory, directory), q.Token, "/usr/bin/env", "-i"}
	env := dependencyContainerEnv(q)
	env["PWD"] = path.Join(dependencyWork, q.Directory, directory)
	maps.Copy(env, extra)
	for _, name := range slices.Sorted(maps.Keys(env)) {
		args = append(args, name+"="+env[name])
	}
	return args
}

// The guard receives paths only as positional arguments. Docker cp without -L
// retains a leaf link as a link; its archive is checked independently below.
const repositorySourceGuard = `set -eu
file=$1
parent=${file%/*}
while [ "$parent" != / ]; do
  [ ! -L "$parent" ] && [ -d "$parent" ] || exit 1
  parent=${parent%/*}
  [ -n "$parent" ] || parent=/
done
[ ! -L "$file" ] && [ -f "$file" ] || exit 1
[ "$(stat -c %h -- "$file")" = 1 ]
`

func (e *dockerRepositoryChecks) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return nil, errors.New("repository check environment is closed")
	}
	if !repositoryAcceptancePath(name) {
		return nil, fmt.Errorf("acceptance source must be a clean part-relative path: %q", name)
	}
	file := path.Join(dependencyWork, e.request.Directory, name)
	guard := []string{"/bin/sh", "-c", repositorySourceGuard, "invariant-source", file}
	if _, err := e.Run(ctx, guard); err != nil {
		return nil, fmt.Errorf("acceptance source has unsafe ancestors or is not regular: %w", err)
	}
	out, err := dependencyDocker(ctx, "cp", e.request.Token+":"+file, "-")
	if err != nil {
		return nil, err
	}
	archive := tar.NewReader(bytes.NewReader(out))
	header, err := archive.Next()
	if err != nil {
		return nil, fmt.Errorf("read acceptance source archive: %w", err)
	}
	if header.Name != path.Base(name) || header.Typeflag != tar.TypeReg || header.Linkname != "" {
		return nil, errors.New("acceptance source archive must contain the requested regular file")
	}
	data, err := io.ReadAll(archive)
	if err != nil {
		return nil, err
	}
	if _, err := archive.Next(); err != io.EOF {
		return nil, errors.New("acceptance source archive contains extra or malformed entries")
	}
	if _, err := e.Run(ctx, guard); err != nil {
		return nil, fmt.Errorf("acceptance source changed while reading: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func (e *dockerRepositoryChecks) Close(ctx context.Context) error {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closed = true
		clients := slices.Clone(e.clients)
		prepared := slices.Clone(e.prepared)
		e.mu.Unlock()
		cleanup := e.cleanupContext(ctx)
		defer e.cleanupDone()
		var err error
		// A live helper first stops and joins its workload, preserving the
		// channel prefix. Its client also removes the container on interruption.
		for _, p := range prepared {
			err = errors.Join(err, p.Close(cleanup))
		}
		err = errors.Join(err, e.removeContainer(cleanup))
		for _, client := range clients {
			// Ordinary command exits have already been returned by Run. Here
			// only failure to join the owned client is a cleanup error.
			err = errors.Join(err, client.join(cleanup))
		}
		e.err = errors.Join(err, removeRepositoryStage(cleanup, e.stage))
		if e.err != nil {
			e.err = fmt.Errorf("remove repository check environment: %w", e.err)
		}
	})
	return e.err
}
