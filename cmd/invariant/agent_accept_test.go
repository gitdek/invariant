package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// agentArgs, set in its environment, has this test binary run the CLI with
// those arguments, one to a line, in place of its tests. A command that
// refuses its flags exits, so the tests below run each in a process of its
// own.
const agentArgs = "INVARIANT_AGENT_ACCEPT_ARGS"

func init() {
	if args, ok := os.LookupEnv(agentArgs); ok {
		os.Args = append([]string{"invariant"}, strings.Split(args, "\n")...)
		main()
	}
}

// agentCLI runs the CLI with args, and returns its exit code and everything
// it printed.
func agentCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), agentArgs+"="+strings.Join(args, "\n"))
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

// watch, formalize and synthesize each take -agent, the coding agent they
// run: claude-code unless it says otherwise (#173). Invariant can run Claude
// Code and Codex (#153), so each refuses an agent it doesn't know with its
// other flags, before anything else runs: it exits 2, naming the agent it
// was given and claude-code, one it can run. claude-code and codex are
// taken: formalize goes on past its flags, and fails only reading a request
// that isn't there.
func TestTheCommandsTakeTheAgentTheyRun(t *testing.T) {
	for _, c := range []struct {
		command string
		rest    []string
	}{
		{"watch", []string{"-repo", "gitdek/invariant"}},
		{"formalize", []string{"request.md"}},
		{"synthesize", []string{"examples/02-twophase-commit"}},
	} {
		_, help := agentCLI(t, c.command, "-h")
		if !strings.Contains(help, "-agent") || !strings.Contains(help, `(default "claude-code")`) {
			t.Errorf("invariant %s -h doesn't show -agent, defaulting to claude-code:\n%s", c.command, help)
		}
		for _, agent := range []string{"gemini"} {
			args := append([]string{c.command, "-agent", agent}, c.rest...)
			code, out := agentCLI(t, args...)
			if code != 2 || !strings.Contains(out, agent) || !strings.Contains(out, "claude-code") || strings.Contains(out, "flag provided but not defined") {
				t.Errorf("invariant %s exited %d; want 2, refusing %s and naming claude-code, the agent it can run:\n%s", strings.Join(args, " "), code, agent, out)
			}
		}
	}

	for _, agent := range []string{"claude-code", "codex"} {
		code, out := agentCLI(t, "formalize", "-agent", agent, "no-such-request.md")
		if code != 2 || !strings.Contains(out, "no-such-request.md") || strings.Contains(out, "flag provided but not defined") {
			t.Errorf("invariant formalize -agent %s exited %d; want 2, from reading a request that isn't there:\n%s", agent, code, out)
		}
	}
}
