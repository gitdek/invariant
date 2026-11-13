package repository

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryOwnsLiteralBracketPaths877(t *testing.T) {
	t.Run("ordinary paths and captured trust", func(t *testing.T) {
		c := contract461(part461("app", "app", "typescript"), part461("neighbor", "app/s", "typescript"))
		root := repo461(t, c)
		const route = "app/[slug]/page.tsx"
		write461(t, root, route, "route base\n")
		write461(t, root, "app/s/page.tsx", "neighbor base\n")
		write461(t, root, "app/[slug]/AGENTS.md", "route rules\n")
		if err := os.Symlink("s", filepath.Join(root, "app/[link]")); err != nil {
			t.Fatal(err)
		}
		s := load461(t, root, commit461(t, root))
		for _, name := range []string{route, "app/[slug]/new.tsx", "app/[[...slug]]/page.tsx", "app/[open/page.tsx", "app/close]/page.tsx", "app/(marketing)/page.tsx"} {
			parts461(t, s, []string{name}, "app")
			p, ok, err := s.DirectoryOwner(name)
			if err != nil || !ok || p.Name != "app" {
				t.Errorf("literal owner of %q = %v, %v, %v", name, p, ok, err)
			}
		}
		parts461(t, s, []string{"app/s/page.tsx"}, "neighbor")
		write461(t, root, route, "dirty route\n")
		read461(t, s, route, "route base\n")
		read461(t, s, "app/s/page.tsx", "neighbor base\n")
		if _, err := s.ReadFile("app/[s]/page.tsx"); !errors.Is(err, ErrFileMissing) {
			t.Errorf("absent bracketed file must not read app/s/page.tsx: %v", err)
		}
		for name, want := range map[string]bool{route: false, "app/[slug]/AGENTS.md": true, "app/[slug]/.codex/settings.json": true, "app/[slug]/go.mod": false} {
			if got, err := s.IsTrusted(name); err != nil || got != want {
				t.Errorf("IsTrusted(%q) = %v, %v; want %v", name, got, err, want)
			}
		}
		for _, bad := range []string{"../app/[slug]/page.tsx", "/app/[slug]/page.tsx", "app//[slug]/page.tsx", "app/[slug]/../page.tsx", "app/[slug]/.git/config", "app/[slug]/bad\nname", `app\[slug]\page.tsx`, "app/[slug]/*.tsx", "app/[slug]/?.tsx", "app/[link]/page.tsx"} {
			if _, err := s.PartsFor([]string{bad}); err == nil {
				t.Errorf("unsafe path admitted: %q", bad)
			}
		}
		if err := os.Remove(filepath.Join(root, route)); err != nil {
			t.Fatal(err)
		}
		parts461(t, load461(t, root, commit461(t, root)), []string{route}, "app")
	})
	t.Run("declarations are literal but selectors stay restricted", func(t *testing.T) {
		p := part461("route", "app/[slug]", "typescript")
		c := contract461(p)
		c["agent_rules"] = []string{"app/[slug]/AGENTS.md"}
		c["current_state"] = []string{"app/[slug]/STATE.md"}
		root := repo461(t, c)
		write461(t, root, "app/[slug]/AGENTS.md", "rules\n")
		write461(t, root, "app/[slug]/STATE.md", "state\n")
		s := load461(t, root, commit461(t, root))
		parts461(t, s, []string{"app/[slug]/page.tsx"}, "route")
		for _, name := range []string{"app/[slug]/package.json", "app/[slug]/package-lock.json"} {
			if trusted, err := s.IsTrusted(name); err != nil || !trusted {
				t.Errorf("declared dependency %q lost trust: %v, %v", name, trusted, err)
			}
		}
		for _, field := range []string{"trusted_paths", "shared_paths"} {
			for _, selector := range []string{"app/[slug]/", "app/[slug]/page.tsx", "app/open[/", "app/close]/"} {
				bad := contract461(p)
				if field == "trusted_paths" {
					bad[field] = []string{selector}
				} else {
					bad[field] = []map[string]any{{"path": selector, "parts": []string{"route"}}}
				}
				write461(t, root, Path, json461(t, bad))
				if _, err := Load(t.Context(), root, commit461(t, root)); !errors.Is(err, ErrInvalid) {
					t.Errorf("%s admitted bracketed selector %q: %v", field, selector, err)
				}
			}
		}
	})
}
