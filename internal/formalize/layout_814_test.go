package formalize

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
)

// A draft pins each statement's layout with its text, so the layout a person
// sees in a proposal is the one they ratify, and its proposal hashes both
// (#814). Whatever layout pin the drafting agent wrote is replaced.
func TestADraftPinsItsLayout814(t *testing.T) {
	ws := workspace(t, map[string]func(string) string{"proposal.json": func(s string) string {
		return strings.Replace(s, `{"name": "CanFill", "kind": "witness", "says": "The buffer can fill up."`,
			`{"name": "CanFill", "kind": "witness", "says": "The buffer can fill up.", "layout": "sha256:`+strings.Repeat("0", 64)+`", "migrated_layout": "sha256:`+strings.Repeat("1", 64)+`"`, 1)
	}})
	p, err := Read(ws)
	if err != nil {
		t.Fatal(err)
	}
	model, err := tla.Read(p.ModuleText, p.Includes)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Statements {
		want, err := model.LayoutHash(s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		if s.Layout != want || s.MigratedLayout != "" {
			t.Errorf("%s: layout %q, migrated %q; want %s", s.Name, s.Layout, s.MigratedLayout, want)
		}
	}
	unpinned := append([]project.Statement(nil), p.Statements...)
	for i := range unpinned {
		unpinned[i].Layout = ""
	}
	if p.Hash != project.ProposalHash(p.Bounds, p.Statements) || p.Hash == project.ProposalHash(p.Bounds, unpinned) {
		t.Error("the proposal doesn't hash its layout pins")
	}
}

// An amendment of a lock ratified before layout pins carries a bug exactly
// as the lock has it, whether the lock pins no layout yet or gained its
// layout pins by migration. Where the lock pins the bug's layout, a bug whose
// layout the amendment moves is a bug it changes, so it must classify it; a
// lock that pins none can't say, and keeps the rule it had. Either way, the
// comparison a person reads shows a statement the amendment moved as
// changed.
func TestAnAmendmentOfALockRatifiedBeforeLayoutPins814(t *testing.T) {
	ratified := legacy857(t)
	unpinned, migrated := *ratified, *ratified
	unpinned.Lock.Statements = append([]project.Statement(nil), ratified.Lock.Statements...)
	migrated.Lock.Statements = append([]project.Statement(nil), ratified.Lock.Statements...)
	for i, s := range ratified.Lock.Statements {
		if s.Layout == "" {
			t.Fatalf("%s: the fixture pins no layout", s.Name)
		}
		unpinned.Lock.Statements[i].Layout = ""
		migrated.Lock.Statements[i].Layout, migrated.Lock.Statements[i].MigratedLayout = "", s.Layout
	}
	if unpinned.Lock.LayoutState() != project.LayoutUnpinned || migrated.Lock.LayoutState() != project.LayoutMigrated {
		t.Fatal("the fixtures aren't the two kinds of lock")
	}
	moved := map[string]func(string) string{"BoundedBuffer.tla": func(s string) string {
		return strings.Replace(s, "PutWhenFull == \\E m \\in Msgs : buf' = Append(buf, m)", "PutWhenFull ==\n    \\E m \\in Msgs : buf' = Append(buf, m)", 1)
	}}
	for name, c := range map[string]*Current{"pins no layout yet": &unpinned, "migrated": &migrated} {
		t.Run(name, func(t *testing.T) {
			lock := c.Lock.Statements
			p, err := ReadAmending(seeded857(t, c, nil), lock)
			if err != nil {
				t.Fatalf("the factory's seed of the lock is refused: %v", err)
			}
			if p.Hash == project.ProposalHash(c.Lock.Bounds, lock) {
				t.Error("an untouched amendment's proposal leaves out the layout pins it ratifies")
			}
			if err := p.Amend(c); err != nil {
				t.Fatal(err)
			}
			if got := changed857(c, p); len(got) != 0 {
				t.Errorf("an untouched amendment changes %v", got)
			}

			p, err = ReadAmending(seeded857(t, c, moved), lock)
			switch {
			case c.Lock.LayoutState() == project.LayoutUnpinned && err != nil:
				t.Fatalf("an amendment that moves a token of a bug whose layout the lock doesn't pin: %v", err)
			case c.Lock.LayoutState() == project.LayoutUnpinned:
				if err := p.Amend(c); err != nil {
					t.Fatal(err)
				}
				if got := changed857(c, p); !reflect.DeepEqual(got, []string{"PutWhenFull changed"}) {
					t.Errorf("the amendment's changes are %v, want [PutWhenFull changed]", got)
				}
			case err == nil || !strings.Contains(err.Error(), "statement PutWhenFull: an amendment must classify each bug it adds or changes") ||
				!strings.Contains(err.Error(), "keep its lines where the lock has them to carry it as it is"):
				t.Fatalf("an amendment that moves a token of the carried bug: %v; want it to classify the bug, or put its lines back", err)
			}
			classified := map[string]func(string) string{"BoundedBuffer.tla": moved["BoundedBuffer.tla"], "proposal.json": func(s string) string {
				return strings.Replace(s, `"expect": "WithinCap"`, `"expect": "WithinCap", "code_behavior": "current"`, 1)
			}}
			p, err = ReadAmending(seeded857(t, c, classified), lock)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Amend(c); err != nil {
				t.Fatal(err)
			}
			if got := changed857(c, p); !reflect.DeepEqual(got, []string{"PutWhenFull changed"}) {
				t.Errorf("the amendment's changes are %v, want [PutWhenFull changed]", got)
			}
		})
	}

	// A statement whose layout alone moved is changed, and its own text,
	// as written, isn't the same.
	p, err := ReadAmending(seeded857(t, &migrated, map[string]func(string) string{"BoundedBuffer.tla": func(s string) string {
		return strings.Replace(s, "WithinCap == Len(buf) <= Cap", "WithinCap ==\n    Len(buf) <= Cap", 1)
	}}), migrated.Lock.Statements)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range Diff(&migrated, p).Statements {
		if ch.Name == "WithinCap" && (ch.How != Changed || ch.OwnTextSame()) {
			t.Errorf("WithinCap, re-indented, is %s, own text the same %v", ch.How, ch.OwnTextSame())
		}
	}
}
