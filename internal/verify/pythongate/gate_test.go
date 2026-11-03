package pythongate_test

import (
	"testing"

	"github.com/gitdek/invariant/internal/verify/internal/pythongatetest"
)

// These suites change the process environment and stay serial. Their separate
// package lets Go schedule them alongside verify's independent local cases.
// Local execution is unconditional, including INVARIANT_PYTHON_PINNED=1.
func TestPythonApplicationGateFailures(t *testing.T) {
	pythongatetest.GateFailures(t, false)
}

func TestPythonApplicationGateFingerprints(t *testing.T) {
	pythongatetest.GateFingerprints(t, false)
}
