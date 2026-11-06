package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/gitdek/invariant/internal/github"
)

// A restart keeps the parts of GitHub's reads that a refresh already reuses
// until GitHub says they changed (#674): each issue's comments, as of the
// issue's last update, and each merged pull request's head and whether its
// gate passed, which don't change once it has merged. Its first refresh then
// reads only what changed since the last run, rather than every issue's
// comments and every merge again, one gh call after another.

const sourcesVersion = 1

type keptSources struct {
	Version int                 `json:"version"`
	Repos   map[string]keptRepo `json:"repos"`
}

type keptRepo struct {
	Comments map[int]keptComments `json:"comments,omitempty"`
	Merges   map[int]keptMerge    `json:"merges,omitempty"`
}

type keptComments struct {
	Updated  string           `json:"updated"`
	Comments []github.Comment `json:"comments"`
}

type keptMerge struct {
	SHA   string `json:"sha"`
	Green bool   `json:"green"`
}

// sourcesFile is where the kept reads are, beside the snapshot.
func (s *Server) sourcesFile() string { return filepath.Join(s.Cache, "sources.json.gz") }

// loadSources fills each repository's reads from the last run's. A file
// that's missing, damaged or of another version fills none, and the first
// refresh reads everything, as it did before.
func (s *Server) loadSources() {
	if s.Cache == "" {
		return
	}
	gz, err := os.ReadFile(s.sourcesFile())
	if err != nil {
		return
	}
	js, err := gunzip(gz)
	if err != nil {
		return
	}
	var k keptSources
	if json.Unmarshal(js, &k) != nil || k.Version != sourcesVersion {
		return
	}
	for _, r := range s.Repos {
		kr := k.Repos[r.Name]
		for n, c := range kr.Comments {
			if c.Updated != "" {
				r.src.comments[n] = cachedComments{updated: c.Updated, comments: c.Comments}
			}
		}
		for n, m := range kr.Merges {
			if m.SHA != "" {
				r.src.merges[n] = mergeCheck{sha: m.SHA, green: m.Green}
			}
		}
	}
}

// saveSources keeps the reads for the next run, once a refresh has changed
// them, replacing the file in one step.
func (s *Server) saveSources() {
	if s.Cache == "" {
		return
	}
	changed := false
	k := keptSources{Version: sourcesVersion, Repos: map[string]keptRepo{}}
	for _, r := range s.Repos {
		changed = changed || r.src.changed
		kr := keptRepo{Comments: map[int]keptComments{}, Merges: map[int]keptMerge{}}
		for n, c := range r.src.comments {
			kr.Comments[n] = keptComments{Updated: c.updated, Comments: c.comments}
		}
		for n, m := range r.src.merges {
			kr.Merges[n] = keptMerge{SHA: m.sha, Green: m.green}
		}
		k.Repos[r.Name] = kr
	}
	if !changed {
		return
	}
	js, err := json.Marshal(k)
	if err != nil {
		s.logf("keeping GitHub's reads: %v", err)
		return
	}
	gz, err := gzipped(js)
	if err != nil {
		s.logf("keeping GitHub's reads: %v", err)
		return
	}
	tmp := s.sourcesFile() + ".tmp"
	if err := os.MkdirAll(s.Cache, 0o755); err != nil {
		s.logf("keeping GitHub's reads: %v", err)
		return
	}
	if err := os.WriteFile(tmp, gz, 0o644); err != nil {
		s.logf("keeping GitHub's reads: %v", err)
		return
	}
	if err := os.Rename(tmp, s.sourcesFile()); err != nil {
		s.logf("keeping GitHub's reads: %v", err)
		return
	}
	for _, r := range s.Repos {
		r.src.changed = false
	}
}
