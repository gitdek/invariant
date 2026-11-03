package toolchain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("INVARIANT_PULL_PIPE_HELPER") == "1" {
		os.Exit(dockerPipeMain())
	}
}

func dockerPipeMain() int {
	path := os.Getenv("INVARIANT_PULL_PIPE_CALLS")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	n, _ := strconv.Atoi(string(b))
	n++
	if err := os.WriteFile(path, []byte(strconv.Itoa(n)), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprint(os.Stdout, dockerPipeOutput("stdout", n))
	fmt.Fprint(os.Stderr, dockerPipeOutput("stderr", n))
	switch os.Getenv("INVARIANT_PULL_PIPE_MODE") {
	case "recover":
		if n == 3 {
			return 0
		}
	case "forever":
	case "integrity":
		fmt.Fprintln(os.Stderr, "digest mismatch")
		return 1
	default:
		return 0
	}
	fmt.Fprintf(os.Stderr, "registry unavailable attempt-%d\n", n)
	return 1
}

func dockerPipeOutput(stream string, attempt int) string {
	// More than a pipe buffer makes losing or truncating a stream visible.
	return strings.Repeat(fmt.Sprintf("%s attempt-%d\n", stream, attempt), 8192)
}

func dockerPipeSetup(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "'"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nexec "+quoted+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "calls")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_PULL_PIPE_HELPER", "1")
	t.Setenv("INVARIANT_PULL_PIPE_CALLS", path)
	t.Setenv("INVARIANT_PULL_PIPE_MODE", mode)
	return path
}

type dockerPipeRead struct {
	out []byte
	err error
}

func dockerReadPipe(pipe io.Reader) <-chan dockerPipeRead {
	done := make(chan dockerPipeRead, 1)
	go func() {
		out, err := io.ReadAll(pipe)
		done <- dockerPipeRead{out, err}
	}()
	return done
}

func TestPinnedDockerPullKeepsOutputPipesAcrossAttempts(t *testing.T) {
	for _, mode := range []string{"success", "recover", "forever", "integrity"} {
		t.Run(mode, func(t *testing.T) {
			path := dockerPipeSetup(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := Docker(ctx, "pull", JavaImage)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdout.Close()
			stderr, err := cmd.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stderr.Close()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				cancel()
				stdout.Close()
				stderr.Close()
				if !waited {
					cmd.Wait()
				}
			}()
			readers := []<-chan dockerPipeRead{dockerReadPipe(stdout), dockerReadPipe(stderr)}
			var got [2]string
			for i, reader := range readers {
				select {
				case result := <-reader:
					if result.err != nil {
						t.Fatalf("pipe %d: %v", i, result.err)
					}
					got[i] = string(result.out)
				case <-ctx.Done():
					t.Fatal("pipes did not reach EOF before Wait")
				}
			}
			err = cmd.Wait()
			waited = true
			failed := mode == "forever" || mode == "integrity"
			if (err != nil) != failed {
				t.Fatalf("Wait error=%v, want failure=%v", err, failed)
			}
			attempts := 1
			if mode == "recover" || mode == "forever" {
				attempts = 3
			}
			calls, readErr := os.ReadFile(path)
			if readErr != nil || string(calls) != strconv.Itoa(attempts) {
				t.Fatalf("attempts=%q, error=%v; want %d", calls, readErr, attempts)
			}
			var want [2]string
			for n := 1; n <= attempts; n++ {
				want[0] += dockerPipeOutput("stdout", n)
				want[1] += dockerPipeOutput("stderr", n)
				if mode == "forever" || mode == "recover" && n < 3 {
					want[1] += fmt.Sprintf("registry unavailable attempt-%d\n", n)
				}
			}
			if mode == "integrity" {
				want[1] += "digest mismatch\n"
			}
			if got != want {
				t.Errorf("pipe output changed: got lengths %d/%d, want %d/%d", len(got[0]), len(got[1]), len(want[0]), len(want[1]))
			}
			last := "registry unavailable attempt-3"
			if mode == "integrity" {
				last = "digest mismatch"
			}
			if failed && !strings.Contains(err.Error(), last) {
				t.Error("Wait lost the final diagnostic")
			}
		})
	}
}

func TestDockerOutputPipesCloseOnStartFailure(t *testing.T) {
	dockerPipeSetup(t, "success")
	cmd := Docker(context.Background(), "pull", JavaImage)
	cmd.Path = filepath.Join(t.TempDir(), "missing-docker")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	writers := []io.Writer{cmd.Stdout, cmd.Stderr}
	if err := cmd.Start(); err == nil {
		t.Fatal("missing Docker client started")
	}
	for _, reader := range []io.Reader{stdout, stderr} {
		if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
			t.Errorf("reader left open after failed Start: %v", err)
		}
	}
	for _, writer := range writers {
		if _, err := writer.Write([]byte("unused")); !errors.Is(err, os.ErrClosed) {
			t.Errorf("writer left open after failed Start: %v", err)
		}
	}
}
