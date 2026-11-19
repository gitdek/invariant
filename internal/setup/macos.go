package setup

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/toolchain"
)

// gateMacOS is the gate's macOS job, which a workflow has only when a part
// of the repository's contract declares macOS checks (#929). It's a file of
// its own, after gate.yml's: preflight reads the gate's job from gate.yml to
// its end, and a second job there would read as the gate's.
//
//go:embed gate_macos.yml
var gateMacOS string

// MacOSCheck is the name of the gate's macOS job, and so of the check it
// reports. A repository requires it beside invariant/gate once a part of
// its contract declares macOS checks.
const MacOSCheck = "invariant/gate-macos"

// ProtectionWithMacOS is Protection for a repository with a part that
// builds on macOS: it requires the gate's macOS job beside invariant/gate.
const ProtectionWithMacOS = `{"required_status_checks":{"strict":false,"checks":[{"context":"invariant/gate","app_id":15368},{"context":"invariant/gate-macos","app_id":15368}]},"enforce_admins":true,"required_pull_request_reviews":{"required_approving_review_count":0},"restrictions":null}`

// WorkflowFor is the gate workflow for a repository, building Invariant at
// ref. With macos, it has the job that checks the parts that build on macOS
// too, on a hosted macOS runner, after the gate's own job. Without, it's
// Workflow's, byte for byte.
func WorkflowFor(ref string, macos bool) (string, error) {
	text, err := Workflow(ref)
	if err != nil || !macos {
		return text, err
	}
	job := strings.ReplaceAll(gateMacOS, "{{REF}}", ref)
	return text + strings.ReplaceAll(job, "{{GO}}", toolchain.GoVersion), nil
}

// ProtectionFor is the branch protection a repository's gate needs: the
// one that requires the macOS job too when macos is set.
func ProtectionFor(macos bool) string {
	if macos {
		return ProtectionWithMacOS
	}
	return Protection
}

// InitMacOS names the parts the retained contract in dir declares macOS
// checks for, so init writes the gate's macOS job for a contract that has
// one, and only then. A draft init writes declares none, so a repository
// without a contract gets the gate it always did. Reading an unfinished
// contract here doesn't endorse it: CI's loader still decides whether it
// loads.
func InitMacOS(dir string) ([]string, error) {
	data, err := regular.ReadFile(dir, repository.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c repository.Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("read macos from retained contract: %w", err)
	}
	var names []string
	for _, p := range c.MacOSParts() {
		names = append(names, p.Name)
	}
	return names, nil
}
