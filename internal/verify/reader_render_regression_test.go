package verify_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func TestReaderRenderFailureBindsComputedTools(t *testing.T) {
	tc, _ := reader615Tools(t)
	dir := reader615Project(t, 2)
	reader615Write(t, filepath.Join(dir, ".invariant/view.json"), strings.ReplaceAll(reader615View, "Ready", `Ready\u0001`))
	run := func(t *testing.T, tc toolchain.Toolchain) reader615Record {
		t.Helper()
		r := reader615Run(t, dir, tc, true)
		got := reader615RecordOf(t, r)
		if !r.Passed || got.Chart != nil || got.Limitation != nil || got.Unavailable == nil || got.Unavailable.Code != "reader_render" {
			t.Fatalf("render failure lost its diagnostic or changed the verdict: %+v", got)
		}
		if source := r.Reader.Unavailable.Source; source == nil || source.Tool.Image != tc.JavaImage || source.Tool.JarSHA256 == "" {
			t.Fatalf("render failure lost tool provenance: %+v", source)
		}
		plain := reader615Without(t, r)
		if r.Fingerprint != verify.Fingerprint(plain) || r.Claim() != plain.Claim() || r.CodeAssurance() != plain.CodeAssurance() {
			t.Fatal("render failure changed verification evidence or assurance")
		}
		return got
	}
	base := run(t, tc)
	for _, change := range []string{"image", "jar"} {
		t.Run(change, func(t *testing.T) {
			changed := tc
			if change == "image" {
				changed.JavaImage = "reader-java@sha256:" + strings.Repeat("a", 64)
			} else {
				changed.TLCJar = filepath.Join(t.TempDir(), "tla2tools.jar")
				reader615Write(t, changed.TLCJar, "different fixture TLC jar bytes")
			}
			got := run(t, changed)
			if !reflect.DeepEqual(got.Inputs, base.Inputs) || !reflect.DeepEqual(got.Unavailable, base.Unavailable) {
				t.Fatal("fixture changed source inputs or render diagnostic")
			}
			if got.Fingerprint == base.Fingerprint {
				t.Fatalf("render failure discarded the computed %s provenance", change)
			}
			if again := run(t, changed); !reflect.DeepEqual(got, again) {
				t.Fatal("identical inputs changed the render-failure reader identity")
			}
		})
	}
}
