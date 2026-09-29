package formalize

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// A PRD becomes a plan of issues (D-0105, #112). A writer's /invariant plan
// on an issue that holds or links a PRD has an agent draft the issues that
// carry it out, in order, each one modeled or plumbing. People ratify the
// plan by its hash, as they ratify statements.

// MaxIssues is the most issues a plan holds (D-0105).
const MaxIssues = 10

// IssuePlan is a PRD's proposal: the issues that carry it out, in the order
// the factory takes them.
type IssuePlan struct {
	Name    string         `json:"name"`    // the plan, in a few plain words
	Summary string         `json:"summary"` // what the issues do together, and why
	Issues  []PlannedIssue `json:"issues"`
}

// PlannedIssue is one issue in a plan, as it will be opened.
type PlannedIssue struct {
	Title string `json:"title"`
	Body  string `json:"body"` // the whole issue, with the line that says how the factory takes it
}

// Hash identifies the plan: its name, its summary, and each issue's title
// and body, in order. A person's ratification names it.
func (p *IssuePlan) Hash() string {
	b, _ := json.Marshal(IssuePlan{Name: p.Name, Summary: p.Summary, Issues: p.Issues})
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// MaxPlanText is how long a plan's name, summary, titles and bodies may be,
// together. Its proposal post shows all of them, and the post's marker holds
// them again, compressed, so a plan within this fits in one comment whatever
// its text. A longer one would be cut off at GitHub's limit, its ratify
// command first, while its hash still pinned what no one could see.
const MaxPlanText = 24000

// textLen is how long the plan's name, summary, titles and bodies are.
func (p *IssuePlan) textLen() int {
	n := len(p.Name) + len(p.Summary)
	for _, is := range p.Issues {
		n += len(is.Title) + len(is.Body)
	}
	return n
}

// Validate checks a plan as its draft left it: a name, a summary, and 1 to
// MaxIssues issues. Each has a title and a whole body that says how the
// factory takes it: modeled, with a Project: line naming a clean directory
// in the repository, or plumbing, with a Kind: plumbing line and no code to
// check, and never both. No body carries an /invariant command: the factory
// takes each issue itself.
func (p *IssuePlan) Validate() error {
	switch {
	case strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Summary) == "":
		return errors.New("the plan needs a name and a summary")
	case len(p.Issues) == 0:
		return errors.New("the plan has no issues")
	case len(p.Issues) > MaxIssues:
		return fmt.Errorf("the plan has %d issues, and a plan holds at most %d: plan the first %d, and say in the summary what's left", len(p.Issues), MaxIssues, MaxIssues)
	case strings.Contains(p.Name+p.Summary, "<!--"):
		return errors.New("the plan's name or summary has an HTML comment, which its proposal wouldn't show though its hash pins it, so it has none")
	}
	if n := p.textLen(); n > MaxPlanText {
		return fmt.Errorf("the plan's name, summary, titles and bodies come to %d characters, and its proposal has to fit in one comment, so they come to at most %d: shorten the bodies, or plan fewer issues and say in the summary what's left", n, MaxPlanText)
	}
	for i, is := range p.Issues {
		if err := is.validate(); err != nil {
			return fmt.Errorf("issue %d: %w", i+1, err)
		}
	}
	return nil
}

func (is PlannedIssue) validate() error {
	switch {
	case strings.TrimSpace(is.Title) == "":
		return errors.New("it has no title")
	case strings.ContainsAny(is.Title, "\r\n"):
		return errors.New("its title is more than one line")
	case strings.TrimSpace(is.Body) == "":
		return errors.New("it has no body")
	case strings.Contains(is.Title+is.Body, "<!--"):
		// A person ratifies what the proposal shows, and GitHub shows no
		// HTML comment. One in the proposal could also pass for its marker.
		return errors.New("it has an HTML comment, which its proposal wouldn't show though its hash pins it, so it has none")
	}
	if line := commandLine(is.Body); line != "" {
		return fmt.Errorf("its body has the command %q, and the factory takes each issue itself, so no body carries an /invariant command", line)
	}
	kind, project, code := issueLines(is.Body)
	switch {
	case kind == "plumbing" && project != "":
		return fmt.Errorf("it's both modeled, with the Project: line %q, and plumbing, with a Kind: plumbing line, and it can only be one", project)
	case kind == "plumbing" && len(code) > 0:
		return errors.New("it's plumbing, which names no code to check, so it has no Code: line")
	case kind == "plumbing":
		return nil
	case project == "":
		return errors.New("it's neither modeled, with a Project: line naming its directory, nor plumbing, with a Kind: plumbing line")
	}
	if problem := badProject(project); problem != "" {
		return fmt.Errorf("its Project: line names %q, which %s", project, problem)
	}
	return nil
}

// issueLines reads the lines of an issue's body that say how the factory
// takes it, as the factory reads them: the first Kind: and Project: lines,
// and every Code: line, outside code blocks.
func issueLines(body string) (kind, project string, code []string) {
	fenced, kinded, projected := false, false, false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		switch {
		case strings.EqualFold(name, "kind") && !kinded:
			kind, kinded = strings.ToLower(strings.Trim(value, "`")), true
		case strings.EqualFold(name, "project") && !projected:
			project, projected = strings.Trim(value, "`"), true
		case strings.EqualFold(name, "code"):
			if p := strings.Trim(value, "`/"); p != "" {
				code = append(code, p)
			}
		}
	}
	return kind, project, code
}

// commandLine finds a line in a body that's an /invariant command: a line of
// its own, possibly in backticks, outside code blocks and quotes. It finds
// one whatever its verb, so a command added later can't hide in a plan.
func commandLine(body string) string {
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(line, ">") {
			continue
		}
		if f := strings.Fields(strings.Trim(line, "`")); len(f) >= 2 && f[0] == "/invariant" {
			return line
		}
	}
	return ""
}

// badProject says what's wrong with the directory a Project: line names, or
// "" if nothing is. A trailing slash is fine.
func badProject(dir string) string {
	d := strings.TrimRight(dir, "/")
	switch {
	case d == "" || path.IsAbs(d) || path.Clean(d) != d || d == "." || d == ".." || strings.HasPrefix(d, "../"):
		return "isn't a clean path inside the repository"
	case d == ".git" || strings.HasPrefix(d, ".git/"):
		return "is git's own"
	case d == ".github" || strings.HasPrefix(d, ".github/"):
		return "is CI's own, which the factory never touches"
	case strings.ContainsAny(d, " \t"):
		return "has a space in it"
	}
	return ""
}

// ReadIssues reads the plan of issues an agent left in its workspace, in
// issues.json, and checks it.
func ReadIssues(ws string) (*IssuePlan, error) {
	b, err := os.ReadFile(filepath.Join(ws, "issues.json"))
	if err != nil {
		return nil, fmt.Errorf("the draft has no issues.json: %w", err)
	}
	var p IssuePlan
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("issues.json: %w", err)
	}
	return &p, p.Validate()
}

// PlanIssues drafts a PRD's plan of issues with the agent (#112): the
// issues that carry it out, in order, or the forks it can't settle. repo is
// a checkout of the base branch, which the agent reads, the PRD with it when
// the issue names it there. Its transcript and draft land in out.
func (f Formalizer) PlanIssues(ctx context.Context, req Request, repo, out string) (*Result, error) {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	ws, err := os.MkdirTemp("", "invariant-issues-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(ws)
	if err := writeFile(filepath.Join(ws, "request.md"), req.Markdown()); err != nil {
		return nil, err
	}
	if err := plumbing.CopyCheckout(ctx, repo, filepath.Join(ws, "repo")); err != nil {
		return nil, err
	}
	if p := req.Previous; p != nil && p.IssuePlan != nil {
		b, err := json.MarshalIndent(p.IssuePlan, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(ws, "previous", "issues.json"), string(b)+"\n"); err != nil {
			return nil, err
		}
	}
	transcript, err := os.Create(filepath.Join(out, "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer transcript.Close()
	checkLog := filepath.Join(out, "check-runs.jsonl")
	runCtx, cancel := context.WithTimeout(ctx, f.Timeout)
	usage, runErr := f.Backend.Run(runCtx, synth.Job{
		Workspace:  ws,
		Prompt:     IssuesPrompt(req, f.CheckRuns),
		GateServer: []string{f.Binary, "mcp", "-issues", "-max-runs", fmt.Sprint(f.CheckRuns), "-log", checkLog, ws},
		Tools:      []string{"check"},
		Transcript: transcript,
	})
	cancel()
	usage.Backend = f.Backend.Name()
	r := &Result{Usage: usage}
	r.CheckRuns, _ = readRuns(checkLog)
	defer func() {
		for _, name := range []string{"issues.json", "proposal.json"} {
			if b, err := os.ReadFile(filepath.Join(ws, name)); err == nil {
				os.WriteFile(filepath.Join(out, name), b, 0o644)
			}
		}
		b, _ := json.MarshalIndent(r, "", "  ")
		os.WriteFile(filepath.Join(out, "formalization.json"), append(b, '\n'), 0o644)
	}()
	fail := func(problem string) (*Result, error) {
		if runErr != nil {
			problem = fmt.Sprintf("the agent's run failed (%v), and it left no usable plan: %s", runErr, problem)
		}
		r.Problem = problem
		return r, nil
	}
	if _, err := os.Stat(filepath.Join(ws, "issues.json")); err != nil {
		// No plan: forks to ask, or why the factory can't take the issue.
		p, err := Read(ws)
		switch {
		case err != nil:
			return fail(err.Error())
		case p.Unsupported == "" && len(p.Forks) == 0:
			return fail("a PRD's draft is issues.json, or proposal.json with forks to ask")
		}
		r.Proposal = p
		return r, nil
	}
	// The factory checks the plan itself, whatever the agent says.
	plan, err := ReadIssues(ws)
	if err != nil {
		return fail(err.Error())
	}
	r.Proposal = &Proposal{Draft: Draft{Name: plan.Name}, IssuePlan: plan, Hash: plan.Hash()}
	return r, nil
}

// IssuesPrompt is the task for the agent that plans a PRD's issues.
func IssuesPrompt(req Request, checks int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You're planning the issues that carry out a PRD in this repository, for issue #%d. A writer asked for a plan of issues: the changes that together do what the PRD asks, each one an issue of its own, taken one at a time, in order. A person ratifies your plan by its hash, and each issue is then opened exactly as you write it, so the plan is the contract.

request.md holds the issue, what's been decided, and the discussion. The issue holds the PRD, or says where it is. repo/ is the repository as it is on the base branch. Read the PRD, the code it touches, and AGENTS.md for the repository's rules. Don't change anything in repo/: it's there to read.

# Write the plan

issues.json:

`+"```json"+`
{
  "name": "the plan, in a few plain words",
  "summary": "What the issues do together, and why, in two to five plain sentences. It's what the person ratifies.",
  "issues": [
    {"title": "Serve the decision graph as JSON", "body": "The dashboard serves every decision and its edges at /api/graph.json, for the page to draw.\n\nKind: plumbing"},
    {"title": "Prove how the graph's layout settles", "body": "The layout moves each node toward its neighbors until nothing moves, and no two nodes overlap.\n\nProject: examples/09-graph-layout"}
  ]
}
`+"```"+`

- **issues** are 1 to %d, in the order they're taken. Each is opened once the one before it has merged, so it can build on the ones before it, and never on one after. Keep each small enough to review as one pull request. If the PRD needs more than %d, plan the first %d, and say in the summary what's left.
- Each **title** is one line.
- Each **body** is the whole issue: what changes and why, complete enough that someone who hasn't read the PRD can take it on, with what's been decided that it rests on. A line of its own says how the factory takes it:
  - **Modeled**, for a state machine whose rules are worth proving: a `+"`Project: <directory>`"+` line naming where its project goes, or the existing project it changes. The directory is a clean path inside the repository, and not under .github/. People ratify statements for it, and its code is proved or checked against them.
  - **Plumbing**, for everything else: a `+"`Kind: plumbing`"+` line, and no `+"`Code:`"+` line. People ratify a plan and acceptance tests for it, and its code is tested, not proved (D-0105).
  - Never both.
- No body has an `+"`/invariant`"+` command on a line of its own. The factory takes each issue itself.

# Ask when you must

If the PRD allows materially different plans, don't guess. Write proposal.json with forks instead of issues.json, and people will answer:

`+"```json"+`
{"forks": [{"id": "F1", "question": "…?", "options": [{"id": "A", "says": "…"}, {"id": "B", "says": "…"}]}]}
`+"```"+`

Ask only what changes the plan: which issues there are, what each one does, or their order. Each issue asks its own questions once it's opened, so leave those to it. Don't ask about names or code structure: choose those yourself. If the issue isn't a PRD the factory can plan, write proposal.json with `+"`unsupported`"+` set to one sentence saying why.

# Check your plan

The `+"`check`"+` tool reads issues.json and checks that the plan holds together: its name and summary, how many issues it has, and each issue's title and body. You have %d checks, so reread your file before each. When the check passes, you're done. Then reply with a two-sentence summary.
`, req.Issue, MaxIssues, MaxIssues, MaxIssues, checks)
	if req.Previous != nil && req.Previous.IssuePlan != nil {
		b.WriteString("\n# The plan people asked to revise\n\nprevious/issues.json is the plan before. Keep what the discussion didn't ask to change.\n")
	}
	return b.String()
}
