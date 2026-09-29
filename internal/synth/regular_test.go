package synth

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
)

// A build takes only regular files from the agent's workspace (#153). A
// link to a file on the host, standing in for the factory's key, left where
// the agent's model or code goes, is refused by name, and the file's text
// never reaches the project, whether the link is symbolic or hard.
func TestABuildTakesNoLinkFromItsWorkspace(t *testing.T) {
	const secret = "-----BEGIN PRIVATE KEY----- stand-in"
	for _, c := range []struct {
		name, file string
		link       func(key, at string) error
	}{
		{"code as a symbolic link", "src/machine.ts", os.Symlink},
		{"code as a hard link", "src/machine.ts", os.Link},
		{"the model as a symbolic link", ".invariant/specs/Buf.tla", os.Symlink},
		{"the model as a hard link", ".invariant/specs/Buf.tla", os.Link},
	} {
		t.Run(c.name, func(t *testing.T) {
			src, ws, dst := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "out")
			key := filepath.Join(t.TempDir(), "factory.pem")
			m := project.Manifest{Name: "buf", Module: ".invariant/specs/Buf.tla", Code: "src", Language: "typescript", Conformance: "conformance.ts", Exhaustive: true}
			for dir, files := range map[string]map[string]string{
				src: {".invariant/ratified.lock": "lock", "package.json": `{"name": "buf"}`, ".invariant/specs/Buf.tla": "draft"},
				ws:  {".invariant/ratified.lock": "lock", "package.json": `{"name": "buf"}`, ".invariant/specs/Buf.tla": "model", "src/machine.ts": "code", "conformance.ts": "driver"},
			} {
				for name, text := range files {
					if err := writeFile(filepath.Join(dir, name), text); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := os.WriteFile(key, []byte(secret), 0o600); err != nil {
				t.Fatal(err)
			}
			at := filepath.Join(ws, filepath.FromSlash(c.file))
			if err := os.Remove(at); err != nil {
				t.Fatal(err)
			}
			if err := c.link(key, at); err != nil {
				t.Fatal(err)
			}
			err := Assemble(&project.Project{Dir: src, Manifest: m}, ws, dst)
			var refused *regular.Refused
			if !errors.As(err, &refused) || refused.File != c.file {
				t.Errorf("Assemble = %v; want %s refused", err, c.file)
			}
			filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					if b, _ := os.ReadFile(p); strings.Contains(string(b), secret) {
						t.Errorf("%s holds the key's text", p)
					}
				}
				return nil
			})
		})
	}
}
