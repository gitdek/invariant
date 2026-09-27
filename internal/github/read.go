package github

import (
	"context"
	"fmt"
	"net/url"
)

// These reads serve the dashboard (D-0049). None of them changes anything
// on GitHub.

// Issues lists the issues that carry a label, open and closed, leaving out
// pull requests. An empty label lists them all.
func (c Client) Issues(ctx context.Context, label string) ([]Issue, error) {
	query := "issues?state=all&per_page=100"
	if label != "" {
		query += "&labels=" + url.QueryEscape(label)
	}
	var all []Issue
	if err := c.pages(ctx, c.path(query), &all); err != nil {
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

// Commit is one commit on a branch. Author is the GitHub account behind it,
// when GitHub knows one.
type Commit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Date string `json:"date"`
		} `json:"author"`
		Committer struct {
			Date string `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
	Author *User `json:"author"`
}

// Commits lists a branch's newest commits, newest first.
func (c Client) Commits(ctx context.Context, branch string, n int) ([]Commit, error) {
	var out []Commit
	return out, c.call(ctx, "GET", c.path(fmt.Sprintf("commits?sha=%s&per_page=%d", url.QueryEscape(branch), n)), nil, &out)
}

// Run is one run of a GitHub Actions workflow.
type Run struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	HeadSHA      string `json:"head_sha"`
	HeadBranch   string `json:"head_branch"`
	Event        string `json:"event"`
	Status       string `json:"status"`     // queued, in_progress or completed
	Conclusion   string `json:"conclusion"` // success, failure, ... once completed
	DisplayTitle string `json:"display_title"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	StartedAt    string `json:"run_started_at"`
}

// Runs lists a branch's newest workflow runs, newest first.
func (c Client) Runs(ctx context.Context, branch string, n int) ([]Run, error) {
	var out struct {
		Runs []Run `json:"workflow_runs"`
	}
	err := c.call(ctx, "GET", c.path(fmt.Sprintf("actions/runs?branch=%s&per_page=%d", url.QueryEscape(branch), n)), nil, &out)
	return out.Runs, err
}

// Artifact is a file a workflow run uploaded.
type Artifact struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Size    int64  `json:"size_in_bytes"`
	Expired bool   `json:"expired"`
}

// Artifacts lists what a workflow run uploaded.
func (c Client) Artifacts(ctx context.Context, run int64) ([]Artifact, error) {
	var out struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	err := c.call(ctx, "GET", c.path(fmt.Sprintf("actions/runs/%d/artifacts?per_page=100", run)), nil, &out)
	return out.Artifacts, err
}

// Download fetches an artifact, as the zip GitHub stores it in.
func (c Client) Download(ctx context.Context, artifact int64) ([]byte, error) {
	return c.run(ctx, nil, "api", c.path(fmt.Sprintf("actions/artifacts/%d/zip", artifact)))
}

// TreeEntry is one file or directory in a commit's tree.
type TreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // blob or tree
	SHA  string `json:"sha"`
	Size int64  `json:"size,omitempty"`
}

// Tree lists every file in a commit.
func (c Client) Tree(ctx context.Context, ref string) ([]TreeEntry, error) {
	var out struct {
		Tree      []TreeEntry `json:"tree"`
		Truncated bool        `json:"truncated"`
	}
	if err := c.call(ctx, "GET", c.path("git/trees/"+url.PathEscape(ref)+"?recursive=1"), nil, &out); err != nil {
		return nil, err
	}
	if out.Truncated {
		return out.Tree, fmt.Errorf("the tree at %s is too big to list in one request", ref)
	}
	return out.Tree, nil
}

// File reads one file as it is at ref.
func (c Client) File(ctx context.Context, path, ref string) ([]byte, error) {
	return c.run(ctx, nil, "api", "-H", "Accept: application/vnd.github.raw+json", c.path("contents/"+path+"?ref="+url.QueryEscape(ref)))
}
