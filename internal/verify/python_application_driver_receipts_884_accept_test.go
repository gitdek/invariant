package verify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonApplicationPerCheckDriverReceipts884(t *testing.T) {
	for _, mode := range []string{"success", "execution", "no first bounds", "second mismatch"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := verify.PerCheckDriverReport884(t, mode)
			if r.Fingerprint == "" || verify.Fingerprint(*r) != r.Fingerprint {
				t.Fatal("gate report lacks its valid portable fingerprint")
			}
			dir := t.TempDir()
			if err := receipt.Write(dir, r); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			var decoded verify.Report
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if verify.Fingerprint(decoded) != r.Fingerprint || decoded.Passed != r.Passed || !reflect.DeepEqual(decoded.Application.Larger, r.Application.Larger) || !reflect.DeepEqual(decoded.Coverage(), r.Coverage()) {
				t.Fatal("receipt JSON lost per-driver provenance, verdict or base coverage")
			}
			md := receipt.Markdown(&decoded)
			saved, err := os.ReadFile(filepath.Join(dir, "receipt.md"))
			if err != nil || md != string(saved) {
				t.Fatalf("Markdown changed across receipt JSON round trip: %v", err)
			}
			for _, l := range decoded.Application.Larger {
				// Markdown's literal rendering keeps these slash paths and
				// full digests inside its per-invocation evidence details.
				for _, want := range []string{l.Key.ID, l.Original.Path, l.Original.SHA256} {
					if want == "" || !strings.Contains(md, want) {
						t.Fatalf("receipt hides original driver identity %q", want)
					}
				}
				if l.Execution != nil {
					if !strings.Contains(md, l.Driver.Path) || !strings.Contains(md, l.Driver.SHA256) || !strings.Contains(md, "One size larger") {
						t.Fatal("receipt hides the actual rewritten driver")
					}
				} else if len(l.Skipped) == 0 || !strings.Contains(md, "skipped") || l.Result.Required || l.Result.Passed {
					t.Fatal("skipped invocation gained evidence or lost its explanation")
				}
			}
			if mode == "execution" && (!strings.Contains(md, "first base failed 884") || !strings.Contains(md, "**What failed**")) {
				t.Fatal("receipt hides the original failure behind a skipped larger run")
			}
			if mode == "second mismatch" && (decoded.Passed || decoded.Larger == nil || decoded.Larger.Passed || !decoded.Larger.Required || !strings.Contains(md, "**What failed**")) {
				t.Fatal("successful first driver hid the second driver's larger mismatch")
			}
			for i := range decoded.Application.Larger {
				changes := []struct {
					name  string
					apply func(*verify.PythonApplicationLarger)
				}{
					{"target", func(l *verify.PythonApplicationLarger) { l.Key.Target += "-changed" }},
					{"check", func(l *verify.PythonApplicationLarger) { l.Key.Check += "-changed" }},
					{"invocation", func(l *verify.PythonApplicationLarger) { l.Key.ID += "-changed" }},
					{"original path", func(l *verify.PythonApplicationLarger) { l.Original.Path += ".changed" }},
					{"original hash", func(l *verify.PythonApplicationLarger) { l.Original.SHA256 = "sha256:" + strings.Repeat("a", 64) }},
				}
				if decoded.Application.Larger[i].Execution != nil {
					changes = append(changes, struct {
						name  string
						apply func(*verify.PythonApplicationLarger)
					}{"copy path", func(l *verify.PythonApplicationLarger) { l.Driver.Path += ".changed" }},
						struct {
							name  string
							apply func(*verify.PythonApplicationLarger)
						}{"copy hash", func(l *verify.PythonApplicationLarger) { l.Driver.SHA256 = "sha256:" + strings.Repeat("b", 64) }},
						struct {
							name  string
							apply func(*verify.PythonApplicationLarger)
						}{"overlay path", func(l *verify.PythonApplicationLarger) { l.Execution.Actual.Overlays[0].Path += ".changed" }},
						struct {
							name  string
							apply func(*verify.PythonApplicationLarger)
						}{"overlay hash", func(l *verify.PythonApplicationLarger) {
							l.Execution.Actual.Overlays[0].SHA256 = "sha256:" + strings.Repeat("c", 64)
						}},
						struct {
							name  string
							apply func(*verify.PythonApplicationLarger)
						}{"observed driver hash", func(l *verify.PythonApplicationLarger) {
							l.Execution.Observations[0].DriverSHA256 = "sha256:" + strings.Repeat("d", 64)
						}})
				}
				for _, change := range changes {
					var changed verify.Report
					if err := json.Unmarshal(data, &changed); err != nil {
						t.Fatal(err)
					}
					change.apply(&changed.Application.Larger[i])
					if verify.Fingerprint(changed) == r.Fingerprint {
						t.Errorf("fingerprint omits %s of invocation %s", change.name, decoded.Application.Larger[i].Key.ID)
					}
				}
			}
			if mode == "success" {
				again, _ := verify.PerCheckDriverReport884(t, mode)
				if again.Fingerprint != r.Fingerprint {
					t.Fatal("private driver paths or relocation changed the portable fingerprint")
				}
			}
		})
	}
}
