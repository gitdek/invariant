package repository

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectsDirectoryIsCapturedAndValidated779(t *testing.T) {
	config := contract461(part461("web", ".", "typescript"))
	config["projects_directory"] = "checks/modeled projects"
	root := repo461(t, config)
	base := commit461(t, root)
	s, err := Load(t.Context(), root, base)
	if err != nil {
		t.Fatalf("a safe, not-yet-created projects directory must load: %v", err)
	}
	readDirectory := func(s *Snapshot) string {
		t.Helper()
		b, err := json.Marshal(s.Config())
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(b, &fields); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := json.Unmarshal(fields["projects_directory"], &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	config["projects_directory"] = "src"
	write461(t, root, Path, json461(t, config))
	commit461(t, root)
	if got := readDirectory(s); got != "checks/modeled projects" {
		t.Fatalf("capture changed with HEAD: %q", got)
	}
	again, err := Load(t.Context(), root, base)
	if err != nil || readDirectory(again) != "checks/modeled projects" {
		t.Fatalf("base was replaced: %v", err)
	}
	trusted, err := s.IsTrusted(Path)
	if err != nil || !trusted {
		t.Fatalf("projects policy lost contract trust: %v", err)
	}

	// Omission remains omitted on serialization, retaining existing native
	// execution digests; the execution layer supplies the invariant default.
	legacy := contract461(part461("web", ".", "typescript"))
	legacyRoot := repo461(t, legacy)
	old, err := Load(t.Context(), legacyRoot, commit461(t, legacyRoot))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(old.Config())
	if err != nil || strings.Contains(string(encoded), "projects_directory") {
		t.Fatalf("legacy serialization changed: %s, %v", encoded, err)
	}

	for _, value := range []any{"", ".", "..", "../outside", "/outside", "web/../src", "models/", `web\models`, "models/**", ".git/models", ".invariant", ".github", nil, 7, []string{"models"}} {
		c := contract461(part461("web", ".", "typescript"))
		c["projects_directory"] = value
		r := repo461(t, c)
		_, err := Load(t.Context(), r, commit461(t, r))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "projects_directory") {
			t.Errorf("invalid projects_directory %v: %v", value, err)
		}
	}
	for _, mode := range []string{"part", "input", "lock", "file", "symlink"} {
		c := contract461(part461("web", ".", "typescript"))
		c["projects_directory"] = "models"
		if mode == "part" {
			c["parts"] = append(c["parts"].([]map[string]any), part461("model", "models/one", "go"))
		}
		r := repo461(t, c)
		switch mode {
		case "input", "lock":
			key := "inputs"
			if mode == "lock" {
				key = "lockfiles"
			}
			p := c["parts"].([]map[string]any)[0]
			p[key] = append(p[key].([]string), "models/policy.json")
			write461(t, r, "models/policy.json", "{}")
			write461(t, r, Path, json461(t, c))
		case "file":
			write461(t, r, "models", "not a directory")
		case "symlink":
			if err := os.Symlink("src", filepath.Join(r, "models")); err != nil {
				t.Fatal(err)
			}
		}
		_, err := Load(t.Context(), r, commit461(t, r))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "projects_directory") {
			t.Errorf("conflicting %s declaration: %v", mode, err)
		}
	}
}
