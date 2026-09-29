// Package github is the factory's view of GitHub: issues, comments, pull
// requests and check runs. It talks to GitHub through the gh CLI, as whoever
// is logged in to gh, so Invariant never handles a token itself.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Client is a GitHub repository, reached through gh.
type Client struct {
	Repo string // owner/name
	GH   string // the gh CLI; "gh" when empty
	// Token, when set, is who the client acts as: the factory's App, handed
	// to gh as GH_TOKEN. Without it, gh acts as whoever is logged in.
	Token func(ctx context.Context) (string, error)
}

type User struct {
	Login string `json:"login"`
	Name  string `json:"name,omitempty"`
	ID    int64  `json:"id,omitempty"`
	Type  string `json:"type,omitempty"` // User or Bot
}

type Label struct {
	Name string `json:"name"`
}

type Issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	User        User      `json:"user"`
	Labels      []Label   `json:"labels"`
	State       string    `json:"state"`
	URL         string    `json:"html_url"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
	ClosedAt    string    `json:"closed_at,omitempty"`
	PullRequest *struct{} `json:"pull_request,omitempty"` // set when the issue is a pull request
}

// HasLabel says whether the issue carries the label.
func (i Issue) HasLabel(name string) bool {
	for _, l := range i.Labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

type Comment struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	User      User   `json:"user"`
	URL       string `json:"html_url"`
	IssueURL  string `json:"issue_url"`
	CreatedAt string `json:"created_at"`
}

// Issue is the number of the issue the comment is on.
func (c Comment) Issue() int {
	n, _ := strconv.Atoi(c.IssueURL[strings.LastIndex(c.IssueURL, "/")+1:])
	return n
}

// Event is one entry in an issue's timeline of events, such as a label
// being added.
type Event struct {
	Event string `json:"event"`
	Actor User   `json:"actor"`
	Label *Label `json:"label,omitempty"`
}

type Ref struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type PullRequest struct {
	Number         int    `json:"number"`
	URL            string `json:"html_url"`
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	Draft          bool   `json:"draft"`
	Head           Ref    `json:"head"`
	Base           Ref    `json:"base"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	MergedAt       string `json:"merged_at,omitempty"`
	MergedBy       *User  `json:"merged_by,omitempty"`
}

// NewPullRequest is a pull request to open.
type NewPullRequest struct {
	Title string `json:"title"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Body  string `json:"body"`
	Draft bool   `json:"draft"`
}

type CheckRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued, in_progress or completed
	Conclusion string `json:"conclusion"` // success, failure, ... once completed
	URL        string `json:"html_url"`
	HeadSHA    string `json:"head_sha"`
}

// ErrNotFound is what a request for something that doesn't exist returns.
var ErrNotFound = errors.New("not found")

// ErrNoPermission is what a request returns when the App isn't allowed to
// make it, such as reading GitHub Actions without that permission. Waiting
// won't change that, unlike a rate limit.
var ErrNoPermission = errors.New("the App isn't allowed to do this")

// Viewer is the user gh is logged in as.
func (c Client) Viewer(ctx context.Context) (User, error) {
	var u User
	return u, c.call(ctx, "GET", "user", nil, &u)
}

// OpenIssues lists the repository's open issues, leaving out pull requests.
func (c Client) OpenIssues(ctx context.Context) ([]Issue, error) {
	var all []Issue
	if err := c.pages(ctx, c.path("issues?state=open&per_page=100"), &all); err != nil {
		return nil, err
	}
	var issues []Issue
	for _, i := range all {
		if i.PullRequest == nil {
			issues = append(issues, i)
		}
	}
	return issues, nil
}

func (c Client) Issue(ctx context.Context, n int) (Issue, error) {
	var i Issue
	return i, c.call(ctx, "GET", c.path(fmt.Sprintf("issues/%d", n)), nil, &i)
}

// Comments lists an issue's comments, oldest first.
func (c Client) Comments(ctx context.Context, issue int) ([]Comment, error) {
	var out []Comment
	return out, c.pages(ctx, c.path(fmt.Sprintf("issues/%d/comments?per_page=100", issue)), &out)
}

func (c Client) Comment(ctx context.Context, id int64) (Comment, error) {
	var out Comment
	return out, c.call(ctx, "GET", c.path(fmt.Sprintf("issues/comments/%d", id)), nil, &out)
}

// Events lists an issue's events, oldest first.
func (c Client) Events(ctx context.Context, issue int) ([]Event, error) {
	var out []Event
	return out, c.pages(ctx, c.path(fmt.Sprintf("issues/%d/events?per_page=100", issue)), &out)
}

// Permission is a user's permission on the repository: admin, maintain,
// write, triage, read or none.
func (c Client) Permission(ctx context.Context, login string) (string, error) {
	var out struct {
		Permission string `json:"permission"`
	}
	err := c.call(ctx, "GET", c.path("collaborators/"+url.PathEscape(login)+"/permission"), nil, &out)
	if errors.Is(err, ErrNotFound) {
		return "none", nil
	}
	return out.Permission, err
}

func (c Client) PostComment(ctx context.Context, issue int, body string) (Comment, error) {
	var out Comment
	return out, c.call(ctx, "POST", c.path(fmt.Sprintf("issues/%d/comments", issue)), map[string]string{"body": body}, &out)
}

// NewIssue is an issue to open.
type NewIssue struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
}

// CreateIssue opens an issue.
func (c Client) CreateIssue(ctx context.Context, is NewIssue) (Issue, error) {
	var out Issue
	return out, c.call(ctx, "POST", c.path("issues"), is, &out)
}

// EnsureLabel creates a label unless the repository already has it.
func (c Client) EnsureLabel(ctx context.Context, name, color, description string) error {
	err := c.call(ctx, "GET", c.path("labels/"+url.PathEscape(name)), nil, nil)
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	return c.call(ctx, "POST", c.path("labels"), map[string]string{"name": name, "color": color, "description": description}, nil)
}

func (c Client) AddLabels(ctx context.Context, issue int, labels ...string) error {
	return c.call(ctx, "POST", c.path(fmt.Sprintf("issues/%d/labels", issue)), map[string][]string{"labels": labels}, nil)
}

// RemoveLabel takes a label off an issue. Removing one it doesn't carry is
// not an error.
func (c Client) RemoveLabel(ctx context.Context, issue int, label string) error {
	err := c.call(ctx, "DELETE", c.path(fmt.Sprintf("issues/%d/labels/%s", issue, url.PathEscape(label))), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (c Client) CreatePullRequest(ctx context.Context, pr NewPullRequest) (PullRequest, error) {
	var out PullRequest
	return out, c.call(ctx, "POST", c.path("pulls"), pr, &out)
}

func (c Client) PullRequest(ctx context.Context, n int) (PullRequest, error) {
	var out PullRequest
	return out, c.call(ctx, "GET", c.path(fmt.Sprintf("pulls/%d", n)), nil, &out)
}

// OpenPullRequest finds the open pull request from branch, in this
// repository, if there is one.
func (c Client) OpenPullRequest(ctx context.Context, branch string) (PullRequest, bool, error) {
	owner, _, _ := strings.Cut(c.Repo, "/")
	var out []PullRequest
	if err := c.call(ctx, "GET", c.path("pulls?state=open&head="+url.QueryEscape(owner+":"+branch)), nil, &out); err != nil {
		return PullRequest{}, false, err
	}
	if len(out) == 0 {
		return PullRequest{}, false, nil
	}
	return out[0], true, nil
}

// CheckRuns lists the check runs with the given name on a commit.
func (c Client) CheckRuns(ctx context.Context, sha, name string) ([]CheckRun, error) {
	var out struct {
		CheckRuns []CheckRun `json:"check_runs"`
	}
	err := c.call(ctx, "GET", c.path(fmt.Sprintf("commits/%s/check-runs?check_name=%s&per_page=100", sha, url.QueryEscape(name))), nil, &out)
	return out.CheckRuns, err
}

// Merge merges a pull request, but only if its head is still sha, and
// returns the merge commit.
func (c Client) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	err := c.call(ctx, "PUT", c.path(fmt.Sprintf("pulls/%d/merge", n)), map[string]string{"sha": sha, "merge_method": method}, &out)
	return out.SHA, err
}

// MarkReady takes a draft pull request out of draft, so it can merge. The
// REST API can't, so it reads the pull request's node ID and runs GraphQL's
// markPullRequestReadyForReview mutation on it (#145).
func (c Client) MarkReady(ctx context.Context, n int) error {
	var pr struct {
		NodeID string `json:"node_id"`
	}
	if err := c.call(ctx, "GET", c.path(fmt.Sprintf("pulls/%d", n)), nil, &pr); err != nil {
		return err
	}
	stdout, err := c.run(ctx, nil, "api", "graphql",
		"-f", "query=mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }",
		"-f", "id="+pr.NodeID)
	if err != nil {
		return fmt.Errorf("POST graphql: %w", err)
	}
	var out struct {
		Data struct {
			Marked *struct {
				PullRequest struct {
					IsDraft bool `json:"isDraft"`
				} `json:"pullRequest"`
			} `json:"markPullRequestReadyForReview"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout, &out); err != nil {
		return err
	}
	if out.Data.Marked == nil || out.Data.Marked.PullRequest.IsDraft {
		return fmt.Errorf("GitHub didn't mark #%d ready for review", n)
	}
	return nil
}

// DeleteBranch deletes a branch. Deleting one that's already gone is not an
// error.
func (c Client) DeleteBranch(ctx context.Context, branch string) error {
	err := c.call(ctx, "DELETE", c.path("git/refs/heads/"+branch), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Workflows reads the text of each workflow file under .github/workflows at
// ref, by path, through the contents API. A ref with no such directory has
// none.
func (c Client) Workflows(ctx context.Context, ref string) (map[string]string, error) {
	var entries []struct {
		Path string `json:"path"`
		Type string `json:"type"` // file, dir, symlink or submodule
	}
	err := c.call(ctx, "GET", c.path("contents/.github/workflows?ref="+url.QueryEscape(ref)), nil, &entries)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	workflows := map[string]string{}
	for _, e := range entries {
		// GitHub Actions runs the .yml and .yaml files there, and nothing else.
		if e.Type != "file" || !(strings.HasSuffix(e.Path, ".yml") || strings.HasSuffix(e.Path, ".yaml")) {
			continue
		}
		text, err := c.File(ctx, e.Path, ref)
		if err != nil {
			return nil, fmt.Errorf("%s at %s: %w", e.Path, ref, err)
		}
		workflows[e.Path] = string(text)
	}
	return workflows, nil
}

// MergeCommits says whether the repository allows merge commits, from
// GraphQL's mergeCommitAllowed, which anyone who can read the repository
// sees.
func (c Client) MergeCommits(ctx context.Context) (bool, error) {
	owner, name, _ := strings.Cut(c.Repo, "/")
	stdout, err := c.run(ctx, nil, "api", "graphql",
		"-f", "query=query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) { mergeCommitAllowed } }",
		"-f", "owner="+owner, "-f", "name="+name)
	if err != nil {
		return false, fmt.Errorf("POST graphql: %w", err)
	}
	var out struct {
		Data struct {
			Repository *struct {
				MergeCommitAllowed bool `json:"mergeCommitAllowed"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout, &out); err != nil {
		return false, err
	}
	if out.Data.Repository == nil {
		return false, fmt.Errorf("GitHub didn't find %s", c.Repo)
	}
	return out.Data.Repository.MergeCommitAllowed, nil
}

func (c Client) path(rest string) string { return "repos/" + c.Repo + "/" + rest }

func (c Client) gh() string {
	if c.GH == "" {
		return "gh"
	}
	return c.GH
}

// call makes one request. A body is sent as JSON, and a response is decoded
// into out unless out is nil.
func (c Client) call(ctx context.Context, method, path string, body, out any) error {
	args := []string{"api", "-X", method, "-H", "Accept: application/vnd.github+json", path}
	var stdin io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		args = append(args, "--input", "-")
		stdin = bytes.NewReader(b)
	}
	stdout, err := c.run(ctx, stdin, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	if out == nil || len(bytes.TrimSpace(stdout)) == 0 {
		return nil
	}
	return json.Unmarshal(stdout, out)
}

// pages fetches every page of a list. gh prints each page as its own JSON
// array.
func (c Client) pages(ctx context.Context, path string, out any) error {
	stdout, err := c.run(ctx, nil, "api", "--paginate", "-H", "Accept: application/vnd.github+json", path)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	var all []json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for dec.More() {
		var page []json.RawMessage
		if err := dec.Decode(&page); err != nil {
			return err
		}
		all = append(all, page...)
	}
	b, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (c Client) run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.gh(), args...)
	cmd.Stdin = stdin
	if c.Token != nil {
		token, err := c.Token(ctx)
		if err != nil {
			return nil, err
		}
		cmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "(HTTP 404)") {
			return nil, ErrNotFound
		}
		if strings.Contains(msg, "Resource not accessible by integration") {
			return nil, fmt.Errorf("%w: %s", ErrNoPermission, msg)
		}
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("%w: %s", err, msg)
	}
	return stdout.Bytes(), nil
}
