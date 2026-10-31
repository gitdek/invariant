package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonApplicationRelativeInvocationValidation(t *testing.T) {
	for _, tc := range []struct {
		name, cwd, driver, want string
	}{
		{"application root", ".", pa327DriverPath, ""},
		{"parent driver path", pa327Project, "../lifecycle/driver.py", ""},
		{"escaping driver", ".", "../" + pa327DriverPath, "driver escapes application"},
		{"mismatched driver", ".", pa327Source, "conformance driver does not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := pa327New(t, true, false)
			v := &f.p.Manifest.Components.Checks[0].Invocations[1]
			v.WorkingDir, v.Args = tc.cwd, []string{tc.driver}
			pa327Save(t, f)
			before := pa327Tree(t, f.root)
			t.Chdir(f.p.Dir)
			var invocations []verify.PythonApplicationInvocation
			var provenance verify.PythonApplicationProvenance
			for _, dir := range []string{f.p.Dir, "."} {
				err := verify.Check(dir)
				if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
					t.Fatalf("Check(%q): %v, want %q", dir, err, tc.want)
				}
				p, err := project.Load(dir)
				if err != nil {
					t.Fatal(err)
				}
				originalDir := p.Dir
				stage := filepath.Join(t.TempDir(), "stage")
				selected := false
				s, err := verify.PreparePythonApplicationSession(context.Background(), p, stage, verify.PythonApplicationSessionOptions{
					SelectImage: func(context.Context, *verify.PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
						selected = true
						return toolchain.PythonImage, nil, nil
					},
				})
				if s != nil {
					t.Cleanup(func() { s.Close() })
				}
				if p.Dir != originalDir {
					t.Fatal("preparation changed the caller's project directory")
				}
				if tc.want != "" {
					if err == nil || !strings.Contains(err.Error(), tc.want) || s != nil || selected {
						t.Fatalf("Prepare(%q): session=%v err=%v selected=%v", dir, s, err, selected)
					}
					if _, err := os.Stat(stage); !os.IsNotExist(err) {
						t.Fatalf("invalid invocation reached staging: %v", err)
					}
					continue
				}
				if err != nil || s == nil {
					t.Fatalf("Prepare(%q): %v", dir, err)
				}
				if invocations == nil {
					invocations, provenance = s.Invocations(), s.Provenance()
				} else if !reflect.DeepEqual(invocations, s.Invocations()) || !reflect.DeepEqual(provenance, s.Provenance()) {
					t.Fatal("relative path changed invocation or provenance")
				}
			}
			if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
				t.Fatal("validation or preparation changed the original application")
			}
		})
	}
}
