package plumbing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerProcessEarlyCloseKeepsCleanupReserve(t *testing.T) {
	b := newDocker591(t)
	q := request484(t, "go")
	env, err := (&DockerDependencyRunner{}).OpenChecks(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = docker591Close(t, env) })
	e := env.(RepositoryProcessTransport)
	source := `package main
import("fmt";"time")
func main(){fmt.Print("cleanup probe ready");for{time.Sleep(time.Hour)}}
`
	main, err := e.Stage(t.Context(), "cleanup/main.go", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	binary, err := e.Stage(t.Context(), "cleanup/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := e.Run(t.Context(), []string{"go", "build", "-o", binary, main}); err != nil {
		t.Fatalf("compile cleanup probe: %v %s", err, out)
	}
	prepared, err := e.Prepare(t.Context(), RepositoryProcessSpec{Argv: []string{binary}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	process, err := prepared.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	docker591Await(t, ctx, func() bool {
		return strings.Contains(string(process.Snapshot().Stdout), "cleanup probe ready")
	}, "cleanup probe start")
	docker591Touch(t, filepath.Join(b.root, "hold-cleanup"))
	release := filepath.Join(b.root, "release-cleanup")
	defer func() { _ = os.WriteFile(release, []byte("release"), 0600) }()
	if err := ctx.Err(); err != nil {
		t.Fatal("execution expired before early close: ", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- process.Close(ctx) }()
	docker591Await(t, ctx, func() bool {
		return docker591Exists(filepath.Join(b.root, "cleanup-entered"))
	}, "delayed Docker removal")
	// Keep rm blocked past the live execution deadline that Close interrupted.
	// Cleanup must still own the client and container until removal finishes.
	<-ctx.Done()
	select {
	case err := <-closed:
		t.Fatalf("early close returned before delayed Docker removal: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	container := filepath.Join(b.root, "containers", q.Token)
	if !docker591Exists(container) {
		t.Fatal("Docker removal did not remain blocked")
	}
	docker591Touch(t, release)
	select {
	case err := <-closed:
		if err == nil || !strings.Contains(err.Error(), "closed before completion") || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("early close lost interruption or inherited execution deadline: %v", err)
		}
	case <-time.After(SandboxCleanupAllowance):
		t.Fatal("early close did not join Docker cleanup")
	}
	if _, err := os.Stat(container); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("early close left its container behind: %v", err)
	}
	if out := process.Snapshot(); !out.Started || string(out.Stdout) != "cleanup probe ready" {
		t.Fatalf("early close erased authentic output: %+v", out)
	}
	// Close reuses the completed removal and cleans staging under the same
	// reserve, retaining the earlier interruption without a cleanup timeout.
	if err := docker591Close(t, e); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("environment close lost interruption or failed cleanup: %v", err)
	}
	b.clean()
}
