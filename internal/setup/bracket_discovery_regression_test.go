package setup_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBracketedSharedEvidenceStaysUnresolved(t *testing.T) {
	for _, name := range []string{"config/[shared].json", "config/open[.json", "config/close].json", ".github/workflows/[check].yml"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"one", "two"} {
				node727(t, filepath.Join(root, dir), "vitest", 3)
				if !strings.HasPrefix(name, ".github/") {
					write727(t, root, dir+"/tsconfig.json", json727(t, map[string]string{"extends": "../" + name}))
				}
			}
			write727(t, root, name, "{}\n")
			before := tree727(t, root)
			data, config, issues := draft727(t, root)
			if len(issues) != 1 {
				t.Fatalf("want only the shared selector requirement, got %v", issues)
			}
			issue727(t, issues, name, "ownership", "brackets")
			for _, shared := range config.SharedPaths {
				if strings.ContainsAny(shared.Path, "[]") {
					t.Errorf("draft emitted an invalid shared selector: %+v", shared)
				}
			}
			if !strings.HasPrefix(name, ".github/") {
				for _, part := range config.Parts {
					has727(t, part.Inputs, name)
				}
			}
			again, _, againIssues := draft727(t, root)
			if string(again) != string(data) || !slices.Equal(againIssues, issues) || !reflect.DeepEqual(before, tree727(t, root)) {
				t.Fatal("discovery must be deterministic and read-only")
			}
			refused727(t, root, data)
		})
	}
}

func TestBracketedPartsRetainNativeSelectorRequirements(t *testing.T) {
	for _, framework := range []string{"pytest", "vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			root := t.TempDir()
			const dir = "apps/[route]"
			language := "typescript"
			if framework == "pytest" {
				language = "python"
				python727(t, filepath.Join(root, dir), "requirements.lock")
			} else {
				node727(t, filepath.Join(root, dir), framework, 3)
			}
			data, config, issues := draft727(t, root)
			part := part727(t, config, dir, language)
			for _, field := range []string{"test", "test_one"} {
				missing727(t, part, field)
				issue727(t, issues, dir, field)
			}
			for _, issue := range issues {
				if strings.Contains(issue, "rename unsafe") {
					t.Errorf("ordinary bracketed path was rejected: %s", issue)
				}
			}
			refused727(t, root, data)
		})
	}
}
