package setup

import (
	"strings"
	"testing"
)

// A project directory whose name starts with "-" would reach verify and
// ratification as a flag, and a help flag would end the step successfully
// without checking anything. Both loops refuse it instead.
func TestGeneratedGateRefusesFlagLikeProjectPaths(t *testing.T) {
	w := workflow736(t)
	for _, command := range []string{"verify", "ratification"} {
		t.Run(command, func(t *testing.T) {
			s := findStep736(t, w, command)
			if !strings.Contains(s.text, "shell: bash") {
				t.Errorf("the %s step relies on Bash without asking for it", command)
			}
			calls, err, out := script736(t, s, []string{"-h/.invariant/invariant.json", "models/one/.invariant/invariant.json"}, "pull_request", "feature", "")
			if err == nil || !strings.Contains(out, "starts with -") {
				t.Fatalf("a flag-like project path passed the %s step: %v\n%s", command, err, out)
			}
			for _, call := range calls {
				for _, arg := range call {
					if strings.HasPrefix(arg, "-h") {
						t.Fatalf("a flag-like project reached invariant: %v", call)
					}
				}
			}
		})
	}
}

// A factory-looking branch that names no issue gets ordinary pull request
// checks, rather than an -issue 0 that check-repository refuses as usage.
func TestGeneratedGateOmitsAnUnnamedIssue(t *testing.T) {
	s := findStep736(t, workflow736(t), "check-repository")
	calls, err, out := script736(t, s, nil, "pull_request", "invariant/feature-x", "")
	if err != nil {
		t.Fatalf("check step: %v\n%s", err, out)
	}
	checked := false
	for _, call := range calls {
		if call[0] == "check-repository" {
			checked = true
			if option736(call, "-issue") != "" || option736(call, "-base") == "" {
				t.Fatalf("an unnamed issue didn't get ordinary pull request checks: %v", call)
			}
		}
	}
	if !checked {
		t.Fatalf("no repository check ran:\n%s", out)
	}
}
