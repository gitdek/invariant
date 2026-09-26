package formalize

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Request is everything people have said about an issue: the issue, their
// comments, the forks they've decided, and the draft they asked to revise.
type Request struct {
	Repo     string // owner/name
	Language string // the language the code will be written in
	Issue    int
	Title    string
	Body     string
	Author   string
	Thread   []Message // people's comments, oldest first
	Answers  []Answer  // the forks people decided
	Previous *Proposal // the draft people asked to revise, if any
	Current  *Current  // the project an amendment changes, if any (D-0045)
}

// Current is an existing project as it stands: what an amendment starts
// from.
type Current struct {
	Dir        string
	Manifest   project.Manifest
	Lock       project.Lock
	ModuleText string
}

// ModuleName is the TLA+ module's name, from its file name.
func (c *Current) ModuleName() string {
	return strings.TrimSuffix(filepath.Base(c.Manifest.Module), ".tla")
}

// Previous says where the lock an amendment replaces was ratified.
func (c *Current) Previous() string {
	if r := c.Lock.Ratified; r != nil {
		return fmt.Sprintf("#%d", r.Issue)
	}
	return c.Lock.Decision
}

// Message is one comment a person made.
type Message struct {
	By   string `json:"by"`
	Body string `json:"body"`
}

// Answer is a fork a person decided.
type Answer struct {
	Fork     string `json:"fork"`
	Question string `json:"question"`
	Option   string `json:"option"`
	Says     string `json:"says"`
	By       string `json:"by"`
	Comment  string `json:"comment"` // the URL of the comment that decided it
}

// Markdown renders the request. The agent reads it, and the project keeps it
// as .invariant/request.md.
func (r Request) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nIssue #%d in %s, opened by @%s.\n\n%s\n", r.Title, r.Issue, r.Repo, r.Author, strings.TrimSpace(r.Body))
	if len(r.Answers) > 0 {
		b.WriteString("\n## Decided\n\n")
		for _, a := range r.Answers {
			fmt.Fprintf(&b, "- **%s. %s** %s. %s (decided by @%s: %s)\n", a.Fork, a.Question, a.Option, a.Says, a.By, a.Comment)
		}
	}
	if len(r.Thread) > 0 {
		b.WriteString("\n## Discussion\n")
		for _, m := range r.Thread {
			fmt.Fprintf(&b, "\n**@%s:**\n\n%s\n", m.By, strings.TrimSpace(m.Body))
		}
	}
	return b.String()
}

// Formalizer drafts statements for issues with a coding agent.
type Formalizer struct {
	Backend   synth.Backend
	Binary    string // this invariant binary, which serves the check tool
	CheckRuns int    // the most checks the agent gets
	Timeout   time.Duration
	Toolchain toolchain.Toolchain
}

// Result is how a formalization went.
type Result struct {
	Proposal  *Proposal       `json:"proposal,omitempty"` // forks to ask, a reason the issue is unsupported, or a draft to ratify
	Report    *verify.Report  `json:"report,omitempty"`   // the factory's own check of a draft to ratify
	Problem   string          `json:"problem,omitempty"`  // why there's nothing to ask or ratify, when there isn't
	Changes   *Changes        `json:"changes,omitempty"`  // what an amendment changes
	Usage     synth.Usage     `json:"usage"`
	CheckRuns []synth.GateRun `json:"check_runs,omitempty"`
}

// Formalize runs the agent on a request. Its transcript and draft land in
// out. Whatever the agent says, the factory checks the draft itself.
func (f Formalizer) Formalize(ctx context.Context, req Request, out string) (*Result, error) {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	ws, err := os.MkdirTemp("", "invariant-formalize-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(ws)
	if err := writeFile(filepath.Join(ws, "request.md"), req.Markdown()); err != nil {
		return nil, err
	}
	if c := req.Current; c != nil {
		if err := seedAmendment(ws, c); err != nil {
			return nil, err
		}
	}
	if p := req.Previous; p != nil {
		b, err := json.MarshalIndent(p.Draft, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(ws, "previous", "proposal.json"), string(b)+"\n"); err != nil {
			return nil, err
		}
		if p.ModuleText != "" {
			if err := writeFile(filepath.Join(ws, "previous", p.Module+".tla"), p.ModuleText); err != nil {
				return nil, err
			}
		}
	}
	transcript, err := os.Create(filepath.Join(out, "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer transcript.Close()
	checkLog := filepath.Join(out, "check-runs.jsonl")

	runCtx, cancel := context.WithTimeout(ctx, f.Timeout)
	defer cancel()
	usage, runErr := f.Backend.Run(runCtx, synth.Job{
		Workspace:  ws,
		Prompt:     Prompt(req, f.CheckRuns),
		GateServer: []string{f.Binary, "mcp", "-formalize", "-max-runs", fmt.Sprint(f.CheckRuns), "-log", checkLog, ws},
		Tools:      []string{"check"},
		Transcript: transcript,
	})
	usage.Backend = f.Backend.Name()
	r := &Result{Usage: usage}
	r.CheckRuns, _ = readRuns(checkLog)

	p, report, err := Check(ctx, ws, f.Toolchain)
	if p != nil {
		p.Language = req.Language
	}
	if c := req.Current; p != nil && err == nil {
		if err = p.Amend(c); err == nil && p.Ratifiable() {
			r.Changes = Diff(c, p)
		}
	}
	switch {
	case err != nil && runErr != nil:
		r.Problem = fmt.Sprintf("the agent's run failed (%v), and it left no usable draft: %v", runErr, err)
	case err != nil:
		r.Problem = err.Error()
	default:
		r.Proposal, r.Report = p, report
		if report != nil && !report.Passed {
			r.Problem = verify.Feedback(report)
		}
	}
	for _, name := range []string{"proposal.json"} {
		if b, err := os.ReadFile(filepath.Join(ws, name)); err == nil {
			os.WriteFile(filepath.Join(out, name), b, 0o644)
		}
	}
	if p != nil && p.ModuleText != "" {
		os.WriteFile(filepath.Join(out, p.Module+".tla"), []byte(p.ModuleText), 0o644)
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(filepath.Join(out, "formalization.json"), append(b, '\n'), 0o644)
	return r, nil
}

func readRuns(path string) ([]synth.GateRun, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var runs []synth.GateRun
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var g synth.GateRun
		if json.Unmarshal(scanner.Bytes(), &g) == nil {
			runs = append(runs, g)
		}
	}
	return runs, scanner.Err()
}

// Amend makes a draft an amendment of the project c: it keeps the project's
// name, module, package and language, and records which lock it replaces.
func (p *Proposal) Amend(c *Current) error {
	p.Target = &Target{Dir: c.Dir, Manifest: c.Manifest, Amends: project.ProposalHash(c.Lock.Bounds, c.Lock.Statements), Previous: c.Previous()}
	p.Name, p.Slug, p.Package, p.Language = c.Manifest.Name, slugOf(c.Dir), filepath.Base(c.Manifest.Code), c.Manifest.Language
	if p.Ratifiable() && p.Module != c.ModuleName() {
		return fmt.Errorf("an amendment keeps its module: %s, not %s", c.ModuleName(), p.Module)
	}
	return nil
}

// slugOf is a project directory's name without its number: log-buffer
// for examples/03-log-buffer.
func slugOf(dir string) string {
	base := filepath.Base(dir)
	if i := strings.Index(base, "-"); i > 0 && strings.Trim(base[:i], "0123456789") == "" {
		return base[i+1:]
	}
	return base
}

// seedAmendment starts an amendment's workspace from the project as it is:
// its module, and its ratified statements as a draft for the agent to edit.
func seedAmendment(ws string, c *Current) error {
	d := Draft{Name: c.Manifest.Name, Slug: slugOf(c.Dir), Module: c.ModuleName(), Package: filepath.Base(c.Manifest.Code),
		Bounds: c.Lock.Bounds, Language: c.Manifest.Language}
	for _, s := range c.Lock.Statements {
		s.SHA256 = ""
		d.Statements = append(d.Statements, s)
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(ws, "proposal.json"), string(b)+"\n"); err != nil {
		return err
	}
	return writeFile(filepath.Join(ws, d.Module+".tla"), c.ModuleText)
}
