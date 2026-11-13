package plumbing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/decisions"
)

// Checked is what the factory found checking a drafted plan against the
// repository as it is.
type Checked struct {
	Plan    *Plan
	Problem string // why the plan can't be proposed, or ""
	Run     Result // the acceptance tests, run on the code as it is
}

// Check reads the plan an agent drafted in ws, and checks it against the
// checkout at repo: each acceptance test is a new file, and each one fails
// on the code as it is. A test that already passes can't show the change
// works, as a planted bug TLC can't catch shows nothing. With a decision
// journal, the staged acceptance sources must also leave the graph valid.
func Check(ctx context.Context, ws, repo string, s Sandbox) (*Checked, error) {
	if s.Checks != nil {
		return s.Checks.check(ctx, ws)
	}
	var plan *Plan
	var err error
	if s.Repository != nil {
		plan, err = ReadRepositoryDraft(ctx, ws, s.Repository.Snapshot)
	} else {
		plan, err = ReadDraft(ws)
	}
	if err != nil {
		return &Checked{Problem: err.Error()}, nil
	}
	if problem := ignoredByGit(ctx, repo, plan); problem != "" {
		return &Checked{Plan: plan, Problem: problem}, nil
	}
	c := &Checked{Plan: plan}
	if plan.Native() && s.Repository == nil {
		c.Problem = "native acceptance needs the caller's captured repository authority"
		return c, nil
	}
	if s.Repository != nil {
		// ReadRepositoryDraft checked source absence in the captured commit.
		// Working-tree files cannot replace that independent base.
		s.Original = repo
		c, err := s.checkRepositoryBase(ctx, repo, plan)
		// Configured failures belong to the draft's report, including all
		// completed evidence and the infrastructure reason that vetoed it.
		if err != nil && c.Problem == "" {
			c.Problem = "acceptance checks could not finish: " + err.Error()
		}
		return c, nil
	}
	for _, f := range plan.TestFiles() {
		if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(f))); err == nil {
			c.Problem = fmt.Sprintf("acceptance test file %s is already in the repository; put the tests in a new file, which the build can't change", f)
			return c, nil
		}
	}
	dir, err := os.MkdirTemp("", "invariant-plan-check-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := copyCheckout(ctx, repo, dir); err != nil {
		return nil, err
	}
	for f, src := range plan.Sources {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			return nil, err
		}
	}
	c.Problem, err = checkPlanDecisions(repo, dir)
	if err != nil {
		return nil, err
	}
	if c.Problem != "" {
		return c, nil
	}
	// The copy isn't a checkout, so the sandbox lists its files by walking.
	c.Run, err = s.Run(ctx, dir, plan)
	if err != nil {
		// The built-in runner's failure is the factory's, not the plan's.
		// A configured check refuses the draft instead.
		if s.Repository == nil {
			return nil, err
		}
		c.Problem = "acceptance checks could not finish: " + err.Error()
		return c, nil
	}
	var passing []string
	var unfinished []string
	for _, t := range plan.Tests {
		passed := c.Run.Accepted[t.File+":"+t.Name]
		// Legacy results have only Accepted. Structured runs distinguish a
		// completed failure (including an attributed production API error)
		// from a check that never ran, skipped, or exited before finishing.
		if c.Run.TestResults != nil || c.Run.CommandResults != nil || s.Repository != nil {
			status := "not_run"
			reason := ""
			for _, result := range c.Run.TestResults {
				if result.Phase == "accept" && result.identity() == t.Identity() {
					status = result.Status
					reason = result.Reason
					break
				}
			}
			if status != "failed" && status != "passed" {
				detail := status
				if reason != "" {
					detail += ": " + reason
				}
				unfinished = append(unfinished, fmt.Sprintf("%s (%s)", t.Identity(), detail))
			}
			passed = status == "passed"
		}
		if passed {
			passing = append(passing, t.Name)
		}
	}
	var problems []string
	if len(passing) > 0 {
		problems = append(problems, fmt.Sprintf("these acceptance tests already pass on the code as it is, so they can't show the change works: %s", strings.Join(passing, ", ")))
	}
	if c.Run.DeadlineExceeded {
		problems = append(problems, "the sandbox reached its deadline, so this run cannot establish that the acceptance tests fail on the code as it is")
	}
	if c.Run.TimedOut {
		problems = append(problems, "a sandbox test phase timed out, so this run cannot establish that the acceptance tests fail on the code as it is")
	}
	if c.Run.Interrupted {
		problems = append(problems, "the sandbox was interrupted, so this run cannot establish that the acceptance tests fail on the code as it is")
	}
	if len(unfinished) > 0 {
		problems = append(problems, fmt.Sprintf("these acceptance tests did not complete with failure evidence: %s", strings.Join(unfinished, ", ")))
	}
	c.Problem = strings.Join(problems, "; ")
	return c, nil
}

// ignoredByGit says which files of a plan the checkout's ignore rules match:
// the files it names for the build, its acceptance tests, and the record a
// ratification writes. git leaves such a file out of a commit without a
// word, so a build that wrote it would publish a branch that lacks it
// (#840). A file the repository already tracks is never ignored, and a
// directory that isn't a checkout has no rules to ask.
func ignoredByGit(ctx context.Context, repo string, plan *Plan) string {
	record := LockPath(0)
	files := append(append(slices.Clone(plan.Files), plan.TestFiles()...), record)
	// The paths go in on stdin, which is what git reads NUL-separated.
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "check-ignore", "-v", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(files, "\x00") + "\x00")
	// It exits 1 where nothing matches, and 128 where repo isn't a checkout.
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Each match is the rule's file, its line, the pattern and the path.
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	var problems []string
	for i := 0; i+3 < len(fields); i += 4 {
		source, line, pattern, file := fields[i], fields[i+1], fields[i+2], fields[i+3]
		if strings.HasPrefix(pattern, "!") {
			continue // a rule that takes the file back out of what's ignored
		}
		rule := fmt.Sprintf("%s line %s: %s", source, line, pattern)
		if file == record {
			problems = append(problems, fmt.Sprintf("the repository's ignore rules leave %s out of every commit (%s), so a ratified plan can't be recorded there", LockDir, rule))
			continue
		}
		problems = append(problems, fmt.Sprintf("the plan names %s, which the repository's ignore rules leave out of every commit (%s), so a build couldn't publish it; give the file a name those rules don't match", file, rule))
	}
	return strings.Join(problems, "; ")
}

// checkPlanDecisions checks the staged copy in a fresh store, so neither
// another draft nor the machine's decision store supplies its citations.
func checkPlanDecisions(repo, dir string) (string, error) {
	// Copying regular files doesn't preserve an empty journal directory.
	if _, err := os.Stat(filepath.Join(repo, "decisions", "journal")); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "invariant-plan-decisions-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	s, err := decisions.Open(filepath.Join(tmp, "decisions.db"))
	if err != nil {
		return "", err
	}
	defer s.Close()
	repos := []decisions.Repo{{Name: "repo", Dir: dir}}
	if err := s.Rebuild(repos); err != nil {
		return "the journal doesn't rebuild: " + err.Error(), nil
	}
	problems, err := s.Check(repos)
	if err != nil {
		return "", err
	}
	return strings.Join(problems, "; "), nil
}

// Feedback says what a check found, for the agent drafting the plan.
func (c *Checked) Feedback() string {
	if c.Problem != "" {
		out := "The plan can't be proposed yet: " + c.Problem + "."
		if c.Run.CommandResults != nil {
			out += "\n\n" + c.Run.Summary()
		}
		if c.Run.Output != "" {
			out += "\n\nThe acceptance tests, run on the code as it is:\n" + c.Run.Tail(4000)
		}
		return out
	}
	p := c.Plan
	out := fmt.Sprintf("The plan holds together: the build may write %d files, and its %d acceptance tests fail on the code as it is, as they should until the change is made.", len(p.Files), len(p.Tests))
	if len(p.Trusted) > 0 {
		out += fmt.Sprintf(" It touches the trusted base (%s), so a person merges it.", strings.Join(p.Trusted, ", "))
	}
	if c.Run.CommandResults != nil {
		out += "\n\n" + c.Run.Summary()
	}
	return out
}
