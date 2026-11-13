package setup_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInitDiscoversBracketedRoutes877(t *testing.T) {
	root := t.TempDir()
	node727(t, root, "vitest", 3)
	for name, body := range map[string]string{
		"app/[slug]/page.tsx":      "export default function Page() { return 'route'; }\n",
		"app/s/page.tsx":           "export default function Page() { return 'neighbor'; }\n",
		"app/[slug]/AGENTS.md":     "The current state is `STATE.md`.\n",
		"app/[slug]/STATE.md":      "Route behavior.\n",
		"app/[[...slug]]/page.tsx": "export default function CatchAll() {}\n",
		"app/(marketing)/page.tsx": "export default function Marketing() {}\n",
	} {
		write727(t, root, name, body)
	}
	before := tree727(t, root)
	data, config, issues := draft727(t, root)
	for _, issue := range issues {
		if strings.Contains(issue, "rename unsafe") || strings.Contains(issue, "[slug]") {
			t.Errorf("bracketed checkout evidence was skipped: %s", issue)
		}
	}
	if len(issues) != 0 {
		t.Fatalf("complete Node evidence left unresolved requirements: %v", issues)
	}
	has727(t, config.AgentRules, "app/[slug]/AGENTS.md")
	has727(t, config.CurrentState, "app/[slug]/STATE.md")
	if !reflect.DeepEqual(before, tree727(t, root)) {
		t.Fatal("drafting changed the checkout")
	}
	snapshot, err := load727(t, root, data)
	if err != nil {
		t.Fatal(err)
	}
	for name, directory := range map[string]string{"app/[slug]/page.tsx": ".", "app/s/page.tsx": "."} {
		owner, ok, err := snapshot.DirectoryOwner(name)
		if err != nil || !ok || owner.Directory != directory {
			t.Errorf("drafted owner of %q = %v, %v, %v", name, owner, ok, err)
		}
	}
	// Discovery also retains a bracketed part directory. Its unsupported
	// check declarations remain unresolved just as for other Go drafts.
	other := t.TempDir()
	write727(t, other, filepath.ToSlash(filepath.Join("tools/[tool]", "go.mod")), go727)
	_, nested, unresolved := draft727(t, other)
	part727(t, nested, "tools/[tool]", "go")
	for _, issue := range unresolved {
		if strings.Contains(issue, "rename unsafe") {
			t.Errorf("bracketed part was treated as an unsafe path: %s", issue)
		}
	}
}
