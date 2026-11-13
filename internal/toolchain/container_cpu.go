package toolchain

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"runtime"
	"strconv"
)

const ContainerCPUsEnv = "INVARIANT_CONTAINER_CPUS"

var containerCPUDecimal = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)$`)

// machineCPUs is how many CPUs this machine has. Where Docker's daemon runs
// on the same machine, as it does on a CI runner, it's the daemon's count.
var machineCPUs = runtime.NumCPU

// ContainerCPUs is the host's default quota for each workload container.
// An empty setting selects four CPUs, or the machine's own count when it
// has fewer: Docker refuses a container a quota above what its machine has,
// so on a two-CPU runner the gate could create no container at all (#920).
// An explicit setting is used as it is, and explicit container quotas still
// win.
func ContainerCPUs() (string, error) {
	value := os.Getenv(ContainerCPUsEnv)
	if value == "" {
		if n := machineCPUs(); n >= 1 && n < 4 {
			return strconv.Itoa(n), nil
		}
		return "4", nil
	}
	n, err := strconv.ParseFloat(value, 64)
	if !containerCPUDecimal.MatchString(value) || err != nil || math.IsInf(n, 0) || math.IsNaN(n) || n < 0.01 {
		return "", fmt.Errorf("%s must be a finite decimal CPU count of at least 0.01, got %q", ContainerCPUsEnv, value)
	}
	return value, nil
}
