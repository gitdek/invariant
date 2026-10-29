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
// run: codex unless it says otherwise (#173, #207). Invariant can run Codex
// and Claude Code (#153), so each refuses an agent it doesn't know with its
// other flags, before anything else runs: it exits 2, naming the agent it
// was given and claude-code, one it can run. codex and claude-code are
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
		if !strings.Contains(help, "-agent") || !strings.Contains(help, `(default "codex")`) {
			t.Errorf("invariant %s -h doesn't show -agent, defaulting to codex:\n%s", c.command, help)
		}
		// Codex runs on gpt-6-astra at ultra, its highest reasoning, unless
		// told otherwise, and an effort Codex doesn't take is refused (#207).
		if !strings.Contains(help, `(default "gpt-6-astra")`) || !strings.Contains(help, `(default "ultra")`) {
			t.Errorf("invariant %s -h doesn't show Codex on gpt-6-astra at ultra:\n%s", c.command, help)
		}
		args := append([]string{c.command, "-codex-effort", "turbo"}, c.rest...)
		if code, out := agentCLI(t, args...); code != 2 || !strings.Contains(out, "-codex-effort") || !strings.Contains(out, "ultra") {
			t.Errorf("invariant %s exited %d; want 2, refusing -codex-effort turbo:\n%s", strings.Join(args, " "), code, out)
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

// A watcher runs Claude Code only when -agents names it (#207). -agents
// defaults to codex alone, and a watcher refuses, before anything else
// runs, an -agent that -agents doesn't name, or an -agents name Invariant
// doesn't know.
func TestAWatcherRunsClaudeCodeOnlyWhenTold(t *testing.T) {
	_, help := agentCLI(t, "watch", "-h")
	if !strings.Contains(help, "-agents") || !strings.Contains(help, `-agents string`) || !strings.Contains(help, "never runs Claude Code") {
		t.Errorf("invariant watch -h doesn't show -agents, and that it never runs Claude Code unless told:\n%s", help)
	}
	for _, c := range []struct {
		args []string
		says string
	}{
		{[]string{"-agent", "claude-code"}, "-agents codex,claude-code"},
		{[]string{"-agents", "codex,gemini"}, `"gemini"`},
	} {
		args := append(append([]string{"watch"}, c.args...), "-repo", "gitdek/invariant")
		code, out := agentCLI(t, args...)
		if code != 2 || !strings.Contains(out, c.says) {
			t.Errorf("invariant %s exited %d; want 2, saying %s:\n%s", strings.Join(args, " "), code, c.says, out)
		}
	}
	for list, want := range map[string]string{"codex": "codex", " codex , claude-code ,codex": "codex claude-code"} {
		may, err := mayRun(list, "codex")
		if err != nil || strings.Join(may, " ") != want {
			t.Errorf("-agents %q gave %q, %v; want %s", list, may, err, want)
		}
	}
}

// A build the loop guard stops runs once more at -fallback-effort only when
// that's below the effort it ran at (D-0125): below Codex's ultra as below
// Claude Code's max (#207).
func TestABuildFallsBackOnlyBelowItsEffort(t *testing.T) {
	for _, c := range []struct{ effort, fallback, want string }{
		{"ultra", "xhigh", "xhigh"},
		{"max", "xhigh", "xhigh"},
		{"xhigh", "xhigh", ""},
		{"ultra", "", ""},
	} {
		if got := fallbackEffort(c.effort, c.fallback); got != c.want {
			t.Errorf("at %s, falling back to %q runs %q; want %q", c.effort, c.fallback, got, c.want)
		}
	}
}
