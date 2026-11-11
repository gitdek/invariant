package statechart_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/statechart"
)

// These fixtures use the existing checked-chart builders without changing
// their tests. They exercise branches, long arrows, returns, parallel arrows,
// self-loops, multiple initials, literal multiline text and complete evidence.
func processor792Images(t *testing.T) map[string]string {
	t.Helper()
	chain, chainEdges := svg613Chain(5, true)
	// This small cycle with a long forward arrow produces different poster
	// bytes on the base when implicit FMA is disabled, in both motion modes.
	rounding, roundingEdges := svg613Chain(3, true)
	roundingEdges = append(roundingEdges, svg613Edge{2, 0, "A", false}, svg613Edge{0, 2, "Jump", false})
	charts := []struct {
		name  string
		chart *statechart.Result
	}{
		{"rounding", svg613Chart(t, rounding, []int{0}, roundingEdges)},
		{"chain", svg613Chart(t, chain, []int{0}, chainEdges)},
		{"branches", svg613Chart(t,
			[]string{"Ready", "Left branch", "Right branch", "Joining", "Done"},
			[]int{0, 2}, []svg613Edge{
				{0, 1, "Begin", false}, {0, 2, "Choose right", false},
				{0, 4, "Finish early", false}, {1, 3, "Continue", false},
				{2, 3, "Continue", false}, {3, 4, "Finish", false},
				{4, 0, "Restart", false}, {3, 1, "Try again", false},
				{1, 1, "Wait", false}, {1, 1, "Inspect", false},
				{0, 1, "Retry", false}, {0, 1, "Begin", false},
				{2, 2, "Internal poll", true},
			})},
		{"evidence", svg608Chart(t, "Ready\n準備 <&>", "Working\nKeep  both lines", "Begin\nagain")},
		{"singleton", svg613Chart(t, []string{"Ready"}, []int{0}, nil)},
	}
	images := map[string]string{}
	for _, fixture := range charts {
		for _, animated := range []bool{false, true} {
			kind := "still"
			if animated {
				kind = "animated"
			}
			s, err := fixture.chart.Poster(statechart.PosterOptions{Title: fixture.name + " fixture", Animated: animated})
			if err != nil {
				t.Fatalf("%s %s: %v", fixture.name, kind, err)
			}
			images[fixture.name+"/"+kind] = s
		}
		s, err := fixture.chart.SVG()
		if err != nil {
			t.Fatalf("%s reader SVG: %v", fixture.name, err)
		}
		images[fixture.name+"/reader"] = s
	}
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.SVG()
	if err != nil {
		t.Fatal(err)
	}
	images["comparison/reader"] = s
	return images
}

// Captured from the unmodified base with Go 1.27.3 on darwin/arm64 and
// default compiler fusion. These are whole SVG hashes, including whitespace,
// metadata, report text, animation timing, background and all geometry.
var processor792ARM64 = map[string]string{
	"branches/animated":  "21244721a5a94ac56a5284a4c8cf28ea15aed351637ccf8ec20c4b1c9af35187",
	"branches/reader":    "fb160ee1fd932c32caf904d15e61269de1f76d89119c4e95fef74a2805b4218c",
	"branches/still":     "80fb974afdf85c58497a3d7c85cf646ce42b254763bdc8a7dd2cbb21e700fa04",
	"chain/animated":     "3e9fcb63c45b20e567992c0907534ac97a593539ba04c1f048424801f7e2b15a",
	"chain/reader":       "f1a9c4c51db304a5aa159fb750e0deb90000cee19152cc8fdd39a946097fa4e1",
	"chain/still":        "6c250c4cb13c33ca6c4abf6c9daad47f324d978ca94b9af0bf485049d58ec1e9",
	"comparison/reader":  "5d99d830c80163e78b0316b6294669449bb065cc48b2d1ec90eb2883b0d4d795",
	"evidence/animated":  "71954784f727c3ef791a6bb40cfcfd90abd345a2d84d3d9912e6ce23ccc128f7",
	"evidence/reader":    "afa44a5b2d82c6a8da8d7bebadd89128b5f402b61901dbcb5466567511a3564a",
	"evidence/still":     "e9592b406ee85ab9d7cf298a8e78bb834e16e4e5d096c5f3e9a6b4124bd293b9",
	"rounding/animated":  "c3f00c160e4401d058e4fe32df0ab62aaa4c06a371744829869651d7f0cf52b6",
	"rounding/reader":    "04edd6bee9c15c71b9f3b7fafb06f87101f80f82feeed2108103d8395f9e2361",
	"rounding/still":     "947dd2b77cf43633d2f33a8c9d443a784539f6bf94396559f8d6af572c1b2f0c",
	"singleton/animated": "00d952b1238959019b1f28a260ee6a60989c68cffd0b9aa556031af0d0294323",
	"singleton/reader":   "3ccd84b6ed19ab977f0b2a02cc392f5c7daf3d2bc3701fe14bc7eb9ba803c51e",
	"singleton/still":    "2d58818bf3e1774720761e26737740bdb1398efea7f2886f388ebd2112a85946",
}

func processor792Go() string {
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(runtime.GOROOT(), "bin", name)
}

func processor792Env(overrides map[string]string) []string {
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, replace := overrides[key]; !replace {
			env = append(env, item)
		}
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func TestStatechartProcessorIndependentBytes792(t *testing.T) {
	images := processor792Images(t)
	if len(images) != len(processor792ARM64) {
		t.Errorf("rendered %d images, want %d pinned arm64 images", len(images), len(processor792ARM64))
	}
	for name, svg := range images {
		got := fmt.Sprintf("%x", sha256.Sum256([]byte(svg)))
		if got != processor792ARM64[name] {
			t.Errorf("%s: SHA-256 %s, want arm64 %s", name, got, processor792ARM64[name])
		}
	}
	// This ordinary package test is already included by CI's unit-test step.
	// Rerun the entire suite, not just the fixtures, under the compiler setting
	// that exposed #791. The child still checks every golden; only launching a
	// further child is suppressed. No architecture, short-mode or CI skip.
	const child = "INVARIANT_STATECHART_NOFMA_792"
	if os.Getenv(child) == "1" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, processor792Go(), "test", "-json", "-count=1", "-timeout=3m",
		"-gcflags=all=-d=fmahash=n", ".")
	cmd.Env = processor792Env(map[string]string{child: "1"})
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		// fmahash can print a large compiler trace. Keep the error and a tail
		// for build failures; test failures are decoded by name below.
		diagnostic := stderr.String()
		if len(diagnostic) > 4096 {
			diagnostic = diagnostic[len(diagnostic)-4096:]
		}
		t.Errorf("statechart suite with implicit fusion disabled: %v\n%s", err, diagnostic)
	}
	// A successful process with an empty selection would prove nothing.
	decoder := json.NewDecoder(&stdout)
	fixturePassed, packagePassed := false, false
	for {
		var event struct{ Action, Package, Test, Output string }
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if event.Package != "github.com/gitdek/invariant/internal/statechart" {
			continue
		}
		if event.Action == "fail" || event.Action == "skip" {
			t.Errorf("fusion-disabled suite: %s %s %s", event.Action, event.Package, event.Test)
		}
		if event.Action == "output" && strings.Contains(event.Output, "SHA-256") {
			t.Log(strings.TrimSpace(event.Output))
		}
		if event.Action == "pass" && event.Test == "TestStatechartProcessorIndependentBytes792" {
			fixturePassed = true
		}
		if event.Action == "pass" && event.Test == "" {
			packagePassed = true
		}
	}
	if !fixturePassed || !packagePassed {
		t.Fatalf("fusion-disabled suite did not complete: fixtures=%v package=%v", fixturePassed, packagePassed)
	}
}
