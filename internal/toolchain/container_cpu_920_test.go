package toolchain

import (
	"context"
	"runtime"
	"slices"
	"testing"
)

// With no setting, a container's default quota is four CPUs, or the
// machine's own count when it has fewer (#920). Docker refuses a quota
// above what its machine has, so on a two-CPU runner, which is what a
// private repository's CI gets, the gate could create no container at all.
// A machine with four or more gives what it gave before.
func TestTheDefaultQuotaFitsASmallMachine920(t *testing.T) {
	real := machineCPUs
	t.Cleanup(func() { machineCPUs = real })
	t.Setenv(ContainerCPUsEnv, "")
	if machineCPUs() != runtime.NumCPU() {
		t.Errorf("the machine's count is taken as %d; it has %d CPUs", machineCPUs(), runtime.NumCPU())
	}
	for cpus, want := range map[int]string{1: "1", 2: "2", 3: "3", 4: "4", 5: "4", 7: "4", 8: "4", 18: "4", 0: "4", -1: "4"} {
		machineCPUs = func() int { return cpus }
		if got, err := ContainerCPUs(); err != nil || got != want {
			t.Errorf("on a machine with %d CPUs the default quota is %q, %v; want %q", cpus, got, err, want)
		}
	}

	// The quota reaches the container that's created.
	machineCPUs = func() int { return 2 }
	for _, verb := range []string{"run", "create"} {
		args := Docker(context.Background(), verb, "--rm", "img", "true").Cmd.Args
		if i := slices.Index(args, "--cpus"); i < 0 || i+1 >= len(args) || args[i+1] != "2" {
			t.Errorf("docker %s on a two-CPU machine is given %v; want --cpus 2", verb, args)
		}
	}

	// An explicit setting is the operator's, and is used as it is: one
	// above what the machine has fails with Docker's own reason, as before.
	for _, value := range []string{"8", "4", "0.5"} {
		t.Setenv(ContainerCPUsEnv, value)
		if got, err := ContainerCPUs(); err != nil || got != value {
			t.Errorf("with the setting at %s on a two-CPU machine the quota is %q, %v; want the setting", value, got, err)
		}
	}
}
