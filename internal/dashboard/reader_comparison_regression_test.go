package dashboard

import (
	"bytes"
	"strings"
	"testing"
)

// Even an unrenderable receipt comparison cannot displace independently bound
// final evidence. Only native comparison panels may differ in the companion.
func TestReaderComparisonFallbackChecksCompleteNativeFinalPanels(t *testing.T) {
	f := receipt723Fixture(t)
	e := receipt723Evidence(t, f)
	e.Report.Reader.Comparison.Baseline.Model += "changed comparison source"
	receipt723Seal(t, e.Report, e.Lock)
	e.Artifacts.JSON = reader631JSON(t, e.Report.Reader)
	before := reader631JSON(t, e.Report)
	status, message, svg := e.checked()
	if status != "chart" || !strings.Contains(message, "comparison_evidence") {
		t.Fatalf("comparison source damage displaced final chart: %s %s", status, message)
	}
	receipt723Topology(t, []byte(svg), f.Report.Reader.Chart)
	if !bytes.Equal(before, reader631JSON(t, e.Report)) {
		t.Fatal("fallback changed the retained receipt")
	}
	for _, mode := range []string{"root style", "final wrapper", "final title", "final report", "comparison script"} {
		t.Run(mode, func(t *testing.T) {
			bad := e
			raw := string(e.Artifacts.SVG)
			switch mode {
			case "root style":
				raw = strings.Replace(raw, "<svg ", `<svg opacity="0" `, 1)
			case "final wrapper":
				at := strings.LastIndex(raw, `<g transform="translate(0 `)
				raw = raw[:at] + strings.Replace(raw[at:], `<g transform=`, `<g opacity="0" transform=`, 1)
			case "final title":
				raw = strings.Replace(raw, "Independently checked final-model chart", "Substituted final source", 1)
			case "final report":
				at := strings.LastIndex(raw, `id="panel3-chart-report"`)
				if at < 0 {
					t.Fatal("missing final report fixture")
				}
				raw = raw[:at] + `opacity="0" ` + raw[at:]
			case "comparison script":
				raw = strings.Replace(raw, `<g transform="translate(0 0)">`, `<g transform="translate(0 0)"><script>active()</script>`, 1)
			}
			bad.Artifacts.SVG = []byte(raw)
			status, _, svg := bad.checked()
			if status != "artifact_mismatch" || svg != "" {
				t.Fatalf("final companion damage accepted: %s", status)
			}
		})
	}
}

func TestReaderEmptyComparisonPresenceSurvivesEncoding(t *testing.T) {
	f := receipt723Fixture(t)
	e := receipt723Evidence(t, f)
	e.Comparison = []byte{}
	var restored readerEvidence
	if err := decodeReaderJSON(readerJSON(e), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Comparison == nil {
		t.Fatal("an empty comparison became an absent file")
	}
	_, message, _ := restored.checked()
	if !strings.Contains(message, "EOF") {
		t.Fatalf("empty file lost its parser diagnostic: %s", message)
	}
	if makeReader(e).presentation != makeReader(restored).presentation {
		t.Fatal("empty comparison changed identity after encoding")
	}
}
