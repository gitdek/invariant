package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// standIn is a CLI that answers --version, and a status that says whether
// it's signed in, as its file there says, recording each call.
func standIn(t *testing.T, dir, name, status string, signedIn bool) string {
	t.Helper()
	exit := "1"
	if signedIn {
		exit = "0"
	}
	script := "#!/bin/sh\n" +
		`echo "$@ CODEX_HOME=$CODEX_HOME" >> "` + filepath.Join(dir, name+".calls") + "\"\n" +
		`if [ "$1" = "--version" ]; then echo "` + name + ` 1.0"; exit 0; fi` + "\n" +
		"echo '" + status + "'\nexit " + exit + "\n"
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// The watcher starts only if it can run every agent it may be asked for:
// its own, and every one a project's manifest names. Each problem names its
// fix, and no check shows what a CLI said about its account.
func TestTheWatcherChecksItsAgentsAtStart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	claude := standIn(t, dir, "claude", `{"loggedIn":true,"email":"SECRET@example.com"}`, true)
	codex := standIn(t, dir, "codex", "Logged in using ChatGPT SECRET", true)
	home := filepath.Join(dir, "codex-home")
	can := map[string]Agent{"claude-code": ClaudeCode(claude), "codex": Codex(codex, home)}

	if err := CheckAgents(ctx, []string{"claude-code", "codex", "claude-code"}, can); err != nil {
		t.Fatalf("both agents run and are signed in: %v", err)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "codex.calls"))
	if !strings.Contains(string(calls), "login status CODEX_HOME="+home) {
		t.Errorf("codex's status wasn't asked of its own home: %s", calls)
	}

	for _, c := range []struct {
		name string
		need []string
		can  map[string]Agent
		fix  string
	}{
		{"claude-code signed out", []string{"claude-code"},
			map[string]Agent{"claude-code": ClaudeCode(standIn(t, dir, "claude-out", `{"loggedIn":false}`, true))}, "claude auth login"},
		{"codex signed out", []string{"claude-code", "codex"},
			map[string]Agent{"claude-code": ClaudeCode(claude), "codex": Codex(standIn(t, dir, "codex-out", "Not logged in", false), home)}, "codex-out login"},
		{"a CLI that doesn't run", []string{"claude-code"},
			map[string]Agent{"claude-code": ClaudeCode(filepath.Join(dir, "missing"))}, "Install it, or name it with -claude"},
		{"an agent the watcher can't run", []string{"claude-code", "codex"},
			map[string]Agent{"claude-code": ClaudeCode(claude)}, "names codex as its coding agent, and this watcher can't run it. It can run claude-code."},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := CheckAgents(ctx, c.need, c.can)
			if err == nil || !strings.Contains(err.Error(), c.fix) {
				t.Fatalf("got %v; want the fix %q", err, c.fix)
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Errorf("the check shows what a CLI said about its account: %v", err)
			}
		})
	}
}
