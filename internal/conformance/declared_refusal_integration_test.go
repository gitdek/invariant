//go:build integration

package conformance

import "testing"

// CI's other jobs check declared attempts against a TLC runner that always
// passes, which can't tell a check of declared outcomes that TLC enforces
// from one it doesn't. The integration job has Docker, so it runs the
// acceptance test's real TLC cases too, on every change that can touch the
// gate. They need only TLC's image, which runs on Linux runners.
func TestDeclaredAttemptConformanceRealTLC(t *testing.T) {
	t.Setenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL", "1")
	TestDeclaredAttemptConformance(t)
}
