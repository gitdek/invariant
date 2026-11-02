//go:build integration

package verify

import "testing"

// CI's other jobs run TestPythonApplicationGateDeclaredRefusals against a
// TLC transport that always passes, which can't tell a check of declared
// outcomes that TLC enforces from one it doesn't. The integration job has
// Docker, so it runs the test's real Docker Python and TLC cases too, on
// every change that can touch the gate.
func TestPythonApplicationGateDeclaredRefusalsRealTLC(t *testing.T) {
	t.Setenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL", "1")
	TestPythonApplicationGateDeclaredRefusals(t)
}
