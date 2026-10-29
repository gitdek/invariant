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
// doesn't start while one names a coding agent it may run but can't, or one
// Invariant doesn't know (#153). A manifest that names an agent the watcher
// wasn't told to run, such as Claude Code beside Codex alone, doesn't stop
// it, and that agent's CLI never runs: each of that project's issues is
// answered instead (#207).
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
	bin := t.TempDir()
	ran := filepath.Join(bin, "claude-ran")
	claude := filepath.Join(bin, "claude")
	if err := os.WriteFile(claude, []byte("#!/bin/sh\ntouch "+ran+"\n[ \"$1\" = --version ] && exit 0\necho '{\"loggedIn\":false}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(bin, "codex")
	if err := os.WriteFile(codex, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ready := func(context.Context) error { return nil }
	alone := map[string]setup.Agent{"codex": setup.Codex(codex, t.TempDir(), ready)}
	if err := checkAgents(ctx, clone, "main", "codex", alone); err != nil {
		t.Fatalf("a watcher on codex alone shouldn't be stopped by a project that names claude-code: %v", err)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("a watcher on codex alone ran Claude Code's CLI")
	}

	both := map[string]setup.Agent{"codex": alone["codex"], "claude-code": setup.ClaudeCode(claude)}
	err := checkAgents(ctx, clone, "main", "codex", both)
	if err == nil || !strings.Contains(err.Error(), "Claude Code isn't signed in") {
		t.Errorf("a watcher told to run claude-code, which a manifest names, should need it signed in: %v", err)
	}

	write("factory/c/.invariant/invariant.json", `{"name":"c","module":"C.tla","code":"c","language":"go","agent":"gemini"}`)
	git(origin, "add", "-A")
	git(origin, "commit", "--quiet", "-m", "a project on an agent Invariant doesn't know")
	err = checkAgents(ctx, clone, "main", "codex", alone)
	if err == nil || !strings.Contains(err.Error(), "names gemini") {
		t.Errorf("a manifest names gemini, which Invariant doesn't know: %v", err)
	}
}
