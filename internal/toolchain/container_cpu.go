package toolchain

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
)

const ContainerCPUsEnv = "INVARIANT_CONTAINER_CPUS"

var containerCPUDecimal = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)$`)

// ContainerCPUs is the host's default quota for each workload container.
// An empty setting selects four CPUs; explicit container quotas still win.
func ContainerCPUs() (string, error) {
	value := os.Getenv(ContainerCPUsEnv)
	if value == "" {
		return "4", nil
	}
	n, err := strconv.ParseFloat(value, 64)
	if !containerCPUDecimal.MatchString(value) || err != nil || math.IsInf(n, 0) || math.IsNaN(n) || n < 0.01 {
		return "", fmt.Errorf("%s must be a finite decimal CPU count of at least 0.01, got %q", ContainerCPUsEnv, value)
	}
	return value, nil
}
