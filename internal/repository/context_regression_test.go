package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestContextKeepsCapturedRulesAndDocuments(t *testing.T) {
	root := repo461(t, contract461(part461("app", "app", "go")))
	write461(t, root, "app/AGENTS.override.md", "Nested rule.\n```text\nLiteral fence.\n```\n")
	write461(t, root, "policy/rule.md", "Linked rule.\n")
	if err := os.Symlink("../policy/rule.md", filepath.Join(root, "app", "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	base := commit461(t, root)
	snapshot := load461(t, root, base)
	// Moving HEAD and replacing working files cannot change the captured text.
	write461(t, root, "AGENTS.md", "Replacement rules.\n")
	write461(t, root, "SPEC.md", "Replacement state.\n")
	write461(t, root, "policy/rule.md", "Replacement link target.\n")
	commit461(t, root)
	got, err := snapshot.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantRules := []ContextFile{
		{Path: "AGENTS.md", Text: "Read SPEC.md before changing code.\n"},
		{Path: "app/AGENTS.override.md", Text: "Nested rule.\n```text\nLiteral fence.\n```\n"},
		{Path: "app/CLAUDE.md", Target: "policy/rule.md", Text: "Linked rule.\n"},
	}
	if got.Base != base || !reflect.DeepEqual(got.Rules, wantRules) || got.CurrentState[0].Text != "The current application.\n" || !slices.Contains(got.Paths, "app/CLAUDE.md") {
		t.Fatalf("context lost captured sources: %+v", got)
	}
	got.Rules[0].Text = "mutated return"
	got.Config.Parts[0].Commands["test"] = Command{Argv: []string{"replacement"}}
	again, err := snapshot.Context(t.Context())
	if err != nil || !reflect.DeepEqual(again.Rules, wantRules) || reflect.DeepEqual(again.Config, got.Config) {
		t.Fatalf("context leaked mutable snapshot state: %+v, %v", again, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := snapshot.Context(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached context ignored cancellation: %v", err)
	}
}
