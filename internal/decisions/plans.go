package decisions

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A ratified plan is a decision record of its own (#809). The file a
// ratification commits, .invariant/plans/issue-N.json, holds what a person
// decided for one issue, who ratified it, when, and the exact proposal they
// ratified, and CI checks that ratification on every change. The graph
// holds each one as a node named for its issue, P- and the issue's number,
// so what rests on a plan can be asked for as it can of a decision, and a
// SPEC line that rests on a plan traces to it.

// plansDir is where a repository records its ratified plans, one file an
// issue. It's the plumbing package's LockDir, which can't be imported here.
const plansDir = ".invariant/plans"

var (
	planFile = regexp.MustCompile(`^issue-([0-9]+)\.json$`)
	// A plan is named by its ID, or by the path of its file, as a link in
	// the SPEC or the docs holds it.
	planID   = regexp.MustCompile(`\bP-(\d{4,})\b`)
	planPath = regexp.MustCompile(`\.invariant/plans/issue-([0-9]+)\.json`)
)

// A Plan is one ratified plan, as its file records it.
type Plan struct {
	ID       string // P- and the issue's number
	Issue    int
	Name     string
	Summary  string
	By       string // the ratifier's login
	At       string // when, in RFC 3339
	Proposal string // the proposal's hash
	Path     string // the plan's file, from the repository's root
}

// PlanID is the ID of an issue's plan: P- and the issue's number, at least
// four digits wide, as a decision's is.
func PlanID(issue int) string { return fmt.Sprintf("P-%04d", issue) }

// Text is what the graph says a plan decided: its name and its summary.
func (p Plan) Text() string {
	text := fmt.Sprintf("#%d's ratified plan: %s.", p.Issue, strings.TrimSuffix(strings.TrimSpace(p.Name), "."))
	if s := strings.TrimSpace(p.Summary); s != "" {
		text += " " + s
	}
	return text
}

// ReadPlans reads a checkout's ratified plans, in the order of their issues.
// A plan's file with no ratification, or with no proposal a person ratified,
// isn't a record of anything yet, and is left out.
func ReadPlans(repo Repo) ([]Plan, error) {
	dir := filepath.Join(repo.Dir, filepath.FromSlash(plansDir))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plans []Plan
	for _, e := range entries {
		m := planFile.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		issue, err := strconv.Atoi(m[1])
		if err != nil || issue <= 0 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var f struct {
			Ratified *struct {
				By       string `json:"by"`
				At       string `json:"at"`
				Proposal string `json:"proposal"`
			} `json:"ratified"`
			Plan struct {
				Name    string `json:"name"`
				Summary string `json:"summary"`
			} `json:"plan"`
		}
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("%s/%s: %v", plansDir, e.Name(), err)
		}
		if f.Ratified == nil || !IsPerson("@"+f.Ratified.By) || !proposalPattern.MatchString(f.Ratified.Proposal) {
			continue
		}
		plans = append(plans, Plan{ID: PlanID(issue), Issue: issue, Name: f.Plan.Name, Summary: f.Plan.Summary,
			By: f.Ratified.By, At: f.Ratified.At, Proposal: f.Ratified.Proposal, Path: plansDir + "/" + e.Name()})
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Issue < plans[j].Issue })
	return plans, nil
}

// putPlans writes a checkout's ratified plans into the graph within tx, and
// the citations their words make.
func putPlans(tx *sql.Tx, repo Repo) error {
	plans, err := ReadPlans(repo)
	if err != nil {
		return err
	}
	for _, p := range plans {
		date := p.At
		if len(date) > 10 {
			date = date[:10]
		}
		id := repo.Name + "/" + p.ID
		if _, err := tx.Exec(`INSERT OR REPLACE INTO node (id, kind, project, date, status, who, text, record, proposal) VALUES (?, 'plan', ?, ?, 'ratified', ?, ?, ?, ?)`,
			id, repo.Name, date, "@"+p.By, p.Text(), p.Path, p.Proposal); err != nil {
			return err
		}
	}
	// Every plan is in the graph before any is read for what it cites, so a
	// plan can name a later one.
	for _, p := range plans {
		if err := citeFrom(tx, repo.Name+"/"+p.ID, repo.Name, p.Text()); err != nil {
			return err
		}
	}
	return nil
}

// planIDs is the plans a line of text names by ID, each once, in order.
func planIDs(text string) []string {
	return planMentions(text, planID)
}

// planLinks is the plans a line of text names by ID or by the path of its
// file, each once, in order.
func planLinks(text string) []string {
	return planMentions(text, planID, planPath)
}

func planMentions(text string, forms ...*regexp.Regexp) []string {
	var out []string
	seen := map[string]bool{}
	for _, form := range forms {
		for _, m := range form.FindAllStringSubmatch(text, -1) {
			issue, err := strconv.Atoi(m[1])
			if err != nil || issue <= 0 {
				continue
			}
			if id := PlanID(issue); !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// isPlan says whether a short ID is a plan's.
func isPlan(short string) bool { return strings.HasPrefix(short, "P-") }

// planExists refuses a plan's ID that the checkout holds no ratified plan
// for.
func planExists(repo Repo, short string) error {
	plans, err := ReadPlans(repo)
	if err != nil {
		return err
	}
	for _, p := range plans {
		if p.ID == short {
			return nil
		}
	}
	return fmt.Errorf("there's no ratified plan %s in %s", short, repo.Dir)
}

// PlanCount is how many ratified plans a project has in the store.
func (s *Store) PlanCount(project string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM node WHERE project = ? AND kind = 'plan'`, project).Scan(&n)
	return n, err
}
