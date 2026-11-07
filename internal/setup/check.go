package setup

import (
	"context"
	"fmt"
	"regexp"
	"slices"
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
	return CheckRepository(ctx, gh, repo, base, false)
}

// CheckRepository retains modeled-only setup and requires the configured
// gate's execution and authority checks in the job the factory merges on.
func CheckRepository(ctx context.Context, gh GitHub, repo, base string, configured bool) error {
	workflows, err := gh.Workflows(ctx, base)
	if err != nil {
		return fmt.Errorf("reading %s's workflows on %s: %w", repo, base, err)
	}
	merges, err := gh.MergeCommits(ctx)
	if err != nil {
		return fmt.Errorf("reading whether %s allows merge commits: %w", repo, err)
	}
	gate, capable := false, false
	for _, w := range workflows {
		gate = gate || hasGateJob(w)
		capable = capable || hasRepositoryGate(w)
	}
	var fixes []string
	if !gate {
		fixes = append(fixes, fmt.Sprintf("No workflow on %s has a job named invariant/gate, the check the factory merges on. "+
			"Run invariant init in a checkout of %s, and commit the workflow it writes to %s.", base, repo, base))
	}
	if configured && !capable {
		fixes = append(fixes, fmt.Sprintf("Manually upgrade invariant/gate on %s: use invariant init -invariant FULL_COMMIT in a separate directory to generate a pinned template, review and merge its check-repository, factory scope and guarded project/plan ratification steps with your custom workflow, then commit the upgraded workflow to %s. Init does not replace a different workflow without -force.", base, base))
	}
	if !merges {
		fixes = append(fixes, fmt.Sprintf("%s doesn't allow merge commits, and the factory merges with one. Allow them:\n  gh api -X PATCH repos/%s -F allow_merge_commit=true", repo, repo))
	}
	if len(fixes) == 0 {
		return nil
	}
	return fmt.Errorf("the factory can't work on %s yet:\n- %s", repo, strings.Join(fixes, "\n- "))
}

type gateStep struct {
	script      string
	condition   string
	environment string
	shell       string
	ignored     bool
}

// This recognizes literal jobs and run steps, as hasGateJob does. It does
// not evaluate YAML expressions or arbitrary shell programs. Custom steps
// can surround the template's checks; uncertain forms need manual review.
func hasRepositoryGate(workflow string) bool {
	lines := strings.Split(workflow, "\n")
	inJobs, jobIndent, start := false, -1, -1
	check := func(end int) bool {
		return start >= 0 && repositoryGateJob(lines[start:end], jobIndent)
	}
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			if inJobs && check(i) {
				return true
			}
			inJobs, start, jobIndent = text == "jobs:", -1, -1
		} else if inJobs && (jobIndent < 0 || indent == jobIndent) {
			if check(i) {
				return true
			}
			start, jobIndent = i, indent
		}
	}
	return inJobs && check(len(lines))
}

func repositoryGateJob(lines []string, jobIndent int) bool {
	steps, valid := repositoryGateSteps(lines, jobIndent)
	if !valid {
		return false
	}
	// Recognize the reviewed literal scripts rather than guess whether an
	// arbitrary shell program reaches a command or propagates its failure.
	// Custom jobs and steps can coexist with these three required steps.
	template := strings.Split(gate, "\n")
	start := slices.Index(template, "  gate:")
	if start < 0 {
		return false
	}
	required, valid := repositoryGateSteps(template[start:], 2)
	if !valid {
		return false
	}
	for _, command := range []string{"check-repository", "scope", "ratification"} {
		found := false
		for _, want := range required {
			if !gateInvocation(want.script, command) {
				continue
			}
			for _, step := range steps {
				if !step.ignored && step.shell == want.shell && step.script == want.script && gateCondition(step.condition) == gateCondition(want.condition) &&
					gateEnvironment(step.environment) == gateEnvironment(want.environment) {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func gateCondition(value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "${{"), "}}"))
	if value == "true" {
		return ""
	}
	return strings.Join(strings.Fields(value), " ")
}

func gateEnvironment(value string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

func repositoryGateSteps(lines []string, jobIndent int) ([]gateStep, bool) {
	named, enabled, inSteps := false, true, false
	keyIndent, stepIndent := -1, -1
	var steps []gateStep
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if keyIndent < 0 {
			keyIndent = indent
		}
		if indent == keyIndent && !strings.HasPrefix(text, "- ") {
			inSteps = text == "steps:"
			if gateName.MatchString(text) {
				named = true
			}
			key, value, _ := strings.Cut(text, ":")
			if key == "if" && !gateTrue(value) || key == "continue-on-error" && !gateFalse(value) {
				enabled = false
			}
			continue
		}
		if !inSteps || indent <= jobIndent {
			continue
		}
		if strings.HasPrefix(text, "- ") && (stepIndent < 0 || indent == stepIndent) {
			steps = append(steps, gateStep{})
			stepIndent = indent
			text, indent = strings.TrimPrefix(text, "- "), indent+2
		}
		if len(steps) == 0 {
			continue
		}
		step := &steps[len(steps)-1]
		if indent != stepIndent+2 {
			step.environment += text + "\n"
			continue
		}
		key, value, _ := strings.Cut(text, ":")
		value = strings.TrimSpace(value)
		switch key {
		case "if":
			step.condition = value
		case "continue-on-error":
			step.ignored = step.ignored || !gateFalse(value)
		case "shell":
			// The generated script needs Bash's arrays and fail propagation.
			step.shell = value
			step.ignored = step.ignored || value != "bash"
		case "run":
			if value != "|" && value != "|-" {
				step.script = value
				continue
			}
			for i+1 < len(lines) {
				body := lines[i+1]
				trim := strings.TrimSpace(body)
				n := len(body) - len(strings.TrimLeft(body, " "))
				if trim != "" && n <= indent {
					break
				}
				i++
				if trim != "" && !strings.HasPrefix(trim, "#") {
					step.script += trim + "\n"
				}
			}
		case "name", "id", "env":
		default:
			step.ignored = true
		}
	}
	return steps, named && enabled
}

func gateTrue(value string) bool {
	return strings.TrimSpace(value) == "true" || strings.TrimSpace(value) == "${{ true }}"
}

func gateFalse(value string) bool {
	return strings.TrimSpace(value) == "false" || strings.TrimSpace(value) == "${{ false }}"
}

var gateExecutable = regexp.MustCompile(`^(?:invariant|"\$RUNNER_TEMP/invariant")\s+([a-z-]+)(?:\s|$)`)

func gateInvocation(script, command string) bool {
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		match := gateExecutable.FindStringSubmatch(line)
		if len(match) == 2 && match[1] == command && !strings.Contains(line, "||") && !strings.Contains(line, ";") && !strings.HasSuffix(line, "&") {
			return true
		}
	}
	return false
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
