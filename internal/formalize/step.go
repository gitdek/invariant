package formalize

import (
	"fmt"
	"os"
	"path/filepath"
)

// StepPlanProblem explains why a step cannot propose another plan of issues.
func StepPlanProblem(parent int) string {
	return fmt.Sprintf("issues.json cannot be proposed for a step of parent #%d: each step must be one complete plumbing or modeled change. If the whole step cannot fit one reviewable pull request, ask a chart-free fork with kind \"step_fit\" in proposal.json explaining what does not fit and suggesting replacement steps in the parent. Use these two option texts: \"Build the complete step as one change.\" and \"A writer stops parent #%d with /invariant stop and requests a replacement plan on a new top-level issue with /invariant plan.\" The second may append replacement-step details after a comma or period. Only that build-or-replan question uses step_fit; modeled behavior choices still require checked previews.", parent, parent)
}

// CheckStepDraft refuses issues.json for a caller-authenticated plan step,
// even alongside another draft. Zero leaves ordinary planning unrestricted.
func CheckStepDraft(ws string, parent int) error {
	if parent == 0 {
		return nil
	}
	_, err := os.Lstat(filepath.Join(ws, "issues.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check issues.json for a step of parent #%d: %w", parent, err)
	}
	return fmt.Errorf("%s", StepPlanProblem(parent))
}

func stepPrompt(parent int) string {
	if parent == 0 {
		return ""
	}
	return fmt.Sprintf("\n# One complete change\n\nThis issue is a factory-authenticated step of the ratified plan on parent #%d. Cover the whole step in one complete plumbing or modeled change, even after /invariant plan, an answer or a revision. Do not split it into another plan of issues or leave work for an unplanned follow-up. %s\n", parent, StepPlanProblem(parent))
}
