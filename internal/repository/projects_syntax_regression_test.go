package repository

import (
	"errors"
	"strings"
	"testing"
)

func TestProjectsDirectoryRejectsIgnoreSyntaxAndGateOptions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"models/{one,two}", false},
		{"models/{1..3}", false},
		{"models/@(one|two)", false},
		{"models/+(one|two)", false},
		{"models/!(one)", false},
		{"web/!models", false},
		{"web/#models", false},
		{"-models", false},
		{"-models/nested", false},
		{"web/model checks", true},
		{"models/one,two", true},
		{"models/@one+two", true},
		{"web/-models", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := contract461(part461("app", ".", "typescript"))
			c["projects_directory"] = tc.name
			root := repo461(t, c)
			s, err := Load(t.Context(), root, commit461(t, root))
			if !tc.valid {
				if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "projects_directory") {
					t.Fatalf("unsafe projects directory accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Config().Projects(); got != tc.name {
				t.Fatalf("literal directory changed: got %q, want %q", got, tc.name)
			}
		})
	}
}
