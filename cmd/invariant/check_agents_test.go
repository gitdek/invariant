package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/setup"
)

// The watcher reads every project's manifest on the base branch, and
// doesn't start while one names a coding agent it can't run (#153).
func TestTheWatcherNeedsEveryAgentAManifestNames(t *testing.T) {
	origin := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Seed", "-c", "user.email=seed@example.com"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(p, text string) {
		t.Helper()
		full := filepath.Join(origin, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(origin, "init", "--quiet", "-b", "main")
	write("examples/a/.invariant/invariant.json", `{"name":"a","module":"A.tla","code":"a","language":"go"}`)
	write("examples/b/.invariant/invariant.json", `{"name":"b","module":"B.tla","code":"b","language":"go","agent":"claude-code"}`)
	git(origin, "add", "-A")
	git(origin, "commit", "--quiet", "-m", "seed")

	clone := factory.Clone{Dir: filepath.Join(t.TempDir(), "clone"), Remote: origin}
	ctx := context.Background()
	if err := clone.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	stand := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(stand, []byte("#!/bin/sh\n[ \"$1\" = --version ] && exit 0\necho '{\"loggedIn\":true}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	can := map[string]setup.Agent{"claude-code": setup.ClaudeCode(stand)}
	if err := checkAgents(ctx, clone, "main", "claude-code", can); err != nil {
		t.Fatalf("every manifest names an agent the watcher can run: %v", err)
	}

	write("factory/c/.invariant/invariant.json", `{"name":"c","module":"C.tla","code":"c","language":"go","agent":"codex"}`)
	git(origin, "add", "-A")
	git(origin, "commit", "--quiet", "-m", "a project on codex")
	err := checkAgents(ctx, clone, "main", "claude-code", can)
	if err == nil || !strings.Contains(err.Error(), "names codex") {
		t.Errorf("a manifest names codex, which the watcher can't run: %v", err)
	}
}
