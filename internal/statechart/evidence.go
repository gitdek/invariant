package statechart

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// EvidenceReference names only a fixed sibling. Its digest binds the exact
// saved bytes; the fingerprint binds the independently checked contents.
type EvidenceReference struct {
	Version     int         `json:"version"`
	File        string      `json:"file"`
	SHA256      string      `json:"sha256"`
	Fingerprint string      `json:"fingerprint"`
	Limitation  *Limitation `json:"limitation"`
}

func NewEvidenceReference(file string, data []byte, fingerprint string, l *Limitation) *EvidenceReference {
	return &EvidenceReference{Version: 1, File: file, SHA256: comparisonHash(data), Fingerprint: fingerprint, Limitation: l}
}

func (r *EvidenceReference) Validate(file string, data []byte) error {
	if r == nil || r.Version != 1 || r.File != file || (file != "reader-comparison.json" && file != "reader-baseline.json" && file != "reader-candidate.json") ||
		!hash.MatchString(r.Fingerprint) || r.Limitation == nil || r.Limitation.Validate() != nil || r.Limitation.Dimension != "output_bytes" ||
		len(data) > MaxEvidenceBytes || r.SHA256 != comparisonHash(data) {
		return fmt.Errorf("missing, stale or invalid %s evidence companion", file)
	}
	return nil
}

func (r *EvidenceReference) ValidateChart(c *Result) error {
	if r == nil || r.Limitation == nil || r.Limitation.Validate() != nil || c == nil {
		return fmt.Errorf("volume reference has no checked chart")
	}
	l := r.Limitation
	if l.Dimension != "output_bytes" || !comparisonEqual(l.Source, c.Report.Source) ||
		int64(l.ConcreteStates) != c.Report.Counts["tlc_states"] || l.ProjectedStates == nil || *l.ProjectedStates != len(c.States) {
		return fmt.Errorf("volume reference source or counts differ from the checked chart")
	}
	return nil
}

// ComparisonFiles returns the original record when it fits. Only volume
// records may split their two independently fitting snapshots into companions.
func ComparisonFiles(c *Comparison) ([]byte, map[string][]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, nil, err
	}
	files := map[string][]byte{}
	encode := func(v any) ([]byte, error) { b, e := json.MarshalIndent(v, "", "  "); return append(b, '\n'), e }
	b, err := encode(c)
	if err != nil || len(b) <= MaxEvidenceBytes {
		return b, files, err
	}
	if c.Volume() == nil {
		return nil, nil, fmt.Errorf("comparison has no volume outcome")
	}
	copy := *c
	unavailable := *c.Unavailable
	copy.Unavailable = &unavailable
	for _, side := range []struct {
		name     string
		snapshot *ComparisonSnapshot
		ref      **EvidenceReference
	}{
		{"reader-baseline.json", &copy.Baseline, &copy.Unavailable.BaselineRef},
		{"reader-candidate.json", &copy.Candidate, &copy.Unavailable.CandidateRef},
	} {
		data, err := encode(side.snapshot)
		if err != nil {
			return nil, nil, err
		}
		if len(data) > MaxEvidenceBytes {
			return nil, nil, fmt.Errorf("independent %s exceeds evidence capacity", side.name)
		}
		files[side.name] = data
		*side.ref = NewEvidenceReference(side.name, data, comparisonHash(comparisonJSON(side.snapshot)), c.Volume())
		*side.snapshot = ComparisonSnapshot{}
	}
	b, err = encode(&copy)
	if len(b) > MaxEvidenceBytes {
		return nil, nil, fmt.Errorf("comparison volume reference exceeds evidence capacity")
	}
	return b, files, err
}

func decodeEvidence(data []byte, v any) error {
	if len(data) > MaxEvidenceBytes {
		return fmt.Errorf("evidence exceeds complete entry limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	var err error
	if s, ok := v.(*ComparisonSnapshot); ok {
		type snapshot ComparisonSnapshot
		var decoded snapshot
		err = d.Decode(&decoded)
		*s = ComparisonSnapshot(decoded)
	} else {
		err = d.Decode(v)
	}
	if err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("extra evidence JSON")
	}
	return nil
}

func (r *EvidenceReference) Snapshot(file string, data []byte) (ComparisonSnapshot, error) {
	var s ComparisonSnapshot
	if err := r.Validate(file, data); err != nil {
		return s, err
	}
	if err := decodeEvidence(data, &s); err != nil {
		return s, err
	}
	if comparisonHash(comparisonJSON(s)) != r.Fingerprint {
		return s, fmt.Errorf("comparison snapshot fingerprint differs")
	}
	return s, nil
}

// ResolveComparison accepts no paths from the saved record. A caller provides
// regular-file reads of the three fixed siblings; reference chains are absent
// from snapshots and are rejected by their exact digest and canonical shape.
func ResolveComparison(data []byte, read func(string) ([]byte, error)) (*Comparison, error) {
	var c Comparison
	if err := decodeEvidence(data, &c); err != nil {
		return nil, err
	}
	if !c.HasCompanions() {
		return c.Validated()
	}
	for _, side := range []struct {
		name     string
		snapshot *ComparisonSnapshot
		ref      **EvidenceReference
	}{
		{"reader-baseline.json", &c.Baseline, &c.Unavailable.BaselineRef},
		{"reader-candidate.json", &c.Candidate, &c.Unavailable.CandidateRef},
	} {
		ref := *side.ref
		if ref == nil {
			continue
		}
		if read == nil || !comparisonEqual(side.snapshot, ComparisonSnapshot{}) || c.Volume() == nil || !comparisonEqual(ref.Limitation, c.Volume()) {
			return nil, fmt.Errorf("invalid comparison companion reference")
		}
		b, err := read(side.name)
		if err != nil {
			return nil, err
		}
		snapshot, err := ref.Snapshot(side.name, b)
		if err != nil {
			return nil, err
		}
		*side.snapshot = snapshot
		*side.ref = nil
	}
	return c.Validated()
}
