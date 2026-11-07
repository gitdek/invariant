package plumbing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

func TestRepositoryPreparationKeepsItsShorterAllowance(t *testing.T) {
	root, candidate, snapshot := fixture484(t, []repository.Part{part484("go", "api")}, nil)
	runner := newRunner484(t)
	sandbox := sandbox484(t, root, snapshot, runner)
	sandbox.Repository.Dependencies.Timeout = 200 * time.Millisecond
	runner.installHook = func(ctx context.Context, _ DependencyRequest) (DependencyArtifact, error) {
		<-ctx.Done()
		return DependencyArtifact{}, ctx.Err()
	}
	result, err := sandbox.Run(context.Background(), candidate, &Plan{Files: []string{"api/main.go"}})
	if !errors.Is(err, context.DeadlineExceeded) || result.Passed() || !result.TimedOut || result.DeadlineExceeded || result.Interrupted {
		t.Fatalf("preparation allowance: %v %+v", err, result)
	}
	if len(runner.opens) != 0 || status484(t, result, "api", "format") != "not_run" {
		t.Fatal("preparation timeout started checks or lost unfinished work")
	}
}

func TestRepositoryLaterStartupConsumesTestAllowance(t *testing.T) {
	root, candidate, snapshot := fixture484(t, []repository.Part{part484("go", "api"), part484("typescript", "web")}, nil)
	runner := newRunner484(t)
	sandbox := sandbox484(t, root, snapshot, runner)
	sandbox.ShardTimeout = 250 * time.Millisecond
	var testEnd time.Time
	runner.hook = func(ctx context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
		if argv[1] == "test" {
			testEnd, _ = ctx.Deadline()
		}
		return nil, nil
	}
	runner.openHook = func(ctx context.Context, q DependencyRequest) error {
		if q.Directory == "web" {
			end, _ := ctx.Deadline()
			if testEnd.IsZero() || !end.Equal(testEnd) {
				t.Error("later startup renewed the shared test allowance")
			}
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	result, err := sandbox.Run(context.Background(), candidate, &Plan{Files: []string{"api/main.go", "web/main.ts"}})
	if !errors.Is(err, context.DeadlineExceeded) || result.Passed() || !result.TimedOut || result.DeadlineExceeded || result.Interrupted {
		t.Fatalf("later startup allowance: %v %+v", err, result)
	}
	if status484(t, result, "api", "test") != "passed" || status484(t, result, "web", "format") != "not_run" || len(runner.closed) != 1 {
		t.Fatal("startup timeout lost completed work or its cleanup")
	}
}

func TestRepositoryCleanupFailureStopsBeforeNextPart(t *testing.T) {
	root, candidate, snapshot := fixture484(t, []repository.Part{part484("go", "api"), part484("typescript", "web")}, nil)
	runner := newRunner484(t)
	runner.closeHook = func(context.Context, DependencyRequest) error { return errors.New("cannot remove container") }
	result, err := sandbox484(t, root, snapshot, runner).Run(context.Background(), candidate, &Plan{Files: []string{"api/main.go", "web/main.ts"}})
	if err == nil || result.Passed() || len(runner.opens) != 1 || status484(t, result, "api", "test") != "passed" || status484(t, result, "web", "format") != "not_run" {
		t.Fatalf("cleanup failure: %v %+v", err, result)
	}
	if !strings.Contains(result.Summary(), "api") || !strings.Contains(result.Summary(), "cleanup") {
		t.Fatal(result.Summary())
	}
}

// Run the actual env/exec wrapper captured at the Docker boundary, with no
// language runtime. A filename containing '=' and shell syntax in arguments
// exercise both env assignment parsing and the shell's positional forwarding.
func TestRepositoryContainerExecPreservesLiteralArguments(t *testing.T) {
	_, logs := docker484(t)
	q := request484(t, "go")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	environment, err := (&DockerDependencyRunner{}).OpenChecks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer close484(t, environment)
	program := filepath.Join(t.TempDir(), "check=literal")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf '%s\\000' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	argv := []string{program, "", "one two", "$(false); `false`", "name=value", "--option", "line\nbreak"}
	if _, err := environment.Run(ctx, argv); err != nil {
		t.Fatal(err)
	}
	for _, args := range invocations484(t, logs) {
		if len(args) == 0 || args[0] != "exec" {
			continue
		}
		start := slices.Index(args, "/usr/bin/env")
		if start < 0 {
			t.Fatal("missing clean environment")
		}
		out, err := exec.CommandContext(ctx, args[start], args[start+1:]...).CombinedOutput()
		if err != nil || string(out) != strings.Join(argv[1:], "\x00")+"\x00" {
			t.Fatalf("literal arguments changed: %v %q", err, out)
		}
		return
	}
	t.Fatal("no container exec was recorded")
}

func TestRepositoryCandidateLinksAreRefused(t *testing.T) {
	for _, link := range []string{"symlink", "hard link"} {
		t.Run(link, func(t *testing.T) {
			root, candidate, snapshot := fixture484(t, []repository.Part{part484("go", "api")}, nil)
			outside := filepath.Join(t.TempDir(), "outside.go")
			if err := os.WriteFile(outside, []byte("package outside\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			to := filepath.Join(candidate, "api", "linked.go")
			var err error
			if link == "symlink" {
				err = os.Symlink(outside, to)
			} else {
				err = os.Link(outside, to)
			}
			if err != nil {
				t.Fatal(err)
			}
			runner := newRunner484(t)
			result, err := sandbox484(t, root, snapshot, runner).Run(context.Background(), candidate, &Plan{Files: []string{"api/main.go"}})
			if err == nil || result.Passed() || len(runner.opens) != 0 || status484(t, result, "api", "test") != "not_run" {
				t.Fatalf("candidate link admitted: %v %+v", err, result)
			}
		})
	}
}

func TestRepositoryTreeRemovalStopsAndDoesNotFollowDirectories(t *testing.T) {
	outside := t.TempDir()
	file := filepath.Join(outside, "keep")
	if err := os.WriteFile(file, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(stage, "linked")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := removeRepositoryTree(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatalf("removal ignored its own deadline: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(stage, "linked")); err != nil {
		t.Fatalf("canceled removal continued: %v", err)
	}
	if err := removeRepositoryStage(ctx, stage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("removal returned with staging still present: %v", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "outside" {
		t.Fatalf("cleanup followed a directory link: %q, %v", data, err)
	}
}
