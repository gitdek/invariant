package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestWatchDefaultsToFifteenMinuteLease544(t *testing.T) {
	code, help := agentCLI(t, "watch", "-h")
	if code != 0 || !regexp.MustCompile(`(?m)^  -lease duration\n[^\n]*\(default 15m0s\)`).MatchString(help) {
		t.Errorf("watch help must show a fifteen-minute lease default (exit %d):\n%s", code, help)
	}
	// Stop at the first GitHub request to distinguish valid flags from
	// rejected flags without credentials, a watcher, agents or network.
	dir := t.TempDir()
	const reached = "lease-544-flags-accepted"
	script := "#!/bin/sh\n" +
		"if [ \"$#\" -ne 7 ] || [ \"$1\" != api ] || [ \"$2\" != --include ] || [ \"$3\" != -X ] || [ \"$4\" != GET ] || [ \"$5\" != -H ] || [ \"$6\" != 'Accept: application/vnd.github+json' ] || [ \"$7\" != user ]; then\n" +
		"  echo 'unexpected GitHub fixture invocation' >&2\n  exit 97\nfi\n" +
		"echo '" + reached + "' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Give conditional GET a credential identity without asking any real
	// credential helper; the stand-in gh never sends it anywhere.
	t.Setenv("GH_TOKEN", "fixture-544")
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"default_allows_five_minute_polls", []string{"-every", "5m"}, true},
		{"default_refuses_more_than_five_minute_polls", []string{"-every", "5m1s"}, false},
		{"explicit_old_default", []string{"-lease", "5m"}, true},
		{"explicit_short_valid", []string{"-lease", "3m"}, true},
		{"explicit_long", []string{"-lease", "30m", "-every", "10m"}, true},
		{"disabled", []string{"-lease", "0", "-every", "1h"}, true},
		{"at_skew_limit", []string{"-lease", "2m", "-every", "30s"}, false},
		{"under_three_polls", []string{"-lease", "5m", "-every", "2m"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"watch", "-repo", "fixture/repository", "-work", dir}, tc.args...)
			code, out := agentCLI(t, args...)
			if code != 2 || strings.Contains(out, reached) != tc.want || strings.Contains(out, "unexpected GitHub fixture invocation") {
				t.Errorf("watch %v: exit %d, reached startup %v, want %v:\n%s", tc.args, code, strings.Contains(out, reached), tc.want, out)
			}
		})
	}
}
