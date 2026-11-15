package setup_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

func TestProjectsGuidanceUsesEveryEnclosingPart(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".", "web", "sibling", "web/model checks/project"} {
		write727(t, root, filepath.ToSlash(filepath.Join(dir, "package.json")), `{"name":"fixture","private":true}`)
	}
	selected, advice, err := setup.InitProjects(root, "web/model checks")
	if err != nil || selected != "web/model checks" || len(advice) != 2 {
		t.Fatalf("projects guidance: %q %q %v", selected, advice, err)
	}
	for i, want := range []string{`"web/model checks/**"`, `"model checks/**"`} {
		if !strings.Contains(advice[i], want) || !strings.Contains(advice[i], "exclude") || !strings.Contains(advice[i], "ESLint ignores") {
			t.Errorf("guidance %d: %s", i, advice[i])
		}
	}
	data, _, err := setup.DraftRepositoryWithProjects(root, selected)
	if err != nil {
		t.Fatal(err)
	}
	write727(t, root, repository.Path, string(data))
	kept, again, err := setup.InitProjects(root, "different")
	if err != nil || kept != selected || strings.Join(again, "\n") != strings.Join(advice, "\n") {
		t.Fatalf("retained guidance: %q %q %v", kept, again, err)
	}
	var config repository.Config
	if err := json.Unmarshal(data, &config); err != nil || len(config.Parts) != 3 {
		t.Fatalf("custom discovery: %+v, %v", config, err)
	}
}

func TestProjectsInitRefusesNonDirectoryAncestors(t *testing.T) {
	for _, kind := range []string{"file", "link"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if kind == "file" {
				write727(t, root, "models", "application bytes\n")
			} else if err := os.Symlink(t.TempDir(), filepath.Join(root, "models")); err != nil {
				t.Fatal(err)
			}
			if _, _, err := setup.InitProjects(root, "models/nested"); err == nil || !strings.Contains(err.Error(), "projects_directory") {
				t.Fatalf("unsafe ancestor: %v", err)
			}
			if _, _, err := setup.DraftRepositoryWithProjects(root, "models/nested"); err == nil {
				t.Fatal("draft accepted unsafe ancestor")
			}
		})
	}
}
