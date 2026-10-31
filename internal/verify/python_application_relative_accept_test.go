package verify_test

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Reuse #327's fixtures and transport/TLC doubles. Python and the embedded
// runner/collector are real; these tests require neither Docker nor TLC.
func TestPythonApplicationRelativePreflightAndRun(t *testing.T) {
	tc, _ := pa327Fake(t)
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			f := pa327New(t, explicit, false)
			before := pa327Tree(t, f.root)
			t.Chdir(filepath.Dir(f.root))
			relative := filepath.Join(filepath.Base(f.root), pa327Project)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			var absolute *verify.Report
			for _, dir := range []string{f.p.Dir, relative} {
				if err := verify.Check(dir); err != nil {
					t.Fatalf("Check(%q): %v", dir, err)
				}
				r, err := verify.Run(ctx, dir, "", tc)
				if err != nil || r == nil {
					t.Fatalf("Run(%q): report=%+v err=%v", dir, r, err)
				}
				pa327Assert(t, f, r, true)
				if r.Fingerprint == "" || verify.Fingerprint(*r) != r.Fingerprint {
					t.Fatal("receipt lacks a valid portable fingerprint")
				}
				// Generation time is not verification evidence.
				r.GeneratedAt = ""
				if absolute == nil {
					absolute = r
				} else {
					if r.Fingerprint != absolute.Fingerprint {
						t.Fatalf("path spelling changed fingerprint: %s != %s", r.Fingerprint, absolute.Fingerprint)
					}
					if receipt.Markdown(r) != receipt.Markdown(absolute) {
						t.Fatal("relative and absolute paths produced different Markdown receipts")
					}
					if len(r.Application.Executions) != len(absolute.Application.Executions) {
						t.Fatal("path spelling changed the executed checks")
					}
					for i, execution := range r.Application.Executions {
						want := absolute.Application.Executions[i]
						if !reflect.DeepEqual(execution.Declared, want.Declared) || execution.Key != want.Key || execution.Fingerprint != want.Fingerprint {
							t.Fatalf("path spelling changed invocation evidence: %+v != %+v", execution, want)
						}
					}
				}
			}
			if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
				t.Fatal("verification changed the original application")
			}
		})
	}
}

func TestPythonApplicationRelativeSession(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			f := pa327New(t, explicit, false)
			before := pa327Tree(t, f.root)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			prepare := func(t *testing.T, dir string) *verify.PythonApplicationSession {
				t.Helper()
				p, err := project.Load(dir)
				if err != nil {
					t.Fatal(err)
				}
				s, err := verify.PreparePythonApplicationSession(ctx, p, filepath.Join(t.TempDir(), "stage"), verify.PythonApplicationSessionOptions{
					SelectImage: func(context.Context, *verify.PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
						return toolchain.PythonImage, nil, nil
					},
				})
				if err != nil {
					t.Fatalf("PreparePythonApplicationSession(%q): %v", dir, err)
				}
				t.Cleanup(func() { s.Close() })
				return s
			}
			absolute := prepare(t, f.p.Dir)
			for _, where := range []struct{ cwd, arg string }{
				{filepath.Dir(f.root), filepath.Join(filepath.Base(f.root), pa327Project)},
				{f.p.Dir, "."},
				{f.p.Dir, "../lifecycle"},
			} {
				t.Run(where.arg, func(t *testing.T) {
					t.Chdir(where.cwd)
					relative := prepare(t, where.arg)
					if !reflect.DeepEqual(relative.Invocations(), absolute.Invocations()) {
						t.Fatal("relative path changed prepared invocations")
					}
					if !reflect.DeepEqual(relative.Provenance(), absolute.Provenance()) {
						t.Fatal("relative path changed staged portable provenance")
					}
				})
			}
			if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
				t.Fatal("session preparation changed the original application")
			}
		})
	}
}
