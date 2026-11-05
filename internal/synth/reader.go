package synth

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
)

// Reader inputs belong to the saved project. An agent may edit the model,
// but cannot replace its saved explanation or sibling modules at assembly.
func readerInput(m project.Manifest, rel string) bool {
	return rel == ".invariant/view.json" ||
		rel != m.Module && path.Dir(rel) == path.Dir(m.Module) && strings.HasSuffix(rel, ".tla")
}

func copyReaderInputs(p *project.Project, dst string) error {
	view, err := regular.ReadFile(p.Dir, ".invariant/view.json")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := writeFile(filepath.Join(dst, ".invariant/view.json"), string(view)); err != nil {
			return err
		}
	}
	dir := filepath.Dir(p.Manifest.Module)
	entries, err := os.ReadDir(filepath.Join(p.Dir, dir))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		rel := filepath.ToSlash(filepath.Join(dir, entry.Name()))
		if !readerInput(p.Manifest, rel) {
			continue
		}
		b, err := regular.ReadFile(p.Dir, rel)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dst, filepath.FromSlash(rel)), string(b)); err != nil {
			return err
		}
	}
	return nil
}
