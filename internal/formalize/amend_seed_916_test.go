package formalize

import "testing"

// The draft an amendment starts from passes the name rules as it stands,
// wherever its project keeps its code (#916). A project that checks code as
// it is keeps it at ".", and its seed said "package": ".", which the
// draft's own check refused before it read anything else: the drafting
// agent had to change a field the amendment never uses, with nothing to say
// the factory wrote it. The amendment still keeps its project's manifest as
// it is, and a proposal saved from it carries a name a revision can copy.
func TestAnAmendmentsSeedPassesTheNameRules916(t *testing.T) {
	for _, tc := range []struct{ dir, code, want, slug string }{
		{"examples/03-bounded-buffer", "buffer", "buffer", "bounded-buffer"},
		{"invariant/lease", "src", "src", "lease"},
		{"invariant/06-auto-repair", ".", "autorepair", "auto-repair"},
		{"checks/Lease_Protocol.v2", ".", "leaseprotocolv2", "lease-protocol-v2"},
		{"checks/42", ".", "project", "42"},
		{"checks/__", ".", "project", "project"},
		{"checks/Queue_", ".", "queue", "queue"},
		{"invariant/queue", "internal/My-Queue", "queue", "queue"},
	} {
		c := current(t)
		c.Dir, c.Manifest.Code = tc.dir, tc.code
		if tc.code == "." {
			c.Manifest.Language, c.Manifest.Conformance, c.Manifest.Existing = "typescript", "conformance.ts", []string{"hooks/use-buffer.ts"}
		}
		ws := t.TempDir()
		if err := seedAmendment(ws, c); err != nil {
			t.Fatal(err)
		}
		p, err := ReadAmending(ws, c.Lock.Statements)
		if err != nil {
			t.Errorf("the seed for a project at %s with its code at %q is refused: %v", tc.dir, tc.code, err)
			continue
		}
		if !p.Ratifiable() || p.Package != tc.want || p.Slug != tc.slug {
			t.Errorf("the seed for a project at %s with its code at %q carries the package %q and the slug %q; want %q and %q", tc.dir, tc.code, p.Package, p.Slug, tc.want, tc.slug)
			continue
		}
		manifest := c.Manifest
		if err := p.Amend(c); err != nil || p.Package != tc.want || p.Slug != tc.slug || p.Target.Dir != tc.dir {
			t.Errorf("the amendment of a project at %s carries the package %q and the slug %q, for %s, %v; want %q and %q, for its own directory", tc.dir, p.Package, p.Slug, p.Target.Dir, err, tc.want, tc.slug)
		}
		if got := p.Manifest(); got.Code != manifest.Code || got.Language != manifest.Language || len(got.Existing) != len(manifest.Existing) {
			t.Errorf("the amendment's manifest is %+v; want its project's, %+v", got, manifest)
		}
	}
}
