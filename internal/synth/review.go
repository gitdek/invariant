package synth

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// Review is a second agent's reading of a driver another agent just wrote,
// with fresh context (D-0082, D-0086). It's an opinion, not evidence: the
// receipt never counts it, and the factory posts it with the pull request.
type Review struct {
	Usage Usage  `json:"usage"`
	Text  string `json:"text"`
	// Of is what it read: "driver", or "explorer" for a Go project's.
	Of string `json:"of,omitempty"`
}

// ReviewDriver has a second agent read project's conformance driver, or a Go
// project's explorer, read only, and report what it skips, what it doesn't
// read back, and whether the manifest's parameters are the sizes the code
// takes. A project with neither gets no review.
func ReviewDriver(ctx context.Context, backend Backend, dir string, transcript string) (*Review, error) {
	p, err := project.Load(dir)
	if err != nil {
		return nil, err
	}
	driver := driverFile(p)
	if driver == "" {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(dir, driver)); err != nil {
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
	// Invariant's harness, as the gate supplies it, for the reviewer to read.
	if name, source := verify.Harness(p.Manifest.Language); name != "" && len(p.Manifest.Existing) == 0 {
		dir := ws
		if p.Manifest.Language == "typescript" {
			dir = filepath.Join(ws, filepath.Dir(p.Manifest.Conformance))
		}
		if err := writeFile(filepath.Join(dir, name), string(source)); err != nil {
			return nil, err
		}
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
	of := "driver"
	if l := p.Manifest.Language; l == "" || l == "go" {
		of = "explorer"
	}
	return &Review{Usage: u, Text: strings.TrimSpace(u.Summary), Of: of}, nil
}

// driverFile is what the review reads: a TypeScript or Python project's
// conformance driver, or a Go project's explorer.
func driverFile(p *project.Project) string {
	if l := p.Manifest.Language; l == "" || l == "go" {
		return filepath.ToSlash(filepath.Join(p.Manifest.Code, "explore.go"))
	}
	return p.Manifest.Conformance
}

func reviewPrompt(p *project.Project) string {
	params := "none"
	if len(p.Manifest.Parameters) > 0 {
		params = "`" + strings.Join(p.Manifest.Parameters, "`, `") + "`"
	}
	noun, name, what := "a conformance driver", "driver", ""
	how := "It explores every state the code can reach, trying every step the model's Next names with Invariant's harness, and the gate checks that no step went untried except where the environment's bounds rule it out. So look hardest at what the gate can't see: states the driver writes from what it expected instead of reading them back from the code, and sizes the code takes that the manifest doesn't name as parameters, which would let the driver stop at a limit the code must enforce."
	if !verify.Explores(p) {
		how = "It samples runs at random, so no check can say it tried every step in every state, and your review is the check of its steps. Look hardest at how it chooses a step: it must choose from every step the model's Next names, never only from those the state allows."
	}
	bounds := "The driver's bounds are constants at its top, named after the model's constants. The gate makes each one size larger and runs the driver again, which ties the driver's sizes to the ones TLC checks."
	done := "A step of the environment alone that can't happen yet, such as a Done before the end, returns the node it was given."
	if l := p.Manifest.Language; l == "" || l == "go" {
		noun, name, what = "an explorer", "explorer", " It drives a Go project's code through the steps of its model."
		how = "Invariant's gate explores the code from `Init()` through `Try`, breadth first, calling `Try` in every state it reaches and recording every step `Try` reports, and it checks that no step went untried except where the environment's bounds rule it out. So look hardest at what the gate can't see: states `Try` reports from what it expected instead of reading them back from the code, refusals it reports without calling the code, and sizes the code takes that the manifest doesn't name as parameters, which would let the explorer stop at a limit the code must enforce."
		if !verify.Explores(p) {
			how = "It has no `Try`, only `Successors`, which returns the states it reaches, so no check can say it tried every step in every state, and your review is the check of its steps. Look hardest at the conditions it tests before calling the code: only the environment's bounds may stop a step."
		}
		bounds = "The explorer's bounds are constants in a `const` block, named after the model's constants. The gate makes each one size larger and explores again, which ties the explorer's sizes to the ones TLC checks."
		done = "A step of the environment alone that can't happen yet, such as a Done before the end, reports the state it was given."
	}
	return fmt.Sprintf(`You're reviewing %s that another agent wrote a moment ago.%s Read it with fresh eyes, and change nothing.

The project is this directory. Its model is `+"`%s`"+`, the TLA+ spec the code must keep, and its Next names every step a person or worker can take. The code is in `+"`%s/`"+`, and the %s is `+"`%s`"+`. The manifest, `+"`.invariant/invariant.json`"+`, names the sizes the code takes as parameters: %s.

%s is evidence only if it tries every step a person or worker could take, wherever they could take it, lets the code refuse what the model rules out, and records every state as the code reports it. %s

Some things are the gate's conventions, so don't report them as problems:

- %s
- The manifest's parameters name only numbers the code takes, such as a capacity. A set of participants the code is made with needs no entry, because the gate never grows a set when it checks the %s's steps.
- %s Trying it there changes nothing, and that's right: skipping it would be the problem.%s

Look for:

1. A step the %s skips because of the state the code is in. Only the environment's bounds may stop a step, such as a sixth call when five is the bound.
2. A state the %s records from what it expected, rather than reading it back from the code.
3. A step of Next the %s never tries, or tries with fewer arguments than Next can pass it.
4. A size the code takes, such as a capacity it's made with, that's missing from the parameters, or a parameter that's really the environment's bound.

Reply with a short review in Markdown, with no heading. Say first, in one sentence, whether you found a problem. For each problem, name the file and line, what's wrong, and why it matters. If you found none, say what you checked. Claim nothing you didn't read.`,
		noun, what, p.Manifest.Module, p.Manifest.Code, name, driverFile(p), params, strings.ToUpper(noun[:1])+noun[1:], how, bounds, name, done, harnessNote(p), name, name, name)
}

// harnessNote tells the reviewer where Invariant's harness is, for a driver
// built on it: the review copies it in, as the gate does when the driver runs.
func harnessNote(p *project.Project) string {
	if name, _ := verify.Harness(p.Manifest.Language); name != "" && len(p.Manifest.Existing) == 0 {
		return "\n- Invariant's harness, `" + name + "`, is in this directory for you to read. It isn't the project's: the gate writes its own copy in when the driver runs."
	}
	return ""
}

// ReviewTranscript is where a review's transcript goes, beside synthesis's.
func ReviewTranscript(out string) string { return filepath.Join(out, "review.jsonl") }
