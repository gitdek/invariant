package receipt_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

// Pin the sole legacy Markdown addition and keep the existing whole-receipt
// digests for every other byte, as well as their evidence fingerprints.
func reader615LegacyMarkdown(t *testing.T, md string) string {
	t.Helper()
	const notice = "\nNo reader chart is available. Supply .invariant/view.json and verify again.\n"
	if strings.Count(md, notice) != 1 {
		t.Fatal("legacy receipt must include exactly one no-reader explanation")
	}
	return strings.Replace(md, notice, "", 1)
}

func TestReaderReceiptRetainsExactTraceNumbers(t *testing.T) {
	chart := receipt615Chart(t)
	chart.Report.Temporal = []tlc.TraceFile{{Module: "Receipt615", Check: "Eventually", Stutters: true,
		States: []tlc.TraceState{{Index: 1, Action: "Init", Vars: map[string]any{"nested": []any{json.Number("9007199254740993")}}}}}}
	r := receipt615Report(t, chart, false)
	before := receipt615JSON(t, r)
	if !bytes.Contains(before, []byte("9007199254740993")) {
		t.Fatal("decoding rounded an exact trace value")
	}
	var decoded verify.Report
	if err := json.Unmarshal(before, &decoded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, receipt615JSON(t, &decoded)) {
		t.Fatal("reader trace changed on a second round trip")
	}
}

func TestReaderWriterRenderingFailureAndLegacyReuse(t *testing.T) {
	r := receipt615Report(t, receipt615Chart(t), false)
	out := t.TempDir()
	if err := receipt.Write(out, r); err != nil {
		t.Fatal(err)
	}
	// A corrupt decoded chart is explanatory failure, not an output error.
	r.Reader.Chart.States[0].Label += "\x01"
	before := receipt615JSON(t, r)
	if err := receipt.Write(out, r); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, receipt615JSON(t, r)) {
		t.Fatal("writer mutated the input report")
	}
	if _, err := os.Stat(filepath.Join(out, "reader.svg")); !os.IsNotExist(err) {
		t.Fatal("stale drawable chart survived a rendering failure")
	}
	data, err := os.ReadFile(filepath.Join(out, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved verify.Report
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Passed || saved.Fingerprint != r.Fingerprint || verify.Fingerprint(saved) != verify.Fingerprint(*r) ||
		saved.Reader.Chart != nil || saved.Reader.Unavailable.Code != "render_failed" {
		t.Fatal("render failure changed evidence or retained a partial chart")
	}
	md, err := os.ReadFile(filepath.Join(out, "receipt.md"))
	if err != nil || string(md) != receipt.Markdown(r) || strings.Contains(string(md), "stateDiagram-v2") {
		t.Fatalf("rendered diagnostic disagrees with receipt: %v", err)
	}
	r.Reader = nil
	if err := receipt.Write(out, r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reader.json", "reader.svg"} {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy receipt retained obsolete %s", name)
		}
	}
}

func TestReaderWriterPreservesArtifactOutputErrors(t *testing.T) {
	r := receipt615Report(t, receipt615Chart(t), true)
	out := t.TempDir()
	blocked := filepath.Join(out, "reader.svg", "keep")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := receipt.Write(out, r); err == nil {
		t.Fatal("writer suppressed an artifact cleanup error")
	}
	if _, err := os.Stat(blocked); err != nil {
		t.Fatal("writer recursively removed an unexpected directory")
	}
}
