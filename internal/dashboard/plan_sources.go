package dashboard

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

var planSourcePath = regexp.MustCompile(`^/api/plan-sources/([0-9a-f]{64})\.json$`)
var planSourceHash = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// A source record keeps the complete factory payload, separately from the
// lightweight snapshot. Restart checks its hash again before serving bytes.
type planSourceRecord struct {
	Repo     string              `json:"repo"`
	Factory  string              `json:"factory"`
	Proposal *formalize.Proposal `json:"proposal"`
}

type planSourceResponse struct {
	Hash      string            `json:"hash"`
	Sources   map[string]string `json:"sources"`
	Languages map[string]string `json:"languages"`
}

func planSourcesURL(hash string) string {
	return "/api/plan-sources/" + strings.TrimPrefix(hash, "sha256:") + ".json"
}

// checkedPlanSources verifies completeness without Validate, which normalizes
// a plan. Review must never rewrite the proposal or its presentation metadata.
func checkedPlanSources(p *formalize.Proposal) []byte {
	if p == nil || p.Plan == nil || !planSourceHash.MatchString(p.Hash) || p.Hash != p.Plan.Hash() {
		return nil
	}
	pl := p.Plan
	files := pl.TestFiles()
	if len(files) == 0 || len(pl.Sources) != len(files) {
		return nil
	}
	languages := map[string]string{}
	for _, file := range files {
		if file == "" || pl.Sources[file] == "" {
			return nil
		}
		languages[file] = pl.SourceLanguage(file)
	}
	body, err := json.Marshal(planSourceResponse{Hash: p.Hash, Sources: pl.Sources, Languages: languages})
	if err != nil {
		return nil
	}
	return body
}

// presentPlanSources publishes a URL only after retaining the complete,
// verified payload from this repository's own factory. Old URLs stay kept
// when a later proposal replaces the card or a command answers it.
func (s *Server) presentPlanSources(is *Issue, comments []github.Comment, login string) {
	if login == "" || is.Waiting == nil || is.Waiting.Plan == nil {
		return
	}
	v := is.Waiting.Plan
	for _, c := range comments {
		if c.User.Login != login {
			continue
		}
		m, ok := factory.DecodeMarker(c.Body)
		if !ok || m.Kind != factory.KindProposal || m.Proposal == nil || m.Proposal.Hash != v.SourceHash {
			continue
		}
		record := planSourceRecord{Repo: is.Repo, Factory: login, Proposal: m.Proposal}
		if s.keepPlanSources(record) {
			v.SourceURL = planSourcesURL(v.SourceHash)
		}
	}
}

func (s *Server) keepPlanSources(record planSourceRecord) bool {
	// Serialize and detach once: neither an in-memory proposal nor a caller's
	// source map may change the bytes already published under this hash.
	raw, err := json.Marshal(record)
	var detached planSourceRecord
	if err != nil || json.Unmarshal(raw, &detached) != nil {
		return false
	}
	body := checkedPlanSources(detached.Proposal)
	if body == nil {
		return false
	}
	hash := record.Proposal.Hash
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.planSources[hash] != nil {
		return true
	}
	if s.Cache != "" {
		dir := filepath.Join(s.Cache, "plan-sources")
		file := filepath.Join(dir, strings.TrimPrefix(hash, "sha256:")+".json")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			s.logf("keeping plan sources: %v", err)
			return false
		}
		if err := os.WriteFile(file+".tmp", raw, 0o600); err != nil {
			s.logf("keeping plan sources: %v", err)
			return false
		}
		if err := os.Rename(file+".tmp", file); err != nil {
			s.logf("keeping plan sources: %v", err)
			return false
		}
	}
	if s.planSources == nil {
		s.planSources = map[string][]byte{}
	}
	s.planSources[hash] = body
	return true
}

func (s *Server) loadPlanSources() {
	if s.Cache == "" {
		return
	}
	dir := filepath.Join(s.Cache, "plan-sources")
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.IsDir() || !planSourcePath.MatchString("/api/plan-sources/"+entry.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		var record planSourceRecord
		if err != nil || json.Unmarshal(raw, &record) != nil || record.Proposal == nil {
			continue
		}
		authorized := false
		for _, r := range s.Repos {
			if r.Name == record.Repo && record.Factory != "" && s.factoryOf(r) == record.Factory {
				authorized = true
			}
		}
		hash := record.Proposal.Hash
		if !authorized || entry.Name() != strings.TrimPrefix(hash, "sha256:")+".json" {
			continue
		}
		body := checkedPlanSources(record.Proposal)
		if body == nil {
			continue
		}
		s.mu.Lock()
		if s.planSources == nil {
			s.planSources = map[string][]byte{}
		}
		s.planSources[hash] = body
		s.mu.Unlock()
	}
}

func (s *Server) servePlanSources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	m := planSourcePath.FindStringSubmatch(r.URL.Path)
	var body []byte
	if m != nil {
		s.mu.RLock()
		body = s.planSources["sha256:"+m[1]]
		s.mu.RUnlock()
	}
	if body == nil {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
		} else {
			http.Error(w, "acceptance sources unavailable for that full proposal hash", http.StatusNotFound)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

// Old snapshots have only the display hash. Their inline sources cannot
// establish source authority, even when their text looks like a real plan.
func inlinePlanSources(raw []byte) bool {
	var snap struct {
		Issues []struct {
			Waiting *struct {
				Plan     map[string]json.RawMessage `json:"plan"`
				Proposal *struct {
					Plan map[string]json.RawMessage `json:"plan"`
				} `json:"proposal"`
			} `json:"waiting"`
		} `json:"issues"`
	}
	if json.Unmarshal(raw, &snap) != nil {
		return true
	}
	for _, is := range snap.Issues {
		if w := is.Waiting; w != nil {
			if _, ok := w.Plan["sources"]; ok {
				return true
			}
			if w.Proposal != nil {
				if _, ok := w.Proposal.Plan["sources"]; ok {
					return true
				}
			}
		}
	}
	return false
}
