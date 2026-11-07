package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The cache lock covers builds, evidence writes and retirement. HTTP readers
// take only Server.mu and keep their immutable bytes after a page is retired.
// Publication takes acted.mu, then this lock, then Server.mu.
type proposalCache struct {
	mu     sync.Mutex
	inputs map[string]*proposalEntry
	live   map[string]bool
	builds [2]uint64
	writes uint64
}

type proposalEntry struct {
	directory string
	record    []byte
	page      readerPage
	saved     string // the file successfully written or independently restored
}

// Each adapter retains its typed validator, renderer and damage wording.
// Change format when its validation or rendering contract changes.
type proposalCodec[E any] struct {
	kind                        int
	directory, format, template string
	build                       func(E) readerPage
	problem                     func(E, string) E
}

func (c proposalCodec[E]) key(record []byte) string {
	return readerHash(readerJSON(struct {
		Kind, Format, Template string
		Evidence               json.RawMessage
	}{c.directory, c.format, c.template, record}))
}

func (s *Server) proposalCacheWork() (uint64, uint64, uint64) {
	s.proposals.mu.Lock()
	defer s.proposals.mu.Unlock()
	return s.proposals.builds[0], s.proposals.builds[1], s.proposals.writes
}

// Build from the serialized record, never from caller-owned maps or pointers.
// UseNumber also preserves exact integers in retained temporal evidence.
func buildProposal[E any](s *Server, c proposalCodec[E], record []byte) (*proposalEntry, error) {
	var e E
	if err := decodeReaderJSON(record, &e); err != nil {
		return nil, err
	}
	s.proposals.builds[c.kind]++
	return &proposalEntry{directory: c.directory, record: record, page: c.build(e)}, nil
}

func keepProposal[E any](s *Server, c proposalCodec[E], e E) *ReaderPresentation {
	s.proposals.mu.Lock()
	defer s.proposals.mu.Unlock()
	p, err := keepProposalLocked(s, c, e)
	if err != nil {
		s.logf("keeping %s evidence: %v", c.directory, err)
	}
	return p
}

func keepProposalLocked[E any](s *Server, c proposalCodec[E], e E) (*ReaderPresentation, error) {
	record, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	key := c.key(record)
	entry := s.proposals.inputs[key]
	if entry == nil {
		entry, err = buildProposal(s, c, record)
		if err != nil {
			return nil, err
		}
	}
	s.installProposal(key, entry)
	s.saveProposal(entry)
	p := entry.page.presentation
	return &p, nil
}

// The caller holds proposals.mu. Installation never exposes mutable inputs or
// returns the presentation stored in the entry.
func (s *Server) installProposal(key string, e *proposalEntry) {
	if s.proposals.inputs == nil {
		s.proposals.inputs = map[string]*proposalEntry{}
	}
	s.proposals.inputs[key] = e
	s.mu.Lock()
	if s.readers == nil {
		s.readers = map[string]readerPage{}
	}
	s.readers[strings.TrimPrefix(e.page.presentation.Fingerprint, "sha256:")] = e.page
	s.mu.Unlock()
}

func (s *Server) saveProposal(e *proposalEntry) {
	if s.Cache == "" {
		return
	}
	key := strings.TrimPrefix(e.page.presentation.Fingerprint, "sha256:")
	file := filepath.Join(s.Cache, e.directory, key+".json")
	if e.saved == file {
		return
	}
	s.proposals.writes++
	if err := writeProposal(file, e.record); err != nil {
		s.logf("keeping %s evidence: %v", e.directory, err)
		return
	}
	e.saved = file
}

func writeProposal(file string, record []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".evidence-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(record)
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	return os.Rename(f.Name(), file)
}

func restoreProposal[E any](s *Server, c proposalCodec[E], expected E, old *ReaderPresentation, problem string) *ReaderPresentation {
	s.proposals.mu.Lock()
	defer s.proposals.mu.Unlock()
	// A memory hit is not authority for a saved file or its snapshot card.
	if old != nil && old.Format == c.format {
		parts := readerPath.FindStringSubmatch(old.Page)
		if len(parts) == 3 && parts[2] == "html" {
			file := filepath.Join(s.Cache, c.directory, parts[1]+".json")
			b, err := os.ReadFile(file)
			var saved E
			if err == nil && decodeReaderJSON(b, &saved) == nil && sameReaderJSON(c.problem(saved, ""), expected) {
				record, err := json.Marshal(saved)
				if err == nil {
					entry, err := buildProposal(s, c, record)
					if err == nil && entry.page.presentation == *old {
						entry.saved = file
						s.installProposal(c.key(record), entry)
						p := entry.page.presentation
						return &p
					}
				}
			}
		}
	}
	p, err := keepProposalLocked(s, c, c.problem(expected, problem))
	if err != nil {
		s.logf("restoring %s evidence: %v", c.directory, err)
	}
	return p
}

// A concurrent publication may retire a candidate's pages between assembly and
// publication. Rebuild only missing pages from that candidate's complete record,
// requiring the exact same presentation before the snapshot can become live.
func ensureProposal[E any](s *Server, c proposalCodec[E], e E, p *ReaderPresentation) error {
	if p == nil {
		return nil
	}
	s.mu.RLock()
	page, ok := s.readers[strings.TrimPrefix(p.Fingerprint, "sha256:")]
	s.mu.RUnlock()
	if ok && page.presentation == *p {
		return nil
	}
	if p.Status == "cached_evidence" {
		e = c.problem(e, p.Message)
	}
	got, err := keepProposalLocked(s, c, e)
	if err != nil {
		return err
	}
	if *got != *p {
		return fmt.Errorf("%s presentation no longer matches its snapshot", c.directory)
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
		if w.Reader != nil {
			e := comparisonEvidence{Format: comparisonFormat, Repo: is.Repo, Issue: is.Number, Proposal: w.Proposal}
			if err := ensureProposal(s, comparisonCodec, e, w.Reader); err != nil {
				return err
			}
		}
		for _, q := range w.Forks {
			for _, o := range q.Options {
				if err := ensureProposal(s, previewCodec, optionEvidence(&is, q, o), o.Reader); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func proposalReferences(snap Snapshot, live map[string]bool) {
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
}

// Called only after replacement succeeds, with proposals.mu and Server.mu
// held. Receipt pages have their own recovery and lifetime.
func (s *Server) retireProposals(live map[string]bool) {
	for input, e := range s.proposals.inputs {
		key := strings.TrimPrefix(e.page.presentation.Fingerprint, "sha256:")
		if !live[key] {
			delete(s.proposals.inputs, input)
			delete(s.readers, key)
		}
	}
}

// The cache lock prevents a build/save from racing the directory sweep, which
// also removes records left by an interrupted run before publication.
func (s *Server) pruneProposals(live map[string]bool) {
	if s.Cache == "" {
		return
	}
	retained := map[string]bool{}
	for key := range live {
		retained[key] = true
	}
	// A failed snapshot save must leave the previous durable snapshot usable.
	// After its atomic replacement, saveSnapshot repeats the sweep.
	if gz, err := os.ReadFile(s.snapshotFile()); err == nil {
		if js, err := gunzip(gz); err == nil {
			var saved Snapshot
			if decodeReaderJSON(js, &saved) == nil {
				proposalReferences(saved, retained)
			}
		}
	}
	for _, dir := range []string{"comparisons", "previews"} {
		files, err := os.ReadDir(filepath.Join(s.Cache, dir))
		if err != nil {
			if !os.IsNotExist(err) {
				s.logf("reading %s cache: %v", dir, err)
			}
			continue
		}
		for _, f := range files {
			key := strings.TrimSuffix(f.Name(), ".json")
			temporary := strings.HasPrefix(f.Name(), ".evidence-") && strings.HasSuffix(f.Name(), ".tmp")
			if f.IsDir() || (!temporary && (f.Name() != key+".json" || !readerPath.MatchString("/reader/"+key+".html") || retained[key])) {
				continue
			}
			if err := os.Remove(filepath.Join(s.Cache, dir, f.Name())); err != nil && !os.IsNotExist(err) {
				s.logf("retiring %s evidence: %v", dir, err)
			}
		}
	}
}
