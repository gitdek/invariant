package synth

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
)

func readerInput(m project.Manifest, rel string) bool {
	return rel == ".invariant/comparison.json" || rel == ".invariant/view.json" || rel != m.Module &&
		path.Dir(rel) == path.Dir(m.Module) && strings.HasSuffix(rel, ".tla")
}

// These inputs belong to the original project, even when the model itself
// is rebuilt. Derived proposal files never supply the final gate's reader.
func copyReaderInputs(p *project.Project, dst string) error {
	comparison, err := regular.ReadFile(p.Dir, ".invariant/comparison.json")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := writeFile(filepath.Join(dst, ".invariant/comparison.json"), string(comparison)); err != nil {
			return err
		}
	} else if err := os.Remove(filepath.Join(dst, ".invariant/comparison.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	view, err := regular.ReadFile(p.Dir, ".invariant/view.json")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := writeFile(filepath.Join(dst, ".invariant/view.json"), string(view)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(filepath.Dir(p.ModulePath()))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		rel := path.Join(path.Dir(p.Manifest.Module), entry.Name())
		if !readerInput(p.Manifest, rel) {
			continue
		}
		data, err := regular.ReadFile(p.Dir, rel)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dst, filepath.FromSlash(rel)), string(data)); err != nil {
			return err
		}
	}
	return nil
}
