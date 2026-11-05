package verify_test

import (
	"path/filepath"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

func TestReaderIdentityBindsStatementsBoundsAndTools(t *testing.T) {
	tc, _ := reader615Tools(t)
	dir := reader615Project(t, 2)
	initial := reader615RecordOf(t, reader615Run(t, dir, tc, true))
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	p.Lock.Statements[1].Says += " Exact additional words."
	reader615Write(t, filepath.Join(dir, ".invariant/ratified.lock"), string(reader615JSON(t, p.Lock)))
	wording := reader615RecordOf(t, reader615Run(t, dir, tc, true))
	if wording.Fingerprint == initial.Fingerprint || wording.Chart.Report.Rules[1].Says != p.Lock.Statements[1].Says {
		t.Fatal("reader identity did not bind the locked wording")
	}
	p.Lock.Bounds["Limit"] = "3"
	reader615Write(t, filepath.Join(dir, ".invariant/ratified.lock"), string(reader615JSON(t, p.Lock)))
	bounds := reader615RecordOf(t, reader615Run(t, dir, tc, true))
	reader615Chart(t, bounds, 4)
	if bounds.Fingerprint == wording.Fingerprint {
		t.Fatal("reader identity did not bind changed bounds")
	}
	reader615Write(t, tc.TLCJar, "different fixture TLC jar")
	tools := reader615RecordOf(t, reader615Run(t, dir, tc, true))
	if tools.Fingerprint == bounds.Fingerprint || tools.Chart.Report.Source.Tool.JarSHA256 == bounds.Chart.Report.Source.Tool.JarSHA256 {
		t.Fatal("reader identity did not bind the tool actually used")
	}
}
