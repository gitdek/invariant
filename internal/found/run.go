package found

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Command is one run of gh or git: the only way a founding reaches GitHub
// or changes a checkout's history.
type Command struct {
	Dir   string   // where it runs; the process's own directory when empty
	Name  string   // gh or git
	Args  []string // its arguments, never read by a shell
	Stdin string   // what it reads, when it reads anything
}

// String is the command as a person would type it, for an error to name.
func (c Command) String() string {
	return strings.Join(append([]string{c.Name}, c.Args...), " ")
}

// Runner runs a founding's commands. Exec is the real one, and a test hands
// in its own, so no test creates a repository or pushes.
type Runner interface {
	// Run runs the command and returns what it printed. A command that
	// fails returns an error holding what it said of the failure.
	Run(ctx context.Context, c Command) ([]byte, error)
}

// Exec runs each command as a process, as whoever gh and git are set up for.
// It hands them no token and reads none.
type Exec struct{}

func (Exec) Run(ctx context.Context, c Command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// gh and git say why on stderr, and gh api prints GitHub's answer.
		said := strings.TrimSpace(stderr.String())
		if said == "" {
			said = strings.TrimSpace(stdout.String())
		}
		if said == "" {
			return stdout.Bytes(), err
		}
		return stdout.Bytes(), errors.New(said)
	}
	return stdout.Bytes(), nil
}
