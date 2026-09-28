package synth

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/project"
)

// Review is a second agent's reading of a driver another agent just wrote,
// with fresh context (D-0082, D-0086). It's an opinion, not evidence: the
// receipt never counts it, and the factory posts it with the pull request.
type Review struct {
	Usage Usage  `json:"usage"`
	Text  string `json:"text"`
}

// ReviewDriver has a second agent read project's conformance driver, read
// only, and report what it skips, what it doesn't read back, and whether the
// manifest's parameters are the sizes the code takes. A project with no
// driver gets no review.
func ReviewDriver(ctx context.Context, backend Backend, dir string, transcript string) (*Review, error) {
	p, err := project.Load(dir)
	if err != nil {
		return nil, err
	}
	if p.Manifest.Conformance == "" {
		return nil, nil
	}
	// The reviewer reads a copy, outside the home directory its tools may
	// not reach, as synthesis's agent does.
	ws, err := os.MkdirTemp("", "invariant-review-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(ws)
	if err := filepath.WalkDir(dir, func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(dir, file)
		return copyFile(file, filepath.Join(ws, rel))
	}); err != nil {
		return nil, err
	}
	f, err := os.Create(transcript)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	u, err := backend.Run(ctx, Job{Workspace: ws, Prompt: reviewPrompt(p), ReadOnly: true, Transcript: f})
	if err != nil {
		return nil, fmt.Errorf("the review: %w", err)
	}
	return &Review{Usage: u, Text: strings.TrimSpace(u.Summary)}, nil
}

func reviewPrompt(p *project.Project) string {
	params := "none"
	if len(p.Manifest.Parameters) > 0 {
		params = "`" + strings.Join(p.Manifest.Parameters, "`, `") + "`"
	}
	how := "It explores every state the code can reach, trying every step the model's Next names with Invariant's harness, and the gate checks that no step went untried except where the environment's bounds rule it out. So look hardest at what the gate can't see: states the driver writes from what it expected instead of reading them back from the code, and sizes the code takes that the manifest doesn't name as parameters, which would let the driver stop at a limit the code must enforce."
	if !p.Manifest.Exhaustive {
		how = "It samples runs at random, so no check can say it tried every step in every state, and your review is the check of its steps. Look hardest at how it chooses a step: it must choose from every step the model's Next names, never only from those the state allows."
	}
	return fmt.Sprintf(`You're reviewing a conformance driver that another agent wrote a moment ago. Read it with fresh eyes, and change nothing.

The project is this directory. Its model is `+"`%s`"+`, the TLA+ spec the code must keep, and its Next names every step a person or worker can take. The code is in `+"`%s/`"+`, and the driver is `+"`%s`"+`. The manifest, `+"`.invariant/invariant.json`"+`, names the sizes the code takes as parameters: %s.

A driver is evidence only if it tries every step a person or worker could take, wherever they could take it, lets the code refuse what the model rules out, and records every state as the code reports it. %s

Look for:

1. A step the driver skips because of the state the code is in. Only the environment's bounds may stop a step, such as a sixth call when five is the bound.
2. A state the driver records from what it expected, rather than reading it back from the code.
3. A step of Next the driver never tries, or tries with fewer arguments than Next can pass it.
4. A size the code takes, such as a capacity it's made with, that's missing from the parameters, or a parameter that's really the environment's bound.

Reply with a short review in Markdown, with no heading. Say first, in one sentence, whether you found a problem. For each problem, name the file and line, what's wrong, and why it matters. If you found none, say what you checked. Claim nothing you didn't read.`,
		p.Manifest.Module, p.Manifest.Code, p.Manifest.Conformance, params, how)
}

// ReviewTranscript is where a review's transcript goes, beside synthesis's.
func ReviewTranscript(out string) string { return filepath.Join(out, "review.jsonl") }
