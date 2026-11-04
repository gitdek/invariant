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
)

// OpenChecks gives a part one writable container for all its commands. The
// host owns sequencing and exit status; no language-specific runner is copied
// into the image. The caller owns Close once opening succeeds.
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
	return e, nil
}

type dockerRepositoryChecks struct {
	runner  *DockerDependencyRunner
	request DependencyRequest
	stage   string
	closed  bool
	err     error
}

func (e *dockerRepositoryChecks) Run(ctx context.Context, argv []string) ([]byte, error) {
	if e.closed {
		return nil, errors.New("repository check environment is closed")
	}
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" || strings.HasPrefix(argv[0], "-") {
		return nil, errors.New("repository check needs an executable")
	}
	for _, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("repository check argument contains NUL")
		}
	}
	q := e.request
	args := []string{"exec", "--workdir", path.Join(dependencyWork, q.Directory), q.Token, "/usr/bin/env", "-i"}
	env := dependencyContainerEnv(q)
	for _, name := range slices.Sorted(maps.Keys(env)) {
		args = append(args, name+"="+env[name])
	}
	// The fixed wrapper also keeps an executable containing '=' from being
	// interpreted as an env assignment. Only positional arguments reach exec.
	args = append(args, "/bin/sh", "-c", "exec \"$@\"", "invariant-check")
	args = append(args, argv...)
	return dependencyDocker(ctx, args...)
}

// ReadFile copies no archive entries to the host filesystem. Check ancestors
// in the container, then require exactly one regular, unlinked archive entry.
// The path is a positional argument, never part of the fixed shell program.
func (e *dockerRepositoryChecks) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.closed {
		return nil, errors.New("repository check environment is closed")
	}
	if !goAcceptancePath(name) {
		return nil, fmt.Errorf("unsafe prepared source path %q", name)
	}
	file := path.Join(dependencyWork, e.request.Directory, name)
	if _, err := e.Run(ctx, []string{"/bin/sh", "-c", repositorySourceCheck, "invariant-source", file}); err != nil {
		return nil, fmt.Errorf("prepared source %s is not a regular file with safe ancestors: %w", name, err)
	}
	out, err := dependencyDocker(ctx, "cp", e.request.Token+":"+file, "-")
	if err != nil {
		return nil, err
	}
	archive := tar.NewReader(bytes.NewReader(out))
	header, err := archive.Next()
	if err != nil {
		return nil, fmt.Errorf("read prepared source archive: %w", err)
	}
	if header.Name != path.Base(name) || header.Typeflag != tar.TypeReg || header.Linkname != "" {
		return nil, errors.New("prepared source archive is not the requested regular file")
	}
	data, err := io.ReadAll(archive)
	if err != nil {
		return nil, err
	}
	if _, err := archive.Next(); err != io.EOF {
		return nil, errors.New("prepared source archive contains extra or malformed entries")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

const repositorySourceCheck = `set -eu
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

func (e *dockerRepositoryChecks) Close(ctx context.Context) error {
	if e.closed {
		return e.err
	}
	e.closed = true
	ctx, done := context.WithTimeout(ctx, SandboxCleanupAllowance)
	defer done()
	err := e.runner.Cleanup(ctx, e.request.Token)
	e.err = errors.Join(err, removeRepositoryStage(ctx, e.stage))
	if e.err != nil {
		e.err = fmt.Errorf("remove repository check environment: %w", e.err)
	}
	return e.err
}
