package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// A watcher runs whichever coding agent an issue or its project picks
// (#173), so before it takes an issue, it checks that it can run each agent
// it may be asked for: its own, and every one a project's manifest names on
// the base branch (#153). For each, its CLI must run and be signed in.
// Missing any, the watcher doesn't start, and names the fix for each, as
// Check does for a repository whose pull requests can't merge (D-0120). The
// checks read versions and sign-in status only: they print no credential,
// start no agent run and spend nothing.

// Agent is how the watcher checks one coding agent it can run.
type Agent struct {
	Version []string // runs the agent's CLI, which says it's installed
	Status  []string // asks whether it's signed in
	Env     []string // the environment both run with, beyond the watcher's own
	// SignedIn reads what the status said, and how it exited. Nothing it
	// read is ever shown.
	SignedIn func(out []byte, err error) bool
	Install  string // the fix for a CLI that doesn't run
	SignIn   string // the fix for one that isn't signed in
	// Probe, if there is one, checks the place the agent runs is ready,
	// such as Codex's sandbox (D-0138). Its error is the fix.
	Probe func(ctx context.Context) error
}

// ClaudeCode is how the watcher checks Claude Code, at binary.
func ClaudeCode(binary string) Agent {
	return Agent{
		Version: []string{binary, "--version"},
		Status:  []string{binary, "auth", "status", "--json"},
		SignedIn: func(out []byte, err error) bool {
			var s struct {
				LoggedIn bool `json:"loggedIn"`
			}
			return err == nil && json.Unmarshal(out, &s) == nil && s.LoggedIn
		},
		Install: fmt.Sprintf("Claude Code's CLI doesn't run as %s. Install it, or name it with -claude.", binary),
		SignIn:  "Claude Code isn't signed in. Sign it in with your own account:\n  claude auth login",
	}
}

// Codex is how the watcher checks Codex, at binary, signed in to the home
// of its own that its runs use, and with probe checking its sandbox holds
// (D-0138).
func Codex(binary, home string, probe func(ctx context.Context) error) Agent {
	return Agent{
		Probe:    probe,
		Version:  []string{binary, "--version"},
		Status:   []string{binary, "login", "status"},
		Env:      []string{"CODEX_HOME=" + home},
		SignedIn: func(_ []byte, err error) bool { return err == nil },
		Install:  fmt.Sprintf("Codex's CLI doesn't run as %s. Install it, or name it with -codex.", binary),
		SignIn:   fmt.Sprintf("Codex isn't signed in to its own home, %s. Sign it in there with your own account:\n  mkdir -p %q && CODEX_HOME=%q %s login", home, home, home, binary),
	}
}

// checkTimeout is how long one check's command may take.
const checkTimeout = 30 * time.Second

// CheckAgents says whether the watcher can run each agent in need, the ones
// it may be asked for. can is how it checks each agent it can run. Its
// error names the fix for each agent it can't run, or whose CLI doesn't
// run or isn't signed in.
func CheckAgents(ctx context.Context, need []string, can map[string]Agent) error {
	var runnable []string
	for name := range can {
		runnable = append(runnable, name)
	}
	sort.Strings(runnable)
	var fixes []string
	seen := map[string]bool{}
	for _, name := range need {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		a, ok := can[name]
		if !ok {
			fixes = append(fixes, fmt.Sprintf("A project's manifest names %s as its coding agent, and this watcher can't run it. It can run %s. "+
				"Set up %s for this watcher, or name one it can run in that project's manifest.", name, strings.Join(runnable, " and "), name))
			continue
		}
		if _, err := run(ctx, a.Version, a.Env); err != nil {
			fixes = append(fixes, a.Install)
			continue
		}
		out, err := run(ctx, a.Status, a.Env)
		if !a.SignedIn(out, err) {
			fixes = append(fixes, a.SignIn)
			continue
		}
		if a.Probe != nil {
			if err := a.Probe(ctx); err != nil {
				fixes = append(fixes, fmt.Sprintf("%s can't run where the factory runs it: %v", name, err))
			}
		}
	}
	if len(fixes) == 0 {
		return nil
	}
	return fmt.Errorf("Invariant can't run every coding agent it needs here:\n- %s", strings.Join(fixes, "\n- "))
}

// run runs a check's command, and returns what it wrote to its output, for
// the check to read, never to show.
func run(ctx context.Context, command, env []string) ([]byte, error) {
	if len(command) == 0 {
		return nil, errors.New("no command")
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}
