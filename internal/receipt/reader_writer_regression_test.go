package receipt_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// The legacy goldens still check every original byte. The only addition must
// be the exact explanation; removing it lets their original hashes stand.
func readerLegacyBody(t *testing.T, md string) string {
	t.Helper()
	const explanation = "\nNo reader chart is available. Supply .invariant/view.json and verify again.\n"
	if !strings.HasSuffix(md, explanation) {
		t.Fatal("legacy receipt lacks its no-reader explanation")
	}
	return strings.TrimSuffix(md, explanation)
}

func TestReaderWriterReuseAndRenderFailure(t *testing.T) {
	r := receipt615Report(t, receipt615Chart(t), false)
	dir := t.TempDir()
	if err := receipt.Write(dir, r); err != nil {
		t.Fatal(err)
	}
	r.Reader.Chart.States[0].Label += "\x01"
	before := receipt615JSON(t, r)
	if err := receipt.Write(dir, r); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, receipt615JSON(t, r)) {
		t.Fatal("writing a diagnostic mutated the supplied report")
	}
	data, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved verify.Report
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Passed || saved.Reader.Chart != nil || saved.Reader.Unavailable == nil || saved.Reader.Unavailable.Code != "reader_render" || verify.Fingerprint(saved) != verify.Fingerprint(*r) {
		t.Fatal("render failure changed evidence or kept a chart")
	}
	if !reflect.DeepEqual(saved.Reader.Unavailable.Source, &r.Reader.Chart.Report.Source) {
		t.Fatal("render failure discarded the computed tool provenance")
	}
	md, err := os.ReadFile(filepath.Join(dir, "receipt.md"))
	if err != nil || string(md) != receipt.Markdown(r) || strings.Contains(string(md), "stateDiagram-v2") {
		t.Fatalf("render failure did not retain consistent diagnostic Markdown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reader.svg")); !os.IsNotExist(err) {
		t.Fatal("obsolete SVG survived render failure")
	}
	r.Reader = nil
	if err := receipt.Write(dir, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reader.json")); !os.IsNotExist(err) {
		t.Fatal("obsolete reader JSON survived a legacy receipt")
	}
	// Cleanup failures are output errors, not silent success.
	if err := os.Mkdir(filepath.Join(dir, "reader.svg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reader.svg", "keep"), []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := receipt.Write(dir, r); err == nil {
		t.Fatal("discarded an output cleanup error")
	}
}
