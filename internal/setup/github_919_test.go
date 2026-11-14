package setup

import (
	"encoding/json"
	"testing"
)

// A remote's address names its repository on GitHub over https or ssh,
// with or without .git. Any other host names none, and neither does an
// address that carries a user or a token, so nothing that reads one can
// repeat it.
func TestARemotesAddressNamesItsRepositoryOnGitHub919(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/acme/widgets.git":                  "acme/widgets",
		"https://github.com/acme/widgets":                      "acme/widgets",
		"https://github.com/acme/widgets/":                     "acme/widgets",
		"git@github.com:acme/widgets.git":                      "acme/widgets",
		"ssh://git@github.com/Acme/Widgets.v2":                 "Acme/Widgets.v2",
		"https://someone:a-token@github.com/acme/widgets.git":  "",
		"https://x-access-token@github.com/acme/widgets":       "",
		"https://gitlab.com/acme/widgets.git":                  "",
		"https://github.com.example.org/acme/widgets":          "",
		"https://github.com/acme":                              "",
		"https://github.com/acme/widgets/tree/main":            "",
		"/home/someone/widgets.git":                            "",
		"":                                                     "",
		"https://github.com/acme/widgets.git\nhttps://example": "",
	} {
		if got := GitHubRepo(remote); got != want {
			t.Errorf("the repository %q names is %q, want %q", remote, got, want)
		}
	}
}

// The protection init prints and new applies is one JSON object GitHub's
// branch protection takes: invariant/gate required as GitHub Actions'
// check, for administrators too, with no reviews and no push restrictions.
func TestTheProtectionRequiresTheGateForEveryone919(t *testing.T) {
	var p struct {
		Checks struct {
			Strict bool `json:"strict"`
			Checks []struct {
				Context string `json:"context"`
				AppID   int    `json:"app_id"`
			} `json:"checks"`
		} `json:"required_status_checks"`
		Admins  bool `json:"enforce_admins"`
		Reviews struct {
			Count int `json:"required_approving_review_count"`
		} `json:"required_pull_request_reviews"`
		Restrictions *struct{} `json:"restrictions"`
	}
	if err := json.Unmarshal([]byte(Protection), &p); err != nil {
		t.Fatalf("the protection isn't JSON: %v", err)
	}
	if len(p.Checks.Checks) != 1 || p.Checks.Checks[0].Context != "invariant/gate" || p.Checks.Checks[0].AppID != 15368 || p.Checks.Strict {
		t.Errorf("the protection requires %+v, want invariant/gate from GitHub Actions alone", p.Checks)
	}
	if !p.Admins || p.Reviews.Count != 0 || p.Restrictions != nil {
		t.Errorf("the protection has enforce_admins=%t, %d reviews and restrictions %v", p.Admins, p.Reviews.Count, p.Restrictions)
	}
}
