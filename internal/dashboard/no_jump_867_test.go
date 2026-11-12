package dashboard

import (
	"strings"
	"testing"
)

// The page keeps a row for its Show bar until its first snapshot says what
// the bar holds, only where there will be a bar (#867): a dashboard of one
// repository has none, and its page, kept a row, moved up when the snapshot
// arrived. Both pages start as opening.
func TestOnlyAPageWithAShowBarKeepsItsRow867(t *testing.T) {
	for repos, kept := range map[int]bool{0: false, 1: false, 2: true, 3: true} {
		page := pageFor(repos)
		if got := strings.Contains(page, `class="scope pending"`); got != kept {
			t.Errorf("a page of %d repositories keeps a row for the Show bar: %v, want %v", repos, got, kept)
		}
		if !strings.Contains(page, `<main class="opening">`) || strings.Contains(page, "{{version}}") {
			t.Errorf("a page of %d repositories doesn't start as opening, or holds a placeholder", repos)
		}
	}
	if page := string(actPage("owner@example.com", "", 1)); strings.Contains(page, `class="scope pending"`) || !strings.Contains(page, `data-act="owner@example.com"`) {
		t.Error("the owner's page of one repository keeps a row for the Show bar, or isn't marked for acting")
	}
}
