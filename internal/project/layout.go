package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gitdek/invariant/internal/tla"
)

// A statement's pin hashes its canonical text, which collapses whitespace,
// and TLA+ reads a bulleted list of /\ or \/ by the columns its items start
// in, so re-indenting a ratified statement could change what TLC checks and
// keep its pin. A lock pins each statement's layout too (#814): a ratified
// layout pin, which the proposal hashes, or, for a lock ratified before
// layout pins, a migrated one, added once from the model at the commit that
// recorded its ratification, which the proposal leaves out and CI holds to
// that commit.

// LayoutState says how a lock pins its statements' layouts.
type LayoutState int

const (
	// LayoutUnpinned is a lock ratified before layout pins that hasn't been
	// migrated: the gate checks its pins as before and says its layout isn't
	// pinned yet.
	LayoutUnpinned LayoutState = iota
	// LayoutRatified is a lock whose proposal hashes every statement's
	// layout pin, so a person ratified the layout with the text.
	LayoutRatified
	// LayoutMigrated is a lock ratified before layout pins whose layout pins
	// were added by migration, from the model at the commit that recorded
	// its ratification.
	LayoutMigrated
)

func (s LayoutState) String() string {
	switch s {
	case LayoutRatified:
		return "ratified"
	case LayoutMigrated:
		return "migrated"
	}
	return "not pinned yet"
}

// LayoutPin is the statement's layout pin, ratified or migrated, or "" when
// its lock pins no layout yet.
func (s Statement) LayoutPin() string {
	if s.Layout != "" {
		return s.Layout
	}
	return s.MigratedLayout
}

// LayoutState says how the lock pins its statements' layouts. A lock Validate
// accepts holds one kind of layout pin for every statement, or none.
func (l Lock) LayoutState() LayoutState {
	for _, s := range l.Statements {
		switch {
		case s.Layout != "":
			return LayoutRatified
		case s.MigratedLayout != "":
			return LayoutMigrated
		}
	}
	return LayoutUnpinned
}

var layoutHash = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// validLayouts holds a lock to one kind of layout pin for every statement,
// or none: a lock ratified with layout pins can't leave a statement's out,
// and a migration adds one to every statement.
func validLayouts(l Lock) error {
	state := l.LayoutState()
	for _, s := range l.Statements {
		switch {
		case s.Layout != "" && s.MigratedLayout != "":
			return fmt.Errorf("%s: statement %s has a ratified layout pin and a migrated one; a lock holds one kind", lockFile, s.Name)
		case s.LayoutPin() != "" && !layoutHash.MatchString(s.LayoutPin()):
			return fmt.Errorf("%s: statement %s's layout pin %q isn't a sha256: hash", lockFile, s.Name, s.LayoutPin())
		case state == LayoutRatified && s.Layout == "", state == LayoutMigrated && s.MigratedLayout == "":
			return fmt.Errorf("%s: statement %s has no %s layout pin, and the lock pins every other statement's layout; a lock pins every statement's layout, or none", lockFile, s.Name, state)
		}
	}
	return nil
}

// LayoutPins returns the layout pin of each of the lock's statements in the
// model, by name.
func (p *Project) LayoutPins(model *tla.Model) (map[string]string, error) {
	pins := map[string]string{}
	for _, s := range p.Lock.Statements {
		h, err := model.LayoutHash(s.Name, Model)
		if err != nil {
			return nil, err
		}
		pins[s.Name] = h
	}
	return pins, nil
}

// A history is the git repository a project is in, from the project's point
// of view: its top level, and the project's directory relative to it.
type history struct {
	ctx     context.Context
	root    string
	prefix  string // the project's directory relative to root, with a slash at its end, or "" for the root
	shallow bool
}

// ErrNoHistory says a project isn't in a git repository whose history can
// say where its lock was ratified.
var ErrNoHistory = errors.New("the project isn't in a git checkout")

func historyOf(ctx context.Context, dir string) (*history, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel", "--show-prefix", "--is-shallow-repository").Output()
	if err != nil {
		return nil, ErrNoHistory
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		return nil, fmt.Errorf("git rev-parse said %q", out)
	}
	return &history{ctx: ctx, root: lines[0], prefix: lines[1], shallow: lines[2] == "true"}, nil
}

func (h *history) git(args ...string) ([]byte, error) {
	cmd := exec.CommandContext(h.ctx, "git", append([]string{"-C", h.root}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// show is a file of the project as commit has it.
func (h *history) show(commit, rel string) ([]byte, error) {
	return h.git("show", commit+":"+h.prefix+rel)
}

// A lockHistory is every version of a project's lock that the history
// reachable from HEAD holds: each commit that changed the lock, whatever
// branch it was on, and the lock as each of them and each of their parents
// has it. A version on any branch counts, so merging a branch that kept an
// older lock can't hide one, and nothing a pull request adds to the history
// changes which commits recorded a lock's ratification before it.
type lockHistory struct {
	commits []string            // newest first
	parents map[string][]string // each commit's parents
	locks   map[string]*Lock    // the lock as each commit and parent has it, or nil
}

var errShallow = errors.New("the repository's history is shallow, so it may not hold the commits that recorded the lock's ratification; fetch its whole history, as with fetch-depth: 0")

func (h *history) lockHistory() (*lockHistory, error) {
	if h.shallow {
		return nil, errShallow
	}
	lh := &lockHistory{parents: map[string][]string{}, locks: map[string]*Lock{}}
	if _, err := h.git("rev-parse", "-q", "--verify", "HEAD^{commit}"); err != nil {
		// A repository with no commit yet holds no version of the lock.
		return lh, nil
	}
	out, err := h.git("log", "--full-history", "--topo-order", "--format=%H %P", "--", h.prefix+lockFile)
	if err != nil {
		return nil, err
	}
	var revs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		lh.commits = append(lh.commits, fields[0])
		lh.parents[fields[0]] = fields[1:]
		for _, rev := range fields {
			if !seen[rev] {
				seen[rev] = true
				revs = append(revs, rev)
			}
		}
	}
	if len(revs) == 0 {
		return lh, nil
	}
	// One git process reads the lock as every commit and parent has it.
	var in bytes.Buffer
	for _, rev := range revs {
		fmt.Fprintf(&in, "%s:%s%s\n", rev, h.prefix, lockFile)
	}
	cmd := exec.CommandContext(h.ctx, "git", "-C", h.root, "cat-file", "--batch")
	cmd.Stdin = &in
	batch, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("reading the lock's history: %w", err)
	}
	for _, rev := range revs {
		header, rest, ok := bytes.Cut(batch, []byte("\n"))
		if !ok {
			return nil, fmt.Errorf("reading the lock at %s: git's answer ended early", short(rev))
		}
		fields := strings.Fields(string(header))
		if len(fields) != 3 || fields[1] != "blob" {
			// The lock isn't there at rev: missing, or not a file.
			batch = rest
			continue
		}
		var size int
		if _, err := fmt.Sscan(fields[2], &size); err != nil || size+1 > len(rest) {
			return nil, fmt.Errorf("reading the lock at %s: git said %q", short(rev), header)
		}
		var l Lock
		if json.Unmarshal(rest[:size], &l) == nil {
			lh.locks[rev] = &l
		}
		batch = rest[size+1:]
	}
	return lh, nil
}

// holds says whether the lock as rev has it holds proposal.
func (lh *lockHistory) holds(rev, proposal string) bool {
	l := lh.locks[rev]
	return l != nil && ProposalHash(l.Bounds, l.Statements) == proposal
}

// holding lists the commits whose lock holds proposal, newest first.
func (lh *lockHistory) holding(proposal string) []string {
	var out []string
	for _, c := range lh.commits {
		if lh.holds(c, proposal) {
			out = append(out, c)
		}
	}
	return out
}

// recorded lists the commits that recorded proposal in the lock: each holds
// it, and no parent of it does. Usually there's one, the commit that wrote
// the lock as it was ratified; a lock changed and changed back records it
// again.
func (lh *lockHistory) recorded(proposal string) []string {
	var out []string
	for _, c := range lh.commits {
		if !lh.holds(c, proposal) {
			continue
		}
		again := false
		for _, p := range lh.parents[c] {
			again = again || lh.holds(p, proposal)
		}
		if !again {
			out = append(out, c)
		}
	}
	return out
}

// RatificationCommits are the commits that recorded the proposal the lock in
// dir holds, in the history of the repository dir is in: each holds the lock
// with that proposal, and no parent of it does. The first is the newest.
// Commits that changed the lock and kept its proposal, such as the one that
// migrated its layout pins, record nothing. It returns none when the
// history doesn't hold the proposal, as for a lock not committed yet, and
// ErrNoHistory outside a git checkout.
func RatificationCommits(ctx context.Context, dir string, l Lock) ([]string, error) {
	h, err := historyOf(ctx, dir)
	if err != nil {
		return nil, err
	}
	lh, err := h.lockHistory()
	if err != nil {
		return nil, err
	}
	return lh.recorded(ProposalHash(l.Bounds, l.Statements)), nil
}

// ModelAt reads the model of the project in dir as commit has it: its
// module, where commit's manifest says it is, with the modules beside it.
func ModelAt(ctx context.Context, dir, commit string) (*tla.Model, error) {
	h, err := historyOf(ctx, dir)
	if err != nil {
		return nil, err
	}
	return h.modelAt(commit)
}

func (h *history) modelAt(commit string) (*tla.Model, error) {
	b, err := h.show(commit, manifestFile)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s at %s: %w", manifestFile, short(commit), err)
	}
	if m.Module == "" || path.IsAbs(m.Module) || path.Clean(m.Module) != m.Module || strings.HasPrefix(m.Module, "../") {
		return nil, fmt.Errorf("%s at %s names module %q, which isn't a path inside the project", manifestFile, short(commit), m.Module)
	}
	src, err := h.show(commit, m.Module)
	if err != nil {
		return nil, err
	}
	dir, base := path.Dir(h.prefix+m.Module), path.Base(m.Module)
	out, err := h.git("ls-tree", "-z", commit, "--", dir+"/")
	if err != nil {
		return nil, err
	}
	includes := map[string]string{}
	for _, entry := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		meta, file, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		// Only a regular file is a module TLC reads; the checkout's
		// Siblings follows a link, but a commit holds a link's target's
		// name, not its text.
		if !ok || len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			continue
		}
		name := path.Base(file)
		if !strings.HasSuffix(name, ".tla") || name == base {
			continue
		}
		text, err := h.git("cat-file", "blob", fields[2])
		if err != nil {
			return nil, err
		}
		includes[name] = string(text)
	}
	return tla.Read(string(src), includes)
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// layoutsAt computes the layout pin of each of the lock's statements from
// the model as commit has it, by name. It refuses when that model doesn't
// hold every statement as the lock pins it: its layout then isn't the
// layout of what was ratified.
func (h *history) layoutsAt(l Lock, commit string) (map[string]string, error) {
	model, err := h.modelAt(commit)
	if err != nil {
		return nil, fmt.Errorf("the model at %s, which recorded the lock's ratification: %w", short(commit), err)
	}
	pins := map[string]string{}
	for _, s := range l.Statements {
		pin, err := model.PinHash(s.Name, Model)
		if err != nil {
			return nil, fmt.Errorf("the model at %s, which recorded the lock's ratification: %s: %w", short(commit), s.Name, err)
		}
		if pin != s.SHA256 {
			return nil, fmt.Errorf("the model at %s, which recorded the lock's ratification, doesn't hold %s as the lock pins it, so its layout isn't the ratified one", short(commit), s.Name)
		}
		if pins[s.Name], err = model.LayoutHash(s.Name, Model); err != nil {
			return nil, err
		}
	}
	return pins, nil
}

// ratifiedLayouts computes the layout pin of each of the lock's statements
// from the model at each commit that recorded its proposal, which must all
// agree, and returns them by name, with those commits. It's what a migration
// records and what CI holds a migrated layout pin to.
func (h *history) ratifiedLayouts(lh *lockHistory, l Lock) ([]string, map[string]string, error) {
	commits := lh.recorded(ProposalHash(l.Bounds, l.Statements))
	if len(commits) == 0 {
		return nil, nil, fmt.Errorf("the repository's history doesn't hold the lock's proposal, so it can't say which commit recorded its ratification; commit the lock as it was ratified first")
	}
	var pins map[string]string
	for i, c := range commits {
		at, err := h.layoutsAt(l, c)
		if err != nil {
			return nil, nil, err
		}
		if i > 0 {
			for name, pin := range pins {
				if at[name] != pin {
					return nil, nil, fmt.Errorf("%s and %s each recorded the lock's proposal, and they lay out %s differently, so its ratified layout isn't one layout; only a new ratification can pin it", short(commits[0]), short(c), name)
				}
			}
		}
		pins = at
	}
	return commits, pins, nil
}

// RatifiedLayouts computes the layout pin of each of the lock's statements
// from the model at the commits that recorded its ratification, by name, as
// a migration records them and CI holds a migrated layout pin to them. It
// returns those commits, the newest first.
func (p *Project) RatifiedLayouts(ctx context.Context) ([]string, map[string]string, error) {
	h, err := historyOf(ctx, p.Dir)
	if err != nil {
		return nil, nil, err
	}
	lh, err := h.lockHistory()
	if err != nil {
		return nil, nil, err
	}
	return h.ratifiedLayouts(lh, p.Lock)
}

// MigrateLayouts gives a lock ratified before layout pins the layout pin of
// each of its statements, from the model at the commit that recorded its
// ratification, and writes it. It changes no pin and no proposal, so the
// lock's comment or decision still ratifies it. It returns that commit. A
// lock that already pins its layouts is refused.
func (p *Project) MigrateLayouts(ctx context.Context) (string, error) {
	if state := p.Lock.LayoutState(); state != LayoutUnpinned {
		return "", fmt.Errorf("the lock already pins its statements' layouts (%s)", state)
	}
	commits, pins, err := p.RatifiedLayouts(ctx)
	if err != nil {
		return "", err
	}
	before := ProposalHash(p.Lock.Bounds, p.Lock.Statements)
	for i, s := range p.Lock.Statements {
		p.Lock.Statements[i].MigratedLayout = pins[s.Name]
	}
	if after := ProposalHash(p.Lock.Bounds, p.Lock.Statements); after != before {
		return "", fmt.Errorf("migrating the lock's layout pins changed its proposal from %s to %s", before, after)
	}
	return commits[len(commits)-1], p.writeLock()
}

// CheckLayouts holds the layout pins of the lock in dir to what was
// ratified, as CI checks every project's lock (#814). A ratified layout pin
// is part of the proposal its comment or decision names, which the
// ratification check holds it to. A migrated layout pin must be the one the
// model gives at each commit that recorded the lock's proposal, and no
// version of the lock with that proposal, on any branch the history holds,
// may have had migrated layout pins the lock no longer has: only a new
// ratification changes or removes a layout pin. A lock that pins no layout,
// and never did with its proposal, passes. It reads the repository's whole
// history, and fails when it can't.
func CheckLayouts(ctx context.Context, dir string, l Lock) error {
	state := l.LayoutState()
	if state == LayoutRatified {
		return nil
	}
	h, err := historyOf(ctx, dir)
	if errors.Is(err, ErrNoHistory) && state == LayoutUnpinned {
		// Outside any checkout, there's no history its layout pins could
		// have been in, and no CI.
		return nil
	}
	if err != nil {
		return err
	}
	lh, err := h.lockHistory()
	if err != nil {
		return err
	}
	if state == LayoutUnpinned {
		for _, c := range lh.holding(ProposalHash(l.Bounds, l.Statements)) {
			if lh.locks[c].LayoutState() != LayoutUnpinned {
				return fmt.Errorf("%s gave the lock layout pins, and the lock has none now; only a new ratification changes or removes a layout pin", short(c))
			}
		}
		return nil
	}
	commits, pins, err := h.ratifiedLayouts(lh, l)
	if err != nil {
		return err
	}
	for _, s := range l.Statements {
		if s.MigratedLayout != pins[s.Name] {
			return fmt.Errorf("%s's migrated layout pin is %s, and the model at %s, which recorded the lock's ratification, lays it out as %s; only a new ratification changes a layout pin", s.Name, s.MigratedLayout, short(commits[len(commits)-1]), pins[s.Name])
		}
	}
	return nil
}

// writeLock writes the lock as the factory and Pin write it.
func (p *Project) writeLock() error {
	b, err := json.MarshalIndent(p.Lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.Dir, lockFile), append(b, '\n'), 0o644)
}
