package formalize

import "testing"

// The draft an amendment starts from passes the name rules as it stands,
// wherever its project keeps its code (#916). A project that checks code as
// it is keeps it at ".", and its seed said "package": ".", which the
// draft's own check refused before it read anything else: the drafting
// agent had to change a field the amendment never uses, with nothing to say
// the factory wrote it. Once checked, the amendment has its directory's own
// name, which a saved amendment is found by (#799), the seed's package, so
// a revision copied from its saved proposal passes the rules too, and its
// project's manifest as it is.
func TestAnAmendmentsSeedPassesTheNameRules916(t *testing.T) {
	for _, tc := range []struct{ dir, code, want, slug string }{
		{"examples/03-bounded-buffer", "buffer", "buffer", "bounded-buffer"},
		{"invariant/lease", "src", "src", "lease"},
		{"invariant/06-auto-repair", ".", "autorepair", "auto-repair"},
		{"checks/Lease_Protocol.v2", ".", "leaseprotocolv2", "lease-protocol-v2"},
		{"checks/42", ".", "project", "42"},
		{"checks/__", ".", "project", "project"},
		{"checks/Queue_", ".", "queue", "queue"},
		{"checks/Queue", ".", "queue", "queue"},
		{"checks/a__b", ".", "ab", "a-b"},
		{"checks/Zig_Zag", ".", "zigzag", "zig-zag"},
		{"invariant/lease", "internal/queue", "queue", "lease"},
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
		if err := p.Amend(c); err != nil || p.Slug != slugOf(tc.dir) || p.Package != tc.want || p.Target.Dir != tc.dir {
			t.Errorf("the amendment of a project at %s is called %q, with the package %q, for %s, %v; want its directory's own name and the seed's package", tc.dir, p.Slug, p.Package, p.Target.Dir, err)
		}
		if got := p.Manifest(); got.Code != manifest.Code || got.Language != manifest.Language || len(got.Existing) != len(manifest.Existing) {
			t.Errorf("the amendment's manifest is %+v; want its project's, %+v", got, manifest)
		}
	}
}
