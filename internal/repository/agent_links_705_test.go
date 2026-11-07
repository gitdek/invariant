package repository

import (
	"os"
	"path/filepath"
	"testing"
)

// A symbolic link at a trusted path makes what it names trusted too, read
// from the base's tree: an edit there changes what every agent reading the
// link sees. The same edit with no link stays ordinary. A link that leaves the
// repository or loops adds nothing, since scope refuses any change through it
// (#705).
func TestTrustedLinksTrustTheirTargets(t *testing.T) {
	for _, c := range []struct {
		name           string
		files, links   map[string]string
		trusted, plain []string
	}{
		{name: "agent file", files: map[string]string{"docs/notes.md": "Shared.\n"},
			links:   map[string]string{"CLAUDE.md": "docs/notes.md"},
			trusted: []string{"docs/notes.md", "Docs/Notes.md"}, plain: []string{"docs/other.md", "src/old.go"}},
		{name: "no link", files: map[string]string{"docs/notes.md": "Shared.\n", "CLAUDE.md": "Own.\n"},
			plain: []string{"docs/notes.md"}},
		{name: "chain", files: map[string]string{"notes/shared.md": "Shared.\n"},
			links:   map[string]string{"app/AGENTS.md": "../docs/a.md", "docs/a.md": "../notes/shared.md"},
			trusted: []string{"notes/shared.md"}, plain: []string{"docs/b.md"}},
		{name: "through a linked directory", files: map[string]string{"real/shared.md": "Shared.\n"},
			links:   map[string]string{"CLAUDE.md": "alias/shared.md", "alias": "real"},
			trusted: []string{"real/shared.md"}, plain: []string{"real/other.md"}},
		{name: "linked directory", files: map[string]string{"config/claude/settings.json": "{}\n"},
			links:   map[string]string{".claude": "config/claude"},
			trusted: []string{"config/claude/settings.json", "config/claude/commands/new.md"}, plain: []string{"config/other.json"}},
		{name: "link inside a linked directory", files: map[string]string{"config/claude/hooks.json": "{}\n", "shared/settings.json": "{}\n"},
			links:   map[string]string{".claude": "config/claude", "config/claude/settings.json": "../../shared/settings.json"},
			trusted: []string{"shared/settings.json"}, plain: []string{"shared/other.json"}},
		{name: "inside a trusted directory", files: map[string]string{"scripts/run.sh": "exit 0\n"},
			links:   map[string]string{"tools/run.sh": "../scripts/run.sh"},
			trusted: []string{"scripts/run.sh"}, plain: []string{"scripts/other.sh"}},
		{name: "dangling", links: map[string]string{"CLAUDE.md": "docs/missing.md"},
			trusted: []string{"docs/missing.md"}, plain: []string{"docs/other.md"}},
		{name: "outside the repository", links: map[string]string{"CLAUDE.md": "../outside.md", ".mcp.json": "/etc/mcp.json"},
			plain: []string{"outside.md", "etc/mcp.json", "docs/notes.md"}},
		{name: "loop", links: map[string]string{"CLAUDE.md": "docs/loop.md", "docs/loop.md": "../CLAUDE.md"},
			plain: []string{"docs/other.md"}},
		{name: "ordinary link", files: map[string]string{"docs/guide.md": "Guide.\n"},
			links: map[string]string{"docs/alias.md": "guide.md"},
			plain: []string{"docs/guide.md"}},
		{name: "the repository's root", links: map[string]string{".claude": "."},
			trusted: []string{"src/old.go", "SPEC.md"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := repo461(t, contract461(part461("app", ".", "go")))
			for name, body := range c.files {
				write461(t, root, name, body)
			}
			for name, target := range c.links {
				p := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
			}
			s := load461(t, root, commit461(t, root))
			for _, name := range c.trusted {
				trusted461(t, s, name, true)
			}
			for _, name := range c.plain {
				trusted461(t, s, name, false)
			}
			// A link itself stays outside what scope accepts.
			for name := range c.links {
				if _, err := s.IsTrusted(name); err == nil {
					t.Errorf("trust of link %q was answered", name)
				}
			}
		})
	}
}

// The targets come from the base's tree. A candidate's own new link names
// nothing trusted, though its agent file is held by name.
func TestLinkTargetsComeFromTheBase(t *testing.T) {
	root := repo461(t, contract461(part461("app", ".", "go")))
	write461(t, root, "docs/notes.md", "Shared.\n")
	base := commit461(t, root)
	if err := os.Symlink("docs/notes.md", filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	commit461(t, root)
	s := load461(t, root, base)
	trusted461(t, s, "CLAUDE.md", true)
	trusted461(t, s, "docs/notes.md", false)
}
