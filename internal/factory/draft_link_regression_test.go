package factory

import (
	"strings"
	"testing"
)

func TestDraftLinkBaseAndLiteralPath(t *testing.T) {
	ref := DraftReference{Version: 1, Repository: "owner & team/repo?#%", Issue: 911,
		Commit: strings.Repeat("a", 40), SHA256: "sha256:" + strings.Repeat("b", 64)}
	for _, base := range []string{"", "/relative", "//example.test/path", "ftp://example.test", "https:///path",
		"https://user@example.test", "https://example.test?", "https://example.test#", "https://example.test/%zz"} {
		if _, err := ref.URL(base); err == nil {
			t.Errorf("accepted unusable link base %q", base)
		}
	}
	if err := ValidateDashboardURL(""); err != nil {
		t.Fatal("empty configuration must disable compact publication")
	}
	for _, base := range []string{"https://example.test/install%20one/(reader)", "https://example.test/install%20one/(reader)/"} {
		want := "https://example.test/install%20one/%28reader%29/draft/owner%20&%20team/repo%3F%23%25/911/" + ref.Commit + "/" + strings.TrimPrefix(ref.SHA256, "sha256:")
		got, err := ref.URL(base)
		if err != nil || got != want {
			t.Fatalf("literal link = %q, want %q: %v", got, want, err)
		}
	}
}

func TestInvalidFactoryDashboardBeforeWork(t *testing.T) {
	f := &Factory{DashboardURL: "https://user:password@example.test"}
	// Nil services make any work before validation fail loudly.
	if err := f.Prepare(t.Context()); err == nil {
		t.Fatal("Prepare accepted credentials in the dashboard base")
	}
	if err := f.Poll(t.Context()); err == nil {
		t.Fatal("Poll accepted credentials in the dashboard base")
	}
}
