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
	"regexp"
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
	// AuthorAssociation is who its author is to the repository, as GitHub
	// says: OWNER, MEMBER, COLLABORATOR, or someone else.
	AuthorAssociation string `json:"author_association,omitempty"`
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
	// AuthorAssociation is who its author is to the repository, as the
	// issue's is.
	AuthorAssociation string `json:"author_association,omitempty"`
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
	CreatedAt      string `json:"created_at,omitempty"`
	// Mergeable says whether GitHub can merge it, as when it doesn't
	// conflict with its base. It's nil until GitHub has worked that out,
	// which it starts once asked for this one pull request, and it's always
	// nil in a list of them.
	Mergeable *bool `json:"mergeable"`
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

// Transient says err looks like GitHub failing for a moment, rather than
// refusing the request, so the same request may well succeed if made
// again (#369's review, #376). The HTTP status gh's error carries decides
// first: a 5xx, a 429, a 408, which asks for the request again, and a 403
// that says it's a rate limit are GitHub failing for a moment, and any
// other 4xx, such as a 405, a 409 or a 422, is a refusal, whatever else its
// message says. An error with no status, as GraphQL's and the client's own
// are, is decided by its words: GitHub's own for its REST or GraphQL timing
// out, a connection dropped, refused, reset or never made, a request that
// timed out on the client, a name that didn't resolve, gh killed by a
// signal, and a rate limit, secondary or primary. ErrNotFound and
// ErrNoPermission are refusals, whatever else they say.
func Transient(err error) bool {
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrNoPermission) {
		return false
	}
	msg := err.Error()
	limited := primaryLimit.MatchString(msg) || secondaryLimit.MatchString(msg)
	for _, m := range httpStatus.FindAllStringSubmatch(msg, -1) {
		switch code, _ := strconv.Atoi(m[1]); {
		case code == 403 && limited, code == 408, code == 429:
		case code >= 400 && code < 500:
			return false
		}
	}
	return limited || transient.MatchString(msg)
}

// RateLimited says err is GitHub's primary rate limit: the App, or whoever
// gh is logged in as, has made every request GitHub allows it for the hour,
// and GitHub serves no more until the limit resets, which it does within
// the hour. It's transient, since waiting fixes it, and it's GitHub saying
// when to come back rather than failing. A secondary rate limit, which asks
// for a wait of minutes, isn't one.
func RateLimited(err error) bool {
	return Transient(err) && primaryLimit.MatchString(err.Error())
}

// httpStatus is the HTTP status gh's error carries, as gh writes it: "(HTTP
// 422)" after GitHub's message, or "HTTP 502" where GitHub gave none.
var httpStatus = regexp.MustCompile(`(?i)\bHTTP (\d{3})\b`)

// transient is what gh says of a request GitHub failed for a moment: a 5xx
// or 408 answer, a 429's reason, or GitHub's own words for its REST or
// GraphQL timing out; a connection dropped, refused or never made, such as
// one closed before any answer, which leaves a bare EOF, one an HTTP/2
// stream's INTERNAL_ERROR reset, or one a TLS alert, sent or received,
// ended; a request that timed out on the client; a name that didn't
// resolve, of which gh says only that it couldn't connect; and gh killed by
// a signal before it heard GitHub's answer.
var transient = regexp.MustCompile(`(?im)` + strings.Join([]string{
	`\bHTTP 5\d\d\b`, `bad gateway`, `service unavailable`, `gateway time-?out`, `internal server error`,
	`couldn't respond to your request in time`, `something went wrong while executing your query`,
	`connection reset`, `connection refused`, `broken pipe`, `unexpected eof`, `: EOF$`, `server closed idle connection`,
	`http2: client connection lost`, `server sent GOAWAY`, `stream error: stream ID \d+; INTERNAL_ERROR`,
	`(remote|local) error: tls: `, `network is unreachable`, `no route to host`,
	`i/o timeout`, `tls handshake timeout`, `timeout awaiting response headers`, `Client\.Timeout exceeded`,
	`context deadline exceeded`, `operation timed out`, `connection timed out`, `\bHTTP 408\b`,
	`error connecting to`, `check your internet connection`, `no such host`, `temporary failure in name resolution`, `server misbehaving`,
	`(^|: )signal: [a-z]`, `\bHTTP 429\b`, `too many requests`,
}, "|"))

// secondaryLimit is how GitHub says a secondary rate limit, which asks for
// a wait of minutes, held a request back.
var secondaryLimit = regexp.MustCompile(`(?i)secondary rate limit|abuse detection`)

// primaryLimit is how GitHub says a primary rate limit ran out, over REST
// and GraphQL alike.
var primaryLimit = regexp.MustCompile(`(?i)\bAPI rate limit (already )?exceeded\b`)

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

// PullRequestFrom finds the newest pull request from branch, in this
// repository, open, closed or merged, if there is one.
func (c Client) PullRequestFrom(ctx context.Context, branch string) (PullRequest, bool, error) {
	owner, _, _ := strings.Cut(c.Repo, "/")
	var out []PullRequest
	if err := c.call(ctx, "GET", c.path("pulls?state=all&sort=created&direction=desc&head="+url.QueryEscape(owner+":"+branch)), nil, &out); err != nil {
		return PullRequest{}, false, err
	}
	if len(out) == 0 {
		return PullRequest{}, false, nil
	}
	newest := out[0]
	for _, pr := range out[1:] {
		if pr.Number > newest.Number {
			newest = pr
		}
	}
	// A list of pull requests says when one merged, not whether it did.
	newest.Merged = newest.MergedAt != ""
	return newest, true, nil
}

// ClosePullRequest closes pull request n without merging it, and returns it
// as it is then. Closing one that's already closed changes nothing, and one
// that's merged stays merged: the caller reads Merged to tell, as when
// someone merges it just before the close.
func (c Client) ClosePullRequest(ctx context.Context, n int) (PullRequest, error) {
	pr, err := c.PullRequest(ctx, n)
	if err != nil || pr.Merged || pr.State == "closed" {
		return pr, err
	}
	var out PullRequest
	err = c.call(ctx, "PATCH", c.path(fmt.Sprintf("pulls/%d", n)), map[string]string{"state": "closed"}, &out)
	if err == nil {
		return out, nil
	}
	// GitHub refuses to close one that was merged or closed meanwhile. What
	// it is now says which.
	if now, readErr := c.PullRequest(ctx, n); readErr == nil && (now.Merged || now.State == "closed") {
		return now, nil
	}
	return pr, err
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
