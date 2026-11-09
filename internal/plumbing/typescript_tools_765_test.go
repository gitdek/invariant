package plumbing

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

// A TypeScript part's declared commands find its prepared node_modules/.bin
// first, as npm run does. Invariant's own helper commands, and every part in
// another language, keep the image's PATH, so a locked package can't stand in
// for node (#765).
func TestDeclaredTypeScriptCommandsFindLockedTools765(t *testing.T) {
	_, logs := docker484(t)
	for _, language := range []string{"go", "python", "typescript"} {
		q := request484(t, language)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		environment, err := (&DockerDependencyRunner{}).OpenChecks(ctx, q)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if _, err := runRepositoryCommand(ctx, environment, []string{"declared-765", language}); err != nil {
			t.Fatal(err)
		}
		if _, err := environment.Run(ctx, []string{"helper-765", language}); err != nil {
			t.Fatal(err)
		}
		close484(t, environment)
		cancel()
		want := map[string]string{"declared-765": q.Env["PATH"], "helper-765": q.Env["PATH"]}
		if language == "typescript" {
			want["declared-765"] = "/invariant/work/app/node_modules/.bin:" + q.Env["PATH"]
		}
		got := map[string]string{}
		for _, args := range invocations484(t, logs) {
			if len(args) == 0 || args[0] != "exec" || !slices.Contains(args, q.Token) {
				continue
			}
			shell := slices.Index(args, "/bin/sh")
			if shell < 0 || len(args) < 2 || args[len(args)-1] != language {
				continue
			}
			for _, arg := range args[:shell] {
				if value, ok := strings.CutPrefix(arg, "PATH="); ok {
					got[args[len(args)-2]] = value
				}
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: PATH by command = %q, want %q", language, got, want)
		}
	}
}

type commandRunner765 struct {
	*runner484
	mu       sync.Mutex
	commands [][]string
}

func (r *commandRunner765) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	e, err := r.runner484.OpenChecks(ctx, q)
	if err != nil {
		return nil, err
	}
	return &commandEnvironment765{environment484: e.(*environment484), runner: r}, nil
}

type commandEnvironment765 struct {
	*environment484
	runner *commandRunner765
}

func (e *commandEnvironment765) RunCommand(ctx context.Context, argv []string) ([]byte, error) {
	e.runner.mu.Lock()
	e.runner.commands = append(e.runner.commands, slices.Clone(argv))
	e.runner.mu.Unlock()
	return e.environment484.Run(ctx, argv)
}

func (e *commandEnvironment765) Run(ctx context.Context, argv []string) ([]byte, error) {
	return nil, fmt.Errorf("a declared command ran as a helper: %q", argv)
}

// Sandbox.Run gives an environment's command path every declared command, and
// none of them to the helper path.
func TestSandboxRunsDeclaredCommandsOnTheCommandPath765(t *testing.T) {
	part := part484("typescript", "app")
	root, candidate, snapshot := fixture484(t, []repository.Part{part}, nil)
	r := &commandRunner765{runner484: newRunner484(t)}
	s := sandbox484(t, root, snapshot, r.runner484)
	s.Repository.Dependencies.Runner = r
	result, err := s.Run(context.Background(), candidate, &Plan{Files: []string{"app/main.ts"}})
	if err != nil || !result.Passed() {
		t.Fatalf("declared checks: %v\n%s\n%s", err, result.Summary(), result.Output)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.commands) != len(phases484) {
		t.Fatalf("got %d declared commands on the command path, want %d: %q", len(r.commands), len(phases484), r.commands)
	}
	for i, phase := range phases484 {
		if !reflect.DeepEqual(r.commands[i], part.Commands[phase].Argv) {
			t.Errorf("%s ran %q, want %q", phase, r.commands[i], part.Commands[phase].Argv)
		}
	}
}
