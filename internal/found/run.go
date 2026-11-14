package found

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
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
// It hands them no token and reads none. git works on the checkout the
// command runs in, and gh on github.com, whatever the environment says: a
// hook or an alias can leave GIT_DIR or its like set, which point git at
// another repository, so Exec clears the ones git clears itself when it
// works in another repository. GH_HOST, or a login to one other server,
// points gh at that server, so Exec names github.com.
type Exec struct{}

// repositoryVariables are the variables git rev-parse --local-env-vars
// lists: the ones that tie git to one repository.
var repositoryVariables = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE",
	"GIT_INDEX_FILE", "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
	"GIT_SHALLOW_FILE", "GIT_COMMON_DIR",
}

func (Exec) Run(ctx context.Context, c Command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if name != "GH_HOST" && !slices.Contains(repositoryVariables, name) {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "GH_HOST=github.com")
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
