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
		fixes = append(fixes, fmt.Sprintf("Manually upgrade invariant/gate on %s: use invariant init -invariant FULL_COMMIT in a separate directory to generate a pinned template, review and merge its check-repository, factory scope and guarded project/plan ratification steps with your custom workflow, then commit the upgraded workflow to %s. Init does not replace a different workflow without -force. "+
			"The gate's job must run on push and on every pull request, with no condition, needs, continue-on-error, container, or workflow or job defaults or env, which reach its steps; set environment on your own steps instead.", base, base))
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
	if !repositoryGateTriggers(workflow) {
		return false
	}
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
			key, value, ok := gateKey(text)
			inJobs, start, jobIndent = ok && key == "jobs" && value == "", -1, -1
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
	jobKeys, stepKeys := map[string]bool{}, map[string]bool{}
	jobKey := ""
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
			key, value, ok := gateKey(text)
			if !ok || jobKeys[key] || key == "<<" {
				return nil, false
			}
			jobKeys[key] = true
			jobKey = key
			inSteps = key == "steps" && value == ""
			if key == "name" && gateName.MatchString("name: "+value) {
				named = true
			}
			needs, literalNeeds := gateFlow(value, '[', ']')
			// A job's defaults, environment and container reach its steps as
			// the workflow's do; the gate's own steps set what they need.
			if key == "if" && !gateTrue(value) || key == "continue-on-error" && !gateFalse(value) || key == "needs" && (!literalNeeds || len(needs) != 0) ||
				key == "defaults" || key == "env" || key == "container" {
				enabled = false
			}
			continue
		}
		if !inSteps && (jobKey == "if" || jobKey == "continue-on-error" || jobKey == "needs" || jobKey == "name") {
			return nil, false
		}
		if !inSteps || indent <= jobIndent {
			continue
		}
		if strings.HasPrefix(text, "- ") && (stepIndent < 0 || indent == stepIndent) {
			steps = append(steps, gateStep{})
			stepKeys = map[string]bool{}
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
		key, value, ok := gateKey(text)
		if !ok || stepKeys[key] {
			step.ignored = true
		}
		stepKeys[key] = true
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

// Recognize a small literal YAML surface, without interpreting expressions,
// aliases or executable workflow content. Quoting and spacing a key cannot
// hide a condition, dependency or trigger from preflight.
var gateLiteralKey = regexp.MustCompile(`^(?:([A-Za-z0-9_-]+|<<)|'([A-Za-z0-9_-]+|<<)'|"([A-Za-z0-9_-]+|<<)")\s*:(?:\s+(.*)|$)`)
var gateLiteralName = regexp.MustCompile(`^[A-Za-z0-9_/-]+$`)

func gateKey(text string) (string, string, bool) {
	match := gateLiteralKey.FindStringSubmatch(gateUncomment(text))
	if match == nil {
		return "", "", false
	}
	return match[1] + match[2] + match[3], strings.TrimSpace(match[4]), true
}

func gateUncomment(text string) string {
	quote := byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		} else if c == '\'' || c == '"' {
			quote = c
		} else if c == '#' && (i == 0 || text[i-1] == ' ' || text[i-1] == '\t') {
			return strings.TrimSpace(text[:i])
		}
	}
	return strings.TrimSpace(text)
}

func gateLiteral(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if len(text) >= 2 && (text[0] == '\'' || text[0] == '"') && text[len(text)-1] == text[0] {
		text = text[1 : len(text)-1]
	}
	return text, gateLiteralName.MatchString(text)
}

// Flow lists and maps are split only at their outer commas. Nested values
// still have to pass the event-specific literal checks below.
func gateFlow(text string, open, close byte) ([]string, bool) {
	if len(text) < 2 || text[0] != open || text[len(text)-1] != close {
		return nil, false
	}
	var fields []string
	stack := []byte{close}
	quote, start := byte(0), 1
	for i := 1; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[':
			stack = append(stack, ']')
		case '{':
			stack = append(stack, '}')
		case ']', '}':
			if len(stack) == 0 || stack[len(stack)-1] != c {
				return nil, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 && i != len(text)-1 {
				return nil, false
			}
		}
		if c == ',' && len(stack) == 1 || i == len(text)-1 && len(stack) == 0 {
			field := strings.TrimSpace(text[start:i])
			if field != "" {
				fields = append(fields, field)
			} else if i != len(text)-1 {
				return nil, false
			}
			start = i + 1
		}
	}
	return fields, quote == 0 && len(stack) == 0
}

func gateEmptyEvent(value string) bool {
	fields, literal := gateFlow(value, '{', '}')
	return value == "" || value == "null" || value == "~" || literal && len(fields) == 0
}

func gatePushFilter(key, value string) bool {
	if !slices.Contains([]string{"branches", "branches-ignore", "tags", "tags-ignore", "paths", "paths-ignore"}, key) {
		return false
	}
	fields, ok := gateFlow(value, '[', ']')
	if !ok {
		return false
	}
	for _, field := range fields {
		if _, ok := gateLiteral(field); !ok {
			// Quoted glob patterns are literal too; aliases and expressions
			// do not qualify as reviewable filter values.
			if len(field) < 2 || (field[0] != '\'' && field[0] != '"') || field[len(field)-1] != field[0] ||
				strings.ContainsAny(field[1:len(field)-1], "'\"\\$\r\n") {
				return false
			}
		}
	}
	return true
}

func gateEventValue(name, value string) bool {
	if gateEmptyEvent(value) {
		return true
	}
	if name != "push" {
		return false
	}
	fields, ok := gateFlow(value, '{', '}')
	seen := map[string]bool{}
	for _, field := range fields {
		key, v, literal := gateKey(field)
		if !literal || seen[key] || !gatePushFilter(key, v) {
			return false
		}
		seen[key] = true
	}
	return ok
}

func repositoryGateTriggers(workflow string) bool {
	lines := strings.Split(workflow, "\n")
	start, end, value := -1, len(lines), ""
	keys := map[string]bool{}
	for i, line := range lines {
		text := gateUncomment(line)
		if text == "" || strings.HasPrefix(line, " ") {
			continue
		}
		key, v, ok := gateKey(text)
		if !ok || keys[key] || key == "<<" {
			return false
		}
		// Workflow defaults and environment reach the gate's steps wherever
		// they appear: a default working directory empties their project and
		// plan discovery, and BASH_ENV runs code before each script.
		if key == "defaults" || key == "env" {
			return false
		}
		keys[key] = true
		if start >= 0 && end == len(lines) {
			end = i
		}
		if key == "on" {
			start, value = i, v
		}
	}
	if start < 0 {
		return false
	}
	events := map[string]bool{}
	add := func(name string) bool {
		if events[name] {
			return false
		}
		events[name] = true
		return true
	}
	if value != "" {
		for _, line := range lines[start+1 : end] {
			if gateUncomment(line) != "" {
				return false
			}
		}
		if fields, ok := gateFlow(value, '[', ']'); ok {
			for _, field := range fields {
				name, ok := gateLiteral(field)
				if !ok || !add(name) {
					return false
				}
			}
		} else if fields, ok := gateFlow(value, '{', '}'); ok {
			for _, field := range fields {
				name, v, ok := gateKey(field)
				if !ok || !add(name) || !gateEventValue(name, v) {
					return false
				}
			}
		} else {
			return false
		}
	} else {
		indent, event := -1, ""
		sequence, nested := false, false
		pushKeys := map[string]bool{}
		for _, line := range lines[start+1 : end] {
			text := gateUncomment(line)
			if text == "" {
				continue
			}
			n := len(line) - len(strings.TrimLeft(line, " "))
			if indent < 0 {
				indent = n
				sequence = strings.HasPrefix(text, "- ")
			}
			if sequence {
				name, literal := gateLiteral(strings.TrimPrefix(text, "- "))
				if n != indent || !strings.HasPrefix(text, "- ") || !literal || !add(name) {
					return false
				}
				continue
			}
			if n == indent {
				name, v, ok := gateKey(text)
				if !ok || !add(name) || name == "<<" || !gateEventValue(name, v) {
					return false
				}
				event, nested = name, v == ""
			} else if n < indent || !nested || event == "pull_request" {
				return false
			} else if event == "push" {
				key, v, ok := gateKey(text)
				if !ok || pushKeys[key] || !gatePushFilter(key, v) {
					return false
				}
				pushKeys[key] = true
			}
		}
	}
	return events["push"] && events["pull_request"]
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
			k, _, ok := gateKey(text)
			jobs, job, key = ok && k == "jobs", -1, -1
		case !jobs:
			// Outside jobs, no name is a job's.
		case job < 0 || indent <= job:
			// A job's ID.
			job, key = indent, -1
		case key < 0 || indent == key:
			// One of the job's own keys. Anything deeper is a step's, or a
			// value's.
			key = indent
			k, v, ok := gateKey(text)
			if ok && k == "name" && gateName.MatchString("name: "+v) {
				return true
			}
		}
	}
	return false
}
