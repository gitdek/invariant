package dashboard

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// Complete provenance keeps display tests about checked conformance rather
// than cached flags alone, including when a report is read from old JSON.
func coverageComponentFixture802() (project.ComponentInventory, verify.ComponentConformanceResult) {
	file := func(path string) project.ComponentFile {
		return project.ComponentFile{Path: path, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(path)))}
	}
	component := func(symbol, source string) project.Component {
		return project.Component{ID: "go:example.test/coverage#" + symbol + "@" + source, Language: "go", Package: "example.test/coverage",
			Symbol: symbol, Source: source, SourceSHA256: file(source).SHA256}
	}
	target, driver := component("Step", "core/step.go"), component("Drive", "drivers/drive.go")
	inputs := []project.ComponentFile{file(driver.Source), file("model/Counter.tla"), file(".invariant/ratified.lock")}
	call := project.ResolvedComponentInvocation{ID: "sampled", Kind: "conformance", Check: driver.ID, Command: "driver", WorkingDir: ".", Inputs: inputs}
	exhaustive := call
	exhaustive.ID = "exhaustive"
	inv := project.ComponentInventory{Mode: "explicit", Scope: []string{"core"}, Files: []project.ComponentFile{file(target.Source)}, Components: []project.Component{target},
		Provenance: &project.ComponentProvenance{Declarations: []project.Component{driver}, Files: inputs},
		Checks:     []project.ResolvedComponentCheck{{Target: target.ID, Conformance: []string{driver.ID}, Invocations: []project.ResolvedComponentInvocation{call, exhaustive}}}}
	result := verify.ComponentConformanceResult{Target: target.ID, Check: driver.ID, Invocation: &call,
		Runner: &verify.ComponentProofTool{Name: "TLC", Version: "pinned"}, Inputs: []project.Component{target, driver},
		FileInputs: append([]project.ComponentFile{file(target.Source)}, inputs...),
		Model:      &verify.ComponentConformanceModel{Module: "model/Counter.tla", Inputs: []project.ComponentFile{file("model/Counter.tla")}},
		Bounds:     &verify.ComponentConformanceBounds{Sources: []project.ComponentFile{file(".invariant/ratified.lock")}},
		Mode:       "sampled", Outcome: "passed", Checked: []string{driver.ID}, Runs: 1, Steps: 7, States: 3, Transitions: 2, ModelStates: 5}
	return inv, result
}

func TestComponentCoverageRequalifiesDashboard802(t *testing.T) {
	inv, result := coverageComponentFixture802()
	r := verify.Report{Passed: true}
	if err := r.AttributeComponentConformance(&inv, []verify.ComponentConformanceResult{result}); err != nil {
		t.Fatal(err)
	}
	r.Components.Conformance[0].ExhaustiveStates = true
	r.Components.Conformance[0].Status = "tested in every state"
	before := verify.Fingerprint(r)
	p := projectOf(projectSource{}, &r)
	if p.Assurance != "tested (sampled)" || len(p.Coverage) != 1 || p.Coverage[0].Fraction != "3/5" {
		t.Fatalf("stale flags won: %+v", p)
	}
	page := makeReader(readerEvidence{Report: &r})
	if visible := dashboardText802(page.html); !strings.Contains(visible, "3/5") || !strings.Contains(visible, "60%") || !strings.Contains(visible, "tested (sampled)") {
		t.Fatalf("missing component share: %s", visible)
	}
	if verify.Fingerprint(r) != before {
		t.Fatal("display changed component evidence")
	}
	r.Components.Conformance[0].Result.Invocation = nil
	p = projectOf(projectSource{}, &r)
	if p.Assurance != "unchecked" || p.Coverage[0].Checked || p.Coverage[0].Status != "unknown" || p.Coverage[0].Percent != "" {
		t.Fatalf("missing provenance earned coverage: %+v", p)
	}
}

func TestCachedCoverageComesFromSavedReport802(t *testing.T) {
	s := &Server{Cache: t.TempDir(), Repos: []*Repo{{Name: "owner/coverage"}}}
	r := &verify.Report{Passed: true, Conformance: &conformance.Result{Passed: true, States: 2, ModelStates: 5}}
	r.Fingerprint = verify.Fingerprint(*r)
	e := readerEvidence{Format: readerFormat, Repo: "owner/coverage", Dir: "examples/sample", Report: r}
	p := projectOf(projectSource{dir: e.Dir}, r)
	p.Repo, p.Reader = e.Repo, s.keepReader(e)
	p.Assurance = "tested in every state"
	p.Coverage = []verify.Coverage{{Source: "conformance", Checked: true, Status: "full", Fraction: "5/5", Percent: "100%"}}
	snap := Snapshot{Repo: e.Repo, Projects: []Project{p}}
	if s.restoreReaders(&snap) {
		t.Fatal("unchanged evidence rejected during cache restoration")
	}
	got := snap.Projects[0]
	if got.Assurance != "tested (sampled)" || len(got.Coverage) != 1 || got.Coverage[0].Fraction != "2/5" || got.Coverage[0].Percent != "40%" || got.Fingerprint != r.Fingerprint {
		t.Fatalf("cached claims overrode checked evidence: %+v", got)
	}
}
