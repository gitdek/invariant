package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
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

// Job is one job of a workflow run.
type Job struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	RunnerID   int64  `json:"runner_id"`
	Steps      []struct {
		Name string `json:"name"`
	} `json:"steps"`
}

// Started says whether the job ran at all. GitHub fails a job it never gives
// a runner, such as when the account's Actions minutes have run out, and
// that job has no runner and no steps.
func (j Job) Started() bool { return j.RunnerID != 0 || len(j.Steps) > 0 }

// Job reads one job. A check run that GitHub Actions made has its job's ID.
func (c Client) Job(ctx context.Context, id int64) (Job, error) {
	var out Job
	return out, c.call(ctx, "GET", c.path(fmt.Sprintf("actions/jobs/%d", id)), nil, &out)
}

// Jobs lists a workflow run's jobs.
func (c Client) Jobs(ctx context.Context, run int64) ([]Job, error) {
	var out struct {
		Jobs []Job `json:"jobs"`
	}
	err := c.call(ctx, "GET", c.path(fmt.Sprintf("actions/runs/%d/jobs?per_page=100", run)), nil, &out)
	return out.Jobs, err
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

// Ref is where a ref points, such as "invariant/lease", or "" when it
// doesn't exist.
func (c Client) Ref(ctx context.Context, ref string) (string, error) {
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := c.call(ctx, "GET", c.path("git/ref/"+ref), nil, &out)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return out.Object.SHA, err
}

// CommitMessage is a commit's message.
func (c Client) CommitMessage(ctx context.Context, sha string) (string, error) {
	var out struct {
		Message string `json:"message"`
	}
	return out.Message, c.call(ctx, "GET", c.path("git/commits/"+sha), nil, &out)
}

// File reads one file as it is at ref.
func (c Client) File(ctx context.Context, path, ref string) ([]byte, error) {
	return c.run(ctx, nil, "api", "-H", "Accept: application/vnd.github.raw+json", c.path("contents/"+path+"?ref="+url.QueryEscape(ref)))
}

// DirFile is one file in a directory, as it is at a commit.
type DirFile struct {
	Name string
	OID  string // its blob ID, which changes whenever the file does
	Text string
}

// Files reads each file directly in a directory at ref, leaving out its
// subdirectories, in one GraphQL query however many files there are. A
// directory that isn't there is ErrNotFound, and a file whose text GitHub
// doesn't give whole is an error.
func (c Client) Files(ctx context.Context, dir, ref string) ([]DirFile, error) {
	owner, name, _ := strings.Cut(c.Repo, "/")
	stdout, err := c.run(ctx, nil, "api", "graphql",
		"-f", "query=query($owner: String!, $name: String!, $expression: String!) { repository(owner: $owner, name: $name) { object(expression: $expression) { ... on Tree { entries { name type oid object { ... on Blob { text isTruncated } } } } } } }",
		"-f", "owner="+owner, "-f", "name="+name, "-f", "expression="+ref+":"+dir)
	if err != nil {
		return nil, fmt.Errorf("POST graphql: %w", err)
	}
	var out struct {
		Data struct {
			Repository *struct {
				Object *struct {
					Entries []struct {
						Name   string `json:"name"`
						Type   string `json:"type"` // blob, tree or commit
						OID    string `json:"oid"`
						Object struct {
							Text        *string `json:"text"`
							IsTruncated bool    `json:"isTruncated"`
						} `json:"object"`
					} `json:"entries"`
				} `json:"object"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout, &out); err != nil {
		return nil, err
	}
	if out.Data.Repository == nil {
		return nil, fmt.Errorf("GitHub didn't find %s", c.Repo)
	}
	if out.Data.Repository.Object == nil {
		return nil, fmt.Errorf("%s at %s: %w", dir, ref, ErrNotFound)
	}
	var files []DirFile
	for _, e := range out.Data.Repository.Object.Entries {
		if e.Type != "blob" {
			continue
		}
		// GitHub gives a binary file no text, and cuts a big one's short.
		if e.Object.Text == nil || e.Object.IsTruncated {
			return nil, fmt.Errorf("%s at %s can't be read whole", path.Join(dir, e.Name), ref)
		}
		files = append(files, DirFile{Name: e.Name, OID: e.OID, Text: *e.Object.Text})
	}
	return files, nil
}
