package formalize

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

func TestProjection944SavedRecordByteCeilings(t *testing.T) {
	for _, name := range []string{"ordinary", "preview", "compact"} {
		t.Run(name, func(t *testing.T) {
			fixtureName := name
			if name == "compact" {
				fixtureName = "ordinary"
			}
			data, err := os.ReadFile("testdata/reader944/" + fixtureName + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Proposal *Proposal      `json:"proposal"`
				Receipt  *verify.Report `json:"receipt"`
				Preview  *Proposal      `json:"preview"`
			}
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			result := &Result{Proposal: fixture.Proposal, Report: fixture.Receipt}
			var fork Fork
			var option Option
			if name == "preview" {
				fork = fixture.Preview.Forks[0]
				option = fork.Options[0]
				result.Proposal, result.Report = option.Preview.Model, option.Preview.Receipt
			}
			module := result.Proposal.ModuleText
			// Comments leave the pinned behavior intact but count in full where
			// the original source is emitted in an ordinary or preview wrapper.
			setSource := func(padding int) {
				p, r := result.Proposal, result.Report
				p.ModuleText = module + "\n\\* " + strings.Repeat("x", padding) + "\n"
				r.Reader.Inputs.Model = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(p.ModuleText)))
				r.Reader.Fingerprint = receiptReaderFingerprint(r.Reader, project.Lock{Bounds: p.Bounds, Statements: p.Statements})
				p.Reader.Fingerprint, err = p.readerFingerprint()
				if err != nil {
					t.Fatal(err)
				}
				if name == "preview" {
					option.Preview.Fingerprint, err = fork.PreviewFingerprint(option)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			encode := func(r *Result) ([]byte, error) {
				if name == "compact" {
					return json.Marshal(r)
				}
				var value any = r
				if name == "preview" {
					copy := *option.Preview
					copy.Model, copy.Receipt = r.Proposal, r.Report
					value = &copy
				}
				b, err := json.MarshalIndent(value, "", "  ")
				return append(b, '\n'), err
			}
			setSource(0)
			base, err := encode(result)
			if err != nil {
				t.Fatal(err)
			}
			padding := statechart.MaxEvidenceBytes - len(base)
			for _, extra := range []int{0, 1} {
				setSource(padding + extra)
				original, err := encode(result)
				if err != nil || len(original) != statechart.MaxEvidenceBytes+extra {
					t.Fatal("incorrect complete boundary fixture", len(original), err)
				}
				file := "formalization.json"
				if name == "preview" {
					file = "preview.json"
				}
				saved, files, err := result.EvidenceFiles(file, encode)
				if err != nil || len(saved) > statechart.MaxEvidenceBytes {
					t.Fatal("complete saved record did not fit", err)
				}
				if extra == 0 {
					if len(files) != 0 || !bytes.Equal(original, saved) {
						t.Fatal("exact ceiling changed fitting evidence")
					}
					continue
				}
				var decoded Result
				if name == "preview" {
					var preview Preview
					if err := json.Unmarshal(saved, &preview); err != nil {
						t.Fatal(err)
					}
					decoded.Proposal, decoded.Report = preview.Model, preview.Receipt
				} else if err := json.Unmarshal(saved, &decoded); err != nil {
					t.Fatal(err)
				}
				ref := decoded.Proposal.Reader.CandidateRef
				representation := file
				if name == "compact" {
					representation = "compact_json"
				}
				if ref == nil || ref.Limitation.Actual != statechart.MaxEvidenceBytes+1 || ref.Limitation.Representation != representation || len(files) != 1 {
					t.Fatal("missing exact complete-record overflow", ref)
				}
				if err := decoded.ResolveEvidence(func(name string) ([]byte, error) { return files[name], nil }); err != nil {
					t.Fatal(err)
				}
				restored, err := encode(&decoded)
				if err != nil || !bytes.Equal(original, restored) {
					t.Fatal("bounded transport changed original sources or fingerprints", err)
				}
				if name == "preview" {
					copy := *option.Preview
					copy.Model, copy.Receipt = decoded.Proposal, decoded.Report
					checked := option
					checked.Preview = &copy
					if err := fork.ValidatePreview(checked); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestProjection944WholeQuestionByteCeilings(t *testing.T) {
	data, err := os.ReadFile("testdata/reader944/preview.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct { Preview *Proposal `json:"preview"` }
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	result := &Result{Proposal: fixture.Preview, CheckContext: &CheckContext{}}
	f := &result.Proposal.Forks[0]
	models := []string{f.Options[0].Preview.Model.ModuleText, f.Options[1].Preview.Model.ModuleText}
	setSource := func(j, padding int) {
		o := &f.Options[j]
		p, r := o.Preview.Model, o.Preview.Receipt
		p.ModuleText = models[j] + "\n\\* " + strings.Repeat("x", padding) + "\n"
		r.Reader.Inputs.Model = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(p.ModuleText)))
		r.Reader.Fingerprint = receiptReaderFingerprint(r.Reader, project.Lock{Bounds: p.Bounds, Statements: p.Statements})
		p.Reader.Fingerprint, err = p.readerFingerprint()
		if err != nil { t.Fatal(err) }
		o.Preview.Fingerprint, err = f.PreviewFingerprint(*o)
		if err != nil { t.Fatal(err) }
	}
	encode := func(r *Result) ([]byte, error) {
		b, e := json.MarshalIndent(r, "", "  ")
		return append(b, '\n'), e
	}
	setSource(0, 0)
	setSource(1, 0)
	base, err := encode(result)
	if err != nil { t.Fatal(err) }
	padding := statechart.MaxEvidenceBytes - len(base)
	setSource(0, padding/2)
	for _, extra := range []int{0, 1} {
		setSource(1, padding-padding/2+extra)
		original, err := encode(result)
		if err != nil || len(original) != statechart.MaxEvidenceBytes+extra {
			t.Fatal("wrong complete question boundary", len(original), err)
		}
		for _, o := range f.Options {
			b, err := json.MarshalIndent(o.Preview, "", "  ")
			if err != nil || len(b)+1 > statechart.MaxEvidenceBytes {
				t.Fatal("individual option must fit", err)
			}
		}
		saved, files, err := result.EvidenceFiles("formalization.json", encode)
		if err != nil || len(saved) > statechart.MaxEvidenceBytes {
			t.Fatal("complete question could not be saved", err)
		}
		if extra == 0 {
			if len(files) != 0 || !bytes.Equal(saved, original) {
				t.Fatal("exact ceiling changed the original full proposal")
			}
			continue
		}
		if len(files) != 2 { t.Fatal("each option needs its complete fixed companion") }
		var decoded Result
		if err := json.Unmarshal(saved, &decoded); err != nil { t.Fatal(err) }
		for _, o := range decoded.Proposal.Forks[0].Options {
			ref := o.Preview.Model.Reader.CandidateRef
			if ref == nil || ref.Limitation.Actual != statechart.MaxEvidenceBytes+1 || ref.Limitation.Representation != "formalization.json" || ref.Limitation.Side != "preview" {
				t.Fatal("lost exact whole-proposal overflow", ref)
			}
		}
		if err := decoded.ResolveEvidence(func(name string) ([]byte, error) { return files[name], nil }); err != nil {
			t.Fatal(err)
		}
		restored, err := encode(&decoded)
		if err != nil || !bytes.Equal(original, restored) {
			t.Fatal("question recovery changed sources, option or receipt identities", err)
		}
		after, _ := encode(result)
		if !bytes.Equal(original, after) { t.Fatal("saving mutated the complete question") }
	}
}
