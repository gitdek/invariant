// Package dashboard serves a live view of Invariant (D-0049): what the
// factory is working on, each project's evidence, the roadmap and the
// decisions. It reads GitHub through gh, CI's receipts, the repository at
// main and TLC's own state graphs, and it changes nothing anywhere.
package dashboard

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

// Server keeps the dashboard's data fresh and serves it with the page.
type Server struct {
	// Repos are the repositories the page shows. The first is Invariant's
	// own: the decisions, the roadmap and the commits come from it. The
	// others show their factory work only (D-0054).
	Repos  []*Repo
	Branch string      // the branch the factory merges into
	Runner *tlc.Runner // draws state graphs; nil leaves them out
	Cache  string      // where drawn graphs are kept between runs
	Work   string      // the watchers' work directory, whose logs show a step's checks as they run
	Every  time.Duration
	Log    func(format string, args ...any)

	mu      sync.RWMutex
	state   []byte // the latest snapshot, gzipped JSON
	graphs  map[string][]byte
	drawing map[string]bool
	failed  map[string]time.Time // graphs TLC couldn't draw, and when
	pending chan graphJob
}

// Repo is one repository the page shows.
type Repo struct {
	Name   string // owner/name
	GitHub github.Client
	Status string // its watcher's status file
	src    sources
}

// Short is the repository's name without its owner.
func (r *Repo) Short() string { return path.Base(r.Name) }

// sources is the last good read of everything the snapshot is built from,
// so one source failing leaves the rest fresh.
type sources struct {
	issues   []github.Issue
	comments map[int]cachedComments
	commits  []github.Commit
	runs     []github.Run
	receipts *receiptSet
	projects []projectSource
	files    map[string][]byte // by commit and path; a commit's files never change
	docsAt   string            // the commit docs were read at
	log      string
	readme   string
	prd      string
	merges   map[int]mergeCheck
}

type cachedComments struct {
	updated  string
	comments []github.Comment
}

type mergeCheck struct {
	sha   string
	green bool
}

type receiptSet struct {
	run     github.Run
	reports map[string]*verify.Report           // by project directory name
	traces  map[string]map[string]tlc.TraceFile // by project directory name, then trace file
}

type projectSource struct {
	dir      string
	manifest project.Manifest
	lock     project.Lock
	key      string            // identifies the model: its module text, spec and bounds
	files    map[string][]byte // the model's .tla files, for drawing its graph
	module   string
}

type graphJob struct {
	key    string
	source projectSource
	report *verify.Report
	traces map[string]tlc.TraceFile
}

// Snapshot is everything the page draws, as of GeneratedAt.
type Snapshot struct {
	Repo        string      `json:"repo"`
	GeneratedAt time.Time   `json:"generatedAt"`
	Factory     Watcher     `json:"factory"`
	Now         Now         `json:"now"`
	Issues      []Issue     `json:"issues"`
	Repos       []RepoState `json:"repos"`
	Main        *MainState  `json:"main,omitempty"`
	Projects    []Project   `json:"projects"`
	Receipts    *RunRef     `json:"receipts,omitempty"` // the CI run the receipts come from
	Decisions   Decisions   `json:"decisions"`
	Slices      []Slice     `json:"slices"`
	Totals      Totals      `json:"totals"`
	Who         Who         `json:"who"`
	Activity    []Event     `json:"activity"`
	Stale       []string    `json:"stale,omitempty"` // sources that couldn't be read this time
}

// RepoState is one repository's line on the page.
type RepoState struct {
	Name     string  `json:"name"`
	Short    string  `json:"short"`
	Primary  bool    `json:"primary"`
	Factory  Watcher `json:"factory"`
	Gate     *RunRef `json:"gate,omitempty"`     // the newest gate run on the branch
	Receipts *RunRef `json:"receipts,omitempty"` // the run its receipts come from
	Projects int     `json:"projects"`
}

// Watcher is the factory's watcher, as its status file tells it.
type Watcher struct {
	Running   bool       `json:"running"`
	Started   *time.Time `json:"started,omitempty"`
	Heartbeat *time.Time `json:"heartbeat,omitempty"`
	Issue     int        `json:"issue,omitempty"`
	Doing     string     `json:"doing,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	// Runs are the checks the current step has run so far: TLC checks of a
	// draft while formalizing, or gate runs while building.
	Runs     []RunMark `json:"runs,omitempty"`
	RunsKind string    `json:"runsKind,omitempty"` // check or gate
}

// RunMark is one check the factory ran during its current step.
type RunMark struct {
	Run    int  `json:"run"`
	Passed bool `json:"passed"`
}

// Now is the one line at the top of the page: what the factory is doing,
// or waiting for.
type Now struct {
	Headline  string     `json:"headline"`
	Detail    string     `json:"detail,omitempty"`
	Issue     int        `json:"issue,omitempty"`
	Repo      string     `json:"repo,omitempty"`
	Stage     string     `json:"stage"`               // idle, or the issue's stage
	WaitingOn string     `json:"waitingOn,omitempty"` // factory, people or ci
	Since     *time.Time `json:"since,omitempty"`
}

type MainState struct {
	SHA   string    `json:"sha"`
	Title string    `json:"title"`
	By    string    `json:"by"`
	At    time.Time `json:"at"`
	Gate  *RunRef   `json:"gate,omitempty"`
}

type RunRef struct {
	ID         int64     `json:"id"`
	SHA        string    `json:"sha"`
	Title      string    `json:"title,omitempty"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion,omitempty"`
	Started    time.Time `json:"started"`
	Updated    time.Time `json:"updated"`
}

// Project is one project's evidence, from CI's receipt for it.
type Project struct {
	Repo        string            `json:"repo"`
	Dir         string            `json:"dir"`
	Name        string            `json:"name"`
	Language    string            `json:"language"`
	Assurance   string            `json:"assurance"` // proved, tested in every state, or tested
	Passed      bool              `json:"passed"`
	States      int64             `json:"states"`
	Depth       int               `json:"depth"`
	Visited     int64             `json:"visited,omitempty"` // states the code reached, for conformance and agreement
	Statements  []Statement       `json:"statements"`
	Witnesses   []Witness         `json:"witnesses"`
	Bugs        []Bug             `json:"bugs"`
	Verifier    string            `json:"verifier,omitempty"`
	Verified    int               `json:"verified,omitempty"`
	Unverified  []string          `json:"unverified,omitempty"`
	Bounds      map[string]string `json:"bounds"`
	Ratified    *Provenance       `json:"ratified,omitempty"`
	Decision    string            `json:"decision,omitempty"`
	Fingerprint string            `json:"fingerprint"`
	Model       string            `json:"model"`           // shared by projects that check the same model
	Graph       bool              `json:"graph,omitempty"` // its state graph is ready
	Factory     bool              `json:"factory"`         // built by the factory from an issue
}

type Statement struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Says string `json:"says"`
}

type Witness struct {
	Name    string `json:"name"`
	Says    string `json:"says"`
	Reached bool   `json:"reached"`
	Steps   int    `json:"steps,omitempty"`
}

type Bug struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Says     string `json:"says"`
	Violated string `json:"violated,omitempty"`
	Steps    int    `json:"steps,omitempty"`
	Caught   bool   `json:"caught"`
}

type Provenance struct {
	By       string `json:"by"`
	At       string `json:"at"`
	Issue    int    `json:"issue"`
	Previous string `json:"previous,omitempty"` // what it amended: "#3", or a decision
}

type Decisions struct {
	Total  int            `json:"total"`
	Status map[string]int `json:"status"`
	Who    map[string]int `json:"who"`
	Latest []Decision     `json:"latest"`
}

// Totals are the big numbers. Projects that check the same model count
// its states, statements and bugs once.
type Totals struct {
	Projects      int   `json:"projects"`
	Models        int   `json:"models"`
	States        int64 `json:"states"`
	Statements    int   `json:"statements"`
	Bugs          int   `json:"bugs"`
	BugsCaught    int   `json:"bugsCaught"`
	Witnesses     int   `json:"witnesses"`
	Proved        int   `json:"proved"` // functions Gobra or Nagini verified
	Merged        int   `json:"merged"` // pull requests the factory merged
	MergesChecked int   `json:"mergesChecked"`
	BadMerges     int   `json:"badMerges"` // merged without CI's gate passing on the merged head
	LocksChecked  int   `json:"locksChecked"`
	BadLocks      int   `json:"badLocks"` // a lock that isn't the proposal its ratification names
	Decisions     int   `json:"decisions"`
}

// Who is who builds what (D-0048): people decide, Invariant proves, and
// coding agents build the machinery.
type Who struct {
	Ratified      int `json:"ratified"`      // decisions @gitdek ratified
	Answered      int `json:"answered"`      // forks people decided on issues
	Ratifications int `json:"ratifications"` // statements ratified on issues
	Merged        int `json:"merged"`        // pull requests the factory merged
	BotCommits    int `json:"botCommits"`    // commits on the branch by the factory's bot
	Built         int `json:"built"`         // statements in projects the factory built
	Logged        int `json:"logged"`        // decisions agents made while building: the decided ones
	Slices        int `json:"slices"`        // slices done
	Commits       int `json:"commits"`       // commits on the branch by people's sessions
	Itself        int `json:"itself"`        // Invariant's own parts it proves, the projects under factory/
}

// Start reads everything once, then keeps it fresh until ctx ends.
func (s *Server) Start(ctx context.Context) {
	s.graphs, s.drawing, s.failed = map[string][]byte{}, map[string]bool{}, map[string]time.Time{}
	for _, r := range s.Repos {
		r.src.comments, r.src.files, r.src.merges = map[int]cachedComments{}, map[string][]byte{}, map[int]mergeCheck{}
	}
	s.pending = make(chan graphJob, 64)
	go s.drawGraphs(ctx)
	go func() {
		for {
			start := time.Now()
			if err := s.refresh(ctx); err != nil {
				s.logf("refresh: %v", err)
			}
			s.logf("refreshed in %s", time.Since(start).Round(time.Millisecond))
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.Every):
			}
		}
	}()
}

func (s *Server) refresh(ctx context.Context) error {
	var stale []string
	for i, r := range s.Repos {
		fail := func(what string, err error) {
			s.logf("%s: %s: %v", r.Name, what, err)
			stale = append(stale, r.Short()+" "+what)
		}
		if all, err := r.GitHub.Issues(ctx, ""); err != nil {
			fail("issues", err)
		} else {
			var issues []github.Issue
			for _, is := range all {
				if theFactorys(is) {
					issues = append(issues, is)
				}
			}
			r.src.issues = issues
			for _, is := range issues {
				if c, ok := r.src.comments[is.Number]; ok && c.updated == is.UpdatedAt {
					continue
				}
				comments, err := r.GitHub.Comments(ctx, is.Number)
				if err != nil {
					fail(fmt.Sprintf("comments on #%d", is.Number), err)
					continue
				}
				r.src.comments[is.Number] = cachedComments{updated: is.UpdatedAt, comments: comments}
			}
		}
		// Only Invariant's own commits are shown. Another repository's
		// commits are its own business.
		if i == 0 {
			if commits, err := r.GitHub.Commits(ctx, s.Branch, 100); err != nil {
				fail("commits", err)
			} else {
				r.src.commits = commits
			}
		}
		if runs, err := r.GitHub.Runs(ctx, s.Branch, 30); err != nil {
			fail("CI runs", err)
		} else {
			r.src.runs = runs
		}
		if err := s.readReceipts(ctx, r); err != nil {
			fail("receipts", err)
		}
		if set := r.src.receipts; set != nil {
			for _, p := range r.src.projects {
				name := path.Base(p.dir)
				s.queueGraph(graphJob{key: p.key, source: p, report: set.reports[name], traces: set.traces[name]})
			}
		}
		if i == 0 {
			if err := s.readDocs(ctx, r); err != nil {
				fail("docs", err)
			}
		}
		s.checkMerges(ctx, r)
	}

	snap := s.assemble(time.Now().UTC())
	snap.Stale = stale
	js, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	gz, err := gzipped(js)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.state = gz
	s.mu.Unlock()
	return nil
}

// readReceipts takes the receipts from the newest gate run on the branch
// that passed, and the projects as they were at that commit.
func (s *Server) readReceipts(ctx context.Context, r *Repo) error {
	var run *github.Run
	for i, run2 := range r.src.runs {
		if run2.Name == "gate" && run2.Status == "completed" && run2.Conclusion == "success" {
			run = &r.src.runs[i]
			break
		}
	}
	if run == nil {
		return nil // no gate run on the branch has passed yet, so there are no receipts
	}
	if r.src.receipts != nil && r.src.receipts.run.ID == run.ID {
		return nil
	}
	arts, err := r.GitHub.Artifacts(ctx, run.ID)
	if err != nil {
		return err
	}
	var id int64
	for _, a := range arts {
		if a.Name == "receipts" && !a.Expired {
			id = a.ID
		}
	}
	if id == 0 {
		// A gate run with no projects to check has nothing to receipt.
		r.src.receipts, r.src.projects = &receiptSet{run: *run, reports: map[string]*verify.Report{}, traces: map[string]map[string]tlc.TraceFile{}}, nil
		return nil
	}
	raw, err := r.GitHub.Download(ctx, id)
	if err != nil {
		return err
	}
	set, err := readReceiptZip(raw)
	if err != nil {
		return err
	}
	set.run = *run
	projects, err := s.readProjects(ctx, r, run.HeadSHA)
	if err != nil {
		return err
	}
	r.src.receipts, r.src.projects = set, projects
	return nil
}

func readReceiptZip(raw []byte) (*receiptSet, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	set := &receiptSet{reports: map[string]*verify.Report{}, traces: map[string]map[string]tlc.TraceFile{}}
	for _, f := range zr.File {
		parts := strings.Split(strings.Trim(f.Name, "/"), "/")
		if len(parts) < 2 || f.UncompressedSize64 > 8<<20 {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		_, err = b.ReadFrom(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		name := parts[0]
		switch {
		case len(parts) == 2 && parts[1] == "receipt.json":
			var r verify.Report
			if err := json.Unmarshal(b.Bytes(), &r); err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			set.reports[name] = &r
		case len(parts) == 3 && parts[1] == "traces" && strings.HasSuffix(parts[2], ".json"):
			var t tlc.TraceFile
			if err := json.Unmarshal(b.Bytes(), &t); err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			if set.traces[name] == nil {
				set.traces[name] = map[string]tlc.TraceFile{}
			}
			set.traces[name][parts[2]] = t
		}
	}
	if len(set.reports) == 0 {
		return nil, errors.New("the receipts hold no receipt.json")
	}
	return set, nil
}

// readProjects finds every project at a commit, the way CI does, and reads
// what's needed to draw its model.
func (s *Server) readProjects(ctx context.Context, r *Repo, sha string) ([]projectSource, error) {
	tree, err := r.GitHub.Tree(ctx, sha)
	if err != nil {
		return nil, err
	}
	var out []projectSource
	for _, e := range tree {
		if e.Type != "blob" || !strings.HasSuffix(e.Path, "/.invariant/invariant.json") {
			continue
		}
		dir := strings.TrimSuffix(e.Path, "/.invariant/invariant.json")
		p := projectSource{dir: dir, files: map[string][]byte{}}
		raw, err := s.file(ctx, r, sha, e.Path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &p.manifest); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Path, err)
		}
		if raw, err = s.file(ctx, r, sha, dir+"/.invariant/ratified.lock"); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &p.lock); err != nil {
			return nil, fmt.Errorf("%s's lock: %w", dir, err)
		}
		modulePath := path.Join(dir, p.manifest.Module)
		p.module = strings.TrimSuffix(path.Base(modulePath), ".tla")
		specs := path.Dir(modulePath) + "/"
		h := sha256.New()
		for _, f := range tree {
			if f.Type == "blob" && strings.HasPrefix(f.Path, specs) && !strings.Contains(strings.TrimPrefix(f.Path, specs), "/") && strings.HasSuffix(f.Path, ".tla") {
				text, err := s.file(ctx, r, sha, f.Path)
				if err != nil {
					return nil, err
				}
				p.files[path.Base(f.Path)] = text
				fmt.Fprintf(h, "%s\n%s\n", path.Base(f.Path), text)
			}
		}
		spec := ""
		for _, st := range p.lock.Statements {
			if st.Kind == project.Spec {
				spec = st.Name
			}
		}
		bounds, _ := json.Marshal(p.lock.Bounds)
		fmt.Fprintf(h, "%s\n%s\n%s\n", p.module, spec, bounds)
		p.key = hex.EncodeToString(h.Sum(nil))[:16]
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out, nil
}

// readDocs reads the decision log, the README and the PRD at the branch's
// newest commit.
func (s *Server) readDocs(ctx context.Context, r *Repo) error {
	if len(r.src.commits) == 0 {
		return errors.New("no commits read")
	}
	sha := r.src.commits[0].SHA
	if sha == r.src.docsAt {
		return nil
	}
	var err error
	var log, readme, prd []byte
	if log, err = s.file(ctx, r, sha, "decisions/log.md"); err != nil {
		return err
	}
	if readme, err = s.file(ctx, r, sha, "README.md"); err != nil {
		return err
	}
	if prd, err = s.file(ctx, r, sha, "docs/PRD.md"); err != nil && !errors.Is(err, github.ErrNotFound) {
		return err
	}
	r.src.log, r.src.readme, r.src.prd, r.src.docsAt = string(log), string(readme), string(prd), sha
	return nil
}

func (s *Server) file(ctx context.Context, r *Repo, sha, p string) ([]byte, error) {
	key := r.Name + "@" + sha + ":" + p
	if b, ok := r.src.files[key]; ok {
		return b, nil
	}
	b, err := r.GitHub.File(ctx, p, sha)
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", p, sha[:7], err)
	}
	r.src.files[key] = b
	return b, nil
}

// checkMerges confirms, for each pull request the factory merged, that
// CI's gate passed on the head it merged. A merged pull request never
// changes, so each is checked once.
func (s *Server) checkMerges(ctx context.Context, r *Repo) {
	for _, is := range r.src.issues {
		l := Lane(is, r.src.comments[is.Number].comments, "", time.Now())
		if l.Stage != StageMerged || l.PR == 0 {
			continue
		}
		if _, ok := r.src.merges[l.PR]; ok {
			continue
		}
		pr, err := r.GitHub.PullRequest(ctx, l.PR)
		if err != nil || !pr.Merged {
			continue
		}
		runs, err := r.GitHub.CheckRuns(ctx, pr.Head.SHA, "invariant/gate")
		if err != nil {
			continue
		}
		green := false
		for _, r := range runs {
			if r.Status == "completed" && r.Conclusion == "success" {
				green = true
			}
		}
		r.src.merges[l.PR] = mergeCheck{sha: pr.Head.SHA, green: green}
	}
}

func (s *Server) assemble(now time.Time) Snapshot {
	primary := s.Repos[0]
	snap := Snapshot{Repo: primary.Name, GeneratedAt: now, Issues: []Issue{}, Repos: []RepoState{}, Projects: []Project{}, Activity: []Event{}}
	s.mu.RLock()
	ready := map[string]bool{}
	for k := range s.graphs {
		ready[k] = true
	}
	s.mu.RUnlock()
	models := map[string]bool{}
	pins, built := map[string]bool{}, map[string]bool{}
	var watchers []namedWatcher
	for i, r := range s.Repos {
		rs := RepoState{Name: r.Name, Short: r.Short(), Primary: i == 0}
		if st, err := ReadStatus(r.Status); err == nil && st.PID != 0 {
			w := Watcher{Running: st.Running(), Issue: st.Issue, Doing: st.Doing, Since: st.Since}
			started, beat := st.Started, st.Heartbeat
			w.Started, w.Heartbeat = &started, &beat
			if !w.Running {
				w.Issue, w.Doing, w.Since = 0, "", nil
			}
			w.Runs, w.RunsKind = s.runsOf(r.Name, w)
			rs.Factory = w
		}
		watchers = append(watchers, namedWatcher{repo: r.Name, w: rs.Factory})

		for _, is := range r.src.issues {
			l := Lane(is, r.src.comments[is.Number].comments, "", now)
			l.Repo = r.Name
			for k := range l.Events {
				l.Events[k].Repo = r.Name
			}
			if l.Stage == StageMerged && l.PR != 0 {
				snap.Totals.Merged++
				if m, ok := r.src.merges[l.PR]; ok {
					snap.Totals.MergesChecked++
					if !m.green {
						snap.Totals.BadMerges++
					}
				}
			}
			snap.Issues = append(snap.Issues, l)
		}
		for _, run := range r.src.runs {
			if run.Name == "gate" {
				rs.Gate = runRef(run)
				break
			}
		}
		if set := r.src.receipts; set != nil {
			rs.Receipts = runRef(set.run)
			if i == 0 {
				snap.Receipts = rs.Receipts
			}
			for _, ps := range r.src.projects {
				rep := set.reports[path.Base(ps.dir)]
				if rep == nil {
					continue
				}
				p := projectOf(ps, rep)
				p.Repo = r.Name
				p.Graph = ready[ps.key] || s.cached(ps.key)
				snap.Projects = append(snap.Projects, p)
				rs.Projects++
				t := &snap.Totals
				t.Projects++
				t.Proved += p.Verified
				if rep.Ratified != nil {
					t.LocksChecked++
					if rep.Proposal != rep.Ratified.Proposal {
						t.BadLocks++
					}
				}
				for _, pin := range rep.Pins {
					pins[pin.Want] = true
					if p.Factory {
						built[pin.Want] = true
					}
				}
				if models[ps.key] {
					continue
				}
				models[ps.key] = true
				t.Models++
				t.States += p.States
				t.Witnesses += len(p.Witnesses)
				for _, b := range p.Bugs {
					t.Bugs++
					if b.Caught {
						t.BugsCaught++
					}
				}
			}
		}
		for k, run := range r.src.runs {
			if k >= 12 || run.Name != "gate" {
				continue
			}
			e := Event{At: parseTime(run.UpdatedAt), Repo: r.Name, Who: WhoCI, Kind: run.Status, Text: "gate " + runWords(run) + " on " + run.HeadSHA[:7]}
			if run.Conclusion != "" {
				e.Kind = run.Conclusion
			}
			snap.Activity = append(snap.Activity, e)
		}
		snap.Repos = append(snap.Repos, rs)
	}
	snap.Totals.Statements, snap.Who.Built = len(pins), len(built)
	for _, p := range snap.Projects {
		if (p.Repo == "" || p.Repo == primary.Name) && strings.HasPrefix(p.Dir, "factory/") {
			snap.Who.Itself++
		}
	}
	sort.SliceStable(snap.Issues, func(i, j int) bool { return snap.Issues[i].Opened.After(snap.Issues[j].Opened) })
	snap.Factory = combined(snap.Repos)
	snap.Now = nowLine(watchers, snap.Issues, primary.Name)

	// An issue without a language label was written in the project's
	// language: the repository's default when it was made.
	langs := map[string]string{}
	for _, p := range snap.Projects {
		langs[p.Repo+"/"+p.Dir] = p.Language
	}
	for i := range snap.Issues {
		if l := &snap.Issues[i]; l.Language == "" {
			l.Language = langs[l.Repo+"/"+l.Project]
		}
	}

	if len(primary.src.commits) > 0 {
		c := primary.src.commits[0]
		m := &MainState{SHA: c.SHA, Title: firstLine(c.Commit.Message), By: author(c), At: parseTime(c.Commit.Committer.Date)}
		for _, r := range primary.src.runs {
			if r.Name == "gate" && r.HeadSHA == c.SHA {
				m.Gate = runRef(r)
				break
			}
		}
		snap.Main = m
	}

	decisions := ParseLog(primary.src.log)
	snap.Decisions = Decisions{Total: len(decisions), Status: map[string]int{}, Who: map[string]int{}, Latest: []Decision{}}
	for i := len(decisions) - 1; i >= 0; i-- {
		d := decisions[i]
		snap.Decisions.Status[d.Status]++
		snap.Decisions.Who[d.Who]++
		if len(snap.Decisions.Latest) < 12 {
			snap.Decisions.Latest = append(snap.Decisions.Latest, d)
		}
		if d.Who == "@gitdek" && d.Status == "ratified" {
			snap.Who.Ratified++
		}
		if d.Status == "decided" {
			snap.Who.Logged++
		}
	}
	snap.Totals.Decisions = len(decisions)
	snap.Slices = ParseSlices(primary.src.readme, primary.src.prd)
	for _, sl := range snap.Slices {
		if sl.Status == "done" {
			snap.Who.Slices++
		}
	}

	for _, l := range snap.Issues {
		for _, e := range l.Events {
			if e.Who == WhoPerson && e.Kind == factory.Ratify {
				snap.Who.Ratifications++
			}
			snap.Activity = append(snap.Activity, e)
		}
		snap.Who.Answered += l.Answers
		if l.Stage == StageMerged {
			snap.Who.Merged++
		}
	}
	for i, c := range primary.src.commits {
		by := author(c)
		if strings.HasSuffix(by, "[bot]") {
			snap.Who.BotCommits++
		} else {
			snap.Who.Commits++
		}
		if i < 12 {
			snap.Activity = append(snap.Activity, Event{At: parseTime(c.Commit.Committer.Date), Repo: primary.Name, Who: "commit", By: by, Kind: c.SHA[:7], Text: firstLine(c.Commit.Message)})
		}
	}
	sort.SliceStable(snap.Activity, func(i, j int) bool { return snap.Activity[i].At.After(snap.Activity[j].At) })
	if len(snap.Activity) > 40 {
		snap.Activity = snap.Activity[:40]
	}
	return snap
}

// runsOf reads the checks a watcher's current step has run, from its own
// log in the work directory: the newest formalize or build directory for
// the issue it's on.
func (s *Server) runsOf(repo string, w Watcher) ([]RunMark, string) {
	if s.Work == "" || !w.Running || w.Issue == 0 {
		return nil, ""
	}
	var kind, prefix, file string
	switch w.Doing {
	case "building":
		kind, prefix, file = "gate", "build-", "gate-runs.jsonl"
	case "formalizing", "answering":
		kind, prefix, file = "check", "formalize-", "check-runs.jsonl"
	default:
		return nil, ""
	}
	dirs, _ := filepath.Glob(filepath.Join(s.Work, filepath.FromSlash(repo), "issues", fmt.Sprintf("issue-%d", w.Issue), prefix+"*"))
	if len(dirs) == 0 {
		return nil, kind
	}
	sort.Strings(dirs)
	b, err := os.ReadFile(filepath.Join(dirs[len(dirs)-1], file))
	if err != nil {
		return nil, kind
	}
	var runs []RunMark
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var r RunMark
		if json.Unmarshal([]byte(line), &r) == nil && r.Run > 0 {
			runs = append(runs, r)
		}
	}
	return runs, kind
}

// namedWatcher is a repository's watcher.
type namedWatcher struct {
	repo string
	w    Watcher
}

// combined is the factory as the page's top bar shows it: on when any
// watcher runs, and working on whatever one of them is working on.
func combined(repos []RepoState) Watcher {
	var on *Watcher
	for i := range repos {
		w := repos[i].Factory
		switch {
		case w.Running && w.Doing != "":
			return w
		case w.Running && on == nil:
			on = &repos[i].Factory
		}
	}
	if on != nil {
		return *on
	}
	if len(repos) > 0 {
		return repos[0].Factory
	}
	return Watcher{}
}

// theFactorys says whether an issue is one the factory takes: it carries
// the invariant label or one of the factory's own, or it opens with a
// /invariant solve line.
func theFactorys(is github.Issue) bool {
	for _, l := range is.Labels {
		if l.Name == factory.LabelTrigger || strings.HasPrefix(l.Name, factory.LabelTrigger+":") {
			return true
		}
	}
	for _, c := range factory.ParseCommands(is.Body) {
		if c.Verb == factory.Solve {
			return true
		}
	}
	return false
}

// issueRef names an issue: #5 in Invariant's own repository, and
// copythis-ad#1 in another.
func issueRef(repo, primary string, n int) string {
	if repo == "" || repo == primary {
		return fmt.Sprintf("#%d", n)
	}
	return fmt.Sprintf("%s#%d", path.Base(repo), n)
}

func projectOf(ps projectSource, r *verify.Report) Project {
	p := Project{
		Dir: ps.dir, Name: r.Project, Language: ps.manifest.Language, Passed: r.Passed,
		States: r.Design.DistinctStates, Depth: r.Design.Depth, Bounds: r.Bounds, Decision: r.Decision,
		Fingerprint: r.Fingerprint, Model: ps.key, Statements: []Statement{}, Witnesses: []Witness{}, Bugs: []Bug{},
	}
	if p.Language == "" {
		p.Language = "go"
	}
	for _, pin := range r.Pins {
		p.Statements = append(p.Statements, Statement{Name: pin.Name, Kind: pin.Kind, Says: pin.Says})
	}
	for _, w := range r.Witnesses {
		p.Witnesses = append(p.Witnesses, Witness{Name: w.Name, Says: w.Says, Reached: w.Reached, Steps: w.Steps})
	}
	for _, b := range r.Bugs {
		p.Bugs = append(p.Bugs, Bug{Name: b.Name, Label: b.Label, Says: b.Says, Violated: b.Violated, Steps: b.Steps, Caught: b.Caught})
	}
	switch {
	case r.Assurance == "proved":
		p.Assurance = "proved"
	case r.Conformance != nil && r.Conformance.Exhaustive && int64(r.Conformance.States) == r.Conformance.ModelStates:
		p.Assurance = "tested in every state"
	default:
		p.Assurance = "tested"
	}
	if a := r.Agreement; a != nil {
		p.Visited = a.States
	}
	if c := r.Conformance; c != nil {
		p.Visited = int64(c.States)
	}
	if c := r.Code; c != nil {
		p.Verifier, p.Unverified = c.Verifier, c.Unverified
		if c.Passed {
			p.Verified = len(c.Functions)
		}
	}
	if rt := r.Ratified; rt != nil {
		p.Ratified = &Provenance{By: rt.By, At: rt.At, Issue: rt.Issue, Previous: rt.Previous}
		p.Factory = rt.Issue > 0
	}
	return p
}

// nowLine says what the factory is doing, or what it's waiting for.
func nowLine(ws []namedWatcher, issues []Issue, primary string) Now {
	find := func(repo string, n int) *Issue {
		for i := range issues {
			if issues[i].Number == n && (issues[i].Repo == repo || issues[i].Repo == "") {
				return &issues[i]
			}
		}
		return nil
	}
	running := false
	for _, nw := range ws {
		w := nw.w
		running = running || w.Running
		if !w.Running || w.Issue == 0 || w.Doing == "" {
			continue
		}
		ref := issueRef(nw.repo, primary, w.Issue)
		n := Now{Issue: w.Issue, Repo: nw.repo, Since: w.Since, WaitingOn: WhoFactory, Stage: StageBuilding}
		switch w.Doing {
		case "formalizing":
			n.Headline, n.Stage = "Drafting what must be true for "+ref, StageQueued
		case "answering":
			n.Headline, n.Stage = "Drafting again with the answers on "+ref, StageAsking
		case "ratifying":
			n.Headline, n.Stage = "Committing the ratification on "+ref, StageRatifying
		default:
			n.Headline = "Writing the code for " + ref
		}
		if is := find(nw.repo, w.Issue); is != nil {
			n.Detail = is.Title
		}
		return n
	}
	order := []string{StageBuilding, StageGate, StageQueued, StageRatifying, StageAsking, StageReview}
	for _, stage := range order {
		for _, is := range issues {
			if !is.Open || is.Stage != stage {
				continue
			}
			ref := issueRef(is.Repo, primary, is.Number)
			n := Now{Issue: is.Number, Repo: is.Repo, Detail: is.Title, Stage: stage}
			if k := len(is.Spans); k > 0 && is.Spans[k-1].Open {
				from := is.Spans[k-1].From
				n.Since, n.WaitingOn = &from, is.Spans[k-1].Who
			}
			switch stage {
			case StageBuilding:
				n.Headline = "Writing the code for " + ref
			case StageGate:
				n.Headline = "Waiting for CI's gate on " + issueRef(is.Repo, primary, is.PR)
			case StageQueued:
				n.Headline = "Picking up " + ref
			case StageRatifying:
				n.Headline = "Waiting for a person to ratify " + ref
			case StageAsking:
				n.Headline = "Waiting for answers on " + ref
			case StageReview:
				n.Headline = ref + " needs a person"
			}
			return n
		}
	}
	n := Now{Stage: "idle", Headline: "Idle, and watching for issues"}
	if !running {
		n.Headline = "Idle. The factory is switched off"
	}
	var last *Issue
	for i := range issues {
		if is := &issues[i]; is.Stage == StageMerged && is.Closed != nil && (last == nil || is.Closed.After(*last.Closed)) {
			last = is
		}
	}
	if last != nil {
		n.Issue, n.Repo, n.Detail, n.Since = last.Number, last.Repo, last.Title, last.Closed
	}
	return n
}

func runRef(r github.Run) *RunRef {
	return &RunRef{ID: r.ID, SHA: r.HeadSHA, Title: r.DisplayTitle, Status: r.Status, Conclusion: r.Conclusion,
		Started: parseTime(r.StartedAt), Updated: parseTime(r.UpdatedAt)}
}

func runWords(r github.Run) string {
	switch {
	case r.Status != "completed":
		return "running"
	case r.Conclusion == "success":
		return "passed"
	case r.Conclusion == "failure":
		return "failed"
	}
	return r.Conclusion
}

func author(c github.Commit) string {
	if c.Author != nil && c.Author.Login != "" {
		return c.Author.Login
	}
	return "someone"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// queueGraph asks for a model's state graph to be drawn, once.
func (s *Server) queueGraph(j graphJob) {
	if s.Runner == nil || j.report == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.graphs[j.key] != nil || s.drawing[j.key] || time.Since(s.failed[j.key]) < 10*time.Minute {
		return
	}
	if b, err := os.ReadFile(s.graphFile(j.key)); err == nil {
		s.graphs[j.key] = b
		return
	}
	s.drawing[j.key] = true
	select {
	case s.pending <- j:
	default:
		delete(s.drawing, j.key)
	}
}

// drawGraphs has TLC dump each model's state graph, one at a time, and
// finds each known bug's counterexample on it.
func (s *Server) drawGraphs(ctx context.Context) {
	for {
		var j graphJob
		select {
		case <-ctx.Done():
			return
		case j = <-s.pending:
		}
		start := time.Now()
		gz, err := s.draw(ctx, j)
		s.mu.Lock()
		delete(s.drawing, j.key)
		if err == nil {
			s.graphs[j.key] = gz
		} else {
			s.failed[j.key] = time.Now()
		}
		s.mu.Unlock()
		if err != nil {
			s.logf("graph for %s: %v", j.source.dir, err)
			continue
		}
		s.logf("drew %s's state graph in %s", j.source.dir, time.Since(start).Round(time.Millisecond))
	}
}

func (s *Server) draw(ctx context.Context, j graphJob) ([]byte, error) {
	dir, err := os.MkdirTemp("", "invariant-graph-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	for name, text := range j.source.files {
		if err := os.WriteFile(filepath.Join(dir, name), text, 0o644); err != nil {
			return nil, err
		}
	}
	spec := ""
	for _, st := range j.source.lock.Statements {
		if st.Kind == project.Spec {
			spec = st.Name
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	dot, err := s.Runner.Dump(ctx, dir, j.source.module, tlc.Config{Specification: spec, Constants: j.source.lock.Bounds})
	if err != nil {
		return nil, err
	}
	g, err := ParseDot(dot)
	if err != nil {
		return nil, err
	}
	for _, b := range j.report.Bugs {
		t, ok := j.traces[b.Trace]
		if !ok || !b.Caught {
			continue
		}
		p, action, escape := g.Trace(t)
		if len(p) == 0 {
			continue
		}
		g.Bugs = append(g.Bugs, BugPath{Name: b.Name, Label: b.Label, Says: b.Says, Violated: b.Violated, Steps: b.Steps, Path: p, Action: action, Escape: escape})
	}
	g.Compact()
	js, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	gz, err := gzipped(js)
	if err != nil {
		return nil, err
	}
	if s.Cache != "" {
		if err := os.MkdirAll(filepath.Dir(s.graphFile(j.key)), 0o755); err == nil {
			_ = os.WriteFile(s.graphFile(j.key), gz, 0o644)
		}
	}
	return gz, nil
}

func (s *Server) graphFile(key string) string {
	return filepath.Join(s.Cache, "graphs", key+".json.gz")
}

func (s *Server) cached(key string) bool {
	if s.Cache == "" {
		return false
	}
	_, err := os.Stat(s.graphFile(key))
	return err == nil
}

func gzipped(b []byte) ([]byte, error) {
	var out bytes.Buffer
	w, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if _, err := w.Write(b); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

func gunzip(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out bytes.Buffer
	_, err = out.ReadFrom(r)
	return out.Bytes(), err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
