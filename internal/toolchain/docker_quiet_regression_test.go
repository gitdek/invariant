package toolchain

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPinnedDockerQuietRunAcquiresImage(t *testing.T) {
	for _, quiet := range []string{"--quiet", "-q"} {
		t.Run(quiet, func(t *testing.T) {
			dir := docker433Setup(t, "recover")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			args := []string{"run", "--platform", "linux/amd64", quiet, JavaImage, "verify", "literal argument"}
			cmd := Docker(ctx, args...)
			cmd.Stdin = strings.NewReader("quiet input\n")
			out, err := cmd.Output()
			if err != nil || string(out) != "verified:quiet input\n" {
				t.Fatalf("quiet run lost acquisition retries or input: %q, %v", out, err)
			}
			pulls := docker433OfKind(dir, "pull")
			if len(pulls) != 3 {
				t.Fatalf("pulls=%d, want 3", len(pulls))
			}
			for _, pull := range pulls {
				if pull.Args[len(pull.Args)-1] != JavaImage || docker433Option(pull.Args, "--platform") != "linux/amd64" {
					t.Errorf("quiet acquisition changed image or platform: %q", pull.Args)
				}
			}
			runs := docker433OfKind(dir, "run")
			if len(runs) != 1 {
				t.Fatalf("workload executions=%d, want one", len(runs))
			}
			got, want := runs[0].Args, args[1:]
			if len(got) < len(want) || !reflect.DeepEqual(got[len(got)-len(want):], want) {
				t.Errorf("quiet run changed arguments: %q, want suffix %q", got, want)
			}
		})
	}
}
