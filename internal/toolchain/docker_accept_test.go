package toolchain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var containerName = regexp.MustCompile(fmt.Sprintf(`^invariant-%d-[0-9]+-[0-9]+$`, os.Getpid()))

// runName returns the name Docker gives a run, which must come as
// "--name", name right after "run".
func runName(t *testing.T, args []string) string {
	t.Helper()
	if len(args) < 4 || args[0] != "docker" || args[1] != "run" || args[2] != "--name" {
		t.Fatalf("a run must start docker run --name <name>, got %q", args)
	}
	if !containerName.MatchString(args[3]) {
		t.Fatalf("container name %q isn't invariant-<pid>-<start>-<n>", args[3])
	}
	return args[3]
}

func TestDockerRunIsNamedAndKeepsItsArguments(t *testing.T) {
	cmd := Docker(context.Background(), "run", "--rm", "--network", "none", "-v", "/a:/b", "img", "sh", "-c", "echo hi")
	runName(t, cmd.Args)
	want := []string{"--rm", "--network", "none", "-v", "/a:/b", "img", "sh", "-c", "echo hi"}
	if got := cmd.Args[4:]; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("run's other arguments = %q, want %q", got, want)
	}
}

func TestDockerLeavesOtherCommandsAsTheyWere(t *testing.T) {
	for _, args := range [][]string{
		{"image", "inspect", "img"},
		{"pull", "--quiet", "img"},
		{"build", "-t", "tag", "-"},
		{"info"},
	} {
		cmd := Docker(context.Background(), args...)
		want := append([]string{"docker"}, args...)
		if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("Docker(%q).Args = %q, want %q", args, cmd.Args, want)
		}
	}
}

func TestDockerNeverGivesTwoContainersOneName(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		name := runName(t, Docker(context.Background(), "run", "img").Args)
		if seen[name] {
			t.Fatalf("two containers named %q", name)
		}
		seen[name] = true
	}
}

// A fake docker on PATH records every command it's given, and its run sleeps
// until it's killed.
func TestDockerRunRemovesItsContainerWhenItsContextEnds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\nif [ \"$1\" = run ]; then exec sleep 60; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := Docker(ctx, "run", "--rm", "img", "sleep", "600")
	name := runName(t, cmd.Args)
	start := time.Now()
	if err := cmd.Run(); err == nil {
		t.Fatal("a run cut off by its context must return an error")
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("the run took %s to stop after its context ended", took)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "rm --force "+name+"\n") {
		t.Errorf("the container wasn't removed with docker rm --force %s; docker was given:\n%s", name, data)
	}
}

func TestDockerRunLeavesAFinishedContainerAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cmd := Docker(context.Background(), "run", "--rm", "img", "true")
	runName(t, cmd.Args)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "rm --force") {
		t.Errorf("a run that finished on its own must not be removed; docker was given:\n%s", data)
	}
}
