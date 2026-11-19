package formalize

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Reader is computed by the check, never read from an agent's derived files.
// Its fingerprint binds explanatory evidence independently of ratification.
type Reader struct {
	Version          int                           `json:"version"`
	Fingerprint      string                        `json:"fingerprint"`
	Chart            *statechart.Result            `json:"chart,omitempty"`
	Limitation       *ReaderLimitation             `json:"limitation,omitempty"`
	Comparison       *statechart.Comparison        `json:"comparison,omitempty"`
	ComparisonRef    *statechart.EvidenceReference `json:"comparison_ref,omitempty"`
	CandidateRef     *statechart.EvidenceReference `json:"candidate_ref,omitempty"`
	CandidateSources bool                          `json:"candidate_sources,omitempty"`
}

type ReaderLimitation = statechart.Limitation

// A limited alternative has no drawable chart from which to manufacture an
// output limitation. Retain its complete preview and original size outcome;
// these byte counts describe only the containing question record's transport.
type PreviewReference struct {
	Version        int    `json:"version"`
	SHA256         string `json:"sha256"`
	Fingerprint    string `json:"fingerprint"`
	Actual         int    `json:"actual"`
	Limit          int    `json:"limit"`
	Representation string `json:"representation"`
}

// transport reports a well-formed size record of a containing record that
// overflowed. It binds no chart; its consumer checks the saved bytes.
func (ref *PreviewReference) transport() bool {
	return ref != nil && ref.Version == 1 && ref.Actual > statechart.MaxEvidenceBytes && ref.Limit == statechart.MaxEvidenceBytes &&
		slices.Contains([]string{"compact_json", "result.json", "formalization.json", "preview.json"}, ref.Representation)
}

func resolvePreviewEvidence(v *Preview, read func(string) ([]byte, error)) error {
	ref := v.EvidenceRef
	if ref == nil {
		return nil
	}
	if read == nil || !ref.transport() {
		return fmt.Errorf("invalid complete preview reference")
	}
	data, err := read("reader-candidate.json")
	if err != nil {
		return err
	}
	if len(data) > statechart.MaxEvidenceBytes || ref.SHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(data)) {
		return fmt.Errorf("missing, stale or oversized preview companion")
	}
	var saved Preview
	if err := json.Unmarshal(data, &saved); err != nil {
		return err
	}
	if saved.EvidenceRef != nil || saved.ReceiptFromModel || proposalHasEvidenceReferences(saved.Model) || saved.Fingerprint != ref.Fingerprint ||
		saved.Receipt != nil && saved.Receipt.Reader != nil && saved.Receipt.Reader.ComparisonRef != nil {
		return fmt.Errorf("preview companion identity differs or contains reference chains")
	}
	stub := saved
	stub.Model, stub.Receipt, stub.EvidenceRef = nil, nil, ref
	a, _ := json.Marshal(stub)
	b, _ := json.Marshal(v)
	if !bytes.Equal(a, b) {
		return fmt.Errorf("preview reference conflicts with its saved option")
	}
	*v = saved
	return nil
}

// BaselineSources removes only repeated model/include bytes. The captured
// lock, view (including an absent legacy view) and all authority stay inline.
// Reference binds a drawn chart in the same record. A question set whose
// alternatives are all limited has none, so Volume records only the
// containing record's transport size, as a limited alternative's preview does.
type BaselineSources struct {
	Reference *statechart.EvidenceReference `json:"reference,omitempty"`
	Volume    *PreviewReference             `json:"volume,omitempty"`
	Context   bool                          `json:"context,omitempty"`
	Proposal  bool                          `json:"proposal,omitempty"`
	Target    bool                          `json:"target,omitempty"`
}

// boundBaselineSources binds the companion to l when the record draws a
// chart, and otherwise to volume, which only a question set supplies.
func (r *Result) boundBaselineSources(baseline *statechart.ComparisonSnapshot, l *statechart.Limitation, volume *PreviewReference, files map[string][]byte) error {
	if baseline == nil {
		return nil
	}
	b, ok := files["reader-baseline.json"]
	if !ok {
		var err error
		b, err = json.MarshalIndent(baseline, "", "  ")
		if err != nil {
			return err
		}
		b = append(b, '\n')
	}
	if len(b) > statechart.MaxEvidenceBytes {
		return fmt.Errorf("independent baseline sources exceed their byte budget")
	}
	var saved statechart.ComparisonSnapshot
	if err := json.Unmarshal(b, &saved); err != nil {
		return err
	}
	canonical, _ := json.Marshal(saved)
	fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	sources := &BaselineSources{}
	switch {
	case l != nil:
		sources.Reference = statechart.NewEvidenceReference("reader-baseline.json", b, fingerprint, l)
	case volume != nil && saved.Chart == nil && saved.Limitation == nil:
		v := *volume
		v.SHA256, v.Fingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256(b)), fingerprint
		sources.Volume = &v
	default:
		return fmt.Errorf("captured baseline sources have no reference to bind them")
	}
	strip := func(model *string, includes *map[string]string) error {
		if *model != saved.Model || !maps.Equal(*includes, saved.Includes) {
			return fmt.Errorf("captured baseline sources differ from their companion")
		}
		*model = ""
		if len(*includes) > 0 {
			*includes = nil
		}
		return nil
	}
	if r.CheckContext != nil && r.CheckContext.Baseline != nil {
		context, snapshot := *r.CheckContext, *r.CheckContext.Baseline
		r.CheckContext, context.Baseline = &context, &snapshot
		if err := strip(&snapshot.Model, &snapshot.Includes); err != nil {
			return err
		}
		sources.Context = true
	}
	if r.Proposal.Baseline != nil {
		snapshot := *r.Proposal.Baseline
		r.Proposal.Baseline = &snapshot
		if err := strip(&snapshot.Model, &snapshot.Includes); err != nil {
			return err
		}
		sources.Proposal = true
	}
	if r.Proposal.Target != nil && r.Proposal.Target.Baseline != nil {
		target, current := *r.Proposal.Target, *r.Proposal.Target.Baseline
		r.Proposal.Target, target.Baseline = &target, &current
		if err := strip(&current.ModuleText, &current.Includes); err != nil {
			return err
		}
		sources.Target = true
	}
	if sources.Context || sources.Proposal || sources.Target {
		r.BaselineSources = sources
		files["reader-baseline.json"] = b
	}
	return nil
}

func (r *Result) resolveBaselineSources(read func(string) ([]byte, error)) error {
	b := r.BaselineSources
	if b == nil {
		return nil
	}
	if read == nil || (b.Reference == nil) == (b.Volume == nil) || (!b.Context && !b.Proposal && !b.Target) {
		return fmt.Errorf("captured baseline requires its fixed evidence companion")
	}
	data, err := read("reader-baseline.json")
	if err != nil {
		return err
	}
	var snapshot statechart.ComparisonSnapshot
	if b.Reference != nil {
		if snapshot, err = b.Reference.Snapshot("reader-baseline.json", data); err != nil {
			return err
		}
		if err := b.Reference.ValidateChart(proposalEvidenceChart(r.Proposal)); err != nil {
			return err
		}
	} else if snapshot, err = transportBaseline(r.Proposal, b.Volume, data); err != nil {
		return err
	}
	restore := func(model *string, includes *map[string]string) error {
		if *model != "" || len(snapshot.Includes) > 0 && *includes != nil {
			return fmt.Errorf("captured baseline reference conflicts with inline sources")
		}
		*model = snapshot.Model
		if len(snapshot.Includes) > 0 {
			*includes = maps.Clone(snapshot.Includes)
		}
		return nil
	}
	if b.Context {
		if r.CheckContext == nil || r.CheckContext.Baseline == nil {
			return fmt.Errorf("captured check context is missing")
		}
		if err := restore(&r.CheckContext.Baseline.Model, &r.CheckContext.Baseline.Includes); err != nil {
			return err
		}
	}
	if b.Proposal {
		if r.Proposal == nil || r.Proposal.Baseline == nil {
			return fmt.Errorf("captured proposal baseline is missing")
		}
		if err := restore(&r.Proposal.Baseline.Model, &r.Proposal.Baseline.Includes); err != nil {
			return err
		}
	}
	if b.Target {
		if r.Proposal == nil || r.Proposal.Target == nil || r.Proposal.Target.Baseline == nil {
			return fmt.Errorf("captured amendment baseline is missing")
		}
		current := r.Proposal.Target.Baseline
		if err := restore(&current.ModuleText, &current.Includes); err != nil {
			return err
		}
	}
	r.BaselineSources = nil
	return nil
}

// transportBaseline accepts the chart-free form only where no drawn chart
// could bind the companion: a question set whose resolved alternatives are
// all limited. The companion holds sources alone, decoded strictly and bound
// by its exact digest and canonical fingerprint.
func transportBaseline(p *Proposal, ref *PreviewReference, data []byte) (statechart.ComparisonSnapshot, error) {
	var s statechart.ComparisonSnapshot
	if p == nil || len(p.Forks) == 0 || proposalEvidenceChart(p) != nil || !ref.transport() ||
		len(data) > statechart.MaxEvidenceBytes || ref.SHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(data)) {
		return s, fmt.Errorf("missing, stale or invalid reader-baseline.json evidence companion")
	}
	type strict statechart.ComparisonSnapshot
	var decoded strict
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(&decoded); err != nil {
		return s, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return s, fmt.Errorf("extra reader-baseline.json evidence")
	}
	s = statechart.ComparisonSnapshot(decoded)
	canonical, _ := json.Marshal(s)
	if s.Chart != nil || s.Limitation != nil || fmt.Sprintf("sha256:%x", sha256.Sum256(canonical)) != ref.Fingerprint {
		return s, fmt.Errorf("captured baseline companion identity differs")
	}
	return s, nil
}

func proposalEvidenceChart(p *Proposal) *statechart.Result {
	if p == nil {
		return nil
	}
	if r := p.Reader; r != nil {
		if r.Chart != nil {
			return r.Chart
		}
		if c := r.Comparison; c != nil {
			if c.Candidate.Chart != nil {
				return c.Candidate.Chart
			}
			return c.Baseline.Chart
		}
	}
	for _, f := range p.Forks {
		for _, o := range f.Options {
			if o.Preview != nil {
				if c := proposalEvidenceChart(o.Preview.Model); c != nil {
					return c
				}
			}
		}
	}
	return nil
}

// EvidenceCompanion accepts only the fixed siblings, either for a selected
// proposal or in an option's existing preview export directory. References
// never choose a path; the containing fork and option select that directory.
func EvidenceCompanion(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) == 4 && parts[0] == "previews" && previewID.MatchString(parts[1]) && previewID.MatchString(parts[2]) {
		parts = parts[3:]
	}
	return len(parts) == 1 && slices.Contains([]string{"reader-comparison.json", "reader-baseline.json", "reader-candidate.json"}, parts[0])
}

func evidenceSize(representation string, data []byte) (string, int, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return "", 0, err
	}
	if compact.Len() > statechart.MaxEvidenceBytes {
		return "compact_json", compact.Len(), nil
	}
	return representation, len(data), nil
}

func candidateEvidence(p *Proposal, l *statechart.Limitation, files map[string][]byte) error {
	snapshot := p.comparisonSnapshot()
	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if len(b) > statechart.MaxEvidenceBytes {
		return fmt.Errorf("independent candidate evidence exceeds its byte budget")
	}
	if old, exists := files["reader-candidate.json"]; exists && !bytes.Equal(old, b) {
		return fmt.Errorf("candidate evidence conflicts with comparison companion")
	}
	files["reader-candidate.json"] = b
	canonical, _ := json.Marshal(snapshot)
	fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	p.Reader.CandidateRef = statechart.NewEvidenceReference("reader-candidate.json", b, fingerprint, l)
	p.Reader.Chart, p.Reader.Limitation = nil, nil
	p.Reader.CandidateSources = true
	p.ModuleText, p.Includes, p.View = "", nil, ""
	return nil
}

func (r *Result) previewEvidenceFiles(representation string, original []byte, encode func(*Result) ([]byte, error)) ([]byte, map[string][]byte, error) {
	if err := r.Proposal.ValidatePreviews(); err != nil {
		return nil, nil, err
	}
	representation, actual, err := evidenceSize(representation, original)
	if err != nil {
		return nil, nil, err
	}
	copyResult, copyProposal := *r, *r.Proposal
	copyResult.Proposal = &copyProposal
	copyProposal.Forks = slices.Clone(copyProposal.Forks)
	files := map[string][]byte{}
	for i := range copyProposal.Forks {
		f := &copyProposal.Forks[i]
		f.Options = slices.Clone(f.Options)
		for j := range f.Options {
			o := &f.Options[j]
			if o.Preview == nil {
				continue
			}
			v, p, rr := *o.Preview, *o.Preview.Model, *o.Preview.Model.Reader
			if rr.Chart == nil {
				b, err := json.MarshalIndent(&v, "", "  ")
				if err != nil {
					return nil, nil, err
				}
				b = append(b, '\n')
				if len(b) > statechart.MaxEvidenceBytes {
					return nil, nil, fmt.Errorf("independent limited preview exceeds its byte budget")
				}
				v.EvidenceRef = &PreviewReference{Version: 1, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(b)),
					Fingerprint: v.Fingerprint, Actual: actual, Limit: statechart.MaxEvidenceBytes, Representation: representation}
				v.Model, v.Receipt = nil, nil
				o.Preview = &v
				files["previews/"+f.ID+"/"+o.ID+"/reader-candidate.json"] = b
				continue
			}
			o.Preview, v.Model, p.Reader = &v, &p, &rr
			l := rr.Chart.OutputLimit(representation, "preview", actual)
			companions := map[string][]byte{}
			if c := rr.Comparison; c != nil {
				b, others, err := statechart.ComparisonFiles(c)
				if err != nil {
					return nil, nil, err
				}
				companions = others
				companions["reader-comparison.json"] = b
				rr.ComparisonRef = statechart.NewEvidenceReference("reader-comparison.json", b, c.Fingerprint, l)
				rr.Comparison = nil
			}
			if err := candidateEvidence(&p, l, companions); err != nil {
				return nil, nil, err
			}
			// ValidatePreviews has checked exact equality, including preview
			// scope, of these independently obtained outcomes. Keep both
			// original fingerprints and reconstruct before any validation.
			report, reader := *v.Receipt, *v.Receipt.Reader
			v.Receipt, report.Reader = &report, &reader
			reader.Chart, reader.Limitation = nil, nil
			v.ReceiptFromModel = true
			for name, b := range companions {
				files["previews/"+f.ID+"/"+o.ID+"/"+name] = b
			}
		}
	}
	data, err := encode(&copyResult)
	if err == nil && len(data) > statechart.MaxEvidenceBytes && r.CheckContext != nil {
		// A drawn alternative binds the baseline companion. With every
		// alternative limited, it records this record's transport size.
		var l *statechart.Limitation
		if chart := proposalEvidenceChart(r.Proposal); chart != nil {
			l = chart.OutputLimit(representation, "preview", actual)
		}
		volume := &PreviewReference{Version: 1, Actual: actual, Limit: statechart.MaxEvidenceBytes, Representation: representation}
		if err := copyResult.boundBaselineSources(r.CheckContext.Baseline, l, volume, files); err != nil {
			return nil, nil, err
		}
		data, err = encode(&copyResult)
	}
	if err == nil && len(data) > statechart.MaxEvidenceBytes {
		err = fmt.Errorf("complete saved question record exceeds its byte budget")
	}
	return data, files, err
}

// EvidenceFiles bounds complete saved records without changing the checked
// in-memory result. The encoder owns the actual wrapper and whitespace.
func (r *Result) EvidenceFiles(representation string, encode func(*Result) ([]byte, error)) ([]byte, map[string][]byte, error) {
	data, err := encode(r)
	files := map[string][]byte{}
	if err != nil || len(data) <= statechart.MaxEvidenceBytes {
		return data, files, err
	}
	if r.Proposal != nil && len(r.Proposal.Forks) > 0 {
		return r.previewEvidenceFiles(representation, data, encode)
	}
	if r.Proposal == nil || r.Proposal.Reader == nil {
		return nil, nil, fmt.Errorf("complete saved evidence exceeds its byte budget")
	}
	chart, side := r.Proposal.Reader.Chart, "proposal"
	if chart == nil && r.Proposal.Reader.Comparison != nil {
		chart, side = r.Proposal.Reader.Comparison.Candidate.Chart, "comparison"
		if chart == nil {
			chart = r.Proposal.Reader.Comparison.Baseline.Chart
		}
	}
	if chart == nil {
		return nil, nil, fmt.Errorf("complete saved limitation record exceeds its byte budget")
	}
	copyResult, copyProposal, copyReader := *r, *r.Proposal, *r.Proposal.Reader
	copyResult.Proposal = &copyProposal
	copyProposal.Reader = &copyReader
	representation, actual, err := evidenceSize(representation, data)
	if err != nil {
		return nil, nil, err
	}
	l := chart.OutputLimit(representation, side, actual)
	if c := copyReader.Comparison; c != nil {
		b, companions, err := statechart.ComparisonFiles(c)
		if err != nil {
			return nil, nil, err
		}
		files = companions
		files["reader-comparison.json"] = b
		copyReader.ComparisonRef = statechart.NewEvidenceReference("reader-comparison.json", b, c.Fingerprint, l)
		copyReader.Comparison = nil
		data, err = encode(&copyResult)
		if err != nil {
			return nil, nil, err
		}
	}
	// Only a drawn candidate moves into reader-candidate.json, since its
	// reference binds that chart. A limited candidate keeps its own outcome
	// and sources inline; its repeated baseline copies still move below.
	if len(data) > statechart.MaxEvidenceBytes && copyReader.Chart != nil {
		if err := candidateEvidence(&copyProposal, l, files); err != nil {
			return nil, nil, err
		}
		data, err = encode(&copyResult)
	}
	if err == nil && len(data) > statechart.MaxEvidenceBytes && r.Proposal.Reader.Comparison != nil {
		if err := copyResult.boundBaselineSources(&r.Proposal.Reader.Comparison.Baseline, l, nil, files); err != nil {
			return nil, nil, err
		}
		data, err = encode(&copyResult)
	}
	if err == nil && len(data) > statechart.MaxEvidenceBytes {
		err = fmt.Errorf("complete saved volume record exceeds its byte budget")
		if copyReader.Chart == nil {
			err = fmt.Errorf("complete saved limitation record exceeds its byte budget")
		}
	}
	return data, files, err
}

func proposalHasEvidenceReferences(p *Proposal) bool {
	if p == nil {
		return false
	}
	if r := p.Reader; r != nil && (r.CandidateRef != nil || r.ComparisonRef != nil || r.CandidateSources) {
		return true
	}
	for _, f := range p.Forks {
		for _, o := range f.Options {
			if v := o.Preview; v != nil && (v.EvidenceRef != nil || v.ReceiptFromModel || proposalHasEvidenceReferences(v.Model)) {
				return true
			}
		}
	}
	return false
}

func (r *Result) ResolveEvidence(read func(string) ([]byte, error)) error {
	if r.BaselineSources == nil && !proposalHasEvidenceReferences(r.Proposal) {
		return nil
	}
	if r.Proposal != nil && len(r.Proposal.Forks) > 0 {
		for i := range r.Proposal.Forks {
			f := &r.Proposal.Forks[i]
			for j := range f.Options {
				o := &f.Options[j]
				v := o.Preview
				if v == nil {
					continue
				}
				if !previewID.MatchString(f.ID) || !previewID.MatchString(o.ID) {
					return fmt.Errorf("invalid preview companion directory")
				}
				readOption := func(name string) ([]byte, error) {
					if read == nil || !EvidenceCompanion(name) || strings.Contains(name, "/") {
						return nil, fmt.Errorf("preview requires its fixed evidence companions")
					}
					return read("previews/" + f.ID + "/" + o.ID + "/" + name)
				}
				if err := resolvePreviewEvidence(v, readOption); err != nil {
					return err
				}
				local := &Result{Proposal: v.Model, Report: v.Receipt}
				if err := local.ResolveEvidence(readOption); err != nil {
					return err
				}
				if v.ReceiptFromModel {
					if v.Receipt == nil || v.Receipt.Reader == nil || v.Model == nil || v.Model.Reader == nil ||
						v.Receipt.Reader.Chart != nil || v.Receipt.Reader.Limitation != nil {
						return fmt.Errorf("preview receipt conflicts with candidate evidence")
					}
					v.Receipt.Reader.Chart, v.Receipt.Reader.Limitation = v.Model.Reader.Chart, v.Model.Reader.Limitation
					v.ReceiptFromModel = false
				}
			}
		}
		if err := r.resolveBaselineSources(read); err != nil {
			return err
		}
		return r.Proposal.ValidatePreviews()
	}
	if r.Proposal == nil || r.Proposal.Reader == nil {
		return fmt.Errorf("saved references require a complete modeled proposal")
	}
	p := r.Proposal
	reader := p.Reader
	if reader.CandidateSources && reader.CandidateRef == nil {
		return fmt.Errorf("candidate sources require their fixed evidence companion")
	}
	if read == nil {
		return fmt.Errorf("reader requires its fixed evidence companions")
	}
	if ref := reader.CandidateRef; ref != nil {
		if reader.Chart != nil || reader.Limitation != nil {
			return fmt.Errorf("candidate reference conflicts with inline evidence")
		}
		data, err := read("reader-candidate.json")
		if err != nil {
			return err
		}
		s, err := ref.Snapshot("reader-candidate.json", data)
		if err != nil {
			return err
		}
		canonical, _ := json.Marshal(s)
		if s.Chart == nil {
			return fmt.Errorf("candidate companion identity differs")
		}
		if err := ref.ValidateChart(s.Chart); err != nil {
			return err
		}
		if reader.CandidateSources {
			if p.ModuleText != "" || p.Includes != nil || p.View != "" {
				return fmt.Errorf("candidate reference conflicts with inline sources")
			}
			p.ModuleText, p.Includes, p.View = s.Model, s.Includes, s.View
			reader.CandidateSources = false
		}
		want := p.comparisonSnapshot()
		want.Chart = s.Chart
		a, _ := json.Marshal(want)
		if !bytes.Equal(a, canonical) {
			return fmt.Errorf("candidate companion sources differ")
		}
		reader.Chart, reader.CandidateRef = s.Chart, nil
	}
	if ref := reader.ComparisonRef; ref != nil {
		if reader.Comparison != nil {
			return fmt.Errorf("comparison reference conflicts with inline evidence")
		}
		data, err := read("reader-comparison.json")
		if err != nil {
			return err
		}
		if err := ref.Validate("reader-comparison.json", data); err != nil {
			return err
		}
		c, err := statechart.ResolveComparison(data, read)
		if err != nil {
			return err
		}
		if c.Fingerprint != ref.Fingerprint {
			return fmt.Errorf("comparison companion fingerprint differs")
		}
		chart := reader.Chart
		if chart == nil {
			chart = c.Candidate.Chart
			if chart == nil {
				chart = c.Baseline.Chart
			}
		}
		if err := ref.ValidateChart(chart); err != nil {
			return err
		}
		reader.Comparison, reader.ComparisonRef = c, nil
	}
	if err := r.resolveBaselineSources(read); err != nil {
		return err
	}
	return p.ValidateReader()
}

// A project's reader keeps its original comparison reference format. Bounded
// saved records use EvidenceReference and reconstruct the in-memory reader.
type comparisonReference struct {
	Version     int    `json:"version"`
	File        string `json:"file"`
	Fingerprint string `json:"fingerprint"`
}

func (p *Proposal) computeReader(ctx context.Context, dir string, tc toolchain.Toolchain, states int64) error {
	p.Reader = nil
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	chart, limitation, err := statechart.Read(ctx, runner, dir, p.Module,
		project.Lock{Bounds: p.Bounds, Statements: p.Statements}, []byte(p.View), states)
	if err != nil {
		return err
	}
	reader := &Reader{Version: 1, Chart: chart, Limitation: limitation}
	if chart != nil {
		l, err := chart.EvidenceLimit(p.Name, "candidate")
		if err != nil {
			return err
		}
		if l != nil {
			reader.Chart, reader.Limitation = nil, l
		}
	}
	p.Reader = reader
	reader.Fingerprint, err = p.readerFingerprint()
	if err != nil {
		p.Reader = nil
	}
	return err
}

func (p *Proposal) readerFingerprint() (string, error) {
	// Factory metadata (target, language, agent, directory) can change after
	// checking. Only the inputs to the computation and its output belong here.
	b, err := json.Marshal(struct {
		Version    int                    `json:"version"`
		Module     string                 `json:"module"`
		Model      string                 `json:"model"`
		Includes   map[string]string      `json:"includes,omitempty"`
		View       string                 `json:"view"`
		Bounds     map[string]string      `json:"bounds"`
		Statements []project.Statement    `json:"statements"`
		Chart      *statechart.Result     `json:"chart,omitempty"`
		Limitation *ReaderLimitation      `json:"limitation,omitempty"`
		Comparison *statechart.Comparison `json:"comparison,omitempty"`
	}{p.Reader.Version, p.Module, p.ModuleText, p.Includes, p.View, p.Bounds, p.Statements, p.Reader.Chart, p.Reader.Limitation, p.Reader.Comparison})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b)), nil
}

// ValidateReader refuses missing or stale evidence before new publication.
// Older proposals without a reader remain readable and writable as before.
func (p *Proposal) ValidateReader() error {
	r := p.Reader
	if r == nil || p.View == "" {
		return fmt.Errorf("reader evidence is missing: check the modeled draft with view.json before publishing")
	}
	if r.Version != 1 || (r.Chart == nil) == (r.Limitation == nil) {
		return fmt.Errorf("reader evidence must contain exactly one version 1 chart or limitation; check the draft again")
	}
	if r.ComparisonRef != nil || r.CandidateRef != nil || r.CandidateSources {
		return fmt.Errorf("reader requires its fixed evidence companions")
	}
	if r.Chart != nil && r.Chart.Version != 1 {
		return fmt.Errorf("reader chart version is unsupported; check the draft again")
	}
	if l := r.Limitation; l != nil && l.ValidateSource(p.Module, project.Lock{Bounds: p.Bounds, Statements: p.Statements}, 0) != nil {
		return fmt.Errorf("reader size limitation is invalid; check the draft again")
	}
	want, err := p.readerFingerprint()
	if err != nil {
		return err
	}
	if r.Fingerprint != want {
		return fmt.Errorf("reader evidence is stale: model, includes, view, bounds, statements, tools or artifact changed; check the draft again")
	}
	var comparison *statechart.Comparison
	if c := r.Comparison; c != nil {
		// Recompute against the current candidate once. Comparing the whole
		// record checks every retained field as well as the candidate binding.
		comparison, err = statechart.Compare(c.Baseline, p.comparisonSnapshot())
		if err != nil {
			return err
		}
		got, err := json.Marshal(c)
		if err != nil {
			return err
		}
		want, err := json.Marshal(comparison)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("comparison candidate or retained evidence is stale; check against the captured baseline again")
		}
	}
	if p.Target != nil && p.Target.Baseline != nil {
		if err := validateComparisonBaseline(comparison, currentSnapshot(p.Target.Baseline)); err != nil {
			return err
		}
	}
	if p.Baseline != nil {
		if err := validateComparisonBaseline(comparison, p.Baseline); err != nil {
			return err
		}
	}
	return validateIncludes(p.Includes, p.Module)
}

// ReaderFeedback accompanies the model gate's feedback on a passing draft.
func (p *Proposal) ReaderFeedback() string {
	if p.Reader == nil {
		return ""
	}
	var b strings.Builder
	if l := p.Reader.Limitation; l != nil {
		b.WriteString(l.Description())
	} else {
		c := p.Reader.Chart.Report.Counts
		fmt.Fprintf(&b, "Reader checked: %d TLC states, %d TLC steps, %d generated states; %d chart states, %d visible arrows, %d hidden steps.",
			c["tlc_states"], c["tlc_steps"], c["tlc_generated_states"], c["chart_states"], c["chart_arrows"], c["hidden_steps"])
	}
	b.WriteString(" Checked bounds:")
	for _, name := range slices.Sorted(maps.Keys(p.Bounds)) {
		fmt.Fprintf(&b, " %s = %s;", name, p.Bounds[name])
	}
	if c := p.Reader.Comparison; c != nil {
		fmt.Fprintf(&b, " Comparison checked against %s. %s", c.Baseline.Name, strings.Join(c.Qualifications(), " "))
	}
	return b.String()
}

func validateIncludes(includes map[string]string, module string) error {
	for name := range includes {
		if !tla.ValidIncludeName(name, module) {
			return fmt.Errorf("reader include %q must be a sibling TLA+ file distinct from the model", name)
		}
	}
	return nil
}

func readIncludes(dir, module string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	includes := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".tla") || name == module+".tla" {
			continue
		}
		b, err := regular.ReadFile(dir, name)
		if err != nil {
			return nil, err
		}
		includes[name] = string(b)
	}
	return includes, validateIncludes(includes, module)
}

func writeIncludes(dir string, includes map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(includes)) {
		if err := writeFile(filepath.Join(dir, name), includes[name]); err != nil {
			return err
		}
	}
	return nil
}

// A checked amendment replaces the include set, including removals. Leaving
// an old sibling behind would write inputs different from the saved snapshot.
func removeOldIncludes(dir, module string, includes map[string]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".tla") || name == module+".tla" {
			continue
		}
		if _, keep := includes[name]; !keep {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeReader(dir, view string, reader *Reader, reference bool) error {
	for _, name := range []string{"reader.json", "comparison.json", "reader-comparison.json", "reader-baseline.json", "reader-candidate.json"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if view != "" {
		if err := writeFile(filepath.Join(dir, "view.json"), view); err != nil {
			return err
		}
	}
	if reader != nil {
		if reader.Comparison != nil {
			b, companions, err := statechart.ComparisonFiles(reader.Comparison)
			if err != nil {
				return err
			}
			if err := writeFile(filepath.Join(dir, "comparison.json"), string(b)); err != nil {
				return err
			}
			for name, data := range companions {
				if err := writeFile(filepath.Join(dir, name), string(data)); err != nil {
					return err
				}
			}
		}
		var record any = reader
		if reference && reader.Comparison != nil {
			copyReader := *reader
			copyReader.Comparison = nil
			record = struct {
				Reader
				ComparisonRef comparisonReference `json:"comparison_ref"`
			}{copyReader, comparisonReference{Version: 1, File: "comparison.json", Fingerprint: reader.Comparison.Fingerprint}}
		}
		b, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return err
		}
		if len(b)+1 > statechart.MaxEvidenceBytes && reader.Comparison != nil && reader.Chart != nil {
			data, files, err := statechart.ComparisonFiles(reader.Comparison)
			if err != nil {
				return err
			}
			files["reader-comparison.json"] = data
			copy := *reader
			copy.Comparison = nil
			copy.ComparisonRef = statechart.NewEvidenceReference("reader-comparison.json", data, reader.Comparison.Fingerprint, reader.Chart.OutputLimit("indented_json", "proposal", len(b)+1))
			b, err = json.MarshalIndent(&copy, "", "  ")
			if err != nil {
				return err
			}
			for name, data := range files {
				if err := writeFile(filepath.Join(dir, name), string(data)); err != nil {
					return err
				}
			}
		}
		if len(b)+1 > statechart.MaxEvidenceBytes {
			return fmt.Errorf("reader record exceeds complete evidence budget")
		}
		return writeFile(filepath.Join(dir, "reader.json"), string(b)+"\n")
	}
	return nil
}

// ReadReader loads a current project's optional saved explanation and its
// sibling includes. Legacy projects need no invented view or evidence.
func (c *Current) ReadReader(dir string) error {
	c.Reader = nil
	var err error
	c.Includes, err = readIncludes(filepath.Join(dir, filepath.Dir(c.Manifest.Module)), c.ModuleName())
	if err != nil {
		return err
	}
	b, err := regular.ReadFile(dir, ".invariant/view.json")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	c.View = string(b)
	b, err = regular.ReadFile(dir, ".invariant/reader.json")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved struct {
		*Reader
		ComparisonRef json.RawMessage `json:"comparison_ref"`
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		return fmt.Errorf("reader comparison record: %w", err)
	}
	var ref comparisonReference
	if len(saved.ComparisonRef) != 0 {
		if err := json.Unmarshal(saved.ComparisonRef, &ref); err != nil {
			return fmt.Errorf("comparison reference: %w", err)
		}
		if ref.Version != 1 || ref.File != "comparison.json" || ref.Fingerprint == "" || saved.Reader == nil || saved.Version != 1 {
			return fmt.Errorf("comparison reference requires a version 1 reader and {version:1, file:comparison.json, fingerprint}")
		}
	}
	// A sibling alone cannot add a comparison to an ordinary or absent reader.
	if saved.Reader == nil || saved.Comparison == nil && len(saved.ComparisonRef) == 0 {
		c.Reader = saved.Reader
		return nil
	}
	if saved.Comparison != nil {
		if err := saved.Comparison.Validate(); err != nil {
			return err
		}
	}
	data, err := regular.ReadFile(dir, ".invariant/comparison.json")
	if os.IsNotExist(err) && len(saved.ComparisonRef) == 0 {
		c.Reader = saved.Reader
		return nil
	}
	if err != nil {
		return fmt.Errorf("comparison evidence: %w", err)
	}
	comparison, err := statechart.ResolveComparison(data, func(name string) ([]byte, error) { return regular.ReadFile(dir, ".invariant/"+name) })
	if err != nil {
		return fmt.Errorf("saved comparison: %w", err)
	}
	if len(saved.ComparisonRef) != 0 && ref.Fingerprint != comparison.Fingerprint {
		return fmt.Errorf("comparison reference fingerprint differs from comparison.json")
	}
	if saved.Comparison != nil {
		inline, err := json.Marshal(saved.Comparison)
		if err != nil {
			return err
		}
		want, err := json.Marshal(comparison)
		if err != nil {
			return err
		}
		if !bytes.Equal(inline, want) {
			return fmt.Errorf("inline comparison differs from comparison.json")
		}
	}
	saved.Comparison = comparison
	c.Reader = saved.Reader
	return nil
}
