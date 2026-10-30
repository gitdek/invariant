package receipt_test

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// Explicit receipts can contain supplied facts and declared assumptions. Their
// footer must acknowledge those sources without changing proof coverage,
// literal escaping, report data or any legacy receipt bytes.
func TestComponentReceiptAcknowledgesSuppliedData(t *testing.T) {
	const supplied = " from recorded checks, supplied data and declared assumptions. Fingerprint "
	const legacy = " from tool output only. Fingerprint "
	const timestamp = "time_&amp;`|\r\n</sub><script>bad</script>[forged](https://evil.invalid)"
	for _, proved := range []bool{true, false} {
		for _, passed := range []bool{true, false} {
			t.Run(fmt.Sprintf("proved=%v/pass=%v", proved, passed), func(t *testing.T) {
				r := explicit247Report(t, "core/step.go", proved)
				r.Passed = passed
				r.Fingerprint = verify.Fingerprint(r)
				baseline := receipt.Markdown(&r)
				r.GeneratedAt = timestamp
				md := explicit247RenderUnchanged(t, &r)
				footer := explicit247Line(t, md, "<sub>Generated ")
				stamp, fingerprint, found := strings.Cut(strings.TrimPrefix(footer, "<sub>Generated "), supplied)
				if !found || strings.Contains(footer, legacy) {
					t.Errorf("explicit footer does not acknowledge supplied data and declared assumptions: %s", footer)
				} else {
					if got := explicit247Visible(t, stamp); got != timestamp {
						t.Errorf("footer timestamp displays %q, want %q", got, timestamp)
					}
					explicit247WantCodes(t, fingerprint, r.Fingerprint)
				}
				if !reflect.DeepEqual(explicit247Shape(baseline), explicit247Shape(md)) {
					t.Error("hostile timestamp changed receipt lines or table structure")
				}
				tags := regexp.MustCompile(`<[^>]*>`)
				if !reflect.DeepEqual(tags.FindAllString(baseline, -1), tags.FindAllString(md, -1)) {
					t.Error("hostile timestamp created or closed HTML elements")
				}
				rows := explicit247Table(t, md, "| Component | Proof | Source | Contract | Evidence |")
				if len(rows) != 3 {
					t.Fatalf("receipt lost complete production proof coverage: %#v", rows)
				}
				want := map[string]string{}
				for _, component := range r.Components.Inventory.Components {
					want[component.ID] = "unchecked"
					if proved && component.Symbol == "Step" {
						want[component.ID] = "proved"
					}
				}
				for _, row := range rows {
					if len(row) != 5 {
						t.Fatalf("receipt changed component table cells: %#v", row)
					}
					id := explicit247Visible(t, row[0])
					if want[id] == "" || row[1] != want[id] {
						t.Errorf("component proof coverage %q = %q, want %q", id, row[1], want[id])
					}
					delete(want, id)
				}
				if len(want) != 0 {
					t.Errorf("receipt omitted component proof rows: %#v", want)
				}
				if r.Passed != passed || r.CodeAssurance() != "component evidence" || r.Claim() != "component evidence" || r.EverySize() {
					t.Error("footer rendering changed gate outcome or component assurance")
				}
				if strings.Contains(md, "at every size") || strings.Contains(md, "The code is **proved") {
					t.Error("explicit receipt acquired a project-wide proof claim")
				}
				if verify.Fingerprint(r) != r.Fingerprint {
					t.Error("footer rendering changed the stored evidence fingerprint")
				}
			})
		}
	}
	// The existing legacy golden tests remain unchanged; also pin the footer
	// distinction here so a shared-footer edit cannot silently widen this change.
	for _, r := range receiptAssuranceFixtures() {
		r.Fingerprint = verify.Fingerprint(r)
		md := explicit247RenderUnchanged(t, &r)
		footer := explicit247Line(t, md, "<sub>Generated ")
		if !strings.Contains(footer, legacy) || strings.Contains(footer, supplied) {
			t.Errorf("legacy %s footer changed: %s", r.Project, footer)
		}
	}
}
