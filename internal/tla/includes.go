package tla

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Siblings reads the modules beside a model's main module: every other .tla
// file in dir, by file name. They are what Read takes as a model's includes,
// and what TLC can load when the model extends one. A file that's gone by
// the time it's read is left out: checks that share a directory write their
// own modules there and remove them, and a model that extends a module
// that's missing is refused when it's read.
func Siblings(dir, module string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	includes := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tla") || e.Name() == module {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		includes[e.Name()] = string(b)
	}
	return includes, nil
}

// ValidIncludeName accepts ASCII TLA+ identifiers in sibling .tla files,
// distinct from the main module and from every module TLC ships, which a
// file of that name would replace. Callers still check regular files on read.
func ValidIncludeName(name, module string) bool {
	stem := strings.TrimSuffix(name, ".tla")
	return strings.HasSuffix(name, ".tla") && isIdentifier(stem) && name != module+".tla" && !shipped[stem]
}
