package repository

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectsDefaultAndAncestorConflicts(t *testing.T) {
	for _, kind := range []string{"part", "input", "file ancestor", "link ancestor"} {
		t.Run(kind, func(t *testing.T) {
			c := contract461(part461("app", ".", "typescript"))
			if kind == "part" {
				c["parts"] = append(c["parts"].([]map[string]any), part461("model", "invariant/one", "go"))
			}
			if strings.HasSuffix(kind, "ancestor") {
				c["projects_directory"] = "models/nested"
			}
			root := repo461(t, c)
			switch kind {
			case "input":
				p := c["parts"].([]map[string]any)[0]
				p["inputs"] = append(p["inputs"].([]string), "invariant/policy.json")
				write461(t, root, "invariant/policy.json", "{}")
				write461(t, root, Path, json461(t, c))
			case "file ancestor":
				write461(t, root, "models", "application file\n")
			case "link ancestor":
				if err := os.Symlink("src", filepath.Join(root, "models")); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Load(t.Context(), root, commit461(t, root))
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "projects_directory") {
				t.Fatalf("conflict accepted: %v", err)
			}
		})
	}
}
