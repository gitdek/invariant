package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

const savedDraftFormat = "1: authenticated complete saved draft"

// SavedDraftPresentation links the original result independently of the
// current card's remaining questions and observed answers (#897).
type SavedDraftPresentation struct {
	Reference factory.DraftReference `json:"reference"`
	Locator   string                 `json:"locator"`
	Reader    *ReaderPresentation    `json:"reader,omitempty"`
	Download  string                 `json:"download,omitempty"`
	Problem   string                 `json:"problem,omitempty"`
}

type savedDraftEvidence struct {
	Format  string
	Repo    string
	Issue   int
	Comment github.Comment
	Run     savedDraftRun
	Payload []byte
}

type savedDraftRun struct {
	Issue   int
	Step    string
	Commit  string
	Message string
}

type savedDraftEntry struct {
	evidence     savedDraftEvidence
	presentation SavedDraftPresentation
	storage      *proposalEntry
	pages        []ReaderPresentation
}

type savedDraftResolution struct {
	marker       factory.Marker
	presentation SavedDraftPresentation
}

type savedLocator struct {
	factory      string
	presentation SavedDraftPresentation
}

func draftLocator(ref factory.DraftReference) string {
	return fmt.Sprintf("/draft/%s/%d/%s/%s", ref.Repository, ref.Issue, ref.Commit, strings.TrimPrefix(ref.SHA256, "sha256:"))
}

func draftPresentation(m factory.Marker) *SavedDraftPresentation {
	var ref factory.DraftReference
	_ = json.Unmarshal(m.ProposalRef, &ref)
	return &SavedDraftPresentation{Reference: ref, Locator: draftLocator(ref)}
}

func savedCommentKey(repo string, issue int, c github.Comment) string {
	return readerHash(readerJSON(struct {
		Repo    string
		Issue   int
		Comment github.Comment
	}{repo, issue, c}))
}

func (s *Server) savedFactory(repo, login string) bool {
	if login == "" {
		return false
	}
	for _, r := range s.Repos {
		if r.Name == repo && s.factoryOf(r) == login {
			return true
		}
	}
	return false
}

func (run savedDraftRun) read(_ context.Context, issue int, step string) (factory.RunState, string, error) {
	if run.Issue != issue || run.Step != step || run.Commit == "" {
		return factory.RunNone, "", fmt.Errorf("saved completed-run authority is missing")
	}
	if strings.HasPrefix(run.Message, "invariant: recorded ") {
		return factory.RunRecorded, run.Commit, nil
	}
	return factory.RunDone, run.Commit, nil
}

func (e savedDraftEvidence) show(_ context.Context, commit, file string) ([]byte, error) {
	if commit != e.Run.Commit || file != "result.json" {
		return nil, fmt.Errorf("saved draft read is outside its immutable result")
	}
	return e.Payload, nil
}

// Only the shared resolver authorizes reads, after the enclosing comment's
// factory account has been checked. No branch, local work tree or URL fallback
// can supply a result. A failed resolution remains retryable on every refresh.
func (s *Server) resolveSavedComment(ctx context.Context, r *Repo, issue int, c github.Comment, online bool) {
	if !s.savedFactory(r.Name, c.User.Login) {
		return
	}
	m, ok := factory.DecodeMarker(c.Body)
	if !ok || m.ProposalRef == nil {
		return
	}
	s.proposals.mu.Lock()
	defer s.proposals.mu.Unlock()
	if s.proposals.resolved == nil {
		s.proposals.resolved = map[string]savedDraftResolution{}
	}
	key := savedCommentKey(r.Name, issue, c)
	if old, ok := s.proposals.resolved[key]; ok && old.presentation.Problem == "" {
		return
	}
	d := *draftPresentation(m)
	e := savedDraftEvidence{Format: savedDraftFormat, Repo: r.Name, Issue: issue, Comment: c}
	entry := s.proposals.drafts[d.Locator]
	var hydrated factory.Marker
	var result *formalize.Result
	var err error
	if entry != nil && s.savedFactory(r.Name, entry.evidence.Comment.User.Login) {
		hydrated, result, err = m.ResolveDraft(ctx, r.Name, issue, entry.evidence.Run.read, entry.evidence.show)
	} else if online {
		client := r.GitHub
		client.Repo = r.Name
		hydrated, result, err = m.ResolveDraft(ctx, r.Name, issue,
			func(ctx context.Context, n int, step string) (factory.RunState, string, error) {
				commit, err := client.Ref(ctx, fmt.Sprintf("invariant/runs/%d/%s", n, step))
				if err != nil || commit == "" {
					return factory.RunNone, "", err
				}
				// ResolveDraft checks the returned commit too. Refuse a
				// substituted head before even reading its commit message.
				if commit != d.Reference.Commit {
					return factory.RunNone, commit, nil
				}
				message, err := client.CommitMessage(ctx, commit)
				if err != nil {
					return factory.RunNone, "", err
				}
				e.Run = savedDraftRun{Issue: n, Step: step, Commit: commit, Message: message}
				return e.Run.read(ctx, n, step)
			},
			func(ctx context.Context, commit, file string) ([]byte, error) {
				if commit != d.Reference.Commit || file != "result.json" {
					return nil, fmt.Errorf("unexpected saved draft read")
				}
				b, err := client.File(ctx, file, commit)
				e.Payload = b
				return b, err
			})
	} else {
		err = fmt.Errorf("cached saved draft evidence is missing or damaged; retry after restoring the saved record")
	}
	if err == nil && result != nil && entry == nil {
		entry, err = s.keepSavedDraft(e, result, "")
	}
	if err == nil && entry != nil {
		d = entry.presentation
	} else {
		if err == nil {
			err = fmt.Errorf("saved draft evidence is unavailable")
		}
		d.Problem = err.Error()
		// In particular, conflicting inline fields never become a fallback.
		hydrated = m
		hydrated.Proposal, hydrated.Forks = nil, nil
	}
	s.proposals.resolved[key] = savedDraftResolution{marker: hydrated, presentation: d}
}

func (s *Server) readSavedDrafts(ctx context.Context, r *Repo) {
	// Damaged retained records can recover even after their issue's current
	// card and comment have moved on to another draft.
	s.proposals.mu.Lock()
	var retry []savedDraftEvidence
	for _, e := range s.proposals.retryDrafts {
		if e.Repo == r.Name {
			retry = append(retry, e)
		}
	}
	s.proposals.mu.Unlock()
	for _, e := range retry {
		s.resolveSavedComment(ctx, r, e.Issue, e.Comment, true)
	}
	for issue, cached := range r.src.comments {
		for _, c := range cached.comments {
			s.resolveSavedComment(ctx, r, issue, c, true)
		}
	}
}

// The caller holds proposals.mu. Persist the detached original and its
// authority before publishing its locator. Generated child pages can always
// be rebuilt from this record, independently of the snapshot's lifetime.
func (s *Server) keepSavedDraft(e savedDraftEvidence, result *formalize.Result, saved string) (*savedDraftEntry, error) {
	m, _ := factory.DecodeMarker(e.Comment.Body)
	d := *draftPresentation(m)
	var pages []ReaderPresentation
	var comparison *ReaderPresentation
	var native readerPage
	if result.Proposal.Reader != nil {
		entry, err := comparisonCache.keepLocked(s, comparisonEvidence{Format: comparisonFormat, Repo: e.Repo, Issue: e.Issue, Proposal: result.Proposal})
		if err != nil {
			return nil, err
		}
		p := entry.page.presentation
		comparison, native = &p, entry.page
		pages = append(pages, p)
	}
	var questions []Question
	for _, f := range result.Proposal.Forks {
		q := Question{ID: f.ID, Kind: f.Kind, Question: f.Question}
		for _, o := range f.Options {
			choice := Choice{ID: o.ID, Says: o.Says, Preview: o.Preview}
			if o.Preview != nil {
				entry, err := previewCache.keepLocked(s, previewEvidence{Format: previewFormat, Repo: e.Repo, Issue: e.Issue, Fork: f.ID, Kind: f.Kind, Question: f.Question, Option: o})
				if err != nil {
					return nil, err
				}
				p := entry.page.presentation
				choice.Reader, choice.Posters = &p, entry.page.posters
				pages = append(pages, p)
			}
			q.Options = append(q.Options, choice)
		}
		questions = append(questions, q)
	}
	s.proposals.comparisonBuilds++
	page, err := makeSavedDraft(d.Reference, e.Payload, result, comparison, native, questions)
	if err != nil {
		return nil, err
	}
	page.factory = e.Comment.User.Login
	d.Reader = &page.presentation
	d.Download = strings.TrimSuffix(d.Reader.Page, ".html") + ".json"
	record, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	storage := &proposalEntry{directory: "saved-drafts", storageKey: strings.TrimPrefix(readerHash([]byte(d.Locator)), "sha256:"), record: record, page: page, saved: saved}
	if err := s.saveProposal(storage); err != nil {
		return nil, fmt.Errorf("saving authenticated draft evidence: %w; retry after restoring the cache", err)
	}
	entry := &savedDraftEntry{evidence: e, presentation: d, storage: storage, pages: pages}
	if s.proposals.drafts == nil {
		s.proposals.drafts = map[string]*savedDraftEntry{}
	}
	s.proposals.drafts[d.Locator] = entry
	delete(s.proposals.retryDrafts, d.Locator)
	s.mu.Lock()
	if s.readers == nil {
		s.readers = map[string]readerPage{}
	}
	s.readers[strings.TrimPrefix(page.presentation.Fingerprint, "sha256:")] = page
	if s.savedLocators == nil {
		s.savedLocators = map[string]savedLocator{}
	}
	s.savedLocators[d.Locator] = savedLocator{factory: e.Comment.User.Login, presentation: d}
	s.mu.Unlock()
	return entry, nil
}

func (s *Server) savedLane(issue github.Issue, r *Repo, login string, now time.Time) Issue {
	comments := r.src.comments[issue.Number].comments
	if login == "" {
		return factoryLane(issue, comments, login, now)
	}
	s.proposals.mu.Lock()
	defer s.proposals.mu.Unlock()
	var latest *SavedDraftPresentation
	l := lane(issue, comments, login, now, func(c github.Comment, m factory.Marker) factory.Marker {
		if m.Kind != factory.KindNote {
			latest = nil
		}
		if m.ProposalRef == nil {
			return m
		}
		resolved, ok := s.proposals.resolved[savedCommentKey(r.Name, issue.Number, c)]
		if !ok {
			m.Proposal, m.Forks = nil, nil
			return m
		}
		if m.Kind != factory.KindNote {
			d := resolved.presentation
			latest = &d
		}
		return resolved.marker
	})
	if l.Waiting != nil && latest != nil {
		l.Waiting.SavedDraft = latest
	}
	return l
}

// Restart validates retained authority and the exact result bytes through the
// same resolver. Never reconstruct a saved source from a snapshot proposal.
func (s *Server) loadSavedDrafts() {
	if s.Cache == "" {
		return
	}
	dir := filepath.Join(s.Cache, "saved-drafts")
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, file.Name()))
		var e savedDraftEvidence
		if err != nil || decodeReaderJSON(b, &e) != nil || e.Format != savedDraftFormat || !s.savedFactory(e.Repo, e.Comment.User.Login) {
			continue
		}
		m, ok := factory.DecodeMarker(e.Comment.Body)
		if !ok || m.ProposalRef == nil {
			continue
		}
		d := draftPresentation(m)
		if file.Name() != strings.TrimPrefix(readerHash([]byte(d.Locator)), "sha256:")+".json" {
			continue
		}
		_, result, err := m.ResolveDraft(context.Background(), e.Repo, e.Issue, e.Run.read, e.show)
		s.proposals.mu.Lock()
		if err == nil {
			_, err = s.keepSavedDraft(e, result, filepath.Join(dir, file.Name()))
		}
		if err != nil {
			if s.proposals.retryDrafts == nil {
				s.proposals.retryDrafts = map[string]savedDraftEvidence{}
			}
			// Retain only the comment for retry; damaged bytes are never
			// written back or accepted under a freshly computed identity.
			e.Payload = nil
			s.proposals.retryDrafts[d.Locator] = e
			s.logf("restoring saved draft: %v", err)
		}
		s.proposals.mu.Unlock()
	}
	for _, r := range s.Repos {
		for issue, cached := range r.src.comments {
			for _, c := range cached.comments {
				s.resolveSavedComment(context.Background(), r, issue, c, false)
			}
		}
	}
}

func (s *Server) restoreSavedDrafts(snap *Snapshot) {
	for i := range snap.Issues {
		is := &snap.Issues[i]
		w := is.Waiting
		if w == nil || w.SavedDraft == nil {
			continue
		}
		if is.Repo == "" {
			is.Repo = snap.Repo
		}
		var restored *Waiting
		for _, r := range s.Repos {
			if r.Name == is.Repo && s.factoryOf(r) != "" && len(r.src.comments[is.Number].comments) > 0 {
				l := s.savedLane(github.Issue{Number: is.Number, State: "open"}, r, s.factoryOf(r), snap.GeneratedAt)
				restored = l.Waiting
				break
			}
		}
		if restored == nil || restored.SavedDraft == nil {
			d := *w.SavedDraft
			d.Reader, d.Download = nil, ""
			d.Problem = "Saved draft authority is unavailable; retry after restoring the factory comment and saved record."
			restored = &Waiting{Kind: w.Kind, SavedDraft: &d, Takes: []string{factory.Revise}}
		}
		restored.Since, restored.ActURL = w.Since, w.ActURL
		is.Waiting = restored
		s.presentPreviews(is)
		s.presentComparison(is)
	}
}

func savedDraftError(w http.ResponseWriter, r *http.Request, message string, code int) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", readerPolicy("text/plain"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		fmt.Fprintln(w, message)
	}
}

func (s *Server) serveSavedDraft(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	entry, ok := s.savedLocators[r.URL.Path]
	s.mu.RUnlock()
	if !ok || !s.savedFactory(entry.presentation.Reference.Repository, entry.factory) {
		savedDraftError(w, r, "Authenticated saved draft evidence is missing or invalid; restore the factory comment and immutable saved record.", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", readerPolicy("text/plain"))
	w.Header().Set("Location", entry.presentation.Reader.Page)
	w.WriteHeader(http.StatusFound)
}
