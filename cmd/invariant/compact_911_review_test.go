package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/factory"
)

// The watcher's dashboard address names a host: a DNS name or an IP address.
// One that doesn't, such as https://dash.example) with a parenthesis left
// on its end, passed the watcher's flags and broke the link of every compact
// comment it went on to post (#911's review). It is refused with the other
// flags, before the watcher asks GitHub who it is or opens anything. An
// address on this machine names a host, and is still taken.
func TestCompact911ReviewWatchRefusesAnAddressThatNamesNoHost(t *testing.T) {
	bin := t.TempDir()
	const reached = "compact911-review-first-external-call"
	script := "#!/bin/sh\nprintf '%s\\n' '" + reached + "' >&2\nexit 97\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "fixture-911")
	for _, base := range []string{"https://dash.example)", "https://dash(board).example", `https://dash"board.example`, "https://dash.example:", "https://dash.example:65536", "https://./"} {
		code, out := agentCLI(t, "watch", "-repo", "owner/saved", "-dashboard-url", base, "-work", t.TempDir())
		if code != 2 || strings.Contains(out, reached) || !strings.Contains(out, "dashboard-url") || !strings.Contains(out, "host") {
			t.Errorf("the dashboard address %q wasn't refused for its host before any work: exit=%d, %s", base, code, out)
		}
	}
	for _, base := range []string{"http://127.0.0.1:8484", "http://localhost:8484/prefix", "http://[::1]:8484", "https://dashboard.example"} {
		starts := 0
		code := watchWithFactory(t.Context(), []string{"-repo", "owner/saved", "-once", "-lease", "0", "-work", t.TempDir(), "-dashboard-url", base}, func(_ context.Context, f *factory.Factory) int {
			starts++
			if f.DashboardURL != base {
				t.Errorf("the watcher starts with the dashboard address %q, not %q", f.DashboardURL, base)
			}
			return 0
		})
		if code != 0 || starts != 1 {
			t.Errorf("the dashboard address %q should start the watcher once: exit=%d, starts=%d", base, code, starts)
		}
	}
}
