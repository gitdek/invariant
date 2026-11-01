package plumbing

import (
	"io/fs"
	"os"
	"path/filepath"
)

// RemoveCache removes a run's Go build cache once the run has ended (#366).
// The sandbox writes it as the user who runs the factory, and what it leaves
// can be read-only, so a directory that refuses its removal is made
// writable first. A symbolic link in it is removed as a link, never
// followed, so nothing outside the cache changes. An empty dir removes
// nothing.
func RemoveCache(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		// WalkDir doesn't follow links, so d is a link's own entry. Check
		// again right before changing a directory, so a link that took its
		// place isn't followed either.
		if d != nil && d.IsDir() {
			if info, err := os.Lstat(p); err == nil && info.IsDir() {
				os.Chmod(p, info.Mode().Perm()|0o700)
			}
		}
		return nil
	})
	return os.RemoveAll(dir)
}
