package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

func TestInitRefusesProjectsSyntaxBeforeEitherWrite(t *testing.T) {
	for _, name := range []string{"models/{one,two}", "models/{1..3}", "models/@(one|two)", "web/!models", "web/#models", "-models", "-models/nested"} {
		for _, source := range []string{"flag", "retained contract"} {
			t.Run(name+"/"+source, func(t *testing.T) {
				root := t.TempDir()
				files := map[string]string{
					"package.json":      `{"name":"app","private":true}`,
					"tsconfig.json":     `{"include":["**/*.ts"]}`,
					"eslint.config.mjs": "export default [];\n",
				}
				args := []string{"-repo", "acme/app", "-invariant", initPin}
				if source == "flag" {
					args = append(args, "-projects", name)
				} else {
					b, err := json.Marshal(map[string]string{"projects_directory": name})
					if err != nil {
						t.Fatal(err)
					}
					files[repository.Path] = string(b)
					files[setup.WorkflowPath] = "name: customized workflow\n"
					args = append(args, "-force")
				}
				for file, body := range files {
					initWrite727(t, root, file, body)
				}
				code, printed := initPrints(t, append(args, root)...)
				if code == 0 || !strings.Contains(printed, "projects_directory") {
					t.Fatalf("unsafe projects directory accepted: %d\n%s", code, printed)
				}
				for _, destination := range []string{repository.Path, setup.WorkflowPath} {
					if _, exists := files[destination]; !exists {
						if _, err := os.Lstat(filepath.Join(root, destination)); !os.IsNotExist(err) {
							t.Errorf("invalid projects directory wrote %s: %v", destination, err)
						}
					}
				}
				for file, want := range files {
					if got := string(initRead727(t, root, file)); got != want {
						t.Errorf("invalid projects directory changed %s", file)
					}
				}
			})
		}
	}
}
