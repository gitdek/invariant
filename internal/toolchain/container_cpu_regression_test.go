package toolchain

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestContainerCPUDecimalSettings(t *testing.T) {
	for _, value := range []string{".01", "4.", "004.25", "0.0100"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(ContainerCPUsEnv, value)
			if got, err := ContainerCPUs(); err != nil || got != value {
				t.Fatalf("CPU count %q: got %q, %v", value, got, err)
			}
		})
	}
	for _, value := range []string{"+4", "1e2", "0x1p2", "1_000", "4 ", "\t4", ".", "0.0099", strings.Repeat("9", 400)} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(ContainerCPUsEnv, value)
			if _, err := ContainerCPUs(); err == nil || !strings.Contains(err.Error(), ContainerCPUsEnv) {
				t.Fatalf("invalid CPU count %q: %v", value, err)
			}
		})
	}
}

func TestContainerCPUCreateOptions(t *testing.T) {
	t.Setenv(ContainerCPUsEnv, "2.5")
	for _, options := range [][]string{
		{"--cpus", "1"}, {"--cpus=1"}, {"--cpu-period", "100000", "--cpu-quota", "100000"},
		{"--cpuset-cpus=0-1"}, {"--cpu-count", "1"}, {"--cpu-percent=50"},
	} {
		args := append([]string{"create"}, options...)
		args = append(args, "--", "image", "--cpus", "99")
		cmd := Docker(context.Background(), args...)
		if !slices.Equal(cmd.Args[1:], args) {
			t.Errorf("explicit CPU limit changed: %q, want %q", cmd.Args[1:], args)
		}
	}
	// Values that look like flags and flags after the image are literal data.
	args := []string{"create", "--env", "--cpus", "-e--cpus=1", "--label=--cpus", "--cpu-shares", "512", "--", "image", "--cpus=99"}
	cmd := Docker(context.Background(), args...)
	want := append([]string{"create", "--cpus", "2.5"}, args[1:]...)
	if !slices.Equal(cmd.Args[1:], want) {
		t.Fatalf("default quota changed operands: %q, want %q", cmd.Args[1:], want)
	}
}

func TestContainerCPUInvalidWithExplicitLimit(t *testing.T) {
	t.Setenv(ContainerCPUsEnv, "bad")
	for _, command := range []string{"run", "create"} {
		for _, method := range []string{"Start", "Output", "CombinedOutput"} {
			cmd := Docker(context.Background(), command, "--cpus=1", "image", "true")
			var err error
			switch method {
			case "Start":
				err = cmd.Start()
			case "Output":
				_, err = cmd.Output()
			case "CombinedOutput":
				_, err = cmd.CombinedOutput()
			}
			if err == nil || !strings.Contains(err.Error(), ContainerCPUsEnv) || cmd.Process != nil {
				t.Errorf("%s.%s must refuse before starting a process: %v", command, method, err)
			}
		}
	}
}
