package receipt_test

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// Source-derived text must remain literal everywhere the component receipt
// includes it: the subject, selected contracts and failed-proof diagnostics.
// The table oracle below follows GFM's relevant rules, independently of the
// receipt's escaping implementation. In particular, backticks do NOT protect
// a pipe from splitting a table cell, and backslashes inside a code span do
// NOT protect backticks from closing it. No renderer dependency is needed.
func TestComponentProofMarkdownCannotForgeProofStatus(t *testing.T) {
	// Calibrate the oracle against the reported exploit, not just safe output.
	forged := componentMDCells("| `go:example.test/escape/core#Neighbor@core/x|proved|tail.go` | unchecked |")
	if len(forged) != 4 || strings.TrimSpace(forged[1]) != "proved" {
		t.Fatal("table oracle incorrectly lets backticks protect a pipe")
	}
	if _, ok := componentMDLiteral("`core/x\\`**proved**`tail.go`"); ok {
		t.Fatal("literal oracle incorrectly lets a backslash escape a code-span delimiter")
	}

	for _, source := range []string{
		"core/x|proved|tail.go",
		"core/x`**proved**`tail.go",
		"core/x``**proved**``tail.go",
		"core/x\n| forged | proved |tail.go",
		"core/x\r| forged | proved |tail.go",
		"core/x\r\n| forged | proved |tail.go",
		"core/x|proved|`**forged**`\r\n| forged | proved |tail.go",
	} {
		t.Run(fmt.Sprintf("%q", source), func(t *testing.T) {
			// Discover real source files through the existing Go inventory API.
			// All three identities, including the selected contract, carry the
			// hostile filename. Only Step may acquire proof evidence.
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "core"), 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{
				"go.mod": "module example.test/escape\n\ngo 1.27.1\n",
				source: "package core\n" +
					"//@ ensures result >= 0\nfunc Step() (result int) { return 1 }\n" +
					"//@ ensures result >= 0\nfunc Contract() (result int) { return 0 }\n" +
					"func Neighbor() {}\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			inv, err := project.ResolveComponents(&project.Project{Dir: dir, Manifest: project.Manifest{
				Language: "go", Components: &project.ComponentsConfig{Scope: []string{"core"},
					Checks: []project.ComponentCheck{{Target: project.ComponentRef{Source: source, Symbol: "Step"},
						Proof: &project.ComponentProof{Contracts: []project.ComponentRef{{Source: source, Symbol: "Contract"}}}}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			step, contract := inv.Checks[0].Target, inv.Checks[0].Proof.Contracts[0]
			proof := verify.ComponentProofResult{Target: step, Verifier: "Gobra", Passed: true,
				Verified: []string{step, contract}, Contracts: []string{contract}}
			for _, c := range inv.Components {
				if c.ID == step || c.ID == contract {
					proof.Inputs = append(proof.Inputs, c)
				}
			}
			for _, outcome := range []string{"successful", "failed", "missing"} {
				t.Run(outcome, func(t *testing.T) {
					result := proof
					if outcome == "failed" {
						result.Passed = false
						result.Errors = []string{"proof diagnostic begins: " + source + ": proof diagnostic ends"}
					}
					results := []verify.ComponentProofResult{result}
					if outcome == "missing" {
						results = nil
					}
					r := receiptAssuranceFixtures()[0]
					if err := r.AttributeComponentProofs(inv, results); err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(r)
					if err != nil {
						t.Fatal(err)
					}
					var wire struct {
						Components struct {
							Inventory project.ComponentInventory        `json:"inventory"`
							Proofs    []struct{ Target, Status string } `json:"proofs"`
						} `json:"components"`
					}
					if err := json.Unmarshal(encoded, &wire); err != nil {
						t.Fatal(err)
					}
					want := map[string]string{}
					for _, c := range inv.Components {
						want[c.ID] = "unchecked"
					}
					if outcome == "successful" {
						want[step] = "proved"
					}
					if len(wire.Components.Inventory.Components) != len(want) || len(wire.Components.Proofs) != len(want) {
						t.Fatal("JSON lost component identities or proof coverage")
					}
					for _, c := range wire.Components.Inventory.Components {
						if c.Source != source || want[c.ID] == "" {
							t.Fatalf("Markdown escaping changed the JSON identity: %#v", c)
						}
					}
					for _, p := range wire.Components.Proofs {
						if want[p.Target] == "" || p.Status != want[p.Target] {
							t.Fatalf("JSON proof status changed: %#v", p)
						}
					}
					var decoded verify.Report
					if err := json.Unmarshal(encoded, &decoded); err != nil {
						t.Fatal(err)
					}
					for _, report := range []*verify.Report{&r, &decoded} {
						md := receipt.Markdown(report)
						componentMDCheckTable(t, md, want)
						componentMDCheckOccurrences(t, md, source)
						if outcome == "failed" {
							// A diagnostic containing a filename is escaped too;
							// it cannot create another Markdown row or block.
							begin := strings.Index(md, "proof diagnostic begins:")
							end := strings.Index(md, ": proof diagnostic ends")
							if begin < 0 || end < begin || strings.ContainsAny(md[begin:end], "\r\n") {
								t.Fatalf("diagnostic disappeared or its filename split a line:\n%s", md)
							}
						}
					}
				})
			}
		})
	}
}

// Split a GFM table row before interpreting inline Markdown. Escaped pipes
// are literal even in code spans; every other pipe is a cell boundary.
func componentMDCells(line string) []string {
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line):
			if line[i+1] != '|' {
				cell.WriteByte('\\')
			}
			i++
			cell.WriteByte(line[i])
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(line[i])
		}
	}
	if len(cells) == 0 {
		return nil
	}
	cells = append(cells, strings.TrimSpace(cell.String()))
	if cells[0] == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells
}

func componentMDLines(md string) []string {
	return strings.Split(strings.ReplaceAll(strings.ReplaceAll(md, "\r\n", "\n"), "\r", "\n"), "\n")
}

func componentMDCheckTable(t *testing.T, md string, want map[string]string) {
	t.Helper()
	lines := componentMDLines(md)
	for i, line := range lines {
		header := componentMDCells(line)
		subject, proof := -1, -1
		for col, label := range header {
			switch strings.ToLower(strings.Trim(label, " *_`")) {
			case "component":
				subject = col
			case "proof":
				proof = col
			}
		}
		if subject < 0 || proof < 0 {
			continue
		}
		if i+1 >= len(lines) || len(componentMDCells(lines[i+1])) != len(header) {
			t.Fatalf("component table has no matching delimiter row:\n%s", md)
		}
		for _, cell := range componentMDCells(lines[i+1]) {
			if strings.Trim(cell, " :-") != "" || !strings.Contains(cell, "-") {
				t.Fatalf("component table has an invalid delimiter row: %q", lines[i+1])
			}
		}
		seen := map[string]bool{}
		for _, line := range lines[i+2:] {
			cells := componentMDCells(line)
			if len(cells) == 0 {
				break
			}
			if len(cells) != len(header) {
				t.Fatalf("source text changed table cell boundaries: %q", line)
			}
			identity, ok := componentMDLiteral(cells[subject])
			if !ok {
				t.Fatalf("source text escaped its literal/code span: %q", cells[subject])
			}
			id := ""
			for candidate := range want {
				if componentMDDisplayKey(identity) == componentMDDisplayKey(candidate) {
					id = candidate
				}
			}
			if id == "" || seen[id] {
				t.Fatalf("receipt has an invented, truncated or duplicate component: %q", cells[subject])
			}
			seen[id] = true
			status := strings.Trim(cells[proof], " *_`")
			if status != want[id] {
				t.Fatalf("rendered Proof for %q = %q, want %q", id, status, want[id])
			}
		}
		if len(seen) != len(want) {
			t.Fatalf("filename broke component rows: got %d, want %d:\n%s", len(seen), len(want), md)
		}
		return
	}
	t.Fatalf("receipt has no Component/Proof table:\n%s", md)
}

// A source may use a code span with a longer delimiter, escaped plain text,
// or an HTML code element with escaped contents. Accept visible escapes as
// well as literal text. Decoding entities inside backtick spans here is only
// identity comparison for visibly encoded filenames, not a claim that GFM
// decodes them there: cell boundaries and code-span boundaries are checked
// before that normalization.
func componentMDLiteral(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<code>") && strings.HasSuffix(s, "</code>") {
		body := s[len("<code>") : len(s)-len("</code>")]
		// Inline HTML does not suppress Markdown parsing inside its tags.
		return componentMDPlain(body)
	}
	if strings.HasPrefix(s, "`") {
		start, end, after := componentMDCodeSpan(s, 0)
		if after != len(s) || after == 0 {
			return "", false
		}
		return html.UnescapeString(s[start:end]), true
	}
	return componentMDPlain(s)
}

func componentMDPlain(s string) (string, bool) {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(s[i+1])) {
			i++
			out.WriteByte(s[i])
		} else if strings.ContainsRune("`*_[]<>~", rune(s[i])) {
			return "", false
		} else {
			out.WriteByte(s[i])
		}
	}
	return html.UnescapeString(out.String()), true
}

// Return the content and closing boundary of the first code span at pos.
// Only a backtick run of the exact opening length can close it.
func componentMDCodeSpan(s string, pos int) (start, end, after int) {
	start = pos
	for start < len(s) && s[start] == '`' {
		start++
	}
	for i := start; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		if j-i == start-pos {
			return start, i, j
		}
		i = j
	}
	return 0, 0, 0
}

// A safely displayed newline may be a visible escape, entity or space. It
// must not be a physical row break, and the rest of the identity must survive.
func componentMDDisplayKey(s string) string {
	s = strings.NewReplacer("\\r\\n", " ", "\\r", " ", "\\n", " ", "\r\n", " ", "\r", " ", "\n", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func componentMDCheckOccurrences(t *testing.T, md, source string) {
	t.Helper()
	occurrences := 0
	for _, line := range componentMDLines(md) {
		// Escaping a slash is optional and cannot change these boundaries.
		line = strings.NewReplacer("\\/", "/", "&#47;", "/", "&#x2F;", "/", "&#x2f;", "/").Replace(line)
		// Record complete inline code spans, without treating escaped
		// backticks in prose as openers. Inline HTML contents still need
		// Markdown escaping even when the tag happens to be code.
		var spans []struct {
			start, end int
			html       bool
		}
		for i := 0; i < len(line); {
			if line[i] == '\\' && i+1 < len(line) {
				i += 2
			} else if strings.HasPrefix(line[i:], "<code>") {
				end := strings.Index(line[i+6:], "</code>")
				if end < 0 {
					break
				}
				spans = append(spans, struct {
					start, end int
					html       bool
				}{i + 6, i + 6 + end, true})
				i += 6 + end + 7
			} else if line[i] == '`' {
				start, end, after := componentMDCodeSpan(line, i)
				if after > 0 {
					spans = append(spans, struct {
						start, end int
						html       bool
					}{start, end, false})
					i = after
				} else {
					for i < len(line) && line[i] == '`' {
						i++
					}
				}
			} else {
				i++
			}
		}
		for offset := 0; offset < len(line); {
			start := strings.Index(line[offset:], "core/x")
			if start < 0 {
				break
			}
			start += offset
			end := strings.Index(line[start:], ".go")
			if end < 0 {
				t.Fatalf("filename ended a Markdown row: %q", line)
			}
			end += start + len(".go")
			fragment := strings.ReplaceAll(line[start:end], "\\|", "|")
			literal := false
			for _, span := range spans {
				if start < span.end && end > span.start {
					if start < span.start || end > span.end {
						t.Fatalf("filename ended or entered a code span: %q", line)
					}
					literal = !span.html
				}
			}
			decoded, ok := html.UnescapeString(fragment), true
			if !literal {
				decoded, ok = componentMDPlain(fragment)
			}
			if !ok || componentMDDisplayKey(decoded) != componentMDDisplayKey(source) {
				t.Fatalf("source occurrence is active Markdown or lost identity: %q", line)
			}
			occurrences++
			offset = end
		}
	}
	if occurrences < 3 {
		t.Fatalf("receipt omitted source identities: %d occurrences of %q", occurrences, source)
	}
}
