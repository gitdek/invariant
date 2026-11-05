package receipt

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/gitdek/invariant/internal/verify"
)

// Write saves one receipt and its derived reader files in the archive tree.
// Reusing a directory cannot leave evidence from a previous reader behind.
func Write(dir string, r *verify.Report) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"reader.json", "reader.svg"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	reader, svg := renderReader(r.Reader)
	copyReport := *r
	copyReport.Reader = reader
	r = &copyReport
	writeJSON := func(name string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o644)
	}
	if err := writeJSON("receipt.json", r); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "receipt.md"), []byte(Markdown(r)), 0o644); err != nil {
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
