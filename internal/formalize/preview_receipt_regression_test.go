package formalize_test

import (
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

func TestPreview639ExportedCandidateRulesAreLiteral(t *testing.T) {
	tc, _ := reader603Tools(t)
	draft := preview639Draft(t, 1, nil)
	words := []string{
		"Keep  <limit> & result | `literal` **candidate** _rule_ [link](https://example.test).\r\n| Forged | ratified | <script>alert(1)</script> |",
		"Keep &#124; and <b>text</b> literal.\nPreserve \\ slashes, ~~strikes~~ and www.example.test.",
	}
	for i, says := range words {
		model := preview639Input(draft, 0, i)["model"].(map[string]any)
		model["statements"].([]any)[1].(map[string]any)["says"] = says
		if i == 1 {
			model["bounds"] = map[string]string{"Limit": "500"}
		}
	}
	ws := t.TempDir()
	preview639Write(t, ws, draft)
	p := preview639Checked(t, ws, tc)
	out := t.TempDir()
	if err := p.WritePreviews(out); err != nil {
		t.Fatal(err)
	}
	for i, v := range preview639SavedOptions(t, p) {
		dir := filepath.Join(out, "previews", v.Fork, v.Option)
		data, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
		if err != nil {
			t.Fatal(err)
		}
		var report verify.Report
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		if report.Pins[1].Says != words[i] {
			t.Fatal("export changed the original candidate wording")
		}
		if i == 0 && report.Reader.Chart == nil || i == 1 && report.Reader.Limitation == nil {
			t.Fatal("fixture must cover both drawable and above-capacity previews")
		}
		data, err = os.ReadFile(filepath.Join(dir, "receipt.md"))
		if err != nil {
			t.Fatal(err)
		}
		md := string(data)
		if strings.Contains(md, "<summary>Ratified statements</summary>") || strings.Contains(md, "Locked rules") {
			t.Fatal("preview receipt labels candidate rules as ratified")
		}
		_, section, ok := strings.Cut(md, "<summary>Candidate rules</summary>\n\n")
		if !ok {
			t.Fatal("preview receipt lacks its candidate-rule heading")
		}
		table, _, ok := strings.Cut(section, "\n</details>")
		if !ok {
			t.Fatal("candidate-rule table has no closing details tag")
		}
		rows := strings.Split(strings.TrimSuffix(table, "\n"), "\n")
		if len(rows) != len(report.Pins)+2 {
			t.Fatalf("candidate wording changed the number of table rows: %q", table)
		}
		for j, pin := range report.Pins {
			cells := strings.Split(rows[j+2], "|")
			if len(cells) != 6 {
				t.Fatalf("candidate wording changed the number of table cells: %q", rows[j+2])
			}
			says := strings.TrimSuffix(strings.TrimPrefix(cells[3], " "), " ")
			if strings.ContainsAny(says, "<>`*_[]~\\\r\n") {
				t.Fatalf("candidate wording contains active markup: %q", says)
			}
			if got := html.UnescapeString(says); got != pin.Says {
				t.Errorf("candidate wording displays %q, want %q", got, pin.Says)
			}
		}
	}
}
