package statechart

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Both poster modes print strokeW to one decimal place. These counts put
// the width on opposite sides of that boundary when Log1p's inner arithmetic
// rounds differently. Testing the width avoids allocating the occurrences.
// The ordinary suite reruns this assertion with implicit fusion disabled.
func TestPosterLogarithmStrokeWidth(t *testing.T) {
	p := poster{maxStep: 959512575}
	if got := fmt.Sprintf("%.1f", p.strokeW(175)); got != "2.5" {
		t.Errorf("stroke width = %s, want arm64 2.5 (unfused Log1p gives 2.6)", got)
	}
	for _, count := range []int{1, 2, 175, 959512575, int(^uint(0) >> 1)} {
		p.maxStep = count
		if got := fmt.Sprintf("%.1f", p.strokeW(count)); got != "5.1" {
			t.Errorf("maximum count %d: width = %s, want 5.1", count, got)
		}
	}
}

// Check the port against the original arm64 library over small counts,
// exponent boundaries and larger counts. Compile the reference separately
// with default fusion, including when the suite itself disables fusion.
// The fixed width regression above and the assembly checks run on every host.
func TestPosterLogarithmARM64(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		return
	}
	counts := []int{175, 959512575, int(^uint(0) >> 1)}
	for count := 0; count < 4096; count++ {
		counts = append(counts, count)
	}
	for power := uint64(2); power <= uint64(^uint(0)>>1); power <<= 1 {
		for offset := -2; offset <= 2; offset++ {
			counts = append(counts, int(power)+offset)
		}
	}
	n := uint64(792)
	for i := 0; i < 256; i++ {
		n = n*6364136223846793005 + 1442695040888963407
		counts = append(counts, int(n>>1))
	}
	var source strings.Builder
	source.WriteString("package main\nimport (\"fmt\"; \"math\")\nfunc main() {\nfor _, n := range []int{\n")
	for _, count := range counts {
		fmt.Fprintf(&source, "%d,\n", count)
	}
	source.WriteString("} { fmt.Printf(\"%016x\\n\", math.Float64bits(math.Log1p(float64(n)))) }\n}\n")
	path := filepath.Join(t.TempDir(), "log1p.go")
	if err := os.WriteFile(path, []byte(source.String()), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	goName := "go"
	if runtime.GOOS == "windows" {
		goName += ".exe"
	}
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", goName), "run", "-gcflags=all=", path)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GOFLAGS", "GOENV", "GOWORK", "GOARCH", "GOOS", "CGO_ENABLED":
		default:
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GOFLAGS=-mod=readonly -p=1", "GOENV=off", "GOWORK=off",
		"GOARCH="+runtime.GOARCH, "GOOS="+runtime.GOOS, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("arm64 Log1p reference: %v\n%s", err, &stderr)
	}
	lines := strings.Fields(string(out))
	if len(lines) != len(counts) {
		t.Fatalf("reference returned %d values for %d counts", len(lines), len(counts))
	}
	for i, count := range counts {
		want, err := strconv.ParseUint(lines[i], 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		if got := math.Float64bits(posterLog1p(count)); got != want {
			t.Fatalf("Log1p(%d) bits = %016x, want arm64 %016x", count, got, want)
		}
	}
}
