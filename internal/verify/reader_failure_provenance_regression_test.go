package verify_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func TestReaderViewFailureBindsComputedTools(t *testing.T) {
	tc, _ := reader615Tools(t)
	for _, test := range []struct {
		code, view string
	}{
		{"unmapped_state", strings.Replace(reader615View, `"id":"done","when":true`, `"id":"done","when":false`, 1)},
		{"unmapped_step", strings.Replace(reader615View, `"id":"reset","when":true`, `"id":"reset","when":false`, 1)},
		{"empty_outgoing", strings.Replace(reader615View, `"label":"Start again"`, `"label":"Start again","hide":true`, 1)},
	} {
		t.Run(test.code, func(t *testing.T) {
			dir := reader615Project(t, 2)
			reader615Write(t, filepath.Join(dir, ".invariant/view.json"), test.view)
			run := func(t *testing.T, tc toolchain.Toolchain, jar string) verify.Reader {
				t.Helper()
				r := reader615Run(t, dir, tc, true)
				reader615RecordOf(t, r)
				got := r.Reader
				if !r.Passed || got.Chart != nil || got.Limitation != nil || got.Unavailable == nil || got.Unavailable.Code != test.code {
					t.Fatalf("reader failure lost its diagnostic or changed the verdict: %+v", got)
				}
				source := got.Unavailable.Source
				cfg := tlc.Config{Specification: "Spec", Constants: map[string]string{"Limit": "2"}, Invariants: []string{"Safe"}}
				if source == nil || source.Module != "Reader615" || !reflect.DeepEqual(source.Config, cfg) ||
					source.Tool.Image != tc.JavaImage || source.Tool.JarSHA256 != strings.TrimPrefix(reader615Hash([]byte(jar)), "sha256:") || source.TLC == "" {
					t.Fatalf("reader failure lost completed graph provenance: %+v", source)
				}
				plain := reader615Without(t, r)
				if r.Fingerprint != verify.Fingerprint(plain) || r.Claim() != plain.Claim() || r.CodeAssurance() != plain.CodeAssurance() {
					t.Fatal("reader failure changed verification evidence or assurance")
				}
				var round verify.Report
				if err := json.Unmarshal(reader615JSON(t, r), &round); err != nil || !reflect.DeepEqual(round.Reader, got) {
					t.Fatalf("reader failure lost provenance in JSON round trip: %v", err)
				}
				return *got
			}
			base := run(t, tc, "fixture TLC jar 615")
			for _, change := range []string{"image", "jar"} {
				t.Run(change, func(t *testing.T) {
					changed, jar := tc, "fixture TLC jar 615"
					if change == "image" {
						changed.JavaImage = "reader-java@sha256:" + strings.Repeat("a", 64)
					} else {
						changed.TLCJar = filepath.Join(t.TempDir(), "tla2tools.jar")
						jar = "different fixture TLC jar bytes"
						reader615Write(t, changed.TLCJar, jar)
					}
					got := run(t, changed, jar)
					if !reflect.DeepEqual(got.Inputs, base.Inputs) || got.Unavailable.Code != base.Unavailable.Code || got.Unavailable.Message != base.Unavailable.Message {
						t.Fatal("fixture changed source inputs or refusal")
					}
					if got.Fingerprint == "" || got.Fingerprint == base.Fingerprint {
						t.Fatalf("reader failure discarded the computed %s provenance", change)
					}
					if again := run(t, changed, jar); !reflect.DeepEqual(got, again) {
						t.Fatal("identical inputs changed the reader-failure identity")
					}
				})
			}
		})
	}
}
