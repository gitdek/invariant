package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

func TestReaderSnapshotReachesHelperChecks(t *testing.T) {
	p := tlapsProject(t, tlapsPool, tlapsPoolProof)
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	specDir := filepath.Dir(p.ModulePath())
	includes := map[string]string{
		"Helper.tla":  "---- MODULE Helper ----\nZero == 0\n====\n",
		"Removed.tla": "---- MODULE Removed ----\nOne == 1\n====\n",
	}
	for name, body := range includes {
		write(filepath.Join(specDir, name), body)
	}
	work := t.TempDir()
	if _, err := stage(work, "model-snapshot", p, tlapsPool, ""); err != nil {
		t.Fatal(err)
	}
	// These changes happen after capture, before either helper stages its
	// check. Both the bytes and the set of includes must stay captured.
	write(p.ModulePath(), strings.ReplaceAll(tlapsPool, "Size", "ChangedSize"))
	write(filepath.Join(specDir, "Helper.tla"), strings.ReplaceAll(includes["Helper.tla"], "0", "9"))
	write(filepath.Join(specDir, "Later.tla"), "---- MODULE Later ----\n====\n")
	if err := os.Remove(filepath.Join(specDir, "Removed.tla")); err != nil {
		t.Fatal(err)
	}

	// Only verifier processes are doubled. The actual helpers stage and run
	// their checks, and the assertions inspect those staged input bytes.
	bin := t.TempDir()
	proofOutput := strings.Replace(tlapsOutput(1, [][3]string{{"1", "proved", "smt"}}), "4744c0f", toolchain.TLAPSVersion, 1)
	script := `#!/bin/sh
case "$1" in
image|rm) exit 0 ;;
run) ;;
*) exit 91 ;;
esac
case " $* " in
*" --config "*) printf '%s\n' 'version == "` + toolchain.TLAPSVersion + `"' ;;
*" tlc2.TLC "*) printf '%s\n' '@!@!@STARTMSG 2193:0 @!@!@
Model checking completed. No error has been found.
@!@!@ENDMSG 2193 @!@!@
@!@!@STARTMSG 2199:0 @!@!@
2 states generated, 1 distinct states found, 0 states left on queue.
@!@!@ENDMSG 2199 @!@!@
@!@!@STARTMSG 2186:0 @!@!@
Finished in 10ms
@!@!@ENDMSG 2186 @!@!@' ;;
*) printf '%s' '` + proofOutput + `' ;;
esac
`
	docker := filepath.Join(bin, "docker")
	write(docker, script)
	if err := os.Chmod(docker, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	image := "invariant-tlaps:" + toolchain.TLAPSRecipe()[len("sha256:"):][:12]
	previous, loaded := tlapsConfig.LoadAndDelete(image)
	t.Cleanup(func() {
		tlapsConfig.Delete(image)
		if loaded {
			tlapsConfig.Store(image, previous)
		}
	})
	jar := filepath.Join(t.TempDir(), "tla2tools.jar")
	write(jar, "snapshot fixture jar")

	for _, check := range []string{"property bug", "TLAPS"} {
		t.Run(check, func(t *testing.T) {
			var staged string
			if check == "property bug" {
				bug := project.Statement{Name: "Stall", Expect: "EventuallyReleased"}
				var result Bug
				runner := tlc.Runner{Image: toolchain.JavaImage, Jar: jar}
				if err := checkPropertyBug(t.Context(), &result, bug, work, "", p, tlapsPool, runner, tlc.Config{}, ""); err != nil {
					t.Fatal(err)
				}
				if result.Outcome != string(tlc.Passed) {
					t.Fatalf("property bug did not reach TLC: %+v", result)
				}
				staged = filepath.Join(work, "Bug_Stall")
			} else {
				proofs, _, err := proveTLAPS(t.Context(), work, p, tlapsPool, nil)
				if err != nil || len(proofs) != 1 || !proofs[0].Proved {
					t.Fatalf("TLAPS helper did not finish: %+v, %v", proofs, err)
				}
				staged = filepath.Join(work, "tlaps-NoSharedConnection")
			}
			for name, want := range includes {
				got, err := os.ReadFile(filepath.Join(staged, name))
				if err != nil || string(got) != want {
					t.Errorf("%s did not retain captured %s: %q, %v", check, name, got, err)
				}
			}
			if got, err := os.ReadFile(filepath.Join(staged, "ConnectionPool.tla")); err != nil || string(got) != tlapsPool {
				t.Errorf("%s did not retain the captured model: %v", check, err)
			}
			if _, err := os.Stat(filepath.Join(staged, "Later.tla")); !os.IsNotExist(err) {
				t.Errorf("%s staged an include added after capture: %v", check, err)
			}
		})
	}
}
