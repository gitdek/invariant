//go:build integration

package verify_test

import "testing"

func TestReader586RealIntegration(t *testing.T) { reader586Real(t) }

func TestReader586ReplayValuesRealIntegration(t *testing.T) { reader586ReplayValuesReal(t) }
