package toolchain

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dockerStartupPipes(t *testing.T, cmd *DockerCommand) map[string]*os.File {
	t.Helper()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close() })
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdout.Close() })
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stderr.Close() })
	pipes := make(map[string]*os.File)
	for name, endpoint := range map[string]any{
		"stdin writer": stdin, "stdin reader": cmd.Stdin,
		"stdout reader": stdout, "stdout writer": cmd.Stdout,
		"stderr reader": stderr, "stderr writer": cmd.Stderr,
	} {
		file, ok := endpoint.(*os.File)
		if !ok {
			t.Fatalf("%s is %T, want an OS pipe", name, endpoint)
		}
		pipes[name] = file
		t.Cleanup(func() { file.Close() })
	}
	return pipes
}

func TestDockerStartupFailureClosesAllPipeEndpoints(t *testing.T) {
	for _, failure := range []string{"acquisition", "acquisition cancellation", "lookup", "start", "cancelled start"} {
		for _, command := range []string{"run", "pull"} {
			acquire := strings.HasPrefix(failure, "acquisition")
			if acquire && command == "pull" {
				continue
			}
			t.Run(failure+"/"+command, func(t *testing.T) {
				dir := docker433Setup(t, "forever")
				if failure == "lookup" {
					t.Setenv("PATH", t.TempDir())
				}
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				defer cancel()
				image := JavaImage
				if command == "run" && !acquire {
					image = "local:recipe"
				}
				cmd := Docker(ctx, command, image)
				pipes := dockerStartupPipes(t, cmd)
				if failure == "start" {
					cmd.Path = filepath.Join(t.TempDir(), "missing-docker")
				}
				if failure == "cancelled start" {
					cancel()
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Start() }()
				if failure == "acquisition cancellation" {
					docker433Wait(t, func() bool { return len(docker433OfKind(dir, "pull")) > 0 })
					cancel()
				}
				var err error
				select {
				case err = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("startup did not finish")
				}
				if err == nil {
					cmd.Process.Kill()
					cmd.Wait()
					t.Fatal("startup unexpectedly succeeded")
				}
				if strings.Contains(failure, "cancel") && !errors.Is(err, context.Canceled) {
					t.Errorf("cancellation lost: %v", err)
				}
				if failure == "acquisition" && !strings.Contains(err.Error(), "attempt-3") {
					t.Errorf("last acquisition diagnostic lost: %v", err)
				}
				if failure == "lookup" && (cmd.Err == nil || !errors.Is(err, cmd.Err)) {
					t.Errorf("lookup error lost: %v, command error %v", err, cmd.Err)
				}
				if cmd.Process != nil || len(docker433OfKind(dir, "run")) != 0 {
					t.Error("failed startup launched a workload")
				}
				// Keep the command and all six endpoints alive: cleanup must not
				// depend on a finalizer or on the caller closing its pipe ends.
				for name, file := range pipes {
					if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
						t.Errorf("%s remains open after failed startup: %v", name, err)
					}
				}
			})
		}
	}
}

func TestDockerRunPipesSurviveRepeatedStart(t *testing.T) {
	docker433Setup(t, "local")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := Docker(ctx, "run", "local:recipe")
	pipes := dockerStartupPipes(t, cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err == nil {
		t.Error("a second Start succeeded")
	}
	if _, err := pipes["stdin writer"].WriteString("payload\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := pipes["stdin writer"].Close(); err != nil {
		t.Fatal(err)
	}
	stdout, outErr := io.ReadAll(pipes["stdout reader"])
	stderr, errErr := io.ReadAll(pipes["stderr reader"])
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if outErr != nil || errErr != nil || string(stdout) != "verified:payload\n" || len(stderr) != 0 {
		t.Fatalf("pipe I/O changed: stdout=%q, %v; stderr=%q, %v", stdout, outErr, stderr, errErr)
	}
	for name, file := range pipes {
		if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("%s remains open after Wait: %v", name, err)
		}
	}
}
