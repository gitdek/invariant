package plumbing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// works, as a planted bug TLC can't catch shows nothing.
func Check(ctx context.Context, ws, repo string, s Sandbox) (*Checked, error) {
	plan, err := ReadDraft(ws)
	if err != nil {
		return &Checked{Problem: err.Error()}, nil
	}
	c := &Checked{Plan: plan}
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
	// The copy isn't a checkout, so the sandbox lists its files by walking.
	if c.Run, err = s.Run(ctx, dir, plan); err != nil {
		return nil, err
	}
	var passing []string
	for _, t := range plan.Tests {
		if c.Run.Accepted[t.File+":"+t.Name] {
			passing = append(passing, t.Name)
		}
	}
	if len(passing) > 0 {
		c.Problem = fmt.Sprintf("these acceptance tests already pass on the code as it is, so they can't show the change works: %s", strings.Join(passing, ", "))
	}
	return c, nil
}

// Feedback says what a check found, for the agent drafting the plan.
func (c *Checked) Feedback() string {
	if c.Problem != "" {
		out := "The plan can't be proposed yet: " + c.Problem + "."
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
	return out
}
