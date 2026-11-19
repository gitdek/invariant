package receipt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

// Write saves one receipt and its derived reader files in the archive tree.
// Reusing a directory cannot leave evidence from a previous reader behind.
func Write(dir string, r *verify.Report) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"reader.json", "reader.svg", "reader-comparison.json", "reader-baseline.json", "reader-candidate.json"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	reader, svg := renderReader(r.Reader)
	copyReport := *r
	copyReport.Reader = reader
	r = &copyReport
	if reader != nil && reader.Comparison != nil {
		c := reader.Chart
		if c == nil {
			c = reader.Comparison.Candidate.Chart
			if c == nil {
				c = reader.Comparison.Baseline.Chart
			}
		}
		var l *statechart.Limitation
		if c != nil {
			var err error
			l, err = statechart.EvidenceLimit(r, c.Report.Source, int(c.Report.Counts["tlc_states"]), len(c.States), "comparison_and_final", func() string { return Markdown(r) }, func() (string, error) { return svg, nil })
			if err != nil {
				return err
			}
		}
		if l != nil {
			data, files, err := statechart.ComparisonFiles(reader.Comparison)
			if err != nil {
				return err
			}
			files["reader-comparison.json"] = data
			copyReader := *reader
			copyReader.ComparisonRef = statechart.NewEvidenceReference("reader-comparison.json", data, reader.Comparison.Fingerprint, l)
			copyReader.FinalSHA256 = reader.FinalDigest()
			copyReader.Comparison = nil
			reader = &copyReader
			r.Reader = reader
			svg, err = reader.SVG()
			if err != nil {
				return err
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
					return err
				}
			}
		}
	}
	writeJSON := func(name string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		if len(data)+1 > statechart.MaxEvidenceBytes {
			return fmt.Errorf("%s exceeds complete evidence entry budget", name)
		}
		return os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o644)
	}
	if err := writeJSON("receipt.json", r); err != nil {
		return err
	}
	md := Markdown(r)
	if len(md) > statechart.MaxEvidenceBytes || len(svg) > statechart.MaxEvidenceBytes {
		return fmt.Errorf("rendered receipt exceeds complete evidence entry budget")
	}
	if err := os.WriteFile(filepath.Join(dir, "receipt.md"), []byte(md), 0o644); err != nil {
		return err
	}
	if r.Reader != nil {
		if err := writeJSON("reader.json", r.Reader); err != nil {
			return err
		}
	}
	if svg != "" {
		return os.WriteFile(filepath.Join(dir, "reader.svg"), []byte(svg), 0o644)
	}
	return nil
}
