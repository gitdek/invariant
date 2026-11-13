package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Proposal and option evidence share storage, not validation. The complete
// detached record keys work within this process; the rendered page still owns
// the published identity. mu orders builds, saves and snapshot retirement.
// Publication takes acted.mu, then this mutex, then Server.mu. HTTP readers
// only take Server.mu and keep immutable page bytes after releasing it.
type proposalCache struct {
	mu                                             sync.Mutex
	entries                                        map[string]*proposalEntry
	drafts                                         map[string]*savedDraftEntry
	resolved                                       map[string]savedDraftResolution
	retryDrafts                                    map[string]savedDraftEvidence
	comparisonBuilds, previewBuilds, writeAttempts uint64
}

type proposalEntry struct {
	directory  string
	storageKey string // retained sources can outlive their generated presentations
	record     []byte
	page       readerPage
	saved      string // successful destination; an empty or failed save stays retryable
}

type proposalKind[T any] struct {
	directory, format, template string
	render                      func(T) readerPage
	withProblem                 func(T, string) T
}

func (s *Server) proposalCacheWork() (uint64, uint64, uint64) {
	s.proposals.mu.Lock()
	defer s.proposals.mu.Unlock()
	return s.proposals.comparisonBuilds, s.proposals.previewBuilds, s.proposals.writeAttempts
}

func (c proposalKind[T]) inputKey(record []byte) string {
	return readerHash(readerJSON(struct {
		Kind, Format, Template string
		Record                 json.RawMessage
	}{c.directory, c.format, c.template, record}))
}

// The caller holds proposals.mu for all builds and entry operations.
func (c proposalKind[T]) build(s *Server, e T) readerPage {
	if c.directory == "comparisons" {
		s.proposals.comparisonBuilds++
	} else {
		s.proposals.previewBuilds++
	}
	return c.render(e)
}

func (s *Server) installProposal(input string, entry *proposalEntry) {
	if s.proposals.entries == nil {
		s.proposals.entries = map[string]*proposalEntry{}
	}
	s.proposals.entries[input] = entry
	key := strings.TrimPrefix(entry.page.presentation.Fingerprint, "sha256:")
	s.mu.Lock()
	if s.readers == nil {
		s.readers = map[string]readerPage{}
	}
	s.readers[key] = entry.page
	s.mu.Unlock()
}

func (s *Server) saveProposal(entry *proposalEntry) error {
	if s.Cache == "" {
		return nil
	}
	key := strings.TrimPrefix(entry.page.presentation.Fingerprint, "sha256:")
	if entry.storageKey != "" {
		key = entry.storageKey
	}
	file := filepath.Join(s.Cache, entry.directory, key+".json")
	if entry.saved == file {
		return nil
	}
	s.proposals.writeAttempts++
	err := os.MkdirAll(filepath.Dir(file), 0o755)
	if err == nil {
		defer os.Remove(file + ".tmp")
		err = os.WriteFile(file+".tmp", entry.record, 0o600)
	}
	if err == nil {
		err = os.Rename(file+".tmp", file)
	}
	if err != nil {
		s.logf("keeping %s evidence: %v", entry.directory, err)
		return err
	}
	entry.saved = file
	return nil
}

func (c proposalKind[T]) keepLocked(s *Server, e T) (*proposalEntry, error) {
	record, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	input := c.inputKey(record)
	entry := s.proposals.entries[input]
	if entry == nil {
		var detached T
		if err := decodeReaderJSON(record, &detached); err != nil {
			return nil, err
		}
		entry = &proposalEntry{directory: c.directory, record: record, page: c.build(s, detached)}
		s.installProposal(input, entry)
	}
	s.saveProposal(entry)
	return entry, nil
}

func (c proposalKind[T]) keep(s *Server, e T) *ReaderPresentation {
	s.proposals.mu.Lock()
	defer s.proposals.mu.Unlock()
	entry, err := c.keepLocked(s, e)
	if err != nil {
		// Non-JSON caller input cannot be a reusable or durable record.
		s.logf("keeping %s evidence: %v", c.directory, err)
		return nil
	}
	p := entry.page.presentation
	return &p
}

func (c proposalKind[T]) restore(s *Server, expected T, old *ReaderPresentation, problem string) *ReaderPresentation {
	s.proposals.mu.Lock()
	defer s.proposals.mu.Unlock()
	// Always read and validate the saved record and card, even when an equal
	// input was already checked in memory. That lookup grants no disk trust.
	if old != nil && old.Format == c.format {
		parts := readerPath.FindStringSubmatch(old.Page)
		if len(parts) == 3 && parts[2] == "html" {
			file := filepath.Join(s.Cache, c.directory, parts[1]+".json")
			record, err := os.ReadFile(file)
			var saved T
			if err == nil && decodeReaderJSON(record, &saved) == nil && sameReaderJSON(c.withProblem(saved, ""), expected) {
				page := c.build(s, saved)
				if page.presentation == *old {
					record = readerJSON(saved)
					s.installProposal(c.inputKey(record), &proposalEntry{directory: c.directory, record: record, page: page, saved: file})
					p := page.presentation
					return &p
				}
			}
		}
	}
	entry, err := c.keepLocked(s, c.withProblem(expected, problem))
	if err != nil {
		s.logf("restoring %s evidence: %v", c.directory, err)
		return nil
	}
	p := entry.page.presentation
	return &p
}

// An overlapping publication may have retired a candidate between assembly
// and publication. Reinstall it from the candidate's complete snapshot before
// exposing any links, requiring the same immutable presentation.
func (c proposalKind[T]) ensure(s *Server, e T, p *ReaderPresentation) error {
	if p == nil {
		return nil
	}
	if p.Format != c.format {
		return fmt.Errorf("obsolete %s presentation format", c.directory)
	}
	if p.Status == "cached_evidence" {
		e = c.withProblem(e, p.Message)
	}
	entry, err := c.keepLocked(s, e)
	if err != nil {
		return err
	}
	if entry.page.presentation != *p {
		return fmt.Errorf("%s presentation differs from its snapshot evidence", c.directory)
	}
	return nil
}

func (s *Server) ensureProposals(snap Snapshot) error {
	for _, is := range snap.Issues {
		if is.Repo == "" {
			is.Repo = snap.Repo
		}
		w := is.Waiting
		if w == nil || !s.readerRepo(is.Repo) {
			continue
		}
		if w.SavedDraft != nil && w.SavedDraft.Problem == "" {
			entry := s.proposals.drafts[w.SavedDraft.Locator]
			if entry == nil || !sameReaderJSON(entry.presentation, w.SavedDraft) {
				return fmt.Errorf("saved draft differs from its authenticated record")
			}
			if err := s.saveProposal(entry.storage); err != nil {
				return err
			}
		}
		if err := comparisonCache.ensure(s, comparisonEvidence{Format: comparisonFormat, Repo: is.Repo, Issue: is.Number, Proposal: w.Proposal}, w.Reader); err != nil {
			return err
		}
		for _, q := range w.Forks {
			for _, o := range q.Options {
				if err := previewCache.ensure(s, optionEvidence(&is, q, o), o.Reader); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func proposalReferences(snap Snapshot) map[string]bool {
	live := map[string]bool{}
	add := func(p *ReaderPresentation) {
		if p != nil {
			live[strings.TrimPrefix(p.Fingerprint, "sha256:")] = true
		}
	}
	for _, is := range snap.Issues {
		if w := is.Waiting; w != nil {
			add(w.Reader)
			for _, q := range w.Forks {
				for _, o := range q.Options {
					add(o.Reader)
				}
			}
		}
	}
	for _, p := range snap.Projects {
		add(p.Reader)
	}
	return live
}

// Retire only after replacement, under proposals.mu. Receipt entries and
// sources have their own recovery policy and are never removed here.
func (s *Server) pruneProposals(snap Snapshot) {
	live := proposalReferences(snap)
	// Saved records retain every original alternative, even after its card
	// has been answered or replaced. Inline evidence keeps its old lifetime.
	for _, entry := range s.proposals.drafts {
		for _, p := range entry.pages {
			live[strings.TrimPrefix(p.Fingerprint, "sha256:")] = true
		}
	}
	s.mu.Lock()
	for input, entry := range s.proposals.entries {
		if !live[strings.TrimPrefix(entry.page.presentation.Fingerprint, "sha256:")] {
			delete(s.proposals.entries, input)
		}
	}
	for key, page := range s.readers {
		if (page.presentation.Format == comparisonFormat || page.presentation.Format == previewFormat) && !live[key] {
			delete(s.readers, key)
		}
	}
	s.mu.Unlock()
	if s.Cache == "" {
		return
	}
	for _, directory := range []string{"comparisons", "previews"} {
		dir := filepath.Join(s.Cache, directory)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				s.logf("reading %s cache: %v", directory, err)
			}
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			partial := strings.HasSuffix(name, ".json.tmp")
			key := strings.TrimSuffix(strings.TrimSuffix(name, ".tmp"), ".json")
			if entry.IsDir() || (!strings.HasSuffix(name, ".json") && !partial) || !readerPath.MatchString("/reader/"+key+".html") || (live[key] && !partial) {
				continue
			}
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				s.logf("retiring %s evidence: %v", directory, err)
			}
		}
	}
}
