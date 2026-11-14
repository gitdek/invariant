package setup

import "regexp"

// Protection is the branch protection that requires invariant/gate on a
// branch: the body of a PUT to repos/OWNER/NAME/branches/BRANCH/protection.
// app_id 15368 is GitHub Actions, so only the workflow's own check counts.
// init prints the call for a person to make, and new makes it.
const Protection = `{"required_status_checks":{"strict":false,"checks":[{"context":"invariant/gate","app_id":15368}]},"enforce_admins":true,"required_pull_request_reviews":{"required_approving_review_count":0},"restrictions":null}`

// githubRemote is a GitHub repository's remote, over https or ssh.
var githubRemote = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)([^/]+/[^/]+?)(?:\.git)?/?$`)

// GitHubRepo is the repository on GitHub that a git remote's address names,
// as owner/name, or "" when it names none there. An address that carries a
// user or a token names none, so nothing that reads it repeats one.
func GitHubRepo(remote string) string {
	m := githubRemote.FindStringSubmatch(remote)
	if m == nil {
		return ""
	}
	return m[1]
}
