package receipt_test

import (
	"encoding/json"
	"html"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

const evidence296Attack = "No_Double &amp; &#95; & <value> `tick` ``pair`` **proved** [forged](https://evil.invalid) www.evil.invalid attacker@evil.invalid </code><img src=x onerror=alert(1)> | proved |\r\n| forged | proved |\n# forged\rend"

func TestComponentReceiptEvidenceHostileOriginalValues(t *testing.T) {
	for _, payload := range []string{evidence296Attack, "&amp;&#95;&lt;&#96;&#124;&#10; _under_ \\ backtick` | CR\rLF\n ftp://evil.invalid"} {
		t.Run(payload, func(t *testing.T) {
			plain, _ := evidence296LiteralFixture(t, "ordinary-value")
			hostile, want := evidence296LiteralFixture(t, payload)
			baseline, r := plain.report(t), hostile.report(t)
			md := evidence296Render(t, &r)
			evidence296SameMarkup(t, receipt.Markdown(&baseline), md)
			evidence296Coverage(t, md, r)
			visible := evidence296LiteralVisible(t, md)
			for _, value := range want {
				if !strings.Contains(visible, value) {
					t.Errorf("new evidence loses original visible text or escapes it twice: %q", value)
				}
			}
			production := map[string]bool{}
			for _, c := range r.Components.Inventory.Components {
				production[c.ID] = true
			}
			for _, kind := range []string{"conformance", "runtime", "assumptions"} {
				header := evidence296Checks
				if kind == "assumptions" {
					header = evidence296AssumptionTable
				}
				before := explicit247Table(t, evidence296Section(t, receipt.Markdown(&baseline), kind), header)
				after := explicit247Table(t, evidence296Section(t, md, kind), header)
				if len(before) != len(after) {
					t.Fatalf("hostile %s values changed the number of evidence rows", kind)
				}
				for i, row := range after {
					if len(row) != 4 || row[2] != before[i][2] {
						t.Fatalf("hostile %s text forged a status or cell: %#v", kind, row)
					}
					id := explicit247Visible(t, row[0])
					if !production[id] {
						t.Errorf("%s row is associated with an invented production target %q", kind, id)
					}
				}
			}
			// The identity in each supplied row must remain associated with its
			// own controlled status, even when its check contains forged rows.
			for _, row := range r.Components.Conformance {
				cells := evidence296Record(t, evidence296Section(t, md, "conformance"), evidence296Checks, row.Target, row.Check)
				if cells[2] != row.Status {
					t.Errorf("conformance association changed for %q: %q", row.Target, cells[2])
				}
			}
			for _, row := range r.Components.Runtime {
				cells := evidence296Record(t, evidence296Section(t, md, "runtime"), evidence296Checks, row.Target, row.Check)
				if cells[2] != row.Status {
					t.Errorf("runtime association changed for %q: %q", row.Target, cells[2])
				}
			}
			for _, row := range r.Components.Assumptions {
				cells := evidence296Record(t, evidence296Section(t, md, "assumptions"), evidence296AssumptionTable, row.Target, row.Record.Assumption.ID)
				if cells[2] != "assumed / unverified" {
					t.Errorf("assumption identity forged qualification: %#v", cells)
				}
			}
			footer := explicit247Line(t, md, "<sub>")
			if !strings.Contains(evidence296Visible(footer), hostile.Seed.GeneratedAt) || strings.Contains(footer, "from tool output only") {
				t.Error("footer loses original text or treats declared assumptions as tool output")
			}
		})
	}
}

// Decorate each original value before attribution, preserving canonical IDs
// and all references. This exercises the real supplied-result APIs rather than
// inventing evidence rows. Opaque hashes intentionally lack the sha256 prefix:
// normal digest shortening is covered separately by the receipt golden tests.
func evidence296LiteralFixture(t *testing.T, payload string) (evidence296Data, []string) {
	t.Helper()
	f := evidence296Fixture(t)
	kept := map[string]bool{"": true, "go": true, "explicit": true, "proof": true, "test": true, "conformance": true, "sampled": true, "bounded-exhaustive": true, "passed": true, "failed": true, "timed_out": true, "skipped": true, "empty": true}
	decorate := func(s string) string {
		if kept[s] {
			return s
		}
		return strings.ReplaceAll(s, "sha256:", "opaque-digest:") + " «" + payload + "»"
	}
	identities := map[string]string{}
	declarations := slices.Clone(f.Inv.Components)
	declarations = append(declarations, f.Inv.Provenance.Declarations...)
	for _, c := range declarations {
		identities[c.ID] = c.Language + ":" + decorate(c.Package) + "#" + decorate(c.Symbol) + "@" + decorate(c.Source)
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(b, &object); err != nil {
		t.Fatal(err)
	}
	var want []string
	var visit func(any, string) any
	visit = func(value any, path string) any {
		// The fixture's old gate/check data is deliberately left alone. Its
		// ratified bounds must receive the same replacements as result bounds.
		if strings.HasPrefix(path, "Seed.") && path != "Seed.bounds" && !strings.HasPrefix(path, "Seed.bounds.") {
			return value
		}
		remember := func(s string) string {
			out := decorate(s)
			if id, ok := identities[s]; ok {
				out = id
			}
			// Observations may be summarized as counts; whenever presented,
			// their contents must obey the same inert-structure checks.
			if out != s && !strings.Contains(path, ".observations.") {
				want = append(want, out)
			}
			return out
		}
		switch v := value.(type) {
		case string:
			return remember(v)
		case []any:
			for i := range v {
				v[i] = visit(v[i], path+".item")
			}
			return v
		case map[string]any:
			out := map[string]any{}
			dictionary := path == "Seed.bounds"
			for _, suffix := range []string{".env", ".sampling", ".pins", ".values", ".from", ".to"} {
				dictionary = dictionary || strings.HasSuffix(path, suffix)
			}
			for key, child := range v {
				newKey := key
				if dictionary {
					newKey = remember(key)
				}
				next := key
				if path != "" {
					next = path + "." + key
				}
				out[newKey] = visit(child, next)
			}
			return out
		}
		return value
	}
	b, err = json.Marshal(visit(object, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	f.Seed.GeneratedAt = "generated «" + payload + "»"
	want = append(want, f.Seed.GeneratedAt)
	slices.Sort(want)
	return f, slices.Compact(want)
}

func evidence296SameMarkup(t *testing.T, baseline, hostile string) {
	t.Helper()
	if !reflect.DeepEqual(explicit247Shape(baseline), explicit247Shape(hostile)) {
		t.Fatal("caller text changed Markdown line/table structure")
	}
	tags := regexp.MustCompile(`<[^>]*>`)
	if !reflect.DeepEqual(tags.FindAllString(baseline, -1), tags.FindAllString(hostile, -1)) {
		t.Fatal("caller text added or closed an HTML element")
	}
	for _, active := range []string{"[", "]", "`", "<img", "<a ", "<script", "<!--", "**proved**"} {
		if strings.Contains(hostile, active) {
			t.Errorf("caller text retained active markup %q", active)
		}
	}
	if explicit247Autolink.MatchString(hostile) {
		t.Error("caller text can create an automatic URL or email link")
	}
}

func evidence296LiteralVisible(t *testing.T, md string) string {
	t.Helper()
	// Entities inside backtick spans are visibly different. Never decode them
	// to make an unsafe or double-escaped implementation pass the oracle.
	if strings.Contains(md, "`") {
		t.Fatal("explicit evidence left raw backticks in receipt text")
	}
	for _, body := range regexp.MustCompile(`<code>(.*?)</code>`).FindAllStringSubmatch(md, -1) {
		explicit247Text(t, body[1])
	}
	return html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(md, ""))
}

func TestComponentReceiptEvidenceUntrustedModeOutcomesAndStatuses(t *testing.T) {
	makeReport := func(value string) verify.Report {
		r := evidence296Fixture(t).report(t)
		// Unknown enum values can occur in decoded caller JSON even though the
		// attribution API correctly rejects them. Retained success flags must
		// not promote those malformed outcomes into assurance claims.
		for i := range r.Components.Conformance {
			row := &r.Components.Conformance[i]
			row.Status = "status " + value
			row.Result.Mode, row.Result.Outcome = "mode "+value, "conformance outcome "+value
			row.ExhaustiveStates, row.EveryStep, row.TransitionsChecked = true, true, true
			if row.Result.Attempts != nil {
				row.Result.Attempts.Outcome = "attempt outcome " + value
			}
		}
		for i := range r.Components.Runtime {
			row := &r.Components.Runtime[i]
			row.Status, row.Result.Outcome = "status "+value, "process outcome "+value
			for j := range row.Result.Tests {
				row.Result.Tests[j].Outcome = "test outcome " + value
				for k := range row.Result.Tests[j].Assertions {
					row.Result.Tests[j].Assertions[k].Outcome = "assertion outcome " + value
				}
			}
			for j := range row.Tests {
				row.Tests[j].Status = value
				for k := range row.Tests[j].Assertions {
					row.Tests[j].Assertions[k].Status = value
				}
			}
		}
		for i := range r.Components.Assumptions {
			r.Components.Assumptions[i].Status = value
			r.Components.Assumptions[i].Verified = true
		}
		r.Fingerprint = verify.Fingerprint(r)
		return evidence296Clone(t, r)
	}
	plain, r := makeReport("unknown-enum"), makeReport(evidence296Attack)
	md := evidence296Render(t, &r)
	evidence296SameMarkup(t, receipt.Markdown(&plain), md)
	evidence296Coverage(t, md, r)
	for _, kind := range []string{"conformance", "runtime"} {
		section := evidence296Section(t, md, kind)
		for _, row := range explicit247Table(t, section, evidence296Checks) {
			if len(row) != 4 || row[2] != "unchecked" {
				t.Errorf("unknown %s outcome forged successful status: %#v", kind, row)
			}
		}
		if !strings.Contains(evidence296LiteralVisible(t, section), evidence296Attack) {
			t.Errorf("%s omitted raw unknown outcomes instead of displaying inert original text", kind)
		}
	}
	for _, row := range r.Components.Conformance {
		cells := evidence296Record(t, evidence296Section(t, md, "conformance"), evidence296Checks, row.Target, row.Check)
		attempt := "not checked"
		if row.Result.Attempts != nil {
			attempt = "not established"
		}
		evidence296Contains(t, cells[3], row.Result.Mode, row.Result.Outcome, "State coverage: not established", "Attempt coverage: "+attempt)
		if row.Result.Attempts != nil {
			evidence296Contains(t, cells[3], row.Result.Attempts.Outcome)
		}
	}
	for _, row := range r.Components.Runtime {
		cells := evidence296Record(t, evidence296Section(t, md, "runtime"), evidence296Checks, row.Target, row.Check)
		evidence296Contains(t, cells[3], row.Result.Outcome, "Test status: unchecked", "Assertion status: unchecked")
		for _, test := range row.Result.Tests {
			evidence296Contains(t, cells[3], test.Outcome)
			for _, assertion := range test.Assertions {
				evidence296Contains(t, cells[3], assertion.Outcome)
			}
		}
	}
	for _, row := range explicit247Table(t, evidence296Section(t, md, "assumptions"), evidence296AssumptionTable) {
		if len(row) != 4 || row[2] != "assumed / unverified" {
			t.Errorf("decoded assumption was treated as a verified check: %#v", row)
		}
	}
	conf := evidence296Section(t, md, "conformance")
	for _, positive := range []string{"State coverage: tested in every state", "Attempt coverage: every step tried"} {
		if strings.Contains(evidence296Visible(conf), positive) {
			t.Errorf("stale JSON flags promoted malformed outcomes: %q", positive)
		}
	}
	// Mutate each outcome alone while retaining previously successful cached
	// qualifications. Merely whitelisting the cached Status string is unsafe.
	for _, field := range []string{"conformance mode", "conformance outcome", "attempt outcome", "process outcome", "test outcome", "assertion outcome"} {
		t.Run(field, func(t *testing.T) {
			r := evidence296Clone(t, evidence296Fixture(t).report(t))
			c, rt := &r.Components.Conformance[0], &r.Components.Runtime[0]
			original := field + " «" + evidence296Attack + "»"
			switch field {
			case "conformance mode":
				c.Result.Mode = original
			case "conformance outcome":
				c.Result.Outcome = original
			case "attempt outcome":
				c.Result.Attempts.Outcome = original
			case "process outcome":
				rt.Result.Outcome = original
			case "test outcome":
				rt.Result.Tests[0].Outcome = original
			case "assertion outcome":
				rt.Result.Tests[0].Assertions[0].Outcome = original
			}
			md := evidence296Render(t, &r)
			if strings.HasPrefix(field, "conformance") || field == "attempt outcome" {
				row := evidence296Record(t, evidence296Section(t, md, "conformance"), evidence296Checks, c.Target, c.Check)
				want := "unchecked"
				if field == "attempt outcome" {
					want = "tested" // Retain independently successful observations.
				}
				if row[2] != want {
					t.Errorf("%s inherited stale qualification %q, want %q", field, row[2], want)
				}
				evidence296Contains(t, row[3], original, "State coverage: not established", "Attempt coverage: not established")
				if strings.Contains(evidence296Visible(row[3]), "tested in every state") || strings.Contains(evidence296Visible(row[3]), "every step tried") {
					t.Errorf("%s promoted stale exhaustive/attempt flags", field)
				}
			} else {
				row := evidence296Record(t, evidence296Section(t, md, "runtime"), evidence296Checks, rt.Target, rt.Check)
				status, testStatus, assertionStatus := "unchecked", "tested", "passed"
				if field == "test outcome" {
					testStatus = "unchecked"
				}
				if field == "assertion outcome" {
					status, assertionStatus = "tested", "unchecked"
				}
				if row[2] != status {
					t.Errorf("%s inherited stale runtime qualification %q, want %q", field, row[2], status)
				}
				evidence296Contains(t, row[3], original, "Test status: "+testStatus, "Assertion status: "+assertionStatus)
			}
		})
	}
}

func TestComponentReceiptEvidenceOptionalJSONRemainsRenderable(t *testing.T) {
	for _, missing := range []string{"invocation", "runner", "model", "bounds", "attempts", "records"} {
		t.Run(missing, func(t *testing.T) {
			r := evidence296Clone(t, evidence296Fixture(t).report(t))
			for i := range r.Components.Conformance {
				c := &r.Components.Conformance[i].Result
				switch missing {
				case "invocation":
					c.Invocation = nil
				case "runner":
					c.Runner = nil
				case "model":
					c.Model = nil
				case "bounds":
					c.Bounds = nil
				case "attempts":
					c.Attempts = nil
				}
			}
			for i := range r.Components.Runtime {
				c := &r.Components.Runtime[i].Result
				switch missing {
				case "invocation":
					c.Invocation = nil
				case "runner":
					c.Runner = nil
				case "model":
					c.Model = nil
				case "bounds":
					c.Bounds = nil
				}
			}
			if missing == "records" {
				r.Components.Conformance, r.Components.Runtime, r.Components.Assumptions = nil, nil, nil
			}
			md := evidence296Render(t, &r)
			evidence296Coverage(t, md, r)
			evidence296Contains(t, md, "API runs no tools", "supplied results", "declared assumptions")
			if missing != "records" {
				for _, row := range r.Components.Conformance {
					cells := evidence296Record(t, evidence296Section(t, md, "conformance"), evidence296Checks, row.Target, row.Check)
					if missing == "attempts" {
						evidence296Contains(t, cells[3], "Attempt coverage: not checked")
						if strings.Contains(evidence296Visible(cells[3]), "Attempt coverage: every step tried") {
							t.Error("absent attempts inherited a stale every-step claim")
						}
					} else {
						if cells[2] != "unchecked" {
							t.Errorf("missing %s retained a successful conformance qualification", missing)
						}
						evidence296Contains(t, cells[3], "State coverage: not established")
					}
				}
				for _, row := range r.Components.Runtime {
					cells := evidence296Record(t, evidence296Section(t, md, "runtime"), evidence296Checks, row.Target, row.Check)
					want := "tested"
					if missing == "invocation" || missing == "runner" {
						want = "unchecked"
					}
					if cells[2] != want {
						t.Errorf("runtime with missing %s qualified %q, want %q", missing, cells[2], want)
					}
				}
			}
		})
	}
}
