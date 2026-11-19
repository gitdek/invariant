package project

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tla"
)

// A lock ratified before layout pins keeps its proposal when it gains them
// by migration, and a lock ratified with them has a new one (#814).
func TestProposalHashLeavesOutMigratedLayoutPins814(t *testing.T) {
	bounds := map[string]string{"N": "2"}
	legacy := []Statement{
		{Name: "Spec", Kind: Spec, Says: "s", SHA256: "sha256:" + strings.Repeat("1", 64)},
		{Name: "Inv", Kind: Invariant, Says: "i", SHA256: "sha256:" + strings.Repeat("2", 64)},
	}
	before := ProposalHash(bounds, legacy)
	migrated := append([]Statement(nil), legacy...)
	ratified := append([]Statement(nil), legacy...)
	for i := range legacy {
		migrated[i].MigratedLayout = "sha256:" + strings.Repeat("3", 64)
		ratified[i].Layout = "sha256:" + strings.Repeat("3", 64)
	}
	if got := ProposalHash(bounds, migrated); got != before {
		t.Errorf("migrated layout pins changed the proposal from %s to %s", before, got)
	}
	if got := ProposalHash(bounds, ratified); got == before {
		t.Error("ratified layout pins left the proposal alone; a person ratifies them")
	}
	if migrated[0].MigratedLayout == "" {
		t.Error("ProposalHash changed the statements it was given")
	}
	if ProposalHash(bounds, nil) != ProposalHash(bounds, []Statement(nil)) {
		t.Error("ProposalHash of no statements changed")
	}
}

func TestALockHoldsOneKindOfLayoutPin814(t *testing.T) {
	pin := "sha256:" + strings.Repeat("a", 64)
	base := func() Lock {
		return Lock{Bounds: map[string]string{"N": "1"}, Statements: []Statement{
			{Name: "Spec", Kind: Spec, Says: "s", SHA256: pin},
			{Name: "Inv", Kind: Invariant, Says: "i", SHA256: pin},
		}}
	}
	m := Manifest{Module: "M.tla", Code: "m"}
	cases := map[string]struct {
		edit func(*Lock)
		want string
	}{
		"none":                {func(*Lock) {}, ""},
		"every one ratified":  {func(l *Lock) { l.Statements[0].Layout, l.Statements[1].Layout = pin, pin }, ""},
		"every one migrated":  {func(l *Lock) { l.Statements[0].MigratedLayout, l.Statements[1].MigratedLayout = pin, pin }, ""},
		"one left out":        {func(l *Lock) { l.Statements[1].Layout = pin }, "has no ratified layout pin"},
		"one migration short": {func(l *Lock) { l.Statements[0].MigratedLayout = pin }, "has no migrated layout pin"},
		"both kinds": {func(l *Lock) {
			l.Statements[0].Layout, l.Statements[1].MigratedLayout = pin, pin
		}, "has no ratified layout pin"},
		"both on one statement": {func(l *Lock) {
			l.Statements[0].Layout, l.Statements[0].MigratedLayout, l.Statements[1].Layout = pin, pin, pin
		}, "a ratified layout pin and a migrated one"},
		"not a hash": {func(l *Lock) { l.Statements[0].Layout, l.Statements[1].Layout = "sha256:zz", pin }, "isn't a sha256: hash"},
	}
	for name, c := range cases {
		l := base()
		c.edit(&l)
		err := Validate(m, l)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: Validate = %v, want %q", name, err, c.want)
		}
	}
}

// The repository's own locks were ratified before layout pins and migrated
// (#814). Each keeps its pins and its proposal, which its ratifying comment
// or decision names, and each migrated layout pin is the one the model gives
// at the commit that recorded the lock's ratification, as the ratification
// check holds it to be. A lock ratified since pins its layout in its
// proposal. The plumbing sandbox copies the repository without its history,
// so it can't check this; CI's unit tests run with the whole history.
func TestEveryLockHereKeepsItsProposalAndPinsItsRatifiedLayout814(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "--is-shallow-repository").Output()
	if err != nil || strings.TrimSpace(string(out)) != "false" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("CI's checkout has no whole history to check the locks' layout pins against: %s %v", out, err)
		}
		t.Skip(root + " isn't a git checkout with its whole history, so the commits that recorded each lock's ratification can't be read")
	}
	tracked, err := exec.Command("git", "-C", root, "ls-files", "*/.invariant/invariant.json").Output()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	checked := 0
	for _, manifest := range strings.Fields(string(tracked)) {
		dir := filepath.Join(root, strings.TrimSuffix(manifest, "/.invariant/invariant.json"))
		p, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		proposal := ProposalHash(p.Lock.Bounds, p.Lock.Statements)
		if r := p.Lock.Ratified; r != nil && r.Proposal != proposal {
			t.Errorf("%s: the lock holds proposal %s, and its comment ratified %s", manifest, proposal, r.Proposal)
		}
		switch state := p.Lock.LayoutState(); state {
		case LayoutRatified:
			continue
		case LayoutUnpinned:
			t.Errorf("%s: the lock pins no layout. Every lock here was migrated, and every ratification since pins its layout; one a watcher from before layout pins drafted gets its layout pins with invariant pin -layout", manifest)
			continue
		}
		commits, pins, err := p.RatifiedLayouts(ctx)
		if err != nil {
			t.Errorf("%s: %v", manifest, err)
			continue
		}
		for _, s := range p.Lock.Statements {
			if s.MigratedLayout != pins[s.Name] {
				t.Errorf("%s: %s's migrated layout pin is %s, and the model at %s, which recorded the lock's ratification, gives %s", manifest, s.Name, s.MigratedLayout, commits[len(commits)-1], pins[s.Name])
			}
		}
		if err := CheckLayouts(ctx, dir, p.Lock); err != nil {
			t.Errorf("%s: the ratification check refuses its layout pins: %v", manifest, err)
		}
	}
	if checked < 18 {
		t.Errorf("checked %d projects; the repository holds 18", checked)
	}
}

// layoutRepo814 is a repository holding one project, which a person ratified
// before layout pins: its lock pins Inv's text, and nothing of its layout.
type layoutRepo814 struct {
	t    *testing.T
	root string
	dir  string
}

const layoutModel814 = "---- MODULE Small ----\nEXTENDS Naturals\nVARIABLE x\nInit == x = 0\nNext == x' = (x + 1) % 3\nSpec == Init /\\ [][Next]_x\n" +
	"Bounded ==\n    /\\ x >= 0\n    /\\ x <= 3\nInv == Bounded\n====\n"

func newLayoutRepo814(t *testing.T) *layoutRepo814 {
	t.Helper()
	r := &layoutRepo814{t: t, root: t.TempDir()}
	r.dir = filepath.Join(r.root, "examples", "small")
	r.git("init", "-q", "-b", "main")
	r.write(".invariant/invariant.json", `{"name": "small", "module": ".invariant/specs/Small.tla", "code": "small", "language": "go"}`)
	r.write(".invariant/specs/Small.tla", layoutModel814)
	model, err := tla.Read(layoutModel814, nil)
	if err != nil {
		t.Fatal(err)
	}
	lock := Lock{Bounds: map[string]string{"N": "3"}, Statements: []Statement{
		{Name: "Spec", Kind: Spec, Says: "x counts around."},
		{Name: "Inv", Kind: Invariant, Says: "x stays within 0 to 3."},
	}}
	for i, s := range lock.Statements {
		if lock.Statements[i].SHA256, err = model.PinHash(s.Name, Model); err != nil {
			t.Fatal(err)
		}
	}
	lock.Ratified = &Ratification{By: "someone", At: "2026-11-19T00:00:00Z", Issue: 1, Comment: "https://github.com/o/r/issues/1#issuecomment-1", Proposal: ProposalHash(lock.Bounds, lock.Statements)}
	r.writeLock(lock)
	r.commit("Ratify the statements for #1")
	return r
}

func (r *layoutRepo814) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *layoutRepo814) write(rel, text string) {
	r.t.Helper()
	path := filepath.Join(r.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *layoutRepo814) writeLock(l Lock) {
	r.t.Helper()
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		r.t.Fatal(err)
	}
	r.write(lockFile, string(b)+"\n")
}

func (r *layoutRepo814) commit(message string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", message)
	return r.git("rev-parse", "HEAD")
}

func (r *layoutRepo814) load() *Project {
	r.t.Helper()
	p, err := Load(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	return p
}

// reindent moves a token of Bounded, which Inv depends on: the pins stay,
// and the layout pins don't.
func (r *layoutRepo814) reindent() {
	r.t.Helper()
	r.write(".invariant/specs/Small.tla", strings.Replace(layoutModel814, "    /\\ x <= 3", "      /\\ x <= 3", 1))
}

func (r *layoutRepo814) check() error {
	r.t.Helper()
	p := r.load()
	return CheckLayouts(context.Background(), r.dir, p.Lock)
}

func layoutsOf814(t *testing.T, src string) map[string]string {
	t.Helper()
	model, err := tla.Read(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, name := range []string{"Spec", "Inv"} {
		if out[name], err = model.LayoutHash(name, Model); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// A migration takes the layout the lock was ratified with, from the commit
// that recorded it, not the one the model has now: a layout changed since
// then is what the gate must catch.
func TestAMigrationPinsTheLayoutAsRatified814(t *testing.T) {
	r := newLayoutRepo814(t)
	ratifiedAt := r.git("rev-parse", "HEAD")
	ratified := layoutsOf814(t, layoutModel814)
	r.reindent()
	r.commit("Tidy the model")
	if err := r.check(); err != nil {
		t.Fatalf("a lock that pins no layout, and never did, is refused: %v", err)
	}

	p := r.load()
	before := ProposalHash(p.Lock.Bounds, p.Lock.Statements)
	commit, err := p.MigrateLayouts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if commit != ratifiedAt {
		t.Errorf("migrated from %s; the ratification was recorded at %s", commit, ratifiedAt)
	}
	p = r.load()
	if p.Lock.LayoutState() != LayoutMigrated || ProposalHash(p.Lock.Bounds, p.Lock.Statements) != before || p.Lock.Ratified.Proposal != before {
		t.Fatalf("the migrated lock is %s with proposal %s; want migrated, keeping %s", p.Lock.LayoutState(), ProposalHash(p.Lock.Bounds, p.Lock.Statements), before)
	}
	for _, s := range p.Lock.Statements {
		if s.MigratedLayout != ratified[s.Name] || s.Layout != "" {
			t.Errorf("%s: migrated %q, ratified %q; want the layout as ratified, %s", s.Name, s.MigratedLayout, s.Layout, ratified[s.Name])
		}
	}
	r.commit("Pin the locks' layouts (#814)")
	if err := r.check(); err != nil {
		t.Errorf("the migrated lock is refused: %v", err)
	}
	if _, err := r.load().MigrateLayouts(context.Background()); err == nil {
		t.Error("a lock was migrated twice")
	}
}

// CI refuses a change to a layout pin that doesn't come with a new
// ratification, however a pull request's history gets there.
func TestOnlyANewRatificationChangesAMigratedLayoutPin814(t *testing.T) {
	migrated := func(t *testing.T) *layoutRepo814 {
		r := newLayoutRepo814(t)
		if _, err := r.load().MigrateLayouts(context.Background()); err != nil {
			t.Fatal(err)
		}
		r.commit("Pin the locks' layouts (#814)")
		return r
	}
	repin := func(r *layoutRepo814) {
		p := r.load()
		moved := layoutsOf814(t, strings.Replace(layoutModel814, "    /\\ x <= 3", "      /\\ x <= 3", 1))
		for i, s := range p.Lock.Statements {
			p.Lock.Statements[i].MigratedLayout = moved[s.Name]
		}
		r.writeLock(p.Lock)
	}

	t.Run("re-indented, and its pin moved to match", func(t *testing.T) {
		r := migrated(t)
		r.reindent()
		repin(r)
		r.commit("Re-indent Bounded")
		if err := r.check(); err == nil || !strings.Contains(err.Error(), "only a new ratification changes a layout pin") {
			t.Errorf("CheckLayouts = %v", err)
		}
	})

	t.Run("its layout pins removed", func(t *testing.T) {
		r := migrated(t)
		r.reindent()
		p := r.load()
		for i := range p.Lock.Statements {
			p.Lock.Statements[i].MigratedLayout = ""
		}
		r.writeLock(p.Lock)
		r.commit("Re-indent Bounded")
		if err := r.check(); err == nil || !strings.Contains(err.Error(), "gave the lock layout pins, and the lock has none now") {
			t.Errorf("CheckLayouts = %v", err)
		}
	})

	t.Run("broken, then restored with the new layout", func(t *testing.T) {
		r := migrated(t)
		p := r.load()
		broken := p.Lock
		broken.Statements = append([]Statement(nil), p.Lock.Statements...)
		broken.Statements[1].Says = "something else"
		r.writeLock(broken)
		r.commit("Break the lock")
		r.reindent()
		r.writeLock(p.Lock)
		repin(r)
		r.commit("Restore the lock")
		if err := r.check(); err == nil || !strings.Contains(err.Error(), "each recorded the lock's proposal, and they lay out") {
			t.Errorf("CheckLayouts = %v", err)
		}
	})

	t.Run("a stale branch merged, keeping its lock", func(t *testing.T) {
		r := newLayoutRepo814(t)
		r.git("branch", "stale")
		if _, err := r.load().MigrateLayouts(context.Background()); err != nil {
			t.Fatal(err)
		}
		r.commit("Pin the locks' layouts (#814)")
		r.git("checkout", "-q", "stale")
		r.reindent()
		r.commit("Re-indent Bounded")
		// The merge keeps the branch's lock, which pins no layout, so the
		// lock's own log, simplified, never reaches the migration.
		r.git("merge", "-q", "-s", "ours", "-m", "Merge main", "main")
		if p := r.load(); p.Lock.LayoutState() != LayoutUnpinned {
			t.Fatal("the merge took main's lock")
		}
		if err := r.check(); err == nil || !strings.Contains(err.Error(), "gave the lock layout pins, and the lock has none now") {
			t.Errorf("CheckLayouts = %v", err)
		}
	})

	t.Run("a new ratification", func(t *testing.T) {
		r := migrated(t)
		r.reindent()
		p := r.load()
		if _, err := p.Pin(); err != nil {
			t.Fatal(err)
		}
		p = r.load()
		if p.Lock.LayoutState() != LayoutRatified {
			t.Fatalf("pinning a moved layout left the lock %s", p.Lock.LayoutState())
		}
		r.commit("Ratify the new layout")
		if err := r.check(); err != nil {
			t.Errorf("a lock that pins its layout in its proposal is refused: %v", err)
		}
	})

	t.Run("a shallow clone", func(t *testing.T) {
		r := migrated(t)
		clone := filepath.Join(t.TempDir(), "clone")
		out, err := exec.Command("git", "clone", "-q", "--depth", "1", "file://"+r.root, clone).CombinedOutput()
		if err != nil {
			t.Fatalf("git clone: %v\n%s", err, out)
		}
		dir := filepath.Join(clone, "examples", "small")
		p, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckLayouts(context.Background(), dir, p.Lock); err == nil || !strings.Contains(err.Error(), "shallow") {
			t.Errorf("CheckLayouts in a shallow clone = %v", err)
		}
	})
}

// Outside any checkout there's no history: a migrated layout pin can't be
// checked, and a lock that pins no layout has nothing to be held to.
func TestLayoutPinsOutsideACheckout814(t *testing.T) {
	r := newLayoutRepo814(t)
	if _, err := r.load().MigrateLayouts(context.Background()); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(t.TempDir(), "small")
	if out, err := exec.Command("cp", "-R", r.dir, copied).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v\n%s", err, out)
	}
	p, err := Load(copied)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckLayouts(context.Background(), copied, p.Lock); err == nil {
		t.Error("a migrated lock outside any checkout passed")
	}
	unpinned := p.Lock
	unpinned.Statements = append([]Statement(nil), p.Lock.Statements...)
	for i := range unpinned.Statements {
		unpinned.Statements[i].MigratedLayout = ""
	}
	if err := CheckLayouts(context.Background(), copied, unpinned); err != nil {
		t.Errorf("a lock that pins no layout, outside any checkout, is refused: %v", err)
	}
	if _, err := p.MigrateLayouts(context.Background()); err == nil {
		t.Error("a lock outside any checkout was migrated")
	}
}

// invariant pin records a lock's layout pins with any change, as a
// ratification, and leaves a lock nothing changed in as it is.
func TestPinRecordsTheLayoutWithTheText814(t *testing.T) {
	r := newLayoutRepo814(t)
	if changed, err := r.load().Pin(); err != nil || len(changed) != 0 {
		t.Fatalf("Pin = %v, %v", changed, err)
	}
	if p := r.load(); p.Lock.LayoutState() != LayoutUnpinned {
		t.Errorf("pinning a lock nothing changed in pinned its layout: %s", p.Lock.LayoutState())
	}
	if _, err := r.load().MigrateLayouts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if changed, err := r.load().Pin(); err != nil || len(changed) != 0 {
		t.Fatalf("Pin of a migrated lock = %v, %v", changed, err)
	}
	if p := r.load(); p.Lock.LayoutState() != LayoutMigrated {
		t.Errorf("pinning a migrated lock nothing changed in made it %s", p.Lock.LayoutState())
	}
	r.reindent()
	changed, err := r.load().Pin()
	if err != nil || strings.Join(changed, ",") != "Inv" {
		t.Fatalf("Pin after moving a token of Inv = %v, %v; want [Inv]", changed, err)
	}
	p := r.load()
	moved := layoutsOf814(t, strings.Replace(layoutModel814, "    /\\ x <= 3", "      /\\ x <= 3", 1))
	for _, s := range p.Lock.Statements {
		if s.Layout != moved[s.Name] || s.MigratedLayout != "" {
			t.Errorf("%s: layout %q, migrated %q; want ratified %s", s.Name, s.Layout, s.MigratedLayout, moved[s.Name])
		}
	}
}
