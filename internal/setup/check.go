package setup

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// GitHub is what Check reads of a repository. github.Client is the real one.
type GitHub interface {
	// Workflows is the text of each workflow file at ref, by path.
	Workflows(ctx context.Context, ref string) (map[string]string, error)
	// MergeCommits says whether the repository allows merge commits.
	MergeCommits(ctx context.Context) (bool, error)
}

// Check says whether the factory can work on repo, whose pull requests merge
// into base. The factory merges a pull request only once invariant/gate
// passes, so a workflow on base needs a job by that name, and only with a
// merge commit, so the repository must allow them. Its error names the fix
// for each that's missing.
func Check(ctx context.Context, gh GitHub, repo, base string) error {
	workflows, err := gh.Workflows(ctx, base)
	if err != nil {
		return fmt.Errorf("reading %s's workflows on %s: %w", repo, base, err)
	}
	merges, err := gh.MergeCommits(ctx)
	if err != nil {
		return fmt.Errorf("reading whether %s allows merge commits: %w", repo, err)
	}
	gate := false
	for _, w := range workflows {
		gate = gate || hasGateJob(w)
	}
	var fixes []string
	if !gate {
		fixes = append(fixes, fmt.Sprintf("No workflow on %s has a job named invariant/gate, the check the factory merges on. "+
			"Run invariant init in a checkout of %s, and commit the workflow it writes to %s.", base, repo, base))
	}
	if !merges {
		fixes = append(fixes, fmt.Sprintf("%s doesn't allow merge commits, and the factory merges with one. Allow them:\n  gh api -X PATCH repos/%s -F allow_merge_commit=true", repo, repo))
	}
	if len(fixes) == 0 {
		return nil
	}
	return fmt.Errorf("the factory can't work on %s yet:\n- %s", repo, strings.Join(fixes, "\n- "))
}

// gateName is a job's name line when the name is invariant/gate, quoted or
// not, with any comment after it.
var gateName = regexp.MustCompile(`^name\s*:\s+("invariant/gate"|'invariant/gate'|invariant/gate)(\s+#.*)?$`)

// hasGateJob says whether a workflow has a job named invariant/gate. A check
// run takes its job's name, so the workflow's own name doesn't count, and
// neither does a step's. Reading the YAML by its indentation is enough to
// tell a job's own keys from the rest.
func hasGateJob(workflow string) bool {
	// Whether the line is in the top-level jobs map, and how far a job's ID
	// and its keys are indented there.
	jobs, job, key := false, -1, -1
	for _, line := range strings.Split(workflow, "\n") {
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 0:
			k, _, _ := strings.Cut(text, ":")
			jobs, job, key = strings.TrimSpace(k) == "jobs", -1, -1
		case !jobs:
			// Outside jobs, no name is a job's.
		case job < 0 || indent <= job:
			// A job's ID.
			job, key = indent, -1
		case key < 0 || indent == key:
			// One of the job's own keys. Anything deeper is a step's, or a
			// value's.
			key = indent
			if gateName.MatchString(text) {
				return true
			}
		}
	}
	return false
}
