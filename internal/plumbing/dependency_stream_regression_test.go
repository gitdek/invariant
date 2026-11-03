package plumbing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A rejected copy must release a writer whose stream exceeds the pipe's
// buffer, even when Docker exits without consuming any of it.
func TestDependencyDockerCopyRejectsBeforeReading(t *testing.T) {
	for _, install := range []bool{true, false} {
		t.Run(fmt.Sprintf("install_%t", install), func(t *testing.T) {
			q := dependency485Fixture(t, install)
			body := strings.Repeat("x", 2<<20)
			dependency485Write(t, q.SourceDir, "app/large", body, 0o600)
			q.Inputs["app/large"] = dependency485Hash(body)
			log := dependency485Docker(t, q)
			docker, err := exec.LookPath("docker")
			if err != nil {
				t.Fatal(err)
			}
			bin, stages := t.TempDir(), t.TempDir()
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
			script := "#!/bin/sh\nset -eu\n" +
				"if [ \"$1\" = cp ] && [ \"$2\" = '-' ]; then\n" +
				"  echo cp >> \"$S485_LOG/calls\"\n" +
				"  echo early-copy-refusal >&2\n  exit 71\nfi\n" +
				"exec " + quote(docker) + " \"$@\"\n"
			dependency485Write(t, bin, "docker", script, 0o755)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TMPDIR", stages)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if install {
				_, err = (&DockerDependencyRunner{}).Install(ctx, q)
			} else {
				_, err = (&DockerDependencyRunner{}).Run(ctx, q)
			}
			if err == nil || !strings.Contains(err.Error(), "early-copy-refusal") || ctx.Err() != nil {
				t.Fatalf("copy did not promptly report Docker's refusal: %v", err)
			}
			dependency485Stopped(t, log)
			left, err := filepath.Glob(filepath.Join(stages, "invariant-dependency-copy-*"))
			if err != nil || len(left) != 0 {
				t.Fatalf("failed copy left staging directories: %v, %v", left, err)
			}
		})
	}
}
