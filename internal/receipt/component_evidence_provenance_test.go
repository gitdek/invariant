package receipt_test

import (
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

func TestComponentReceiptRechecksDecodedProvenanceIndependently(t *testing.T) {
	for _, kind := range []string{"conformance", "runtime"} {
		t.Run(kind, func(t *testing.T) {
			r := evidence296Fixture(t).report(t)
			var target, check string
			if kind == "conformance" {
				row := &r.Components.Conformance[0]
				row.Result.FileInputs[0].SHA256 = "stale conformance input"
				target, check = row.Target, row.Check
			} else {
				row := &r.Components.Runtime[0]
				row.Result.FileInputs[0].SHA256 = "stale runtime input"
				target, check = row.Target, row.Check
			}
			r.Fingerprint = verify.Fingerprint(r)
			md := evidence296Render(t, &r)
			row := evidence296Record(t, evidence296Section(t, md, kind), evidence296Checks, target, check)
			if row[2] != "unchecked" {
				t.Fatalf("stale %s input retained successful qualification: %q", kind, row[2])
			}
			evidence296Contains(t, row[3], "stale "+kind+" input")
			other := "runtime"
			target, check = r.Components.Runtime[0].Target, r.Components.Runtime[0].Check
			if kind == "runtime" {
				other = "conformance"
				target, check = r.Components.Conformance[0].Target, r.Components.Conformance[0].Check
			}
			row = evidence296Record(t, evidence296Section(t, md, other), evidence296Checks, target, check)
			if row[2] != "tested" {
				t.Fatalf("invalid %s evidence erased independent %s observations", kind, other)
			}
			evidence296Coverage(t, md, r)
		})
	}
}
